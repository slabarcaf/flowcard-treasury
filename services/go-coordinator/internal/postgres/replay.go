package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

type ReplayReceipt struct {
	CaseID     string
	Status     string
	Stage      string
	Revision   int
	ActorID    string
	ReplayedAt time.Time
}

// ReplayCase resumes only a held case or a case whose lease expired, preserving revisions and budgets.
func (r *Repository) ReplayCase(ctx context.Context, caseID, bindingID, mode, actorID, reason string) (ReplayReceipt, error) {
	var receipt ReplayReceipt
	actorID = strings.TrimSpace(actorID)
	reason = strings.TrimSpace(reason)
	if actorID == "" || len(actorID) > 128 || reason == "" || len(reason) > 1000 || bindingID == "" || (mode != "DEMO" && mode != "SANDBOX") {
		return receipt, errors.New("case_replay_input_invalid")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return receipt, errors.New("case_replay_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var kind, priorStatus, nextStage string
	var leaseStage *string
	var leaseToken *string
	var leaseExpiresAt *time.Time
	var bridgeID *string
	var bridgeVersion, targetVersion *int64
	var pendingTrigger *string
	var refreshNeeded, leaseExpired bool
	err = tx.QueryRow(ctx, `SELECT c.kind,c.status,c.next_stage,c.lease_stage,c.lease_token::text,c.lease_expires_at,c.bridge_id,c.bridge_version,c.target_bridge_version,c.pending_investigation_trigger,c.refresh_needed,(c.lease_token IS NOT NULL AND c.lease_expires_at<=clock_timestamp()) FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id WHERE c.id=$1 AND c.binding_id=$2 AND b.mode=$3 FOR UPDATE OF c`, caseID, bindingID, mode).Scan(&kind, &priorStatus, &nextStage, &leaseStage, &leaseToken, &leaseExpiresAt, &bridgeID, &bridgeVersion, &targetVersion, &pendingTrigger, &refreshNeeded, &leaseExpired)
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, domain.ErrNotFound
	}
	if err != nil {
		return receipt, errors.New("case_replay_case_read_failed")
	}
	if priorStatus != "HELD" && !leaseExpired {
		return receipt, domain.ErrConflict
	}
	stage := nextStage
	if priorStatus != "HELD" && leaseStage != nil && *leaseStage != "" {
		stage = *leaseStage
	}
	priorStage := stage
	switch stage {
	case "LOAD_DETAIL", "EVALUATE", "INVESTIGATE":
		if kind != "SHORTFALL" && stage != "INVESTIGATE" {
			return receipt, domain.ErrConflict
		}
	case "REFRESH_BRIDGE", "MONITORING":
		if kind != "COLLECTION" {
			return receipt, domain.ErrConflict
		}
	default:
		return receipt, domain.ErrConflict
	}
	status := priorStatus
	if kind == "SHORTFALL" {
		switch stage {
		case "LOAD_DETAIL":
			status = "NEW"
		case "EVALUATE":
			status = "EVALUATING"
		case "INVESTIGATE":
			status = "INVESTIGATING"
		}
	} else {
		if stage == "MONITORING" {
			stage = "REFRESH_BRIDGE"
		}
		if stage == "INVESTIGATE" && pendingTrigger == nil {
			stage = "REFRESH_BRIDGE"
		}
		if stage == "REFRESH_BRIDGE" {
			status = "ACTION_REQUIRED"
			refreshNeeded = true
			if targetVersion == nil && bridgeVersion != nil {
				value := *bridgeVersion
				targetVersion = &value
			}
		}
		if stage == "INVESTIGATE" && priorStatus == "HELD" {
			status = "ACTION_REQUIRED"
		}
	}
	var revision int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0) FROM coordinator.runs WHERE case_id=$1`, caseID).Scan(&revision); err != nil {
		return receipt, errors.New("case_replay_revision_read_failed")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO coordinator.case_replays(case_id,actor_id,reason,prior_status,prior_stage,resumed_revision) VALUES($1,$2,$3,$4,$5,NULLIF($6,0))`, caseID, actorID, reason, priorStatus, priorStage, revision); err != nil {
		return receipt, errors.New("case_replay_audit_write_failed")
	}
	now := time.Now().UTC()
	if err := tx.QueryRow(ctx, `UPDATE coordinator.cases SET status=$2,next_stage=$3,retry_at=NULL,held_reason=NULL,refresh_needed=$4,target_bridge_version=$5,lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, caseID, status, stage, refreshNeeded, targetVersion).Scan(&receipt.ReplayedAt); err != nil {
		return ReplayReceipt{}, errors.New("case_replay_write_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return ReplayReceipt{}, errors.New("case_replay_commit_failed")
	}
	receipt.CaseID = caseID
	receipt.Status = status
	receipt.Stage = stage
	receipt.Revision = revision
	receipt.ActorID = actorID
	if receipt.ReplayedAt.IsZero() {
		receipt.ReplayedAt = now
	}
	return receipt, nil
}
