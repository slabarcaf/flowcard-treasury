package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
)

// OperationLeaseProof fences worker-owned cancellation to one live QUEUED claim.
type OperationLeaseProof struct {
	State          domain.OperationState
	Version        int64
	Token          string
	LeaseExpiresAt time.Time
	FundingCheckID string
}

// HeldUnsentCancellationProof identifies a held operation whose latest journal
// evidence proves that no provider send began.
type HeldUnsentCancellationProof struct {
	Candidate      OperationCandidate
	FundingCheckID string
}

// CancelUnsentFunding atomically releases a locally proven never-sent FUND reservation.
func (r *Repository) CancelUnsentFunding(ctx context.Context, mode domain.Mode, bridgeID, reason string, clock func() time.Time) error {
	if mode != domain.ModeDemo && mode != domain.ModeSandbox || bridgeID == "" || !validCancellationReason(reason) || clock == nil {
		return errors.New("funding_cancellation_request_invalid")
	}
	tx, err := r.BeginFinancial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := r.CancelUnsentFundingTx(ctx, tx, mode, bridgeID, reason, clock, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("funding_cancellation_commit_failed")
	}
	return nil
}

// CancelUnsentFundingTx releases a never-sent FUND reservation in the caller's transaction.
// A non-nil proof requires an unexpired matching QUEUED lease in the cancellation CAS.
func (r *Repository) CancelUnsentFundingTx(ctx context.Context, tx pgx.Tx, mode domain.Mode, bridgeID,
	reason string, clock func() time.Time, proof *OperationLeaseProof) error {
	return r.cancelUnsentFundingTx(ctx, tx, mode, bridgeID, reason, clock, proof, nil)
}

// CancelHeldUnsentFundingTx releases an expired HELD never-sent reservation
// using its exact discovered state and version. Possible-movement holds fail closed.
func (r *Repository) CancelHeldUnsentFundingTx(ctx context.Context, tx pgx.Tx,
	proof HeldUnsentCancellationProof, reason string, clock func() time.Time) error {
	candidate := proof.Candidate
	if candidate.State != domain.OperationHeld || candidate.Kind != "FUND" || candidate.Version < 1 ||
		candidate.OperationID == "" || candidate.BridgeID == "" || candidate.FacilityID == "" || candidate.CustomerID == "" {
		return errors.New("funding_held_cancellation_proof_invalid")
	}
	return r.cancelUnsentFundingTx(ctx, tx, candidate.Mode, candidate.BridgeID, reason, clock, nil, &proof)
}

// CancelHeldUnsentFunding commits expiry recovery for one discovered held-unsent candidate.
func (r *Repository) CancelHeldUnsentFunding(ctx context.Context,
	proof HeldUnsentCancellationProof, reason string, clock func() time.Time) error {
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if err := r.CancelHeldUnsentFundingTx(txCtx, tx, proof, reason, clock); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("funding_held_unsent_cancel_commit_failed")
	}
	return nil
}

