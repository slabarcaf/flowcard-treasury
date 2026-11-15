// Package underwriting adapts the committed public policy evaluator to funding.
package underwriting

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

const (
	dependencyCommit = "d2aeb7ff1987fb7ae6ac4c47fc40e5ca8bae3766"
	pinnedPolicyID   = "bridge-capital-go-v1"
	pinnedPolicyHash = "0a0cf42f72e101a458c260be8fbca315704d9a093264049acbf366f8cd4d38f0"
)

// Evaluator retains the immutable policy configuration loaded once at startup.
type Evaluator struct {
	config  policy.Config
	pricing domain.Pricing
}

// DependencyCommit returns the reviewed public module revision.
func DependencyCommit() string { return dependencyCommit }

// Load validates the runtime pin against the committed public policy module.
func Load(policyID, policyHash string) (*Evaluator, error) {
	if policyID != pinnedPolicyID || policyHash != pinnedPolicyHash {
		return nil, errors.New("underwriting_policy_pin_mismatch")
	}
	config, err := policy.LoadConfig()
	if err != nil {
		return nil, errors.New("underwriting_policy_unavailable")
	}
	if config.PolicyID() != pinnedPolicyID || config.PolicyHash() != pinnedPolicyHash {
		return nil, errors.New("underwriting_policy_pin_mismatch")
	}
	pricing := config.Pricing()
	domainPricing := domain.Pricing{ServiceFeeCents: domain.Money(pricing.BaseFeeCents),
		MonthlyBasisPoints: pricing.MonthlyRateBasisPoints, DaysPerMonth: pricing.DaysPerMonth,
		MinimumTermDays: pricing.MinimumTermDays, MaximumTermDays: pricing.MaximumTermDays}
	if domainPricing.Validate() != nil {
		return nil, errors.New("underwriting_policy_pricing_invalid")
	}
	return &Evaluator{config: config, pricing: domainPricing}, nil
}

// LoadPinned loads the committed policy using this adapter's reviewed pin.
func LoadPinned() (*Evaluator, error) { return Load(pinnedPolicyID, pinnedPolicyHash) }

// PolicyID returns the identifier of the loaded policy configuration.
func (e *Evaluator) PolicyID() string { return e.config.PolicyID() }

// PolicyHash returns the hash of the loaded policy configuration.
func (e *Evaluator) PolicyHash() string { return e.config.PolicyHash() }

// Pricing returns the immutable scalar pricing copied from the loaded policy configuration.
func (e *Evaluator) Pricing() domain.Pricing { return e.pricing }

// Evaluate delegates the complete assessment to the committed public policy.
func (e *Evaluator) Evaluate(input policy.Input) (policy.Result, error) {
	if e == nil || e.config.PolicyID() != pinnedPolicyID || e.config.PolicyHash() != pinnedPolicyHash {
		return policy.Result{}, errors.New("underwriting_policy_unavailable")
	}
	result := policy.Evaluate(input, e.config)
	if result.PolicyID != e.config.PolicyID() || result.PolicyHash != e.config.PolicyHash() || result.FundingAuthorized || !result.RequiresRecheck {
		return policy.Result{}, errors.New("underwriting_policy_result_mismatch")
	}
	return result, nil
}

type contextData struct {
	AccountID       string                  `json:"account_id"`
	CardID          string                  `json:"card_id"`
	Standing        *policy.Standing        `json:"standing"`
	HistoryWindow   policy.HistoryWindow    `json:"history_window"`
	BalanceHistory  []policy.Balance        `json:"balance_history"`
	InflowHistory   []policy.Inflow         `json:"inflow_history"`
	Bridges         []policy.Bridge         `json:"bridges"`
	Reservations    []policy.Reservation    `json:"reservations"`
	Receivables     []policy.Receivable     `json:"receivables"`
	Classifications []policy.Classification `json:"classifications"`
}

