package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

type DiscoveryPage struct {
	BindingID                  string
	ExpectedCursor, NextCursor *string
	StartedAt, SucceededAt     time.Time
	GreatestCreatedAt          *time.Time
	GreatestDetectionID        *string
	Cases                      []DiscoveryCase
}
type DiscoveryCase struct {
	CaseID                 string
	DetectionID            string
	CreatedAt              time.Time
	CustomerID, AccountRef *string
	RawAccountID           *string
	Status, Stage          string
	HeldReason             *string
}

type DiscoveryState struct {
	Cursor                  *string
	ScanStartedAt           *time.Time
	ScanCompletedAt         *time.Time
	LastPageSucceededAt     *time.Time
	GreatestCreatedAt       *time.Time
	GreatestDetectionID     *string
	ServicingSequence       uint64
	ShortfallFailedAttempts int
	ShortfallRetryAt        *time.Time
	ServicingFailedAttempts int
	ServicingRetryAt        *time.Time
	ServicingLastPageAt     *time.Time
	ShortfallHoldReason     *string
	ServicingHoldReason     *string
}

func (r *Repository) ReadDiscoveryState(ctx context.Context, bindingID string) (DiscoveryState, error) {
	var state DiscoveryState
	err := r.pool.QueryRow(ctx, `SELECT shortfall_cursor,scan_started_at,scan_completed_at,last_page_succeeded_at,greatest_created_at,greatest_detection_id,servicing_sequence,shortfall_failed_attempts,shortfall_retry_at,servicing_failed_attempts,servicing_retry_at,servicing_last_page_at,shortfall_hold_reason,servicing_hold_reason FROM coordinator.discovery_progress WHERE binding_id=$1`, bindingID).Scan(&state.Cursor, &state.ScanStartedAt, &state.ScanCompletedAt, &state.LastPageSucceededAt, &state.GreatestCreatedAt, &state.GreatestDetectionID, &state.ServicingSequence, &state.ShortfallFailedAttempts, &state.ShortfallRetryAt, &state.ServicingFailedAttempts, &state.ServicingRetryAt, &state.ServicingLastPageAt, &state.ShortfallHoldReason, &state.ServicingHoldReason)
	if err != nil {
		return DiscoveryState{}, errors.New("discovery_state_read_failed")
	}
	return state, nil
}

func (r *Repository) RecordFeedFailure(ctx context.Context, bindingID, feed string, retryAt time.Time) error {
	column := "shortfall"
	if feed == "servicing" {
		column = "servicing"
	} else if feed != "shortfall" {
		return errors.New("feed_name_invalid")
	}
	_, err := r.pool.Exec(ctx, `UPDATE coordinator.discovery_progress SET `+column+`_failed_attempts=`+column+`_failed_attempts+1,`+column+`_retry_at=$2,updated_at=clock_timestamp() WHERE binding_id=$1`, bindingID, retryAt)
	if err != nil {
		return errors.New("discovery_failure_record_failed")
	}
	return nil
}
func (r *Repository) HoldFeed(ctx context.Context, bindingID, feed, reason string) error {
	column := "shortfall"
	if feed == "servicing" {
		column = "servicing"
	} else if feed != "shortfall" {
		return errors.New("feed_name_invalid")
	}
	_, err := r.pool.Exec(ctx, `UPDATE coordinator.discovery_progress SET `+column+`_hold_reason=$2,`+column+`_retry_at=NULL,updated_at=clock_timestamp() WHERE binding_id=$1`, bindingID, reason)
	if err != nil {
		return errors.New("discovery_hold_write_failed")
	}
	return nil
}
func (r *Repository) ServicingAge(ctx context.Context, bindingID string, now time.Time) (time.Duration, bool, error) {
	var last *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT servicing_last_page_at FROM coordinator.discovery_progress WHERE binding_id=$1`, bindingID).Scan(&last); err != nil {
		return 0, false, errors.New("servicing_age_read_failed")
	}
	if last == nil {
		return 0, true, nil
	}
	return now.Sub(*last), false, nil
}

func (r *Repository) RestartShortfallScan(ctx context.Context, bindingID string, expected *string, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("discovery_restart_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cursor *string
	if err := tx.QueryRow(ctx, `SELECT shortfall_cursor FROM coordinator.discovery_progress WHERE binding_id=$1 FOR UPDATE`, bindingID).Scan(&cursor); err != nil {
		return errors.New("discovery_checkpoint_read_failed")
	}
	if !same(cursor, expected) {
		return domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.discovery_progress SET shortfall_cursor=NULL,scan_started_at=$2,scan_completed_at=NULL,updated_at=$2 WHERE binding_id=$1`, bindingID, now); err != nil {
		return errors.New("discovery_restart_write_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("discovery_restart_commit_failed")
	}
	return nil
}

