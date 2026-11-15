package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	providerfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/provider"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

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
