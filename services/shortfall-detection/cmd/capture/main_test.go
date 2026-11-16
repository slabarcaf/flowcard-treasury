package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCaptureUsesProductionSignaturesAndPersistsOnlyOwnedEvents(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(dir, "subscriptions.json")
	output := filepath.Join(dir, "evidence")
	runID := "fc-sbx-12345678"
	secret := "private-signing-key"
	cfg := captureConfig{Owner: owner, RunID: runID, Environment: "SANDBOX", Accounts: []string{"owned-a", "owned-b"}}
	cfg.Subscriptions = map[string]struct {
		ID      string `json:"id"`
		Secret  string `json:"secret"`
		Version string `json:"version"`
	}{"SHORTFALL": {"test-subscription", secret, "2026-08-21"}}
	rawConfig, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, rawConfig, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := captureHandler(configFile, runID, output)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("{\n  \"id\":\"event-1\", \"name\":\"issuing.card_transaction.declined\", \"account_id\":\"owned-b\"\n}")
	send := func(path string, payload []byte, valid bool) int {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp))
		mac.Write(payload)
		signature := hex.EncodeToString(mac.Sum(nil))
		if !valid {
			signature = strings.Repeat("0", 64)
		}
		request.Header.Set("X-Timestamp", timestamp)
		request.Header.Set("X-Signature", signature)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), "owned-b") {
			t.Fatal("private response value leaked")
		}
		return response.Code
	}
	if code := send("/api/session/login", body, true); code != 404 {
		t.Fatalf("unexpected public route %d", code)
	}
	if code := send("/webhooks/airwallex/shortfalls", body, false); code != 401 {
		t.Fatalf("forged signature status %d", code)
	}
	if code := send("/webhooks/airwallex/shortfalls", body, true); code != 200 {
		t.Fatalf("valid status %d", code)
	}
	if code := send("/webhooks/airwallex/shortfalls", body, true); code != 200 {
		t.Fatalf("duplicate status %d", code)
	}
	other := bytes.ReplaceAll(body, []byte("owned-b"), []byte("other-account"))
	if code := send("/webhooks/airwallex/shortfalls", other, true); code != 200 {
		t.Fatalf("unowned event status %d", code)
	}
	changed := bytes.ReplaceAll(body, []byte("declined"), []byte("changed"))
	if code := send("/webhooks/airwallex/shortfalls", changed, true); code != 500 {
		t.Fatalf("conflicting event status %d", code)
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 1 {
		t.Fatalf("wrong stored event count: %d", len(entries))
	}
	path := filepath.Join(output, entries[0].Name())
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("capture is not private")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var evidence capturedDelivery
	if json.Unmarshal(raw, &evidence) != nil || !bytes.Equal(evidence.RawBody, body) || evidence.RunID != runID || evidence.Signature == "" || evidence.Timestamp == "" {
		t.Fatal("original signed evidence not preserved")
	}
}

func TestCaptureRejectsPublicListenerAndForeignConfiguration(t *testing.T) {
	if err := run(context.Background(), []string{"--http-addr", "0.0.0.0:18095"}); err == nil {
		t.Fatal("public listener accepted")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "subscriptions.json")
	if err := os.WriteFile(path, []byte(`{"owner":"foreign","run_id":"fc-sbx-12345678","environment":"SANDBOX"}`), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := captureHandler(path, "fc-sbx-12345678", filepath.Join(dir, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/webhooks/airwallex/shortfalls", strings.NewReader(`{}`)))
	if response.Code != 503 {
		t.Fatal("foreign configuration accepted")
	}
}
