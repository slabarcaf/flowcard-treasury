package http_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalhttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
)

var testSecret = []byte("backend-service-secret-at-least-32-bytes-long-12345")

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

func validOperatorClaims(actor string, now time.Time, ttl time.Duration) map[string]interface{} {
	return map[string]interface{}{
		"iss": internalhttp.DefaultIssuer,
		"aud": internalhttp.DefaultOperatorAudience,
		"sub": actor,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
		"jti": "nonce-12345",
	}
}

func validWebhookClaims(actor string, now time.Time, ttl time.Duration) map[string]interface{} {
	return map[string]interface{}{
		"iss": internalhttp.DefaultIssuer,
		"aud": internalhttp.DefaultWebhookAudience,
		"sub": actor,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
		"jti": "nonce-webhook-12345",
	}
}

func TestNewJWTAuthenticator_Validation(t *testing.T) {
	_, err := internalhttp.NewJWTAuthenticator(internalhttp.JWTAuthenticatorConfig{
		Secret:           []byte("too-short"),
		ExpectedAudience: internalhttp.DefaultOperatorAudience,
	})
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("expected error for short secret, got: %v", err)
	}

	_, err = internalhttp.NewJWTAuthenticator(internalhttp.JWTAuthenticatorConfig{
		Secret:           testSecret,
		ExpectedAudience: "",
	})
	if err == nil || !strings.Contains(err.Error(), "expected audience must not be empty") {
		t.Fatalf("expected error for empty audience, got: %v", err)
	}
}

func TestJWTAuthenticator_Success(t *testing.T) {
	now := time.Now().UTC()
	auth, err := internalhttp.NewOperatorAuthenticator(testSecret)
	if err != nil {
		t.Fatalf("failed to create operator authenticator: %v", err)
	}

	token := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validOperatorClaims("operator-sam", now, 30*time.Second),
		testSecret,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/companies", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	actor, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if actor != "operator-sam" {
		t.Errorf("expected actor 'operator-sam', got %q", actor)
	}
}

func TestJWTAuthenticator_RejectsUntrustedHeadersAndMissingAuth(t *testing.T) {
	auth, _ := internalhttp.NewOperatorAuthenticator(testSecret)

	// Missing authorization header
	req := httptest.NewRequest(http.MethodGet, "/api/v1/companies", nil)
	req.Header.Set("X-Actor-Id", "spoofed-actor")
	req.Header.Set("X-Actor", "spoofed-actor")

	actor, err := auth.Authenticate(req)
	if err == nil || actor != "" {
		t.Fatalf("expected error for missing Authorization header, got actor=%q", actor)
	}

	// Non-bearer authorization header
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	actor, err = auth.Authenticate(req)
	if err == nil || actor != "" {
		t.Fatalf("expected error for non-bearer header, got actor=%q", actor)
	}
}

func TestJWTAuthenticator_RejectsAlgorithmConfusion(t *testing.T) {
	now := time.Now().UTC()
	auth, _ := internalhttp.NewOperatorAuthenticator(testSecret)

	// 1. alg: "none"
	tokenNone := makeTestJWT(
		map[string]interface{}{"alg": "none", "typ": "JWT"},
		validOperatorClaims("operator-sam", now, 30*time.Second),
		testSecret,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/companies", nil)
	req.Header.Set("Authorization", "Bearer "+tokenNone)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "unsupported algorithm") {
		t.Errorf("expected rejection for alg: none, got: %v", err)
	}

	// 2. alg: "RS256"
	tokenRS := makeTestJWT(
		map[string]interface{}{"alg": "RS256", "typ": "JWT"},
		validOperatorClaims("operator-sam", now, 30*time.Second),
		testSecret,
	)
	req.Header.Set("Authorization", "Bearer "+tokenRS)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "unsupported algorithm") {
		t.Errorf("expected rejection for alg: RS256, got: %v", err)
	}
}

func TestJWTAuthenticator_RejectsTamperingAndWrongSecret(t *testing.T) {
	now := time.Now().UTC()
	auth, _ := internalhttp.NewOperatorAuthenticator(testSecret)

	// 1. Signed with wrong secret
	tokenWrongKey := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validOperatorClaims("operator-sam", now, 30*time.Second),
		[]byte("wrong-attacker-secret-at-least-32-bytes-long"),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/companies", nil)
	req.Header.Set("Authorization", "Bearer "+tokenWrongKey)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "invalid token signature") {
		t.Errorf("expected rejection for wrong secret, got: %v", err)
	}

	// 2. Tampered signature
	validToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validOperatorClaims("operator-sam", now, 30*time.Second),
		testSecret,
	)
	tamperedSigToken := validToken + "corrupted"
	req.Header.Set("Authorization", "Bearer "+tamperedSigToken)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "invalid token signature") {
		t.Errorf("expected rejection for tampered signature, got: %v", err)
	}
}

