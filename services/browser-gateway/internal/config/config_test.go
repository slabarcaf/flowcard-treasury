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

func TestDemoWriteGate(t *testing.T) {
	c, err := (Config{}).WithFinancial("", "", "http://127.0.0.1:8085", "owned-reader-key-at-least-32-bytes-long")
	if err != nil {
		t.Fatal(err)
	}
	operator, customer := "owned-operator-key-at-least-32-bytes-long", "owned-customer-key-at-least-32-bytes-long"
	if _, err := c.WithDemoWrites("DEMO", operator, customer); err != nil {
		t.Fatal("valid local DEMO profile rejected")
	}
	for _, tt := range []struct{ mode, operator, customer string }{{"SANDBOX", operator, customer}, {"", operator, customer}, {"DEMO", operator, operator}, {"DEMO", operator, ""}, {"DEMO", c.ServicingSecret, customer}} {
		if _, err := c.WithDemoWrites(tt.mode, tt.operator, tt.customer); err == nil {
			t.Fatal("unsafe financial profile accepted")
		}
	}
	remote, err := (Config{}).WithFinancial("", "", "https://servicing.example", "owned-reader-key-at-least-32-bytes-long")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.WithDemoWrites("DEMO", operator, customer); err == nil {
		t.Fatal("remote financial commands enabled")
	}
}

func TestExplicitSandboxWriteProfile(t *testing.T) {
	reader := "fixture-reader-secret-0000000000000000000000"
	operator := "fixture-operator-secret-00000000000000000000"
	customer := "fixture-customer-secret-00000000000000000000"
	base, err := (Config{}).WithFinancial("", "", "http://127.0.0.1:18091", reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.WithDemoWrites("SANDBOX", operator, customer); err == nil {
		t.Fatal("old DEMO profile enabled sandbox")
	}
	valid, err := base.WithFinancialWrites("SANDBOX", operator, customer)
	if err != nil || valid.WriteMode != "SANDBOX" {
		t.Fatal("explicit sandbox profile rejected")
	}
	for _, tc := range []struct{ mode, operator, customer string }{
		{"PRODUCTION", operator, customer}, {"SANDBOX", reader, customer}, {"SANDBOX", operator, operator}, {"SANDBOX", "short", customer},
	} {
		if _, err := base.WithFinancialWrites(tc.mode, tc.operator, tc.customer); err == nil {
			t.Fatal("invalid source mode or role key accepted")
		}
	}
	if _, err := (Config{}).WithFinancialWrites("SANDBOX", operator, customer); err == nil {
		t.Fatal("unconfigured source enabled writes")
	}
}
