package provider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

type sandboxReadTransport func(*http.Request) (*http.Response, error)

func (f sandboxReadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSandboxPaymentRefreshAndFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		approved, ambiguous, creditLine, outage, covered bool
	}{
		{name: "fresh current need"}, {name: "subsequent approval", approved: true}, {name: "ambiguous authorization", ambiguous: true}, {name: "credit source changed", creditLine: true}, {name: "provider outage", outage: true}, {name: "wallet now covers payment", covered: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			calls := 0
			balanceCalls := 0
			contract := &provider.SandboxPaymentContract{SchemaVersion: 1, Environment: "SANDBOX", RunID: "fc-sbx-12345678", Source: "fixture-subscription", Version: "2026-08-21", CustomerID: "fixture-customer", AccountID: "fixture-borrower", CardID: "fixture-card", FundingModel: "USD_WALLET", AmountConvention: "positive_currency_units", CanonicalEvidenceHash: strings.Repeat("a", 64), WebhookEvidenceHashes: []string{strings.Repeat("b", 64), strings.Repeat("c", 64)}, ExpiresAt: now.Add(time.Hour)}
			event := `{"id":"fixture-event","card_id":"fixture-card","card_transaction_id":"fixture-parent","type":"AUTHORIZATION","subtype":"AUTHORIZATION","transaction_category":"PURCHASE","process_result":"DECLINED","failure_reason":"INSUFFICIENT_FUNDS","transaction_amount":9000,"transaction_currency":"USD","billing_amount":9000,"billing_currency":"USD"}`
			adapter, err := provider.NewAirwallexAdapter(provider.AdapterOptions{Mode: "SANDBOX", ProviderMode: "AIRWALLEX_SANDBOX", BaseURL: "https://api.sandbox.airwallex.com", FundingAccountID: "fixture-source", ClientID: "fixture-client", APIKey: "fixture-key", LoginAs: "fixture-platform", SandboxPaymentContract: contract, Now: func() time.Time { return now }, HTTPClient: &http.Client{Transport: sandboxReadTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "api.sandbox.airwallex.com" {
					t.Fatal("credential destination changed")
				}
				if tc.outage {
					return nil, context.DeadlineExceeded
				}
				body := event
				code := 200
				if r.URL.Path == "/api/v1/authentication/login" {
					if r.Header.Get("X-Login-As") != "fixture-platform" {
						t.Fatal("platform login scope missing")
					}
					code = 201
					body = `{"token":"fixture-token","expires_at":"` + now.Add(time.Hour).Format("2006-01-02T15:04:05-0700") + `"}`
				} else {
					if r.Header.Get("X-On-Behalf-Of") != "fixture-borrower" || r.Header.Get("X-Api-Key") != "" {
						t.Fatal("provider account scope wrong")
					}
					if strings.HasPrefix(r.URL.Path, "/api/v1/issuing/") && r.Header.Get("X-Api-Version") != "2026-08-21" {
						t.Fatal("unversioned issuing request")
					}
					switch r.URL.Path {
					case "/api/v1/issuing/card_transaction_events":
						if r.URL.Query().Get("card_transaction_id") != "fixture-parent" {
							t.Fatal("parent filter lost")
						}
						if r.URL.Query().Get("page") == "" {
							body = `{"items":[],"page_after":"next-page"}`
						} else {
							item := event
							if tc.approved {
								item = strings.ReplaceAll(event, "DECLINED", "APPROVED")
							}
							items := item
							if tc.ambiguous {
								items += "," + strings.ReplaceAll(item, "fixture-event", "another-event")
							}
							body = `{"items":[` + items + `],"page_after":null}`
						}
					case "/api/v1/issuing/card_transaction_events/fixture-event":
					case "/api/v1/issuing/cards/fixture-card":
						body = `{"card_id":"fixture-card","card_status":"ACTIVE","program":{"purpose":"COMMERCIAL","type":"CREDIT","sub_type":"GOOD_FUNDS_CREDIT"}}`
						if tc.creditLine {
							body = strings.ReplaceAll(body, "GOOD_FUNDS_CREDIT", "")
						}
					case "/api/v1/balances/current":
						balanceCalls++
						body = `[{"account_type":"cash","currency":"USD","available_amount":2000}]`
						if tc.covered {
							body = strings.ReplaceAll(body, "2000", "9000")
						}
					default:
						t.Fatalf("unexpected provider path %s", r.URL.Path)
					}
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			payment, err := adapter.Payment(context.Background(), "fixture-borrower", "fixture-parent")
			if tc.approved || tc.ambiguous || tc.creditLine || tc.outage {
				if err == nil || balanceCalls != 0 {
					t.Fatal("unverified payment produced funding facts")
				}
				return
			}
			shortfall := int64(700000)
			if tc.covered {
				shortfall = 0
			}
			if err != nil || payment.ShortfallCents != shortfall || payment.AttemptedAmountCents != 900000 || payment.AuthorizationEventID != "fixture-event" || payment.CustomerID != "fixture-customer" || payment.Mode != "SANDBOX" || balanceCalls != 1 {
				t.Fatalf("incorrect current facts: %v", err)
			}
			prior := calls
			if _, err := adapter.Payment(context.Background(), "other-account", "fixture-parent"); err == nil || calls != prior {
				t.Fatal("other account reached provider")
			}
		})
	}
}

func TestSandboxPaymentUnavailableWithoutVerifiedContract(t *testing.T) {
	calls := 0
	adapter, err := provider.NewAirwallexAdapter(provider.AdapterOptions{Mode: "SANDBOX", ProviderMode: "AIRWALLEX_SANDBOX", BaseURL: "https://api.sandbox.airwallex.com", FundingAccountID: "fixture-source", ClientID: "fixture-client", APIKey: "fixture-key", HTTPClient: &http.Client{Transport: sandboxReadTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") })}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Payment(context.Background(), "fixture-borrower", "fixture-parent")
	var failure *provider.ProviderError
	if !errors.As(err, &failure) || calls != 0 {
		t.Fatal("missing contract allowed provider work")
	}
}
