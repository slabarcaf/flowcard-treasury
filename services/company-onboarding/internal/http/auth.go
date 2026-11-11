package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const (
	actorContextKey contextKey = "actor_identity"

	// DefaultIssuer is the fixed issuer for Node service assertions.
	DefaultIssuer = "flowcard-node-proxy"
	// DefaultOperatorAudience is the expected audience for operator API calls.
	DefaultOperatorAudience = "flowcard-onboarding-service"
	// DefaultWebhookAudience is the expected audience for webhook forwarding calls.
	DefaultWebhookAudience = "flowcard-onboarding-webhooks"
	// DefaultWebhookSubject is the expected subject for webhook forwarder assertions.
	DefaultWebhookSubject = "node-webhook-forwarder"
	// DefaultMaxTTL is the maximum permitted TTL for service assertions.
	DefaultMaxTTL = 30 * time.Second
	// DefaultClockSkew is the allowable clock tolerance.
	DefaultClockSkew = 5 * time.Second
)

// ErrUnauthenticated is returned when request lacks valid authentication credentials.
var ErrUnauthenticated = errors.New("unauthenticated: authorization credentials required")

// ErrMissingActorIdentity is returned when an operation requires an actor but context has none.
var ErrMissingActorIdentity = errors.New("missing actor identity: request context lacks authenticated operator")

// Authenticator verifies an incoming request and returns the authenticated operator identity.
// Implementations must reject unauthorized requests with an error.
type Authenticator interface {
	Authenticate(r *http.Request) (string, error)
}

// DenyAllAuthenticator is the default production authenticator when verifier is absent or unconfigured.
// Always rejects requests, enforcing a strict deny-by-default boundary.
type DenyAllAuthenticator struct{}

// Authenticate always fails closed, rejecting all requests regardless of headers.
func (DenyAllAuthenticator) Authenticate(r *http.Request) (string, error) {
	return "", ErrUnauthenticated
}

