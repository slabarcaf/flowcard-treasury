// Package config parses server-owned coordinator configuration.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL             string `json:"-"`
	Mode                    string
	HTTPAddress             string
	SourceBindingID         string
	ShortfallURL            string
	ShortfallSecret         string `json:"-"`
	UnderwritingURL         string
	UnderwritingSecret      string `json:"-"`
	ServicingURL            string
	ServicingSecret         string `json:"-"`
	AnthropicAPIKey         string `json:"-"`
	CoordinatorReaderSecret string `json:"-"`
	AgentMode               string
	AnthropicModel          string
	SourceRoot              string
}

// Load reads required configuration without logging secret values.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:             strings.TrimSpace(os.Getenv("DATABASE_URL")),
		Mode:                    strings.TrimSpace(os.Getenv("COORDINATOR_MODE")),
		HTTPAddress:             strings.TrimSpace(os.Getenv("COORDINATOR_HTTP_ADDR")),
		SourceBindingID:         strings.TrimSpace(os.Getenv("COORDINATOR_SOURCE_BINDING_ID")),
		ShortfallURL:            strings.TrimSpace(os.Getenv("SHORTFALL_BASE_URL")),
		ShortfallSecret:         os.Getenv("SHORTFALL_SERVICE_SECRET"),
		UnderwritingURL:         strings.TrimSpace(os.Getenv("UNDERWRITING_BASE_URL")),
		UnderwritingSecret:      os.Getenv("UNDERWRITING_SERVICE_SECRET"),
		ServicingURL:            strings.TrimSpace(os.Getenv("SERVICING_BASE_URL")),
		ServicingSecret:         os.Getenv("SERVICING_SERVICE_SECRET"),
		AnthropicAPIKey:         os.Getenv("ANTHROPIC_API_KEY"),
		CoordinatorReaderSecret: os.Getenv("COORDINATOR_READER_SECRET"),
		AgentMode:               strings.TrimSpace(os.Getenv("COORDINATOR_AGENT_MODE")),
		AnthropicModel:          strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")),
		SourceRoot:              strings.TrimSpace(os.Getenv("COORDINATOR_SOURCE_ROOT")),
	}
	if c.HTTPAddress == "" {
		c.HTTPAddress = "127.0.0.1:8084"
	}
	if !loopbackAddress(c.HTTPAddress) {
		return Config{}, errors.New("coordinator_http_address_must_be_loopback")
	}
	if c.DatabaseURL == "" || c.SourceBindingID == "" {
		return Config{}, errors.New("required_configuration_missing")
	}
	if c.Mode != "DEMO" && c.Mode != "SANDBOX" {
		return Config{}, errors.New("coordinator_mode_invalid")
	}
	if len(strings.TrimSpace(c.CoordinatorReaderSecret)) < 32 {
		return Config{}, errors.New("coordinator_reader_auth_configuration_invalid")
	}
	if c.AgentMode == "" {
		c.AgentMode = "FIXTURE"
	}
	if c.AgentMode != "FIXTURE" && c.AgentMode != "ANTHROPIC" {
		return Config{}, errors.New("agent_mode_invalid")
	}
	if c.AgentMode == "ANTHROPIC" && (c.AnthropicModel == "" || strings.TrimSpace(c.AnthropicAPIKey) == "") {
		return Config{}, errors.New("anthropic_configuration_missing")
	}
	for _, endpoint := range []string{c.ShortfallURL, c.UnderwritingURL, c.ServicingURL} {
		if endpoint == "" {
			continue
		}
		parsed, err := url.ParseRequestURI(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return Config{}, errors.New("service_endpoint_invalid")
		}
		if parsed.Scheme == "http" {
			host := parsed.Hostname()
			ip := net.ParseIP(host)
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				return Config{}, errors.New("service_endpoint_requires_tls")
			}
		}
	}
	return c, nil
}

// Fingerprint identifies nonsecret source-binding settings.
func (c Config) Fingerprint() string {
	value := strings.Join([]string{c.Mode, c.SourceBindingID, c.ShortfallURL, c.UnderwritingURL, c.ServicingURL, c.SourceRoot}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func loopbackAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
