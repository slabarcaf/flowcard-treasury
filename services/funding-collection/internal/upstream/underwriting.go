package upstream

import (
	"bytes"
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

const maxUnderwritingResponse = 4_000_000

// UnderwritingClient performs read-only requests for saved assessments/context.
type UnderwritingClient struct {
	baseURL string
	secret  []byte
	http    *http.Client
	now     func() time.Time
}

// RuleTrace preserves the upstream rule trace without reimplementing it.
type RuleTrace struct {
	RuleID     string                `json:"rule_id"`
	Status     string                `json:"status"`
	ReasonCode string                `json:"reason_code"`
	Actual     json.RawMessage       `json:"actual"`
	Threshold  json.RawMessage       `json:"threshold"`
	SourceRefs []EvaluationSourceRef `json:"source_refs"`
}

type EvaluationSourceRef struct {
	System   string `json:"system"`
	RecordID string `json:"record_id"`
	Version  string `json:"version"`
}

// SavedEvaluation is a typed saved-decision envelope. It never authorizes funding.
type SavedEvaluation struct {
	ID                string          `json:"id"`
	ShortfallID       string          `json:"shortfall_id"`
	CustomerID        string          `json:"customer_id"`
	AccountID         string          `json:"account_id"`
	TransactionID     string          `json:"transaction_id"`
	SourceMode        string          `json:"source_mode"`
	PolicyID          string          `json:"policy_id"`
	PolicyHash        string          `json:"policy_hash"`
	Status            string          `json:"status"`
	ApprovalRequired  *bool           `json:"approval_required"`
	FundingAuthorized *bool           `json:"funding_authorized"`
	RequiresRecheck   *bool           `json:"requires_recheck"`
	PrincipalCents    *int64          `json:"principal_cents"`
	TermDays          *int            `json:"term_days"`
	EstimatedFeeCents *int64          `json:"estimated_fee_cents"`
	TotalDueCents     *int64          `json:"total_due_cents"`
	ReasonCodes       []string        `json:"reason_codes"`
	EvaluatedAt       time.Time       `json:"evaluated_at"`
	CreatedAt         time.Time       `json:"created_at"`
	Rules             []RuleTrace     `json:"rules"`
	Raw               json.RawMessage `json:"-"`
}

// CustomerContext preserves the verified normalized context envelope for the
// funding-owned adapter to decode into the public Go policy types.
type CustomerContext struct {
	SchemaVersion   int             `json:"schema_version"`
	SourceMode      string          `json:"source_mode"`
	CustomerID      string          `json:"customer_id"`
	SnapshotID      string          `json:"snapshot_id"`
	SnapshotVersion int64           `json:"snapshot_version"`
	CapturedAt      time.Time       `json:"captured_at"`
	PolicyID        string          `json:"policy_id"`
	PolicyHash      string          `json:"policy_hash"`
	Fresh           bool            `json:"fresh"`
	Coverage        json.RawMessage `json:"coverage"`
	Data            json.RawMessage `json:"data"`
	SourceRefs      json.RawMessage `json:"source_refs"`
	Raw             json.RawMessage `json:"-"`
}

// NewUnderwritingClient validates the endpoint and constructs a bounded client.
func NewUnderwritingClient(baseURL, secret string) (*UnderwritingClient, error) {
	return NewUnderwritingClientWithClock(baseURL, secret, time.Now)
}

// NewUnderwritingClientWithClock injects time for deterministic caller fixtures.
func NewUnderwritingClientWithClock(baseURL, secret string, now func() time.Time) (*UnderwritingClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(secret) < 32 {
		return nil, errors.New("underwriting_client_configuration_invalid")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("underwriting_url_requires_https")
	}
	if now == nil {
		now = time.Now
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &UnderwritingClient{baseURL: strings.TrimRight(u.String(), "/"), secret: []byte(secret), http: client, now: now}, nil
}

// ReadEvaluation retrieves one saved assessment without reevaluating it.
func (c *UnderwritingClient) ReadEvaluation(ctx context.Context, id, mode string) (SavedEvaluation, error) {
	if id == "" {
		return SavedEvaluation{}, errors.New("underwriting_evaluation_id_invalid")
	}
	path := "/api/underwriting/evaluations/" + url.PathEscape(id)
	raw, err := c.get(ctx, path)
	if err != nil {
		return SavedEvaluation{}, err
	}
	var value SavedEvaluation
	if err = json.Unmarshal(raw, &value); err != nil || value.ID != id || value.ShortfallID == "" || value.SourceMode != mode || !validMode(mode) || value.CustomerID == "" || value.AccountID == "" || value.TransactionID == "" || value.PolicyID == "" || !lowerHash(value.PolicyHash) || (value.Status != "ELIGIBLE" && value.Status != "INELIGIBLE" && value.Status != "REVIEW_REQUIRED") || value.FundingAuthorized == nil || *value.FundingAuthorized || value.RequiresRecheck == nil || !*value.RequiresRecheck || value.ApprovalRequired == nil || value.EvaluatedAt.IsZero() || value.CreatedAt.IsZero() || value.ReasonCodes == nil || !validRules(value.Rules) || !validEvaluationMoney(value) {
		return SavedEvaluation{}, errors.New("underwriting_evaluation_response_invalid")
	}
	value.Raw = append(json.RawMessage(nil), raw...)
	return value, nil
}

// FindEvaluation returns the unique saved assessment matching the prepared
// customer, shortfall and term. It never creates or reevaluates an assessment.
func (c *UnderwritingClient) FindEvaluation(ctx context.Context, customerID, shortfallID string, termDays int, mode string) (SavedEvaluation, error) {
	if customerID == "" || shortfallID == "" || termDays < 7 || termDays > 90 || !validMode(mode) {
		return SavedEvaluation{}, errors.New("underwriting_evaluation_query_invalid")
	}
	type listPage struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	type listIdentity struct {
		ID          string `json:"id"`
		CustomerID  string `json:"customer_id"`
		ShortfallID string `json:"shortfall_id"`
		TermDays    *int   `json:"term_days"`
	}
	ids := make(map[string]struct{})
	cursor := ""
	for pageNumber := 0; pageNumber < 40; pageNumber++ {
		query := url.Values{"customer_id": {customerID}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		raw, err := c.get(ctx, "/api/underwriting/evaluations?"+query.Encode())
		if err != nil {
			return SavedEvaluation{}, err
		}
		var page listPage
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&page) != nil || decoder.Decode(new(any)) != io.EOF || page.Items == nil {
			return SavedEvaluation{}, errors.New("underwriting_evaluation_list_invalid")
		}
		for _, item := range page.Items {
			var identity listIdentity
			if json.Unmarshal(item, &identity) != nil || identity.ID == "" || identity.CustomerID != customerID {
				return SavedEvaluation{}, errors.New("underwriting_evaluation_list_invalid")
			}
			if identity.ShortfallID == shortfallID && identity.TermDays != nil && *identity.TermDays == termDays {
				ids[identity.ID] = struct{}{}
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		if pageNumber == 39 {
			return SavedEvaluation{}, errors.New("underwriting_evaluation_list_limit_reached")
		}
		cursor = *page.NextCursor
	}
	if len(ids) != 1 {
		return SavedEvaluation{}, errors.New("underwriting_evaluation_match_not_unique")
	}
	for id := range ids {
		return c.ReadEvaluation(ctx, id, mode)
	}
	return SavedEvaluation{}, errors.New("underwriting_evaluation_match_not_unique")
}

// ReadCustomerContext retrieves normalized facts; freshness and source ownership
// remain explicit and no policy decision is computed here.
func (c *UnderwritingClient) ReadCustomerContext(ctx context.Context, customerID, mode string) (CustomerContext, error) {
	if customerID == "" {
		return CustomerContext{}, errors.New("underwriting_customer_id_invalid")
	}
	path := "/api/underwriting/customers/" + url.PathEscape(customerID) + "/context"
	raw, err := c.get(ctx, path)
	if err != nil {
		return CustomerContext{}, err
	}
	var value CustomerContext
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil || decoder.Decode(new(any)) != io.EOF || value.SchemaVersion != 1 || value.SourceMode != mode || !validMode(mode) || value.CustomerID != customerID || value.SnapshotID == "" || value.SnapshotVersion < 1 || value.PolicyID == "" || !lowerHash(value.PolicyHash) || !validContextSourceRefs(value.SourceRefs) || !isJSONObject(value.Data) || !isJSONObject(value.Coverage) || value.CapturedAt.IsZero() || !isUTC(value.CapturedAt) || value.CapturedAt.After(c.now().UTC().Add(5*time.Second)) {
		return CustomerContext{}, errors.New("underwriting_context_response_invalid")
	}
	age := c.now().UTC().Sub(value.CapturedAt.UTC())
	value.Fresh = value.Fresh && age >= -5*time.Second && age <= 24*time.Hour
	value.Raw = append(json.RawMessage(nil), raw...)
	return value, nil
}

func (c *UnderwritingClient) get(parent context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	token, err := c.assertion()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, errors.New("underwriting_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("underwriting_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("underwriting_resource_not_found")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("underwriting_auth_rejected")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("underwriting_unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUnderwritingResponse+1))
	if err != nil || len(body) > maxUnderwritingResponse || !json.Valid(body) {
		return nil, errors.New("underwriting_response_invalid")
	}
	return body, nil
}

func (c *UnderwritingClient) assertion() (string, error) {
	now := c.now().UTC()
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", errors.New("underwriting_assertion_failed")
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": "flowcard-node-proxy", "aud": "flowcard-underwriting-service", "sub": "underwriting-evaluator", "jti": base64.RawURLEncoding.EncodeToString(jti), "iat": now.Unix(), "exp": now.Add(30 * time.Second).Unix()})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// MarshalContextData is a safe typed JSON helper for logging metadata only.
func MarshalContextData(value CustomerContext) ([]byte, error) {
	return json.Marshal(struct {
		Mode            string `json:"source_mode"`
		CustomerID      string `json:"customer_id"`
		SnapshotVersion int64  `json:"snapshot_version"`
		Fresh           bool   `json:"fresh"`
	}{value.SourceMode, value.CustomerID, value.SnapshotVersion, value.Fresh})
}

func validMode(mode string) bool { return mode == "DEMO" || mode == "SANDBOX" }
func lowerHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
func isJSONObject(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &obj) == nil && obj != nil
}
func validRules(rules []RuleTrace) bool {
	if rules == nil || len(rules) > 100 {
		return false
	}
	for _, r := range rules {
		thresholdValid := isJSONObject(r.Threshold) || (r.Status == "UNKNOWN" && isJSONNull(r.Threshold))
		if r.RuleID == "" || (r.Status != "PASS" && r.Status != "FAIL" && r.Status != "UNKNOWN") || r.ReasonCode == "" || !isJSONObject(r.Actual) || !thresholdValid || r.SourceRefs == nil {
			return false
		}
		for _, ref := range r.SourceRefs {
			if ref.System == "" || ref.RecordID == "" || ref.Version == "" {
				return false
			}
		}
	}
	return true
}
func isJSONNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

type contextSourceRef struct {
	System      string    `json:"system"`
	RecordID    string    `json:"record_id"`
	Version     string    `json:"version"`
	EffectiveAt time.Time `json:"effective_at"`
}

func validContextSourceRefs(raw json.RawMessage) bool {
	var refs []contextSourceRef
	if len(raw) == 0 || json.Unmarshal(raw, &refs) != nil || len(refs) == 0 || len(refs) > 10000 {
		return false
	}
	for _, ref := range refs {
		if ref.System == "" || ref.RecordID == "" || ref.Version == "" || ref.EffectiveAt.IsZero() || !isUTC(ref.EffectiveAt) {
			return false
		}
	}
	return true
}

func validEvaluationMoney(v SavedEvaluation) bool {
	if v.PrincipalCents != nil && *v.PrincipalCents < 0 {
		return false
	}
	if v.EstimatedFeeCents != nil && *v.EstimatedFeeCents < 0 {
		return false
	}
	if v.TotalDueCents != nil && *v.TotalDueCents < 0 {
		return false
	}
	if v.TermDays != nil && (*v.TermDays < 7 || *v.TermDays > 90) {
		return false
	}
	return true
}
func isUTC(t time.Time) bool { _, offset := t.Zone(); return offset == 0 }