func TestJWTAuthenticator_RejectsAudienceMismatch(t *testing.T) {
	now := time.Now().UTC()
	opAuth, _ := internalhttp.NewOperatorAuthenticator(testSecret)
	whAuth, _ := internalhttp.NewWebhookAuthenticator(testSecret)

	// Webhook token presented to Operator endpoint -> rejected!
	webhookToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validWebhookClaims("webhook-forwarder", now, 30*time.Second),
		testSecret,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/companies", nil)
	req.Header.Set("Authorization", "Bearer "+webhookToken)
	if _, err := opAuth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "audience mismatch") {
		t.Errorf("expected operator endpoint to reject webhook audience token, got: %v", err)
	}

	// Operator token presented to Webhook endpoint -> rejected!
	operatorToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validOperatorClaims("operator-sam", now, 30*time.Second),
		testSecret,
	)
	reqWebhook := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/airwallex", nil)
	reqWebhook.Header.Set("Authorization", "Bearer "+operatorToken)
	if _, err := whAuth.Authenticate(reqWebhook); err == nil || !strings.Contains(err.Error(), "audience mismatch") {
		t.Errorf("expected webhook endpoint to reject operator audience token, got: %v", err)
	}
}

func TestJWTAuthenticator_RejectsExpiredAndExcessiveTTL(t *testing.T) {
	now := time.Now().UTC()
	auth, _ := internalhttp.NewOperatorAuthenticator(testSecret)

	// Expired token
	expiredClaims := validOperatorClaims("operator-sam", now.Add(-60*time.Second), 30*time.Second)
	expiredToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		expiredClaims,
		testSecret,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/companies", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "token expired") {
		t.Errorf("expected rejection for expired token, got: %v", err)
	}

	// Excessive TTL (> 30s + skew)
	longTtlClaims := validOperatorClaims("operator-sam", now, 3600*time.Second)
	longTtlToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		longTtlClaims,
		testSecret,
	)
	req.Header.Set("Authorization", "Bearer "+longTtlToken)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "ttl exceeds maximum") {
		t.Errorf("expected rejection for excessive TTL, got: %v", err)
	}

	// Token issued in future
	futureClaims := validOperatorClaims("operator-sam", now.Add(60*time.Second), 30*time.Second)
	futureToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		futureClaims,
		testSecret,
	)
	req.Header.Set("Authorization", "Bearer "+futureToken)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "issued in the future") {
		t.Errorf("expected rejection for future token, got: %v", err)
	}
}

func TestRequireAuth_Middleware(t *testing.T) {
	auth, _ := internalhttp.NewOperatorAuthenticator(testSecret)
	now := time.Now().UTC()

	var capturedActor string
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := internalhttp.GetActor(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		capturedActor = actor
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	wrapped := internalhttp.RequireAuth(auth)(testHandler)

	// 1. Unauthorized request
	unauthReq := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, unauthReq)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Authorized request
	token := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validOperatorClaims("operator-alice", now, 30*time.Second),
		testSecret,
	)
	authReq := httptest.NewRequest(http.MethodGet, "/test", nil)
	authReq.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec2, authReq)

	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec2.Code)
	}
	if capturedActor != "operator-alice" {
		t.Errorf("expected captured actor 'operator-alice', got %q", capturedActor)
	}
}

func TestDenyAllAuthenticator(t *testing.T) {
	deny := internalhttp.DenyAllAuthenticator{}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer some-token")

	actor, err := deny.Authenticate(req)
	if err == nil || actor != "" {
		t.Errorf("expected DenyAllAuthenticator to fail closed, got actor=%q, err=%v", actor, err)
	}
}

func TestJWTAuthenticator_SecretSliceNotAliased(t *testing.T) {
	mutableSecret := append([]byte(nil), testSecret...)
	auth, err := internalhttp.NewOperatorAuthenticator(mutableSecret)
	if err != nil {
		t.Fatalf("failed to create authenticator: %v", err)
	}

	// Mutate the original secret slice
	mutableSecret[0] ^= 0xff

	now := time.Now().UTC()
	token := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validOperatorClaims("operator-isolated", now, 30*time.Second),
		testSecret, // signed with original secret
	)

	req := httptest.NewRequest(http.MethodGet, "/api/companies", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	actor, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("expected authenticator to retain unmutated copy of secret, got error: %v", err)
	}
	if actor != "operator-isolated" {
		t.Errorf("expected actor 'operator-isolated', got %q", actor)
	}
}

