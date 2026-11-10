// Package config manages runtime configuration for the company-onboarding service.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPort is the standard HTTP port for the onboarding service when unset.
const DefaultPort = 8080

// ErrInvalidPort is returned when the configured PORT is not an integer between 1 and 65535.
var ErrInvalidPort = errors.New("invalid port: must be an integer between 1 and 65535")

// Config holds runtime configuration values parsed from environment variables.
type Config struct {
	Port        int
	DatabaseURL string
	Environment string
}

// Load reads configuration from the environment using standard os.Getenv.
func Load() (*Config, error) {
	return LoadWithLookup(os.Getenv)
}

// LoadWithLookup reads configuration using the provided environment lookup function.
func LoadWithLookup(lookup func(string) string) (*Config, error) {
	cfg := &Config{
		Port:        DefaultPort,
		Environment: "development",
	}

	if nodeEnv := strings.TrimSpace(lookup("NODE_ENV")); nodeEnv != "" {
		cfg.Environment = nodeEnv
	}

	if rawPort := strings.TrimSpace(lookup("PORT")); rawPort != "" {
		p, err := strconv.Atoi(rawPort)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("%w: received %q", ErrInvalidPort, rawPort)
		}
		cfg.Port = p
	}

	if dbURL := strings.TrimSpace(lookup("DATABASE_URL")); dbURL != "" {
		cfg.DatabaseURL = dbURL
	}

	return cfg, nil
}
