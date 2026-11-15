package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

func (r *Repository) EnsureBinding(ctx context.Context, id, mode, fingerprint, endpoint string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO coordinator.source_bindings(id,mode,fingerprint,endpoint_identity) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, id, mode, fingerprint, endpoint)
	if err != nil {
		return errors.New("source_binding_write_failed")
	}
	var matches bool
	if err := r.pool.QueryRow(ctx, `SELECT mode=$2 AND fingerprint=$3 AND endpoint_identity=$4 FROM coordinator.source_bindings WHERE id=$1`, id, mode, fingerprint, endpoint).Scan(&matches); err != nil || !matches {
		return domain.ErrConflict
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO coordinator.discovery_progress(binding_id) VALUES($1) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return errors.New("discovery_progress_initialize_failed")
	}
	return nil
}

// StartRun creates or recovers the same case revision under a valid case lease.
func (r *Repository) StartRun(ctx context.Context, caseID, token, runID string, revision int, stage, promptVersion, modelVersion string, contextRefs, privateContextRefs []byte) (string, error) {
	if runID == "" || revision < 1 || stage == "" || promptVersion == "" || modelVersion == "" || len(contextRefs) == 0 || len(privateContextRefs) == 0 {
		return "", errors.New("run_identity_invalid")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", errors.New("run_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return "", err
	}
	var id string
	var storedStage, storedPrompt, storedModel string
	var sameContext, samePrivateContext bool
	err = tx.QueryRow(ctx, `INSERT INTO coordinator.runs(id,case_id,revision,stage,prompt_version,model_version,context_refs,private_context_refs) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(case_id,revision) DO NOTHING RETURNING id`, runID, caseID, revision, stage, promptVersion, modelVersion, contextRefs, privateContextRefs).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `SELECT id,stage,prompt_version,model_version,context_refs=$3::jsonb,COALESCE(private_context_refs=$4::jsonb,false) FROM coordinator.runs WHERE case_id=$1 AND revision=$2 FOR UPDATE`, caseID, revision, contextRefs, privateContextRefs).Scan(&id, &storedStage, &storedPrompt, &storedModel, &sameContext, &samePrivateContext); err != nil {
			return "", errors.New("run_recovery_read_failed")
		}
		if id != runID || storedStage != stage || storedPrompt != promptVersion || storedModel != modelVersion || !sameContext || !samePrivateContext {
			return "", domain.ErrConflict
		}
	} else if err != nil {
		return "", errors.New("run_insert_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", errors.New("run_commit_failed")
	}
	return id, nil
}

type Attempt struct {
	RunID                                string
	Sequence                             int
	Kind                                 string
	AttemptID                            string
	CallID                               *string
	RequestHash, RequestBody, ResultBody []byte
	Outcome                              string
	CreatedAt                            time.Time
	CompletedAt                          *time.Time
}

