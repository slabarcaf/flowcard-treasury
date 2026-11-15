package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

// CompleteShortfallRun atomically links the saved assessment and publication to the case.
func (r *Repository) CompleteShortfallRun(ctx context.Context, caseID, token, runID, assessmentRowID, assessmentID string, publication []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("shortfall_publication_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var caseKind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM coordinator.cases WHERE id=$1 FOR UPDATE`, caseID).Scan(&caseKind); err != nil || caseKind != "SHORTFALL" {
		return domain.ErrConflict
	}
	var runCase string
	var runRevision int
	var agentOutcome *string
	var previousPublication []byte
	if err := tx.QueryRow(ctx, `SELECT case_id::text,revision,disposition,publication FROM coordinator.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&runCase, &runRevision, &agentOutcome, &previousPublication); err != nil || runCase != caseID {
		return domain.ErrLeaseLost
	}
	if err := validateAgentPublication(agentOutcome, publication); err != nil {
		return err
	}
	var assessmentCase string
	var assessmentRevision int
	var savedID *string
	var savedResponse []byte
	if err := tx.QueryRow(ctx, `SELECT case_id::text,revision,saved_assessment_id,response_body FROM coordinator.assessments WHERE id=$1 FOR UPDATE`, assessmentRowID).Scan(&assessmentCase, &assessmentRevision, &savedID, &savedResponse); err != nil || assessmentCase != caseID {
		return domain.ErrConflict
	}
	if assessmentRevision != runRevision || savedID == nil || *savedID != assessmentID || len(savedResponse) == 0 {
		return domain.ErrConflict
	}
	if len(previousPublication) > 0 {
		var samePublication bool
		if err := tx.QueryRow(ctx, `SELECT publication=$2::jsonb FROM coordinator.runs WHERE id=$1`, runID, publication).Scan(&samePublication); err != nil {
			return errors.New("shortfall_publication_replay_read_failed")
		}
		if !samePublication {
			return domain.ErrConflict
		}
	} else if _, err := tx.Exec(ctx, `UPDATE coordinator.runs SET publication=$2::jsonb,stage='FINISHED',updated_at=clock_timestamp() WHERE id=$1 AND publication IS NULL`, runID, publication); err != nil {
		return errors.New("shortfall_publication_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE coordinator.cases SET status='COMPLETED',next_stage='COMPLETED',retry_at=NULL,held_reason=NULL,transient_failures=0,lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,updated_at=clock_timestamp() WHERE id=$1 AND kind='SHORTFALL' AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp()`, caseID, token)
	if err != nil {
		return errors.New("shortfall_case_completion_write_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("shortfall_publication_commit_failed")
	}
	return nil
}

// CompleteCollectionRun publishes an explanation only while its source version/actions stay current.
func (r *Repository) CompleteCollectionRun(ctx context.Context, caseID, token, runID, bridgeID string, bridgeVersion int64, allowedActions []string, publication []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("collection_publication_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var storedBridgeID string
	var storedVersion *int64
	var refreshNeeded bool
	if err := tx.QueryRow(ctx, `SELECT bridge_id,bridge_version,refresh_needed FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION' FOR UPDATE`, caseID).Scan(&storedBridgeID, &storedVersion, &refreshNeeded); err != nil || storedBridgeID != bridgeID || storedVersion == nil || *storedVersion != bridgeVersion || refreshNeeded {
		return domain.ErrConflict
	}
	var storedActions []byte
	if err := tx.QueryRow(ctx, `SELECT state->'allowed_next_actions' FROM coordinator.collection_observations WHERE case_id=$1 AND bridge_version=$2 AND resource_type='BRIDGE' AND resource_id=$3`, caseID, bridgeVersion, bridgeID).Scan(&storedActions); err != nil {
		return errors.New("collection_actions_read_failed")
	}
	var decodedActions []string
	if json.Unmarshal(storedActions, &decodedActions) != nil || !sameStrings(decodedActions, allowedActions) {
		return domain.ErrConflict
	}
	var runCase string
	var agentOutcome *string
	var previousPublication []byte
	if err := tx.QueryRow(ctx, `SELECT case_id::text,disposition,publication FROM coordinator.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&runCase, &agentOutcome, &previousPublication); err != nil || runCase != caseID {
		return domain.ErrLeaseLost
	}
	if err := validateAgentPublication(agentOutcome, publication); err != nil {
		return err
	}
	if len(previousPublication) > 0 {
		var samePublication bool
		if err := tx.QueryRow(ctx, `SELECT publication=$2::jsonb FROM coordinator.runs WHERE id=$1`, runID, publication).Scan(&samePublication); err != nil {
			return errors.New("collection_publication_replay_read_failed")
		}
		if !samePublication {
			return domain.ErrConflict
		}
	} else if _, err := tx.Exec(ctx, `UPDATE coordinator.runs SET publication=$2::jsonb,stage='FINISHED',updated_at=clock_timestamp() WHERE id=$1 AND publication IS NULL`, runID, publication); err != nil {
		return errors.New("collection_publication_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE coordinator.cases SET next_stage='MONITORING',retry_at=NULL,transient_failures=0,pending_investigation_trigger=NULL,pending_trigger_bridge_version=NULL,lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,updated_at=clock_timestamp() WHERE id=$1 AND kind='COLLECTION' AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp() AND bridge_id=$3 AND bridge_version=$4 AND refresh_needed=false`, caseID, token, bridgeID, bridgeVersion)
	if err != nil {
		return errors.New("collection_case_publication_write_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("collection_publication_commit_failed")
	}
	return nil
}

// SupersedeCollectionRun preserves stale commentary and schedules deterministic source refresh.
func (r *Repository) SupersedeCollectionRun(ctx context.Context, caseID, token, runID, bridgeID string, observedVersion *int64, disposition []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("collection_supersede_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var storedBridgeID string
	var storedVersion, targetVersion *int64
	var status string
	if err := tx.QueryRow(ctx, `SELECT bridge_id,bridge_version,target_bridge_version,status FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION' FOR UPDATE`, caseID).Scan(&storedBridgeID, &storedVersion, &targetVersion, &status); err != nil || storedBridgeID != bridgeID || storedVersion == nil {
		return domain.ErrConflict
	}
	var runCase string
	var previousPublication []byte
	if err := tx.QueryRow(ctx, `SELECT case_id::text,publication FROM coordinator.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&runCase, &previousPublication); err != nil || runCase != caseID {
		return domain.ErrLeaseLost
	}
	if len(previousPublication) == 0 {
		if _, err := tx.Exec(ctx, `UPDATE coordinator.runs SET publication=$2::jsonb,stage='SUPERSEDED',superseded_by_bridge_version=$3,updated_at=clock_timestamp() WHERE id=$1 AND publication IS NULL`, runID, disposition, targetVersionValue(observedVersion, storedVersion)); err != nil {
			return errors.New("collection_run_supersede_write_failed")
		}
	} else if _, err := tx.Exec(ctx, `UPDATE coordinator.runs SET superseded_by_bridge_version=$2,updated_at=clock_timestamp() WHERE id=$1`, runID, targetVersionValue(observedVersion, storedVersion)); err != nil {
		return errors.New("collection_run_supersede_write_failed")
	}
	target := *storedVersion
	if targetVersion != nil && *targetVersion > target {
		target = *targetVersion
	}
	if observedVersion != nil && *observedVersion > target {
		target = *observedVersion
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE coordinator.cases SET status=CASE WHEN status='CLOSED' THEN 'MONITORING' ELSE status END,next_stage='REFRESH_BRIDGE',refresh_needed=true,target_bridge_version=$3,pending_investigation_trigger=NULL,pending_trigger_bridge_version=NULL,retry_at=NULL,lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,updated_at=clock_timestamp() WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp() AND bridge_id=$4`, caseID, token, target, bridgeID)
	if err != nil {
		return errors.New("collection_case_supersede_write_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("collection_supersede_commit_failed")
	}
	return nil
}

func targetVersionValue(observed, stored *int64) int64 {
	target := int64(0)
	if stored != nil {
		target = *stored
	}
	if observed != nil && *observed > target {
		target = *observed
	}
	return target
}

// The service's action array is an exact set in the coordinator packet, while JSON preserves order.
func sameStrings(left, right []string) bool {
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validateAgentPublication(agentOutcome *string, publication []byte) error {
	var stored struct {
		Kind   string          `json:"kind"`
		Finish json.RawMessage `json:"finish"`
	}
	var published struct {
		ExplanationMode string          `json:"explanation_mode"`
		Finish          json.RawMessage `json:"finish"`
	}
	if json.Unmarshal(publication, &published) != nil {
		return errors.New("case_publication_invalid")
	}
	if published.ExplanationMode != "AI_COMMENTARY" {
		return nil
	}
	if agentOutcome == nil || json.Unmarshal([]byte(*agentOutcome), &stored) != nil || stored.Kind != "FINISH" || !equalJSON(stored.Finish, published.Finish) {
		return domain.ErrConflict
	}
	return nil
}

func equalJSON(left, right []byte) bool {
	decode := func(raw []byte) (any, bool) {
		var value any
		if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		return value, true
	}
	leftValue, leftOK := decode(left)
	rightValue, rightOK := decode(right)
	return leftOK && rightOK && reflect.DeepEqual(leftValue, rightValue)
}
