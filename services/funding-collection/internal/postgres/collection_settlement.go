package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

// FinishCollectionOperation records reads and applies a verified settlement
// under the same operation lease fence and transaction.
func (r *Repository) FinishCollectionOperation(ctx context.Context, claim OperationClaim,
	outcome OperationReadOutcome, clock func() time.Time, pricing domain.Pricing,
	policyHash string) error {
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if err := FinishCollectionOperationTx(txCtx, tx, claim, outcome, clock, pricing, policyHash); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("collection_operation_finish_commit_failed")
	}
	return nil
}

// FinishCollectionOperationTx records a final GET and, when it proves SETTLED,
// books actual movement, debt, backing, fees and holds atomically.
func FinishCollectionOperationTx(ctx context.Context, tx pgx.Tx, claim OperationClaim,
	outcome OperationReadOutcome, clock func() time.Time, pricing domain.Pricing,
	policyHash string) error {
	if claim.Kind != "COLLECT_FEE" && claim.Kind != "COLLECT_PRINCIPAL" ||
		tx == nil || clock == nil || !validHash(policyHash) || pricing.Validate() != nil {
		return errors.New("collection_settlement_request_invalid")
	}
	var lockedCustomerBridges []string
	var err error
	if outcome.Source == "RETRIEVE" || outcome.Source == "LOOKUP" {
		lockedCustomerBridges, err = lockCollectionCustomerBridges(ctx, tx, claim.OperationCandidate)
		if err != nil {
			return err
		}
	}
	scope, err := lockOperationScope(ctx, tx, claim.OperationCandidate)
	if err != nil {
		return err
	}
	if scope.leaseToken == nil || scope.leaseExpiresAt == nil || *scope.leaseToken != claim.LeaseToken ||
		!scope.leaseExpiresAt.Equal(claim.LeaseExpiresAt) {
		return ErrOperationClaimStale
	}
	if outcome.Source != "RETRIEVE" && outcome.Source != "LOOKUP" {
		if err := FinishOperationTx(ctx, tx, claim, outcome); err != nil {
			return err
		}
		return syncCollectionAttemptState(ctx, tx, claim)
	}
	plan, planErr := planOperationOutcome(scope, outcome)
	if planErr == nil {
		applyBoundEvidenceConflicts(scope, outcome.Source, &plan)
	}
	now := clock().UTC()
	settles := planErr == nil && scope.guardCode == "" && plan.receipt != nil &&
		plan.providerStatus == "SETTLED" && plan.state == domain.OperationPending &&
		plan.recovery == provider.RecoveryVerifyFinal && plan.certainty == provider.CertaintyDefinitive &&
		outcome.HoldCode == ""
	if !settles {
		if err := FinishOperationTx(ctx, tx, claim, outcome); err != nil {
			return err
		}
		return syncCollectionAttemptState(ctx, tx, claim)
	}
	receipt := *plan.receipt
	if receipt.ProviderFeeCents == nil || *receipt.ProviderFeeCents < 0 {
		outcome.HoldCode = "COLLECTION_SETTLEMENT_FEE_UNKNOWN"
		if err := FinishOperationTx(ctx, tx, claim, outcome); err != nil {
			return err
		}
		return syncCollectionAttemptState(ctx, tx, claim)
	}
	if receipt.SettledAt == nil || receipt.SettledAt.IsZero() || !fundingTimeUTC(*receipt.SettledAt) ||
		!fundingTimeUTC(receipt.ObservedAt) || receipt.SettledAt.After(receipt.ObservedAt.Add(5*time.Second)) ||
		receipt.ObservedAt.After(now.Add(5*time.Second)) || receipt.SettledAt.After(now.Add(5*time.Second)) ||
		receipt.SettledAt.Before(scope.createdAt.Add(-5*time.Second)) ||
		receipt.ObservedSourceAccountRef == nil || *receipt.ObservedSourceAccountRef != scope.sourceAccount ||
		receipt.ProviderFeeCents != nil && *receipt.ProviderFeeCents < 0 ||
		receipt.SourceAccountRef != scope.sourceAccount || receipt.DestinationAccountRef != scope.destinationAccount {
		outcome.HoldCode = "COLLECTION_SETTLEMENT_EVIDENCE_INVALID"
		if err := FinishOperationTx(ctx, tx, claim, outcome); err != nil {
			return err
		}
		return syncCollectionAttemptState(ctx, tx, claim)
	}
	if scope.terms.PolicyHash != policyHash || scope.termsHash != scope.HashOrEmpty() {
		outcome.HoldCode = "COLLECTION_TERMS_BINDING_INVALID"
		if err := FinishOperationTx(ctx, tx, claim, outcome); err != nil {
			return err
		}
		return syncCollectionAttemptState(ctx, tx, claim)
	}
	if err := FinishOperationTx(ctx, tx, claim, outcome); err != nil {
		return err
	}
	var finishedState, finishedStatus string
	var finishedVersion int64
	var finishedObjectID, finishedFee *string
	if err := tx.QueryRow(ctx, `SELECT state,version,COALESCE(provider_status,''),
		provider_object_id,provider_fee_cents::text FROM servicing.operations WHERE mode=$1 AND id=$2`,
		string(claim.Mode), claim.OperationID).Scan(&finishedState, &finishedVersion,
		&finishedStatus, &finishedObjectID, &finishedFee); err != nil {
		return errors.New("collection_finish_handoff_read_failed")
	}
	if finishedState != "PENDING" || finishedVersion != claim.Version+1 ||
		finishedStatus != "SETTLED" || finishedObjectID == nil ||
		*finishedObjectID != receipt.ProviderObjectID || finishedFee == nil ||
		*finishedFee != strconv.FormatInt(*receipt.ProviderFeeCents, 10) {
		return syncCollectionAttemptState(ctx, tx, claim)
	}
	return settleVerifiedCollectionTx(ctx, tx, scope, claim, receipt, now, pricing, policyHash,
		lockedCustomerBridges)
}

func syncCollectionAttemptState(ctx context.Context, tx pgx.Tx, claim OperationClaim) error {
	var operationState string
	if err := tx.QueryRow(ctx, `SELECT state FROM servicing.operations WHERE mode=$1 AND id=$2`,
		string(claim.Mode), claim.OperationID).Scan(&operationState); err != nil {
		return errors.New("collection_attempt_state_read_failed")
	}
	var attemptState string
	switch operationState {
	case "QUEUED":
		attemptState = "QUEUED"
	case "PENDING", "DISPATCHING":
		attemptState = "PENDING"
	case "UNKNOWN":
		attemptState = "UNKNOWN"
	case "HELD":
		attemptState = "HELD"
	case "SETTLED":
		attemptState = "SETTLED"
	case "FAILED_CONFIRMED", "CANCELLED_UNSENT":
		attemptState = "FAILED_CONFIRMED"
	default:
		return errors.New("collection_attempt_state_invalid")
	}
	if _, err := tx.Exec(ctx, `UPDATE servicing.collection_attempts ca SET state=$3
		WHERE ca.mode=$1 AND ca.id=(SELECT a.attempt_id FROM servicing.collection_allocations a
			WHERE a.mode=$1 AND a.operation_id=$2 LIMIT 1)
		AND ca.state IN ('QUEUED','PENDING','UNKNOWN','HELD')`,
		string(claim.Mode), claim.OperationID, attemptState); err != nil {
		return errors.New("collection_attempt_state_update_failed")
	}
	return nil
}

