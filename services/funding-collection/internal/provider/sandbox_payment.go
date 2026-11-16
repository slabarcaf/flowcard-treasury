package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// SandboxPaymentContract pins observed read semantics and trusted internal
// customer mapping. It grants neither provider dispatch nor browser authority.
type SandboxPaymentContract struct {
	SchemaVersion         int       `json:"schema_version"`
	Environment           string    `json:"environment"`
	RunID                 string    `json:"run_id"`
	Source                string    `json:"source"`
	Version               string    `json:"version"`
	CustomerID            string    `json:"customer_id"`
	AccountID             string    `json:"account_id"`
	CardID                string    `json:"card_id"`
	FundingModel          string    `json:"funding_model"`
	AmountConvention      string    `json:"amount_convention"`
	CanonicalEvidenceHash string    `json:"canonical_evidence_hash"`
	WebhookEvidenceHashes []string  `json:"webhook_evidence_hashes"`
	ExpiresAt             time.Time `json:"expires_at"`
}

func loadSandboxPaymentContract(path string) (*SandboxPaymentContract, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("sandbox_payment_contract_unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("sandbox_payment_contract_not_private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("sandbox_payment_contract_invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var contract SandboxPaymentContract
	if decoder.Decode(&contract) != nil || decoder.Decode(&struct{}{}) != io.EOF || !contract.valid(time.Now()) {
		return nil, errors.New("sandbox_payment_contract_invalid")
	}
	return &contract, nil
}

func (c *SandboxPaymentContract) valid(now time.Time) bool {
	if c == nil || c.SchemaVersion != 1 || c.Environment != "SANDBOX" || !strings.HasPrefix(c.RunID, "fc-sbx-") || c.Source == "" || c.Version != "2026-08-21" || c.CustomerID == "" || c.AccountID == "" || c.CardID == "" || c.FundingModel != "USD_WALLET" || c.AmountConvention != "positive_currency_units" || len(c.CanonicalEvidenceHash) != 64 || len(c.WebhookEvidenceHashes) != 2 || !c.ExpiresAt.After(now) {
		return false
	}
	for _, hash := range append([]string{c.CanonicalEvidenceHash}, c.WebhookEvidenceHashes...) {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 {
			return false
		}
	}
	return true
}

type sandboxEvent struct {
	ID                  string          `json:"id"`
	CardID              string          `json:"card_id"`
	Parent              string          `json:"card_transaction_id"`
	Type                string          `json:"type"`
	Subtype             string          `json:"subtype"`
	Category            string          `json:"transaction_category"`
	Result              string          `json:"process_result"`
	Reason              string          `json:"failure_reason"`
	TransactionAmount   json.RawMessage `json:"transaction_amount"`
	TransactionCurrency string          `json:"transaction_currency"`
	BillingAmount       json.RawMessage `json:"billing_amount"`
	BillingCurrency     string          `json:"billing_currency"`
}

func (a *AirwallexAdapter) sandboxPayment(ctx context.Context, accountID, parentID string) (PaymentObservation, error) {
	contract := a.sandboxPaymentContract
	if a.mode != "SANDBOX" || !contract.valid(a.now()) || accountID != contract.AccountID || !validProviderReference(parentID) {
		return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_CONTRACT_UNAVAILABLE")
	}
	query := url.Values{"card_transaction_id": {parentID}, "page_size": {"100"}}
	seenPages, seenEvents := map[string]bool{}, map[string]bool{}
	candidates := []sandboxEvent{}
	complete := false
	for page := 0; page < maxLookupPages; page++ {
		raw, status, err := a.request(ctx, http.MethodGet, "/api/v1/issuing/card_transaction_events?"+query.Encode(), accountID, nil)
		if err != nil {
			return PaymentObservation{}, err
		}
		if status < 200 || status >= 300 {
			return PaymentObservation{}, providerHTTPError(status, raw, false)
		}
		var result struct {
			Items []sandboxEvent `json:"items"`
			After string         `json:"page_after"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Items == nil {
			return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_SCHEMA_HOLD")
		}
		for _, event := range result.Items {
			if event.ID == "" || seenEvents[event.ID] || event.Parent != parentID || event.CardID != contract.CardID {
				return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_IDENTITY_HOLD")
			}
			seenEvents[event.ID] = true
			if event.Result == "APPROVED" {
				return PaymentObservation{}, paymentHold("PAYMENT_NO_LONGER_ACTIONABLE")
			}
			if event.Type == "AUTHORIZATION" && event.Subtype == "AUTHORIZATION" {
				candidates = append(candidates, event)
			}
		}
		if result.After == "" {
			complete = true
			break
		}
		if seenPages[result.After] {
			return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_PAGINATION_HOLD")
		}
		seenPages[result.After] = true
		query.Set("page", result.After)
	}
	if !complete || len(candidates) != 1 {
		return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_AMBIGUOUS_HOLD")
	}
	raw, status, err := a.request(ctx, http.MethodGet, "/api/v1/issuing/card_transaction_events/"+url.PathEscape(candidates[0].ID), accountID, nil)
	if err != nil {
		return PaymentObservation{}, err
	}
	if status < 200 || status >= 300 {
		return PaymentObservation{}, providerHTTPError(status, raw, false)
	}
	var event sandboxEvent
	if json.Unmarshal(raw, &event) != nil || event.ID != candidates[0].ID || event.CardID != contract.CardID || event.Parent != parentID || event.Type != "AUTHORIZATION" || event.Subtype != "AUTHORIZATION" || event.Category != "PURCHASE" || event.Result != "DECLINED" || event.Reason != "INSUFFICIENT_FUNDS" || event.TransactionCurrency != "USD" || event.BillingCurrency != "USD" {
		return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_FACTS_HOLD")
	}
	attempted, err := decimalCents(event.TransactionAmount)
	if err != nil || attempted <= 0 {
		return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_AMOUNT_HOLD")
	}
	billing, err := decimalCents(event.BillingAmount)
	if err != nil || billing != attempted {
		return PaymentObservation{}, paymentHold("SANDBOX_PAYMENT_AMOUNT_HOLD")
	}
	raw, status, err = a.request(ctx, http.MethodGet, "/api/v1/issuing/cards/"+url.PathEscape(contract.CardID), accountID, nil)
	if err != nil {
		return PaymentObservation{}, err
	}
	if status < 200 || status >= 300 {
		return PaymentObservation{}, providerHTTPError(status, raw, false)
	}
	var card struct {
		ID            string `json:"card_id"`
		Status        string `json:"card_status"`
		FundingSource string `json:"funding_source_id"`
		Program       struct {
			Purpose string `json:"purpose"`
			Type    string `json:"type"`
			Subtype string `json:"sub_type"`
		} `json:"program"`
	}
	if json.Unmarshal(raw, &card) != nil || card.ID != contract.CardID || card.Status != "ACTIVE" || card.FundingSource != "" || card.Program.Purpose != "COMMERCIAL" || card.Program.Type != "CREDIT" || card.Program.Subtype != "GOOD_FUNDS_CREDIT" {
		return PaymentObservation{}, paymentHold("SANDBOX_WALLET_FUNDING_UNVERIFIED")
	}
	balance, err := a.Balance(ctx, accountID)
	if err != nil {
		return PaymentObservation{}, err
	}
	shortfall := int64(0)
	if attempted > balance.AvailableCents {
		shortfall = attempted - balance.AvailableCents
	}
	return PaymentObservation{Mode: "SANDBOX", CustomerID: contract.CustomerID, CanonicalAccountID: accountID, ParentTransactionID: parentID, AuthorizationEventID: event.ID, AttemptedAmountCents: attempted, ShortfallCents: shortfall, Currency: "USD", ObservedAt: a.now().UTC(), AvailableCents: balance.AvailableCents, BalanceObservedAt: balance.ObservedAt}, nil
}

func paymentHold(code string) error {
	return &ProviderError{Code: code, Recovery: RecoveryHold, Certainty: CertaintyDefinitive}
}

func parseProviderExpiry(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("provider_expiry_invalid")
}
