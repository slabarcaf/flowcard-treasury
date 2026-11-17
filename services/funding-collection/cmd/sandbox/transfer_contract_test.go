package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestExactTransferProbesRequireScopedSettlementAndBalanceProof(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		lost, extraDebit, pending bool
	}{{name: "observed zero fees"}, {name: "lost create response", lost: true}, {name: "unexpected debit", extraDebit: true}, {name: "pending receipt", pending: true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			runID := "fc-sbx-12345678"
			cfg := credentials{platform: "fixture-platform"}
			if err := withManifest(path, runID, cfg.platform, true, func(m *sandboxManifest) error {
				for _, role := range []string{"funding", "borrower"} {
					m.Intents["account-"+role] = setupIntent{ResourceID: "fixture-" + role}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			balances := map[string]int64{"fixture-funding": 10000000, "fixture-borrower": 219000}
			objects := map[string]contractTransfer{}
			posts := 0
			api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body []byte
				if strings.HasPrefix(r.URL.Path, "/api/v1/accounts/") {
					role := strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/fixture-")
					var a ownedAccount
					if json.Unmarshal(accountPayload(runID, role, true), &a) != nil {
						t.Fatal("fixture")
					}
					a.ID = "fixture-" + role
					a.Status = "ACTIVE"
					body, _ = json.Marshal(a)
				} else {
					scope := r.Header.Get("X-On-Behalf-Of")
					if scope != "fixture-funding" && scope != "fixture-borrower" {
						t.Fatal("unverified transfer source")
					}
					if r.URL.Path == "/api/v1/balances/current" {
						amount := balances[scope]
						body = []byte(`[{"account_type":"cash","currency":"USD","available_amount":` + strconv.FormatInt(amount/100, 10) + "." + twoDigitCents(amount%100) + `}]`)
					} else if r.Method == "POST" {
						posts++
						if r.URL.Path != "/api/v1/connected_account_transfers/create" {
							t.Fatal("unexpected provider mutation")
						}
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						var transfer contractTransfer
						if json.Unmarshal(raw, &transfer) != nil {
							t.Fatal("invalid payload")
						}
						amount, ok := majorAmount(transfer.Amount)
						if !ok {
							t.Fatal("non-exact amount")
						}
						amount.Mul(amount, big.NewRat(100, 1))
						cents := amount.Num().Int64()
						balances[scope] -= cents
						balances[transfer.Destination] += cents
						if tc.extraDebit {
							balances[scope]--
						}
						transfer.ID = "fixture-transfer-" + strconv.Itoa(posts)
						transfer.Status = "SETTLED"
						if tc.pending {
							transfer.Status = "PENDING"
						}
						objects[transfer.ID] = transfer
						if tc.lost && posts == 1 {
							return nil, errors.New("private receipt lost")
						}
						body, _ = json.Marshal(transfer)
					} else {
						id := strings.TrimPrefix(r.URL.Path, "/api/v1/connected_account_transfers/")
						body, _ = json.Marshal(objects[id])
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			first := probeTransfers(context.Background(), api, cfg, path, runID)
			failed := tc.lost || tc.extraDebit || tc.pending
			if failed && first == nil || !failed && first != nil {
				t.Fatalf("unexpected first result: %v", first)
			}
			again := probeTransfers(context.Background(), api, cfg, path, runID)
			if failed {
				if again == nil || posts != 1 {
					t.Fatal("unverified transfer passed or was resent")
				}
				return
			}
			if again != nil || posts != 3 || balances["fixture-borrower"] != 200000 || balances["fixture-funding"] != 10019000 {
				t.Fatalf("wrong probe outcome: %v, calls=%d", again, posts)
			}
		})
	}
}
