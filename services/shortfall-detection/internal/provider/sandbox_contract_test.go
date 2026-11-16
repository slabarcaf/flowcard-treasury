package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/provider"
)

type sandboxTransport func(*http.Request) (*http.Response, error)

func (f sandboxTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func sandboxContractFixture(t *testing.T, body string, calls *int) *provider.SandboxContract {
	t.Helper()
	now := time.Now().UTC()
	client, err := provider.NewClient(provider.ClientConfig{BaseURL: "https://api.sandbox.airwallex.com", Version: "2026-08-21", ClientID: "fixture-client", APIKey: "fixture-key", HTTPClient: &http.Client{Transport: sandboxTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		raw := body
		if r.URL.Path == "/api/v1/authentication/login" {
			raw = `{"token":"fixture-token","expires_at":"` + now.Add(time.Hour).Format("2006-01-02T15:04:05-0700") + `"}`
		} else {
			if r.URL.Path != "/api/v1/issuing/cards/fixture-card" || r.Header.Get("X-On-Behalf-Of") != "fixture-account" || r.Header.Get("X-Api-Version") != "2026-08-21" {
				t.Fatal("funding verification scope/version changed")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	contract := provider.SandboxContract{SchemaVersion: 1, Environment: "SANDBOX", RunID: "fc-sbx-12345678", Source: "fixture-subscription", Version: "2026-08-21", CustomerID: "fixture-customer", AccountID: "fixture-account", CardID: "fixture-card", FundingModel: "USD_WALLET", AmountConvention: "positive_currency_units", CanonicalEvidenceHash: strings.Repeat("a", 64), WebhookEvidenceHashes: []string{strings.Repeat("b", 64), strings.Repeat("c", 64)}, ExpiresAt: now.Add(time.Hour)}
	raw, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := provider.LoadSandboxContract(path, "SANDBOX", "fixture-subscription", client)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestVerifiedSandboxReferencePaths(t *testing.T) {
	calls := 0
	contract := sandboxContractFixture(t, `{}`, &calls)
	event := `{"id":"webhook-event","name":"issuing.card_transaction_event.failed","account_id":"fixture-account","version":"2026-08-21","data":{"id":"canonical-event","card_id":"fixture-card","card_transaction_id":"parent-transaction"}}`
	parent := `{"id":"webhook-parent","name":"issuing.card_transaction.declined","account_id":"fixture-account","version":"2026-08-21","data":{"id":"parent-transaction","card_id":"fixture-card"}}`
	for _, tc := range []struct{ name, raw, webhook, eventName, eventID, parentID string }{
		{"event", event, "webhook-event", "issuing.card_transaction_event.failed", "canonical-event", "parent-transaction"},
		{"parent", parent, "webhook-parent", "issuing.card_transaction.declined", "", "parent-transaction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := contract.Extract(context.Background(), domain.Delivery{Environment: "SANDBOX", Source: "fixture-subscription", WebhookID: tc.webhook, EventName: tc.eventName, RawBody: []byte(tc.raw)})
			if err != nil || ref.EventID != tc.eventID || ref.TransactionID != tc.parentID || ref.EventID == tc.webhook {
				t.Fatalf("reference identity confused: %v", err)
			}
		})
	}
	for _, tc := range []struct{ name, raw, source string }{
		{"wrong version", strings.ReplaceAll(event, "2026-08-21", "2025-02-14"), "fixture-subscription"},
		{"wrong account", strings.ReplaceAll(event, "fixture-account", "other-account"), "fixture-subscription"},
		{"wrong card", strings.ReplaceAll(event, "fixture-card", "other-card"), "fixture-subscription"},
		{"duplicate reference", strings.ReplaceAll(event, `"id":"canonical-event"`, `"id":"first","id":"canonical-event"`), "fixture-subscription"},
		{"wrong subscription", event, "other-subscription"},
		{"trailing JSON", event + `{}`, "fixture-subscription"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := contract.Extract(context.Background(), domain.Delivery{Environment: "SANDBOX", Source: tc.source, WebhookID: "webhook-event", EventName: "issuing.card_transaction_event.failed", RawBody: []byte(tc.raw)}); err == nil {
				t.Fatal("unverified reference accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatal("reference extraction made provider calls")
	}
}

func TestSandboxFundingEvidenceRequiresCurrentWalletCardAndBinding(t *testing.T) {
	mapping := domain.Mapping{CustomerID: "fixture-customer", AccountID: "fixture-account", CardID: "fixture-card"}
	wallet := `{"card_id":"fixture-card","card_status":"ACTIVE","program":{"purpose":"COMMERCIAL","type":"CREDIT","sub_type":"GOOD_FUNDS_CREDIT"}}`
	for _, tc := range []struct {
		name, body string
		invalid    bool
	}{
		{"wallet", wallet, false}, {"credit line", strings.ReplaceAll(wallet, "GOOD_FUNDS_CREDIT", ""), true}, {"disabled", strings.ReplaceAll(wallet, "ACTIVE", "INACTIVE"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			contract := sandboxContractFixture(t, tc.body, &calls)
			evidence, err := contract.Evidence(context.Background(), mapping, "2026-08-21")
			if tc.invalid && err == nil || !tc.invalid && (err != nil || evidence.FundingModel != "USD_WALLET") {
				t.Fatalf("unexpected funding evidence: %v", err)
			}
			if calls != 2 {
				t.Fatal("current funding source was not retrieved")
			}
		})
	}
	calls := 0
	contract := sandboxContractFixture(t, wallet, &calls)
	wrong := mapping
	wrong.CustomerID = "other-customer"
	_, err := contract.Evidence(context.Background(), wrong, "2026-08-21")
	var failure *domain.Failure
	if !errors.As(err, &failure) || calls != 0 {
		t.Fatal("changed binding made a provider call or was accepted")
	}
}
