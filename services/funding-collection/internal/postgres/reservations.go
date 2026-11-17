package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

// FundingGuard contains provider and upstream reads completed before the SQL transaction.
type FundingGuard struct {
	ActionKind                 string
	ActionID                   string
	ActionVersion              int64
	LastFundingCheckID         string
	PrePolicyBlockCode         string
	Mode                       string
	FacilityID                 string
	OfferID                    string
	ExpectedVersion            int64
	Principal                  string
	HTTPMethod                 string
	ResourcePath               string
	IdempotencyKey             string
	RequestHash                string
	Clock                      func() time.Time
	SavedShortfall             upstream.ShortfallObservation
	Assessment                 upstream.SavedEvaluation
	Payment                    provider.PaymentObservation
	BorrowerBalance            provider.BalanceObservation
	FacilityBalance            provider.BalanceObservation
	KnownSourceFeeCents        *int64
	SourceFeeContractID        string
	InitialSourceFeeCents      int64
	InitialSourceFeeContractID string
	MockFeeContract            bool
	ExcludeOperationID         string
	ExcludeReservationID       string
	ExcludeBridgeID            string
	ExcludeTransactionID       string
	OtherSourceLiabilityCents  int64
	CustomerContext            upstream.CustomerContext
	CustomerSnapshot           policy.CustomerSnapshot
	Evaluator                  *underwriting.Evaluator
	Pricing                    domain.Pricing
	PricingReady               bool
}

// FundingOutcome is a persisted command response. A 202 always means RESERVED, never settled.
type FundingOutcome struct {
	Status int
	Body   []byte
}

// SourceLiquidityProjection contains only a current provider read and locked local debit holds.
type SourceLiquidityProjection struct {
	Mode                 string
	SourceAccountRef     string
	AvailableCents       int64
	CandidateAmountCents int64
	CandidateFeeCents    *int64
	MockFeeContract      bool
	ExcludeOperationID   string
	ExcludeReservationID string
	ExcludeBridgeID      string
	ExcludeTransactionID string
}

var (
	errFundingSourceFeeUnknown           = errors.New("funding_source_fee_unknown")
	errFundingSourceInsufficient         = errors.New("funding_source_balance_insufficient")
	errFundingFreshEvidenceStale         = errors.New("funding_fresh_evidence_stale")
	errFundingFacilityEvidenceIncomplete = errors.New("funding_facility_evidence_incomplete")
)

type fundingOffer struct {
	id, facilityID, customerID, accountID, transactionID, authorizationID string
	policyID, policyHash, termsHash, state, evaluationID, caseID          string
	termsJSON, sourceRefs                                                 []byte
	candidateEvidence                                                     []byte
	principal, contractFee                                                int64
	version, revision                                                     int64
	expiresAt                                                             time.Time
	offeredOn, dueOn                                                      time.Time
	terms                                                                 domain.Terms
}

type fundingFacility struct {
	fundingAccount                                            string
	sourceID, sourceVersion                                   string
	sourceRefs                                                []byte
	capturedAt                                                *time.Time
	total, principalOwed, reservedPrincipal, otherCommitments int64
	reserveBPS                                                int64
	configurationVersion, version                             int64
	updatedAt                                                 time.Time
}

type fundingCustomer struct {
	version                                                    int64
	historyComplete, activeHold                                bool
	defaultedAt                                                *time.Time
	principalOwed, unpaidFees, reservedPrincipal, reservedFees int64
}

type fundingInvoice struct {
	sourceSystem, id, revision, payer, currency                            string
	original, unpaid, reserved, allocated                                  int64
	dueOn                                                                  *time.Time
	dueVerified, allocationComplete, disputed, sourceComplete, backingHold bool
	version                                                                int64
	sourceRef                                                              []byte
	lastCapturedAt                                                         *time.Time
}

type fundingBridge struct {
	id, transactionID, lifecycle, evaluationID                                                      string
	everFunded                                                                                      bool
	principalOriginal, principalOwed, contractOwed, lateOwed, principalPaid, contractPaid, latePaid int64
	dueOn, createdAt                                                                                time.Time
	fundedAt, closedAt                                                                              *time.Time
	defaultedOn                                                                                     *time.Time
	delinquencyStage                                                                                string
	lastAccruedOn                                                                                   *time.Time
	assessedLate, paidLate, backingDeficit                                                          int64
	holdReasons                                                                                     []string
}

