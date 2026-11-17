package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

const maxCollectionRuns = 3
const collectionRunInterval = 5 * time.Minute

type CollectionAdmissionPlan struct {
	Count    int
	RunID    string
	Revision int
	RetryAt  *time.Time
	Skipped  bool
}

// CountCollectionRuns returns admitted agent runs for one case/bridge under its lease.
func (r *Repository) CountCollectionRuns(ctx context.Context, caseID, token, bridgeID string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, errors.New("collection_run_count_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return 0, err
	}
	var storedBridgeID string
	if err := tx.QueryRow(ctx, `SELECT bridge_id FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION'`, caseID).Scan(&storedBridgeID); err != nil || storedBridgeID != bridgeID {
		return 0, domain.ErrConflict
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM coordinator.investigation_triggers WHERE case_id=$1 AND bridge_id=$2 AND run_id IS NOT NULL`, caseID, bridgeID).Scan(&count); err != nil {
		return 0, errors.New("collection_run_count_read_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, errors.New("collection_run_count_commit_failed")
	}
	return count, nil
}

// LatestCollectionAdmission returns the newest admitted run for deterministic supersession.
func (r *Repository) LatestCollectionAdmission(ctx context.Context, caseID, token string) (string, string, int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", 0, errors.New("collection_latest_run_read_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return "", "", 0, err
	}
	var bridgeID, runID string
	var version int64
	err = tx.QueryRow(ctx, `SELECT a.bridge_id,a.run_id::text,a.bridge_version FROM coordinator.investigation_triggers a WHERE a.case_id=$1 AND a.run_id IS NOT NULL ORDER BY a.admitted_at DESC LIMIT 1`, caseID).Scan(&bridgeID, &runID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, domain.ErrNotFound
	}
	if err != nil {
		return "", "", 0, errors.New("collection_latest_run_read_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return "", "", 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", 0, errors.New("collection_latest_run_commit_failed")
	}
	return bridgeID, runID, version, nil
}

// CollectionAdmission reads the pinned case budget and stable trigger identity.
func (r *Repository) CollectionAdmission(ctx context.Context, caseID, token, bridgeID, triggerCode string, version int64) (CollectionAdmissionPlan, error) {
	var plan CollectionAdmissionPlan
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return plan, errors.New("collection_admission_read_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return plan, err
	}
	var bindingID string
	var storedBridgeID string
	var storedVersion *int64
	var dirty bool
	if err := tx.QueryRow(ctx, `SELECT binding_id,bridge_id,bridge_version,refresh_needed FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION'`, caseID).Scan(&bindingID, &storedBridgeID, &storedVersion, &dirty); err != nil || storedBridgeID != bridgeID || storedVersion == nil || *storedVersion != version || dirty {
		return plan, domain.ErrConflict
	}
	var lastAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT count(*),max(admitted_at) FROM coordinator.investigation_triggers WHERE case_id=$1 AND bridge_id=$2 AND run_id IS NOT NULL`, caseID, bridgeID).Scan(&plan.Count, &lastAt); err != nil {
		return plan, errors.New("collection_admission_count_failed")
	}
	var runID *string
	var revision *int
	err = tx.QueryRow(ctx, `SELECT run_id::text,revision FROM coordinator.investigation_triggers WHERE binding_id=$1 AND bridge_id=$2 AND bridge_version=$3 AND trigger_code=$4`, bindingID, bridgeID, version, triggerCode).Scan(&runID, &revision)
	if err == nil {
		if runID == nil || revision == nil {
			plan.Skipped = true
		} else {
			plan.RunID = *runID
			plan.Revision = *revision
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return plan, errors.New("collection_trigger_read_failed")
	}
	if plan.RunID == "" && !plan.Skipped {
		if plan.Count >= maxCollectionRuns {
			plan.Skipped = true
		} else {
			plan.Revision = plan.Count + 1
			if lastAt != nil {
				retryAt := lastAt.Add(collectionRunInterval)
				var now time.Time
				if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
					return plan, errors.New("collection_admission_clock_failed")
				}
				if now.Before(retryAt) {
					plan.RetryAt = &retryAt
				}
			}
		}
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return CollectionAdmissionPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CollectionAdmissionPlan{}, errors.New("collection_admission_read_commit_failed")
	}
	return plan, nil
}

// StartCollectionRun atomically admits one new source trigger and its immutable run.
func (r *Repository) StartCollectionRun(ctx context.Context, caseID, token, bridgeID, triggerCode, runID string, version int64, revision int, stage, promptVersion, modelVersion string, contextRefs, privateContextRefs []byte) (string, bool, *time.Time, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, nil, errors.New("collection_run_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return "", false, nil, err
	}
	var bindingID, storedBridgeID string
	var storedVersion, targetVersion *int64
	var dirty bool
	if err := tx.QueryRow(ctx, `SELECT binding_id,bridge_id,bridge_version,target_bridge_version,refresh_needed FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION' FOR UPDATE`, caseID).Scan(&bindingID, &storedBridgeID, &storedVersion, &targetVersion, &dirty); err != nil || storedBridgeID != bridgeID || storedVersion == nil || *storedVersion != version || dirty || targetVersion != nil {
		return "", false, nil, domain.ErrConflict
	}
	var existingID *string
	var existingRevision *int
	err = tx.QueryRow(ctx, `SELECT run_id::text,revision FROM coordinator.investigation_triggers WHERE binding_id=$1 AND bridge_id=$2 AND bridge_version=$3 AND trigger_code=$4 FOR UPDATE`, bindingID, bridgeID, version, triggerCode).Scan(&existingID, &existingRevision)
	if err == nil {
		if existingID == nil || existingRevision == nil || *existingID != runID || *existingRevision != revision {
			return "", false, nil, domain.ErrConflict
		}
		if err := ensureRunTx(ctx, tx, caseID, runID, revision, stage, promptVersion, modelVersion, contextRefs, privateContextRefs); err != nil {
			return "", false, nil, err
		}
		if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
			return "", false, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", false, nil, errors.New("collection_run_replay_commit_failed")
		}
		return runID, false, nil, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil, errors.New("collection_trigger_read_failed")
	}
	var count int
	var lastAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT count(*),max(admitted_at) FROM coordinator.investigation_triggers WHERE case_id=$1 AND bridge_id=$2 AND run_id IS NOT NULL`, caseID, bridgeID).Scan(&count, &lastAt); err != nil {
		return "", false, nil, errors.New("collection_admission_count_failed")
	}
	if count >= maxCollectionRuns {
		return "", false, nil, nil
	}
	if revision != count+1 || runID == "" || stage == "" || promptVersion == "" || modelVersion == "" || len(contextRefs) == 0 || len(privateContextRefs) == 0 {
		return "", false, nil, domain.ErrConflict
	}
	if lastAt != nil {
		retryAt := lastAt.Add(collectionRunInterval)
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return "", false, nil, errors.New("collection_admission_clock_failed")
		}
		if now.Before(retryAt) {
			return "", false, &retryAt, nil
		}
	}
	if err := ensureRunTx(ctx, tx, caseID, runID, revision, stage, promptVersion, modelVersion, contextRefs, privateContextRefs); err != nil {
		return "", false, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO coordinator.investigation_triggers(binding_id,bridge_id,bridge_version,trigger_code,case_id,run_id,revision,admitted_at) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp())`, bindingID, bridgeID, version, triggerCode, caseID, runID, revision); err != nil {
		return "", false, nil, errors.New("collection_trigger_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return "", false, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, nil, errors.New("collection_run_commit_failed")
	}
	return runID, true, nil, nil
}