// JWTClaims holds standard claims validated from service assertions.
type JWTClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	TokenID   string `json:"jti"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// JWTAuthenticatorConfig configures validation rules for service-to-service assertions.
type JWTAuthenticatorConfig struct {
	Secret           []byte
	ExpectedIssuer   string
	ExpectedAudience string
	ExpectedSubject  string
	MaxTTL           time.Duration
	AllowedClockSkew time.Duration
	Now              func() time.Time
}

// JWTAuthenticator implements Authenticator using HMAC-SHA256 (HS256) JWT verification.
type JWTAuthenticator struct {
	secret           []byte
	expectedIssuer   string
	expectedAudience string
	expectedSubject  string
	maxTTL           time.Duration
	allowedClockSkew time.Duration
	now              func() time.Time
}

// NewJWTAuthenticator constructs a verified JWT authenticator with strict validation rules.
func NewJWTAuthenticator(cfg JWTAuthenticatorConfig) (*JWTAuthenticator, error) {
	if len(cfg.Secret) < 32 {
		return nil, errors.New("jwt secret must be at least 32 bytes")
	}
	if strings.TrimSpace(cfg.ExpectedAudience) == "" {
		return nil, errors.New("expected audience must not be empty")
	}
	issuer := cfg.ExpectedIssuer
	if issuer == "" {
		issuer = DefaultIssuer
	}
	maxTTL := cfg.MaxTTL
	if maxTTL <= 0 {
		maxTTL = DefaultMaxTTL
	}
	clockSkew := cfg.AllowedClockSkew
	if clockSkew <= 0 {
		clockSkew = DefaultClockSkew
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	// Defensive copy to prevent mutable secret aliasing by caller
	secretCopy := make([]byte, len(cfg.Secret))
	copy(secretCopy, cfg.Secret)

	return &JWTAuthenticator{
		secret:           secretCopy,
		expectedIssuer:   issuer,
		expectedAudience: cfg.ExpectedAudience,
		expectedSubject:  strings.TrimSpace(cfg.ExpectedSubject),
		maxTTL:           maxTTL,
		allowedClockSkew: clockSkew,
		now:              nowFn,
	}, nil
}

// NewOperatorAuthenticator constructs an Authenticator for operator API proxy endpoints.
func NewOperatorAuthenticator(secret []byte) (*JWTAuthenticator, error) {
	return NewJWTAuthenticator(JWTAuthenticatorConfig{
		Secret:           secret,
		ExpectedAudience: DefaultOperatorAudience,
		ExpectedIssuer:   DefaultIssuer,
	})
}

// NewWebhookAuthenticator constructs an Authenticator for private webhook ingestion endpoints.
// Strictly enforces both the dedicated webhook audience and exact forwarder subject.
func NewWebhookAuthenticator(secret []byte) (*JWTAuthenticator, error) {
	return NewJWTAuthenticator(JWTAuthenticatorConfig{
		Secret:           secret,
		ExpectedAudience: DefaultWebhookAudience,
		ExpectedIssuer:   DefaultIssuer,
		ExpectedSubject:  DefaultWebhookSubject,
	})
}

// Authenticate verifies the incoming Authorization Bearer token against fixed alg, claims, and signature.
func (a *JWTAuthenticator) Authenticate(r *http.Request) (string, error) {
	if a == nil || len(a.secret) < 32 {
		return "", ErrUnauthenticated
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", ErrUnauthenticated
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		return "", fmt.Errorf("%w: empty bearer token", ErrUnauthenticated)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: malformed jwt structure", ErrUnauthenticated)
	}

	// 1. Decode and verify header
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("%w: invalid header base64", ErrUnauthenticated)
	}

	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return "", fmt.Errorf("%w: invalid header json", ErrUnauthenticated)
	}

	// Algorithm confusion prevention: strictly require HS256, reject none/RS/ES/etc.
	if hdr.Alg != "HS256" {
		return "", fmt.Errorf("%w: unsupported algorithm %q", ErrUnauthenticated, hdr.Alg)
	}
	if hdr.Typ != "" && hdr.Typ != "JWT" {
		return "", fmt.Errorf("%w: unexpected token typ %q", ErrUnauthenticated, hdr.Typ)
	}

	// 2. Verify signature using constant-time comparison
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)
	expectedSigBase64 := base64.RawURLEncoding.EncodeToString(expectedSig)

	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expectedSigBase64)) != 1 {
		return "", fmt.Errorf("%w: invalid token signature", ErrUnauthenticated)
	}

	// 3. Decode and verify claims
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: invalid payload base64", ErrUnauthenticated)
	}

	var claims JWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", fmt.Errorf("%w: invalid payload json", ErrUnauthenticated)
	}

	// Mandatory nonempty jti
	if strings.TrimSpace(claims.TokenID) == "" {
		return "", fmt.Errorf("%w: missing or empty jti claim", ErrUnauthenticated)
	}

	// Mandatory nonempty iss matching expected
	if strings.TrimSpace(claims.Issuer) == "" || claims.Issuer != a.expectedIssuer {
		return "", fmt.Errorf("%w: issuer mismatch: expected %q, got %q", ErrUnauthenticated, a.expectedIssuer, claims.Issuer)
	}

	// Mandatory nonempty aud matching expected
	if strings.TrimSpace(claims.Audience) == "" || claims.Audience != a.expectedAudience {
		return "", fmt.Errorf("%w: audience mismatch: expected %q, got %q", ErrUnauthenticated, a.expectedAudience, claims.Audience)
	}

	actor := strings.TrimSpace(claims.Subject)
	if actor == "" {
		return "", fmt.Errorf("%w: empty subject", ErrUnauthenticated)
	}

	// Strict subject verification if expected subject is configured
	if a.expectedSubject != "" && actor != a.expectedSubject {
		return "", fmt.Errorf("%w: subject mismatch: expected %q, got %q", ErrUnauthenticated, a.expectedSubject, actor)
	}

	if claims.IssuedAt <= 0 {
		return "", fmt.Errorf("%w: missing or invalid iat claim", ErrUnauthenticated)
	}

	if claims.ExpiresAt <= 0 {
		return "", fmt.Errorf("%w: missing or invalid exp claim", ErrUnauthenticated)
	}

	if claims.ExpiresAt <= claims.IssuedAt {
		return "", fmt.Errorf("%w: exp must be strictly greater than iat", ErrUnauthenticated)
	}

	now := a.now().Unix()
	clockSkewSec := int64(a.allowedClockSkew.Seconds())

	if claims.IssuedAt > now+clockSkewSec {
		return "", fmt.Errorf("%w: token issued in the future", ErrUnauthenticated)
	}

	if claims.ExpiresAt < now-clockSkewSec {
		return "", fmt.Errorf("%w: token expired", ErrUnauthenticated)
	}

	if claims.ExpiresAt-claims.IssuedAt > int64(a.maxTTL.Seconds()) {
		return "", fmt.Errorf("%w: token ttl exceeds maximum permitted limit", ErrUnauthenticated)
	}

	return actor, nil
}

// RequireAuth wraps an http.Handler with token verification and sets authenticated actor on context.
func RequireAuth(auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth == nil {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			actor, err := auth.Authenticate(r)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid or missing authentication token")
				return
			}

			if strings.TrimSpace(actor) == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing authenticated operator identity")
				return
			}

			ctx := context.WithValue(r.Context(), actorContextKey, actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetActor extracts the authenticated operator identity from the HTTP request context.
func GetActor(r *http.Request) (string, error) {
	if r == nil {
		return "", ErrMissingActorIdentity
	}
	return ActorFromContext(r.Context())
}

// ActorFromContext retrieves the authenticated operator identity from request context.
func ActorFromContext(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrMissingActorIdentity
	}
	val, ok := ctx.Value(actorContextKey).(string)
	if !ok || strings.TrimSpace(val) == "" {
		return "", ErrMissingActorIdentity
	}
	return val, nil
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    "UNAUTHENTICATED",
		"error":   "unauthorized",
		"message": message,
	})
}
