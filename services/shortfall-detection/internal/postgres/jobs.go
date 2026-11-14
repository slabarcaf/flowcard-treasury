package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

// ErrLeaseLost prevents revoked, expired, or replaced claims from persisting work.
var ErrLeaseLost = errors.New("inbox_lease_lost")

// Claim commits one scoped 60-second lease before returning protected evidence.
func (r *Repository) Claim(ctx context.Context, environment, source string) (*domain.Job, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, ErrInboxUnavailable
	}
	defer func() {
		// Rollback after commit is harmless; failed rollback closes the connection.
		_ = tx.Rollback(ctx)
	}()
	var job domain.Job
	err = tx.QueryRow(ctx, `WITH picked AS (
		SELECT id FROM shortfall.webhook_inbox
		WHERE environment = $1 AND source = $2 AND NOT conflicted
		AND ((state IN ('RECEIVED', 'RETRY_WAIT')
		      AND (next_attempt_at IS NULL OR next_attempt_at <= clock_timestamp()))
		     OR (state = 'PROCESSING' AND lease_expires_at <= clock_timestamp()))
		ORDER BY received_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE shortfall.webhook_inbox i SET state = 'PROCESSING',
		lease_token = gen_random_uuid(), lease_expires_at = clock_timestamp() + interval '60 seconds',
		next_attempt_at = NULL, updated_at = clock_timestamp()
	FROM picked WHERE i.id = picked.id
	RETURNING i.id::text, i.lease_token::text, i.lease_expires_at, i.failed_attempts,
		i.environment, i.source, i.webhook_id, i.event_name, i.raw_body`, environment, source).
		Scan(&job.ID, &job.LeaseToken, &job.LeaseExpiresAt, &job.FailedAttempts,
			&job.Delivery.Environment, &job.Delivery.Source, &job.Delivery.WebhookID,
			&job.Delivery.EventName, &job.Delivery.RawBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrInboxUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, ErrInboxUnavailable
	}
	return &job, nil
}

func lockJob(ctx context.Context, tx pgx.Tx, job domain.Job) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM shortfall.webhook_inbox
		WHERE id = $1 AND environment = $2 AND source = $3
		AND state = 'PROCESSING' AND lease_token = $4 AND NOT conflicted
		FOR UPDATE`, job.ID, job.Delivery.Environment, job.Delivery.Source, job.LeaseToken).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return ErrInboxUnavailable
	}
	// Check database time after acquiring the lock, including time spent waiting.
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT lease_expires_at > clock_timestamp()
		FROM shortfall.webhook_inbox WHERE id = $1`, job.ID).Scan(&valid); err != nil {
		return ErrInboxUnavailable
	}
	if !valid {
		return ErrLeaseLost
	}
	return nil
}

