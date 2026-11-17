package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// relayShortfallEvidence forwards the original provider-authenticated delivery
// after local setup, preserving its body, timestamp, signature and dedupe ID.
// It cannot manufacture observations or change the provider payment.
func relayShortfallEvidence(ctx context.Context, manifest, runID string) error {
	var capture captureSubscriptions
	if err := readPrivateJSON(filepath.Join(filepath.Dir(manifest), "capture-subscriptions.json"), &capture, 65536); err != nil {
		return err
	}
	if capture.Owner != manifestOwner || capture.RunID != runID || capture.Environment != "SANDBOX" {
		return errors.New("sandbox_relay_owner_mismatch")
	}
	subscription := capture.Subscriptions["SHORTFALL"]
	if subscription.Secret == "" {
		return errors.New("sandbox_relay_secret_unavailable")
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(manifest), "webhook-evidence"))
	if err != nil {
		return errors.New("sandbox_relay_evidence_unavailable")
	}
	count := 0
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var record struct {
			RunID     string `json:"run_id"`
			Source    string `json:"source"`
			Timestamp string `json:"x_timestamp"`
			Signature string `json:"x_signature"`
			Raw       []byte `json:"raw_body_base64"`
		}
		if err := readPrivateJSON(filepath.Join(filepath.Dir(manifest), "webhook-evidence", entry.Name()), &record, 16*1024*1024); err != nil {
			return err
		}
		if record.RunID != runID || record.Source != "SHORTFALL" {
			continue
		}
		signature, err := hex.DecodeString(record.Signature)
		if err != nil {
			return errors.New("sandbox_relay_signature_invalid")
		}
		mac := hmac.New(sha256.New, []byte(subscription.Secret))
		mac.Write([]byte(record.Timestamp))
		mac.Write(record.Raw)
		if !hmac.Equal(signature, mac.Sum(nil)) {
			return errors.New("sandbox_relay_signature_invalid")
		}
		var envelope struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(record.Raw, &envelope) != nil || envelope.Version != "2026-08-21" {
			return errors.New("sandbox_relay_schema_unverified")
		}
		request, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:18082/webhooks/airwallex/shortfalls", bytes.NewReader(record.Raw))
		if err != nil {
			return errors.New("sandbox_relay_request_invalid")
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Timestamp", record.Timestamp)
		request.Header.Set("X-Signature", record.Signature)
		response, err := client.Do(request)
		if err != nil {
			return errors.New("sandbox_relay_receiver_unavailable")
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			return errors.New("sandbox_relay_receiver_refused")
		}
		count++
	}
	if count != 2 {
		return errors.New("sandbox_relay_expected_two_observed_deliveries")
	}
	return nil
}
