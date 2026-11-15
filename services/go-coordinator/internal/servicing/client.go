// Package servicing implements the bounded read-only servicing HTTP boundary.
package servicing

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

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

const maxResponseBytes = 256 << 10

type Change struct {
	Sequence          uint64          `json:"sequence"`
	ResourceType      string          `json:"resource_type"`
	ResourceID        string          `json:"resource_id"`
	ResourceVersion   int64           `json:"resource_version"`
	ChangeType        string          `json:"change_type"`
	RecordedAt        time.Time       `json:"recorded_at"`
	Summary           json.RawMessage `json:"summary"`
	CustomerID        *string         `json:"customer_id"`
	AccountRef        *string         `json:"account_ref"`
	BridgeID          *string         `json:"bridge_id"`
	BridgeVersion     *int64          `json:"bridge_version"`
	OriginatingCaseID *string         `json:"originating_case_id"`
	EvaluationID      *string         `json:"evaluation_id"`
	OfferID           *string         `json:"offer_id"`
}
type ChangePage struct {
	Mode         string   `json:"mode"`
	Items        []Change `json:"items"`
	NextAfterSeq uint64   `json:"next_after_seq"`
	HasMore      bool     `json:"has_more"`
}
type Bridge struct {
	ObligationID              string      `json:"obligation_id"`
	ID                        string      `json:"id"`
	Mode                      string      `json:"mode"`
	SourceBindingID           string      `json:"source_binding_id"`
	CustomerID                string      `json:"customer_id"`
	AccountRef                string      `json:"account_ref"`
	Version                   int64       `json:"version"`
	AsOf                      time.Time   `json:"as_of"`
	OriginatingCaseID         *string     `json:"originating_case_id"`
	EvaluationID              *string     `json:"evaluation_id"`
	OfferID                   *string     `json:"offer_id"`
	TermsHash                 *string     `json:"terms_hash"`
	Currency                  string      `json:"currency"`
	PrincipalOwedCents        *int64      `json:"principal_owed_cents"`
	ContractFeeOwedCents      *int64      `json:"contract_fee_owed_cents"`
	LateFeeOwedCents          *int64      `json:"late_fee_owed_cents"`
	DueOn                     *string     `json:"due_on"`
	FundedAt                  *time.Time  `json:"funded_at"`
	Lifecycle                 string      `json:"lifecycle"`
	Delinquency               string      `json:"delinquency"`
	DaysPastDue               *int        `json:"days_past_due"`
	HoldReasons               []string    `json:"hold_reasons"`
	LastAttemptID             *string     `json:"last_attempt_id"`
	LastAttemptState          *string     `json:"last_attempt_state"`
	LastProviderObservedAt    *time.Time  `json:"last_provider_observed_at"`
	NextEligibleRetryAt       *time.Time  `json:"next_eligible_retry_at"`
	AccrualCurrent            *bool       `json:"accrual_current"`
	AccrualCurrentThrough     *string     `json:"accrual_current_through"`
	DebtAsOf                  *time.Time  `json:"debt_as_of"`
	AllowedNextActions        []string    `json:"allowed_next_actions"`
	AcceptanceStatus          *string     `json:"acceptance_status"`
	ApprovalStatus            *string     `json:"approval_status"`
	CollectionAuthorityStatus *string     `json:"collection_authority_status"`
	AcceptanceReason          *string     `json:"acceptance_reason"`
	ApprovalReason            *string     `json:"approval_reason"`
	CollectionAuthorityReason *string     `json:"collection_authority_reason"`
	Operations                []Operation `json:"operations"`
}
type CollectionPage struct {
	BridgeVersion int64             `json:"bridge_version"`
	Items         []json.RawMessage `json:"items"`
	NextCursor    *string           `json:"next_cursor"`
}
type Operation struct {
	ID                  string     `json:"id"`
	Version             int64      `json:"version"`
	Kind                string     `json:"kind"`
	State               string     `json:"state"`
	ReservedAmountCents *int64     `json:"reserved_amount_cents"`
	SettlementReference *string    `json:"last_verified_settlement_reference"`
	RetryCount          int        `json:"retry_count"`
	NextCheck           *time.Time `json:"next_check"`
	HoldReason          *string    `json:"hold_reason"`
}
type UpstreamError struct {
	Status int
	Retry  time.Duration
}

