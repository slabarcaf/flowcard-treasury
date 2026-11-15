// Package underwriting implements the coordinator's narrow saved-evaluation client.
package underwriting

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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
)

const (
	maxResponseBytes = 256 << 10
	requestIssuer    = "flowcard-node-proxy"
	requestAudience  = "flowcard-underwriting-service"
	requestSubject   = "underwriting-evaluator"
)

type UpstreamError struct {
	Status int
	Retry  time.Duration
}

func (e *UpstreamError) Error() string             { return fmt.Sprintf("underwriting_status_%d", e.Status) }
func (e *UpstreamError) RetryAfter() time.Duration { return e.Retry }
func (e *UpstreamError) HTTPStatus() int           { return e.Status }

type ContractError struct{ Code string }

func (e *ContractError) Error() string   { return e.Code }
func (e *ContractError) Permanent() bool { return true }

type requestBody struct {
	ShortfallID string `json:"shortfall_id"`
	TermDays    int    `json:"term_days"`
}

type Rule struct {
	ID         string                      `json:"rule_id"`
	Status     string                      `json:"status"`
	ReasonCode string                      `json:"reason_code"`
	Actual     json.RawMessage             `json:"actual"`
	Threshold  json.RawMessage             `json:"threshold"`
	SourceRefs []customercontext.SourceRef `json:"source_refs"`
}

type Evaluation struct {
	ID                       string    `json:"id"`
	ShortfallID              string    `json:"shortfall_id"`
	CustomerID               string    `json:"customer_id"`
	AccountID                string    `json:"account_id"`
	TransactionID            string    `json:"transaction_id"`
	SourceMode               string    `json:"source_mode"`
	EvaluatedAt              time.Time `json:"evaluated_at"`
	CreatedAt                time.Time `json:"created_at"`
	PolicyID                 string    `json:"policy_id"`
	PolicyHash               string    `json:"policy_hash"`
	CustomerSnapshotID       *string   `json:"customer_snapshot_id"`
	CustomerSnapshotVersion  *int64    `json:"customer_snapshot_version"`
	FacilitySnapshotID       *string   `json:"facility_snapshot_id"`
	FacilitySnapshotVersion  *int64    `json:"facility_snapshot_version"`
	Status                   string    `json:"status"`
	ApprovalRequired         bool      `json:"approval_required"`
	FundingAuthorized        bool      `json:"funding_authorized"`
	RequiresRecheck          bool      `json:"requires_recheck"`
	Tier                     *string   `json:"tier"`
	CustomerLimitCents       *int64    `json:"customer_limit_cents"`
	ExistingExposureCents    *int64    `json:"existing_exposure_cents"`
	PostFundingExposureCents *int64    `json:"post_funding_exposure_cents"`
	AvailableCapitalCents    *int64    `json:"available_capital_cents"`
	PrincipalCents           *int64    `json:"principal_cents"`
	TermDays                 int       `json:"term_days"`
	EstimatedFeeCents        *int64    `json:"estimated_fee_cents"`
	TotalDueCents            *int64    `json:"total_due_cents"`
	Rules                    []Rule    `json:"rules"`
	ReasonCodes              []string  `json:"reason_codes"`
}

type Linkage struct {
	ShortfallID   string
	CustomerID    string
	AccountID     string
	TransactionID string
	SourceMode    string
	TermDays      int
}

type Client struct {
	base   *url.URL
	secret []byte
	mode   string
	http   *http.Client
	now    func() time.Time
}

