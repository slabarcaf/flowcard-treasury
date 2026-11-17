package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

const maxWebhookBodyBytes int64 = 10_000_000

// InboxStore durably accepts a delivery before webhook acknowledgement.
type InboxStore interface {
	Receive(context.Context, domain.Delivery) error
}

// WebhookConfig identifies the server-owned subscription and signature clock.
type WebhookConfig struct {
	Environment string
	Source      string
	Secret      string
	Now         func() time.Time
}

// WebhookReceiver authenticates original bytes and stores them without provider calls.
type WebhookReceiver struct {
	store       InboxStore
	environment string
	source      string
	secret      []byte
	now         func() time.Time
}

// NewWebhookReceiver builds a receiver with immutable server-owned source scope.
func NewWebhookReceiver(store InboxStore, cfg WebhookConfig) (*WebhookReceiver, error) {
	environment, source := strings.TrimSpace(cfg.Environment), strings.TrimSpace(cfg.Source)
	if store == nil || environment == "" || source == "" || cfg.Secret == "" {
		return nil, errors.New("webhook_configuration_invalid")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &WebhookReceiver{
		store: store, environment: environment, source: source,
		secret: []byte(cfg.Secret), now: cfg.Now,
	}, nil
}

// ServeHTTP serves only the dedicated shortfall webhook path.
func (r *WebhookReceiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/webhooks/airwallex/shortfalls" {
		http.NotFound(w, request)
		return
	}
	if request.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeWebhookJSON(w, http.StatusMethodNotAllowed, `{"error":"method_not_allowed"}`)
		return
	}

	// MaxBytesReader checks streamed bytes, including chunked bodies and a lying
	// Content-Length. It reads at most one extra byte to detect overflow.
	body := http.MaxBytesReader(w, request.Body, maxWebhookBodyBytes)
	defer func() {
		// A close error cannot change receipt; do not expose transport error text.
		_ = body.Close()
	}()
	raw, err := io.ReadAll(body)
	if err != nil {
		var limitError *http.MaxBytesError
		if errors.As(err, &limitError) {
			writeWebhookJSON(w, http.StatusRequestEntityTooLarge, `{"error":"payload_too_large"}`)
		} else {
			writeWebhookJSON(w, http.StatusBadRequest, `{"error":"invalid_event"}`)
		}
		return
	}
	if !r.validSignature(request.Header, raw) {
		writeWebhookJSON(w, http.StatusUnauthorized, `{"error":"invalid_signature"}`)
		return
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		writeWebhookJSON(w, http.StatusBadRequest, `{"error":"invalid_event"}`)
		return
	}
	var id, name string
	if json.Unmarshal(envelope["id"], &id) != nil || strings.TrimSpace(id) == "" ||
		json.Unmarshal(envelope["name"], &name) != nil || strings.TrimSpace(name) == "" {
		writeWebhookJSON(w, http.StatusBadRequest, `{"error":"invalid_event"}`)
		return
	}
	if err := r.store.Receive(request.Context(), domain.Delivery{
		Environment: r.environment, Source: r.source,
		WebhookID: id, EventName: name, RawBody: raw,
	}); err != nil {
		writeWebhookJSON(w, http.StatusInternalServerError, `{"error":"inbox_unavailable"}`)
		return
	}
	writeWebhookJSON(w, http.StatusOK, `{"received":true}`)
}

func (r *WebhookReceiver) validSignature(headers http.Header, raw []byte) bool {
	timestamps, signatures := headers.Values("X-Timestamp"), headers.Values("X-Signature")
	if len(timestamps) != 1 || len(signatures) != 1 || timestamps[0] == "" {
		return false
	}
	timestamp := timestamps[0]
	if len(signatures[0]) != sha256.Size*2 {
		return false
	}
	supplied, err := hex.DecodeString(signatures[0])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, r.secret)
	// hash.Hash.Write cannot fail. Preserve the exact original timestamp string.
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(raw)
	if !hmac.Equal(supplied, mac.Sum(nil)) {
		return false
	}

	// Numeric syntax/range and time bounds follow successful HMAC verification.
	// Node's digit-only contract is retained, including leading zeroes.
	for _, ch := range timestamp {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	milliseconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	// Comparing times avoids overflow when a valid int64 timestamp is very large.
	// No lower age bound applies to an otherwise valid signed delivery.
	return !time.UnixMilli(milliseconds).After(r.now().UTC().Add(5 * time.Minute))
}

func writeWebhookJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// Responses contain fixed safe text; transport failures cannot undo receipt.
	_, _ = io.WriteString(w, body)
}
