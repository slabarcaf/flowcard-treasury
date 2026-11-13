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

// Standard configuration errors.
var (
	// ErrInvalidPort is returned when the configured PORT is not an integer between 1 and 65535.
	ErrInvalidPort = errors.New("invalid port: must be an integer between 1 and 65535")

	// ErrInvalidAuthSecret is returned when the service authentication secret is shorter than 32 bytes.
	ErrInvalidAuthSecret = errors.New("ONBOARDING_SERVICE_SECRET must be at least 32 bytes")

	// ErrMissingKMSKey is returned when S3_BUCKET_DOCUMENTS is provided without S3_KMS_KEY_ID.
	ErrMissingKMSKey = errors.New("S3_KMS_KEY_ID is required when S3_BUCKET_DOCUMENTS is configured")
	// ErrMissingDocumentsBucket is returned when S3_KMS_KEY_ID is provided without S3_BUCKET_DOCUMENTS.
	ErrMissingDocumentsBucket = errors.New("S3_BUCKET_DOCUMENTS is required when S3_KMS_KEY_ID is configured")
)

// Config holds runtime configuration values parsed from environment variables.
type Config struct {
	Port              int
	DatabaseURL       string
	Environment       string
	AWSRegion         string
	ServiceAuthSecret string
	S3BucketDocuments string
	S3KMSKeyID        string
	AirwallexBaseURL  string
	AirwallexClientID string
	AirwallexAPIKey   string
}

// Load reads configuration from the environment using standard os.Getenv.
func Load() (*Config, error) {
	return LoadWithLookup(os.Getenv)
}

// LoadWithLookup reads configuration using the provided environment lookup function.
// Optional runtime integrations remain unconfigured when their approved values are absent.
func LoadWithLookup(lookup func(string) string) (*Config, error) {
	cfg := &Config{
		Port:             DefaultPort,
		Environment:      "development",
		AWSRegion:        "us-west-2",
		AirwallexBaseURL: "https://api.sandbox.airwallex.com",
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

	// Standard AWS SDK region (approved standard variable)
	if region := strings.TrimSpace(lookup("AWS_REGION")); region != "" {
		cfg.AWSRegion = region
	} else if region := strings.TrimSpace(lookup("AWS_DEFAULT_REGION")); region != "" {
		cfg.AWSRegion = region
	}

	// Existing approved Airwallex platform configuration
	if baseURL := strings.TrimSpace(lookup("AIRWALLEX_BASE_URL")); baseURL != "" {
		cfg.AirwallexBaseURL = baseURL
	}
	if clientID := strings.TrimSpace(lookup("AIRWALLEX_CLIENT_ID")); clientID != "" {
		cfg.AirwallexClientID = clientID
	}
	if apiKey := strings.TrimSpace(lookup("AIRWALLEX_API_KEY")); apiKey != "" {
		cfg.AirwallexAPIKey = apiKey
	}
	if err := applyRuntimeIntegrations(cfg, lookup); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyRuntimeIntegrations loads approved optional service authentication and document storage values.
func applyRuntimeIntegrations(cfg *Config, lookup func(string) string) error {
	secret := strings.TrimSpace(lookup("ONBOARDING_SERVICE_SECRET"))
	if secret != "" {
		if len(secret) < 32 {
			return fmt.Errorf("%w (received %d)", ErrInvalidAuthSecret, len(secret))
		}
		cfg.ServiceAuthSecret = secret
	}

	if bucket := strings.TrimSpace(lookup("S3_BUCKET_DOCUMENTS")); bucket != "" {
		cfg.S3BucketDocuments = bucket
	}

	if kmsKey := strings.TrimSpace(lookup("S3_KMS_KEY_ID")); kmsKey != "" {
		cfg.S3KMSKeyID = kmsKey
	}

	if cfg.S3BucketDocuments != "" && cfg.S3KMSKeyID == "" {
		return ErrMissingKMSKey
	}
	if cfg.S3BucketDocuments == "" && cfg.S3KMSKeyID != "" {
		return ErrMissingDocumentsBucket
	}

	return nil
}
