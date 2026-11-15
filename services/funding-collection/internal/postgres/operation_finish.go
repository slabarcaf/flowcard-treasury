package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

const (
	maxProtectedOperationEvidenceBytes = 80_000_000
	maxProtectedOperationBodyBytes     = 4_000_000
)

// FinishOperationTx records one typed provider outcome behind the live lease fence.
// It records evidence and schedules work; it never posts settlement accounting.
func FinishOperationTx(ctx context.Context, tx pgx.Tx, claim OperationClaim, outcome OperationReadOutcome) error {
	if tx == nil || !validLeaseToken(claim.LeaseToken) || claim.LeaseExpiresAt.IsZero() ||
		!claimableOperationState(claim.State) || !validOutcomeSource(outcome.Source) ||
		(outcome.Source == "CREATE" && claim.State != domain.OperationDispatching) ||
		((outcome.Source == "RETRIEVE" || outcome.Source == "LOOKUP") && claim.State == domain.OperationQueued) {
		return errors.New("operation_finish_request_invalid")
	}
	scope, err := lockOperationScope(ctx, tx, claim.OperationCandidate)
	if err != nil {
		return err
	}
	if scope.leaseToken == nil || scope.leaseExpiresAt == nil || *scope.leaseToken != claim.LeaseToken ||
		!scope.leaseExpiresAt.Equal(claim.LeaseExpiresAt) {
		return ErrOperationClaimStale
	}
	if !validFinishOutcomeShape(claim, outcome) {
		return errors.New("operation_finish_outcome_shape_invalid")
	}
	plan, err := planOperationOutcome(scope, outcome)
	if err != nil {
		plan = invalidOperationOutcomePlan(outcome, err)
	}
	if outcome.HoldCode != "" {
		plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, outcome.HoldCode
		plan.nextDelaySeconds, plan.expiryBoundQueuedRetry = nil, false
		plan.movementCertainty = "UNCERTAIN"
		if plan.normalizedEvidence != nil {
			plan.normalizedEvidence["explicit_hold_code"] = outcome.HoldCode
		}
	} else if scope.guardCode != "" {
		plan = invalidOperationOutcomePlan(outcome, errors.New(scope.guardCode))
		if outcome.Source == "GUARD" {
			plan.movementCertainty = "PROVEN_UNSENT"
		}
	}
	applyBoundEvidenceConflicts(scope, outcome.Source, &plan)
	var version int64
	var finalState string
	err = tx.QueryRow(ctx, `UPDATE servicing.operations o SET
		state=CASE WHEN $14::boolean AND o.kind='FUND' AND NOT (
			EXISTS(SELECT 1 FROM servicing.reservations r WHERE r.mode=o.mode AND r.id=o.reservation_id
				AND r.state='NEVER_SENT' AND r.expires_at>clock_timestamp()) AND
			EXISTS(SELECT 1 FROM servicing.bridges b JOIN servicing.offers f ON f.mode=b.mode AND f.id=b.offer_id
				WHERE b.mode=o.mode AND b.id=o.bridge_id AND f.expires_at>clock_timestamp()))
			THEN 'HELD' ELSE $7 END,version=version+1,
		lease_token=NULL,lease_expires_at=NULL,retry_count=$8,
		next_check_at=CASE WHEN $14::boolean AND o.kind='FUND' AND NOT (
			EXISTS(SELECT 1 FROM servicing.reservations r WHERE r.mode=o.mode AND r.id=o.reservation_id
				AND r.state='NEVER_SENT' AND r.expires_at>clock_timestamp()) AND
			EXISTS(SELECT 1 FROM servicing.bridges b JOIN servicing.offers f ON f.mode=b.mode AND f.id=b.offer_id
				WHERE b.mode=o.mode AND b.id=o.bridge_id AND f.expires_at>clock_timestamp())) THEN NULL
			WHEN $9::bigint IS NULL THEN NULL ELSE clock_timestamp()+($9 * interval '1 second') END,
		provider_object_id=COALESCE(provider_object_id,$10),provider_status=COALESCE($11,provider_status),
		provider_fee_cents=COALESCE(provider_fee_cents,$12),
		sanitized_error_code=CASE WHEN $14::boolean AND o.kind='FUND' AND NOT (
			EXISTS(SELECT 1 FROM servicing.reservations r WHERE r.mode=o.mode AND r.id=o.reservation_id
				AND r.state='NEVER_SENT' AND r.expires_at>clock_timestamp()) AND
			EXISTS(SELECT 1 FROM servicing.bridges b JOIN servicing.offers f ON f.mode=b.mode AND f.id=b.offer_id
				WHERE b.mode=o.mode AND b.id=o.bridge_id AND f.expires_at>clock_timestamp()))
			THEN 'UNSENT_INTENT_EXPIRED' ELSE $13 END,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state=$3 AND version=$4 AND lease_token=$5::uuid
		AND lease_expires_at=$6 AND lease_expires_at>clock_timestamp()
		RETURNING state,version`, string(claim.Mode), claim.OperationID, string(claim.State), claim.Version,
		claim.LeaseToken, claim.LeaseExpiresAt, string(plan.state), plan.retryCount, plan.nextDelaySeconds,
		plan.providerObjectID, nullableString(plan.providerStatus), plan.providerFeeCents,
		nullableString(plan.errorCode), plan.expiryBoundQueuedRetry).Scan(&finalState, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOperationClaimStale
	}
	if err != nil {
		return errors.New("operation_finish_cas_failed")
	}
	if domain.OperationState(finalState) != plan.state {
		plan.state = domain.OperationState(finalState)
		plan.errorCode = "UNSENT_INTENT_EXPIRED"
		plan.nextDelaySeconds = nil
		plan.movementCertainty = "PROVEN_UNSENT"
		plan.normalizedEvidence["retry_expired_before_reschedule"] = true
	}
	observationID, err := persistOperationOutcome(ctx, tx, scope, plan, outcome.Source)
	if err != nil {
		return err
	}
	var observationRef *string
	if observationID != "" {
		observationRef = &observationID
	}
	fromState := string(claim.State)
	toState := string(plan.state)
	if err := AppendOperationJournal(ctx, tx, claim.Mode, claim.OperationID, "OBSERVATION_RECORDED",
		fromState, toState, plan.errorCode, &claim.LeaseToken, &claim.LeaseExpiresAt, observationRef,
		operationOutcomeJournalEvidence(outcome.Source, plan)); err != nil {
		return err
	}
	scope.state = toState
	scope.version = version
	scope.providerObjectID = plan.providerObjectID
	scope.providerStatus = nullableStringPointer(plan.providerStatus)
	bridgeVersion, err := bumpOperationBridgeVersion(ctx, tx, claim.Mode, scope.bridgeID)
	if err != nil {
		return err
	}
	scope.bridgeVersion = bridgeVersion
	changeType := "OPERATION_READ_RECORDED"
	if plan.state == domain.OperationHeld {
		changeType = "OPERATION_HELD"
	} else if plan.state != claim.State {
		changeType = "OPERATION_STATE_CHANGED"
	}
	details := map[string]any{"state": toState, "source": outcome.Source,
		"certainty": string(plan.certainty), "recovery": string(plan.recovery),
		"observation_id": observationID, "movement_certainty": string(plan.movementCertainty)}
	if plan.errorCode != "" {
		details["code"] = plan.errorCode
	}
	if err := appendBridgeOperationChange(ctx, tx, scope, bridgeVersion, changeType, details); err != nil {
		return err
	}
	return appendOperationChange(ctx, tx, scope, version, bridgeVersion, changeType, details)
}

