// Package shortfall contains the authenticated, bounded shortfall read client.
package shortfall

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const issuer = "flowcard-node-proxy"
const audience = "flowcard-shortfall-service"
const subject = "shortfall-reader"
const maxResponseBytes = 256 << 10

type Record struct {
	ID                    string     `json:"id"`
	CustomerID            *string    `json:"customer_id"`
	AccountID             string     `json:"account_id"`
	CardID                *string    `json:"card_id"`
	Outcome               string     `json:"outcome"`
	Reason                *string    `json:"reason"`
	Currency              *string    `json:"currency"`
	AttemptedAmountCents  *int64     `json:"attempted_amount_cents"`
	AvailableBalanceCents *int64     `json:"available_balance_cents"`
	ShortfallCents        *int64     `json:"shortfall_cents"`
	CanonicalEventID      string     `json:"canonical_event_id"`
	TransactionID         *string    `json:"transaction_id"`
	LifecycleID           *string    `json:"lifecycle_id"`
	CreatedAt             time.Time  `json:"created_at"`
	ProviderCreatedAt     *time.Time `json:"provider_created_at"`
	ProviderTransactedAt  *time.Time `json:"provider_transacted_at"`
	ProviderUpdatedAt     *time.Time `json:"provider_updated_at"`
	BalanceRequestedAt    *time.Time `json:"balance_requested_at"`
	BalanceObservedAt     *time.Time `json:"balance_observed_at"`
	RequiresRecheck       bool       `json:"requires_recheck"`
	AmountSemantics       string     `json:"amount_semantics"`
}
type Page struct {
	Items      []Record `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}
type UpstreamError struct {
	Status int
	Retry  time.Duration
}

func (e *UpstreamError) Error() string             { return fmt.Sprintf("shortfall_upstream_status_%d", e.Status) }
func (e *UpstreamError) RetryAfter() time.Duration { return e.Retry }
func (e *UpstreamError) HTTPStatus() int           { return e.Status }

type ProtocolError struct{ Code string }

func (e *ProtocolError) Error() string   { return e.Code }
func (e *ProtocolError) Permanent() bool { return true }
func protocolError(code string) error    { return &ProtocolError{Code: code} }

type Client struct {
	base          *url.URL
	secret        []byte
	binding, mode string
	http          *http.Client
	now           func() time.Time
}

func New(baseURL, secret, binding, mode string, client *http.Client) (*Client, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, protocolError("shortfall_endpoint_invalid")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, protocolError("shortfall_endpoint_requires_tls")
		}
	}
	if len(secret) < 32 || binding == "" {
		return nil, protocolError("shortfall_auth_configuration_invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	c := *client
	c.Timeout = 5 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, secret: []byte(secret), binding: binding, mode: mode, http: &c, now: time.Now}, nil
}
func (c *Client) List(ctx context.Context, cursor *string) (Page, int, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/shortfalls"
	q := url.Values{"outcome": []string{"SHORTFALL_DETECTED"}}
	if cursor != nil {
		q.Set("cursor", *cursor)
	}
	u.RawQuery = q.Encode()
	var p Page
	status, err := c.get(ctx, u.String(), &p)
	if err != nil {
		return Page{}, status, err
	}
	if len(p.Items) > 25 {
		return Page{}, status, protocolError("shortfall_page_too_large")
	}
	for i, r := range p.Items {
		if err := validateIdentity(r); err != nil {
			return Page{}, status, err
		}
		if i > 0 {
			prev := p.Items[i-1]
			if prev.CreatedAt.Before(r.CreatedAt) || (prev.CreatedAt.Equal(r.CreatedAt) && prev.ID <= r.ID) {
				return Page{}, status, protocolError("shortfall_order_invalid")
			}
		}
	}
	if p.NextCursor != nil && (strings.TrimSpace(*p.NextCursor) == "" || len(*p.NextCursor) > 2048 || (cursor != nil && *cursor == *p.NextCursor) || len(p.Items) == 0) {
		return Page{}, status, protocolError("shortfall_cursor_invalid")
	}
	return p, status, nil
}
func (c *Client) Read(ctx context.Context, id string) (Record, int, error) {
	var r Record
	if !validID(id) {
		return r, 0, protocolError("shortfall_id_invalid")
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/shortfalls/" + id
	status, err := c.get(ctx, u.String(), &r)
	if err != nil {
		return r, status, err
	}
	if r.ID != id {
		return Record{}, status, protocolError("shortfall_scope_mismatch")
	}
	if err := validateIdentity(r); err != nil {
		return Record{}, status, err
	}
	return r, status, nil
}
func (c *Client) get(ctx context.Context, target string, dst any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.assertion(c.now())
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, protocolError("shortfall_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("shortfall_upstream_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
		return resp.StatusCode, &UpstreamError{Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, &UpstreamError{Status: resp.StatusCode, Retry: parseRetryAfter(resp.Header.Get("Retry-After"), c.now())}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return resp.StatusCode, protocolError("shortfall_response_invalid")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil || dec.Decode(new(any)) != io.EOF {
		return resp.StatusCode, protocolError("shortfall_response_invalid")
	}
	return resp.StatusCode, nil
}
func (c *Client) assertion(now time.Time) (string, error) {
	head, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims := struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		TokenID   string `json:"jti"`
	}{issuer, audience, subject, now.Unix(), now.Add(20 * time.Second).Unix(), fmt.Sprintf("coordinator-%d", now.UnixNano())}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", protocolError("shortfall_assertion_failed")
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString(head) + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
func validateIdentity(r Record) error {
	if !validID(r.ID) {
		return protocolError("shortfall_record_identity_invalid")
	}
	if r.Outcome != "SHORTFALL_DETECTED" {
		return protocolError("shortfall_outcome_mismatch")
	}
	return nil
}

// HoldReason classifies a readable positive row without changing credit policy.
func HoldReason(r Record) string {
	if strings.TrimSpace(r.AccountID) == "" || strings.TrimSpace(r.CanonicalEventID) == "" || r.CreatedAt.IsZero() {
		return "SOURCE_IDENTITY_INCOMPLETE"
	}
	if r.CustomerID == nil || strings.TrimSpace(*r.CustomerID) == "" || r.CardID == nil || strings.TrimSpace(*r.CardID) == "" || r.TransactionID == nil || strings.TrimSpace(*r.TransactionID) == "" {
		return "SOURCE_SCOPE_INVALID"
	}
	if r.Currency == nil || *r.Currency != "USD" {
		return "SOURCE_CURRENCY_UNSUPPORTED"
	}
	if r.AttemptedAmountCents == nil || *r.AttemptedAmountCents <= 0 || r.AvailableBalanceCents == nil || *r.AvailableBalanceCents < 0 || r.ShortfallCents == nil || *r.ShortfallCents <= 0 || *r.ShortfallCents != *r.AttemptedAmountCents-*r.AvailableBalanceCents || r.BalanceRequestedAt == nil || r.BalanceObservedAt == nil || r.BalanceObservedAt.Before(*r.BalanceRequestedAt) {
		return "SOURCE_FACTS_INVALID"
	}
	return ""
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil && n > 0 && n <= int64((time.Duration(1<<63-1))/time.Second) {
		return time.Duration(n) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}
func validID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, ch := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') && !(ch >= 'A' && ch <= 'F') {
			return false
		}
	}
	return true
}

func (p *Page) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return protocolError("shortfall_page_json_invalid")
	}
	for _, key := range []string{"items", "next_cursor"} {
		if _, ok := fields[key]; !ok {
			return protocolError("shortfall_page_field_missing")
		}
	}
	if bytes.Equal(bytes.TrimSpace(fields["items"]), []byte("null")) {
		return protocolError("shortfall_page_items_null")
	}
	type alias Page
	var value alias
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return protocolError("shortfall_page_json_invalid")
	}
	*p = Page(value)
	return nil
}
