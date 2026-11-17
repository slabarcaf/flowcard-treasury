package main

import "time"

const (
	fixtureVersion           = 2
	fixtureFacilityID        = "facility_underwriting_demo"
	fixtureSourceBindingID   = "flowcard-integration-20261114"
	fundingDatabaseName      = "flowcard_funding_demo"
	underwritingDatabaseName = "flowcard_underwriting_demo"
	borrowerOpeningCents     = int64(200_000)
	fundingOpeningCents      = int64(100_000_000)
	providerAddress          = "127.0.0.1:8091"
	apiAddress               = "127.0.0.1:8085"
	underwritingAddress      = "127.0.0.1:8083"
	shortfallAddress         = "127.0.0.1:8084"
	upstreamInputComponent   = "COMPONENT_SYNTHETIC_INPUTS"
	upstreamInputOwnedBundle = "OWNED_KNOWN_EMPTY_UPSTREAM_BUNDLE"
)

type fundingManifest struct {
	SchemaVersion            int                  `json:"schema_version"`
	RunID                    string               `json:"run_id"`
	RunAnchor                time.Time            `json:"run_anchor"`
	SourceMode               string               `json:"source_mode"`
	SourceBindingID          string               `json:"source_binding_id"`
	FacilityID               string               `json:"facility_id"`
	FundingAccountRef        string               `json:"funding_account_ref"`
	FundingDatabaseName      string               `json:"funding_database_name"`
	UnderwritingDatabaseName string               `json:"underwriting_database_name"`
	FundingOpeningCents      int64                `json:"funding_account_opening_cents"`
	APIAddress               string               `json:"api_address"`
	ProviderAddress          string               `json:"provider_address"`
	PolicyID                 string               `json:"policy_id"`
	PolicyHash               string               `json:"policy_hash"`
	UpstreamInputKind        string               `json:"upstream_input_kind"`
	ArtifactRoot             string               `json:"artifact_root"`
	PrivateEnvFile           string               `json:"private_env_file"`
	ProviderStateFile        string               `json:"provider_state_file"`
	FacilityAbundantFile     string               `json:"facility_abundant_file"`
	FacilityTightFile        string               `json:"facility_tight_file"`
	ReceivablesFile          string               `json:"receivables_file"`
	AuthorityFile            string               `json:"authority_file"`
	ActorsFile               string               `json:"actors_file"`
	MappingSQLFile           string               `json:"mapping_sql_file"`
	UpstreamProfiles         []upstreamProfileRef `json:"upstream_profiles"`
	Provenance               string               `json:"provenance"`
}

type upstreamProfileRef struct {
	Name                 string   `json:"name"`
	UnderwritingManifest string   `json:"underwriting_manifest"`
	CustomerID           string   `json:"customer_id"`
	AccountID            string   `json:"account_id"`
	CardID               string   `json:"card_id"`
	OperatorSubject      string   `json:"operator_subject"`
	CustomerSubject      string   `json:"customer_subject"`
	ShortfallID          string   `json:"shortfall_id"`
	TransactionID        string   `json:"transaction_id"`
	CanonicalEventID     string   `json:"canonical_event_id"`
	ExpectedStatus       string   `json:"expected_status"`
	ApprovalRequired     bool     `json:"approval_required"`
	PrincipalCents       int64    `json:"principal_cents"`
	TermDays             int      `json:"term_days"`
	ContractFeeCents     int64    `json:"contract_fee_cents"`
	TotalDueCents        int64    `json:"total_due_cents"`
	AttemptedCents       int64    `json:"attempted_amount_cents"`
	AvailableCents       int64    `json:"available_balance_cents"`
	ShortfallCents       int64    `json:"shortfall_cents"`
	ProviderDepositIDs   []string `json:"provider_deposit_ids"`
}