func ensureRunTx(ctx context.Context, tx pgx.Tx, caseID, runID string, revision int, stage, promptVersion, modelVersion string, contextRefs, privateContextRefs []byte) error {
	var id string
	var storedStage, storedPrompt, storedModel string
	var sameContext, samePrivateContext bool
	err := tx.QueryRow(ctx, `INSERT INTO coordinator.runs(id,case_id,revision,stage,prompt_version,model_version,context_refs,private_context_refs) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(case_id,revision) DO NOTHING RETURNING id`, runID, caseID, revision, stage, promptVersion, modelVersion, contextRefs, privateContextRefs).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `SELECT id::text,stage,prompt_version,model_version,context_refs=$3::jsonb,COALESCE(private_context_refs=$4::jsonb,false) FROM coordinator.runs WHERE case_id=$1 AND revision=$2 FOR UPDATE`, caseID, revision, contextRefs, privateContextRefs).Scan(&id, &storedStage, &storedPrompt, &storedModel, &sameContext, &samePrivateContext); err != nil {
			return errors.New("run_recovery_read_failed")
		}
		if id != runID || storedStage != stage || storedPrompt != promptVersion || storedModel != modelVersion || !sameContext || !samePrivateContext {
			return domain.ErrConflict
		}
		return nil
	}
	if err != nil {
		return errors.New("run_insert_failed")
	}
	if id != runID {
		return domain.ErrConflict
	}
	return nil
}
