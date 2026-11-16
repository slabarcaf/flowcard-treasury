package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

func executionFixture(now time.Time) *provider.SandboxExecutionContract {
	hash := strings.Repeat("a", 64)
	return &provider.SandboxExecutionContract{SchemaVersion: 1, Environment: "SANDBOX", RunID: "fc-sbx-12345678", FacilityID: "fixture-facility", SourceBindingID: "fixture-binding", CustomerID: "fixture-customer", FundingAccountID: "fixture-A", BorrowerAccountID: "fixture-B", CardID: "fixture-card", BorrowerGlobalAccountID: "fixture-global-B", Version: "2026-08-21", FundingPurpose: "investment_capital", TransferFeeField: "fee", SettlementTimeField: "updated_at", DepositFeeModel: "EXPLICIT_ZERO_ONLY", CutoverAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), FundingVerified: true, CollectionVerified: true, Quotes: []provider.SandboxQuote{{Kind: provider.OperationFund, AmountCents: 700000, FeeCents: 0, EvidenceHash: hash}, {Kind: provider.OperationCollectFee, AmountCents: 19000, FeeCents: 0, EvidenceHash: hash}, {Kind: provider.OperationCollectPrincipal, AmountCents: 700000, FeeCents: 0, EvidenceHash: hash}}, EvidenceHashes: map[string]string{"fund": hash, "principal": hash, "fee": hash, "deposit": hash}}
}

