package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// parseSandboxReceipt keeps the observed native CA-transfer wire separate from
// the older mock contract. Numeric fee uses the transfer currency; terminal
// updated_at is the verified settlement transition time in this sandbox API.
func (a *AirwallexAdapter) parseSandboxReceipt(op Operation, source string, raw []byte) (Receipt, error) {
	c := a.sandboxExecutionContract
	if !c.valid(a.now()) || op.Mode != "SANDBOX" || source != op.SourceAccountRef {
		return Receipt{}, errors.New("sandbox_receipt_contract_unavailable")
	}
	if op.Kind == OperationFund {
		if source != c.FundingAccountID || op.DestinationAccountRef != c.BorrowerAccountID {
			return Receipt{}, errors.New("sandbox_receipt_scope_mismatch")
		}
	} else if source != c.BorrowerAccountID || op.DestinationAccountRef != c.FundingAccountID {
		return Receipt{}, errors.New("sandbox_receipt_scope_mismatch")
	}
	var wire struct {
		ID              string          `json:"id"`
		RequestID       string          `json:"request_id"`
		Destination     string          `json:"destination"`
		Amount          json.RawMessage `json:"amount"`
		Currency        string          `json:"currency"`
		Fee             json.RawMessage `json:"fee"`
		Status          string          `json:"status"`
		UpdatedAt       string          `json:"updated_at"`
		SourceAccountID *string         `json:"source_account_id"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.ID == "" || wire.RequestID != op.RequestID || wire.Destination != op.DestinationAccountRef || wire.Currency != "USD" || wire.SourceAccountID != nil && *wire.SourceAccountID != source {
		return Receipt{}, errors.New("sandbox_receipt_identity_invalid")
	}
	amount, err := decimalCents(wire.Amount)
	if err != nil || amount != op.AmountCents {
		return Receipt{}, errors.New("sandbox_receipt_amount_invalid")
	}
	fee, err := decimalCents(wire.Fee)
	if err != nil {
		return Receipt{}, errors.New("sandbox_receipt_fee_unknown")
	}
	found := false
	for _, quote := range c.Quotes {
		if quote.Kind == op.Kind && quote.AmountCents == op.AmountCents && quote.FeeCents == fee {
			found = true
		}
	}
	if !found {
		return Receipt{}, errors.New("sandbox_receipt_fee_contract_mismatch")
	}
	switch wire.Status {
	case "NEW", "PENDING", "SUSPENDED", "SETTLED":
	default:
		return Receipt{}, errors.New("sandbox_transfer_recovery_contract_unverified")
	}
	var settledAt *time.Time
	if wire.Status == "SETTLED" {
		timestamp, err := parseProviderExpiry(wire.UpdatedAt)
		if err != nil || timestamp.IsZero() || timestamp.After(a.now().UTC().Add(5*time.Second)) {
			return Receipt{}, errors.New("sandbox_settlement_time_invalid")
		}
		settledAt = &timestamp
	}
	// The native CA response omits source_account_id. This parser is called
	// only after an authenticated source-scoped request, matching the private
	// installation contract and exact operation/request/destination. The scoped
	// response establishes the source; a conflicting explicit field is rejected.
	observedSource := source
	return Receipt{OperationID: op.ID, ProviderObjectID: wire.ID, RequestID: wire.RequestID, Status: wire.Status, SourceAccountRef: source, ObservedSourceAccountRef: &observedSource, DestinationAccountRef: wire.Destination, AmountCents: amount, Currency: "USD", ProviderFeeCents: &fee, ObservedAt: a.now().UTC(), SettledAt: settledAt, RawEvidence: append([]byte(nil), raw...)}, nil
}

func (a *AirwallexAdapter) sandboxDeposit(ctx context.Context, accountID, depositID string) (DepositObservation, error) {
	c := a.sandboxExecutionContract
	if a.mode != "SANDBOX" || !c.valid(a.now()) || accountID != c.BorrowerAccountID || !validProviderReference(depositID) {
		return DepositObservation{}, paymentHold("SANDBOX_DEPOSIT_CONTRACT_UNAVAILABLE")
	}
	raw, status, err := a.request(ctx, http.MethodGet, "/api/v1/deposits/"+url.PathEscape(depositID), accountID, nil)
	if err != nil {
		return DepositObservation{}, err
	}
	// Actual sandbox indexing can lag a signed SETTLED delivery. Read retries
	// are bounded by the existing job policy and never repeat a money mutation.
	if status == 400 || status == 404 || status == 429 || status >= 500 {
		return DepositObservation{}, &ProviderError{Code: "SANDBOX_DEPOSIT_READ_RETRY", Recovery: RecoveryRetryRead, Certainty: CertaintyUncertain, HTTPStatus: status, RawEvidence: raw}
	}
	if status < 200 || status >= 300 {
		return DepositObservation{}, providerHTTPError(status, raw, false)
	}
	var wire struct {
		ID              string          `json:"id"`
		Amount          json.RawMessage `json:"amount"`
		Currency        string          `json:"currency"`
		GlobalAccountID string          `json:"global_account_id"`
		Status          string          `json:"status"`
		Type            string          `json:"type"`
		SettledAt       string          `json:"settled_at"`
		Fee             *struct {
			Amount   json.RawMessage `json:"amount"`
			Currency string          `json:"currency"`
		} `json:"fee"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.ID != depositID || wire.Currency != "USD" || wire.GlobalAccountID != c.BorrowerGlobalAccountID || wire.Type != "BANK_TRANSFER" || wire.Status != "SETTLED" || wire.Fee == nil || wire.Fee.Currency != "USD" {
		return DepositObservation{}, paymentHold("SANDBOX_DEPOSIT_FACTS_UNVERIFIED")
	}
	amount, err := decimalCents(wire.Amount)
	if err != nil || amount <= 0 {
		return DepositObservation{}, paymentHold("SANDBOX_DEPOSIT_AMOUNT_UNVERIFIED")
	}
	fee, err := decimalCents(wire.Fee.Amount)
	if err != nil || fee != 0 {
		return DepositObservation{}, paymentHold("SANDBOX_NONZERO_DEPOSIT_FEE_UNVERIFIED")
	}
	settledAt, err := parseProviderExpiry(wire.SettledAt)
	if err != nil || settledAt.Before(c.CutoverAt) || settledAt.After(a.now().UTC().Add(5*time.Second)) {
		return DepositObservation{}, paymentHold("SANDBOX_DEPOSIT_SETTLEMENT_UNVERIFIED")
	}
	return DepositObservation{AccountRef: accountID, DepositID: depositID, CustomerID: c.CustomerID, Currency: "USD", GrossCents: amount, NetCents: amount, ProviderFeeCents: &fee, Status: "SETTLED", Kind: "EXTERNAL_SETTLED", SettledAt: &settledAt, ObservedAt: a.now().UTC(), RawEvidence: append([]byte(nil), raw...)}, nil
}
