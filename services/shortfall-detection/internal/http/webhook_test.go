package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/testutil"
)

type receiptSpy struct {
	deliveries []domain.Delivery
	err        error
}

func (s *receiptSpy) Receive(_ context.Context, d domain.Delivery) error {
	s.deliveries = append(s.deliveries, d)
	return s.err
}

func signReceipt(raw []byte, timestamp string) string {
	mac := hmac.New(sha256.New, []byte("synthetic-webhook-secret"))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestReceiverContract(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{ "id":"receiver-1", "name":"unrelated", "environment":"untrusted", "unknown":42 }`)
	for _, tc := range []struct {
		name, timestamp                                               string
		body                                                          []byte
		status                                                        int
		duplicateTimestamp, duplicateSignature, badSignature, altered bool
	}{
		{name: "original_bytes", timestamp: strconv.FormatInt(now.UnixMilli(), 10), body: raw, status: 200},
		{name: "changed_bytes", timestamp: "0", body: raw, status: 401, altered: true},
		{name: "missing_timestamp", body: raw, status: 401},
		{name: "timestamp_overflow", timestamp: "9223372036854775808", body: raw, status: 401},
		{name: "timestamp_malformed", timestamp: "12abc", body: raw, status: 401},
		{name: "duplicate_timestamp", timestamp: "0", body: raw, status: 401, duplicateTimestamp: true},
		{name: "duplicate_signature", timestamp: "0", body: raw, status: 401, duplicateSignature: true},
		{name: "bad_signature", timestamp: "0", body: raw, status: 401, badSignature: true},
		{name: "old_signed", timestamp: "0", body: raw, status: 200},
		{name: "leading_zero_timestamp", timestamp: "0000", body: raw, status: 200},
		{name: "future_boundary", timestamp: strconv.FormatInt(now.Add(5*time.Minute).UnixMilli(), 10), body: raw, status: 200},
		{name: "future_excess", timestamp: strconv.FormatInt(now.Add(5*time.Minute).UnixMilli()+1, 10), body: raw, status: 401},
		{name: "null_envelope", timestamp: "0", body: []byte(`null`), status: 400},
		{name: "array_envelope", timestamp: "0", body: []byte(`[]`), status: 400},
		{name: "invalid_json", timestamp: "0", body: []byte(`{`), status: 400},
		{name: "wrong_id_type", timestamp: "0", body: []byte(`{"id":1,"name":"x"}`), status: 400},
		{name: "blank_name", timestamp: "0", body: []byte(`{"id":"1","name":" "}`), status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &receiptSpy{}
			clockCalls := 0
			handler, err := NewWebhookReceiver(store, WebhookConfig{Environment: "test", Source: "configured-source", Secret: "synthetic-webhook-secret", Now: func() time.Time { clockCalls++; return now }})
			testutil.Check(t, "construct receiver", err)
			body := tc.body
			signature := signReceipt(body, tc.timestamp)
			if tc.altered {
				body = append([]byte(" "), body...)
			}
			request := httptest.NewRequest("POST", "/webhooks/airwallex/shortfalls", bytes.NewReader(body))
			if tc.timestamp != "" {
				request.Header.Set("x-timestamp", tc.timestamp)
			}
			request.Header.Set("x-signature", signature)
			if tc.duplicateTimestamp {
				request.Header.Add("x-timestamp", tc.timestamp)
			}
			if tc.duplicateSignature {
				request.Header.Add("x-signature", signature)
			}
			if tc.badSignature {
				request.Header.Set("x-signature", strings.Repeat("0", 64))
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("receipt status=%d, want %d", response.Code, tc.status)
			}
			if tc.status == 200 {
				if len(store.deliveries) != 1 || !bytes.Equal(store.deliveries[0].RawBody, body) || store.deliveries[0].Environment != "test" || store.deliveries[0].Source != "configured-source" {
					t.Fatal("original bytes/server scope not preserved")
				}
			} else if len(store.deliveries) != 0 {
				t.Fatal("invalid request accessed inbox")
			}
			if (tc.badSignature || tc.altered) && clockCalls != 0 {
				t.Fatal("clock used before signature validation")
			}
		})
	}
	for _, size := range []int{10_000_000, 10_000_001} {
		t.Run("chunked_size_"+strconv.Itoa(size), func(t *testing.T) {
			prefix, suffix := `{"id":"size","name":"unrelated","padding":"`, `"}`
			body := []byte(prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix)
			store := &receiptSpy{}
			handler, err := NewWebhookReceiver(store, WebhookConfig{Environment: "test", Source: "source", Secret: "synthetic-webhook-secret", Now: func() time.Time { return now }})
			testutil.Check(t, "construct size receiver", err)
			server := httptest.NewServer(handler)
			defer server.Close()
			request, err := http.NewRequest("POST", server.URL+"/webhooks/airwallex/shortfalls", bytes.NewReader(body))
			testutil.Check(t, "size request", err)
			request.ContentLength = -1
			request.Header.Set("x-timestamp", "0")
			request.Header.Set("x-signature", signReceipt(body, "0"))
			response, err := server.Client().Do(request)
			testutil.Check(t, "chunked delivery", err)
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			want := 200
			if size > 10_000_000 {
				want = 413
			}
			if response.StatusCode != want {
				t.Fatalf("chunked size %d status=%d, want %d", size, response.StatusCode, want)
			}
		})
	}
	t.Run("storage_failure", func(t *testing.T) {
		handler, err := NewWebhookReceiver(&receiptSpy{err: errors.New("private-storage-marker")}, WebhookConfig{Environment: "test", Source: "source", Secret: "synthetic-webhook-secret"})
		testutil.Check(t, "construct failure receiver", err)
		r := httptest.NewRequest("POST", "/webhooks/airwallex/shortfalls", bytes.NewReader(raw))
		r.Header.Set("x-timestamp", "0")
		r.Header.Set("x-signature", signReceipt(raw, "0"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 500 || strings.Contains(w.Body.String(), "private-storage-marker") {
			t.Fatal("storage error response leaks or misclassifies failure")
		}
	})
	t.Run("durable_dedup_conflict", func(t *testing.T) {
		db := testutil.NewDatabase(t, "receiver")
		handler, err := NewWebhookReceiver(db.Repo, WebhookConfig{Environment: "test", Source: "source", Secret: "synthetic-webhook-secret"})
		testutil.Check(t, "construct durable receiver", err)
		deliver := func(body []byte) {
			r := httptest.NewRequest("POST", "/webhooks/airwallex/shortfalls", bytes.NewReader(body))
			r.Header.Set("x-timestamp", "0")
			r.Header.Set("x-signature", signReceipt(body, "0"))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("durable status=%d", w.Code)
			}
		}
		deliver(raw)
		deliver(raw)
		var count int
		testutil.Check(t, "count inbox", db.Admin.QueryRow(context.Background(), "SELECT count(*) FROM shortfall.webhook_inbox").Scan(&count))
		if count != 1 {
			t.Fatalf("identical inbox rows=%d, want 1", count)
		}
		_, err = db.Admin.Exec(context.Background(), "UPDATE shortfall.webhook_inbox SET state='PROCESSING',lease_token=gen_random_uuid(),lease_expires_at=now()+interval '60 seconds'")
		testutil.Check(t, "lease original fixture", err)
		deliver(append([]byte(" "), raw...))
		var state string
		var revoked, protected bool
		testutil.Check(t, "inspect durable conflict", db.Admin.QueryRow(context.Background(), "SELECT state,lease_token IS NULL AND lease_expires_at IS NULL,raw_body=$1 AND conflicted FROM shortfall.webhook_inbox", raw).Scan(&state, &revoked, &protected))
		if state != "HELD" || !revoked || !protected {
			t.Fatal("conflict did not fence lease/preserve original bytes")
		}
		testutil.Check(t, "count conflicts", db.Admin.QueryRow(context.Background(), "SELECT count(*) FROM shortfall.inbox_conflicts").Scan(&count))
		if count != 1 {
			t.Fatal("conflict evidence not durable")
		}
	})
}
