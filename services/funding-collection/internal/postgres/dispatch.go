package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

// DispatchBindings are immutable operation and source bindings read before upstream I/O.
type DispatchBindings struct {
	Candidate              OperationCandidate
	OfferID                string
	CardRef                string
	ReservationID          string
	ReservationState       string
	ReservationExpiresAt   time.Time
	FacilityAccount        string
	AuthorizationID        string
	TransactionID          string
	BridgeEvaluationID     string
	OfferVersion           int64
	OfferRevision          int64
	OfferState             string
	OfferPolicyID          string
	OfferPolicyHash        string
	OfferTermsHash         string
	OfferEvaluationID      string
	OfferTermsJSON         []byte
	OfferSourceRefs        []byte
	OfferCandidateEvidence []byte
	OfferPrincipalCents    int64
	OfferContractFeeCents  int64
	OfferExpiresAt         time.Time
	OfferedOn              time.Time
	DueOn                  time.Time
	ReservedSourceFeeCents int64
	ProviderObjectID       *string
	LastFundingCheckID     string
	FeeContractID          string
	FeeQuoteCents          int64
	Operation              provider.Operation
	LeaseToken             string
	LeaseExpiresAt         time.Time
}

// DispatchIntent contains the immutable payload and fresh-check reference a
// later worker may send only after the surrounding transaction commits.
type DispatchIntent struct {
	Operation         provider.Operation
	Claim             OperationClaim
	FundingCheckID    string
	BridgeVersion     int64
	SourceFeeContract string
	SourceFeeCents    int64
}

// DispatchPreparation records a committed local result without performing I/O.
type DispatchPreparation struct {
	Intent         *DispatchIntent
	Cancelled      bool
	RetryScheduled bool
	FundingCheckID string
	BlockedCode    string
}

type ownDispatchBacking struct {
	Projection        underwriting.NeverSentReservation
	Allocations       []fundingAllocation
	CanProject        bool
	CurrentlyEligible bool
}

// LoadDispatchBindings re-reads immutable intent bindings without holding financial locks.
func (r *Repository) LoadDispatchBindings(ctx context.Context, claim OperationClaim) (DispatchBindings, error) {
	var result DispatchBindings
	var mode, kind, state, bridgeID, offerID, facilityID, customerID string
	var requestID, payloadHash, source, destination, currency, operationTermsHash string
	var amount, operationVersion, offerVersion, offerRevision int64
	var reservedFee *int64
	var providerObject, operationLease *string
	var operationLeaseExpiry *time.Time
	var checkID *string
	err := r.pool.QueryRow(ctx, `SELECT o.mode,o.id::text,o.kind,o.state,o.version,o.request_id,o.payload_hash,
		o.source_account_ref,o.destination_account_ref,o.amount_cents,o.currency,o.terms_hash,
		o.reserved_provider_fee_cents,o.provider_object_id,o.lease_token::text,o.lease_expires_at,
		b.facility_id,b.customer_id,b.id::text,b.offer_id::text,
		r.id::text,r.state,r.expires_at,f.funding_account_ref,
		ofr.version,ofr.revision,ofr.state,ofr.policy_id,ofr.policy_hash,ofr.terms_hash,ofr.evaluation_id,ofr.authorization_event_id,
		ofr.terms_json,ofr.source_refs,ofr.candidate_evidence,ofr.principal_cents,ofr.contract_fee_cents,
		ofr.expires_at,ofr.offered_on,ofr.due_on,o.last_funding_check_id
		FROM servicing.operations o
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		JOIN servicing.reservations r ON r.mode=o.mode AND r.id=o.reservation_id
		JOIN servicing.facilities f ON f.mode=b.mode AND f.id=b.facility_id
		JOIN servicing.offers ofr ON ofr.mode=b.mode AND ofr.id=b.offer_id
		WHERE o.mode=$1 AND o.id=$2 AND o.state='QUEUED' AND o.version=$3
		AND o.lease_token=$4::uuid AND o.lease_expires_at>clock_timestamp()`,
		string(claim.Mode), claim.OperationID, claim.Version, claim.LeaseToken).Scan(
		&mode, &result.Candidate.OperationID, &kind, &state, &operationVersion, &requestID, &payloadHash,
		&source, &destination, &amount, &currency, &operationTermsHash, &reservedFee, &providerObject,
		&operationLease, &operationLeaseExpiry, &facilityID, &customerID, &bridgeID, &offerID,
		&result.ReservationID, &result.ReservationState, &result.ReservationExpiresAt, &result.FacilityAccount,
		&offerVersion, &offerRevision, &result.OfferState, &result.OfferPolicyID, &result.OfferPolicyHash,
		&result.OfferTermsHash, &result.OfferEvaluationID, &result.AuthorizationID, &result.OfferTermsJSON, &result.OfferSourceRefs,
		&result.OfferCandidateEvidence, &result.OfferPrincipalCents, &result.OfferContractFeeCents,
		&result.OfferExpiresAt, &result.OfferedOn, &result.DueOn, &checkID)
	if err != nil {
		return DispatchBindings{}, ErrOperationClaimStale
	}
	// Re-read the bridge columns into their typed destinations separately. The
	// operation's saved account references remain the provider payload authority.
	var canonicalAccount, transactionID, bridgeEvaluationID, lifecycle string
	var everFunded bool
	var bridgePrincipal, bridgeFee int64
	var bridgeTermsHash string
	if err := r.pool.QueryRow(ctx, `SELECT canonical_account_id,transaction_id,evaluation_id,lifecycle,
		ever_funded,principal_original_cents,contract_fee_original_cents,terms_hash
		FROM servicing.bridges WHERE mode=$1 AND id=$2`, mode, bridgeID).Scan(&canonicalAccount,
		&transactionID, &bridgeEvaluationID, &lifecycle, &everFunded, &bridgePrincipal, &bridgeFee, &bridgeTermsHash); err != nil {
		return DispatchBindings{}, errors.New("dispatch_bridge_binding_unavailable")
	}
	result.TransactionID, result.BridgeEvaluationID = transactionID, bridgeEvaluationID
	if mode != string(claim.Mode) || operationVersion != claim.Version || state != string(claim.State) ||
		kind != "FUND" || claim.Kind != "FUND" || bridgeID != claim.BridgeID || facilityID != claim.FacilityID ||
		customerID != claim.CustomerID || offerID == "" || result.ReservationState != "NEVER_SENT" ||
		result.OfferState != "CONSUMED" || providerObject != nil || checkID == nil || *checkID == "" ||
		reservedFee == nil || *reservedFee < 0 || source != result.FacilityAccount || destination != canonicalAccount ||
		currency != "USD" || amount != result.OfferPrincipalCents || operationTermsHash != result.OfferTermsHash ||
		result.OfferEvaluationID != bridgeEvaluationID || transactionID == "" || result.AuthorizationID == "" || lifecycle != "RESERVED" || everFunded ||
		bridgePrincipal != amount || bridgeFee != result.OfferContractFeeCents || bridgeTermsHash != operationTermsHash {
		return DispatchBindings{}, errors.New("dispatch_intent_binding_invalid")
	}
	var terms domain.Terms
	if json.Unmarshal(result.OfferTermsJSON, &terms) != nil || terms.Validate() != nil {
		return DispatchBindings{}, errors.New("dispatch_terms_binding_invalid")
	}
	termsHash, termsErr := terms.Hash()
	if termsErr != nil ||
		termsHash != result.OfferTermsHash || terms.CustomerID != customerID ||
		terms.FacilityID != facilityID || terms.BorrowerAccountRef != destination ||
		terms.TransactionID != transactionID || terms.PrincipalCents != amount ||
		terms.ContractFeeCents != result.OfferContractFeeCents || terms.PolicyID != result.OfferPolicyID ||
		terms.PolicyHash != result.OfferPolicyHash {
		return DispatchBindings{}, errors.New("dispatch_terms_binding_invalid")
	}
	result.Candidate = OperationCandidate{Mode: domain.Mode(mode), OperationID: claim.OperationID,
		FacilityID: facilityID, CustomerID: customerID, BridgeID: bridgeID, Kind: kind,
		State: domain.OperationQueued, Version: operationVersion}
	result.OfferVersion, result.OfferRevision = offerVersion, offerRevision
	result.OfferID = offerID
	result.CardRef = terms.CardRef
	result.LastFundingCheckID = *checkID
	result.ReservedSourceFeeCents = *reservedFee
	result.ProviderObjectID = providerObject
	if operationLease == nil || operationLeaseExpiry == nil || *operationLease != claim.LeaseToken ||
		!operationLeaseExpiry.Equal(claim.LeaseExpiresAt) {
		return DispatchBindings{}, ErrOperationClaimStale
	}
	result.LeaseToken, result.LeaseExpiresAt = *operationLease, *operationLeaseExpiry
	result.Operation = provider.Operation{ID: claim.OperationID, Kind: provider.OperationFund, Mode: mode,
		SourceAccountRef: source, DestinationAccountRef: destination, AmountCents: amount,
		Currency: currency, RequestID: requestID, PayloadHash: payloadHash, BridgeID: bridgeID,
		TriggerID: "", PrincipalCents: 0, ContractFeeCents: 0, LateFeeCents: 0}
	computedHash, err := provider.PayloadHash(result.Operation)
	if err != nil || computedHash != payloadHash {
		return DispatchBindings{}, errors.New("dispatch_payload_hash_invalid")
	}
	var sourceFeeContractID string
	var initialFeeCents int64
	if err := r.pool.QueryRow(ctx, `SELECT provider_observations->>'source_fee_contract_id',
		(provider_observations->>'known_source_fee_cents')::bigint
		FROM servicing.funding_checks WHERE mode=$1 AND id=$2 AND offer_id=$3 AND offer_revision=$4
		AND terms_hash=$5 AND passed`, mode, *checkID, offerID, offerRevision, result.OfferTermsHash).Scan(
		&sourceFeeContractID, &initialFeeCents); err != nil || sourceFeeContractID == "" || initialFeeCents != *reservedFee {
		return DispatchBindings{}, errors.New("dispatch_initial_fee_binding_invalid")
	}
	result.FeeContractID, result.FeeQuoteCents = sourceFeeContractID, initialFeeCents
	return result, nil
}

