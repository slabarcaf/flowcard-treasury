package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"time"
)

// These are the implemented onboarding handlers, never an arbitrary prefix.
var onboardingRoutes = []string{
	"POST /api/companies", "GET /api/companies", "GET /api/companies/{id}",
	"PATCH /api/companies/{id}", "PUT /api/companies/{id}",
	"POST /api/companies/{id}/archive", "DELETE /api/companies/{id}",
	"GET /api/companies/{id}/platform-accounts",
	"POST /api/onboarding-applications", "GET /api/onboarding-applications",
	"GET /api/onboarding-applications/{id}", "PATCH /api/onboarding-applications/{id}",
	"POST /api/onboarding-applications/{id}/cancel", "POST /api/onboarding-applications/{id}/verify",
	"POST /api/onboarding-applications/{id}/submit", "GET /api/onboarding-applications/{id}/submissions",
	"POST /api/onboarding-applications/{id}/documents", "GET /api/onboarding-applications/{id}/documents",
	"GET /api/onboarding-applications/{id}/documents/{doc_id}",
	"GET /api/submissions/{id}", "GET /api/platform-accounts/{id}",
}

func upstreamClient() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		// Fresh connections avoid net/http's implicit replay on stale pooled sockets.
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second},
	}
}

func assertion(secret, subject string) (string, error) {
	return serviceAssertion(secret, "flowcard-node-proxy", "flowcard-onboarding-service", subject)
}

func serviceAssertion(secret, issuer, audience, subject string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	now := time.Now().Unix()
	payload, err := json.Marshal(struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		TokenID   string `json:"jti"`
	}{issuer, audience, subject, now, now + 30, base64.RawURLEncoding.EncodeToString(nonce)})
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	input := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (a *Server) onboarding(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	if session.User.Role != "OPERATOR" {
		fail(w, 403, "forbidden")
		return
	}
	if a.config.OnboardingURL == nil {
		fail(w, 503, "unavailable")
		return
	}
	limit := int64(1024 * 1024)
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media == "multipart/form-data" {
		if r.Method != "POST" || r.Pattern != "POST /api/onboarding-applications/{id}/documents" {
			fail(w, 400, "invalid request")
			return
		}
		limit = 15 * 1024 * 1024
	}
	token, err := assertion(a.config.OnboardingSecret, session.User.ID)
	if err != nil {
		fail(w, 503, "unavailable")
		return
	}
	a.forward(ctx, w, r, a.config.OnboardingURL, token, limit, 0)
}
