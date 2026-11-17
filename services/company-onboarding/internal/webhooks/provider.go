package webhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxProviderBodyBytes = 10 * 1024 * 1024

// ProviderHandler authenticates original provider bytes before inbox delivery.
type ProviderHandler struct {
	receiver    *Handler
	secret      []byte
	environment string
}

// NewProviderHandler keeps absent configuration disabled and rejects partial scope.
func NewProviderHandler(repo Repository, secret []byte, environment string) (*ProviderHandler, error) {
	if (len(secret) == 0) != (environment == "") || (environment != "" && environment != "sandbox" && environment != "production") || (len(secret) > 0 && strings.TrimSpace(string(secret)) == "") {
		return nil, errors.New("provider webhook secret and environment must be configured together")
	}
	return &ProviderHandler{receiver: NewHandler(repo, nil), secret: append([]byte(nil), secret...), environment: environment}, nil
}

// ServeHTTP accepts only authenticated account/RFI deliveries, with durable acknowledgement.
func (h *ProviderHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		sendJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if len(h.secret) == 0 || h.receiver.repo == nil {
		sendJSON(w, 503, map[string]string{"error": "webhook intake unavailable"})
		return
	}
	timestamps, signatures := r.Header.Values("X-Timestamp"), r.Header.Values("X-Signature")
	if len(timestamps) != 1 || len(signatures) != 1 || timestamps[0] == "" || len(signatures[0]) != 64 {
		sendJSON(w, 401, map[string]string{"error": "invalid signature"})
		return
	}
	// Bound slow request bodies without changing timeouts on upload/API routes.
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(15 * time.Second)); err == nil {
		defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	} else if !errors.Is(err, http.ErrNotSupported) {
		sendJSON(w, 503, map[string]string{"error": "webhook transport unavailable"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProviderBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		var timeout net.Error
		switch {
		case errors.As(err, &tooLarge):
			sendJSON(w, 413, map[string]string{"error": "payload too large"})
		case errors.As(err, &timeout) && timeout.Timeout():
			sendJSON(w, 408, map[string]string{"error": "webhook body timeout"})
		default:
			sendJSON(w, 400, map[string]string{"error": "invalid webhook body"})
		}
		return
	}
	supplied, err := hex.DecodeString(signatures[0])
	mac := hmac.New(sha256.New, h.secret)
	_, _ = mac.Write([]byte(timestamps[0]))
	_, _ = mac.Write(body)
	if err != nil || !hmac.Equal(supplied, mac.Sum(nil)) {
		sendJSON(w, 401, map[string]string{"error": "invalid signature"})
		return
	}
	validTimestamp := len(timestamps[0]) <= 19
	for _, c := range timestamps[0] {
		if c < '0' || c > '9' {
			validTimestamp = false
		}
	}
	millis, err := strconv.ParseInt(timestamps[0], 10, 64)
	if !validTimestamp || err != nil || millis <= 0 {
		sendJSON(w, 400, map[string]string{"error": "invalid timestamp"})
		return
	}
	if millis > time.Now().Add(5*time.Minute).UnixMilli() {
		sendJSON(w, 401, map[string]string{"error": "future timestamp"})
		return
	}
	if !strictObject(body) {
		sendJSON(w, 400, map[string]string{"error": "invalid json payload"})
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		sendJSON(w, 400, map[string]string{"error": "invalid json payload"})
		return
	}
	for key := range fields {
		for _, canonical := range []string{"id", "name", "accountId", "account_id", "environment"} {
			if key != canonical && strings.EqualFold(key, canonical) {
				sendJSON(w, 400, map[string]string{"error": "ambiguous event fields"})
				return
			}
		}
	}
	// Canonical top-level keys supply identity, never case-insensitive aliases or headers.
	var payload InboundWebhookPayload
	for _, field := range []struct {
		name  string
		value any
	}{
		{"id", &payload.ID}, {"name", &payload.Name}, {"accountId", &payload.AccountID},
		{"account_id", &payload.AccountIDAlt}, {"environment", &payload.Environment},
	} {
		if raw, ok := fields[field.name]; ok {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, field.value) != nil {
				sendJSON(w, 400, map[string]string{"error": "invalid event fields"})
				return
			}
		}
	}
	payload.Data = fields["data"]
	if payload.AccountID != "" && payload.AccountIDAlt != "" && payload.AccountID != payload.AccountIDAlt {
		sendJSON(w, 400, map[string]string{"error": "conflicting account fields"})
		return
	}
	if payload.ID == "" || payload.Name == "" || strings.TrimSpace(payload.ID) != payload.ID || strings.TrimSpace(payload.Name) != payload.Name || len(payload.ID) > 256 || len(payload.Name) > 256 {
		sendJSON(w, 400, map[string]string{"error": "missing or invalid event id or name"})
		return
	}
	if !strings.HasPrefix(payload.Name, "account.") && !strings.HasPrefix(payload.Name, "rfi.") {
		sendJSON(w, 410, map[string]string{"error": "unsupported webhook event"})
		return
	}
	if payload.Environment != "" && payload.Environment != h.environment {
		sendJSON(w, 400, map[string]string{"error": "event environment mismatch"})
		return
	}
	h.receiver.record(w, r, body, payload, h.environment, false)
}

func strictObject(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	if !jsonContainer(decoder, json.Delim('{'), 1) {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF
}

func jsonContainer(decoder *json.Decoder, opening json.Delim, depth int) bool {
	if depth > 64 {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		if opening == '{' {
			key, err := decoder.Token()
			s, ok := key.(string)
			if err != nil || !ok || seen[s] {
				return false
			}
			seen[s] = true
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if delimiter, ok := token.(json.Delim); ok {
			if (delimiter != '{' && delimiter != '[') || !jsonContainer(decoder, delimiter, depth+1) {
				return false
			}
		}
	}
	closing, err := decoder.Token()
	expected := json.Delim('}')
	if opening == '[' {
		expected = ']'
	}
	return err == nil && closing == expected
}
