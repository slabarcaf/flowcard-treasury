package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

// FinishFundingOperation records the provider outcome and applies verified FUND
// settlement accounting in one short transaction. Other outcomes stay on the
// generic fenced finish path.
func (r *Repository) FinishFundingOperation(ctx context.Context, claim OperationClaim,
	outcome OperationReadOutcome, clock func() time.Time, pricing domain.Pricing,
	policyHash string) error {
	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	if err := FinishFundingOperationTx(txCtx, tx, claim, outcome, clock, pricing, policyHash); err != nil {
		return err
	}
	if err := tx.Commit(txCtx); err != nil {
		return errors.New("funding_operation_finish_commit_failed")
	}
	return nil
}

// FinishFundingOperationTx shares the operation lease fence with settlement
// booking. CREATE receipts never enter the settlement reducer.
func FinishFundingOperationTx(ctx context.Context, tx pgx.Tx, claim OperationClaim,
	outcome OperationReadOutcome, clock func() time.Time, pricing domain.Pricing,
	policyHash string) error {
	if claim.Kind != "FUND" || tx == nil || clock == nil {
		return errors.New("funding_settlement_request_invalid")
	}
	if outcome.Source != "RETRIEVE" && outcome.Source != "LOOKUP" {
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	scope, err := lockOperationScope(ctx, tx, claim.OperationCandidate)
	if err != nil {
		return err
	}
	if scope.leaseToken == nil || scope.leaseExpiresAt == nil || *scope.leaseToken != claim.LeaseToken ||
		!scope.leaseExpiresAt.Equal(claim.LeaseExpiresAt) {
		return ErrOperationClaimStale
	}
	plan, err := planOperationOutcome(scope, outcome)
	if err != nil || scope.guardCode != "" {
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	if scope.everFunded || scope.reservationState == "CONVERTED" || scope.lifecycle == "VOID_UNFUNDED" ||
		scope.reservationState == "RELEASED" {
		outcome.HoldCode = "FUNDING_TERMINAL_CONFLICT"
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	applyBoundEvidenceConflicts(scope, outcome.Source, &plan)
	if plan.state == domain.OperationHeld || plan.receipt == nil || plan.providerStatus != "SETTLED" ||
		plan.recovery != provider.RecoveryVerifyFinal || plan.certainty != provider.CertaintyDefinitive {
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	receipt := *plan.receipt
	if receipt.SettledAt == nil || receipt.ProviderFeeCents == nil || receipt.ObservedSourceAccountRef == nil ||
		*receipt.ObservedSourceAccountRef != scope.sourceAccount {
		outcome.HoldCode = "FUNDING_SETTLEMENT_EVIDENCE_INCOMPLETE"
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	if !fundingTimeUTC(receipt.ObservedAt) || !fundingTimeUTC(*receipt.SettledAt) ||
		receipt.SettledAt.After(receipt.ObservedAt.Add(5*time.Second)) ||
		receipt.SettledAt.Before(scope.createdAt.Add(-5*time.Second)) {
		outcome.HoldCode = "FUNDING_SETTLEMENT_TIME_INVALID"
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	if *receipt.ProviderFeeCents < 0 || scope.reservedProviderFee == nil || *scope.reservedProviderFee < 0 {
		outcome.HoldCode = "FUNDING_SETTLEMENT_FEE_INVALID"
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	if scope.state != string(claim.State) || scope.kind != "FUND" || scope.reservationID == "" ||
		scope.reservationState != "DISPATCHED" || scope.lifecycle != "FUNDING_PENDING" || scope.everFunded ||
		scope.providerObjectID != nil && *scope.providerObjectID != receipt.ProviderObjectID {
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	if err := pricing.Validate(); err != nil || !validHash(policyHash) {
		return errors.New("funding_settlement_pricing_unavailable")
	}
	now := clock().UTC()
	if now.IsZero() || receipt.ObservedAt.After(now.Add(5*time.Second)) || receipt.SettledAt.After(now.Add(5*time.Second)) {
		outcome.HoldCode = "FUNDING_SETTLEMENT_TIME_INVALID"
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	var principalOwed, contractOwed, lateOwed, principalPaid, contractPaid, latePaid int64
	var originalPrincipal, originalContractFee int64
	var dueOn time.Time
	var holdReasons []string
	err = tx.QueryRow(ctx, `SELECT principal_original_cents,contract_fee_original_cents,due_on,
		principal_owed_cents,contract_fee_owed_cents,late_fee_owed_cents,
		principal_paid_cents,contract_fee_paid_cents,late_fee_paid_cents,hold_reasons
		FROM servicing.bridges WHERE mode=$1 AND id=$2`, string(claim.Mode), scope.bridgeID).Scan(
		&originalPrincipal, &originalContractFee, &dueOn, &principalOwed, &contractOwed,
		&lateOwed, &principalPaid, &contractPaid, &latePaid, &holdReasons)
	if err != nil {
		return errors.New("funding_settlement_bridge_read_failed")
	}
	if originalPrincipal != scope.amount || originalContractFee != scope.terms.ContractFeeCents ||
		principalOwed != 0 || contractOwed != 0 || lateOwed != 0 || principalPaid != 0 ||
		contractPaid != 0 || latePaid != 0 || scope.amount <= 0 || scope.terms.ContractFeeCents < 0 {
		return FinishOperationTx(ctx, tx, claim, outcome)
	}
	var facilityReserved, facilityOwed, customerReserved, customerReservedFee, customerOwed, customerFees int64
	if err := tx.QueryRow(ctx, `SELECT reserved_principal_cents,principal_owed_cents
		FROM servicing.facilities WHERE mode=$1 AND id=$2`, string(claim.Mode), scope.facilityID).Scan(
		&facilityReserved, &facilityOwed); err != nil {
		return errors.New("funding_settlement_facility_read_failed")
	}
	if err := tx.QueryRow(ctx, `SELECT reserved_principal_cents,reserved_fee_cents,principal_owed_cents,unpaid_fee_cents
		FROM servicing.customers WHERE mode=$1 AND id=$2`, string(claim.Mode), scope.customerID).Scan(
		&customerReserved, &customerReservedFee, &customerOwed, &customerFees); err != nil {
		return errors.New("funding_settlement_customer_read_failed")
	}
	principal, contractFee := scope.amount, scope.terms.ContractFeeCents
	actualFee := *receipt.ProviderFeeCents
	if facilityReserved < principal || customerReserved < principal || customerReservedFee < contractFee ||
		facilityOwed > math.MaxInt64-principal || customerOwed > math.MaxInt64-principal ||
		customerFees > math.MaxInt64-contractFee || principal > math.MaxInt64-contractFee {
		return errors.New("funding_settlement_counter_invalid")
	}
	var reservationPrincipal, reservationCustomerPrincipal, reservationFee, invoiceCoverage int64
	if err := tx.QueryRow(ctx, `SELECT facility_principal_cents,customer_principal_cents,
		customer_fee_cents,invoice_coverage_cents FROM servicing.reservations
		WHERE mode=$1 AND id=$2 AND state='DISPATCHED'`, string(claim.Mode), scope.reservationID).Scan(
		&reservationPrincipal, &reservationCustomerPrincipal, &reservationFee, &invoiceCoverage); err != nil ||
		reservationPrincipal != principal || reservationCustomerPrincipal != principal ||
		reservationFee != contractFee || invoiceCoverage != principal+contractFee {
		return errors.New("funding_settlement_reservation_invalid")
	}
	lateFunding := false
	var reservationExpires, offerExpires, offeredOn time.Time
	if err := tx.QueryRow(ctx, `SELECT r.expires_at,o.expires_at,o.offered_on
		FROM servicing.reservations r JOIN servicing.offers o ON o.mode=r.mode AND o.id=$3
		WHERE r.mode=$1 AND r.id=$2`, string(claim.Mode), scope.reservationID, scope.offerID).Scan(
		&reservationExpires, &offerExpires, &offeredOn); err != nil {
		return errors.New("funding_settlement_expiry_read_failed")
	}
	lateFunding = receipt.SettledAt.After(reservationExpires) || receipt.SettledAt.After(offerExpires) ||
		receipt.SettledAt.UTC().Format("2006-01-02") != offeredOn.UTC().Format("2006-01-02") ||
		receipt.SettledAt.After(dueOn.Add(24*time.Hour))
	feeOverage := actualFee > *scope.reservedProviderFee
	newHolds := make([]string, len(holdReasons))
	copy(newHolds, holdReasons)
	if lateFunding {
		newHolds = appendUniqueString(newHolds, "LATE_FUNDING_SETTLEMENT")
	}
	if feeOverage {
		newHolds = appendUniqueString(newHolds, "SOURCE_FEE_OVERAGE")
	}
	var operationVersion int64
	err = tx.QueryRow(ctx, `UPDATE servicing.operations SET state='SETTLED',version=version+1,
		lease_token=NULL,lease_expires_at=NULL,provider_object_id=COALESCE(provider_object_id,$7),
		provider_status='SETTLED',provider_fee_cents=$8,sanitized_error_code=NULL,
		next_check_at=NULL,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state=$3 AND version=$4 AND lease_token=$5::uuid
		AND lease_expires_at=$6 AND lease_expires_at>clock_timestamp()
		AND (provider_object_id IS NULL OR provider_object_id=$7)
		AND (provider_fee_cents IS NULL OR provider_fee_cents=$8)
		RETURNING version`, string(claim.Mode), claim.OperationID, string(claim.State), claim.Version,
		claim.LeaseToken, claim.LeaseExpiresAt, receipt.ProviderObjectID, actualFee).Scan(&operationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOperationClaimStale
	}
	if err != nil {
		return errors.New("funding_settlement_operation_cas_failed")
	}
	observationID, err := persistOperationOutcome(ctx, tx, scope, plan, outcome.Source)
	if err != nil {
		return err
	}
	bridgeRef, operationRef := scope.bridgeID, scope.operationID
	checkID := scope.lastFundingCheckID
	provenance, err := json.Marshal(map[string]any{"mode": string(claim.Mode),
		"operation_id": scope.operationID, "request_id": scope.requestID,
		"provider_object_id": receipt.ProviderObjectID, "source_account_ref": scope.sourceAccount,
		"destination_account_ref": scope.destinationAccount, "amount_cents": principal,
		"contract_fee_cents": contractFee, "provider_fee_cents": actualFee,
		"provider_fee_reserved_cents": *scope.reservedProviderFee,
		"terms_hash":                  scope.termsHash, "funding_check_id": checkID,
		"provider_observation_id": observationID, "observed_at": receipt.ObservedAt.UTC(),
		"effective_settled_at": receipt.SettledAt.UTC(), "source": outcome.Source,
		"late_settlement": lateFunding, "source_fee_overage": feeOverage})
	if err != nil {
		return errors.New("funding_settlement_provenance_invalid")
	}
	lines := []domain.LedgerLine{
		{Account: "PRINCIPAL_RECEIVABLE", Debit: domain.Money(principal)},
		{Account: "FACILITY_CASH", Credit: domain.Money(principal)},
	}
	if contractFee > 0 {
		lines = append(lines,
			domain.LedgerLine{Account: "CONTRACT_FEE_RECEIVABLE", Debit: domain.Money(contractFee)},
			domain.LedgerLine{Account: "FEE_INCOME", Credit: domain.Money(contractFee)})
	}
	if actualFee > 0 {
		if actualFee > math.MaxInt64-principal {
			return errors.New("funding_settlement_cash_overflow")
		}
		lines = append(lines,
			domain.LedgerLine{Account: "PROVIDER_FEE_EXPENSE", Debit: domain.Money(actualFee)},
			domain.LedgerLine{Account: "FACILITY_CASH", Credit: domain.Money(actualFee)})
	}
	reservedHold, ok := checkedFundingAdd(principal, *scope.reservedProviderFee)
	if !ok {
		return errors.New("funding_settlement_commitment_overflow")
	}
	lines = append(lines,
		domain.LedgerLine{Account: "COMMITMENT_MEMO", Debit: domain.Money(reservedHold)},
		domain.LedgerLine{Account: "COMMITMENT_MEMO", Credit: domain.Money(reservedHold)})
	effectKey := "fund-settlement:" + scope.operationID
	if err := InsertLedgerTransaction(ctx, tx, claim.Mode, effectKey, "FUNDING_SETTLEMENT",
		&bridgeRef, &operationRef, receipt.SettledAt.UTC().Format(time.RFC3339Nano), provenance, lines); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE servicing.facilities SET
		reserved_principal_cents=reserved_principal_cents-$3,
		principal_owed_cents=principal_owed_cents+$3,version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND reserved_principal_cents >= $3`,
		string(claim.Mode), scope.facilityID, principal)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_settlement_facility_update_failed")
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.customers SET
		reserved_principal_cents=reserved_principal_cents-$3,reserved_fee_cents=reserved_fee_cents-$4,
		principal_owed_cents=principal_owed_cents+$3,unpaid_fee_cents=unpaid_fee_cents+$4,
		version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND reserved_principal_cents >= $3 AND reserved_fee_cents >= $4`,
		string(claim.Mode), scope.customerID, principal, contractFee)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_settlement_customer_update_failed")
	}
	tag, err = tx.Exec(ctx, `UPDATE servicing.reservations SET state='CONVERTED',updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND state='DISPATCHED'`, string(claim.Mode), scope.reservationID)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("funding_settlement_reservation_update_failed")
	}
	var bridgeVersion int64
	err = tx.QueryRow(ctx, `UPDATE servicing.bridges SET lifecycle='ACTIVE',ever_funded=true,
		principal_owed_cents=$3,contract_fee_owed_cents=$4,funded_at=$5,hold_reasons=$6,
		version=version+1,collection_debt_version=collection_debt_version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND id=$2 AND lifecycle='FUNDING_PENDING' AND NOT ever_funded
		AND principal_owed_cents=0 AND contract_fee_owed_cents=0 AND late_fee_owed_cents=0
		RETURNING version`, string(claim.Mode), scope.bridgeID, principal, contractFee,
		receipt.SettledAt.UTC(), newHolds).Scan(&bridgeVersion)
	if err != nil {
		return errors.New("funding_settlement_bridge_update_failed")
	}
	if err := AppendOperationJournal(ctx, tx, claim.Mode, claim.OperationID, "OBSERVATION_RECORDED",
		string(claim.State), "SETTLED", "", &claim.LeaseToken, &claim.LeaseExpiresAt,
		&observationID, map[string]any{"source": outcome.Source, "provider_status": "SETTLED",
			"provider_fee_cents": actualFee, "funding_check_id": checkID,
			"movement_certainty": "VERIFIED_SETTLED", "late_settlement": lateFunding,
			"source_fee_overage": feeOverage}); err != nil {
		return err
	}
	scope.state = string(domain.OperationSettled)
	scope.version = operationVersion
	scope.bridgeVersion = bridgeVersion
	details := map[string]any{"state": "SETTLED", "status": "ACTIVE",
		"funding_check_id": checkID, "provider_observation_id": observationID,
		"provider_fee_cents": actualFee, "effective_at": receipt.SettledAt.UTC(),
		"terms_hash": scope.termsHash, "late_settlement": lateFunding,
		"source_fee_overage": feeOverage, "hold_reasons": newHolds}
	if err := appendBridgeOperationChange(ctx, tx, scope, bridgeVersion, "FUNDING_SETTLED", details); err != nil {
		return err
	}
	if err := appendOperationChange(ctx, tx, scope, operationVersion, bridgeVersion, "FUNDING_SETTLED", details); err != nil {
		return err
	}
	mode := domain.Mode(claim.Mode)
	_, err = AccrueThrough(ctx, tx, AccrualOptions{Mode: mode, Pricing: pricing,
		ExpectedPolicyHash: policyHash}, scope.bridgeID, now)
	if err != nil {
		return errors.New("funding_settlement_accrual_failed")
	}
	if err := RefreshDelinquency(ctx, tx, mode, scope.bridgeID, now); err != nil {
		return errors.New("funding_settlement_delinquency_failed")
	}
	return nil
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
