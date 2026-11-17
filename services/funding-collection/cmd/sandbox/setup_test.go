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

func TestSetupOwnsAccountPairAndResumesWithoutDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lost, accept bool
	}{{"normal", false, false}, {"lost create response", true, false}, {"explicit sandbox agreements", false, true}} {
		name, lost, accept := tc.name, tc.lost, tc.accept
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			runID := "fc-sbx-12345678"
			cfg := credentials{platform: "test-platform"}
			accounts := map[string]ownedAccount{}
			posts := 0
			api := &sandboxAPI{token: "private-token", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.sandbox.airwallex.com" || r.Header.Get("Authorization") != "Bearer private-token" {
					t.Fatal("provider authority changed")
				}
				var body []byte
				if r.Method == "POST" {
					if r.URL.Path != "/api/v1/accounts/create" {
						t.Fatal("unexpected mutation")
					}
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(raw), "customer_agreements") != accept {
						t.Fatal("fabricated agreement")
					}
					var account ownedAccount
					if json.Unmarshal(raw, &account) != nil {
						t.Fatal("invalid request")
					}
					posts++
					account.ID = "test-account-" + string(rune('a'+posts-1))
					account.Status = "CREATED"
					accounts[account.ID] = account
					if lost && posts == 1 {
						return nil, errors.New("private transport failure")
					}
					body, _ = json.Marshal(account)
				} else if r.URL.Path == "/api/v1/accounts" {
					items := []ownedAccount{}
					for _, a := range accounts {
						items = append(items, a)
					}
					body, _ = json.Marshal(struct {
						Items   []ownedAccount `json:"items"`
						HasMore bool           `json:"has_more"`
					}{items, false})
				} else if strings.HasPrefix(r.URL.Path, "/api/v1/accounts/") {
					body, _ = json.Marshal(accounts[strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/")])
				} else {
					t.Fatal("unexpected read")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
			})}}
			first := setupAccounts(context.Background(), api, cfg, path, runID, accept)
			if lost && first == nil {
				t.Fatal("lost response appeared successful")
			}
			if !lost && first != nil {
				t.Fatal(first)
			}
			if err := setupAccounts(context.Background(), api, cfg, path, runID, accept); err != nil {
				t.Fatal(err)
			}
			if err := setupAccounts(context.Background(), api, cfg, path, runID, accept); err != nil {
				t.Fatal(err)
			}
			if posts != 2 {
				t.Fatalf("created %d accounts, want exactly 2", posts)
			}
			if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error {
				if (m.SandboxAgreementsAcceptedAt != "") != accept {
					t.Fatal("agreement decision not recorded")
				}
				if len(m.Intents) != 2 || m.Intents["account-funding"].ResourceID == m.Intents["account-borrower"].ResourceID {
					t.Fatal("pair not distinct")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnknownAccountCreateNeverBlindlyResends(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	runID := "fc-sbx-12345678"
	posts := 0
	api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			posts++
			return nil, errors.New("private error")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[],"has_more":false}`))}, nil
	})}}
	for range 2 {
		if err := setupAccounts(context.Background(), api, credentials{platform: "test-platform"}, path, runID, false); err == nil {
			t.Fatal("unknown create accepted")
		}
	}
	if posts != 1 {
		t.Fatal("unknown create was resent")
	}
}

func TestAccountLookupRejectsAmbiguityAndIncompletePagination(t *testing.T) {
	for _, body := range []string{
		`{"items":[]}`,
		`{"items":[{"id":"a","account_details":{"business_details":{"business_name":"FlowCard Sandbox fc-sbx-12345678 funding"}},"primary_contact":{"email":"fc-sbx-12345678-funding@flowcard.example.com"}},{"id":"b","account_details":{"business_details":{"business_name":"FlowCard Sandbox fc-sbx-12345678 funding"}},"primary_contact":{"email":"fc-sbx-12345678-funding@flowcard.example.com"}}],"has_more":false}`,
	} {
		api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		if _, _, err := api.findAccount(context.Background(), "fc-sbx-12345678", "funding"); err == nil {
			t.Fatal("unsafe lookup accepted")
		}
	}
}

func TestProviderRefusalsUseFixedCodes(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"message":"customer_agreements.agreed_to_data_usage is required, private-token","source":"customer_agreements"}`, "CUSTOMER_AGREEMENT_REQUIRED"},
		{`{"message":"permission required private-token"}`, "PLATFORM_PERMISSION_REQUIRED"},
		{`{"message":"unexpected private-token"}`, "REFUSED"},
		{`not JSON private-token`, "REFUSED"},
	} {
		if got := refusalCode([]byte(tc.body)); got != tc.want {
			t.Fatalf("got %s, want %s", got, tc.want)
		}
	}
}