// FinishOperation applies a typed read result in a short committed transaction.
func (r *Repository) FinishOperation(ctx context.Context, claim OperationClaim,
	outcome OperationReadOutcome) error {
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
		return errors.New("operation_finish_commit_failed")
	}
	return nil
}

func validFinishOutcomeShape(claim OperationClaim, outcome OperationReadOutcome) bool {
	if outcome.Source != "GUARD" {
		if outcome.HoldCode != "" {
			return (outcome.Source == "RETRIEVE" || outcome.Source == "LOOKUP") && outcome.NoDispatchCode == "" &&
				!(outcome.Result != nil && outcome.Lookup != nil) && validJournalCode(outcome.HoldCode)
		}
		if outcome.NoDispatchCode != "" || outcome.Result != nil && outcome.ProviderError != nil {
			return false
		}
		if outcome.ProviderError != nil && outcome.Lookup != nil {
			return outcome.Source == "LOOKUP" && outcome.Result == nil
		}
		return !(outcome.Result != nil && outcome.Lookup != nil)
	}
	if claim.State != domain.OperationQueued {
		return false
	}
	if outcome.NoDispatchCode != "" {
		return outcome.Result == nil && outcome.Lookup == nil && outcome.ProviderError == nil
	}
	return outcome.ProviderError != nil && outcome.Result == nil && outcome.Lookup == nil
}

