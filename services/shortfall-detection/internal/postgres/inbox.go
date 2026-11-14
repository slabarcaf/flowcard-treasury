package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

// ErrInboxUnavailable hides driver details and protected payloads at receipt boundaries.
var ErrInboxUnavailable = errors.New("inbox_unavailable")

// Receive atomically ingests a new delivery, identifies its original, or records
// conflicting bytes and fences uncompleted work. It uses runtime column grants.
func (r *Repository) Receive(ctx context.Context, delivery domain.Delivery) error {
	// READ COMMITTED gives the post-insert lookup a fresh snapshot after a
	// concurrent conflicting insert commits. A single CTE snapshot cannot do so.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ErrInboxUnavailable
	}
	defer func() {
		// Rollback after commit is harmless; failed rollback closes the connection.
		_ = tx.Rollback(ctx)
	}()
	hash := sha256.Sum256(delivery.RawBody)
	if _, err := tx.Exec(ctx, `INSERT INTO shortfall.webhook_inbox
		(environment, source, webhook_id, event_name, raw_body, payload_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (environment, source, webhook_id) DO NOTHING`,
		delivery.Environment, delivery.Source, delivery.WebhookID, delivery.EventName,
		delivery.RawBody, hash[:]); err != nil {
		return ErrInboxUnavailable
	}
	var inboxID string
	var originalHash []byte
	if err := tx.QueryRow(ctx, `SELECT id::text, payload_hash
		FROM shortfall.webhook_inbox
		WHERE environment = $1 AND source = $2 AND webhook_id = $3
		FOR UPDATE`, delivery.Environment, delivery.Source, delivery.WebhookID).
		Scan(&inboxID, &originalHash); err != nil {
		return ErrInboxUnavailable
	}
	if !bytes.Equal(hash[:], originalHash) {
		if _, err := tx.Exec(ctx, `INSERT INTO shortfall.inbox_conflicts
			(inbox_id, raw_body, payload_hash) VALUES ($1, $2, $3)
			ON CONFLICT (inbox_id, payload_hash) DO NOTHING`,
			inboxID, delivery.RawBody, hash[:]); err != nil {
			return ErrInboxUnavailable
		}
		// One update satisfies state/lease/conflict checks together. If a worker
		// committed first, preserve PROCESSED and its immutable detection/link.
		// Otherwise revoke its token before any later fenced completion can win.
		if _, err := tx.Exec(ctx, `UPDATE shortfall.webhook_inbox SET
			conflicted = true,
			state = CASE WHEN state = 'PROCESSED' THEN 'PROCESSED' ELSE 'HELD' END,
			lease_token = NULL, lease_expires_at = NULL, next_attempt_at = NULL,
			last_reason = 'PAYLOAD_CONFLICT', updated_at = clock_timestamp()
			WHERE id = $1`, inboxID); err != nil {
			return ErrInboxUnavailable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrInboxUnavailable
	}
	return nil
}