// ReserveFundingTx rechecks a complete fresh projection while holding the
// common financial lock order, then commits evidence and every reservation row atomically.
func ReserveFundingTx(ctx context.Context, tx pgx.Tx, request FundingGuard) (FundingOutcome, error) {
	if request.ActionKind != "" || request.ActionID != "" || request.ActionVersion != 0 || request.Evaluator == nil || request.Mode != "DEMO" && request.Mode != "SANDBOX" ||
		request.ExpectedVersion < 1 || request.OfferID == "" || request.FacilityID == "" ||
		request.HTTPMethod != "POST" || request.ResourcePath == "" || request.IdempotencyKey == "" ||
		!validFundingHash(request.RequestHash) || request.Clock == nil {
		return FundingOutcome{}, errors.New("funding_guard_input_invalid")
	}
	if !request.PricingReady || request.Pricing.Validate() != nil {
		return FundingOutcome{}, errors.New("funding_pricing_binding_unavailable")
	}
	mode := domain.Mode(request.Mode)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('funding:facility:' || $1 || ':' || $2,0))`, request.Mode, request.FacilityID); err != nil {
		return FundingOutcome{}, errors.New("funding_financial_lock_failed")
	}
	facility, err := lockFundingFacility(ctx, tx, request.Mode, request.FacilityID)
	if err != nil {
		return FundingOutcome{}, err
	}
	customerID, err := fundingOfferCustomer(ctx, tx, request.Mode, request.OfferID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistFundingBlock(ctx, tx, request, 404, "RESOURCE_NOT_FOUND", "Offer not found")
		}
		return FundingOutcome{}, errors.New("funding_offer_lookup_failed")
	}
	customer, err := lockFundingCustomer(ctx, tx, request.Mode, customerID)
	if err != nil {
		return FundingOutcome{}, errors.New("funding_customer_unavailable")
	}
	offer, err := lockFundingOffer(ctx, tx, request.Mode, request.OfferID)
	if err != nil {
		return FundingOutcome{}, errors.New("funding_offer_unavailable")
	}
	bridgeIDs, err := lockCustomerBridges(ctx, tx, request.Mode, customerID)
	if err != nil {
		return FundingOutcome{}, err
	}
	if err := lockCustomerReservations(ctx, tx, request.Mode, customerID); err != nil {
		return FundingOutcome{}, err
	}
	if err := lockCustomerInvoices(ctx, tx, request.Mode, customerID); err != nil {
		return FundingOutcome{}, err
	}
	mapping, fingerprint, mapErr := LockVerifiedMapping(ctx, tx, offer.customerID, offer.accountID, offer.terms.CardRef)
	prePolicyBlock := ""
	if mapErr != nil {
		if mapErr.Error() == "mapping_unavailable" || mapErr.Error() == "mapping_fingerprint_failed" {
			return FundingOutcome{}, mapErr
		}
		prePolicyBlock = "MAPPING_CONFLICT"
	} else if fingerprint != offer.terms.MappingFingerprint || mapping.RosterAccountID != offer.terms.BorrowerAccountRef || mapping.CardID != offer.terms.CardRef {
		prePolicyBlock = "MAPPING_CONFLICT"
	}
	now, authorityErr := validateCurrentAuthority(ctx, tx, request.Mode, offer, facility, request.Clock)
	if authorityErr != nil {
		if authorityErr.Error() == "funding_authority_read_failed" || authorityErr.Error() == "funding_clock_invalid" {
			return FundingOutcome{}, authorityErr
		}
		if prePolicyBlock == "" {
			prePolicyBlock = "COLLECTION_AUTHORITY_BLOCKED"
		}
	}
	if now.IsZero() {
		return FundingOutcome{}, errors.New("funding_clock_invalid")
	}
	if outcome, code, message := validateLockedOffer(request, offer, facility, now); code != "" {
		if terminalFundingOfferState(code) != "" {
			return retirePrebridgeOffer(ctx, tx, request, offer, now, terminalFundingOfferState(code), code, message)
		}
		return persistFundingBlock(ctx, tx, request, outcome, code, message)
	}
	acceptanceErr := requireMatchingAcceptance(ctx, tx, request.Mode, offer)
	freshReadErr := validateFreshFundingReads(request, offer, facility, now)
	if errors.Is(freshReadErr, errFundingFreshEvidenceStale) {
		return FundingOutcome{}, freshReadErr
	}
	liquidityBlockCode := ""
	if freshReadErr == nil {
		otherSourceLiability, liquidityErr := ValidateSourceLiquidity(ctx, tx, SourceLiquidityProjection{
			Mode: request.Mode, SourceAccountRef: facility.fundingAccount,
			AvailableCents: request.FacilityBalance.AvailableCents, CandidateAmountCents: offer.principal,
			CandidateFeeCents: request.KnownSourceFeeCents, MockFeeContract: request.MockFeeContract,
			ExcludeOperationID: request.ExcludeOperationID, ExcludeReservationID: request.ExcludeReservationID,
			ExcludeBridgeID: request.ExcludeBridgeID, ExcludeTransactionID: request.ExcludeTransactionID,
		})
		request.OtherSourceLiabilityCents = otherSourceLiability
		if errors.Is(liquidityErr, errFundingSourceFeeUnknown) {
			liquidityBlockCode = "SOURCE_LIQUIDITY_UNKNOWN"
		} else if errors.Is(liquidityErr, errFundingSourceInsufficient) {
			liquidityBlockCode = "FUNDING_SOURCE_BALANCE_INSUFFICIENT"
		} else if liquidityErr != nil {
			return FundingOutcome{}, liquidityErr
		}
	}
	for _, id := range bridgeIDs {
		var funded bool
		var lifecycle string
		if err := tx.QueryRow(ctx, `SELECT ever_funded,lifecycle FROM servicing.bridges WHERE mode=$1 AND id=$2`, request.Mode, id).Scan(&funded, &lifecycle); err != nil {
			return FundingOutcome{}, errors.New("funding_bridge_read_failed")
		}
		if !funded || lifecycle == "REPAID" || lifecycle == "VOID_UNFUNDED" {
			continue
		}
		accrual, accrualErr := AccrueThrough(ctx, tx, AccrualOptions{Mode: mode, Pricing: request.Pricing,
			ExpectedPolicyHash: request.Evaluator.PolicyHash()}, id, now)
		if accrualErr != nil {
			return FundingOutcome{}, errors.New("funding_accrual_unavailable")
		}
		if accrual.Pending || accrual.CorrectionRequired {
			return persistFundingBlock(ctx, tx, request, 422, "ACCRUAL_CATCHUP_PENDING", "Existing bridge accrual must be reconciled first")
		}
	}
	if err := catchUpFundingDefaults(ctx, tx, request.Mode, bridgeIDs, now); err != nil {
		return FundingOutcome{}, err
	}
	customer, err = lockFundingCustomer(ctx, tx, request.Mode, customerID)
	if err != nil {
		return FundingOutcome{}, errors.New("funding_customer_unavailable")
	}
	bridges, err := readFundingBridges(ctx, tx, request.Mode, customerID)
	if err != nil {
		return FundingOutcome{}, err
	}
	reservations, reservationTotals, err := readFundingReservations(ctx, tx, request.Mode, customerID)
	if err != nil {
		return FundingOutcome{}, err
	}
	invoices, customerCapturedAt, receivablesComplete, invoiceSourceRefs, invoiceSources, err := readFundingInvoices(ctx, tx, request.Mode, customerID)
	if err != nil {
		return FundingOutcome{}, err
	}
	if _, err := refreshFundingFacility(ctx, tx, request.Mode, request.FacilityID, &facility); err != nil {
		return FundingOutcome{}, err
	}
	if err := validateLedgerCounters(facility, customer, bridges, reservationTotals); err != nil {
		return persistFundingBlock(ctx, tx, request, 422, "LEDGER_RECONCILIATION_REQUIRED", "Funding ledger counters do not match immutable records")
	}
	input, err := buildFundingPolicyInput(request, offer, facility, customer, bridges, reservations,
		invoices, customerCapturedAt, receivablesComplete, invoiceSourceRefs, now, nil)
	if errors.Is(err, errFundingFacilityEvidenceIncomplete) {
		return persistFundingBlock(ctx, tx, request, 422, "FACILITY_EVIDENCE_INCOMPLETE", "Facility source references are unavailable")
	}
	if err != nil {
		return FundingOutcome{}, err
	}
	policyResult, err := request.Evaluator.Evaluate(input)
	if err != nil {
		return FundingOutcome{}, errors.New("shared_policy_evaluation_failed")
	}
	blockedCode := prePolicyBlock
	if blockedCode == "" && acceptanceErr != nil {
		if acceptanceErr.Error() == "funding_acceptance_read_failed" {
			return FundingOutcome{}, acceptanceErr
		}
		blockedCode = "ACCEPTANCE_REQUIRED"
	}
	if blockedCode == "" && freshReadErr != nil {
		blockedCode = fundingBlockCode(freshReadErr)
	} else if blockedCode == "" {
		blockedCode = fundingPolicyBlock(policyResult, offer, request)
	}
	if liquidityBlockCode != "" && blockedCode == "" {
		blockedCode = liquidityBlockCode
	}
	if code, _ := fundingLocalHold(customer, bridges, invoices); code != "" && blockedCode == "" {
		blockedCode = code
	}
	if code, err := fundingBookGuard(ctx, tx, request.Mode, offer, bridges); err != nil {
		return FundingOutcome{}, err
	} else if code != "" && blockedCode == "" {
		blockedCode = code
	}
	approval := ""
	if blockedCode == "" && policyResult.ApprovalRequired {
		approved, approvalErr := hasHumanApproval(ctx, tx, request.Mode, offer)
		if approvalErr != nil {
			return FundingOutcome{}, approvalErr
		}
		if !approved {
			blockedCode = "APPROVAL_REQUIRED"
		} else {
			approval = "APPROVE"
		}
	}
	if blockedCode == "" && policyResult.Regime != nil &&
		*policyResult.Regime != policy.RegimeAbundant && *policyResult.Regime != policy.RegimeNormal {
		blockedCode = "SCARCE_CAPITAL_RANKING_UNSUPPORTED"
	}
	var systemActor systemFundingActor
	if blockedCode == "" && approval == "" {
		systemActor, err = readSystemFundingActor(ctx, tx, request.Mode)
		if err != nil {
			blockedCode = "SYSTEM_ACTOR_UNAVAILABLE"
		} else {
			approval = "AUTO_APPROVE"
		}
	}
	var allocations []fundingAllocation
	allocations = make([]fundingAllocation, 0)
	if blockedCode == "" {
		allocations, err = fundingAllocations(input.Customer.Receivables, invoiceSources, offer.terms, now, offer.dueOn, offer.principal+offer.contractFee)
		if err != nil || !validateFundingAllocationBasis(policyResult, input.Customer.Receivables, allocations, invoiceSources, now, offer.dueOn) {
			blockedCode = "INVOICE_ALLOCATION_POLICY_DRIFT"
		}
	}
	checkID, err := insertFundingCheck(ctx, tx, request, offer, input, policyResult, facility, allocations, blockedCode)
	if err != nil {
		return FundingOutcome{}, err
	}
	if blockedCode != "" {
		status := 422
		message := "Funding recheck did not satisfy all required guards"
		if terminalState := terminalFundingOfferState(blockedCode); terminalState != "" {
			return retirePrebridgeOffer(ctx, tx, request, offer, now, terminalState, blockedCode, message)
		}
		return persistFundingBlock(ctx, tx, request, status, blockedCode, message)
	}
	bridgeID, err := dbUUID(ctx, tx)
	if err != nil {
		return FundingOutcome{}, err
	}
	reservationID, err := dbUUID(ctx, tx)
	if err != nil {
		return FundingOutcome{}, err
	}
	operationID, err := dbUUID(ctx, tx)
	if err != nil {
		return FundingOutcome{}, err
	}
	operation := provider.Operation{ID: operationID, Kind: provider.OperationFund, Mode: request.Mode,
		SourceAccountRef: facility.fundingAccount, DestinationAccountRef: offer.accountID,
		AmountCents: offer.principal, Currency: "USD", RequestID: "bridge-" + bridgeID, BridgeID: bridgeID}
	operation.PayloadHash, err = provider.PayloadHash(operation)
	if err != nil {
		return FundingOutcome{}, errors.New("funding_operation_payload_invalid")
	}
	if err := createFundingReservation(ctx, tx, request, offer, facility, customer, input, policyResult,
		bridgeID, reservationID, operationID, checkID, approval, systemActor, allocations, now); err != nil {
		return FundingOutcome{}, err
	}
	response := map[string]any{"bridge_id": bridgeID, "reservation_id": reservationID, "operation_id": operationID,
		"version": 1, "status": "RESERVED", "read_url": "/api/bridges/" + bridgeID}
	body, err := json.Marshal(response)
	if err != nil {
		return FundingOutcome{}, errors.New("funding_response_invalid")
	}
	if err := insertFundingCommand(ctx, tx, request, 202, body, bridgeID); err != nil {
		return FundingOutcome{}, err
	}
	return FundingOutcome{Status: 202, Body: body}, nil
}

// RetirePrebridgeOfferTx supersedes an accepted but not yet reserved offer
// after a definitive bound-source change, using the same financial lock order.
func RetirePrebridgeOfferTx(ctx context.Context, tx pgx.Tx, request FundingGuard, code, message string) (FundingOutcome, error) {
	if request.Clock == nil || request.Mode == "" || request.FacilityID == "" || request.OfferID == "" {
		return FundingOutcome{}, errors.New("funding_retirement_request_invalid")
	}
	if _, err := lockFundingFacility(ctx, tx, request.Mode, request.FacilityID); err != nil {
		return FundingOutcome{}, err
	}
	customerID, err := fundingOfferCustomer(ctx, tx, request.Mode, request.OfferID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistFundingBlock(ctx, tx, request, 404, "RESOURCE_NOT_FOUND", "Offer not found")
		}
		return FundingOutcome{}, errors.New("funding_offer_lookup_failed")
	}
	if _, err := lockFundingCustomer(ctx, tx, request.Mode, customerID); err != nil {
		return FundingOutcome{}, errors.New("funding_customer_unavailable")
	}
	offer, err := lockFundingOffer(ctx, tx, request.Mode, request.OfferID)
	if err != nil {
		return FundingOutcome{}, errors.New("funding_offer_unavailable")
	}
	if request.ExpectedVersion != offer.version {
		return persistFundingBlock(ctx, tx, request, 409, "VERSION_CONFLICT", "Offer version changed")
	}
	if _, err := lockCustomerBridges(ctx, tx, request.Mode, customerID); err != nil {
		return FundingOutcome{}, err
	}
	if err := lockCustomerReservations(ctx, tx, request.Mode, customerID); err != nil {
		return FundingOutcome{}, err
	}
	if err := lockCustomerInvoices(ctx, tx, request.Mode, customerID); err != nil {
		return FundingOutcome{}, err
	}
	now := request.Clock().UTC()
	if now.IsZero() {
		return FundingOutcome{}, errors.New("funding_clock_invalid")
	}
	state := "SUPERSEDED"
	if code == "OFFER_EXPIRED" || code == "OFFER_MIDNIGHT_SUPERSEDED" {
		state = "EXPIRED"
	}
	return retirePrebridgeOffer(ctx, tx, request, offer, now, state, code, message)
}

func lockFundingFacility(ctx context.Context, tx pgx.Tx, mode, id string) (fundingFacility, error) {
	var f fundingFacility
	err := tx.QueryRow(ctx, `SELECT funding_account_ref,total_cents,reserve_bps,principal_owed_cents,
		reserved_principal_cents,other_commitments_cents,configuration_version,version,
		COALESCE(source_id,''),COALESCE(source_version,''),captured_at,source_refs,updated_at
		FROM servicing.facilities WHERE mode=$1 AND id=$2 FOR UPDATE`, mode, id).Scan(
		&f.fundingAccount, &f.total, &f.reserveBPS, &f.principalOwed, &f.reservedPrincipal,
		&f.otherCommitments, &f.configurationVersion, &f.version, &f.sourceID, &f.sourceVersion,
		&f.capturedAt, &f.sourceRefs, &f.updatedAt)
	if err != nil || f.capturedAt == nil {
		return fundingFacility{}, errors.New("funding_facility_unavailable")
	}
	return f, nil
}

func fundingOfferCustomer(ctx context.Context, tx pgx.Tx, mode, offerID string) (string, error) {
	var customerID string
	err := tx.QueryRow(ctx, `SELECT customer_id FROM servicing.offers WHERE mode=$1 AND id=$2`, mode, offerID).Scan(&customerID)
	return customerID, err
}

func lockFundingCustomer(ctx context.Context, tx pgx.Tx, mode, customerID string) (fundingCustomer, error) {
	var customer fundingCustomer
	err := tx.QueryRow(ctx, `SELECT version,history_complete,active_hold,defaulted_at,
		principal_owed_cents,unpaid_fee_cents,reserved_principal_cents,reserved_fee_cents
		FROM servicing.customers WHERE mode=$1 AND id=$2 FOR UPDATE`, mode, customerID).Scan(
		&customer.version, &customer.historyComplete, &customer.activeHold, &customer.defaultedAt,
		&customer.principalOwed, &customer.unpaidFees, &customer.reservedPrincipal, &customer.reservedFees)
	return customer, err
}

func lockFundingOffer(ctx context.Context, tx pgx.Tx, mode, offerID string) (fundingOffer, error) {
	var offer fundingOffer
	err := tx.QueryRow(ctx, `SELECT id::text,facility_id,customer_id,canonical_account_id,transaction_id,
		authorization_event_id,policy_id,policy_hash,terms_hash,state,evaluation_id,
		COALESCE(originating_case_id,''),terms_json,source_refs,candidate_evidence,principal_cents,contract_fee_cents,
		version,revision,expires_at,offered_on,due_on
		FROM servicing.offers WHERE mode=$1 AND id=$2 FOR UPDATE`, mode, offerID).Scan(
		&offer.id, &offer.facilityID, &offer.customerID, &offer.accountID, &offer.transactionID,
		&offer.authorizationID, &offer.policyID, &offer.policyHash, &offer.termsHash, &offer.state,
		&offer.evaluationID, &offer.caseID, &offer.termsJSON, &offer.sourceRefs, &offer.candidateEvidence, &offer.principal,
		&offer.contractFee, &offer.version, &offer.revision, &offer.expiresAt, &offer.offeredOn, &offer.dueOn)
	if err != nil {
		return fundingOffer{}, err
	}
	if json.Unmarshal(offer.termsJSON, &offer.terms) != nil {
		return fundingOffer{}, errors.New("funding_offer_terms_invalid")
	}
	return offer, nil
}

func validateLockedOffer(request FundingGuard, offer fundingOffer, facility fundingFacility, now time.Time) (int, string, string) {
	termsHash, hashErr := offer.terms.Hash()
	if hashErr != nil || termsHash != offer.termsHash || offer.terms.OfferID != offer.id ||
		offer.terms.OfferRevision != offer.revision || offer.terms.SourceMode != request.Mode ||
		offer.terms.FacilityID != request.FacilityID || offer.terms.CustomerID != offer.customerID ||
		offer.terms.BorrowerAccountRef != offer.accountID || offer.terms.TransactionID != offer.transactionID ||
		offer.terms.PrincipalCents != offer.principal || offer.terms.ContractFeeCents != offer.contractFee ||
		offer.terms.PolicyID != offer.policyID || offer.terms.PolicyHash != offer.policyHash {
		return 422, "TERMS_BINDING_MISMATCH", "Stored offer terms failed binding verification"
	}
	if request.ExpectedVersion != offer.version {
		return 409, "VERSION_CONFLICT", "Offer version changed"
	}
	if offer.state != "AWAITING_APPROVAL" && offer.state != "READY" {
		return 422, "OFFER_NOT_READY", "Offer is not eligible for funding"
	}
	if !now.Before(offer.expiresAt) || offer.offeredOn.Format("2006-01-02") != now.Format("2006-01-02") || offer.terms.OfferedOn != now.Format("2006-01-02") {
		return 422, "OFFER_EXPIRED", "Offer expired or crossed its accepted UTC date"
	}
	if facility.fundingAccount == "" || offer.terms.FundingAccountRef != facility.fundingAccount || offer.facilityID != request.FacilityID {
		return 422, "FACILITY_BINDING_MISMATCH", "Offer funding account no longer matches its facility"
	}
	assessment := request.Assessment
	if assessment.ID != offer.evaluationID || assessment.PolicyID != offer.policyID || assessment.PolicyHash != offer.policyHash ||
		assessment.PolicyID != request.Evaluator.PolicyID() || assessment.PolicyHash != request.Evaluator.PolicyHash() ||
		assessment.CustomerID != offer.customerID || assessment.AccountID != offer.accountID ||
		assessment.TransactionID != offer.transactionID || !validFundingAssessment(assessment) ||
		assessment.PrincipalCents == nil || *assessment.PrincipalCents != offer.principal ||
		assessment.TermDays == nil || *assessment.TermDays != offer.terms.TermDays ||
		assessment.EstimatedFeeCents == nil || *assessment.EstimatedFeeCents != offer.contractFee ||
		!candidateEvidenceMatches(offer, assessment) {
		return 422, "CANDIDATE_ASSESSMENT_INCOMPATIBLE", "Saved assessment no longer matches the accepted offer"
	}
	return 0, "", ""
}

func terminalFundingOfferState(code string) string {
	switch code {
	case "OFFER_EXPIRED":
		return "EXPIRED"
	case "TERMS_BINDING_MISMATCH", "FACILITY_BINDING_MISMATCH", "CANDIDATE_ASSESSMENT_INCOMPATIBLE",
		"MAPPING_CONFLICT", "COLLECTION_AUTHORITY_BLOCKED", "CURRENT_SHORTFALL_CHANGED",
		"CURRENT_SHORTFALL_INVALID", "PAYMENT_ALREADY_FUNDED", "TERMS_POLICY_PRICE_MISMATCH":
		return "SUPERSEDED"
	default:
		return ""
	}
}

// retirePrebridgeOffer ends only an unreserved offer. Money intents and bridges
// are never fabricated to represent a refusal before reservation.
func retirePrebridgeOffer(ctx context.Context, tx pgx.Tx, request FundingGuard, offer fundingOffer,
	now time.Time, state, code, message string) (FundingOutcome, error) {
	var hasBridge bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND offer_id=$2)`, request.Mode, offer.id).Scan(&hasBridge); err != nil {
		return FundingOutcome{}, errors.New("funding_prebridge_check_failed")
	}
	if hasBridge {
		return persistFundingBlock(ctx, tx, request, 422, "FUNDING_OPERATION_EXISTS", "Offer already has a bridge operation")
	}
	var version int64
	if err := tx.QueryRow(ctx, `UPDATE servicing.offers SET state=$3,version=version+1
		WHERE mode=$1 AND id=$2 AND state NOT IN ('SUPERSEDED','EXPIRED','TERMINATED','CONSUMED')
		RETURNING version`, request.Mode, offer.id, state).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistFundingBlock(ctx, tx, request, 422, code, message)
		}
		return FundingOutcome{}, errors.New("funding_offer_retire_failed")
	}
	var caseRef any
	if offer.caseID != "" {
		caseRef = offer.caseID
	}
	summary, _ := json.Marshal(map[string]any{"customer_id": offer.customerID,
		"account_ref": "customer:" + offer.customerID, "originating_case_id": caseRef,
		"evaluation_id": offer.evaluationID, "offer_id": offer.id, "bridge_id": nil,
		"bridge_version": nil, "status": state, "terms_hash": offer.termsHash})
	changeType := "OFFER_SUPERSEDED"
	if state == "EXPIRED" {
		changeType = "OFFER_EXPIRED"
	}
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: domain.Mode(request.Mode), ResourceType: "OFFER",
		ResourceID: offer.id, ResourceVersion: version, ChangeType: changeType, Summary: summary}, nil, nil); err != nil {
		return FundingOutcome{}, err
	}
	return persistFundingBlock(ctx, tx, request, 422, code, message)
}