func (e *UpstreamError) Error() string             { return fmt.Sprintf("servicing_status_%d", e.Status) }
func (e *UpstreamError) RetryAfter() time.Duration { return e.Retry }
func (e *UpstreamError) HTTPStatus() int           { return e.Status }

var ErrVersionChanged = errors.New("servicing_version_changed")

type ContractError struct{ Code string }

func (e *ContractError) Error() string   { return e.Code }
func (e *ContractError) Permanent() bool { return true }
func contractError(code string) error    { return &ContractError{Code: code} }

type Client struct {
	base          *url.URL
	secret        []byte
	mode, binding string
	http          *http.Client
	now           func() time.Time
}

func New(baseURL, secret, mode, binding string, httpClient *http.Client) (*Client, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, contractError("servicing_endpoint_invalid")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, contractError("servicing_endpoint_requires_tls")
		}
	}
	if len(secret) < 32 {
		return nil, contractError("servicing_secret_invalid")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	c := *httpClient
	c.Timeout = 5 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, secret: []byte(secret), mode: mode, binding: binding, http: &c, now: time.Now}, nil
}

func (c *Client) Changes(ctx context.Context, after uint64) (ChangePage, int, error) {
	var p ChangePage
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/servicing/changes"
	q := u.Query()
	q.Set("after_seq", fmt.Sprint(after))
	u.RawQuery = q.Encode()
	status, err := c.get(ctx, u.String(), &p)
	if err != nil {
		return p, status, err
	}
	if p.Mode != c.mode || p.Items == nil || len(p.Items) > 25 {
		return p, status, contractError("servicing_page_scope_invalid")
	}
	prev := after
	for _, it := range p.Items {
		if it.Sequence <= prev || it.Sequence != prev+1 || it.ResourceVersion < 0 || it.RecordedAt.IsZero() {
			return p, status, contractError("servicing_sequence_invalid")
		}
		prev = it.Sequence
		if it.ResourceID == "" || it.ChangeType == "" || it.Summary == nil || !jsonObject(it.Summary) {
			return p, status, contractError("servicing_change_invalid")
		}
		if it.ResourceType != "OFFER" && it.ResourceType != "BRIDGE" && it.ResourceType != "OPERATION" {
			return p, status, contractError("servicing_resource_type_invalid")
		}
		if (it.BridgeID == nil) != (it.BridgeVersion == nil) {
			return p, status, contractError("servicing_parent_version_invalid")
		}
		if it.CustomerID == nil || strings.TrimSpace(*it.CustomerID) == "" || it.AccountRef == nil || strings.TrimSpace(*it.AccountRef) == "" {
			return p, status, contractError("servicing_scope_invalid")
		}
		if *it.AccountRef != domain.InternalAccountRef(*it.CustomerID) {
			return p, status, contractError("servicing_account_alias_invalid")
		}
		if it.ResourceType != "OFFER" && it.BridgeID == nil {
			return p, status, contractError("servicing_parent_bridge_required")
		}
		if it.ResourceType == "BRIDGE" && (*it.BridgeID != it.ResourceID || *it.BridgeVersion != it.ResourceVersion) {
			return p, status, contractError("servicing_bridge_version_invalid")
		}
	}
	if len(p.Items) == 0 && p.NextAfterSeq != after {
		return p, status, contractError("servicing_empty_checkpoint_invalid")
	}
	if len(p.Items) > 0 && p.NextAfterSeq != prev {
		return p, status, contractError("servicing_checkpoint_invalid")
	}
	if p.HasMore && len(p.Items) == 0 {
		return p, status, contractError("servicing_empty_more_invalid")
	}
	return p, status, nil
}

