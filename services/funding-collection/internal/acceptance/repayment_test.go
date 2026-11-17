package acceptance_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func repayFunding(t *testing.T, ctx context.Context, h *servicefixture.Harness, funding fundReceipt) (provider.DepositObservation, provider.DepositObservation) {
	t.Helper()
	var err error
	fundingObjectID, ok := h.ProviderHTTP.ObjectIDForRequest("bridge-" + funding.BridgeID)
	if !ok {
		t.Fatal("provider double lost the funded transfer object")
	}
	fundingSettledAt, ok := h.ProviderHTTP.TransferSettledAt(fundingObjectID)
	if !ok {
		t.Fatal("provider double lost the funded transfer settlement time")
	}
	depositsSettledAt := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	if minimum := fundingSettledAt.Add(time.Second); depositsSettledAt.Before(minimum) {
		depositsSettledAt = minimum
	}
	if wait := time.Until(depositsSettledAt); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("wait for fixture deposit settlement time: %v", ctx.Err())
		case <-timer.C:
		}
	}
	zeroFee := int64(0)
	firstDeposit := provider.DepositObservation{AccountRef: servicefixture.BorrowerAccount,
		DepositID: "synthetic-deposit-1", CustomerID: servicefixture.CustomerID, Currency: "USD",
		GrossCents: 10_000, NetCents: 10_000, ProviderFeeCents: &zeroFee,
		Status: "SETTLED", Kind: "EXTERNAL_SETTLED", SettledAt: &depositsSettledAt, ObservedAt: depositsSettledAt}
	h.ProviderHTTP.SetDeposit(firstDeposit)
	h.ProviderHTTP.SetBalance(servicefixture.BorrowerAccount, 910_000)
	firstDepositEvent, err := sendDepositWake(h, "journey-deposit-event-1", firstDeposit.DepositID)
	if err != nil {
		t.Fatalf("first signed deposit event: %v", err)
	}
	if firstDepositEvent.StatusCode != http.StatusOK {
		firstDepositEvent.Body.Close()
		t.Fatalf("first deposit event status = %d, want 200", firstDepositEvent.StatusCode)
	}
	firstDepositEvent.Body.Close()
	duplicateEvent, err := sendDepositWake(h, "journey-deposit-event-1", firstDeposit.DepositID)
	if err != nil || duplicateEvent.StatusCode != http.StatusOK {
		t.Fatalf("identical deposit event replay status = %v err %v, want 200", journeyResponseStatus(duplicateEvent), err)
	}
	duplicateEvent.Body.Close()
	observedCreates := h.ProviderHTTP.CreateRequestCount()
	if observedCreates != 1 {
		t.Fatalf("provider creates before deposit collection = %d, want funding only", observedCreates)
	}
	if err := driveWorkerUntil(ctx, h, &observedCreates, 12*time.Second, func() bool {
		var depositCount int
		var principalOwed, contractFeeOwed int64
		if h.Pool.QueryRow(ctx, `SELECT count(*) FROM servicing.deposits WHERE mode='DEMO'
			AND customer_id=$1 AND provider_deposit_id=$2 AND status='SETTLED' AND kind='EXTERNAL_SETTLED'`,
			servicefixture.CustomerID, firstDeposit.DepositID).Scan(&depositCount) != nil || depositCount != 1 {
			return false
		}
		if h.Pool.QueryRow(ctx, `SELECT principal_owed_cents,contract_fee_owed_cents
			FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).
			Scan(&principalOwed, &contractFeeOwed) != nil {
			return false
		}
		return principalOwed == 700_000 && contractFeeOwed == 9_000
	}); err != nil {
		t.Fatalf("first $100 deposit collection: %v", err)
	}
	if observedCreates != 2 {
		t.Fatalf("provider creates after first $100 deposit = %d, want funding plus one fee operation", observedCreates)
	}

	// The second provider credit is observed later, after the $100 fee
	// collection, so the next $7090 is allocated to $90 fee then $7000 principal.
	secondSettledAt := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	if wait := time.Until(secondSettledAt); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("wait for second fixture deposit settlement time: %v", ctx.Err())
		case <-timer.C:
		}
	}
	secondDeposit := provider.DepositObservation{AccountRef: servicefixture.BorrowerAccount,
		DepositID: "synthetic-deposit-2", CustomerID: servicefixture.CustomerID, Currency: "USD",
		GrossCents: 709_000, NetCents: 709_000, ProviderFeeCents: &zeroFee,
		Status: "SETTLED", Kind: "EXTERNAL_SETTLED", SettledAt: &secondSettledAt, ObservedAt: secondSettledAt}
	h.ProviderHTTP.SetDeposit(secondDeposit)
	h.ProviderHTTP.SetBalance(servicefixture.BorrowerAccount, 1_609_000)
	secondDepositEvent, err := sendDepositWake(h, "journey-deposit-event-2", secondDeposit.DepositID)
	if err != nil || secondDepositEvent.StatusCode != http.StatusOK {
		t.Fatalf("second signed deposit event status = %v err %v, want 200", journeyResponseStatus(secondDepositEvent), err)
	}
	secondDepositEvent.Body.Close()
	if err := driveWorkerUntil(ctx, h, &observedCreates, 12*time.Second, func() bool {
		var lifecycle string
		var depositCount int
		if h.Pool.QueryRow(ctx, `SELECT count(*) FROM servicing.deposits WHERE mode='DEMO'
			AND customer_id=$1 AND status='SETTLED' AND kind='EXTERNAL_SETTLED'`,
			servicefixture.CustomerID).Scan(&depositCount) != nil || depositCount != 2 {
			return false
		}
		if h.Pool.QueryRow(ctx, `SELECT lifecycle FROM servicing.bridges WHERE mode='DEMO' AND id=$1`,
			funding.BridgeID).Scan(&lifecycle) != nil {
			return false
		}
		return lifecycle == "REPAID" && observedCreates == 4
	}); err != nil {
		t.Fatalf("second $7090 deposit collection: %v", err)
	}

	return firstDeposit, secondDeposit
}