func candidateEvidenceMatches(offer fundingOffer, current upstream.SavedEvaluation) bool {
	var evidence struct {
		Evaluation json.RawMessage `json:"evaluation"`
		Shortfall  json.RawMessage `json:"shortfall"`
	}
	if json.Unmarshal(offer.candidateEvidence, &evidence) != nil || len(evidence.Evaluation) == 0 || len(evidence.Shortfall) == 0 {
		return false
	}
	var original upstream.SavedEvaluation
	var originalShortfall upstream.ShortfallObservation
	if json.Unmarshal(evidence.Evaluation, &original) != nil || json.Unmarshal(evidence.Shortfall, &originalShortfall) != nil {
		return false
	}
	return validFundingAssessment(original) && original.ID == current.ID && original.ShortfallID == current.ShortfallID &&
		original.SourceMode == current.SourceMode && original.CustomerID == current.CustomerID &&
		original.AccountID == current.AccountID && original.TransactionID == current.TransactionID &&
		original.PolicyID == current.PolicyID && original.PolicyHash == current.PolicyHash &&
		original.PrincipalCents != nil && current.PrincipalCents != nil && *original.PrincipalCents == *current.PrincipalCents &&
		original.TermDays != nil && current.TermDays != nil && *original.TermDays == *current.TermDays &&
		original.EstimatedFeeCents != nil && current.EstimatedFeeCents != nil && *original.EstimatedFeeCents == *current.EstimatedFeeCents &&
		originalShortfall.ID == current.ShortfallID && originalShortfall.CustomerID == offer.customerID &&
		originalShortfall.AccountID == offer.accountID && originalShortfall.TransactionID != nil &&
		*originalShortfall.TransactionID == offer.transactionID && originalShortfall.CanonicalEventID == offer.authorizationID &&
		originalShortfall.ShortfallCents != nil && *originalShortfall.ShortfallCents == offer.principal
}

func validFundingAssessment(assessment upstream.SavedEvaluation) bool {
	if assessment.Status != "ELIGIBLE" && assessment.Status != "REVIEW_REQUIRED" ||
		assessment.FundingAuthorized == nil || *assessment.FundingAuthorized ||
		assessment.RequiresRecheck == nil || !*assessment.RequiresRecheck || assessment.ApprovalRequired == nil {
		return false
	}
	passRules := map[string]bool{}
	for _, rule := range assessment.Rules {
		if passRules[rule.RuleID] || rule.Status != "PASS" {
			return false
		}
		passRules[rule.RuleID] = true
	}
	for _, id := range []string{"INPUT_FRESH", "CUSTOMER_ACTIVE", "HISTORY_SUFFICIENT", "NO_OVERDUE_OR_DEFAULT",
		"CUSTOMER_CAPACITY", "PAYMENT_NOT_BRIDGED", "RECEIVABLE_COVERAGE", "CAPITAL_AVAILABLE", "SPEND_ALLOWED", "REGIME_TIER_ALLOWED"} {
		if !passRules[id] {
			return false
		}
	}
	if assessment.Status == "ELIGIBLE" {
		return !*assessment.ApprovalRequired
	}
	return *assessment.ApprovalRequired && len(assessment.ReasonCodes) == 1 && assessment.ReasonCodes[0] == "HUMAN_APPROVAL_REQUIRED"
}

