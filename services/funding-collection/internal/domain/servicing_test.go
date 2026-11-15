package domain_test

import (
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	upstreamfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

func TestTermsAndGuards(t *testing.T) {
	now := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	synthetic, err := upstreamfixture.New(now, "DEMO")
	if err != nil {
		t.Fatalf("load pinned policy fixture: %v", err)
	}
	pricing := synthetic.Evaluator.Pricing()
	fee, total, err := pricing.TotalDue(700_000, 30)
	if err != nil || fee != 19_000 || total != 719_000 {
		t.Fatalf("30-day $7,000 quote = fee %d total %d err %v, want 19000 and 719000 cents", fee, total, err)
	}
	terms := domain.Terms{
		TermsVersion: 1, SourceMode: "DEMO", FacilityID: "facility-1",
		FundingAccountRef: "funding-account", FeeBeneficiaryRef: "fee-beneficiary",
		CustomerID: "customer-1", BorrowerAccountRef: "borrower-account", CardRef: "card-1",
		TransactionID: "transaction-1", OfferID: "offer-1", OfferRevision: 1,
		PrincipalCents: int64(700_000), ContractFeeCents: int64(fee), TermDays: 30,
		OfferedOn: "2026-11-14", DueOn: "2026-12-14", ExpiresAt: "2026-11-14T13:00:00Z",
		PolicyID: synthetic.Evaluator.PolicyID(), PolicyHash: synthetic.Evaluator.PolicyHash(),
		DisclosuresVersion: "disclosure-v1", WaterfallVersion: 1, LateFeeConvention: "OPENING_PRINCIPAL_UTC_DAY",
		CollectionAuthorityID: "authority-1", CollectionAuthorityVersion: 1,
		MappingFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	hash30, err := terms.Hash()
	if err != nil {
		t.Fatalf("Terms.Hash(30 days) error = %v", err)
	}
	fee60, total60, err := pricing.TotalDue(700_000, 60)
	if err != nil || fee60 != 33_000 || total60 != 733_000 {
		t.Fatalf("60-day $7,000 quote = fee %d total %d err %v, want 33000 and 733000 cents", fee60, total60, err)
	}
	terms.TermDays, terms.ContractFeeCents, terms.DueOn = 60, int64(fee60), "2027-01-13"
	hash60, err := terms.Hash()
	if err != nil || hash60 == hash30 {
		t.Fatalf("Terms.Hash(60 days) = %q err %v, want a valid term-bound hash distinct from 30 days", hash60, err)
	}

	eligible, err := synthetic.Evaluate()
	if err != nil || eligible.PolicyID != synthetic.Evaluator.PolicyID() ||
		eligible.PolicyHash != synthetic.Evaluator.PolicyHash() || eligible.Status != policy.DecisionEligible ||
		eligible.EstimatedFeeCents == nil || *eligible.EstimatedFeeCents != int64(fee) ||
		eligible.TotalDueCents == nil || *eligible.TotalDueCents != int64(total) ||
		eligible.FundingAuthorized || !eligible.RequiresRecheck {
		t.Fatalf("pinned shared evaluator result = status %q fee %v total %v authority %t recheck %t err %v",
			eligible.Status, eligible.EstimatedFeeCents, eligible.TotalDueCents,
			eligible.FundingAuthorized, eligible.RequiresRecheck, err)
	}

	if eligible.CustomerLimitCents == nil || *eligible.CustomerLimitCents != 720_000 {
		t.Fatalf("eligible.CustomerLimitCents = %v, want the synthetic Tier B limit of 720000", eligible.CustomerLimitCents)
	}

	// Tier B's line is $7100: enough for principal but short of the full
	// principal-plus-fee obligation, so the evaluator must reject the whole need.
	for i := range synthetic.Input.Customer.InflowHistory {
		synthetic.Input.Customer.InflowHistory[i].AmountCents = 3_550_000
	}
	customerLimited, err := synthetic.Evaluate()
	capacityFailed := false
	for _, rule := range customerLimited.Rules {
		if rule.RuleID == policy.RuleCustomerCapacity && rule.Status == policy.RuleFail {
			capacityFailed = true
		}
	}
	if err != nil || customerLimited.Status != policy.DecisionIneligible || !capacityFailed ||
		customerLimited.CustomerLimitCents == nil || *customerLimited.CustomerLimitCents != 710_000 ||
		customerLimited.PrincipalCents == nil || *customerLimited.PrincipalCents != 700_000 ||
		customerLimited.TotalDueCents == nil || *customerLimited.TotalDueCents != 719_000 ||
		customerLimited.FundingAuthorized || !customerLimited.RequiresRecheck {
		t.Fatalf("fee-inclusive customer capacity result = %+v err %v, want INELIGIBLE at $7100 line", customerLimited, err)
	}
	for i := range synthetic.Input.Customer.InflowHistory {
		synthetic.Input.Customer.InflowHistory[i].AmountCents = 3_600_000
	}

	// Tight capital requires human approval even when all substantive rules pass.
	synthetic.Input.Facility.ReserveFloorBPS = 8000
	synthetic.Input.Facility.FundedPrincipalCents = 0
	synthetic.Input.Facility.ReservedPrincipalCents = 0
	tight, err := synthetic.Evaluate()
	if err != nil || tight.Status != policy.DecisionReviewRequired || !tight.ApprovalRequired ||
		tight.Regime == nil || *tight.Regime != policy.RegimeTight || tight.FundingAuthorized || !tight.RequiresRecheck {
		t.Fatalf("tight-capital evaluator result = %+v err %v, want approval-gated REVIEW_REQUIRED", tight, err)
	}

	// At the approved reserve floor, available principal is zero and the
	// public evaluator reports the capital rule as a hard refusal.
	synthetic.Input.Facility.ReserveFloorBPS = 2000
	synthetic.Input.Facility.FundedPrincipalCents = 80_000_000
	noCapital, err := synthetic.Evaluate()
	capitalFailed := false
	for _, rule := range noCapital.Rules {
		if rule.RuleID == policy.RuleCapitalAvailable && rule.Status == policy.RuleFail {
			capitalFailed = true
		}
	}
	if err != nil || noCapital.Status != policy.DecisionIneligible || !capitalFailed ||
		noCapital.FundingAuthorized || !noCapital.RequiresRecheck {
		t.Fatalf("at-floor evaluator result = status %q rules %+v authority %t recheck %t err %v",
			noCapital.Status, noCapital.Rules, noCapital.FundingAuthorized, noCapital.RequiresRecheck, err)
	}

	// Fixture arithmetic follows cumulative opening-principal numerators. The
	// persisted UTC-day history and catch-up path are exercised by
	// TestCollectionReversalAndAccrual in the acceptance package.
	openingPrincipal := []domain.Money{700_000, 350_000, 350_000}
	var numerator int64
	var assessed domain.Money
	wantDelta := []domain.Money{467, 233, 233}
	for day, opening := range openingPrincipal {
		numerator += int64(opening) * pricing.MonthlyBasisPoints
		cumulative, calcErr := domain.MultiplyDivideHalfUp(domain.Money(numerator), 1, 10_000*pricing.DaysPerMonth)
		if calcErr != nil {
			t.Fatalf("fixture daily accrual day %d: %v", day+1, calcErr)
		}
		delta := cumulative - assessed
		if delta != wantDelta[day] {
			t.Fatalf("fixture daily accrual day %d = %d cents, want %d", day+1, delta, wantDelta[day])
		}
		assessed = cumulative
	}
}
