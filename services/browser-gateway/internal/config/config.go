// Package config validates the browser boundary before a listener is opened.
package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

// Config contains server-only settings. Database URLs must never be logged.
type Config struct {
	Address           string
	DatabaseURL       string
	Origins           map[string]bool
	SecureCookie      bool
	OnboardingURL     *url.URL
	OnboardingSecret  string
	CoordinatorURL    *url.URL
	CoordinatorSecret string
	ServicingURL      *url.URL
	ServicingSecret   string
	WriteMode         string
	ReadMode          string
	OperatorSecret    string
	CustomerSecret    string
}

// Origin validates and returns an exact serialized HTTP origin.
func Origin(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" || strings.Contains(s, "#") {
		return "", false
	}
	if u.String() != s || u.Hostname() == "" || strings.ContainsAny(u.Host, " \t\r\n,\\") {
		return "", false
	}
	if strings.Contains(u.Host, ":") && !strings.HasPrefix(u.Host, "[") && u.Port() == "" {
		return "", false
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	return s, true
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Load requires explicit trusted origins and defaults to secure cookies.
func Load() (Config, error) {
	c, err := Parse(os.Getenv("BROWSER_GATEWAY_HTTP_ADDR"), os.Getenv("DATABASE_URL"), os.Getenv("BROWSER_GATEWAY_ALLOWED_ORIGINS"), os.Getenv("BROWSER_GATEWAY_ALLOW_LOOPBACK_HTTP"))
	if err != nil {
		return Config{}, err
	}
	c, err = c.WithOnboarding(os.Getenv("BROWSER_GATEWAY_ONBOARDING_URL"), os.Getenv("BROWSER_GATEWAY_ONBOARDING_SECRET"))
	if err != nil {
		return Config{}, err
	}
	c, err = c.WithFinancial(os.Getenv("BROWSER_GATEWAY_COORDINATOR_URL"), os.Getenv("BROWSER_GATEWAY_COORDINATOR_READ_SECRET"), os.Getenv("BROWSER_GATEWAY_SERVICING_URL"), os.Getenv("BROWSER_GATEWAY_SERVICING_READ_SECRET"))
	if err != nil {
		return Config{}, err
	}
	return c.WithFinancialAccessFromEnv()
}

// WithFinancialAccessFromEnv loads the gateway's actor delegation settings.
func (c Config) WithFinancialAccessFromEnv() (Config, error) {
	readMode := os.Getenv("BROWSER_GATEWAY_FINANCIAL_READ_MODE")
	writeMode := os.Getenv("BROWSER_GATEWAY_FINANCIAL_WRITE_MODE")
	operator := os.Getenv("BROWSER_GATEWAY_SERVICING_OPERATOR_SECRET")
	customer := os.Getenv("BROWSER_GATEWAY_SERVICING_CUSTOMER_SECRET")

	if readMode == "" {
		return c.WithFinancialWrites(writeMode, operator, customer)
	}
	if readMode != "SANDBOX" || (writeMode != "" && writeMode != readMode) {
		return Config{}, errors.New("invalid financial read mode")
	}
	if writeMode != "" {
		return c.WithFinancialWrites(writeMode, operator, customer)
	}
	if c.ServicingURL == nil || len(strings.TrimSpace(operator)) < 32 || operator == c.ServicingSecret || customer != "" {
		return Config{}, errors.New("invalid operator read configuration")
	}
	c.ReadMode, c.OperatorSecret = readMode, operator
	return c, nil
}

// BindingMode is the mode for verified actor delegation.
func (c Config) BindingMode() string {
	if c.ReadMode != "" {
		return c.ReadMode
	}
	return c.WriteMode
}

// WithDemoWrites enables only an explicitly local DEMO delegation profile.
func (c Config) WithDemoWrites(mode, operator, customer string) (Config, error) {
	if mode == "" && operator == "" && customer == "" {
		return c, nil
	}
	if mode != "DEMO" || c.ServicingURL == nil || !loopback(c.ServicingURL.Hostname()) || len(strings.TrimSpace(operator)) < 32 || len(strings.TrimSpace(customer)) < 32 || operator == customer || operator == c.ServicingSecret || customer == c.ServicingSecret {
		return Config{}, errors.New("invalid demo command configuration")
	}
	c.WriteMode, c.OperatorSecret, c.CustomerSecret = mode, operator, customer
	return c, nil
}

// WithFinancialWrites selects an explicit DEMO or SANDBOX delegation profile.
// Role keys remain separate from each other and the read-only service key.
func (c Config) WithFinancialWrites(mode, operator, customer string) (Config, error) {
	if mode != "SANDBOX" {
		return c.WithDemoWrites(mode, operator, customer)
	}
	if c.ServicingURL == nil || len(strings.TrimSpace(operator)) < 32 || len(strings.TrimSpace(customer)) < 32 || operator == customer || operator == c.ServicingSecret || customer == c.ServicingSecret {
		return Config{}, errors.New("invalid sandbox command configuration")
	}
	c.WriteMode, c.OperatorSecret, c.CustomerSecret = mode, operator, customer
	return c, nil
}

// WithOnboarding validates the optional, server-owned service destination.
func (c Config) WithOnboarding(upstream, secret string) (Config, error) {
	u, err := serviceOrigin(upstream, secret)
	if err != nil {
		return Config{}, err
	}
	c.OnboardingURL, c.OnboardingSecret = u, secret
	return c, nil
}

// WithFinancial configures independent optional, read-only service connections.
func (c Config) WithFinancial(coordinator, coordinatorSecret, servicing, servicingSecret string) (Config, error) {
	var err error
	c.CoordinatorURL, err = serviceOrigin(coordinator, coordinatorSecret)
	if err != nil {
		return Config{}, err
	}
	c.ServicingURL, err = serviceOrigin(servicing, servicingSecret)
	if err != nil {
		return Config{}, err
	}
	c.CoordinatorSecret, c.ServicingSecret = coordinatorSecret, servicingSecret
	return c, nil
}

func serviceOrigin(upstream, secret string) (*url.URL, error) {
	if upstream == "" && secret == "" {
		return nil, nil
	}
	if _, ok := Origin(upstream); !ok || len(strings.TrimSpace(secret)) < 32 {
		return nil, errors.New("invalid service configuration")
	}
	u, _ := url.Parse(upstream)
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return nil, errors.New("service http requires loopback")
	}
	return u, nil
}

