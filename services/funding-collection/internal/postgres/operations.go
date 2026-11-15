package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

const (
	maxOperationCandidates = 25
	operationLeaseSeconds  = 60
)

var ErrOperationClaimStale = errors.New("operation_claim_stale")

// ErrOperationLookupNotApplicable means a proven-unsent funding intent has no lookup work.
var ErrOperationLookupNotApplicable = errors.New("operation_lookup_not_applicable")

// OperationCandidate is a bounded read-only queue hint; claim code revalidates every field.
type OperationCandidate struct {
	Mode        domain.Mode
	OperationID string
	FacilityID  string
	CustomerID  string
	BridgeID    string
	Kind        string
	State       domain.OperationState
	Version     int64
}

// OperationClaim is a token-fenced lease on one durable operation.
type OperationClaim struct {
	OperationCandidate
	LeaseToken     string
	LeaseExpiresAt time.Time
	RetryCount     int
	LookupOnly     bool
	BridgeVersion  int64
}

// OperationReadOutcome carries exactly one typed provider read result or error.
// It never means a ledger settlement was applied.
type OperationReadOutcome struct {
	Source             string
	Result             *provider.Result
	Lookup             *provider.LookupResult
	CollectionReversal *provider.CollectionReversalObservation
	ProviderError      *provider.ProviderError
	NoDispatchCode     string
	HoldCode           string
}

// OperationReadIntent contains the original immutable scope for GET-only reconciliation.
type OperationReadIntent struct {
	Operation        provider.Operation
	ProviderObjectID *string
	ProviderStatus   *string
}

// LoadClaimedReadIntent reads a saved operation under its live read-lease fence.
// It does not consult current mapping, eligibility, authority or offer state.
func (r *Repository) LoadClaimedReadIntent(ctx context.Context, claim OperationClaim) (OperationReadIntent, error) {
	if !claim.LookupOnly || !validLeaseToken(claim.LeaseToken) || claim.LeaseExpiresAt.IsZero() ||
		claim.State == domain.OperationQueued {
		return OperationReadIntent{}, ErrOperationClaimStale
	}
	var mode, id, kind, state, requestID, payloadHash, source, destination, currency, bridgeID string
	var facilityID, customerID string
	var version, amount, lateFee, contractFee, principal int64
	var providerObjectID, providerStatus, leaseToken *string
	var leaseExpiry *time.Time
	err := r.pool.QueryRow(ctx, `SELECT o.mode,o.id::text,o.kind,o.state,o.version,o.request_id,o.payload_hash,
		o.source_account_ref,o.destination_account_ref,o.amount_cents,o.currency,o.bridge_id::text,
		o.late_fee_cents,o.contract_fee_cents,o.principal_cents,o.provider_object_id,o.provider_status,
		o.lease_token::text,o.lease_expires_at,b.facility_id,b.customer_id
		FROM servicing.operations o JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND o.id=$2 AND o.state=$3 AND o.version=$4
		AND o.lease_token=$5::uuid AND o.lease_expires_at=$6 AND o.lease_expires_at>clock_timestamp()`,
		string(claim.Mode), claim.OperationID, string(claim.State), claim.Version,
		claim.LeaseToken, claim.LeaseExpiresAt).Scan(&mode, &id, &kind, &state, &version,
		&requestID, &payloadHash, &source, &destination, &amount, &currency, &bridgeID,
		&lateFee, &contractFee, &principal, &providerObjectID, &providerStatus, &leaseToken, &leaseExpiry,
		&facilityID, &customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationReadIntent{}, ErrOperationClaimStale
	}
	if err != nil {
		return OperationReadIntent{}, errors.New("operation_read_intent_unavailable")
	}
	if mode != string(claim.Mode) || id != claim.OperationID || kind != claim.Kind || state != string(claim.State) ||
		version != claim.Version || bridgeID != claim.BridgeID || facilityID != claim.FacilityID || customerID != claim.CustomerID ||
		requestID == "" || source == "" || destination == "" || currency != "USD" || amount <= 0 ||
		!validFundingHash(payloadHash) || leaseToken == nil || *leaseToken != claim.LeaseToken ||
		leaseExpiry == nil || !leaseExpiry.Equal(claim.LeaseExpiresAt) {
		return OperationReadIntent{}, errors.New("operation_read_intent_binding_invalid")
	}
	triggerID := ""
	if kind == "COLLECT_FEE" || kind == "COLLECT_PRINCIPAL" {
		triggerID, err = loadCollectionTrigger(ctx, r.pool, domain.Mode(mode), id,
			customerID, source, bridgeID)
		if err != nil {
			return OperationReadIntent{}, err
		}
	}
	operation := provider.Operation{ID: id, Kind: provider.OperationKind(kind), Mode: mode,
		SourceAccountRef: source, DestinationAccountRef: destination, AmountCents: amount,
		Currency: currency, RequestID: requestID, PayloadHash: payloadHash, BridgeID: bridgeID,
		TriggerID: triggerID, LateFeeCents: lateFee, ContractFeeCents: contractFee,
		PrincipalCents: principal}
	calculatedHash, err := provider.PayloadHash(operation)
	if err != nil || calculatedHash != payloadHash {
		return OperationReadIntent{}, errors.New("operation_read_payload_invalid")
	}
	return OperationReadIntent{Operation: operation, ProviderObjectID: providerObjectID,
		ProviderStatus: providerStatus}, nil
}

