// Package auth defines password and session cryptography for invited users.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"regexp"
	"unicode/utf8"
)

const (
	Algorithm        = "pbkdf2-sha256"
	Version          = 1
	Iterations       = 600000
	MaxPasswordBytes = 1024
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
var ErrCredentials = errors.New("invalid credentials")

// ValidUsername accepts the canonical, case-sensitive ASCII account name.
func ValidUsername(s string) bool { return usernamePattern.MatchString(s) }

// ValidPassword checks bounds without normalizing or truncating the value.
func ValidPassword(s string) bool {
	return len(s) <= MaxPasswordBytes && utf8.ValidString(s) && utf8.RuneCountInString(s) >= 15
}

// Password contains the stored, versioned verifier, never the plaintext.
type Password struct {
	Algorithm  string
	Version    int
	Iterations int
	Salt       []byte
	Verifier   []byte
}

// NewPassword derives a verifier with a fresh salt.
func NewPassword(s string) (Password, error) {
	if !ValidPassword(s) {
		return Password{}, ErrCredentials
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Password{}, err
	}
	key, err := pbkdf2.Key(sha256.New, s, salt, Iterations, 32)
	return Password{Algorithm, Version, Iterations, salt, key}, err
}

// Verify always derives a key, including for missing or unsupported records.
func Verify(s string, p Password) bool {
	valid := p.Algorithm == Algorithm && p.Version == Version && p.Iterations == Iterations && len(p.Salt) == 16 && len(p.Verifier) == 32
	if !valid {
		p = Password{Algorithm, Version, Iterations, make([]byte, 16), make([]byte, 32)}
	}
	key, err := pbkdf2.Key(sha256.New, s, p.Salt, Iterations, 32)
	return err == nil && subtle.ConstantTimeCompare(key, p.Verifier) == 1 && valid
}

// NewSession returns the cookie value and its database digest.
func NewSession() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256(raw)
	return token, digest[:], nil
}

// SessionDigest accepts only canonical 32-byte base64url tokens.
func SessionDigest(token string) ([]byte, bool) {
	if len(token) != 43 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	digest := sha256.Sum256(raw)
	return digest[:], true
}