func invalidOperationOutcomePlan(outcome OperationReadOutcome, cause error) operationFinishPlan {
	plan := operationFinishPlan{state: domain.OperationHeld, errorCode: "PROVIDER_OUTCOME_INVALID",
		certainty: provider.CertaintyUncertain, recovery: provider.RecoveryHold,
		movementCertainty: "UNCERTAIN", normalizedEvidence: map[string]any{
			"outcome": "INVALID_PROVIDER_OUTCOME", "source": outcome.Source,
			"code": "PROVIDER_OUTCOME_INVALID", "reason": safeOperationErrorCode(cause),
		}}
	switch {
	case outcome.Result != nil:
		plan.evidenceRaw = append([]byte(nil), outcome.Result.Receipt.RawEvidence...)
		plan.protectedRawBody = append([]byte(nil), outcome.Result.Receipt.RawEvidence...)
	case outcome.Lookup != nil:
		plan.evidenceRaw = hashLookupPages(outcome.Lookup.RawPages)
		plan.protectedRawPages = cloneRawPages(outcome.Lookup.RawPages)
		if outcome.ProviderError != nil && !isLoginEvidence(outcome.ProviderError.Code) {
			plan.protectedRawErrorBody = append([]byte(nil), outcome.ProviderError.RawEvidence...)
			plan.evidenceRaw = append(plan.evidenceRaw, outcome.ProviderError.RawEvidence...)
		}
	case outcome.ProviderError != nil:
		if !isLoginEvidence(outcome.ProviderError.Code) {
			plan.evidenceRaw = append([]byte(nil), outcome.ProviderError.RawEvidence...)
			plan.protectedRawBody = append([]byte(nil), outcome.ProviderError.RawEvidence...)
		}
	}
	return plan
}

func applyBoundEvidenceConflicts(scope lockedOperationScope, source string, plan *operationFinishPlan) {
	if plan.receipt == nil {
		return
	}
	code := ""
	if scope.providerObjectID != nil && *scope.providerObjectID != plan.receipt.ProviderObjectID {
		code = "PROVIDER_OBJECT_ID_CONFLICT"
	} else if source != "CREATE" && scope.providerFee != nil && plan.receipt.ProviderFeeCents != nil &&
		*scope.providerFee != *plan.receipt.ProviderFeeCents {
		code = "PROVIDER_FEE_CONFLICT"
	}
	if code == "" {
		return
	}
	plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, code
	plan.certainty, plan.movementCertainty = provider.CertaintyUncertain, "UNCERTAIN"
	plan.providerObjectID, plan.providerFeeCents, plan.providerStatus = nil, nil, ""
	plan.nextDelaySeconds, plan.expiryBoundQueuedRetry = nil, false
	plan.normalizedEvidence["conflict_code"] = code
	if scope.providerObjectID != nil {
		plan.normalizedEvidence["previous_provider_object_id"] = *scope.providerObjectID
	}
	if scope.providerFee != nil {
		plan.normalizedEvidence["previous_verified_provider_fee_cents"] = *scope.providerFee
	}
}

func isLoginEvidence(code string) bool {
	return len(code) >= len("PROVIDER_LOGIN_") && code[:len("PROVIDER_LOGIN_")] == "PROVIDER_LOGIN_"
}

func cloneRawPages(pages [][]byte) [][]byte {
	if len(pages) == 0 {
		return nil
	}
	copyPages := make([][]byte, len(pages))
	for index, page := range pages {
		copyPages[index] = append([]byte(nil), page...)
	}
	return copyPages
}

func safeOperationErrorCode(err error) string {
	if err == nil {
		return "UNKNOWN"
	}
	value := err.Error()
	if validJournalCode(value) {
		return value
	}
	return "PROVIDER_OUTCOME_INVALID"
}

type operationFinishPlan struct {
	state                  domain.OperationState
	providerStatus         string
	providerObjectID       *string
	providerFeeCents       *int64
	errorCode              string
	certainty              provider.Certainty
	recovery               provider.RecoveryClass
	movementCertainty      string
	retryCount             int
	nextDelaySeconds       *int64
	receipt                *provider.Receipt
	evidenceRaw            []byte
	normalizedEvidence     map[string]any
	protectedRawBody       []byte
	protectedRawErrorBody  []byte
	protectedRawPages      [][]byte
	expiryBoundQueuedRetry bool
}

