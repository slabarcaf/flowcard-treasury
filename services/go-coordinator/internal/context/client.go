// Package customercontext reads the proposed bounded underwriting context API.
package customercontext

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
	"strings"
	"time"
)

const maxResponseBytes = 256 << 10

var ErrMissing = errors.New("customer_context_missing")

type SourceRef struct {
	System      string     `json:"system"`
	RecordID    string     `json:"record_id"`
	Version     string     `json:"version"`
	EffectiveAt *time.Time `json:"effective_at"`
}
type Response struct {
	SchemaVersion   int             `json:"schema_version"`
	SourceMode      string          `json:"source_mode"`
	CustomerID      string          `json:"customer_id"`
	SnapshotID      string          `json:"snapshot_id"`
	SnapshotVersion int64           `json:"snapshot_version"`
	CapturedAt      time.Time       `json:"captured_at"`
	PolicyID        string          `json:"policy_id"`
	PolicyHash      string          `json:"policy_hash"`
	Coverage        map[string]bool `json:"coverage"`
	Fresh           bool            `json:"fresh"`
	Data            json.RawMessage `json:"data"`
	SourceRefs      []SourceRef     `json:"source_refs"`
}
type Client struct {
	base   *url.URL
	secret []byte
	mode   string
	http   *http.Client
	now    func() time.Time
}

func New(baseURL, secret, mode string, httpClient *http.Client) (*Client, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("underwriting_endpoint_invalid")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("underwriting_endpoint_requires_tls")
		}
	}
	if len(secret) < 32 {
		return nil, errors.New("underwriting_auth_configuration_invalid")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	c := *httpClient
	c.Timeout = 5 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, secret: []byte(secret), mode: mode, http: &c, now: time.Now}, nil
}

func (c *Client) Read(ctx context.Context, customerID string) (Response, int, error) {
	var result Response
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/underwriting/customers/" + url.PathEscape(customerID) + "/context"
	status, err := c.get(ctx, u.String(), &result)
	if err != nil {
		if status == 404 {
			return Response{}, status, ErrMissing
		}
		return Response{}, status, err
	}
	if result.SchemaVersion != 1 || result.SourceMode != c.mode || result.CustomerID != customerID || result.SnapshotID == "" || result.SnapshotVersion <= 0 || result.CapturedAt.IsZero() || result.PolicyID == "" || result.PolicyHash == "" || result.Coverage == nil || result.SourceRefs == nil || !jsonObject(result.Data) || len(result.Data) > 32<<10 || hasForbiddenKeys(result.Data) {
		return Response{}, status, errors.New("customer_context_contract_invalid")
	}
	for _, ref := range result.SourceRefs {
		if ref.System == "" || ref.RecordID == "" || ref.Version == "" {
			return Response{}, status, errors.New("customer_context_source_ref_invalid")
		}
	}
	return result, status, nil
}

func (c *Client) get(ctx context.Context, target string, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.assertion(c.now())
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, errors.New("underwriting_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("underwriting_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("underwriting_status_%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return resp.StatusCode, errors.New("underwriting_response_invalid")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil || dec.Decode(new(any)) != io.EOF {
		return resp.StatusCode, errors.New("underwriting_response_invalid")
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
		JTI       string `json:"jti"`
	}{"flowcard-node-proxy", "flowcard-underwriting-service", "underwriting-evaluator", now.Unix(), now.Add(20 * time.Second).Unix(), fmt.Sprintf("coordinator-%d", now.UnixNano())}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", errors.New("underwriting_assertion_failed")
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString(head) + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
func jsonObject(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return json.Unmarshal(raw, &obj) == nil && obj != nil
}
func hasForbiddenKeys(raw json.RawMessage) bool {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&value) != nil {
		return true
	}
	return forbidden(value)
}
func forbidden(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			k := strings.ToLower(key)
			if strings.Contains(k, "facility") || strings.Contains(k, "note") || strings.Contains(k, "document") || strings.Contains(k, "secret") || strings.Contains(k, "credential") {
				return true
			}
			if forbidden(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if forbidden(child) {
				return true
			}
		}
	}
	return false
}

// HasForbiddenData detects forbidden note/document/facility/credential keys recursively.
func HasForbiddenData(raw json.RawMessage) bool { return hasForbiddenKeys(raw) }

// ReadScoped verifies the context's raw account and card linkage as well as route customer scope.
func (c *Client) ReadScoped(ctx context.Context, customerID, rawAccountID, rawCardID string) (Response, int, error) {
	response, status, err := c.Read(ctx, customerID)
	if err != nil {
		return Response{}, status, err
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(response.Data, &data) != nil {
		return Response{}, status, errors.New("customer_context_data_invalid")
	}
	var account string
	if raw, ok := data["account_id"]; !ok || json.Unmarshal(raw, &account) != nil || account == "" || account != rawAccountID {
		return Response{}, status, errors.New("customer_context_account_scope_mismatch")
	}
	var cardID string
	if raw, ok := data["card_id"]; !ok || json.Unmarshal(raw, &cardID) != nil || cardID == "" || cardID != rawCardID {
		return Response{}, status, errors.New("customer_context_card_scope_mismatch")
	}
	return response, status, nil
}