func settleVerifiedCollectionTx(ctx context.Context, tx pgx.Tx, scope lockedOperationScope,
	claim OperationClaim, receipt provider.Receipt, now time.Time, pricing domain.Pricing,
	policyHash string, lockedCustomerBridges []string) error {
	var operationState string
	var operationVersion int64
	if err := tx.QueryRow(ctx, `SELECT state,version FROM servicing.operations WHERE mode=$1 AND id=$2`,
		string(claim.Mode), claim.OperationID).Scan(&operationState, &operationVersion); err != nil || operationState != "PENDING" {
		return errors.New("collection_settlement_operation_state_invalid")
	}
	var observationID, providerEventID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM servicing.provider_observations
		WHERE mode=$1 AND operation_id=$2 AND observation_kind='TRANSFER'
		AND status='SETTLED' ORDER BY observed_at DESC,id DESC LIMIT 1`,
		string(claim.Mode), claim.OperationID).Scan(&observationID); err != nil {
		return errors.New("collection_settlement_observation_missing")
	}
	var depositSourceObservation *string
	if err := tx.QueryRow(ctx, `SELECT source_observation_id::text FROM servicing.deposits
		WHERE mode=$1 AND id=$2`, string(claim.Mode), scope.collectionDepositID).Scan(&depositSourceObservation); err != nil {
		return errors.New("collection_settlement_deposit_read_failed")
	}
	if depositSourceObservation != nil {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(normalized_evidence->>'webhook_event_id','')
			FROM servicing.provider_observations WHERE mode=$1 AND id=$2`,
			string(claim.Mode), *depositSourceObservation).Scan(&providerEventID); err != nil {
			return errors.New("collection_settlement_event_read_failed")
		}
	}
	if receipt.ProviderFeeCents == nil || *receipt.ProviderFeeCents < 0 {
		return errors.New("collection_settlement_provider_fee_unknown")
	}
	actualFee := *receipt.ProviderFeeCents
	feeKnown := true
	if actualFee > math.MaxInt64-scope.amount {
		return errors.New("collection_settlement_amount_overflow")
	}
	actualDebit := scope.amount + actualFee
	var principalOwed, contractOwed, lateOwed int64
	var customerID, facilityID string
	var bridgeVersion int64
	var holdReasons []string
	if err := tx.QueryRow(ctx, `SELECT b.customer_id,b.facility_id,b.version,
		b.principal_owed_cents,b.contract_fee_owed_cents,b.late_fee_owed_cents,b.hold_reasons
		FROM servicing.bridges b WHERE b.mode=$1 AND b.id=$2`, string(claim.Mode),
		claim.BridgeID).Scan(&customerID, &facilityID, &bridgeVersion,
		&principalOwed, &contractOwed, &lateOwed, &holdReasons); err != nil {
		return errors.New("collection_settlement_bridge_read_failed")
	}
	var lateApplied, contractApplied, principalApplied int64
	if scope.kind == "COLLECT_FEE" {
		lateApplied = minInt64(scope.lateFeeCents, lateOwed)
		contractApplied = minInt64(scope.contractFeeCents, contractOwed)
	} else {
		principalApplied = minInt64(scope.principalCents, principalOwed)
	}
	debtApplied := lateApplied + contractApplied + principalApplied
	if debtApplied > scope.amount {
		return errors.New("collection_settlement_allocation_invalid")
	}
	refundPayable := scope.amount - debtApplied
	provenance, _ := json.Marshal(map[string]any{"operation_id": claim.OperationID,
		"attempt_id": scope.collectionAttemptID, "deposit_id": scope.collectionDepositID,
		"provider_deposit_id":              scope.providerDepositID,
		"deposit_source_observation_id":    depositSourceObservation,
		"webhook_event_id":                 nullableString(providerEventID),
		"provider_transfer_observation_id": observationID,
		"provider_transfer_id":             receipt.ProviderObjectID, "request_id": receipt.RequestID,
		"source_account_ref": receipt.SourceAccountRef, "destination_account_ref": receipt.DestinationAccountRef,
		"transfer_amount_cents": scope.amount, "provider_fee_cents": actualFee,
		"provider_fee_observed": feeKnown, "settled_at": receipt.SettledAt.UTC(),
		"observed_at": receipt.ObservedAt.UTC(), "refund_payable_cents": refundPayable,
		"saved_kind": scope.kind, "saved_late_fee_cents": scope.lateFeeCents,
		"saved_contract_fee_cents": scope.contractFeeCents,
		"saved_principal_cents":    scope.principalCents})
	lines := []domain.LedgerLine{{Account: "COLLECTED_CASH", Debit: domain.Money(scope.amount)}}
	if lateApplied > 0 {
		lines = append(lines, domain.LedgerLine{Account: "LATE_FEE_RECEIVABLE", Credit: domain.Money(lateApplied)})
	}
	if contractApplied > 0 {
		lines = append(lines, domain.LedgerLine{Account: "CONTRACT_FEE_RECEIVABLE", Credit: domain.Money(contractApplied)})
	}
	if principalApplied > 0 {
		lines = append(lines, domain.LedgerLine{Account: "PRINCIPAL_RECEIVABLE", Credit: domain.Money(principalApplied)})
	}
	if refundPayable > 0 {
		lines = append(lines, domain.LedgerLine{Account: "CUSTOMER_REFUND_PAYABLE", Credit: domain.Money(refundPayable)})
	}
	if actualFee > 0 {
		lines = append(lines,
			domain.LedgerLine{Account: "PROVIDER_FEE_EXPENSE", Debit: domain.Money(actualFee)},
			domain.LedgerLine{Account: "PROVIDER_FEE_CLEARING", Credit: domain.Money(actualFee)})
	}
	bridgeRef := claim.BridgeID
	operationRef := claim.OperationID
	effectKey := "collection-settlement:" + claim.OperationID
	if err := InsertLedgerTransaction(ctx, tx, claim.Mode, effectKey,
		"COLLECTION_SETTLEMENT", &bridgeRef, &operationRef, receipt.SettledAt.UTC().Format(time.RFC3339Nano),
		provenance, lines); err != nil {
		return err
	}
	_, err := releaseCollectionBacking(ctx, tx, claim.Mode, claim.BridgeID,
		contractApplied+principalApplied)
	if err != nil {
		return err
	}
	finalPrincipalDebt := principalOwed - principalApplied
	finalContractDebt := contractOwed - contractApplied
	backingDeficit, backingEvidenceHold, err := collectionBackingState(ctx, tx,
		claim.Mode, claim.BridgeID, finalPrincipalDebt, finalContractDebt)
	if err != nil {
		return err
	}
	if principalApplied > 0 {
		var facilityPrincipalOwed int64
		if err := tx.QueryRow(ctx, `SELECT principal_owed_cents FROM servicing.facilities
			WHERE mode=$1 AND id=$2`, string(claim.Mode), facilityID).Scan(&facilityPrincipalOwed); err != nil ||
			facilityPrincipalOwed < principalApplied {
			return errors.New("collection_facility_principal_counter_invalid")
		}
		tag, err := tx.Exec(ctx, `UPDATE servicing.facilities SET principal_owed_cents=principal_owed_cents-$3,
			version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`,
			string(claim.Mode), facilityID, principalApplied)
		if err != nil {
			return errors.New("collection_facility_repayment_update_failed")
		}
		if tag.RowsAffected() != 1 {
			return errors.New("collection_facility_repayment_update_failed")
		}
	}
	feeApplied := lateApplied + contractApplied
	tag, err := tx.Exec(ctx, `UPDATE servicing.customers SET principal_owed_cents=principal_owed_cents-$3,
		unpaid_fee_cents=unpaid_fee_cents-$4,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND principal_owed_cents>=$3 AND unpaid_fee_cents>=$4`,
		string(claim.Mode), customerID, principalApplied, feeApplied)
	if err != nil {
		return errors.New("collection_customer_repayment_update_failed")
	}
	if tag.RowsAffected() != 1 {
		return errors.New("collection_customer_repayment_update_failed")
	}
	bridgeVersion, err = updateCollectionDebt(ctx, tx, claim.Mode, claim.BridgeID,
		lateApplied, contractApplied, principalApplied, backingDeficit, backingEvidenceHold)
	if err != nil {
		return err
	}
	if refundPayable > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.collection_refund_payables(mode,
			customer_id,bridge_id,operation_id,amount_cents,remaining_cents,currency,state)
			VALUES($1,$2,$3,$4,$5,$5,'USD','OPEN')`, string(claim.Mode), customerID,
			claim.BridgeID, claim.OperationID, refundPayable); err != nil {
			return errors.New("collection_refund_payable_insert_failed")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE servicing.collection_attempts SET state='SETTLED',
		reserved_debit_cents=0,settled_debit_cents=$3,provider_fee_cents=$4
		WHERE mode=$1 AND id=$2 AND state IN ('QUEUED','PENDING','UNKNOWN','HELD')
		AND reserved_debit_cents=$5`, string(claim.Mode), scope.collectionAttemptID,
		actualDebit, actualFee, scope.collectionReservedDebit); err != nil {
		return errors.New("collection_attempt_settlement_update_failed")
	}
	var authorityStatus string
	var authorityLimit, authorityConsumed int64
	var authorityExpiresAt time.Time
	var newerAuthority bool
	if err := tx.QueryRow(ctx, `SELECT a.status,a.maximum_debit_cents,a.consumed_debit_cents,
		a.expires_at,EXISTS(SELECT 1 FROM servicing.authorities newer WHERE newer.mode=a.mode
		AND newer.id=a.id AND newer.version>a.version) FROM servicing.authorities a
		WHERE a.mode=$1 AND a.id=$2 AND a.version=$3`, string(claim.Mode), *scope.authorityID,
		*scope.authorityVersion).Scan(&authorityStatus, &authorityLimit, &authorityConsumed,
		&authorityExpiresAt, &newerAuthority); err != nil {
		return errors.New("collection_authority_post_settlement_read_failed")
	}
	if actualDebit > math.MaxInt64-authorityConsumed {
		return errors.New("collection_authority_consumption_overflow")
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.authorities SET
		reserved_debit_cents=reserved_debit_cents-$4,consumed_debit_cents=consumed_debit_cents+$5
		WHERE mode=$1 AND id=$2 AND version=$3 AND reserved_debit_cents>=$4`,
		string(claim.Mode), *scope.authorityID, *scope.authorityVersion,
		scope.collectionReservedDebit, actualDebit)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("collection_authority_settlement_update_failed")
	}
	settledOperationVersion := operationVersion + 1
	tag, err = tx.Exec(ctx, `UPDATE servicing.operations SET state='SETTLED',version=version+1,
		next_check_at=NULL,sanitized_error_code=NULL,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state='PENDING' AND version=$3 AND provider_status='SETTLED'`,
		string(claim.Mode), claim.OperationID, operationVersion)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("collection_operation_settlement_update_failed")
	}
	var extraHolds []string
	if actualFee != scope.collectionQuotedFee {
		extraHolds = appendUniqueString(extraHolds, "COLLECTION_PROVIDER_FEE_CONFLICT")
	}
	if authorityStatus != "ACTIVE" || !now.Before(authorityExpiresAt) || newerAuthority ||
		authorityConsumed+actualDebit > authorityLimit {
		extraHolds = appendUniqueString(extraHolds, "COLLECTION_AUTHORITY_BLOCKED")
	}
	if backingDeficit > 0 || backingEvidenceHold {
		extraHolds = appendUniqueString(extraHolds, "RECEIVABLE_BACKING_CONFLICT")
	}
	if refundPayable > 0 {
		extraHolds = appendUniqueString(extraHolds, "REFUND_PAYABLE")
	}
	if len(extraHolds) > 0 {
		if len(lockedCustomerBridges) == 0 {
			return errors.New("collection_settlement_parent_locks_missing")
		}
		for _, reason := range extraHolds {
			if err := holdCustomerCollections(ctx, tx, claim.Mode, customerID,
				lockedCustomerBridges, reason, map[string]any{"operation_id": claim.OperationID,
					"provider_observation_id": observationID, "actual_transfer_cents": scope.amount,
					"provider_fee_cents": actualFee, "refund_payable_cents": refundPayable,
					"movement_certainty": "VERIFIED_SETTLED"}); err != nil {
				return err
			}
		}
	}
	accrual, err := AccrueThrough(ctx, tx, AccrualOptions{Mode: claim.Mode,
		Pricing: pricing, ExpectedPolicyHash: policyHash}, claim.BridgeID, now)
	if err != nil {
		return err
	}
	if err := RefreshDelinquency(ctx, tx, claim.Mode, claim.BridgeID, now); err != nil {
		return err
	}
	if err := closeRepaidCollectionIfSafe(ctx, tx, claim.Mode, claim.BridgeID,
		receipt.SettledAt.UTC(), accrual); err != nil {
		return err
	}
	var finalBridgeVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(claim.Mode), claim.BridgeID).Scan(&finalBridgeVersion); err != nil {
		return errors.New("collection_settlement_bridge_version_read_failed")
	}
	summary, _ := json.Marshal(map[string]any{"customer_id": customerID,
		"account_ref": "customer:" + customerID, "bridge_id": claim.BridgeID,
		"bridge_version": finalBridgeVersion, "operation_id": claim.OperationID,
		"deposit_id": scope.collectionDepositID, "provider_observation_id": observationID,
		"amount_cents": scope.amount, "principal_paid_cents": principalApplied,
		"contract_fee_paid_cents": contractApplied, "late_fee_paid_cents": lateApplied,
		"provider_fee_cents": actualFee, "refund_payable_cents": refundPayable,
		"backing_deficit_cents": backingDeficit, "backing_evidence_hold": backingEvidenceHold,
		"state": "SETTLED", "effective_at": receipt.SettledAt.UTC()})
	parent, parentVersion := claim.BridgeID, finalBridgeVersion
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: claim.Mode, ResourceType: "BRIDGE",
		ResourceID: claim.BridgeID, ResourceVersion: finalBridgeVersion,
		ChangeType: "COLLECTION_SETTLED", Summary: summary}, &parent, &parentVersion); err != nil {
		return err
	}
	operationSummary, _ := json.Marshal(map[string]any{"operation_id": claim.OperationID,
		"bridge_id": claim.BridgeID, "bridge_version": finalBridgeVersion,
		"state": "SETTLED", "provider_observation_id": observationID})
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: claim.Mode, ResourceType: "OPERATION",
		ResourceID: claim.OperationID, ResourceVersion: settledOperationVersion,
		ChangeType: "COLLECTION_SETTLED", Summary: operationSummary}, &parent, &parentVersion); err != nil {
		return err
	}
	return nil
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

type collectionBackingRow struct {
	sourceSystem string
	invoiceID    string
	activeCents  int64
}

func releaseCollectionBacking(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID string, amount int64) (int64, error) {
	if amount < 0 {
		return 0, errors.New("collection_backing_release_invalid")
	}
	if amount == 0 {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `SELECT source_system,invoice_id,active_cents
		FROM servicing.bridge_invoice_allocations WHERE mode=$1 AND bridge_id=$2
		ORDER BY allocation_index,source_system,invoice_id FOR UPDATE`, string(mode), bridgeID)
	if err != nil {
		return 0, errors.New("collection_backing_read_failed")
	}
	var allocations []collectionBackingRow
	for rows.Next() {
		var row collectionBackingRow
		if rows.Scan(&row.sourceSystem, &row.invoiceID, &row.activeCents) != nil {
			rows.Close()
			return 0, errors.New("collection_backing_read_failed")
		}
		allocations = append(allocations, row)
	}
	if rows.Err() != nil {
		rows.Close()
		return 0, errors.New("collection_backing_read_failed")
	}
	rows.Close()
	var released int64
	remaining := amount
	for _, allocation := range allocations {
		if remaining == 0 {
			break
		}
		take := minInt64(remaining, allocation.activeCents)
		if take <= 0 {
			continue
		}
		tag, err := tx.Exec(ctx, `UPDATE servicing.bridge_invoice_allocations
			SET active_cents=active_cents-$4 WHERE mode=$1 AND bridge_id=$2
			AND source_system=$3 AND invoice_id=$5 AND active_cents>=$4`,
			string(mode), bridgeID, allocation.sourceSystem, take, allocation.invoiceID)
		if err != nil || tag.RowsAffected() != 1 {
			return 0, errors.New("collection_backing_allocation_release_failed")
		}
		tag, err = tx.Exec(ctx, `UPDATE servicing.receivables SET reserved_cents=reserved_cents-$4
			WHERE mode=$1 AND source_system=$2 AND invoice_id=$3 AND reserved_cents>=$4`,
			string(mode), allocation.sourceSystem, allocation.invoiceID, take)
		if err != nil || tag.RowsAffected() != 1 {
			return 0, errors.New("collection_backing_invoice_release_failed")
		}
		released += take
		remaining -= take
	}
	return released, nil
}

// collectionBackingState reapplies the source-import allocation ordering to
// current invoice truth and returns this bridge's exact remaining deficit.
func collectionBackingState(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID string, finalPrincipalDebt, finalContractDebt int64) (int64, bool, error) {
	if finalPrincipalDebt < 0 || finalContractDebt < 0 ||
		finalPrincipalDebt > math.MaxInt64-finalContractDebt {
		return 0, false, errors.New("collection_backing_debt_invalid")
	}
	var deficitText, activeText string
	var evidenceHold bool
	err := tx.QueryRow(ctx, `WITH target_invoices AS (
		SELECT DISTINCT source_system,invoice_id FROM servicing.bridge_invoice_allocations
		WHERE mode=$1 AND bridge_id=$2
	), ranked AS (
		SELECT a.bridge_id,a.active_cents,
			LEAST(a.active_cents,GREATEST(0,
				SUM(a.active_cents) OVER (PARTITION BY a.mode,a.source_system,a.invoice_id
					ORDER BY b.created_at,b.id,a.allocation_index ROWS UNBOUNDED PRECEDING)
				-r.unpaid_cents)) AS deficit_cents,
			(a.active_cents>0 AND (r.disputed OR NOT r.source_complete OR
				NOT r.allocation_complete OR NOT r.due_date_verified)) AS evidence_hold
		FROM servicing.bridge_invoice_allocations a
		JOIN servicing.bridges b ON b.mode=a.mode AND b.id=a.bridge_id
		JOIN servicing.receivables r ON r.mode=a.mode AND r.source_system=a.source_system
			AND r.invoice_id=a.invoice_id
		WHERE a.mode=$1 AND EXISTS(SELECT 1 FROM target_invoices i
			WHERE i.source_system=a.source_system AND i.invoice_id=a.invoice_id)
	)
	SELECT COALESCE(sum(deficit_cents) FILTER (WHERE bridge_id=$2),0)::text,
		COALESCE(sum(active_cents) FILTER (WHERE bridge_id=$2),0)::text,
		COALESCE(bool_or(evidence_hold) FILTER (WHERE bridge_id=$2),false)
	FROM ranked`, string(mode), bridgeID).Scan(&deficitText, &activeText, &evidenceHold)
	if err != nil {
		return 0, false, errors.New("collection_backing_reconcile_failed")
	}
	deficit, err := strconv.ParseInt(deficitText, 10, 64)
	if err != nil || deficit < 0 {
		return 0, false, errors.New("collection_backing_deficit_overflow")
	}
	active, err := strconv.ParseInt(activeText, 10, 64)
	if err != nil || active < 0 {
		return 0, false, errors.New("collection_backing_allocation_overflow")
	}
	coveredDebt := finalPrincipalDebt + finalContractDebt
	missing := int64(0)
	if coveredDebt > active {
		missing = coveredDebt - active
	}
	if deficit > math.MaxInt64-missing {
		return 0, false, errors.New("collection_backing_deficit_overflow")
	}
	return missing + deficit, evidenceHold, nil
}

func updateCollectionDebt(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID string, lateFee, contractFee, principal, backingDeficit int64,
	backingEvidenceHold bool) (int64, error) {
	if lateFee < 0 || contractFee < 0 || principal < 0 || backingDeficit < 0 {
		return 0, errors.New("collection_debt_update_invalid")
	}
	var version int64
	err := tx.QueryRow(ctx, `UPDATE servicing.bridges SET
		principal_owed_cents=principal_owed_cents-$5,
		contract_fee_owed_cents=contract_fee_owed_cents-$4,
		late_fee_owed_cents=late_fee_owed_cents-$3,
		principal_paid_cents=principal_paid_cents+$5,
		contract_fee_paid_cents=contract_fee_paid_cents+$4,
		late_fee_paid_cents=late_fee_paid_cents+$3,
		cumulative_late_fee_paid_cents=cumulative_late_fee_paid_cents+$3,
		backing_deficit_cents=$6,
		hold_reasons=CASE WHEN ($6::bigint>0 OR $7::boolean) AND
			NOT ('RECEIVABLE_BACKING_CONFLICT'=ANY(hold_reasons))
			THEN array_append(hold_reasons,'RECEIVABLE_BACKING_CONFLICT') ELSE hold_reasons END,
		version=version+1,collection_debt_version=collection_debt_version+1,
		updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND principal_owed_cents>=$5
		AND contract_fee_owed_cents>=$4 AND late_fee_owed_cents>=$3
		AND principal_paid_cents<=principal_original_cents-$5
		AND contract_fee_paid_cents<=contract_fee_original_cents-$4
		RETURNING version`, string(mode), bridgeID, lateFee, contractFee, principal,
		backingDeficit, backingEvidenceHold).Scan(&version)
	if err != nil {
		return 0, errors.New("collection_bridge_debt_update_failed")
	}
	return version, nil
}

func appendCollectionHoldVersioned(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID, reason string, evidence map[string]any) (int64, error) {
	if err := appendCollectionHold(ctx, tx, mode, bridgeID, reason, evidence); err != nil {
		return 0, err
	}
	var version int64
	if err := tx.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(mode), bridgeID).Scan(&version); err != nil {
		return 0, errors.New("collection_hold_version_read_failed")
	}
	return version, nil
}

func closeRepaidCollectionIfSafe(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID string, settledAt time.Time, accrual AccrualResult) error {
	var customerID, lifecycle string
	var principal, contractFee, lateFee, backingDeficit int64
	var holds []string
	err := tx.QueryRow(ctx, `SELECT customer_id,lifecycle,principal_owed_cents,
		contract_fee_owed_cents,late_fee_owed_cents,backing_deficit_cents,hold_reasons
		FROM servicing.bridges WHERE mode=$1 AND id=$2`, string(mode), bridgeID).Scan(
		&customerID, &lifecycle, &principal, &contractFee, &lateFee, &backingDeficit, &holds)
	if err != nil {
		return errors.New("collection_closure_bridge_read_failed")
	}
	if lifecycle == "REPAID" || principal != 0 || contractFee != 0 || lateFee != 0 ||
		backingDeficit != 0 || len(holds) != 0 || !accrual.Current || accrual.Pending || accrual.CorrectionRequired {
		return nil
	}
	var customerHold, pendingOperations, pendingAttempts, activeBacking, openRefund bool
	if err := tx.QueryRow(ctx, `SELECT active_hold FROM servicing.customers WHERE mode=$1 AND id=$2`,
		string(mode), customerID).Scan(&customerHold); err != nil {
		return errors.New("collection_closure_customer_read_failed")
	}
	if err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM servicing.operations WHERE mode=$1 AND bridge_id=$2
			AND state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD')),
		EXISTS(SELECT 1 FROM servicing.collection_attempts WHERE mode=$1 AND bridge_id=$2
			AND state IN ('PLANNED','QUEUED','PENDING','UNKNOWN','HELD')),
		EXISTS(SELECT 1 FROM servicing.bridge_invoice_allocations WHERE mode=$1 AND bridge_id=$2
			AND active_cents>0),
		EXISTS(SELECT 1 FROM servicing.collection_refund_payables WHERE mode=$1 AND bridge_id=$2
			AND state='OPEN')`, string(mode), bridgeID).Scan(&pendingOperations, &pendingAttempts,
		&activeBacking, &openRefund); err != nil {
		return errors.New("collection_closure_work_read_failed")
	}
	if customerHold || pendingOperations || pendingAttempts || activeBacking || openRefund {
		return nil
	}
	var version int64
	err = tx.QueryRow(ctx, `UPDATE servicing.bridges SET lifecycle='REPAID',closed_at=$3,
		version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2
		AND lifecycle IN ('ACTIVE','COLLECTING','REOPENED_HELD')
		AND principal_owed_cents=0 AND contract_fee_owed_cents=0 AND late_fee_owed_cents=0
		AND backing_deficit_cents=0 AND cardinality(hold_reasons)=0 RETURNING version`,
		string(mode), bridgeID, settledAt.UTC()).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("collection_closure_update_failed")
	}
	summary, _ := json.Marshal(map[string]any{"customer_id": customerID,
		"account_ref": "customer:" + customerID, "bridge_id": bridgeID,
		"bridge_version": version, "closed_at": settledAt.UTC(), "state": "REPAID"})
	parent, parentVersion := bridgeID, version
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: mode, ResourceType: "BRIDGE",
		ResourceID: bridgeID, ResourceVersion: version, ChangeType: "COLLECTION_CLOSED",
		Summary: summary}, &parent, &parentVersion); err != nil {
		return err
	}
	return nil
}

