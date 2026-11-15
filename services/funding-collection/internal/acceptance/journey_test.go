package acceptance_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	providerfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/provider"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestServicingJourney(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	h, err := servicefixture.New(t, now, 2000)
	if err != nil {
		t.Fatalf("service fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

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

	settledAt := time.Now().UTC().Add(-time.Second)
	if err = h.ProviderHTTP.SetStatus("SETTLED", &settledAt); err != nil {
		t.Fatalf("set provider settlement fact: %v", err)
	}
	fundResults := concurrentFundRequests(ctx, h, retryOffer)
	var fundedResponse fundResult
	acceptedCount := 0
	refusedCount := 0
	for _, result := range fundResults {
		if result.err != nil {
			t.Fatalf("concurrent fund request %q: %v", result.key, result.err)
		}
		if result.status == http.StatusAccepted {
			acceptedCount++
			fundedResponse = result
		} else if result.status == http.StatusConflict || result.status == http.StatusUnprocessableEntity {
			refusedCount++
		} else {
			t.Fatalf("competing fitting reservation status = %d body %s, want a version/state refusal", result.status, result.body)
		}
	}
	if acceptedCount != 1 || refusedCount != 1 || h.ProviderHTTP.CreateRequestCount() != 0 {
		t.Fatalf("concurrent fitting reservation accepted=%d refused=%d create_requests=%d, want one reservation, one refusal and zero API-side creates", acceptedCount, refusedCount, h.ProviderHTTP.CreateRequestCount())
	}
	var funding fundReceipt
	if err := json.Unmarshal(fundedResponse.body, &funding); err != nil || funding.BridgeID == "" || funding.OperationID == "" {
		t.Fatalf("funding reservation receipt = %+v err %v", funding, err)
	}

	// The local provider stores a settled movement and drops only its reply.
	// Restarting the serial worker then reconciles the original request by GET.
	h.ProviderHTTP.DropNextCreateReceipt()
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatalf("first serial worker pass: %v", err)
	}
	if h.ProviderHTTP.CreateRequestCount() != 1 {
		t.Fatalf("create request count after uncertain send = %d, want exactly one", h.ProviderHTTP.CreateRequestCount())
	}
	createRequest, ok := findCreateRequest(h.ProviderHTTP.Requests(), 0)
	if !ok {
		t.Fatal("provider double did not capture the actual create request")
	}
	var sent struct {
		RequestID   string `json:"request_id"`
		Destination string `json:"destination"`
		Amount      string `json:"amount"`
	}
	if json.Unmarshal(createRequest.Body, &sent) != nil || createRequest.Source != servicefixture.FundingAccount ||
		sent.RequestID != "bridge-"+funding.BridgeID || sent.Destination != servicefixture.BorrowerAccount || sent.Amount != "7000.00" {
		t.Fatalf("captured funding create source=%q body=%s, want original scoped $7000 request", createRequest.Source, createRequest.Body)
	}
	if err := sendTransferWake(h, "journey-funding-wake", sent.RequestID); err != nil {
		t.Fatalf("signed funding lookup wake: %v", err)
	}
	h.Worker, err = h.NewWorker()
	if err != nil {
		t.Fatalf("restart serial worker: %v", err)
	}
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		var lifecycle, state string
		var everFunded bool
		diagnosticErr := h.Pool.QueryRow(ctx, `SELECT b.lifecycle,b.ever_funded,o.state
			FROM servicing.bridges b JOIN servicing.operations o ON o.mode=b.mode AND o.bridge_id=b.id
			WHERE b.mode='DEMO' AND b.id=$1 AND o.id=$2`, funding.BridgeID, funding.OperationID).
			Scan(&lifecycle, &everFunded, &state)
		t.Fatalf("restarted worker GET recovery: %v; bridge_lifecycle=%s ever_funded=%t operation_state=%s diagnostic=%v",
			err, lifecycle, everFunded, state, diagnosticErr)
	}
	if err := requireOperationState(ctx, h, funding.OperationID, "SETTLED", 5*time.Second); err != nil {
		t.Fatalf("funding lookup recovery: %v", err)
	}
	if h.ProviderHTTP.CreateRequestCount() != 1 {
		t.Fatalf("create request count after GET recovery = %d, want exactly one", h.ProviderHTTP.CreateRequestCount())
	}
	if !hasScopedLookup(h.ProviderHTTP.Requests(), sent.RequestID, servicefixture.FundingAccount) {
		t.Fatal("provider lookup did not retain the original funding request ID and source account scope")
	}

	fundingObjectID, ok := h.ProviderHTTP.ObjectIDForRequest(sent.RequestID)
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

	var allocations []struct {
		DepositID string
		Bucket    string
		Amount    int64
	}
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
		var allocation struct {
			DepositID string
			Bucket    string
			Amount    int64
		}
		if err := rows.Scan(&allocation.DepositID, &allocation.Bucket, &allocation.Amount); err != nil {
			rows.Close()
			t.Fatalf("scan collection allocation: %v", err)
		}
		allocations = append(allocations, allocation)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read collection allocation rows: %v", err)
	}
	rows.Close()
	wantAllocations := []struct {
		deposit string
		bucket  string
		amount  int64
	}{{firstDeposit.DepositID, "CONTRACT_FEE", 10_000},
		{secondDeposit.DepositID, "CONTRACT_FEE", 9_000},
		{secondDeposit.DepositID, "PRINCIPAL", 700_000}}
	if len(allocations) != len(wantAllocations) {
		t.Fatalf("collection allocations = %+v, want $100 fee then $90 fee and $7000 principal", allocations)
	}
	for i, got := range allocations {
		want := wantAllocations[i]
		if got.DepositID != want.deposit || got.Bucket != want.bucket || got.Amount != want.amount {
			var originalFee, feePaid, feeOwed, principalPaid, principalOwed int64
			diagnosticErr := h.Pool.QueryRow(ctx, `SELECT contract_fee_original_cents,contract_fee_paid_cents,
				contract_fee_owed_cents,principal_paid_cents,principal_owed_cents
				FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).
				Scan(&originalFee, &feePaid, &feeOwed, &principalPaid, &principalOwed)
			t.Fatalf("collection allocation %d = %+v, want %+v (all=%+v fee=%d/%d/%d principal=%d/%d diagnostic=%v)",
				i, got, want, allocations, feePaid, feeOwed, originalFee, principalPaid, principalOwed, diagnosticErr)
		}
	}

	bridgeResponse, err := h.Request(http.MethodGet, "/api/bridges/"+funding.BridgeID,
		"coordinator-reader", servicefixture.CoordinatorKey, "", nil)
	if err != nil || bridgeResponse.StatusCode != http.StatusOK {
		t.Fatalf("final bridge read status = %v err %v, want 200", journeyResponseStatus(bridgeResponse), err)
	}
	var bridge struct {
		Lifecycle       string     `json:"lifecycle"`
		SourceBindingID string     `json:"source_binding_id"`
		FundedAt        *time.Time `json:"funded_at"`
		PrincipalOwed   *int64     `json:"principal_owed_cents"`
		ContractFeeOwed *int64     `json:"contract_fee_owed_cents"`
		LateFeeOwed     *int64     `json:"late_fee_owed_cents"`
		HoldReasons     []string   `json:"hold_reasons"`
		Operations      []struct {
			Kind                string  `json:"kind"`
			State               string  `json:"state"`
			SettlementReference *string `json:"last_verified_settlement_reference"`
		} `json:"operations"`
	}
	if err := json.NewDecoder(bridgeResponse.Body).Decode(&bridge); err != nil {
		bridgeResponse.Body.Close()
		t.Fatalf("decode final bridge read: %v", err)
	}
	bridgeResponse.Body.Close()
	fundingSettled := false
	for _, operation := range bridge.Operations {
		if operation.Kind == "FUND" && operation.State == "SETTLED" &&
			operation.SettlementReference != nil && *operation.SettlementReference != "" {
			fundingSettled = true
		}
	}
	if bridge.Lifecycle != "REPAID" || bridge.SourceBindingID != h.Config.SourceBindingID ||
		bridge.PrincipalOwed == nil || *bridge.PrincipalOwed != 0 || bridge.ContractFeeOwed == nil ||
		*bridge.ContractFeeOwed != 0 || bridge.LateFeeOwed == nil || *bridge.LateFeeOwed != 0 ||
		bridge.FundedAt == nil || len(bridge.HoldReasons) != 0 || !fundingSettled {
		t.Fatalf("final bridge read = %+v, want source-bound ever-funded zero-debt closure", bridge)
	}
	stateResponse, err := h.Request(http.MethodGet, "/api/servicing/state?customer_id="+servicefixture.CustomerID,
		"underwriting-reader", servicefixture.ReaderKey, "", nil)
	if err != nil || stateResponse.StatusCode != http.StatusOK {
		t.Fatalf("final servicing state status = %v err %v, want 200", journeyResponseStatus(stateResponse), err)
	}
	var finalState struct {
		Facility struct {
			FacilityID string `json:"facility_id"`
		} `json:"facility"`
		Customer struct {
			CustomerID        string `json:"customer_id"`
			PrincipalOwed     int64  `json:"principal_owed_cents"`
			UnpaidFee         int64  `json:"unpaid_fee_cents"`
			ReservedPrincipal int64  `json:"reserved_principal_cents"`
			ReservedFee       int64  `json:"reserved_fee_cents"`
			ActiveHold        bool   `json:"active_hold"`
		} `json:"customer"`
		Invoices []struct {
			InvoiceID     string `json:"invoice_id"`
			ReservedCents int64  `json:"reserved_cents"`
		} `json:"invoices"`
		History []struct {
			BridgeID             string     `json:"bridge_id"`
			Lifecycle            string     `json:"lifecycle"`
			PrincipalPaidCents   int64      `json:"principal_paid_cents"`
			ContractFeePaidCents int64      `json:"contract_fee_paid_cents"`
			ClosedAt             *time.Time `json:"closed_at"`
		} `json:"history"`
		FundingAuthorized bool `json:"funding_authorized"`
	}
	if err := json.NewDecoder(stateResponse.Body).Decode(&finalState); err != nil {
		stateResponse.Body.Close()
		t.Fatalf("decode final servicing state: %v", err)
	}
	stateResponse.Body.Close()
	if finalState.Facility.FacilityID != servicefixture.FacilityID ||
		finalState.Customer.CustomerID != servicefixture.CustomerID || finalState.Customer.PrincipalOwed != 0 ||
		finalState.Customer.UnpaidFee != 0 || finalState.Customer.ReservedPrincipal != 0 ||
		finalState.Customer.ReservedFee != 0 || finalState.Customer.ActiveHold || finalState.FundingAuthorized ||
		len(finalState.Invoices) != 1 || finalState.Invoices[0].InvoiceID != servicefixture.InvoiceID ||
		finalState.Invoices[0].ReservedCents != 0 || len(finalState.History) != 1 ||
		finalState.History[0].BridgeID != funding.BridgeID || finalState.History[0].Lifecycle != "REPAID" ||
		finalState.History[0].PrincipalPaidCents != 700_000 || finalState.History[0].ContractFeePaidCents != 19_000 ||
		finalState.History[0].ClosedAt == nil {
		t.Fatalf("final scoped read model = %+v, want zero debt, released backing and complete $7190 repayment", finalState)
	}

	// Ever-funded canonical payment identity remains blocked after repayment.
	response, err := h.Request(http.MethodPost, "/api/offers", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "journey-ever-funded-retry", []byte(`{"evaluation_id":"synthetic-evaluation"}`))
	if err != nil || response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ever-funded payment offer retry status = %v err %v, want 422", journeyResponseStatus(response), err)
	}
	response.Body.Close()

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
	bridgeResponse, err = h.Request(http.MethodGet, "/api/bridges/"+funding.BridgeID,
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

	if !hasPath(h.ProviderHTTP.Requests(), "/__fixture/payments/"+servicefixture.TransactionID) ||
		!hasPath(h.ProviderHTTP.Requests(), "/api/v1/balances/current") ||
		!hasPath(h.ProviderHTTP.Requests(), "/__fixture/deposits/"+firstDeposit.DepositID) ||
		!hasPath(h.ProviderHTTP.Requests(), "/__fixture/deposits/"+secondDeposit.DepositID) ||
		h.ProviderHTTP.CreateRequestCount() != 4 {
		t.Fatalf("provider HTTP evidence is incomplete: creates=%d requests=%+v", h.ProviderHTTP.CreateRequestCount(), h.ProviderHTTP.Requests())
	}
}

type journeyOffer struct {
	ID        string `json:"id"`
	Version   int64  `json:"version"`
	TermsHash string `json:"terms_hash"`
	State     string `json:"state"`
}

type fundReceipt struct {
	BridgeID    string `json:"bridge_id"`
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
}

type fundResult struct {
	key    string
	status int
	body   []byte
	err    error
}

func createOffer(t *testing.T, h *servicefixture.Harness, key string) (journeyOffer, error) {
	t.Helper()
	response, err := h.Request(http.MethodPost, "/api/offers", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, key, []byte(`{"evaluation_id":"synthetic-evaluation"}`))
	if err != nil {
		return journeyOffer{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		return journeyOffer{}, fmt.Errorf("offer creation status %d: %s", response.StatusCode, body)
	}
	var result journeyOffer
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return journeyOffer{}, err
	}
	return result, nil
}

func acceptJourneyOffer(t *testing.T, h *servicefixture.Harness, offer journeyOffer, key string) error {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"terms_hash":%q,"expected_version":1}`, offer.TermsHash))
	response, err := h.Request(http.MethodPost, "/api/offers/"+offer.ID+"/acceptances",
		servicefixture.CustomerSubject, servicefixture.CustomerSecret, key, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("acceptance status %d: %s", response.StatusCode, body)
	}
	return nil
}

