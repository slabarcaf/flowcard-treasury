package domain

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// Failure is a sanitized processing disposition, without provider error bodies.
type Failure struct {
	Reason     string
	Retryable  bool
	RetryAfter time.Duration
	Ignored    bool
}

// Error returns only the controlled reason code, never underlying transport text.
func (f *Failure) Error() string { return strings.ToLower(f.Reason) }

// Mapping is the active roster/card binding verified before scoped provider reads.
type Mapping struct {
	CustomerID string `json:"customer_id"`
	AccountID  string `json:"account_id"`
	CardID     string `json:"card_id"`
}

// ContractEvidence records verified funding and amount conventions separately
// from reference extraction. An active card mapping alone establishes neither.
type ContractEvidence struct {
	FundingModel     string `json:"funding_model"`
	AmountConvention string `json:"amount_convention"`
	Provenance       string `json:"provenance"`
}

// Facts contains only allowlisted normalized facts, never a complete provider body.
type Facts struct {
	Mapping             Mapping           `json:"mapping"`
	CanonicalEventID    string            `json:"canonical_event_id"`
	TransactionID       string            `json:"transaction_id"`
	LifecycleID         string            `json:"lifecycle_id"`
	Type                string            `json:"type"`
	Subtype             string            `json:"subtype"`
	Category            string            `json:"category"`
	ProcessResult       string            `json:"process_result"`
	FailureReason       string            `json:"failure_reason"`
	TransactionAmount   json.RawMessage   `json:"transaction_amount"`
	TransactionCurrency string            `json:"transaction_currency"`
	BillingAmount       json.RawMessage   `json:"billing_amount"`
	BillingCurrency     string            `json:"billing_currency"`
	CreatedAt           *time.Time        `json:"provider_created_at"`
	TransactedAt        *time.Time        `json:"provider_transacted_at"`
	UpdatedAt           *time.Time        `json:"provider_updated_at"`
	Contract            ContractEvidence  `json:"contract"`
	Provenance          map[string]string `json:"field_provenance"`
	OrderingReason      string            `json:"ordering_reason,omitempty"`
}

// Observation is a timestamped current-need result, with absent amounts as nil.
type Observation struct {
	Facts                 Facts
	Outcome               Outcome
	Reason                string
	Currency              *string
	AttemptedAmountCents  *int64
	AvailableBalanceCents *int64
	ShortfallCents        *int64
	BalanceRequestedAt    *time.Time
	BalanceObservedAt     *time.Time
}

// StoredObservation identifies the first committed result for a canonical event.
type StoredObservation struct {
	ID          string
	CreatedAt   time.Time
	Observation Observation
}

