package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
)

func TestBuildHandlerDirectWebhookBoundary(t *testing.T) {
	for _, secret := range []string{"", "synthetic-provider-key"} {
		cfg := &config.Config{ServiceAuthSecret: "synthetic-internal-signing-key-long-enough", WebhookSecret: secret}
		if secret != "" {
			cfg.WebhookEnvironment = "sandbox"
		}
		handler, closeFn, err := buildHandler(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if closeFn != nil {
			defer closeFn()
		}
		public := httptest.NewRecorder()
		handler.ServeHTTP(public, httptest.NewRequest(http.MethodPost, "/webhooks/airwallex", strings.NewReader(`{}`)))
		if public.Code != 503 || public.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unconfigured public intake status=%d", public.Code)
		}
		internal := httptest.NewRecorder()
		handler.ServeHTTP(internal, httptest.NewRequest(http.MethodPost, "/api/webhooks/airwallex", strings.NewReader(`{}`)))
		if internal.Code != 401 {
			t.Fatalf("internal delivery lost JWT guard: %d", internal.Code)
		}
	}
}
