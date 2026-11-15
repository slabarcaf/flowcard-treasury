package domain

import "time"

type SourceRef struct {
	System      string    `json:"system"`
	RecordID    string    `json:"record_id"`
	Version     string    `json:"version"`
	EffectiveAt time.Time `json:"effective_at"`
}

type ImportMetadata struct {
	SchemaVersion int         `json:"schema_version"`
	SourceMode    string      `json:"source_mode"`
	SourceID      string      `json:"source_id"`
	SourceVersion string      `json:"source_version"`
	CapturedAt    time.Time   `json:"captured_at"`
	SourceRefs    []SourceRef `json:"source_refs"`
}

type FacilityImport struct {
	ImportMetadata
	FacilityID            string                   `json:"facility_id"`
	FundingAccountRef     string                   `json:"funding_account_ref"`
	Currency              string                   `json:"currency"`
	FacilityTotalCents    int64                    `json:"facility_total_cents"`
	ReserveFloorBPS       int                      `json:"reserve_floor_bps"`
	OtherCommitmentsCents int64                    `json:"other_commitments_cents"`
	KnownEmptyBooks       bool                     `json:"known_empty_books"`
	Customers             []FacilityCustomerImport `json:"customers"`
}

type FacilityCustomerImport struct {
	CustomerID      string `json:"customer_id"`
	AccountID       string `json:"account_id"`
	CardID          string `json:"card_id"`
	HistoryComplete bool   `json:"history_complete"`
}

type ReceivablesImport struct {
	ImportMetadata
	Receivables []ReceivableImport `json:"receivables"`
}

type ReceivableImport struct {
	CustomerID          string    `json:"customer_id"`
	SourceSystem        string    `json:"source_system"`
	InvoiceID           string    `json:"invoice_id"`
	PayerID             string    `json:"payer_id"`
	Currency            string    `json:"currency"`
	EligibleUnpaidCents int64     `json:"eligible_unpaid_cents"`
	DueOn               *string   `json:"due_on"`
	DueDateVerified     bool      `json:"due_date_verified"`
	AllocationComplete  bool      `json:"allocation_complete"`
	SourceComplete      bool      `json:"source_complete"`
	Disputed            bool      `json:"disputed"`
	SourceRef           SourceRef `json:"source_ref"`
}

type AuthoritiesImport struct {
	ImportMetadata
	Authorities []AuthorityImport `json:"authorities"`
}

type AuthorityImport struct {
	ID                    string      `json:"id"`
	Version               int64       `json:"version"`
	CustomerID            string      `json:"customer_id"`
	SourceAccountRef      string      `json:"source_account_ref"`
	DestinationAccountRef string      `json:"destination_account_ref"`
	ProductCode           string      `json:"product_code"`
	MandateReference      string      `json:"mandate_reference"`
	Currency              string      `json:"currency"`
	PermittedKinds        []string    `json:"permitted_kinds"`
	PermittedPurposes     []string    `json:"permitted_purposes"`
	MaximumDebitCents     int64       `json:"maximum_debit_cents"`
	ConsumedDebitCents    int64       `json:"consumed_debit_cents"`
	ReservedDebitCents    int64       `json:"reserved_debit_cents"`
	ValidFrom             time.Time   `json:"valid_from"`
	ExpiresAt             time.Time   `json:"expires_at"`
	Status                string      `json:"status"`
	DisclosuresVersion    string      `json:"disclosures_version"`
	EvidenceRefs          []SourceRef `json:"evidence_refs"`
}

type ActorsImport struct {
	ImportMetadata
	Actors []ActorImport `json:"actors"`
}

type ActorImport struct {
	ActorID               string    `json:"actor_id"`
	ActorType             string    `json:"actor_type"`
	AuthenticationSubject string    `json:"authentication_subject"`
	CustomerID            *string   `json:"customer_id"`
	Active                bool      `json:"active"`
	SourceRef             SourceRef `json:"source_ref"`
}