func lockCustomerBridges(ctx context.Context, tx pgx.Tx, mode, customerID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM servicing.bridges WHERE mode=$1 AND customer_id=$2 ORDER BY id FOR UPDATE`, mode, customerID)
	if err != nil {
		return nil, errors.New("funding_bridge_lock_failed")
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, errors.New("funding_bridge_lock_failed")
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, errors.New("funding_bridge_lock_failed")
	}
	return ids, nil
}

func lockCustomerReservations(ctx context.Context, tx pgx.Tx, mode, customerID string) error {
	rows, err := tx.Query(ctx, `SELECT id::text FROM servicing.reservations
		WHERE mode=$1 AND customer_id=$2 ORDER BY id FOR UPDATE`, mode, customerID)
	if err != nil {
		return errors.New("funding_reservation_lock_failed")
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			return errors.New("funding_reservation_lock_failed")
		}
	}
	if rows.Err() != nil {
		return errors.New("funding_reservation_lock_failed")
	}
	return nil
}

func lockCustomerInvoices(ctx context.Context, tx pgx.Tx, mode, customerID string) error {
	rows, err := tx.Query(ctx, `SELECT source_system,invoice_id FROM servicing.receivables
		WHERE mode=$1 AND customer_id=$2 ORDER BY source_system,invoice_id FOR UPDATE`, mode, customerID)
	if err != nil {
		return errors.New("funding_invoice_lock_failed")
	}
	defer rows.Close()
	for rows.Next() {
		var source, id string
		if rows.Scan(&source, &id) != nil {
			return errors.New("funding_invoice_lock_failed")
		}
	}
	if rows.Err() != nil {
		return errors.New("funding_invoice_lock_failed")
	}
	return nil
}

func validateFreshFundingReads(request FundingGuard, offer fundingOffer, facility fundingFacility, now time.Time) error {
	shortfall := request.SavedShortfall
	payment := request.Payment
	if payment.Mode != request.Mode || payment.CustomerID != offer.customerID || payment.CanonicalAccountID != offer.accountID ||
		payment.ParentTransactionID != offer.transactionID || payment.AuthorizationEventID != offer.authorizationID ||
		payment.Currency != "USD" {
		return errors.New("funding_payment_binding_changed")
	}
	if payment.AttemptedAmountCents <= 0 || payment.AvailableCents < 0 || payment.ShortfallCents < 0 {
		return errors.New("funding_payment_amount_invalid")
	}
	if !freshFundingTime(payment.ObservedAt, now) || !freshFundingTime(payment.BalanceObservedAt, now) ||
		!freshFundingBalance(request.BorrowerBalance, offer.accountID, now) ||
		!freshFundingBalance(request.FacilityBalance, facility.fundingAccount, now) {
		return errFundingFreshEvidenceStale
	}
	if payment.ObservedAt.After(request.BorrowerBalance.RequestedAt.Add(5*time.Second)) ||
		payment.BalanceObservedAt.After(request.BorrowerBalance.ObservedAt.Add(5*time.Second)) {
		return errors.New("funding_payment_balance_observation_incoherent")
	}
	if shortfall.AmountSemantics != "current_need_at_balance_observation" || !shortfall.RequiresRecheck ||
		shortfall.Currency == nil || *shortfall.Currency != "USD" ||
		shortfall.TransactionID == nil || *shortfall.TransactionID != offer.transactionID ||
		shortfall.CustomerID != offer.customerID || shortfall.AccountID != offer.accountID ||
		shortfall.CardID == nil || *shortfall.CardID != offer.terms.CardRef || shortfall.CanonicalEventID != offer.authorizationID ||
		request.Assessment.ShortfallID == "" || request.Assessment.ShortfallID != shortfall.ID {
		return errors.New("funding_payment_binding_changed")
	}
	currentNeed := int64(0)
	if payment.AttemptedAmountCents > request.BorrowerBalance.AvailableCents {
		currentNeed = payment.AttemptedAmountCents - request.BorrowerBalance.AvailableCents
	}
	if currentNeed == 0 || currentNeed != offer.principal || currentNeed != payment.ShortfallCents ||
		shortfall.AttemptedAmountCents == nil || *shortfall.AttemptedAmountCents != payment.AttemptedAmountCents ||
		shortfall.ShortfallCents == nil || *shortfall.ShortfallCents != currentNeed {
		return errors.New("funding_payment_amount_invalid")
	}
	if request.KnownSourceFeeCents == nil || *request.KnownSourceFeeCents < 0 || request.SourceFeeContractID == "" {
		return errFundingSourceFeeUnknown
	}
	return nil
}

func unresolvedSourceLiability(ctx context.Context, tx pgx.Tx, mode, source string, candidateFee *int64,
	mockFeeContract bool, excludeOperationID, excludeReservationID, excludeBridgeID, excludeTransactionID string) (int64, error) {
	if candidateFee == nil || *candidateFee < 0 || source == "" {
		return 0, errFundingSourceFeeUnknown
	}
	rows, err := tx.Query(ctx, `SELECT o.id::text,o.state,o.amount_cents,o.reserved_provider_fee_cents,
		o.provider_fee_cents,r.state,o.kind,COALESCE(o.reservation_id::text,''),o.bridge_id::text,
		o.destination_account_ref,o.request_id,b.transaction_id FROM servicing.operations o
		LEFT JOIN servicing.reservations r ON r.mode=o.mode AND r.id=o.reservation_id
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND o.source_account_ref=$2
		AND o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD')
		ORDER BY o.id`, mode, source)
	if err != nil {
		return 0, errors.New("funding_source_liability_read_failed")
	}
	defer rows.Close()
	var total int64
	excludedFound := false
	for rows.Next() {
		var id, state, kind, reservationID, bridgeID, destination, requestID, transactionID string
		var amount int64
		var reservedFee, actualFee *int64
		var reservationState *string
		if rows.Scan(&id, &state, &amount, &reservedFee, &actualFee, &reservationState, &kind,
			&reservationID, &bridgeID, &destination, &requestID, &transactionID) != nil {
			return 0, errors.New("funding_source_liability_read_failed")
		}
		if id == excludeOperationID {
			if state != "QUEUED" || reservationState == nil || *reservationState != "NEVER_SENT" || kind != "FUND" ||
				reservationID == "" || reservationID != excludeReservationID || bridgeID != excludeBridgeID ||
				transactionID != excludeTransactionID || destination == source || requestID != "bridge-"+bridgeID {
				return 0, errors.New("funding_source_projection_not_proven_unsent")
			}
			excludedFound = true
			continue
		}
		fee, known := int64(0), false
		if reservedFee != nil {
			fee, known = *reservedFee, true
			if actualFee != nil && *actualFee > fee {
				fee = *actualFee
			}
		} else if actualFee != nil {
			fee, known = *actualFee, true
		} else if mockFeeContract {
			fee, known = 0, true
		}
		if !known || amount <= 0 || fee < 0 {
			return 0, errFundingSourceFeeUnknown
		}
		debit, ok := checkedFundingAdd(amount, fee)
		if !ok {
			return 0, errors.New("funding_source_liability_overflow")
		}
		total, ok = checkedFundingAdd(total, debit)
		if !ok {
			return 0, errors.New("funding_source_liability_overflow")
		}
	}
	if rows.Err() != nil {
		return 0, errors.New("funding_source_liability_read_failed")
	}
	if excludeOperationID != "" && !excludedFound {
		return 0, errors.New("funding_source_projection_operation_missing")
	}
	return total, nil
}

// ValidateSourceLiquidity subtracts every other unresolved local debit and
// known provider fee from the fresh balance without adding local holds to cash.
func ValidateSourceLiquidity(ctx context.Context, tx pgx.Tx, projection SourceLiquidityProjection) (int64, error) {
	if projection.AvailableCents < 0 || projection.CandidateAmountCents <= 0 ||
		projection.CandidateFeeCents == nil || *projection.CandidateFeeCents < 0 {
		return 0, errFundingSourceFeeUnknown
	}
	other, err := unresolvedSourceLiability(ctx, tx, projection.Mode, projection.SourceAccountRef,
		projection.CandidateFeeCents, projection.MockFeeContract, projection.ExcludeOperationID,
		projection.ExcludeReservationID, projection.ExcludeBridgeID, projection.ExcludeTransactionID)
	if err != nil {
		return 0, err
	}
	needed, ok := checkedFundingAdd(projection.CandidateAmountCents, *projection.CandidateFeeCents)
	if !ok {
		return 0, errors.New("funding_source_liability_overflow")
	}
	needed, ok = checkedFundingAdd(needed, other)
	if !ok {
		return 0, errors.New("funding_source_liability_overflow")
	}
	if projection.AvailableCents < needed {
		return other, errFundingSourceInsufficient
	}
	return other, nil
}

func freshFundingBalance(value provider.BalanceObservation, account string, now time.Time) bool {
	return value.AccountRef == account && value.Currency == "USD" && value.AvailableCents >= 0 &&
		fundingTimeUTC(value.RequestedAt) && fundingTimeUTC(value.ObservedAt) &&
		freshFundingTime(value.RequestedAt, now) && freshFundingTime(value.ObservedAt, now) &&
		!value.RequestedAt.After(value.ObservedAt)
}

func freshFundingTime(value, now time.Time) bool {
	return fundingTimeUTC(value) && !value.IsZero() && !value.After(now.Add(5*time.Second)) && !value.Before(now.Add(-30*time.Second))
}

func fundingTimeUTC(value time.Time) bool { _, offset := value.Zone(); return offset == 0 }

func fundingBlockCode(err error) string {
	switch err.Error() {
	case "funding_payment_changed", "funding_payment_binding_changed":
		return "CURRENT_SHORTFALL_CHANGED"
	case "funding_payment_amount_invalid":
		return "CURRENT_SHORTFALL_INVALID"
	case "funding_source_balance_insufficient":
		return "FUNDING_SOURCE_BALANCE_INSUFFICIENT"
	case "funding_source_fee_unknown":
		return "SOURCE_LIQUIDITY_UNKNOWN"
	default:
		return "FRESH_PROVIDER_EVIDENCE_INVALID"
	}
}

func catchUpFundingDefaults(ctx context.Context, tx pgx.Tx, mode string, bridgeIDs []string, now time.Time) error {
	for _, id := range bridgeIDs {
		var funded bool
		var lifecycle string
		if err := tx.QueryRow(ctx, `SELECT ever_funded,lifecycle FROM servicing.bridges WHERE mode=$1 AND id=$2`, mode, id).Scan(&funded, &lifecycle); err != nil {
			return errors.New("funding_default_catchup_failed")
		}
		if !funded || lifecycle == "REPAID" || lifecycle == "VOID_UNFUNDED" {
			continue
		}
		if err := RefreshDelinquency(ctx, tx, domain.Mode(mode), id, now); err != nil {
			return errors.New("funding_default_catchup_failed")
		}
	}
	return nil
}

func readFundingBridges(ctx context.Context, tx pgx.Tx, mode, customerID string) ([]policy.Bridge, error) {
	rows, err := tx.Query(ctx, `SELECT id::text,transaction_id,lifecycle,ever_funded,principal_original_cents,
		principal_owed_cents,contract_fee_owed_cents,late_fee_owed_cents,due_on,funded_at,closed_at,
		first_defaulted_on,created_at FROM servicing.bridges WHERE mode=$1 AND customer_id=$2 ORDER BY id`, mode, customerID)
	if err != nil {
		return nil, errors.New("funding_bridge_read_failed")
	}
	defer rows.Close()
	result := make([]policy.Bridge, 0)
	for rows.Next() {
		var id, transactionID, lifecycle string
		var funded bool
		var original, principal, contractFee, lateFee int64
		var due, created time.Time
		var fundedAt, closedAt, defaultedAt *time.Time
		if rows.Scan(&id, &transactionID, &lifecycle, &funded, &original, &principal, &contractFee, &lateFee,
			&due, &fundedAt, &closedAt, &defaultedAt, &created) != nil {
			return nil, errors.New("funding_bridge_read_failed")
		}
		if !funded || lifecycle == "VOID_UNFUNDED" || lifecycle == "RESERVED" || lifecycle == "FUNDING_PENDING" {
			continue
		}
		if fundedAt == nil {
			return nil, errors.New("funding_funded_bridge_timestamp_missing")
		}
		fees, ok := checkedFundingAdd(contractFee, lateFee)
		if !ok {
			return nil, errors.New("funding_bridge_amount_invalid")
		}
		status := "OPEN"
		if lifecycle == "REPAID" {
			status = "REPAID"
		} else if defaultedAt != nil {
			status = "DEFAULTED"
		}
		opened := *fundedAt
		var closed *policy.Date
		var outcome *string
		if closedAt != nil {
			date := policy.Date(closedAt.UTC().Format("2006-01-02"))
			closed = &date
			value := "ON_TIME"
			if closedAt.UTC().After(due.UTC()) {
				value = "LATE"
			}
			outcome = &value
		}
		var defaultOn *policy.Date
		if defaultedAt != nil {
			date := policy.Date(defaultedAt.UTC().Format("2006-01-02"))
			defaultOn = &date
		}
		result = append(result, policy.Bridge{ID: id, TransactionID: transactionID, OriginalPrincipalCents: original,
			PrincipalOwedCents: principal, FeesOwedCents: fees, Status: status,
			OpenedOn: policy.Date(opened.UTC().Format("2006-01-02")), DueOn: policy.Date(due.UTC().Format("2006-01-02")),
			ClosedOn: closed, DefaultedOn: defaultOn, RepaymentOutcome: outcome})
	}
	if rows.Err() != nil {
		return nil, errors.New("funding_bridge_read_failed")
	}
	return result, nil
}

func readFundingReservations(ctx context.Context, tx pgx.Tx, mode, customerID string) ([]policy.Reservation, [3]int64, error) {
	rows, err := tx.Query(ctx, `SELECT r.id::text,b.transaction_id,r.state,r.facility_principal_cents,
		r.customer_principal_cents,r.customer_fee_cents,r.bridge_id::text,b.ever_funded
		FROM servicing.reservations r JOIN servicing.bridges b ON b.mode=r.mode AND b.id=r.bridge_id
		WHERE r.mode=$1 AND r.customer_id=$2 ORDER BY r.id`, mode, customerID)
	if err != nil {
		return nil, [3]int64{}, errors.New("funding_reservation_read_failed")
	}
	defer rows.Close()
	result := make([]policy.Reservation, 0)
	var totals [3]int64
	for rows.Next() {
		var id, transactionID, state, bridgeID string
		var facilityPrincipal, principal, fee int64
		var everFunded bool
		if rows.Scan(&id, &transactionID, &state, &facilityPrincipal, &principal, &fee, &bridgeID, &everFunded) != nil {
			return nil, totals, errors.New("funding_reservation_read_failed")
		}
		status := "RELEASED"
		var bridgeRef *string
		switch state {
		case "NEVER_SENT", "DISPATCHED":
			status = "ACTIVE"
		case "CONVERTED":
			if !everFunded {
				return nil, totals, errors.New("funding_converted_reservation_invalid")
			}
			status, bridgeRef = "CONVERTED", &bridgeID
		case "RELEASED":
		default:
			return nil, totals, errors.New("funding_reservation_state_invalid")
		}
		if status == "ACTIVE" {
			var ok bool
			totals[0], ok = checkedFundingAdd(totals[0], facilityPrincipal)
			if !ok {
				return nil, totals, errors.New("funding_amount_overflow")
			}
			totals[1], ok = checkedFundingAdd(totals[1], principal)
			if !ok {
				return nil, totals, errors.New("funding_amount_overflow")
			}
			totals[2], ok = checkedFundingAdd(totals[2], fee)
			if !ok {
				return nil, totals, errors.New("funding_amount_overflow")
			}
		}
		result = append(result, policy.Reservation{ID: id, TransactionID: transactionID,
			PrincipalCents: principal, FeeCents: fee, Status: status, BridgeID: bridgeRef})
	}
	if rows.Err() != nil {
		return nil, totals, errors.New("funding_reservation_read_failed")
	}
	return result, totals, nil
}

func readFundingInvoices(ctx context.Context, tx pgx.Tx, mode, customerID string) ([]policy.Receivable, time.Time, bool, []policy.SourceReference, map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT r.source_system,r.invoice_id,r.source_revision,r.payer_id,r.currency,
		r.original_cents,r.unpaid_cents,r.reserved_cents,r.due_on,r.due_date_verified,r.allocation_complete,
		r.disputed,r.source_complete,r.version,r.source_ref,r.last_captured_at,r.backing_hold,
		COALESCE((SELECT sum(a.active_cents)::bigint FROM servicing.bridge_invoice_allocations a
		 WHERE a.mode=r.mode AND a.source_system=r.source_system AND a.invoice_id=r.invoice_id),0)
		FROM servicing.receivables r WHERE r.mode=$1 AND r.customer_id=$2 ORDER BY r.source_system,r.invoice_id`, mode, customerID)
	if err != nil {
		return nil, time.Time{}, false, nil, nil, errors.New("funding_receivable_read_failed")
	}
	defer rows.Close()
	result := make([]policy.Receivable, 0)
	var refs []policy.SourceReference
	invoiceSources := make(map[string]string)
	var oldest time.Time
	complete := true
	for rows.Next() {
		var inv fundingInvoice
		if rows.Scan(&inv.sourceSystem, &inv.id, &inv.revision, &inv.payer, &inv.currency,
			&inv.original, &inv.unpaid, &inv.reserved, &inv.dueOn, &inv.dueVerified, &inv.allocationComplete,
			&inv.disputed, &inv.sourceComplete, &inv.version, &inv.sourceRef, &inv.lastCapturedAt, &inv.backingHold,
			&inv.allocated) != nil {
			return nil, time.Time{}, false, nil, nil, errors.New("funding_receivable_read_failed")
		}
		var source domain.SourceRef
		if json.Unmarshal(inv.sourceRef, &source) != nil || source.System == "" || source.RecordID == "" || source.Version == "" || source.EffectiveAt.IsZero() {
			return nil, time.Time{}, false, nil, nil, errors.New("funding_receivable_source_invalid")
		}
		invoiceSources[inv.id] = inv.sourceSystem
		if inv.lastCapturedAt == nil || inv.allocated != inv.reserved {
			complete = false
		}
		if !inv.sourceComplete {
			complete = false
		}
		if inv.lastCapturedAt != nil && (oldest.IsZero() || inv.lastCapturedAt.Before(oldest)) {
			oldest = *inv.lastCapturedAt
		}
		refs = appendUniquePolicyRefs(refs, policy.SourceReference{System: source.System, RecordID: source.RecordID,
			Version: source.Version, EffectiveAt: source.EffectiveAt})
		var due *policy.Date
		if inv.dueOn != nil {
			date := policy.Date(inv.dueOn.UTC().Format("2006-01-02"))
			due = &date
		}
		result = append(result, policy.Receivable{InvoiceID: inv.id, PayerID: inv.payer, Currency: inv.currency,
			EligibleUnpaidCents: inv.unpaid, AllocatedCents: inv.allocated, DueOn: due,
			DueDateVerified: inv.dueVerified, AllocationComplete: inv.allocationComplete && inv.sourceComplete && !inv.disputed && !inv.backingHold,
			SourceRef: policy.SourceReference{System: source.System, RecordID: source.RecordID,
				Version: source.Version, EffectiveAt: source.EffectiveAt}})
	}
	if rows.Err() != nil {
		return nil, time.Time{}, false, nil, nil, errors.New("funding_receivable_read_failed")
	}
	if len(result) == 0 {
		complete = false
	}
	return result, oldest, complete, refs, invoiceSources, nil
}

