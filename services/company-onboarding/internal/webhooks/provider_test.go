package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
)

var providerTestSecret = []byte("synthetic-notification-url-secret")

const providerTestBody = `{"id":"evt_test","name":"account.updated","data":{"id":"acct_test"}}`

type providerTestRepository struct {
	items []postgres.WebhookInboxItem
	err   error
}

func (r *providerTestRepository) InsertWebhookOnce(_ context.Context, item postgres.WebhookInboxItem) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	r.items = append(r.items, item)
	return true, nil
}
func signedProviderRequest(body, timestamp string) *http.Request {
	r := httptest.NewRequest("POST", "/webhooks/airwallex", strings.NewReader(body))
	mac := hmac.New(sha256.New, providerTestSecret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte(body))
	r.Header.Set("X-Timestamp", timestamp)
	r.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	return r
}

type timeoutBody struct{}

func (timeoutBody) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }
func (timeoutBody) Close() error             { return nil }

func TestProviderWebhookAuthentication(t *testing.T) {
	tests := []struct {
		name, body, timestamp string
		change                func(*http.Request)
		status                int
	}{
		{name: "wrong method", body: providerTestBody, timestamp: "1", change: func(r *http.Request) { r.Method = "GET" }, status: 405},
		{name: "body timeout", body: providerTestBody, timestamp: "1", change: func(r *http.Request) { r.Body = timeoutBody{} }, status: 408},
		{name: "valid", body: providerTestBody, timestamp: "1", status: 200},
		{name: "old timestamp", body: providerTestBody, timestamp: "1000", status: 200},
		{name: "uppercase signature", body: providerTestBody, timestamp: "1", change: func(r *http.Request) { r.Header.Set("X-Signature", strings.ToUpper(r.Header.Get("X-Signature"))) }, status: 200},
		{name: "future timestamp", body: providerTestBody, timestamp: strconv.FormatInt(time.Now().Add(6*time.Minute).UnixMilli(), 10), status: 401},
		{name: "signed invalid timestamp", body: providerTestBody, timestamp: "invalid", status: 400},
		{name: "zero timestamp", body: providerTestBody, timestamp: "0", status: 400},
		{name: "duplicate timestamp", body: providerTestBody, timestamp: "1", change: func(r *http.Request) { r.Header.Add("X-Timestamp", "1") }, status: 401},
		{name: "duplicate signature", body: providerTestBody, timestamp: "1", change: func(r *http.Request) { r.Header.Add("X-Signature", r.Header.Get("X-Signature")) }, status: 401},
		{name: "tampered body", body: providerTestBody, timestamp: "1", change: func(r *http.Request) { r.Body = http.NoBody }, status: 401},
		{name: "JWT not provider signature", body: providerTestBody, timestamp: "1", change: func(r *http.Request) {
			r.Header.Del("X-Signature")
			r.Header.Set("Authorization", "Bearer internal-assertion")
		}, status: 401},
		{name: "duplicate JSON field", body: `{"id":"one","id":"two","name":"account.updated"}`, timestamp: "1", status: 400},
		{name: "nested duplicate", body: `{"id":"one","name":"account.updated","data":{"id":"a","id":"b"}}`, timestamp: "1", status: 400},
		{name: "trailing object", body: providerTestBody + ` {}`, timestamp: "1", status: 400},
		{name: "array", body: `[]`, timestamp: "1", status: 400},
		{name: "header identity refused", body: `{"name":"account.updated"}`, timestamp: "1", change: func(r *http.Request) { r.Header.Set("X-Provider-Event-Id", "evt_forged") }, status: 400},
		{name: "case alias refused", body: `{"ID":"evt","name":"account.updated"}`, timestamp: "1", status: 400},
		{name: "canonical and case alias", body: `{"id":"one","ID":"two","name":"account.updated"}`, timestamp: "1", status: 400},
		{name: "conflicting account aliases", body: `{"id":"evt","name":"account.updated","accountId":"a","account_id":"b"}`, timestamp: "1", status: 400},
		{name: "environment mismatch", body: `{"id":"evt","name":"account.updated","environment":"production"}`, timestamp: "1", status: 400},
		{name: "wrong event owner", body: `{"id":"evt","name":"deposit.settled"}`, timestamp: "1", status: 410},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &providerTestRepository{}
			h, err := NewProviderHandler(repo, providerTestSecret, "sandbox")
			if err != nil {
				t.Fatal(err)
			}
			r := signedProviderRequest(test.body, test.timestamp)
			if test.change != nil {
				test.change(r)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d want=%d: %s", w.Code, test.status, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
			if test.status == 200 {
				if len(repo.items) != 1 || repo.items[0].Environment != "sandbox" || string(repo.items[0].RawPayload) != test.body {
					t.Fatal("original receipt/scope not preserved")
				}
			} else if len(repo.items) != 0 {
				t.Fatal("denied event reached persistence")
			}
		})
	}
}

func TestProviderWebhookReceiptFailuresAndLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		secret []byte
		repo   Repository
		status int
	}{
		{"disabled", nil, &providerTestRepository{}, 503},
		{"unavailable storage", providerTestSecret, nil, 503},
		{"failed commit", providerTestSecret, &providerTestRepository{err: errors.New("private DB failure")}, 500},
		{"conflict", providerTestSecret, &providerTestRepository{err: postgres.ErrWebhookEventConflict}, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := "sandbox"
			if len(test.secret) == 0 {
				env = ""
			}
			h, err := NewProviderHandler(test.repo, test.secret, env)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, signedProviderRequest(providerTestBody, "1"))
			if w.Code != test.status || strings.Contains(w.Body.String(), "private DB") {
				t.Fatalf("unsafe receipt failure: %d", w.Code)
			}
		})
	}
	for _, extra := range []int{0, 1} {
		t.Run(strconv.Itoa(extra), func(t *testing.T) {
			prefix := `{"id":"limit","name":"account.updated","padding":"`
			suffix := `"}`
			body := prefix + strings.Repeat("x", maxProviderBodyBytes-len(prefix)-len(suffix)+extra) + suffix
			repo := &providerTestRepository{}
			h, _ := NewProviderHandler(repo, providerTestSecret, "sandbox")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, signedProviderRequest(body, "1"))
			want := 200
			if extra > 0 {
				want = 413
			}
			if w.Code != want {
				t.Fatalf("body limit: status=%d want=%d", w.Code, want)
			}
		})
	}
}

func TestProviderJSONDepth(t *testing.T) {
	body := `{"id":"deep","name":"account.updated","data":` + strings.Repeat(`[`, 65) + `0` + strings.Repeat(`]`, 65) + `}`
	if strictObject([]byte(body)) {
		t.Fatal("unbounded JSON depth")
	}
	if !json.Valid([]byte(body)) {
		t.Fatal("invalid depth fixture")
	}
}
