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

func TestNaturalDeclineRequiresCanonicalGETAndCannotAutoResend(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		lost, wrongReason, parentOnly bool
	}{{name: "insufficient funds"}, {name: "parent-only response", parentOnly: true}, {name: "response lost", lost: true}, {name: "other decline rejected", wrongReason: true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			runID := "fc-sbx-12345678"
			cfg := credentials{platform: "test-platform"}
			if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
				m.Intents["account-borrower"] = setupIntent{ResourceID: "test-borrower"}
				m.Intents["card-borrower"] = setupIntent{ResourceID: "test-card", State: "VERIFIED"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			posts := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body []byte
				switch r.URL.Path {
				case "/api/v1/accounts/test-borrower":
					var a ownedAccount
					if json.Unmarshal(accountPayload(runID, "borrower", true), &a) != nil {
						t.Fatal("fixture")
					}
					a.ID = "test-borrower"
					a.Status = "ACTIVE"
					body, _ = json.Marshal(a)
				case "/api/v1/balances/current":
					body = []byte(`[{"currency":"USD","account_type":"cash","available_amount":2000}]`)
				case "/api/v1/simulation/issuing/create":
					posts++
					if r.Header.Get("X-On-Behalf-Of") != "test-borrower" || r.Header.Get("X-Api-Version") != "2026-08-21" {
						t.Fatal("simulation scope/version wrong")
					}
					payload, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(payload), "transaction_failure_reason") || strings.Contains(string(payload), "card_number") {
						t.Fatal("forced reason or card-number dependency")
					}
					if tc.lost {
						return nil, errors.New("private response lost")
					}
					body = []byte(`{"id":"test-event","card_id":"test-card","card_transaction_id":"test-parent","process_result":"DECLINED","failure_reason":"INSUFFICIENT_FUNDS","card_number":"must-not-be-persisted"}`)
					if tc.parentOnly {
						body = []byte(strings.ReplaceAll(string(body), `"id":"test-event",`, ""))
					}
				case "/api/v1/issuing/card_transaction_events":
					body = []byte(`{"items":[{"id":"test-event","card_id":"test-card","card_transaction_id":"test-parent","type":"AUTHORIZATION","subtype":"AUTHORIZATION"}],"page_after":null}`)
				case "/api/v1/issuing/card_transaction_events/test-event":
					body = []byte(`{"id":"test-event","card_id":"test-card","card_transaction_id":"test-parent","type":"AUTHORIZATION","subtype":"AUTHORIZATION","transaction_category":"PURCHASE","process_result":"DECLINED","failure_reason":"INSUFFICIENT_FUNDS","billing_amount":9000,"billing_currency":"USD"}`)
					if tc.wrongReason {
						body = []byte(strings.ReplaceAll(string(body), "INSUFFICIENT_FUNDS", "TRANSACTION_BLOCKED"))
					}
				default:
					t.Fatalf("unexpected provider route %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			var output strings.Builder
			first := simulateDecline(context.Background(), api, cfg, path, runID, &output)
			if tc.lost && first == nil || !tc.lost && first != nil {
				t.Fatalf("unexpected simulation result: %v", first)
			}
			if err := simulateDecline(context.Background(), api, cfg, path, runID, &output); err == nil || posts != 1 {
				t.Fatal("simulation blindly resent")
			}
			err := captureEvent(context.Background(), api, cfg, path, runID, &output)
			if tc.lost || tc.wrongReason {
				if err == nil {
					t.Fatal("unverified canonical shortfall accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "contracts", "issuing-decline-create.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "must-not-be-persisted") {
				t.Fatal("unneeded card details persisted")
			}
		})
	}
}
