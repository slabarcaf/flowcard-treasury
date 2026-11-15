package underwriting_test

import (
	"reflect"
	"testing"
	"time"

	upstreamfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

func TestNeverSentProjectionPreservesOtherCommitments(t *testing.T) {
	fixture, err := upstreamfixture.New(time.Now().UTC(), "DEMO")
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.Input
	own := underwriting.NeverSentReservation{
		ProvenNeverSent: true, OperationState: "QUEUED", ReservationState: "NEVER_SENT",
		ReservationID: "own-reservation", BridgeID: "own-placeholder", TransactionID: input.Shortfall.TransactionID,
		FacilityPrincipalCents: 700_000, CustomerPrincipalCents: 700_000, CustomerFeeCents: 19_000,
		Invoices: []underwriting.InvoiceEncumbrance{{InvoiceID: "invoice-1",
			SourceRefSystem: "synthetic-invoicing-fixture", StorageSystem: "synthetic-invoicing-fixture", AmountCents: 719_000}},
	}
	input.Facility.ReservedPrincipalCents = 702_000
	input.Customer.Reservations = []policy.Reservation{
		{ID: own.ReservationID, TransactionID: own.TransactionID, PrincipalCents: 700_000, FeeCents: 19_000, Status: "ACTIVE"},
		{ID: "other-reservation", TransactionID: "other-payment", PrincipalCents: 2_000, FeeCents: 50, Status: "ACTIVE"},
	}
	input.Customer.Bridges = []policy.Bridge{
		{ID: own.BridgeID, TransactionID: own.TransactionID, OriginalPrincipalCents: 700_000, Status: "OPEN"},
		{ID: "other-bridge", TransactionID: "other-funded-payment", OriginalPrincipalCents: 1_000, PrincipalOwedCents: 1_000, Status: "OPEN"},
	}
	input.Customer.Receivables[0].AllocatedCents = 721_050
	projected, err := underwriting.ProjectNeverSentReservation(input, own)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Facility.ReservedPrincipalCents != 2_000 ||
		len(projected.Customer.Reservations) != 1 || projected.Customer.Reservations[0] != input.Customer.Reservations[1] ||
		len(projected.Customer.Bridges) != 1 || projected.Customer.Bridges[0] != input.Customer.Bridges[1] ||
		projected.Customer.Receivables[0].AllocatedCents != 2_050 {
		t.Fatalf("projection = %+v, want only own facility/customer/invoice holds and placeholder removed", projected)
	}
	// Removing both own payment-bearing records must leave no duplicate of the
	// current payment; unrelated reservations and funded debt remain visible.
	for _, reservation := range projected.Customer.Reservations {
		if reservation.TransactionID == input.Shortfall.TransactionID {
			t.Fatalf("own payment duplicate survived projection: %+v", reservation)
		}
	}
	for _, bridge := range projected.Customer.Bridges {
		if bridge.TransactionID == input.Shortfall.TransactionID {
			t.Fatalf("own placeholder/payment duplicate survived projection: %+v", bridge)
		}
	}
	if input.Facility.ReservedPrincipalCents != 702_000 || len(input.Customer.Reservations) != 2 ||
		len(input.Customer.Bridges) != 2 || input.Customer.Receivables[0].AllocatedCents != 721_050 {
		t.Fatal("projection mutated its locked input facts")
	}
	// Possible movement must never receive self-exclusion privileges.
	own.ProvenNeverSent = false
	if unsafe, err := underwriting.ProjectNeverSentReservation(input, own); err == nil || !reflect.DeepEqual(unsafe, policy.Input{}) {
		t.Fatalf("possible-send projection = %+v err %v, want refusal without a usable projection", unsafe, err)
	}
}
