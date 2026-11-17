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

func TestReceivingSetupScopesOwnershipAndLostCreateRecovery(t *testing.T) {
	for _, tc := range []struct {
		name             string
		lost, badFeature bool
	}{{"normal", false, false}, {"lost response", true, false}, {"missing deposit capability", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			runID := "fc-sbx-12345678"
			cfg := credentials{platform: "test-platform"}
			if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
				for _, role := range []string{"funding", "borrower"} {
					m.Intents["account-"+role] = setupIntent{ResourceID: "test-" + role}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			accounts := map[string]globalAccount{}
			posts := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body []byte
				if strings.HasPrefix(r.URL.Path, "/api/v1/accounts/") {
					role := strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/test-")
					var a ownedAccount
					if json.Unmarshal(accountPayload(runID, role, true), &a) != nil {
						t.Fatal("fixture")
					}
					a.ID = "test-" + role
					a.Status = "ACTIVE"
					body, _ = json.Marshal(a)
				} else {
					scope := r.Header.Get("X-On-Behalf-Of")
					role := strings.TrimPrefix(scope, "test-")
					if role != "funding" && role != "borrower" {
						t.Fatal("unverified receiving-account scope")
					}
					switch {
					case r.Method == "POST":
						posts++
						if r.URL.Path != "/api/v1/global_accounts/create" {
							t.Fatal("unexpected mutation")
						}
						payload, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						var a globalAccount
						if json.Unmarshal(payload, &a) != nil {
							t.Fatal("invalid create")
						}
						a.ID = "global-" + role
						a.Status = "ACTIVE"
						features := `[{"currency":"USD","type":"DEPOSIT"}]`
						if tc.badFeature {
							features = `[{"currency":"EUR","type":"DEPOSIT"}]`
						}
						if json.Unmarshal([]byte(features), &a.Features) != nil {
							t.Fatal("fixture")
						}
						accounts[scope] = a
						if tc.lost && posts == 1 {
							return nil, errors.New("private transport failure")
						}
						body, _ = json.Marshal(a)
					case r.URL.Path == "/api/v1/global_accounts":
						items := []globalAccount{}
						if a, ok := accounts[scope]; ok {
							items = append(items, a)
						}
						body, _ = json.Marshal(struct {
							Items []globalAccount `json:"items"`
						}{items})
					default:
						a := accounts[scope]
						if r.URL.Path != "/api/v1/global_accounts/"+a.ID {
							t.Fatal("cross-account lookup")
						}
						body, _ = json.Marshal(a)
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			first := setupReceiving(context.Background(), api, cfg, path, runID)
			if (tc.lost || tc.badFeature) && first == nil || !tc.lost && !tc.badFeature && first != nil {
				t.Fatalf("unexpected first result: %v", first)
			}
			again := setupReceiving(context.Background(), api, cfg, path, runID)
			if tc.badFeature {
				if again == nil || posts != 1 {
					t.Fatal("missing receiving capability passed or duplicate created")
				}
				return
			}
			if again != nil {
				t.Fatal(again)
			}
			if posts != 2 {
				t.Fatalf("got %d creates, want 2", posts)
			}
		})
	}
}
