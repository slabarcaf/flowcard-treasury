package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

func (r *Repository) PrepareAssessment(ctx context.Context, caseID, token string, revision int, key, sourceTransactionID, sourceCardID string, body []byte) (string, error) {
	if strings.TrimSpace(sourceTransactionID) == "" || strings.TrimSpace(sourceCardID) == "" {
		return "", errors.New("assessment_source_linkage_required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", errors.New("assessment_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO coordinator.assessments(case_id,revision,idempotency_key,request_body,request_hash,source_transaction_id,source_card_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(case_id,revision) DO NOTHING RETURNING id`, caseID, revision, key, body, hash[:], sourceTransactionID, sourceCardID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedKey, storedTransactionID, storedCardID string
		var storedHash, storedBody []byte
		if err := tx.QueryRow(ctx, `SELECT id,idempotency_key,request_hash,request_body,source_transaction_id,source_card_id FROM coordinator.assessments WHERE case_id=$1 AND revision=$2 FOR UPDATE`, caseID, revision).Scan(&id, &storedKey, &storedHash, &storedBody, &storedTransactionID, &storedCardID); err != nil {
			return "", errors.New("assessment_replay_read_failed")
		}
		if storedKey != key || !bytesEqual(storedHash, hash[:]) || !bytesEqual(storedBody, body) || storedTransactionID != sourceTransactionID || storedCardID != sourceCardID {
			return "", domain.ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return "", errors.New("assessment_replay_commit_failed")
		}
		return id, nil
	} else if err != nil {
		return "", errors.New("assessment_request_persist_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", errors.New("assessment_request_commit_failed")
	}
	return id, nil
}

func (r *Repository) SaveAssessmentResult(ctx context.Context, caseID, token, assessmentRowID, savedID string, response []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("assessment_result_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var rowCase string
	var existingID *string
	var existingBody []byte
	if err := tx.QueryRow(ctx, `SELECT case_id,saved_assessment_id,response_body FROM coordinator.assessments WHERE id=$1 FOR UPDATE`, assessmentRowID).Scan(&rowCase, &existingID, &existingBody); err != nil || rowCase != caseID {
		return domain.ErrLeaseLost
	}
	if existingID != nil {
		if *existingID != savedID {
			return domain.ErrConflict
		}
		if len(existingBody) == 0 {
			tag, err := tx.Exec(ctx, `UPDATE coordinator.assessments SET response_body=$2 WHERE id=$1 AND saved_assessment_id=$3 AND response_body IS NULL`, assessmentRowID, response, savedID)
			if err != nil {
				return errors.New("assessment_result_recovery_write_failed")
			}
			if tag.RowsAffected() != 1 {
				return domain.ErrConflict
			}
			if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return errors.New("assessment_result_recovery_commit_failed")
			}
			return nil
		}
		if !bytesEqual(existingBody, response) {
			return domain.ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("assessment_result_replay_commit_failed")
		}
		return nil
	}
	if len(existingBody) != 0 {
		return domain.ErrConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE coordinator.assessments SET saved_assessment_id=$2,response_body=$3,saved_at=clock_timestamp() WHERE id=$1 AND case_id=$4 AND saved_assessment_id IS NULL`, assessmentRowID, savedID, response, caseID)
	if err != nil {
		return errors.New("assessment_result_write_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("assessment_result_commit_failed")
	}
	return nil
}

func (r *Repository) ReadAssessment(ctx context.Context, caseID string, revision int) (domain.AssessmentRequest, error) {
	var a domain.AssessmentRequest
	var saved *string
	err := r.pool.QueryRow(ctx, `SELECT id::text,case_id::text,revision,idempotency_key,request_body,request_hash,source_transaction_id,source_card_id,saved_assessment_id,response_body FROM coordinator.assessments WHERE case_id=$1 AND revision=$2`, caseID, revision).Scan(&a.ID, &a.CaseID, &a.Revision, &a.IdempotencyKey, &a.Body, &a.BodyHash, &a.SourceTransactionID, &a.SourceCardID, &saved, &a.Response)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AssessmentRequest{}, domain.ErrNotFound
		}
		return domain.AssessmentRequest{}, errors.New("assessment_read_failed")
	}
	a.SavedAssessmentID = saved
	return a, nil
}

// ReadLatestAssessment returns the latest immutable saved-decision row for API display.
func (r *Repository) ReadLatestAssessment(ctx context.Context, caseID string) (domain.AssessmentRequest, error) {
	var a domain.AssessmentRequest
	var saved *string
	err := r.pool.QueryRow(ctx, `SELECT id::text,case_id::text,revision,idempotency_key,request_body,request_hash,source_transaction_id,source_card_id,saved_assessment_id,response_body FROM coordinator.assessments WHERE case_id=$1 ORDER BY revision DESC LIMIT 1`, caseID).Scan(&a.ID, &a.CaseID, &a.Revision, &a.IdempotencyKey, &a.Body, &a.BodyHash, &a.SourceTransactionID, &a.SourceCardID, &saved, &a.Response)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AssessmentRequest{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AssessmentRequest{}, errors.New("assessment_read_failed")
	}
	a.SavedAssessmentID = saved
	return a, nil
}