func planLookupReadError(scope lockedOperationScope, lookup provider.LookupResult,
	providerError provider.ProviderError) operationFinishPlan {
	plan := operationFinishPlan{state: domain.OperationUnknown, errorCode: providerError.Code,
		certainty: providerError.Certainty, recovery: providerError.Recovery,
		movementCertainty: "UNCERTAIN", retryCount: scope.retryCount + 1,
		protectedRawPages: cloneRawPages(lookup.RawPages), normalizedEvidence: map[string]any{
			"outcome": "INCOMPLETE_LOOKUP_READ", "complete": lookup.Complete,
			"match_count": len(lookup.Matches), "code": providerError.Code,
			"recovery": string(providerError.Recovery), "cause": string(providerError.Cause),
			"certainty": string(providerError.Certainty), "http_status": providerError.HTTPStatus}}
	if !isLoginEvidence(providerError.Code) {
		plan.protectedRawErrorBody = append([]byte(nil), providerError.RawEvidence...)
	}
	plan.evidenceRaw = append(hashLookupPages(lookup.RawPages), 0)
	if !isLoginEvidence(providerError.Code) {
		plan.evidenceRaw = append(plan.evidenceRaw, providerError.RawEvidence...)
	}
	if providerError.Recovery == provider.RecoveryHold || providerError.Cause == provider.RecoveryHold {
		plan.state, plan.recovery = domain.OperationHeld, provider.RecoveryHold
		if len(lookup.Matches) > 1 {
			plan.errorCode = "LOOKUP_MULTIPLE_MATCHES"
		} else if len(lookup.Matches) == 1 && !lookup.Complete {
			plan.errorCode = "LOOKUP_INCOMPLETE_MATCH"
		}
		return plan
	}
	if len(lookup.Matches) > 1 {
		plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "LOOKUP_MULTIPLE_MATCHES"
		return plan
	}
	if providerError.Recovery == provider.RecoveryRetryRead || providerError.Recovery == provider.RecoveryLookupOnly {
		if scope.state == "PENDING" {
			plan.state = domain.OperationPending
		}
		if delay, ok := operationRetryDelay(scope.retryCount); ok {
			seconds := int64(delay / time.Second)
			plan.nextDelaySeconds = &seconds
			return plan
		}
	}
	plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "LOOKUP_RETRY_LIMIT_REACHED"
	return plan
}

func validOutcomeSource(source string) bool {
	switch source {
	case "CREATE", "RETRIEVE", "LOOKUP", "GUARD":
		return true
	default:
		return false
	}
}

