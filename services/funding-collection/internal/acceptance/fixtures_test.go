package acceptance_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func newJourney(t *testing.T) (context.Context, *servicefixture.Harness) {
	t.Helper()
	h, err := servicefixture.New(t, time.Now().UTC().Truncate(time.Second), 2000)
	if err != nil {
		t.Fatalf("service fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx, h
}

func acceptedOffer(t *testing.T, h *servicefixture.Harness, key string) journeyOffer {
	t.Helper()
	offer, err := createOffer(t, h, key+"-offer")
	if err != nil {
		t.Fatal(err)
	}
	if err := acceptJourneyOffer(t, h, offer, key+"-acceptance"); err != nil {
		t.Fatal(err)
	}
	return offer
}

// settledFunding exercises the normal API, worker and HTTP reconciliation path
// when funding is setup for a collection scenario.
func settledFunding(t *testing.T, ctx context.Context, h *servicefixture.Harness) fundReceipt {
	t.Helper()
	offer := acceptedOffer(t, h, "journey")
	funding, err := reserveJourneyOffer(t, h, offer, "journey-fund")
	if err != nil {
		t.Fatal(err)
	}
	settledAt := time.Now().UTC().Add(-time.Second)
	if err := h.ProviderHTTP.SetStatus("SETTLED", &settledAt); err != nil {
		t.Fatal(err)
	}
	observedCreates := 0
	if err := driveWorkerUntil(ctx, h, &observedCreates, 5*time.Second, func() bool {
		state, err := operationState(ctx, h, funding.OperationID)
		return err == nil && state == "SETTLED"
	}); err != nil {
		t.Fatalf("settle fixture funding: %v", err)
	}
	return funding
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

func journeyResponseStatus(response *http.Response) any {
	if response == nil {
		return "nil"
	}
	return response.StatusCode
}