func (c *Client) ReadBridge(ctx context.Context, id string) (Bridge, int, error) {
	var b Bridge
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/bridges/" + url.PathEscape(id)
	status, err := c.get(ctx, u.String(), &b)
	if err != nil {
		return b, status, err
	}
	if b.ID != id || b.ObligationID != id || b.Mode != c.mode || b.SourceBindingID != c.binding || b.CustomerID == "" || b.AccountRef != domain.InternalAccountRef(b.CustomerID) || b.Version < 0 || b.AsOf.IsZero() || b.Currency != "USD" || b.Lifecycle == "" || len(b.Operations) > 25 {
		return Bridge{}, status, contractError("servicing_bridge_scope_invalid")
	}
	if !knownLifecycle(b.Lifecycle) || ((b.LastAttemptID == nil) != (b.LastAttemptState == nil)) || (b.DaysPastDue != nil && *b.DaysPastDue < 0) {
		return Bridge{}, status, contractError("servicing_bridge_state_invalid")
	}
	if (b.PrincipalOwedCents != nil && *b.PrincipalOwedCents < 0) || (b.ContractFeeOwedCents != nil && *b.ContractFeeOwedCents < 0) || (b.LateFeeOwedCents != nil && *b.LateFeeOwedCents < 0) {
		return Bridge{}, status, contractError("servicing_debt_invalid")
	}
	if b.DueOn != nil {
		if _, err := time.Parse("2006-01-02", *b.DueOn); err != nil {
			return Bridge{}, status, contractError("servicing_due_date_invalid")
		}
	}
	if b.AccrualCurrentThrough != nil {
		if _, err := time.Parse("2006-01-02", *b.AccrualCurrentThrough); err != nil {
			return Bridge{}, status, contractError("servicing_accrual_date_invalid")
		}
	}
	for _, reason := range b.HoldReasons {
		if strings.TrimSpace(reason) == "" {
			return Bridge{}, status, contractError("servicing_hold_reason_invalid")
		}
	}
	for _, op := range b.Operations {
		if !validOperation(op) {
			return Bridge{}, status, contractError("servicing_operation_invalid")
		}
	}
	for _, value := range []*string{b.AcceptanceStatus, b.ApprovalStatus, b.CollectionAuthorityStatus} {
		if value != nil && !knownAuthorityStatus(*value) {
			return Bridge{}, status, contractError("servicing_authority_status_invalid")
		}
	}
	if b.LastAttemptState != nil && !knownOperationState(*b.LastAttemptState) {
		return Bridge{}, status, contractError("servicing_attempt_state_invalid")
	}
	return b, status, nil
}
func (c *Client) Collections(ctx context.Context, id string, version int64, cursor *string) (CollectionPage, int, error) {
	var p CollectionPage
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/bridges/" + url.PathEscape(id) + "/collections"
	q := u.Query()
	if cursor != nil {
		q.Set("cursor", *cursor)
	}
	u.RawQuery = q.Encode()
	status, err := c.get(ctx, u.String(), &p)
	if err != nil {
		return p, status, err
	}
	if p.BridgeVersion != version {
		return p, status, ErrVersionChanged
	}
	if p.Items == nil || len(p.Items) > 25 || (cursor != nil && p.NextCursor != nil && *cursor == *p.NextCursor) {
		return p, status, contractError("servicing_collection_cursor_invalid")
	}
	return p, status, nil
}
func (c *Client) ReadOperation(ctx context.Context, id string) (Operation, int, error) {
	var op Operation
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/operations/" + url.PathEscape(id)
	status, err := c.get(ctx, u.String(), &op)
	if err != nil {
		return op, status, err
	}
	if op.ID != id || !validOperation(op) {
		return Operation{}, status, contractError("servicing_operation_invalid")
	}
	return op, status, nil
}
func validOperation(op Operation) bool {
	return op.ID != "" && op.Version > 0 && validOperationKind(op.Kind) && knownOperationState(op.State) && op.RetryCount >= 0 && (op.ReservedAmountCents == nil || *op.ReservedAmountCents >= 0)
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
		return 0, contractError("servicing_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("servicing_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := time.Duration(0)
		if n, parseErr := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); parseErr == nil && n > 0 && n <= int64((time.Duration(1<<63-1))/time.Second) {
			retry = time.Duration(n) * time.Second
		} else if at, parseErr := http.ParseTime(resp.Header.Get("Retry-After")); parseErr == nil && at.After(c.now()) {
			retry = at.Sub(c.now())
		}
		return resp.StatusCode, &UpstreamError{Status: resp.StatusCode, Retry: retry}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return resp.StatusCode, contractError("servicing_response_invalid")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil || dec.Decode(new(any)) != io.EOF {
		return resp.StatusCode, contractError("servicing_response_invalid")
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
	}{"flowcard-go", "flowcard-funding-collection", "coordinator-reader", now.Unix(), now.Add(20 * time.Second).Unix(), fmt.Sprintf("coordinator-%d", now.UnixNano())}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", contractError("servicing_assertion_failed")
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString(head) + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}

