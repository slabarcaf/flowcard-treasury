package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCredentialsAreLiteralAndBounded(t *testing.T) {
	base := "AIRWALLEX_CLIENT_ID='test-client'\nAIRWALLEX_API_KEY=\"test-key#kept\" # comment\nexport AIRWALLEX_ACCOUNT_ID=test-platform\n"
	cfg, err := readCredentials(strings.NewReader(base + "UNRELATED=$(never-run)\nAIRWALLEX_TOKEN=ignored\n"))
	if err != nil || cfg.apiKey != "test-key#kept" || cfg.platform != "test-platform" {
		t.Fatalf("literal credentials not preserved: %v", err)
	}
	for _, tc := range []struct{ name, extra string }{
		{"duplicate", "AIRWALLEX_API_KEY=second\n"},
		{"production host", "AIRWALLEX_BASE_URL=https://api.airwallex.com\n"},
		{"command substitution", "AIRWALLEX_WEBHOOK_ID=$(never-run)\n"},
		{"variable expansion", "AIRWALLEX_WEBHOOK_ID=${OTHER}\n"},
		{"unterminated quote", "AIRWALLEX_WEBHOOK_ID='unclosed\n"},
		{"shell suffix", "AIRWALLEX_WEBHOOK_ID='literal'; echo secret\n"},
		{"oversize", strings.Repeat("x", maxCredentialFile)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readCredentials(strings.NewReader(base + tc.extra)); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}

func TestPreflightScopeAndRedaction(t *testing.T) {
	var calls []string
	cfg := credentials{clientID: "private-client", apiKey: "private-key", platform: "test-platform", webhookID: "private-webhook", webhookSecret: "private-secret"}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.sandbox.airwallex.com" {
			t.Fatal("credential destination changed")
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		code, body := 200, `{"items":[]}`
		if r.URL.Path == "/api/v1/authentication/login" {
			if r.Method != "POST" || r.Header.Get("X-Api-Key") != cfg.apiKey || r.Header.Get("X-Login-As") != cfg.platform {
				t.Fatal("login scope missing")
			}
			body = `{"token":"private-token","expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`
		} else {
			if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-token" || r.Header.Get("X-Api-Key") != "" {
				t.Fatal("unexpected authority or write")
			}
			if r.URL.Path == "/api/v1/account" {
				body = `{"id":"test-platform","status":"ACTIVE"}`
			} else if r.URL.Path == "/api/v1/accounts" {
				code, body = 403, `{"message":"private-key private-token private-url"}`
			}
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	report := preflight(context.Background(), cfg, transport)
	if report.ReadAccessVerified || len(calls) != 8 {
		t.Fatalf("denied permissions falsely verified or wrong call count: %d", len(calls))
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{cfg.apiKey, cfg.clientID, cfg.platform, cfg.webhookSecret, cfg.webhookID, "private-token", "private-url"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private value leaked")
		}
	}
	if !strings.Contains(string(raw), "PERMISSION_REFUSED") || !strings.Contains(string(raw), "CONFIGURED_DELIVERY_UNVERIFIED") {
		t.Fatal("missing distinct capability states")
	}
}

func TestPreflightStopsOnMismatchedIdentity(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"token":"private-token","expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`
		if calls == 2 {
			body = `{"id":"wrong-platform","status":"ACTIVE"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	report := preflight(context.Background(), credentials{platform: "expected"}, transport)
	if report.ReadAccessVerified || calls != 2 {
		t.Fatal("identity mismatch did not stop probes")
	}
}

func TestProviderBoundsAndRedirect(t *testing.T) {
	for _, body := range []string{"", strings.Repeat("x", 1_000_001)} {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		if _, _, state := readProvider(context.Background(), client, "GET", "/api/v1/account", nil); state != "RESPONSE_INVALID" {
			t.Fatal("unbounded or empty response accepted")
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect forwarded credentials") }))
	defer target.Close()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target.URL}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	report := preflight(context.Background(), credentials{}, transport)
	if report.ReadAccessVerified || report.Checks[0].HTTPStatus != 302 {
		t.Fatal("redirect not refused")
	}
}

func TestCLIRejectsImplicitOrProductionExecution(t *testing.T) {
	for _, args := range [][]string{nil, {"setup"}, {"preflight", "--environment", "PRODUCTION", "--env-file", "private-path"}, {"preflight", "--env-file", "private-path"}, {"preflight", "--environment", "SANDBOX", "--env-file", "missing-private-path"}} {
		var out strings.Builder
		err := run(context.Background(), args, &out)
		if err == nil || strings.Contains(err.Error(), "private-path") || out.Len() != 0 {
			t.Fatal("unsafe invocation accepted or leaked input")
		}
	}
	var out strings.Builder
	err := run(context.Background(), []string{"setup-accounts", "--history", "--environment", "SANDBOX", "--env-file", "must-not-be-read"}, &out)
	if err == nil || err.Error() != "sandbox_history_only_for_serve_local" || out.Len() != 0 {
		t.Fatal("history mode reached a provider setup command or credential loading")
	}
}

func TestAuthenticationExpiryFormats(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, value := range []string{"2026-11-15T13:00:00Z", "2026-11-15T13:00:00+0000"} {
		if !validExpiry(value, now) {
			t.Fatal("supported future expiry rejected")
		}
	}
	for _, value := range []string{"", "2026-11-15T11:00:00Z", "2026-11-15T12:00:00Z", "not-a-time"} {
		if validExpiry(value, now) {
			t.Fatal("expired or invalid token accepted")
		}
	}
}