// Readiness checks the existing side-effect-free service readiness route.
func (c *Client) Readiness(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/readyz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, &ContractError{Code: "UNDERWRITING_READINESS_REQUEST_INVALID"}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, errors.New("underwriting_unavailable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil || len(body) >= 16<<10 {
		return false, &ContractError{Code: "UNDERWRITING_READINESS_RESPONSE_INVALID"}
	}
	var result struct {
		Ready                     bool   `json:"ready"`
		SourceMode                string `json:"source_mode"`
		DatabaseReady             bool   `json:"database_ready"`
		PolicyReady               bool   `json:"policy_ready"`
		ShortfallClientConfigured bool   `json:"shortfall_client_configured"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable) {
		return false, &ContractError{Code: "UNDERWRITING_READINESS_RESPONSE_INVALID"}
	}
	return resp.StatusCode == http.StatusOK && result.Ready && result.SourceMode == c.mode && result.DatabaseReady && result.PolicyReady && result.ShortfallClientConfigured, nil
}

func New(baseURL, secret, mode string, httpClient *http.Client) (*Client, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, &ContractError{Code: "UNDERWRITING_ENDPOINT_INVALID"}
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, &ContractError{Code: "UNDERWRITING_ENDPOINT_REQUIRES_TLS"}
		}
	}
	if len(secret) < 32 || (mode != "DEMO" && mode != "SANDBOX") {
		return nil, &ContractError{Code: "UNDERWRITING_AUTH_CONFIGURATION_INVALID"}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	c := *httpClient
	c.Timeout = 5 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, secret: []byte(secret), mode: mode, http: &c, now: time.Now}, nil
}

// Evaluate submits the fixed typed evaluation request under its stable key.
// EvaluationRequestBody returns the exact typed request persisted before POST.
func EvaluationRequestBody(shortfallID string, termDays int) ([]byte, error) {
	if shortfallID == "" || termDays < 7 || termDays > 90 {
		return nil, &ContractError{Code: "UNDERWRITING_REQUEST_INVALID"}
	}
	body, err := json.Marshal(requestBody{ShortfallID: shortfallID, TermDays: termDays})
	if err != nil {
		return nil, &ContractError{Code: "UNDERWRITING_REQUEST_ENCODE_FAILED"}
	}
	return body, nil
}

// EvaluateBody submits exactly the request bytes already persisted locally.
func (c *Client) EvaluateBody(ctx context.Context, body []byte, idempotencyKey string, expected Linkage) (Evaluation, []byte, int, error) {
	var result Evaluation
	if !validIdempotencyKey(idempotencyKey) || expected.SourceMode != c.mode || expected.TermDays < 7 || expected.TermDays > 90 {
		return result, nil, 0, &ContractError{Code: "UNDERWRITING_REQUEST_INVALID"}
	}
	var request requestBody
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.ShortfallID != expected.ShortfallID || request.TermDays != expected.TermDays {
		return result, nil, 0, &ContractError{Code: "UNDERWRITING_REQUEST_INVALID"}
	}
	target := c.endpoint("/api/underwriting/evaluations")
	status, response, err := c.do(ctx, http.MethodPost, target, idempotencyKey, body, &result)
	if err != nil {
		return Evaluation{}, nil, status, err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return Evaluation{}, nil, status, &ContractError{Code: "UNDERWRITING_STATUS_INVALID"}
	}
	if err := ValidateEvaluation(result, expected); err != nil {
		return Evaluation{}, nil, status, err
	}
	return result, response, status, nil
}

// Read fetches a known saved evaluation without reevaluating its request.
func (c *Client) Read(ctx context.Context, id string, expected Linkage) (Evaluation, []byte, int, error) {
	var result Evaluation
	if id == "" || len(id) > 128 {
		return result, nil, 0, &ContractError{Code: "UNDERWRITING_EVALUATION_ID_INVALID"}
	}
	target := c.endpoint("/api/underwriting/evaluations/" + url.PathEscape(id))
	status, response, err := c.do(ctx, http.MethodGet, target, "", nil, &result)
	if err != nil {
		return Evaluation{}, nil, status, err
	}
	if status != http.StatusOK {
		return Evaluation{}, nil, status, &ContractError{Code: "UNDERWRITING_STATUS_INVALID"}
	}
	if err := ValidateEvaluation(result, expected); err != nil {
		return Evaluation{}, nil, status, err
	}
	if result.ID != id {
		return Evaluation{}, nil, status, &ContractError{Code: "UNDERWRITING_EVALUATION_LINKAGE_INVALID"}
	}
	return result, response, status, nil
}

func (c *Client) endpoint(path string) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
}

func (c *Client) do(ctx context.Context, method, target, idempotencyKey string, body []byte, out any) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.assertion(c.now())
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, &ContractError{Code: "UNDERWRITING_REQUEST_INVALID"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errors.New("underwriting_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil, &UpstreamError{Status: resp.StatusCode, Retry: parseRetryAfter(resp.Header.Get("Retry-After"), c.now())}
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(response) > maxResponseBytes {
		return resp.StatusCode, nil, &ContractError{Code: "UNDERWRITING_RESPONSE_INVALID"}
	}
	if err := requireEvaluationFields(response); err != nil {
		return resp.StatusCode, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return resp.StatusCode, nil, &ContractError{Code: "UNDERWRITING_RESPONSE_INVALID"}
	}
	return resp.StatusCode, response, nil
}

func requireEvaluationFields(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return &ContractError{Code: "UNDERWRITING_RESPONSE_INVALID"}
	}
	required := []string{"id", "shortfall_id", "customer_id", "account_id", "transaction_id", "source_mode", "evaluated_at", "created_at", "policy_id", "policy_hash", "customer_snapshot_id", "customer_snapshot_version", "facility_snapshot_id", "facility_snapshot_version", "status", "approval_required", "funding_authorized", "requires_recheck", "tier", "customer_limit_cents", "existing_exposure_cents", "post_funding_exposure_cents", "available_capital_cents", "principal_cents", "term_days", "estimated_fee_cents", "total_due_cents", "rules", "reason_codes"}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return &ContractError{Code: "UNDERWRITING_RESPONSE_FIELD_MISSING"}
		}
	}
	var rules []json.RawMessage
	if json.Unmarshal(fields["rules"], &rules) != nil || len(rules) != 10 {
		return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
	}
	for _, rawRule := range rules {
		var rule map[string]json.RawMessage
		if json.Unmarshal(rawRule, &rule) != nil || rule == nil {
			return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
		}
		for _, name := range []string{"rule_id", "status", "reason_code", "actual", "threshold", "source_refs"} {
			if _, ok := rule[name]; !ok {
				return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
			}
		}
		for _, name := range []string{"actual", "threshold"} {
			var object map[string]json.RawMessage
			if json.Unmarshal(rule[name], &object) != nil || object == nil {
				return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
			}
		}
		var refs []json.RawMessage
		if json.Unmarshal(rule["source_refs"], &refs) != nil || refs == nil {
			return &ContractError{Code: "UNDERWRITING_SOURCE_REF_INVALID"}
		}
		for _, rawRef := range refs {
			var ref map[string]json.RawMessage
			if json.Unmarshal(rawRef, &ref) != nil || ref == nil {
				return &ContractError{Code: "UNDERWRITING_SOURCE_REF_INVALID"}
			}
			for _, name := range []string{"system", "record_id", "version", "effective_at"} {
				if _, ok := ref[name]; !ok {
					return &ContractError{Code: "UNDERWRITING_SOURCE_REF_INVALID"}
				}
			}
		}
	}
	var reasonCodes []string
	if json.Unmarshal(fields["reason_codes"], &reasonCodes) != nil || reasonCodes == nil {
		return &ContractError{Code: "UNDERWRITING_REASON_CODES_INVALID"}
	}
	return nil
}

// DecodeEvaluation strictly decodes one saved service result without editing its values.
func DecodeEvaluation(raw []byte) (Evaluation, error) {
	var result Evaluation
	if err := requireEvaluationFields(raw); err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return Evaluation{}, &ContractError{Code: "UNDERWRITING_RESPONSE_INVALID"}
	}
	return result, nil
}

// ValidateEvaluation checks an immutable response against the caller's saved case linkage.
func ValidateEvaluation(e Evaluation, expected Linkage) error {
	if !validReference(e.ID, 128) || e.ShortfallID != expected.ShortfallID || e.SourceMode != expected.SourceMode || e.CustomerID == "" || expected.CustomerID != "" && e.CustomerID != expected.CustomerID || e.AccountID == "" || expected.AccountID != "" && e.AccountID != expected.AccountID || e.TransactionID == "" || expected.TransactionID != "" && e.TransactionID != expected.TransactionID || e.PolicyID == "" || !validHash(e.PolicyHash) || e.EvaluatedAt.IsZero() || e.CreatedAt.IsZero() || e.TermDays != expected.TermDays || e.FundingAuthorized || !e.RequiresRecheck {
		return &ContractError{Code: "UNDERWRITING_RESPONSE_LINKAGE_INVALID"}
	}
	if (e.Status != "ELIGIBLE" && e.Status != "INELIGIBLE" && e.Status != "REVIEW_REQUIRED") || e.ApprovalRequired && e.Status == "ELIGIBLE" {
		return &ContractError{Code: "UNDERWRITING_STATUS_INVALID"}
	}
	if !pairedSnapshot(e.CustomerSnapshotID, e.CustomerSnapshotVersion) || !pairedSnapshot(e.FacilitySnapshotID, e.FacilitySnapshotVersion) {
		return &ContractError{Code: "UNDERWRITING_SNAPSHOT_LINKAGE_INVALID"}
	}
	for _, amount := range []*int64{e.CustomerLimitCents, e.ExistingExposureCents, e.PostFundingExposureCents, e.AvailableCapitalCents, e.PrincipalCents, e.EstimatedFeeCents, e.TotalDueCents} {
		if amount != nil && *amount < 0 {
			return &ContractError{Code: "UNDERWRITING_MONEY_INVALID"}
		}
	}
	if len(e.Rules) != 10 {
		return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
	}
	seen := make(map[string]bool, len(e.Rules))
	allowedReasons := map[string]bool{"HUMAN_APPROVAL_REQUIRED": true, "SHORTFALL_STALE": true}
	expectedReasons := map[string]bool{}
	for _, rule := range e.Rules {
		if !validReference(rule.ID, 128) || seen[rule.ID] || rule.Actual == nil || !isJSONObject(rule.Actual) || rule.Threshold == nil || !isJSONObject(rule.Threshold) || rule.SourceRefs == nil {
			return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
		}
		seen[rule.ID] = true
		suffix := "_UNKNOWN"
		switch rule.Status {
		case "PASS":
			suffix = "_PASSED"
		case "FAIL":
			suffix = "_FAILED"
		case "UNKNOWN":
		default:
			return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
		}
		if rule.ReasonCode != rule.ID+suffix {
			return &ContractError{Code: "UNDERWRITING_RULES_INVALID"}
		}
		if rule.Status != "PASS" {
			allowedReasons[rule.ReasonCode] = true
			expectedReasons[rule.ReasonCode] = true
		}
		for _, ref := range rule.SourceRefs {
			if ref.System == "" || ref.RecordID == "" || ref.Version == "" {
				return &ContractError{Code: "UNDERWRITING_SOURCE_REF_INVALID"}
			}
		}
	}
	for _, code := range e.ReasonCodes {
		if !allowedReasons[code] {
			return &ContractError{Code: "UNDERWRITING_REASON_CODE_INVALID"}
		}
		delete(expectedReasons, code)
	}
	if len(expectedReasons) != 0 || e.ApprovalRequired && !containsReason(e.ReasonCodes, "HUMAN_APPROVAL_REQUIRED") {
		return &ContractError{Code: "UNDERWRITING_REASON_CODE_INVALID"}
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func containsReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}

func pairedSnapshot(id *string, version *int64) bool {
	if (id == nil) != (version == nil) {
		return false
	}
	return version == nil || *version > 0
}

func validHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validReference(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func validIdempotencyKey(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'A' && char <= 'Z') && !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && !strings.ContainsRune("._:-", char) {
			return false
		}
	}
	return true
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		maxSeconds := uint64((time.Duration(1<<63 - 1)) / time.Second)
		if seconds > maxSeconds {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	if decimalDigits(value) {
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
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (c *Client) assertion(now time.Time) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", &ContractError{Code: "UNDERWRITING_ASSERTION_FAILED"}
	}
	head, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims := struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		JWTID     string `json:"jti"`
	}{requestIssuer, requestAudience, requestSubject, now.Unix(), now.Add(20 * time.Second).Unix(), hex.EncodeToString(id[:])}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", &ContractError{Code: "UNDERWRITING_ASSERTION_FAILED"}
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString(head) + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