func (b *Bridge) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return contractError("servicing_bridge_json_invalid")
	}
	for _, key := range []string{"id", "obligation_id", "mode", "source_binding_id", "customer_id", "account_ref", "version", "as_of", "currency", "lifecycle", "hold_reasons", "allowed_next_actions", "operations", "accrual_current"} {
		if _, ok := fields[key]; !ok {
			return contractError("servicing_bridge_field_missing")
		}
	}
	for _, key := range []string{"hold_reasons", "allowed_next_actions", "operations"} {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return contractError("servicing_bridge_field_null")
		}
	}
	type alias Bridge
	var value alias
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return contractError("servicing_bridge_json_invalid")
	}
	*b = Bridge(value)
	return nil
}

func (p *CollectionPage) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return contractError("servicing_collection_json_invalid")
	}
	for _, key := range []string{"bridge_version", "items", "next_cursor"} {
		if _, ok := fields[key]; !ok {
			return contractError("servicing_collection_field_missing")
		}
	}
	if bytes.Equal(bytes.TrimSpace(fields["items"]), []byte("null")) {
		return contractError("servicing_collection_items_null")
	}
	type alias CollectionPage
	var value alias
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return contractError("servicing_collection_json_invalid")
	}
	*p = CollectionPage(value)
	return nil
}

func (p *ChangePage) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return contractError("servicing_changes_json_invalid")
	}
	for _, key := range []string{"mode", "items", "next_after_seq", "has_more"} {
		if _, ok := fields[key]; !ok {
			return contractError("servicing_changes_field_missing")
		}
	}
	if bytes.Equal(bytes.TrimSpace(fields["items"]), []byte("null")) {
		return contractError("servicing_changes_items_null")
	}
	type alias ChangePage
	var value alias
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return contractError("servicing_changes_json_invalid")
	}
	*p = ChangePage(value)
	return nil
}

func knownOperationState(value string) bool {
	switch value {
	case "QUEUED", "DISPATCHING", "PENDING", "UNKNOWN", "HELD", "SETTLED", "FAILED_CONFIRMED", "CANCELLED_UNSENT", "RECONCILIATION_REQUIRED":
		return true
	default:
		return false
	}
}
func knownAuthorityStatus(value string) bool {
	switch value {
	case "VALID", "MISSING", "EXPIRED", "REVOKED", "NOT_REQUIRED", "UNKNOWN":
		return true
	default:
		return false
	}
}

func jsonObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func validOperationKind(v string) bool {
	return v == "FUND" || v == "COLLECT_FEE" || v == "COLLECT_PRINCIPAL"
}

func knownLifecycle(v string) bool {
	switch v {
	case "OFFERED", "AWAITING_ACCEPTANCE", "AWAITING_APPROVAL", "RESERVED", "FUNDING_PENDING", "ACTIVE", "COLLECTING", "REPAID", "VOID_UNFUNDED", "REOPENED_HELD":
		return true
	default:
		return false
	}
}
