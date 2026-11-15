// Package shortfall reads authenticated observations from the configured service.
package shortfall

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
)

const maxResponseBytes = 4_000_000

var (
	ErrNotFound        = errors.New("shortfall_not_found")
	ErrUnavailable     = errors.New("shortfall_unavailable")
	ErrResponseInvalid = errors.New("shortfall_response_invalid")
)

// Client performs bounded, authenticated GETs against the mode-bound base URL.
type Client struct {
	baseURL    *url.URL
	secret     []byte
	httpClient *http.Client
}

// NewClient constructs a reader that never follows credential-bearing redirects.
func NewClient(baseURL string, secret string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !validBaseURL(parsed, baseURL) || len(secret) < 32 {
		return nil, errors.New("shortfall_client_configuration_invalid")
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Client{baseURL: parsed, secret: []byte(secret), httpClient: client}, nil
}

func validBaseURL(parsed *url.URL, original string) bool {
	if parsed == nil || !parsed.IsAbs() || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(original, "#") {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "https" {
		return true
	}
	if scheme != "http" {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// Get fetches one final shortfall observation with a short-lived service assertion.
func (c *Client) Get(ctx context.Context, id string, now time.Time) (domain.ShortfallRecord, error) {
	if !domain.ValidID(id) || now.IsZero() {
		return domain.ShortfallRecord{}, ErrResponseInvalid
	}
	endpoint, err := url.JoinPath(c.baseURL.String(), "api", "shortfalls", id)
	if err != nil {
		return domain.ShortfallRecord{}, ErrUnavailable
	}
	token, err := assertionToken(c.secret, now.UTC())
	if err != nil {
		return domain.ShortfallRecord{}, ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domain.ShortfallRecord{}, ErrUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return domain.ShortfallRecord{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return domain.ShortfallRecord{}, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return domain.ShortfallRecord{}, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes || !validJSONObjects(body) {
		return domain.ShortfallRecord{}, ErrResponseInvalid
	}
	var record domain.ShortfallRecord
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || !completeRecord(body, record) {
		return domain.ShortfallRecord{}, ErrResponseInvalid
	}
	return record, nil
}

func assertionToken(secret []byte, now time.Time) (string, error) {
	jti, err := randomUUID()
	if err != nil {
		return "", err
	}
	header, err := json.Marshal(struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		TokenID   string `json:"jti"`
	}{
		Issuer: "flowcard-node-proxy", Audience: "flowcard-shortfall-service",
		Subject: "shortfall-reader", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
		TokenID: jti,
	})
	if err != nil {
		return "", err
	}
	encoding := base64.RawURLEncoding
	unsigned := encoding.EncodeToString(header) + "." + encoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(unsigned)); err != nil {
		return "", err
	}
	return unsigned + "." + encoding.EncodeToString(mac.Sum(nil)), nil
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func completeRecord(body []byte, record domain.ShortfallRecord) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return false
	}
	required := []string{
		"id", "customer_id", "account_id", "card_id", "outcome", "reason", "currency",
		"attempted_amount_cents", "available_balance_cents", "shortfall_cents",
		"canonical_event_id", "transaction_id", "lifecycle_id", "created_at",
		"provider_created_at", "provider_transacted_at", "provider_updated_at",
		"balance_requested_at", "balance_observed_at", "requires_recheck", "amount_semantics",
	}
	for _, field := range required {
		if _, exists := object[field]; !exists {
			return false
		}
	}
	for _, field := range []string{"account_id", "outcome", "canonical_event_id", "created_at", "requires_recheck", "amount_semantics"} {
		if bytes.Equal(bytes.TrimSpace(object[field]), []byte("null")) {
			return false
		}
	}
	if !domain.ValidID(record.ID) || !validIdentifier(record.AccountID) ||
		!validIdentifier(record.CanonicalEventID) || !record.RequiresRecheck ||
		record.CreatedAt.IsZero() ||
		record.AmountSemantics != "current_need_at_balance_observation" {
		return false
	}
	if record.Outcome != "SHORTFALL_DETECTED" && record.Outcome != "NO_CURRENT_SHORTFALL" {
		return true
	}
	return record.Currency != nil &&
		record.AttemptedAmountCents != nil && record.AvailableBalanceCents != nil &&
		record.ShortfallCents != nil && record.BalanceRequestedAt != nil &&
		record.BalanceObservedAt != nil && !record.BalanceRequestedAt.IsZero() &&
		!record.BalanceObservedAt.IsZero() && !record.BalanceObservedAt.Before(*record.BalanceRequestedAt)
}

func validIdentifier(value string) bool {
	return value != "" && len(value) <= 200 && value == strings.TrimSpace(value) &&
		utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validJSONObjects(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !scanValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func scanValue(decoder *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, exists := keys[key]; exists {
				return false
			}
			keys[key] = struct{}{}
			if !scanValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !scanValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
