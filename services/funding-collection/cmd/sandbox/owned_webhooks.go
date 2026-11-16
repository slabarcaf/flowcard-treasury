package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type ownedWebhook struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Secret    string   `json:"secret"`
	Version   string   `json:"version"`
	RequestID string   `json:"request_id"`
	Events    []string `json:"events"`
}
type captureSubscription struct {
	ID      string `json:"id"`
	Secret  string `json:"secret"`
	Version string `json:"version"`
}
type captureSubscriptions struct {
	Owner         string                         `json:"owner"`
	Environment   string                         `json:"environment"`
	RunID         string                         `json:"run_id"`
	Accounts      []string                       `json:"accounts"`
	Subscriptions map[string]captureSubscription `json:"subscriptions"`
}

func writePrivateFile(path string, raw []byte) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return errors.New("sandbox_private_directory_required")
	}
	file, err := os.CreateTemp(dir, ".sandbox-private-*")
	if err != nil {
		return errors.New("sandbox_private_write_failed")
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return errors.New("sandbox_private_write_failed")
	}
	if file.Sync() != nil {
		file.Close()
		return errors.New("sandbox_private_write_failed")
	}
	if file.Close() != nil || os.Rename(name, path) != nil {
		return errors.New("sandbox_private_write_failed")
	}
	parent, err := os.Open(dir)
	if err != nil {
		return errors.New("sandbox_private_sync_failed")
	}
	defer parent.Close()
	if parent.Sync() != nil {
		return errors.New("sandbox_private_sync_failed")
	}
	return nil
}

func readCallbackOrigin(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("sandbox_callback_origin_file_unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return "", errors.New("sandbox_callback_origin_invalid")
	}
	origin := strings.TrimSpace(string(raw))
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.String() != origin {
		return "", errors.New("sandbox_callback_origin_invalid")
	}
	return origin, nil
}

func (a *sandboxAPI) findWebhook(ctx context.Context, requestID, target string) (ownedWebhook, bool, error) {
	var found ownedWebhook
	count := 0
	query := url.Values{"page_size": {"100"}}
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		raw, _, state := a.get(ctx, "/api/v1/webhooks?"+query.Encode())
		var result struct {
			Items []ownedWebhook `json:"items"`
			After string         `json:"page_after"`
		}
		if state != "VERIFIED" || json.Unmarshal(raw, &result) != nil || result.Items == nil {
			return found, false, errors.New("sandbox_webhook_lookup_unavailable")
		}
		for _, item := range result.Items {
			if item.RequestID == requestID && item.URL == target {
				found = item
				count++
			}
		}
		if count > 1 {
			return found, false, errors.New("sandbox_webhook_lookup_ambiguous")
		}
		if result.After == "" {
			return found, count == 1, nil
		}
		if seen[result.After] {
			return found, false, errors.New("sandbox_webhook_pagination_invalid")
		}
		seen[result.After] = true
		query.Set("page", result.After)
	}
	return found, false, errors.New("sandbox_webhook_lookup_incomplete")
}

func setupWebhooks(ctx context.Context, api *sandboxAPI, cfg credentials, path, runID, originFile string) error {
	origin, err := readCallbackOrigin(originFile)
	if err != nil {
		return err
	}
	capture := captureSubscriptions{Owner: manifestOwner, Environment: "SANDBOX", RunID: runID, Subscriptions: map[string]captureSubscription{}}
	for _, role := range []string{"funding", "borrower"} {
		scoped, err := ownedScopedAPI(ctx, api, cfg, path, runID, role)
		if err != nil {
			return err
		}
		capture.Accounts = append(capture.Accounts, scoped.scope)
	}
	for _, source := range []string{"SHORTFALL", "SERVICING"} {
		route := "/webhooks/airwallex/shortfalls"
		events := []string{"issuing.card_transaction.declined", "issuing.card_transaction_event.failed"}
		if source == "SERVICING" {
			route = "/webhooks/airwallex/servicing"
			events = []string{"connected_account_transfer.new", "connected_account_transfer.pending", "connected_account_transfer.settled", "connected_account_transfer.suspended", "connected_account_transfer.failed", "deposit.pending", "deposit.settled", "deposit.rejected", "deposit.reversed"}
		}
		target := origin + route
		name := "webhook-" + strings.ToLower(source)
		requestID := requestIDFor(runID, name)
		intent, err := savedIntent(path, runID, cfg.platform, name)
		if err != nil {
			return err
		}
		id := intent.ResourceID
		if id == "" {
			prior, found, err := api.findWebhook(ctx, requestID, target)
			if err != nil {
				return err
			}
			if found {
				if intent.RequestID == "" {
					return errors.New("sandbox_webhook_without_intent")
				}
				id = prior.ID
			} else {
				if intent.RequestID != "" {
					return errors.New("sandbox_webhook_unknown_reconcile_only")
				}
				payload, _ := json.Marshal(struct {
					URL       string   `json:"url"`
					Version   string   `json:"version"`
					Events    []string `json:"events"`
					RequestID string   `json:"request_id"`
				}{target, "2026-08-21", events, requestID})
				if err := withManifest(path, runID, cfg.platform, false, func(m *sandboxManifest) error { return reserveIntent(m, name, requestID, payload) }); err != nil {
					return err
				}
				raw, code, state := api.post(ctx, "/api/v1/webhooks/create", payload)
				if state != "RECEIVED" {
					return errors.New("sandbox_webhook_create_" + stateCode(state, code))
				}
				var created ownedWebhook
				if json.Unmarshal(raw, &created) != nil || created.ID == "" {
					return errors.New("sandbox_webhook_response_unknown")
				}
				id = created.ID
			}
		}
		raw, _, state := api.get(ctx, "/api/v1/webhooks/"+url.PathEscape(id))
		var verified ownedWebhook
		if state != "VERIFIED" || json.Unmarshal(raw, &verified) != nil || verified.ID != id || verified.URL != target || verified.Version != "2026-08-21" || verified.RequestID != requestID || verified.Secret == "" {
			return errors.New("sandbox_webhook_contract_unverified")
		}
		if len(verified.Events) != len(events) {
			return errors.New("sandbox_webhook_events_mismatch")
		}
		for _, expected := range events {
			found := false
			for _, actual := range verified.Events {
				if actual == expected {
					found = true
				}
			}
			if !found {
				return errors.New("sandbox_webhook_events_mismatch")
			}
		}
		if err := saveAccount(path, runID, cfg.platform, name, id); err != nil {
			return err
		}
		capture.Subscriptions[source] = captureSubscription{verified.ID, verified.Secret, verified.Version}
	}
	raw, err := json.MarshalIndent(capture, "", "  ")
	if err != nil {
		return errors.New("sandbox_capture_config_invalid")
	}
	return writePrivateFile(filepath.Join(filepath.Dir(path), "capture-subscriptions.json"), raw)
}
