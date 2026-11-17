package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

// cleanupWebhooks removes only this run's two temporary subscriptions. Accounts,
// cards, receiving instruments, funds and protected audit evidence are retained.
func cleanupWebhooks(ctx context.Context, api *sandboxAPI, cfg credentials, manifest, runID, originFile string) error {
	origin, err := readCallbackOrigin(originFile)
	if err != nil {
		return err
	}
	for _, source := range []string{"shortfall", "servicing"} {
		name := "webhook-" + source
		intent, err := savedIntent(manifest, runID, cfg.platform, name)
		if err != nil {
			return err
		}
		if intent.ResourceID == "" || intent.ResourceID == cfg.webhookID {
			return errors.New("sandbox_cleanup_webhook_not_owned")
		}
		route := "/webhooks/airwallex/shortfalls"
		if source == "servicing" {
			route = "/webhooks/airwallex/servicing"
		}
		expectedURL := origin + route
		cleanupName := "cleanup-" + name
		deletion, err := savedIntent(manifest, runID, cfg.platform, cleanupName)
		if err != nil {
			return err
		}
		raw, code, state := api.get(ctx, "/api/v1/webhooks/"+url.PathEscape(intent.ResourceID))
		if code == 404 && deletion.RequestID != "" {
			if err := markWebhookDeleted(manifest, runID, cfg.platform, name, cleanupName); err != nil {
				return err
			}
			continue
		}
		var found ownedWebhook
		if state != "VERIFIED" || json.Unmarshal(raw, &found) != nil || found.ID != intent.ResourceID || found.RequestID != intent.RequestID || found.RequestID != requestIDFor(runID, name) || found.URL != expectedURL || found.Version != "2026-08-21" {
			return errors.New("sandbox_cleanup_webhook_binding_mismatch")
		}
		if deletion.RequestID != "" {
			return errors.New("sandbox_cleanup_webhook_unknown_read_only")
		}
		payload, _ := json.Marshal(struct{ ID, URL string }{found.ID, found.URL})
		if err := withManifest(manifest, runID, cfg.platform, false, func(m *sandboxManifest) error {
			return reserveIntent(m, cleanupName, requestIDFor(runID, cleanupName), payload)
		}); err != nil {
			return err
		}
		_, _, _ = api.post(ctx, "/api/v1/webhooks/"+url.PathEscape(found.ID)+"/delete", nil)
		_, code, _ = api.get(ctx, "/api/v1/webhooks/"+url.PathEscape(found.ID))
		if code != 404 {
			return errors.New("sandbox_cleanup_webhook_deletion_unverified")
		}
		if err := markWebhookDeleted(manifest, runID, cfg.platform, name, cleanupName); err != nil {
			return err
		}
	}
	return nil
}

func markWebhookDeleted(manifest, runID, platform, name, cleanupName string) error {
	return withManifest(manifest, runID, platform, false, func(m *sandboxManifest) error {
		for _, key := range []string{name, cleanupName} {
			intent := m.Intents[key]
			if intent.RequestID == "" {
				return errors.New("sandbox_cleanup_webhook_intent_missing")
			}
			intent.State = "DELETED"
			m.Intents[key] = intent
		}
		return nil
	})
}
