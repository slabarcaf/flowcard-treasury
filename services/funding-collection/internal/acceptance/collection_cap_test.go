package acceptance_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestCollectionChangedCapCancelsOnlyUnsentIntent(t *testing.T) {
	ctx, h := newJourney(t)
	funding := settledFunding(t, ctx, h)
	settledAt := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	if wait := time.Until(settledAt); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	zeroFee := int64(0)
	deposit := provider.DepositObservation{AccountRef: servicefixture.BorrowerAccount,
		DepositID: "changed-cap-deposit", CustomerID: servicefixture.CustomerID, Currency: "USD",
		GrossCents: 10_000, NetCents: 10_000, ProviderFeeCents: &zeroFee,
		Status: "SETTLED", Kind: "EXTERNAL_SETTLED", SettledAt: &settledAt, ObservedAt: settledAt}
	h.ProviderHTTP.SetDeposit(deposit)
	h.ProviderHTTP.SetBalance(servicefixture.BorrowerAccount, 10_000)
	response, err := sendDepositWake(h, "changed-cap-wake", deposit.DepositID)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("deposit wake status=%v err=%v, want 200", journeyResponseStatus(response), err)
	}
	response.Body.Close()
	// One pass stores the verified deposit; the next reserves a fee debit.
	for i := 0; i < 2; i++ {
		if _, err := h.Worker.ProcessOne(ctx); err != nil {
			t.Fatalf("prepare collection pass %d: %v", i, err)
		}
	}
	var operationID, requestID, state string
	var amount int64
	if err := h.Pool.QueryRow(ctx, `SELECT id,request_id,state,amount_cents FROM servicing.operations
		WHERE mode='DEMO' AND bridge_id=$1 AND kind='COLLECT_FEE'`, funding.BridgeID).
		Scan(&operationID, &requestID, &state, &amount); err != nil || state != "QUEUED" || amount != 10_000 {
		t.Fatalf("planned collection=%s/%s/%d err=%v, want queued 10000-cent intent", operationID, state, amount, err)
	}
	if h.ProviderHTTP.CreateRequestCount() != 1 {
		t.Fatal("collection planning sent a provider create")
	}
	// Provider-owned liquidity falls after reservation, before the first send.
	h.ProviderHTTP.SetBalance(servicefixture.BorrowerAccount, 0)
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatalf("changed-cap dispatch: %v", err)
	}
	var afterRequest string
	var afterAmount, reservedDebit int64
	if err := h.Pool.QueryRow(ctx, `SELECT o.request_id,o.state,o.amount_cents,ca.reserved_debit_cents
		FROM servicing.operations o JOIN servicing.collection_allocations a ON a.mode=o.mode AND a.operation_id=o.id
		JOIN servicing.collection_attempts ca ON ca.mode=a.mode AND ca.id=a.attempt_id
		WHERE o.mode='DEMO' AND o.id=$1`, operationID).
		Scan(&afterRequest, &state, &afterAmount, &reservedDebit); err != nil ||
		state != "CANCELLED_UNSENT" || afterRequest != requestID || afterAmount != amount || reservedDebit != 0 {
		t.Fatalf("cancelled collection=%s/%s/%d reserved=%d err=%v, want unchanged intent with released debit", afterRequest, state, afterAmount, reservedDebit, err)
	}
	var principalOwed, feeOwed int64
	if err := h.Pool.QueryRow(ctx, `SELECT principal_owed_cents,contract_fee_owed_cents
		FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).
		Scan(&principalOwed, &feeOwed); err != nil || principalOwed != 700_000 || feeOwed != 19_000 {
		t.Fatalf("debt after unsent cancellation=%d/%d err=%v, want unchanged principal/fee", principalOwed, feeOwed, err)
	}
	if h.ProviderHTTP.CreateRequestCount() != 1 {
		t.Fatalf("provider creates after changed cap=%d, want funding only", h.ProviderHTTP.CreateRequestCount())
	}
	fundingState, err := operationState(ctx, h, funding.OperationID)
	if err != nil || fundingState != "SETTLED" {
		t.Fatalf("original funding state=%s err=%v, want unchanged SETTLED", fundingState, err)
	}
}
