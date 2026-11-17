package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWebhookInspectionDoesNotExposePrivateConfiguration(t *testing.T) {
	cfg := credentials{webhookID: "private-webhook", webhookSecret: "private-secret"}
	api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/webhooks/private-webhook" {
			t.Fatal("unexpected webhook call")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"private-webhook","secret":"private-secret","url":"https://private-host.example/webhooks/airwallex/shortfalls","version":"2026-08-21","events":["issuing.card_transaction.declined"]}`))}, nil
	})}}
	var out strings.Builder
	if err := inspectWebhook(context.Background(), api, cfg, &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{cfg.webhookID, cfg.webhookSecret, "private-host.example"} {
		if strings.Contains(out.String(), s) {
			t.Fatal("private webhook config leaked")
		}
	}
	if !strings.Contains(out.String(), `"shortfall_route_matches":true`) || !strings.Contains(out.String(), `"delivery":"UNVERIFIED"`) {
		t.Fatal("incorrect delivery claim")
	}
}