func (r *Repository) SaveShortfallPage(ctx context.Context, p DiscoveryPage) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, errors.New("discovery_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cursor *string
	if err := tx.QueryRow(ctx, `SELECT shortfall_cursor FROM coordinator.discovery_progress WHERE binding_id=$1 FOR UPDATE`, p.BindingID).Scan(&cursor); err != nil {
		return 0, errors.New("discovery_checkpoint_read_failed")
	}
	if !same(cursor, p.ExpectedCursor) {
		return 0, domain.ErrConflict
	}
	created := 0
	for _, c := range p.Cases {
		expectedCaseID, identityErr := domain.StableCaseID(p.BindingID, domain.CaseShortfall, c.DetectionID)
		if identityErr != nil || c.CaseID != expectedCaseID || c.CreatedAt.IsZero() {
			return 0, errors.New("discovery_case_identity_missing")
		}
		if c.CustomerID != nil && strings.TrimSpace(*c.CustomerID) != "" && (c.AccountRef == nil || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID)) {
			return 0, domain.ErrConflict
		}
		tag, err := tx.Exec(ctx, `INSERT INTO coordinator.cases(id,binding_id,kind,detection_id,customer_id,account_ref,raw_account_id,status,next_stage,held_reason,created_at) VALUES($1,$2,'SHORTFALL',$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, c.CaseID, p.BindingID, c.DetectionID, c.CustomerID, c.AccountRef, c.RawAccountID, c.Status, c.Stage, c.HeldReason, c.CreatedAt)
		if err != nil {
			return 0, errors.New("discovery_case_insert_failed")
		}
		if tag.RowsAffected() == 1 {
			created++
			continue
		}
		var customer, account, rawAccount *string
		if err := tx.QueryRow(ctx, `SELECT customer_id,account_ref,raw_account_id FROM coordinator.cases WHERE binding_id=$1 AND kind='SHORTFALL' AND detection_id=$2 FOR UPDATE`, p.BindingID, c.DetectionID).Scan(&customer, &account, &rawAccount); err != nil {
			return 0, domain.ErrConflict
		}
		if !same(customer, c.CustomerID) || !same(account, c.AccountRef) || !same(rawAccount, c.RawAccountID) {
			return 0, domain.ErrConflict
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.discovery_progress SET shortfall_cursor=$2::text,scan_started_at=CASE WHEN $2::text IS NULL THEN NULL ELSE COALESCE(scan_started_at,$3::timestamptz) END,scan_completed_at=CASE WHEN $2::text IS NULL THEN $4::timestamptz ELSE NULL END,last_page_succeeded_at=$4::timestamptz,shortfall_failed_attempts=0,shortfall_retry_at=NULL,shortfall_hold_reason=NULL,greatest_created_at=CASE WHEN $5::timestamptz IS NULL THEN greatest_created_at WHEN greatest_created_at IS NULL OR $5::timestamptz>greatest_created_at THEN $5::timestamptz ELSE greatest_created_at END,greatest_detection_id=CASE WHEN $5::timestamptz IS NOT NULL AND (greatest_created_at IS NULL OR $5::timestamptz>greatest_created_at) THEN $6::text ELSE greatest_detection_id END,updated_at=$4::timestamptz WHERE binding_id=$1`, p.BindingID, p.NextCursor, p.StartedAt, p.SucceededAt, p.GreatestCreatedAt, p.GreatestDetectionID); err != nil {
		return 0, errors.New("discovery_checkpoint_write_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, errors.New("discovery_commit_failed")
	}
	return created, nil
}

type CollectionProgress struct {
	PageNumber int
	CursorOut  *string
	Items      []byte
}

func (r *Repository) ReadCollectionProgress(ctx context.Context, caseID string, version int64) (CollectionProgress, error) {
	var p CollectionProgress
	err := r.pool.QueryRow(ctx, `SELECT page_number,cursor_out,items FROM coordinator.collection_refresh_progress WHERE case_id=$1 AND bridge_version=$2 ORDER BY page_number DESC LIMIT 1`, caseID, version).Scan(&p.PageNumber, &p.CursorOut, &p.Items)
	if errors.Is(err, pgx.ErrNoRows) {
		return CollectionProgress{}, nil
	}
	if err != nil {
		return CollectionProgress{}, errors.New("collection_progress_read_failed")
	}
	return p, nil
}
func (r *Repository) SaveCollectionProgress(ctx context.Context, caseID, token string, version int64, page int, cursorIn, cursorOut *string, items []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("collection_progress_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO coordinator.collection_refresh_progress(case_id,bridge_version,page_number,cursor_in,cursor_out,items) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(case_id,bridge_version,page_number) DO NOTHING`, caseID, version, page, cursorIn, cursorOut, items)
	if err != nil {
		return errors.New("collection_progress_write_failed")
	}
	if tag.RowsAffected() == 0 {
		var storedIn, storedOut *string
		var storedItems []byte
		if err := tx.QueryRow(ctx, `SELECT cursor_in,cursor_out,items FROM coordinator.collection_refresh_progress WHERE case_id=$1 AND bridge_version=$2 AND page_number=$3 FOR UPDATE`, caseID, version, page).Scan(&storedIn, &storedOut, &storedItems); err != nil {
			return domain.ErrConflict
		}
		if !same(storedIn, cursorIn) || !same(storedOut, cursorOut) || !bytesEqualJSON(storedItems, items) {
			return domain.ErrConflict
		}
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("collection_progress_commit_failed")
	}
	return nil
}

func bytesEqualJSON(a, b []byte) bool {
	var left, right any
	leftDecoder := json.NewDecoder(bytes.NewReader(a))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(b))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&left) != nil || rightDecoder.Decode(&right) != nil {
		return string(a) == string(b)
	}
	la, err1 := json.Marshal(left)
	rb, err2 := json.Marshal(right)
	return err1 == nil && err2 == nil && string(la) == string(rb)
}

