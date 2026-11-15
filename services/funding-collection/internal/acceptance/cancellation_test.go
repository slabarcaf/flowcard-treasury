package acceptance_test

import (
	"encoding/json"
	"net/http"
	"testing"

	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestUnsentFundingCancellation(t *testing.T) {
	ctx, h := newJourney(t)
	firstOffer, err := createOffer(t, h, "journey-first-offer")
	if err != nil {
		t.Fatal(err)
	}
	if err = acceptJourneyOffer(t, h, firstOffer, "journey-first-acceptance"); err != nil {
		t.Fatal(err)
	}
	firstReceipt, err := reserveJourneyOffer(t, h, firstOffer, "journey-first-fund")
	if err != nil {
		t.Fatal(err)
	}
	if firstReceipt.OperationID == "" || firstReceipt.BridgeID == "" {
		t.Fatalf("first funding receipt = %+v, want durable bridge and operation IDs", firstReceipt)
	}

	// A source 404 after reservation proves the operation unsent; the worker
	// must void that bridge and release its reservation before the same payment
	// can receive fresh accepted terms.
	h.UpstreamBehavior.SetEvaluationMissing(true)
	if _, err = h.Worker.ProcessOne(ctx); err != nil {
		t.Fatalf("worker cancel proven-unsent funding intent: %v", err)
	}
	h.UpstreamBehavior.SetEvaluationMissing(false)
	if h.ProviderHTTP.CreateRequestCount() != 0 {
		t.Fatalf("provider create requests after source-loss cancel = %d, want 0", h.ProviderHTTP.CreateRequestCount())
	}
	var voidLifecycle string
	var releasedInvoiceCents int64
	if err = h.Pool.QueryRow(ctx, `SELECT lifecycle FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, firstReceipt.BridgeID).Scan(&voidLifecycle); err != nil || voidLifecycle != "VOID_UNFUNDED" {
		t.Fatalf("unsent bridge lifecycle = %q err %v, want VOID_UNFUNDED", voidLifecycle, err)
	}
	if err = h.Pool.QueryRow(ctx, `SELECT reserved_cents FROM servicing.receivables WHERE mode='DEMO' AND source_system=$1 AND invoice_id=$2`, servicefixture.InvoiceSystem, servicefixture.InvoiceID).Scan(&releasedInvoiceCents); err != nil || releasedInvoiceCents != 0 {
		t.Fatalf("released invoice backing = %d err %v, want zero after proven-unsent void", releasedInvoiceCents, err)
	}

	stateResponse, err := h.Request(http.MethodGet, "/api/servicing/state?customer_id="+servicefixture.CustomerID,
		"underwriting-reader", servicefixture.ReaderKey, "", nil)
	if err != nil || stateResponse.StatusCode != http.StatusOK {
		t.Fatalf("state after unsent cancellation status = %v err %v, want 200", journeyResponseStatus(stateResponse), err)
	}
	var state struct {
		Customer struct {
			PrincipalOwed     int64 `json:"principal_owed_cents"`
			UnpaidFee         int64 `json:"unpaid_fee_cents"`
			ReservedPrincipal int64 `json:"reserved_principal_cents"`
			ReservedFee       int64 `json:"reserved_fee_cents"`
		} `json:"customer"`
		History []struct {
			BridgeID string `json:"bridge_id"`
		} `json:"history"`
	}
	if err := json.NewDecoder(stateResponse.Body).Decode(&state); err != nil {
		stateResponse.Body.Close()
		t.Fatalf("decode state after unsent cancellation: %v", err)
	}
	stateResponse.Body.Close()
	if state.Customer.PrincipalOwed != 0 || state.Customer.UnpaidFee != 0 ||
		state.Customer.ReservedPrincipal != 0 || state.Customer.ReservedFee != 0 || len(state.History) != 0 {
		t.Fatalf("state after unsent cancellation = %+v, want released capacity and no funded history", state)
	}

	retryOffer, err := createOffer(t, h, "journey-fresh-retry-offer")
	if err != nil {
		t.Fatalf("fresh offer after VOID_UNFUNDED: %v", err)
	}
	if retryOffer.ID == firstOffer.ID || retryOffer.TermsHash == firstOffer.TermsHash {
		t.Fatalf("fresh retry offer = %+v, old offer = %+v, want a new offer ID and offer-bound terms hash", retryOffer, firstOffer)
	}
	if err = acceptJourneyOffer(t, h, retryOffer, "journey-retry-acceptance"); err != nil {
		t.Fatal(err)
	}
	retryReceipt, err := reserveJourneyOffer(t, h, retryOffer, "journey-retry-fund")
	if err != nil || retryReceipt.BridgeID == "" || retryReceipt.OperationID == "" || retryReceipt.BridgeID == firstReceipt.BridgeID {
		t.Fatalf("reserve fresh offer after cancellation = %+v err %v, want a new durable bridge and operation", retryReceipt, err)
	}
	if h.ProviderHTTP.CreateRequestCount() != 0 {
		t.Fatalf("provider creates after retry reservation = %d, want 0", h.ProviderHTTP.CreateRequestCount())
	}
}
