// Package policy evaluates normalized underwriting facts using deterministic rules.
package policy

import "time"

// Tier identifies a proven Bridge Capital customer tier.
type Tier string

const (
	// TierA is the highest proven credit tier.
	TierA Tier = "A"
	// TierB is the middle proven credit tier.
	TierB Tier = "B"
	// TierC is the base proven credit tier.
	TierC Tier = "C"
)

// RuleStatus describes one deterministic rule result.
type RuleStatus string

const (
	// RulePass means the rule's required condition is known and satisfied.
	RulePass RuleStatus = "PASS"
	// RuleFail means a known substantive condition is false.
	RuleFail RuleStatus = "FAIL"
	// RuleUnknown means required evidence or an operand is unavailable.
	RuleUnknown RuleStatus = "UNKNOWN"
)

// DecisionStatus is the overall assessment, never a funding authorization.
type DecisionStatus string

const (
	// DecisionEligible means every rule passed and no approval gate remains.
	DecisionEligible DecisionStatus = "ELIGIBLE"
	// DecisionIneligible means at least one hard rule failed.
	DecisionIneligible DecisionStatus = "INELIGIBLE"
	// DecisionReviewRequired means evidence is unknown or human approval is required.
	DecisionReviewRequired DecisionStatus = "REVIEW_REQUIRED"
)

// CapitalRegime describes current available facility capital before this request.
type CapitalRegime string

const (
	// RegimeAbundant means more than 60% of the facility remains available.
	RegimeAbundant CapitalRegime = "ABUNDANT"
	// RegimeNormal means more than 30% and at most 60% remains available.
	RegimeNormal CapitalRegime = "NORMAL"
	// RegimeTight means more than 10% and at most 30% remains available.
	RegimeTight CapitalRegime = "TIGHT"
	// RegimeCritical means positive availability is at most 10% of the facility.
	RegimeCritical CapitalRegime = "CRITICAL"
	// RegimeAtFloor means no capital is available above the reserve floor.
	RegimeAtFloor CapitalRegime = "AT_FLOOR"
)

// RuleID is a stable identifier from the versioned rule catalogue.
type RuleID string

const (
	// RuleInputFresh checks source freshness, mode and linkage.
	RuleInputFresh RuleID = "INPUT_FRESH"
	// RuleCustomerActive checks verified customer and provider-account standing.
	RuleCustomerActive RuleID = "CUSTOMER_ACTIVE"
	// RuleHistorySufficient checks tenure and complete daily balance history.
	RuleHistorySufficient RuleID = "HISTORY_SUFFICIENT"
	// RuleNoOverdueOrDefault checks overdue funded debt and recent defaults.
	RuleNoOverdueOrDefault RuleID = "NO_OVERDUE_OR_DEFAULT"
	// RuleCustomerCapacity checks total debt exposure against the customer line.
	RuleCustomerCapacity RuleID = "CUSTOMER_CAPACITY"
	// RulePaymentNotBridged checks for an active bridge or reservation on the payment.
	RulePaymentNotBridged RuleID = "PAYMENT_NOT_BRIDGED"
	// RuleReceivableCoverage checks verified free receivables against principal and fee.
	RuleReceivableCoverage RuleID = "RECEIVABLE_COVERAGE"
	// RuleCapitalAvailable checks requested principal against available facility cash.
	RuleCapitalAvailable RuleID = "CAPITAL_AVAILABLE"
	// RuleSpendAllowed checks category eligibility in the current regime.
	RuleSpendAllowed RuleID = "SPEND_ALLOWED"
	// RuleRegimeTierAllowed checks customer-tier eligibility in the current regime.
	RuleRegimeTierAllowed RuleID = "REGIME_TIER_ALLOWED"
)

// SourceReference identifies the verified record supporting a normalized fact.
type SourceReference struct {
	System      string    `json:"system"`
	RecordID    string    `json:"record_id"`
	Version     string    `json:"version"`
	EffectiveAt time.Time `json:"effective_at"`
}

// Coverage marks data groups as complete; false keeps all group facts unknown.
type Coverage struct {
	Standing         bool `json:"standing"`
	BalanceHistory   bool `json:"balance_history"`
	InflowHistory    bool `json:"inflow_history"`
	BridgeHistory    bool `json:"bridge_history"`
	LifetimeDefaults bool `json:"lifetime_defaults"`
	Reservations     bool `json:"reservations"`
	Receivables      bool `json:"receivables"`
	Classification   bool `json:"classification"`
}

// Date is a UTC calendar date in YYYY-MM-DD form.
type Date string

