package airwallex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Standard Airwallex platform errors.
var (
	// ErrUnconfigured indicates the platform client is unconfigured or credentials are missing.
	ErrUnconfigured = errors.New("airwallex: platform credentials or endpoints unconfigured")

	// ErrAmbiguousRemoteOutcome indicates an ambiguous timeout, connection drop, or network partition
	// during a non-idempotent outbound call. Blind replay is strictly prohibited.
	ErrAmbiguousRemoteOutcome = errors.New("airwallex: remote call timed out or network partition occurred; outcome is uncertain")

	// ErrInvalidData indicates definitive platform validation failure (HTTP 400).
	ErrInvalidData = errors.New("airwallex: platform rejected submission due to invalid data")

	// ErrRateLimited indicates platform rate limiting (HTTP 429).
	ErrRateLimited = errors.New("airwallex: rate limit exceeded")

	// ErrServerError indicates upstream platform 5xx outage.
	ErrServerError = errors.New("airwallex: upstream platform service unavailable")

	// ErrAuthenticationFailed indicates login token acquisition failure.
	ErrAuthenticationFailed = errors.New("airwallex: platform authentication failed")

	// ErrInvalidStateForOperation indicates the platform resource is not in the required state for the operation (HTTP 400).
	ErrInvalidStateForOperation = errors.New("airwallex: invalid state for operation")

	// ErrAccountNotAuthorisedForOperation indicates the account is not authorised for this operation (HTTP 400/403).
	ErrAccountNotAuthorisedForOperation = errors.New("airwallex: account not authorised for operation")

	// ErrResourceNotFound indicates the requested platform resource was not found (HTTP 404).
	ErrResourceNotFound = errors.New("airwallex: resource not found")
)

// PlatformClient defines the consumer contract for Airwallex operations.
type PlatformClient interface {
	CreateAccount(ctx context.Context, req AccountCreateRequest) (*AccountCreateResponse, error)
	UploadFile(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*FileUploadResponse, error)
	UpdateAccount(ctx context.Context, accountID string, req AccountUpdateRequest) (*AccountDetailsResponse, error)
	SubmitAccount(ctx context.Context, accountID string) (*AccountSubmitResponse, error)
	GetAccount(ctx context.Context, accountID string) (*AccountDetailsResponse, error)
	GetRFI(ctx context.Context, rfiID string) (*RFIResponsePayload, error)
	RespondRFI(ctx context.Context, rfiID string, req RFIRespondRequest) (*RFIRespondResponse, error)
}

// ClientConfig holds explicit injected configuration parameters for the Airwallex client.
type ClientConfig struct {
	BaseURL             string
	FilesURL            string
	ClientID            string
	APIKey              string
	APIVersion          string // e.g. "2026-08-21"
	HTTPClient          *http.Client
	PlatformEnvironment string // "sandbox" (default/demo) or "production"; separated from application runtime execution mode
}

// Client implements PlatformClient against the Airwallex native REST APIs.
type Client struct {
	cfg         ClientConfig
	httpClient  *http.Client
	token       string
	tokenExpiry time.Time
	mu          sync.RWMutex
}

