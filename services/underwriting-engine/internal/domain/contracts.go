// Package domain defines the versioned snapshot contracts owned by underwriting.
package domain

import (
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// SchemaVersion is the only snapshot shape accepted by this service.
	SchemaVersion = 1
	// MaxSnapshotRecords bounds every imported record array.
	MaxSnapshotRecords = 10000
)

// SnapshotMetadata identifies the producer and capture of immutable input facts.
type SnapshotMetadata struct {
	SchemaVersion int               `json:"schema_version"`
	SourceMode    string            `json:"source_mode"`
	SourceID      string            `json:"source_id"`
	SourceVersion string            `json:"source_version"`
	CapturedAt    time.Time         `json:"captured_at"`
	SourceRefs    []SourceReference `json:"source_refs"`
}

// SourceReference attributes a fact group to a versioned source record.
type SourceReference struct {
	System      string    `json:"system"`
	RecordID    string    `json:"record_id"`
	Version     string    `json:"version"`
	EffectiveAt time.Time `json:"effective_at"`
}

// Coverage records whether each input group is complete, including known-empty groups.
// Consumers must treat all facts in an incomplete group as unknown, even when present.
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

// Standing contains declared onboarding, account and relationship facts.
// Nil fields preserve unknown values; incomplete coverage never makes them known.
type Standing struct {
	RelationshipStartedOn *Date   `json:"relationship_started_on"`
	OnboardingComplete    *bool   `json:"onboarding_complete"`
	Active                *bool   `json:"active"`
	Suspended             *bool   `json:"suspended"`
	ProviderAccountStatus *string `json:"provider_account_status"`
}

// Date is a strict UTC calendar date in YYYY-MM-DD form.
type Date string

// HistoryWindow declares the interval whose balance and inflow data was assessed.
type HistoryWindow struct {
	WindowStart Date `json:"window_start"`
	WindowEnd   Date `json:"window_end"`
}

// Balance is one end-of-day USD observation.
type Balance struct {
	Date         Date  `json:"date"`
	BalanceCents int64 `json:"balance_cents"`
}

// Inflow is a normalized settled or excluded payment record.
type Inflow struct {
	ID          string `json:"id"`
	Date        Date   `json:"date"`
	AmountCents int64  `json:"amount_cents"`
	Kind        string `json:"kind"`
	PayerID     string `json:"payer_id"`
}

// Bridge is one funded obligation and its remaining principal/fee debt.
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

// Reservation is an active, converted, or released funding commitment.
type Reservation struct {
	ID             string  `json:"id"`
	TransactionID  string  `json:"transaction_id"`
	PrincipalCents int64   `json:"principal_cents"`
	FeeCents       int64   `json:"fee_cents"`
	Status         string  `json:"status"`
	BridgeID       *string `json:"bridge_id"`
}

