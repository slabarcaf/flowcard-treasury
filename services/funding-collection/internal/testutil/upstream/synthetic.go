// Package upstreamfixture supplies clearly synthetic facts to the pinned evaluator.
package upstreamfixture

import (
	"errors"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

var (
	errSyntheticMode        = errors.New("synthetic_upstream_mode_invalid")
	errSyntheticUnavailable = errors.New("synthetic_upstream_evaluator_unavailable")
)

// Synthetic binds a real pinned evaluator to a complete, synthetic baseline
// with known-empty obligation groups and synthetic source identities.
type Synthetic struct {
	Evaluator *underwriting.Evaluator
	Input     policy.Input
}

// New loads the production-pinned public evaluator and prepares one complete,
// eligible synthetic observation. It performs no upstream network calls.
func New(now time.Time, mode string) (*Synthetic, error) {
	evaluator, err := underwriting.LoadPinned()
	if err != nil {
		return nil, err
	}
	if mode != "DEMO" && mode != "SANDBOX" {
		return nil, errSyntheticMode
	}
	instant := now.UTC()
	ref := policy.SourceReference{System: "synthetic-upstream-fixture", RecordID: "fixture-record-1",
		Version: "1", EffectiveAt: instant}
	date := func(value time.Time) policy.Date { return policy.Date(value.UTC().Format("2006-01-02")) }
	windowStart, windowEnd := date(instant.AddDate(0, 0, -90)), date(instant.AddDate(0, 0, -1))
	started := date(instant.AddDate(0, -9, 0))
	onboarding, active, suspended := true, true, false
	accountStatus := "ACTIVE"
	due := date(instant.AddDate(0, 0, 20))
	balances := make([]policy.Balance, 0, 90)
	for day := instant.AddDate(0, 0, -90); day.Before(time.Date(instant.Year(), instant.Month(), instant.Day(), 0, 0, 0, 0, time.UTC)); day = day.AddDate(0, 0, 1) {
		balances = append(balances, policy.Balance{Date: date(day), BalanceCents: 5_000_000})
	}
	refs := []policy.SourceReference{ref,
		{System: "synthetic-invoicing-fixture", RecordID: "invoice-1", Version: "1", EffectiveAt: instant},
		{System: "synthetic-classification-fixture", RecordID: "classification-1", Version: "1", EffectiveAt: instant}}
	input := policy.Input{
		EvaluatedAt: instant, Mode: mode, FacilityID: "synthetic-facility",
		TermDays: 30,
		Shortfall: policy.Shortfall{ID: "synthetic-shortfall", CustomerID: "synthetic-customer",
			AccountID: "synthetic-account", CardID: "synthetic-card", Outcome: "SHORTFALL_DETECTED",
			Currency: "USD", AttemptedAmountCents: 900_000, AvailableBalanceCents: 200_000,
			ShortfallCents: 700_000, CanonicalEventID: "synthetic-event", TransactionID: "synthetic-transaction",
			BalanceObservedAt: instant.Add(-5 * time.Second)},
		Customer: policy.CustomerSnapshot{ID: "synthetic-customer-snapshot", Version: 1,
			Mode: mode, CapturedAt: instant, CustomerID: "synthetic-customer", AccountID: "synthetic-account",
			CardID: "synthetic-card", SourceRefs: refs,
			Coverage: policy.Coverage{Standing: true, BalanceHistory: true, InflowHistory: true,
				BridgeHistory: true, LifetimeDefaults: true, Reservations: true, Receivables: true, Classification: true},
			Standing: &policy.Standing{RelationshipStartedOn: &started, OnboardingComplete: &onboarding,
				Active: &active, Suspended: &suspended, ProviderAccountStatus: &accountStatus},
			HistoryWindow:  policy.HistoryWindow{WindowStart: windowStart, WindowEnd: windowEnd},
			BalanceHistory: balances,
			InflowHistory: []policy.Inflow{
				{ID: "synthetic-inflow-1", Date: date(instant.AddDate(0, 0, -20)), AmountCents: 3_600_000, Kind: "EXTERNAL_SETTLED", PayerID: "synthetic-payer"},
				{ID: "synthetic-inflow-2", Date: date(instant.AddDate(0, 0, -50)), AmountCents: 3_600_000, Kind: "EXTERNAL_SETTLED", PayerID: "synthetic-payer"},
				{ID: "synthetic-inflow-3", Date: date(instant.AddDate(0, 0, -80)), AmountCents: 3_600_000, Kind: "EXTERNAL_SETTLED", PayerID: "synthetic-payer"}},
			Bridges:      make([]policy.Bridge, 0),
			Reservations: make([]policy.Reservation, 0),
			Receivables: []policy.Receivable{{InvoiceID: "invoice-1", PayerID: "synthetic-payer", Currency: "USD",
				EligibleUnpaidCents: 800_000, DueOn: &due, DueDateVerified: true, AllocationComplete: true, SourceRef: refs[1]}},
			Classifications: []policy.Classification{{TransactionID: "synthetic-transaction",
				MerchantCategory: "suppliers", SourceRef: refs[2]}}},
		Facility: policy.FacilitySnapshot{ID: "synthetic-facility-snapshot", Version: 1,
			Mode: mode, CapturedAt: instant, FacilityID: "synthetic-facility", SourceRefs: []policy.SourceReference{ref},
			Currency: "USD", FacilityTotalCents: 100_000_000, ReserveFloorBPS: 2000,
			FundedPrincipalCents: 8_000_000, ReservedPrincipalCents: 2_000_000,
			LedgerVersion: "synthetic-ledger-v1", LedgerComplete: true},
	}
	return &Synthetic{Evaluator: evaluator, Input: input}, nil
}

// Evaluate invokes the committed public policy with this synthetic observation.
func (s *Synthetic) Evaluate() (policy.Result, error) {
	if s == nil || s.Evaluator == nil {
		return policy.Result{}, errSyntheticUnavailable
	}
	return s.Evaluator.Evaluate(s.Input)
}
