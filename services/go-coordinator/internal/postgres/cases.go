package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

// CreateCase inserts a discovery stub and safely reuses its immutable source identity.
func (r *Repository) CreateCase(ctx context.Context, c domain.Case) (string, error) {
	caseID := c.ID
	if caseID == "" {
		sourceID := c.DetectionID
		if c.Kind == domain.CaseCollection {
			sourceID = c.BridgeID
		}
		if sourceID == nil {
			return "", errors.New("case_source_identity_missing")
		}
		var err error
		caseID, err = domain.StableCaseID(c.BindingID, c.Kind, *sourceID)
		if err != nil {
			return "", err
		}
	}
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO coordinator.cases(id,binding_id,kind,detection_id,bridge_id,customer_id,account_ref,raw_account_id,status,next_stage)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING RETURNING id`, caseID, c.BindingID, c.Kind, c.DetectionID, c.BridgeID, c.CustomerID, c.AccountRef, c.RawAccountID, c.Status, c.NextStage).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		query := `SELECT id,customer_id,account_ref,raw_account_id FROM coordinator.cases WHERE binding_id=$1 AND ((detection_id IS NOT NULL AND detection_id=$2) OR (bridge_id IS NOT NULL AND bridge_id=$3))`
		var customer, account, rawAccount *string
		if err := r.pool.QueryRow(ctx, query, c.BindingID, c.DetectionID, c.BridgeID).Scan(&id, &customer, &account, &rawAccount); err != nil {
			return "", domain.ErrConflict
		}
		if !same(customer, c.CustomerID) || !same(account, c.AccountRef) || !same(rawAccount, c.RawAccountID) {
			return "", domain.ErrConflict
		}
		return id, nil
	}
	if err != nil {
		return "", errors.New("case_insert_failed")
	}
	return id, nil
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("lease_token_generation_failed")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:]), nil
}

// ClaimNext leases a runnable case without changing its business status.
func (r *Repository) ClaimNext(ctx context.Context, lease time.Duration, bindingID, mode string) (domain.Case, error) {
	if bindingID == "" || (mode != "DEMO" && mode != "SANDBOX") {
		return domain.Case{}, errors.New("case_claim_scope_invalid")
	}
	token, err := randomToken()
	if err != nil {
		return domain.Case{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Case{}, errors.New("case_claim_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var c domain.Case
	err = tx.QueryRow(ctx, `WITH candidate AS (
		SELECT c.id FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id WHERE c.binding_id=$3 AND b.mode=$4 AND (c.lease_expires_at IS NULL OR c.lease_expires_at<=clock_timestamp())
		AND (c.retry_at IS NULL OR c.retry_at<=clock_timestamp())
		AND ((c.status IN ('NEW','RETRY_WAIT','EVALUATING','INVESTIGATING')) OR (c.kind='COLLECTION' AND c.status IN ('MONITORING','ACTION_REQUIRED') AND (c.refresh_needed OR c.next_stage='INVESTIGATE')))
		ORDER BY CASE WHEN c.kind='COLLECTION' AND (c.refresh_needed OR COALESCE(c.target_bridge_version,0)>COALESCE(c.bridge_version,-1)) THEN 0 ELSE 1 END,COALESCE(c.retry_at,c.created_at),c.created_at,c.id FOR UPDATE OF c SKIP LOCKED LIMIT 1
	) UPDATE coordinator.cases c SET lease_token=$1::uuid,lease_expires_at=clock_timestamp()+$2::interval,
		lease_stage=CASE WHEN c.kind='COLLECTION' AND (c.refresh_needed OR COALESCE(c.target_bridge_version,0)>COALESCE(c.bridge_version,-1)) THEN 'REFRESH_BRIDGE' WHEN c.lease_expires_at IS NOT NULL THEN COALESCE(c.lease_stage,c.next_stage) ELSE c.next_stage END,
		next_stage=CASE WHEN c.kind='COLLECTION' AND (c.refresh_needed OR COALESCE(c.target_bridge_version,0)>COALESCE(c.bridge_version,-1)) THEN 'REFRESH_BRIDGE' WHEN c.lease_expires_at IS NOT NULL THEN COALESCE(c.lease_stage,c.next_stage) ELSE c.next_stage END,
		updated_at=clock_timestamp() FROM candidate WHERE c.id=candidate.id
		RETURNING c.id,c.binding_id,c.kind,c.detection_id,c.bridge_id,c.customer_id,c.account_ref,c.raw_account_id,c.status,c.next_stage,c.retry_at,c.held_reason,
		c.lease_token::text,c.lease_expires_at,c.lease_stage,c.bridge_version,c.refresh_needed,c.target_bridge_version,c.transient_failures,c.pending_investigation_trigger,c.pending_trigger_bridge_version`, token, lease.String(), bindingID, mode).Scan(&c.ID, &c.BindingID, &c.Kind, &c.DetectionID, &c.BridgeID, &c.CustomerID, &c.AccountRef, &c.RawAccountID, &c.Status, &c.NextStage, &c.RetryAt, &c.HeldReason, &c.LeaseToken, &c.LeaseExpiresAt, &c.LeaseStage, &c.BridgeVersion, &c.RefreshNeeded, &c.TargetVersion, &c.TransientFailures, &c.PendingTrigger, &c.PendingTriggerVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Case{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Case{}, errors.New("case_claim_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Case{}, errors.New("case_claim_commit_failed")
	}
	return c, nil
}

type Binding struct{ ID, Mode, Fingerprint, EndpointIdentity string }

func (r *Repository) ReadBinding(ctx context.Context, id string) (Binding, error) {
	var b Binding
	err := r.pool.QueryRow(ctx, `SELECT id,mode,fingerprint,endpoint_identity FROM coordinator.source_bindings WHERE id=$1`, id).Scan(&b.ID, &b.Mode, &b.Fingerprint, &b.EndpointIdentity)
	if err != nil {
		return Binding{}, errors.New("source_binding_read_failed")
	}
	return b, nil
}

func (r *Repository) DisposeCase(ctx context.Context, caseID, token, status, stage string, retryAt *time.Time, heldReason *string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE coordinator.cases SET status=$3,next_stage=$4,retry_at=$5,held_reason=$6,transient_failures=0,pending_investigation_trigger=CASE WHEN $4='INVESTIGATE' THEN pending_investigation_trigger ELSE NULL END,pending_trigger_bridge_version=CASE WHEN $4='INVESTIGATE' THEN pending_trigger_bridge_version ELSE NULL END,lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp()`, caseID, token, status, stage, retryAt, heldReason)
	if err != nil {
		return errors.New("case_disposition_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}

func (r *Repository) RecordCaseFailure(ctx context.Context, caseID, token string, retryAt time.Time, reason string, maxFailures int) (int, error) {
	var failures int
	err := r.pool.QueryRow(ctx, `UPDATE coordinator.cases SET transient_failures=transient_failures+1,status=CASE WHEN transient_failures+1 >= $5 THEN 'HELD' ELSE 'RETRY_WAIT' END,retry_at=CASE WHEN transient_failures+1 >= $5 THEN NULL ELSE $3 END,held_reason=$4,lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,updated_at=clock_timestamp() WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp() RETURNING transient_failures`, caseID, token, retryAt, reason, maxFailures).Scan(&failures)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrLeaseLost
	}
	if err != nil {
		return 0, errors.New("case_retry_write_failed")
	}
	return failures, nil
}

func (r *Repository) ClearCaseFailures(ctx context.Context, caseID, token string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE coordinator.cases SET transient_failures=0 WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp()`, caseID, token)
	if err != nil {
		return errors.New("case_failure_reset_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}

func (r *Repository) ReleaseInterrupted(ctx context.Context, caseID, token string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE coordinator.cases SET status='RETRY_WAIT',retry_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL,lease_stage=NULL,held_reason='SHUTDOWN_INTERRUPTED',updated_at=clock_timestamp() WHERE id=$1 AND lease_token=$2::uuid AND lease_expires_at>clock_timestamp()`, caseID, token)
	if err != nil {
		return errors.New("case_shutdown_release_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}
