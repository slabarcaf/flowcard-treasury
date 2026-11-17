package acceptance_test

import (
	"testing"
	"time"
)

func TestAmbiguousLookupPreservesFundingHolds(t *testing.T) {
	ctx, h := newJourney(t)
	offer := acceptedOffer(t, h, "ambiguous")
	funding, err := reserveJourneyOffer(t, h, offer, "ambiguous-fund")
	if err != nil {
		t.Fatal(err)
	}
	settledAt := time.Now().UTC().Add(-time.Second)
	if err := h.ProviderHTTP.SetStatus("SETTLED", &settledAt); err != nil {
		t.Fatal(err)
	}
	h.ProviderHTTP.DropNextCreateReceipt()
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	requestID := "bridge-" + funding.BridgeID
	if err := h.ProviderHTTP.DuplicateLookupMatch(requestID); err != nil {
		t.Fatal(err)
	}
	for _, eventID := range []string{"ambiguous-wake", "ambiguous-repeated-wake"} {
		if err := sendTransferWake(h, eventID, requestID); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Worker.ProcessOne(ctx); err != nil {
			t.Fatalf("ambiguous lookup %s: %v", eventID, err)
		}
		state, err := operationState(ctx, h, funding.OperationID)
		if err != nil || state != "HELD" || h.ProviderHTTP.CreateRequestCount() != 1 {
			t.Fatalf("ambiguous lookup state=%s creates=%d err=%v, want HELD with one original create", state, h.ProviderHTTP.CreateRequestCount(), err)
		}
	}
	var principal, fee, facilityReservation, backing int64
	if err := h.Pool.QueryRow(ctx, `SELECT b.principal_owed_cents,b.contract_fee_owed_cents,
		f.reserved_principal_cents,(SELECT sum(reserved_cents) FROM servicing.receivables WHERE mode=b.mode AND customer_id=b.customer_id)
		FROM servicing.bridges b JOIN servicing.facilities f ON f.mode=b.mode AND f.id=b.facility_id
		WHERE b.mode='DEMO' AND b.id=$1`, funding.BridgeID).
		Scan(&principal, &fee, &facilityReservation, &backing); err != nil ||
		principal != 0 || fee != 0 || facilityReservation != 700_000 || backing != 719_000 {
		t.Fatalf("ambiguous debt/reservation/backing=%d/%d/%d/%d err=%v, want no guessed debt and retained 700000/719000 commitments", principal, fee, facilityReservation, backing, err)
	}
}
