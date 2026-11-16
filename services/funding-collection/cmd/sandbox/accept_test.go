package main

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
)

func TestOwnedWebhookCleanupReconcilesLostResponseAndPreservesSharedSubscription(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lost, shared bool
	}{{name: "normal"}, {name: "lost delete response", lost: true}, {name: "shared subscription", shared: true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if os.Chmod(dir, 0700) != nil {
				t.Fatal("private fixture directory")
			}
			manifest := filepath.Join(dir, "manifest.json")
			origin := filepath.Join(dir, "callback-origin.txt")
			if os.WriteFile(origin, []byte("https://fixture.example"), 0600) != nil {
				t.Fatal("origin fixture")
			}
			runID := "fc-sbx-12345678"
			cfg := credentials{platform: "fixture-platform", webhookID: "shared-hook"}
			if tc.shared {
				cfg.webhookID = "fixture-shortfall"
			}
			if err := withManifest(manifest, runID, cfg.platform, true, func(m *sandboxManifest) error {
				for _, source := range []string{"shortfall", "servicing"} {
					name := "webhook-" + source
					m.Intents[name] = setupIntent{ResourceID: "fixture-" + source, RequestID: requestIDFor(runID, name), State: "VERIFIED"}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			deleted := map[string]bool{}
			posts := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				source := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/delete"), "/api/v1/webhooks/fixture-")
				if source != "shortfall" && source != "servicing" {
					t.Fatal("unexpected cleanup target")
				}
				code := 200
				body := []byte(`{}`)
				if r.Method == "POST" {
					posts++
					deleted[source] = true
					if tc.lost {
						return nil, errors.New("private network error")
					}
				} else if deleted[source] {
					code = 404
				} else {
					route := "/webhooks/airwallex/shortfalls"
					if source == "servicing" {
						route = "/webhooks/airwallex/servicing"
					}
					body, _ = json.Marshal(ownedWebhook{ID: "fixture-" + source, RequestID: requestIDFor(runID, "webhook-"+source), URL: "https://fixture.example" + route, Version: "2026-08-21"})
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			first := cleanupWebhooks(context.Background(), api, cfg, manifest, runID, origin)
			if tc.shared {
				if first == nil || posts != 0 {
					t.Fatal("shared webhook deletion allowed")
				}
				return
			}
			if first != nil {
				t.Fatal(first)
			}
			if err := cleanupWebhooks(context.Background(), api, cfg, manifest, runID, origin); err != nil || posts != 2 {
				t.Fatal("cleanup repeated deletes or lost-response reconciliation failed")
			}
		})
	}
}

func TestOwnedDepositDoesNotRepeatUnknownSimulation(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture directory")
	}
	path := filepath.Join(dir, "manifest.json")
	runID := "fc-sbx-12345678"
	cfg := credentials{platform: "fixture-platform"}
	if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
		m.Intents["account-borrower"] = setupIntent{ResourceID: "fixture-borrower"}
		m.Intents["global-account-borrower"] = setupIntent{ResourceID: "fixture-global", State: "VERIFIED"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	posts := 0
	api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			if r.URL.Path != "/api/v1/simulation/deposit/create" || r.Header.Get("X-On-Behalf-Of") != "fixture-borrower" {
				t.Fatal("deposit scope changed")
			}
			posts++
			return nil, errors.New("private lost response")
		}
		var account ownedAccount
		if json.Unmarshal(accountPayload(runID, "borrower", true), &account) != nil {
			t.Fatal("fixture")
		}
		account.ID = "fixture-borrower"
		account.Status = "ACTIVE"
		raw, _ := json.Marshal(account)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}}
	for range 2 {
		if err := simulateOwnedDeposit(context.Background(), api, cfg, path, runID, "journey-income", 7190); err == nil {
			t.Fatal("unknown deposit passed")
		}
	}
	if posts != 1 {
		t.Fatal("unknown deposit simulation repeated")
	}
}
