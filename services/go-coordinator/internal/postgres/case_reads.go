package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

type CaseListItem struct {
	ID                  string
	Kind                string
	Status              string
	NextStage           string
	CustomerID          *string
	AccountRef          *string
	DetectionID         *string
	BridgeID            *string
	BridgeVersion       *int64
	RefreshNeeded       bool
	TargetBridgeVersion *int64
	RetryAt             *time.Time
	HeldReason          *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	AssessmentID        *string
	AssessmentStatus    *string
	ApprovalRequired    *bool
}

type CaseView struct {
	Case               domain.Case
	Mode               string
	ServicingStale     bool
	OriginatingCaseID  *string
	LastChangeSequence *int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type RunView struct {
	ID                        string
	CaseID                    string
	Revision                  int
	Stage                     string
	PromptVersion             string
	ModelVersion              string
	ContextRefs               []byte
	Disposition               []byte
	Publication               []byte
	SupersededByBridgeVersion *int64
	StartedAt                 time.Time
	UpdatedAt                 time.Time
}

type CollectionSnapshot struct {
	BridgeVersion int64
	ObservedAt    time.Time
	State         []byte
}

// ListCases returns at most limit rows within the configured source binding.
func (r *Repository) ListCases(ctx context.Context, bindingID, mode string, customerID *string, before *time.Time, beforeID string, limit int) ([]CaseListItem, error) {
	if limit < 1 || limit > 26 {
		return nil, errors.New("case_page_limit_invalid")
	}
	rows, err := r.pool.Query(ctx, `SELECT c.id::text,c.kind,c.status,c.next_stage,c.customer_id,c.account_ref,c.detection_id,c.bridge_id,c.bridge_version,c.refresh_needed,c.target_bridge_version,c.retry_at,c.held_reason,c.created_at,c.updated_at,a.saved_assessment_id,a.response_body FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id LEFT JOIN LATERAL (SELECT saved_assessment_id,response_body FROM coordinator.assessments WHERE case_id=c.id ORDER BY revision DESC LIMIT 1) a ON true WHERE c.binding_id=$1 AND b.mode=$2 AND ($3::text IS NULL OR c.customer_id=$3) AND ($4::timestamptz IS NULL OR (c.created_at,c.id)<($4,$5::uuid)) ORDER BY c.created_at DESC,c.id DESC LIMIT $6`, bindingID, mode, customerID, before, nullableUUID(beforeID), limit)
	if err != nil {
		return nil, errors.New("case_list_read_failed")
	}
	defer rows.Close()
	var result []CaseListItem
	for rows.Next() {
		var item CaseListItem
		var assessmentBody []byte
		if err := rows.Scan(&item.ID, &item.Kind, &item.Status, &item.NextStage, &item.CustomerID, &item.AccountRef, &item.DetectionID, &item.BridgeID, &item.BridgeVersion, &item.RefreshNeeded, &item.TargetBridgeVersion, &item.RetryAt, &item.HeldReason, &item.CreatedAt, &item.UpdatedAt, &item.AssessmentID, &assessmentBody); err != nil {
			return nil, errors.New("case_list_decode_failed")
		}
		if len(assessmentBody) != 0 {
			var assessment struct {
				Status           string `json:"status"`
				ApprovalRequired bool   `json:"approval_required"`
			}
			if json.Unmarshal(assessmentBody, &assessment) != nil {
				return nil, errors.New("case_assessment_decode_failed")
			}
			item.AssessmentStatus = &assessment.Status
			item.ApprovalRequired = &assessment.ApprovalRequired
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("case_list_read_failed")
	}
	return result, nil
}

// ReadCaseScoped returns one case only when it belongs to the configured binding and mode.
func (r *Repository) ReadCaseScoped(ctx context.Context, caseID, bindingID, mode string) (CaseView, error) {
	var view CaseView
	c := &view.Case
	err := r.pool.QueryRow(ctx, `SELECT c.id::text,c.binding_id,c.kind,c.detection_id,c.bridge_id,c.customer_id,c.account_ref,c.raw_account_id,c.status,c.next_stage,c.retry_at,c.lease_token::text,c.lease_expires_at,c.lease_stage,c.bridge_version,c.refresh_needed,c.target_bridge_version,c.transient_failures,c.pending_investigation_trigger,c.pending_trigger_bridge_version,b.mode,c.originating_case_id::text,c.last_change_sequence,(d.servicing_hold_reason IS NOT NULL OR d.servicing_last_page_at IS NULL OR d.servicing_last_page_at < clock_timestamp()-interval '60 seconds'),c.created_at,c.updated_at FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id JOIN coordinator.discovery_progress d ON d.binding_id=c.binding_id WHERE c.id=$1 AND c.binding_id=$2 AND b.mode=$3`, caseID, bindingID, mode).Scan(&c.ID, &c.BindingID, &c.Kind, &c.DetectionID, &c.BridgeID, &c.CustomerID, &c.AccountRef, &c.RawAccountID, &c.Status, &c.NextStage, &c.RetryAt, &c.LeaseToken, &c.LeaseExpiresAt, &c.LeaseStage, &c.BridgeVersion, &c.RefreshNeeded, &c.TargetVersion, &c.TransientFailures, &c.PendingTrigger, &c.PendingTriggerVersion, &view.Mode, &view.OriginatingCaseID, &view.LastChangeSequence, &view.ServicingStale, &view.CreatedAt, &view.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CaseView{}, domain.ErrNotFound
	}
	if err != nil {
		return CaseView{}, errors.New("case_detail_read_failed")
	}
	return view, nil
}

// CaseDetailFence reports whether collection state changed while a detail response was assembled.
func (r *Repository) CaseDetailFence(ctx context.Context, expected CaseView, bindingID, mode string) (bool, error) {
	c := expected.Case
	var status, nextStage string
	var bridgeID, pendingTrigger *string
	var bridgeVersion, targetVersion, pendingVersion, sequence *int64
	var refreshNeeded, servicingStale bool
	err := r.pool.QueryRow(ctx, `SELECT c.status,c.next_stage,c.bridge_id,c.bridge_version,c.refresh_needed,c.target_bridge_version,c.pending_investigation_trigger,c.pending_trigger_bridge_version,c.last_change_sequence,(d.servicing_hold_reason IS NOT NULL OR d.servicing_last_page_at IS NULL OR d.servicing_last_page_at < clock_timestamp()-interval '60 seconds') FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id JOIN coordinator.discovery_progress d ON d.binding_id=c.binding_id WHERE c.id=$1 AND c.binding_id=$2 AND b.mode=$3 AND c.kind='COLLECTION'`, c.ID, bindingID, mode).Scan(&status, &nextStage, &bridgeID, &bridgeVersion, &refreshNeeded, &targetVersion, &pendingTrigger, &pendingVersion, &sequence, &servicingStale)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, domain.ErrNotFound
	}
	if err != nil {
		return false, errors.New("case_detail_fence_read_failed")
	}
	return status == string(c.Status) && nextStage == c.NextStage && same(bridgeID, c.BridgeID) && sameInt64(bridgeVersion, c.BridgeVersion) && refreshNeeded == c.RefreshNeeded && sameInt64(targetVersion, c.TargetVersion) && same(pendingTrigger, c.PendingTrigger) && sameInt64(pendingVersion, c.PendingTriggerVersion) && sameInt64(sequence, expected.LastChangeSequence) && servicingStale == expected.ServicingStale, nil
}

func sameInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// ListRunsForCase returns bounded run metadata without protocol bodies.
func (r *Repository) ListRunsForCase(ctx context.Context, caseID string) ([]RunView, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,case_id::text,revision,stage,prompt_version,model_version,context_refs,disposition,publication,superseded_by_bridge_version,started_at,updated_at FROM coordinator.runs WHERE case_id=$1 ORDER BY revision`, caseID)
	if err != nil {
		return nil, errors.New("case_runs_read_failed")
	}
	defer rows.Close()
	var result []RunView
	for rows.Next() {
		var item RunView
		if err := rows.Scan(&item.ID, &item.CaseID, &item.Revision, &item.Stage, &item.PromptVersion, &item.ModelVersion, &item.ContextRefs, &item.Disposition, &item.Publication, &item.SupersededByBridgeVersion, &item.StartedAt, &item.UpdatedAt); err != nil {
			return nil, errors.New("case_runs_decode_failed")
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("case_runs_read_failed")
	}
	return result, nil
}

// ListAttemptsForRun returns durable rows for internal redaction into a safe trace.
func (r *Repository) ListAttemptsForRun(ctx context.Context, caseID, runID string) ([]Attempt, error) {
	var owner string
	if err := r.pool.QueryRow(ctx, `SELECT case_id::text FROM coordinator.runs WHERE id=$1`, runID).Scan(&owner); err != nil || owner != caseID {
		return nil, domain.ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT run_id::text,sequence,kind,attempt_id,call_id,request_hash,request_body,result_body,outcome,created_at,completed_at FROM coordinator.steps WHERE run_id=$1 ORDER BY sequence`, runID)
	if err != nil {
		return nil, errors.New("case_steps_read_failed")
	}
	defer rows.Close()
	var result []Attempt
	for rows.Next() {
		var item Attempt
		if err := rows.Scan(&item.RunID, &item.Sequence, &item.Kind, &item.AttemptID, &item.CallID, &item.RequestHash, &item.RequestBody, &item.ResultBody, &item.Outcome, &item.CreatedAt, &item.CompletedAt); err != nil {
			return nil, errors.New("case_steps_decode_failed")
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("case_steps_read_failed")
	}
	return result, nil
}

// ReadCollectionSnapshot returns the current scoped bridge observation for case display.
func (r *Repository) ReadCollectionSnapshot(ctx context.Context, caseID, bindingID, mode string) (CollectionSnapshot, error) {
	var snapshot CollectionSnapshot
	err := r.pool.QueryRow(ctx, `SELECT o.bridge_version,o.observed_at,o.state FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id JOIN coordinator.collection_observations o ON o.case_id=c.id AND o.resource_type='BRIDGE' AND o.resource_id=c.bridge_id AND o.bridge_version=c.bridge_version WHERE c.id=$1 AND c.binding_id=$2 AND b.mode=$3 AND c.kind='COLLECTION'`, caseID, bindingID, mode).Scan(&snapshot.BridgeVersion, &snapshot.ObservedAt, &snapshot.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return CollectionSnapshot{}, domain.ErrNotFound
	}
	if err != nil {
		return CollectionSnapshot{}, errors.New("collection_snapshot_read_failed")
	}
	return snapshot, nil
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
