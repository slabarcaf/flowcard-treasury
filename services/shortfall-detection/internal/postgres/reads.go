package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

const recordColumns = `id::text, customer_id, account_id, card_id, outcome, reason,
	currency, attempted_amount_cents, available_balance_cents, shortfall_cents,
	canonical_event_id, transaction_id, lifecycle_id, created_at, provider_created_at,
	provider_transacted_at, provider_updated_at, balance_requested_at, balance_observed_at`

// ListObservations reads final observations within the configured environment.
func (r *Repository) ListObservations(ctx context.Context, query domain.ListQuery) (domain.Page, error) {
	var afterTime *time.Time
	afterID := "00000000-0000-0000-0000-000000000000"
	if query.After != nil {
		afterTime, afterID = &query.After.CreatedAt, query.After.ID
	}
	rows, err := r.pool.Query(ctx, `SELECT `+recordColumns+` FROM shortfall.detections
		WHERE environment = $1 AND outcome IN ('SHORTFALL_DETECTED', 'NO_CURRENT_SHORTFALL', 'IGNORED')
		AND ($2::text = '' OR customer_id = $2) AND ($3::text = '' OR outcome = $3)
		AND ($4::timestamptz IS NULL OR (created_at, id) < ($4::timestamptz, $5::uuid))
		ORDER BY created_at DESC, id DESC LIMIT 26`, query.Environment, query.CustomerID,
		string(query.Outcome), afterTime, afterID)
	if err != nil {
		return domain.Page{}, errors.New("observations_unavailable")
	}
	defer rows.Close()
	page := domain.Page{Items: make([]domain.Record, 0, 25)}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return domain.Page{}, errors.New("observations_unavailable")
		}
		page.Items = append(page.Items, record)
	}
	if rows.Err() != nil {
		return domain.Page{}, errors.New("observations_unavailable")
	}
	if len(page.Items) > 25 {
		page.Items = page.Items[:25]
		last := page.Items[24]
		page.Next = &domain.ReadCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// Observation returns one final record, or nil when unknown in this environment.
func (r *Repository) Observation(ctx context.Context, environment, id string) (*domain.Record, error) {
	record, err := scanRecord(r.pool.QueryRow(ctx, `SELECT `+recordColumns+`
		FROM shortfall.detections WHERE environment = $1 AND id = $2
		AND outcome IN ('SHORTFALL_DETECTED', 'NO_CURRENT_SHORTFALL', 'IGNORED')`, environment, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("observations_unavailable")
	}
	return &record, nil
}

func scanRecord(row interface{ Scan(...any) error }) (domain.Record, error) {
	var record domain.Record
	err := row.Scan(&record.ID, &record.CustomerID, &record.AccountID, &record.CardID,
		&record.Outcome, &record.Reason, &record.Currency, &record.AttemptedAmountCents,
		&record.AvailableBalanceCents, &record.ShortfallCents, &record.CanonicalEventID,
		&record.TransactionID, &record.LifecycleID, &record.CreatedAt, &record.ProviderCreatedAt,
		&record.ProviderTransactedAt, &record.ProviderUpdatedAt,
		&record.BalanceRequestedAt, &record.BalanceObservedAt)
	if err != nil {
		return domain.Record{}, err
	}
	record.CreatedAt = record.CreatedAt.UTC()
	for _, value := range []*time.Time{record.ProviderCreatedAt, record.ProviderTransactedAt,
		record.ProviderUpdatedAt, record.BalanceRequestedAt, record.BalanceObservedAt} {
		if value != nil {
			*value = value.UTC()
		}
	}
	record.RequiresRecheck = true
	record.AmountSemantics = "current_need_at_balance_observation"
	return record, nil
}
