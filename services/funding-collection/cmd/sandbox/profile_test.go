package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyntheticProfilesRequireOwnedAccountsAndVerifiedCategory(t *testing.T) {
	for _, validCategory := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid", false: "changed industry contract"}[validCategory], func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			runID := "fc-sbx-12345678"
			cfg := credentials{platform: "test-platform"}
			accounts := map[string]ownedAccount{}
			for _, role := range []string{"funding", "borrower"} {
				var a ownedAccount
				if json.Unmarshal(accountPayload(runID, role, true), &a) != nil {
					t.Fatal("fixture")
				}
				a.ID = "test-" + role
				a.Status = "CREATED"
				accounts[a.ID] = a
			}
			if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
				m.SandboxAgreementsAcceptedAt = "2026-11-15T00:00:00Z"
				for _, role := range []string{"funding", "borrower"} {
					m.Intents["account-"+role] = setupIntent{ResourceID: "test-" + role, State: "VERIFIED"}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			posts := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body []byte
				switch {
				case r.URL.Path == "/api/v1/reference/industry_categories":
					body = []byte(`[{"name":"Software development","type":"CODE","value":"ICCV3_0006XX"}]`)
					if !validCategory {
						body = []byte(`[]`)
					}
				case r.Method == "POST":
					posts++
					if !strings.HasSuffix(r.URL.Path, "/update") {
						t.Fatal("unexpected mutation")
					}
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatal(err)
					}
					var update struct {
						AccountDetails struct {
							BusinessDetails map[string]any   `json:"business_details"`
							People          []map[string]any `json:"business_person_details"`
						} `json:"account_details"`
					}
					if json.Unmarshal(raw, &update) != nil || update.AccountDetails.BusinessDetails["industry_category_code"] != "ICCV3_0006XX" || len(update.AccountDetails.People) != 1 {
						t.Fatal("profile not complete")
					}
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/"), "/update")
					body, _ = json.Marshal(accounts[id])
				default:
					body, _ = json.Marshal(accounts[strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/")])
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			err := completeProfiles(context.Background(), api, cfg, path, runID)
			if validCategory && err != nil || !validCategory && err == nil {
				t.Fatalf("unexpected profile result: %v", err)
			}
			if !validCategory && posts != 0 {
				t.Fatal("unverified category mutated accounts")
			}
			if validCategory {
				if err := completeProfiles(context.Background(), api, cfg, path, runID); err != nil {
					t.Fatal(err)
				}
				if posts != 2 {
					t.Fatal("profile update resent after successful checkpoint")
				}
			}
		})
	}
}