func recordCollectionTerminalReversalTx(ctx context.Context, tx pgx.Tx,
	job WebhookJobClaim, candidate OperationCandidate, scope lockedOperationScope,
	outcome OperationReadOutcome, customerBridgeIDs []string, now time.Time,
	pricing domain.Pricing, policyHash string) error {
	if job.Mode != domain.ModeDemo || job.TargetKind != "FUND_REQUEST" ||
		candidate.State != domain.OperationSettled || outcome.Result == nil ||
		outcome.Result.Receipt.Status != "REVERSED" ||
		(candidate.Kind != "COLLECT_FEE" && candidate.Kind != "COLLECT_PRINCIPAL") {
		return recordCollectionReversalHoldTx(ctx, tx, job, scope, customerBridgeIDs,
			"COLLECTION_REVERSAL_EVIDENCE_UNSUPPORTED", outcome)
	}
	proof := outcome.CollectionReversal
	if proof == nil {
		return recordCollectionReversalHoldTx(ctx, tx, job, scope, customerBridgeIDs,
			"COLLECTION_REVERSAL_EVIDENCE_UNSUPPORTED", outcome)
	}
	receipt := outcome.Result.Receipt
	if pricing.Validate() != nil || !validHash(policyHash) || scope.terms.PolicyHash != policyHash ||
		proof.ReversalID == "" || proof.OriginalObjectID == "" || scope.providerObjectID == nil ||
		*scope.providerObjectID != proof.OriginalObjectID || receipt.ProviderObjectID != proof.OriginalObjectID ||
		proof.RequestID != scope.requestID || receipt.RequestID != scope.requestID ||
		proof.AccountRef != scope.sourceAccount || proof.FundingAccountRef != scope.destinationAccount ||
		proof.Currency != "USD" || proof.AmountCents <= 0 || !fundingTimeUTC(proof.ReversedAt) ||
		!fundingTimeUTC(proof.ObservedAt) || proof.ReversedAt.After(proof.ObservedAt.Add(5*time.Second)) ||
		now.IsZero() || proof.ObservedAt.After(now.Add(5*time.Second)) ||
		len(proof.RawEvidence) == 0 || len(proof.RawEvidence) > maxProtectedOperationBodyBytes ||
		string(proof.RawEvidence) != string(receipt.RawEvidence) {
		return recordCollectionReversalHoldTx(ctx, tx, job, scope, customerBridgeIDs,
			"COLLECTION_REVERSAL_EVIDENCE_INVALID", outcome)
	}
	evidenceHash := hashEvidence(proof.RawEvidence)
	var previousOperation, previousHash string
	var previousAmount int64
	err := tx.QueryRow(ctx, `SELECT original_operation_id::text,evidence_hash,amount_cents
		FROM servicing.collection_reversals WHERE mode=$1 AND account_ref=$2 AND provider_reversal_id=$3`,
		string(job.Mode), proof.AccountRef, proof.ReversalID).Scan(&previousOperation, &previousHash, &previousAmount)
	if err == nil {
		if previousOperation == scope.operationID && previousHash == evidenceHash && previousAmount == proof.AmountCents {
			return CompleteWebhookJobTx(ctx, tx, job, "DONE", "COLLECTION_REVERSAL_DUPLICATE")
		}
		return recordCollectionReversalHoldTx(ctx, tx, job, scope, customerBridgeIDs,
			"COLLECTION_REVERSAL_ID_CONFLICT", outcome)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("collection_reversal_dedup_read_failed")
	}
	var originalAmount int64
	if err := tx.QueryRow(ctx, `SELECT amount_cents FROM servicing.operations
		WHERE mode=$1 AND id=$2 AND state='SETTLED'`, string(job.Mode), scope.operationID).Scan(&originalAmount); err != nil {
		return errors.New("collection_reversal_operation_read_failed")
	}
	var originalSettledAt time.Time
	if err := tx.QueryRow(ctx, `SELECT effective_at FROM servicing.ledger_transactions
		WHERE mode=$1 AND effect_key=$2`, string(job.Mode),
		"collection-settlement:"+scope.operationID).Scan(&originalSettledAt); err != nil {
		return errors.New("collection_reversal_original_settlement_read_failed")
	}
	var previouslyReversed int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_cents),0) FROM servicing.collection_reversals
		WHERE mode=$1 AND original_operation_id=$2`, string(job.Mode), scope.operationID).Scan(&previouslyReversed); err != nil {
		return errors.New("collection_reversal_history_read_failed")
	}
	if originalAmount <= 0 || previouslyReversed < 0 || !proof.ReversedAt.After(originalSettledAt) {
		return recordCollectionReversalHoldTx(ctx, tx, job, scope, customerBridgeIDs,
			"COLLECTION_REVERSAL_EVIDENCE_INVALID", outcome)
	}
	normalized, _ := json.Marshal(map[string]any{"source": "DEMO_NORMALIZED_COLLECTION_REVERSAL",
		"operation_id": scope.operationID, "original_transfer_id": proof.OriginalObjectID,
		"request_id": proof.RequestID, "reversal_id": proof.ReversalID,
		"account_ref": proof.AccountRef, "funding_account_ref": proof.FundingAccountRef,
		"amount_cents": proof.AmountCents, "currency": proof.Currency,
		"reversed_at": proof.ReversedAt.UTC(), "observed_at": proof.ObservedAt.UTC(),
		"original_response_body_encoding": "base64",
		"original_response_body_base64":   base64.StdEncoding.EncodeToString(proof.RawEvidence)})
	var observationID string
	err = tx.QueryRow(ctx, `INSERT INTO servicing.provider_observations(mode,operation_id,
		observation_kind,provider_object_id,source_account_ref,destination_account_ref,
		request_id,status,amount_cents,currency,effective_at,observed_at,evidence_hash,
		normalized_evidence) VALUES($1,$2,'TRANSFER',$3,$4,$5,$6,'REVERSED',$7,'USD',$8,$9,$10,$11::jsonb)
		RETURNING id::text`, string(job.Mode), scope.operationID, proof.ReversalID,
		proof.AccountRef, proof.FundingAccountRef, proof.RequestID, proof.AmountCents,
		proof.ReversedAt.UTC(), proof.ObservedAt.UTC(), evidenceHash, string(normalized)).Scan(&observationID)
	if err != nil {
		return errors.New("collection_reversal_observation_insert_failed")
	}
	var reversalRowID string
	err = tx.QueryRow(ctx, `INSERT INTO servicing.collection_reversals(mode,original_operation_id,
		provider_reversal_id,account_ref,amount_cents,currency,reversed_at,observed_at,
		provider_observation_id,evidence_hash)
		VALUES($1,$2,$3,$4,$5,'USD',$6,$7,$8,$9) RETURNING id::text`,
		string(job.Mode), scope.operationID, proof.ReversalID, proof.AccountRef,
		proof.AmountCents, proof.ReversedAt.UTC(), proof.ObservedAt.UTC(), observationID,
		evidenceHash).Scan(&reversalRowID)
	if err != nil {
		return errors.New("collection_reversal_insert_failed")
	}
	originalCredits, originalCash, err := collectionSettlementCredits(ctx, tx, job.Mode, scope.operationID)
	if err != nil {
		return err
	}
	if originalCash != originalAmount {
		return recordCollectionReversalHoldTx(ctx, tx, job, scope, customerBridgeIDs,
			"COLLECTION_SETTLEMENT_LEDGER_CONFLICT", outcome)
	}
	priorAllocations, err := collectionReversalBucketTotals(ctx, tx, job.Mode, scope.operationID)
	if err != nil {
		return err
	}
	remainingReversal := proof.AmountCents
	var lateRestore, contractRestore, principalRestore, refundRestore int64
	allocationIndex := 0
	var refundID *string
	var refundRemaining int64
	if err := tx.QueryRow(ctx, `SELECT id::text,remaining_cents FROM servicing.collection_refund_payables
		WHERE mode=$1 AND operation_id=$2 AND state='OPEN' FOR UPDATE`,
		string(job.Mode), scope.operationID).Scan(&refundID, &refundRemaining); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("collection_reversal_refund_read_failed")
	}
	refundAvailable := originalCredits["CUSTOMER_REFUND_PAYABLE"] - priorAllocations["REFUND_PAYABLE"]
	if refundAvailable < 0 {
		return errors.New("collection_reversal_refund_history_invalid")
	}
	refundAvailable = minInt64(refundAvailable, refundRemaining)
	refundRestore, allocationIndex, remainingReversal, err = applyCollectionReversalBucket(ctx, tx,
		job.Mode, reversalRowID, allocationIndex, "REFUND_PAYABLE", refundAvailable,
		remainingReversal, priorAllocations)
	if err != nil {
		return err
	}
	for _, bucket := range []string{"PRINCIPAL", "CONTRACT_FEE", "LATE_FEE"} {
		available := originalCredits[bucket] - priorAllocations[bucket]
		if available < 0 {
			return errors.New("collection_reversal_debt_history_invalid")
		}
		take, nextIndex, nextRemaining, applyErr := applyCollectionReversalBucket(ctx, tx,
			job.Mode, reversalRowID, allocationIndex, bucket, available,
			remainingReversal, priorAllocations)
		if applyErr != nil {
			return applyErr
		}
		allocationIndex, remainingReversal = nextIndex, nextRemaining
		switch bucket {
		case "PRINCIPAL":
			principalRestore = take
		case "CONTRACT_FEE":
			contractRestore = take
		case "LATE_FEE":
			lateRestore = take
		}
	}
	if remainingReversal > 0 {
		if err := insertCollectionReversalAllocation(ctx, tx, job.Mode, reversalRowID,
			allocationIndex, "REVERSAL_RECEIVABLE", remainingReversal); err != nil {
			return err
		}
	}
	_, err = restoreCollectionBacking(ctx, tx, job.Mode, candidate.BridgeID,
		contractRestore+principalRestore)
	if err != nil {
		return err
	}
	var priorPrincipalDebt, priorContractDebt int64
	if err := tx.QueryRow(ctx, `SELECT principal_owed_cents,contract_fee_owed_cents
		FROM servicing.bridges WHERE mode=$1 AND id=$2`, string(job.Mode),
		candidate.BridgeID).Scan(&priorPrincipalDebt, &priorContractDebt); err != nil {
		return errors.New("collection_reversal_debt_read_failed")
	}
	if principalRestore > math.MaxInt64-priorPrincipalDebt ||
		contractRestore > math.MaxInt64-priorContractDebt {
		return errors.New("collection_reversal_debt_overflow")
	}
	backingDeficit, backingEvidenceHold, err := collectionBackingState(ctx, tx, job.Mode,
		candidate.BridgeID, priorPrincipalDebt+principalRestore, priorContractDebt+contractRestore)
	if err != nil {
		return err
	}
	lines := make([]domain.LedgerLine, 0, 6)
	if lateRestore > 0 {
		lines = append(lines, domain.LedgerLine{Account: "LATE_FEE_RECEIVABLE", Debit: domain.Money(lateRestore)})
	}
	if contractRestore > 0 {
		lines = append(lines, domain.LedgerLine{Account: "CONTRACT_FEE_RECEIVABLE", Debit: domain.Money(contractRestore)})
	}
	if principalRestore > 0 {
		lines = append(lines, domain.LedgerLine{Account: "PRINCIPAL_RECEIVABLE", Debit: domain.Money(principalRestore)})
	}
	if refundRestore > 0 {
		lines = append(lines, domain.LedgerLine{Account: "CUSTOMER_REFUND_PAYABLE", Debit: domain.Money(refundRestore)})
	}
	if remainingReversal > 0 {
		lines = append(lines, domain.LedgerLine{Account: "COLLECTION_REVERSAL_RECEIVABLE", Debit: domain.Money(remainingReversal)})
	}
	lines = append(lines, domain.LedgerLine{Account: "COLLECTED_CASH", Credit: domain.Money(proof.AmountCents)})
	provenance, _ := json.Marshal(map[string]any{"source": "DEMO_NORMALIZED_COLLECTION_REVERSAL",
		"reversal_id": proof.ReversalID, "operation_id": scope.operationID,
		"provider_observation_id": observationID, "amount_cents": proof.AmountCents,
		"previously_reversed_cents": previouslyReversed, "unallocated_reversal_receivable_cents": remainingReversal,
		"backing_deficit_cents": backingDeficit, "reversed_at": proof.ReversedAt.UTC(),
		"observed_at": proof.ObservedAt.UTC()})
	bridgeRef, operationRef := candidate.BridgeID, scope.operationID
	if err := InsertLedgerTransaction(ctx, tx, job.Mode,
		"collection-reversal:"+reversalRowID, "COLLECTION_REVERSAL", &bridgeRef,
		&operationRef, proof.ReversedAt.UTC().Format(time.RFC3339Nano), provenance, lines); err != nil {
		return err
	}
	var otherOpenBridge bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1
		AND customer_id=$2 AND id<>$3 AND lifecycle IN ('RESERVED','FUNDING_PENDING','ACTIVE','COLLECTING','REOPENED_HELD'))`,
		string(job.Mode), scope.customerID, candidate.BridgeID).Scan(&otherOpenBridge); err != nil {
		return errors.New("collection_reversal_other_bridge_read_failed")
	}
	bridgeVersion, err := restoreCollectionDebt(ctx, tx, job.Mode, candidate.BridgeID,
		lateRestore, contractRestore, principalRestore, backingDeficit, backingEvidenceHold, otherOpenBridge)
	if err != nil {
		return err
	}
	if principalRestore > 0 {
		tag, e := tx.Exec(ctx, `UPDATE servicing.facilities SET principal_owed_cents=principal_owed_cents+$3,
			version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2
			AND principal_owed_cents<=$4::bigint-$3::bigint`, string(job.Mode), scope.facilityID,
			principalRestore, int64(math.MaxInt64))
		if e != nil || tag.RowsAffected() != 1 {
			return errors.New("collection_reversal_facility_update_failed")
		}
	}
	feeRestore := lateRestore + contractRestore
	if principalRestore > math.MaxInt64-feeRestore {
		return errors.New("collection_reversal_customer_overflow")
	}
	tag, err := tx.Exec(ctx, `UPDATE servicing.customers SET principal_owed_cents=principal_owed_cents+$3,
		unpaid_fee_cents=unpaid_fee_cents+$4,active_hold=true,version=version+1,
		updated_at=clock_timestamp() WHERE mode=$1 AND id=$2
		AND principal_owed_cents<=$5::bigint-$3::bigint AND unpaid_fee_cents<=$6::bigint-$4::bigint`, string(job.Mode),
		scope.customerID, principalRestore, feeRestore, int64(math.MaxInt64), int64(math.MaxInt64))
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("collection_reversal_customer_update_failed")
	}
	if refundRestore > 0 && refundID != nil {
		remaining := refundRemaining - refundRestore
		state := "OPEN"
		var resolved any
		if remaining == 0 {
			state, resolved = "RESOLVED", proof.ReversedAt.UTC()
		}
		if _, err := tx.Exec(ctx, `UPDATE servicing.collection_refund_payables SET
			remaining_cents=$3,state=$4,resolved_at=$5 WHERE mode=$1 AND id=$2 AND state='OPEN'`,
			string(job.Mode), *refundID, remaining, state, resolved); err != nil {
			return errors.New("collection_reversal_refund_update_failed")
		}
	}
	if _, err := AccrueThrough(ctx, tx, AccrualOptions{Mode: job.Mode,
		Pricing: pricing, ExpectedPolicyHash: policyHash}, candidate.BridgeID, now); err != nil {
		return err
	}
	if err := RefreshDelinquency(ctx, tx, job.Mode, candidate.BridgeID, now); err != nil {
		return err
	}
	if err := AppendOperationJournal(ctx, tx, job.Mode, scope.operationID,
		"OBSERVATION_RECORDED", "SETTLED", "SETTLED", "", nil, nil, &observationID,
		map[string]any{"source": "DEMO_NORMALIZED_COLLECTION_REVERSAL",
			"reversal_id": proof.ReversalID, "webhook_event_id": job.EventID,
			"amount_cents": proof.AmountCents, "movement_certainty": "VERIFIED_RETURNED"}); err != nil {
		return err
	}
	holds := []string{"REVERSAL_REVIEW"}
	if backingDeficit > 0 || backingEvidenceHold {
		holds = append(holds, "RECEIVABLE_BACKING_CONFLICT")
	}
	if remainingReversal > 0 {
		holds = append(holds, "COLLECTION_REVERSAL_OVERAGE")
	}
	for _, reason := range holds {
		if len(customerBridgeIDs) == 0 {
			return errors.New("collection_reversal_parent_locks_missing")
		}
		if err := holdCustomerCollections(ctx, tx, job.Mode, scope.customerID,
			customerBridgeIDs, reason, map[string]any{"reversal_id": proof.ReversalID,
				"operation_id": scope.operationID, "provider_observation_id": observationID,
				"backing_deficit_cents":                 backingDeficit,
				"unallocated_reversal_receivable_cents": remainingReversal,
				"movement_certainty":                    "VERIFIED_RETURNED"}); err != nil {
			return err
		}
	}
	if err := tx.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(job.Mode), candidate.BridgeID).Scan(&bridgeVersion); err != nil {
		return errors.New("collection_reversal_bridge_version_read_failed")
	}
	changeSummary, _ := json.Marshal(map[string]any{"customer_id": scope.customerID,
		"account_ref": "customer:" + scope.customerID, "bridge_id": candidate.BridgeID,
		"bridge_version": bridgeVersion, "operation_id": scope.operationID,
		"reversal_id": proof.ReversalID, "amount_cents": proof.AmountCents,
		"late_fee_restored_cents": lateRestore, "contract_fee_restored_cents": contractRestore,
		"principal_restored_cents": principalRestore, "backing_deficit_cents": backingDeficit,
		"backing_evidence_hold": backingEvidenceHold,
		"state":                 "REVERSED"})
	parent, parentVersion := candidate.BridgeID, bridgeVersion
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: job.Mode, ResourceType: "BRIDGE",
		ResourceID: candidate.BridgeID, ResourceVersion: bridgeVersion,
		ChangeType: "COLLECTION_REVERSED", Summary: changeSummary}, &parent, &parentVersion); err != nil {
		return err
	}
	operationSummary, _ := json.Marshal(map[string]any{"operation_id": scope.operationID,
		"bridge_id": candidate.BridgeID, "bridge_version": bridgeVersion,
		"reversal_id": proof.ReversalID, "provider_observation_id": observationID,
		"state": "SETTLED", "movement_certainty": "VERIFIED_RETURNED"})
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: job.Mode, ResourceType: "OPERATION",
		ResourceID: scope.operationID, ResourceVersion: scope.version,
		ChangeType: "COLLECTION_REVERSED", Summary: operationSummary}, &parent, &parentVersion); err != nil {
		return err
	}
	return CompleteWebhookJobTx(ctx, tx, job, "DONE", "COLLECTION_REVERSAL_APPLIED")
}