// Invoice contains verified eligible unpaid receivable and allocation facts.
type Invoice struct {
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

// Classification is the verified merchant-category mapping for a payment.
type Classification struct {
	TransactionID    string          `json:"transaction_id"`
	MerchantCategory string          `json:"merchant_category"`
	SourceRef        SourceReference `json:"source_ref"`
}

// CustomerSnapshot is a typed, immutable customer input file.
type CustomerSnapshot struct {
	SnapshotMetadata
	CustomerID      string           `json:"customer_id"`
	AccountID       string           `json:"account_id"`
	CardID          string           `json:"card_id"`
	Coverage        Coverage         `json:"coverage"`
	Standing        *Standing        `json:"standing"`
	HistoryWindow   HistoryWindow    `json:"history_window"`
	BalanceHistory  []Balance        `json:"balance_history"`
	InflowHistory   []Inflow         `json:"inflow_history"`
	Bridges         []Bridge         `json:"bridges"`
	Reservations    []Reservation    `json:"reservations"`
	Receivables     []Invoice        `json:"receivables"`
	Classifications []Classification `json:"classifications"`
}

// FacilitySnapshot is a typed immutable view of one selected facility ledger.
type FacilitySnapshot struct {
	SnapshotMetadata
	FacilityID             string `json:"facility_id"`
	Currency               string `json:"currency"`
	FacilityTotalCents     int64  `json:"facility_total_cents"`
	ReserveFloorBPS        int32  `json:"reserve_floor_bps"`
	FundedPrincipalCents   int64  `json:"funded_principal_cents"`
	ReservedPrincipalCents int64  `json:"reserved_principal_cents"`
	LedgerVersion          string `json:"ledger_version"`
	LedgerComplete         bool   `json:"ledger_complete"`
}

// ImportResult is the safe identifier/version result returned by snapshot import.
type ImportResult struct {
	SnapshotID string `json:"snapshot_id"`
	Version    int64  `json:"version"`
	Replayed   bool   `json:"replayed"`
}

// CustomerSnapshotRecord adds database identity to its immutable typed content.
type CustomerSnapshotRecord struct {
	ID       string
	Version  int64
	Snapshot CustomerSnapshot
}

// FacilitySnapshotRecord adds database identity to its immutable typed content.
type FacilitySnapshotRecord struct {
	ID       string
	Version  int64
	Snapshot FacilitySnapshot
}

// ShortfallRecord is the strict allowlist for the shortfall service detail response.
type ShortfallRecord struct {
	ID                    string     `json:"id"`
	CustomerID            *string    `json:"customer_id"`
	AccountID             string     `json:"account_id"`
	CardID                *string    `json:"card_id"`
	Outcome               string     `json:"outcome"`
	Reason                *string    `json:"reason"`
	Currency              *string    `json:"currency"`
	AttemptedAmountCents  *int64     `json:"attempted_amount_cents"`
	AvailableBalanceCents *int64     `json:"available_balance_cents"`
	ShortfallCents        *int64     `json:"shortfall_cents"`
	CanonicalEventID      string     `json:"canonical_event_id"`
	TransactionID         *string    `json:"transaction_id"`
	LifecycleID           *string    `json:"lifecycle_id"`
	CreatedAt             time.Time  `json:"created_at"`
	ProviderCreatedAt     *time.Time `json:"provider_created_at"`
	ProviderTransactedAt  *time.Time `json:"provider_transacted_at"`
	ProviderUpdatedAt     *time.Time `json:"provider_updated_at"`
	BalanceRequestedAt    *time.Time `json:"balance_requested_at"`
	BalanceObservedAt     *time.Time `json:"balance_observed_at"`
	RequiresRecheck       bool       `json:"requires_recheck"`
	AmountSemantics       string     `json:"amount_semantics"`
}

// EvaluationRequest contains the only caller-selected evaluation fields.
type EvaluationRequest struct {
	ShortfallID string `json:"shortfall_id"`
	TermDays    int    `json:"term_days"`
}

// EvaluationRule is the wire-safe per-rule portion of a saved assessment.
type EvaluationRule struct {
	RuleID     string            `json:"rule_id"`
	Status     string            `json:"status"`
	ReasonCode string            `json:"reason_code"`
	Actual     map[string]any    `json:"actual"`
	Threshold  map[string]any    `json:"threshold"`
	SourceRefs []SourceReference `json:"source_refs"`
}

// EvaluationResponse is the pinned HTTP response and stored replay body.
type EvaluationResponse struct {
	ID                       string           `json:"id"`
	ShortfallID              string           `json:"shortfall_id"`
	CustomerID               string           `json:"customer_id"`
	AccountID                string           `json:"account_id"`
	TransactionID            string           `json:"transaction_id"`
	SourceMode               string           `json:"source_mode"`
	EvaluatedAt              time.Time        `json:"evaluated_at"`
	CreatedAt                time.Time        `json:"created_at"`
	PolicyID                 string           `json:"policy_id"`
	PolicyHash               string           `json:"policy_hash"`
	CustomerSnapshotID       *string          `json:"customer_snapshot_id"`
	CustomerSnapshotVersion  *int64           `json:"customer_snapshot_version"`
	FacilitySnapshotID       *string          `json:"facility_snapshot_id"`
	FacilitySnapshotVersion  *int64           `json:"facility_snapshot_version"`
	Status                   string           `json:"status"`
	ApprovalRequired         bool             `json:"approval_required"`
	FundingAuthorized        bool             `json:"funding_authorized"`
	RequiresRecheck          bool             `json:"requires_recheck"`
	Tier                     *string          `json:"tier"`
	CustomerLimitCents       *int64           `json:"customer_limit_cents"`
	ExistingExposureCents    *int64           `json:"existing_exposure_cents"`
	PostFundingExposureCents *int64           `json:"post_funding_exposure_cents"`
	AvailableCapitalCents    *int64           `json:"available_capital_cents"`
	PrincipalCents           *int64           `json:"principal_cents"`
	TermDays                 int              `json:"term_days"`
	EstimatedFeeCents        *int64           `json:"estimated_fee_cents"`
	TotalDueCents            *int64           `json:"total_due_cents"`
	Rules                    []EvaluationRule `json:"rules"`
	ReasonCodes              []string         `json:"reason_codes"`
}

// CustomerContextData contains normalized financial and standing inputs only.
type CustomerContextData struct {
	AccountID       string           `json:"account_id"`
	CardID          string           `json:"card_id"`
	Standing        *Standing        `json:"standing"`
	HistoryWindow   HistoryWindow    `json:"history_window"`
	BalanceHistory  []Balance        `json:"balance_history"`
	InflowHistory   []Inflow         `json:"inflow_history"`
	Bridges         []Bridge         `json:"bridges"`
	Reservations    []Reservation    `json:"reservations"`
	Receivables     []Invoice        `json:"receivables"`
	Classifications []Classification `json:"classifications"`
}

// CustomerContextResponse is the bounded coordinator read view.
type CustomerContextResponse struct {
	SchemaVersion   int                 `json:"schema_version"`
	SourceMode      string              `json:"source_mode"`
	CustomerID      string              `json:"customer_id"`
	SnapshotID      string              `json:"snapshot_id"`
	SnapshotVersion int64               `json:"snapshot_version"`
	CapturedAt      time.Time           `json:"captured_at"`
	PolicyID        string              `json:"policy_id"`
	PolicyHash      string              `json:"policy_hash"`
	Coverage        Coverage            `json:"coverage"`
	Fresh           bool                `json:"fresh"`
	Data            CustomerContextData `json:"data"`
	SourceRefs      []SourceReference   `json:"source_refs"`
}

// EvaluationCursor is an opaque, filter-bound keyset position.
type EvaluationCursor struct {
	Mode       string    `json:"mode"`
	CustomerID string    `json:"customer_id"`
	CreatedAt  time.Time `json:"created_at"`
	ID         string    `json:"id"`
}

// EvaluationPage contains saved response bodies and a scoped next cursor.
type EvaluationPage struct {
	Items      []json.RawMessage
	NextCursor *EvaluationCursor
}

// ValidMode reports whether mode is a supported source boundary.
func ValidMode(mode string) bool {
	return mode == "DEMO" || mode == "SANDBOX"
}

// ValidID reports whether value is a canonical hyphenated UUID.
func ValidID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') ||
			(char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

// ValidateCustomer checks shape, linkage fields, dates, amounts and internal references.
func ValidateCustomer(s CustomerSnapshot, now time.Time) error {
	if !validMetadata(s.SnapshotMetadata, now) || !validText(s.CustomerID) ||
		!validText(s.AccountID) || !validText(s.CardID) ||
		!validDate(s.HistoryWindow.WindowStart) || !validDate(s.HistoryWindow.WindowEnd) ||
		s.HistoryWindow.WindowStart > s.HistoryWindow.WindowEnd ||
		!validArrayLengths(len(s.SourceRefs), len(s.BalanceHistory), len(s.InflowHistory),
			len(s.Bridges), len(s.Reservations), len(s.Receivables), len(s.Classifications)) ||
		s.BalanceHistory == nil || s.InflowHistory == nil || s.Bridges == nil ||
		s.Reservations == nil || s.Receivables == nil || s.Classifications == nil {
		return errInvalidSnapshot
	}
	captureDate := Date(s.CapturedAt.UTC().Format("2006-01-02"))
	if s.Standing == nil {
		if s.Coverage.Standing {
			return errInvalidSnapshot
		}
	} else {
		if s.Coverage.Standing && (s.Standing.RelationshipStartedOn == nil ||
			s.Standing.OnboardingComplete == nil || s.Standing.Active == nil ||
			s.Standing.Suspended == nil || s.Standing.ProviderAccountStatus == nil) {
			return errInvalidSnapshot
		}
		if s.Standing.RelationshipStartedOn != nil &&
			(!validDate(*s.Standing.RelationshipStartedOn) || *s.Standing.RelationshipStartedOn > captureDate) {
			return errInvalidSnapshot
		}
		if s.Standing.ProviderAccountStatus != nil &&
			!oneOf(*s.Standing.ProviderAccountStatus, "ACTIVE", "FROZEN", "CLOSED", "UNKNOWN") {
			return errInvalidSnapshot
		}
	}
	windowDays := daysBetween(s.HistoryWindow.WindowStart, s.HistoryWindow.WindowEnd)
	if windowDays < 0 || windowDays >= MaxSnapshotRecords ||
		s.HistoryWindow.WindowEnd > captureDate {
		return errInvalidSnapshot
	}
	balanceDates := make(map[Date]struct{}, len(s.BalanceHistory))
	for _, row := range s.BalanceHistory {
		if !validDate(row.Date) || row.Date < s.HistoryWindow.WindowStart ||
			row.Date > s.HistoryWindow.WindowEnd {
			return errInvalidSnapshot
		}
		if _, exists := balanceDates[row.Date]; exists {
			return errInvalidSnapshot
		}
		balanceDates[row.Date] = struct{}{}
	}
	if s.Coverage.BalanceHistory && int64(len(balanceDates)) != windowDays+1 {
		return errInvalidSnapshot
	}
	inflowIDs := make(map[string]struct{}, len(s.InflowHistory))
	for _, row := range s.InflowHistory {
		if !validText(row.ID) || !validText(row.PayerID) || !validDate(row.Date) ||
			row.Date < s.HistoryWindow.WindowStart || row.Date > s.HistoryWindow.WindowEnd ||
			row.Date > captureDate || row.AmountCents < 0 ||
			!oneOf(row.Kind, "EXTERNAL_SETTLED", "OWN_TRANSFER", "REFUND", "BRIDGE_FUNDING", "REVERSAL", "UNKNOWN") ||
			(row.Kind == "UNKNOWN" && s.Coverage.InflowHistory) {
			return errInvalidSnapshot
		}
		if _, exists := inflowIDs[row.ID]; exists {
			return errInvalidSnapshot
		}
		inflowIDs[row.ID] = struct{}{}
	}
	if err := validateBridges(s.Bridges, captureDate); err != nil {
		return err
	}
	if err := validateReservations(s.Reservations, s.Bridges); err != nil {
		return err
	}
	if err := validateReceivables(s.Receivables, s.SourceRefs); err != nil {
		return err
	}
	if err := validateClassifications(s.Classifications, s.SourceRefs); err != nil {
		return err
	}
	var customerExposure int64
	for _, row := range s.Bridges {
		var ok bool
		customerExposure, ok = checkedSum(customerExposure, row.PrincipalOwedCents)
		if !ok {
			return errInvalidSnapshot
		}
		customerExposure, ok = checkedSum(customerExposure, row.FeesOwedCents)
		if !ok {
			return errInvalidSnapshot
		}
	}
	for _, row := range s.Reservations {
		if row.Status != "ACTIVE" {
			continue
		}
		var ok bool
		customerExposure, ok = checkedSum(customerExposure, row.PrincipalCents)
		if !ok {
			return errInvalidSnapshot
		}
		customerExposure, ok = checkedSum(customerExposure, row.FeeCents)
		if !ok {
			return errInvalidSnapshot
		}
	}
	return nil
}

// ValidateFacility checks a facility snapshot's configured identity and ledger totals.
func ValidateFacility(s FacilitySnapshot, expectedID string, now time.Time) error {
	if !validMetadata(s.SnapshotMetadata, now) || !validText(s.FacilityID) ||
		s.FacilityID != expectedID || s.Currency != "USD" ||
		s.FacilityTotalCents <= 0 || s.ReserveFloorBPS < 0 || s.ReserveFloorBPS > 10000 ||
		s.FundedPrincipalCents < 0 || s.ReservedPrincipalCents < 0 ||
		!validText(s.LedgerVersion) {
		return errInvalidSnapshot
	}
	if _, ok := checkedSum(s.FundedPrincipalCents, s.ReservedPrincipalCents); !ok {
		return errInvalidSnapshot
	}
	return nil
}

var errInvalidSnapshot = snapshotError("invalid_snapshot")

// snapshotError is a fixed safe import error, without user-provided values.
type snapshotError string

func (e snapshotError) Error() string { return string(e) }

func validMetadata(m SnapshotMetadata, now time.Time) bool {
	if m.SchemaVersion != SchemaVersion || !ValidMode(m.SourceMode) ||
		!validText(m.SourceID) || !validText(m.SourceVersion) || m.CapturedAt.IsZero() ||
		!isUTC(m.CapturedAt) || m.CapturedAt.After(now.Add(5*time.Second)) ||
		len(m.SourceRefs) == 0 || len(m.SourceRefs) > MaxSnapshotRecords {
		return false
	}
	seen := make(map[string]struct{}, len(m.SourceRefs))
	for i := range m.SourceRefs {
		r := &m.SourceRefs[i]
		if !validText(r.System) || !validText(r.RecordID) || !validText(r.Version) ||
			r.EffectiveAt.IsZero() || !isUTC(r.EffectiveAt) {
			return false
		}
		key := r.System + "\x00" + r.RecordID + "\x00" + r.Version
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
		r.EffectiveAt = r.EffectiveAt.UTC()
	}
	m.CapturedAt = m.CapturedAt.UTC()
	return true
}

func validArrayLengths(lengths ...int) bool {
	for _, length := range lengths {
		if length > MaxSnapshotRecords {
			return false
		}
	}
	return true
}

func validateBridges(rows []Bridge, captureDate Date) error {
	ids := make(map[string]struct{}, len(rows))
	openByTransaction := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if !validText(row.ID) || !validText(row.TransactionID) ||
			row.OriginalPrincipalCents <= 0 || row.PrincipalOwedCents < 0 ||
			row.PrincipalOwedCents > row.OriginalPrincipalCents || row.FeesOwedCents < 0 ||
			!sumValid(row.PrincipalOwedCents, row.FeesOwedCents) ||
			!validDate(row.OpenedOn) || !validDate(row.DueOn) || row.DueOn < row.OpenedOn ||
			row.OpenedOn > captureDate {
			return errInvalidSnapshot
		}
		if _, exists := ids[row.ID]; exists {
			return errInvalidSnapshot
		}
		ids[row.ID] = struct{}{}
		if row.ClosedOn != nil && (!validDate(*row.ClosedOn) || *row.ClosedOn < row.OpenedOn || *row.ClosedOn > captureDate) {
			return errInvalidSnapshot
		}
		if row.DefaultedOn != nil && (!validDate(*row.DefaultedOn) || *row.DefaultedOn < row.OpenedOn || *row.DefaultedOn > captureDate) {
			return errInvalidSnapshot
		}
		switch row.Status {
		case "OPEN":
			if row.ClosedOn != nil || row.DefaultedOn != nil || row.RepaymentOutcome != nil {
				return errInvalidSnapshot
			}
			if _, exists := openByTransaction[row.TransactionID]; exists {
				return errInvalidSnapshot
			}
			openByTransaction[row.TransactionID] = struct{}{}
		case "REPAID":
			if row.PrincipalOwedCents != 0 || row.FeesOwedCents != 0 || row.ClosedOn == nil ||
				row.RepaymentOutcome == nil || !validRepaymentOutcome(row) ||
				(row.DefaultedOn != nil && *row.ClosedOn < *row.DefaultedOn) {
				return errInvalidSnapshot
			}
		case "DEFAULTED":
			// A default event remains in history if the obligation is later repaid.
			if row.DefaultedOn == nil || *row.DefaultedOn < row.DueOn ||
				(row.ClosedOn == nil) != (row.RepaymentOutcome == nil) ||
				(row.ClosedOn != nil && *row.ClosedOn < *row.DefaultedOn) ||
				(row.ClosedOn != nil && (row.PrincipalOwedCents != 0 || row.FeesOwedCents != 0 ||
					!validRepaymentOutcome(row))) {
				return errInvalidSnapshot
			}
		default:
			return errInvalidSnapshot
		}
	}
	return nil
}

func validRepaymentOutcome(row Bridge) bool {
	if row.ClosedOn == nil || row.RepaymentOutcome == nil ||
		!oneOf(*row.RepaymentOutcome, "ON_TIME", "LATE") {
		return false
	}
	return (*row.ClosedOn <= row.DueOn) == (*row.RepaymentOutcome == "ON_TIME")
}

func validateReservations(rows []Reservation, bridges []Bridge) error {
	ids := make(map[string]struct{}, len(rows))
	bridgesByID := make(map[string]Bridge, len(bridges))
	debtByTransaction := make(map[string]bool, len(bridges))
	for _, row := range bridges {
		bridgesByID[row.ID] = row
		if row.PrincipalOwedCents > 0 || row.FeesOwedCents > 0 {
			debtByTransaction[row.TransactionID] = true
		}
	}
	activeByTransaction := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if !validText(row.ID) || !validText(row.TransactionID) || row.PrincipalCents < 0 ||
			row.FeeCents < 0 || !sumValid(row.PrincipalCents, row.FeeCents) {
			return errInvalidSnapshot
		}
		if _, exists := ids[row.ID]; exists {
			return errInvalidSnapshot
		}
		ids[row.ID] = struct{}{}
		switch row.Status {
		case "ACTIVE":
			if row.BridgeID != nil || debtByTransaction[row.TransactionID] {
				return errInvalidSnapshot
			}
			if _, exists := activeByTransaction[row.TransactionID]; exists {
				return errInvalidSnapshot
			}
			activeByTransaction[row.TransactionID] = struct{}{}
		case "CONVERTED":
			if row.BridgeID == nil || !validText(*row.BridgeID) {
				return errInvalidSnapshot
			}
			bridge, exists := bridgesByID[*row.BridgeID]
			if !exists || bridge.TransactionID != row.TransactionID {
				return errInvalidSnapshot
			}
		case "RELEASED":
			if row.BridgeID != nil {
				return errInvalidSnapshot
			}
		default:
			return errInvalidSnapshot
		}
	}
	return nil
}

