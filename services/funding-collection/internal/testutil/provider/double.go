// Package providerfixture supplies a loopback HTTP double for the real adapter.
package providerfixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

const (
	loginPath  = "/api/v1/authentication/login"
	createPath = "/api/v1/connected_account_transfers/create"
	listPath   = "/api/v1/connected_account_transfers"
)

// Request records the actual wire request received by the HTTP double.
type Request struct {
	Method string
	Path   string
	Query  string
	Source string
	Body   []byte
	Status int
}

// Double changes provider-owned facts and captures requests. It never settles
// application accounting or bridge state.
type Double struct {
	mu           sync.Mutex
	server       *httptest.Server
	requests     []Request
	transfers    []map[string]any
	balances     map[string]int64
	payments     map[string]provider.PaymentObservation
	deposits     map[string]provider.DepositObservation
	status       string
	dropCreate   bool
	createCount  int
	nextObjectID int
	clock        func() time.Time
	settledAt    *time.Time
	settled      map[string]bool
}

// New starts only an in-process loopback HTTP double and builds the production
// Airwallex adapter against it. The caller's test cleanup stops the server.
func New(t testing.TB, _ time.Time) (*Double, *provider.AirwallexAdapter, error) {
	return NewWithFundingAccount(t, time.Time{}, "demo-funding-account")
}

// NewWithFundingAccount uses the same adapter with a test-owned source account.
func NewWithFundingAccount(t testing.TB, _ time.Time, fundingAccount string) (*Double, *provider.AirwallexAdapter, error) {
	t.Helper()
	d := &Double{status: "NEW", clock: func() time.Time { return time.Now().UTC() },
		balances: map[string]int64{fundingAccount: 100_000_000},
		payments: make(map[string]provider.PaymentObservation), deposits: make(map[string]provider.DepositObservation),
		settled: make(map[string]bool)}
	d.server = httptest.NewServer(http.HandlerFunc(d.serveHTTP))
	t.Cleanup(d.server.Close)
	adapter, err := provider.NewAirwallexAdapter(provider.AdapterOptions{
		Mode: config.ModeDemo, ProviderMode: config.ProviderMock, BaseURL: d.server.URL,
		FundingAccountID: fundingAccount, ClientID: config.DemoClientID,
		APIKey: config.DemoAPIKey, Now: d.clock,
	})
	if err != nil {
		d.server.Close()
		return nil, nil, fmt.Errorf("provider_fixture_adapter_invalid: %w", err)
	}
	return d, adapter, nil
}

// BaseURL returns the in-process endpoint used to configure the real adapter.
func (d *Double) BaseURL() string { return d.server.URL }

// SetBalance changes one provider-owned available cash observation.
func (d *Double) SetBalance(account string, cents int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.balances[account] = cents
}

// SetPayment changes one provider-owned current payment observation.
func (d *Double) SetPayment(value provider.PaymentObservation) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.payments[value.ParentTransactionID] = value
}

// SetDeposit changes one provider-owned settled deposit observation.
func (d *Double) SetDeposit(value provider.DepositObservation) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deposits[value.DepositID] = value
}

// ObjectIDForRequest returns the synthetic provider object bound to one exact
// request identity so later fixture controls change provider facts only.
func (d *Double) ObjectIDForRequest(requestID string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, transfer := range d.transfers {
		if transfer["request_id"] == requestID {
			id, ok := transfer["id"].(string)
			return id, ok
		}
	}
	return "", false
}

// TransferSettledAt returns the exact provider timestamp recorded for a settled
// transfer, before application read/reconciliation timestamps are observed.
func (d *Double) TransferSettledAt(objectID string) (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, transfer := range d.transfers {
		if transfer["id"] != objectID || transfer["status"] != "SETTLED" {
			continue
		}
		value, ok := transfer["settled_at"].(string)
		if !ok {
			return time.Time{}, false
		}
		instant, err := time.Parse(time.RFC3339, value)
		return instant, err == nil
	}
	return time.Time{}, false
}