func reserveJourneyOffer(t *testing.T, h *servicefixture.Harness, offer journeyOffer, key string) (fundReceipt, error) {
	t.Helper()
	body := []byte(`{"expected_version":2}`)
	response, err := h.Request(http.MethodPost, "/api/offers/"+offer.ID+"/fund",
		servicefixture.OperatorSubject, servicefixture.OperatorSecret, key, body)
	if err != nil {
		return fundReceipt{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		return fundReceipt{}, fmt.Errorf("fund reservation status %d: %s", response.StatusCode, body)
	}
	var result fundReceipt
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fundReceipt{}, err
	}
	return result, nil
}

func concurrentFundRequests(ctx context.Context, h *servicefixture.Harness, offer journeyOffer) []fundResult {
	keys := []string{"journey-concurrent-fund-a", "journey-concurrent-fund-b"}
	start := make(chan struct{})
	results := make(chan fundResult, len(keys))
	var group sync.WaitGroup
	for _, key := range keys {
		group.Add(1)
		go func(key string) {
			defer group.Done()
			<-start
			body := []byte(`{"expected_version":2}`)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost,
				h.API.URL+"/api/offers/"+offer.ID+"/fund", bytes.NewReader(body))
			if err != nil {
				results <- fundResult{key: key, err: err}
				return
			}
			request.Header.Set("Authorization", "Bearer "+servicefixture.Token(servicefixture.OperatorSubject,
				servicefixture.OperatorSecret, time.Now().UTC()))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", key)
			response, err := h.API.Client().Do(request)
			if err != nil {
				results <- fundResult{key: key, err: err}
				return
			}
			responseBody, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				results <- fundResult{key: key, err: readErr}
				return
			}
			results <- fundResult{key: key, status: response.StatusCode, body: responseBody}
		}(key)
	}
	close(start)
	group.Wait()
	close(results)
	collected := make([]fundResult, 0, len(keys))
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func sendTransferWake(h *servicefixture.Harness, eventID, requestID string) error {
	body, err := json.Marshal(map[string]any{"id": eventID, "name": "DEMO_TRANSFER_UPDATED",
		"data": map[string]string{"request_id": requestID}})
	if err != nil {
		return err
	}
	response, err := signedServicingWebhook(h, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		return fmt.Errorf("transfer webhook receipt status %d: %s", response.StatusCode, responseBody)
	}
	return nil
}

