package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/testutil"
)

func TestProviderDetector(t *testing.T) {
	db := testutil.NewDatabase(t, "provider")
	for _, tc := range []struct {
		name, token string
		want        int64
		invalid     bool
	}{
		{name: "decimal", token: "9000.00", want: 900000},
		{name: "exponent", token: "9e3", want: 900000},
		{name: "cent", token: "1e-2", want: 1},
		{name: "maximum", token: "92233720368547758.07", want: math.MaxInt64},
		{name: "overflow", token: "92233720368547758.08", invalid: true},
		{name: "extra_precision", token: "1.001", invalid: true},
		{name: "extra_zero_precision", token: "1.000", invalid: true},
		{name: "string", token: `"1.00"`, invalid: true},
		{name: "missing", token: `null`, invalid: true},
		{name: "huge_exponent", token: `1e99999999`, invalid: true},
	} {
		t.Run("cents_"+tc.name, func(t *testing.T) {
			got, err := domain.USDCents(json.RawMessage(tc.token))
			if (err != nil) != tc.invalid || (!tc.invalid && got != tc.want) {
				t.Fatalf("USDCents(%s)=%d invalid=%t, want %d invalid=%t", tc.token, got, err != nil, tc.want, tc.invalid)
			}
		})
	}
	for _, tc := range []struct {
		name      string
		change    func(*testutil.Mock)
		outcome   domain.Outcome
		reason    string
		shortfall int64
		balance   bool
	}{
		{name: "9000_2000", outcome: domain.ShortfallDetected, shortfall: 700000, balance: true},
		{name: "equal", change: func(m *testutil.Mock) {
			m.BalanceBody = `[{"account_type":"cash","currency":"USD","available_amount":9000}]`
		}, outcome: domain.NoCurrentShortfall, balance: true},
		{name: "excess", change: func(m *testutil.Mock) {
			m.BalanceBody = `[{"account_type":"cash","currency":"USD","available_amount":10000}]`
		}, outcome: domain.NoCurrentShortfall, balance: true},
		{name: "explicit_zero", change: func(m *testutil.Mock) {
			m.BalanceBody = `[{"account_type":"cash","currency":"USD","available_amount":0}]`
		}, outcome: domain.ShortfallDetected, shortfall: 900000, balance: true},
		{name: "missing_balance", change: func(m *testutil.Mock) { m.BalanceBody = `[]` }, outcome: domain.Unresolved, reason: "USD_CASH_BALANCE_UNAVAILABLE_OR_AMBIGUOUS", balance: true},
		{name: "duplicate_balance", change: func(m *testutil.Mock) {
			m.BalanceBody = `[{"account_type":"cash","currency":"USD","available_amount":0},{"account_type":"cash","currency":"USD","available_amount":1}]`
		}, outcome: domain.Unresolved, reason: "USD_CASH_BALANCE_UNAVAILABLE_OR_AMBIGUOUS", balance: true},
		{name: "negative_balance", change: func(m *testutil.Mock) {
			m.BalanceBody = `[{"account_type":"cash","currency":"USD","available_amount":-1}]`
		}, outcome: domain.Unresolved, reason: "BALANCE_AMOUNT_INVALID", balance: true},
		{name: "balance_precision", change: func(m *testutil.Mock) {
			m.BalanceBody = `[{"account_type":"cash","currency":"USD","available_amount":1.001}]`
		}, outcome: domain.Unresolved, reason: "BALANCE_AMOUNT_INVALID", balance: true},
		{name: "balance_missing_type", change: func(m *testutil.Mock) { m.BalanceBody = `[{"currency":"USD","available_amount":0}]` }, outcome: domain.Unresolved, reason: "BALANCE_ACCOUNT_TYPE_MISSING", balance: true},
		{name: "other_decline", change: func(m *testutil.Mock) { m.Events[0].FailureReason = "CARD_NOT_ACTIVE" }, outcome: domain.Ignored, reason: "UNSUPPORTED_DECLINE_CLASSIFICATION"},
		{name: "incremental", change: func(m *testutil.Mock) { m.Events[0].Subtype = "INCREMENTAL_AUTHORIZATION" }, outcome: domain.Ignored, reason: "UNSUPPORTED_DECLINE_CLASSIFICATION"},
		{name: "non_usd", change: func(m *testutil.Mock) { m.Events[0].BillingCurrency = "EUR" }, outcome: domain.Ignored, reason: "UNSUPPORTED_CURRENCY"},
		{name: "missing_result", change: func(m *testutil.Mock) { m.Events[0].ProcessResult = "" }, outcome: domain.Unresolved, reason: "AUTHORIZATION_FACTS_MISSING"},
		{name: "invalid_sign", change: func(m *testutil.Mock) { m.Events[0].TransactionAmount = json.RawMessage(`-9000`) }, outcome: domain.Unresolved, reason: "ATTEMPT_AMOUNT_INVALID"},
		{name: "billing_mismatch", change: func(m *testutil.Mock) { m.Events[0].BillingAmount = json.RawMessage(`9001`) }, outcome: domain.Unresolved, reason: "BILLING_AMOUNT_INVALID"},
		{name: "ambiguous_attempts", change: func(m *testutil.Mock) {
			other := m.Events[0]
			other.ID = "00000000-0000-4000-8000-000000000006"
			m.Events = append(m.Events, other)
		}, outcome: domain.Unresolved, reason: "AUTHORIZATION_AMBIGUOUS"},
		{name: "wrong_card", change: func(m *testutil.Mock) { m.Events[0].CardID = "00000000-0000-4000-8000-000000000009" }, outcome: domain.Unresolved, reason: "EVENT_OWNERSHIP_MISMATCH"},
		{name: "repeated_cursor", change: func(m *testutil.Mock) { m.RepeatCursor = true }, outcome: domain.Unresolved, reason: "PROVIDER_PAGINATION_INVALID"},
		{name: "later_success", change: func(m *testutil.Mock) {
			other := m.Events[0]
			other.ID = "00000000-0000-4000-8000-000000000006"
			other.Type = "CLEARING"
			other.Subtype = "CLEARING"
			other.ProcessResult = "APPROVED"
			other.CreatedAt = "2026-11-13T12:01:00Z"
			other.TransactedAt = other.CreatedAt
			m.Events = append(m.Events, other)
		}, outcome: domain.Ignored, reason: "PAYMENT_NO_LONGER_ACTIONABLE"},
		{name: "unordered_success", change: func(m *testutil.Mock) {
			other := m.Events[0]
			other.ID = "00000000-0000-4000-8000-000000000006"
			other.Type = "CLEARING"
			other.Subtype = "CLEARING"
			other.ProcessResult = "APPROVED"
			m.Events = append(m.Events, other)
		}, outcome: domain.Unresolved, reason: "OUTCOME_UNRESOLVED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMock(t)
			if tc.change != nil {
				mock.Mu.Lock()
				tc.change(mock)
				mock.Mu.Unlock()
			}
			attempt := mock.Client.BeginAttempt()
			facts, err := testutil.Resolver(t, db.Repo).Resolve(context.Background(), domain.Delivery{}, attempt)
			var observation domain.Observation
			if err == nil {
				var eligible bool
				observation, eligible = domain.Prepare(facts)
				if eligible {
					var balance provider.BalanceObservation
					balance, err = attempt.Balance(context.Background(), facts.Mapping.AccountID)
					if err == nil {
						observation = domain.Observe(facts, balance.AvailableCents, balance.RequestedAt, balance.ObservedAt)
					}
				}
			}
			if err != nil {
				var failure *domain.Failure
				if !errors.As(err, &failure) {
					t.Fatal("unexpected unsanitized processing error")
				}
				observation = domain.Observation{Outcome: domain.Unresolved, Reason: failure.Reason}
			}
			if observation.Outcome != tc.outcome || observation.Reason != tc.reason {
				t.Fatalf("outcome/reason=%s/%s, want %s/%s", observation.Outcome, observation.Reason, tc.outcome, tc.reason)
			}
			if tc.outcome == domain.ShortfallDetected || tc.outcome == domain.NoCurrentShortfall {
				if observation.ShortfallCents == nil || *observation.ShortfallCents != tc.shortfall || observation.BalanceObservedAt == nil {
					t.Fatal("exact amount/observation missing or incorrect")
				}
			} else if observation.ShortfallCents != nil || observation.AvailableBalanceCents != nil {
				t.Fatal("unavailable result became an amount")
			}
			events, balances, logins, bad := mock.Counts()
			want := 0
			if tc.balance {
				want = 1
			}
			if balances != want || bad || logins != 1 || events < 2 {
				t.Fatalf("HTTP counts events=%d balances=%d logins=%d bad_scope=%t", events, balances, logins, bad)
			}
		})
	}
	t.Run("mapping_exclusions", func(t *testing.T) {
		for _, tc := range []struct{ name, account, card, reason string }{
			{"missing_card", "fixture-connected-account", "missing-card", "MAPPING_UNAVAILABLE"},
			{"wrong_account", "wrong-account", "00000000-0000-4000-8000-000000000001", "MAPPING_CONFLICT"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := db.Repo.ResolveMapping(context.Background(), tc.account, tc.card)
				var f *domain.Failure
				if !errors.As(err, &f) || f.Reason != tc.reason {
					t.Fatal("mapping exclusion misclassified")
				}
			})
		}
		_, err := db.Admin.Exec(context.Background(), "UPDATE public.issuing_card_bindings SET is_active=false")
		testutil.Check(t, "deactivate test binding", err)
		_, err = db.Repo.ResolveMapping(context.Background(), "fixture-connected-account", "00000000-0000-4000-8000-000000000001")
		var f *domain.Failure
		if !errors.As(err, &f) || f.Reason != "MAPPING_UNAVAILABLE" {
			t.Fatal("inactive mapping not held")
		}
		_, err = db.Admin.Exec(context.Background(), "UPDATE public.issuing_card_bindings SET is_active=true")
		testutil.Check(t, "restore test binding", err)
	})
	for _, tc := range []struct {
		name                 string
		unauthorized, status int
		retry                bool
		reason               string
	}{
		{"refresh_once", 1, 0, false, ""},
		{"repeated_401", 2, 0, false, "PROVIDER_AUTHORIZATION_FAILED"},
		{"rate_limit", 0, 429, true, "PROVIDER_TEMPORARILY_UNAVAILABLE"},
		{"lookup_404", 0, 404, true, "PROVIDER_TEMPORARILY_UNAVAILABLE"},
		{"server_503", 0, 503, true, "PROVIDER_TEMPORARILY_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMock(t)
			mock.Mu.Lock()
			mock.Unauthorized = tc.unauthorized
			mock.EventStatus = tc.status
			mock.RetryAfter = "72"
			mock.Mu.Unlock()
			_, err := testutil.Resolver(t, db.Repo).Resolve(context.Background(), domain.Delivery{}, mock.Client.BeginAttempt())
			if tc.reason == "" {
				testutil.Check(t, "single refresh resolves", err)
			} else {
				var f *domain.Failure
				if !errors.As(err, &f) || f.Reason != tc.reason || f.Retryable != tc.retry || strings.Contains(err.Error(), "private-provider-body-marker") {
					t.Fatal("provider failure/refresh classification incorrect")
				}
				if tc.retry && f.RetryAfter.Seconds() < 72 {
					t.Fatal("Retry-After minimum lost")
				}
			}
			_, _, logins, _ := mock.Counts()
			if tc.unauthorized > 0 && logins != 2 {
				t.Fatalf("login count=%d, want 2", logins)
			}
		})
	}
	t.Run("live_unsupported_no_mapping_or_http", func(t *testing.T) {
		mock := testutil.NewMock(t)
		_, err := (&provider.Resolver{Mappings: db.Repo}).Resolve(context.Background(), domain.Delivery{EventName: "issuing.card_transaction.declined"}, mock.Client.BeginAttempt())
		var f *domain.Failure
		if !errors.As(err, &f) || f.Reason != "UNSUPPORTED_WEBHOOK_VERSION" {
			t.Fatal("live adapter did not hold")
		}
		events, balances, logins, _ := mock.Counts()
		if events+balances+logins != 0 {
			t.Fatal("unsupported live delivery called provider")
		}
	})
}
