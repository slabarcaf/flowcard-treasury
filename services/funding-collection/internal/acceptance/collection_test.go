package acceptance_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestCollectionReversalAndAccrual(t *testing.T) {
	ctx, h := newJourney(t)
	funding := settledFunding(t, ctx, h)
	firstDeposit, secondDeposit := repayFunding(t, ctx, h, funding)
	type allocation struct {
		DepositID string
		Bucket    string
		Amount    int64
	}
	var allocations []allocation
	rows, err := h.Pool.Query(ctx, `SELECT d.provider_deposit_id,a.bucket,a.amount_cents
		FROM servicing.collection_allocations a
		JOIN servicing.collection_attempts c ON c.mode=a.mode AND c.id=a.attempt_id
		JOIN servicing.deposits d ON d.mode=c.mode AND d.id=c.deposit_id
		WHERE a.mode='DEMO' AND c.bridge_id=$1
		ORDER BY d.provider_deposit_id,c.created_at,c.id,a.allocation_index`, funding.BridgeID)
	if err != nil {
		t.Fatalf("read actual collection allocations: %v", err)
	}
	for rows.Next() {
		var got allocation
		if err := rows.Scan(&got.DepositID, &got.Bucket, &got.Amount); err != nil {
			rows.Close()
			t.Fatalf("scan collection allocation: %v", err)
		}
		allocations = append(allocations, got)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read collection allocation rows: %v", err)
	}
	rows.Close()
	wantAllocations := []allocation{
		{DepositID: firstDeposit.DepositID, Bucket: "CONTRACT_FEE", Amount: 10_000},
		{DepositID: secondDeposit.DepositID, Bucket: "CONTRACT_FEE", Amount: 9_000},
		{DepositID: secondDeposit.DepositID, Bucket: "PRINCIPAL", Amount: 700_000},
	}
	if len(allocations) != len(wantAllocations) {
		t.Fatalf("collection allocations = %+v, want $100 fee then $90 fee and $7000 principal", allocations)
	}
	for i, got := range allocations {
		want := wantAllocations[i]
		if got != want {
			var originalFee, feePaid, feeOwed, principalPaid, principalOwed int64
			diagnosticErr := h.Pool.QueryRow(ctx, `SELECT contract_fee_original_cents,contract_fee_paid_cents,
				contract_fee_owed_cents,principal_paid_cents,principal_owed_cents
				FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).
				Scan(&originalFee, &feePaid, &feeOwed, &principalPaid, &principalOwed)
			t.Fatalf("collection allocation %d = %+v, want %+v (all=%+v fee=%d/%d/%d principal=%d/%d diagnostic=%v)",
				i, got, want, allocations, feePaid, feeOwed, originalFee, principalPaid, principalOwed, diagnosticErr)
		}
	}

	firstCollectionRequest, ok := findCreateRequest(h.ProviderHTTP.Requests(), 1)
	if !ok {
		t.Fatal("first fee collection create request is missing")
	}
	var firstCollectionIntent struct {
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(firstCollectionRequest.Body, &firstCollectionIntent) != nil || firstCollectionIntent.RequestID == "" {
		t.Fatalf("first fee collection request body = %s, want its immutable request ID", firstCollectionRequest.Body)
	}
	providerObjectID, ok := h.ProviderHTTP.ObjectIDForRequest(firstCollectionIntent.RequestID)
	if !ok {
		t.Fatal("provider fixture did not retain the first fee collection identity")
	}
	originalSettledAt, ok := h.ProviderHTTP.TransferSettledAt(providerObjectID)
	if !ok {
		t.Fatal("provider fixture did not retain the original settlement timestamp")
	}
	reversedAt := originalSettledAt.Add(time.Second)
	if err := h.ProviderHTTP.SetTransferReversal(providerObjectID, 10_000, reversedAt); err != nil {
		t.Fatalf("set provider fee reversal fact: %v", err)
	}
	if err := sendTransferWake(h, "journey-fee-reversal-wake", firstCollectionIntent.RequestID); err != nil {
		t.Fatalf("signed collection reversal wake: %v", err)
	}
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatalf("worker verified terminal collection reversal: %v", err)
	}
	var reversalBucket string
	var reversalAmount int64
	if err := h.Pool.QueryRow(ctx, `SELECT a.bucket,a.amount_cents
		FROM servicing.collection_reversal_allocations a
		JOIN servicing.collection_reversals r ON r.mode=a.mode AND r.id=a.reversal_id
		WHERE a.mode='DEMO' AND r.original_operation_id=(SELECT id FROM servicing.operations
			WHERE mode='DEMO' AND request_id=$1) ORDER BY a.allocation_index LIMIT 1`,
		firstCollectionIntent.RequestID).Scan(&reversalBucket, &reversalAmount); err != nil ||
		reversalBucket != "CONTRACT_FEE" || reversalAmount != 10_000 {
		t.Fatalf("actual reversal allocation = %s/%d err %v, want CONTRACT_FEE/10000", reversalBucket, reversalAmount, err)
	}
	var effectiveAt, observedAt, recordedAt time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT reversed_at,observed_at,recorded_at
		FROM servicing.collection_reversals WHERE mode='DEMO' AND original_operation_id=(
			SELECT id FROM servicing.operations WHERE mode='DEMO' AND request_id=$1)`,
		firstCollectionIntent.RequestID).Scan(&effectiveAt, &observedAt, &recordedAt); err != nil ||
		!effectiveAt.Before(observedAt) || observedAt.After(recordedAt) {
		t.Fatalf("reversal effective/observed/recorded times = %s/%s/%s err %v, want immutable backdated source time", effectiveAt, observedAt, recordedAt, err)
	}
	bridgeResponse, err := h.Request(http.MethodGet, "/api/bridges/"+funding.BridgeID,
		"coordinator-reader", servicefixture.CoordinatorKey, "", nil)
	if err != nil || bridgeResponse.StatusCode != http.StatusOK {
		t.Fatalf("reopened bridge read status = %v err %v, want 200", journeyResponseStatus(bridgeResponse), err)
	}
	var reopened struct {
		Lifecycle       string   `json:"lifecycle"`
		ContractFeeOwed *int64   `json:"contract_fee_owed_cents"`
		HoldReasons     []string `json:"hold_reasons"`
	}
	if err := json.NewDecoder(bridgeResponse.Body).Decode(&reopened); err != nil {
		bridgeResponse.Body.Close()
		t.Fatalf("decode reversal bridge read: %v", err)
	}
	bridgeResponse.Body.Close()
	if reopened.Lifecycle != "COLLECTING" || reopened.ContractFeeOwed == nil ||
		*reopened.ContractFeeOwed != 10_000 || len(reopened.HoldReasons) == 0 {
		t.Fatalf("reversal bridge state = %+v, want held $100 contract-fee debt", reopened)
	}

	principalCollectionRequest, ok := findCreateRequest(h.ProviderHTTP.Requests(), 3)
	if !ok {
		t.Fatal("settled principal collection create request is missing")
	}
	var principalCollectionIntent struct {
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(principalCollectionRequest.Body, &principalCollectionIntent) != nil || principalCollectionIntent.RequestID == "" {
		t.Fatalf("principal collection request body = %s, want its immutable request ID", principalCollectionRequest.Body)
	}
	principalObjectID, ok := h.ProviderHTTP.ObjectIDForRequest(principalCollectionIntent.RequestID)
	if !ok {
		t.Fatal("provider fixture did not retain the principal collection identity")
	}
	principalReversedAt, ok := h.ProviderHTTP.TransferSettledAt(principalObjectID)
	if !ok {
		t.Fatal("provider fixture did not retain the principal settlement time")
	}
	principalReversedAt = principalReversedAt.Add(time.Second)
	if err := h.ProviderHTTP.SetTransferReversal(principalObjectID, 350_000, principalReversedAt); err != nil {
		t.Fatalf("set provider principal reversal fact: %v", err)
	}
	if err := sendTransferWake(h, "journey-principal-reversal-wake", principalCollectionIntent.RequestID); err != nil {
		t.Fatalf("signed principal reversal wake: %v", err)
	}
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatalf("worker verified terminal principal reversal: %v", err)
	}
	var principalBucket string
	var principalReversalAmount int64
	if err := h.Pool.QueryRow(ctx, `SELECT a.bucket,a.amount_cents
		FROM servicing.collection_reversal_allocations a
		JOIN servicing.collection_reversals r ON r.mode=a.mode AND r.id=a.reversal_id
		WHERE a.mode='DEMO' AND r.original_operation_id=(SELECT id FROM servicing.operations
			WHERE mode='DEMO' AND request_id=$1) ORDER BY a.allocation_index LIMIT 1`,
		principalCollectionIntent.RequestID).Scan(&principalBucket, &principalReversalAmount); err != nil ||
		principalBucket != "PRINCIPAL" || principalReversalAmount != 350_000 {
		t.Fatalf("principal reversal allocation = %s/%d err %v, want PRINCIPAL/350000", principalBucket, principalReversalAmount, err)
	}

	var dueOn time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT due_on FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).Scan(&dueOn); err != nil {
		t.Fatalf("read original due date for accrual catch-up: %v", err)
	}
	logicalAsOf := time.Date(dueOn.UTC().Year(), dueOn.UTC().Month(), dueOn.UTC().Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 2)
	// This is a logical business date passed to the accrual routine; the test
	// leaves PostgreSQL and captured provider/source clocks untouched.
	accrualTx, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin accrual transaction: %v", err)
	}
	defer accrualTx.Rollback(ctx)
	if err := postgres.LockFacilityForCollectionReview(ctx, accrualTx, config.ModeDemo,
		servicefixture.FacilityID, servicefixture.CustomerID, funding.BridgeID); err != nil {
		t.Fatalf("take servicing accrual locks: %v", err)
	}
	accrual, err := postgres.AccrueThrough(ctx, accrualTx, postgres.AccrualOptions{
		Mode: domain.ModeDemo, Pricing: h.Evaluator.Pricing(), ExpectedPolicyHash: h.Evaluator.PolicyHash(),
	}, funding.BridgeID, logicalAsOf)
	if err != nil || !accrual.Current || accrual.Pending || accrual.DaysApplied != 2 {
		t.Fatalf("AccrueThrough(%s) = %+v err %v, want two current opening-principal days", logicalAsOf, accrual, err)
	}
	rows, err = accrualTx.Query(ctx, `SELECT accrual_on,opening_principal_cents,cumulative_numerator::text,assessed_delta_cents
		FROM servicing.accrual_days WHERE mode='DEMO' AND bridge_id=$1 ORDER BY accrual_on`, funding.BridgeID)
	if err != nil {
		t.Fatalf("read persisted accrual days: %v", err)
	}
	var accrualDates []time.Time
	var cumulativeNumerators []string
	var accrualDeltas []int64
	for rows.Next() {
		var date time.Time
		var opening, delta int64
		var numerator string
		if err := rows.Scan(&date, &opening, &numerator, &delta); err != nil {
			rows.Close()
			t.Fatalf("scan persisted accrual day: %v", err)
		}
		if opening != 350_000 {
			rows.Close()
			t.Fatalf("persisted opening principal = %d, want 350000", opening)
		}
		accrualDates = append(accrualDates, date)
		cumulativeNumerators = append(cumulativeNumerators, numerator)
		accrualDeltas = append(accrualDeltas, delta)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read persisted accrual days: %v", err)
	}
	rows.Close()
	wantDayOne := time.Date(dueOn.UTC().Year(), dueOn.UTC().Month(), dueOn.UTC().Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	wantDayTwo := wantDayOne.AddDate(0, 0, 1)
	if len(accrualDates) != 2 || !accrualDates[0].Equal(wantDayOne) || !accrualDates[1].Equal(wantDayTwo) ||
		cumulativeNumerators[0] != "70000000" || cumulativeNumerators[1] != "140000000" ||
		accrualDeltas[0] != 233 || accrualDeltas[1] != 234 {
		t.Fatalf("persisted UTC accrual days=%v numerators=%v deltas=%v, want due+1/due+2 with cumulative 467 cents", accrualDates, cumulativeNumerators, accrualDeltas)
	}
	var lateFeeOwed, assessedLateFees int64
	var cursor time.Time
	if err := accrualTx.QueryRow(ctx, `SELECT late_fee_owed_cents,cumulative_late_fee_assessed_cents,last_accrued_on
		FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).Scan(&lateFeeOwed, &assessedLateFees, &cursor); err != nil ||
		lateFeeOwed != 467 || assessedLateFees != 467 || !cursor.Equal(wantDayTwo) {
		t.Fatalf("persisted accrual debt=%d assessed=%d cursor=%s err=%v, want 467 cents through due+2", lateFeeOwed, assessedLateFees, cursor, err)
	}
	var activeBacking, invoiceBacking int64
	if err := accrualTx.QueryRow(ctx, `SELECT COALESCE(sum(active_cents),0) FROM servicing.bridge_invoice_allocations
		WHERE mode='DEMO' AND bridge_id=$1`, funding.BridgeID).Scan(&activeBacking); err != nil {
		t.Fatalf("read bridge backing after accrual: %v", err)
	}
	if err := accrualTx.QueryRow(ctx, `SELECT reserved_cents FROM servicing.receivables
		WHERE mode='DEMO' AND source_system=$1 AND invoice_id=$2`, servicefixture.InvoiceSystem,
		servicefixture.InvoiceID).Scan(&invoiceBacking); err != nil || activeBacking != 360_000 || invoiceBacking != 360_000 {
		t.Fatalf("fee accrual changed principal/contract backing: bridge=%d invoice=%d err=%v, want both 360000", activeBacking, invoiceBacking, err)
	}
	if err := accrualTx.Commit(ctx); err != nil {
		t.Fatalf("commit persisted accrual catch-up: %v", err)
	}
	replayTx, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin accrual replay transaction: %v", err)
	}
	defer replayTx.Rollback(ctx)
	if err := postgres.LockFacilityForCollectionReview(ctx, replayTx, config.ModeDemo,
		servicefixture.FacilityID, servicefixture.CustomerID, funding.BridgeID); err != nil {
		t.Fatalf("take accrual replay locks: %v", err)
	}
	replayAccrual, err := postgres.AccrueThrough(ctx, replayTx, postgres.AccrualOptions{
		Mode: domain.ModeDemo, Pricing: h.Evaluator.Pricing(), ExpectedPolicyHash: h.Evaluator.PolicyHash(),
	}, funding.BridgeID, logicalAsOf)
	if err != nil || replayAccrual.DaysApplied != 0 || replayAccrual.Pending || !replayAccrual.Current {
		t.Fatalf("AccrueThrough replay = %+v err %v, want current zero-day replay", replayAccrual, err)
	}
	var accrualDayCount, replayAssessedFees int64
	if err := replayTx.QueryRow(ctx, `SELECT count(*),sum(assessed_delta_cents)
		FROM servicing.accrual_days WHERE mode='DEMO' AND bridge_id=$1`, funding.BridgeID).
		Scan(&accrualDayCount, &replayAssessedFees); err != nil || accrualDayCount != 2 || replayAssessedFees != 467 {
		t.Fatalf("accrual replay rows=%d fees=%d err=%v, want two rows and unchanged 467 cents", accrualDayCount, replayAssessedFees, err)
	}
	if err := replayTx.Commit(ctx); err != nil {
		t.Fatalf("commit accrual replay transaction: %v", err)
	}

	unsupportedRaw := []byte(`{"id":"journey-unsupported-event","name":"UNRECOGNIZED_EVENT","data":{}}`)
	unsupportedResponse, err := signedServicingWebhook(h, unsupportedRaw)
	if err != nil || unsupportedResponse.StatusCode != http.StatusOK {
		t.Fatalf("unsupported signed event receipt status = %v err %v, want durable 200 receipt", journeyResponseStatus(unsupportedResponse), err)
	}
	unsupportedResponse.Body.Close()
	var heldJobs int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM servicing.webhook_jobs WHERE mode='DEMO' AND event_id='journey-unsupported-event' AND state='HELD'`).Scan(&heldJobs); err != nil || heldJobs != 1 {
		t.Fatalf("unsupported webhook held jobs = %d err %v, want one safe held event", heldJobs, err)
	}
}
