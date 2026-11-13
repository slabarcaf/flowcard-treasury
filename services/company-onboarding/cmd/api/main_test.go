package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
	internalHttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
)

// pipeListener implements net.Listener using net.Pipe for in-memory, deterministic testing
// without loopback network socket requirements.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (p *pipeListener) Accept() (net.Conn, error) {
	select {
	case <-p.closed:
		return nil, net.ErrClosed
	case c, ok := <-p.conns:
		if !ok {
			return nil, net.ErrClosed
		}
		return c, nil
	}
}

func (p *pipeListener) Close() error {
	p.once.Do(func() {
		close(p.closed)
	})
	return nil
}

func (p *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
}

func (p *pipeListener) Dial() (net.Conn, error) {
	select {
	case <-p.closed:
		return nil, net.ErrClosed
	default:
	}
	serverConn, clientConn := net.Pipe()
	select {
	case p.conns <- serverConn:
		return clientConn, nil
	case <-p.closed:
		_ = serverConn.Close()
		_ = clientConn.Close()
		return nil, net.ErrClosed
	}
}

// closedOnAcceptListener immediately returns http.ErrServerClosed on Accept
// to deterministically test Serve returning clean exit without sending on errCh.
type closedOnAcceptListener struct{}

func (closedOnAcceptListener) Accept() (net.Conn, error) {
	return nil, http.ErrServerClosed
}

func (closedOnAcceptListener) Close() error {
	return nil
}

func (closedOnAcceptListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	handler := newHandler()

	tests := []struct {
		name string
		path string
	}{
		{name: "healthz_endpoint", path: "/healthz"},
		{name: "livez_endpoint", path: "/livez"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("GET %s status = %v, want %v", tt.path, rec.Code, http.StatusOK)
			}

			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if body["status"] != "ok" {
				t.Errorf("status = %q, want ok", body["status"])
			}
			if body["service"] != "company-onboarding" {
				t.Errorf("service = %q, want company-onboarding", body["service"])
			}
		})
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := newHandler()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz status = %v, want %v", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestRunWithListenerGracefulShutdown(t *testing.T) {
	t.Parallel()

	pl := newPipeListener()
	t.Cleanup(func() {
		_ = pl.Close()
	})

	cfg := &config.Config{
		Port:        8080,
		Environment: "test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithListener(ctx, cfg, pl, nil, 2*time.Second)
	}()

	// Signal graceful shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runWithListener() unexpected error on cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithListener() did not shut down within 3 seconds")
	}
}

func TestRunWithListenerErrChannelCloseReturnsNil(t *testing.T) {
	t.Parallel()

	// closedOnAcceptListener causes server.Serve to immediately return http.ErrServerClosed.
	// This closes errCh without sending an error.
	// Verifies that receiving from closed errCh cleanly returns nil rather than a false wrapped error.
	l := closedOnAcceptListener{}
	cfg := &config.Config{
		Port:        8080,
		Environment: "test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithListener(ctx, cfg, l, nil, 2*time.Second)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runWithListener() expected nil error on closed channel, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithListener() did not complete within 3 seconds")
	}
}

func TestRunWithListenerShutdownTimeoutForcedClose(t *testing.T) {
	t.Parallel()

	pl := newPipeListener()
	t.Cleanup(func() {
		_ = pl.Close()
	})

	cfg := &config.Config{
		Port:        8080,
		Environment: "test",
	}

	handlerEntered := make(chan struct{})
	handlerRelease := make(chan struct{})
	handlerCtxCancelled := make(chan struct{})

	blockedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerEntered)
		select {
		case <-handlerRelease:
		case <-r.Context().Done():
			close(handlerCtxCancelled)
			// Remain blocked until explicitly released to force server.Shutdown deadline timeout
			<-handlerRelease
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		select {
		case <-handlerRelease:
		default:
			close(handlerRelease)
		}
	})

	errCh := make(chan error, 1)
	go func() {
		// Use short 20ms shutdown timeout to deterministically exercise forced server.Close path
		errCh <- runWithListener(ctx, cfg, pl, blockedHandler, 20*time.Millisecond)
	}()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return pl.Dial()
			},
		},
	}

	reqErrCh := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://pipe.local/blocked")
		if resp != nil {
			_ = resp.Body.Close()
		}
		reqErrCh <- err
	}()

	// Wait deterministically for handler to receive request via synchronization (no sleeps)
	select {
	case <-handlerEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler was not reached within 3 seconds")
	}

	// Trigger shutdown while handler is blocked
	cancel()

	// Verify BaseContext cancelled handler context
	select {
	case <-handlerCtxCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("BaseContext cancellation was not received by handler within 3 seconds")
	}

	// runWithListener should time out during graceful shutdown, invoke server.Close(), and return context.DeadlineExceeded
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("runWithListener() expected shutdown timeout error, got nil")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("runWithListener() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithListener() did not shut down within 3 seconds")
	}

	// Release handler goroutine cleanly
	close(handlerRelease)
}

func makeTestJWT(header map[string]interface{}, claims map[string]interface{}, secret []byte) string {
	hdrBytes, _ := json.Marshal(header)
	claimsBytes, _ := json.Marshal(claims)

	hdrB64 := base64.RawURLEncoding.EncodeToString(hdrBytes)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signingInput := hdrB64 + "." + claimsB64

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sigB64
}

