// Package config reads server-owned shortfall configuration.
package config

import (
	"errors"
	"os"
	"strings"
)

// Config holds the restricted runtime database connection and server-owned scope.
type Config struct {
	DatabaseURL      string `json:"-"`
	Environment      string
	WebhookSource    string
	HTTPAddress      string
	WebhookSecret    string `json:"-"`
	ServiceSecret    string `json:"-"`
	ProviderBaseURL  string
	ProviderVersion  string
	ProviderClientID string `json:"-"`
	ProviderAPIKey   string `json:"-"`
}

// Load reads runtime configuration without a privileged migration URL fallback.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
		Environment:      strings.TrimSpace(os.Getenv("SHORTFALL_ENVIRONMENT")),
		WebhookSource:    strings.TrimSpace(os.Getenv("SHORTFALL_WEBHOOK_SOURCE")),
		HTTPAddress:      strings.TrimSpace(os.Getenv("SHORTFALL_HTTP_ADDR")),
		WebhookSecret:    os.Getenv("SHORTFALL_WEBHOOK_SECRET"),
		ServiceSecret:    os.Getenv("SHORTFALL_SERVICE_SECRET"),
		ProviderBaseURL:  strings.TrimSpace(os.Getenv("AIRWALLEX_BASE_URL")),
		ProviderVersion:  strings.TrimSpace(os.Getenv("SHORTFALL_ISSUING_API_VERSION")),
		ProviderClientID: os.Getenv("AIRWALLEX_CLIENT_ID"),
		ProviderAPIKey:   os.Getenv("AIRWALLEX_API_KEY"),
	}
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = "127.0.0.1:8082"
	}
	if cfg.ProviderBaseURL == "" {
		cfg.ProviderBaseURL = "https://api.sandbox.airwallex.com"
	}
	if cfg.DatabaseURL == "" {
		return cfg, errors.New("database_url_required")
	}
	return cfg, nil
}
