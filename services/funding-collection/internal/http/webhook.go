package http

import (
	"bytes"
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

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

const maxServicingWebhookBody = 10_000_000

type servicingWebhookEnvelope struct {
	ID         string
	Name       string
	TargetKind string
	RequestID  string
	CustomerID string
	CardID     string
	DepositID  string
	State      string
	Blocked    string
}

type demoTransferWake struct {
	RequestID string `json:"request_id"`
}

type demoDepositWake struct {
	CustomerID string `json:"customer_id"`
	CardID     string `json:"card_id"`
	DepositID  string `json:"deposit_id"`
}

func (h *handler) servicingWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	if h.pool == nil || len(h.cfg.AirwallexWebhookSecret) < 32 {
		writeError(w, http.StatusServiceUnavailable, "WEBHOOK_UNAVAILABLE", "Signed webhook receipt is not configured")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxServicingWebhookBody)
	defer func() { _ = limited.Close() }()
	raw, err := io.ReadAll(limited)
	if err != nil {
		var limitError *http.MaxBytesError
		if errors.As(err, &limitError) {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Webhook body exceeds the limit")
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_EVENT", "Webhook body could not be read")
		}
		return
	}
	if !validServicingSignature(r.Header, raw, []byte(h.cfg.AirwallexWebhookSecret), h.now()) {
		writeError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "Webhook signature is invalid")
		return
	}
	event, err := parseServicingEnvelope(raw, domain.Mode(h.cfg.Mode))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_EVENT", "Webhook envelope is invalid")
		return
	}
	_, err = postgres.NewRepository(h.pool).ReceiveServicingWebhook(r.Context(), postgres.ServicingWebhookReceipt{
		Mode: domain.Mode(h.cfg.Mode), Source: "AIRWALLEX_SERVICING", EventID: event.ID,
		EventName: event.Name, TargetKind: event.TargetKind, RequestID: event.RequestID,
		CustomerID: event.CustomerID, CardID: event.CardID, DepositID: event.DepositID,
		InitialState: event.State, BlockedCode: event.Blocked, RawBody: raw})
	if errors.Is(err, postgres.ErrWebhookBodyConflict) {
		writeError(w, http.StatusConflict, "WEBHOOK_EVENT_CONFLICT", "Event ID already has a different signed body")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "WEBHOOK_INBOX_UNAVAILABLE", "Signed event was not durably stored")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": true})
}

func validServicingSignature(headers http.Header, raw, secret []byte, now time.Time) bool {
	timestamps, signatures := headers.Values("X-Timestamp"), headers.Values("X-Signature")
	if len(timestamps) != 1 || len(signatures) != 1 || timestamps[0] == "" || len(signatures[0]) != sha256.Size*2 {
		return false
	}
	supplied, err := hex.DecodeString(signatures[0])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamps[0]))
	_, _ = mac.Write(raw)
	if !hmac.Equal(supplied, mac.Sum(nil)) {
		return false
	}
	for _, digit := range timestamps[0] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	milliseconds, err := strconv.ParseInt(timestamps[0], 10, 64)
	if err != nil {
		return false
	}
	return !time.UnixMilli(milliseconds).After(now.UTC().Add(5 * time.Minute))
}

func parseServicingEnvelope(raw []byte, mode domain.Mode) (servicingWebhookEnvelope, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return servicingWebhookEnvelope{}, errors.New("servicing_webhook_json_invalid")
	}
	var event servicingWebhookEnvelope
	if json.Unmarshal(fields["id"], &event.ID) != nil || json.Unmarshal(fields["name"], &event.Name) != nil ||
		strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Name) == "" ||
		event.ID != strings.TrimSpace(event.ID) || event.Name != strings.TrimSpace(event.Name) ||
		len(event.ID) > 256 || len(event.Name) > 128 {
		return servicingWebhookEnvelope{}, errors.New("servicing_webhook_identity_invalid")
	}
	if mode == domain.ModeSandbox {
		event.TargetKind, event.State, event.Blocked = "UNSUPPORTED", "HELD", "LIVE_WEBHOOK_EXTRACTION_UNSUPPORTED"
		return event, nil
	}
	switch event.Name {
	case "DEMO_TRANSFER_UPDATED":
		var data demoTransferWake
		if decodeStrictWebhook(fields["data"], &data) != nil || !validWebhookReference(data.RequestID, 50) {
			return servicingWebhookEnvelope{}, errors.New("servicing_webhook_transfer_reference_invalid")
		}
		event.TargetKind, event.RequestID, event.State = "FUND_REQUEST", data.RequestID, "PENDING"
	case "DEMO_DEPOSIT_SETTLED":
		var data demoDepositWake
		if decodeStrictWebhook(fields["data"], &data) != nil || data.CustomerID == "" || len(data.CustomerID) > 256 ||
			data.CardID == "" || len(data.CardID) > 256 || !validWebhookReference(data.DepositID, 256) {
			return servicingWebhookEnvelope{}, errors.New("servicing_webhook_deposit_reference_invalid")
		}
		event.TargetKind, event.CustomerID, event.CardID = "DEPOSIT", data.CustomerID, data.CardID
		event.DepositID, event.State = data.DepositID, "PENDING"
	default:
		event.TargetKind, event.State, event.Blocked = "UNSUPPORTED", "HELD", "DEMO_WEBHOOK_EVENT_UNSUPPORTED"
	}
	return event, nil
}

func validWebhookReference(value string, maximum int) bool {
	if value == "" || len(value) > maximum || value == "." || value == ".." || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' ||
			character == '.' || character == ':') {
			return false
		}
	}
	return true
}

func decodeStrictWebhook(raw []byte, target any) error {
	if len(raw) == 0 || !json.Valid(raw) {
		return errors.New("servicing_webhook_data_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("servicing_webhook_data_trailing")
	}
	return nil
}