func refreshFundingFacility(ctx context.Context, tx pgx.Tx, mode, id string, current *fundingFacility) (*fundingFacility, error) {
	var sumOwed, sumReserved int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(principal_owed_cents),0)::bigint FROM servicing.bridges WHERE mode=$1 AND facility_id=$2`, mode, id).Scan(&sumOwed); err != nil {
		return nil, errors.New("funding_facility_ledger_read_failed")
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(facility_principal_cents),0)::bigint FROM servicing.reservations WHERE mode=$1 AND facility_id=$2 AND state IN ('NEVER_SENT','DISPATCHED')`, mode, id).Scan(&sumReserved); err != nil {
		return nil, errors.New("funding_facility_ledger_read_failed")
	}
	if sumOwed != current.principalOwed || sumReserved != current.reservedPrincipal {
		return nil, errors.New("funding_facility_counter_mismatch")
	}
	return current, nil
}

func validateLedgerCounters(facility fundingFacility, customer fundingCustomer, bridges []policy.Bridge, reservationTotals [3]int64) error {
	var principalOwed, feesOwed int64
	var seenTxnDebt = map[string]bool{}
	for _, bridge := range bridges {
		var ok bool
		principalOwed, ok = checkedFundingAdd(principalOwed, bridge.PrincipalOwedCents)
		if !ok {
			return errors.New("funding_customer_counter_mismatch")
		}
		feesOwed, ok = checkedFundingAdd(feesOwed, bridge.FeesOwedCents)
		if !ok {
			return errors.New("funding_customer_counter_mismatch")
		}
		if bridge.PrincipalOwedCents > 0 || bridge.FeesOwedCents > 0 {
			seenTxnDebt[bridge.TransactionID] = true
		}
	}
	if principalOwed != customer.principalOwed || feesOwed != customer.unpaidFees ||
		reservationTotals[1] != customer.reservedPrincipal || reservationTotals[2] != customer.reservedFees {
		return errors.New("funding_customer_counter_mismatch")
	}
	if facility.total <= 0 || facility.reserveBPS < 0 || facility.reserveBPS > 10000 || facility.sourceID == "" || facility.sourceVersion == "" {
		return errors.New("funding_facility_evidence_incomplete")
	}
	return nil
}

func buildFundingPolicyInput(request FundingGuard, offer fundingOffer, facility fundingFacility,
	customer fundingCustomer, bridges []policy.Bridge, reservations []policy.Reservation,
	invoices []policy.Receivable, customerCapturedAt time.Time, receivablesComplete bool,
	invoiceSourceRefs []policy.SourceReference, now time.Time,
	own *underwriting.NeverSentReservation) (policy.Input, error) {
	policyCustomer := request.CustomerSnapshot
	policyCustomer.Bridges = make([]policy.Bridge, len(bridges))
	copy(policyCustomer.Bridges, bridges)
	policyCustomer.Reservations = make([]policy.Reservation, len(reservations))
	copy(policyCustomer.Reservations, reservations)
	policyCustomer.Receivables = make([]policy.Receivable, len(invoices))
	copy(policyCustomer.Receivables, invoices)
	policyCustomer.Coverage.BridgeHistory = customer.historyComplete
	policyCustomer.Coverage.LifetimeDefaults = customer.historyComplete
	policyCustomer.Coverage.Reservations = customer.historyComplete
	policyCustomer.Coverage.Receivables = receivablesComplete
	if !customerCapturedAt.IsZero() && customerCapturedAt.Before(policyCustomer.CapturedAt) {
		policyCustomer.CapturedAt = customerCapturedAt
	}
	policyCustomer.SourceRefs = appendUniquePolicyRefs(policyCustomer.SourceRefs, invoiceSourceRefs...)
	ledgerCustomerRef := policy.SourceReference{System: "funding-collection-ledger", RecordID: "customer:" + offer.customerID,
		Version: fmt.Sprint(customer.version), EffectiveAt: now}
	policyCustomer.SourceRefs = appendUniquePolicyRefs(policyCustomer.SourceRefs, ledgerCustomerRef)
	if own != nil {
		// readFundingBridges intentionally omits never-funded placeholders. Add
		// only this exact locked placeholder so the projection can remove it.
		policyCustomer.Bridges = append(policyCustomer.Bridges, policy.Bridge{ID: own.BridgeID,
			TransactionID: own.TransactionID, OriginalPrincipalCents: own.CustomerPrincipalCents, Status: "OPEN"})
	}
	facilityRefs, err := decodePolicyRefs(facility.sourceRefs)
	if err != nil || len(facilityRefs) == 0 {
		return policy.Input{}, errFundingFacilityEvidenceIncomplete
	}
	// These totals were read under the facility/customer locks in this exact
	// transaction. The allocation import time remains in sourceRefs and the
	// saved funding-check provenance; it is not the current ledger observation.
	ledgerFacilityRef := policy.SourceReference{System: "funding-collection-ledger", RecordID: "facility:" + request.FacilityID,
		Version: fmt.Sprint(facility.version), EffectiveAt: now}
	facilityRefs = appendUniquePolicyRefs(facilityRefs, ledgerFacilityRef)
	reservedForPolicy, ok := checkedFundingAdd(facility.reservedPrincipal, facility.otherCommitments)
	if !ok {
		return policy.Input{}, errors.New("funding_amount_overflow")
	}
	shortfall := policy.Shortfall{ID: request.SavedShortfall.ID, CustomerID: request.Payment.CustomerID,
		AccountID: request.Payment.CanonicalAccountID, CardID: offer.terms.CardRef,
		Outcome: request.SavedShortfall.Outcome, Currency: request.Payment.Currency,
		AttemptedAmountCents: request.Payment.AttemptedAmountCents, AvailableBalanceCents: request.BorrowerBalance.AvailableCents,
		ShortfallCents: request.Payment.ShortfallCents, CanonicalEventID: request.Payment.AuthorizationEventID,
		TransactionID: request.Payment.ParentTransactionID, BalanceObservedAt: request.BorrowerBalance.ObservedAt}
	input := policy.Input{EvaluatedAt: now, Mode: request.Mode, FacilityID: request.FacilityID, TermDays: offer.terms.TermDays,
		Shortfall: shortfall, Customer: policyCustomer,
		Facility: policy.FacilitySnapshot{ID: facility.sourceID, Version: facility.configurationVersion,
			Mode: request.Mode, CapturedAt: now, FacilityID: request.FacilityID,
			SourceRefs: facilityRefs, Currency: "USD", FacilityTotalCents: facility.total,
			ReserveFloorBPS: facility.reserveBPS, FundedPrincipalCents: facility.principalOwed,
			ReservedPrincipalCents: reservedForPolicy, LedgerVersion: fmt.Sprintf("%d", facility.version), LedgerComplete: true}}
	if own != nil {
		input, err = underwriting.ProjectNeverSentReservation(input, *own)
		if err != nil {
			return policy.Input{}, err
		}
	}
	return input, nil
}

