package config_test

import (
	"errors"
	"testing"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
)

func TestLoadWithLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		wantPort  int
		wantEnv   string
		wantDBURL string
		wantErr   error
	}{
		{
			name:      "defaults_applied_when_environment_is_empty",
			env:       map[string]string{},
			wantPort:  config.DefaultPort,
			wantEnv:   "development",
			wantDBURL: "",
		},
		{
			name: "custom_port_and_database_url",
			env: map[string]string{
				"PORT":         "9090",
				"DATABASE_URL": "postgresql://localhost:5432/testdb",
				"NODE_ENV":     "production",
			},
			wantPort:  9090,
			wantEnv:   "production",
			wantDBURL: "postgresql://localhost:5432/testdb",
		},
		{
			name: "trims_whitespace_from_variables",
			env: map[string]string{
				"PORT":         "  8085  ",
				"DATABASE_URL": "  postgresql://localhost/flowcard  ",
				"NODE_ENV":     "  staging  ",
			},
			wantPort:  8085,
			wantEnv:   "staging",
			wantDBURL: "postgresql://localhost/flowcard",
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
		})
	}
}
