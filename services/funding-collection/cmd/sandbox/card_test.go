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

func TestIssuingSetupScopeLimitsAndUnknownRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		lostHolder, lostCard, badProgram, lowLimit bool
	}{
		{name: "normal"}, {name: "lost cardholder response", lostHolder: true}, {name: "lost card response", lostCard: true}, {name: "credit line rejected", badProgram: true}, {name: "insufficient card limit", lowLimit: true},
	} {
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
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var holder *cardholder
			var card *issuedCard
			holderCreates, cardCreates := 0, 0
			api := &sandboxAPI{token: "private-token", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body []byte
				if r.URL.Path == "/api/v1/accounts/test-borrower" {
					var a ownedAccount
					if json.Unmarshal(accountPayload(runID, "borrower", true), &a) != nil {
						t.Fatal("fixture")
					}
					a.ID = "test-borrower"
					a.Status = "ACTIVE"
					body, _ = json.Marshal(a)
				} else {
					if r.Header.Get("X-On-Behalf-Of") != "test-borrower" || r.Header.Get("X-Api-Version") != "2026-08-21" {
						t.Fatal("issuing identity/version changed")
					}
					switch r.URL.Path {
					case "/api/v1/issuing/config":
						body = []byte(`{"spending_limit_settings":{"per_transaction_limits":[{"currency":"USD","maximum":20000}]}}`)
						if tc.lowLimit {
							body = []byte(`{"spending_limit_settings":{"per_transaction_limits":[{"currency":"USD","maximum":1000}]}}`)
						}
					case "/api/v1/issuing/cardholders":
						items := []cardholder{}
						if holder != nil {
							items = append(items, *holder)
						}
						body, _ = json.Marshal(struct {
							Items   []cardholder `json:"items"`
							HasMore bool         `json:"has_more"`
						}{items, false})
					case "/api/v1/issuing/cardholders/create":
						holderCreates++
						if holderCreates != 1 {
							t.Fatal("duplicate cardholder create")
						}
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						holder = &cardholder{}
						if json.Unmarshal(raw, holder) != nil || holder.Type != "DELEGATE" || holder.Email != accountEmail(runID, "borrower-card") {
							t.Fatal("wrong delegate identity")
						}
						holder.ID = "test-holder"
						holder.Status = "READY"
						if tc.lostHolder {
							return nil, errors.New("private lost response")
						}
						body, _ = json.Marshal(holder)
					case "/api/v1/issuing/cardholders/test-holder":
						body, _ = json.Marshal(holder)
					case "/api/v1/issuing/cards":
						items := []issuedCard{}
						if card != nil {
							items = append(items, *card)
						}
						body, _ = json.Marshal(struct {
							Items   []issuedCard `json:"items"`
							HasMore bool         `json:"has_more"`
						}{items, false})
					case "/api/v1/issuing/cards/create":
						cardCreates++
						if cardCreates != 1 {
							t.Fatal("duplicate card create")
						}
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						card = &issuedCard{}
						if json.Unmarshal(raw, card) != nil || holder == nil || card.HolderID != holder.ID {
							t.Fatal("wrong cardholder")
						}
						card.ID = "test-card"
						card.Status = "ACTIVE"
						if tc.badProgram {
							card.Program.Subtype = ""
							card.FundingSourceID = "test-credit-line"
						}
						if tc.lostCard {
							return nil, errors.New("private lost response")
						}
						body, _ = json.Marshal(card)
					case "/api/v1/issuing/cards/test-card":
						body, _ = json.Marshal(card)
					default:
						t.Fatalf("unexpected issuing route %s", r.URL.Path)
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			firstHolder := setupCardholder(context.Background(), api, cfg, path, runID)
			if tc.lostHolder && firstHolder == nil || !tc.lostHolder && firstHolder != nil {
				t.Fatalf("unexpected first holder result: %v", firstHolder)
			}
			if err := setupCardholder(context.Background(), api, cfg, path, runID); err != nil {
				t.Fatal(err)
			}
			firstCard := setupCard(context.Background(), api, cfg, path, runID)
			if (tc.lostCard || tc.badProgram || tc.lowLimit) && firstCard == nil || !tc.lostCard && !tc.badProgram && !tc.lowLimit && firstCard != nil {
				t.Fatalf("unexpected first card result: %v", firstCard)
			}
			again := setupCard(context.Background(), api, cfg, path, runID)
			if tc.badProgram || tc.lowLimit {
				if again == nil {
					t.Fatal("unverified card passed")
				}
			} else if again != nil {
				t.Fatal(again)
			}
			expectedCardCreates := 1
			if tc.lowLimit {
				expectedCardCreates = 0
			}
			if holderCreates != 1 || cardCreates != expectedCardCreates {
				t.Fatalf("wrong mutation counts: holder=%d card=%d", holderCreates, cardCreates)
			}
		})
	}
}
