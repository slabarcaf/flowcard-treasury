// Package domain contains persisted funding and collection record values.
package domain

import "time"

// Mode isolates fixture and sandbox books.
type Mode string

const (
	ModeDemo    Mode = "DEMO"
	ModeSandbox Mode = "SANDBOX"
)

// Lifecycle is the durable bridge state; holds remain separate fields.
type Lifecycle string

const (
	Reserved       Lifecycle = "RESERVED"
	FundingPending Lifecycle = "FUNDING_PENDING"
	Active         Lifecycle = "ACTIVE"
	Collecting     Lifecycle = "COLLECTING"
	Repaid         Lifecycle = "REPAID"
	VoidUnfunded   Lifecycle = "VOID_UNFUNDED"
	ReopenedHeld   Lifecycle = "REOPENED_HELD"
)

// ReservationState records the funding commitment state.
type ReservationState string

const (
	ReservationNeverSent  ReservationState = "NEVER_SENT"
	ReservationDispatched ReservationState = "DISPATCHED"
	ReservationConverted  ReservationState = "CONVERTED"
	ReservationReleased   ReservationState = "RELEASED"
)

// OperationState records durable provider certainty.
type OperationState string

const (
	OperationQueued          OperationState = "QUEUED"
	OperationDispatching     OperationState = "DISPATCHING"
	OperationPending         OperationState = "PENDING"
	OperationUnknown         OperationState = "UNKNOWN"
	OperationHeld            OperationState = "HELD"
	OperationSettled         OperationState = "SETTLED"
	OperationFailedConfirmed OperationState = "FAILED_CONFIRMED"
	OperationCancelledUnsent OperationState = "CANCELLED_UNSENT"
)

// Money stores checked integer USD cents.
type Money int64

// Offer holds immutable canonical terms and a distinct mutable aggregate version.
type Offer struct {
	ID        string
	Mode      Mode
	Revision  int64
	Version   int64
	TermsHash string
	TermsJSON []byte
	State     string
	ExpiresAt time.Time
}

// Bridge stores debt counters and immutable original offer binding.
type Bridge struct {
	ID                        string
	Mode                      Mode
	CustomerID                string
	OfferID                   string
	Version                   int64
	Lifecycle                 Lifecycle
	EverFunded                bool
	PrincipalOwed             Money
	ContractFeeOwed           Money
	LateFeeOwed               Money
	LastAccruedOn             *time.Time
	CumulativeLateFeeAssessed Money
	CumulativeLateFeePaid     Money
	BackingDeficit            Money
}

// LedgerLine is one balanced USD posting leg.
type LedgerLine struct {
	Account string
	Debit   Money
	Credit  Money
}

// Change is a committed, mode-sequenced coordinator feed item.
type Change struct {
	Mode            Mode
	Sequence        int64
	ResourceType    string
	ResourceID      string
	ResourceVersion int64
	ChangeType      string
	RecordedAt      time.Time
	Summary         []byte
}

// Operation is immutable transfer intent plus mutable processing state.
type Operation struct {
	ID               string
	Mode             Mode
	Kind             string
	State            OperationState
	Version          int64
	RequestID        string
	PayloadHash      string
	Amount           Money
	ProviderObjectID *string
	LeaseToken       *string
	LeaseExpiresAt   *time.Time
}