// SetTransferReversal changes one provider transfer observation to a settled
// reversal and reverses only the local double's provider-owned cash facts.
func (d *Double) SetTransferReversal(objectID string, amountCents int64, reversedAt time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, transfer := range d.transfers {
		if transfer["id"] != objectID {
			continue
		}
		if transfer["status"] != "SETTLED" || amountCents <= 0 || reversedAt.IsZero() || transfer["settled_at"] == nil {
			return errors.New("provider_fixture_reversal_binding_invalid")
		}
		var settledAt time.Time
		if json.Unmarshal([]byte(strconv.Quote(fmt.Sprint(transfer["settled_at"]))), &settledAt) != nil || !reversedAt.After(settledAt) {
			return errors.New("provider_fixture_reversal_time_invalid")
		}
		amount, ok := transfer["amount"].(json.Number)
		if !ok {
			return errors.New("provider_fixture_reversal_amount_invalid")
		}
		originalCents, err := fixtureAmountCents(amount)
		if err != nil || amountCents > originalCents {
			return errors.New("provider_fixture_reversal_amount_invalid")
		}
		source := transfer["source_account_id"].(string)
		destination := transfer["destination"].(string)
		if d.balances[destination] < amountCents || d.balances[source] > int64(^uint64(0)>>1)-amountCents {
			return errors.New("provider_fixture_reversal_balance_invalid")
		}
		d.balances[destination] -= amountCents
		d.balances[source] += amountCents
		transfer["status"] = "REVERSED"
		transfer["reversal_id"] = "demo-reversal-" + objectID
		transfer["reversed_amount"] = json.Number(decimalAmount(amountCents))
		transfer["reversal_currency"] = "USD"
		transfer["reversed_at"] = reversedAt.UTC().Format(time.RFC3339)
		return nil
	}
	return errors.New("provider_fixture_transfer_not_found")
}

// SetStatus changes transfer facts returned by GET. SETTLED requires its
// provider settlement timestamp and moves only the double's provider balances.
func (d *Double) SetStatus(status string, settledAt *time.Time) error {
	if status == "SETTLED" && (settledAt == nil || settledAt.IsZero()) {
		return errors.New("provider_fixture_settled_at_required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = status
	d.settledAt = nil
	if settledAt != nil {
		value := settledAt.UTC()
		d.settledAt = &value
	}
	for _, transfer := range d.transfers {
		transfer["status"] = status
		if status == "SETTLED" && d.settledAt != nil {
			transfer["settled_at"] = d.settledAt.Format(time.RFC3339)
			d.applySettlement(transfer)
		} else {
			delete(transfer, "settled_at")
		}
	}
	return nil
}

// DropNextCreateReceipt persists the provider-side transfer, then closes the
// HTTP response so the real adapter sees an uncertain create.
func (d *Double) DropNextCreateReceipt() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dropCreate = true
}

// Requests returns a copy of recorded wire requests, including every create.
func (d *Double) Requests() []Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	copy := make([]Request, len(d.requests))
	for i, request := range d.requests {
		copy[i] = request
		copy[i].Body = append([]byte(nil), request.Body...)
	}
	return copy
}

// CreateRequestCount reports actual HTTP POST create requests, not unique IDs.
func (d *Double) CreateRequestCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.createCount
}

