// Package provider defines server-owned boundaries for funding and collection.
package provider

import (
	"context"
	"errors"
	"time"
)

// OperationKind identifies an immutable provider movement intent.
type OperationKind string

const (
	OperationFund             OperationKind = "FUND"
	OperationCollectFee       OperationKind = "COLLECT_FEE"
	OperationCollectPrincipal OperationKind = "COLLECT_PRINCIPAL"
)

// OperationState describes certainty and allowed recovery behavior.
type OperationState string

const (
	StateQueued          OperationState = "QUEUED"
	StateDispatching     OperationState = "DISPATCHING"
	StatePending         OperationState = "PENDING"
	StateUnknown         OperationState = "UNKNOWN"
	StateHeld            OperationState = "HELD"
	StateSettled         OperationState = "SETTLED"
	StateFailedConfirmed OperationState = "FAILED_CONFIRMED"
	StateCancelledUnsent OperationState = "CANCELLED_UNSENT"
)

// Operation is an immutable server-created transfer intent.
type Operation struct {
	ID                    string        `json:"operation_id"`
	Kind                  OperationKind `json:"kind"`
	Mode                  string        `json:"mode"`
	SourceAccountRef      string        `json:"source_account_ref"`
	DestinationAccountRef string        `json:"destination_account_ref"`
	AmountCents           int64         `json:"amount_cents"`
	Currency              string        `json:"currency"`
	RequestID             string        `json:"request_id"`
	PayloadHash           string        `json:"payload_hash"`
	BridgeID              string        `json:"bridge_id"`
	TriggerID             string        `json:"trigger_id"`
	LateFeeCents          int64         `json:"late_fee_cents"`
	ContractFeeCents      int64         `json:"contract_fee_cents"`
	PrincipalCents        int64         `json:"principal_cents"`
}

// PaymentObservation is verified shortfall/payment and provider balance context.
type PaymentObservation struct {
	Mode                 string    `json:"source_mode"`
	CustomerID           string    `json:"customer_id"`
	CanonicalAccountID   string    `json:"canonical_account_id"`
	ParentTransactionID  string    `json:"parent_transaction_id"`
	AuthorizationEventID string    `json:"authorization_event_id"`
	AttemptedAmountCents int64     `json:"attempted_amount_cents"`
	ShortfallCents       int64     `json:"shortfall_cents"`
	Currency             string    `json:"currency"`
	ObservedAt           time.Time `json:"observed_at"`
	AvailableCents       int64     `json:"available_cents"`
	BalanceObservedAt    time.Time `json:"balance_observed_at"`
}

// BalanceObservation is a fresh available USD amount in one trusted account scope.
type BalanceObservation struct {
	AccountRef     string    `json:"account_ref"`
	Currency       string    `json:"currency"`
	AvailableCents int64     `json:"available_cents"`
	RequestedAt    time.Time `json:"requested_at"`
	ObservedAt     time.Time `json:"observed_at"`
	RawEvidence    []byte    `json:"-"`
}

// FundingFeeQuote is a known debit fee, never a transfer settlement fact.
type FundingFeeQuote struct {
	Cents      int64
	ContractID string
}

// FundingFeeQuoter exposes only explicit provider fee contracts.
type FundingFeeQuoter interface {
	QuoteFundingFee(context.Context, string, string, int64) (FundingFeeQuote, error)
}

// CollectionFeeQuoter exposes an explicit B-to-A collection fee contract.
type CollectionFeeQuoter interface {
	QuoteCollectionFee(context.Context, string, string, int64) (FundingFeeQuote, error)
}

// DepositObservation is a retrieved canonical settled external credit.
type DepositObservation struct {
	AccountRef       string     `json:"account_ref"`
	DepositID        string     `json:"deposit_id"`
	CustomerID       string     `json:"customer_id"`
	Currency         string     `json:"currency"`
	GrossCents       int64      `json:"gross_cents"`
	NetCents         int64      `json:"net_cents"`
	ProviderFeeCents *int64     `json:"provider_fee_cents"`
	Status           string     `json:"status"`
	Kind             string     `json:"kind"`
	SettledAt        *time.Time `json:"settled_at"`
	ObservedAt       time.Time  `json:"observed_at"`
	RawEvidence      []byte     `json:"-"`
}

