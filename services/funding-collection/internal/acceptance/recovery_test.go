package acceptance_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestFundingRecovery(t *testing.T) {
	ctx, h := newJourney(t)
	offer := acceptedOffer(t, h, "recovery")
	settledAt := time.Now().UTC().Add(-time.Second)
	if err := h.ProviderHTTP.SetStatus("SETTLED", &settledAt); err != nil {
		t.Fatalf("set provider settlement fact: %v", err)
	}
	fundResults := concurrentFundRequests(ctx, h, offer)
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
	restartedWorker, err := h.NewWorker()
	if err != nil {
		t.Fatalf("restart serial worker: %v", err)
	}
	h.Worker = restartedWorker
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
}
