package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

var errObservationDatabaseUnavailable error = &domain.Failure{Reason: "OBSERVATION_DATABASE_UNAVAILABLE", Retryable: true}

// FindObservation returns an existing canonical observation before a balance read.
func (r *Repository) FindObservation(ctx context.Context, environment, accountID, eventID string) (*domain.StoredObservation, error) {
	var stored domain.StoredObservation
	var facts []byte
	err := r.pool.QueryRow(ctx, `SELECT id::text, created_at, provider_facts,
		outcome, COALESCE(reason, ''), currency, attempted_amount_cents,
		available_balance_cents, shortfall_cents, balance_requested_at, balance_observed_at
		FROM shortfall.detections
		WHERE environment = $1 AND account_id = $2 AND canonical_event_id = $3
		AND outcome IN ('SHORTFALL_DETECTED', 'NO_CURRENT_SHORTFALL', 'IGNORED')`,
		environment, accountID, eventID).Scan(&stored.ID, &stored.CreatedAt, &facts,
		&stored.Observation.Outcome, &stored.Observation.Reason, &stored.Observation.Currency,
		&stored.Observation.AttemptedAmountCents, &stored.Observation.AvailableBalanceCents,
		&stored.Observation.ShortfallCents, &stored.Observation.BalanceRequestedAt,
		&stored.Observation.BalanceObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || json.Unmarshal(facts, &stored.Observation.Facts) != nil {
		return nil, &domain.Failure{Reason: "OBSERVATION_DATABASE_UNAVAILABLE", Retryable: true}
	}
	return &stored, nil
}

// insertObservation is called only inside E's fenced inbox completion transaction.
// A canonical uniqueness race returns the first committed ID, never an overwrite.
func insertObservation(ctx context.Context, tx pgx.Tx, environment string, o domain.Observation) (string, error) {
	if o.Outcome != domain.ShortfallDetected && o.Outcome != domain.NoCurrentShortfall && o.Outcome != domain.Ignored {
		return "", errors.New("observation_must_be_final")
	}
	facts, err := json.Marshal(o.Facts)
	if err != nil {
		return "", errors.New("observation_facts_invalid")
	}
	provenance, err := json.Marshal(o.Facts.Provenance)
	if err != nil {
		return "", errors.New("observation_provenance_invalid")
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO shortfall.detections (
		environment, account_id, canonical_event_id, card_id, customer_id,
		transaction_id, lifecycle_id, provider_facts, field_provenance,
		provider_created_at, provider_transacted_at, provider_updated_at,
		attempted_amount_cents, available_balance_cents, shortfall_cents, currency,
		balance_requested_at, balance_observed_at, outcome, reason)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),
		$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,NULLIF($20,''))
		ON CONFLICT (environment, account_id, canonical_event_id) DO NOTHING RETURNING id::text`,
		environment, o.Facts.Mapping.AccountID, o.Facts.CanonicalEventID,
		o.Facts.Mapping.CardID, o.Facts.Mapping.CustomerID, o.Facts.TransactionID, o.Facts.LifecycleID,
		facts, provenance, o.Facts.CreatedAt, o.Facts.TransactedAt, o.Facts.UpdatedAt,
		o.AttemptedAmountCents, o.AvailableBalanceCents, o.ShortfallCents, o.Currency,
		o.BalanceRequestedAt, o.BalanceObservedAt, o.Outcome, o.Reason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id::text FROM shortfall.detections
			WHERE environment = $1 AND account_id = $2 AND canonical_event_id = $3
			AND outcome IN ('SHORTFALL_DETECTED', 'NO_CURRENT_SHORTFALL', 'IGNORED')`,
			environment, o.Facts.Mapping.AccountID, o.Facts.CanonicalEventID).Scan(&id)
	}
	if err != nil {
		return "", errObservationDatabaseUnavailable
	}
	return id, nil
}
