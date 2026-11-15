// Package http exposes authenticated underwriting evaluations and bounded reads.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/evaluation"
)

const evaluationBodyLimit = 16_384

type readyStore interface {
	Ready(context.Context) error
}

// ServerConfig contains the service-owned authentication and readiness state.
type ServerConfig struct {
	Mode                      string
	ServiceSecret             []byte
	PolicyReady               bool
	ShortfallClientConfigured bool
	Store                     readyStore
	Evaluation                *evaluation.Service
	Now                       func() time.Time
}

// Server owns the bounded HTTP surface for underwriting.
type Server struct {
	mode           string
	serviceSecret  []byte
	policyReady    bool
	shortfallReady bool
	store          readyStore
	evaluation     *evaluation.Service
	now            func() time.Time
}

type callerContextKey struct{}

// NewServer constructs the API with the supplied server-owned dependencies.
func NewServer(config ServerConfig) *Server {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		mode: config.Mode, serviceSecret: append([]byte(nil), config.ServiceSecret...),
		policyReady: config.PolicyReady, shortfallReady: config.ShortfallClientConfigured,
		store: config.Store, evaluation: config.Evaluation, now: now,
	}
}

// Routes returns the no-store routes and process/readiness endpoints.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.Handle("POST /api/underwriting/evaluations", s.authorize(http.HandlerFunc(s.evaluate)))
	mux.Handle("GET /api/underwriting/evaluations/{id}", s.authorize(http.HandlerFunc(s.getEvaluation)))
	mux.Handle("GET /api/underwriting/evaluations", s.authorize(http.HandlerFunc(s.listEvaluations)))
	mux.Handle("GET /api/underwriting/customers/{customer_id}/context", s.authorize(http.HandlerFunc(s.customerContext)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "INVALID_QUERY")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ready, databaseReady := s.readyState(ctx)
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ready": ready, "source_mode": s.mode, "database_ready": databaseReady,
		"policy_ready": s.policyReady, "shortfall_client_configured": s.shortfallReady,
	})
}

func (s *Server) readyState(ctx context.Context) (bool, bool) {
	databaseReady := s.store != nil && s.store.Ready(ctx) == nil
	return databaseReady && s.policyReady && s.shortfallReady && s.evaluation != nil, databaseReady
}

func (s *Server) requireReady(w http.ResponseWriter, r *http.Request) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ready, _ := s.readyState(ctx)
	if ready {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
	return false
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, status := authenticate(r, s.serviceSecret, s.now().UTC())
		if status != 0 {
			code := "UNAUTHORIZED"
			if status == http.StatusForbidden {
				code = "FORBIDDEN"
			}
			writeError(w, status, code)
			return
		}
		ctx := context.WithValue(r.Context(), callerContextKey{}, caller)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) evaluate(w http.ResponseWriter, r *http.Request) {
	if s.evaluation == nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
		return
	}
	keyValues := r.Header.Values("Idempotency-Key")
	if len(keyValues) != 1 || !validIdempotencyKey(keyValues[0]) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	request, err := decodeEvaluationRequest(w, r)
	if err != nil {
		if errors.Is(err, errPayloadTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE")
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		}
		return
	}
	if !s.requireReady(w, r) {
		return
	}
	caller, _ := r.Context().Value(callerContextKey{}).(string)
	operationCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	outcome, err := s.evaluation.Evaluate(operationCtx, caller, keyValues[0], request)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeRawJSON(w, outcome.HTTPStatus, outcome.Body)
}

func decodeEvaluationRequest(w http.ResponseWriter, r *http.Request) (domain.EvaluationRequest, error) {
	var request domain.EvaluationRequest
	data, err := io.ReadAll(io.LimitReader(r.Body, evaluationBodyLimit+1))
	if err != nil {
		return request, errors.New("invalid_request")
	}
	if len(data) > evaluationBodyLimit {
		return request, errPayloadTooLarge
	}
	if !validJSONObjects(data) {
		return request, errors.New("invalid_request")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return request, errors.New("invalid_request")
	}
	if _, ok := fields["shortfall_id"]; !ok {
		return request, errors.New("invalid_request")
	}
	if _, ok := fields["term_days"]; !ok {
		return request, errors.New("invalid_request")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF ||
		!domain.ValidID(request.ShortfallID) || request.TermDays < 7 || request.TermDays > 90 {
		return domain.EvaluationRequest{}, errors.New("invalid_request")
	}
	return request, nil
}

var errPayloadTooLarge = errors.New("payload_too_large")

func (s *Server) getEvaluation(w http.ResponseWriter, r *http.Request) {
	if s.evaluation == nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	id := r.PathValue("id")
	if !domain.ValidID(id) {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	if !s.requireReady(w, r) {
		return
	}
	body, found, err := s.evaluation.Get(r.Context(), id)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "EVALUATION_NOT_FOUND")
		return
	}
	writeRawJSON(w, http.StatusOK, body)
}