func executionAdapter(t *testing.T, now time.Time, enabled bool, profile *provider.SandboxExecutionContract, transport http.RoundTripper) *provider.AirwallexAdapter {
	t.Helper()
	hash := strings.Repeat("b", 64)
	payment := &provider.SandboxPaymentContract{SchemaVersion: 1, Environment: "SANDBOX", RunID: "fc-sbx-12345678", Source: "fixture-subscription", Version: "2026-08-21", CustomerID: "fixture-customer", AccountID: "fixture-B", CardID: "fixture-card", FundingModel: "USD_WALLET", AmountConvention: "positive_currency_units", CanonicalEvidenceHash: hash, WebhookEvidenceHashes: []string{hash, hash}, ExpiresAt: now.Add(time.Hour)}
	adapter, err := provider.NewAirwallexAdapter(provider.AdapterOptions{Mode: "SANDBOX", ProviderMode: "AIRWALLEX_SANDBOX", BaseURL: "https://api.sandbox.airwallex.com", FundingAccountID: "fixture-A", ClientID: "fixture-client", APIKey: "fixture-key", SandboxPaymentContract: payment, SandboxExecutionContract: profile, SandboxFundingEnabled: enabled, SandboxCollectionEnabled: enabled, Now: func() time.Time { return now }, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestSandboxExecutionRequiresExactBoundEvidenceAndExplicitSwitch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	calls := 0
	failTransport := sandboxReadTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") })
	profile := executionFixture(now)
	disabled := executionAdapter(t, now, false, profile, failTransport)
	op := provider.Operation{ID: "fixture-operation", Kind: provider.OperationFund, Mode: "SANDBOX", SourceAccountRef: "fixture-A", DestinationAccountRef: "fixture-B", AmountCents: 700000, Currency: "USD", RequestID: "fixture-request", BridgeID: "fixture-bridge"}
	if _, err := disabled.Create(context.Background(), op); !errors.Is(err, provider.ErrFundingExecutionDisabled) || calls != 0 {
		t.Fatal("switch default allowed provider work")
	}
	enabled := executionAdapter(t, now, true, profile, failTransport)
	if !enabled.ExecutionCapabilities().FundingEnabled || !enabled.ExecutionCapabilities().CollectionEnabled {
		t.Fatal("verified profile did not enable requested capabilities")
	}
	quote, err := enabled.QuoteFundingFee(context.Background(), "fixture-A", "fixture-B", 700000)
	if err != nil || quote.Cents != 0 || !strings.HasPrefix(quote.ContractID, "AIRWALLEX_SANDBOX_EXACT_") {
		t.Fatal("exact observed quote unavailable")
	}
	for _, amount := range []int64{700001, 100} {
		if _, err := enabled.QuoteFundingFee(context.Background(), "fixture-A", "fixture-B", amount); err == nil {
			t.Fatal("unobserved amount inherited fee assumption")
		}
	}
	op.DestinationAccountRef = "other-customer"
	if _, err := enabled.Create(context.Background(), op); !errors.Is(err, provider.ErrFundingExecutionDisabled) || calls != 0 {
		t.Fatal("other customer reached provider")
	}
	profile.ExpiresAt = now.Add(-time.Second)
	if enabled.ExecutionCapabilities().FundingEnabled {
		t.Fatal("expired profile remained enabled")
	}
}

func TestSandboxCreateAndGETUseObservedNumericFeeAndTerminalUpdatedTime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	profile := executionFixture(now)
	posts := 0
	conflictingSource := false
	transport := sandboxReadTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"id":"fixture-transfer","request_id":"fixture-request","destination":"fixture-B","amount":7000.0,"currency":"USD","fee":0,"status":"SETTLED","updated_at":"` + now.Format("2006-01-02T15:04:05-0700") + `"}`
		if r.URL.Path == "/api/v1/authentication/login" {
			body = `{"token":"fixture-token","expires_at":"` + now.Add(time.Hour).Format("2006-01-02T15:04:05-0700") + `"}`
		} else {
			if r.Header.Get("X-On-Behalf-Of") != "fixture-A" || r.Header.Get("X-Api-Version") != "2026-08-21" {
				t.Fatal("transfer scope/version changed")
			}
			if r.Method == "POST" {
				posts++
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var payload map[string]any
				if json.Unmarshal(raw, &payload) != nil || payload["reason"] != "investment_capital" || payload["amount"] != "7000.00" {
					t.Fatal("wrong sandbox funding payload")
				}
				body = strings.ReplaceAll(body, "SETTLED", "NEW")
			}
		}
		if conflictingSource && r.URL.Path != "/api/v1/authentication/login" {
			body = strings.Replace(body, `"destination":`, `"source_account_id":"other-account","destination":`, 1)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	adapter := executionAdapter(t, now, true, profile, transport)
	op := provider.Operation{ID: "fixture-operation", Kind: provider.OperationFund, Mode: "SANDBOX", SourceAccountRef: "fixture-A", DestinationAccountRef: "fixture-B", AmountCents: 700000, Currency: "USD", RequestID: "fixture-request", BridgeID: "fixture-bridge"}
	op.PayloadHash, _ = provider.PayloadHash(op)
	created, err := adapter.Create(context.Background(), op)
	if err != nil || created.Receipt.Status != "NEW" || created.Receipt.SettledAt != nil {
		t.Fatalf("create falsely settled or failed: %v", err)
	}
	result, err := adapter.Retrieve(context.Background(), op, "fixture-transfer")
	if err != nil || result.Receipt.Status != "SETTLED" || result.Receipt.ProviderFeeCents == nil || *result.Receipt.ProviderFeeCents != 0 || result.Receipt.ObservedSourceAccountRef == nil || *result.Receipt.ObservedSourceAccountRef != "fixture-A" || result.Receipt.SettledAt == nil || !result.Receipt.SettledAt.Equal(now) || posts != 1 {
		t.Fatalf("incorrect settlement normalization: %v", err)
	}
	conflictingSource = true
	if _, err := adapter.Retrieve(context.Background(), op, "fixture-transfer"); err == nil || posts != 1 {
		t.Fatal("conflicting explicit source was accepted or transfer recreated")
	}
}

func TestSandboxDepositReadRetriesIndexLagAndHoldsUnknownFees(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name      string
		status    int
		fee       int
		wantRetry bool
	}{{"settled explicit zero", 200, 0, false}, {"index not yet available", 400, 0, true}, {"fee convention unverified", 200, 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			transport := sandboxReadTransport(func(r *http.Request) (*http.Response, error) {
				status := tc.status
				body := `{"id":"fixture-deposit","global_account_id":"fixture-global-B","currency":"USD","amount":7190,"fee":{"currency":"USD","amount":` + map[bool]string{false: "0.0", true: "1.0"}[tc.fee != 0] + `},"type":"BANK_TRANSFER","status":"SETTLED","settled_at":"` + now.Format("2006-01-02T15:04:05-0700") + `"}`
				if r.URL.Path == "/api/v1/authentication/login" {
					status = 201
					body = `{"token":"fixture-token","expires_at":"` + now.Add(time.Hour).Format("2006-01-02T15:04:05-0700") + `"}`
				} else if r.Method != "GET" || r.URL.Path != "/api/v1/deposits/fixture-deposit" || r.Header.Get("X-On-Behalf-Of") != "fixture-B" {
					t.Fatal("deposit read scope changed")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			adapter := executionAdapter(t, now, true, executionFixture(now), transport)
			deposit, err := adapter.Deposit(context.Background(), "fixture-B", "fixture-deposit")
			if tc.status == 200 && tc.fee == 0 {
				if err != nil || deposit.NetCents != 719000 || deposit.CustomerID != "fixture-customer" {
					t.Fatalf("invalid deposit facts: %v", err)
				}
				return
			}
			var failure *provider.ProviderError
			if !errors.As(err, &failure) || (failure.Recovery == provider.RecoveryRetryRead) != tc.wantRetry {
				t.Fatal("unverified deposit was accepted or classified incorrectly")
			}
		})
	}
}

func TestSandboxWebhookWakesContainOnlyBoundReferences(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	transport := sandboxReadTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("webhook parsing made a provider call")
		return nil, errors.New("must not call")
	})
	adapter := executionAdapter(t, now, true, executionFixture(now), transport)
	for _, tc := range []struct {
		name, event, account, version, created, kind string
		data                                         map[string]string
		held                                         bool
	}{
		{"transfer", "connected_account_transfer.settled", "fixture-A", "2026-08-21", now.Format("2006-01-02T15:04:05-0700"), "FUND_REQUEST", map[string]string{"id": "fixture-transfer", "request_id": "fixture-request", "destination": "fixture-B", "currency": "USD"}, false},
		{"deposit", "deposit.settled", "fixture-B", "2026-08-21", now.Format(time.RFC3339), "DEPOSIT", map[string]string{"id": "fixture-deposit", "global_account_id": "fixture-global-B", "type": "BANK_TRANSFER", "currency": "USD", "customer_id": "untrusted-customer"}, false},
		{"reversal review", "deposit.reversed", "fixture-B", "2026-08-21", now.Format(time.RFC3339), "UNSUPPORTED", map[string]string{"id": "fixture-deposit", "global_account_id": "fixture-global-B", "type": "BANK_TRANSFER", "currency": "USD"}, false},
		{"other account", "deposit.settled", "other-account", "2026-08-21", now.Format(time.RFC3339), "UNSUPPORTED", map[string]string{}, true},
		{"unverified version", "deposit.settled", "fixture-B", "2025-02-14", now.Format(time.RFC3339), "UNSUPPORTED", map[string]string{}, true},
		{"pre-cutover", "deposit.settled", "fixture-B", "2026-08-21", now.Add(-time.Hour).Format(time.RFC3339), "UNSUPPORTED", map[string]string{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"id": "fixture-delivery", "name": tc.event, "account_id": tc.account, "version": tc.version, "created_at": tc.created, "data": tc.data})
			if err != nil {
				t.Fatal(err)
			}
			wake, err := adapter.NormalizeSandboxWebhook(raw)
			if err != nil || wake.TargetKind != tc.kind || (wake.State == "HELD") != tc.held {
				t.Fatalf("incorrect wake disposition: %v", err)
			}
			if tc.kind == "DEPOSIT" || tc.name == "reversal review" {
				if wake.CustomerID != "fixture-customer" || wake.CardID != "fixture-card" {
					t.Fatal("provider body supplied customer authority")
				}
			}
		})
	}
}

func TestCollectionFeeContractsCannotCrossModes(t *testing.T) {
	sandbox := provider.FundingFeeQuote{ContractID: "AIRWALLEX_SANDBOX_EXACT_" + strings.Repeat("a", 64)}
	mock := provider.FundingFeeQuote{ContractID: "MOCK_HTTP_ZERO_COLLECTION_FEE_V1"}
	for _, tc := range []struct {
		mode  string
		quote provider.FundingFeeQuote
		want  bool
	}{
		{"SANDBOX", sandbox, true}, {"DEMO", mock, true}, {"SANDBOX", mock, false}, {"DEMO", sandbox, false}, {"PRODUCTION", sandbox, false},
		{"SANDBOX", provider.FundingFeeQuote{ContractID: sandbox.ContractID, Cents: 1}, false},
		{"SANDBOX", provider.FundingFeeQuote{ContractID: "AIRWALLEX_SANDBOX_EXACT_unknown"}, false},
	} {
		if provider.ValidCollectionFeeQuote(tc.mode, tc.quote) != tc.want {
			t.Fatal("collection mode/fee boundary failed")
		}
	}
}