// Receipt contains provider evidence, never an inferred settlement.
type Receipt struct {
	OperationID              string
	ProviderObjectID         string
	RequestID                string
	Status                   string
	SourceAccountRef         string
	DestinationAccountRef    string
	AmountCents              int64
	Currency                 string
	ProviderFeeCents         *int64     `json:"provider_fee_cents"`
	ObservedAt               time.Time  `json:"observed_at"`
	SettledAt                *time.Time `json:"settled_at"`
	RawEvidence              []byte     `json:"-"`
	ObservedSourceAccountRef *string    `json:"observed_source_account_ref,omitempty"`
}

// CollectionReversalObservation is DEMO-normalized evidence from the mock GET.
type CollectionReversalObservation struct {
	ReversalID        string
	OriginalObjectID  string
	RequestID         string
	AccountRef        string
	FundingAccountRef string
	AmountCents       int64
	Currency          string
	ReversedAt        time.Time
	ObservedAt        time.Time
	RawEvidence       []byte
}

// CollectionReversalReader validates DEMO normalized return evidence.
type CollectionReversalReader interface {
	NormalizeCollectionReversal(Operation, Receipt) (CollectionReversalObservation, error)
}

// Certainty distinguishes definitive outcomes from uncertain transport results.
type Certainty string

const (
	CertaintyDefinitive Certainty = "DEFINITIVE"
	CertaintyUncertain  Certainty = "UNCERTAIN"
)

// Result pairs provider evidence with explicit certainty.
type RecoveryClass string

const (
	RecoveryRetryRead   RecoveryClass = "RETRY_READ"
	RecoveryHold        RecoveryClass = "HOLD"
	RecoveryLookupOnly  RecoveryClass = "LOOKUP_ONLY"
	RecoveryVerifyFinal RecoveryClass = "VERIFY_FINAL"
)

type Result struct {
	Receipt   Receipt
	Certainty Certainty
	Recovery  RecoveryClass
}

type ProviderError struct {
	Code        string
	Recovery    RecoveryClass
	Cause       RecoveryClass
	Certainty   Certainty
	HTTPStatus  int
	RawEvidence []byte
}

func (e *ProviderError) Error() string { return e.Code }

// LookupResult keeps empty search results explicitly uncertain.
type LookupResult struct {
	Matches   []Result
	Complete  bool
	Certainty Certainty
	Recovery  RecoveryClass
	RawPages  [][]byte `json:"-"`
}

var (
	ErrProviderUnavailable      = errors.New("provider_capability_unavailable")
	ErrFundingExecutionDisabled = errors.New("funding_execution_disabled")
)

// Boundary exposes typed read, create and lookup operations. Create remains
// disabled until an independently reviewed execution task enables it.
type Boundary interface {
	Payment(context.Context, string, string) (PaymentObservation, error)
	Balance(context.Context, string) (BalanceObservation, error)
	Deposit(context.Context, string, string) (DepositObservation, error)
	Create(context.Context, Operation) (Result, error)
	Retrieve(context.Context, Operation, string) (Result, error)
	LookupByRequest(context.Context, Operation) (LookupResult, error)
}

// Unsupported is a fail-closed boundary for not-yet-implemented adapter work.
type Unsupported struct{}

func (Unsupported) Payment(context.Context, string, string) (PaymentObservation, error) {
	return PaymentObservation{}, ErrProviderUnavailable
}

func (Unsupported) Balance(context.Context, string) (BalanceObservation, error) {
	return BalanceObservation{}, ErrProviderUnavailable
}

func (Unsupported) Deposit(context.Context, string, string) (DepositObservation, error) {
	return DepositObservation{}, ErrProviderUnavailable
}

func (Unsupported) Create(context.Context, Operation) (Result, error) {
	return Result{}, ErrFundingExecutionDisabled
}

func (Unsupported) Retrieve(context.Context, Operation, string) (Result, error) {
	return Result{}, ErrProviderUnavailable
}

func (Unsupported) LookupByRequest(context.Context, Operation) (LookupResult, error) {
	return LookupResult{}, ErrProviderUnavailable
}
