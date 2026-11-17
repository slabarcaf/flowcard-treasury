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

func TestSandboxActivationUsesOnlyOwnedSubmittedAccounts(t *testing.T) {
	for _, test := range []struct {
		name     string
		lost     bool
		runID    string
		profiled bool
	}{
		{name: "normal", runID: "fc-sbx-12345678"},
		{name: "lost submit response", lost: true, runID: "fc-sbx-12345678"},
		{name: "maximum marker with completed profiles", runID: "fc-sbx-" + strings.Repeat("a", 32), profiled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			runID, lost := test.runID, test.lost
			cfg := credentials{platform: "test-platform"}
			accounts := map[string]ownedAccount{}
			for _, role := range []string{"funding", "borrower"} {
				var a ownedAccount
				if err := json.Unmarshal(accountPayload(runID, role, true), &a); err != nil {
					t.Fatal(err)
				}
				a.ID = "test-" + role
				a.Status = "CREATED"
				accounts[a.ID] = a
			}
			if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
				m.SandboxAgreementsAcceptedAt = "2026-11-15T00:00:00Z"
				for _, role := range []string{"funding", "borrower"} {
					m.Intents["account-"+role] = setupIntent{ResourceID: "test-" + role, State: "VERIFIED"}
					if test.profiled {
						m.Intents["profile-v1-"+role] = setupIntent{State: "VERIFIED"}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			posts := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var a ownedAccount
				if r.Method == "POST" {
					posts++
					if strings.HasSuffix(r.URL.Path, "/submit") {
						id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/"), "/submit")
						a = accounts[id]
						if a.Status != "CREATED" {
							t.Fatal("duplicate submit")
						}
						a.Status = "SUBMITTED"
						accounts[id] = a
						if lost && posts == 1 {
							return nil, errors.New("private response lost")
						}
					} else if strings.HasSuffix(r.URL.Path, "/update_status") {
						id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/simulation/accounts/"), "/update_status")
						a = accounts[id]
						if a.Status != "SUBMITTED" {
							t.Fatal("activation bypassed submit")
						}
						a.Status = "ACTIVE"
						accounts[id] = a
						raw, err := io.ReadAll(r.Body)
						if err != nil || string(raw) != `{"next_status":"ACTIVE","force":true}` {
							t.Fatal("wrong sandbox transition")
						}
					} else {
						t.Fatal("unexpected mutation")
					}
				} else {
					a = accounts[strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/")]
				}
				raw, _ := json.Marshal(a)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}}
			first := activateAccounts(context.Background(), api, cfg, path, runID)
			if lost && first == nil || !lost && first != nil {
				t.Fatalf("unexpected first result: %v", first)
			}
			for range 2 {
				if err := activateAccounts(context.Background(), api, cfg, path, runID); err != nil {
					t.Fatal(err)
				}
			}
			if posts != 4 {
				t.Fatalf("got %d mutations, want 4", posts)
			}
			for _, a := range accounts {
				if a.Status != "ACTIVE" {
					t.Fatal("account activation unverified")
				}
			}
		})
	}
}

func TestActivationPreservesLegacyUnknownIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	runID, name := "fc-sbx-12345678", "submit-funding"
	legacyKey := runID + "-" + name
	payload := []byte(`{}`)
	cfg := credentials{platform: "test-platform"}
	if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
		return reserveIntent(m, name, legacyKey, payload)
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected provider request")
	})}}
	err := accountTransition(context.Background(), api, cfg, path, runID, "funding", "submit", "test-funding", payload, "/api/v1/accounts/test-funding/submit")
	if err == nil || err.Error() != "sandbox_setup_reconciliation_required" || calls != 0 {
		t.Fatalf("legacy unknown transition: err=%v HTTP calls=%d, want reconciliation and no HTTP", err, calls)
	}
	if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
		if got := m.Intents[name]; got.RequestID != legacyKey || got.State != "UNKNOWN" {
			t.Fatal("legacy unknown identity or state changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
