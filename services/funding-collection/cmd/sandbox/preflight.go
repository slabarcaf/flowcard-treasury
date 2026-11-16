package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type check struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type preflightReport struct {
	Environment        string  `json:"environment"`
	ReadAccessVerified bool    `json:"read_access_verified"`
	Checks             []check `json:"checks"`
}

// preflight issues authentication and read calls only. GET success is evidence
// for that endpoint and scope, never proof of write authority or settlement.
func preflight(ctx context.Context, cfg credentials, transport http.RoundTripper) preflightReport {
	report := preflightReport{Environment: "SANDBOX"}
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	headers := http.Header{"X-Client-Id": {cfg.clientID}, "X-Api-Key": {cfg.apiKey}, "X-Login-As": {cfg.platform}}
	raw, status, state := readProvider(ctx, client, http.MethodPost, "/api/v1/authentication/login", headers)
	if state != "VERIFIED" {
		report.Checks = append(report.Checks, check{"authentication", state, status})
		return report
	}
	var login struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal(raw, &login) != nil || login.Token == "" || !validExpiry(login.ExpiresAt, time.Now()) {
		report.Checks = append(report.Checks, check{"authentication", "INVALID_RESPONSE", status})
		return report
	}
	report.Checks = append(report.Checks, check{"authentication", "VERIFIED", status})
	headers = http.Header{"Authorization": {"Bearer " + login.Token}}
	raw, status, state = readProvider(ctx, client, http.MethodGet, "/api/v1/account", headers)
	if state == "VERIFIED" {
		var account struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &account) != nil || account.ID != cfg.platform || account.Status != "ACTIVE" {
			state = "ACCOUNT_IDENTITY_OR_STATUS_MISMATCH"
		}
	}
	report.Checks = append(report.Checks, check{"platform_identity", state, status})
	if state != "VERIFIED" {
		return report
	}
	report.ReadAccessVerified = true
	for _, probe := range []struct{ name, path string }{
		{"connected_accounts_read", "/api/v1/accounts?page_size=10"},
		{"platform_balances_read", "/api/v1/balances/current?account_type=cash"},
		{"platform_global_accounts_read", "/api/v1/global_accounts?page_size=10"},
		{"platform_issuing_read", "/api/v1/issuing/cards?page_size=10"},
		{"platform_deposits_read", "/api/v1/deposits"},
		{"platform_ca_transfers_read", "/api/v1/connected_account_transfers"},
	} {
		body, code, result := readProvider(ctx, client, http.MethodGet, probe.path, headers)
		if result == "VERIFIED" && !json.Valid(body) {
			result = "INVALID_RESPONSE"
		}
		report.Checks = append(report.Checks, check{probe.name, result, code})
		if result != "VERIFIED" {
			report.ReadAccessVerified = false
		}
	}
	webhookState := "NOT_CONFIGURED"
	if cfg.webhookID != "" && cfg.webhookSecret != "" {
		webhookState = "CONFIGURED_DELIVERY_UNVERIFIED"
	}
	report.Checks = append(report.Checks, check{"webhook", webhookState, 0}, check{"connected_account_scopes", "UNVERIFIED", 0}, check{"transfer_write_authority", "UNVERIFIED", 0}, check{"fee_and_purpose_contract", "UNVERIFIED", 0})
	return report
}

func readProvider(ctx context.Context, client *http.Client, method, path string, headers http.Header) ([]byte, int, string) {
	req, err := http.NewRequestWithContext(ctx, method, sandboxHost+path, nil)
	if err != nil {
		return nil, 0, "REQUEST_INVALID"
	}
	if headers != nil {
		req.Header = headers.Clone()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "NETWORK_UNAVAILABLE"
	}
	defer resp.Body.Close()
	// Error bodies can contain credential/account details and never reach output.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		state := "PROVIDER_REFUSED"
		switch resp.StatusCode {
		case 401:
			state = "AUTHENTICATION_REFUSED"
		case 403:
			state = "PERMISSION_REFUSED"
		case 429:
			state = "RATE_LIMITED"
		}
		return nil, resp.StatusCode, state
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1_000_001))
	if err != nil || len(body) > 1_000_000 {
		return nil, resp.StatusCode, "RESPONSE_INVALID"
	}
	if strings.TrimSpace(string(body)) == "" {
		return nil, resp.StatusCode, "RESPONSE_INVALID"
	}
	return body, resp.StatusCode, "VERIFIED"
}

// Airwallex timestamps may use RFC3339 or its documented compact UTC offset.
func validExpiry(value string, now time.Time) bool {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700"} {
		if expiry, err := time.Parse(layout, value); err == nil {
			return expiry.After(now)
		}
	}
	return false
}
