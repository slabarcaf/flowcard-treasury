package upstream

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ShortfallObservation is the bounded saved-observation read DTO. Its source
// mode is supplied by the trusted configured endpoint because the DTO omits it.
type ShortfallObservation struct {
	ID                    string          `json:"id"`
	CustomerID            string          `json:"customer_id"`
	AccountID             string          `json:"account_id"`
	CardID                *string         `json:"card_id"`
	Outcome               string          `json:"outcome"`
	Currency              *string         `json:"currency"`
	AttemptedAmountCents  *int64          `json:"attempted_amount_cents"`
	AvailableBalanceCents *int64          `json:"available_balance_cents"`
	ShortfallCents        *int64          `json:"shortfall_cents"`
	CanonicalEventID      string          `json:"canonical_event_id"`
	TransactionID         *string         `json:"transaction_id"`
	CreatedAt             time.Time       `json:"created_at"`
	BalanceRequestedAt    *time.Time      `json:"balance_requested_at"`
	BalanceObservedAt     *time.Time      `json:"balance_observed_at"`
	RequiresRecheck       bool            `json:"requires_recheck"`
	AmountSemantics       string          `json:"amount_semantics"`
	Raw                   json.RawMessage `json:"-"`
}

type ShortfallClient struct {
	baseURL string
	mode    string
	secret  []byte
	http    *http.Client
	now     func() time.Time
}

func NewShortfallClient(baseURL, secret string) (*ShortfallClient, error) {
	return NewShortfallClientForModeWithClock(baseURL, secret, "", time.Now)
}
func NewShortfallClientWithClock(baseURL, secret string, now func() time.Time) (*ShortfallClient, error) {
	return NewShortfallClientForModeWithClock(baseURL, secret, "", now)
}
func NewShortfallClientForModeWithClock(baseURL, secret, mode string, now func() time.Time) (*ShortfallClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(secret) < 32 {
		return nil, errors.New("shortfall_client_configuration_invalid")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("shortfall_url_requires_https")
	}
	if mode != "" && mode != "DEMO" && mode != "SANDBOX" {
		return nil, errors.New("shortfall_mode_invalid")
	}
	if now == nil {
		now = time.Now
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &ShortfallClient{baseURL: strings.TrimRight(u.String(), "/"), mode: mode, secret: []byte(secret), http: client, now: now}, nil
}

func (c *ShortfallClient) Read(ctx context.Context, id string) (ShortfallObservation, error) {
	return c.ReadForMode(ctx, id, c.mode)
}
func (c *ShortfallClient) ReadForMode(ctx context.Context, id, expectedMode string) (ShortfallObservation, error) {
	if id == "" {
		return ShortfallObservation{}, errors.New("shortfall_id_invalid")
	}
	if expectedMode != "DEMO" && expectedMode != "SANDBOX" {
		return ShortfallObservation{}, errors.New("shortfall_mode_invalid")
	}
	if c.mode != "" && c.mode != expectedMode {
		return ShortfallObservation{}, errors.New("shortfall_mode_mismatch")
	}
	token, err := c.assertion()
	if err != nil {
		return ShortfallObservation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/shortfalls/"+url.PathEscape(id), nil)
	if err != nil {
		return ShortfallObservation{}, errors.New("shortfall_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return ShortfallObservation{}, errors.New("shortfall_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ShortfallObservation{}, errors.New("shortfall_not_found")
	}
	if resp.StatusCode != http.StatusOK {
		return ShortfallObservation{}, errors.New("shortfall_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4_000_001))
	if err != nil || len(raw) > 4_000_000 || !json.Valid(raw) {
		return ShortfallObservation{}, errors.New("shortfall_response_invalid")
	}
	var value ShortfallObservation
	if json.Unmarshal(raw, &value) != nil || value.ID != id || value.CustomerID == "" || value.AccountID == "" || value.CardID == nil || *value.CardID == "" || value.Outcome != "SHORTFALL_DETECTED" || value.Currency == nil || *value.Currency != "USD" || value.AttemptedAmountCents == nil || *value.AttemptedAmountCents <= 0 || value.AvailableBalanceCents == nil || *value.AvailableBalanceCents < 0 || value.ShortfallCents == nil || *value.ShortfallCents <= 0 || value.TransactionID == nil || *value.TransactionID == "" || value.CanonicalEventID == "" || value.BalanceObservedAt == nil || value.BalanceRequestedAt == nil || value.CreatedAt.IsZero() || !value.RequiresRecheck || value.AmountSemantics != "current_need_at_balance_observation" || value.BalanceRequestedAt.After(*value.BalanceObservedAt) || !isUTCInstant(value.CreatedAt) || !isUTCInstant(*value.BalanceRequestedAt) || !isUTCInstant(*value.BalanceObservedAt) || value.BalanceObservedAt.After(c.now().UTC().Add(5*time.Second)) {
		return ShortfallObservation{}, errors.New("shortfall_response_invalid")
	}
	attempted, available := *value.AttemptedAmountCents, *value.AvailableBalanceCents
	want := int64(0)
	if attempted > available {
		want = attempted - available
	}
	if want != *value.ShortfallCents {
		return ShortfallObservation{}, errors.New("shortfall_amount_semantics_invalid")
	}
	value.Raw = append(json.RawMessage(nil), raw...)
	return value, nil
}

func (c *ShortfallClient) assertion() (string, error) {
	now := c.now().UTC()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", errors.New("shortfall_assertion_failed")
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": "flowcard-node-proxy", "aud": "flowcard-shortfall-service", "sub": "shortfall-reader", "jti": base64.RawURLEncoding.EncodeToString(nonce[:]), "iat": now.Unix(), "exp": now.Add(20 * time.Second).Unix()})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func isUTCInstant(t time.Time) bool { _, offset := t.Zone(); return offset == 0 }