func (r *Repository) SaveServicingPage(ctx context.Context, bindingID, mode string, expected, next uint64, changes []ServicingChange) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("servicing_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentMode string
	var current uint64
	if err := tx.QueryRow(ctx, `SELECT b.mode,d.servicing_sequence FROM coordinator.source_bindings b JOIN coordinator.discovery_progress d ON d.binding_id=b.id WHERE b.id=$1 FOR UPDATE OF b,d`, bindingID).Scan(&currentMode, &current); err != nil {
		return errors.New("servicing_checkpoint_read_failed")
	}
	if currentMode != mode {
		return errors.New("servicing_mode_mismatch")
	}
	if current != expected {
		return domain.ErrConflict
	}
	previous := expected
	for _, c := range changes {
		if c.Sequence != previous+1 || c.Sequence > next || c.Mode != mode {
			return errors.New("servicing_page_invalid")
		}
		previous = c.Sequence
		if c.ResourceType != "OFFER" && c.ResourceType != "BRIDGE" && c.ResourceType != "OPERATION" {
			return errors.New("servicing_resource_type_invalid")
		}
		if (c.BridgeID == nil) != (c.BridgeVersion == nil) {
			return errors.New("servicing_parent_version_invalid")
		}
		if c.ResourceType != "OFFER" && c.BridgeID == nil {
			return errors.New("servicing_parent_bridge_required")
		}
		if c.ResourceType == "BRIDGE" && (*c.BridgeID != c.ResourceID || *c.BridgeVersion != c.ResourceVersion) {
			return errors.New("servicing_bridge_version_invalid")
		}
		if c.BridgeID != nil && (c.CustomerID == nil || c.AccountRef == nil) {
			return errors.New("servicing_scope_missing")
		}
		if c.CustomerID != nil && c.AccountRef != nil && *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
			return domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO coordinator.servicing_changes(binding_id,sequence,mode,resource_type,resource_id,resource_version,bridge_id,bridge_version,change_type,recorded_at,summary,customer_id,account_ref,originating_case_id,evaluation_id,offer_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(binding_id,sequence) DO NOTHING`, bindingID, c.Sequence, c.Mode, c.ResourceType, c.ResourceID, c.ResourceVersion, c.BridgeID, c.BridgeVersion, c.ChangeType, c.RecordedAt, c.Summary, c.CustomerID, c.AccountRef, c.OriginatingCaseID, c.EvaluationID, c.OfferID); err != nil {
			return errors.New("servicing_change_insert_failed")
		}
		if c.BridgeID != nil {
			if err := upsertCollectionCaseTx(ctx, tx, bindingID, c); err != nil {
				return err
			}
		} else if c.OriginatingCaseID != nil {
			if err := refreshOriginCaseTx(ctx, tx, bindingID, c, true); err != nil {
				return err
			}
		}
	}
	if previous != next || (len(changes) == 0 && next != expected) {
		return errors.New("servicing_checkpoint_mismatch")
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.discovery_progress SET servicing_sequence=$2,servicing_failed_attempts=0,servicing_retry_at=NULL,servicing_hold_reason=NULL,servicing_last_page_at=clock_timestamp(),updated_at=clock_timestamp() WHERE binding_id=$1`, bindingID, next); err != nil {
		return errors.New("servicing_checkpoint_write_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("servicing_commit_failed")
	}
	return nil
}

func refreshOriginCaseTx(ctx context.Context, tx pgx.Tx, bindingID string, c ServicingChange, requireOffer bool) error {
	if c.CustomerID == nil || c.AccountRef == nil || c.OriginatingCaseID == nil || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
		return errors.New("servicing_origin_scope_missing")
	}
	if requireOffer && (c.EvaluationID == nil || *c.EvaluationID == "" || c.OfferID == nil || *c.OfferID == "") {
		return domain.ErrConflict
	}
	if c.ResourceType == "OFFER" && (c.OfferID == nil || *c.OfferID != c.ResourceID) {
		return domain.ErrConflict
	}
	var kind, customer, account, rawAccount, mode string
	err := tx.QueryRow(ctx, `SELECT c.kind,c.customer_id,c.account_ref,c.raw_account_id,b.mode FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id WHERE c.id=$1 AND c.binding_id=$2 FOR UPDATE OF c`, *c.OriginatingCaseID, bindingID).Scan(&kind, &customer, &account, &rawAccount, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return errors.New("servicing_origin_case_read_failed")
	}
	if kind != "SHORTFALL" || customer != *c.CustomerID || account != *c.AccountRef || mode != c.Mode || rawAccount == "" {
		return domain.ErrConflict
	}
	if c.EvaluationID != nil && *c.EvaluationID != "" {
		var savedID *string
		var response []byte
		err = tx.QueryRow(ctx, `SELECT saved_assessment_id,response_body FROM coordinator.assessments WHERE case_id=$1 ORDER BY revision DESC LIMIT 1`, *c.OriginatingCaseID).Scan(&savedID, &response)
		if err != nil {
			return errors.New("servicing_origin_assessment_read_failed")
		}
		if savedID == nil || *savedID != *c.EvaluationID || len(response) == 0 {
			return domain.ErrConflict
		}
		var assessment struct {
			ID         string `json:"id"`
			CustomerID string `json:"customer_id"`
			AccountID  string `json:"account_id"`
			SourceMode string `json:"source_mode"`
		}
		if json.Unmarshal(response, &assessment) != nil || assessment.ID != *savedID || assessment.CustomerID != customer || assessment.AccountID != rawAccount || assessment.SourceMode != mode {
			return domain.ErrConflict
		}
	} else if c.OfferID != nil && *c.OfferID != "" {
		return domain.ErrConflict
	}
	if c.OfferID != nil && *c.OfferID != "" {
		if c.EvaluationID == nil || *c.EvaluationID == "" {
			return domain.ErrConflict
		}
		var offerLinked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM coordinator.servicing_changes WHERE binding_id=$1 AND resource_type='OFFER' AND originating_case_id=$2 AND evaluation_id=$3 AND offer_id=$4 AND customer_id=$5 AND account_ref=$6)`, bindingID, *c.OriginatingCaseID, *c.EvaluationID, *c.OfferID, customer, account).Scan(&offerLinked); err != nil {
			return errors.New("servicing_origin_offer_read_failed")
		}
		if !offerLinked {
			return domain.ErrConflict
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE coordinator.cases SET last_change_sequence=$2,updated_at=clock_timestamp() WHERE id=$1 AND binding_id=$3 AND kind='SHORTFALL' AND customer_id=$4 AND account_ref=$5 AND raw_account_id=$6`, *c.OriginatingCaseID, c.Sequence, bindingID, *c.CustomerID, *c.AccountRef, rawAccount)
	if err != nil {
		return errors.New("servicing_origin_case_update_failed")
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func upsertCollectionCaseTx(ctx context.Context, tx pgx.Tx, bindingID string, c ServicingChange) error {
	var id string
	var customer, account *string
	err := tx.QueryRow(ctx, `SELECT id,customer_id,account_ref FROM coordinator.cases WHERE binding_id=$1 AND kind='COLLECTION' AND bridge_id=$2 FOR UPDATE`, bindingID, *c.BridgeID).Scan(&id, &customer, &account)
	if errors.Is(err, pgx.ErrNoRows) {
		var origin any
		if c.OriginatingCaseID != nil {
			if err := refreshOriginCaseTx(ctx, tx, bindingID, c, true); err != nil {
				return err
			}
			origin = *c.OriginatingCaseID
		}
		caseID, err := domain.StableCaseID(bindingID, domain.CaseCollection, *c.BridgeID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO coordinator.cases(id,binding_id,kind,bridge_id,customer_id,account_ref,status,next_stage,refresh_needed,target_bridge_version,last_change_sequence,originating_case_id) VALUES($1,$2,'COLLECTION',$3,$4,$5,'MONITORING','REFRESH_BRIDGE',true,$6,$7,$8)`, caseID, bindingID, *c.BridgeID, *c.CustomerID, *c.AccountRef, *c.BridgeVersion, c.Sequence, origin)
		if err != nil {
			return errors.New("collection_case_create_failed")
		}
		return nil
	}
	if err != nil {
		return errors.New("collection_case_read_failed")
	}
	if !same(customer, c.CustomerID) || !same(account, c.AccountRef) {
		return domain.ErrConflict
	}
	if c.OriginatingCaseID != nil {
		var originID *string
		if err := tx.QueryRow(ctx, `SELECT originating_case_id::text FROM coordinator.cases WHERE id=$1 AND binding_id=$2 FOR UPDATE`, id, bindingID).Scan(&originID); err != nil {
			return errors.New("collection_origin_read_failed")
		}
		if originID == nil || *originID != *c.OriginatingCaseID {
			return domain.ErrConflict
		}
		if err := refreshOriginCaseTx(ctx, tx, bindingID, c, c.ResourceType != "OPERATION"); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE coordinator.cases SET status=CASE WHEN status='CLOSED' AND $2>COALESCE(bridge_version,-1) THEN 'MONITORING' ELSE status END,next_stage=CASE WHEN $2>COALESCE(bridge_version,-1) OR COALESCE(target_bridge_version,0)>COALESCE(bridge_version,-1) THEN 'REFRESH_BRIDGE' ELSE next_stage END,refresh_needed=refresh_needed OR $2>COALESCE(bridge_version,-1) OR COALESCE(target_bridge_version,0)>COALESCE(bridge_version,-1),target_bridge_version=GREATEST(COALESCE(target_bridge_version,0),$2),last_change_sequence=$3,updated_at=clock_timestamp() WHERE id=$1`, id, *c.BridgeVersion, c.Sequence)
	if err != nil {
		return errors.New("servicing_refresh_mark_failed")
	}
	return nil
}