// NewClient initializes a typed Airwallex client with strict URL parsing, credential checks,
// and TLS enforcement. Fails closed with ErrUnconfigured if credentials or endpoints are empty.
func NewClient(cfg ClientConfig) (*Client, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	clientID := strings.TrimSpace(cfg.ClientID)
	apiKey := strings.TrimSpace(cfg.APIKey)

	if baseURL == "" || clientID == "" || apiKey == "" {
		return nil, ErrUnconfigured
	}

	parsedBase, err := url.Parse(baseURL)
	if err != nil || !parsedBase.IsAbs() {
		return nil, fmt.Errorf("%w: invalid base URL", ErrUnconfigured)
	}
	isLocal := parsedBase.Hostname() == "127.0.0.1" || parsedBase.Hostname() == "localhost"
	if parsedBase.Scheme != "https" && !isLocal {
		return nil, fmt.Errorf("%w: strict HTTPS required for remote Airwallex base endpoint", ErrUnconfigured)
	}

	platformEnv := strings.ToLower(strings.TrimSpace(cfg.PlatformEnvironment))
	if platformEnv == "production" {
		if !isLocal && parsedBase.Hostname() != "api.airwallex.com" {
			return nil, fmt.Errorf("%w: production Airwallex endpoint must target api.airwallex.com, got %q", ErrUnconfigured, parsedBase.Hostname())
		}
	} else {
		// Default sandbox platform boundary (used for demo and development runtimes)
		if !isLocal && parsedBase.Hostname() != "api.sandbox.airwallex.com" {
			return nil, fmt.Errorf("%w: demo runtime Airwallex endpoint must target sandbox (api.sandbox.airwallex.com), got %q", ErrUnconfigured, parsedBase.Hostname())
		}
	}

	filesURL := strings.TrimSpace(cfg.FilesURL)
	if filesURL != "" {
		parsedFiles, err := url.Parse(filesURL)
		if err != nil || !parsedFiles.IsAbs() {
			return nil, fmt.Errorf("%w: invalid files URL", ErrUnconfigured)
		}
		isFilesLocal := parsedFiles.Hostname() == "127.0.0.1" || parsedFiles.Hostname() == "localhost"
		if parsedFiles.Scheme != "https" && !isFilesLocal {
			return nil, fmt.Errorf("%w: strict HTTPS required for remote Airwallex files endpoint", ErrUnconfigured)
		}
		if platformEnv == "production" {
			if !isFilesLocal && parsedFiles.Hostname() != "api.airwallex.com" {
				return nil, fmt.Errorf("%w: production Airwallex files endpoint must target api.airwallex.com, got %q", ErrUnconfigured, parsedFiles.Hostname())
			}
		} else {
			if !isFilesLocal && parsedFiles.Hostname() != "api.sandbox.airwallex.com" {
				return nil, fmt.Errorf("%w: demo runtime Airwallex files endpoint must target sandbox (api.sandbox.airwallex.com), got %q", ErrUnconfigured, parsedFiles.Hostname())
			}
		}
	} else {
		filesURL = baseURL
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("security: platform endpoint redirects strictly prohibited")
			},
		}
	} else {
		copied := *httpClient
		copied.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return errors.New("security: platform endpoint redirects strictly prohibited")
		}
		httpClient = &copied
	}

	apiVersion := strings.TrimSpace(cfg.APIVersion)
	if apiVersion == "" {
		apiVersion = "2026-08-21"
	}

	return &Client{
		cfg: ClientConfig{
			BaseURL:    strings.TrimRight(baseURL, "/"),
			FilesURL:   strings.TrimRight(filesURL, "/"),
			ClientID:   clientID,
			APIKey:     apiKey,
			APIVersion: apiVersion,
		},
		httpClient: httpClient,
	}, nil
}

// NewUnconfiguredClient returns an adapter that fails closed on all operations.
func NewUnconfiguredClient(reason string) *UnavailableClient {
	if reason == "" {
		reason = "Airwallex platform credentials or endpoints not configured"
	}
	return &UnavailableClient{reason: reason}
}

// UnavailableClient fails all calls closed.
type UnavailableClient struct {
	reason string
}

func (u *UnavailableClient) CreateAccount(ctx context.Context, req AccountCreateRequest) (*AccountCreateResponse, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnconfigured, u.reason)
}
func (u *UnavailableClient) UploadFile(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*FileUploadResponse, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnconfigured, u.reason)
}
func (u *UnavailableClient) UpdateAccount(ctx context.Context, accountID string, req AccountUpdateRequest) (*AccountDetailsResponse, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnconfigured, u.reason)
}
func (u *UnavailableClient) SubmitAccount(ctx context.Context, accountID string) (*AccountSubmitResponse, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnconfigured, u.reason)
}
func (u *UnavailableClient) GetAccount(ctx context.Context, accountID string) (*AccountDetailsResponse, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnconfigured, u.reason)
}
func (u *UnavailableClient) GetRFI(ctx context.Context, rfiID string) (*RFIResponsePayload, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnconfigured, u.reason)
}
func (u *UnavailableClient) RespondRFI(ctx context.Context, rfiID string, req RFIRespondRequest) (*RFIRespondResponse, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnconfigured, u.reason)
}

