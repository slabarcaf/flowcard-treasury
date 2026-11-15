package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const anthropicVersion = "2023-06-01"
const maxProviderResponse = MaxRequestBytes

type Anthropic struct {
	endpoint string
	model    string
	key      string
	http     *http.Client
}

func NewAnthropic(endpoint, model, key string, httpClient *http.Client) (*Anthropic, error) {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(key) == "" {
		return nil, providerFailure(ProviderPermanent, "ANTHROPIC_CONFIGURATION_MISSING", 0)
	}
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	u, err := url.ParseRequestURI(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, providerFailure(ProviderPermanent, "ANTHROPIC_ENDPOINT_INVALID", 0)
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, providerFailure(ProviderPermanent, "ANTHROPIC_ENDPOINT_REQUIRES_TLS", 0)
		}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 25 * time.Second}
	}
	c := *httpClient
	c.Timeout = 25 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Anthropic{endpoint: u.String(), model: model, key: key, http: &c}, nil
}

func (a *Anthropic) CreateMessage(ctx context.Context, request Request) (Response, error) {
	var out Response
	if request.Model != "" && request.Model != a.model {
		return out, providerFailure(ProviderPermanent, "ANTHROPIC_MODEL_MISMATCH", 0)
	}
	request.Model = a.model
	body, err := json.Marshal(request)
	if err != nil {
		return out, providerFailure(ProviderPermanent, "ANTHROPIC_REQUEST_INVALID", 0)
	}
	if len(body) > MaxRequestBytes {
		return out, providerFailure(ProviderPermanent, "AGENT_REQUEST_LIMIT_REACHED", 0)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return out, providerFailure(ProviderPermanent, "ANTHROPIC_REQUEST_INVALID", 0)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", anthropicVersion)
	resp, err := a.http.Do(req)
	if err != nil {
		return out, providerFailure(ProviderAmbiguous, "ANTHROPIC_TRANSPORT_UNCERTAIN", 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := fmt.Sprintf("ANTHROPIC_HTTP_%d", resp.StatusCode)
		if resp.StatusCode == http.StatusRequestTimeout {
			return out, providerFailure(ProviderAmbiguous, code, parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()))
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return out, providerFailure(ProviderTransient, code, parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()))
		}
		return out, providerFailure(ProviderPermanent, code, 0)
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse+1))
	if err != nil || len(response) > maxProviderResponse {
		return out, providerFailure(ProviderPermanent, "ANTHROPIC_RESPONSE_INCOMPLETE", 0)
	}
	dec := json.NewDecoder(bytes.NewReader(response))
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF {
		return out, providerFailure(ProviderPermanent, "ANTHROPIC_RESPONSE_INVALID", 0)
	}
	if out.Type != "message" || out.Role != "assistant" || len(out.Content) == 0 {
		return out, providerFailure(ProviderPermanent, "ANTHROPIC_RESPONSE_INVALID", 0)
	}
	return out, nil
}

func providerFailure(kind ProviderFailureKind, code string, retryAfter time.Duration) *ProviderError {
	return &ProviderError{Kind: kind, Code: code, RetryAfter: retryAfter}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		maxSeconds := int64((time.Duration(1<<63 - 1)) / time.Second)
		if seconds > uint64(maxSeconds) {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	} else if decimalDigits(value) {
		return time.Duration(1<<63 - 1)
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
