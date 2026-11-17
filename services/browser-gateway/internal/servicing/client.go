// Package servicing handles constrained DEMO actor delegation, never policy or
// financial calculation. Browser input cannot select an assertion principal.
package servicing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Restriction is verified against the owning service's current registry.
type Restriction struct {
	ActorID         string `json:"actor_id"`
	ActorType       string `json:"actor_type"`
	CustomerID      string `json:"customer_id"`
	Mode            string `json:"mode"`
	FacilityID      string `json:"facility_id"`
	SourceBindingID string `json:"source_binding_id"`
}

// Actor describes an authenticated registry entry and its fixture installation.
type Actor struct {
	Restriction
	FixtureOnly       bool `json:"fixture_only"`
	SandboxDelegation bool `json:"sandbox_delegation"`
}

var ErrUnavailable = errors.New("servicing unavailable")
var ErrBinding = errors.New("servicing binding unavailable")

// HTTPClient bounds requests and refuses redirects, ambient proxies and retries.
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 15 * time.Second}}
}

// Assertion signs only server-owned subject and optional restrictive metadata.
func Assertion(secret, subject string, binding *Restriction) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrUnavailable
	}
	now := time.Now().Unix()
	raw, err := json.Marshal(struct {
		Issuer   string       `json:"iss"`
		Audience string       `json:"aud"`
		Subject  string       `json:"sub"`
		Issued   int64        `json:"iat"`
		Expires  int64        `json:"exp"`
		ID       string       `json:"jti"`
		Binding  *Restriction `json:"binding,omitempty"`
	}{"flowcard-go", "flowcard-funding-collection", subject, now, now + 30, base64.RawURLEncoding.EncodeToString(nonce), binding})
	if err != nil {
		return "", ErrUnavailable
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	input := header + "." + base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// ReadActor verifies current registry identity using the selected role key.
func ReadActor(ctx context.Context, client *http.Client, base *url.URL, secret, subject string, binding *Restriction) (Actor, error) {
	token, err := Assertion(secret, subject, binding)
	if err != nil {
		return Actor{}, err
	}
	target := *base
	target.Path = "/api/servicing/actor"
	target.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return Actor{}, ErrUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return Actor{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 403 {
		return Actor{}, ErrBinding
	}
	if response.StatusCode != 200 {
		return Actor{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32769))
	if err != nil || len(raw) > 32768 {
		return Actor{}, ErrUnavailable
	}
	var actor Actor
	if json.Unmarshal(raw, &actor) != nil || actor.ActorID == "" || actor.FacilityID == "" || actor.SourceBindingID == "" || !actor.DelegationAllowed() || (actor.ActorType != "OPERATOR" && actor.ActorType != "CUSTOMER") || (actor.ActorType == "CUSTOMER") != (actor.CustomerID != "") {
		return Actor{}, ErrBinding
	}
	return actor, nil
}

// DelegationAllowed checks the explicit owning-service profile. A mode label
// alone never turns a remote or sandbox source into the old fixture profile.
func (a Actor) DelegationAllowed() bool {
	switch a.Mode {
	case "DEMO":
		return a.FixtureOnly && !a.SandboxDelegation
	case "SANDBOX":
		return !a.FixtureOnly && a.SandboxDelegation
	default:
		return false
	}
}