func TestBuildHandler_UnconfiguredAuthSecretDeniesAll(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Environment: "test",
		Port:        8080,
	}

	handler, closeFn, err := buildHandler(context.Background(), cfg)
	if err != nil {
		t.Fatalf("buildHandler returned unexpected error: %v", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	req := httptest.NewRequest(http.MethodGet, "/api/companies", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/companies status = %d, want %d (fail closed unconfigured)", rec.Code, http.StatusUnauthorized)
	}
}

func TestBuildHandler_ConfiguredServiceAuthSecretVerifiesToken(t *testing.T) {
	t.Parallel()

	testSecret := "super-secure-shared-signing-secret-at-least-32-bytes"
	cfg := &config.Config{
		Environment:       "test",
		Port:              8080,
		ServiceAuthSecret: testSecret,
	}

	handler, closeFn, err := buildHandler(context.Background(), cfg)
	if err != nil {
		t.Fatalf("buildHandler returned unexpected error: %v", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	now := time.Now()
	token := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		map[string]interface{}{
			"iss": internalHttp.DefaultIssuer,
			"aud": internalHttp.DefaultOperatorAudience,
			"sub": "usr_operator_123",
			"iat": now.Unix(),
			"exp": now.Add(30 * time.Second).Unix(),
			"jti": "test-jti-12345",
		},
		[]byte(testSecret),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/companies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Since DATABASE_URL is not set, repository is unconfigured.
	// Passing authentication means it reaches the handler and returns 503 DATABASE_UNAVAILABLE, NOT 401 Unauthorized!
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /api/companies status = %d, want 503 ServiceUnavailable (auth passed, repo unconfigured)", rec.Code)
	}
}

func TestBuildHandler_AudienceMismatchRejected(t *testing.T) {
	t.Parallel()

	testSecret := "super-secure-shared-signing-secret-at-least-32-bytes"
	cfg := &config.Config{
		Environment:       "test",
		Port:              8080,
		ServiceAuthSecret: testSecret,
	}

	handler, closeFn, err := buildHandler(context.Background(), cfg)
	if err != nil {
		t.Fatalf("buildHandler returned unexpected error: %v", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	now := time.Now()
	// Mint token with webhook audience instead of operator audience
	token := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		map[string]interface{}{
			"iss": internalHttp.DefaultIssuer,
			"aud": internalHttp.DefaultWebhookAudience,
			"sub": "usr_operator_123",
			"iat": now.Unix(),
			"exp": now.Add(30 * time.Second).Unix(),
			"jti": "test-jti-12345",
		},
		[]byte(testSecret),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/companies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Audience mismatch must be rejected with 401 Unauthorized
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/companies with webhook token status = %d, want 401 Unauthorized", rec.Code)
	}
}

func TestBuildHandler_SwaggerDocsPublic(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Environment: "test",
		Port:        8080,
	}

	handler, closeFn, err := buildHandler(context.Background(), cfg)
	if err != nil {
		t.Fatalf("buildHandler returned unexpected error: %v", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	req := httptest.NewRequest(http.MethodGet, "/docs/api/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /docs/api/ status = %d, want 200 OK", rec.Code)
	}
}

func TestBuildHandler_ConfiguredS3Storage_CredentialsResolution(t *testing.T) {
	// Subtests cannot run parallel when mutating process environment with t.Setenv
	t.Run("missing_credentials_fails_closed", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "")
		t.Setenv("AWS_SESSION_TOKEN", "")
		t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
		t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")

		cfg := &config.Config{
			Environment:       "test",
			Port:              8080,
			S3BucketDocuments: "test-documents-bucket",
			S3KMSKeyID:        "arn:aws:kms:us-west-2:123456789012:key/test-key",
			AWSRegion:         "us-west-2",
		}

		_, closeFn, err := buildHandler(context.Background(), cfg)
		if closeFn != nil {
			defer closeFn()
		}

		if err == nil {
			t.Fatal("expected error with unconfigured S3 credentials, got nil")
		}
		if !strings.Contains(err.Error(), "object store unconfigured") {
			t.Errorf("expected ErrStorageUnconfigured, got: %v", err)
		}
	})

	t.Run("static_environment_credentials_configured_successfully", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKEACCESSKEY1234")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "fakeSecretAccessKey5678901234567890")
		t.Setenv("AWS_SESSION_TOKEN", "fakeSessionToken")
		t.Setenv("AWS_REGION", "us-west-2")

		cfg := &config.Config{
			Environment:       "test",
			Port:              8080,
			S3BucketDocuments: "test-documents-bucket",
			S3KMSKeyID:        "arn:aws:kms:us-west-2:123456789012:key/test-key",
			AWSRegion:         "us-west-2",
		}

		handler, closeFn, err := buildHandler(context.Background(), cfg)
		if err != nil {
			t.Fatalf("expected successful handler build with static credentials, got error: %v", err)
		}
		if closeFn != nil {
			defer closeFn()
		}
		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})

	t.Run("ecs_task_role_container_credentials_configured_successfully", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "")
		t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "/v2/credentials/fake-task-role")
		t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN", "fake-token")

		cfg := &config.Config{
			Environment:       "test",
			Port:              8080,
			S3BucketDocuments: "test-documents-bucket",
			S3KMSKeyID:        "arn:aws:kms:us-west-2:123456789012:key/test-key",
			AWSRegion:         "us-west-2",
		}

		handler, closeFn, err := buildHandler(context.Background(), cfg)
		if err != nil {
			t.Fatalf("expected successful handler build with ECS container credentials, got error: %v", err)
		}
		if closeFn != nil {
			defer closeFn()
		}
		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})
}
