package main

import "net/http"

// webhookOnlyRoutes deliberately exposes no business, docs or internal JWT
// endpoints through a public callback tunnel. Normal API routing is unchanged.
func webhookOnlyRoutes(provider http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/airwallex/onboarding", provider)
	mux.HandleFunc("POST /webhooks/airwallex", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":"legacy_webhook_retired"}`))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}