// Standing contains nullable imported account and relationship evidence.
type Standing struct {
	RelationshipStartedOn *Date   `json:"relationship_started_on"`
	OnboardingComplete    *bool   `json:"onboarding_complete"`
	Active                *bool   `json:"active"`
	Suspended             *bool   `json:"suspended"`
	ProviderAccountStatus *string `json:"provider_account_status"`
}

// HistoryWindow declares the interval evaluated for balance and inflow completeness.
type HistoryWindow struct {
	WindowStart Date `json:"window_start"`
	WindowEnd   Date `json:"window_end"`
}

// Balance is one end-of-day USD wallet observation.
type Balance struct {
	Date         Date  `json:"date"`
	BalanceCents int64 `json:"balance_cents"`
}

// Inflow is one classified payment record in the history window.
type Inflow struct {
	ID          string `json:"id"`
	Date        Date   `json:"date"`
	AmountCents int64  `json:"amount_cents"`
	Kind        string `json:"kind"`
	PayerID     string `json:"payer_id"`
}

// Bridge records one funded obligation and its remaining debt.
type Bridge struct {
	ID                     string  `json:"id"`
	TransactionID          string  `json:"transaction_id"`
	OriginalPrincipalCents int64   `json:"original_principal_cents"`
	PrincipalOwedCents     int64   `json:"principal_owed_cents"`
	FeesOwedCents          int64   `json:"fees_owed_cents"`
	Status                 string  `json:"status"`
	OpenedOn               Date    `json:"opened_on"`
	DueOn                  Date    `json:"due_on"`
	ClosedOn               *Date   `json:"closed_on"`
	DefaultedOn            *Date   `json:"defaulted_on"`
	RepaymentOutcome       *string `json:"repayment_outcome"`
}

// Reservation is one funding commitment for a payment identity.
type Reservation struct {
	ID             string  `json:"id"`
	TransactionID  string  `json:"transaction_id"`
	PrincipalCents int64   `json:"principal_cents"`
	FeeCents       int64   `json:"fee_cents"`
	Status         string  `json:"status"`
	BridgeID       *string `json:"bridge_id"`
}

// Receivable contains unpaid USD amount, due-date verification and allocations.
type Receivable struct {
	InvoiceID           string          `json:"invoice_id"`
	PayerID             string          `json:"payer_id"`
	Currency            string          `json:"currency"`
	EligibleUnpaidCents int64           `json:"eligible_unpaid_cents"`
	AllocatedCents      int64           `json:"allocated_cents"`
	DueOn               *Date           `json:"due_on"`
	DueDateVerified     bool            `json:"due_date_verified"`
	AllocationComplete  bool            `json:"allocation_complete"`
	SourceRef           SourceReference `json:"source_ref"`
}

// Classification is one verified merchant-category mapping.
type Classification struct {
	TransactionID    string          `json:"transaction_id"`
	MerchantCategory string          `json:"merchant_category"`
	SourceRef        SourceReference `json:"source_ref"`
}

// CustomerSnapshot is the normalized, versioned customer picture for an assessment.
type CustomerSnapshot struct {
	ID              string            `json:"snapshot_id"`
	Version         int64             `json:"snapshot_version"`
	Mode            string            `json:"source_mode"`
	CapturedAt      time.Time         `json:"captured_at"`
	CustomerID      string            `json:"customer_id"`
	AccountID       string            `json:"account_id"`
	CardID          string            `json:"card_id"`
	SourceRefs      []SourceReference `json:"source_refs"`
	Coverage        Coverage          `json:"coverage"`
	Standing        *Standing         `json:"standing"`
	HistoryWindow   HistoryWindow     `json:"history_window"`
	BalanceHistory  []Balance         `json:"balance_history"`
	InflowHistory   []Inflow          `json:"inflow_history"`
	Bridges         []Bridge          `json:"bridges"`
	Reservations    []Reservation     `json:"reservations"`
	Receivables     []Receivable      `json:"receivables"`
	Classifications []Classification  `json:"classifications"`
}

// FacilitySnapshot is the normalized ledger picture for one configured facility.
type FacilitySnapshot struct {
	ID                     string            `json:"snapshot_id"`
	Version                int64             `json:"snapshot_version"`
	Mode                   string            `json:"source_mode"`
	CapturedAt             time.Time         `json:"captured_at"`
	FacilityID             string            `json:"facility_id"`
	SourceRefs             []SourceReference `json:"source_refs"`
	Currency               string            `json:"currency"`
	FacilityTotalCents     int64             `json:"facility_total_cents"`
	ReserveFloorBPS        int64             `json:"reserve_floor_bps"`
	FundedPrincipalCents   int64             `json:"funded_principal_cents"`
	ReservedPrincipalCents int64             `json:"reserved_principal_cents"`
	LedgerVersion          string            `json:"ledger_version"`
	LedgerComplete         bool              `json:"ledger_complete"`
}

