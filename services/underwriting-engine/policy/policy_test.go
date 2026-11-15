package policy

import (
	"testing"
	"time"
)

func TestPolicyDecisions(t *testing.T) {
	config, err := LoadConfig()
	if err != nil {
		t.Fatal("LoadConfig() error =", err)
	}
	wantPricing := Pricing{
		BaseFeeCents: 5000, MonthlyRateBasisPoints: 200, DaysPerMonth: 30,
		MinimumTermDays: 7, MaximumTermDays: 90,
	}
	pricing := config.Pricing()
	if pricing != wantPricing {
		t.Fatalf("Config.Pricing() = %+v, want %+v", pricing, wantPricing)
	}
	pricing.BaseFeeCents = 0
	if got := config.Pricing(); got != wantPricing {
		t.Fatalf("Config.Pricing() after caller mutation = %+v, want value copy %+v", got, wantPricing)
	}
	base := testPolicyInput(t)

	eligible := Evaluate(base, config)
	if eligible.Status != DecisionEligible || eligible.Tier == nil || *eligible.Tier != TierB {
		t.Fatalf("Evaluate(eligible) = %s tier %v, want ELIGIBLE tier B", eligible.Status, eligible.Tier)
	}
	if eligible.EstimatedFeeCents == nil || *eligible.EstimatedFeeCents != 19_000 ||
		eligible.ExistingExposureCents == nil || *eligible.ExistingExposureCents != 1_200_000 ||
		eligible.PostFundingExposureCents == nil || *eligible.PostFundingExposureCents != 1_919_000 {
		t.Fatalf("Evaluate(eligible) money = fee %v exposure %v post %v, want 19000, 1200000, 1919000",
			eligible.EstimatedFeeCents, eligible.ExistingExposureCents, eligible.PostFundingExposureCents)
	}

	for _, test := range []struct {
		name     string
		months   int
		wantTier Tier
	}{{name: "six-month boundary", months: 6, wantTier: TierB},
		{name: "twelve-month boundary", months: 12, wantTier: TierA}} {
		input := testPolicyInput(t)
		started := policyDate(input.EvaluatedAt.AddDate(0, -test.months, 0))
		input.Customer.Standing.RelationshipStartedOn = &started
		result := Evaluate(input, config)
		if result.Tier == nil || *result.Tier != test.wantTier {
			t.Errorf("Evaluate(%s) tier = %v, want %s", test.name, result.Tier, test.wantTier)
		}
	}

	for _, test := range []struct {
		name     string
		days     int
		wantTier Tier
	}{{name: "late exactly 180 days ago", days: 180, wantTier: TierB},
		{name: "late 181 days ago", days: 181, wantTier: TierA}} {
		input := testPolicyInput(t)
		opened := policyDate(input.EvaluatedAt.AddDate(0, 0, -test.days-30))
		due := policyDate(input.EvaluatedAt.AddDate(0, 0, -test.days-15))
		closed := policyDate(input.EvaluatedAt.AddDate(0, 0, -test.days))
		started := policyDate(input.EvaluatedAt.AddDate(0, -12, 0))
		input.Customer.Standing.RelationshipStartedOn = &started
		late := "LATE"
		input.Customer.Bridges = append(input.Customer.Bridges, Bridge{
			ID: "late-bridge", TransactionID: "late-transaction",
			OriginalPrincipalCents: 500_000, Status: "REPAID", OpenedOn: opened,
			DueOn: due, ClosedOn: &closed, RepaymentOutcome: &late,
		})
		result := Evaluate(input, config)
		if result.Tier == nil || *result.Tier != test.wantTier {
			t.Errorf("Evaluate(%s) tier = %v, want %s", test.name, result.Tier, test.wantTier)
		}
	}

	capacity := testPolicyInput(t)
	capacity.Customer.Reservations[0].PrincipalCents = 300_000
	capacityResult := Evaluate(capacity, config)
	if capacityResult.Status != DecisionIneligible ||
		ruleStatus(capacityResult, RuleCustomerCapacity) != RuleFail ||
		capacityResult.PostFundingExposureCents == nil || *capacityResult.PostFundingExposureCents != 2_119_000 {
		t.Fatalf("Evaluate(capacity) = %s capacity %s post %v, want INELIGIBLE FAIL 2119000",
			capacityResult.Status, ruleStatus(capacityResult, RuleCustomerCapacity),
			capacityResult.PostFundingExposureCents)
	}

	missingDate := testPolicyInput(t)
	missingDate.Customer.Receivables[0].DueOn = nil
	missingDate.Customer.Receivables[0].DueDateVerified = false
	missingDateResult := Evaluate(missingDate, config)
	if missingDateResult.Status != DecisionReviewRequired ||
		ruleStatus(missingDateResult, RuleReceivableCoverage) != RuleUnknown {
		t.Fatalf("Evaluate(missing date) = %s coverage %s, want REVIEW_REQUIRED UNKNOWN",
			missingDateResult.Status, ruleStatus(missingDateResult, RuleReceivableCoverage))
	}

	missingFacility := testPolicyInput(t)
	missingFacility.Facility = FacilitySnapshot{}
	missingFacility.Customer.Standing.Suspended = boolPointer(true)
	missingFacilityResult := Evaluate(missingFacility, config)
	if missingFacilityResult.Status != DecisionIneligible ||
		ruleStatus(missingFacilityResult, RuleCustomerActive) != RuleFail {
		t.Fatalf("Evaluate(suspended without facility) = %s active %s, want INELIGIBLE FAIL",
			missingFacilityResult.Status, ruleStatus(missingFacilityResult, RuleCustomerActive))
	}
	staleSuspended := testPolicyInput(t)
	staleSuspended.Customer.CapturedAt = staleSuspended.EvaluatedAt.Add(-24*time.Hour - time.Second)
	staleSuspended.Customer.Standing.Suspended = boolPointer(true)
	staleSuspendedResult := Evaluate(staleSuspended, config)
	if staleSuspendedResult.Status != DecisionReviewRequired ||
		ruleStatus(staleSuspendedResult, RuleInputFresh) != RuleUnknown {
		t.Fatalf("Evaluate(stale suspended input) = %s freshness %s, want REVIEW_REQUIRED UNKNOWN",
			staleSuspendedResult.Status, ruleStatus(staleSuspendedResult, RuleInputFresh))
	}

	missingCustomerAtFloor := testPolicyInput(t)
	missingCustomerAtFloor.Customer = CustomerSnapshot{}
	missingCustomerAtFloor.Facility.FundedPrincipalCents = 78_000_000
	missingCustomerAtFloorResult := Evaluate(missingCustomerAtFloor, config)
	if missingCustomerAtFloorResult.Status != DecisionIneligible ||
		ruleStatus(missingCustomerAtFloorResult, RuleCapitalAvailable) != RuleFail {
		t.Fatalf("Evaluate(missing customer at floor) = %s capital %s, want INELIGIBLE FAIL",
			missingCustomerAtFloorResult.Status,
			ruleStatus(missingCustomerAtFloorResult, RuleCapitalAvailable))
	}

	settledRecentDefault := testPolicyInput(t)
	opened := policyDate(base.EvaluatedAt.AddDate(0, 0, -400))
	due := policyDate(base.EvaluatedAt.AddDate(0, 0, -350))
	defaulted := policyDate(base.EvaluatedAt.AddDate(0, 0, -300))
	closed := policyDate(base.EvaluatedAt.AddDate(0, 0, -10))
	outcome := "LATE"
	settledRecentDefault.Customer.Bridges = append(settledRecentDefault.Customer.Bridges, Bridge{
		ID: "settled-default", TransactionID: "old-default-transaction",
		OriginalPrincipalCents: 500_000, Status: "REPAID", OpenedOn: opened, DueOn: due,
		ClosedOn: &closed, DefaultedOn: &defaulted, RepaymentOutcome: &outcome,
	})
	defaultResult := Evaluate(settledRecentDefault, config)
	if defaultResult.Status != DecisionIneligible || defaultResult.Tier != nil ||
		ruleStatus(defaultResult, RuleNoOverdueOrDefault) != RuleFail {
		t.Fatalf("Evaluate(settled default at 300 days) = %s tier %v rule %s, want INELIGIBLE no tier FAIL",
			defaultResult.Status, defaultResult.Tier,
			ruleStatus(defaultResult, RuleNoOverdueOrDefault))
	}

	duplicateInflow := testPolicyInput(t)
	duplicateInflow.Customer.InflowHistory = append(duplicateInflow.Customer.InflowHistory,
		duplicateInflow.Customer.InflowHistory[0])
	duplicateInflowResult := Evaluate(duplicateInflow, config)
	if duplicateInflowResult.Status != DecisionReviewRequired ||
		ruleStatus(duplicateInflowResult, RuleCustomerCapacity) != RuleUnknown {
		t.Fatalf("Evaluate(duplicate inflow) = %s capacity %s, want REVIEW_REQUIRED UNKNOWN",
			duplicateInflowResult.Status, ruleStatus(duplicateInflowResult, RuleCustomerCapacity))
	}

	duplicateInvoice := testPolicyInput(t)
	duplicateInvoice.Customer.Receivables = append(duplicateInvoice.Customer.Receivables,
		duplicateInvoice.Customer.Receivables[0])
	duplicateInvoiceResult := Evaluate(duplicateInvoice, config)
	if duplicateInvoiceResult.Status != DecisionReviewRequired ||
		ruleStatus(duplicateInvoiceResult, RuleReceivableCoverage) != RuleUnknown {
		t.Fatalf("Evaluate(duplicate invoice) = %s coverage %s, want REVIEW_REQUIRED UNKNOWN",
			duplicateInvoiceResult.Status, ruleStatus(duplicateInvoiceResult, RuleReceivableCoverage))
	}
	unreferencedInvoice := testPolicyInput(t)
	unreferencedInvoice.Customer.Receivables[0].SourceRef.RecordID = "unmapped-version"
	unreferencedInvoiceResult := Evaluate(unreferencedInvoice, config)
	if ruleStatus(unreferencedInvoiceResult, RuleReceivableCoverage) != RuleUnknown {
		t.Fatalf("Evaluate(unreferenced invoice) coverage = %s, want UNKNOWN",
			ruleStatus(unreferencedInvoiceResult, RuleReceivableCoverage))
	}
	unreferencedClassification := testPolicyInput(t)
	unreferencedClassification.Facility.FundedPrincipalCents = 70_000_000
	unreferencedClassification.Facility.ReservedPrincipalCents = 0
	unreferencedClassification.Customer.Classifications[0].SourceRef.RecordID = "unmapped-version"
	unreferencedClassificationResult := Evaluate(unreferencedClassification, config)
	if ruleStatus(unreferencedClassificationResult, RuleSpendAllowed) != RuleUnknown {
		t.Fatalf("Evaluate(unreferenced classification) spend = %s, want UNKNOWN",
			ruleStatus(unreferencedClassificationResult, RuleSpendAllowed))
	}

	conflictingClassification := testPolicyInput(t)
	conflictingClassification.Facility.FundedPrincipalCents = 70_000_000
	conflictingClassification.Facility.ReservedPrincipalCents = 0
	conflictingClassification.Customer.Classifications = append(
		conflictingClassification.Customer.Classifications, Classification{
			TransactionID: base.Shortfall.TransactionID, MerchantCategory: "travel",
			SourceRef: base.Customer.SourceRefs[2],
		})
	classificationResult := Evaluate(conflictingClassification, config)
	if ruleStatus(classificationResult, RuleSpendAllowed) != RuleUnknown {
		t.Fatalf("Evaluate(conflicting classification) spend = %s, want UNKNOWN",
			ruleStatus(classificationResult, RuleSpendAllowed))
	}

	incompleteStanding := testPolicyInput(t)
	incompleteStanding.Customer.Coverage.Standing = false
	incompleteStanding.Customer.Standing.Active = boolPointer(false)
	incompleteStanding.Customer.Standing.Suspended = boolPointer(false)
	incompleteStandingResult := Evaluate(incompleteStanding, config)
	if ruleStatus(incompleteStandingResult, RuleCustomerActive) != RuleUnknown {
		t.Fatalf("Evaluate(incomplete standing) active = %s, want UNKNOWN",
			ruleStatus(incompleteStandingResult, RuleCustomerActive))
	}

	convertedReservation := testPolicyInput(t)
	bridgeID := convertedReservation.Customer.Bridges[0].ID
	convertedReservation.Customer.Reservations[0] = Reservation{
		ID: "converted-reservation", TransactionID: convertedReservation.Customer.Bridges[0].TransactionID,
		PrincipalCents: 1_000_000, FeeCents: 100_000, Status: "CONVERTED", BridgeID: &bridgeID,
	}
	convertedResult := Evaluate(convertedReservation, config)
	if convertedResult.ExistingExposureCents == nil || *convertedResult.ExistingExposureCents != 1_100_000 {
		t.Fatalf("Evaluate(converted reservation) exposure = %v, want 1100000",
			convertedResult.ExistingExposureCents)
	}

	allZero := testPolicyInput(t)
	for i := range allZero.Customer.BalanceHistory {
		allZero.Customer.BalanceHistory[i].BalanceCents = 0
	}
	allZeroResult := Evaluate(allZero, config)
	if allZeroResult.Tier == nil || *allZeroResult.Tier == TierA {
		t.Fatalf("Evaluate(all-zero history) tier = %v, want a non-A tier", allZeroResult.Tier)
	}

	for _, days := range []int{7, 90} {
		term := base
		term.TermDays = days
		result := Evaluate(term, config)
		if result.EstimatedFeeCents == nil {
			t.Fatalf("Evaluate(term %d) fee is unknown", days)
		}
	}

	for _, test := range []struct {
		name       string
		available  int64
		wantRegime CapitalRegime
	}{{name: "tight boundary", available: 30_000_000, wantRegime: RegimeTight},
		{name: "critical boundary", available: 10_000_000, wantRegime: RegimeCritical}} {
		input := base
		input.Facility.ReserveFloorBPS = 0
		input.Facility.ReservedPrincipalCents = 0
		input.Facility.FundedPrincipalCents = input.Facility.FacilityTotalCents - test.available
		result := Evaluate(input, config)
		if result.Regime == nil || *result.Regime != test.wantRegime {
			t.Errorf("Evaluate(%s) regime = %v, want %s", test.name, result.Regime, test.wantRegime)
		}
		if !result.ApprovalRequired {
			t.Errorf("Evaluate(%s) approval_required = false, want true", test.name)
		}
	}
}

