package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

type CollectionOriginLink struct {
	CaseID       *string
	EvaluationID *string
	OfferID      *string
}

// ReadCollectionOriginLink returns the exact persisted bridge link for one leased case.
func (r *Repository) ReadCollectionOriginLink(ctx context.Context, caseID, token string) (CollectionOriginLink, error) {
	var link CollectionOriginLink
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return link, errors.New("collection_origin_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return link, err
	}
	var bindingID, bridgeID string
	err = tx.QueryRow(ctx, `SELECT binding_id,bridge_id,originating_case_id::text FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION'`, caseID).Scan(&bindingID, &bridgeID, &link.CaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CollectionOriginLink{}, domain.ErrNotFound
	}
	if err != nil {
		return CollectionOriginLink{}, errors.New("collection_origin_case_read_failed")
	}
	if link.CaseID != nil {
		err = tx.QueryRow(ctx, `SELECT evaluation_id,offer_id FROM coordinator.servicing_changes WHERE binding_id=$1 AND bridge_id=$2 AND resource_type='BRIDGE' AND originating_case_id=$3 AND evaluation_id IS NOT NULL AND offer_id IS NOT NULL ORDER BY sequence LIMIT 1`, bindingID, bridgeID, *link.CaseID).Scan(&link.EvaluationID, &link.OfferID)
		if errors.Is(err, pgx.ErrNoRows) {
			return CollectionOriginLink{}, domain.ErrConflict
		}
		if err != nil {
			return CollectionOriginLink{}, errors.New("collection_origin_event_read_failed")
		}
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return CollectionOriginLink{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CollectionOriginLink{}, errors.New("collection_origin_commit_failed")
	}
	return link, nil
}