func collectionSettlementCredits(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	operationID string) (map[string]int64, int64, error) {
	rows, err := tx.Query(ctx, `SELECT l.account,sum(l.debit_cents)::text,sum(l.credit_cents)::text
		FROM servicing.ledger_transactions t JOIN servicing.ledger_lines l
		ON l.mode=t.mode AND l.transaction_id=t.id
		WHERE t.mode=$1 AND t.operation_id=$2 AND t.effect_key=$3
		AND t.transaction_kind='COLLECTION_SETTLEMENT' GROUP BY l.account`,
		string(mode), operationID, "collection-settlement:"+operationID)
	if err != nil {
		return nil, 0, errors.New("collection_settlement_ledger_read_failed")
	}
	defer rows.Close()
	credits := map[string]int64{}
	var collectedCash int64
	for rows.Next() {
		var account, debitText, creditText string
		if rows.Scan(&account, &debitText, &creditText) != nil {
			return nil, 0, errors.New("collection_settlement_ledger_read_failed")
		}
		debit, debitErr := strconv.ParseInt(debitText, 10, 64)
		credit, creditErr := strconv.ParseInt(creditText, 10, 64)
		if debitErr != nil || creditErr != nil || debit < 0 || credit < 0 {
			return nil, 0, errors.New("collection_settlement_ledger_amount_invalid")
		}
		switch account {
		case "COLLECTED_CASH":
			collectedCash = debit
		case "LATE_FEE_RECEIVABLE":
			credits["LATE_FEE"] = credit
		case "CONTRACT_FEE_RECEIVABLE":
			credits["CONTRACT_FEE"] = credit
		case "PRINCIPAL_RECEIVABLE":
			credits["PRINCIPAL"] = credit
		case "CUSTOMER_REFUND_PAYABLE":
			credits["CUSTOMER_REFUND_PAYABLE"] = credit
		}
	}
	if rows.Err() != nil {
		return nil, 0, errors.New("collection_settlement_ledger_read_failed")
	}
	if collectedCash <= 0 {
		return nil, 0, errors.New("collection_settlement_cash_line_missing")
	}
	return credits, collectedCash, nil
}

