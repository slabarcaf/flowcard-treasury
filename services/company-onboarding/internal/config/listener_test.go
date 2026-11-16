package config_test

import (
	"testing"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
)

func TestOnboardingHTTPHost(t *testing.T) {
	for _, tc := range []struct {
		host  string
		valid bool
	}{{"", true}, {"127.0.0.1", true}, {"::1", true}, {"localhost", true}, {"0.0.0.0", true}, {"127.0.0.1:8080", false}, {"https://example.com", false}} {
		cfg, err := config.LoadWithLookup(func(k string) string {
			if k == "ONBOARDING_HTTP_HOST" {
				return tc.host
			}
			return ""
		})
		if (err == nil) != tc.valid {
			t.Fatalf("host validity mismatch for %q", tc.host)
		}
		if err == nil && cfg.HTTPHost != tc.host {
			t.Fatal("bind host was changed")
		}
	}
}