func planOperationOutcome(scope lockedOperationScope, outcome OperationReadOutcome) (operationFinishPlan, error) {
	plan := operationFinishPlan{state: domain.OperationUnknown, certainty: provider.CertaintyUncertain,
		recovery: provider.RecoveryRetryRead, movementCertainty: "UNCERTAIN", retryCount: scope.retryCount + 1}
	count := 0
	if outcome.Result != nil {
		count++
	}
	if outcome.Lookup != nil && outcome.ProviderError == nil {
		count++
	}
	if outcome.ProviderError != nil {
		count++
	}
	if outcome.NoDispatchCode != "" {
		count++
	}
	if count != 1 {
		return plan, errors.New("operation_finish_outcome_ambiguous")
	}
	if outcome.Source == "LOOKUP" && outcome.Lookup != nil && outcome.ProviderError != nil {
		if !validRecovery(outcome.ProviderError.Recovery) || !validCertainty(outcome.ProviderError.Certainty) ||
			!validJournalCode(outcome.ProviderError.Code) {
			return plan, errors.New("operation_lookup_error_invalid")
		}
		return planLookupReadError(scope, *outcome.Lookup, *outcome.ProviderError), nil
	}
	if outcome.NoDispatchCode != "" {
		if outcome.Source != "GUARD" || !validJournalCode(outcome.NoDispatchCode) || scope.state != "QUEUED" {
			return plan, errors.New("operation_no_dispatch_outcome_invalid")
		}
		plan.state, plan.errorCode = domain.OperationHeld, outcome.NoDispatchCode
		plan.certainty, plan.recovery, plan.movementCertainty = provider.CertaintyDefinitive, provider.RecoveryHold, "PROVEN_UNSENT"
		plan.normalizedEvidence = map[string]any{"outcome": "NO_DISPATCH", "code": outcome.NoDispatchCode}
		return plan, nil
	}
	if outcome.ProviderError != nil {
		providerError := outcome.ProviderError
		if !validRecovery(providerError.Recovery) || !validCertainty(providerError.Certainty) || !validJournalCode(providerError.Code) {
			return plan, errors.New("operation_provider_error_invalid")
		}
		plan.errorCode, plan.certainty, plan.recovery = providerError.Code, providerError.Certainty, providerError.Recovery
		if !isLoginEvidence(providerError.Code) {
			plan.evidenceRaw = append([]byte(nil), providerError.RawEvidence...)
			plan.protectedRawBody = append([]byte(nil), providerError.RawEvidence...)
		}
		plan.normalizedEvidence = map[string]any{"outcome": "READ_ERROR", "code": providerError.Code,
			"recovery": string(providerError.Recovery), "certainty": string(providerError.Certainty),
			"cause": string(providerError.Cause), "http_status": providerError.HTTPStatus}
		if outcome.Source == "GUARD" {
			plan.movementCertainty = "PROVEN_UNSENT"
			// Task 8B must route permanent HELD preflight failures through explicit
			// expiry/cancel recovery; they are never possible-send UNKNOWN work.
			cause := providerError.Cause
			if cause == "" {
				cause = providerError.Recovery
			}
			if cause == provider.RecoveryHold || providerError.Recovery == provider.RecoveryHold {
				plan.state, plan.recovery = domain.OperationHeld, provider.RecoveryHold
				return plan, nil
			}
			if cause == provider.RecoveryRetryRead || providerError.Recovery == provider.RecoveryRetryRead {
				delay, ok := operationRetryDelay(scope.retryCount)
				if !ok {
					plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "PREFLIGHT_RETRY_LIMIT_REACHED"
				} else {
					seconds := int64(delay / time.Second)
					plan.state, plan.recovery = domain.OperationQueued, provider.RecoveryRetryRead
					plan.nextDelaySeconds, plan.expiryBoundQueuedRetry = &seconds, true
				}
				return plan, nil
			}
			plan.state, plan.recovery = domain.OperationHeld, provider.RecoveryHold
			return plan, nil
		}
		if outcome.Source == "CREATE" {
			if providerError.Cause == provider.RecoveryHold || providerError.Recovery == provider.RecoveryHold {
				plan.state, plan.recovery = domain.OperationHeld, provider.RecoveryHold
			} else {
				plan.recovery = provider.RecoveryLookupOnly
				plan.state = domain.OperationUnknown
			}
		}
		if outcome.Source == "RETRIEVE" && providerError.Recovery == provider.RecoveryLookupOnly {
			// A provider-level not-found on a bound object switches future reads
			// to request-ID lookup without changing or clearing the immutable ID.
			plan.providerStatus = "LOOKUP_REQUIRED"
		}
		if plan.recovery == provider.RecoveryHold {
			plan.state = domain.OperationHeld
		} else if plan.recovery == provider.RecoveryRetryRead {
			plan.state = domain.OperationUnknown
			if scope.state == "PENDING" && outcome.Source != "CREATE" {
				plan.state = domain.OperationPending
			}
		}
		if plan.state != domain.OperationHeld {
			delay, ok := operationRetryDelay(scope.retryCount)
			if !ok {
				plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "RETRY_LIMIT_REACHED"
			} else {
				seconds := int64(delay / time.Second)
				plan.nextDelaySeconds = &seconds
			}
		}
		return plan, nil
	}
	if outcome.Lookup != nil {
		lookup := outcome.Lookup
		if !validRecovery(lookup.Recovery) || !validCertainty(lookup.Certainty) {
			return plan, errors.New("operation_lookup_outcome_invalid")
		}
		if len(lookup.RawPages) == 0 {
			return plan, errors.New("operation_lookup_evidence_missing")
		}
		plan.certainty, plan.recovery = lookup.Certainty, lookup.Recovery
		plan.evidenceRaw = hashLookupPages(lookup.RawPages)
		plan.protectedRawPages = cloneRawPages(lookup.RawPages)
		plan.normalizedEvidence = map[string]any{"outcome": "LOOKUP", "complete": lookup.Complete,
			"match_count": len(lookup.Matches), "certainty": string(lookup.Certainty), "recovery": string(lookup.Recovery)}
		if len(lookup.Matches) > 1 {
			plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "LOOKUP_MULTIPLE_MATCHES"
			return plan, nil
		}
		if len(lookup.Matches) == 1 && !lookup.Complete {
			plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "LOOKUP_INCOMPLETE_MATCH"
			return plan, nil
		}
		if lookup.Recovery == provider.RecoveryHold {
			plan.state, plan.errorCode = domain.OperationHeld, "LOOKUP_REQUIRES_REVIEW"
			return plan, nil
		}
		if len(lookup.Matches) == 1 {
			result := lookup.Matches[0]
			outcome.Source = "LOOKUP"
			outcome.Lookup = nil
			outcome.Result = &result
			resultPlan, err := planOperationOutcome(scope, outcome)
			if err != nil {
				return plan, err
			}
			resultPlan.evidenceRaw = hashLookupPages(lookup.RawPages)
			resultPlan.protectedRawPages = cloneRawPages(lookup.RawPages)
			resultPlan.normalizedEvidence["lookup_complete"] = true
			return resultPlan, nil
		}
		// Empty search, even complete, never proves that a transfer did not move funds.
		plan.state = domain.OperationUnknown
		if delay, ok := operationRetryDelay(scope.retryCount); ok {
			seconds := int64(delay / time.Second)
			plan.nextDelaySeconds = &seconds
		} else {
			plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "LOOKUP_RETRY_LIMIT_REACHED"
		}
		return plan, nil
	}
	result := outcome.Result
	if !validRecovery(result.Recovery) || !validCertainty(result.Certainty) {
		return plan, errors.New("operation_provider_result_invalid")
	}
	receipt := result.Receipt
	if receipt.OperationID != scope.operationID || receipt.RequestID != scope.requestID || receipt.ProviderObjectID == "" ||
		receipt.SourceAccountRef != scope.sourceAccount || receipt.DestinationAccountRef != scope.destinationAccount ||
		receipt.AmountCents != scope.amount || receipt.Currency != scope.currency || receipt.ObservedAt.IsZero() ||
		!validProviderStatus(receipt.Status) || len(receipt.RawEvidence) == 0 || len(receipt.RawEvidence) > maxProtectedOperationBodyBytes {
		return plan, errors.New("operation_provider_receipt_scope_invalid")
	}
	if receipt.ObservedSourceAccountRef != nil && *receipt.ObservedSourceAccountRef != scope.sourceAccount {
		return plan, errors.New("operation_provider_source_mismatch")
	}
	if receipt.ProviderFeeCents != nil && *receipt.ProviderFeeCents < 0 {
		return plan, errors.New("operation_provider_fee_invalid")
	}
	if receipt.Status == "SETTLED" && (receipt.SettledAt == nil || receipt.SettledAt.IsZero() || receipt.SettledAt.After(receipt.ObservedAt.Add(5*time.Second))) {
		return plan, errors.New("operation_provider_settlement_time_invalid")
	}
	plan.receipt = &receipt
	plan.evidenceRaw = append([]byte(nil), receipt.RawEvidence...)
	plan.protectedRawBody = append([]byte(nil), receipt.RawEvidence...)
	plan.providerObjectID = &receipt.ProviderObjectID
	if outcome.Source != "CREATE" {
		plan.providerFeeCents = receipt.ProviderFeeCents
	}
	plan.providerStatus = receipt.Status
	plan.certainty, plan.recovery = result.Certainty, result.Recovery
	plan.normalizedEvidence = map[string]any{"source": outcome.Source, "provider_object_id": receipt.ProviderObjectID,
		"request_id": receipt.RequestID, "status": receipt.Status, "source_account_ref": receipt.SourceAccountRef,
		"destination_account_ref": receipt.DestinationAccountRef, "amount_cents": receipt.AmountCents,
		"currency": receipt.Currency, "observed_at": receipt.ObservedAt.UTC()}
	if receipt.SettledAt != nil {
		plan.normalizedEvidence["settled_at"] = receipt.SettledAt.UTC()
	}
	if receipt.ProviderFeeCents != nil {
		plan.normalizedEvidence["provider_fee_cents"] = *receipt.ProviderFeeCents
	}
	if outcome.HoldCode != "" {
		plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, outcome.HoldCode
		return plan, nil
	}
	if receipt.Status == "SUSPENDED" {
		plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "PROVIDER_TRANSFER_SUSPENDED"
		return plan, nil
	}
	if outcome.Source == "CREATE" {
		// A create response cannot establish verified final settlement for accounting.
		plan.state, plan.recovery = domain.OperationUnknown, provider.RecoveryLookupOnly
		plan.providerStatus = "CREATE_" + receipt.Status + "_UNVERIFIED"
		delay, ok := operationRetryDelay(scope.retryCount)
		if !ok {
			plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "RETRY_LIMIT_REACHED"
		} else {
			seconds := int64(delay / time.Second)
			plan.nextDelaySeconds = &seconds
		}
		return plan, nil
	}
	if result.Recovery == provider.RecoveryHold {
		plan.state, plan.errorCode = domain.OperationHeld, "PROVIDER_READ_REQUIRES_REVIEW"
		return plan, nil
	}
	if result.Recovery == provider.RecoveryLookupOnly {
		plan.state = domain.OperationUnknown
		if delay, ok := operationRetryDelay(scope.retryCount); ok {
			seconds := int64(delay / time.Second)
			plan.nextDelaySeconds = &seconds
		} else {
			plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "RETRY_LIMIT_REACHED"
		}
		return plan, nil
	}
	if result.Recovery == provider.RecoveryVerifyFinal && result.Certainty == provider.CertaintyDefinitive &&
		receipt.Status == "SETTLED" {
		plan.state = domain.OperationPending
		plan.nextDelaySeconds = nil
		return plan, nil
	}
	if result.Recovery == provider.RecoveryVerifyFinal && result.Certainty == provider.CertaintyDefinitive &&
		receipt.Status == "FAILED" {
		plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "FUNDING_FAILURE_REQUIRES_RETURN_PROOF"
		return plan, nil
	}
	if receipt.Status == "NEW" || receipt.Status == "PENDING" {
		plan.state = domain.OperationPending
		if delay, ok := operationPendingDelay(scope.retryCount); ok {
			seconds := int64(delay / time.Second)
			plan.nextDelaySeconds = &seconds
		} else {
			plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "PENDING_POLL_LIMIT_REACHED"
		}
		return plan, nil
	}
	plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "FINAL_STATUS_NOT_VERIFIED"
	return plan, nil
}