// getToken retrieves a valid bearer token, executing thread-safe refresh when near expiration.
func (c *Client) getToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.token != "" && time.Now().Add(1*time.Minute).Before(c.tokenExpiry) {
		token := c.token
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.token != "" && time.Now().Add(1*time.Minute).Before(c.tokenExpiry) {
		return c.token, nil
	}

	loginURL := c.cfg.BaseURL + "/api/v1/authentication/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthenticationFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", c.cfg.ClientID)
	req.Header.Set("x-api-key", c.cfg.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: login request failed: %v", ErrAuthenticationFailed, sanitizeNetworkError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("%w: login returned unexpected status %d", ErrAuthenticationFailed, resp.StatusCode)
	}

	var authResp AuthLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", fmt.Errorf("%w: failed to parse login response: %v", ErrAuthenticationFailed, err)
	}

	if authResp.Token == "" {
		return "", fmt.Errorf("%w: received empty token from login", ErrAuthenticationFailed)
	}

	c.token = authResp.Token
	if authResp.ExpiresAt.IsZero() {
		c.tokenExpiry = time.Now().Add(10 * time.Minute)
	} else {
		c.tokenExpiry = authResp.ExpiresAt
	}

	return c.token, nil
}

// CreateAccount sends POST /api/v1/accounts/create.
// Ambiguous timeouts/network drops return ErrAmbiguousRemoteOutcome; callers must NEVER retry creation blindly.
func (c *Client) CreateAccount(ctx context.Context, req AccountCreateRequest) (*AccountCreateResponse, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal account create request: %w", err)
	}

	createURL := c.cfg.BaseURL + "/api/v1/accounts/create"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-version", c.cfg.APIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Network errors or timeouts during creation are ambiguous!
		if isTimeoutOrNetworkError(err) {
			return nil, fmt.Errorf("%w: account creation outcome is uncertain: %v", ErrAmbiguousRemoteOutcome, sanitizeNetworkError(err))
		}
		return nil, sanitizeNetworkError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var result AccountCreateResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("%w: failed to parse create response: %v", ErrAmbiguousRemoteOutcome, err)
		}
		if result.ID == "" {
			return nil, fmt.Errorf("%w: platform returned empty account ID", ErrAmbiguousRemoteOutcome)
		}
		return &result, nil

	case http.StatusBadRequest:
		var apiErr APIError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("%w: code=%s message=%s source=%s", ErrInvalidData, apiErr.Code, apiErr.Message, apiErr.Source)

	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, fmt.Errorf("%w: retry after %v", ErrRateLimited, retryAfter)

	default:
		if resp.StatusCode >= 500 {
			// Upstream 5xx on non-idempotent create is ambiguous: remote account may have been created
			return nil, fmt.Errorf("%w: upstream 5xx error (%d): account creation outcome is uncertain", ErrAmbiguousRemoteOutcome, resp.StatusCode)
		}
		return nil, fmt.Errorf("airwallex: unexpected response status %d", resp.StatusCode)
	}
}

// UploadFile sends POST /api/v1/files/upload with multipart form data.
func (c *Client) UploadFile(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*FileUploadResponse, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// File part
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("failed to create multipart file part: %w", err)
	}
	if _, err := io.Copy(part, r); err != nil {
		return nil, fmt.Errorf("failed to copy file bytes: %w", err)
	}

	_ = writer.WriteField("purpose", "business_registration")
	_ = writer.Close()

	uploadURL := c.cfg.FilesURL + "/api/v1/files/upload"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("x-api-version", c.cfg.APIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, sanitizeNetworkError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var result FileUploadResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("failed to parse file upload response: %w", err)
		}
		if result.FileID == "" {
			return nil, errors.New("platform returned empty file_id")
		}
		return &result, nil

	case http.StatusBadRequest:
		var apiErr APIError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("%w: code=%s message=%s", ErrInvalidData, apiErr.Code, apiErr.Message)

	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, fmt.Errorf("%w: retry after %v", ErrRateLimited, retryAfter)

	default:
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: status %d", ErrServerError, resp.StatusCode)
		}
		return nil, fmt.Errorf("airwallex: file upload unexpected status %d", resp.StatusCode)
	}
}

