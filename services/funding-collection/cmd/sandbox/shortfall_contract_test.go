package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestShortfallContractRequiresSignedPairAndCanonicalRead(t *testing.T) {
	for _, test := range []struct {
		name        string
		forged      bool
		separate    bool
		wrongParent bool
		missingID   bool
	}{
		{name: "verified evidence"},
		{name: "tampered evidence refused", forged: true},
		{name: "signed forms on separate declines", separate: true},
		{name: "native response contains only parent reference", separate: true, missingID: true},
		{name: "separate sample canonical parent mismatch refused", separate: true, wrongParent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
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
				m.Intents["cardholder-borrower"] = setupIntent{ResourceID: "test-holder", State: "VERIFIED"}
				m.Intents["callback-decline"] = setupIntent{ParentTransactionID: "test-parent"}
				if test.separate {
					callback := setupIntent{ParentTransactionID: "test-parent"}
					if !test.missingID {
						callback.ResourceID = "test-callback-event"
					}
					m.Intents["callback-decline"] = callback
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			secret := "private-capture-secret"
			capture := captureSubscriptions{Owner: manifestOwner, Environment: "SANDBOX", RunID: runID, Accounts: []string{"test-funding", "test-borrower"}, Subscriptions: map[string]captureSubscription{"SHORTFALL": {"test-subscription", secret, "2026-08-21"}}}
			raw, err := json.Marshal(capture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "capture-subscriptions.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			evidenceDir := filepath.Join(dir, "webhook-evidence")
			if err := os.Mkdir(evidenceDir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"issuing.card_transaction.declined", "issuing.card_transaction_event.failed"} {
				id, parent := "test-parent", ""
				if strings.HasSuffix(name, "event.failed") {
					id, parent = "test-event", "test-parent"
					if test.separate {
						parent = "test-other-parent"
					}
				}
				body, _ := json.Marshal(map[string]any{"id": "delivery-" + name, "name": name, "account_id": "test-borrower", "version": "2026-08-21", "data": map[string]string{"id": id, "card_id": "test-card", "card_transaction_id": parent}})
				timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write([]byte(timestamp))
				mac.Write(body)
				signature := hex.EncodeToString(mac.Sum(nil))
				if test.forged {
					signature = strings.Repeat("0", 64)
				}
				record, _ := json.Marshal(map[string]any{"run_id": runID, "source": "SHORTFALL", "event_id": "delivery-" + name, "event_name": name, "x_timestamp": timestamp, "x_signature": signature, "raw_body_base64": body})
				if err := os.WriteFile(filepath.Join(evidenceDir, name+".json"), record, 0600); err != nil {
					t.Fatal(err)
				}
			}
			canonicalGets := 0
			canonicalLists := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Fatal("contract verification mutated provider")
				}
				var response []byte
				switch r.URL.Path {
				case "/api/v1/accounts/test-borrower":
					var a ownedAccount
					if json.Unmarshal(accountPayload(runID, "borrower", true), &a) != nil {
						t.Fatal("fixture")
					}
					a.ID = "test-borrower"
					a.Status = "ACTIVE"
					response, _ = json.Marshal(a)
				case "/api/v1/issuing/cardholders/test-holder":
					response = []byte(`{"cardholder_id":"test-holder","email":"fc-sbx-12345678-borrower-card@flowcard.example.com","type":"DELEGATE","status":"READY"}`)
				case "/api/v1/issuing/config":
					response = []byte(`{"spending_limit_settings":{"per_transaction_limits":[{"currency":"USD","maximum":20000}]}}`)
				case "/api/v1/issuing/cards/test-card":
					var card issuedCard
					if json.Unmarshal(cardPayload(runID, "test-holder", requestIDFor(runID, "card-borrower")), &card) != nil {
						t.Fatal("fixture")
					}
					card.ID = "test-card"
					card.Status = "ACTIVE"
					response, _ = json.Marshal(card)
				case "/api/v1/issuing/card_transaction_events/test-event":
					canonicalGets++
					response = []byte(`{"id":"test-event","card_id":"test-card","card_transaction_id":"test-parent","type":"AUTHORIZATION","subtype":"AUTHORIZATION","transaction_category":"PURCHASE","process_result":"DECLINED","failure_reason":"INSUFFICIENT_FUNDS","billing_amount":9000,"billing_currency":"USD"}`)
					if test.separate && !test.wrongParent {
						response = []byte(strings.Replace(string(response), "test-parent", "test-other-parent", 1))
					}
				case "/api/v1/issuing/card_transaction_events/test-callback-event":
					canonicalGets++
					response = []byte(`{"id":"test-callback-event","card_id":"test-card","card_transaction_id":"test-parent","type":"AUTHORIZATION","subtype":"AUTHORIZATION","transaction_category":"PURCHASE","process_result":"DECLINED","failure_reason":"INSUFFICIENT_FUNDS","billing_amount":9000,"billing_currency":"USD"}`)
				case "/api/v1/issuing/card_transaction_events":
					canonicalLists++
					if r.URL.Query().Get("card_transaction_id") != "test-parent" {
						t.Fatal("canonical list lost signed parent scope")
					}
					response = []byte(`{"items":[{"id":"test-callback-event","card_id":"test-card","card_transaction_id":"test-parent","type":"AUTHORIZATION","subtype":"AUTHORIZATION"}],"page_after":""}`)
				default:
					t.Fatalf("unexpected provider route %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(response)))}, nil
			})}}
			err = exportShortfallContract(context.Background(), api, cfg, path, runID)
			if test.forged {
				if err == nil || canonicalGets != 0 {
					t.Fatal("forged evidence reached canonical read or created contract")
				}
				return
			}
			if test.wrongParent {
				if err == nil || canonicalGets != 1 {
					t.Fatal("signed evidence was not bound to its own canonical parent")
				}
				return
			}
			wantGets := 1
			wantLists := 0
			if test.separate {
				wantGets = 2
			}
			if test.missingID {
				wantLists = 1
			}
			if err != nil || canonicalGets != wantGets || canonicalLists != wantLists {
				t.Fatalf("verified contract: err=%v canonical GETs=%d want%d lists=%d want%d", err, canonicalGets, wantGets, canonicalLists, wantLists)
			}
			target := filepath.Join(dir, "shortfall-contract.json")
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("contract not private")
			}
			result, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(result), secret) || !strings.Contains(string(result), "canonical_evidence_hash") {
				t.Fatal("secret leaked or provider evidence omitted")
			}
		})
	}
}
