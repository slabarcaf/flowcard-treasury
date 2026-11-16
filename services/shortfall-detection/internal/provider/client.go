package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

const maxProviderBodyBytes int64 = 10_000_000

// ClientConfig contains server-owned credentials and an explicitly selected version.
type ClientConfig struct {
	BaseURL    string
	Version    string
	ClientID   string `json:"-"`
	APIKey     string `json:"-"`
	LoginAs    string `json:"-"`
	HTTPClient *http.Client
	Now        func() time.Time
}

// Client performs authenticated read-only provider operations and token acquisition.
// It does not enable a webhook parser or establish version/funding compatibility.
type Client struct {
	baseURL   string
	version   string
	clientID  string
	apiKey    string
	loginAs   string
	http      *http.Client
	now       func() time.Time
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// Attempt shares one authentication refresh budget across a worker job's reads.
// An Attempt is used serially, matching the one-in-flight worker contract.
type Attempt struct {
	client    *Client
	refreshed bool
}

// NewClient creates a bounded client. Plain HTTP is allowed only for loopback mocks.
func NewClient(cfg ClientConfig) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" ||
		base.Fragment != "" || (base.Path != "" && base.Path != "/") ||
		(cfg.Version == "" || cfg.ClientID == "" || cfg.APIKey == "") {
		return nil, errors.New("provider_configuration_invalid")
	}
	ip := net.ParseIP(base.Hostname())
	loopback := base.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback) {
		return nil, errors.New("provider_url_invalid")
	}
	client := http.Client{}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	client.Timeout = 10 * time.Second
	// Never forward credentials or account scope through redirects.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{baseURL: strings.TrimRight(base.String(), "/"), version: cfg.Version,
		clientID: cfg.ClientID, apiKey: cfg.APIKey, loginAs: cfg.LoginAs, http: &client, now: cfg.Now}, nil
}

// BeginAttempt gives one worker job a fresh, single-refresh budget.
func (c *Client) BeginAttempt() *Attempt { return &Attempt{client: c} }

func (c *Client) accessToken(ctx context.Context, refresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !refresh && c.token != "" && c.now().Add(30*time.Second).Before(c.expiresAt) {
		return c.token, nil
	}
	body, _, err := c.request(ctx, http.MethodPost, "/api/v1/authentication/login",
		http.Header{"X-Client-Id": {c.clientID}, "X-Api-Key": {c.apiKey}, "X-Login-As": {c.loginAs}}, false)
	if err != nil {
		return "", err
	}
	var login struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal(body, &login) != nil || login.Token == "" {
		return "", &domain.Failure{Reason: "PROVIDER_AUTHORIZATION_FAILED"}
	}
	// Reuse Node's 30-minute fallback when the optional expiry is absent.
	expires := c.now().Add(30 * time.Minute)
	if login.ExpiresAt != "" {
		expires, err = parseProviderTime(login.ExpiresAt)
		if err != nil || !expires.After(c.now()) {
			return "", &domain.Failure{Reason: "PROVIDER_AUTHORIZATION_FAILED"}
		}
	}
	c.token, c.expiresAt = login.Token, expires
	return c.token, nil
}

func (a *Attempt) get(ctx context.Context, path, accountID string, lookup bool) ([]byte, error) {
	if accountID == "" {
		return nil, &domain.Failure{Reason: "MAPPING_UNAVAILABLE"}
	}
	token, err := a.client.accessToken(ctx, false)
	if err != nil {
		return nil, err
	}
	for {
		body, status, err := a.client.request(ctx, http.MethodGet, path, http.Header{
			"Authorization": {"Bearer " + token}, "X-On-Behalf-Of": {accountID},
			"X-Api-Version": {a.client.version},
		}, lookup)
		if status != http.StatusUnauthorized || a.refreshed {
			return body, err
		}
		a.refreshed = true
		token, err = a.client.accessToken(ctx, true)
		if err != nil {
			return nil, err
		}
	}
}

func (c *Client) request(ctx context.Context, method, path string, headers http.Header, lookup bool) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, &domain.Failure{Reason: "PROVIDER_REQUEST_INVALID"}
	}
	request.Header = headers
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, &domain.Failure{Reason: "PROVIDER_NETWORK_ERROR", Retryable: true}
	}
	defer func() {
		// Never surface provider bodies/transport errors, including close failures.
		_ = response.Body.Close()
	}()
	status := response.StatusCode
	if status < 200 || status >= 300 {
		failure := &domain.Failure{Reason: "PROVIDER_REQUEST_FAILED"}
		switch {
		case status == 401 || status == 403:
			failure.Reason = "PROVIDER_AUTHORIZATION_FAILED"
		case status == 429 || (status >= 500 && status < 600) || (lookup && status == 404):
			failure.Reason, failure.Retryable = "PROVIDER_TEMPORARILY_UNAVAILABLE", true
			failure.RetryAfter = retryAfter(response.Header.Get("Retry-After"), c.now())
		}
		return nil, status, failure
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProviderBodyBytes+1))
	if err != nil {
		return nil, status, &domain.Failure{Reason: "PROVIDER_NETWORK_ERROR", Retryable: true}
	}
	if int64(len(raw)) > maxProviderBodyBytes || !json.Valid(raw) {
		return nil, status, &domain.Failure{Reason: "PROVIDER_RESPONSE_INVALID"}
	}
	return raw, status, nil
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func parseProviderTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("provider_time_invalid")
}