// Prepare classifies facts and validates amounts before a balance request.
// The bool reports whether a current balance is needed; it is never a funding decision.
func Prepare(facts Facts) (Observation, bool) {
	o := Observation{Facts: facts, Outcome: Unresolved}
	if facts.OrderingReason != "" {
		o.Reason = facts.OrderingReason
		if o.Reason == "PAYMENT_NO_LONGER_ACTIONABLE" {
			o.Outcome = Ignored
		}
		return o, false
	}
	for _, field := range []struct {
		value, supported string
		known            []string
	}{
		{facts.Type, "AUTHORIZATION", []string{"AUTHORIZATION", "CLEARING", "REVERSAL_AUTH"}},
		{facts.Subtype, "AUTHORIZATION", []string{"AUTHORIZATION", "INCREMENTAL_AUTHORIZATION", "VERIFICATION", "REVERSAL", "PARTIAL_REVERSAL", "EXPIRED_AUTHORIZATION", "CLEARING", "PARTIAL_CLEARING", "CLEARING_REVERSAL"}},
		{facts.Category, "PURCHASE", []string{"PURCHASE", "CASH_DISBURSEMENT", "ACCOUNT_FUNDING", "QUASI_CASH", "MERCHANT_CREDIT", "DISPUTE_CREDIT", "ORIGINAL_CREDIT", "ELIGIBILITY_INQUIRY", "BILL_PAYMENT"}},
		{facts.ProcessResult, "DECLINED", []string{"DECLINED", "APPROVED"}},
		{facts.FailureReason, "INSUFFICIENT_FUNDS", nil},
	} {
		if field.value == "" || field.value == "UNKNOWN" {
			o.Reason = "AUTHORIZATION_FACTS_MISSING"
			return o, false
		}
		if field.value != field.supported {
			if field.known != nil && !slices.Contains(field.known, field.value) {
				o.Reason = "AUTHORIZATION_FACTS_UNSUPPORTED"
				return o, false
			}
			o.Outcome, o.Reason = Ignored, "UNSUPPORTED_DECLINE_CLASSIFICATION"
			return o, false
		}
	}
	if facts.TransactionCurrency == "" || facts.BillingCurrency == "" {
		o.Reason = "CURRENCY_MISSING"
		return o, false
	}
	if facts.TransactionCurrency != "USD" || facts.BillingCurrency != "USD" {
		o.Outcome, o.Reason = Ignored, "UNSUPPORTED_CURRENCY"
		return o, false
	}
	if facts.Mapping.AccountID == "" || facts.Mapping.CardID == "" ||
		facts.Mapping.CustomerID == "" || facts.CanonicalEventID == "" ||
		facts.TransactionID == "" || facts.CreatedAt == nil {
		o.Reason = "AUTHORIZATION_FACTS_MISSING"
		return o, false
	}
	if facts.Contract.Provenance == "" || facts.Contract.FundingModel == "" {
		o.Reason = "FUNDING_CONFIGURATION_UNVERIFIED"
		return o, false
	}
	if facts.Contract.FundingModel != "USD_WALLET" {
		o.Outcome, o.Reason = Ignored, "UNSUPPORTED_FUNDING_MODEL"
		return o, false
	}
	if facts.Contract.AmountConvention != "positive_currency_units" &&
		facts.Contract.AmountConvention != "negative_outgoing_currency_units" {
		o.Reason = "AMOUNT_CONVENTION_UNVERIFIED"
		return o, false
	}
	attempt, err := purchaseCents(facts.TransactionAmount, facts.Contract.AmountConvention)
	if err != nil {
		o.Reason = "ATTEMPT_AMOUNT_INVALID"
		return o, false
	}
	billing, err := purchaseCents(facts.BillingAmount, facts.Contract.AmountConvention)
	if err != nil || billing != attempt {
		o.Reason = "BILLING_AMOUNT_INVALID"
		return o, false
	}
	currency := "USD"
	o.Currency, o.AttemptedAmountCents = &currency, &attempt
	o.Reason = "BALANCE_NOT_OBSERVED"
	return o, true
}

// Observe calculates a current-need observation after classification and a valid
// available balance read. It does not change facts or authorize money movement.
func Observe(facts Facts, available int64, requested, observed time.Time) Observation {
	o, eligible := Prepare(facts)
	if !eligible {
		return o
	}
	if available < 0 {
		o.Reason = "BALANCE_AMOUNT_INVALID"
		return o
	}
	if requested.IsZero() || observed.Before(requested) {
		o.Reason = "BALANCE_OBSERVATION_TIME_INVALID"
		return o
	}
	shortfall := int64(0)
	if *o.AttemptedAmountCents > available {
		shortfall = *o.AttemptedAmountCents - available
	}
	requested, observed = requested.UTC(), observed.UTC()
	o.AvailableBalanceCents, o.ShortfallCents = &available, &shortfall
	o.BalanceRequestedAt, o.BalanceObservedAt = &requested, &observed
	o.Outcome, o.Reason = NoCurrentShortfall, ""
	if shortfall > 0 {
		o.Outcome = ShortfallDetected
	}
	return o
}