// Shortfall is an authenticated current-need observation supplied by the service adapter.
type Shortfall struct {
	ID                    string    `json:"id"`
	CustomerID            string    `json:"customer_id"`
	AccountID             string    `json:"account_id"`
	CardID                string    `json:"card_id"`
	Outcome               string    `json:"outcome"`
	Currency              string    `json:"currency"`
	AttemptedAmountCents  int64     `json:"attempted_amount_cents"`
	AvailableBalanceCents int64     `json:"available_balance_cents"`
	ShortfallCents        int64     `json:"shortfall_cents"`
	CanonicalEventID      string    `json:"canonical_event_id"`
	TransactionID         string    `json:"transaction_id"`
	BalanceObservedAt     time.Time `json:"balance_observed_at"`
}

// Input contains all facts and the evaluation instant; it performs no I/O or clock reads.
type Input struct {
	EvaluatedAt time.Time        `json:"evaluated_at"`
	Mode        string           `json:"source_mode"`
	FacilityID  string           `json:"facility_id"`
	TermDays    int              `json:"term_days"`
	Shortfall   Shortfall        `json:"shortfall"`
	Customer    CustomerSnapshot `json:"customer"`
	Facility    FacilitySnapshot `json:"facility"`
}

// RuleResult contains one controlled outcome and its actual/threshold operands.
type RuleResult struct {
	RuleID     RuleID            `json:"rule_id"`
	Status     RuleStatus        `json:"status"`
	ReasonCode string            `json:"reason_code"`
	Actual     map[string]any    `json:"actual"`
	Threshold  map[string]any    `json:"threshold"`
	SourceRefs []SourceReference `json:"source_refs"`
}

// Result is an immutable policy assessment, never an authorization to fund.
type Result struct {
	PolicyID                 string         `json:"policy_id"`
	PolicyHash               string         `json:"policy_hash"`
	EvaluatedAt              time.Time      `json:"evaluated_at"`
	Status                   DecisionStatus `json:"status"`
	ApprovalRequired         bool           `json:"approval_required"`
	FundingAuthorized        bool           `json:"funding_authorized"`
	RequiresRecheck          bool           `json:"requires_recheck"`
	Tier                     *Tier          `json:"tier"`
	Regime                   *CapitalRegime `json:"regime"`
	CustomerLimitCents       *int64         `json:"customer_limit_cents"`
	CustomerHeadroomCents    *int64         `json:"customer_headroom_cents"`
	ExistingExposureCents    *int64         `json:"existing_exposure_cents"`
	PostFundingExposureCents *int64         `json:"post_funding_exposure_cents"`
	AvailableCapitalCents    *int64         `json:"available_capital_cents"`
	PrincipalCents           *int64         `json:"principal_cents"`
	TermDays                 int            `json:"term_days"`
	EstimatedFeeCents        *int64         `json:"estimated_fee_cents"`
	TotalDueCents            *int64         `json:"total_due_cents"`
	Rules                    []RuleResult   `json:"rules"`
	ReasonCodes              []string       `json:"reason_codes"`
}

// Config is an immutable view of the checked-in policy configuration.
type Config struct {
	policyID   string
	policyHash string
	settings   policySettings
}

// Pricing contains the scalar term and fee parameters from the pinned policy.
type Pricing struct {
	BaseFeeCents           int64
	MonthlyRateBasisPoints int64
	DaysPerMonth           int64
	MinimumTermDays        int
	MaximumTermDays        int
}

// PolicyID returns the pinned identifier associated with this configuration.
func (c Config) PolicyID() string { return c.policyID }

// PolicyHash returns the SHA-256 of the exact embedded JSON bytes.
func (c Config) PolicyHash() string { return c.policyHash }

// Pricing returns a value copy of the validated embedded pricing parameters.
func (c Config) Pricing() Pricing {
	return Pricing{
		BaseFeeCents:           c.settings.Pricing.BaseFeeCents,
		MonthlyRateBasisPoints: c.settings.Pricing.MonthlyRateBasisPoints,
		DaysPerMonth:           c.settings.Pricing.DaysPerMonth,
		MinimumTermDays:        c.settings.Pricing.MinimumTermDays,
		MaximumTermDays:        c.settings.Pricing.MaximumTermDays,
	}
}