// UpdateAccount sends POST /api/v1/accounts/{id}/update.
// Ambiguous timeouts/network drops or 5xx return ErrAmbiguousRemoteOutcome.
func (c *Client) UpdateAccount(ctx context.Context, accountID string, req AccountUpdateRequest) (*AccountDetailsResponse, error) {
	cleanID := strings.TrimSpace(accountID)
	if cleanID == "" {
		return nil, errors.New("account ID must not be empty")
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal account update request: %w", err)
	}

	updateURL := fmt.Sprintf("%s/api/v1/accounts/%s/update", c.cfg.BaseURL, cleanID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, updateURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-version", c.cfg.APIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if isTimeoutOrNetworkError(err) {
			return nil, fmt.Errorf("%w: account update outcome uncertain: %v", ErrAmbiguousRemoteOutcome, sanitizeNetworkError(err))
		}
		return nil, sanitizeNetworkError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		var result AccountDetailsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("%w: failed to parse update response: %v", ErrAmbiguousRemoteOutcome, err)
		}
		return &result, nil

	case http.StatusBadRequest:
		var apiErr APIError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("%w: code=%s message=%s source=%s", ErrInvalidData, apiErr.Code, apiErr.Message, apiErr.Source)

	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, fmt.Errorf("%w: retry after %v", ErrRateLimited, retryAfter)

	default:
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: upstream 5xx error (%d): account update outcome uncertain", ErrAmbiguousRemoteOutcome, resp.StatusCode)
		}
		return nil, fmt.Errorf("airwallex: update unexpected status %d", resp.StatusCode)
	}
}

// SubmitAccount sends POST /api/v1/accounts/{id}/submit.
func (c *Client) SubmitAccount(ctx context.Context, accountID string) (*AccountSubmitResponse, error) {
	cleanID := strings.TrimSpace(accountID)
	if cleanID == "" {
		return nil, errors.New("account ID must not be empty")
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	submitURL := fmt.Sprintf("%s/api/v1/accounts/%s/submit", c.cfg.BaseURL, cleanID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, submitURL, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-version", c.cfg.APIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if isTimeoutOrNetworkError(err) {
			return nil, fmt.Errorf("%w: account submit outcome uncertain: %v", ErrAmbiguousRemoteOutcome, sanitizeNetworkError(err))
		}
		return nil, sanitizeNetworkError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		var result AccountSubmitResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("%w: failed to parse submit response: %v", ErrAmbiguousRemoteOutcome, err)
		}
		// Finding 8: strictly verify provider status against SUBMITTED (or ACTIVE)
		if result.Status != "SUBMITTED" && result.Status != "ACTIVE" {
			return nil, fmt.Errorf("%w: unexpected platform submission status %q (expected SUBMITTED)", ErrAmbiguousRemoteOutcome, result.Status)
		}
		return &result, nil

	case http.StatusBadRequest:
		var apiErr APIError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("%w: code=%s message=%s", ErrInvalidData, apiErr.Code, apiErr.Message)

	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, fmt.Errorf("%w: retry after %v", ErrRateLimited, retryAfter)

	default:
		if resp.StatusCode >= 500 {
			// Upstream 5xx on non-idempotent submit is ambiguous
			return nil, fmt.Errorf("%w: upstream 5xx error (%d): account submit outcome uncertain", ErrAmbiguousRemoteOutcome, resp.StatusCode)
		}
		return nil, fmt.Errorf("airwallex: submit unexpected status %d", resp.StatusCode)
	}
}

// GetAccount sends GET /api/v1/accounts/{id}.
func (c *Client) GetAccount(ctx context.Context, accountID string) (*AccountDetailsResponse, error) {
	cleanID := strings.TrimSpace(accountID)
	if cleanID == "" {
		return nil, errors.New("account ID must not be empty")
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	getURL := fmt.Sprintf("%s/api/v1/accounts/%s", c.cfg.BaseURL, cleanID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("x-api-version", c.cfg.APIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, sanitizeNetworkError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: status %d", ErrServerError, resp.StatusCode)
		}
		return nil, fmt.Errorf("airwallex: get account returned status %d", resp.StatusCode)
	}

	var result AccountDetailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse account details response: %w", err)
	}
	return &result, nil
}