func fundingLocalHold(customer fundingCustomer, bridges []policy.Bridge, invoices []policy.Receivable) (string, string) {
	if customer.activeHold {
		return "CUSTOMER_HELD", "customer active hold"
	}
	for _, invoice := range invoices {
		if invoice.AllocatedCents > invoice.EligibleUnpaidCents {
			return "RECEIVABLE_BACKING_CONFLICT", "invoice allocations exceed verified unpaid amount"
		}
	}
	_ = bridges
	return "", ""
}

func fundingBookGuard(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer, bridges []policy.Bridge) (string, error) {
	return fundingBookGuardExcept(ctx, tx, mode, offer, bridges, "")
}

func fundingBookGuardExcept(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer,
	bridges []policy.Bridge, excludeBridgeID string) (string, error) {
	var openBridge, otherOffer, everFundedPayment, terminalConflict, heldBridge bool
	err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND customer_id=$2
			AND ($6='' OR id::text<>$6)
			AND lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REOPENED_HELD')),
		EXISTS(SELECT 1 FROM servicing.offers WHERE mode=$1 AND customer_id=$2 AND id<>$3
			AND state NOT IN ('SUPERSEDED','EXPIRED','TERMINATED','CONSUMED')),
		EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND canonical_account_id=$4 AND transaction_id=$5 AND ever_funded),
		EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND canonical_account_id=$4 AND transaction_id=$5
			AND 'TERMINAL_PROVIDER_CONFLICT'=ANY(hold_reasons)),
		EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND customer_id=$2
			AND ($6='' OR id::text<>$6)
			AND lifecycle IN ('ACTIVE','COLLECTING','REOPENED_HELD')
			AND (cardinality(hold_reasons)>0 OR backing_deficit_cents>0))`,
		mode, offer.customerID, offer.id, offer.accountID, offer.transactionID, excludeBridgeID).Scan(
		&openBridge, &otherOffer, &everFundedPayment, &terminalConflict, &heldBridge)
	if err != nil {
		return "", errors.New("funding_book_guard_read_failed")
	}
	if everFundedPayment {
		return "PAYMENT_ALREADY_FUNDED", nil
	}
	if terminalConflict {
		return "PAYMENT_RECONCILIATION_REQUIRED", nil
	}
	if openBridge || otherOffer {
		return "CUSTOMER_OPEN_BRIDGE", nil
	}
	if heldBridge {
		return "SERVICING_HOLD_ACTIVE", nil
	}
	for _, bridge := range bridges {
		if bridge.Status == "DEFAULTED" && (bridge.PrincipalOwedCents > 0 || bridge.FeesOwedCents > 0) {
			return "CUSTOMER_DEFAULTED", nil
		}
	}
	return "", nil
}

func fundingPolicyBlock(result policy.Result, offer fundingOffer, request FundingGuard) string {
	if result.PolicyID != request.Evaluator.PolicyID() || result.PolicyHash != request.Evaluator.PolicyHash() || result.FundingAuthorized || !result.RequiresRecheck {
		return "POLICY_RESULT_BINDING_INVALID"
	}
	if result.PrincipalCents == nil || *result.PrincipalCents != offer.principal || result.EstimatedFeeCents == nil || *result.EstimatedFeeCents != offer.contractFee {
		return "TERMS_POLICY_PRICE_MISMATCH"
	}
	seen := make(map[policy.RuleID]policy.RuleStatus, len(result.Rules))
	for _, rule := range result.Rules {
		if _, duplicate := seen[rule.RuleID]; duplicate {
			return "POLICY_RULE_TRACE_INVALID"
		}
		seen[rule.RuleID] = rule.Status
	}
	ids := []policy.RuleID{policy.RuleInputFresh, policy.RuleCustomerActive, policy.RuleHistorySufficient,
		policy.RuleNoOverdueOrDefault, policy.RuleCustomerCapacity, policy.RulePaymentNotBridged,
		policy.RuleReceivableCoverage, policy.RuleCapitalAvailable, policy.RuleSpendAllowed, policy.RuleRegimeTierAllowed}
	if len(seen) != len(ids) {
		return "POLICY_RULE_TRACE_INVALID"
	}
	for _, id := range ids {
		if seen[id] != policy.RulePass {
			if seen[id] == policy.RuleFail {
				return "POLICY_HARD_RULE_FAILED"
			}
			return "POLICY_REVIEW_REQUIRED"
		}
	}
	if result.Status != policy.DecisionEligible && result.Status != policy.DecisionReviewRequired {
		return "POLICY_HARD_RULE_FAILED"
	}
	if result.Status == policy.DecisionEligible && result.ApprovalRequired ||
		result.Status == policy.DecisionReviewRequired && !result.ApprovalRequired {
		return "POLICY_RESULT_BINDING_INVALID"
	}
	return ""
}

func hasHumanApproval(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer) (bool, error) {
	var approved bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.actor_decisions WHERE mode=$1 AND offer_id=$2
		AND offer_revision=$3 AND terms_hash=$4 AND decision='APPROVE' AND actor_type='OPERATOR')`, mode, offer.id, offer.revision, offer.termsHash).Scan(&approved)
	if err != nil {
		return false, errors.New("funding_approval_read_failed")
	}
	return approved, nil
}

type systemFundingActor struct{ id, subject string }

func readSystemFundingActor(ctx context.Context, tx pgx.Tx, mode string) (systemFundingActor, error) {
	if mode != "DEMO" && mode != "SANDBOX" {
		return systemFundingActor{}, errors.New("funding_system_actor_unavailable")
	}
	var rows int
	var actor systemFundingActor
	err := tx.QueryRow(ctx, `SELECT count(*),min(actor_id),min(authentication_subject)
		FROM servicing.actors WHERE mode=$1 AND actor_type='SYSTEM' AND active`, mode).Scan(&rows, &actor.id, &actor.subject)
	if err != nil || rows != 1 || actor.id == "" || actor.subject == "" {
		return systemFundingActor{}, errors.New("funding_system_actor_unavailable")
	}
	return actor, nil
}

