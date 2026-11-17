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

func TestOwnedWebhooksPreserveSharedSubscriptionAndRecoverLostCreate(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost response"}[lost], func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			originFile := filepath.Join(dir, "origin.txt")
			runID := "fc-sbx-12345678"
			cfg := credentials{platform: "test-platform"}
			if err := os.WriteFile(originFile, []byte("https://test-callback.example"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
				for _, role := range []string{"funding", "borrower"} {
					m.Intents["account-"+role] = setupIntent{ResourceID: "test-" + role}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			webhooks := map[string]ownedWebhook{"shared": {ID: "shared", URL: "https://shared.example/original", Secret: "shared-secret"}}
			posts := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body []byte
				switch {
				case strings.HasPrefix(r.URL.Path, "/api/v1/accounts/"):
					role := strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/test-")
					var a ownedAccount
					if json.Unmarshal(accountPayload(runID, role, true), &a) != nil {
						t.Fatal("fixture")
					}
					a.ID = "test-" + role
					a.Status = "ACTIVE"
					body, _ = json.Marshal(a)
				case r.URL.Path == "/api/v1/webhooks":
					if r.Method != "GET" {
						t.Fatal("unexpected mutation")
					}
					items := []ownedWebhook{}
					for _, w := range webhooks {
						items = append(items, w)
					}
					body, _ = json.Marshal(struct {
						Items []ownedWebhook `json:"items"`
						After string         `json:"page_after"`
					}{items, ""})
				case r.URL.Path == "/api/v1/webhooks/create":
					posts++
					payload, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatal(err)
					}
					var w ownedWebhook
					if json.Unmarshal(payload, &w) != nil || w.Version != "2026-08-21" || !strings.HasPrefix(w.URL, "https://test-callback.example/webhooks/airwallex/") {
						t.Fatal("unexpected webhook contract")
					}
					w.ID = "owned-" + w.RequestID
					w.Secret = "private-" + w.RequestID
					webhooks[w.ID] = w
					if lost && posts == 1 {
						return nil, errors.New("private response lost")
					}
					body, _ = json.Marshal(w)
				case strings.HasPrefix(r.URL.Path, "/api/v1/webhooks/"):
					if r.Method != "GET" {
						t.Fatal("shared webhook mutation")
					}
					id := strings.TrimPrefix(r.URL.Path, "/api/v1/webhooks/")
					body, _ = json.Marshal(webhooks[id])
				default:
					t.Fatalf("unexpected provider path %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			first := setupWebhooks(context.Background(), api, cfg, path, runID, originFile)
			if lost && first == nil || !lost && first != nil {
				t.Fatalf("unexpected first result: %v", first)
			}
			if err := setupWebhooks(context.Background(), api, cfg, path, runID, originFile); err != nil {
				t.Fatal(err)
			}
			if posts != 2 || webhooks["shared"].URL != "https://shared.example/original" {
				t.Fatal("duplicate create or shared mutation")
			}
			configPath := filepath.Join(dir, "capture-subscriptions.json")
			info, err := os.Stat(configPath)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("secrets are not private")
			}
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			var config captureSubscriptions
			if json.Unmarshal(raw, &config) != nil || config.Owner != manifestOwner || config.RunID != runID || len(config.Accounts) != 2 || len(config.Subscriptions) != 2 || strings.Contains(string(raw), "shared-secret") {
				t.Fatal("wrong private capture configuration")
			}
		})
	}
}