// PrepareFundingDispatch commits the final first-send authorization before it
// returns a payload. It never performs provider I/O.
func (r *Repository) PrepareFundingDispatch(ctx context.Context, claim OperationClaim,
	request FundingGuard, clock func() time.Time) (DispatchPreparation, error) {
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return DispatchPreparation{}, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	prepared, err := r.PrepareFundingDispatchTx(txCtx, tx, claim, request, clock)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if err := tx.Commit(txCtx); err != nil {
		return DispatchPreparation{}, errors.New("dispatch_prepare_commit_failed")
	}
	if prepared.Intent != nil {
		if err := r.verifyPreparedDispatchLease(txCtx, *prepared.Intent); err != nil {
			return DispatchPreparation{}, err
		}
	}
	return prepared, nil
}

func (r *Repository) verifyPreparedDispatchLease(ctx context.Context, intent DispatchIntent) error {
	var current bool
	err := r.pool.QueryRow(ctx, `SELECT o.state='DISPATCHING' AND o.version=$3
		AND o.lease_token=$4::uuid AND o.lease_expires_at=$5
		AND o.lease_expires_at>clock_timestamp()+interval '10 seconds'
		AND o.last_funding_check_id=$6 AND r.state='DISPATCHED' AND b.lifecycle='FUNDING_PENDING'
		FROM servicing.operations o
		JOIN servicing.reservations r ON r.mode=o.mode AND r.id=o.reservation_id
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND o.id=$2`, string(intent.Claim.Mode), intent.Claim.OperationID,
		intent.Claim.Version, intent.Claim.LeaseToken, intent.Claim.LeaseExpiresAt, intent.FundingCheckID).Scan(&current)
	if err != nil || !current {
		return errors.New("dispatch_commit_lease_unverified")
	}
	return nil
}

// CheckPreparedDispatchLease supplies the final database-clock gate immediately before a POST.
func (r *Repository) CheckPreparedDispatchLease(ctx context.Context, intent DispatchIntent) error {
	if intent.Operation.Kind == provider.OperationCollectFee ||
		intent.Operation.Kind == provider.OperationCollectPrincipal {
		return r.verifyPreparedCollectionDispatchLease(ctx, intent)
	}
	if intent.Operation.Kind != provider.OperationFund {
		return errors.New("dispatch_kind_invalid")
	}
	return r.verifyPreparedDispatchLease(ctx, intent)
}