func (d *Double) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	d.mu.Lock()
	defer d.mu.Unlock()
	record := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Source: r.Header.Get("x-on-behalf-of"), Body: append([]byte(nil), body...)}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == loginPath:
		record.Status = http.StatusOK
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"token":"synthetic-token","expires_at":"2099-01-01T00:00:00Z"}`)
	case r.Method == http.MethodPost && r.URL.Path == createPath:
		d.createCount++
		var request struct {
			RequestID   string `json:"request_id"`
			Destination string `json:"destination"`
			Amount      string `json:"amount"`
			Currency    string `json:"currency"`
		}
		if json.Unmarshal(body, &request) != nil || record.Source == "" || request.RequestID == "" {
			record.Status = http.StatusBadRequest
			d.requests = append(d.requests, record)
			http.Error(w, `{"error":"request_invalid"}`, http.StatusBadRequest)
			return
		}
		d.nextObjectID++
		id := "demo-transfer-" + strconv.Itoa(d.nextObjectID)
		if !json.Valid([]byte(request.Amount)) {
			record.Status = http.StatusBadRequest
			d.requests = append(d.requests, record)
			http.Error(w, `{"error":"amount_invalid"}`, http.StatusBadRequest)
			return
		}
		transfer := map[string]any{"id": id, "source_account_id": record.Source,
			"request_id": request.RequestID, "status": d.status, "destination": request.Destination,
			"amount": json.Number(request.Amount), "currency": request.Currency, "fee_amount": json.Number("0.00"),
			"fee_currency": "USD", "created_at": d.clock().UTC().Format(time.RFC3339)}
		if d.status == "SETTLED" {
			settledAt := d.clock().UTC()
			if d.settledAt != nil && d.settledAt.After(settledAt) {
				settledAt = *d.settledAt
			}
			transfer["settled_at"] = settledAt.Format(time.RFC3339)
		}
		d.transfers = append(d.transfers, transfer)
		d.applySettlement(transfer)
		if d.dropCreate {
			d.dropCreate = false
			record.Status = 0
			d.requests = append(d.requests, record)
			hijacker, ok := w.(http.Hijacker)
			if ok {
				connection, _, err := hijacker.Hijack()
				if err == nil {
					_ = connection.Close()
				}
			}
			return
		}
		record.Status = http.StatusOK
		d.requests = append(d.requests, record)
		writeJSON(w, http.StatusOK, transfer)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/balances/current":
		balance, ok := d.balances[record.Source]
		if !ok {
			record.Status = http.StatusNotFound
			d.requests = append(d.requests, record)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "balance_not_found"})
			return
		}
		record.Status = http.StatusOK
		d.requests = append(d.requests, record)
		writeJSON(w, http.StatusOK, []map[string]any{{"account_type": "CASH", "currency": "USD", "available_amount": json.Number(decimalAmount(balance))}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/__fixture/payments/"):
		id := strings.TrimPrefix(r.URL.Path, "/__fixture/payments/")
		payment, ok := d.payments[id]
		if !ok || payment.CanonicalAccountID != record.Source {
			record.Status = http.StatusNotFound
			d.requests = append(d.requests, record)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment_not_found"})
			return
		}
		record.Status = http.StatusOK
		d.requests = append(d.requests, record)
		writeJSON(w, http.StatusOK, payment)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/__fixture/deposits/"):
		id := strings.TrimPrefix(r.URL.Path, "/__fixture/deposits/")
		deposit, ok := d.deposits[id]
		if !ok || deposit.AccountRef != record.Source {
			record.Status = http.StatusNotFound
			d.requests = append(d.requests, record)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "deposit_not_found"})
			return
		}
		record.Status = http.StatusOK
		d.requests = append(d.requests, record)
		writeJSON(w, http.StatusOK, deposit)
	case r.Method == http.MethodGet && r.URL.Path == listPath:
		query, _ := url.ParseQuery(r.URL.RawQuery)
		matches := make([]map[string]any, 0)
		for _, transfer := range d.transfers {
			if query.Get("request_id") == transfer["request_id"] {
				matches = append(matches, transfer)
			}
		}
		record.Status = http.StatusOK
		d.requests = append(d.requests, record)
		writeJSON(w, http.StatusOK, map[string]any{"items": matches, "page_after": ""})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, listPath+"/"):
		id := strings.TrimPrefix(r.URL.Path, listPath+"/")
		for _, transfer := range d.transfers {
			if transfer["id"] == id {
				record.Status = http.StatusOK
				d.requests = append(d.requests, record)
				writeJSON(w, http.StatusOK, transfer)
				return
			}
		}
		record.Status = http.StatusNotFound
		d.requests = append(d.requests, record)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	default:
		record.Status = http.StatusNotFound
		d.requests = append(d.requests, record)
		http.NotFound(w, r)
	}
}

func (d *Double) applySettlement(transfer map[string]any) {
	id, ok := transfer["id"].(string)
	if !ok || transfer["status"] != "SETTLED" || transfer["settled_at"] == nil || d.settled[id] {
		return
	}
	source, sourceOK := transfer["source_account_id"].(string)
	destination, destinationOK := transfer["destination"].(string)
	amount, amountOK := transfer["amount"].(json.Number)
	cents, err := fixtureAmountCents(amount)
	if !sourceOK || !destinationOK || !amountOK || err != nil || cents <= 0 ||
		d.balances[source] < cents || d.balances[destination] > int64(^uint64(0)>>1)-cents {
		return
	}
	d.balances[source] -= cents
	d.balances[destination] += cents
	d.settled[id] = true
}

func fixtureAmountCents(value json.Number) (int64, error) {
	parts := strings.Split(value.String(), ".")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 2 || strings.HasPrefix(parts[0], "-") {
		return 0, errors.New("provider_fixture_amount_invalid")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > math.MaxInt64/100 {
		return 0, errors.New("provider_fixture_amount_overflow")
	}
	fraction, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || fraction < 0 || fraction > 99 || whole*100 > math.MaxInt64-fraction {
		return 0, errors.New("provider_fixture_amount_invalid")
	}
	return whole*100 + fraction, nil
}

func decimalAmount(cents int64) string {
	return strconv.FormatInt(cents/100, 10) + "." + fmt.Sprintf("%02d", cents%100)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// DuplicateLookupMatch exposes contradictory provider identities for the same
// request. It changes lookup facts only and never applies a second cash move.
func (d *Double) DuplicateLookupMatch(requestID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, transfer := range d.transfers {
		if transfer["request_id"] != requestID {
			continue
		}
		conflict := make(map[string]any, len(transfer))
		for key, value := range transfer {
			conflict[key] = value
		}
		conflict["id"] = "conflicting-" + fmt.Sprint(transfer["id"])
		d.transfers = append(d.transfers, conflict)
		return nil
	}
	return errors.New("provider_fixture_transfer_not_found")
}
