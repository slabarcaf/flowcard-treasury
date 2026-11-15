package http_test

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
	"testing"
	"time"

	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestServicingContract(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h, err := servicefixture.New(t, now, 8000)
	if err != nil {
		t.Fatalf("service fixture: %v", err)
	}

	createBody := []byte(`{"evaluation_id":"synthetic-evaluation"}`)
	response, err := h.Request(http.MethodPost, "/api/offers", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "contract-offer-1", createBody)
	if err != nil {
		t.Fatalf("create offer request: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("POST /api/offers status = %d body %s, want 201", response.StatusCode, body)
	}
	var offer struct {
		ID             string `json:"id"`
		Version        int64  `json:"version"`
		TermsHash      string `json:"terms_hash"`
		State          string `json:"state"`
		PrincipalCents int64  `json:"principal_cents"`
		FeeCents       int64  `json:"contract_fee_cents"`
		TotalDueCents  int64  `json:"total_due_cents"`
	}
	if err := json.NewDecoder(response.Body).Decode(&offer); err != nil {
		response.Body.Close()
		t.Fatalf("decode created offer: %v", err)
	}
	response.Body.Close()
	if offer.ID == "" || offer.Version != 1 || offer.TermsHash == "" || offer.State != "AWAITING_ACCEPTANCE" ||
		offer.PrincipalCents != 700_000 || offer.FeeCents != 19_000 || offer.TotalDueCents != 719_000 {
		t.Fatalf("created offer = %+v, want version 1 with bound $7000/$190/$7190 terms", offer)
	}

	response, err = h.Request(http.MethodPost, "/api/offers", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "contract-offer-1", createBody)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("same offer command replay status = %v err %v, want 200", responseStatus(response), err)
	}
	response.Body.Close()
	changedCreate := []byte(`{"evaluation_id":"different-evaluation","originating_case_id":"11111111-1111-4111-8111-111111111111"}`)
	response, err = h.Request(http.MethodPost, "/api/offers", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "contract-offer-1", changedCreate)
	if err != nil || responseCode(response) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed offer command replay = status %v code %q err %v, want idempotency conflict", responseStatus(response), responseCode(response), err)
	}
	response.Body.Close()

	decisionPath := "/api/offers/" + offer.ID
	acceptPath := decisionPath + "/acceptances"
	approvePath := decisionPath + "/approvals"
	acceptBody := []byte(fmt.Sprintf(`{"terms_hash":%q,"expected_version":1}`, offer.TermsHash))
	approveBody := []byte(fmt.Sprintf(`{"terms_hash":%q,"expected_version":2,"reason_code":"REVIEWED"}`, offer.TermsHash))
	response, err = h.Request(http.MethodPost, approvePath, servicefixture.CustomerSubject,
		servicefixture.CustomerSecret, "wrong-role-approve", approveBody)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("customer approval status = %v err %v, want 403", responseStatus(response), err)
	}
	response.Body.Close()
	response, err = h.Request(http.MethodPost, acceptPath, servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "wrong-role-accept", acceptBody)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("operator acceptance status = %v err %v, want 403", responseStatus(response), err)
	}
	response.Body.Close()

	response, err = h.Request(http.MethodPost, acceptPath, servicefixture.CustomerSubject,
		servicefixture.CustomerSecret, "contract-accept-1", acceptBody)
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("customer acceptance status = %v err %v, want 201", responseStatus(response), err)
	}
	response.Body.Close()
	response, err = h.Request(http.MethodPost, acceptPath, servicefixture.CustomerSubject,
		servicefixture.CustomerSecret, "contract-accept-1", acceptBody)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("same acceptance replay status = %v err %v, want 200", responseStatus(response), err)
	}
	response.Body.Close()
	changedAccept := []byte(fmt.Sprintf(`{"terms_hash":%q,"expected_version":2}`, offer.TermsHash))
	response, err = h.Request(http.MethodPost, acceptPath, servicefixture.CustomerSubject,
		servicefixture.CustomerSecret, "contract-accept-1", changedAccept)
	if err != nil || responseCode(response) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed acceptance replay = status %v code %q err %v, want conflict", responseStatus(response), responseCode(response), err)
	}
	response.Body.Close()
	response, err = h.Request(http.MethodPost, acceptPath, servicefixture.CustomerSubject,
		servicefixture.CustomerSecret, "stale-accept-version", acceptBody)
	if err != nil || responseCode(response) != "VERSION_CONFLICT" {
		t.Fatalf("stale acceptance version = status %v code %q err %v, want version conflict", responseStatus(response), responseCode(response), err)
	}
	response.Body.Close()
	staleApproval := []byte(fmt.Sprintf(`{"terms_hash":%q,"expected_version":1,"reason_code":"REVIEWED"}`, offer.TermsHash))
	response, err = h.Request(http.MethodPost, approvePath, servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "stale-approval-version", staleApproval)
	if err != nil || responseCode(response) != "VERSION_CONFLICT" {
		t.Fatalf("stale approval version = status %v code %q err %v, want version conflict", responseStatus(response), responseCode(response), err)
	}
	response.Body.Close()
	response, err = h.Request(http.MethodPost, approvePath, servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "contract-approve-1", approveBody)
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("operator approval status = %v err %v, want 201", responseStatus(response), err)
	}
	var approved struct {
		Version int64  `json:"version"`
		State   string `json:"state"`
	}
	if err := json.NewDecoder(response.Body).Decode(&approved); err != nil || approved.Version != 3 || approved.State != "READY" {
		response.Body.Close()
		t.Fatalf("approved offer version/state = %d/%q err %v, want 3/READY", approved.Version, approved.State, err)
	}
	response.Body.Close()
	response, err = h.Request(http.MethodPost, approvePath, servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "contract-approve-1", approveBody)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("same approval replay status = %v err %v, want 200", responseStatus(response), err)
	}
	response.Body.Close()
	changedApproval := []byte(fmt.Sprintf(`{"terms_hash":%q,"expected_version":2,"reason_code":"OTHER"}`, offer.TermsHash))
	response, err = h.Request(http.MethodPost, approvePath, servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "contract-approve-1", changedApproval)
	if err != nil || responseCode(response) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed approval replay = status %v code %q err %v, want conflict", responseStatus(response), responseCode(response), err)
	}
	response.Body.Close()

	fundBody := []byte(fmt.Sprintf(`{"expected_version":%d}`, approved.Version))
	response, err = h.Request(http.MethodPost, "/api/offers/"+offer.ID+"/fund", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "tight-funding-guard", fundBody)
	if err != nil || response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("approved TIGHT funding guard status = %v err %v, want 422", responseStatus(response), err)
	}
	fundReply, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	var fundError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if readErr != nil || json.Unmarshal(fundReply, &fundError) != nil || fundError.Error.Code != "SCARCE_CAPITAL_RANKING_UNSUPPORTED" || h.ProviderHTTP.CreateRequestCount() != 0 {
		t.Fatalf("approved TIGHT funding response=%s code=%q read_err=%v provider_creates=%d, want explicit 422 and zero creates", fundReply, fundError.Error.Code, readErr, h.ProviderHTTP.CreateRequestCount())
	}
	response, err = h.Request(http.MethodPost, "/api/offers/"+offer.ID+"/fund", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "tight-funding-guard", fundBody)
	if err != nil || response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("same TIGHT funding guard replay status = %v err %v, want 422", responseStatus(response), err)
	}
	replayedFundReply, replayErr := io.ReadAll(response.Body)
	response.Body.Close()
	if replayErr != nil || !bytes.Equal(replayedFundReply, fundReply) || h.ProviderHTTP.CreateRequestCount() != 0 {
		t.Fatalf("TIGHT funding replay same_body=%t first=%s replay=%s err=%v provider_creates=%d, want identical 422 without create", bytes.Equal(replayedFundReply, fundReply), fundReply, replayedFundReply, replayErr, h.ProviderHTTP.CreateRequestCount())
	}

	webhookBody := []byte(`{"id":"synthetic-event-1","name":"DEMO_TRANSFER_UPDATED","data":{"request_id":"synthetic-request-1"}}`)
	response, err = signedWebhook(h, webhookBody, now)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("signed webhook receipt status = %v err %v, want 200", responseStatus(response), err)
	}
	response.Body.Close()
	response, err = signedWebhook(h, webhookBody, now)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("identical webhook duplicate status = %v err %v, want 200", responseStatus(response), err)
	}
	response.Body.Close()
	changedWebhook := []byte(`{"id":"synthetic-event-1","name":"DEMO_TRANSFER_UPDATED","data":{"request_id":"different-request"}}`)
	response, err = signedWebhook(h, changedWebhook, now)
	if err != nil || responseCode(response) != "WEBHOOK_EVENT_CONFLICT" {
		t.Fatalf("changed webhook body = status %v code %q err %v, want body conflict", responseStatus(response), responseCode(response), err)
	}
	response.Body.Close()
	var rawBody []byte
	var inboxCount int
	if err := h.Pool.QueryRow(ctx, `SELECT raw_body,(SELECT count(*) FROM servicing.webhook_inbox WHERE mode='DEMO' AND source='AIRWALLEX_SERVICING' AND event_id='synthetic-event-1') FROM servicing.webhook_inbox WHERE mode='DEMO' AND source='AIRWALLEX_SERVICING' AND event_id='synthetic-event-1'`).Scan(&rawBody, &inboxCount); err != nil || !bytes.Equal(rawBody, webhookBody) || inboxCount != 1 {
		t.Fatalf("durable webhook raw evidence count=%d bytes_equal=%t err=%v, want one exact signed body", inboxCount, bytes.Equal(rawBody, webhookBody), err)
	}

	response, err = h.Request(http.MethodGet, "/api/servicing/state?customer_id="+servicefixture.CustomerID,
		"coordinator-reader", servicefixture.CoordinatorKey, "", nil)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("coordinator reader servicing-state status = %v err %v, want 403", responseStatus(response), err)
	}
	response.Body.Close()
	response, err = h.Request(http.MethodGet, "/api/servicing/state?customer_id="+servicefixture.CustomerID,
		"underwriting-reader", servicefixture.ReaderKey, "", nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("underwriting reader servicing-state status = %v err %v, want 200", responseStatus(response), err)
	}
	var state map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		response.Body.Close()
		t.Fatalf("decode scoped servicing state: %v", err)
	}
	response.Body.Close()
	var stateMode, sourceBinding string
	var stateCustomer struct {
		CustomerID string `json:"customer_id"`
		AccountRef string `json:"account_ref"`
		Version    int64  `json:"version"`
	}
	var stateFacility struct {
		FacilityID string `json:"facility_id"`
	}
	if json.Unmarshal(state["mode"], &stateMode) != nil || json.Unmarshal(state["source_binding_id"], &sourceBinding) != nil ||
		json.Unmarshal(state["customer"], &stateCustomer) != nil || json.Unmarshal(state["facility"], &stateFacility) != nil ||
		stateMode != "DEMO" || sourceBinding != h.Config.SourceBindingID || stateCustomer.CustomerID != servicefixture.CustomerID ||
		stateCustomer.AccountRef != "customer:"+servicefixture.CustomerID || stateCustomer.Version < 1 || stateFacility.FacilityID != servicefixture.FacilityID {
		t.Fatalf("scoped servicing state has invalid mode/source/version bindings: %s", state["customer"])
	}

	response, err = h.Request(http.MethodGet, "/api/servicing/changes?after_seq=0",
		"coordinator-reader", servicefixture.CoordinatorKey, "", nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("coordinator change feed status = %v err %v, want 200", responseStatus(response), err)
	}
	var feed struct {
		Mode  string `json:"mode"`
		Items []struct {
			Sequence        int64   `json:"sequence"`
			ResourceVersion int64   `json:"resource_version"`
			CustomerID      *string `json:"customer_id"`
			AccountRef      *string `json:"account_ref"`
			EvaluationID    *string `json:"evaluation_id"`
			OfferID         *string `json:"offer_id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&feed); err != nil {
		response.Body.Close()
		t.Fatalf("decode coordinator feed: %v", err)
	}
	response.Body.Close()
	if feed.Mode != "DEMO" || len(feed.Items) < 3 {
		t.Fatalf("coordinator feed = mode %q items %d, want DEMO actor/offer changes", feed.Mode, len(feed.Items))
	}
	for i, item := range feed.Items {
		if item.Sequence <= 0 || item.ResourceVersion <= 0 || item.CustomerID == nil ||
			*item.CustomerID != servicefixture.CustomerID || item.AccountRef == nil ||
			*item.AccountRef != "customer:"+servicefixture.CustomerID || item.OfferID == nil || *item.OfferID != offer.ID ||
			item.EvaluationID == nil || *item.EvaluationID != servicefixture.EvaluationID ||
			(i > 0 && item.Sequence <= feed.Items[i-1].Sequence) {
			t.Fatalf("feed item %d = %+v, want ascending scoped offer references", i, item)
		}
	}
}

func signedWebhook(h *servicefixture.Harness, body []byte, now time.Time) (*http.Response, error) {
	timestamp := strconv.FormatInt(now.UnixMilli(), 10)
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

func responseStatus(response *http.Response) any {
	if response == nil {
		return "nil"
	}
	return response.StatusCode
}

func responseCode(response *http.Response) string {
	if response == nil {
		return ""
	}
	defer response.Body.Close()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(response.Body).Decode(&envelope)
	return envelope.Error.Code
}