// GetRFI sends GET /api/v1/rfis/{rfiID}.
// Provenance: https://www.airwallex.com/docs/api/risk/request_for_information_rfi/retrieve.md (2026-11-08)
func (c *Client) GetRFI(ctx context.Context, rfiID string) (*RFIResponsePayload, error) {
	cleanID := strings.TrimSpace(rfiID)
	if cleanID == "" {
		return nil, errors.New("rfi ID must not be empty")
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	getURL := fmt.Sprintf("%s/api/v1/rfis/%s", c.cfg.BaseURL, cleanID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("x-api-version", c.cfg.APIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, sanitizeNetworkError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var result RFIResponsePayload
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("failed to parse rfi retrieve response: %w", err)
		}
		return &result, nil

	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: rfi %s not found", ErrResourceNotFound, cleanID)

	case http.StatusBadRequest:
		var apiErr APIError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("%w: code=%s message=%s", ErrInvalidData, apiErr.Code, apiErr.Message)

	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, fmt.Errorf("%w: retry after %v", ErrRateLimited, retryAfter)

	default:
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: rfi retrieve failed with status %d", ErrServerError, resp.StatusCode)
		}
		return nil, fmt.Errorf("airwallex: get rfi unexpected status %d", resp.StatusCode)
	}
}

// RespondRFI sends POST /api/v1/rfis/{rfiID}/respond with answered questions.
// Ambiguous timeouts/network drops or upstream 5xx return ErrAmbiguousRemoteOutcome; callers must NEVER retry blindly.
// Provenance: https://www.airwallex.com/docs/api/risk/request_for_information_rfi/respond_rfis.md (2026-11-08)
func (c *Client) RespondRFI(ctx context.Context, rfiID string, req RFIRespondRequest) (*RFIRespondResponse, error) {
	cleanID := strings.TrimSpace(rfiID)
	if cleanID == "" {
		return nil, errors.New("rfi ID must not be empty")
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal rfi respond request: %w", err)
	}

	respondURL := fmt.Sprintf("%s/api/v1/rfis/%s/respond", c.cfg.BaseURL, cleanID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, respondURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-version", c.cfg.APIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if isTimeoutOrNetworkError(err) {
			return nil, fmt.Errorf("%w: rfi respond outcome is uncertain: %v", ErrAmbiguousRemoteOutcome, sanitizeNetworkError(err))
		}
		return nil, sanitizeNetworkError(err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		raw, _ := io.ReadAll(resp.Body)
		// Success response schema/status is not yet verified in Docs MCP; return transport success without asserting unverified fields
		return &RFIRespondResponse{
			StatusCode: resp.StatusCode,
			RawBody:    raw,
		}, nil

	case http.StatusBadRequest:
		var apiErr APIError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		switch apiErr.Code {
		case "invalid_state_for_operation":
			return nil, fmt.Errorf("%w: %s", ErrInvalidStateForOperation, apiErr.Message)
		case "account_not_authorised_for_operation":
			return nil, fmt.Errorf("%w: %s", ErrAccountNotAuthorisedForOperation, apiErr.Message)
		default:
			return nil, fmt.Errorf("%w: code=%s message=%s", ErrInvalidData, apiErr.Code, apiErr.Message)
		}

	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: rfi %s not found", ErrResourceNotFound, cleanID)

	case http.StatusForbidden:
		return nil, fmt.Errorf("%w: forbidden access to rfi %s", ErrAccountNotAuthorisedForOperation, cleanID)

	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, fmt.Errorf("%w: retry after %v", ErrRateLimited, retryAfter)

	default:
		if resp.StatusCode >= 500 {
			// Upstream 5xx on non-idempotent RFI response is ambiguous
			return nil, fmt.Errorf("%w: upstream 5xx error (%d): rfi respond outcome uncertain", ErrAmbiguousRemoteOutcome, resp.StatusCode)
		}
		return nil, fmt.Errorf("airwallex: respond rfi unexpected status %d", resp.StatusCode)
	}
}

// sanitizeNetworkError redacts sensitive parameters from connection/network errors.
func sanitizeNetworkError(err error) error {
	if err == nil {
		return nil
	}
	var netErr error
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		netErr = unwrapped
	} else {
		netErr = err
	}
	return fmt.Errorf("network error during platform communication: %v", netErr)
}

// isTimeoutOrNetworkError detects network timeouts, deadlines, and reset connections.
func isTimeoutOrNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe")
}

// parseRetryAfter parses the Retry-After header bounded between 1 and 30 seconds.
func parseRetryAfter(raw string) time.Duration {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return 5 * time.Second
	}
	if seconds, err := strconv.Atoi(clean); err == nil && seconds > 0 {
		if seconds > 30 {
			return 30 * time.Second
		}
		return time.Duration(seconds) * time.Second
	}
	return 5 * time.Second
}