// StartAttempt reserves a call and spends its durable budget before dispatch.
func (r *Repository) StartAttempt(ctx context.Context, caseID, token, runID, kind, attemptID, callID string, request []byte) (int, error) {
	if attemptID == "" || (kind == "TOOL" && callID == "") {
		return 0, errors.New("attempt_id_required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, errors.New("attempt_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return 0, err
	}
	var runCase string
	var modelCalls, toolCalls int
	var disposition *string
	if err := tx.QueryRow(ctx, `SELECT case_id,model_call_count,tool_call_count,disposition FROM coordinator.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&runCase, &modelCalls, &toolCalls, &disposition); err != nil || runCase != caseID {
		return 0, domain.ErrLeaseLost
	}
	if kind != "MODEL" && kind != "TOOL" {
		return 0, errors.New("attempt_kind_invalid")
	}
	hash := sha256.Sum256(request)
	var seq int
	var storedKind string
	var storedHash []byte
	var storedCallID *string
	err = tx.QueryRow(ctx, `SELECT sequence,kind,request_hash,call_id FROM coordinator.steps WHERE run_id=$1 AND attempt_id=$2 FOR UPDATE`, runID, attemptID).Scan(&seq, &storedKind, &storedHash, &storedCallID)
	if err == nil {
		if storedKind != kind || !bytesEqual(storedHash, hash[:]) || !same(storedCallID, nullableString(callID)) {
			return 0, domain.ErrConflict
		}
		if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
			return 0, err
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, errors.New("attempt_replay_commit_failed")
		}
		return seq, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, errors.New("attempt_identity_read_failed")
	}
	if disposition != nil {
		return 0, domain.ErrConflict
	}
	if (kind == "MODEL" && modelCalls >= 12) || (kind == "TOOL" && toolCalls >= 12) {
		return 0, domain.ErrBudgetExhausted
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM coordinator.steps WHERE run_id=$1`, runID).Scan(&seq); err != nil {
		return 0, errors.New("step_sequence_read_failed")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO coordinator.steps(run_id,sequence,kind,call_id,attempt_id,request_hash,request_body,outcome) VALUES($1,$2,$3,$4,$5,$6,$7,'DISPATCHED')`, runID, seq, kind, nullableCallID(callID), attemptID, hash[:], request); err != nil {
		return 0, errors.New("attempt_insert_failed")
	}
	column := "tool_call_count"
	if kind == "MODEL" {
		column = "model_call_count"
	} else if kind != "TOOL" {
		return 0, errors.New("attempt_kind_invalid")
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.runs SET `+column+`=`+column+`+1,updated_at=clock_timestamp() WHERE id=$1`, runID); err != nil {
		return 0, errors.New("attempt_budget_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, errors.New("attempt_commit_failed")
	}
	return seq, nil
}

// CompleteAttempt stores one matching tool result without spending another call.
func (r *Repository) CompleteAttempt(ctx context.Context, caseID, token, runID string, sequence int, result []byte, outcome string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("attempt_completion_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var runCase string
	if err := tx.QueryRow(ctx, `SELECT case_id FROM coordinator.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&runCase); err != nil || runCase != caseID {
		return domain.ErrLeaseLost
	}
	var prior []byte
	var priorOutcome string
	err = tx.QueryRow(ctx, `SELECT result_body,outcome FROM coordinator.steps WHERE run_id=$1 AND sequence=$2 FOR UPDATE`, runID, sequence).Scan(&prior, &priorOutcome)
	if err != nil {
		return errors.New("attempt_not_found")
	}
	if priorOutcome == "COMPLETED" || priorOutcome == "FAILED" {
		if priorOutcome != outcome || !bytesEqual(prior, result) {
			return domain.ErrConflict
		}
		return nil
	}
	if outcome != "COMPLETED" && outcome != "FAILED" {
		return errors.New("attempt_outcome_invalid")
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.steps SET result_body=$3,outcome=$4,completed_at=clock_timestamp() WHERE run_id=$1 AND sequence=$2 AND outcome='DISPATCHED'`, runID, sequence, result, outcome); err != nil {
		return errors.New("attempt_completion_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("attempt_completion_commit_failed")
	}
	return nil
}

func (r *Repository) ListAttempts(ctx context.Context, caseID, token, runID string) ([]Attempt, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, errors.New("attempt_read_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return nil, err
	}
	var runCase string
	if err := tx.QueryRow(ctx, `SELECT case_id FROM coordinator.runs WHERE id=$1`, runID).Scan(&runCase); err != nil || runCase != caseID {
		return nil, domain.ErrLeaseLost
	}
	rows, err := tx.Query(ctx, `SELECT run_id::text,sequence,kind,attempt_id,call_id,request_hash,request_body,result_body,outcome,created_at,completed_at FROM coordinator.steps WHERE run_id=$1 ORDER BY sequence`, runID)
	if err != nil {
		return nil, errors.New("attempt_history_read_failed")
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.RunID, &a.Sequence, &a.Kind, &a.AttemptID, &a.CallID, &a.RequestHash, &a.RequestBody, &a.ResultBody, &a.Outcome, &a.CreatedAt, &a.CompletedAt); err != nil {
			return nil, errors.New("attempt_history_decode_failed")
		}
		out = append(out, a)
	}
	if rows.Err() != nil {
		return nil, errors.New("attempt_history_read_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.New("attempt_history_commit_failed")
	}
	return out, nil
}

// VerifyRunContextRefs checks that a resumed run still uses its original packet.
func (r *Repository) VerifyRunContextRefs(ctx context.Context, caseID, token, runID string, refs []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("run_context_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var matches bool
	if err := tx.QueryRow(ctx, `SELECT case_id=$1 AND context_refs=$3::jsonb FROM coordinator.runs WHERE id=$2`, caseID, runID, refs).Scan(&matches); err != nil || !matches {
		return domain.ErrConflict
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("run_context_commit_failed")
	}
	return nil
}

// ReadRunContextRefs returns the immutable packet pinned to a claimed run.
func (r *Repository) ReadRunContextRefs(ctx context.Context, caseID, token, runID string) ([]byte, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, errors.New("run_context_read_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return nil, err
	}
	var runCase string
	var refs []byte
	if err := tx.QueryRow(ctx, `SELECT case_id::text,context_refs FROM coordinator.runs WHERE id=$1`, runID).Scan(&runCase, &refs); err != nil || runCase != caseID {
		return nil, domain.ErrNotFound
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.New("run_context_read_commit_failed")
	}
	return refs, nil
}

// ReadRunPrivateContextRefs returns the private scope envelope that is never sent to the model or reader API.
func (r *Repository) ReadRunPrivateContextRefs(ctx context.Context, caseID, token, runID string) ([]byte, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, errors.New("run_private_context_read_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return nil, err
	}
	var runCase string
	var refs []byte
	if err := tx.QueryRow(ctx, `SELECT case_id::text,private_context_refs FROM coordinator.runs WHERE id=$1`, runID).Scan(&runCase, &refs); err != nil || runCase != caseID || len(refs) == 0 {
		return nil, domain.ErrNotFound
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.New("run_private_context_read_commit_failed")
	}
	return refs, nil
}

func nullableString(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func nullableCallID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func lockLease(ctx context.Context, tx pgx.Tx, caseID, token string) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM coordinator.cases WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp() FOR UPDATE`, caseID, token).Scan(&id)
	if err != nil {
		return domain.ErrLeaseLost
	}
	return nil
}

func finalLeaseFence(ctx context.Context, tx pgx.Tx, caseID, token string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM coordinator.cases WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp())`, caseID, token).Scan(&valid); err != nil || !valid {
		return domain.ErrLeaseLost
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}

type RefreshDisposition struct {
	Status                      string
	NextStage                   string
	RetryAt                     *time.Time
	HeldReason                  *string
	InvestigationTriggerCode    *string
	InvestigationTriggerVersion *int64
}

// CompleteRefreshDisposition stores versioned source truth, clears only its matching dirty target, and disposes the lease atomically.
func (r *Repository) CompleteRefreshDisposition(ctx context.Context, caseID, token string, version int64, resourceID string, observedAt time.Time, state []byte, d RefreshDisposition) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("collection_refresh_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var bridgeID string
	var target, storedVersion *int64
	if err := tx.QueryRow(ctx, `SELECT bridge_id,target_bridge_version,bridge_version FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION' FOR UPDATE`, caseID).Scan(&bridgeID, &target, &storedVersion); err != nil {
		return errors.New("collection_case_not_found")
	}
	if bridgeID != resourceID || target != nil && version < *target || storedVersion != nil && version < *storedVersion {
		return domain.ErrConflict
	}
	var stored []byte
	err = tx.QueryRow(ctx, `INSERT INTO coordinator.collection_observations(case_id,bridge_version,resource_type,resource_id,resource_version,observed_at,state) VALUES($1,$2,'BRIDGE',$3,$2,$4,$5) ON CONFLICT DO NOTHING RETURNING state`, caseID, version, resourceID, observedAt, state).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		var equal bool
		if err := tx.QueryRow(ctx, `SELECT state=$4::jsonb FROM coordinator.collection_observations WHERE case_id=$1 AND bridge_version=$2 AND resource_type='BRIDGE' AND resource_id=$3 AND resource_version=$2 FOR UPDATE`, caseID, version, resourceID, state).Scan(&equal); err != nil || !equal {
			return domain.ErrConflict
		}
	} else if err != nil {
		return errors.New("collection_observation_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE coordinator.cases SET bridge_version=$3,refresh_needed=false,target_bridge_version=NULL,status=$4,next_stage=$5,retry_at=$6,held_reason=$7,transient_failures=0,pending_investigation_trigger=$8,pending_trigger_bridge_version=$9,lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,updated_at=clock_timestamp() WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp() AND (target_bridge_version IS NULL OR target_bridge_version<=$3) AND (bridge_version IS NULL OR bridge_version<=$3)`, caseID, token, version, d.Status, d.NextStage, d.RetryAt, d.HeldReason, d.InvestigationTriggerCode, d.InvestigationTriggerVersion)
	if err != nil {
		return errors.New("collection_disposition_write_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("collection_disposition_commit_failed")
	}
	return nil
}

func (r *Repository) ReadRunDisposition(ctx context.Context, caseID, token, runID string) ([]byte, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, errors.New("run_disposition_read_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return nil, false, err
	}
	var runCase string
	var disposition *string
	if err := tx.QueryRow(ctx, `SELECT case_id::text,disposition FROM coordinator.runs WHERE id=$1`, runID).Scan(&runCase, &disposition); err != nil || runCase != caseID {
		return nil, false, domain.ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, errors.New("run_disposition_read_failed")
	}
	if disposition == nil {
		return nil, false, nil
	}
	return []byte(*disposition), true, nil
}
