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

func TestProductProbeUsesOwnedScopeAndSeparatesPermissionFailures(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "issuing denied"}[denied], func(t *testing.T) {
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
			api := &sandboxAPI{token: "private-token", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Fatal("read probe mutated provider")
				}
				code := 200
				body := []byte(`{"items":[]}`)
				if strings.HasPrefix(r.URL.Path, "/api/v1/accounts/") {
					role := strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/test-")
					var account ownedAccount
					if json.Unmarshal(accountPayload(runID, role, true), &account) != nil {
						t.Fatal("fixture")
					}
					account.ID = "test-" + role
					account.Status = "ACTIVE"
					body, _ = json.Marshal(account)
					if r.Header.Get("X-On-Behalf-Of") != "" {
						t.Fatal("root identity read scoped incorrectly")
					}
				} else {
					scope := r.Header.Get("X-On-Behalf-Of")
					if scope != "test-funding" && scope != "test-borrower" {
						t.Fatal("unverified account scope")
					}
					if strings.HasPrefix(r.URL.Path, "/api/v1/issuing/") {
						if scope != "test-borrower" || r.Header.Get("X-Api-Version") != "2026-08-21" {
							t.Fatal("issuing scope/version wrong")
						}
						if denied && r.URL.Path == "/api/v1/issuing/config" {
							code = 403
							body = []byte(`{"message":"private-token"}`)
						}
					}
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			var output strings.Builder
			err := probeProducts(context.Background(), api, cfg, path, runID, &output)
			if denied && err == nil || !denied && err != nil {
				t.Fatalf("unexpected product access result: %v", err)
			}
			if strings.Contains(output.String(), "private-token") || strings.Contains(output.String(), "test-borrower") {
				t.Fatal("private scope leaked")
			}
			if denied && !strings.Contains(output.String(), "PERMISSION_REFUSED") {
				t.Fatal("permission denial hidden")
			}
		})
	}
}