type contextCoverage struct {
	Standing         *bool `json:"standing"`
	BalanceHistory   *bool `json:"balance_history"`
	InflowHistory    *bool `json:"inflow_history"`
	BridgeHistory    *bool `json:"bridge_history"`
	LifetimeDefaults *bool `json:"lifetime_defaults"`
	Reservations     *bool `json:"reservations"`
	Receivables      *bool `json:"receivables"`
	Classification   *bool `json:"classification"`
}

// DecodeCustomerContext converts verified upstream metadata and normalized
// facts into the public policy snapshot without supplying missing values.
func (e *Evaluator) DecodeCustomerContext(value upstream.CustomerContext, mode, customerID, accountID, cardID string, now time.Time) (policy.CustomerSnapshot, error) {
	if e == nil || (mode != "DEMO" && mode != "SANDBOX") || customerID == "" || accountID == "" || cardID == "" ||
		value.SchemaVersion != 1 || value.SourceMode != mode || value.CustomerID != customerID ||
		value.SnapshotID == "" || value.SnapshotVersion < 1 ||
		value.PolicyID != e.PolicyID() || value.PolicyHash != e.PolicyHash() || !value.Fresh || value.CapturedAt.IsZero() ||
		!isUTC(value.CapturedAt) || now.IsZero() || value.CapturedAt.After(now.UTC().Add(5*time.Second)) ||
		now.UTC().Sub(value.CapturedAt.UTC()) > 24*time.Hour {
		return policy.CustomerSnapshot{}, errors.New("underwriting_context_binding_invalid")
	}
	var coverage contextCoverage
	if err := decodeStrict(value.Coverage, &coverage); err != nil || !coverage.complete() {
		return policy.CustomerSnapshot{}, errors.New("underwriting_context_coverage_invalid")
	}
	var data contextData
	if !validContextDataShape(value.Data) || decodeStrict(value.Data, &data) != nil || data.AccountID != accountID || data.CardID != cardID ||
		data.BalanceHistory == nil || data.InflowHistory == nil ||
		data.Bridges == nil || data.Reservations == nil || data.Receivables == nil || data.Classifications == nil ||
		data.HistoryWindow.WindowStart == "" || data.HistoryWindow.WindowEnd == "" ||
		(*coverage.Standing && data.Standing == nil) {
		return policy.CustomerSnapshot{}, errors.New("underwriting_context_data_invalid")
	}
	var refs []policy.SourceReference
	if err := decodeStrict(value.SourceRefs, &refs); err != nil || len(refs) == 0 {
		return policy.CustomerSnapshot{}, errors.New("underwriting_context_sources_invalid")
	}
	for _, ref := range refs {
		if ref.System == "" || ref.RecordID == "" || ref.Version == "" || ref.EffectiveAt.IsZero() || !isUTC(ref.EffectiveAt) {
			return policy.CustomerSnapshot{}, errors.New("underwriting_context_sources_invalid")
		}
	}
	return policy.CustomerSnapshot{
		ID: value.SnapshotID, Version: value.SnapshotVersion, Mode: value.SourceMode,
		CapturedAt: value.CapturedAt.UTC(), CustomerID: value.CustomerID, AccountID: data.AccountID,
		CardID: data.CardID, SourceRefs: refs,
		Coverage: policy.Coverage{
			Standing: *coverage.Standing, BalanceHistory: *coverage.BalanceHistory,
			InflowHistory: *coverage.InflowHistory, BridgeHistory: *coverage.BridgeHistory,
			LifetimeDefaults: *coverage.LifetimeDefaults, Reservations: *coverage.Reservations,
			Receivables: *coverage.Receivables, Classification: *coverage.Classification,
		},
		Standing: data.Standing, HistoryWindow: data.HistoryWindow, BalanceHistory: data.BalanceHistory,
		InflowHistory: data.InflowHistory, Bridges: data.Bridges, Reservations: data.Reservations,
		Receivables: data.Receivables, Classifications: data.Classifications,
	}, nil
}

func (c contextCoverage) complete() bool {
	return c.Standing != nil && c.BalanceHistory != nil && c.InflowHistory != nil && c.BridgeHistory != nil &&
		c.LifetimeDefaults != nil && c.Reservations != nil && c.Receivables != nil && c.Classification != nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("underwriting_context_json_trailing_data")
	}
	return nil
}

