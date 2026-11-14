package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// AssertionAudience isolates shortfall service authorization from other services.
const AssertionAudience = "flowcard-shortfall-service"

// AssertionSubject is the server reader identity permitted by this internal API.
const AssertionSubject = "shortfall-reader"

func assertionStatus(request *http.Request, secret []byte, now time.Time) int {
	values := request.Header.Values("Authorization")
	if len(secret) < 32 || len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return http.StatusUnauthorized
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer "))
	if len(token) > 8192 {
		return http.StatusUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return http.StatusUnauthorized
	}
	encoding := base64.RawURLEncoding.Strict()
	header, err := encoding.DecodeString(parts[0])
	if err != nil {
		return http.StatusUnauthorized
	}
	var algorithm struct {
		Alg  string `json:"alg"`
		Type string `json:"typ"`
	}
	if json.Unmarshal(header, &algorithm) != nil || algorithm.Alg != "HS256" ||
		(algorithm.Type != "" && algorithm.Type != "JWT") {
		return http.StatusUnauthorized
	}
	signature, err := encoding.DecodeString(parts[2])
	if err != nil {
		return http.StatusUnauthorized
	}
	mac := hmac.New(sha256.New, secret)
	// hash.Hash.Write cannot fail. Validate the original encoded signing input.
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return http.StatusUnauthorized
	}
	payload, err := encoding.DecodeString(parts[1])
	if err != nil {
		return http.StatusUnauthorized
	}
	var claims struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		TokenID   string `json:"jti"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != "flowcard-node-proxy" ||
		strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.TokenID) == "" ||
		claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > 30 || claims.IssuedAt > now.Unix()+5 ||
		claims.ExpiresAt < now.Unix()-5 {
		return http.StatusUnauthorized
	}
	if claims.Audience != AssertionAudience || claims.Subject != AssertionSubject {
		return http.StatusForbidden
	}
	return 0
}
