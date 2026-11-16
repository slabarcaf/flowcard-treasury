package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedIngressExposesOnlyTheTwoWebhookRoutes(t *testing.T) {
	handler := webhookIngress()
	for _, path := range []string{"/api/session/login", "/api/offers", "/readyz", "/", "/webhooks/airwallex/shortfalls/extra"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 404 {
			t.Fatalf("unexpected public route %s: %d", path, response.Code)
		}
	}
	for _, path := range []string{"/webhooks/airwallex/shortfalls", "/webhooks/airwallex/servicing"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 405 || response.Header().Get("Allow") != "POST" {
			t.Fatal("webhook route accepted wrong method")
		}
	}
}

func TestChildDiagnosticsRedactCredentialsBeforeDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.log")
	done := make(chan struct{}, 1)
	safeChildLog(strings.NewReader("failure with private-password and postgres://private-url\n"), path, []string{"private-password", "postgres://private-url"}, done)
	<-done
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-password") || strings.Contains(string(raw), "postgres://private-url") || !strings.Contains(string(raw), "[redacted]") {
		t.Fatal("credentials reached log")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("log file is not private")
	}
}
