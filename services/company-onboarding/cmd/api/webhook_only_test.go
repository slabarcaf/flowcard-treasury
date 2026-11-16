package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

type callbackReceipt struct {
	raw   []byte
	calls int
}

func (s *callbackReceipt) InsertWebhookOnce(_ context.Context, item postgres.WebhookInboxItem) (bool, error) {
	s.raw = append([]byte(nil), item.RawPayload...)
	s.calls++
	return true, nil
}

func TestWebhookOnlyExposureRequiresNativeSignatureAndHidesBusinessRoutes(t *testing.T) {
	secret := []byte("fixture-notification-secret")
	repo := &callbackReceipt{}
	provider, err := webhooks.NewProviderHandler(repo, secret, "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	handler := webhookOnlyRoutes(provider)
	body := []byte(`{"id":"fixture-native-rfi","name":"rfi.action_required","account_id":"fixture-account","data":{"id":"fixture-rfi"}}`)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write(body)
	request := httptest.NewRequest("POST", "/webhooks/airwallex/onboarding", bytes.NewReader(body))
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || repo.calls != 1 || !bytes.Equal(repo.raw, body) {
		t.Fatal("native signed RFI body was not durably handed to receipt writer")
	}
	request = httptest.NewRequest("POST", "/webhooks/airwallex/onboarding", bytes.NewReader(body))
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", "invalid")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 || repo.calls != 1 {
		t.Fatal("invalid signature reached receipt storage")
	}
	for _, path := range []string{"/api/companies", "/api/webhooks/airwallex", "/api/v1/webhooks/airwallex", "/docs/api/"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", path, nil))
		if response.Code != 404 {
			t.Fatalf("private route %q exposed: %d", path, response.Code)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/webhooks/airwallex", bytes.NewReader(body)))
	if response.Code != 410 || repo.calls != 1 {
		t.Fatal("legacy mixed-owner callback was acknowledged")
	}
}

func TestWebhookOnlyStartupRefusesUnconfiguredPersistence(t *testing.T) {
	cfg := &config.Config{WebhookOnly: true, WebhookSecret: "fixture-notification-secret", WebhookEnvironment: "sandbox"}
	handler, closeFn, err := buildHandler(context.Background(), cfg)
	if err == nil || handler != nil {
		if closeFn != nil {
			closeFn()
		}
		t.Fatal("webhook-only process accepted missing database")
	}
}
