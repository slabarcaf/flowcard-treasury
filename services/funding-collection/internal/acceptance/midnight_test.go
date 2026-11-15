package acceptance_test

import (
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

func TestMidnightCancellationReleasesAcceptedFunding(t *testing.T) {
	ctx, h := newJourney(t)
	offer := acceptedOffer(t, h, "midnight")
	funding, err := reserveJourneyOffer(t, h, offer, "midnight-fund")
	if err != nil {
		t.Fatal(err)
	}
	// Pass a logical business clock to the existing cancellation primitive.
	// Database/provider clocks and the accepted immutable terms stay unchanged.
	now := time.Now().UTC()
	nextDay := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	if err := postgres.NewRepository(h.Pool).CancelUnsentFunding(ctx, domain.ModeDemo,
		funding.BridgeID, "MIDNIGHT_SUPERSEDED", func() time.Time { return nextDay }); err != nil {
		t.Fatal(err)
	}
	var offerState, lifecycle, operationState, reason string
	var reserved, owed int64
	if err := h.Pool.QueryRow(ctx, `SELECT f.state,b.lifecycle,o.state,o.sanitized_error_code,
		c.reserved_principal_cents+c.reserved_fee_cents,c.principal_owed_cents+c.unpaid_fee_cents
		FROM servicing.bridges b JOIN servicing.offers f ON f.mode=b.mode AND f.id=b.offer_id
		JOIN servicing.operations o ON o.mode=b.mode AND o.bridge_id=b.id
		JOIN servicing.customers c ON c.mode=b.mode AND c.id=b.customer_id
		WHERE b.mode='DEMO' AND b.id=$1`, funding.BridgeID).
		Scan(&offerState, &lifecycle, &operationState, &reason, &reserved, &owed); err != nil ||
		offerState != "TERMINATED" || lifecycle != "VOID_UNFUNDED" || operationState != "CANCELLED_UNSENT" ||
		reason != "MIDNIGHT_SUPERSEDED" || reserved != 0 || owed != 0 || h.ProviderHTTP.CreateRequestCount() != 0 {
		t.Fatalf("midnight offer/bridge/operation=%s/%s/%s reason=%s reserved=%d debt=%d creates=%d err=%v, want terminated unsent intent with all capacity released",
			offerState, lifecycle, operationState, reason, reserved, owed, h.ProviderHTTP.CreateRequestCount(), err)
	}
}
