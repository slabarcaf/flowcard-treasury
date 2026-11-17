package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
)

const maxServicingMaintenanceBatch = 25

// ErrServicingPolicyUnavailable means saved terms cannot be serviced with the current pinned policy.
var ErrServicingPolicyUnavailable = errors.New("servicing_original_policy_unavailable")

// LockFacilityForCollectionReview uses the established facility/customer/bridge order.
func LockFacilityForCollectionReview(ctx context.Context, tx pgx.Tx, mode, facilityID, customerID, bridgeID string) error {
	if _, err := lockFundingFacility(ctx, tx, mode, facilityID); err != nil {
		return err
	}
	if _, err := lockFundingCustomer(ctx, tx, mode, customerID); err != nil {
		return err
	}
	ids, err := lockCustomerBridges(ctx, tx, mode, customerID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == bridgeID {
			return nil
		}
	}
	return ErrOperationClaimStale
}

// CollectionReviewCandidate identifies a durable, read-only servicing review.
type CollectionReviewCandidate struct {
	Mode       domain.Mode
	ID         string
	FacilityID string
	BridgeID   string
}

// DiscoverCollectionReviews returns a bounded queue page for the serial worker.
func (r *Repository) DiscoverCollectionReviews(ctx context.Context, mode domain.Mode, facilityID string, limit int) ([]CollectionReviewCandidate, error) {
	if (mode != domain.ModeDemo && mode != domain.ModeSandbox) || facilityID == "" || limit < 1 {
		return nil, errors.New("collection_review_discovery_invalid")
	}
	if limit > maxServicingMaintenanceBatch {
		limit = maxServicingMaintenanceBatch
	}
	rows, err := r.pool.Query(ctx, `SELECT q.mode,q.id::text,b.facility_id,q.bridge_id::text FROM servicing.collection_review_requests q JOIN servicing.bridges b ON b.mode=q.mode AND b.id=q.bridge_id WHERE q.mode=$1 AND b.facility_id=$2 AND q.state='QUEUED' ORDER BY q.requested_at,q.id LIMIT $3`, string(mode), facilityID, limit)
	if err != nil {
		return nil, errors.New("collection_review_discovery_failed")
	}
	defer rows.Close()
	result := []CollectionReviewCandidate{}
	for rows.Next() {
		var item CollectionReviewCandidate
		if err = rows.Scan(&item.Mode, &item.ID, &item.FacilityID, &item.BridgeID); err != nil {
			return nil, errors.New("collection_review_discovery_failed")
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("collection_review_discovery_failed")
	}
	return result, nil
}

// ProcessCollectionReview refreshes only funding-owned accrual/delinquency facts.
// Normal worker discovery may then perform its existing fresh read checks.
func (r *Repository) ProcessCollectionReview(ctx context.Context, candidate CollectionReviewCandidate, pricing domain.Pricing, policyHash string, clock func() time.Time) (bool, error) {
	if candidate.Mode != domain.ModeDemo && candidate.Mode != domain.ModeSandbox || candidate.ID == "" || candidate.FacilityID == "" || candidate.BridgeID == "" || clock == nil || pricing.Validate() != nil || !validHash(policyHash) {
		return false, errors.New("collection_review_candidate_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	var facilityID, customerID string
	if err = tx.QueryRow(txCtx, `SELECT facility_id,customer_id FROM servicing.bridges WHERE mode=$1 AND id=$2`, string(candidate.Mode), candidate.BridgeID).Scan(&facilityID, &customerID); errors.Is(err, pgx.ErrNoRows) {
		return false, ErrOperationClaimStale
	} else if err != nil {
		return false, errors.New("collection_review_bridge_read_failed")
	}
	if facilityID != candidate.FacilityID {
		return false, errors.New("collection_review_facility_mismatch")
	}
	if _, err = lockFundingFacility(txCtx, tx, string(candidate.Mode), facilityID); err != nil {
		return false, err
	}
	if _, err = lockFundingCustomer(txCtx, tx, string(candidate.Mode), customerID); err != nil {
		return false, err
	}
	bridgeIDs, err := lockCustomerBridges(txCtx, tx, string(candidate.Mode), customerID)
	if err != nil {
		return false, err
	}
	found := false
	for _, id := range bridgeIDs {
		if id == candidate.BridgeID {
			found = true
			break
		}
	}
	if !found {
		return false, ErrOperationClaimStale
	}
	var reason string
	err = tx.QueryRow(txCtx, `SELECT reason_code FROM servicing.collection_review_requests WHERE mode=$1 AND id=$2 AND bridge_id=$3 AND state='QUEUED' FOR UPDATE`, string(candidate.Mode), candidate.ID, candidate.BridgeID).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrOperationClaimStale
	}
	if err != nil {
		return false, errors.New("collection_review_request_lock_failed")
	}
	var everFunded bool
	var lifecycle string
	if err = tx.QueryRow(txCtx, `SELECT ever_funded,lifecycle FROM servicing.bridges WHERE mode=$1 AND id=$2`, string(candidate.Mode), candidate.BridgeID).Scan(&everFunded, &lifecycle); err != nil {
		return false, errors.New("collection_review_bridge_read_failed")
	}
	resultCode := "CHECKS_COMPLETE"
	if !everFunded || lifecycle == string(domain.VoidUnfunded) || lifecycle == string(domain.Repaid) {
		resultCode = "NO_ACTIVE_COLLECTION_DEBT"
	} else {
		accrual, accrualErr := AccrueThrough(txCtx, tx, AccrualOptions{Mode: candidate.Mode, Pricing: pricing, ExpectedPolicyHash: policyHash}, candidate.BridgeID, clock())
		if accrualErr != nil {
			if accrualErr.Error() == "accrual_original_pricing_unavailable" {
				resultCode = "REQUEST_OPERATOR_REVIEW"
			} else {
				return false, errors.New("collection_review_accrual_failed")
			}
		}
		if accrualErr == nil && accrual.CorrectionRequired {
			resultCode = "REQUEST_OPERATOR_REVIEW"
		} else if accrualErr == nil && (!accrual.Current || accrual.Pending) {
			resultCode = "WAIT_FOR_ACCRUAL"
		}
		if accrualErr == nil {
			if err = RefreshDelinquency(txCtx, tx, candidate.Mode, candidate.BridgeID, clock()); err != nil {
				return false, errors.New("collection_review_delinquency_failed")
			}
		}
	}
	if _, err = tx.Exec(txCtx, `UPDATE servicing.collection_review_requests SET state='COMPLETED',result_code=$3,processed_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND state='QUEUED'`, string(candidate.Mode), candidate.ID, resultCode); err != nil {
		return false, errors.New("collection_review_complete_failed")
	}
	var caseID, evaluationID, offerID, customer string
	var version int64
	var activeHold bool
	var holds []string
	if err = tx.QueryRow(txCtx, `SELECT b.version,b.customer_id,COALESCE(b.originating_case_id,''),b.evaluation_id,b.offer_id::text,b.hold_reasons,c.active_hold FROM servicing.bridges b JOIN servicing.customers c ON c.mode=b.mode AND c.id=b.customer_id WHERE b.mode=$1 AND b.id=$2`, string(candidate.Mode), candidate.BridgeID).Scan(&version, &customer, &caseID, &evaluationID, &offerID, &holds, &activeHold); err != nil {
		return false, errors.New("collection_review_result_read_failed")
	}
	if activeHold || len(holds) > 0 {
		if resultCode == "CHECKS_COMPLETE" {
			resultCode = "REQUEST_OPERATOR_REVIEW"
		}
		if _, err = tx.Exec(txCtx, `UPDATE servicing.collection_review_requests SET result_code=$3 WHERE mode=$1 AND id=$2`, string(candidate.Mode), candidate.ID, resultCode); err != nil {
			return false, errors.New("collection_review_result_update_failed")
		}
	}
	version++
	if _, err = tx.Exec(txCtx, `UPDATE servicing.bridges SET version=$3,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, string(candidate.Mode), candidate.BridgeID, version); err != nil {
		return false, errors.New("collection_review_bridge_update_failed")
	}
	var caseRef any
	if caseID != "" {
		caseRef = caseID
	}
	summary, _ := json.Marshal(map[string]any{"customer_id": customer, "account_ref": "customer:" + customer, "originating_case_id": caseRef, "evaluation_id": evaluationID, "offer_id": offerID, "bridge_id": candidate.BridgeID, "bridge_version": version, "status": resultCode, "reason_code": reason})
	parent, parentVersion := candidate.BridgeID, version
	if _, err = AppendChange(txCtx, tx, domain.Change{Mode: candidate.Mode, ResourceType: "BRIDGE", ResourceID: candidate.BridgeID, ResourceVersion: version, ChangeType: "COLLECTION_REVIEW_COMPLETED", Summary: summary}, &parent, &parentVersion); err != nil {
		return false, err
	}
	if err = tx.Commit(txCtx); err != nil {
		return false, errors.New("collection_review_commit_failed")
	}
	return true, nil
}

// ServicingBridgeCandidate is one funded bridge requiring UTC maintenance.
type ServicingBridgeCandidate struct {
	Mode                             domain.Mode
	FacilityID, CustomerID, BridgeID string
}

// DiscoverServicingBridges returns a bounded restart-safe accrual/delinquency page.
func (r *Repository) DiscoverServicingBridges(ctx context.Context, mode domain.Mode, facilityID string, asOf time.Time, limit int) ([]ServicingBridgeCandidate, error) {
	if (mode != domain.ModeDemo && mode != domain.ModeSandbox) || facilityID == "" || limit < 1 {
		return nil, errors.New("servicing_scan_request_invalid")
	}
	if limit > maxServicingMaintenanceBatch {
		limit = maxServicingMaintenanceBatch
	}
	day := asOf.UTC().Format("2006-01-02")
	rows, err := r.pool.Query(ctx, `SELECT b.mode,b.facility_id,b.customer_id,b.id::text FROM servicing.bridges b WHERE b.mode=$1 AND b.facility_id=$2 AND b.ever_funded AND b.lifecycle IN ('ACTIVE','COLLECTING','REPAID','REOPENED_HELD') AND NOT ('ACCRUAL_CORRECTION_REQUIRED'=ANY(b.hold_reasons)) AND (
		(b.lifecycle<>'REPAID' AND b.due_on<$3::date AND (b.last_accrued_on IS NULL OR b.last_accrued_on<$3::date)) OR
		(b.lifecycle='REPAID' AND b.closed_at IS NOT NULL AND b.closed_at::date>b.due_on AND LEAST(b.closed_at::date,$3::date)>b.due_on AND (b.last_accrued_on IS NULL OR b.last_accrued_on<LEAST(b.closed_at::date,$3::date))) OR
		b.delinquency_stage<>CASE WHEN b.principal_owed_cents=0 AND b.contract_fee_owed_cents=0 AND b.late_fee_owed_cents=0 THEN 'CURRENT' WHEN $3::date<=b.due_on THEN 'CURRENT' WHEN $3::date-b.due_on<=7 THEN 'WATCH' WHEN $3::date-b.due_on<=30 THEN 'DELINQUENT' WHEN $3::date-b.due_on<=60 THEN 'SERIOUS' ELSE 'DEFAULT' END OR
		(b.first_defaulted_on IS NULL AND $3::date>=b.due_on+61 AND (b.principal_owed_cents>0 OR b.contract_fee_owed_cents>0 OR b.late_fee_owed_cents>0))
	) ORDER BY b.due_on,b.created_at,b.id LIMIT $4`, string(mode), facilityID, day, limit)
	if err != nil {
		return nil, errors.New("servicing_scan_discovery_failed")
	}
	defer rows.Close()
	items := []ServicingBridgeCandidate{}
	for rows.Next() {
		var item ServicingBridgeCandidate
		if err = rows.Scan(&item.Mode, &item.FacilityID, &item.CustomerID, &item.BridgeID); err != nil {
			return nil, errors.New("servicing_scan_discovery_failed")
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("servicing_scan_discovery_failed")
	}
	return items, nil
}

// RefreshServicingBridge performs bounded funding-owned accrual and delinquency catch-up.
func (r *Repository) RefreshServicingBridge(ctx context.Context, candidate ServicingBridgeCandidate, pricing domain.Pricing, policyHash string, asOf time.Time) error {
	if candidate.Mode != domain.ModeDemo && candidate.Mode != domain.ModeSandbox || candidate.FacilityID == "" || candidate.CustomerID == "" || candidate.BridgeID == "" || pricing.Validate() != nil || !validHash(policyHash) {
		return errors.New("servicing_bridge_refresh_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if _, err = lockFundingFacility(txCtx, tx, string(candidate.Mode), candidate.FacilityID); err != nil {
		return err
	}
	if _, err = lockFundingCustomer(txCtx, tx, string(candidate.Mode), candidate.CustomerID); err != nil {
		return err
	}
	ids, err := lockCustomerBridges(txCtx, tx, string(candidate.Mode), candidate.CustomerID)
	if err != nil {
		return err
	}
	found := false
	for _, id := range ids {
		if id == candidate.BridgeID {
			found = true
			break
		}
	}
	if !found {
		return ErrOperationClaimStale
	}
	var facilityID, customerID string
	var everFunded bool
	var lifecycle string
	if err = tx.QueryRow(txCtx, `SELECT facility_id,customer_id,ever_funded,lifecycle FROM servicing.bridges WHERE mode=$1 AND id=$2`, string(candidate.Mode), candidate.BridgeID).Scan(&facilityID, &customerID, &everFunded, &lifecycle); err != nil {
		return errors.New("servicing_bridge_refresh_read_failed")
	}
	if facilityID != candidate.FacilityID || customerID != candidate.CustomerID || !everFunded || lifecycle == string(domain.VoidUnfunded) {
		return ErrOperationClaimStale
	}
	if _, err = AccrueThrough(txCtx, tx, AccrualOptions{Mode: candidate.Mode, Pricing: pricing, ExpectedPolicyHash: policyHash}, candidate.BridgeID, asOf); err != nil {
		if err.Error() == "accrual_original_pricing_unavailable" {
			return ErrServicingPolicyUnavailable
		}
		return errors.New("servicing_bridge_accrual_failed")
	}
	if err = RefreshDelinquency(txCtx, tx, candidate.Mode, candidate.BridgeID, asOf); err != nil {
		return errors.New("servicing_bridge_delinquency_failed")
	}
	if err = tx.Commit(txCtx); err != nil {
		return errors.New("servicing_bridge_refresh_commit_failed")
	}
	return nil
}
