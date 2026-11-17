package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
)

const (
	loginPath                = "/api/v1/authentication/login"
	transferCreatePath       = "/api/v1/connected_account_transfers/create"
	transferPath             = "/api/v1/connected_account_transfers"
	balancePath              = "/api/v1/balances/current?account_type=cash"
	maxResponseBytes   int64 = 4_000_000
	maxLookupPages           = 20
)

type AdapterOptions struct {
	SandboxPaymentContract   *SandboxPaymentContract
	SandboxExecutionContract *SandboxExecutionContract
	SandboxFundingEnabled    bool
	SandboxCollectionEnabled bool
	Mode                     string
	ProviderMode             string
	BaseURL                  string
	FundingAccountID         string
	ClientID                 string
	APIKey                   string
	LoginAs                  string
	HTTPClient               *http.Client
	Now                      func() time.Time
}

type AirwallexAdapter struct {
	sandboxPaymentContract   *SandboxPaymentContract
	sandboxExecutionContract *SandboxExecutionContract
	sandboxFundingEnabled    bool
	sandboxCollectionEnabled bool
	mode                     string
	providerMode             string
	baseURL                  string
	fundingAccountID         string
	clientID                 string
	apiKey                   string
	loginAs                  string
	http                     *http.Client
	now                      func() time.Time
	mu                       sync.Mutex
	token                    string
	tokenExpires             time.Time
}