func validProviderStatus(status string) bool {
	switch status {
	case "NEW", "PENDING", "SUSPENDED", "SETTLED", "FAILED":
		return true
	default:
		return false
	}
}

func validRecovery(recovery provider.RecoveryClass) bool {
	switch recovery {
	case provider.RecoveryRetryRead, provider.RecoveryHold, provider.RecoveryLookupOnly, provider.RecoveryVerifyFinal:
		return true
	default:
		return false
	}
}

func validCertainty(certainty provider.Certainty) bool {
	return certainty == provider.CertaintyDefinitive || certainty == provider.CertaintyUncertain
}

func hashLookupPages(pages [][]byte) []byte {
	var joined []byte
	for _, page := range pages {
		joined = append(joined, page...)
		joined = append(joined, 0)
	}
	return joined
}

func persistOperationOutcome(ctx context.Context, tx pgx.Tx, scope lockedOperationScope,
	plan operationFinishPlan, source string) (string, error) {
	if source == "GUARD" {
		// Guard reads are not transfer observations. Their protected evidence is
		// stored in the operation journal with a nullable observation reference.
		return "", nil
	}
	if plan.normalizedEvidence == nil {
		plan.normalizedEvidence = map[string]any{"outcome": "READ_ERROR"}
	}
	protectedSize := int64(len(plan.protectedRawBody))
	if protectedSize > maxProtectedOperationBodyBytes {
		return "", errors.New("operation_evidence_size_invalid")
	}
	if int64(len(plan.protectedRawErrorBody)) > maxProtectedOperationBodyBytes ||
		protectedSize > maxProtectedOperationEvidenceBytes-int64(len(plan.protectedRawErrorBody)) {
		return "", errors.New("operation_evidence_size_invalid")
	}
	protectedSize += int64(len(plan.protectedRawErrorBody))
	for _, page := range plan.protectedRawPages {
		if int64(len(page)) > maxProtectedOperationBodyBytes ||
			protectedSize > maxProtectedOperationEvidenceBytes-int64(len(page)) {
			return "", errors.New("operation_evidence_size_invalid")
		}
		protectedSize += int64(len(page))
	}
	if protectedSize > maxProtectedOperationEvidenceBytes {
		return "", errors.New("operation_evidence_size_invalid")
	}
	evidenceFields := make(map[string]any, len(plan.normalizedEvidence)+6)
	for key, value := range plan.normalizedEvidence {
		evidenceFields[key] = value
	}
	if len(plan.protectedRawBody) > 0 {
		evidenceFields["original_response_body_encoding"] = "base64"
		evidenceFields["original_response_body_base64"] = base64.StdEncoding.EncodeToString(plan.protectedRawBody)
	}
	if len(plan.protectedRawErrorBody) > 0 {
		evidenceFields["original_error_response_body_encoding"] = "base64"
		evidenceFields["original_error_response_body_base64"] = base64.StdEncoding.EncodeToString(plan.protectedRawErrorBody)
	}
	if len(plan.protectedRawPages) > 0 {
		encodedPages := make([]string, len(plan.protectedRawPages))
		for index, page := range plan.protectedRawPages {
			encodedPages[index] = base64.StdEncoding.EncodeToString(page)
		}
		evidenceFields["original_response_pages_encoding"] = "base64"
		evidenceFields["original_response_pages_base64"] = encodedPages
	}
	evidence, err := json.Marshal(evidenceFields)
	if err != nil {
		return "", errors.New("operation_evidence_encode_failed")
	}
	raw := plan.evidenceRaw
	if len(raw) == 0 {
		raw = evidence
	}
	digest := sha256.Sum256(raw)
	effectiveAt := any(nil)
	var amount, fee any
	var status, objectID, sourceAccount, destinationAccount, requestID, currency any
	if plan.receipt != nil {
		receipt := plan.receipt
		if receipt.SettledAt != nil {
			effectiveAt = receipt.SettledAt.UTC()
		}
		amount, status, objectID = receipt.AmountCents, receipt.Status, receipt.ProviderObjectID
		sourceAccount, destinationAccount, requestID, currency = receipt.SourceAccountRef, receipt.DestinationAccountRef, receipt.RequestID, receipt.Currency
		if receipt.ProviderFeeCents != nil {
			fee = *receipt.ProviderFeeCents
		}
	}
	var observationID string
	err = tx.QueryRow(ctx, `INSERT INTO servicing.provider_observations(mode,operation_id,observation_kind,
		provider_object_id,source_account_ref,destination_account_ref,request_id,status,amount_cents,currency,
		provider_fee_cents,effective_at,observed_at,evidence_hash,normalized_evidence)
		VALUES($1,$2,'TRANSFER',$3,$4,$5,$6,$7,$8,$9,$10,$11,clock_timestamp(),$12,$13::jsonb)
		RETURNING id::text`, string(scope.candidate.Mode), scope.operationID, objectID, sourceAccount,
		destinationAccount, requestID, status, amount, currency, fee, effectiveAt,
		hex.EncodeToString(digest[:]), string(evidence)).Scan(&observationID)
	if err != nil {
		return "", errors.New("operation_observation_insert_failed")
	}
	return observationID, nil
}

func operationOutcomeJournalEvidence(source string, plan operationFinishPlan) map[string]any {
	fields := make(map[string]any, len(plan.normalizedEvidence)+6)
	for key, value := range plan.normalizedEvidence {
		fields[key] = value
	}
	fields["source"] = source
	fields["certainty"] = string(plan.certainty)
	fields["recovery"] = string(plan.recovery)
	fields["provider_status"] = plan.providerStatus
	fields["movement_certainty"] = string(plan.movementCertainty)
	if len(plan.protectedRawBody) > 0 {
		fields["original_response_body_encoding"] = "base64"
		fields["original_response_body_base64"] = base64.StdEncoding.EncodeToString(plan.protectedRawBody)
	}
	if len(plan.protectedRawPages) > 0 {
		pages := make([]string, len(plan.protectedRawPages))
		for index, page := range plan.protectedRawPages {
			pages[index] = base64.StdEncoding.EncodeToString(page)
		}
		fields["original_response_pages_encoding"] = "base64"
		fields["original_response_pages_base64"] = pages
	}
	return fields
}

func nullableStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
