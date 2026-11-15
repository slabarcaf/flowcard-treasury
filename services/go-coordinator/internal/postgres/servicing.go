package postgres

import (
	"encoding/json"
	"time"
)

type ServicingChange struct {
	Sequence          uint64
	Mode              string
	ResourceType      string
	ResourceID        string
	ResourceVersion   int64
	BridgeID          *string
	BridgeVersion     *int64
	CustomerID        *string
	AccountRef        *string
	OriginatingCaseID *string
	EvaluationID      *string
	OfferID           *string
	ChangeType        string
	RecordedAt        time.Time
	Summary           json.RawMessage
}

func same(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// CollectionObservation is the normalized, read-only bridge and bounded history view.
type CollectionObservation struct {
	Bridge     BridgeSnapshot     `json:"bridge"`
	Collection CollectionHistory  `json:"collection"`
	Operation  *OperationSnapshot `json:"operation,omitempty"`
}
type BridgeSnapshot struct {
	ObligationID          string     `json:"obligation_id"`
	ID                    string     `json:"id"`
	Mode                  string     `json:"mode"`
	SourceBindingID       string     `json:"source_binding_id"`
	CustomerID            string     `json:"customer_id"`
	AccountRef            string     `json:"account_ref"`
	Version               int64      `json:"version"`
	AsOf                  time.Time  `json:"as_of"`
	Currency              string     `json:"currency"`
	PrincipalOwedCents    *int64     `json:"principal_owed_cents"`
	ContractFeeOwedCents  *int64     `json:"contract_fee_owed_cents"`
	LateFeeOwedCents      *int64     `json:"late_fee_owed_cents"`
	DueOn                 *string    `json:"due_on"`
	FundedAt              *time.Time `json:"funded_at"`
	Lifecycle             string     `json:"lifecycle"`
	Delinquency           string     `json:"delinquency"`
	DaysPastDue           *int       `json:"days_past_due"`
	HoldReasons           []string   `json:"hold_reasons"`
	AllowedNextActions    []string   `json:"allowed_next_actions"`
	AccrualCurrent        *bool      `json:"accrual_current"`
	AccrualCurrentThrough *string    `json:"accrual_current_through"`
	DebtAsOf              *time.Time `json:"debt_as_of"`
}
type CollectionHistory struct {
	BridgeVersion int64             `json:"bridge_version"`
	Items         []json.RawMessage `json:"items"`
	NextCursor    *string           `json:"next_cursor"`
}
type OperationSnapshot struct {
	ID                              string     `json:"id"`
	Version                         int64      `json:"version"`
	Kind                            string     `json:"kind"`
	State                           string     `json:"state"`
	ReservedAmountCents             *int64     `json:"reserved_amount_cents"`
	LastVerifiedSettlementReference *string    `json:"last_verified_settlement_reference"`
	RetryCount                      int        `json:"retry_count"`
	NextCheck                       *time.Time `json:"next_check"`
	HoldReason                      *string    `json:"hold_reason"`
}
