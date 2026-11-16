package config_test

import (
	"strings"
	"testing"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
)

func TestProviderWebhookConfiguration(t *testing.T) {
	t.Run("distinct service secret", func(t *testing.T) {
		secret := strings.Repeat("k", 32)
		env := map[string]string{"ONBOARDING_WEBHOOK_SECRET": secret, "ONBOARDING_WEBHOOK_ENVIRONMENT": "sandbox", "ONBOARDING_SERVICE_SECRET": secret}
		if _, err := config.LoadWithLookup(func(k string) string { return env[k] }); err == nil {
			t.Fatal("provider and internal credentials may be reused")
		}
	})
	for _, test := range []struct {
		name, secret, environment string
		valid                     bool
	}{
		{"disabled", "", "", true}, {"sandbox", "provider-key", "sandbox", true}, {"production", "provider-key", "production", true},
		{"missing environment", "provider-key", "", false}, {"missing secret", "", "sandbox", false}, {"invalid scope", "provider-key", "development", false}, {"blank key", " ", "sandbox", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := map[string]string{"ONBOARDING_WEBHOOK_SECRET": test.secret, "ONBOARDING_WEBHOOK_ENVIRONMENT": test.environment, "AIRWALLEX_WEBHOOK_SECRET": "legacy-does-not-enable-new-intake"}
			cfg, err := config.LoadWithLookup(func(k string) string { return env[k] })
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
			if err == nil && cfg.WebhookSecret != test.secret {
				t.Fatal("secret altered or legacy value reused")
			}
		})
	}
}
