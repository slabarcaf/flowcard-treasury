package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
)

type Identity struct {
	Mode            string
	Subject         string
	Role            string
	ActorID         string
	CustomerID      string
	ActorType       string
	CredentialClass string
	Binding         *BindingRestriction
}

// BindingRestriction narrows a trusted caller's subject to a verified registry
// entry and installation. It never supplies the principal's actual authority.
type BindingRestriction struct {
	ActorID         string `json:"actor_id"`
	ActorType       string `json:"actor_type"`
	CustomerID      string `json:"customer_id"`
	Mode            string `json:"mode"`
	FacilityID      string `json:"facility_id"`
	SourceBindingID string `json:"source_binding_id"`
}
type claims struct {
	Issuer    string              `json:"iss"`
	Audience  string              `json:"aud"`
	Subject   string              `json:"sub"`
	TokenID   string              `json:"jti"`
	IssuedAt  int64               `json:"iat"`
	ExpiresAt int64               `json:"exp"`
	Binding   *BindingRestriction `json:"binding,omitempty"`
}

// VerifyAssertion validates signature and fixed issuer/audience/TTL before any DB lookup.
func VerifyAssertion(r *http.Request, cfg config.Config, now time.Time) (Identity, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return Identity{}, errors.New("unauthorized")
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer "))
	if len(token) > 8192 {
		return Identity{}, errors.New("unauthorized")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, errors.New("unauthorized")
	}
	enc := base64.RawURLEncoding.Strict()
	headerBytes, err := enc.DecodeString(parts[0])
	if err != nil {
		return Identity{}, errors.New("unauthorized")
	}
	var header struct {
		Alg  string `json:"alg"`
		Type string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Alg != "HS256" || (header.Type != "" && header.Type != "JWT") {
		return Identity{}, errors.New("unauthorized")
	}
	payload, err := enc.DecodeString(parts[1])
	if err != nil {
		return Identity{}, errors.New("unauthorized")
	}
	var c claims
	if json.Unmarshal(payload, &c) != nil || c.Issuer != "flowcard-go" || c.Audience != "flowcard-funding-collection" || c.Subject == "" || c.TokenID == "" || c.IssuedAt <= 0 || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt-c.IssuedAt > 30 || c.IssuedAt > now.Unix()+5 || c.ExpiresAt < now.Unix()-5 {
		return Identity{}, errors.New("unauthorized")
	}
	secrets := secretsForSubject(cfg, c.Subject)
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return Identity{}, errors.New("unauthorized")
	}
	valid := false
	credentialClass := ""
	for index, secret := range secrets {
		if len(secret) < 32 {
			continue
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
		if hmac.Equal(sig, mac.Sum(nil)) {
			valid = true
			if c.Subject != "coordinator-reader" && c.Subject != "funding-coordinator" && c.Subject != "underwriting-reader" {
				if index == 0 {
					credentialClass = "CUSTOMER"
				} else {
					credentialClass = "OPERATOR"
				}
			}
			break
		}
	}
	if !valid {
		return Identity{}, errors.New("unauthorized")
	}
	role := ""
	switch c.Subject {
	case "coordinator-reader":
		role = "COORDINATOR_READER"
	case "funding-coordinator":
		role = "FUNDING_COORDINATOR"
	case "underwriting-reader":
		role = "UNDERWRITING_READER"
	default:
		role = "ACTOR"
	}
	if role == "ACTOR" && (c.Subject == "" || strings.HasPrefix(c.Subject, "system:")) {
		return Identity{}, errors.New("unauthorized")
	}
	if c.Binding != nil {
		b := c.Binding
		if role != "ACTOR" || b.ActorID == "" || (b.ActorType != "OPERATOR" && b.ActorType != "CUSTOMER") || (b.ActorType == "CUSTOMER") != (b.CustomerID != "") || b.Mode != cfg.Mode || b.FacilityID != cfg.FacilityID || b.SourceBindingID != cfg.SourceBindingID {
			return Identity{}, errors.New("unauthorized")
		}
	}
	return Identity{Mode: cfg.Mode, Subject: c.Subject, Role: role, CredentialClass: credentialClass, Binding: c.Binding}, nil
}

func secretsForSubject(cfg config.Config, subject string) []string {
	switch subject {
	case "coordinator-reader", "funding-coordinator":
		return []string{cfg.CoordinatorSecret}
	case "underwriting-reader":
		return []string{cfg.ReaderSecret}
	default:
		if cfg.Mode == config.ModeDemo || (cfg.Mode == config.ModeSandbox && cfg.SandboxDelegationEnabled) {
			return []string{cfg.CustomerSecret, cfg.OperatorSecret}
		}
		return nil
	}
}