func (r *Repository) cancelUnsentFundingTx(ctx context.Context, tx pgx.Tx, mode domain.Mode, bridgeID,
	reason string, clock func() time.Time, proof *OperationLeaseProof, heldProof *HeldUnsentCancellationProof) error {
	if tx == nil || mode != domain.ModeDemo && mode != domain.ModeSandbox || bridgeID == "" ||
		!validCancellationReason(reason) || clock == nil ||
		(proof != nil && (proof.State != domain.OperationQueued || proof.Version < 1 || !validLeaseToken(proof.Token) || proof.LeaseExpiresAt.IsZero())) ||
		(heldProof != nil && (proof != nil || reason != "HELD_UNSENT_EXPIRED")) {
		return errors.New("funding_cancellation_request_invalid")
	}
	var facilityID, customerID string
	if err := tx.QueryRow(ctx, `SELECT facility_id,customer_id FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(mode), bridgeID).Scan(&facilityID, &customerID); err != nil {
		return errors.New("funding_bridge_unavailable")
	}
	var lockedFacility, facilityAccount string
	if err := tx.QueryRow(ctx, `SELECT id,funding_account_ref FROM servicing.facilities WHERE mode=$1 AND id=$2 FOR UPDATE`, string(mode), facilityID).Scan(&lockedFacility, &facilityAccount); err != nil {
		return errors.New("funding_facility_lock_failed")
	}
	var lockedCustomer string
	if err := tx.QueryRow(ctx, `SELECT id FROM servicing.customers WHERE mode=$1 AND id=$2 FOR UPDATE`, string(mode), customerID).Scan(&lockedCustomer); err != nil {
		return errors.New("funding_customer_lock_failed")
	}
	var reservationID, offerID, state, lifecycle string
	var everFunded bool
	var facilityPrincipal, customerPrincipal, customerFee int64
	var bridgeVersion, offerVersion int64
	var termsHash, evaluationID, accountID, transactionID, caseID string
	var originalPrincipal, originalFee int64
	var principalOwed, contractFeeOwed, lateFeeOwed int64
	var offerExpires, reservationExpires time.Time
	var offeredOn time.Time
	err := tx.QueryRow(ctx, `SELECT r.id::text,o.id::text,r.state,b.lifecycle,b.ever_funded,
		r.facility_principal_cents,r.customer_principal_cents,r.customer_fee_cents,b.version,o.version,
		b.terms_hash,b.evaluation_id,b.canonical_account_id,b.transaction_id,
		b.principal_original_cents,b.contract_fee_original_cents,
		b.principal_owed_cents,b.contract_fee_owed_cents,b.late_fee_owed_cents,
		COALESCE(b.originating_case_id,''),o.expires_at,o.offered_on,r.expires_at
		FROM servicing.bridges b JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id
		JOIN servicing.reservations r ON r.mode=b.mode AND r.bridge_id=b.id
		WHERE b.mode=$1 AND b.id=$2 FOR UPDATE OF b,o,r`, string(mode), bridgeID).Scan(
		&reservationID, &offerID, &state, &lifecycle, &everFunded, &facilityPrincipal,
		&customerPrincipal, &customerFee, &bridgeVersion, &offerVersion,
		&termsHash, &evaluationID, &accountID, &transactionID, &originalPrincipal, &originalFee, &principalOwed,
		&contractFeeOwed, &lateFeeOwed, &caseID, &offerExpires, &offeredOn, &reservationExpires)
	if err != nil {
		return errors.New("funding_reservation_unavailable")
	}
	invoiceRows, err := tx.Query(ctx, `SELECT r.source_system,r.invoice_id FROM servicing.receivables r
		JOIN servicing.bridge_invoice_allocations a ON a.mode=r.mode AND a.source_system=r.source_system AND a.invoice_id=r.invoice_id
		WHERE a.mode=$1 AND a.bridge_id=$2 ORDER BY r.source_system,r.invoice_id FOR UPDATE OF r`, string(mode), bridgeID)
	if err != nil {
		return errors.New("funding_invoice_lock_failed")
	}
	for invoiceRows.Next() {
		var sourceSystem, invoiceID string
		if invoiceRows.Scan(&sourceSystem, &invoiceID) != nil {
			invoiceRows.Close()
			return errors.New("funding_invoice_lock_failed")
		}
	}
	if invoiceRows.Err() != nil {
		invoiceRows.Close()
		return errors.New("funding_invoice_lock_failed")
	}
	invoiceRows.Close()
	rows, err := tx.Query(ctx, `SELECT a.source_system,a.invoice_id,a.active_cents
		FROM servicing.bridge_invoice_allocations a WHERE a.mode=$1 AND a.bridge_id=$2
		ORDER BY a.source_system,a.invoice_id FOR UPDATE`, string(mode), bridgeID)
	if err != nil {
		return errors.New("funding_invoice_allocation_read_failed")
	}
	type allocation struct {
		system, invoice string
		amount          int64
	}
	var allocations []allocation
	var invoiceTotal int64
	for rows.Next() {
		var value allocation
		if rows.Scan(&value.system, &value.invoice, &value.amount) != nil || value.amount < 0 || invoiceTotal > int64(^uint64(0)>>1)-value.amount {
			rows.Close()
			return errors.New("funding_invoice_allocation_read_failed")
		}
		if value.amount > 0 {
			allocations = append(allocations, value)
			invoiceTotal += value.amount
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return errors.New("funding_invoice_allocation_read_failed")
	}
	rows.Close()
	var operationID, operationState, requestID, sourceAccount, destinationAccount, operationTermsHash string
	var operationVersion int64
	var amount int64
	var providerObject *string
	var reservedProviderFee *int64
	var leaseToken *string
	var leaseExpiresAt *time.Time
	var lookupRequestedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,state,version,request_id,source_account_ref,destination_account_ref,
		amount_cents,provider_object_id,reserved_provider_fee_cents,terms_hash,lease_token::text,lease_expires_at,lookup_requested_at
		FROM servicing.operations WHERE mode=$1 AND bridge_id=$2 AND kind='FUND' FOR UPDATE`,
		string(mode), bridgeID).Scan(&operationID, &operationState, &operationVersion, &requestID,
		&sourceAccount, &destinationAccount, &amount, &providerObject, &reservedProviderFee, &operationTermsHash,
		&leaseToken, &leaseExpiresAt, &lookupRequestedAt)
	if err != nil {
		return errors.New("funding_operation_unavailable")
	}
	var fundingCheckID string
	var fundingCheckRefs []byte
	checkID := cancellationFundingCheckID(proof, heldProof)
	if checkID != "" {
		if err := tx.QueryRow(ctx, `SELECT id::text,source_refs FROM servicing.funding_checks
			WHERE mode=$1 AND id=$2 AND offer_id=$3`, string(mode), checkID, offerID).Scan(&fundingCheckID, &fundingCheckRefs); err != nil {
			return errors.New("funding_check_unavailable")
		}
	} else if err := tx.QueryRow(ctx, `SELECT id::text,source_refs FROM servicing.funding_checks
		WHERE mode=$1 AND offer_id=$2 AND passed
		ORDER BY created_at DESC,id DESC LIMIT 1`, string(mode), offerID).Scan(&fundingCheckID, &fundingCheckRefs); err != nil {
		return errors.New("funding_check_unavailable")
	}
	var observationExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.provider_observations
		WHERE mode=$1 AND operation_id=$2 AND observation_kind='TRANSFER')`, string(mode), operationID).Scan(&observationExists); err != nil {
		return errors.New("funding_observation_lookup_failed")
	}
	now := clock().UTC()
	if now.IsZero() {
		return errors.New("funding_clock_invalid")
	}
	if heldProof != nil {
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return errors.New("funding_held_expiry_clock_unavailable")
		}
		now = now.UTC()
	}
	expectedOperationState := "QUEUED"
	if heldProof != nil {
		expectedOperationState = "HELD"
		candidate := heldProof.Candidate
		if candidate.Mode != mode || candidate.OperationID != operationID || candidate.BridgeID != bridgeID ||
			candidate.FacilityID != facilityID || candidate.CustomerID != customerID || candidate.Kind != "FUND" ||
			candidate.Version != operationVersion || lookupRequestedAt != nil {
			return ErrOperationClaimStale
		}
		var latestMovementCertainty *string
		if err := tx.QueryRow(ctx, `SELECT evidence_ref->>'movement_certainty' FROM servicing.operation_journal
			WHERE mode=$1 AND operation_id=$2 ORDER BY sequence DESC LIMIT 1`, string(mode), operationID).Scan(&latestMovementCertainty); err != nil ||
			latestMovementCertainty == nil || *latestMovementCertainty != "PROVEN_UNSENT" {
			return errors.New("funding_held_operation_not_proven_unsent")
		}
		if leaseToken != nil && (leaseExpiresAt == nil || leaseExpiresAt.After(now)) {
			return ErrOperationClaimStale
		}
		expired := !now.Before(offerExpires) || !now.Before(reservationExpires) || offeredOn.Format("2006-01-02") != now.Format("2006-01-02")
		if !expired {
			return errors.New("funding_held_unsent_not_expired")
		}
	}
	if state != "NEVER_SENT" || operationState != expectedOperationState || lifecycle != "RESERVED" || everFunded ||
		providerObject != nil || observationExists || principalOwed != 0 || contractFeeOwed != 0 || lateFeeOwed != 0 ||
		requestID != "bridge-"+bridgeID || sourceAccount != facilityAccount || destinationAccount != accountID || amount != facilityPrincipal ||
		amount != originalPrincipal || originalFee != customerFee || operationTermsHash != termsHash ||
		facilityPrincipal != customerPrincipal || reservedProviderFee == nil || *reservedProviderFee < 0 {
		return errors.New("funding_movement_not_proven_unsent")
	}
	if heldProof != nil {
		if reason != "HELD_UNSENT_EXPIRED" {
			return errors.New("funding_held_unsent_reason_invalid")
		}
	} else if proof == nil {
		if leaseToken != nil && (leaseExpiresAt == nil || leaseExpiresAt.After(now)) {
			return ErrOperationClaimStale
		}
	} else if proof.Version != operationVersion || leaseToken == nil || leaseExpiresAt == nil ||
		*leaseToken != proof.Token || !leaseExpiresAt.Equal(proof.LeaseExpiresAt) || !leaseExpiresAt.After(now) {
		return ErrOperationClaimStale
	}
	if reason == "OFFER_EXPIRED" && now.Before(offerExpires) {
		return errors.New("funding_offer_not_expired")
	}
	if reason == "RESERVATION_EXPIRED" && now.Before(reservationExpires) {
		return errors.New("funding_reservation_not_expired")
	}
	if reason == "MIDNIGHT_SUPERSEDED" && offeredOn.Format("2006-01-02") == now.UTC().Format("2006-01-02") {
		return errors.New("funding_offer_date_unchanged")
	}
	invoiceCoverage, ok := checkedFundingAdd(customerPrincipal, customerFee)
	if !ok || invoiceTotal != invoiceCoverage {
		return errors.New("funding_invoice_allocation_mismatch")
	}
	for _, allocation := range allocations {
		tag, err := tx.Exec(ctx, `UPDATE servicing.receivables SET reserved_cents=reserved_cents-$4,version=version+1
			WHERE mode=$1 AND source_system=$2 AND invoice_id=$3 AND reserved_cents >= $4`,
			string(mode), allocation.system, allocation.invoice, allocation.amount)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.New("funding_invoice_release_failed")
		}
		tag, err = tx.Exec(ctx, `UPDATE servicing.bridge_invoice_allocations SET active_cents=0
			WHERE mode=$1 AND bridge_id=$2 AND source_system=$3 AND invoice_id=$4 AND active_cents=$5`,
			string(mode), bridgeID, allocation.system, allocation.invoice, allocation.amount)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.New("funding_invoice_allocation_release_failed")
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE servicing.facilities SET reserved_principal_cents=reserved_principal_cents-$3,
		version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2 AND reserved_principal_cents >= $3`,
		string(mode), facilityID, facilityPrincipal)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_facility_release_failed")
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.customers SET reserved_principal_cents=reserved_principal_cents-$3,
		reserved_fee_cents=reserved_fee_cents-$4,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND reserved_principal_cents >= $3 AND reserved_fee_cents >= $4`,
		string(mode), customerID, customerPrincipal, customerFee)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_customer_release_failed")
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.reservations SET state='RELEASED',updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state='NEVER_SENT'`, string(mode), reservationID)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_reservation_release_failed")
	}
	var operationTag pgx.Row
	if heldProof != nil {
		operationTag = tx.QueryRow(ctx, `UPDATE servicing.operations SET state='CANCELLED_UNSENT',version=version+1,
			lease_token=NULL,lease_expires_at=NULL,sanitized_error_code=$3,lookup_requested_at=NULL,
			lookup_requested_by=NULL,last_funding_check_id=COALESCE($5,last_funding_check_id),updated_at=clock_timestamp()
			WHERE mode=$1 AND id=$2 AND state='HELD' AND version=$4 AND lookup_requested_at IS NULL
			AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING version`,
			string(mode), operationID, reason, heldProof.Candidate.Version, nullableString(heldProof.FundingCheckID))
	} else if proof == nil {
		operationTag = tx.QueryRow(ctx, `UPDATE servicing.operations SET state='CANCELLED_UNSENT',version=version+1,
			lease_token=NULL,lease_expires_at=NULL,sanitized_error_code=$3,updated_at=clock_timestamp()
			WHERE mode=$1 AND id=$2 AND state='QUEUED' AND version=$4
			AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING version`,
			string(mode), operationID, reason, operationVersion)
	} else {
		operationTag = tx.QueryRow(ctx, `UPDATE servicing.operations SET state='CANCELLED_UNSENT',version=version+1,
			lease_token=NULL,lease_expires_at=NULL,sanitized_error_code=$3,
			last_funding_check_id=COALESCE($6,last_funding_check_id),updated_at=clock_timestamp()
			WHERE mode=$1 AND id=$2 AND state='QUEUED' AND version=$4 AND lease_token=$5::uuid
			AND lease_expires_at>clock_timestamp() RETURNING version`,
			string(mode), operationID, reason, proof.Version, proof.Token, nullableString(proof.FundingCheckID))
	}
	var cancelledOperationVersion int64
	err = operationTag.Scan(&cancelledOperationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOperationClaimStale
	}
	if err != nil {
		return errors.New("funding_operation_cancel_failed")
	}
	if err := AppendOperationJournal(ctx, tx, mode, operationID, "STATE_TRANSITION", operationState,
		"CANCELLED_UNSENT", reason, leaseToken, leaseExpiresAt, nil,
		map[string]any{"movement_certainty": "PROVEN_UNSENT", "cancellation_reason": reason,
			"funding_check_id": cancellationFundingCheckID(proof, heldProof)}); err != nil {
		return err
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.bridges SET lifecycle='VOID_UNFUNDED',version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND lifecycle='RESERVED' AND NOT ever_funded`, string(mode), bridgeID)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_bridge_void_failed")
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.offers SET state='TERMINATED',version=version+1
		WHERE mode=$1 AND id=$2 AND state='CONSUMED'`, string(mode), offerID)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_offer_terminate_failed")
	}
	newBridgeVersion := bridgeVersion + 1
	newOperationVersion := cancelledOperationVersion
	newOfferVersion := offerVersion + 1
	sourceHold, ok := checkedFundingAdd(amount, *reservedProviderFee)
	if !ok {
		return errors.New("funding_source_liability_overflow")
	}
	provenance, _ := json.Marshal(map[string]any{"reason_code": reason, "reservation_id": reservationID,
		"operation_id": operationID, "offer_id": offerID, "terms_hash": termsHash,
		"source_account_ref": sourceAccount, "destination_account_ref": destinationAccount})
	bridgeRef, operationRef := bridgeID, operationID
	lines := []domain.LedgerLine{{Account: "COMMITMENT_MEMO", Debit: domain.Money(sourceHold)},
		{Account: "COMMITMENT_MEMO", Credit: domain.Money(sourceHold)}}
	if err := InsertLedgerTransaction(ctx, tx, mode, "fund-reservation-release:"+reservationID,
		"FUNDING_RESERVATION_RELEASE", &bridgeRef, &operationRef, now.UTC().Format(time.RFC3339Nano), provenance, lines); err != nil {
		return err
	}
	var caseRef any
	if caseID != "" {
		caseRef = caseID
	}
	changeFields := map[string]any{"customer_id": customerID, "account_ref": "customer:" + customerID,
		"originating_case_id": caseRef, "evaluation_id": evaluationID, "offer_id": offerID,
		"bridge_id": bridgeID, "bridge_version": newBridgeVersion, "operation_id": operationID,
		"reservation_id": reservationID, "funding_check_id": fundingCheckID,
		"evidence_refs": json.RawMessage(fundingCheckRefs),
		"reason_code":   reason, "terms_hash": termsHash}
	bridgeSummary := fundingChangeBody(changeFields, "VOID_UNFUNDED")
	operationSummary := fundingChangeBody(changeFields, "CANCELLED_UNSENT")
	offerSummary := fundingChangeBody(changeFields, "TERMINATED")
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: mode, ResourceType: "BRIDGE", ResourceID: bridgeID,
		ResourceVersion: newBridgeVersion, ChangeType: "VOID_UNFUNDED", Summary: bridgeSummary}, &bridgeRef, &newBridgeVersion); err != nil {
		return err
	}
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: mode, ResourceType: "OPERATION", ResourceID: operationID,
		ResourceVersion: newOperationVersion, ChangeType: "CANCELLED_UNSENT", Summary: operationSummary}, &bridgeRef, &newBridgeVersion); err != nil {
		return err
	}
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: mode, ResourceType: "OFFER", ResourceID: offerID,
		ResourceVersion: newOfferVersion, ChangeType: "OFFER_TERMINATED", Summary: offerSummary}, &bridgeRef, &newBridgeVersion); err != nil {
		return err
	}
	return nil
}

func fundingChangeBody(base map[string]any, status string) []byte {
	fields := make(map[string]any, len(base)+1)
	for key, value := range base {
		fields[key] = value
	}
	fields["status"] = status
	body, _ := json.Marshal(fields)
	return body
}

func validCancellationReason(reason string) bool {
	switch reason {
	case "OFFER_EXPIRED", "MIDNIGHT_SUPERSEDED", "GUARD_CHANGED", "POLICY_RECHECK_FAILED",
		"MAPPING_CHANGED", "AUTHORITY_CHANGED", "SOURCE_LIQUIDITY_CHANGED", "RESERVATION_EXPIRED",
		"HELD_UNSENT_EXPIRED":
		return true
	default:
		return false
	}
}

func cancellationFundingCheckID(queued *OperationLeaseProof, held *HeldUnsentCancellationProof) string {
	if queued != nil {
		return queued.FundingCheckID
	}
	if held != nil {
		return held.FundingCheckID
	}
	return ""
}