func sendDepositWake(h *servicefixture.Harness, eventID, depositID string) (*http.Response, error) {
	body, err := json.Marshal(map[string]any{"id": eventID, "name": "DEMO_DEPOSIT_SETTLED",
		"data": map[string]string{"customer_id": servicefixture.CustomerID,
			"card_id": servicefixture.CardID, "deposit_id": depositID}})
	if err != nil {
		return nil, err
	}
	return signedServicingWebhook(h, body)
}

func signedServicingWebhook(h *servicefixture.Harness, body []byte) (*http.Response, error) {
	timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(servicefixture.WebhookKey))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(body)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.API.URL+"/webhooks/airwallex/servicing", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("Content-Type", "application/json")
	response, err := h.API.Client().Do(request)
	if err != nil {
		return nil, err
	}
	if err := servicefixture.BufferResponse(response); err != nil {
		return nil, err
	}
	return response, nil
}

func findCreateRequest(requests []providerfixture.Request, index int) (providerfixture.Request, bool) {
	seen := 0
	for _, request := range requests {
		if request.Method == http.MethodPost && request.Path == "/api/v1/connected_account_transfers/create" {
			if seen == index {
				return request, true
			}
			seen++
		}
	}
	return providerfixture.Request{}, false
}

func hasScopedLookup(requests []providerfixture.Request, requestID, source string) bool {
	for _, request := range requests {
		if request.Method != http.MethodGet || request.Path != "/api/v1/connected_account_transfers" || request.Source != source {
			continue
		}
		values, err := url.ParseQuery(request.Query)
		if err == nil && values.Get("request_id") == requestID {
			return true
		}
	}
	return false
}