func validateReceivables(rows []Invoice, refs []SourceReference) error {
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if !validText(row.InvoiceID) || !validText(row.PayerID) || row.Currency != "USD" ||
			row.EligibleUnpaidCents < 0 || row.AllocatedCents < 0 ||
			row.AllocatedCents > row.EligibleUnpaidCents ||
			(row.DueDateVerified && row.DueOn == nil) || (row.DueOn != nil && !validDate(*row.DueOn)) ||
			!containsReference(refs, row.SourceRef) {
			return errInvalidSnapshot
		}
		if _, exists := ids[row.InvoiceID]; exists {
			return errInvalidSnapshot
		}
		ids[row.InvoiceID] = struct{}{}
	}
	return nil
}

func validateClassifications(rows []Classification, refs []SourceReference) error {
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if !validText(row.TransactionID) || !validText(row.MerchantCategory) ||
			!containsReference(refs, row.SourceRef) {
			return errInvalidSnapshot
		}
		if _, exists := ids[row.TransactionID]; exists {
			return errInvalidSnapshot
		}
		ids[row.TransactionID] = struct{}{}
	}
	return nil
}

func containsReference(refs []SourceReference, target SourceReference) bool {
	for _, ref := range refs {
		if ref.System == target.System && ref.RecordID == target.RecordID && ref.Version == target.Version &&
			ref.EffectiveAt.Equal(target.EffectiveAt) {
			return validText(target.System) && validText(target.RecordID) && validText(target.Version) && !target.EffectiveAt.IsZero() && isUTC(target.EffectiveAt)
		}
	}
	return false
}

func validText(value string) bool {
	return value != "" && utf8.ValidString(value) && value == strings.TrimSpace(value) && len(value) <= 200 &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validDate(value Date) bool {
	parsed, err := time.Parse("2006-01-02", string(value))
	return err == nil && parsed.Format("2006-01-02") == string(value)
}

func daysBetween(start, end Date) int64 {
	first, err := time.Parse("2006-01-02", string(start))
	if err != nil {
		return -1
	}
	last, err := time.Parse("2006-01-02", string(end))
	if err != nil {
		return -1
	}
	return int64(last.Sub(first).Hours() / 24)
}

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func sumValid(left, right int64) bool {
	_, ok := checkedSum(left, right)
	return ok
}

func checkedSum(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}
