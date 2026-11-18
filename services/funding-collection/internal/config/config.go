// Package config validates server-owned funding service configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	ModeDemo        = "DEMO"
	ModeSandbox     = "SANDBOX"
	ProviderMock    = "MOCK_HTTP"
	ProviderSandbox = "AIRWALLEX_SANDBOX"
	DemoClientID    = "demo-client-id"
	DemoAPIKey      = "demo-api-key"
	sandboxBaseURL  = "https://api.sandbox.airwallex.com"
)

// Config contains validated runtime settings. Secret fields are never serialized.
type Config struct {
	DatabaseURL                  string `json:"-"`
	Mode                         string
	HTTPAddress                  string
	FacilityID                   string
	SourceBindingID              string
	PolicyID                     string
	PolicyHash                   string
	CoordinatorSecret            string `json:"-"`
	OperatorSecret               string `json:"-"`
	CustomerSecret               string `json:"-"`
	ReaderSecret                 string `json:"-"`
	UnderwritingURL              string
	UnderwritingSecret           string `json:"-"`
	ShortfallURL                 string
	ShortfallSecret              string `json:"-"`
	ProviderMode                 string
	ProviderBaseURL              string
	AirwallexWebhookSecret       string `json:"-"`
	SandboxDelegationEnabled     bool
	SandboxReadDelegationEnabled bool
	LiveDispatchEnabled          bool
	AirwallexClientID            string `json:"-"`
	AirwallexAPIKey              string `json:"-"`
	AirwallexLoginAs             string `json:"-"`
	SandboxPaymentContractFile   string `json:"-"`
	SandboxExecutionContractFile string `json:"-"`
	SandboxFundingEnabled        bool
	SandboxCollectionEnabled     bool
	HTTPTimeout                  time.Duration
	ReadinessBlockers            []string
}

// ValidOwnedDatabaseName accepts safe names under an owner-specific prefix.
func ValidOwnedDatabaseName(value, prefix string) bool {
	if len(value) == 0 || len(value) > 63 || (value != prefix && !strings.HasPrefix(value, prefix+"_")) {
		return false
	}
	for index, char := range value {
		if (index == 0 && (char < 'a' || char > 'z')) ||
			(index > 0 && !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_')) {
			return false
		}
	}
	return true
}

