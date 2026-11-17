package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	readerIssuer   = "flowcard-go"
	readerAudience = "flowcard-coordinator-service"
	readerSubject  = "coordinator-reader"
	readerTokenTTL = 20 * time.Second
	readerSkew     = 5 * time.Second
)

type Auth struct {
	secret []byte
	now    func() time.Time
}

type AuthError struct {
	Status int
	Code   string
}

func (e *AuthError) Error() string { return e.Code }

type tokenClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	JWTID     string `json:"jti"`
}

// NewAuth creates the dedicated reader authenticator.
func NewAuth(secret string, now func() time.Time) (*Auth, error) {
	if len(strings.TrimSpace(secret)) < 32 {
		return nil, errors.New("coordinator_reader_auth_configuration_invalid")
	}
	if now == nil {
		now = time.Now
	}
	return &Auth{secret: []byte(secret), now: now}, nil
}

func (a *Auth) Verify(r *http.Request) error {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	segments := strings.Split(parts[1], ".")
	if len(segments) != 3 {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	encoding := base64.RawURLEncoding
	header, err := encoding.DecodeString(segments[0])
	if err != nil {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	var jwtHeader map[string]string
	if json.Unmarshal(header, &jwtHeader) != nil || jwtHeader["alg"] != "HS256" || jwtHeader["typ"] != "JWT" || len(jwtHeader) != 2 {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	payload, err := encoding.DecodeString(segments[1])
	if err != nil {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	var claims tokenClaims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&claims) != nil || decoder.Decode(new(any)) != io.EOF || claims.Issuer != readerIssuer || claims.JWTID == "" || claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > 30 {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	signature, err := encoding.DecodeString(segments[2])
	if err != nil {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(segments[0] + "." + segments[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	now := a.now()
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(readerSkew)) || time.Unix(claims.IssuedAt, 0).After(now.Add(readerSkew)) {
		return &AuthError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED"}
	}
	if claims.Audience != readerAudience || claims.Subject != readerSubject {
		return &AuthError{Status: http.StatusForbidden, Code: "FORBIDDEN"}
	}
	return nil
}

// SignReaderAssertion creates a short-lived local-reader assertion for cmd/watch.
func SignReaderAssertion(secret string, now time.Time) (string, error) {
	if len(strings.TrimSpace(secret)) < 32 {
		return "", errors.New("coordinator_reader_auth_configuration_invalid")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("coordinator_reader_assertion_failed")
	}
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", errors.New("coordinator_reader_assertion_failed")
	}
	claims := tokenClaims{Issuer: readerIssuer, Audience: readerAudience, Subject: readerSubject, IssuedAt: now.Unix(), ExpiresAt: now.Add(readerTokenTTL).Unix(), JWTID: hex.EncodeToString(random[:])}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", errors.New("coordinator_reader_assertion_failed")
	}
	encoding := base64.RawURLEncoding
	unsigned := encoding.EncodeToString(header) + "." + encoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + encoding.EncodeToString(mac.Sum(nil)), nil
}