func testPolicyInput(t *testing.T) Input {
	t.Helper()
	now := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	date := func(value time.Time) Date { return Date(value.UTC().Format("2006-01-02")) }
	start := date(now.AddDate(0, 0, -90))
	end := date(now.AddDate(0, 0, -1))
	refs := []SourceReference{
		{System: "fixture", RecordID: "customer", Version: "v1", EffectiveAt: now},
		{System: "accounting", RecordID: "invoice", Version: "v1", EffectiveAt: now},
		{System: "fixture", RecordID: "classification", Version: "v1", EffectiveAt: now},
	}
	started := date(now.AddDate(0, -9, 0))
	onboarding, active, suspended := true, true, false
	status := "ACTIVE"
	due := date(now.AddDate(0, 0, 20))
	balances := make([]Balance, 0, 90)
	for day := now.AddDate(0, 0, -90); day.Before(now.Truncate(24 * time.Hour)); day = day.AddDate(0, 0, 1) {
		balances = append(balances, Balance{Date: date(day), BalanceCents: 5_000_000})
	}
	if len(balances) != 90 {
		t.Fatalf("test fixture has %d balance days, want 90", len(balances))
	}
	inflows := []Inflow{
		{ID: "inflow-1", Date: date(now.AddDate(0, 0, -20)), AmountCents: 10_000_000, Kind: "EXTERNAL_SETTLED", PayerID: "payer-1"},
		{ID: "inflow-2", Date: date(now.AddDate(0, 0, -50)), AmountCents: 10_000_000, Kind: "EXTERNAL_SETTLED", PayerID: "payer-1"},
		{ID: "inflow-3", Date: date(now.AddDate(0, 0, -80)), AmountCents: 10_000_000, Kind: "EXTERNAL_SETTLED", PayerID: "payer-1"},
	}
	return Input{
		EvaluatedAt: now, Mode: "DEMO", FacilityID: "facility-1", TermDays: 30,
		Shortfall: Shortfall{ID: "11111111-1111-4111-8111-111111111111",
			CustomerID: "customer-1", AccountID: "account-1", CardID: "card-1",
			Outcome: "SHORTFALL_DETECTED", Currency: "USD", AttemptedAmountCents: 900_000,
			AvailableBalanceCents: 200_000, ShortfallCents: 700_000,
			CanonicalEventID: "event-1", TransactionID: "transaction-new",
			BalanceObservedAt: now.Add(-5 * time.Second)},
		Customer: CustomerSnapshot{
			ID: "snapshot-customer", Version: 1, Mode: "DEMO", CapturedAt: now,
			CustomerID: "customer-1", AccountID: "account-1", CardID: "card-1", SourceRefs: refs,
			Coverage: Coverage{Standing: true, BalanceHistory: true, InflowHistory: true,
				BridgeHistory: true, LifetimeDefaults: true, Reservations: true,
				Receivables: true, Classification: true},
			Standing: &Standing{RelationshipStartedOn: &started, OnboardingComplete: &onboarding,
				Active: &active, Suspended: &suspended, ProviderAccountStatus: &status},
			HistoryWindow:  HistoryWindow{WindowStart: start, WindowEnd: end},
			BalanceHistory: balances, InflowHistory: inflows,
			Bridges: []Bridge{{ID: "bridge-1", TransactionID: "old-bridge-transaction",
				OriginalPrincipalCents: 1_000_000, PrincipalOwedCents: 1_000_000,
				FeesOwedCents: 100_000, Status: "OPEN", OpenedOn: date(now.AddDate(0, 0, -60)),
				DueOn: date(now.AddDate(0, 0, 15))}},
			Reservations: []Reservation{{ID: "reservation-1", TransactionID: "reservation-transaction",
				PrincipalCents: 100_000, Status: "ACTIVE"}},
			Receivables: []Receivable{{InvoiceID: "invoice-1", PayerID: "payer-1", Currency: "USD",
				EligibleUnpaidCents: 800_000, DueOn: &due, DueDateVerified: true,
				AllocationComplete: true, SourceRef: refs[1]}},
			Classifications: []Classification{{TransactionID: "transaction-new",
				MerchantCategory: "suppliers", SourceRef: refs[2]}},
		},
		Facility: FacilitySnapshot{ID: "snapshot-facility", Version: 1, Mode: "DEMO",
			CapturedAt: now, FacilityID: "facility-1", SourceRefs: []SourceReference{
				{System: "ledger", RecordID: "facility", Version: "v1", EffectiveAt: now},
			}, Currency: "USD", FacilityTotalCents: 100_000_000, ReserveFloorBPS: 2000,
			FundedPrincipalCents: 8_000_000, ReservedPrincipalCents: 2_000_000,
			LedgerVersion: "v1", LedgerComplete: true},
	}
}

func boolPointer(value bool) *bool { return &value }

func policyDate(value time.Time) Date { return Date(value.UTC().Format("2006-01-02")) }

func ruleStatus(result Result, id RuleID) RuleStatus {
	for _, rule := range result.Rules {
		if rule.RuleID == id {
			return rule.Status
		}
	}
	return ""
}
