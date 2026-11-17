package testutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/provider"
)

// Fixture loads a synthetic source fixture without changing it.
func Fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ServiceDir(), "internal/provider/testdata", name))
	Check(t, "read synthetic fixture", err)
	return data
}

// ReferenceFunc is a local-only reference dependency, never a runtime adapter.
type ReferenceFunc func(context.Context, domain.Delivery) (provider.Reference, error)

// Extract invokes the explicit harness function.
func (f ReferenceFunc) Extract(ctx context.Context, d domain.Delivery) (provider.Reference, error) {
	return f(ctx, d)
}

// EvidenceFunc supplies configuration facts, not eligibility or a final result.
type EvidenceFunc func(context.Context, domain.Mapping, string) (domain.ContractEvidence, error)

// Evidence invokes the explicit harness dependency.
func (f EvidenceFunc) Evidence(ctx context.Context, m domain.Mapping, version string) (domain.ContractEvidence, error) {
	return f(ctx, m, version)
}

// Resolver injects only the manifest's references/config facts; real SQL/HTTP remains mandatory.
func Resolver(t *testing.T, mappings provider.MappingResolver) *provider.Resolver {
	t.Helper()
	var manifest struct {
		Reference struct {
			Account    string `json:"account_id"`
			Card       string `json:"card_id"`
			Parent     string `json:"card_transaction_id"`
			Version    string `json:"version"`
			Provenance string `json:"provenance"`
		} `json:"resolver_test_double_input"`
		Evidence domain.ContractEvidence `json:"contract_evidence_test_double"`
	}
	Check(t, "parse manifest", json.Unmarshal(Fixture(t, "manifest.json"), &manifest))
	return &provider.Resolver{Mappings: mappings,
		References: ReferenceFunc(func(_ context.Context, _ domain.Delivery) (provider.Reference, error) {
			return provider.Reference{AccountID: manifest.Reference.Account, CardID: manifest.Reference.Card,
				TransactionID: manifest.Reference.Parent, Version: manifest.Reference.Version, Provenance: manifest.Reference.Provenance}, nil
		}),
		Evidence: EvidenceFunc(func(_ context.Context, m domain.Mapping, version string) (domain.ContractEvidence, error) {
			if m.AccountID != manifest.Reference.Account || m.CardID != manifest.Reference.Card || version != manifest.Reference.Version {
				return domain.ContractEvidence{}, &domain.Failure{Reason: "FIXTURE_EVIDENCE_MISMATCH"}
			}
			return manifest.Evidence, nil
		}),
	}
}

// Mock is loopback-only provider HTTP, with counters and controlled response variants.
type Mock struct {
	Server        *httptest.Server
	Client        *provider.Client
	Mu            sync.Mutex
	Events        []provider.Event
	BalanceBody   string
	EventStatus   int
	BalanceStatus int
	RetryAfter    string
	Unauthorized  int
	RepeatCursor  bool
	BalanceCalls  int
	EventCalls    int
	LoginCalls    int
	BadScope      bool
}

// NewMock creates actual HTTP authentication/event/balance endpoints for fixture reads.
func NewMock(t *testing.T) *Mock {
	t.Helper()
	var event provider.Event
	Check(t, "parse synthetic event", json.Unmarshal(Fixture(t, "authorization-event.json"), &event))
	m := &Mock{Events: []provider.Event{event}, BalanceBody: string(Fixture(t, "usd-cash-balance.json"))}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Mu.Lock()
		defer m.Mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/authentication/login" {
			m.LoginCalls++
			if r.Method != "POST" || r.Header.Get("x-client-id") != "synthetic-client" || r.Header.Get("x-api-key") != "synthetic-key" {
				w.WriteHeader(403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "synthetic-token", "expires_at": time.Now().Add(30 * time.Minute).Format(time.RFC3339)})
			return
		}
		if r.Header.Get("x-on-behalf-of") != "fixture-connected-account" || r.Header.Get("x-api-version") != "2026-08-21" || r.Header.Get("Authorization") != "Bearer synthetic-token" {
			m.BadScope = true
			w.WriteHeader(403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/issuing/card_transaction_events") {
			m.EventCalls++
			if m.Unauthorized > 0 {
				m.Unauthorized--
				w.WriteHeader(401)
				return
			}
			if m.EventStatus != 0 {
				w.Header().Set("Retry-After", m.RetryAfter)
				w.WriteHeader(m.EventStatus)
				_, _ = w.Write([]byte(`{"error":"private-provider-body-marker"}`))
				return
			}
			if r.URL.Path != "/api/v1/issuing/card_transaction_events" {
				_ = json.NewEncoder(w).Encode(m.Events[0])
				return
			}
			if r.URL.Query().Get("card_transaction_id") != "00000000-0000-4000-8000-000000000002" || r.URL.Query().Get("page_size") != "100" {
				m.BadScope = true
				w.WriteHeader(400)
				return
			}
			if r.URL.Query().Get("page") == "" || m.RepeatCursor {
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "page_after": "fixture-page-2"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": m.Events, "page_after": nil})
			return
		}
		if r.URL.Path == "/api/v1/balances/current" && r.URL.Query().Get("account_type") == "cash" {
			m.BalanceCalls++
			if m.BalanceStatus != 0 {
				w.Header().Set("Retry-After", m.RetryAfter)
				w.WriteHeader(m.BalanceStatus)
				return
			}
			_, _ = w.Write([]byte(m.BalanceBody))
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(m.Server.Close)
	var err error
	m.Client, err = provider.NewClient(provider.ClientConfig{BaseURL: m.Server.URL, Version: "2026-08-21", ClientID: "synthetic-client", APIKey: "synthetic-key", HTTPClient: m.Server.Client()})
	Check(t, "configure loopback mock client", err)
	return m
}

// Counts returns completed HTTP call counts without racing the server goroutine.
func (m *Mock) Counts() (int, int, int, bool) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	return m.EventCalls, m.BalanceCalls, m.LoginCalls, m.BadScope
}
