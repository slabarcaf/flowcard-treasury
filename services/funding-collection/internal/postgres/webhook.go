package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

const maxServicingWebhookBytes = 10_000_000

var ErrWebhookBodyConflict = errors.New("webhook_event_body_conflict")

// ServicingWebhookReceipt is a server-classified wakeup, never a financial fact.
type ServicingWebhookReceipt struct {
	Mode         domain.Mode
	Source       string
	EventID      string
	EventName    string
	TargetKind   string
	RequestID    string
	CustomerID   string
	CardID       string
	DepositID    string
	InitialState string
	BlockedCode  string
	RawBody      []byte
}

// ReceiveServicingWebhook durably records immutable signed bytes and one
// mutable processing job before the HTTP layer acknowledges receipt.
func (r *Repository) ReceiveServicingWebhook(ctx context.Context,
	input ServicingWebhookReceipt) (bool, error) {
	if (input.Mode != domain.ModeDemo && input.Mode != domain.ModeSandbox) || input.Source != "AIRWALLEX_SERVICING" ||
		input.EventID == "" || len(input.EventID) > 256 || input.EventName == "" || len(input.EventName) > 128 || len(input.RawBody) == 0 ||
		len(input.RawBody) > maxServicingWebhookBytes ||
		(input.InitialState != "PENDING" && input.InitialState != "HELD") ||
		(input.TargetKind != "FUND_REQUEST" && input.TargetKind != "DEPOSIT" && input.TargetKind != "UNSUPPORTED") {
		return false, errors.New("servicing_webhook_receipt_invalid")
	}
	if input.TargetKind == "FUND_REQUEST" && input.RequestID == "" ||
		input.TargetKind == "DEPOSIT" && (input.CustomerID == "" || input.CardID == "" || input.DepositID == "") {
		return false, errors.New("servicing_webhook_target_invalid")
	}
	digest := sha256.Sum256(input.RawBody)
	payloadHash := hex.EncodeToString(digest[:])
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	tag, err := tx.Exec(txCtx, `INSERT INTO servicing.webhook_inbox(mode,source,event_id,raw_body,payload_hash)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(mode,source,event_id) DO NOTHING`,
		string(input.Mode), input.Source, input.EventID, input.RawBody, payloadHash)
	if err != nil {
		return false, errors.New("servicing_webhook_inbox_insert_failed")
	}
	inserted := tag.RowsAffected() == 1
	var storedBody []byte
	var storedHash string
	// The unique insert serializes duplicate event IDs, and inbox bytes are
	// immutable; SELECT FOR SHARE would require UPDATE privilege from runtime.
	if err := tx.QueryRow(txCtx, `SELECT raw_body,payload_hash FROM servicing.webhook_inbox
		WHERE mode=$1 AND source=$2 AND event_id=$3`,
		string(input.Mode), input.Source, input.EventID).Scan(&storedBody, &storedHash); err != nil {
		return false, errors.New("servicing_webhook_inbox_read_failed")
	}
	if storedHash != payloadHash || !bytes.Equal(storedBody, input.RawBody) {
		return false, ErrWebhookBodyConflict
	}
	_, err = tx.Exec(txCtx, `INSERT INTO servicing.webhook_jobs(mode,source,event_id,target_kind,
		request_id,customer_id,card_id,deposit_id,state,sanitized_error_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(mode,source,event_id) DO NOTHING`,
		string(input.Mode), input.Source, input.EventID, input.TargetKind,
		nullableString(input.RequestID), nullableString(input.CustomerID), nullableString(input.CardID),
		nullableString(input.DepositID), input.InitialState, nullableString(input.BlockedCode))
	if err != nil {
		return false, errors.New("servicing_webhook_job_insert_failed")
	}
	if err := tx.Commit(txCtx); err != nil {
		return false, errors.New("servicing_webhook_receipt_commit_failed")
	}
	return !inserted, nil
}

// WebhookJobCandidate is the bounded read-side claim hint for webhook work.
type WebhookJobCandidate struct {
	Mode       domain.Mode
	Source     string
	EventID    string
	TargetKind string
	RequestID  string
	CustomerID string
	CardID     string
	DepositID  string
	State      string
	Version    int64
}

// WebhookJobClaim is the fenced lease on one durable webhook wakeup.
type WebhookJobClaim struct {
	WebhookJobCandidate
	LeaseToken     string
	LeaseExpiresAt time.Time
	RetryCount     int
}

var webhookRetryDelays = [...]time.Duration{5 * time.Second, 30 * time.Second,
	120 * time.Second, 300 * time.Second, 900 * time.Second}