func TestJWTAuthenticator_RequiresNonemptyJTI(t *testing.T) {
	auth, _ := internalhttp.NewOperatorAuthenticator(testSecret)
	now := time.Now().UTC()

	// Missing jti
	claimsNoJti := validOperatorClaims("operator-sam", now, 30*time.Second)
	delete(claimsNoJti, "jti")
	tokenNoJti := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		claimsNoJti,
		testSecret,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/companies", nil)
	req.Header.Set("Authorization", "Bearer "+tokenNoJti)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "missing or empty jti") {
		t.Errorf("expected rejection for missing jti, got: %v", err)
	}

	// Empty jti
	claimsEmptyJti := validOperatorClaims("operator-sam", now, 30*time.Second)
	claimsEmptyJti["jti"] = "   "
	tokenEmptyJti := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		claimsEmptyJti,
		testSecret,
	)
	req.Header.Set("Authorization", "Bearer "+tokenEmptyJti)
	if _, err := auth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "missing or empty jti") {
		t.Errorf("expected rejection for empty whitespace jti, got: %v", err)
	}
}

func TestWebhookAuthenticator_StrictExactSubject(t *testing.T) {
	whAuth, err := internalhttp.NewWebhookAuthenticator(testSecret)
	if err != nil {
		t.Fatalf("failed to create webhook authenticator: %v", err)
	}
	now := time.Now().UTC()

	// Valid webhook caller: subject == "node-webhook-forwarder"
	validToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validWebhookClaims(internalhttp.DefaultWebhookSubject, now, 30*time.Second),
		testSecret,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/airwallex", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	actor, err := whAuth.Authenticate(req)
	if err != nil {
		t.Fatalf("expected valid webhook token to succeed, got: %v", err)
	}
	if actor != internalhttp.DefaultWebhookSubject {
		t.Errorf("expected actor %q, got %q", internalhttp.DefaultWebhookSubject, actor)
	}

	// Invalid webhook caller: arbitrary subject
	spoofedToken := makeTestJWT(
		map[string]interface{}{"alg": "HS256", "typ": "JWT"},
		validWebhookClaims("unauthorized-operator", now, 30*time.Second),
		testSecret,
	)
	req.Header.Set("Authorization", "Bearer "+spoofedToken)
	if _, err := whAuth.Authenticate(req); err == nil || !strings.Contains(err.Error(), "subject mismatch") {
		t.Errorf("expected webhook authenticator to reject non-forwarder subject, got: %v", err)
	}
}

func TestJWTAuthenticator_CrossLanguageNodeMinted(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	repoRoot := wd
	for {
		if _, err := os.Stat(filepath.Join(repoRoot, "server", "src", "http", "onboarding-proxy-auth.ts")); err == nil {
			break
		}
		parent := filepath.Dir(repoRoot)
		if parent == repoRoot {
			t.Fatalf("could not locate repository root containing server/src")
		}
		repoRoot = parent
	}

	distPath := filepath.Join(repoRoot, ".dev", "server", "server", "src", "http", "onboarding-proxy-auth.js")
	if _, err := os.Stat(distPath); err != nil {
		t.Fatalf("compiled module not found at %s (run npm run server:build): %v", distPath, err)
	}

	script := fmt.Sprintf(`
		import { createServiceAssertion } from %q;
		const token = createServiceAssertion({
			secret: "backend-service-secret-at-least-32-bytes-long-12345",
			subject: "operator-cross-lang",
			audience: "flowcard-onboarding-service",
			issuer: "flowcard-node-proxy",
			ttlSeconds: 30,
		});
		process.stdout.write(token);
	`, distPath)

	cmd := exec.Command("node", "--input-type=module", "-e", script)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cross-language test failed: node output: %s, error: %v", string(out), err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		t.Fatalf("node script produced empty token")
	}

	auth, err := internalhttp.NewOperatorAuthenticator(testSecret)
	if err != nil {
		t.Fatalf("failed to create operator authenticator: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/companies", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	actor, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("cross-language Node token failed verification in Go: %v", err)
	}
	if actor != "operator-cross-lang" {
		t.Errorf("expected actor 'operator-cross-lang', got %q", actor)
	}
}