func createFundingReservation(ctx context.Context, tx pgx.Tx, request FundingGuard, offer fundingOffer, facility fundingFacility,
	customer fundingCustomer, input policy.Input, result policy.Result, bridgeID, reservationID, operationID, checkID,
	approval string, systemActor systemFundingActor, allocations []fundingAllocation, now time.Time) error {
	principal, fee := offer.principal, offer.contractFee
	if principal <= 0 || fee < 0 || principal > math.MaxInt64-fee {
		return errors.New("funding_amount_invalid")
	}
	if _, ok := checkedFundingAdd(facility.reservedPrincipal, principal); !ok {
		return errors.New("funding_facility_reservation_overflow")
	}
	if _, ok := checkedFundingAdd(customer.reservedPrincipal, principal); !ok {
		return errors.New("funding_customer_reservation_overflow")
	}
	if _, ok := checkedFundingAdd(customer.reservedFees, fee); !ok {
		return errors.New("funding_customer_reservation_overflow")
	}
	if request.KnownSourceFeeCents == nil || *request.KnownSourceFeeCents < 0 || request.SourceFeeContractID == "" {
		return errors.New("funding_source_fee_unknown")
	}
	reservationExpiry := now.Add(2 * time.Minute)
	if offer.expiresAt.Before(reservationExpiry) {
		reservationExpiry = offer.expiresAt
	}
	if _, err := tx.Exec(ctx, `UPDATE servicing.facilities SET reserved_principal_cents=reserved_principal_cents+$3,
		version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, request.Mode, request.FacilityID, principal); err != nil {
		return errors.New("funding_facility_reservation_failed")
	}
	if _, err := tx.Exec(ctx, `UPDATE servicing.customers SET reserved_principal_cents=reserved_principal_cents+$3,
		reserved_fee_cents=reserved_fee_cents+$4,version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`,
		request.Mode, offer.customerID, principal, fee); err != nil {
		return errors.New("funding_customer_reservation_failed")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.bridges(mode,id,facility_id,customer_id,offer_id,offer_revision,
		canonical_account_id,transaction_id,terms_hash,lifecycle,ever_funded,principal_original_cents,
		contract_fee_original_cents,due_on,originating_case_id,evaluation_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'RESERVED',false,$10,$11,$12,$13,$14)`, request.Mode, bridgeID,
		request.FacilityID, offer.customerID, offer.id, offer.revision, offer.accountID, offer.transactionID,
		offer.termsHash, principal, fee, offer.dueOn, nullableFundingString(offer.caseID), offer.evaluationID); err != nil {
		return errors.New("funding_bridge_insert_failed")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.reservations(mode,id,facility_id,customer_id,bridge_id,state,
		facility_principal_cents,customer_principal_cents,customer_fee_cents,invoice_coverage_cents,expires_at)
		VALUES($1,$2,$3,$4,$5,'NEVER_SENT',$6,$6,$7,$8,$9)`, request.Mode, reservationID,
		request.FacilityID, offer.customerID, bridgeID, principal, fee, principal+fee, reservationExpiry); err != nil {
		return errors.New("funding_reservation_insert_failed")
	}
	for index, allocation := range allocations {
		tag, err := tx.Exec(ctx, `UPDATE servicing.receivables SET reserved_cents=reserved_cents+$4,version=version+1
			WHERE mode=$1 AND source_system=$2 AND invoice_id=$3 AND reserved_cents<=$5::bigint-$4::bigint`, request.Mode,
			allocation.sourceSystem, allocation.invoiceID, allocation.amount, allocation.unpaid)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.New("funding_invoice_reservation_failed")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.bridge_invoice_allocations(mode,bridge_id,source_system,invoice_id,
			allocation_index,original_coverage_cents,active_cents) VALUES($1,$2,$3,$4,$5,$6,$6)`, request.Mode,
			bridgeID, allocation.sourceSystem, allocation.invoiceID, index, allocation.amount); err != nil {
			return errors.New("funding_invoice_allocation_insert_failed")
		}
	}
	requestID := "bridge-" + bridgeID
	operation := provider.Operation{ID: operationID, Kind: provider.OperationFund, Mode: request.Mode,
		SourceAccountRef: facility.fundingAccount, DestinationAccountRef: offer.accountID,
		AmountCents: principal, Currency: "USD", RequestID: requestID, BridgeID: bridgeID}
	operation.PayloadHash, _ = provider.PayloadHash(operation)
	if operation.PayloadHash == "" {
		return errors.New("funding_operation_payload_invalid")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.operations(mode,id,bridge_id,reservation_id,kind,state,version,
		request_id,payload_hash,source_account_ref,destination_account_ref,amount_cents,currency,terms_hash,next_check_at,reserved_provider_fee_cents,last_funding_check_id)
		VALUES($1,$2,$3,$4,'FUND','QUEUED',1,$5,$6,$7,$8,$9,'USD',$10,$11,$12,$13)`, request.Mode, operationID,
		bridgeID, reservationID, requestID, operation.PayloadHash, facility.fundingAccount, offer.accountID,
		principal, offer.termsHash, now, *request.KnownSourceFeeCents, checkID); err != nil {
		return errors.New("funding_operation_insert_failed")
	}
	if err := AppendOperationJournal(ctx, tx, domain.Mode(request.Mode), operationID, "INTENT_CREATED", "", "QUEUED",
		"", nil, nil, nil, map[string]any{"request_id": requestID, "payload_hash": operation.PayloadHash,
			"bridge_id": bridgeID, "reservation_id": reservationID}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE servicing.offers SET state='CONSUMED',version=version+1 WHERE mode=$1 AND id=$2`, request.Mode, offer.id); err != nil {
		return errors.New("funding_offer_consume_failed")
	}
	if approval == "AUTO_APPROVE" {
		decisionRefs, err := json.Marshal(append(append([]policy.SourceReference(nil), input.Customer.SourceRefs...), input.Facility.SourceRefs...))
		if err != nil {
			return errors.New("funding_decision_sources_invalid")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.actor_decisions(mode,offer_id,offer_revision,terms_hash,
			actor_id,actor_type,decision,reason_code,recorded_at,authentication_subject,disclosures_version,
			source_refs,funding_check_id) VALUES($1,$2,$3,$4,$5,'SYSTEM','AUTO_APPROVE',
			'FRESH_ALL_HARD_RULES_PASS',$6,$7,$8,$9::jsonb,$10)`, request.Mode, offer.id, offer.revision,
			offer.termsHash, systemActor.id, now, systemActor.subject, offer.terms.DisclosuresVersion,
			string(decisionRefs), checkID); err != nil {
			return errors.New("funding_auto_approval_insert_failed")
		}
	}
	sourceHold, ok := checkedFundingAdd(principal, *request.KnownSourceFeeCents)
	if !ok {
		return errors.New("funding_source_liability_overflow")
	}
	coverage, _ := json.Marshal(map[string]any{"funding_principal_cents": principal, "customer_total_cents": principal + fee,
		"invoice_coverage_cents": principal + fee, "source_liquidity_hold_cents": sourceHold,
		"source_fee_contract_id": request.SourceFeeContractID, "policy_check_id": checkID})
	bridgeRef, operationRef := bridgeID, operationID
	lines := []domain.LedgerLine{{Account: "COMMITMENT_MEMO", Debit: domain.Money(sourceHold)},
		{Account: "COMMITMENT_MEMO", Credit: domain.Money(sourceHold)}}
	if err := InsertLedgerTransaction(ctx, tx, domain.Mode(request.Mode), "fund-reservation:"+reservationID,
		"FUNDING_RESERVATION", &bridgeRef, &operationRef, now.Format(time.RFC3339Nano), coverage, lines); err != nil {
		return err
	}
	bridgeVersion := int64(1)
	if err := appendFundingBridgeChange(ctx, tx, request.Mode, offer, bridgeID, reservationID, operationID, checkID,
		bridgeVersion, input, "FUNDING_RESERVED", "RESERVED"); err != nil {
		return err
	}
	if err := appendFundingOperationChange(ctx, tx, request.Mode, offer, bridgeID, operationID, reservationID, checkID,
		bridgeVersion, 1, input, "FUNDING_OPERATION_QUEUED", "QUEUED"); err != nil {
		return err
	}
	if err := appendFundingOfferChange(ctx, tx, request.Mode, offer, bridgeID, bridgeVersion,
		reservationID, operationID, checkID, input, "CONSUMED"); err != nil {
		return err
	}
	_ = customer
	_ = result
	return nil
}

type fundingAllocation struct {
	sourceSystem, invoiceID string
	amount, unpaid          int64
}

func insertFundingCheck(ctx context.Context, tx pgx.Tx, request FundingGuard, offer fundingOffer, input policy.Input,
	result policy.Result, facility fundingFacility, allocations []fundingAllocation, blockedCode string) (string, error) {
	if request.ActionKind != "" && request.ActionKind != "FIRST_SEND" {
		return "", errors.New("funding_check_action_invalid")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", errors.New("funding_check_input_encode_failed")
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return "", errors.New("funding_check_result_encode_failed")
	}
	refs := append(append([]policy.SourceReference(nil), input.Customer.SourceRefs...), input.Facility.SourceRefs...)
	offerRefs, err := decodePolicyRefs(offer.sourceRefs)
	if err != nil {
		return "", errors.New("funding_check_offer_sources_invalid")
	}
	refs = appendUniquePolicyRefs(refs, offerRefs...)
	refsJSON, err := json.Marshal(refs)
	if err != nil {
		return "", errors.New("funding_check_sources_encode_failed")
	}
	type allocationSnapshot struct {
		SourceSystem        string `json:"source_system"`
		InvoiceID           string `json:"invoice_id"`
		AmountCents         int64  `json:"amount_cents"`
		EligibleUnpaidCents int64  `json:"eligible_unpaid_cents"`
	}
	allocationRows := make([]allocationSnapshot, 0, len(allocations))
	for _, allocation := range allocations {
		allocationRows = append(allocationRows, allocationSnapshot{SourceSystem: allocation.sourceSystem,
			InvoiceID: allocation.invoiceID, AmountCents: allocation.amount, EligibleUnpaidCents: allocation.unpaid})
	}
	allocationJSON, err := json.Marshal(allocationRows)
	if err != nil {
		return "", errors.New("funding_check_allocations_encode_failed")
	}
	var actionEvidence map[string]any
	if request.ActionKind == "FIRST_SEND" {
		if request.ActionID == "" {
			return "", errors.New("funding_check_action_invalid")
		}
		actionEvidence = map[string]any{"worker_action": request.ActionKind,
			"worker_actor_type": "SYSTEM_WORKER", "worker_actor_id": "funding-collection-worker",
			"operation_id": request.ActionID, "claim_version": request.ActionVersion}
	} else {
		actionEvidence = map[string]any{"command": map[string]string{
			"mode": request.Mode, "principal": request.Principal, "method": request.HTTPMethod,
			"resource_path": request.ResourcePath, "idempotency_key": request.IdempotencyKey,
			"request_hash": request.RequestHash}}
	}
	actionEvidence["saved_evaluation"] = request.Assessment
	actionEvidence["payment"] = request.Payment
	actionEvidence["borrower_balance"] = request.BorrowerBalance
	actionEvidence["facility_balance"] = request.FacilityBalance
	actionEvidence["saved_shortfall"] = request.SavedShortfall
	actionEvidence["facility_source_id"] = facility.sourceID
	actionEvidence["facility_source_version"] = facility.sourceVersion
	actionEvidence["facility_captured_at"] = facility.capturedAt
	actionEvidence["known_source_fee_cents"] = request.KnownSourceFeeCents
	actionEvidence["source_fee_contract_id"] = request.SourceFeeContractID
	if request.ActionKind == "FIRST_SEND" {
		actionEvidence["initial_source_fee_cents"] = request.InitialSourceFeeCents
		actionEvidence["initial_source_fee_contract_id"] = request.InitialSourceFeeContractID
		actionEvidence["prior_funding_check_id"] = request.LastFundingCheckID
	}
	actionEvidence["other_unresolved_source_liability_cents"] = request.OtherSourceLiabilityCents
	observationsJSON, err := json.Marshal(actionEvidence)
	if err != nil {
		return "", errors.New("funding_check_observations_encode_failed")
	}
	inputHash, resultHash := fundingSHA256(inputJSON), fundingSHA256(resultJSON)
	passed := blockedCode == ""
	var blocked any
	if !passed {
		blocked = blockedCode
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO servicing.funding_checks(mode,offer_id,offer_revision,terms_hash,policy_id,policy_hash,
		customer_context_id,customer_context_version,facility_source_id,facility_source_version,shortfall_id,
		payment_observed_at,borrower_balance_observed_at,facility_balance_observed_at,source_refs,invoice_allocations,provider_observations,
		policy_input,policy_result,input_hash,result_hash,passed,blocked_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16::jsonb,$17::jsonb,$18::jsonb,$19::jsonb,$20,$21,$22,$23)
		RETURNING id::text`, request.Mode, offer.id, offer.revision, offer.termsHash, result.PolicyID, result.PolicyHash,
		request.CustomerContext.SnapshotID, request.CustomerContext.SnapshotVersion, facility.sourceID, facility.sourceVersion,
		request.SavedShortfall.ID, request.Payment.ObservedAt, request.BorrowerBalance.ObservedAt,
		request.FacilityBalance.ObservedAt, string(refsJSON), string(allocationJSON), string(observationsJSON), string(inputJSON), string(resultJSON),
		inputHash, resultHash, passed, blocked).Scan(&id)
	if err != nil {
		return "", errors.New("funding_check_insert_failed")
	}
	return id, nil
}

func persistFundingBlock(ctx context.Context, tx pgx.Tx, request FundingGuard, status int, code, message string) (FundingOutcome, error) {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	if err := insertFundingCommand(ctx, tx, request, status, body, request.OfferID); err != nil {
		return FundingOutcome{}, err
	}
	return FundingOutcome{Status: status, Body: body}, nil
}