func hasPath(requests []providerfixture.Request, path string) bool {
	for _, request := range requests {
		if request.Method == http.MethodGet && request.Path == path {
			return true
		}
	}
	return false
}

func operationState(ctx context.Context, h *servicefixture.Harness, operationID string) (string, error) {
	var state string
	err := h.Pool.QueryRow(ctx, `SELECT state FROM servicing.operations WHERE mode='DEMO' AND id=$1`, operationID).Scan(&state)
	return state, err
}

func requireOperationState(ctx context.Context, h *servicefixture.Harness, operationID, wanted string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := operationState(ctx, h, operationID)
		if err != nil {
			return err
		}
		if state == wanted {
			return nil
		}
		if _, err := h.Worker.ProcessOne(ctx); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	state, err := operationState(ctx, h, operationID)
	return fmt.Errorf("operation %s state=%s err=%v, want %s before timeout", operationID, state, err, wanted)
}

func driveWorkerUntil(ctx context.Context, h *servicefixture.Harness, observedCreates *int,
	timeout time.Duration, done func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return nil
		}
		if _, err := h.Worker.ProcessOne(ctx); err != nil {
			return err
		}
		currentCreates := h.ProviderHTTP.CreateRequestCount()
		for *observedCreates < currentCreates {
			requests := h.ProviderHTTP.Requests()
			var requestID string
			for i := len(requests) - 1; i >= 0; i-- {
				if requests[i].Method != http.MethodPost || requests[i].Path != "/api/v1/connected_account_transfers/create" {
					continue
				}
				var request struct {
					RequestID string `json:"request_id"`
				}
				if err := json.Unmarshal(requests[i].Body, &request); err != nil {
					return fmt.Errorf("decode fixture create request: %w", err)
				}
				if requests[i].Source == "" || request.RequestID == "" {
					return errors.New("fixture create request lost source or request identity")
				}
				if _, ok := h.ProviderHTTP.ObjectIDForRequest(request.RequestID); !ok {
					continue
				}
				requestID = request.RequestID
				break
			}
			if requestID == "" {
				return errors.New("fixture create has no bound provider transfer")
			}
			if err := sendTransferWake(h, "journey-transfer-wake-"+requestID, requestID); err != nil {
				return fmt.Errorf("send signed transfer wake: %w", err)
			}
			*observedCreates++
		}
		time.Sleep(100 * time.Millisecond)
	}
	if done() {
		return nil
	}
	return errors.New("worker fixture condition was not reached before timeout")
}

func journeyResponseStatus(response *http.Response) any {
	if response == nil {
		return "nil"
	}
	return response.StatusCode
}