func validContextDataShape(raw []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil ||
		!hasFields(fields, "account_id", "card_id", "standing", "history_window", "balance_history", "inflow_history", "bridges", "reservations", "receivables", "classifications") {
		return false
	}
	var window map[string]json.RawMessage
	if json.Unmarshal(fields["history_window"], &window) != nil || !hasFields(window, "window_start", "window_end") {
		return false
	}
	return validArrayFields(fields["balance_history"], []string{"date", "balance_cents"}) &&
		validArrayValues(fields["balance_history"], []string{"balance_cents"}, nil) &&
		validArrayFields(fields["inflow_history"], []string{"id", "date", "amount_cents", "kind", "payer_id"}) &&
		validArrayValues(fields["inflow_history"], []string{"amount_cents"}, nil) &&
		validArrayFields(fields["bridges"], []string{"id", "transaction_id", "original_principal_cents", "principal_owed_cents", "fees_owed_cents", "status", "opened_on", "due_on", "closed_on", "defaulted_on", "repayment_outcome"}) &&
		validArrayValues(fields["bridges"], []string{"original_principal_cents", "principal_owed_cents", "fees_owed_cents"}, nil) &&
		validArrayFields(fields["reservations"], []string{"id", "transaction_id", "principal_cents", "fee_cents", "status", "bridge_id"}) &&
		validArrayValues(fields["reservations"], []string{"principal_cents", "fee_cents"}, nil) &&
		validArrayFields(fields["receivables"], []string{"invoice_id", "payer_id", "currency", "eligible_unpaid_cents", "allocated_cents", "due_on", "due_date_verified", "allocation_complete", "source_ref"}) &&
		validArrayValues(fields["receivables"], []string{"eligible_unpaid_cents", "allocated_cents"}, []string{"due_date_verified", "allocation_complete"}) &&
		validNestedArrayFields(fields["receivables"], "source_ref", []string{"system", "record_id", "version", "effective_at"}) &&
		validArrayFields(fields["classifications"], []string{"transaction_id", "merchant_category", "source_ref"}) &&
		validNestedArrayFields(fields["classifications"], "source_ref", []string{"system", "record_id", "version", "effective_at"}) &&
		validStandingFields(fields["standing"])
}

func validStandingFields(raw json.RawMessage) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil &&
		hasFields(fields, "relationship_started_on", "onboarding_complete", "active", "suspended", "provider_account_status")
}

func validArrayFields(raw json.RawMessage, required []string) bool {
	var records []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &records) != nil || records == nil {
		return false
	}
	for _, record := range records {
		var fields map[string]json.RawMessage
		if json.Unmarshal(record, &fields) != nil || !hasFields(fields, required...) {
			return false
		}
	}
	return true
}

func validNestedArrayFields(raw json.RawMessage, nested string, required []string) bool {
	var records []map[string]json.RawMessage
	if json.Unmarshal(raw, &records) != nil {
		return false
	}
	for _, record := range records {
		var fields map[string]json.RawMessage
		if json.Unmarshal(record[nested], &fields) != nil || !hasFields(fields, required...) {
			return false
		}
	}
	return true
}

func validArrayValues(raw json.RawMessage, integerFields, booleanFields []string) bool {
	var records []map[string]json.RawMessage
	if json.Unmarshal(raw, &records) != nil || records == nil {
		return false
	}
	for _, record := range records {
		for _, name := range integerFields {
			value, ok := record[name]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return false
			}
			var number int64
			if json.Unmarshal(value, &number) != nil {
				return false
			}
		}
		for _, name := range booleanFields {
			value, ok := record[name]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return false
			}
			var flag bool
			if json.Unmarshal(value, &flag) != nil {
				return false
			}
		}
	}
	return true
}

func hasFields(fields map[string]json.RawMessage, required ...string) bool {
	if fields == nil {
		return false
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			return false
		}
	}
	return true
}

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}