type upstreamManifest struct {
	FixturePackVersion int                   `json:"fixture_pack_version"`
	RunID              string                `json:"run_id"`
	RunAnchor          time.Time             `json:"run_anchor"`
	SourceMode         string                `json:"source_mode"`
	PolicyID           string                `json:"policy_id"`
	PolicyHash         string                `json:"policy_hash"`
	OutputDirectory    string                `json:"output_directory"`
	DatabaseName       string                `json:"database_name"`
	FacilityID         string                `json:"facility_id"`
	FacilityFile       string                `json:"facility_file"`
	ShortfallsFile     string                `json:"shortfalls_file"`
	MappingSetupSQL    string                `json:"mapping_setup_sql"`
	ExpectedTraceFile  string                `json:"expected_trace_file"`
	FacilityBooks      upstreamFacilityBooks `json:"facility_books"`
	Scenarios          []upstreamScenario    `json:"scenarios"`
	Provenance         string                `json:"provenance"`
}

type upstreamFacilityBooks struct {
	FacilityTotalCents             int64 `json:"facility_total_cents"`
	ReserveFloorBPS                int64 `json:"reserve_floor_bps"`
	FundedPrincipalCents           int64 `json:"funded_principal_cents"`
	CustomerFundedPrincipalCents   int64 `json:"customer_funded_principal_cents"`
	OtherFundedPrincipalCents      int64 `json:"other_funded_principal_cents"`
	ReservedPrincipalCents         int64 `json:"reserved_principal_cents"`
	CustomerReservedPrincipalCents int64 `json:"customer_reserved_principal_cents"`
	OtherReservedPrincipalCents    int64 `json:"other_reserved_principal_cents"`
	FeesCountedInFacilityCash      bool  `json:"fees_counted_in_facility_cash"`
	AvailableCapitalCents          int64 `json:"available_capital_cents"`
}

type upstreamScenario struct {
	ID                       string         `json:"scenario_id"`
	CustomerID               string         `json:"customer_id"`
	AccountID                string         `json:"account_id"`
	CardID                   string         `json:"card_id"`
	ShortfallID              string         `json:"shortfall_id"`
	TransactionID            string         `json:"transaction_id"`
	CanonicalEventID         string         `json:"canonical_event_id"`
	LifecycleID              string         `json:"lifecycle_id"`
	IdempotencyKey           string         `json:"idempotency_key"`
	CustomerFile             string         `json:"customer_file"`
	ExpectedStatus           string         `json:"expected_status"`
	PrincipalCents           int64          `json:"principal_cents"`
	TermDays                 int            `json:"term_days"`
	EstimatedFeeCents        int64          `json:"estimated_fee_cents"`
	TotalDueCents            int64          `json:"total_due_cents"`
	CustomerLimitCents       int64          `json:"customer_limit_cents"`
	ExistingExposureCents    int64          `json:"existing_exposure_cents"`
	PostFundingExposureCents int64          `json:"post_funding_exposure_cents"`
	FreeReceivablesCents     int64          `json:"free_receivables_cents"`
	ExpectedRules            []expectedRule `json:"expected_rules"`
	Provenance               string         `json:"provenance"`
}

type expectedRule struct {
	RuleID string `json:"rule_id"`
	Status string `json:"status"`
}

type sourceReference struct {
	System      string    `json:"system"`
	RecordID    string    `json:"record_id"`
	Version     string    `json:"version"`
	EffectiveAt time.Time `json:"effective_at"`
}

type customerSnapshot struct {
	SchemaVersion   int               `json:"schema_version"`
	SourceMode      string            `json:"source_mode"`
	SourceID        string            `json:"source_id"`
	SourceVersion   string            `json:"source_version"`
	CapturedAt      time.Time         `json:"captured_at"`
	SourceRefs      []sourceReference `json:"source_refs"`
	CustomerID      string            `json:"customer_id"`
	AccountID       string            `json:"account_id"`
	CardID          string            `json:"card_id"`
	Coverage        map[string]bool   `json:"coverage"`
	Standing        map[string]any    `json:"standing"`
	HistoryWindow   map[string]string `json:"history_window"`
	BalanceHistory  []map[string]any  `json:"balance_history"`
	InflowHistory   []map[string]any  `json:"inflow_history"`
	Bridges         []map[string]any  `json:"bridges"`
	Reservations    []map[string]any  `json:"reservations"`
	Receivables     []map[string]any  `json:"receivables"`
	Classifications []map[string]any  `json:"classifications"`
}