// Complete commits only final observations (or an inbox-only confirmed ignore),
// linking evidence and completing the inbox under its current unexpired lease.
// Unresolved evidence is never inserted as an immutable canonical observation.
func (r *Repository) Complete(ctx context.Context, job domain.Job, observation *domain.Observation, reason string) (string, error) {
	if observation != nil {
		if observation.Outcome != domain.ShortfallDetected && observation.Outcome != domain.NoCurrentShortfall && observation.Outcome != domain.Ignored {
			return "", errors.New("completion_requires_final_observation")
		}
		reason = observation.Reason
	} else if reason == "" {
		return "", errors.New("completion_requires_ignore_reason")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", errObservationDatabaseUnavailable
	}
	defer func() {
		// Any failed fence rolls back the observation and link with inbox changes.
		_ = tx.Rollback(ctx)
	}()
	if err := lockJob(ctx, tx, job); err != nil {
		if errors.Is(err, ErrInboxUnavailable) {
			return "", errObservationDatabaseUnavailable
		}
		return "", err
	}
	var detectionID string
	if observation != nil {
		detectionID, err = insertObservation(ctx, tx, job.Delivery.Environment, *observation)
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shortfall.inbox_detections (inbox_id, detection_id)
			VALUES ($1, $2)`, job.ID, detectionID); err != nil {
			return "", errObservationDatabaseUnavailable
		}
		// The canonical winner's reason belongs to the link, even if this attempt
		// lost a uniqueness race with a differently classified observation.
		if err := tx.QueryRow(ctx, `SELECT COALESCE(reason, '') FROM shortfall.detections
			WHERE id = $1`, detectionID).Scan(&reason); err != nil {
			return "", errObservationDatabaseUnavailable
		}
	}
	// Recheck expiry after canonical uniqueness waits, immediately before commit.
	tag, err := tx.Exec(ctx, `UPDATE shortfall.webhook_inbox SET state = 'PROCESSED',
		lease_token = NULL, lease_expires_at = NULL, next_attempt_at = NULL,
		last_reason = NULLIF($3, ''), processed_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND lease_token = $2 AND state = 'PROCESSING'
		AND NOT conflicted AND lease_expires_at > clock_timestamp()`, job.ID, job.LeaseToken, reason)
	if err != nil {
		return "", errObservationDatabaseUnavailable
	}
	if tag.RowsAffected() != 1 {
		return "", ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return "", errObservationDatabaseUnavailable
	}
	return detectionID, nil
}

// RecordFailure retains unresolved work on the inbox, clearing the lease and
// counting one actual failure. A failed DB write leaves recovery to lease expiry.
func (r *Repository) RecordFailure(ctx context.Context, job domain.Job, failure domain.Failure, delay time.Duration) (domain.InboxState, error) {
	if delay < 0 {
		return "", errors.New("retry_delay_invalid")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", ErrInboxUnavailable
	}
	defer func() {
		// Rollback leaves the original processing lease for crash recovery.
		_ = tx.Rollback(ctx)
	}()
	if err := lockJob(ctx, tx, job); err != nil {
		return "", err
	}
	state, reason := domain.Held, failure.Reason
	if failure.Retryable {
		if job.FailedAttempts+1 < 8 {
			state = domain.RetryWait
		} else {
			reason = "RETRIES_EXHAUSTED:" + failure.Reason
		}
	}
	// Round upward so a Retry-After minimum is never shortened by conversion.
	microseconds := delay.Microseconds()
	if delay%time.Microsecond != 0 {
		microseconds++
	}
	interval := pgtype.Interval{Microseconds: microseconds, Valid: true}
	tag, err := tx.Exec(ctx, `UPDATE shortfall.webhook_inbox SET state = $3,
		failed_attempts = LEAST(failed_attempts + 1, 8),
		next_attempt_at = CASE WHEN $3 = 'RETRY_WAIT' THEN clock_timestamp() + $5::interval ELSE NULL END,
		last_reason = $4, lease_token = NULL, lease_expires_at = NULL, updated_at = clock_timestamp()
		WHERE id = $1 AND lease_token = $2 AND state = 'PROCESSING'
		AND NOT conflicted AND lease_expires_at > clock_timestamp()`,
		job.ID, job.LeaseToken, string(state), reason, interval)
	if err != nil {
		return "", ErrInboxUnavailable
	}
	if tag.RowsAffected() != 1 {
		return "", ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return "", ErrInboxUnavailable
	}
	return state, nil
}

// Replay requeues only held, unconflicted, unleased items with no completed result.
// It preserves original bytes/identity and appends the operator's reason/history.
func (r *Repository) Replay(ctx context.Context, inboxID, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return errors.New("replay_reason_invalid")
	}
	tag, err := r.pool.Exec(ctx, `UPDATE shortfall.webhook_inbox i SET
		state = 'RECEIVED', failed_attempts = 0, next_attempt_at = NULL,
		last_reason = NULL, updated_at = clock_timestamp(),
		replay_audit = replay_audit || jsonb_build_array(jsonb_build_object(
			'reason', $2::text, 'replayed_at', clock_timestamp(),
			'previous_reason', last_reason, 'failed_attempts', failed_attempts))
		WHERE id = $1 AND state = 'HELD' AND NOT conflicted AND processed_at IS NULL
		AND lease_token IS NULL AND lease_expires_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM shortfall.inbox_conflicts c WHERE c.inbox_id = i.id)
		AND NOT EXISTS (SELECT 1 FROM shortfall.inbox_detections d WHERE d.inbox_id = i.id)`, inboxID, reason)
	if err != nil {
		return ErrInboxUnavailable
	}
	if tag.RowsAffected() != 1 {
		return errors.New("replay_not_eligible")
	}
	return nil
}
