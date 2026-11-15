package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	upstreamfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
)

func TestDispatchOfferRejectsNextUTCDay(t *testing.T) {
	before := time.Date(2026, time.November, 14, 23, 59, 59, 0, time.UTC)
	fixture, err := upstreamfixture.New(before, "DEMO")
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.Evaluate()
	if err != nil || string(result.Status) != "ELIGIBLE" {
		t.Fatalf("fixture evaluation = %s err %v, want real shared-policy ELIGIBLE result", result.Status, err)
	}
	payment := fixture.Input.Shortfall
	assessment := upstream.SavedEvaluation{ID: "synthetic-evaluation", ShortfallID: payment.ID,
		CustomerID: payment.CustomerID, AccountID: payment.AccountID, TransactionID: payment.TransactionID,
		SourceMode: "DEMO", PolicyID: result.PolicyID, PolicyHash: result.PolicyHash, Status: string(result.Status),
		ApprovalRequired: &result.ApprovalRequired, FundingAuthorized: &result.FundingAuthorized,
		RequiresRecheck: &result.RequiresRecheck, PrincipalCents: result.PrincipalCents,
		TermDays: &result.TermDays, EstimatedFeeCents: result.EstimatedFeeCents, TotalDueCents: result.TotalDueCents}
	for _, rule := range result.Rules {
		assessment.Rules = append(assessment.Rules, upstream.RuleTrace{RuleID: string(rule.RuleID), Status: string(rule.Status)})
	}
	terms := domain.Terms{TermsVersion: 1, SourceMode: "DEMO", FacilityID: fixture.Input.FacilityID,
		FundingAccountRef: "synthetic-funding", FeeBeneficiaryRef: "synthetic-funding",
		CustomerID: payment.CustomerID, BorrowerAccountRef: payment.AccountID, CardRef: payment.CardID,
		TransactionID: payment.TransactionID, OfferID: "synthetic-offer", OfferRevision: 1,
		PrincipalCents: 700_000, ContractFeeCents: 19_000, TermDays: 30,
		OfferedOn: "2026-11-14", DueOn: "2026-12-14", ExpiresAt: before.Add(10 * time.Minute).Format(time.RFC3339),
		PolicyID: result.PolicyID, PolicyHash: result.PolicyHash, DisclosuresVersion: "disclosure-v1",
		WaterfallVersion: 1, LateFeeConvention: "OPENING_PRINCIPAL_UTC_DAY",
		CollectionAuthorityID: "authority-1", CollectionAuthorityVersion: 1,
		MappingFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	hash, err := terms.Hash()
	if err != nil {
		t.Fatal(err)
	}
	shortfall := upstream.ShortfallObservation{ID: payment.ID, CustomerID: payment.CustomerID,
		AccountID: payment.AccountID, TransactionID: &payment.TransactionID,
		CanonicalEventID: payment.CanonicalEventID, ShortfallCents: &payment.ShortfallCents}
	evidence, err := json.Marshal(map[string]any{"evaluation": assessment, "shortfall": shortfall})
	if err != nil {
		t.Fatal(err)
	}
	offer := fundingOffer{id: terms.OfferID, revision: 1, state: "CONSUMED", terms: terms, termsHash: hash,
		facilityID: terms.FacilityID, customerID: terms.CustomerID, accountID: terms.BorrowerAccountRef,
		transactionID: terms.TransactionID, authorizationID: payment.CanonicalEventID,
		principal: 700_000, contractFee: 19_000, policyID: result.PolicyID, policyHash: result.PolicyHash,
		evaluationID: assessment.ID, candidateEvidence: evidence, offeredOn: utcDate(before), expiresAt: before.Add(10 * time.Minute)}
	guard := FundingGuard{Mode: "DEMO", FacilityID: terms.FacilityID, Evaluator: fixture.Evaluator, Assessment: assessment}
	facility := fundingFacility{fundingAccount: terms.FundingAccountRef}
	if code := validateDispatchOffer(guard, offer, facility, before); code != "" {
		t.Fatalf("validateDispatchOffer(before midnight) = %q, want eligible original binding", code)
	}
	// The ten-minute TTL is still live, so the UTC date change itself must deny
	// this exact offer. No wall clock, source timestamp or database is modified.
	after := before.Add(time.Second)
	if code := validateDispatchOffer(guard, offer, facility, after); code != "OFFER_EXPIRED" {
		t.Fatalf("validateDispatchOffer(after midnight) = %q, want OFFER_EXPIRED", code)
	}
}
