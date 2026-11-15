package config

import "testing"

func TestCookieConfiguration(t *testing.T) {
	for _, tt := range []struct {
		address, origins, option string
		valid, secure            bool
	}{
		{"", "https://app.example", "", true, true},
		{"", "http://app.example", "", false, false},
		{"127.0.0.1:8094", "http://localhost:5173,http://127.0.0.1:5173", "true", true, false},
		{"[::1]:8094", "http://[::1]:5173", "true", true, false},
		{"0.0.0.0:8094", "http://localhost:5173", "true", false, false},
		{"127.0.0.1:8094", "http://localhost:5173,https://app.example", "true", false, false},
		{"", "", "", false, false},
		{"", "*", "", false, false},
		{"", "https://user:pass@app.example", "", false, false},
		{"", "https://app.example/", "", false, false},
		{"", "null", "", false, false},
		{"", "https://app.example", "yes", false, false},
	} {
		c, err := Parse(tt.address, "postgres://placeholder", tt.origins, tt.option)
		if (err == nil) != tt.valid || (err == nil && c.SecureCookie != tt.secure) {
			t.Errorf("Parse(%q,%q,%q): valid=%v secure=%v", tt.address, tt.origins, tt.option, err == nil, c.SecureCookie)
		}
	}
}

func TestOnboardingDestination(t *testing.T) {
	for _, tt := range []struct {
		upstream, secret string
		valid            bool
	}{
		{"", "", true},
		{"https://onboarding.example", "synthetic-service-secret-at-least-32-bytes", true},
		{"http://127.0.0.1:8080", "synthetic-service-secret-at-least-32-bytes", true},
		{"http://onboarding.example", "synthetic-service-secret-at-least-32-bytes", false},
		{"http://127.0.0.1:8080/prefix", "synthetic-service-secret-at-least-32-bytes", false},
		{"http://user:pass@127.0.0.1:8080", "synthetic-service-secret-at-least-32-bytes", false},
		{"http://127.0.0.1:8080?target=evil", "synthetic-service-secret-at-least-32-bytes", false},
		{"http://127.0.0.1:8080", "", false},
		{"", "synthetic-service-secret-at-least-32-bytes", false},
	} {
		if _, err := (Config{}).WithOnboarding(tt.upstream, tt.secret); (err == nil) != tt.valid {
			t.Errorf("WithOnboarding(%q) valid=%v, want %v", tt.upstream, err == nil, tt.valid)
		}
	}
}

func TestFinancialDestinations(t *testing.T) {
	secret := "owned-financial-reader-secret-at-least-32-bytes"
	for _, tt := range []struct {
		coordinator, key, servicing, reader string
		valid                               bool
	}{
		{"", "", "", "", true},
		{"http://127.0.0.1:8090", secret, "", "", true},
		{"", "", "https://servicing.example", secret, true},
		{"http://127.0.0.1:8090", "", "", "", false},
		{"", "", "", secret, false},
		{"http://external.example", secret, "", "", false},
		{"https://coordinator.example/prefix", secret, "", "", false},
		{"", "", "https://servicing.example?target=other", secret, false},
		{"", "", "https://servicing.example#fragment", secret, false},
	} {
		_, err := (Config{}).WithFinancial(tt.coordinator, tt.key, tt.servicing, tt.reader)
		if (err == nil) != tt.valid {
			t.Errorf("financial paired destination validation=%v, want %v", err == nil, tt.valid)
		}
	}
}