func applyCollectionReversalBucket(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	reversalID string, index int, bucket string, originalAvailable, reversalRemaining int64,
	prior map[string]int64) (int64, int, int64, error) {
	if originalAvailable < 0 || reversalRemaining < 0 {
		return 0, index, reversalRemaining, errors.New("collection_reversal_amount_invalid")
	}
	take := minInt64(originalAvailable, reversalRemaining)
	if take == 0 {
		return 0, index, reversalRemaining, nil
	}
	if err := insertCollectionReversalAllocation(ctx, tx, mode, reversalID, index, bucket, take); err != nil {
		return 0, index, reversalRemaining, err
	}
	prior[bucket] += take
	return take, index + 1, reversalRemaining - take, nil
}

func collectionReversalBucketTotals(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	operationID string) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `SELECT a.bucket,sum(a.amount_cents) FROM servicing.collection_reversal_allocations a
		JOIN servicing.collection_reversals r ON r.mode=a.mode AND r.id=a.reversal_id
		WHERE r.mode=$1 AND r.original_operation_id=$2 GROUP BY a.bucket`,
		string(mode), operationID)
	if err != nil {
		return nil, errors.New("collection_reversal_history_read_failed")
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var bucket string
		var amount int64
		if rows.Scan(&bucket, &amount) != nil {
			return nil, errors.New("collection_reversal_history_read_failed")
		}
		result[bucket] = amount
	}
	if rows.Err() != nil {
		return nil, errors.New("collection_reversal_history_read_failed")
	}
	return result, nil
}

