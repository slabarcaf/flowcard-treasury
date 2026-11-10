package webhooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
)

const maxWebhookBodyBytes = 1_048_576 // 1 MiB max body for webhooks

// Repository defines persistence dependencies for webhook ingestion.
type Repository interface {
	InsertWebhookOnce(ctx context.Context, item postgres.WebhookInboxItem) (bool, error)
}

// Handler handles authenticated incoming webhook deliveries.
type Handler struct {
	repo    Repository
	onNudge func()
}

// NewHandler constructs a Webhook HTTP Handler.
func NewHandler(repo Repository, onNudge func()) *Handler {
	return &Handler{
		repo:    repo,
		onNudge: onNudge,
	}
}

// ServeHTTP receives, validates, and durably records incoming platform webhook events.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		sendJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "payload too large"})
		return
	}

	var payload InboundWebhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json payload"})
		return
	}

	cleanID := strings.TrimSpace(payload.ID)
	if cleanID == "" {
		cleanID = strings.TrimSpace(r.Header.Get("X-Provider-Event-Id"))
	}
	cleanName := strings.TrimSpace(payload.Name)

	if cleanID == "" || cleanName == "" {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "missing event id or name"})
		return
	}

	accountID := strings.TrimSpace(payload.ExtractAccountID())
	env := strings.TrimSpace(payload.Environment)

	item := postgres.WebhookInboxItem{
		EventID:     cleanID,
		AccountID:   accountID,
		EventType:   cleanName,
		Environment: env,
		RawPayload:  json.RawMessage(bodyBytes),
	}

	// Durable write before acknowledging. No false ack.
	_, err = h.repo.InsertWebhookOnce(r.Context(), item)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist webhook"})
		return
	}

	// Acknowledge receipt only after durable storage commits
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(WebhookAcknowledgeResponse{
		Received: true,
		EventID:  cleanID,
	})

	if h.onNudge != nil {
		go h.onNudge()
	}
}

func sendJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