// DiscoverWebhookJobs returns bounded metadata-only wakeups due for processing.
func (r *Repository) DiscoverWebhookJobs(ctx context.Context, mode domain.Mode,
	limit int) ([]WebhookJobCandidate, error) {
	if (mode != domain.ModeDemo && mode != domain.ModeSandbox) || limit < 1 {
		return nil, errors.New("webhook_job_discovery_request_invalid")
	}
	limit = boundedCandidates(limit)
	rows, err := r.pool.Query(ctx, `SELECT mode,source,event_id,target_kind,COALESCE(request_id,''),
		COALESCE(customer_id,''),COALESCE(card_id,''),COALESCE(deposit_id,''),state,version
		FROM servicing.webhook_jobs WHERE mode=$1 AND
		((state IN ('PENDING','RETRY') AND next_check_at<=clock_timestamp()) OR
		 (state='PROCESSING' AND lease_expires_at<=clock_timestamp()))
		ORDER BY next_check_at,created_at,event_id LIMIT $2`, string(mode), limit)
	if err != nil {
		return nil, errors.New("webhook_job_discovery_failed")
	}
	defer rows.Close()
	items := make([]WebhookJobCandidate, 0, limit)
	for rows.Next() {
		var item WebhookJobCandidate
		if rows.Scan(&item.Mode, &item.Source, &item.EventID, &item.TargetKind, &item.RequestID,
			&item.CustomerID, &item.CardID, &item.DepositID, &item.State, &item.Version) != nil {
			return nil, errors.New("webhook_job_discovery_failed")
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("webhook_job_discovery_failed")
	}
	return items, nil
}

// ClaimWebhookJob commits a token-fenced job lease before any provider read.
func (r *Repository) ClaimWebhookJob(ctx context.Context,
	candidate WebhookJobCandidate) (WebhookJobClaim, error) {
	if (candidate.Mode != domain.ModeDemo && candidate.Mode != domain.ModeSandbox) ||
		candidate.Source != "AIRWALLEX_SERVICING" || candidate.EventID == "" || candidate.Version < 1 {
		return WebhookJobClaim{}, errors.New("webhook_job_claim_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return WebhookJobClaim{}, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	token, err := randomLeaseToken()
	if err != nil {
		return WebhookJobClaim{}, err
	}
	var state, requestID, customerID, cardID, depositID string
	var version int64
	var expiresAt time.Time
	var retryCount int
	err = tx.QueryRow(txCtx, `UPDATE servicing.webhook_jobs SET state='PROCESSING',version=version+1,
		lease_token=$4::uuid,lease_expires_at=clock_timestamp()+interval '60 seconds',
		updated_at=clock_timestamp()
		WHERE mode=$1 AND source=$2 AND event_id=$3 AND version=$5
		AND ((state='PROCESSING' AND lease_expires_at<=clock_timestamp()) OR
			(state IN ('PENDING','RETRY') AND next_check_at<=clock_timestamp()))
		RETURNING state,version,lease_expires_at,retry_count,COALESCE(request_id,''),
		COALESCE(customer_id,''),COALESCE(card_id,''),COALESCE(deposit_id,'')`,
		string(candidate.Mode), candidate.Source, candidate.EventID, token, candidate.Version).Scan(
		&state, &version, &expiresAt, &retryCount, &requestID, &customerID, &cardID, &depositID)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookJobClaim{}, ErrOperationClaimStale
	}
	if err != nil {
		return WebhookJobClaim{}, errors.New("webhook_job_claim_cas_failed")
	}
	if err := tx.Commit(txCtx); err != nil {
		return WebhookJobClaim{}, errors.New("webhook_job_claim_commit_failed")
	}
	candidate.State, candidate.Version = state, version
	candidate.RequestID, candidate.CustomerID, candidate.CardID, candidate.DepositID = requestID, customerID, cardID, depositID
	return WebhookJobClaim{WebhookJobCandidate: candidate, LeaseToken: token,
		LeaseExpiresAt: expiresAt, RetryCount: retryCount}, nil
}

// CompleteWebhookJobTx commits one job disposition under its live lease fence.
func CompleteWebhookJobTx(ctx context.Context, tx pgx.Tx, claim WebhookJobClaim,
	disposition, code string) error {
	if tx == nil || claim.Mode != domain.ModeDemo && claim.Mode != domain.ModeSandbox ||
		!validLeaseToken(claim.LeaseToken) || claim.LeaseExpiresAt.IsZero() ||
		(disposition != "DONE" && disposition != "HELD" && disposition != "RETRY") ||
		(code != "" && !validJournalCode(code)) {
		return errors.New("webhook_job_finish_invalid")
	}
	state, sanitizedCode := disposition, nullableString(code)
	var delay any
	if disposition == "RETRY" {
		if claim.RetryCount >= len(webhookRetryDelays) {
			state, sanitizedCode = "HELD", nullableString("WEBHOOK_RETRY_LIMIT_REACHED")
		} else {
			seconds := int64(webhookRetryDelays[claim.RetryCount] / time.Second)
			delay = seconds
		}
	}
	var processed any
	if state == "DONE" {
		processed = true
	}
	tag, err := tx.Exec(ctx, `UPDATE servicing.webhook_jobs SET state=$7,version=version+1,
		lease_token=NULL,lease_expires_at=NULL,retry_count=retry_count+CASE WHEN $7='RETRY' THEN 1 ELSE 0 END,
		next_check_at=CASE WHEN $7='RETRY' THEN clock_timestamp()+($8::bigint*interval '1 second') ELSE next_check_at END,
		sanitized_error_code=$9,processed_at=CASE WHEN $10::boolean THEN clock_timestamp() ELSE processed_at END,
		updated_at=clock_timestamp()
		WHERE mode=$1 AND source=$2 AND event_id=$3 AND state='PROCESSING' AND version=$4
		AND lease_token=$5::uuid AND lease_expires_at=$6 AND lease_expires_at>clock_timestamp()`,
		string(claim.Mode), claim.Source, claim.EventID, claim.Version, claim.LeaseToken,
		claim.LeaseExpiresAt, state, delay, sanitizedCode, processed != nil)
	if err != nil {
		return errors.New("webhook_job_finish_cas_failed")
	}
	if tag.RowsAffected() != 1 {
		return ErrOperationClaimStale
	}
	return nil
}

// CompleteWebhookJob commits a disposition in its own short transaction.
func (r *Repository) CompleteWebhookJob(ctx context.Context, claim WebhookJobClaim,
	disposition, code string) error {
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if err := CompleteWebhookJobTx(txCtx, tx, claim, disposition, code); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("webhook_job_finish_commit_failed")
	}
	return nil
}

// ClaimWebhookOperation resolves only the saved request identity and obtains
// its read lease in the same transaction that consumes the webhook wakeup.
func (r *Repository) ClaimWebhookOperation(ctx context.Context, job WebhookJobClaim,
	facilityID string) (OperationClaim, bool, error) {
	if job.TargetKind != "FUND_REQUEST" || job.RequestID == "" || facilityID == "" {
		return OperationClaim{}, false, errors.New("webhook_transfer_target_invalid")
	}
	candidate, err := r.operationCandidateByRequestID(ctx, job.Mode, facilityID, job.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := r.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_OPERATION_NOT_FOUND"); err != nil {
			return OperationClaim{}, false, err
		}
		return OperationClaim{}, false, nil
	}
	if err != nil {
		return OperationClaim{}, false, err
	}
	switch candidate.State {
	case domain.OperationQueued:
		if candidate.Kind != "FUND" {
			return OperationClaim{}, false, r.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_COLLECTION_SEND_UNSUPPORTED")
		}
		return OperationClaim{}, false, r.CompleteWebhookJob(ctx, job, "DONE", "WEBHOOK_BEFORE_DISPATCH")
	case domain.OperationHeld:
		return OperationClaim{}, false, r.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_OPERATION_REQUIRES_REVIEW")
	case domain.OperationSettled, domain.OperationFailedConfirmed, domain.OperationCancelledUnsent:
		return OperationClaim{}, false, r.CompleteWebhookJob(ctx, job, "DONE", "WEBHOOK_OPERATION_TERMINAL")
	case domain.OperationPending, domain.OperationUnknown, domain.OperationDispatching:
	default:
		return OperationClaim{}, false, r.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_OPERATION_STATE_UNSUPPORTED")
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return OperationClaim{}, false, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	forceRead := candidate.State == domain.OperationPending || candidate.State == domain.OperationUnknown
	claim, claimErr := claimOperationTx(txCtx, tx, candidate, forceRead, job.EventID)
	if errors.Is(claimErr, ErrOperationClaimStale) {
		if err := CompleteWebhookJobTx(txCtx, tx, job, "DONE", "WEBHOOK_OPERATION_ALREADY_CLAIMED"); err != nil {
			return OperationClaim{}, false, err
		}
		if err := tx.Commit(txCtx); err != nil {
			return OperationClaim{}, false, errors.New("webhook_operation_wakeup_commit_failed")
		}
		return OperationClaim{}, false, nil
	}
	if claimErr != nil {
		return OperationClaim{}, false, claimErr
	}
	disposition, code := "DONE", "WEBHOOK_OPERATION_CLAIMED"
	if claim.LeaseToken == "" {
		disposition, code = "HELD", "WEBHOOK_OPERATION_GUARD_HELD"
	}
	if err := CompleteWebhookJobTx(txCtx, tx, job, disposition, code); err != nil {
		return OperationClaim{}, false, err
	}
	if err := tx.Commit(txCtx); err != nil {
		return OperationClaim{}, false, errors.New("webhook_operation_wakeup_commit_failed")
	}
	return claim, claim.LeaseToken != "", nil
}

func (r *Repository) operationCandidateByRequestID(ctx context.Context, mode domain.Mode,
	facilityID, requestID string) (OperationCandidate, error) {
	var candidate OperationCandidate
	err := r.pool.QueryRow(ctx, `SELECT o.mode,o.id::text,b.facility_id,b.customer_id,b.id::text,
		o.kind,o.state,o.version FROM servicing.operations o
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND b.facility_id=$2 AND o.request_id=$3`,
		string(mode), facilityID, requestID).Scan(&candidate.Mode, &candidate.OperationID,
		&candidate.FacilityID, &candidate.CustomerID, &candidate.BridgeID,
		&candidate.Kind, &candidate.State, &candidate.Version)
	if err != nil {
		return OperationCandidate{}, err
	}
	return candidate, nil
}

// ResolveWebhookOperation resolves the signed request reference against the
// configured book, never against an event-supplied provider object or account.
func (r *Repository) ResolveWebhookOperation(ctx context.Context, job WebhookJobClaim,
	facilityID string) (OperationCandidate, error) {
	if job.TargetKind != "FUND_REQUEST" || job.RequestID == "" || facilityID == "" {
		return OperationCandidate{}, errors.New("webhook_transfer_target_invalid")
	}
	return r.operationCandidateByRequestID(ctx, job.Mode, facilityID, job.RequestID)
}

// LoadTerminalOperationReadIntent loads only immutable saved scope for a
// terminal webhook audit GET; it does not reopen or reauthorize that operation.
func (r *Repository) LoadTerminalOperationReadIntent(ctx context.Context,
	candidate OperationCandidate) (OperationReadIntent, error) {
	if candidate.State != domain.OperationSettled && candidate.State != domain.OperationFailedConfirmed &&
		candidate.State != domain.OperationCancelledUnsent {
		return OperationReadIntent{}, errors.New("terminal_operation_state_invalid")
	}
	var mode, id, kind, state, requestID, payloadHash, source, destination, currency, bridgeID string
	var facilityID, customerID string
	var version, amount, lateFee, contractFee, principal int64
	var providerObjectID, providerStatus *string
	err := r.pool.QueryRow(ctx, `SELECT o.mode,o.id::text,o.kind,o.state,o.version,o.request_id,o.payload_hash,
		o.source_account_ref,o.destination_account_ref,o.amount_cents,o.currency,o.bridge_id::text,
		o.late_fee_cents,o.contract_fee_cents,o.principal_cents,o.provider_object_id,o.provider_status,
		b.facility_id,b.customer_id FROM servicing.operations o
		JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id
		WHERE o.mode=$1 AND o.id=$2 AND o.state=$3 AND o.version=$4`,
		string(candidate.Mode), candidate.OperationID, string(candidate.State), candidate.Version).Scan(
		&mode, &id, &kind, &state, &version, &requestID, &payloadHash, &source, &destination,
		&amount, &currency, &bridgeID, &lateFee, &contractFee, &principal, &providerObjectID,
		&providerStatus, &facilityID, &customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationReadIntent{}, ErrOperationClaimStale
	}
	if err != nil {
		return OperationReadIntent{}, errors.New("terminal_operation_read_intent_unavailable")
	}
	if mode != string(candidate.Mode) || id != candidate.OperationID || kind != candidate.Kind ||
		version != candidate.Version || state != string(candidate.State) || bridgeID != candidate.BridgeID ||
		facilityID != candidate.FacilityID || customerID != candidate.CustomerID || requestID == "" ||
		!validFundingHash(payloadHash) || source == "" || destination == "" || currency != "USD" || amount <= 0 {
		return OperationReadIntent{}, errors.New("terminal_operation_scope_invalid")
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
		return OperationReadIntent{}, errors.New("terminal_operation_payload_invalid")
	}
	return OperationReadIntent{Operation: operation, ProviderObjectID: providerObjectID,
		ProviderStatus: providerStatus}, nil
}

// RecordTerminalWebhookRead stores a final-state audit GET without regressing a
// terminal operation. Contradictory or inconclusive evidence creates a hold.
func (r *Repository) RecordTerminalWebhookRead(ctx context.Context, job WebhookJobClaim,
	candidate OperationCandidate, outcome OperationReadOutcome, clock func() time.Time,
	pricing domain.Pricing, policyHash string) error {
	if clock == nil {
		return errors.New("terminal_webhook_clock_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if err := RecordTerminalWebhookReadTx(txCtx, tx, job, candidate, outcome, clock,
		pricing, policyHash); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("terminal_webhook_read_commit_failed")
	}
	return nil
}

// RecordTerminalWebhookReadTx fences audit observations with the webhook-job
// lease and preserves terminal operation state unless conflicting evidence
// requires a review hold.
func RecordTerminalWebhookReadTx(ctx context.Context, tx pgx.Tx, job WebhookJobClaim,
	candidate OperationCandidate, outcome OperationReadOutcome, clock func() time.Time,
	pricing domain.Pricing, policyHash string) error {
	if tx == nil || job.TargetKind != "FUND_REQUEST" || candidate.Mode != job.Mode ||
		clock == nil ||
		(candidate.State != domain.OperationSettled && candidate.State != domain.OperationFailedConfirmed &&
			candidate.State != domain.OperationCancelledUnsent) ||
		(outcome.Source != "RETRIEVE" && outcome.Source != "LOOKUP") {
		return errors.New("terminal_webhook_read_invalid")
	}
	var collectionParentIDs []string
	var err error
	collectionParentIDs, err = lockCollectionCustomerBridges(ctx, tx, candidate)
	if err != nil {
		return err
	}
	scope, err := lockOperationScope(ctx, tx, candidate)
	if err != nil {
		return err
	}
	if (candidate.Kind == "COLLECT_FEE" || candidate.Kind == "COLLECT_PRINCIPAL") &&
		outcome.Result != nil && outcome.Result.Receipt.Status == "REVERSED" {
		return recordCollectionTerminalReversalTx(ctx, tx, job, candidate, scope, outcome,
			collectionParentIDs, clock().UTC(), pricing, policyHash)
	}
	plan, err := planOperationOutcome(scope, outcome)
	if err != nil {
		plan = invalidOperationOutcomePlan(outcome, err)
	}
	applyBoundEvidenceConflicts(scope, outcome.Source, &plan)
	consistent := scope.guardCode == "" && terminalFundingObservationMatches(ctx, tx, scope, candidate.State, plan)
	observationID, err := persistOperationOutcome(ctx, tx, scope, plan, outcome.Source)
	if err != nil {
		return err
	}
	if plan.receipt == nil && candidate.State != domain.OperationCancelledUnsent &&
		candidate.State != domain.OperationFailedConfirmed && plan.errorCode != "LOOKUP_MULTIPLE_MATCHES" &&
		plan.errorCode != "LOOKUP_INCOMPLETE_MATCH" && plan.errorCode != "PROVIDER_OUTCOME_INVALID" {
		if err := AppendOperationJournal(ctx, tx, candidate.Mode, candidate.OperationID,
			"OBSERVATION_RECORDED", string(candidate.State), string(candidate.State), plan.errorCode,
			nil, nil, &observationID, map[string]any{"source": outcome.Source,
				"webhook_event_id": job.EventID, "terminal_recheck": "INCONCLUSIVE",
				"recovery": string(plan.recovery)}); err != nil {
			return err
		}
		disposition := "HELD"
		if plan.recovery == provider.RecoveryRetryRead || plan.recovery == provider.RecoveryLookupOnly {
			disposition = "RETRY"
		}
		code := plan.errorCode
		if code == "" {
			code = "TERMINAL_PROVIDER_READ_INCONCLUSIVE"
		}
		return CompleteWebhookJobTx(ctx, tx, job, disposition, code)
	}
	if consistent {
		if err := AppendOperationJournal(ctx, tx, candidate.Mode, candidate.OperationID,
			"OBSERVATION_RECORDED", string(candidate.State), string(candidate.State), "",
			nil, nil, &observationID, map[string]any{"source": outcome.Source,
				"provider_status": plan.providerStatus, "webhook_event_id": job.EventID,
				"terminal_recheck": "MATCHED"}); err != nil {
			return err
		}
		return CompleteWebhookJobTx(ctx, tx, job, "DONE", "WEBHOOK_TERMINAL_MATCHED")
	}
	plan.state, plan.recovery, plan.errorCode = domain.OperationHeld, provider.RecoveryHold, "TERMINAL_PROVIDER_CONFLICT"
	plan.normalizedEvidence["terminal_webhook_event_id"] = job.EventID
	var operationVersion int64
	err = tx.QueryRow(ctx, `UPDATE servicing.operations SET state='HELD',version=version+1,
		provider_object_id=COALESCE(provider_object_id,$5),
		provider_fee_cents=COALESCE(provider_fee_cents,$6),sanitized_error_code=$7,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state=$3 AND version=$4 AND lease_token IS NULL
		AND lease_expires_at IS NULL AND (provider_object_id IS NULL OR provider_object_id=$5 OR $5::text IS NULL)
		AND (provider_fee_cents IS NULL OR provider_fee_cents=$6 OR $6::bigint IS NULL)
		RETURNING version`, string(candidate.Mode), candidate.OperationID, string(candidate.State), candidate.Version,
		plan.providerObjectID, nullableStringPointerValue(plan.providerFeeCents), plan.errorCode).Scan(&operationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOperationClaimStale
	}
	if err != nil {
		return errors.New("terminal_operation_hold_cas_failed")
	}
	var holdReasons []string
	if err := tx.QueryRow(ctx, `SELECT hold_reasons FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(candidate.Mode), scope.bridgeID).Scan(&holdReasons); err != nil {
		return errors.New("terminal_bridge_hold_read_failed")
	}
	newHolds := appendUniqueString(holdReasons, "TERMINAL_PROVIDER_CONFLICT")
	var bridgeVersion int64
	err = tx.QueryRow(ctx, `UPDATE servicing.bridges SET hold_reasons=$3,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 RETURNING version`, string(candidate.Mode), scope.bridgeID, newHolds).Scan(&bridgeVersion)
	if err != nil {
		return errors.New("terminal_bridge_hold_update_failed")
	}
	if err := holdCustomerCollections(ctx, tx, candidate.Mode, scope.customerID,
		collectionParentIDs, "TERMINAL_PROVIDER_CONFLICT", map[string]any{
			"operation_id": scope.operationID, "provider_observation_id": observationID,
			"webhook_event_id": job.EventID, "provider_status": plan.providerStatus,
			"movement_certainty": "UNCERTAIN"}); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(candidate.Mode), scope.bridgeID).Scan(&bridgeVersion); err != nil {
		return errors.New("terminal_bridge_hold_version_read_failed")
	}
	if err := AppendOperationJournal(ctx, tx, candidate.Mode, candidate.OperationID, "OBSERVATION_RECORDED",
		string(candidate.State), "HELD", plan.errorCode, nil, nil, &observationID,
		map[string]any{"source": outcome.Source, "provider_status": plan.providerStatus,
			"movement_certainty": "UNCERTAIN", "webhook_event_id": job.EventID,
			"hold_code": plan.errorCode}); err != nil {
		return err
	}
	scope.state, scope.version, scope.bridgeVersion = "HELD", operationVersion, bridgeVersion
	details := map[string]any{"state": "HELD", "hold_code": plan.errorCode,
		"provider_observation_id": observationID, "webhook_event_id": job.EventID,
		"provider_status": plan.providerStatus}
	if err := appendBridgeOperationChange(ctx, tx, scope, bridgeVersion, "TERMINAL_PROVIDER_CONFLICT", details); err != nil {
		return err
	}
	if err := appendOperationChange(ctx, tx, scope, operationVersion, bridgeVersion,
		"TERMINAL_PROVIDER_CONFLICT", details); err != nil {
		return err
	}
	return CompleteWebhookJobTx(ctx, tx, job, "HELD", "TERMINAL_PROVIDER_CONFLICT")
}

func terminalFundingObservationMatches(ctx context.Context, tx pgx.Tx, scope lockedOperationScope,
	state domain.OperationState, plan operationFinishPlan) bool {
	if state != domain.OperationSettled || plan.receipt == nil || plan.state == domain.OperationHeld ||
		plan.providerStatus != "SETTLED" || plan.recovery != provider.RecoveryVerifyFinal ||
		plan.certainty != provider.CertaintyDefinitive || scope.providerStatus == nil ||
		*scope.providerStatus != "SETTLED" || scope.providerObjectID == nil ||
		*scope.providerObjectID != plan.receipt.ProviderObjectID || scope.providerFee == nil ||
		plan.receipt.ProviderFeeCents == nil || *scope.providerFee != *plan.receipt.ProviderFeeCents ||
		plan.receipt.ObservedSourceAccountRef == nil || *plan.receipt.ObservedSourceAccountRef != scope.sourceAccount {
		return false
	}
	var objectID, source, destination, requestID, status, currency string
	var amount, fee int64
	var effectiveAt *time.Time
	err := tx.QueryRow(ctx, `SELECT provider_object_id,source_account_ref,destination_account_ref,
		request_id,status,amount_cents,currency,provider_fee_cents,effective_at
		FROM servicing.provider_observations WHERE mode=$1 AND operation_id=$2
		AND observation_kind='TRANSFER' AND status='SETTLED'
		ORDER BY observed_at DESC,id DESC LIMIT 1`, string(scope.candidate.Mode), scope.operationID).Scan(
		&objectID, &source, &destination, &requestID, &status, &amount, &currency, &fee, &effectiveAt)
	if err != nil || effectiveAt == nil || plan.receipt.SettledAt == nil {
		return false
	}
	return objectID == plan.receipt.ProviderObjectID && source == scope.sourceAccount &&
		destination == scope.destinationAccount && requestID == scope.requestID && status == "SETTLED" &&
		amount == scope.amount && currency == scope.currency && fee == *plan.receipt.ProviderFeeCents &&
		effectiveAt.Equal(plan.receipt.SettledAt.UTC())
}

func nullableStringPointerValue(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// TrustedDepositScope is resolved from the server-owned roster/card mapping.
type TrustedDepositScope struct {
	Mode       domain.Mode
	CustomerID string
	CardID     string
	AccountRef string
}

// ResolveTrustedDepositScope uses event IDs only as lookup hints; account scope
// comes from the active card and roster records.
func (r *Repository) ResolveTrustedDepositScope(ctx context.Context, mode domain.Mode,
	customerID, cardID string) (TrustedDepositScope, error) {
	if (mode != domain.ModeDemo && mode != domain.ModeSandbox) || customerID == "" || cardID == "" {
		return TrustedDepositScope{}, errors.New("webhook_deposit_scope_invalid")
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.customers WHERE mode=$1 AND id=$2)`,
		string(mode), customerID).Scan(&exists); err != nil || !exists {
		if err != nil {
			return TrustedDepositScope{}, errors.New("webhook_deposit_customer_read_failed")
		}
		return TrustedDepositScope{}, errors.New("webhook_deposit_customer_unavailable")
	}
	rows, err := r.pool.Query(ctx, `SELECT b.airwallex_account_id,b.is_active,
		COALESCE(r.airwallex_connected_account_id,''),COALESCE(r.is_active,false)
		FROM public.issuing_card_bindings b LEFT JOIN public.customer_roster r ON r.customer_id=b.customer_id
		WHERE b.customer_id=$1 AND b.card_id=$2 LIMIT 2`, customerID, cardID)
	if err != nil {
		return TrustedDepositScope{}, errors.New("webhook_deposit_mapping_read_failed")
	}
	defer rows.Close()
	var account, rosterAccount string
	var cardActive, rosterActive bool
	count := 0
	for rows.Next() {
		count++
		if rows.Scan(&account, &cardActive, &rosterAccount, &rosterActive) != nil {
			return TrustedDepositScope{}, errors.New("webhook_deposit_mapping_read_failed")
		}
	}
	if rows.Err() != nil {
		return TrustedDepositScope{}, errors.New("webhook_deposit_mapping_read_failed")
	}
	if count != 1 || !cardActive || !rosterActive || account == "" || account != rosterAccount {
		return TrustedDepositScope{}, errors.New("webhook_deposit_mapping_conflict")
	}
	return TrustedDepositScope{Mode: mode, CustomerID: customerID, CardID: cardID, AccountRef: account}, nil
}

// StoreVerifiedDepositWebhook persists a canonical GET fact and completes its
// wakeup in one transaction. It creates no collection debit or budget.
func (r *Repository) StoreVerifiedDepositWebhook(ctx context.Context, job WebhookJobClaim,
	facilityID string, scope TrustedDepositScope, observation provider.DepositObservation,
	clock func() time.Time) error {
	if job.TargetKind != "DEPOSIT" || scope.Mode != job.Mode || scope.CustomerID != job.CustomerID ||
		scope.CardID != job.CardID || scope.AccountRef == "" || observation.AccountRef != scope.AccountRef ||
		observation.CustomerID != scope.CustomerID || observation.DepositID != job.DepositID ||
		observation.Currency != "USD" || observation.Status != "SETTLED" || observation.Kind != "EXTERNAL_SETTLED" ||
		observation.SettledAt == nil || observation.ProviderFeeCents == nil || len(observation.RawEvidence) == 0 ||
		len(observation.RawEvidence) > 4_000_000 || clock == nil {
		return errors.New("webhook_deposit_observation_invalid")
	}
	fee := *observation.ProviderFeeCents
	if observation.GrossCents <= 0 || observation.NetCents <= 0 || fee < 0 ||
		observation.NetCents > int64(^uint64(0)>>1)-fee || observation.NetCents+fee != observation.GrossCents ||
		!fundingTimeUTC(*observation.SettledAt) {
		return errors.New("webhook_deposit_amount_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if _, err := lockFundingFacility(txCtx, tx, string(job.Mode), facilityID); err != nil {
		return err
	}
	if _, err := lockFundingCustomer(txCtx, tx, string(job.Mode), scope.CustomerID); err != nil {
		return errors.New("webhook_deposit_customer_lock_failed")
	}
	bridgeIDs, err := lockCustomerBridges(txCtx, tx, string(job.Mode), scope.CustomerID)
	if err != nil {
		return err
	}
	if err := lockCustomerReservations(txCtx, tx, string(job.Mode), scope.CustomerID); err != nil {
		return err
	}
	if err := lockCustomerInvoices(txCtx, tx, string(job.Mode), scope.CustomerID); err != nil {
		return err
	}
	now := clock().UTC()
	if now.IsZero() || observation.SettledAt.After(now.Add(5*time.Second)) {
		return errors.New("webhook_deposit_settlement_time_invalid")
	}
	var existingID, existingCustomer, existingCurrency, existingStatus, existingKind string
	var existingGross, existingNet, existingFee int64
	var existingSettled time.Time
	depositErr := tx.QueryRow(txCtx, `SELECT id::text,customer_id,currency,gross_cents,net_cents,provider_fee_cents,
		status,kind,settled_at FROM servicing.deposits WHERE mode=$1 AND account_ref=$2 AND provider_deposit_id=$3
		FOR UPDATE`, string(job.Mode), scope.AccountRef, observation.DepositID).Scan(
		&existingID, &existingCustomer, &existingCurrency, &existingGross, &existingNet, &existingFee,
		&existingStatus, &existingKind, &existingSettled)
	depositExists := depositErr == nil
	if depositErr != nil && !errors.Is(depositErr, pgx.ErrNoRows) {
		return errors.New("webhook_deposit_dedup_read_failed")
	}
	var affectedAttempts []depositAttemptRef
	if depositExists {
		rows, err := tx.Query(txCtx, `SELECT id::text,bridge_id::text,customer_id,state
			FROM servicing.collection_attempts WHERE mode=$1 AND deposit_id=$2 ORDER BY bridge_id,id FOR UPDATE`,
			string(job.Mode), existingID)
		if err != nil {
			return errors.New("webhook_deposit_attempt_lock_failed")
		}
		for rows.Next() {
			var attempt depositAttemptRef
			if rows.Scan(&attempt.ID, &attempt.BridgeID, &attempt.CustomerID, &attempt.State) != nil {
				rows.Close()
				return errors.New("webhook_deposit_attempt_lock_failed")
			}
			affectedAttempts = append(affectedAttempts, attempt)
		}
		if rows.Err() != nil {
			rows.Close()
			return errors.New("webhook_deposit_attempt_lock_failed")
		}
		rows.Close()
	}
	mapping, _, err := LockVerifiedMapping(txCtx, tx, scope.CustomerID, scope.AccountRef, scope.CardID)
	if err != nil || mapping.RosterAccountID != scope.AccountRef || mapping.CardID != scope.CardID {
		return errors.New("webhook_deposit_mapping_changed")
	}
	normalized, err := json.Marshal(map[string]any{"source": "PROVIDER_DEPOSIT_GET",
		"webhook_event_id": job.EventID,
		"account_ref":      scope.AccountRef, "customer_id": scope.CustomerID,
		"deposit_id": observation.DepositID, "currency": observation.Currency,
		"gross_cents": observation.GrossCents, "net_cents": observation.NetCents,
		"provider_fee_cents": fee, "status": observation.Status, "kind": observation.Kind,
		"settled_at":                      observation.SettledAt.UTC(),
		"original_response_body_encoding": "base64",
		"original_response_body_base64":   base64.StdEncoding.EncodeToString(observation.RawEvidence)})
	if err != nil {
		return errors.New("webhook_deposit_evidence_encode_failed")
	}
	digest := sha256.Sum256(observation.RawEvidence)
	var observationID string
	err = tx.QueryRow(txCtx, `INSERT INTO servicing.provider_observations(mode,observation_kind,
		provider_object_id,source_account_ref,status,amount_cents,currency,provider_fee_cents,
		effective_at,observed_at,evidence_hash,normalized_evidence)
		VALUES($1,'DEPOSIT',$2,$3,$4,$5,$6,$7,$8,clock_timestamp(),$9,$10::jsonb)
		RETURNING id::text`, string(job.Mode), observation.DepositID, scope.AccountRef,
		observation.Status, observation.GrossCents, observation.Currency, fee,
		observation.SettledAt.UTC(), hex.EncodeToString(digest[:]), string(normalized)).Scan(&observationID)
	if err != nil {
		return errors.New("webhook_deposit_observation_insert_failed")
	}
	if depositExists {
		matches := existingCustomer == scope.CustomerID && existingCurrency == observation.Currency &&
			existingGross == observation.GrossCents && existingNet == observation.NetCents &&
			existingFee == fee && existingStatus == "SETTLED" && existingKind == observation.Kind &&
			existingSettled.Equal(observation.SettledAt.UTC())
		if matches {
			if err := CompleteWebhookJobTx(txCtx, tx, job, "DONE", "WEBHOOK_DEPOSIT_DUPLICATE"); err != nil {
				return err
			}
			if err := tx.Commit(txCtx); err != nil {
				return errors.New("webhook_deposit_commit_failed")
			}
			return nil
		}
		if existingStatus == "SETTLED" {
			tag, err := tx.Exec(txCtx, `UPDATE servicing.deposits SET status='HELD'
				WHERE mode=$1 AND id=$2 AND status='SETTLED'`, string(job.Mode), existingID)
			if err != nil || tag.RowsAffected() != 1 {
				return errors.New("webhook_deposit_invalidate_failed")
			}
		}
		if err := holdConflictingDeposit(txCtx, tx, job, scope, existingID, observationID, affectedAttempts, bridgeIDs); err != nil {
			return err
		}
		if err := CompleteWebhookJobTx(txCtx, tx, job, "HELD", "WEBHOOK_DEPOSIT_CONFLICT"); err != nil {
			return err
		}
		if err := tx.Commit(txCtx); err != nil {
			return errors.New("webhook_deposit_commit_failed")
		}
		return nil
	}
	if _, err := tx.Exec(txCtx, `INSERT INTO servicing.deposits(mode,customer_id,account_ref,provider_deposit_id,
		currency,gross_cents,net_cents,provider_fee_cents,status,kind,settled_at,source_observation_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'SETTLED','EXTERNAL_SETTLED',$9,$10)`,
		string(job.Mode), scope.CustomerID, scope.AccountRef, observation.DepositID,
		observation.Currency, observation.GrossCents, observation.NetCents, fee,
		observation.SettledAt.UTC(), observationID); err != nil {
		return errors.New("webhook_deposit_insert_failed")
	}
	if err := CompleteWebhookJobTx(txCtx, tx, job, "DONE", ""); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("webhook_deposit_commit_failed")
	}
	return nil
}

type depositAttemptRef struct {
	ID, BridgeID, CustomerID, State string
}

func holdConflictingDeposit(ctx context.Context, tx pgx.Tx, job WebhookJobClaim,
	scope TrustedDepositScope, depositID, observationID string, attempts []depositAttemptRef,
	lockedBridgeIDs []string) error {
	var customerActiveHold bool
	if err := tx.QueryRow(ctx, `SELECT active_hold FROM servicing.customers WHERE mode=$1 AND id=$2`,
		string(job.Mode), scope.CustomerID).Scan(&customerActiveHold); err != nil {
		return errors.New("webhook_deposit_customer_hold_read_failed")
	}
	if !customerActiveHold {
		tag, err := tx.Exec(ctx, `UPDATE servicing.customers SET active_hold=true,version=version+1,updated_at=clock_timestamp()
			WHERE mode=$1 AND id=$2 AND active_hold=false`, string(job.Mode), scope.CustomerID)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.New("webhook_deposit_customer_hold_failed")
		}
	}
	bridgeSet := make(map[string]bool)
	for _, bridgeID := range lockedBridgeIDs {
		bridgeSet[bridgeID] = true
	}
	affected := make(map[string]bool)
	for _, attempt := range attempts {
		if attempt.CustomerID != scope.CustomerID || !bridgeSet[attempt.BridgeID] {
			return errors.New("webhook_deposit_attempt_scope_invalid")
		}
		affected[attempt.BridgeID] = true
		if attempt.State == "SETTLED" || attempt.State == "FAILED_CONFIRMED" || attempt.State == "HELD" {
			continue
		}
		if attempt.State != "PLANNED" && attempt.State != "QUEUED" && attempt.State != "PENDING" && attempt.State != "UNKNOWN" {
			return errors.New("webhook_deposit_attempt_state_invalid")
		}
		tag, err := tx.Exec(ctx, `UPDATE servicing.collection_attempts SET state='HELD'
			WHERE mode=$1 AND id=$2 AND state=$3`, string(job.Mode), attempt.ID, attempt.State)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.New("webhook_deposit_attempt_hold_failed")
		}
	}
	for _, bridgeID := range lockedBridgeIDs {
		var lifecycle string
		if err := tx.QueryRow(ctx, `SELECT lifecycle FROM servicing.bridges WHERE mode=$1 AND id=$2`,
			string(job.Mode), bridgeID).Scan(&lifecycle); err != nil {
			return errors.New("webhook_deposit_bridge_hold_read_failed")
		}
		if lifecycle == "ACTIVE" || lifecycle == "COLLECTING" || lifecycle == "REOPENED_HELD" {
			affected[bridgeID] = true
		}
	}
	bridgeIDs := make([]string, 0, len(affected))
	for bridgeID := range affected {
		bridgeIDs = append(bridgeIDs, bridgeID)
	}
	sort.Strings(bridgeIDs)
	for _, bridgeID := range bridgeIDs {
		var version int64
		var customerID, caseID, evaluationID, offerID, termsHash string
		var holdReasons []string
		if err := tx.QueryRow(ctx, `SELECT version,customer_id,COALESCE(originating_case_id,''),
			evaluation_id,offer_id::text,terms_hash,hold_reasons FROM servicing.bridges
			WHERE mode=$1 AND id=$2`, string(job.Mode), bridgeID).Scan(&version, &customerID,
			&caseID, &evaluationID, &offerID, &termsHash, &holdReasons); err != nil {
			return errors.New("webhook_deposit_bridge_hold_read_failed")
		}
		newHolds := appendUniqueString(holdReasons, "DEPOSIT_SOURCE_CONFLICT")
		if len(newHolds) == len(holdReasons) {
			continue
		}
		var newVersion int64
		if err := tx.QueryRow(ctx, `UPDATE servicing.bridges SET hold_reasons=$3,version=version+1,
			updated_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND version=$4 RETURNING version`,
			string(job.Mode), bridgeID, newHolds, version).Scan(&newVersion); err != nil {
			return errors.New("webhook_deposit_bridge_hold_update_failed")
		}
		var caseRef any
		if caseID != "" {
			caseRef = caseID
		}
		summary, _ := json.Marshal(map[string]any{"customer_id": customerID,
			"account_ref": "customer:" + customerID, "originating_case_id": caseRef,
			"evaluation_id": evaluationID, "offer_id": offerID, "bridge_id": bridgeID,
			"bridge_version": newVersion, "deposit_id": depositID,
			"provider_observation_id": observationID, "webhook_event_id": job.EventID,
			"hold_code": "DEPOSIT_SOURCE_CONFLICT", "status": "HELD", "terms_hash": termsHash})
		parentID := bridgeID
		if _, err := AppendChange(ctx, tx, domain.Change{Mode: job.Mode, ResourceType: "BRIDGE",
			ResourceID: bridgeID, ResourceVersion: newVersion, ChangeType: "DEPOSIT_SOURCE_CONFLICT",
			Summary: summary}, &parentID, &newVersion); err != nil {
			return err
		}
	}
	return nil
}
