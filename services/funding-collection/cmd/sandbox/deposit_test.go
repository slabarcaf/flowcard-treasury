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

func TestOpeningDepositsRequireObservedSettlementAndNeverBlindlyReplay(t *testing.T) {
	for _, tc := range []struct {
		name          string
		lost, pending bool
	}{{name: "normal"}, {name: "lost create response", lost: true}, {name: "pending deposit", pending: true}} {
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
					m.Intents["global-account-"+role] = setupIntent{ResourceID: "global-" + role, State: "VERIFIED"}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			deposits := map[string]globalDeposit{}
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
						t.Fatal("deposit scope changed")
					}
					if r.Method == "POST" {
						if r.URL.Path != "/api/v1/simulation/deposit/create" {
							t.Fatal("non-sandbox deposit mutation")
						}
						posts++
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						var request struct {
							Amount   json.RawMessage `json:"amount"`
							GlobalID string          `json:"global_account_id"`
							Status   string          `json:"status"`
						}
						if json.Unmarshal(raw, &request) != nil || request.GlobalID != "global-"+role || request.Status != "SETTLED" {
							t.Fatal("wrong deposit recipient")
						}
						deposit := globalDeposit{ID: "deposit-" + role, Amount: request.Amount, Currency: "USD", Status: "SETTLED", Type: "CREDIT"}
						if tc.pending {
							deposit.Status = "PENDING"
						}
						deposits[scope] = deposit
						if tc.lost && posts == 1 {
							return nil, errors.New("private response lost")
						}
						body, _ = json.Marshal(deposit)
					} else {
						if r.URL.Path != "/api/v1/global_accounts/global-"+role+"/transactions" {
							t.Fatal("deposit retrieval scope mismatch")
						}
						body, _ = json.Marshal(struct {
							Items   []globalDeposit `json:"items"`
							HasMore bool            `json:"has_more"`
						}{[]globalDeposit{deposits[scope]}, false})
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			first := seedOpeningBalances(context.Background(), api, cfg, path, runID)
			if (tc.lost || tc.pending) && first == nil || !tc.lost && !tc.pending && first != nil {
				t.Fatalf("unexpected first result: %v", first)
			}
			again := seedOpeningBalances(context.Background(), api, cfg, path, runID)
			if tc.lost || tc.pending {
				if again == nil || posts != 1 {
					t.Fatal("unknown or unsettled deposit passed or was resent")
				}
			} else if again != nil || posts != 2 {
				t.Fatalf("duplicate or missing opening deposit: %v", again)
			}
		})
	}
}
