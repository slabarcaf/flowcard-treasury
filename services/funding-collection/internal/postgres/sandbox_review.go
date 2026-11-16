package postgres

import (
	"context"
	"errors"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
)

// HoldSandboxDepositReview preserves debt and reservations while a signed,
// mapped deposit reversal/rejection requires a separately verified reducer.
func (r *Repository) HoldSandboxDepositReview(ctx context.Context, job WebhookJobClaim, facilityID string, scope TrustedDepositScope) error {
	if job.Mode != domain.ModeSandbox || job.TargetKind != "UNSUPPORTED" || job.CustomerID == "" || job.DepositID == "" || scope.Mode != job.Mode || scope.CustomerID != job.CustomerID || scope.CardID != job.CardID {
		return errors.New("sandbox_deposit_review_scope_invalid")
	}
	tx, err := r.BeginFinancial(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := lockFundingFacility(ctx, tx, string(job.Mode), facilityID); err != nil {
		return err
	}
	if _, err := lockFundingCustomer(ctx, tx, string(job.Mode), job.CustomerID); err != nil {
		return err
	}
	bridgeIDs, err := lockCustomerBridges(ctx, tx, string(job.Mode), job.CustomerID)
	if err != nil {
		return err
	}
	if _, err := upstream.ResolveMapping(ctx, tx, job.CustomerID, scope.AccountRef, job.CardID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE servicing.customers SET active_hold=true,version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND NOT active_hold`, string(job.Mode), job.CustomerID); err != nil {
		return errors.New("sandbox_review_customer_hold_failed")
	}
	for _, id := range bridgeIDs {
		if err := appendCollectionHold(ctx, tx, job.Mode, id, "SANDBOX_DEPOSIT_REVIEW_REQUIRED", map[string]any{"webhook_event_id": job.EventID, "movement_certainty": "UNCERTAIN"}); err != nil {
			return err
		}
	}
	if err := CompleteWebhookJobTx(ctx, tx, job, "HELD", "SANDBOX_DEPOSIT_REVIEW_REQUIRED"); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return errors.New("sandbox_review_commit_failed")
	}
	return nil
}
