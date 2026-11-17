package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

// ServerStore exposes only receipt, readiness, and final-observation reads.
type ServerStore interface {
	InboxStore
	Ready(context.Context) error
	ListObservations(context.Context, domain.ListQuery) (domain.Page, error)
	Observation(context.Context, string, string) (*domain.Record, error)
}

// ServerConfig contains server-owned routing/auth configuration, never a live bypass.
type ServerConfig struct {
	Environment              string
	Source                   string
	WebhookSecret            string
	ServiceSecret            string
	ProviderConfigured       bool
	SandboxContractExpiresAt time.Time
	Now                      func() time.Time
}

// Server serves independent liveness, explicit live-readiness gates and internal reads.
type Server struct {
	store                    ServerStore
	receiver                 *WebhookReceiver
	environment              string
	serviceSecret            []byte
	configurationReady       bool
	sandboxContractExpiresAt time.Time
	now                      func() time.Time
}

// NewServer keeps liveness available when database/configuration is unavailable.
// Readiness remains false until the live parser contract is separately verified.
func NewServer(store ServerStore, cfg ServerConfig) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Server{store: store, environment: strings.TrimSpace(cfg.Environment),
		serviceSecret: []byte(cfg.ServiceSecret), now: cfg.Now, sandboxContractExpiresAt: cfg.SandboxContractExpiresAt}
	receiver, err := NewWebhookReceiver(readyInbox{store}, WebhookConfig{Environment: cfg.Environment,
		Source: cfg.Source, Secret: cfg.WebhookSecret, Now: cfg.Now})
	if err == nil {
		s.receiver = receiver
	}
	// Invalid configuration is reflected in readiness and storage-unavailable replies.
	s.configurationReady = store != nil && s.receiver != nil && len(s.serviceSecret) >= 32 && cfg.ProviderConfigured
	return s
}

// readyInbox preserves signature-first receipt while refusing storage through an
// unavailable schema or an incorrectly privileged runtime connection.
type readyInbox struct{ store ServerStore }

func (s readyInbox) Receive(ctx context.Context, delivery domain.Delivery) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.store == nil || s.store.Ready(ctx) != nil {
		return errors.New("inbox_unavailable")
	}
	return s.store.Receive(ctx, delivery)
}

// Routes exposes no raw-evidence, replay, funding, or browser-auth endpoints.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /webhooks/airwallex/shortfalls", func(w http.ResponseWriter, r *http.Request) {
		if s.receiver == nil {
			writeJSON(w, 500, map[string]string{"error": "inbox_unavailable"})
			return
		}
		s.receiver.ServeHTTP(w, r)
	})
	mux.Handle("GET /api/shortfalls", s.authorize(http.HandlerFunc(s.list)))
	mux.Handle("GET /api/shortfalls/{id}", s.authorize(http.HandlerFunc(s.detail)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status := assertionStatus(r, s.serviceSecret, s.now()); status != 0 {
			reason := "unauthorized"
			if status == http.StatusForbidden {
				reason = "forbidden"
			}
			writeJSON(w, status, map[string]string{"error": reason})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	databaseReady := s.store != nil && s.store.Ready(ctx) == nil
	reason := "UNSUPPORTED_WEBHOOK_VERSION"
	if !databaseReady {
		reason = "DATABASE_UNAVAILABLE"
	}
	if !s.configurationReady {
		reason = "CONFIGURATION_UNAVAILABLE"
	}
	live := databaseReady && s.configurationReady && s.environment == "SANDBOX" && s.sandboxContractExpiresAt.After(s.now())
	parser := "unsupported"
	status := http.StatusServiceUnavailable
	if live {
		parser = "verified_sandbox"
		reason = ""
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"ready": live, "service_ready": databaseReady && s.configurationReady,
		"database_ready": databaseReady, "configuration_ready": s.configurationReady,
		"live_detection_ready": live, "live_parser": parser, "reason": reason,
	})

}

type pageCursor struct {
	domain.ReadCursor
	Environment string         `json:"environment"`
	CustomerID  string         `json:"customer_id"`
	Outcome     domain.Outcome `json:"outcome"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	query, err := parseListQuery(r.URL.RawQuery, s.environment)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_query"})
		return
	}
	if s.store == nil {
		writeJSON(w, 503, map[string]string{"error": "observations_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if s.environment == "" || s.store.Ready(ctx) != nil {
		writeJSON(w, 503, map[string]string{"error": "observations_unavailable"})
		return
	}
	page, err := s.store.ListObservations(ctx, query)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "observations_unavailable"})
		return
	}
	var next *string
	if page.Next != nil {
		data, err := json.Marshal(pageCursor{ReadCursor: *page.Next, Environment: s.environment,
			CustomerID: query.CustomerID, Outcome: query.Outcome})
		if err != nil {
			writeJSON(w, 503, map[string]string{"error": "observations_unavailable"})
			return
		}
		encoded := base64.RawURLEncoding.EncodeToString(data)
		next = &encoded
	}
	writeJSON(w, 200, struct {
		Items []domain.Record `json:"items"`
		Next  *string         `json:"next_cursor"`
	}{page.Items, next})
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !domain.ValidID(id) || r.URL.RawQuery != "" {
		writeJSON(w, 400, map[string]string{"error": "invalid_query"})
		return
	}
	if s.store == nil {
		writeJSON(w, 503, map[string]string{"error": "observations_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if s.environment == "" || s.store.Ready(ctx) != nil {
		writeJSON(w, 503, map[string]string{"error": "observations_unavailable"})
		return
	}
	record, err := s.store.Observation(ctx, s.environment, id)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "observations_unavailable"})
		return
	}
	if record == nil {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, 200, record)
}

func parseListQuery(raw, environment string) (domain.ListQuery, error) {
	invalid := errors.New("invalid_query")
	values, err := url.ParseQuery(raw)
	if err != nil {
		return domain.ListQuery{}, invalid
	}
	for key, entries := range values {
		if (key != "customer_id" && key != "outcome" && key != "cursor") || len(entries) != 1 || entries[0] == "" {
			return domain.ListQuery{}, invalid
		}
	}
	query := domain.ListQuery{Environment: environment, CustomerID: values.Get("customer_id"), Outcome: domain.Outcome(values.Get("outcome"))}
	if !utf8.ValidString(query.CustomerID) || strings.TrimSpace(query.CustomerID) != query.CustomerID || len(query.CustomerID) > 200 ||
		strings.IndexFunc(query.CustomerID, unicode.IsControl) >= 0 {
		return domain.ListQuery{}, invalid
	}
	if query.Outcome != "" && query.Outcome != domain.ShortfallDetected && query.Outcome != domain.NoCurrentShortfall && query.Outcome != domain.Ignored {
		return domain.ListQuery{}, invalid
	}
	if value := values.Get("cursor"); value != "" {
		if len(value) > 2048 {
			return domain.ListQuery{}, invalid
		}
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
		if err != nil {
			return domain.ListQuery{}, invalid
		}
		var cursor pageCursor
		decoder := json.NewDecoder(bytes.NewReader(decoded))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF ||
			cursor.CreatedAt.IsZero() || !domain.ValidID(cursor.ID) || cursor.Environment != environment ||
			cursor.CustomerID != query.CustomerID || cursor.Outcome != query.Outcome {
			return domain.ListQuery{}, invalid
		}
		query.After = &cursor.ReadCursor
	}
	return query, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Only allowlisted values and fixed errors reach the response. A transport
	// failure cannot alter already committed observations or receipt.
	_ = json.NewEncoder(w).Encode(value)
}