func (r *Repository) verifyPreparedCollectionDispatchLease(ctx context.Context,
	intent DispatchIntent) error {
	op := intent.Operation
	claim := intent.Claim
	if claim.State != domain.OperationDispatching || claim.Kind != string(op.Kind) ||
		claim.OperationID != op.ID || claim.Mode == "" || op.Mode != string(claim.Mode) ||
		claim.BridgeID != op.BridgeID || !validLeaseToken(claim.LeaseToken) ||
		claim.LeaseExpiresAt.IsZero() || op.SourceAccountRef == "" ||
		op.DestinationAccountRef == "" || op.SourceAccountRef == op.DestinationAccountRef ||
		op.AmountCents <= 0 || op.Currency != "USD" || op.TriggerID == "" {
		return errors.New("collection_dispatch_gate_binding_invalid")
	}
	requestPrefix := "fee-"
	if op.Kind == provider.OperationCollectPrincipal {
		requestPrefix = "principal-"
	}
	if op.RequestID != requestPrefix+op.ID {
		return errors.New("collection_dispatch_request_identity_invalid")
	}
	hash, err := provider.PayloadHash(op)
	if err != nil || hash != op.PayloadHash {
		return errors.New("collection_dispatch_payload_hash_invalid")
	}
	var current bool
	err = r.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM servicing.operations o
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND o.id=$2 AND o.kind=$3 AND o.state='DISPATCHING'
		AND o.version=$4 AND o.lease_token=$5::uuid AND o.lease_expires_at=$6
		AND o.lease_expires_at>clock_timestamp()+interval '10 seconds'
		AND o.request_id=$7 AND o.payload_hash=$8
		AND o.source_account_ref=$9 AND o.destination_account_ref=$10
		AND o.amount_cents=$11 AND o.currency='USD' AND o.terms_hash=b.terms_hash
		AND o.bridge_id=$12 AND o.reservation_id IS NULL
		AND o.late_fee_cents=$13 AND o.contract_fee_cents=$14 AND o.principal_cents=$15
		AND o.reserved_provider_fee_cents=0 AND o.provider_object_id IS NULL
		AND b.version=$16 AND b.customer_id=$17 AND b.facility_id=$18
		AND b.canonical_account_id=o.source_account_ref AND b.ever_funded
		AND b.lifecycle IN ('ACTIVE','COLLECTING','REOPENED_HELD')
		AND (SELECT count(DISTINCT ca2.id) FROM servicing.collection_allocations a2
			JOIN servicing.collection_attempts ca2 ON ca2.mode=a2.mode AND ca2.id=a2.attempt_id
			WHERE a2.mode=o.mode AND a2.operation_id=o.id)=1
		AND (SELECT COALESCE(sum(a2.amount_cents),0) FROM servicing.collection_allocations a2
			WHERE a2.mode=o.mode AND a2.operation_id=o.id)=o.amount_cents
		AND (SELECT COALESCE(sum(a2.amount_cents) FILTER (WHERE a2.bucket='LATE_FEE'),0)
			FROM servicing.collection_allocations a2 WHERE a2.mode=o.mode AND a2.operation_id=o.id)=o.late_fee_cents
		AND (SELECT COALESCE(sum(a2.amount_cents) FILTER (WHERE a2.bucket='CONTRACT_FEE'),0)
			FROM servicing.collection_allocations a2 WHERE a2.mode=o.mode AND a2.operation_id=o.id)=o.contract_fee_cents
		AND (SELECT COALESCE(sum(a2.amount_cents) FILTER (WHERE a2.bucket='PRINCIPAL'),0)
			FROM servicing.collection_allocations a2 WHERE a2.mode=o.mode AND a2.operation_id=o.id)=o.principal_cents
		AND EXISTS(SELECT 1 FROM servicing.collection_allocations a
			JOIN servicing.collection_attempts ca ON ca.mode=a.mode AND ca.id=a.attempt_id
			JOIN servicing.deposits d ON d.mode=ca.mode AND d.id=ca.deposit_id
			WHERE a.mode=o.mode AND a.operation_id=o.id AND ca.bridge_id=b.id
			AND ca.customer_id=b.customer_id AND ca.account_ref=o.source_account_ref
			AND ca.state='QUEUED' AND ca.reserved_debit_cents=o.amount_cents+ca.provider_fee_cents
			AND ca.provider_fee_cents=COALESCE(o.reserved_provider_fee_cents,0)
			AND ((o.mode='DEMO' AND ca.fee_contract_id='MOCK_HTTP_ZERO_COLLECTION_FEE_V1')
			 OR (o.mode='SANDBOX' AND ca.fee_contract_id ~ '^AIRWALLEX_SANDBOX_EXACT_[0-9a-f]{64}$' AND ca.provider_fee_cents=0))
			AND d.customer_id=ca.customer_id AND d.account_ref=ca.account_ref
			AND d.provider_deposit_id=$19 AND d.status='SETTLED'
			AND d.kind='EXTERNAL_SETTLED')
		AND NOT EXISTS(SELECT 1 FROM servicing.provider_observations p
			WHERE p.mode=o.mode AND p.operation_id=o.id AND p.observation_kind='TRANSFER')
	)`, string(claim.Mode), claim.OperationID, string(op.Kind), claim.Version,
		claim.LeaseToken, claim.LeaseExpiresAt, op.RequestID, op.PayloadHash,
		op.SourceAccountRef, op.DestinationAccountRef, op.AmountCents, claim.BridgeID,
		op.LateFeeCents, op.ContractFeeCents, op.PrincipalCents, claim.BridgeVersion,
		claim.CustomerID, claim.FacilityID, op.TriggerID).Scan(&current)
	if err != nil || !current {
		return errors.New("collection_dispatch_commit_lease_unverified")
	}
	return nil
}

// PrepareFundingDispatchTx rechecks all first-send guards and changes the
// durable intent to DISPATCHING atomically, without making a provider call.
func (r *Repository) PrepareFundingDispatchTx(ctx context.Context, tx pgx.Tx, claim OperationClaim,
	request FundingGuard, clock func() time.Time) (DispatchPreparation, error) {
	if tx == nil || request.ActionKind != "FIRST_SEND" || request.ActionID != claim.OperationID ||
		request.ActionVersion != claim.Version ||
		request.Mode != string(claim.Mode) || request.FacilityID != claim.FacilityID ||
		request.ExcludeOperationID != claim.OperationID || clock == nil || request.Evaluator == nil ||
		!request.PricingReady || request.Pricing.Validate() != nil || request.Principal != "" ||
		request.HTTPMethod != "" || request.ResourcePath != "" || request.IdempotencyKey != "" ||
		request.RequestHash != "" || !validDispatchPrePolicyBlock(request.PrePolicyBlockCode) {
		return DispatchPreparation{}, errors.New("dispatch_guard_input_invalid")
	}
	if !validLeaseToken(claim.LeaseToken) || claim.State != domain.OperationQueued || claim.Kind != "FUND" {
		return DispatchPreparation{}, ErrOperationClaimStale
	}
	bridgeID := claim.BridgeID
	var customerID string
	if err := tx.QueryRow(ctx, `SELECT customer_id FROM servicing.offers WHERE mode=$1 AND id=$2`,
		request.Mode, request.OfferID).Scan(&customerID); err != nil {
		return DispatchPreparation{}, errors.New("dispatch_offer_lookup_failed")
	}
	facility, err := lockFundingFacility(ctx, tx, request.Mode, request.FacilityID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	customer, err := lockFundingCustomer(ctx, tx, request.Mode, customerID)
	if err != nil {
		return DispatchPreparation{}, errors.New("dispatch_customer_lock_failed")
	}
	offer, err := lockFundingOffer(ctx, tx, request.Mode, request.OfferID)
	if err != nil {
		return DispatchPreparation{}, errors.New("dispatch_offer_lock_failed")
	}
	bridgeIDs, err := lockCustomerBridges(ctx, tx, request.Mode, customerID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if err := lockCustomerReservations(ctx, tx, request.Mode, customerID); err != nil {
		return DispatchPreparation{}, err
	}
	if err := lockCustomerInvoices(ctx, tx, request.Mode, customerID); err != nil {
		return DispatchPreparation{}, err
	}
	if offer.id != request.OfferID || offer.customerID != customerID || offer.facilityID != request.FacilityID {
		return DispatchPreparation{}, errors.New("dispatch_offer_scope_invalid")
	}
	mapping, fingerprint, mapErr := LockVerifiedMapping(ctx, tx, offer.customerID, offer.accountID, offer.terms.CardRef)
	blockedCode := request.PrePolicyBlockCode
	if mapErr != nil && (mapErr.Error() == "mapping_unavailable" || mapErr.Error() == "mapping_fingerprint_failed") {
		return DispatchPreparation{}, mapErr
	}
	if mapErr != nil || fingerprint != offer.terms.MappingFingerprint || mapping.RosterAccountID != offer.accountID || mapping.CardID != offer.terms.CardRef {
		blockedCode = "MAPPING_CONFLICT"
	}
	// Acquire authority row locks before the operation lock to keep the common
	// financial lock order. The result is revalidated after the operation lock
	// with the final post-lock clock sample below.
	if _, err := validateCurrentAuthority(ctx, tx, request.Mode, offer, facility, clock); err != nil &&
		(err.Error() == "funding_authority_read_failed" || err.Error() == "funding_clock_invalid") {
		return DispatchPreparation{}, err
	}
	var reservationID, reservationState string
	var reservationExpires time.Time
	var operationID, operationState, operationRequestID, operationPayloadHash, operationSource, operationDestination, operationCurrency, operationTermsHash string
	var operationVersion, operationAmount int64
	var operationFee *int64
	var providerObjectID *string
	var operationLease *string
	var operationLeaseExpiry *time.Time
	var lastCheckID *string
	err = tx.QueryRow(ctx, `SELECT o.id::text,o.state,o.version,o.request_id,o.payload_hash,o.source_account_ref,
		o.destination_account_ref,o.amount_cents,o.currency,o.terms_hash,o.reserved_provider_fee_cents,
		o.provider_object_id,o.lease_token::text,o.lease_expires_at,o.last_funding_check_id,
		r.id::text,r.state,r.expires_at
		FROM servicing.operations o JOIN servicing.reservations r ON r.mode=o.mode AND r.id=o.reservation_id
		WHERE o.mode=$1 AND o.id=$2 FOR UPDATE OF r,o`, request.Mode, claim.OperationID).Scan(
		&operationID, &operationState, &operationVersion, &operationRequestID, &operationPayloadHash,
		&operationSource, &operationDestination, &operationAmount, &operationCurrency, &operationTermsHash,
		&operationFee, &providerObjectID, &operationLease, &operationLeaseExpiry, &lastCheckID,
		&reservationID, &reservationState, &reservationExpires)
	if err != nil {
		return DispatchPreparation{}, errors.New("dispatch_operation_lock_failed")
	}
	if err := verifyDispatchClaimBindings(claim, operationID, operationState, operationVersion,
		operationLease, operationLeaseExpiry, request, offer, facility, reservationID, reservationState,
		operationRequestID, operationPayloadHash, operationSource, operationDestination, operationAmount,
		operationCurrency, operationTermsHash, operationFee, providerObjectID); err != nil {
		return DispatchPreparation{}, err
	}
	if request.InitialSourceFeeCents != operationFeeValue(operationFee) ||
		request.InitialSourceFeeContractID == "" || request.SourceFeeContractID != request.InitialSourceFeeContractID ||
		request.KnownSourceFeeCents == nil || *request.KnownSourceFeeCents != request.InitialSourceFeeCents {
		blockedCode = "SOURCE_LIQUIDITY_CHANGED"
	}
	if lastCheckID == nil || *lastCheckID == "" || *lastCheckID != request.LastFundingCheckID {
		return DispatchPreparation{}, errors.New("dispatch_funding_check_binding_missing")
	}
	// Snapshot financial time only after every financial row and the operation
	// lease are locked, so expiry and freshness use one post-lock clock reading.
	now, authorityErr := validateCurrentAuthority(ctx, tx, request.Mode, offer, facility, clock)
	if now.IsZero() {
		return DispatchPreparation{}, errors.New("funding_clock_invalid")
	}
	if authorityErr != nil && blockedCode == "" {
		if authorityErr.Error() == "funding_authority_read_failed" || authorityErr.Error() == "funding_clock_invalid" {
			return DispatchPreparation{}, authorityErr
		}
		blockedCode = "COLLECTION_AUTHORITY_BLOCKED"
	}
	if validationCode := validateDispatchOffer(request, offer, facility, now); validationCode != "" && blockedCode == "" {
		blockedCode = validationCode
	}
	if offer.version != request.ExpectedVersion && blockedCode == "" {
		blockedCode = "OFFER_VERSION_CHANGED"
	}
	if reservationState != "NEVER_SENT" || !now.Before(reservationExpires) || providerObjectID != nil {
		return r.cancelDispatchIntent(ctx, tx, claim, request, now, "RESERVATION_EXPIRED", "", "RESERVATION_EXPIRED")
	}
	var transferObserved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.provider_observations
		WHERE mode=$1 AND operation_id=$2 AND observation_kind='TRANSFER')`, request.Mode, operationID).Scan(&transferObserved); err != nil {
		return DispatchPreparation{}, errors.New("dispatch_observation_read_failed")
	}
	if transferObserved {
		return DispatchPreparation{}, errors.New("dispatch_transfer_observation_conflict")
	}
	if !now.Before(offer.expiresAt) {
		return r.cancelDispatchIntent(ctx, tx, claim, request, now, "OFFER_EXPIRED", "", "OFFER_EXPIRED")
	}
	if offer.offeredOn.Format("2006-01-02") != now.Format("2006-01-02") {
		return r.cancelDispatchIntent(ctx, tx, claim, request, now, "MIDNIGHT_SUPERSEDED", "", "OFFER_MIDNIGHT_SUPERSEDED")
	}
	freshReadErr := validateFreshFundingReads(request, offer, facility, now)
	if errors.Is(freshReadErr, errFundingFreshEvidenceStale) {
		if err := FinishOperationTx(ctx, tx, claim, OperationReadOutcome{Source: "GUARD", ProviderError: &provider.ProviderError{
			Code: "DISPATCH_EVIDENCE_STALE", Recovery: provider.RecoveryRetryRead,
			Cause: provider.RecoveryRetryRead, Certainty: provider.CertaintyUncertain}}); err != nil {
			return DispatchPreparation{}, err
		}
		return DispatchPreparation{RetryScheduled: true, BlockedCode: "DISPATCH_EVIDENCE_STALE"}, nil
	}
	if freshReadErr != nil && blockedCode == "" {
		blockedCode = fundingBlockCode(freshReadErr)
	}
	acceptanceErr := requireMatchingAcceptance(ctx, tx, request.Mode, offer)
	for _, bridgeID := range bridgeIDs {
		var funded bool
		var lifecycle string
		if err := tx.QueryRow(ctx, `SELECT ever_funded,lifecycle FROM servicing.bridges WHERE mode=$1 AND id=$2`, request.Mode, bridgeID).Scan(&funded, &lifecycle); err != nil {
			return DispatchPreparation{}, errors.New("dispatch_bridge_read_failed")
		}
		if !funded || lifecycle == "REPAID" || lifecycle == "VOID_UNFUNDED" {
			continue
		}
		accrual, accrualErr := AccrueThrough(ctx, tx, AccrualOptions{Mode: domain.Mode(request.Mode),
			Pricing: request.Pricing, ExpectedPolicyHash: request.Evaluator.PolicyHash()}, bridgeID, now)
		if accrualErr != nil {
			return DispatchPreparation{}, errors.New("dispatch_accrual_unavailable")
		}
		if accrual.Pending || accrual.CorrectionRequired {
			if blockedCode == "" {
				blockedCode = "ACCRUAL_CATCHUP_PENDING"
			}
		}
	}
	if err := catchUpFundingDefaults(ctx, tx, request.Mode, bridgeIDs, now); err != nil {
		return DispatchPreparation{}, err
	}
	customer, err = lockFundingCustomer(ctx, tx, request.Mode, customerID)
	if err != nil {
		return DispatchPreparation{}, errors.New("dispatch_customer_read_failed")
	}
	bridges, err := readFundingBridges(ctx, tx, request.Mode, customerID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	reservations, reservationTotals, err := readFundingReservations(ctx, tx, request.Mode, customerID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	invoices, customerCapturedAt, receivablesComplete, invoiceRefs, invoiceSources, err := readFundingInvoices(ctx, tx, request.Mode, customerID)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if _, err := refreshFundingFacility(ctx, tx, request.Mode, request.FacilityID, &facility); err != nil {
		return DispatchPreparation{}, err
	}
	if err := validateLedgerCounters(facility, customer, bridges, reservationTotals); err != nil && blockedCode == "" {
		blockedCode = "LEDGER_RECONCILIATION_REQUIRED"
	}
	if code, _ := fundingLocalHold(customer, bridges, invoices); code != "" && blockedCode == "" {
		blockedCode = code
	}
	if code, err := fundingBookGuardExcept(ctx, tx, request.Mode, offer, bridges, bridgeID); err != nil {
		return DispatchPreparation{}, err
	} else if code != "" && blockedCode == "" {
		blockedCode = code
	}
	own, err := loadOwnNeverSentProjection(ctx, tx, claim, offer, facility, operationVersion, reservationID,
		reservationState, operationAmount, operationFee, now)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if !own.CurrentlyEligible && blockedCode == "" {
		blockedCode = "INVOICE_BACKING_CHANGED"
	}
	var ownProjection *underwriting.NeverSentReservation
	if own.CanProject {
		ownProjection = &own.Projection
	}
	input, err := buildFundingPolicyInput(request, offer, facility, customer, bridges, reservations,
		invoices, customerCapturedAt, receivablesComplete, invoiceRefs, now, ownProjection)
	if err != nil {
		return DispatchPreparation{}, err
	}
	otherLiability, liquidityErr := ValidateSourceLiquidity(ctx, tx, SourceLiquidityProjection{
		Mode: request.Mode, SourceAccountRef: facility.fundingAccount, AvailableCents: request.FacilityBalance.AvailableCents,
		CandidateAmountCents: offer.principal, CandidateFeeCents: request.KnownSourceFeeCents,
		MockFeeContract: request.MockFeeContract, ExcludeOperationID: claim.OperationID,
		ExcludeReservationID: reservationID, ExcludeBridgeID: bridgeID, ExcludeTransactionID: offer.transactionID})
	request.OtherSourceLiabilityCents = otherLiability
	if liquidityErr != nil {
		switch {
		case errors.Is(liquidityErr, errFundingSourceFeeUnknown):
			if blockedCode == "" {
				blockedCode = "SOURCE_LIQUIDITY_UNKNOWN"
			}
		case errors.Is(liquidityErr, errFundingSourceInsufficient):
			if blockedCode == "" {
				blockedCode = "FUNDING_SOURCE_BALANCE_INSUFFICIENT"
			}
		default:
			return DispatchPreparation{}, liquidityErr
		}
	}
	policyResult, err := request.Evaluator.Evaluate(input)
	if err != nil {
		return DispatchPreparation{}, errors.New("dispatch_policy_evaluation_failed")
	}
	if acceptanceErr != nil && blockedCode == "" {
		if acceptanceErr.Error() == "funding_acceptance_read_failed" {
			return DispatchPreparation{}, acceptanceErr
		}
		blockedCode = "ACCEPTANCE_REQUIRED"
	}
	if blockedCode == "" {
		blockedCode = fundingPolicyBlock(policyResult, offer, request)
	}
	if blockedCode == "" && policyResult.ApprovalRequired {
		approved, approvalErr := hasHumanApproval(ctx, tx, request.Mode, offer)
		if approvalErr != nil {
			return DispatchPreparation{}, approvalErr
		}
		if !approved {
			blockedCode = "APPROVAL_REQUIRED"
		}
	}
	if blockedCode == "" && policyResult.Regime != nil && *policyResult.Regime != policy.RegimeAbundant &&
		*policyResult.Regime != policy.RegimeNormal {
		blockedCode = "SCARCE_CAPITAL_RANKING_UNSUPPORTED"
	}
	allocations := own.Allocations
	if !own.CanProject || !validateFundingAllocationBasis(policyResult, input.Customer.Receivables,
		allocations, invoiceSources, now, offer.dueOn) {
		if blockedCode == "" {
			blockedCode = "INVOICE_BACKING_CHANGED"
		}
	}
	checkID, err := insertFundingCheck(ctx, tx, request, offer, input, policyResult, facility, allocations, blockedCode)
	if err != nil {
		return DispatchPreparation{}, err
	}
	if blockedCode != "" {
		return r.cancelDispatchIntent(ctx, tx, claim, request, now, dispatchCancellationReason(blockedCode), checkID, blockedCode)
	}
	var operationVersionAfter int64
	err = tx.QueryRow(ctx, `UPDATE servicing.operations SET state='DISPATCHING',version=version+1,
		last_funding_check_id=$7,next_check_at=NULL,sanitized_error_code=NULL,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state='QUEUED' AND version=$3 AND lease_token=$4::uuid
		AND lease_expires_at=$5 AND lease_expires_at>clock_timestamp()+interval '10 seconds' AND last_funding_check_id=$6
		AND provider_object_id IS NULL AND provider_status IS NULL
		RETURNING version`, claim.Mode, claim.OperationID, claim.Version, claim.LeaseToken,
		claim.LeaseExpiresAt, lastCheckID, checkID).Scan(&operationVersionAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return DispatchPreparation{}, ErrOperationClaimStale
	}
	if err != nil {
		return DispatchPreparation{}, errors.New("dispatch_operation_cas_failed")
	}
	var reservationRows int64
	if err := tx.QueryRow(ctx, `UPDATE servicing.reservations SET state='DISPATCHED',updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state='NEVER_SENT' RETURNING facility_principal_cents`,
		request.Mode, reservationID).Scan(&reservationRows); err != nil || reservationRows != operationAmount {
		return DispatchPreparation{}, errors.New("dispatch_reservation_transition_failed")
	}
	var bridgeVersion int64
	if err := tx.QueryRow(ctx, `UPDATE servicing.bridges SET lifecycle='FUNDING_PENDING',version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND lifecycle='RESERVED' AND NOT ever_funded RETURNING version`,
		request.Mode, bridgeID).Scan(&bridgeVersion); err != nil {
		return DispatchPreparation{}, errors.New("dispatch_bridge_transition_failed")
	}
	if err := AppendOperationJournal(ctx, tx, claim.Mode, claim.OperationID, "ATTEMPT_STARTED",
		"QUEUED", "DISPATCHING", "", &claim.LeaseToken, &claim.LeaseExpiresAt, nil,
		map[string]any{"funding_check_id": checkID, "movement_certainty": "POSSIBLE_SEND",
			"payload_hash": operationPayloadHash}); err != nil {
		return DispatchPreparation{}, err
	}
	scope := lockedOperationScope{candidate: claim.OperationCandidate, operationID: claim.OperationID,
		kind: "FUND", state: "DISPATCHING", version: operationVersionAfter, bridgeID: bridgeID,
		customerID: customerID, offerID: offer.id, evaluationID: offer.evaluationID, caseID: offer.caseID,
		requestID: operationRequestID, bridgeVersion: bridgeVersion}
	details := map[string]any{"state": "DISPATCHING", "funding_check_id": checkID,
		"movement_certainty": "POSSIBLE_SEND", "payload_hash": operationPayloadHash}
	if err := appendBridgeOperationChange(ctx, tx, scope, bridgeVersion, "FUNDING_DISPATCHING", details); err != nil {
		return DispatchPreparation{}, err
	}
	if err := appendOperationChange(ctx, tx, scope, operationVersionAfter, bridgeVersion, "FUNDING_DISPATCHING", details); err != nil {
		return DispatchPreparation{}, err
	}
	preparedClaim := claim
	preparedClaim.State = domain.OperationDispatching
	preparedClaim.Version = operationVersionAfter
	preparedClaim.BridgeVersion = bridgeVersion
	operation := provider.Operation{ID: operationID, Kind: provider.OperationFund, Mode: request.Mode,
		SourceAccountRef: operationSource, DestinationAccountRef: operationDestination,
		AmountCents: operationAmount, Currency: operationCurrency, RequestID: operationRequestID,
		PayloadHash: operationPayloadHash, BridgeID: claim.BridgeID}
	return DispatchPreparation{Intent: &DispatchIntent{Operation: operation,
		Claim: preparedClaim, FundingCheckID: checkID, BridgeVersion: bridgeVersion,
		SourceFeeContract: request.SourceFeeContractID, SourceFeeCents: *request.KnownSourceFeeCents}}, nil
}

func validDispatchPrePolicyBlock(code string) bool {
	return code == "" || code == "CUSTOMER_POLICY_BINDING_CHANGED" || code == "MAPPING_CONFLICT"
}

func verifyDispatchClaimBindings(claim OperationClaim, operationID, state string, version int64,
	leaseToken *string, leaseExpiry *time.Time, request FundingGuard, offer fundingOffer,
	facility fundingFacility, reservationID, reservationState, requestID, payloadHash, source, destination string,
	amount int64, currency, termsHash string, sourceFee *int64, providerObjectID *string) error {
	if operationID != claim.OperationID || state != "QUEUED" || version != claim.Version ||
		leaseToken == nil || *leaseToken != claim.LeaseToken || leaseExpiry == nil ||
		!leaseExpiry.Equal(claim.LeaseExpiresAt) || reservationID == "" || reservationState != "NEVER_SENT" ||
		requestID != "bridge-"+claim.BridgeID || payloadHash == "" || source != facility.fundingAccount ||
		destination != offer.accountID || amount != offer.principal || currency != "USD" ||
		termsHash != offer.termsHash || sourceFee == nil || providerObjectID != nil {
		return ErrOperationClaimStale
	}
	if request.ActionID != claim.OperationID || request.ActionKind != "FIRST_SEND" {
		return errors.New("dispatch_action_binding_invalid")
	}
	return nil
}

func operationFeeValue(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}

func validateDispatchOffer(request FundingGuard, offer fundingOffer, facility fundingFacility, now time.Time) string {
	termsHash, err := offer.terms.Hash()
	if err != nil || termsHash != offer.termsHash || offer.state != "CONSUMED" ||
		offer.terms.OfferID != offer.id || offer.terms.OfferRevision != offer.revision ||
		offer.terms.SourceMode != request.Mode || offer.terms.FacilityID != request.FacilityID ||
		offer.terms.CustomerID != offer.customerID || offer.terms.BorrowerAccountRef != offer.accountID ||
		offer.terms.TransactionID != offer.transactionID || offer.terms.PrincipalCents != offer.principal ||
		offer.terms.ContractFeeCents != offer.contractFee || offer.terms.PolicyID != offer.policyID ||
		offer.terms.PolicyHash != offer.policyHash || offer.policyID != request.Evaluator.PolicyID() ||
		offer.policyHash != request.Evaluator.PolicyHash() || offer.terms.FundingAccountRef != facility.fundingAccount {
		return "TERMS_BINDING_MISMATCH"
	}
	if !now.Before(offer.expiresAt) || offer.offeredOn.Format("2006-01-02") != now.Format("2006-01-02") ||
		offer.terms.OfferedOn != now.Format("2006-01-02") {
		return "OFFER_EXPIRED"
	}
	if request.Assessment.ID != offer.evaluationID || request.Assessment.PolicyID != offer.policyID ||
		request.Assessment.PolicyHash != offer.policyHash || request.Assessment.CustomerID != offer.customerID ||
		request.Assessment.AccountID != offer.accountID || request.Assessment.TransactionID != offer.transactionID ||
		!validFundingAssessment(request.Assessment) || request.Assessment.PrincipalCents == nil ||
		*request.Assessment.PrincipalCents != offer.principal || request.Assessment.TermDays == nil ||
		*request.Assessment.TermDays != offer.terms.TermDays || request.Assessment.EstimatedFeeCents == nil ||
		*request.Assessment.EstimatedFeeCents != offer.contractFee || !candidateEvidenceMatches(offer, request.Assessment) {
		return "CANDIDATE_ASSESSMENT_INCOMPATIBLE"
	}
	return ""
}

func loadOwnNeverSentProjection(ctx context.Context, tx pgx.Tx, claim OperationClaim, offer fundingOffer,
	facility fundingFacility, operationVersion int64, reservationID, reservationState string,
	operationAmount int64, sourceFee *int64, now time.Time) (ownDispatchBacking, error) {
	if reservationState != "NEVER_SENT" || sourceFee == nil {
		return ownDispatchBacking{}, errors.New("dispatch_own_projection_not_proven")
	}
	var facilityPrincipal, customerPrincipal, customerFee, invoiceCoverage, principalOwed, contractOwed, lateOwed int64
	var bridgeID, transactionID, lifecycle string
	var everFunded bool
	err := tx.QueryRow(ctx, `SELECT r.facility_principal_cents,r.customer_principal_cents,r.customer_fee_cents,
		r.invoice_coverage_cents,
		b.id::text,b.transaction_id,b.lifecycle,b.ever_funded,b.principal_owed_cents,
		b.contract_fee_owed_cents,b.late_fee_owed_cents
		FROM servicing.reservations r JOIN servicing.bridges b ON b.mode=r.mode AND b.id=r.bridge_id
		WHERE r.mode=$1 AND r.id=$2 AND r.bridge_id=$3`,
		string(claim.Mode), reservationID, claim.BridgeID).Scan(&facilityPrincipal, &customerPrincipal,
		&customerFee, &invoiceCoverage, &bridgeID, &transactionID, &lifecycle, &everFunded, &principalOwed, &contractOwed, &lateOwed)
	if err != nil || bridgeID != claim.BridgeID || transactionID != offer.transactionID ||
		lifecycle != "RESERVED" || everFunded || principalOwed != 0 || contractOwed != 0 || lateOwed != 0 ||
		facilityPrincipal != operationAmount || facilityPrincipal != customerPrincipal || customerFee < 0 ||
		invoiceCoverage != customerPrincipal+customerFee {
		return ownDispatchBacking{}, errors.New("dispatch_own_projection_scope_invalid")
	}
	var transferObserved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.provider_observations
		WHERE mode=$1 AND operation_id=$2 AND observation_kind='TRANSFER')`,
		string(claim.Mode), claim.OperationID).Scan(&transferObserved); err != nil || transferObserved {
		return ownDispatchBacking{}, errors.New("dispatch_own_projection_observation_failed")
	}
	rows, err := tx.Query(ctx, `SELECT a.source_system,a.invoice_id,a.active_cents,r.source_ref,
		r.source_revision,r.source_complete,r.allocation_complete,r.disputed,r.backing_hold,r.currency,
		r.payer_id,r.unpaid_cents,r.reserved_cents,r.due_on,r.due_date_verified,r.last_captured_at,
		COALESCE((SELECT sum(other.active_cents)::bigint FROM servicing.bridge_invoice_allocations other
			WHERE other.mode=a.mode AND other.source_system=a.source_system AND other.invoice_id=a.invoice_id),0)
		FROM servicing.bridge_invoice_allocations a
		JOIN servicing.receivables r ON r.mode=a.mode AND r.source_system=a.source_system AND r.invoice_id=a.invoice_id
		WHERE a.mode=$1 AND a.bridge_id=$2 ORDER BY a.source_system,a.invoice_id`, string(claim.Mode), bridgeID)
	if err != nil {
		return ownDispatchBacking{}, errors.New("dispatch_own_projection_allocations_failed")
	}
	defer rows.Close()
	backing := ownDispatchBacking{CanProject: true, CurrentlyEligible: true}
	var total int64
	startOn := now.UTC().Format("2006-01-02")
	endOn := offer.dueOn.UTC().Format("2006-01-02")
	for rows.Next() {
		var storageSystem, invoice, sourceRevision, currency, payer string
		var amount, unpaid, reserved, allAllocated int64
		var sourceRefJSON []byte
		var sourceComplete, allocationComplete, disputed, backingHold, dueVerified bool
		var dueOn, lastCapturedAt *time.Time
		if rows.Scan(&storageSystem, &invoice, &amount, &sourceRefJSON, &sourceRevision,
			&sourceComplete, &allocationComplete, &disputed, &backingHold, &currency, &payer,
			&unpaid, &reserved, &dueOn, &dueVerified, &lastCapturedAt, &allAllocated) != nil ||
			storageSystem == "" || invoice == "" || amount <= 0 {
			return ownDispatchBacking{}, errors.New("dispatch_own_projection_allocation_invalid")
		}
		var sourceRef domain.SourceRef
		if json.Unmarshal(sourceRefJSON, &sourceRef) != nil || sourceRef.System == "" ||
			sourceRef.RecordID == "" || sourceRef.Version == "" || sourceRef.EffectiveAt.IsZero() ||
			!fundingTimeUTC(sourceRef.EffectiveAt) || sourceRef.EffectiveAt.After(now.UTC().Add(5*time.Second)) {
			backing.CanProject = false
			backing.CurrentlyEligible = false
		} else {
			backing.Projection.Invoices = append(backing.Projection.Invoices,
				underwriting.InvoiceEncumbrance{InvoiceID: invoice, SourceRefSystem: sourceRef.System,
					StorageSystem: storageSystem, AmountCents: amount})
		}
		backing.Allocations = append(backing.Allocations, fundingAllocation{sourceSystem: storageSystem,
			invoiceID: invoice, amount: amount, unpaid: unpaid})
		eligible := sourceRevision != "" && sourceComplete && allocationComplete && !disputed &&
			!backingHold && currency == "USD" && payer != "" && lastCapturedAt != nil &&
			dueVerified && dueOn != nil && unpaid >= 0 && reserved >= 0 && allAllocated == reserved &&
			unpaid >= reserved && amount <= unpaid &&
			!lastCapturedAt.After(now.UTC().Add(5*time.Second))
		if eligible {
			due := dueOn.UTC().Format("2006-01-02")
			eligible = due >= startOn && due <= endOn
		}
		if !eligible {
			backing.CurrentlyEligible = false
		}
		var ok bool
		total, ok = checkedFundingAdd(total, amount)
		if !ok {
			return ownDispatchBacking{}, errors.New("dispatch_own_projection_overflow")
		}
	}
	if rows.Err() != nil || operationVersion != claim.Version {
		return ownDispatchBacking{}, errors.New("dispatch_own_projection_allocation_read_failed")
	}
	if len(backing.Allocations) == 0 || total != invoiceCoverage || total != customerPrincipal+customerFee {
		backing.CanProject = false
		backing.CurrentlyEligible = false
	}
	backing.Projection = underwriting.NeverSentReservation{ProvenNeverSent: true, OperationState: "QUEUED",
		ReservationState: reservationState, ReservationID: reservationID, BridgeID: bridgeID,
		TransactionID: transactionID, FacilityPrincipalCents: facilityPrincipal,
		CustomerPrincipalCents: customerPrincipal, CustomerFeeCents: customerFee, Invoices: backing.Projection.Invoices}
	return backing, nil
}

// RecordDispatchPreflightFailure completes a claimed QUEUED read-only preflight
// without writing a transfer observation or changing proven-unsent certainty.
func (r *Repository) RecordDispatchPreflightFailure(ctx context.Context, claim OperationClaim,
	outcome OperationReadOutcome) error {
	if outcome.Source != "GUARD" {
		return errors.New("dispatch_preflight_outcome_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if err := FinishOperationTx(txCtx, tx, claim, outcome); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("dispatch_preflight_finish_commit_failed")
	}
	return nil
}

// CancelClaimedUnsentFunding atomically retires a definitive first-send guard
// mismatch using the active QUEUED lease proof.
func (r *Repository) CancelClaimedUnsentFunding(ctx context.Context, claim OperationClaim,
	reason string, now time.Time) error {
	if claim.State != domain.OperationQueued || now.IsZero() {
		return ErrOperationClaimStale
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	proof := &OperationLeaseProof{State: claim.State, Version: claim.Version,
		Token: claim.LeaseToken, LeaseExpiresAt: claim.LeaseExpiresAt}
	if err := r.CancelUnsentFundingTx(txCtx, tx, claim.Mode, claim.BridgeID,
		reason, func() time.Time { return now.UTC() }, proof); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("dispatch_guard_cancel_commit_failed")
	}
	return nil
}

func dispatchCancellationReason(code string) string {
	switch code {
	case "MAPPING_CONFLICT":
		return "MAPPING_CHANGED"
	case "COLLECTION_AUTHORITY_BLOCKED":
		return "AUTHORITY_CHANGED"
	case "SOURCE_LIQUIDITY_UNKNOWN", "FUNDING_SOURCE_BALANCE_INSUFFICIENT":
		return "SOURCE_LIQUIDITY_CHANGED"
	case "TERMS_BINDING_MISMATCH", "CANDIDATE_ASSESSMENT_INCOMPATIBLE", "CURRENT_SHORTFALL_CHANGED",
		"CURRENT_SHORTFALL_INVALID", "TERMS_POLICY_PRICE_MISMATCH", "POLICY_HARD_RULE_FAILED",
		"SCARCE_CAPITAL_RANKING_UNSUPPORTED", "POLICY_REVIEW_REQUIRED", "POLICY_RESULT_BINDING_INVALID":
		return "POLICY_RECHECK_FAILED"
	default:
		return "GUARD_CHANGED"
	}
}

func (r *Repository) cancelDispatchIntent(ctx context.Context, tx pgx.Tx, claim OperationClaim,
	request FundingGuard, now time.Time, reason string, checkID string, blockedCode string) (DispatchPreparation, error) {
	if reason != "OFFER_EXPIRED" && reason != "RESERVATION_EXPIRED" && reason != "MIDNIGHT_SUPERSEDED" {
		reason = dispatchCancellationReason(blockedCode)
	}
	if err := r.CancelUnsentFundingTx(ctx, tx, claim.Mode, claim.BridgeID, reason,
		func() time.Time { return now }, &OperationLeaseProof{State: claim.State,
			Version: claim.Version, Token: claim.LeaseToken, LeaseExpiresAt: claim.LeaseExpiresAt,
			FundingCheckID: checkID}); err != nil {
		return DispatchPreparation{}, err
	}
	return DispatchPreparation{Cancelled: true, FundingCheckID: checkID, BlockedCode: blockedCode}, nil
}