func insertCollectionReversalAllocation(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	reversalID string, index int, bucket string, amount int64) error {
	if amount <= 0 {
		return errors.New("collection_reversal_allocation_invalid")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.collection_reversal_allocations(mode,
		reversal_id,allocation_index,bucket,amount_cents) VALUES($1,$2,$3,$4,$5)`,
		string(mode), reversalID, index, bucket, amount); err != nil {
		return errors.New("collection_reversal_allocation_insert_failed")
	}
	return nil
}

func restoreCollectionBacking(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID string, amount int64) (int64, error) {
	if amount <= 0 {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `SELECT a.source_system,a.invoice_id,a.active_cents,
		a.original_coverage_cents,r.unpaid_cents,r.reserved_cents,r.backing_hold
		FROM servicing.bridge_invoice_allocations a JOIN servicing.receivables r ON r.mode=a.mode
		AND r.source_system=a.source_system AND r.invoice_id=a.invoice_id
		WHERE a.mode=$1 AND a.bridge_id=$2 ORDER BY a.allocation_index,a.source_system,a.invoice_id
		FOR UPDATE OF a,r`, string(mode), bridgeID)
	if err != nil {
		return 0, errors.New("collection_reversal_backing_read_failed")
	}
	type backingRow struct {
		source, invoice                    string
		active, original, unpaid, reserved int64
		held                               bool
	}
	var allocations []backingRow
	for rows.Next() {
		var row backingRow
		if rows.Scan(&row.source, &row.invoice, &row.active, &row.original,
			&row.unpaid, &row.reserved, &row.held) != nil {
			rows.Close()
			return 0, errors.New("collection_reversal_backing_read_failed")
		}
		allocations = append(allocations, row)
	}
	if rows.Err() != nil {
		rows.Close()
		return 0, errors.New("collection_reversal_backing_read_failed")
	}
	rows.Close()
	remaining := amount
	var restored int64
	for _, allocation := range allocations {
		if remaining == 0 {
			break
		}
		if allocation.held || allocation.active >= allocation.original || allocation.unpaid <= allocation.reserved {
			continue
		}
		available := allocation.unpaid - allocation.reserved
		available = minInt64(available, allocation.original-allocation.active)
		take := minInt64(remaining, available)
		if take <= 0 {
			continue
		}
		tag, err := tx.Exec(ctx, `UPDATE servicing.bridge_invoice_allocations SET active_cents=active_cents+$4
			WHERE mode=$1 AND bridge_id=$2 AND source_system=$3 AND invoice_id=$5
			AND original_coverage_cents-active_cents>=$4`, string(mode), bridgeID,
			allocation.source, take, allocation.invoice)
		if err != nil || tag.RowsAffected() != 1 {
			return 0, errors.New("collection_reversal_bridge_backing_update_failed")
		}
		tag, err = tx.Exec(ctx, `UPDATE servicing.receivables SET reserved_cents=reserved_cents+$4
			WHERE mode=$1 AND source_system=$2 AND invoice_id=$3
			AND unpaid_cents-reserved_cents>=$4 AND NOT backing_hold`,
			string(mode), allocation.source, allocation.invoice, take)
		if err != nil || tag.RowsAffected() != 1 {
			return 0, errors.New("collection_reversal_invoice_backing_update_failed")
		}
		remaining -= take
		restored += take
	}
	return restored, nil
}

func restoreCollectionDebt(ctx context.Context, tx pgx.Tx, mode domain.Mode,
	bridgeID string, lateFee, contractFee, principal, backingDeficit int64,
	backingEvidenceHold, otherOpenBridge bool) (int64, error) {
	var version int64
	lifecycleCase := `CASE WHEN lifecycle='REPAID' AND $7::boolean THEN 'REOPENED_HELD'
		WHEN lifecycle='REPAID' THEN 'COLLECTING' ELSE lifecycle END`
	err := tx.QueryRow(ctx, `UPDATE servicing.bridges SET
		lifecycle=`+lifecycleCase+`,closed_at=CASE WHEN lifecycle='REPAID' THEN NULL ELSE closed_at END,
		principal_owed_cents=principal_owed_cents+$4,
		contract_fee_owed_cents=contract_fee_owed_cents+$3,
		late_fee_owed_cents=late_fee_owed_cents+$2,
		principal_paid_cents=principal_paid_cents-$4,
		contract_fee_paid_cents=contract_fee_paid_cents-$3,
		late_fee_paid_cents=late_fee_paid_cents-$2,
		cumulative_late_fee_paid_cents=cumulative_late_fee_paid_cents-$2,
		backing_deficit_cents=$5,
		hold_reasons=CASE WHEN ($5::bigint>0 OR $8::boolean) AND
			NOT ('RECEIVABLE_BACKING_CONFLICT'=ANY(hold_reasons))
			THEN array_append(hold_reasons,'RECEIVABLE_BACKING_CONFLICT') ELSE hold_reasons END,
		collection_debt_version=collection_debt_version+1,version=version+1,
		updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$6 AND principal_paid_cents>=$4
		AND contract_fee_paid_cents>=$3 AND late_fee_paid_cents>=$2
		AND cumulative_late_fee_paid_cents>=$2
		RETURNING version`, string(mode), lateFee, contractFee, principal, backingDeficit,
		bridgeID, otherOpenBridge, backingEvidenceHold).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errors.New("collection_reversal_bridge_debt_update_no_rows")
		}
		return 0, errors.New("collection_reversal_bridge_debt_update_failed")
	}
	return version, nil
}

func recordCollectionReversalHoldTx(ctx context.Context, tx pgx.Tx, job WebhookJobClaim,
	scope lockedOperationScope, customerBridgeIDs []string, reason string,
	outcome OperationReadOutcome) error {
	var raw []byte
	if outcome.Result != nil {
		raw = append([]byte(nil), outcome.Result.Receipt.RawEvidence...)
	}
	if len(raw) == 0 {
		raw = []byte(reason)
	}
	digest := sha256.Sum256(raw)
	normalized, _ := json.Marshal(map[string]any{"source": "COLLECTION_REVERSAL_REVIEW",
		"operation_id": scope.operationID, "webhook_event_id": job.EventID,
		"reason": reason, "raw_evidence_base64": base64.StdEncoding.EncodeToString(raw)})
	var objectID any
	if scope.providerObjectID != nil {
		objectID = *scope.providerObjectID
	}
	var observationID string
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.provider_observations(mode,operation_id,
		observation_kind,provider_object_id,source_account_ref,destination_account_ref,
		request_id,status,amount_cents,currency,effective_at,observed_at,evidence_hash,
		normalized_evidence) VALUES($1,$2,'TRANSFER',$3,$4,$5,$6,'REVERSAL_REVIEW',NULL,'USD',NULL,
		clock_timestamp(),$7,$8::jsonb)`, string(job.Mode), scope.operationID,
		objectID, scope.sourceAccount, scope.destinationAccount,
		scope.requestID, hex.EncodeToString(digest[:]), string(normalized)); err != nil {
		return errors.New("collection_reversal_review_observation_insert_failed")
	}
	if len(customerBridgeIDs) == 0 {
		return errors.New("collection_reversal_parent_locks_missing")
	}
	if err := holdCustomerCollections(ctx, tx, job.Mode, scope.customerID, customerBridgeIDs,
		reason, map[string]any{"operation_id": scope.operationID,
			"provider_observation_id": observationID, "webhook_event_id": job.EventID,
			"movement_certainty": "UNCERTAIN"}); err != nil {
		return err
	}
	var version int64
	if err := tx.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode=$1 AND id=$2`,
		string(job.Mode), scope.bridgeID).Scan(&version); err != nil {
		return errors.New("collection_reversal_review_bridge_version_read_failed")
	}
	if err := AppendOperationJournal(ctx, tx, job.Mode, scope.operationID,
		"OBSERVATION_RECORDED", string(domain.OperationSettled), string(domain.OperationSettled),
		reason, nil, nil, &observationID, map[string]any{"reversal_review": true,
			"webhook_event_id": job.EventID, "movement_certainty": "UNCERTAIN"}); err != nil {
		return err
	}
	parent, parentVersion := scope.bridgeID, version
	changeSummary, _ := json.Marshal(map[string]any{"bridge_id": scope.bridgeID,
		"bridge_version": version, "operation_id": scope.operationID,
		"provider_observation_id": observationID, "state": "SETTLED",
		"hold_code": reason})
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: job.Mode, ResourceType: "OPERATION",
		ResourceID: scope.operationID, ResourceVersion: scope.version,
		ChangeType: reason, Summary: changeSummary}, &parent, &parentVersion); err != nil {
		return err
	}
	return CompleteWebhookJobTx(ctx, tx, job, "HELD", reason)
}
