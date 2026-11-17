package http

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	assertionIssuer   = "flowcard-node-proxy"
	assertionAudience = "flowcard-underwriting-service"
	assertionSubject  = "underwriting-evaluator"
	maxAssertionBytes = 8192
)

type assertionClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	TokenID   string `json:"jti"`
}

// authenticate verifies one short-lived server assertion and returns its caller.
func authenticate(request *http.Request, secret []byte, now time.Time) (string, int) {
	values := request.Header.Values("Authorization")
	if len(secret) < 32 || len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", http.StatusUnauthorized
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer "))
	if token == "" || len(token) > maxAssertionBytes {
		return "", http.StatusUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", http.StatusUnauthorized
	}
	encoding := base64.RawURLEncoding.Strict()
	headerBytes, err := encoding.DecodeString(parts[0])
	if err != nil || !validJSONObjects(headerBytes) {
		return "", http.StatusUnauthorized
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if decodeOneJSON(headerBytes, &header) != nil || header.Algorithm != "HS256" ||
		(header.Type != "" && header.Type != "JWT") {
		return "", http.StatusUnauthorized
	}
	signature, err := encoding.DecodeString(parts[2])
	if err != nil {
		return "", http.StatusUnauthorized
	}
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(parts[0] + "." + parts[1])); err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return "", http.StatusUnauthorized
	}
	payload, err := encoding.DecodeString(parts[1])
	if err != nil || !validJSONObjects(payload) {
		return "", http.StatusUnauthorized
	}
	var claims assertionClaims
	if decodeOneJSON(payload, &claims) != nil || claims.Issuer != assertionIssuer ||
		strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.TokenID) == "" ||
		claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > 30 || claims.IssuedAt > now.Unix()+5 ||
		claims.ExpiresAt < now.Unix()-5 {
		return "", http.StatusUnauthorized
	}
	if claims.Audience != assertionAudience || claims.Subject != assertionSubject {
		return "", http.StatusForbidden
	}
	return claims.Subject, 0
}

func decodeOneJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid_assertion")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid_assertion")
	}
	return nil
}

func validJSONObjects(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !scanJSONValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func scanJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > 32 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, exists := seen[key]; exists {
				return false
			}
			seen[key] = struct{}{}
			if !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