type shortfallRecord struct {
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

type shortfallPack struct {
	SourceMode string           `json:"source_mode"`
	Provenance string           `json:"provenance"`
	Records    []shortfallEntry `json:"records"`
}

type shortfallEntry struct {
	ScenarioID string          `json:"scenario_id"`
	Response   shortfallRecord `json:"response"`
}

type facilitySnapshot struct {
	SchemaVersion          int               `json:"schema_version"`
	SourceMode             string            `json:"source_mode"`
	SourceID               string            `json:"source_id"`
	SourceVersion          string            `json:"source_version"`
	CapturedAt             time.Time         `json:"captured_at"`
	SourceRefs             []sourceReference `json:"source_refs"`
	FacilityID             string            `json:"facility_id"`
	Currency               string            `json:"currency"`
	FacilityTotalCents     int64             `json:"facility_total_cents"`
	ReserveFloorBPS        int32             `json:"reserve_floor_bps"`
	FundedPrincipalCents   int64             `json:"funded_principal_cents"`
	ReservedPrincipalCents int64             `json:"reserved_principal_cents"`
	LedgerVersion          string            `json:"ledger_version"`
	LedgerComplete         bool              `json:"ledger_complete"`
}

type providerState struct {
	SchemaVersion               int                         `json:"schema_version"`
	RunID                       string                      `json:"run_id"`
	Provenance                  string                      `json:"provenance"`
	FundingAccountRef           string                      `json:"funding_account_ref"`
	Accounts                    map[string]int64            `json:"accounts_cents"`
	Payments                    map[string]providerPayment  `json:"payments"`
	Deposits                    map[string]providerDeposit  `json:"deposits"`
	Transfers                   map[string]providerTransfer `json:"transfers"`
	RequestIndex                map[string]string           `json:"request_index"`
	DropFirstCollectionFeeReply bool                        `json:"drop_first_collection_fee_reply"`
	DroppedReplyRequestID       string                      `json:"dropped_reply_request_id,omitempty"`
}

type providerPayment struct {
	CustomerID           string `json:"customer_id"`
	AccountID            string `json:"account_id"`
	AuthorizationEventID string `json:"authorization_event_id"`
	AttemptedCents       int64  `json:"attempted_amount_cents"`
	ShortfallCents       int64  `json:"shortfall_cents"`
}

type providerDeposit struct {
	ID         string     `json:"deposit_id"`
	CustomerID string     `json:"customer_id"`
	AccountRef string     `json:"account_ref"`
	GrossCents int64      `json:"gross_cents"`
	NetCents   int64      `json:"net_cents"`
	FeeCents   int64      `json:"provider_fee_cents"`
	Settled    bool       `json:"settled"`
	SettledAt  *time.Time `json:"settled_at"`
}

type providerTransfer struct {
	ID                  string     `json:"id"`
	RequestID           string     `json:"request_id"`
	SourceAccountID     string     `json:"source_account_id"`
	Destination         string     `json:"destination"`
	AmountCents         int64      `json:"amount_cents"`
	Reason              string     `json:"reason"`
	Reference           string     `json:"reference"`
	Status              string     `json:"status"`
	SettledAt           time.Time  `json:"settled_at"`
	CreatedAt           time.Time  `json:"created_at"`
	ReversalID          string     `json:"reversal_id,omitempty"`
	ReversedAmountCents int64      `json:"reversed_amount_cents,omitempty"`
	ReversalCurrency    string     `json:"reversal_currency,omitempty"`
	ReversedAt          *time.Time `json:"reversed_at,omitempty"`
}