// Parse validates settings without opening network connections.
func Parse(address, databaseURL, origins, allowHTTP string) (Config, error) {
	c := Config{Address: address, DatabaseURL: databaseURL, Origins: map[string]bool{}, SecureCookie: true}
	if c.Address == "" {
		c.Address = "127.0.0.1:8094"
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil || port == "" || databaseURL == "" {
		return Config{}, errors.New("invalid gateway configuration")
	}
	if allowHTTP != "" && allowHTTP != "false" && allowHTTP != "true" {
		return Config{}, errors.New("invalid loopback option")
	}
	for _, s := range strings.Split(origins, ",") {
		origin, ok := Origin(s)
		if !ok {
			return Config{}, errors.New("invalid allowed origins")
		}
		c.Origins[origin] = true
		u, _ := url.Parse(origin)
		if u.Scheme == "http" && !loopback(u.Hostname()) {
			return Config{}, errors.New("non-loopback origins require https")
		}
		if allowHTTP == "true" && !loopback(u.Hostname()) {
			return Config{}, errors.New("insecure cookies require loopback origins")
		}
	}
	if allowHTTP == "true" {
		if !loopback(host) {
			return Config{}, errors.New("insecure cookies require loopback listener")
		}
		c.SecureCookie = false
	}
	return c, nil
}
