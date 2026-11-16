package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
)

type PolicyBinding struct {
	ID       string
	Hash     string
	Pricing  domain.Pricing
	Verified bool
}

const handlerRequestBudget = 8 * time.Second

type handler struct {
	pool      *pgxpool.Pool
	cfg       config.Config
	now       func() time.Time
	policy    *PolicyBinding
	evaluator *underwriting.Evaluator
	provider  provider.Boundary
}

// NewHandler creates the restricted authenticated offer/decision API.
func NewHandler(pool *pgxpool.Pool, cfg config.Config) http.Handler {
	return NewHandlerWithPolicy(pool, cfg, nil, time.Now)
}

// NewHandlerWithPolicy accepts only a server-owned, independently verified binding.
func NewHandlerWithPolicy(pool *pgxpool.Pool, cfg config.Config, policy *PolicyBinding, now func() time.Time) http.Handler {
	evaluator, _ := underwriting.LoadPinned()
	return NewHandlerWithFunding(pool, cfg, policy, evaluator, provider.Unsupported{}, now)
}

// NewHandlerWithFunding wires the reviewed evaluator and read-only provider boundary.
// A missing pricing binding keeps offer creation and funding reservation gated.
func NewHandlerWithFunding(pool *pgxpool.Pool, cfg config.Config, policy *PolicyBinding, evaluator *underwriting.Evaluator, providerClient provider.Boundary, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	if providerClient == nil {
		providerClient = provider.Unsupported{}
	}
	h := &handler{pool: pool, cfg: cfg, now: now, policy: policy, evaluator: evaluator, provider: providerClient}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/servicing/wallet", h.readProductWallet)
	mux.HandleFunc("GET /api/servicing/portfolio", h.readPortfolio)
	mux.HandleFunc("GET /api/servicing/customers", h.readProductCustomers)
	mux.HandleFunc("GET /api/servicing/customers/{id}", h.readProductCustomers)
	mux.HandleFunc("GET /api/bridges", h.readProductBridges)
	mux.HandleFunc("POST /api/offers", h.createOffer)
	mux.HandleFunc("POST /api/offers/{id}/fund", h.fundOffer)
	mux.HandleFunc("GET /api/offers/{id}", h.readOffer)
	mux.HandleFunc("GET /api/bridges/{id}", h.readBridge)
	mux.HandleFunc("GET /api/bridges/{id}/collections", h.readCollections)
	mux.HandleFunc("POST /api/bridges/{id}/collection-reviews", h.reviewCollections)
	mux.HandleFunc("GET /api/operations/{id}", h.readOperation)
	mux.HandleFunc("POST /api/operations/{id}/reconcile", h.reconcileOperation)
	mux.HandleFunc("GET /api/servicing/state", h.readServicingState)
	mux.HandleFunc("GET /api/servicing/changes", h.readChanges)
	mux.HandleFunc("GET /api/servicing/actor", h.readActor)
	mux.HandleFunc("GET /health/live", h.liveHealth)
	mux.HandleFunc("GET /health/ready", h.readyHealth)
	mux.HandleFunc("POST /api/offers/{id}/acceptances", h.acceptOffer)
	mux.HandleFunc("POST /api/offers/{id}/approvals", h.approveOffer)
	mux.HandleFunc("POST /webhooks/airwallex/servicing", h.servicingWebhook)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		methods, known := fundingRouteMethods(r.URL.Path)
		if !known {
			writeError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
			return
		}
		if !routeAllowsMethod(methods, r.Method) {
			w.Header().Set("Allow", strings.Join(methods, ", "))
			writeError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), handlerRequestBudget)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func fundingRouteMethods(path string) ([]string, bool) {
	if path == "" || path[0] != '/' || strings.Contains(path, "//") || strings.HasSuffix(path, "/") {
		return nil, false
	}
	switch path {
	case "/api/offers":
		return []string{"POST"}, true
	case "/api/servicing/state", "/api/servicing/changes", "/api/servicing/actor", "/api/servicing/portfolio", "/api/servicing/customers", "/api/servicing/wallet", "/api/bridges":
		return []string{"GET"}, true
	case "/health/live", "/health/ready":
		return []string{"GET"}, true
	case "/webhooks/airwallex/servicing":
		return []string{"POST"}, true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "servicing" && parts[2] == "customers" && parts[3] != "" {
		return []string{"GET"}, true
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "offers" && parts[2] != "" {
		return []string{"GET"}, true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "offers" && parts[2] != "" {
		switch parts[3] {
		case "fund":
			return []string{"POST"}, true
		case "acceptances", "approvals":
			return []string{"POST"}, true
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "operations" && parts[2] != "" {
		return []string{"GET"}, true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "operations" && parts[2] != "" && parts[3] == "reconcile" {
		return []string{"POST"}, true
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "bridges" && parts[2] != "" {
		return []string{"GET"}, true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "bridges" && parts[2] != "" {
		switch parts[3] {
		case "collections":
			return []string{"GET"}, true
		case "collection-reviews":
			return []string{"POST"}, true
		}
	}
	return nil, false
}

type readinessResponse struct {
	Mode                       string   `json:"mode"`
	Status                     string   `json:"status"`
	RuntimeReady               bool     `json:"runtime_ready"`
	DatabaseReady              bool     `json:"database_ready"`
	DatabaseName               string   `json:"database_name,omitempty"`
	PolicyReady                bool     `json:"policy_ready"`
	PolicyID                   string   `json:"policy_id"`
	PolicyHash                 string   `json:"policy_hash"`
	ProviderMode               string   `json:"provider_mode"`
	ProviderConfigured         bool     `json:"provider_configured"`
	FixtureOnly                bool     `json:"fixture_only"`
	ProviderAcceptanceVerified bool     `json:"provider_acceptance_verified"`
	FundingEnabled             bool     `json:"funding_enabled"`
	CollectionEnabled          bool     `json:"collection_enabled"`
	LiveDispatchEnabled        bool     `json:"live_dispatch_enabled"`
	SupportedRegimes           []string `json:"supported_regimes"`
	ScarceExecutionSupported   bool     `json:"scarce_execution_supported"`
	Blockers                   []string `json:"blockers"`
}

func (h *handler) liveHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "LIVE"})
}

func (h *handler) readyHealth(w http.ResponseWriter, r *http.Request) {
	response := readinessResponse{
		Mode: h.cfg.Mode, Status: "NOT_READY", PolicyID: h.cfg.PolicyID, PolicyHash: h.cfg.PolicyHash,
		ProviderMode: h.cfg.ProviderMode, FixtureOnly: h.cfg.Mode == config.ModeDemo && h.cfg.ProviderMode == config.ProviderMock,
		ProviderAcceptanceVerified: false, LiveDispatchEnabled: h.cfg.LiveDispatchEnabled,
		SupportedRegimes: []string{"ABUNDANT", "NORMAL"}, ScarceExecutionSupported: false,
		Blockers: []string{},
	}
	if h.pool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		response.DatabaseReady = postgres.NewRepository(h.pool).Ready(ctx) == nil
		if response.DatabaseReady {
			if err := h.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&response.DatabaseName); err != nil {
				response.DatabaseReady = false
				response.DatabaseName = ""
			}
		}
		cancel()
	}
	if !response.DatabaseReady {
		response.Blockers = append(response.Blockers, "DATABASE_OR_MIGRATIONS_NOT_READY")
	}
	response.PolicyReady = h.evaluator != nil && h.policy != nil && h.policy.Verified &&
		h.policy.ID == h.evaluator.PolicyID() && h.policy.Hash == h.evaluator.PolicyHash() &&
		h.policy.Pricing.Validate() == nil && h.policy.Pricing == h.evaluator.Pricing() &&
		h.cfg.PolicyID == h.evaluator.PolicyID() && h.cfg.PolicyHash == h.evaluator.PolicyHash()
	if !response.PolicyReady {
		response.Blockers = append(response.Blockers, "POLICY_PRICING_BINDING_UNAVAILABLE")
	}
	_, providerUnsupported := h.provider.(provider.Unsupported)
	response.ProviderConfigured = h.provider != nil && !providerUnsupported
	if !response.ProviderConfigured {
		response.Blockers = append(response.Blockers, "PROVIDER_BOUNDARY_UNAVAILABLE")
	}
	if h.cfg.Mode != config.ModeDemo && h.cfg.Mode != config.ModeSandbox {
		response.Blockers = append(response.Blockers, "SERVICING_MODE_INVALID")
	}
	if h.cfg.UnderwritingURL == "" || len(h.cfg.UnderwritingSecret) < 32 ||
		h.cfg.ShortfallURL == "" || len(h.cfg.ShortfallSecret) < 32 {
		response.Blockers = append(response.Blockers, "UPSTREAM_READERS_NOT_CONFIGURED")
	}
	response.RuntimeReady = len(response.Blockers) == 0
	response.FundingEnabled = response.RuntimeReady && response.FixtureOnly
	response.CollectionEnabled = response.RuntimeReady && response.FixtureOnly
	if h.cfg.Mode == config.ModeSandbox {
		response.Blockers = append(response.Blockers, "SANDBOX_PROVIDER_CREATE_DISABLED")
	}
	if response.RuntimeReady {
		response.Status = "READY"
		writeJSON(w, http.StatusOK, response)
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, response)
}

func routeAllowsMethod(methods []string, method string) bool {
	for _, allowed := range methods {
		if allowed == method || (allowed == "GET" && method == "HEAD") {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h *handler) auth(w http.ResponseWriter, r *http.Request, write bool) (Identity, bool) {
	id, err := VerifyAssertion(r, h.cfg, h.now())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "UNAUTHORIZED", "message": "Authentication required"}})
		return Identity{}, false
	}
	if write && (id.Role == "COORDINATOR_READER" || id.Role == "UNDERWRITING_READER") {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Scope does not allow this action"}})
		return Identity{}, false
	}
	if write && id.Role == "ACTOR" {
		allowedActorClass := id.CredentialClass == "OPERATOR"
		if strings.HasSuffix(r.URL.Path, "/acceptances") {
			allowedActorClass = id.CredentialClass == "CUSTOMER"
		} else if strings.HasSuffix(r.URL.Path, "/approvals") {
			allowedActorClass = id.CredentialClass == "OPERATOR"
		}
		if !allowedActorClass {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Credential class does not allow this action"}})
			return Identity{}, false
		}
	}
	if !write && r.URL.Path == "/api/servicing/state" && id.Role != "UNDERWRITING_READER" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Scope does not allow this read"}})
		return Identity{}, false
	}
	if !write && r.URL.Path == "/api/servicing/changes" && id.Role != "COORDINATOR_READER" && id.Role != "FUNDING_COORDINATOR" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Scope does not allow this read"}})
		return Identity{}, false
	}
	if h.pool == nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return Identity{}, false
	}
	if err := postgres.NewRepository(h.pool).Ready(r.Context()); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return Identity{}, false
	}
	if id.Role == "COORDINATOR_READER" || id.Role == "UNDERWRITING_READER" {
		return id, true
	}
	if id.Role == "FUNDING_COORDINATOR" {
		return id, true
	}
	var actorID, customerID, actorType string
	err = h.pool.QueryRow(r.Context(), `SELECT actor_id,COALESCE(customer_id,''),actor_type FROM servicing.actors WHERE mode=$1 AND authentication_subject=$2 AND active`, h.cfg.Mode, id.Subject).Scan(&actorID, &customerID, &actorType)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Actor is not active"}})
		return Identity{}, false
	}
	if err != nil {
		writeJSON(w, 503, map[string]any{"error": map[string]string{"code": "STORAGE_UNAVAILABLE", "message": "Storage is unavailable"}})
		return Identity{}, false
	}
	id.ActorID, id.CustomerID, id.ActorType = actorID, customerID, actorType
	if id.Binding != nil && (id.Binding.ActorID != actorID || id.Binding.ActorType != actorType || id.Binding.CustomerID != customerID) {
		writeError(w, 403, "BINDING_CHANGED", "Actor binding no longer matches")
		return Identity{}, false
	}
	if actorType != "CUSTOMER" && actorType != "OPERATOR" || actorType != id.CredentialClass {
		writeJSON(w, 403, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Actor credential class does not match registry role"}})
		return Identity{}, false
	}
	if h.cfg.Mode == config.ModeSandbox && actorType == "CUSTOMER" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Customer delegation is unavailable"}})
		return Identity{}, false
	}
	if write && ((r.URL.Path == "/api/offers" && id.Role != "FUNDING_COORDINATOR" && actorType != "OPERATOR") || (actorType != "CUSTOMER" && actorType != "OPERATOR")) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "FORBIDDEN", "message": "Actor scope does not allow this action"}})
		return Identity{}, false
	}
	return id, true
}
