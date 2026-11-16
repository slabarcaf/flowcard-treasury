package main

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var apiVersionPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

func inspectWebhook(ctx context.Context, api *sandboxAPI, cfg credentials, out io.Writer) error {
	if cfg.webhookID == "" || cfg.webhookSecret == "" {
		return errors.New("sandbox_webhook_not_configured")
	}
	raw, code, state := api.get(ctx, "/api/v1/webhooks/"+url.PathEscape(cfg.webhookID))
	if state != "VERIFIED" {
		return errors.New("sandbox_webhook_read_" + stateCode(state, code))
	}
	var webhook struct {
		ID      string   `json:"id"`
		URL     string   `json:"url"`
		Secret  string   `json:"secret"`
		Version string   `json:"version"`
		Events  []string `json:"events"`
	}
	if json.Unmarshal(raw, &webhook) != nil || webhook.ID != cfg.webhookID || !apiVersionPattern.MatchString(webhook.Version) {
		return errors.New("sandbox_webhook_identity_or_version_invalid")
	}
	target, err := url.Parse(webhook.URL)
	if err != nil || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return errors.New("sandbox_webhook_url_invalid")
	}
	events := []string{}
	for _, event := range webhook.Events {
		if strings.HasPrefix(event, "issuing.card_transaction") || strings.HasPrefix(event, "connected_account_transfer.") || strings.HasPrefix(event, "deposit.") || strings.HasPrefix(event, "global_account.") {
			events = append(events, event)
		}
	}
	sort.Strings(events)
	report := struct {
		Version               string   `json:"version"`
		SecretMatches         bool     `json:"secret_matches"`
		HTTPS                 bool     `json:"https"`
		ShortfallRouteMatches bool     `json:"shortfall_route_matches"`
		ServicingRouteMatches bool     `json:"servicing_route_matches"`
		Events                []string `json:"relevant_events"`
		Delivery              string   `json:"delivery"`
	}{webhook.Version, hmac.Equal([]byte(webhook.Secret), []byte(cfg.webhookSecret)), target.Scheme == "https", target.Path == "/webhooks/airwallex/shortfalls", target.Path == "/webhooks/airwallex/servicing", events, "UNVERIFIED"}
	if json.NewEncoder(out).Encode(report) != nil {
		return errors.New("sandbox_report_write_failed")
	}
	if !report.SecretMatches || !report.HTTPS {
		return errors.New("sandbox_webhook_configuration_mismatch")
	}
	return nil
}