func (s *Server) listEvaluations(w http.ResponseWriter, r *http.Request) {
	if s.evaluation == nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
		return
	}
	query, err := parseListQuery(r.URL.RawQuery, s.mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	if !s.requireReady(w, r) {
		return
	}
	page, err := s.evaluation.List(r.Context(), query.customerID, query.cursor)
	if err != nil {
		writeFailure(w, err)
		return
	}
	var next *string
	if page.NextCursor != nil {
		encoded, err := encodeCursor(*page.NextCursor)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
			return
		}
		next = &encoded
	}
	writeJSON(w, http.StatusOK, struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}{Items: page.Items, NextCursor: next})
}

type listQuery struct {
	customerID string
	cursor     *domain.EvaluationCursor
}

func parseListQuery(raw, mode string) (listQuery, error) {
	values, err := urlParseQuery(raw)
	if err != nil {
		return listQuery{}, err
	}
	for key, entries := range values {
		if (key != "customer_id" && key != "cursor") || len(entries) != 1 || entries[0] == "" {
			return listQuery{}, errors.New("invalid_query")
		}
	}
	customerID := values.Get("customer_id")
	if customerID != "" && !validCustomerID(customerID) {
		return listQuery{}, errors.New("invalid_query")
	}
	query := listQuery{customerID: customerID}
	if encoded := values.Get("cursor"); encoded != "" {
		cursor, err := decodeCursor(encoded, mode, customerID)
		if err != nil {
			return listQuery{}, err
		}
		query.cursor = cursor
	}
	return query, nil
}

func urlParseQuery(raw string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errors.New("invalid_query")
	}
	return values, nil
}

func validCustomerID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 200 &&
		utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validIdempotencyKey(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'A' && char <= 'Z') && !(char >= 'a' && char <= 'z') &&
			!(char >= '0' && char <= '9') && char != '.' && char != '_' && char != ':' && char != '-' {
			return false
		}
	}
	return true
}

func (s *Server) customerContext(w http.ResponseWriter, r *http.Request) {
	if s.evaluation == nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	customerID := r.PathValue("customer_id")
	if !validCustomerID(customerID) {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	if !s.requireReady(w, r) {
		return
	}
	response, found, err := s.evaluation.Context(r.Context(), customerID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "CONTEXT_NOT_FOUND")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeFailure(w http.ResponseWriter, err error) {
	var failure *evaluation.Failure
	if errors.As(err, &failure) {
		writeError(w, failure.Status, failure.Code)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
}

func writeError(w http.ResponseWriter, status int, code string) {
	messages := map[string]string{
		"INVALID_REQUEST":            "The request is invalid.",
		"INVALID_QUERY":              "The query is invalid.",
		"UNAUTHORIZED":               "Authentication is required.",
		"FORBIDDEN":                  "The caller is not allowed to use this endpoint.",
		"SHORTFALL_NOT_FOUND":        "The shortfall was not found.",
		"EVALUATION_NOT_FOUND":       "The evaluation was not found.",
		"CONTEXT_NOT_FOUND":          "The customer context was not found.",
		"IDEMPOTENCY_CONFLICT":       "The idempotency key is already used for a different request.",
		"PAYLOAD_TOO_LARGE":          "The request body exceeds the size limit.",
		"SHORTFALL_NOT_ELIGIBLE":     "The shortfall is not eligible for assessment.",
		"SHORTFALL_LINKAGE_INVALID":  "The shortfall linkage is invalid.",
		"SHORTFALL_UNAVAILABLE":      "The shortfall service is unavailable.",
		"SHORTFALL_RESPONSE_INVALID": "The shortfall service returned an invalid response.",
		"STORAGE_UNAVAILABLE":        "Underwriting storage is unavailable.",
	}
	message := messages[code]
	if message == "" {
		code = "STORAGE_UNAVAILABLE"
		message = messages[code]
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeRawJSON(w http.ResponseWriter, status int, body []byte) {
	if !json.Valid(body) {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