func insertFundingCommand(ctx context.Context, tx pgx.Tx, request FundingGuard, status int, body []byte, resourceID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.commands(mode,principal,http_method,resource_path,idempotency_key,
		request_hash,response_status,response_body,resource_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)`,
		request.Mode, request.Principal, request.HTTPMethod, request.ResourcePath, request.IdempotencyKey,
		request.RequestHash, status, string(body), resourceID); err != nil {
		return errors.New("funding_command_receipt_write_failed")
	}
	return nil
}

// InsertFundingCommand stores a deterministic idempotency receipt before or after financial locks.
func InsertFundingCommand(ctx context.Context, tx pgx.Tx, request FundingGuard, status int, body []byte, resourceID string) error {
	return insertFundingCommand(ctx, tx, request, status, body, resourceID)
}

func validateCurrentAuthority(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer, facility fundingFacility, clock func() time.Time) (time.Time, error) {
	type authorityRow struct {
		id, source, destination, currency, status, disclosures string
		version, maximum, consumed, reserved                   int64
		kinds                                                  []string
		validFrom, expires                                     time.Time
	}
	rows, err := tx.Query(ctx, `SELECT id,version,source_account_ref,destination_account_ref,currency,status,
		disclosures_version,permitted_kinds,valid_from,expires_at,maximum_debit_cents,consumed_debit_cents,reserved_debit_cents
		FROM servicing.authorities WHERE mode=$1 AND customer_id=$2 ORDER BY id,version DESC FOR SHARE`, mode, offer.customerID)
	if err != nil {
		return time.Time{}, errors.New("funding_authority_read_failed")
	}
	var current []authorityRow
	seen := make(map[string]bool)
	for rows.Next() {
		var record authorityRow
		if rows.Scan(&record.id, &record.version, &record.source, &record.destination, &record.currency,
			&record.status, &record.disclosures, &record.kinds, &record.validFrom, &record.expires,
			&record.maximum, &record.consumed, &record.reserved) != nil {
			rows.Close()
			return time.Time{}, errors.New("funding_authority_read_failed")
		}
		if seen[record.id] {
			continue
		}
		seen[record.id] = true
		current = append(current, record)
	}
	if rows.Err() != nil {
		rows.Close()
		return time.Time{}, errors.New("funding_authority_read_failed")
	}
	rows.Close()
	now := clock().UTC()
	if now.IsZero() {
		return time.Time{}, errors.New("funding_clock_invalid")
	}
	for index := range current {
		consumed, reserved, usageErr := authorityUsage(ctx, tx, domain.Mode(mode), current[index].id)
		if usageErr != nil {
			return now, errors.New("funding_authority_ambiguous_or_invalid")
		}
		current[index].consumed, current[index].reserved = consumed, reserved
	}
	activeMatches, boundCurrent := 0, false
	for _, record := range current {
		if record.id == offer.terms.CollectionAuthorityID && record.version == offer.terms.CollectionAuthorityVersion {
			boundCurrent = true
		}
		if record.status != "ACTIVE" || record.validFrom.After(now) || !now.Before(record.expires) {
			continue
		}
		if record.source != offer.accountID || record.destination != facility.fundingAccount || record.currency != "USD" ||
			!fundingHasKind(record.kinds, "COLLECT_FEE") || !fundingHasKind(record.kinds, "COLLECT_PRINCIPAL") ||
			record.consumed < 0 || record.reserved < 0 || record.reserved > record.maximum ||
			record.consumed >= record.maximum-record.reserved {
			return now, errors.New("funding_authority_ambiguous_or_invalid")
		}
		activeMatches++
		if record.id != offer.terms.CollectionAuthorityID || record.version != offer.terms.CollectionAuthorityVersion ||
			record.disclosures != offer.terms.DisclosuresVersion {
			return now, errors.New("funding_authority_superseded")
		}
	}
	if activeMatches != 1 || !boundCurrent {
		return now, errors.New("funding_authority_superseded")
	}
	return now, nil
}

func requireMatchingAcceptance(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer) error {
	var accepted bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.actor_decisions WHERE mode=$1 AND offer_id=$2
		AND offer_revision=$3 AND terms_hash=$4 AND decision='ACCEPT' AND actor_type='CUSTOMER')`,
		mode, offer.id, offer.revision, offer.termsHash).Scan(&accepted); err != nil {
		return errors.New("funding_acceptance_read_failed")
	}
	if !accepted {
		return errors.New("funding_acceptance_missing")
	}
	return nil
}

func fundingResourceSummary(offer fundingOffer, bridgeID, reservationID, operationID, checkID string,
	bridgeVersion int64, input policy.Input, status string) []byte {
	var caseID any
	if offer.caseID != "" {
		caseID = offer.caseID
	}
	refs, _ := json.Marshal(append(append([]policy.SourceReference(nil), input.Customer.SourceRefs...), input.Facility.SourceRefs...))
	summary, _ := json.Marshal(map[string]any{"customer_id": offer.customerID,
		"account_ref": "customer:" + offer.customerID, "originating_case_id": caseID,
		"evaluation_id": offer.evaluationID, "offer_id": offer.id, "bridge_id": bridgeID,
		"bridge_version": bridgeVersion, "reservation_id": reservationID, "operation_id": operationID,
		"funding_check_id": checkID, "status": status, "terms_hash": offer.termsHash,
		"evidence_refs": json.RawMessage(refs)})
	return summary
}

func appendFundingBridgeChange(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer, bridgeID,
	reservationID, operationID, checkID string, version int64, input policy.Input, change, status string) error {
	summary := fundingResourceSummary(offer, bridgeID, reservationID, operationID, checkID, version, input, status)
	bridgeRef := bridgeID
	_, err := AppendChange(ctx, tx, domain.Change{Mode: domain.Mode(mode), ResourceType: "BRIDGE", ResourceID: bridgeID,
		ResourceVersion: version, ChangeType: change, Summary: summary}, &bridgeRef, &version)
	return err
}

func appendFundingOperationChange(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer, bridgeID,
	operationID, reservationID, checkID string, bridgeVersion, version int64, input policy.Input, change, status string) error {
	summary := fundingResourceSummary(offer, bridgeID, reservationID, operationID, checkID, bridgeVersion, input, status)
	bridgeRef := bridgeID
	_, err := AppendChange(ctx, tx, domain.Change{Mode: domain.Mode(mode), ResourceType: "OPERATION", ResourceID: operationID,
		ResourceVersion: version, ChangeType: change, Summary: summary}, &bridgeRef, &bridgeVersion)
	return err
}

func appendFundingOfferChange(ctx context.Context, tx pgx.Tx, mode string, offer fundingOffer, bridgeID string,
	bridgeVersion int64, reservationID, operationID, checkID string, input policy.Input, state string) error {
	version := offer.version + 1
	summary := fundingResourceSummary(offer, bridgeID, reservationID, operationID, checkID, bridgeVersion, input, state)
	bridgeRef := bridgeID
	_, err := AppendChange(ctx, tx, domain.Change{Mode: domain.Mode(mode), ResourceType: "OFFER", ResourceID: offer.id,
		ResourceVersion: version, ChangeType: "OFFER_CONSUMED", Summary: summary}, &bridgeRef, &bridgeVersion)
	return err
}

func decodePolicyRefs(raw []byte) ([]policy.SourceReference, error) {
	var refs []policy.SourceReference
	if len(raw) == 0 || json.Unmarshal(raw, &refs) != nil {
		return nil, errors.New("funding_source_refs_invalid")
	}
	if len(refs) == 0 {
		return nil, errors.New("funding_source_refs_invalid")
	}
	for _, ref := range refs {
		if ref.System == "" || ref.RecordID == "" || ref.Version == "" || ref.EffectiveAt.IsZero() {
			return nil, errors.New("funding_source_refs_invalid")
		}
	}
	return refs, nil
}

func appendUniquePolicyRefs(refs []policy.SourceReference, additions ...policy.SourceReference) []policy.SourceReference {
	seen := make(map[string]bool, len(refs)+len(additions))
	result := make([]policy.SourceReference, 0, len(refs)+len(additions))
	for _, ref := range append(refs, additions...) {
		key := ref.System + "\x00" + ref.RecordID + "\x00" + ref.Version + "\x00" + ref.EffectiveAt.UTC().Format(time.RFC3339Nano)
		if !seen[key] {
			seen[key] = true
			result = append(result, ref)
		}
	}
	return result
}

func fundingAllocations(input []policy.Receivable, invoiceSources map[string]string, terms domain.Terms, now, dueOn time.Time, required int64) ([]fundingAllocation, error) {
	start, end := now.UTC().Truncate(24*time.Hour), dueOn.UTC().Truncate(24*time.Hour)
	type invoiceCandidate struct {
		invoice      policy.Receivable
		due          time.Time
		sourceSystem string
	}
	var candidates []invoiceCandidate
	for _, invoice := range input {
		if invoice.DueOn == nil || !invoice.DueDateVerified || !invoice.AllocationComplete || invoice.Currency != "USD" ||
			invoice.AllocatedCents < 0 || invoice.AllocatedCents > invoice.EligibleUnpaidCents {
			continue
		}
		due, err := time.Parse("2006-01-02", string(*invoice.DueOn))
		if err != nil || due.Before(start) || due.After(end) {
			continue
		}
		sourceSystem := invoiceSources[invoice.InvoiceID]
		if sourceSystem == "" {
			return nil, errors.New("funding_invoice_source_missing")
		}
		candidates = append(candidates, invoiceCandidate{invoice: invoice, due: due, sourceSystem: sourceSystem})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].due.Equal(candidates[j].due) {
			return candidates[i].due.Before(candidates[j].due)
		}
		if candidates[i].sourceSystem != candidates[j].sourceSystem {
			return candidates[i].sourceSystem < candidates[j].sourceSystem
		}
		return candidates[i].invoice.InvoiceID < candidates[j].invoice.InvoiceID
	})
	remaining := required
	var allocations []fundingAllocation
	for _, item := range candidates {
		free := item.invoice.EligibleUnpaidCents - item.invoice.AllocatedCents
		if free <= 0 {
			continue
		}
		amount := free
		if amount > remaining {
			amount = remaining
		}
		allocations = append(allocations, fundingAllocation{sourceSystem: item.sourceSystem, invoiceID: item.invoice.InvoiceID,
			amount: amount, unpaid: item.invoice.EligibleUnpaidCents})
		remaining -= amount
		if remaining == 0 {
			break
		}
	}
	if remaining != 0 || len(allocations) == 0 || terms.PrincipalCents <= 0 {
		return nil, errors.New("funding_invoice_coverage_insufficient")
	}
	return allocations, nil
}

func validateFundingAllocationBasis(result policy.Result, receivables []policy.Receivable, allocations []fundingAllocation,
	invoiceSources map[string]string, now, dueOn time.Time) bool {
	var allocated int64
	for _, item := range allocations {
		var ok bool
		allocated, ok = checkedFundingAdd(allocated, item.amount)
		if !ok {
			return false
		}
	}
	if result.EstimatedFeeCents == nil || result.PrincipalCents == nil || *result.PrincipalCents > math.MaxInt64-*result.EstimatedFeeCents {
		return false
	}
	var policyFree int64
	start, end := now.UTC().Truncate(24*time.Hour), dueOn.UTC().Truncate(24*time.Hour)
	for _, invoice := range receivables {
		if invoice.InvoiceID == "" || invoice.DueOn == nil || !invoice.DueDateVerified {
			return false
		}
		due, err := time.Parse("2006-01-02", string(*invoice.DueOn))
		if err != nil {
			return false
		}
		if due.Before(start) || due.After(end) {
			continue
		}
		if !invoice.AllocationComplete || invoice.Currency != "USD" || invoice.EligibleUnpaidCents < 0 ||
			invoice.AllocatedCents < 0 || invoice.AllocatedCents > invoice.EligibleUnpaidCents ||
			invoiceSources[invoice.InvoiceID] == "" {
			return false
		}
		free := invoice.EligibleUnpaidCents - invoice.AllocatedCents
		var ok bool
		policyFree, ok = checkedFundingAdd(policyFree, free)
		if !ok {
			return false
		}
	}
	var reportedFree int64
	foundCoverage := false
	for _, rule := range result.Rules {
		if rule.RuleID == policy.RuleReceivableCoverage {
			value, ok := rule.Actual["verified_free_receivables_cents"].(int64)
			if !ok {
				return false
			}
			reportedFree, foundCoverage = value, true
			break
		}
	}
	return foundCoverage && reportedFree == policyFree &&
		allocated == *result.PrincipalCents+*result.EstimatedFeeCents && policyFree >= allocated
}

func dbUUID(ctx context.Context, tx pgx.Tx) (string, error) {
	var id string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return "", errors.New("funding_identity_failed")
	}
	return id, nil
}

func validFundingHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func fundingSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func checkedFundingAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

func fundingHasKind(kinds []string, expected string) bool {
	for _, kind := range kinds {
		if kind == expected {
			return true
		}
	}
	return false
}

func nullableFundingString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