// DiscoverOperationCandidates returns up to 25 due metadata-only work hints.
func (r *Repository) DiscoverOperationCandidates(ctx context.Context, mode domain.Mode, facilityID string, limit int) ([]OperationCandidate, error) {
	if (mode != domain.ModeDemo && mode != domain.ModeSandbox) || facilityID == "" || limit < 1 || limit > maxOperationCandidates {
		return nil, errors.New("operation_discovery_request_invalid")
	}
	rows, err := r.pool.Query(ctx, `SELECT o.mode,o.id::text,b.facility_id,b.customer_id,b.id::text,
		o.kind,o.state,o.version
		FROM servicing.operations o
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND b.facility_id=$2 AND o.kind IN ('FUND','COLLECT_FEE','COLLECT_PRINCIPAL')
		AND NOT (o.state='PENDING' AND o.provider_status IS NOT NULL AND o.provider_status IN ('SETTLED','FAILED'))
		AND ((o.state='DISPATCHING' AND o.lease_expires_at<=clock_timestamp()) OR
			(o.state IN ('QUEUED','PENDING','UNKNOWN') AND
			 COALESCE(o.next_check_at,o.created_at)<=clock_timestamp() AND
			 (o.lease_token IS NULL OR o.lease_expires_at<=clock_timestamp())) OR
			(o.state='HELD' AND o.lookup_requested_at IS NOT NULL AND
			 COALESCE(o.next_check_at,o.lookup_requested_at)<=clock_timestamp() AND
			 (o.lease_token IS NULL OR o.lease_expires_at<=clock_timestamp())))
		ORDER BY COALESCE(o.next_check_at,o.created_at),o.created_at,o.id LIMIT $3`, string(mode), facilityID, limit)
	if err != nil {
		return nil, errors.New("operation_discovery_failed")
	}
	defer rows.Close()
	result := make([]OperationCandidate, 0, limit)
	for rows.Next() {
		var item OperationCandidate
		if rows.Scan(&item.Mode, &item.OperationID, &item.FacilityID, &item.CustomerID,
			&item.BridgeID, &item.Kind, &item.State, &item.Version) != nil {
			return nil, errors.New("operation_discovery_failed")
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("operation_discovery_failed")
	}
	return result, nil
}

// ClaimOperation commits one fenced lease after bounded candidate discovery.
func (r *Repository) ClaimOperation(ctx context.Context, candidate OperationCandidate) (OperationClaim, error) {
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return OperationClaim{}, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	claim, err := ClaimOperationTx(txCtx, tx, candidate)
	if err != nil {
		return OperationClaim{}, err
	}
	if err := tx.Commit(txCtx); err != nil {
		return OperationClaim{}, errors.New("operation_claim_commit_failed")
	}
	return claim, nil
}

// DiscoverHeldUnsentExpirations returns at most 25 expired HELD reservations
// whose latest immutable journal evidence still proves that no send began.
func (r *Repository) DiscoverHeldUnsentExpirations(ctx context.Context, mode domain.Mode,
	facilityID string, limit int) ([]OperationCandidate, error) {
	if (mode != domain.ModeDemo && mode != domain.ModeSandbox) || facilityID == "" || limit < 1 {
		return nil, errors.New("held_unsent_discovery_request_invalid")
	}
	limit = boundedCandidates(limit)
	rows, err := r.pool.Query(ctx, `SELECT o.mode,o.id::text,b.facility_id,b.customer_id,b.id::text,
		o.kind,o.state,o.version
		FROM servicing.operations o
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		JOIN servicing.reservations r ON r.mode=o.mode AND r.id=o.reservation_id
		JOIN servicing.offers f ON f.mode=b.mode AND f.id=b.offer_id
		WHERE o.mode=$1 AND b.facility_id=$2 AND o.kind='FUND' AND o.state='HELD'
		AND r.state='NEVER_SENT' AND b.lifecycle='RESERVED' AND NOT b.ever_funded
		AND b.principal_owed_cents=0 AND b.contract_fee_owed_cents=0 AND b.late_fee_owed_cents=0
		AND o.provider_object_id IS NULL AND o.lookup_requested_at IS NULL
		AND (o.lease_token IS NULL OR o.lease_expires_at<=clock_timestamp())
		AND (r.expires_at<=clock_timestamp() OR f.expires_at<=clock_timestamp() OR
			f.offered_on<(clock_timestamp() AT TIME ZONE 'UTC')::date)
		AND NOT EXISTS(SELECT 1 FROM servicing.provider_observations p WHERE p.mode=o.mode
			AND p.operation_id=o.id AND p.observation_kind='TRANSFER')
		AND (SELECT j.evidence_ref->>'movement_certainty' FROM servicing.operation_journal j
			WHERE j.mode=o.mode AND j.operation_id=o.id ORDER BY j.sequence DESC LIMIT 1)='PROVEN_UNSENT'
		ORDER BY COALESCE(r.expires_at,f.expires_at),o.created_at,o.id LIMIT $3`, string(mode), facilityID, limit)
	if err != nil {
		return nil, errors.New("held_unsent_discovery_failed")
	}
	defer rows.Close()
	items := make([]OperationCandidate, 0, limit)
	for rows.Next() {
		var item OperationCandidate
		if rows.Scan(&item.Mode, &item.OperationID, &item.FacilityID, &item.CustomerID,
			&item.BridgeID, &item.Kind, &item.State, &item.Version) != nil {
			return nil, errors.New("held_unsent_discovery_failed")
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("held_unsent_discovery_failed")
	}
	return items, nil
}

// AppendOperationJournal appends one protected transition fact. Caller must hold the operation row lock.
func AppendOperationJournal(ctx context.Context, tx pgx.Tx, mode domain.Mode, operationID, eventType,
	fromState, toState, sanitizedCode string, leaseToken *string, leaseExpiresAt *time.Time, observationID *string,
	evidence any) error {
	if mode != domain.ModeDemo && mode != domain.ModeSandbox || operationID == "" || !validJournalEvent(eventType) ||
		!validJournalState(fromState) || !validJournalState(toState) ||
		((leaseToken == nil) != (leaseExpiresAt == nil)) {
		return errors.New("operation_journal_entry_invalid")
	}
	body, err := json.Marshal(evidence)
	if err != nil || len(body) == 0 {
		return errors.New("operation_journal_evidence_invalid")
	}
	var sequence int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM servicing.operation_journal
		WHERE mode=$1 AND operation_id=$2`, string(mode), operationID).Scan(&sequence); err != nil {
		return errors.New("operation_journal_sequence_failed")
	}
	var token any
	var expires any
	if leaseToken != nil {
		token, expires = *leaseToken, *leaseExpiresAt
	}
	var providerObservation any
	if observationID != nil {
		providerObservation = *observationID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.operation_journal(mode,operation_id,sequence,event_type,
		from_state,to_state,lease_token,lease_expires_at,sanitized_code,provider_observation_id,evidence_ref)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb)`, string(mode), operationID, sequence,
		eventType, nullableState(fromState), nullableState(toState), token, expires,
		nullableString(sanitizedCode), providerObservation, string(body)); err != nil {
		return errors.New("operation_journal_insert_failed")
	}
	return nil
}

func validJournalEvent(value string) bool {
	switch value {
	case "INTENT_CREATED", "CLAIMED", "LEASE_EXPIRED_UNKNOWN", "ATTEMPT_STARTED",
		"OBSERVATION_RECORDED", "STATE_TRANSITION", "LOOKUP_REQUESTED":
		return true
	default:
		return false
	}
}

func validJournalState(value string) bool {
	if value == "" {
		return true
	}
	switch value {
	case "QUEUED", "DISPATCHING", "PENDING", "UNKNOWN", "HELD", "SETTLED", "FAILED_CONFIRMED", "CANCELLED_UNSENT":
		return true
	default:
		return false
	}
}

func claimableOperationState(state domain.OperationState) bool {
	switch state {
	case domain.OperationQueued, domain.OperationPending, domain.OperationUnknown, domain.OperationHeld, domain.OperationDispatching:
		return true
	default:
		return false
	}
}

func nullableState(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func randomLeaseToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("operation_lease_token_failed")
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func validLeaseToken(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, ch := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func hashEvidence(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func operationRetryDelay(retryCount int) (time.Duration, bool) {
	delays := [...]time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second, 300 * time.Second, 900 * time.Second}
	if retryCount < 0 || retryCount >= len(delays) {
		return 0, false
	}
	return delays[retryCount], true
}

func operationPendingDelay(retryCount int) (time.Duration, bool) {
	if retryCount < 0 || retryCount >= 30 {
		return 0, false
	}
	return time.Minute, true
}

func boundedCandidates(limit int) int {
	if limit > maxOperationCandidates {
		return maxOperationCandidates
	}
	return limit
}

type lockedOperationScope struct {
	candidate                                                                   OperationCandidate
	operationID, kind, state, requestID, payloadHash                            string
	sourceAccount, destinationAccount, currency, termsHash                      string
	reservationID, reservationState, lifecycle, offerState                      string
	facilityID, facilityAccount, borrowerAccount, customerID, bridgeID, offerID string
	transactionID, evaluationID, caseID                                         string
	everFunded                                                                  bool
	amount                                                                      int64
	lateFeeCents, contractFeeCents, principalCents                              int64
	version, bridgeVersion                                                      int64
	collectionDebtVersion                                                       int64
	createdAt                                                                   time.Time
	retryCount                                                                  int
	leaseToken                                                                  *string
	leaseExpiresAt, nextCheckAt, lookupRequestedAt                              *time.Time
	lookupRequestedBy, providerObjectID, providerStatus                         *string
	lastFundingCheckID                                                          string
	providerFee, reservedProviderFee                                            *int64
	collectionAttemptID, collectionDepositID, providerDepositID                 string
	collectionAttemptState                                                      string
	collectionFeeContract                                                       string
	collectionReservedDebit                                                     int64
	collectionQuotedFee                                                         int64
	authorityID                                                                 *string
	authorityVersion                                                            *int64
	terms                                                                       domain.Terms
	guardCode                                                                   string
}

func lockOperationScope(ctx context.Context, tx pgx.Tx, candidate OperationCandidate) (lockedOperationScope, error) {
	var scope lockedOperationScope
	var mode, state, kind string
	var version int64
	var authorityID *string
	var authorityVersion *int64
	err := tx.QueryRow(ctx, `SELECT o.mode,o.kind,o.state,o.version,b.facility_id,b.customer_id,b.id::text,
		b.offer_id::text,b.canonical_account_id,b.transaction_id,COALESCE(o.reservation_id::text,''),
		o.authority_id,o.authority_version
		FROM servicing.operations o JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND o.id=$2`, string(candidate.Mode), candidate.OperationID).Scan(
		&mode, &kind, &state, &version, &scope.facilityID, &scope.customerID, &scope.bridgeID, &scope.offerID,
		&scope.borrowerAccount, &scope.transactionID, &scope.reservationID, &authorityID, &authorityVersion)
	if err != nil {
		return scope, errors.New("operation_scope_unavailable")
	}
	if mode != string(candidate.Mode) || scope.bridgeID != candidate.BridgeID || scope.facilityID != candidate.FacilityID ||
		scope.customerID != candidate.CustomerID || state != string(candidate.State) || version != candidate.Version {
		return scope, ErrOperationClaimStale
	}
	if err := tx.QueryRow(ctx, `SELECT funding_account_ref FROM servicing.facilities WHERE mode=$1 AND id=$2 FOR UPDATE`,
		mode, scope.facilityID).Scan(&scope.facilityAccount); err != nil {
		return scope, errors.New("operation_facility_lock_failed")
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM servicing.customers WHERE mode=$1 AND id=$2 FOR UPDATE`,
		mode, scope.customerID).Scan(new(string)); err != nil {
		return scope, errors.New("operation_customer_lock_failed")
	}
	var bridgeTermsHash, offerTermsHash string
	var termsJSON []byte
	err = tx.QueryRow(ctx, `SELECT b.version,b.collection_debt_version,b.lifecycle,b.ever_funded,b.canonical_account_id,b.transaction_id,
		o.id::text,o.state,o.evaluation_id,COALESCE(o.originating_case_id,''),o.terms_json,o.terms_hash,b.terms_hash
		FROM servicing.bridges b JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id
		WHERE b.mode=$1 AND b.id=$2 FOR UPDATE OF b,o`, mode, scope.bridgeID).Scan(
		&scope.bridgeVersion, &scope.collectionDebtVersion, &scope.lifecycle, &scope.everFunded, &scope.borrowerAccount, &scope.transactionID,
		&scope.offerID, &scope.offerState, &scope.evaluationID, &scope.caseID, &termsJSON, &offerTermsHash, &bridgeTermsHash)
	if err != nil {
		return scope, errors.New("operation_bridge_lock_failed")
	}
	if err := lockBridgeInvoices(ctx, tx, mode, scope.bridgeID); err != nil {
		return scope, err
	}
	if kind == "COLLECT_FEE" || kind == "COLLECT_PRINCIPAL" {
		if authorityID == nil || authorityVersion == nil {
			return scope, errors.New("collection_authority_binding_missing")
		}
		err := tx.QueryRow(ctx, `SELECT ca.id::text,ca.state,d.id::text,d.provider_deposit_id,d.account_ref,
			ca.reserved_debit_cents,ca.provider_fee_cents,ca.fee_contract_id FROM servicing.collection_allocations a
			JOIN servicing.collection_attempts ca ON ca.mode=a.mode AND ca.id=a.attempt_id
			JOIN servicing.deposits d ON d.mode=ca.mode AND d.id=ca.deposit_id
			WHERE a.mode=$1 AND a.operation_id=$2 ORDER BY a.allocation_index LIMIT 1
			FOR UPDATE OF ca,d`, mode, candidate.OperationID).Scan(&scope.collectionAttemptID,
			&scope.collectionAttemptState, &scope.collectionDepositID, &scope.providerDepositID, &scope.borrowerAccount,
			&scope.collectionReservedDebit, &scope.collectionQuotedFee, &scope.collectionFeeContract)
		if err != nil {
			return scope, errors.New("collection_attempt_deposit_lock_failed")
		}
	}
	if json.Unmarshal(termsJSON, &scope.terms) != nil || scope.terms.Validate() != nil {
		scope.guardCode = "OPERATION_TERMS_INVALID"
	}
	if state == "QUEUED" {
		// Current roster and card mappings guard only a first send. Reconciliation
		// uses the immutable saved account and card references in the operation.
		mapping, fingerprint, mapErr := LockVerifiedMapping(ctx, tx, scope.customerID, scope.borrowerAccount, scope.terms.CardRef)
		if mapErr != nil {
			if mapErr.Error() == "mapping_unavailable" || mapErr.Error() == "mapping_fingerprint_failed" {
				return scope, mapErr
			}
			scope.guardCode = "MAPPING_CONFLICT"
		} else if fingerprint != scope.terms.MappingFingerprint || mapping.CardID != scope.terms.CardRef {
			scope.guardCode = "MAPPING_CONFLICT"
		}
	}
	if authorityID != nil && authorityVersion != nil && kind != "FUND" {
		if err := tx.QueryRow(ctx, `SELECT id FROM servicing.authorities WHERE mode=$1 AND id=$2
			AND version=$3 FOR UPDATE`, mode, *authorityID, *authorityVersion).Scan(new(string)); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return scope, errors.New("collection_authority_lock_failed")
			}
			if state == "QUEUED" {
				scope.guardCode = "COLLECTION_AUTHORITY_BLOCKED"
			}
		}
	}
	if scope.reservationID != "" {
		if err := tx.QueryRow(ctx, `SELECT id::text,state FROM servicing.reservations WHERE mode=$1 AND id=$2 FOR UPDATE`,
			mode, scope.reservationID).Scan(&scope.reservationID, &scope.reservationState); err != nil {
			return scope, errors.New("operation_reservation_lock_failed")
		}
	}
	err = tx.QueryRow(ctx, `SELECT o.id::text,o.kind,o.state,o.version,o.request_id,o.payload_hash,o.source_account_ref,
		o.destination_account_ref,o.amount_cents,o.late_fee_cents,o.contract_fee_cents,o.principal_cents,
		o.currency,o.terms_hash,COALESCE(o.reservation_id::text,''),
		o.lease_token::text,o.lease_expires_at,o.retry_count,o.next_check_at,o.provider_object_id,o.provider_status,
		o.provider_fee_cents,o.reserved_provider_fee_cents,o.lookup_requested_at,o.lookup_requested_by,
		COALESCE(o.last_funding_check_id::text,''),o.created_at
		FROM servicing.operations o WHERE o.mode=$1 AND o.id=$2 FOR UPDATE`, mode, candidate.OperationID).Scan(
		&scope.operationID, &scope.kind, &scope.state, &scope.version, &scope.requestID, &scope.payloadHash,
		&scope.sourceAccount, &scope.destinationAccount, &scope.amount, &scope.lateFeeCents,
		&scope.contractFeeCents, &scope.principalCents, &scope.currency, &scope.termsHash,
		&scope.reservationID, &scope.leaseToken, &scope.leaseExpiresAt, &scope.retryCount, &scope.nextCheckAt,
		&scope.providerObjectID, &scope.providerStatus, &scope.providerFee, &scope.reservedProviderFee,
		&scope.lookupRequestedAt, &scope.lookupRequestedBy, &scope.lastFundingCheckID, &scope.createdAt)
	if err != nil {
		return scope, errors.New("operation_lock_failed")
	}
	if scope.operationID != candidate.OperationID || scope.kind != candidate.Kind || scope.state != string(candidate.State) || scope.version != candidate.Version {
		return scope, ErrOperationClaimStale
	}
	scope.authorityID, scope.authorityVersion = authorityID, authorityVersion
	if scope.termsHash != offerTermsHash || scope.termsHash != bridgeTermsHash || scope.termsHash != scope.HashOrEmpty() ||
		scope.currency != "USD" || scope.amount <= 0 || scope.requestID == "" || !validFundingHash(scope.payloadHash) ||
		(scope.kind == "FUND" && scope.lastFundingCheckID == "") ||
		(scope.state == "QUEUED" && scope.offerState != "CONSUMED") {
		if scope.guardCode == "" {
			scope.guardCode = "OPERATION_SCOPE_INVALID"
		}
	}
	if scope.kind == "FUND" {
		if scope.sourceAccount != scope.facilityAccount || scope.destinationAccount != scope.borrowerAccount ||
			scope.reservationID == "" || !validFundingOperationLifecycle(scope) {
			if scope.guardCode == "" {
				scope.guardCode = "OPERATION_SCOPE_INVALID"
			}
		}
	} else if scope.kind != "COLLECT_FEE" && scope.kind != "COLLECT_PRINCIPAL" ||
		scope.sourceAccount != scope.borrowerAccount || scope.destinationAccount != scope.facilityAccount {
		if scope.guardCode == "" {
			scope.guardCode = "OPERATION_SCOPE_INVALID"
		}
	} else if !validCollectionOperationLifecycle(scope) || scope.authorityID == nil ||
		scope.authorityVersion == nil || scope.collectionAttemptID == "" ||
		scope.collectionDepositID == "" || scope.providerDepositID == "" ||
		(scope.state != "SETTLED" && scope.state != "FAILED_CONFIRMED" &&
			scope.state != "CANCELLED_UNSENT" && scope.collectionReservedDebit < scope.amount) ||
		scope.collectionFeeContract != "MOCK_HTTP_ZERO_COLLECTION_FEE_V1" ||
		!validCollectionAttemptState(scope.state, scope.collectionAttemptState) {
		if scope.guardCode == "" {
			scope.guardCode = "OPERATION_SCOPE_INVALID"
		}
	}
	scope.candidate = candidate
	return scope, nil
}

func validCollectionOperationLifecycle(scope lockedOperationScope) bool {
	if !scope.everFunded || scope.reservationID != "" || scope.reservationState != "" {
		return false
	}
	switch domain.OperationState(scope.state) {
	case domain.OperationQueued:
		return scope.lifecycle == "ACTIVE" || scope.lifecycle == "COLLECTING" || scope.lifecycle == "REOPENED_HELD"
	case domain.OperationDispatching, domain.OperationPending, domain.OperationUnknown:
		return scope.lifecycle == "ACTIVE" || scope.lifecycle == "COLLECTING" ||
			scope.lifecycle == "REPAID" || scope.lifecycle == "REOPENED_HELD"
	case domain.OperationHeld, domain.OperationSettled, domain.OperationFailedConfirmed,
		domain.OperationCancelledUnsent:
		return scope.lifecycle == "ACTIVE" || scope.lifecycle == "COLLECTING" ||
			scope.lifecycle == "REPAID" || scope.lifecycle == "REOPENED_HELD"
	default:
		return false
	}
}

func validCollectionAttemptState(operationState, attemptState string) bool {
	switch operationState {
	case "QUEUED":
		return attemptState == "QUEUED"
	case "DISPATCHING", "PENDING", "UNKNOWN":
		return attemptState == "QUEUED" || attemptState == "PENDING" ||
			attemptState == "UNKNOWN" || attemptState == "HELD"
	case "HELD":
		return attemptState == "HELD" || attemptState == "QUEUED" || attemptState == "PENDING" || attemptState == "UNKNOWN"
	case "SETTLED":
		return attemptState == "SETTLED"
	case "FAILED_CONFIRMED", "CANCELLED_UNSENT":
		return attemptState == "FAILED_CONFIRMED"
	default:
		return false
	}
}

func validFundingOperationLifecycle(scope lockedOperationScope) bool {
	switch domain.OperationState(scope.state) {
	case domain.OperationQueued:
		return scope.lifecycle == "RESERVED" && !scope.everFunded && scope.reservationState == "NEVER_SENT"
	case domain.OperationDispatching, domain.OperationPending, domain.OperationUnknown:
		return scope.lifecycle == "FUNDING_PENDING" && !scope.everFunded && scope.reservationState == "DISPATCHED"
	case domain.OperationHeld:
		return scope.lifecycle == "RESERVED" && !scope.everFunded && scope.reservationState == "NEVER_SENT" ||
			scope.lifecycle == "FUNDING_PENDING" && !scope.everFunded && scope.reservationState == "DISPATCHED" ||
			(scope.lifecycle == "ACTIVE" || scope.lifecycle == "COLLECTING" || scope.lifecycle == "REPAID" ||
				scope.lifecycle == "REOPENED_HELD") && scope.everFunded && scope.reservationState == "CONVERTED" ||
			scope.lifecycle == "VOID_UNFUNDED" && !scope.everFunded && scope.reservationState == "RELEASED"
	case domain.OperationSettled:
		return (scope.lifecycle == "ACTIVE" || scope.lifecycle == "COLLECTING" || scope.lifecycle == "REPAID" ||
			scope.lifecycle == "REOPENED_HELD") && scope.everFunded && scope.reservationState == "CONVERTED"
	case domain.OperationFailedConfirmed, domain.OperationCancelledUnsent:
		return scope.lifecycle == "VOID_UNFUNDED" && !scope.everFunded && scope.reservationState == "RELEASED"
	default:
		return false
	}
}

func lockBridgeInvoices(ctx context.Context, tx pgx.Tx, mode, bridgeID string) error {
	rows, err := tx.Query(ctx, `SELECT r.source_system,r.invoice_id FROM servicing.receivables r
		JOIN servicing.bridge_invoice_allocations a ON a.mode=r.mode AND a.source_system=r.source_system AND a.invoice_id=r.invoice_id
		WHERE a.mode=$1 AND a.bridge_id=$2 ORDER BY r.source_system,r.invoice_id FOR UPDATE OF r`, mode, bridgeID)
	if err != nil {
		return errors.New("operation_invoice_lock_failed")
	}
	defer rows.Close()
	for rows.Next() {
		var system, invoice string
		if rows.Scan(&system, &invoice) != nil {
			return errors.New("operation_invoice_lock_failed")
		}
	}
	if rows.Err() != nil {
		return errors.New("operation_invoice_lock_failed")
	}
	rows, err = tx.Query(ctx, `SELECT source_system,invoice_id FROM servicing.bridge_invoice_allocations
		WHERE mode=$1 AND bridge_id=$2 ORDER BY source_system,invoice_id FOR UPDATE`, mode, bridgeID)
	if err != nil {
		return errors.New("operation_allocation_lock_failed")
	}
	defer rows.Close()
	for rows.Next() {
		var system, invoice string
		if rows.Scan(&system, &invoice) != nil {
			return errors.New("operation_allocation_lock_failed")
		}
	}
	if rows.Err() != nil {
		return errors.New("operation_allocation_lock_failed")
	}
	return nil
}

func (s lockedOperationScope) HashOrEmpty() string {
	hash, err := s.terms.Hash()
	if err != nil {
		return ""
	}
	return hash
}

// ClaimOperationTx claims exactly one previously discovered job with a random
// 60-second token. A stale DISPATCHING lease becomes UNKNOWN, never QUEUED.
func ClaimOperationTx(ctx context.Context, tx pgx.Tx, candidate OperationCandidate) (OperationClaim, error) {
	return claimOperationTx(ctx, tx, candidate, false, "")
}

func claimOperationTx(ctx context.Context, tx pgx.Tx, candidate OperationCandidate,
	webhookWake bool, webhookEventID string) (OperationClaim, error) {
	if !claimableOperationState(candidate.State) {
		return OperationClaim{}, ErrOperationClaimStale
	}
	if webhookWake && ((candidate.State != domain.OperationPending && candidate.State != domain.OperationUnknown) || webhookEventID == "") {
		return OperationClaim{}, ErrOperationClaimStale
	}
	scope, err := lockOperationScope(ctx, tx, candidate)
	if err != nil {
		return OperationClaim{}, err
	}
	collectionUnsent := candidate.State == domain.OperationQueued &&
		(candidate.Kind == "COLLECT_FEE" || candidate.Kind == "COLLECT_PRINCIPAL")
	if scope.guardCode != "" && !collectionUnsent {
		if scope.guardCode != "MAPPING_CONFLICT" || candidate.State == domain.OperationQueued {
			return holdUnclaimedOperationTx(ctx, tx, scope, scope.guardCode)
		}
		// A changed current roster/card map cannot hide a possible old-account movement.
		// The immutable operation scope below remains authoritative for lookup-only work.
		scope.guardCode = ""
	}
	token, err := randomLeaseToken()
	if err != nil {
		return OperationClaim{}, err
	}
	var newState string
	var newVersion int64
	var expiresAt time.Time
	var retryCount int
	err = tx.QueryRow(ctx, `UPDATE servicing.operations SET
		state=CASE WHEN state='DISPATCHING' THEN 'UNKNOWN' ELSE state END,
		lookup_requested_at=CASE WHEN state='HELD' THEN NULL ELSE lookup_requested_at END,
		lookup_requested_by=CASE WHEN state='HELD' THEN NULL ELSE lookup_requested_by END,
		lease_token=$3::uuid,lease_expires_at=clock_timestamp()+($4 * interval '1 second'),
		version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state=$5 AND version=$6
		AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp())
		AND ($7::boolean OR (state='DISPATCHING' AND lease_expires_at<=clock_timestamp()) OR
			(state<>'DISPATCHING' AND (next_check_at IS NULL OR next_check_at<=clock_timestamp())))
		AND (state<>'HELD' OR lookup_requested_at IS NOT NULL)
		RETURNING state,version,lease_expires_at,retry_count`,
		string(candidate.Mode), candidate.OperationID, token, operationLeaseSeconds,
		string(candidate.State), candidate.Version, webhookWake).Scan(&newState, &newVersion, &expiresAt, &retryCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationClaim{}, ErrOperationClaimStale
	}
	if err != nil {
		return OperationClaim{}, errors.New("operation_claim_cas_failed")
	}
	scope.state = newState
	scope.version = newVersion
	scope.retryCount = retryCount
	lookupOnly := newState != "QUEUED"
	if candidate.State == domain.OperationDispatching {
		oldToken, oldExpiry := scope.leaseToken, scope.leaseExpiresAt
		if oldToken == nil || oldExpiry == nil {
			return OperationClaim{}, errors.New("dispatch_lease_evidence_missing")
		}
		if err := AppendOperationJournal(ctx, tx, candidate.Mode, candidate.OperationID, "LEASE_EXPIRED_UNKNOWN",
			"DISPATCHING", "UNKNOWN", "LEASE_EXPIRED_LOOKUP_ONLY", oldToken, oldExpiry, nil,
			map[string]any{"previous_lease_expired_at": oldExpiry.UTC(), "certainty": "UNCERTAIN"}); err != nil {
			return OperationClaim{}, err
		}
	}
	leaseToken := token
	claimEvidence := map[string]any{"lookup_only": lookupOnly, "lease_seconds": operationLeaseSeconds}
	if webhookEventID != "" {
		claimEvidence["webhook_event_id"] = webhookEventID
	}
	if err := AppendOperationJournal(ctx, tx, candidate.Mode, candidate.OperationID, "CLAIMED", newState, newState,
		"", &leaseToken, &expiresAt, nil, claimEvidence); err != nil {
		return OperationClaim{}, err
	}
	bridgeVersion, err := bumpOperationBridgeVersion(ctx, tx, candidate.Mode, scope.bridgeID)
	if err != nil {
		return OperationClaim{}, err
	}
	scope.bridgeVersion = bridgeVersion
	changeType := "OPERATION_CLAIMED"
	details := map[string]any{"state": newState, "lookup_only": lookupOnly}
	if candidate.State == domain.OperationDispatching {
		changeType = "DISPATCH_LEASE_EXPIRED_UNKNOWN"
		details["certainty"] = "UNCERTAIN"
	}
	if err := appendBridgeOperationChange(ctx, tx, scope, bridgeVersion, changeType, details); err != nil {
		return OperationClaim{}, err
	}
	if err := appendOperationChange(ctx, tx, scope, newVersion, bridgeVersion, changeType,
		details); err != nil {
		return OperationClaim{}, err
	}
	claimed := candidate
	claimed.State = domain.OperationState(newState)
	claimed.Version = newVersion
	return OperationClaim{OperationCandidate: claimed, LeaseToken: token, LeaseExpiresAt: expiresAt,
		RetryCount: retryCount, LookupOnly: lookupOnly, BridgeVersion: scope.bridgeVersion}, nil
}

func holdUnclaimedOperationTx(ctx context.Context, tx pgx.Tx, scope lockedOperationScope, code string) (OperationClaim, error) {
	if code == "" || !claimableOperationState(domain.OperationState(scope.state)) {
		return OperationClaim{}, errors.New("operation_hold_code_missing")
	}
	originalState := scope.state
	if originalState == "DISPATCHING" {
		if scope.leaseToken == nil || scope.leaseExpiresAt == nil {
			return OperationClaim{}, errors.New("dispatch_lease_evidence_missing")
		}
		var unknownVersion int64
		err := tx.QueryRow(ctx, `UPDATE servicing.operations SET state='UNKNOWN',lease_token=NULL,lease_expires_at=NULL,
			version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND state='DISPATCHING'
			AND version=$3 AND lease_token=$4::uuid AND lease_expires_at<=clock_timestamp() RETURNING version`,
			string(scope.candidate.Mode), scope.operationID, scope.version, *scope.leaseToken).Scan(&unknownVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return OperationClaim{}, ErrOperationClaimStale
		}
		if err != nil {
			return OperationClaim{}, errors.New("operation_expired_dispatch_cas_failed")
		}
		if err := AppendOperationJournal(ctx, tx, scope.candidate.Mode, scope.operationID, "LEASE_EXPIRED_UNKNOWN",
			"DISPATCHING", "UNKNOWN", "LEASE_EXPIRED_LOOKUP_ONLY", scope.leaseToken, scope.leaseExpiresAt, nil,
			map[string]any{"movement_certainty": "UNCERTAIN"}); err != nil {
			return OperationClaim{}, err
		}
		scope.state, scope.version = "UNKNOWN", unknownVersion
		scope.leaseToken, scope.leaseExpiresAt = nil, nil
	}
	stateBeforeHold := scope.state
	var version int64
	err := tx.QueryRow(ctx, `UPDATE servicing.operations SET state='HELD',lease_token=NULL,lease_expires_at=NULL,
		lookup_requested_at=NULL,lookup_requested_by=NULL,next_check_at=NULL,sanitized_error_code=$4,
		version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state=$3 AND version=$5
		AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp())
		RETURNING version`, string(scope.candidate.Mode), scope.operationID, scope.state, code, scope.version).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationClaim{}, ErrOperationClaimStale
	}
	if err != nil {
		return OperationClaim{}, errors.New("operation_hold_cas_failed")
	}
	scope.state = "HELD"
	scope.version = version
	certainty := "UNCERTAIN"
	if stateBeforeHold == "QUEUED" {
		certainty = "PROVEN_UNSENT"
	}
	if err := AppendOperationJournal(ctx, tx, scope.candidate.Mode, scope.operationID, "STATE_TRANSITION",
		stateBeforeHold, "HELD", code, scope.leaseToken, scope.leaseExpiresAt, nil,
		map[string]any{"movement_certainty": certainty}); err != nil {
		return OperationClaim{}, err
	}
	bridgeVersion := scope.bridgeVersion
	bridgeChanged := originalState != "HELD" || scope.lookupRequestedAt != nil
	if bridgeChanged {
		bridgeVersion, err = bumpOperationBridgeVersion(ctx, tx, scope.candidate.Mode, scope.bridgeID)
		if err != nil {
			return OperationClaim{}, err
		}
		scope.bridgeVersion = bridgeVersion
	}
	if err := appendOperationChange(ctx, tx, scope, version, bridgeVersion, "OPERATION_HELD", map[string]any{"code": code}); err != nil {
		return OperationClaim{}, err
	}
	if bridgeChanged {
		if err := appendBridgeOperationChange(ctx, tx, scope, bridgeVersion, "OPERATION_HELD", map[string]any{"code": code}); err != nil {
			return OperationClaim{}, err
		}
	}
	claimed := scope.candidate
	claimed.State = domain.OperationHeld
	claimed.Version = version
	return OperationClaim{OperationCandidate: claimed, RetryCount: scope.retryCount, BridgeVersion: bridgeVersion}, nil
}

// RequestOperationLookupTx durably authorizes one bounded manual lookup of a HELD operation.
func RequestOperationLookupTx(ctx context.Context, tx pgx.Tx, candidate OperationCandidate, principal, reason string) error {
	if candidate.State != domain.OperationHeld || principal == "" || !validJournalCode(reason) {
		return errors.New("operation_lookup_request_invalid")
	}
	scope, err := lockOperationScope(ctx, tx, candidate)
	if err != nil {
		return err
	}
	if scope.kind == "FUND" && scope.state == "HELD" && scope.lifecycle == "RESERVED" &&
		!scope.everFunded && scope.reservationState == "NEVER_SENT" {
		return ErrOperationLookupNotApplicable
	}
	if scope.guardCode != "" || scope.state != "HELD" {
		return ErrOperationClaimStale
	}
	var version int64
	err = tx.QueryRow(ctx, `UPDATE servicing.operations SET lookup_requested_at=clock_timestamp(),
		lookup_requested_by=$4,next_check_at=clock_timestamp(),retry_count=0,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state='HELD' AND version=$3
		AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING version`,
		string(candidate.Mode), candidate.OperationID, candidate.Version, principal).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOperationClaimStale
	}
	if err != nil {
		return errors.New("operation_lookup_request_cas_failed")
	}
	if err := AppendOperationJournal(ctx, tx, candidate.Mode, candidate.OperationID, "LOOKUP_REQUESTED",
		"HELD", "HELD", reason, nil, nil, nil, map[string]any{"principal": principal, "reason_code": reason}); err != nil {
		return err
	}
	bridgeVersion, err := bumpOperationBridgeVersion(ctx, tx, candidate.Mode, scope.bridgeID)
	if err != nil {
		return err
	}
	scope.bridgeVersion = bridgeVersion
	if err := appendOperationChange(ctx, tx, scope, version, bridgeVersion, "LOOKUP_REQUESTED",
		map[string]any{"state": "HELD", "reason_code": reason}); err != nil {
		return err
	}
	return appendBridgeOperationChange(ctx, tx, scope, bridgeVersion, "LOOKUP_REQUESTED", map[string]any{"reason_code": reason})
}

func validJournalCode(value string) bool {
	if len(value) == 0 || len(value) > 96 {
		return false
	}
	for _, ch := range value {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == ':' || ch == '-') {
			return false
		}
	}
	return true
}

func bumpOperationBridgeVersion(ctx context.Context, tx pgx.Tx, mode domain.Mode, bridgeID string) (int64, error) {
	var version int64
	err := tx.QueryRow(ctx, `UPDATE servicing.bridges SET version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 RETURNING version`, string(mode), bridgeID).Scan(&version)
	if err != nil {
		return 0, errors.New("operation_bridge_version_update_failed")
	}
	return version, nil
}

func operationEventSummary(scope lockedOperationScope, bridgeVersion int64, details map[string]any) []byte {
	var caseRef any
	if scope.caseID != "" {
		caseRef = scope.caseID
	}
	fields := map[string]any{"customer_id": scope.customerID, "account_ref": "customer:" + scope.customerID,
		"originating_case_id": caseRef, "evaluation_id": scope.evaluationID, "offer_id": scope.offerID,
		"bridge_id": scope.bridgeID, "bridge_version": bridgeVersion, "operation_id": scope.operationID,
		"operation_kind": scope.kind, "operation_state": scope.state, "request_id": scope.requestID}
	for key, value := range details {
		fields[key] = value
	}
	body, _ := json.Marshal(fields)
	return body
}

func appendOperationChange(ctx context.Context, tx pgx.Tx, scope lockedOperationScope, operationVersion,
	bridgeVersion int64, change string, details map[string]any) error {
	bridgeID := scope.bridgeID
	summary := operationEventSummary(scope, bridgeVersion, details)
	_, err := AppendChange(ctx, tx, domain.Change{Mode: scope.candidate.Mode, ResourceType: "OPERATION",
		ResourceID: scope.operationID, ResourceVersion: operationVersion, ChangeType: change, Summary: summary},
		&bridgeID, &bridgeVersion)
	return err
}

func appendBridgeOperationChange(ctx context.Context, tx pgx.Tx, scope lockedOperationScope,
	bridgeVersion int64, change string, details map[string]any) error {
	bridgeID := scope.bridgeID
	summary := operationEventSummary(scope, bridgeVersion, details)
	_, err := AppendChange(ctx, tx, domain.Change{Mode: scope.candidate.Mode, ResourceType: "BRIDGE",
		ResourceID: bridgeID, ResourceVersion: bridgeVersion, ChangeType: change, Summary: summary},
		&bridgeID, &bridgeVersion)
	return err
}