type transferPayload struct {
	RequestID   string `json:"request_id"`
	Destination string `json:"destination"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Reason      string `json:"reason"`
	Reference   string `json:"reference"`
}

// NewAirwallexAdapter creates the actual HTTP adapter. DEMO only accepts a
// loopback MOCK_HTTP endpoint and enables synthetic creates. SANDBOX host is
// fixed and all creates remain disabled pending separately reviewed gates.
func NewAirwallexAdapter(options AdapterOptions) (*AirwallexAdapter, error) {
	if options.Mode != "DEMO" && options.Mode != "SANDBOX" {
		return nil, errors.New("provider_mode_invalid")
	}
	if options.FundingAccountID == "" || options.ClientID == "" || options.APIKey == "" {
		return nil, errors.New("provider_configuration_invalid")
	}
	base, err := url.Parse(options.BaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("provider_configuration_invalid")
	}
	if options.Mode == "DEMO" {
		if options.ProviderMode != "MOCK_HTTP" || base.Scheme != "http" || !isLoopback(base.Hostname()) {
			return nil, errors.New("mock_provider_must_be_loopback_http")
		}
		if options.ClientID != config.DemoClientID || options.APIKey != config.DemoAPIKey {
			return nil, errors.New("mock_provider_credentials_must_be_synthetic")
		}
	} else {
		if options.ProviderMode != "AIRWALLEX_SANDBOX" || options.BaseURL != "https://api.sandbox.airwallex.com" {
			return nil, errors.New("sandbox_provider_host_mismatch")
		}
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if options.HTTPClient != nil {
		copy := *options.HTTPClient
		copy.Timeout = 10 * time.Second
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &AirwallexAdapter{sandboxPaymentContract: options.SandboxPaymentContract, sandboxExecutionContract: options.SandboxExecutionContract, sandboxFundingEnabled: options.SandboxFundingEnabled, sandboxCollectionEnabled: options.SandboxCollectionEnabled, mode: options.Mode, providerMode: options.ProviderMode, baseURL: strings.TrimRight(base.String(), "/"), fundingAccountID: options.FundingAccountID, clientID: options.ClientID, apiKey: options.APIKey, loginAs: options.LoginAs, http: client, now: options.Now}, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PayloadHash returns the immutable operation payload identity including scope.
func PayloadHash(operation Operation) (string, error) {
	payload, source, err := makePayload(operation)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(struct {
		Source  string          `json:"source_scope"`
		Payload transferPayload `json:"payload"`
	}{source, payload})
	if err != nil {
		return "", errors.New("provider_payload_invalid")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// QuoteFundingFee returns only the explicit zero-fee MOCK_HTTP contract.
// Sandbox fee schedules remain unknown until the provider contract is reviewed.
func (a *AirwallexAdapter) QuoteFundingFee(_ context.Context, source, destination string, amountCents int64) (FundingFeeQuote, error) {
	if a.mode == "SANDBOX" {
		return a.sandboxQuote(OperationFund, source, destination, amountCents)
	}
	if a.mode != "DEMO" || a.providerMode != "MOCK_HTTP" || source != a.fundingAccountID ||
		destination == "" || destination == source || amountCents <= 0 {
		return FundingFeeQuote{}, ErrProviderUnavailable
	}
	return FundingFeeQuote{Cents: 0, ContractID: "MOCK_HTTP_ZERO_FUNDING_FEE_V1"}, nil
}

// QuoteCollectionFee returns only the explicit zero-fee MOCK_HTTP B-to-A contract.
func (a *AirwallexAdapter) QuoteCollectionFee(_ context.Context, source, destination string, amountCents int64) (FundingFeeQuote, error) {
	if a.mode == "SANDBOX" {
		if quote, err := a.sandboxQuote(OperationCollectFee, source, destination, amountCents); err == nil {
			return quote, nil
		}
		return a.sandboxQuote(OperationCollectPrincipal, source, destination, amountCents)
	}
	if a.mode != "DEMO" || a.providerMode != "MOCK_HTTP" || source == "" || source == a.fundingAccountID ||
		destination != a.fundingAccountID || amountCents <= 0 {
		return FundingFeeQuote{}, ErrProviderUnavailable
	}
	return FundingFeeQuote{Cents: 0, ContractID: "MOCK_HTTP_ZERO_COLLECTION_FEE_V1"}, nil
}

func makePayload(op Operation) (transferPayload, string, error) {
	if op.ID == "" || (op.Mode != "DEMO" && op.Mode != "SANDBOX") || op.SourceAccountRef == "" || op.DestinationAccountRef == "" || op.SourceAccountRef == op.DestinationAccountRef || op.Currency != "USD" || op.AmountCents <= 0 || len(op.RequestID) == 0 || len(op.RequestID) > 50 || len(op.BridgeID) > 140 || len(op.TriggerID) > 140 {
		return transferPayload{}, "", errors.New("provider_operation_invalid")
	}
	source := op.SourceAccountRef
	reason := ""
	switch op.Kind {
	case OperationFund:
		reason = "loan_disbursement"
		if op.Mode == "SANDBOX" {
			reason = "investment_capital"
		}
	case OperationCollectPrincipal:
		reason = "loan_credit_repayment"
		if op.PrincipalCents != op.AmountCents {
			return transferPayload{}, "", errors.New("provider_operation_split_invalid")
		}
	case OperationCollectFee:
		reason = "professional_business_services"
		if op.LateFeeCents < 0 || op.ContractFeeCents < 0 || op.LateFeeCents > int64(^uint64(0)>>1)-op.ContractFeeCents || op.LateFeeCents+op.ContractFeeCents != op.AmountCents {
			return transferPayload{}, "", errors.New("provider_operation_split_invalid")
		}
	default:
		return transferPayload{}, "", errors.New("provider_operation_kind_invalid")
	}
	ref := op.BridgeID
	if ref == "" {
		ref = op.TriggerID
	}
	if ref == "" {
		return transferPayload{}, "", errors.New("provider_reference_required")
	}
	amount := fmt.Sprintf("%d.%02d", op.AmountCents/100, op.AmountCents%100)
	return transferPayload{RequestID: op.RequestID, Destination: op.DestinationAccountRef, Amount: amount, Currency: "USD", Reason: reason, Reference: ref}, source, nil
}

// Create sends exactly one HTTP create. Any transport/response uncertainty is
// returned as UNCERTAIN; this adapter never retries a create automatically.
func (a *AirwallexAdapter) Create(ctx context.Context, op Operation) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if a.mode != "DEMO" || a.providerMode != "MOCK_HTTP" {
		caps := a.ExecutionCapabilities()
		allowed := op.Kind == OperationFund && caps.FundingEnabled || op.Kind != OperationFund && caps.CollectionEnabled
		if !allowed {
			return Result{}, ErrFundingExecutionDisabled
		}
		if _, err := a.sandboxQuote(op.Kind, op.SourceAccountRef, op.DestinationAccountRef, op.AmountCents); err != nil {
			return Result{}, ErrFundingExecutionDisabled
		}
	}
	if err := a.validateOperation(op); err != nil {
		return Result{Certainty: CertaintyDefinitive, Recovery: RecoveryHold}, err
	}
	if _, err := a.accessToken(ctx); err != nil {
		var providerErr *ProviderError
		if errors.As(err, &providerErr) {
			return Result{Certainty: CertaintyDefinitive, Recovery: providerErr.Recovery}, err
		}
		return Result{Certainty: CertaintyDefinitive, Recovery: RecoveryHold}, &ProviderError{Code: "PROVIDER_LOGIN_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive}
	}
	payload, source, err := makePayload(op)
	if err != nil {
		return Result{Certainty: CertaintyDefinitive, Recovery: RecoveryHold}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{Certainty: CertaintyDefinitive, Recovery: RecoveryHold}, errors.New("provider_payload_invalid")
	}
	raw, status, err := a.request(ctx, http.MethodPost, transferCreatePath, source, body)
	if err != nil {
		if status != 0 {
			return Result{Certainty: CertaintyUncertain, Recovery: RecoveryLookupOnly}, providerHTTPError(status, raw, true)
		}
		return Result{Certainty: CertaintyUncertain, Recovery: RecoveryLookupOnly}, &ProviderError{Code: "PROVIDER_CREATE_UNCERTAIN", Recovery: RecoveryLookupOnly, Cause: RecoveryRetryRead, Certainty: CertaintyUncertain}
	}
	if status < 200 || status >= 300 {
		return Result{Certainty: CertaintyUncertain, Recovery: RecoveryLookupOnly}, providerHTTPError(status, raw, true)
	}
	receipt, err := a.parseReceipt(op, source, raw)
	if err != nil {
		return Result{Certainty: CertaintyUncertain, Recovery: RecoveryHold}, &ProviderError{Code: "PROVIDER_CREATE_RESPONSE_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain, RawEvidence: append([]byte(nil), raw...)}
	}
	return Result{Receipt: receipt, Certainty: CertaintyDefinitive, Recovery: receiptRecovery(receipt.Status)}, nil
}
func receiptRecovery(status string) RecoveryClass {
	switch status {
	case "SETTLED", "FAILED":
		return RecoveryVerifyFinal
	case "SUSPENDED":
		return RecoveryHold
	case "NEW", "PENDING":
		return RecoveryRetryRead
	default:
		return RecoveryHold
	}
}

// Balance reads one unambiguous exact USD cash balance under an explicit scope.
func (a *AirwallexAdapter) Balance(ctx context.Context, accountID string) (BalanceObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if accountID == "" {
		return BalanceObservation{}, errors.New("provider_scope_missing")
	}
	requested := a.now().UTC()
	raw, status, err := a.request(ctx, http.MethodGet, balancePath, accountID, nil)
	if err != nil {
		if status != 0 {
			return BalanceObservation{}, providerHTTPError(status, raw, false)
		}
		return BalanceObservation{}, &ProviderError{Code: "PROVIDER_BALANCE_RETRY", Recovery: RecoveryRetryRead, Certainty: CertaintyUncertain}
	}
	if status < 200 || status >= 300 {
		return BalanceObservation{}, providerHTTPError(status, raw, false)
	}
	observed := a.now().UTC()
	var entries []struct {
		AccountType string          `json:"account_type"`
		Currency    string          `json:"currency"`
		Available   json.RawMessage `json:"available_amount"`
	}
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return BalanceObservation{}, &ProviderError{Code: "PROVIDER_BALANCE_SCHEMA_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive, RawEvidence: append([]byte(nil), raw...)}
	}
	var found int64
	count := 0
	for _, entry := range entries {
		if entry.Currency == "USD" {
			if strings.ToLower(entry.AccountType) != "cash" {
				return BalanceObservation{}, &ProviderError{Code: "PROVIDER_BALANCE_ACCOUNT_TYPE_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive, RawEvidence: append([]byte(nil), raw...)}
			}
			count++
			cents, e := decimalCents(entry.Available)
			if e != nil || cents < 0 {
				return BalanceObservation{}, &ProviderError{Code: "PROVIDER_BALANCE_AMOUNT_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive, RawEvidence: append([]byte(nil), raw...)}
			}
			found = cents
		}
	}
	if count != 1 {
		return BalanceObservation{}, &ProviderError{Code: "PROVIDER_BALANCE_AMBIGUOUS_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive, RawEvidence: append([]byte(nil), raw...)}
	}
	return BalanceObservation{AccountRef: accountID, Currency: "USD", AvailableCents: found,
		ObservedAt: observed, RequestedAt: requested, RawEvidence: append([]byte(nil), raw...)}, nil
}

// Payment and Deposit are synthetic MOCK_HTTP-only fixture endpoints, not
// Airwallex production or sandbox APIs. Real extraction remains unsupported.
type paymentFixtureWire struct {
	Mode              string     `json:"source_mode"`
	CustomerID        string     `json:"customer_id"`
	AccountID         string     `json:"canonical_account_id"`
	ParentID          string     `json:"parent_transaction_id"`
	AuthorizationID   string     `json:"authorization_event_id"`
	Attempted         *int64     `json:"attempted_amount_cents"`
	Shortfall         *int64     `json:"shortfall_cents"`
	Currency          string     `json:"currency"`
	ObservedAt        *time.Time `json:"observed_at"`
	Available         *int64     `json:"available_cents"`
	BalanceObservedAt *time.Time `json:"balance_observed_at"`
}
type depositFixtureWire struct {
	AccountRef string     `json:"account_ref"`
	DepositID  string     `json:"deposit_id"`
	CustomerID string     `json:"customer_id"`
	Currency   string     `json:"currency"`
	Gross      *int64     `json:"gross_cents"`
	Net        *int64     `json:"net_cents"`
	Fee        *int64     `json:"provider_fee_cents"`
	Status     string     `json:"status"`
	Kind       string     `json:"kind"`
	SettledAt  *time.Time `json:"settled_at"`
}

func (a *AirwallexAdapter) Payment(ctx context.Context, accountID, parentID string) (PaymentObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if a.providerMode == "AIRWALLEX_SANDBOX" {
		return a.sandboxPayment(ctx, accountID, parentID)
	}
	if a.providerMode != "MOCK_HTTP" || accountID == "" || parentID == "" {
		return PaymentObservation{}, ErrProviderUnavailable
	}
	raw, status, err := a.request(ctx, http.MethodGet, "/__fixture/payments/"+url.PathEscape(parentID), accountID, nil)
	if err != nil {
		if status != 0 {
			return PaymentObservation{}, providerHTTPError(status, raw, false)
		}
		return PaymentObservation{}, &ProviderError{Code: "PROVIDER_PAYMENT_RETRY", Recovery: RecoveryRetryRead, Certainty: CertaintyUncertain}
	}
	if status < 200 || status >= 300 {
		return PaymentObservation{}, providerHTTPError(status, raw, false)
	}
	var w paymentFixtureWire
	if json.Unmarshal(raw, &w) != nil || w.Mode != a.mode || w.CustomerID == "" || w.AccountID != accountID || w.ParentID != parentID || w.AuthorizationID == "" || w.Attempted == nil || *w.Attempted <= 0 || w.Shortfall == nil || *w.Shortfall < 0 || w.Available == nil || *w.Available < 0 || w.Currency != "USD" || w.ObservedAt == nil || w.BalanceObservedAt == nil || !utcTime(*w.ObservedAt) || !utcTime(*w.BalanceObservedAt) || w.ObservedAt.Before(*w.BalanceObservedAt) || w.ObservedAt.After(a.now().UTC().Add(5*time.Second)) || w.BalanceObservedAt.After(a.now().UTC().Add(5*time.Second)) || a.now().UTC().Sub(*w.ObservedAt) > 30*time.Second || a.now().UTC().Sub(*w.BalanceObservedAt) > 30*time.Second {
		return PaymentObservation{}, &ProviderError{Code: "PROVIDER_PAYMENT_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive, RawEvidence: raw}
	}
	shortfall := int64(0)
	if *w.Attempted > *w.Available {
		shortfall = *w.Attempted - *w.Available
	}
	if shortfall != *w.Shortfall {
		return PaymentObservation{}, &ProviderError{Code: "PROVIDER_PAYMENT_AMOUNT_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive, RawEvidence: raw}
	}
	return PaymentObservation{Mode: w.Mode, CustomerID: w.CustomerID, CanonicalAccountID: w.AccountID, ParentTransactionID: w.ParentID, AuthorizationEventID: w.AuthorizationID, AttemptedAmountCents: *w.Attempted, ShortfallCents: *w.Shortfall, Currency: w.Currency, ObservedAt: *w.ObservedAt, AvailableCents: *w.Available, BalanceObservedAt: *w.BalanceObservedAt}, nil
}
func (a *AirwallexAdapter) Deposit(ctx context.Context, accountID, depositID string) (DepositObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if a.providerMode == "AIRWALLEX_SANDBOX" {
		return a.sandboxDeposit(ctx, accountID, depositID)
	}
	if a.providerMode != "MOCK_HTTP" || accountID == "" || !validProviderReference(depositID) {
		return DepositObservation{}, ErrProviderUnavailable
	}
	raw, status, err := a.request(ctx, http.MethodGet, "/__fixture/deposits/"+url.PathEscape(depositID), accountID, nil)
	if err != nil {
		if status != 0 {
			return DepositObservation{}, providerHTTPError(status, raw, false)
		}
		return DepositObservation{}, &ProviderError{Code: "PROVIDER_DEPOSIT_RETRY", Recovery: RecoveryRetryRead, Certainty: CertaintyUncertain}
	}
	if status < 200 || status >= 300 {
		return DepositObservation{}, providerHTTPError(status, raw, false)
	}
	var w depositFixtureWire
	if json.Unmarshal(raw, &w) != nil || w.AccountRef != accountID || w.DepositID != depositID || w.CustomerID == "" || w.Currency != "USD" || w.Status != "SETTLED" || w.Kind != "EXTERNAL_SETTLED" || w.SettledAt == nil || !utcTime(*w.SettledAt) || w.Gross == nil || w.Net == nil || w.Fee == nil || *w.Gross <= 0 || *w.Net <= 0 || *w.Fee < 0 || *w.Net > int64(^uint64(0)>>1)-*w.Fee || *w.Net+*w.Fee != *w.Gross {
		return DepositObservation{}, &ProviderError{Code: "PROVIDER_DEPOSIT_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive, RawEvidence: raw}
	}
	return DepositObservation{AccountRef: w.AccountRef, DepositID: w.DepositID, CustomerID: w.CustomerID,
		Currency: w.Currency, GrossCents: *w.Gross, NetCents: *w.Net, ProviderFeeCents: w.Fee,
		Status: w.Status, Kind: w.Kind, SettledAt: w.SettledAt, ObservedAt: a.now().UTC(),
		RawEvidence: append([]byte(nil), raw...)}, nil
}

func validProviderReference(value string) bool {
	if value == "" || len(value) > 256 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' || character == ':') {
			return false
		}
	}
	return true
}

func (a *AirwallexAdapter) validateOperation(op Operation) error {
	if op.Mode != a.mode {
		return errors.New("provider_operation_mode_mismatch")
	}
	payload, source, err := makePayload(op)
	if err != nil {
		return err
	}
	if op.Kind == OperationFund && source != a.fundingAccountID {
		return errors.New("provider_funding_source_mismatch")
	}
	if op.Kind != OperationFund && payload.Destination != a.fundingAccountID {
		return errors.New("provider_collection_destination_mismatch")
	}
	hash, err := PayloadHash(op)
	if err != nil || op.PayloadHash == "" || hash != op.PayloadHash {
		return errors.New("provider_payload_hash_mismatch")
	}
	return nil
}

// NormalizeCollectionReversal accepts only the explicit DEMO MOCK_HTTP wire
// contract. No Airwallex live reversal schema is inferred here.
func (a *AirwallexAdapter) NormalizeCollectionReversal(op Operation,
	receipt Receipt) (CollectionReversalObservation, error) {
	if a.mode != "DEMO" || a.providerMode != "MOCK_HTTP" ||
		(op.Kind != OperationCollectFee && op.Kind != OperationCollectPrincipal) ||
		receipt.Status != "REVERSED" || len(receipt.RawEvidence) == 0 ||
		receipt.OperationID != op.ID || receipt.RequestID != op.RequestID ||
		receipt.SourceAccountRef != op.SourceAccountRef ||
		receipt.DestinationAccountRef != op.DestinationAccountRef ||
		receipt.ObservedSourceAccountRef == nil ||
		*receipt.ObservedSourceAccountRef != op.SourceAccountRef {
		return CollectionReversalObservation{}, ErrProviderUnavailable
	}
	var wire transferWire
	if json.Unmarshal(receipt.RawEvidence, &wire) != nil || wire.ID != receipt.ProviderObjectID ||
		wire.RequestID != op.RequestID || wire.Destination != op.DestinationAccountRef ||
		wire.Currency != "USD" || wire.ReversalID == "" || wire.ReversalCurrency != "USD" ||
		wire.ReversedAt == nil || !utcTime(*wire.ReversedAt) {
		return CollectionReversalObservation{}, &ProviderError{Code: "DEMO_REVERSAL_EVIDENCE_INVALID",
			Recovery: RecoveryHold, Certainty: CertaintyDefinitive,
			RawEvidence: append([]byte(nil), receipt.RawEvidence...)}
	}
	amount, err := decimalCents(wire.ReversalAmount)
	if err != nil || amount <= 0 || amount > op.AmountCents ||
		wire.ReversedAt.After(receipt.ObservedAt.Add(5*time.Second)) ||
		receipt.ObservedAt.After(a.now().UTC().Add(5*time.Second)) {
		return CollectionReversalObservation{}, &ProviderError{Code: "DEMO_REVERSAL_AMOUNT_INVALID",
			Recovery: RecoveryHold, Certainty: CertaintyDefinitive,
			RawEvidence: append([]byte(nil), receipt.RawEvidence...)}
	}
	return CollectionReversalObservation{ReversalID: wire.ReversalID,
		OriginalObjectID: wire.ID, RequestID: wire.RequestID, AccountRef: op.SourceAccountRef,
		FundingAccountRef: op.DestinationAccountRef, AmountCents: amount, Currency: "USD",
		ReversedAt: wire.ReversedAt.UTC(), ObservedAt: receipt.ObservedAt.UTC(),
		RawEvidence: append([]byte(nil), receipt.RawEvidence...)}, nil
}

func providerHTTPCode(status int) string { return fmt.Sprintf("PROVIDER_HTTP_%d", status) }
func httpRecovery(status int) RecoveryClass {
	if status == 401 || status == 403 || status == 400 || status == 422 {
		return RecoveryHold
	}
	if status == 429 || status >= 500 {
		return RecoveryRetryRead
	}
	return RecoveryHold
}
func providerHTTPError(status int, raw []byte, createSent bool) error {
	certainty := CertaintyDefinitive
	recovery := httpRecovery(status)
	cause := recovery
	if createSent {
		certainty = CertaintyUncertain
		recovery = RecoveryLookupOnly
	}
	return &ProviderError{Code: providerHTTPCode(status), Recovery: recovery, Cause: cause, Certainty: certainty, HTTPStatus: status, RawEvidence: append([]byte(nil), raw...)}
}
func utcTime(t time.Time) bool { _, offset := t.Zone(); return offset == 0 }

func (a *AirwallexAdapter) request(ctx context.Context, method, path, scope string, body []byte) ([]byte, int, error) {
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reader)
	if err != nil {
		return nil, 0, errors.New("provider_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if strings.HasPrefix(path, "/api/v1/issuing/") || strings.HasPrefix(path, "/api/v1/deposits/") || strings.HasPrefix(path, "/api/v1/connected_account_transfers") {
		req.Header.Set("X-Api-Version", "2026-08-21")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if scope != "" {
		req.Header.Set("x-on-behalf-of", scope)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || int64(len(raw)) > maxResponseBytes || !json.Valid(raw) {
		return nil, resp.StatusCode, errors.New("provider_response_invalid")
	}
	return raw, resp.StatusCode, nil
}

func (a *AirwallexAdapter) accessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now().UTC()
	if a.token != "" && now.Add(30*time.Second).Before(a.tokenExpires) {
		return a.token, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+loginPath, strings.NewReader("{}"))
	if err != nil {
		return "", errors.New("provider_login_request_invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", a.clientID)
	req.Header.Set("x-api-key", a.apiKey)
	if a.loginAs != "" {
		req.Header.Set("x-login-as", a.loginAs)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return "", &ProviderError{Code: "PROVIDER_LOGIN_RETRY", Recovery: RecoveryRetryRead, Certainty: CertaintyDefinitive}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32_769))
	if err != nil || len(raw) > 32_768 {
		return "", &ProviderError{Code: "PROVIDER_LOGIN_RESPONSE_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", providerHTTPError(resp.StatusCode, raw, false)
	}
	var login struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal(raw, &login) != nil || login.Token == "" || login.ExpiresAt == "" {
		return "", &ProviderError{Code: "PROVIDER_LOGIN_RESPONSE_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive}
	}
	expiry, err := parseProviderExpiry(login.ExpiresAt)
	if err != nil || !expiry.After(now) {
		return "", &ProviderError{Code: "PROVIDER_LOGIN_EXPIRY_HOLD", Recovery: RecoveryHold, Certainty: CertaintyDefinitive}
	}
	a.token = login.Token
	a.tokenExpires = expiry.UTC()
	return a.token, nil
}

func decimalCents(raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if len(text) == 0 || len(text) > 64 || strings.ContainsAny(text, "eE") || strings.HasPrefix(text, "-") {
		return 0, errors.New("amount_invalid")
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("amount_invalid")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		return 0, errors.New("amount_precision_invalid")
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	whole, ok := new(big.Int).SetString(parts[0]+fraction, 10)
	if !ok || !whole.IsInt64() {
		return 0, errors.New("amount_overflow")
	}
	return whole.Int64(), nil
}

type transferWire struct {
	ID               string          `json:"id"`
	SourceAccountID  *string         `json:"source_account_id"`
	RequestID        string          `json:"request_id"`
	Status           string          `json:"status"`
	Destination      string          `json:"destination"`
	Amount           json.RawMessage `json:"amount"`
	Currency         string          `json:"currency"`
	FeeAmount        json.RawMessage `json:"fee_amount"`
	FeeCurrency      string          `json:"fee_currency"`
	SettledAt        *time.Time      `json:"settled_at"`
	CreatedAt        *time.Time      `json:"created_at"`
	ReversalID       string          `json:"reversal_id"`
	ReversalAmount   json.RawMessage `json:"reversed_amount"`
	ReversalCurrency string          `json:"reversal_currency"`
	ReversedAt       *time.Time      `json:"reversed_at"`
}

type transferPage struct {
	Items     []json.RawMessage `json:"items"`
	PageAfter string            `json:"page_after"`
}

// Retrieve reconciles a known provider object under the operation's original scope.
func (a *AirwallexAdapter) Retrieve(ctx context.Context, op Operation, providerID string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if providerID == "" || a.validateOperation(op) != nil {
		return Result{}, errors.New("provider_retrieval_binding_invalid")
	}
	raw, status, err := a.request(ctx, http.MethodGet, transferPath+"/"+url.PathEscape(providerID), op.SourceAccountRef, nil)
	if err != nil {
		if status != 0 {
			return Result{Certainty: CertaintyUncertain, Recovery: httpRecovery(status)}, operationReadError(providerHTTPError(status, raw, false))
		}
		return Result{Certainty: CertaintyUncertain, Recovery: RecoveryRetryRead}, &ProviderError{Code: "PROVIDER_RETRIEVAL_RETRY", Recovery: RecoveryRetryRead, Certainty: CertaintyUncertain}
	}
	if status == http.StatusNotFound {
		return Result{Receipt: Receipt{OperationID: op.ID, ProviderObjectID: providerID, RequestID: op.RequestID, Status: "UNKNOWN", SourceAccountRef: op.SourceAccountRef, DestinationAccountRef: op.DestinationAccountRef, AmountCents: op.AmountCents, Currency: op.Currency, ObservedAt: a.now().UTC(), RawEvidence: append([]byte(nil), raw...)}, Certainty: CertaintyUncertain, Recovery: RecoveryLookupOnly}, nil
	}
	if status < 200 || status >= 300 {
		return Result{Certainty: CertaintyUncertain, Recovery: httpRecovery(status)}, operationReadError(providerHTTPError(status, raw, false))
	}
	receipt, err := a.parseReceipt(op, op.SourceAccountRef, raw)
	if err != nil {
		return Result{Certainty: CertaintyUncertain, Recovery: RecoveryHold}, &ProviderError{Code: "PROVIDER_RETRIEVAL_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain, RawEvidence: append([]byte(nil), raw...)}
	}
	if receipt.ProviderObjectID != providerID {
		return Result{Certainty: CertaintyUncertain, Recovery: RecoveryHold}, &ProviderError{Code: "PROVIDER_RETRIEVAL_IDENTITY_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain, RawEvidence: append([]byte(nil), raw...)}
	}
	return Result{Receipt: receipt, Certainty: CertaintyDefinitive, Recovery: receiptRecovery(receipt.Status)}, nil
}

// LookupByRequest follows bounded pages in the original source account scope.
// An empty result remains uncertain and never authorizes another create.
func (a *AirwallexAdapter) LookupByRequest(ctx context.Context, op Operation) (LookupResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if a.validateOperation(op) != nil {
		return LookupResult{}, errors.New("provider_lookup_binding_invalid")
	}
	query := url.Values{"request_id": {op.RequestID}, "page_size": {"100"}}
	matches := make([]Result, 0)
	rawPages := make([][]byte, 0)
	seen := map[string]bool{}
	for page := 0; page < maxLookupPages; page++ {
		raw, status, err := a.request(ctx, http.MethodGet, transferPath+"?"+query.Encode(), op.SourceAccountRef, nil)
		if err != nil {
			if status != 0 {
				return LookupResult{Matches: matches, RawPages: rawPages}, operationReadError(providerHTTPError(status, raw, false))
			}
			return LookupResult{Matches: matches, RawPages: rawPages}, &ProviderError{Code: "PROVIDER_LOOKUP_RETRY", Recovery: RecoveryRetryRead, Certainty: CertaintyUncertain}
		}
		if status < 200 || status >= 300 {
			return LookupResult{Matches: matches, RawPages: rawPages}, operationReadError(providerHTTPError(status, raw, false))
		}
		var result transferPage
		if json.Unmarshal(raw, &result) != nil || result.Items == nil {
			return LookupResult{Matches: matches, RawPages: rawPages}, &ProviderError{Code: "PROVIDER_LOOKUP_SCHEMA_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain, RawEvidence: append([]byte(nil), raw...)}
		}
		rawPages = append(rawPages, append([]byte(nil), raw...))
		for _, itemRaw := range result.Items {
			var item transferWire
			if json.Unmarshal(itemRaw, &item) != nil {
				return LookupResult{Matches: matches, RawPages: rawPages}, &ProviderError{Code: "PROVIDER_LOOKUP_ITEM_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain, RawEvidence: append([]byte(nil), itemRaw...)}
			}
			if item.RequestID != op.RequestID {
				continue
			}
			receipt, parseErr := a.parseReceipt(op, op.SourceAccountRef, itemRaw)
			if parseErr != nil {
				return LookupResult{Matches: matches, RawPages: rawPages}, &ProviderError{Code: "PROVIDER_LOOKUP_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain, RawEvidence: append([]byte(nil), itemRaw...)}
			}
			matches = append(matches, Result{Receipt: receipt, Certainty: CertaintyDefinitive, Recovery: receiptRecovery(receipt.Status)})
		}
		if result.PageAfter == "" {
			if len(matches) == 1 {
				return LookupResult{Matches: matches, Complete: true, Certainty: CertaintyDefinitive, Recovery: matches[0].Recovery, RawPages: rawPages}, nil
			}
			if len(matches) > 1 {
				return LookupResult{Matches: matches, Complete: true, Certainty: CertaintyUncertain, Recovery: RecoveryHold, RawPages: rawPages}, nil
			}
			return LookupResult{Matches: matches, Complete: true, Certainty: CertaintyUncertain, Recovery: RecoveryRetryRead, RawPages: rawPages}, nil
		}
		if seen[result.PageAfter] {
			return LookupResult{Matches: matches, RawPages: rawPages}, &ProviderError{Code: "PROVIDER_LOOKUP_PAGINATION_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain, RawEvidence: append([]byte(nil), raw...)}
		}
		seen[result.PageAfter] = true
		query.Set("page", result.PageAfter)
	}
	return LookupResult{Matches: matches, Complete: false, Certainty: CertaintyUncertain, Recovery: RecoveryRetryRead, RawPages: rawPages}, nil
}

// operationReadError keeps movement certainty unknown because a failed read
// cannot establish whether a previously submitted transfer moved funds.
func operationReadError(err error) error {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		copy := *providerErr
		copy.Certainty = CertaintyUncertain
		return &copy
	}
	return &ProviderError{Code: "PROVIDER_READ_HOLD", Recovery: RecoveryHold, Certainty: CertaintyUncertain}
}

func (a *AirwallexAdapter) parseReceipt(op Operation, source string, raw []byte) (Receipt, error) {
	if a.mode == "SANDBOX" && a.providerMode == "AIRWALLEX_SANDBOX" {
		return a.parseSandboxReceipt(op, source, raw)
	}
	var transfer transferWire
	if json.Unmarshal(raw, &transfer) != nil || transfer.ID == "" || transfer.RequestID != op.RequestID || transfer.Destination != op.DestinationAccountRef || transfer.Currency != "USD" || transfer.Status == "" || (transfer.SourceAccountID != nil && *transfer.SourceAccountID != source) {
		return Receipt{}, errors.New("provider_transfer_response_invalid")
	}
	amount, err := decimalCents(transfer.Amount)
	if err != nil || amount != op.AmountCents {
		return Receipt{}, errors.New("provider_transfer_amount_mismatch")
	}
	var fee *int64
	if len(transfer.FeeAmount) > 0 && string(transfer.FeeAmount) != "null" {
		value, e := decimalCents(transfer.FeeAmount)
		if e != nil || transfer.FeeCurrency != "USD" {
			return Receipt{}, errors.New("provider_transfer_fee_invalid")
		}
		fee = &value
	}
	if a.providerMode == "MOCK_HTTP" && fee != nil && *fee != 0 {
		return Receipt{}, errors.New("mock_provider_fee_contract_mismatch")
	}
	if transfer.Status == "SETTLED" && (transfer.SettledAt == nil || transfer.SettledAt.IsZero() || !utcTime(*transfer.SettledAt) || transfer.SettledAt.After(a.now().UTC().Add(5*time.Second))) {
		return Receipt{}, errors.New("provider_settlement_time_missing")
	}
	if transfer.SettledAt != nil {
		normalized := transfer.SettledAt.UTC()
		transfer.SettledAt = &normalized
	}
	return Receipt{OperationID: op.ID, ProviderObjectID: transfer.ID, RequestID: transfer.RequestID, Status: transfer.Status, SourceAccountRef: source, ObservedSourceAccountRef: transfer.SourceAccountID, DestinationAccountRef: transfer.Destination, AmountCents: amount, Currency: transfer.Currency, ProviderFeeCents: fee, ObservedAt: a.now().UTC(), SettledAt: transfer.SettledAt, RawEvidence: append([]byte(nil), raw...)}, nil
}

// NewFromConfig selects only the configured DEMO mock or fixed SANDBOX host.
// The resolved funding account ID must come from a trusted mapping adapter.
func NewFromConfig(cfg config.Config, fundingAccountID, loginAs string, client *http.Client, now func() time.Time) (*AirwallexAdapter, error) {
	var contract *SandboxPaymentContract
	if cfg.SandboxPaymentContractFile != "" {
		var err error
		contract, err = loadSandboxPaymentContract(cfg.SandboxPaymentContractFile)
		if err != nil {
			return nil, err
		}
	}
	var execution *SandboxExecutionContract
	if cfg.SandboxExecutionContractFile != "" {
		var err error
		execution, err = loadSandboxExecutionContract(cfg.SandboxExecutionContractFile)
		if err != nil {
			return nil, err
		}
		if execution.FacilityID != cfg.FacilityID || execution.SourceBindingID != cfg.SourceBindingID {
			return nil, errors.New("sandbox_execution_installation_mismatch")
		}
	}
	if cfg.Mode == "SANDBOX" && loginAs == "" {
		loginAs = cfg.AirwallexLoginAs
	}
	return NewAirwallexAdapter(AdapterOptions{SandboxPaymentContract: contract, SandboxExecutionContract: execution, SandboxFundingEnabled: cfg.LiveDispatchEnabled && cfg.SandboxFundingEnabled, SandboxCollectionEnabled: cfg.LiveDispatchEnabled && cfg.SandboxCollectionEnabled, Mode: cfg.Mode, ProviderMode: cfg.ProviderMode, BaseURL: cfg.ProviderBaseURL,
		FundingAccountID: fundingAccountID, ClientID: cfg.AirwallexClientID, APIKey: cfg.AirwallexAPIKey,
		LoginAs: loginAs, HTTPClient: client, Now: now})
}
