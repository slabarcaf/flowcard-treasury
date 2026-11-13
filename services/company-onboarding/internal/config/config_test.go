package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
)

func TestLoadWithLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		env          map[string]string
		wantPort     int
		wantEnv      string
		wantDBURL    string
		wantRegion   string
		wantBaseURL  string
		wantClientID string
		wantAPIKey   string
		wantAuth     string
		wantBucket   string
		wantKMS      string
		wantErr      error
	}{
		{
			name:        "defaults_applied_when_environment_is_empty",
			env:         map[string]string{},
			wantPort:    config.DefaultPort,
			wantEnv:     "development",
			wantDBURL:   "",
			wantRegion:  "us-west-2",
			wantBaseURL: "https://api.sandbox.airwallex.com",
		},
		{
			name: "custom_port_database_region_and_approved_airwallex_config",
			env: map[string]string{
				"PORT":                "9090",
				"DATABASE_URL":        "postgresql://localhost:5432/testdb",
				"NODE_ENV":            "production",
				"AWS_REGION":          "us-east-1",
				"AIRWALLEX_BASE_URL":  "https://api.sandbox.airwallex.com",
				"AIRWALLEX_CLIENT_ID": "approved-client-123",
				"AIRWALLEX_API_KEY":   "approved-key-xyz",
			},
			wantPort:     9090,
			wantEnv:      "production",
			wantDBURL:    "postgresql://localhost:5432/testdb",
			wantRegion:   "us-east-1",
			wantBaseURL:  "https://api.sandbox.airwallex.com",
			wantClientID: "approved-client-123",
			wantAPIKey:   "approved-key-xyz",
		},
		{
			name: "invalid_non_numeric_port",
			env: map[string]string{
				"PORT": "invalid",
			},
			wantErr: config.ErrInvalidPort,
		},
		{
			name: "port_zero_rejected",
			env: map[string]string{
				"PORT": "0",
			},
			wantErr: config.ErrInvalidPort,
		},
		{
			name: "port_over_65535_rejected",
			env: map[string]string{
				"PORT": "70000",
			},
			wantErr: config.ErrInvalidPort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lookup := func(key string) string {
				return tt.env[key]
			}

			cfg, err := config.LoadWithLookup(lookup)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("LoadWithLookup() expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("LoadWithLookup() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("LoadWithLookup() unexpected error: %v", err)
			}
			if cfg.Port != tt.wantPort {
				t.Errorf("cfg.Port = %v, want %v", cfg.Port, tt.wantPort)
			}
			if cfg.Environment != tt.wantEnv {
				t.Errorf("cfg.Environment = %q, want %q", cfg.Environment, tt.wantEnv)
			}
			if cfg.DatabaseURL != tt.wantDBURL {
				t.Errorf("cfg.DatabaseURL = %q, want %q", cfg.DatabaseURL, tt.wantDBURL)
			}
			if cfg.AWSRegion != tt.wantRegion {
				t.Errorf("cfg.AWSRegion = %q, want %q", cfg.AWSRegion, tt.wantRegion)
			}
			if tt.wantBaseURL != "" && cfg.AirwallexBaseURL != tt.wantBaseURL {
				t.Errorf("cfg.AirwallexBaseURL = %q, want %q", cfg.AirwallexBaseURL, tt.wantBaseURL)
			}
			if tt.wantClientID != "" && cfg.AirwallexClientID != tt.wantClientID {
				t.Errorf("cfg.AirwallexClientID = %q, want %q", cfg.AirwallexClientID, tt.wantClientID)
			}
			if tt.wantAPIKey != "" && cfg.AirwallexAPIKey != tt.wantAPIKey {
				t.Errorf("cfg.AirwallexAPIKey = %q, want %q", cfg.AirwallexAPIKey, tt.wantAPIKey)
			}
			if cfg.ServiceAuthSecret != tt.wantAuth {
				t.Errorf("cfg.ServiceAuthSecret = %q, want %q", cfg.ServiceAuthSecret, tt.wantAuth)
			}
			if cfg.S3BucketDocuments != tt.wantBucket {
				t.Errorf("cfg.S3BucketDocuments = %q, want %q", cfg.S3BucketDocuments, tt.wantBucket)
			}
			if cfg.S3KMSKeyID != tt.wantKMS {
				t.Errorf("cfg.S3KMSKeyID = %q, want %q", cfg.S3KMSKeyID, tt.wantKMS)
			}
		})
	}
}

func TestLoadWithLookupRuntimeIntegrations(t *testing.T) {
	t.Parallel()

	type runtimeFixture struct {
		OnboardingServiceURL    string `json:"onboarding_service_url"`
		OnboardingServiceSecret string `json:"onboarding_service_secret"`
		S3BucketDocuments       string `json:"s3_bucket_documents"`
		S3KMSKeyID              string `json:"s3_kms_key_id"`
	}
	fixtureData, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "approved-runtime-config.json"))
	if err != nil {
		t.Fatalf("ReadFile(shared runtime fixture) error = %v", err)
	}
	var fixture runtimeFixture
	if err := json.Unmarshal(fixtureData, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(shared runtime fixture) error = %v", err)
	}
	lookup := func(key string) string {
		return map[string]string{
			"ONBOARDING_SERVICE_SECRET": fixture.OnboardingServiceSecret,
			"S3_BUCKET_DOCUMENTS":       fixture.S3BucketDocuments,
			"S3_KMS_KEY_ID":             fixture.S3KMSKeyID,
		}[key]
	}
	cfg, err := config.LoadWithLookup(lookup)
	if err != nil {
		t.Fatalf("LoadWithLookup() with shared approved-runtime fixture error = %v", err)
	}
	if cfg.ServiceAuthSecret != fixture.OnboardingServiceSecret {
		t.Errorf("LoadWithLookup() ServiceAuthSecret = %q, want shared fixture secret", cfg.ServiceAuthSecret)
	}
	if cfg.S3BucketDocuments != fixture.S3BucketDocuments {
		t.Errorf("LoadWithLookup() S3BucketDocuments = %q, want %q", cfg.S3BucketDocuments, fixture.S3BucketDocuments)
	}
	if cfg.S3KMSKeyID != fixture.S3KMSKeyID {
		t.Errorf("LoadWithLookup() S3KMSKeyID = %q, want %q", cfg.S3KMSKeyID, fixture.S3KMSKeyID)
	}
	if fixture.OnboardingServiceURL == "" {
		t.Fatal("shared runtime fixture is missing the Node onboarding service URL")
	}

	tests := []struct {
		name    string
		env     map[string]string
		wantErr error
	}{
		{
			name:    "rejects_short_auth_secret",
			env:     map[string]string{"ONBOARDING_SERVICE_SECRET": "short-secret"},
			wantErr: config.ErrInvalidAuthSecret,
		},
		{
			name:    "rejects_bucket_without_kms_key",
			env:     map[string]string{"S3_BUCKET_DOCUMENTS": "fixture-documents-bucket"},
			wantErr: config.ErrMissingKMSKey,
		},
		{
			name:    "rejects_kms_key_without_bucket",
			env:     map[string]string{"S3_KMS_KEY_ID": fixture.S3KMSKeyID},
			wantErr: config.ErrMissingDocumentsBucket,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(key string) string { return tt.env[key] }
			_, err := config.LoadWithLookup(lookup)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("LoadWithLookup() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
