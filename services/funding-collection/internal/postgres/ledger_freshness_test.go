package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	upstreamfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

func TestLockedLedgerReadIsFreshWithoutRefreshingExternalCustomerFacts(t *testing.T) {
	now := time.Now().UTC()
	initialImport := now.Add(-2 * time.Hour)
	synthetic, err := upstreamfixture.New(now, "SANDBOX")
	if err != nil {
		t.Fatal(err)
	}
	customer := synthetic.Input.Customer
	refs, _ := json.Marshal([]policy.SourceReference{{System: "allocation-import", RecordID: "facility", Version: "1", EffectiveAt: initialImport}})
	facility := fundingFacility{sourceID: "initial-allocation", sourceVersion: "1", sourceRefs: refs, capturedAt: &initialImport, total: 100000000, reserveBPS: 2000, configurationVersion: 1, version: 1}
	offer := fundingOffer{customerID: customer.CustomerID, terms: domain.Terms{CardRef: customer.CardID, TermDays: 30}}
	shortfall := synthetic.Input.Shortfall
	request := FundingGuard{Mode: "SANDBOX", FacilityID: synthetic.Input.FacilityID, CustomerSnapshot: customer, SavedShortfall: upstream.ShortfallObservation{ID: shortfall.ID, Outcome: "SHORTFALL_DETECTED"}, Payment: provider.PaymentObservation{CustomerID: shortfall.CustomerID, CanonicalAccountID: shortfall.AccountID, ParentTransactionID: shortfall.TransactionID, AuthorizationEventID: shortfall.CanonicalEventID, AttemptedAmountCents: 900000, ShortfallCents: 700000, Currency: "USD"}, BorrowerBalance: provider.BalanceObservation{AvailableCents: 200000, ObservedAt: now}}
	input, err := buildFundingPolicyInput(request, offer, facility, fundingCustomer{historyComplete: true, version: 1}, nil, nil, customer.Receivables, now, true, nil, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := synthetic.Evaluator.Evaluate(input)
	if err != nil || result.Status != policy.DecisionEligible {
		t.Fatalf("fresh locked ledger was treated as stale allocation: %v", err)
	}
	if !input.Facility.CapturedAt.Equal(now) || !facility.capturedAt.Equal(initialImport) {
		t.Fatal("ledger read time or original allocation provenance was overwritten")
	}
	request.CustomerSnapshot.CapturedAt = now.Add(-25 * time.Hour)
	input, err = buildFundingPolicyInput(request, offer, facility, fundingCustomer{historyComplete: true, version: 1}, nil, nil, customer.Receivables, now, true, nil, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err = synthetic.Evaluator.Evaluate(input)
	if err != nil || result.Status != policy.DecisionReviewRequired {
		t.Fatal("stale external customer facts were refreshed by the ledger read")
	}
}