// Load reads and validates the process environment. It never falls back to a
// different environment's credentials or provider endpoint.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:                strings.TrimSpace(os.Getenv("DATABASE_URL")),
		Mode:                       strings.TrimSpace(os.Getenv("SERVICING_MODE")),
		HTTPAddress:                strings.TrimSpace(os.Getenv("SERVICING_HTTP_ADDR")),
		FacilityID:                 strings.TrimSpace(os.Getenv("SERVICING_FACILITY_ID")),
		SourceBindingID:            strings.TrimSpace(os.Getenv("SERVICING_SOURCE_BINDING_ID")),
		CoordinatorSecret:          os.Getenv("SERVICING_COORDINATOR_SECRET"),
		OperatorSecret:             os.Getenv("SERVICING_OPERATOR_SECRET"),
		CustomerSecret:             os.Getenv("SERVICING_CUSTOMER_SECRET"),
		ReaderSecret:               os.Getenv("SERVICING_READER_SECRET"),
		UnderwritingURL:            strings.TrimSpace(os.Getenv("UNDERWRITING_BASE_URL")),
		UnderwritingSecret:         os.Getenv("UNDERWRITING_SERVICE_SECRET"),
		ShortfallURL:               strings.TrimSpace(os.Getenv("SHORTFALL_BASE_URL")),
		ShortfallSecret:            os.Getenv("SHORTFALL_SERVICE_SECRET"),
		ProviderMode:               strings.TrimSpace(os.Getenv("SERVICING_PROVIDER_MODE")),
		ProviderBaseURL:            strings.TrimSpace(os.Getenv("SERVICING_PROVIDER_BASE_URL")),
		AirwallexWebhookSecret:     os.Getenv("AIRWALLEX_WEBHOOK_SECRET"),
		AirwallexClientID:          os.Getenv("AIRWALLEX_CLIENT_ID"),
		AirwallexAPIKey:            os.Getenv("AIRWALLEX_API_KEY"),
		AirwallexLoginAs:           os.Getenv("AIRWALLEX_ACCOUNT_ID"),
		SandboxPaymentContractFile: os.Getenv("SERVICING_SANDBOX_PAYMENT_CONTRACT_FILE"),
		HTTPTimeout:                10 * time.Second,
	}
	rawDelegation := os.Getenv("SERVICING_SANDBOX_DELEGATION_ENABLED")
	if rawDelegation != "" && rawDelegation != "false" && rawDelegation != "true" {
		return cfg, errors.New("sandbox_delegation_configuration_invalid")
	}
	cfg.SandboxDelegationEnabled = rawDelegation == "true"
	switch os.Getenv("SERVICING_SANDBOX_READ_DELEGATION_ENABLED") {
	case "true":
		cfg.SandboxReadDelegationEnabled = true
	case "", "false":
	default:
		return cfg, errors.New("sandbox_read_delegation_configuration_invalid")
	}
	if (cfg.SandboxDelegationEnabled || cfg.SandboxReadDelegationEnabled) && cfg.Mode != ModeSandbox {
		return cfg, errors.New("sandbox_delegation_requires_sandbox")
	}
	for name, destination := range map[string]*bool{"SERVICING_SANDBOX_FUNDING_ENABLED": &cfg.SandboxFundingEnabled, "SERVICING_SANDBOX_COLLECTION_ENABLED": &cfg.SandboxCollectionEnabled} {
		value := os.Getenv(name)
		if value != "" && value != "true" && value != "false" {
			return cfg, errors.New("sandbox_execution_flag_invalid")
		}
		*destination = value == "true"
	}
	cfg.SandboxExecutionContractFile = os.Getenv("SERVICING_SANDBOX_EXECUTION_CONTRACT_FILE")
	if (cfg.SandboxFundingEnabled || cfg.SandboxCollectionEnabled) && cfg.Mode != ModeSandbox {
		return cfg, errors.New("sandbox_execution_requires_sandbox")
	}
	cfg.LiveDispatchEnabled = LiveDispatchRequested()
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = "127.0.0.1:8085"
	}
	if cfg.Mode != ModeDemo && cfg.Mode != ModeSandbox {
		return cfg, errors.New("servicing_mode_invalid")
	}
	if cfg.DatabaseURL == "" || cfg.FacilityID == "" || cfg.SourceBindingID == "" {
		return cfg, errors.New("servicing_configuration_incomplete")
	}
	if cfg.Mode == ModeDemo && cfg.ProviderMode != ProviderMock {
		return cfg, errors.New("demo_provider_mode_invalid")
	}
	if cfg.Mode == ModeSandbox && cfg.ProviderMode != ProviderSandbox {
		return cfg, errors.New("sandbox_provider_mode_invalid")
	}
	if err := validateRuntimeAddress(cfg.HTTPAddress, cfg.Mode, os.Getenv("SERVICING_ALLOW_PRIVATE_NETWORK_LISTENER")); err != nil {
		return cfg, err
	}
	if err := validateDistinctKeys(cfg); err != nil {
		return cfg, err
	}
	if cfg.Mode == ModeDemo {
		if cfg.AirwallexClientID != "" && cfg.AirwallexClientID != DemoClientID ||
			cfg.AirwallexAPIKey != "" && cfg.AirwallexAPIKey != DemoAPIKey {
			return cfg, errors.New("demo_provider_credentials_must_be_synthetic")
		}
		cfg.AirwallexClientID = DemoClientID
		cfg.AirwallexAPIKey = DemoAPIKey
		if cfg.ProviderBaseURL == "" {
			return cfg, errors.New("mock_provider_url_required")
		}
		if err := validateLoopbackHTTPURL(cfg.ProviderBaseURL); err != nil {
			return cfg, err
		}
	} else {
		if cfg.ProviderBaseURL != "" && cfg.ProviderBaseURL != sandboxBaseURL {
			return cfg, errors.New("sandbox_provider_host_mismatch")
		}
		cfg.ProviderBaseURL = sandboxBaseURL
		if cfg.AirwallexClientID == "" || cfg.AirwallexAPIKey == "" {
			return cfg, errors.New("sandbox_credentials_required")
		}
	}
	if cfg.UnderwritingURL != "" {
		if err := validateDependencyURL(cfg.UnderwritingURL); err != nil {
			return cfg, err
		}
		if len(cfg.UnderwritingSecret) < 32 {
			return cfg, errors.New("underwriting_service_key_invalid")
		}
	}
	if cfg.ShortfallURL != "" {
		if err := validateDependencyURL(cfg.ShortfallURL); err != nil {
			return cfg, err
		}
		if len(cfg.ShortfallSecret) < 32 {
			return cfg, errors.New("shortfall_service_key_invalid")
		}
	}
	if cfg.AirwallexWebhookSecret != "" && len(cfg.AirwallexWebhookSecret) < 32 {
		return cfg, errors.New("airwallex_webhook_secret_invalid")
	}
	if cfg.Mode == ModeSandbox {
		cfg.ReadinessBlockers = []string{"SANDBOX_PROVIDER_CREATE_DISABLED"}
	}
	return cfg, nil
}

// LiveDispatchRequested reports the explicit flag without granting capability.
func LiveDispatchRequested() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SERVICING_LIVE_DISPATCH_ENABLED")), "true")
}

// validateDistinctKeys enforces role separation and minimum signing-key size.
func validateDistinctKeys(cfg Config) error {
	keys := []string{cfg.CoordinatorSecret, cfg.OperatorSecret, cfg.ReaderSecret}
	if cfg.Mode == ModeDemo || cfg.SandboxDelegationEnabled {
		keys = append(keys, cfg.CustomerSecret)
	}
	if cfg.UnderwritingSecret != "" {
		keys = append(keys, cfg.UnderwritingSecret)
	}
	if cfg.ShortfallSecret != "" {
		keys = append(keys, cfg.ShortfallSecret)
	}
	if cfg.AirwallexWebhookSecret != "" {
		keys = append(keys, cfg.AirwallexWebhookSecret)
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if len(key) < 32 {
			return errors.New("assertion_key_too_short")
		}
		if _, exists := seen[key]; exists {
			return errors.New("assertion_keys_not_distinct")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return errors.New("servicing_http_address_invalid")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("servicing_http_address_must_be_loopback")
	}
	return nil
}

func validateRuntimeAddress(address, mode, allow string) error {
	if allow != "" && allow != "false" && allow != "true" {
		return errors.New("private_listener_option_invalid")
	}
	if allow == "true" {
		host, port, err := net.SplitHostPort(address)
		if mode == ModeSandbox && err == nil && host == "0.0.0.0" && port != "" {
			return nil
		}
	}
	return validateAddress(address)
}

func validateLoopbackHTTPURL(value string) error {
	u, err := parseBaseURL(value)
	if err != nil {
		return errors.New("mock_provider_url_invalid")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return errors.New("mock_provider_must_be_loopback_http")
	}
	return nil
}

func validateDependencyURL(value string) error {
	u, err := parseBaseURL(value)
	if err != nil {
		return errors.New("dependency_url_invalid")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return errors.New("dependency_url_requires_https")
	}
	return nil
}

func parseBaseURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("url_invalid")
	}
	return u, nil
}
