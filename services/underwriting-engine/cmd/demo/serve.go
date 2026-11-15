package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
)

const demoShortfallAddress = "127.0.0.1:8084"

func runServeShortfalls(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve-shortfalls", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "prepared DEMO manifest path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *manifestPath == "" {
		return errors.New("usage: demo serve-shortfalls --manifest PATH")
	}
	if strings.TrimSpace(os.Getenv("UNDERWRITING_MODE")) != "DEMO" {
		return errors.New("shortfall mock requires UNDERWRITING_MODE=DEMO")
	}
	secret := os.Getenv("SHORTFALL_SERVICE_SECRET")
	if len(secret) < 32 {
		return errors.New("shortfall mock configuration invalid")
	}
	bundleRoot, manifest, err := loadGeneratedManifest(*manifestPath)
	if err != nil {
		return err
	}
	if manifest.SourceMode != "DEMO" {
		return errors.New("shortfall mock requires DEMO manifest")
	}
	shortfallPath, err := artifactPath(bundleRoot, manifest.ShortfallsFile)
	if err != nil {
		return err
	}
	data, err := readLimitedFile(shortfallPath, fixtureMaxBytes)
	if err != nil {
		return err
	}
	var fixture shortfallFixturePack
	if decodeStrictJSON(data, &fixture) != nil || fixture.SourceMode != "DEMO" ||
		fixture.Provenance == "" || len(fixture.Records) != len(manifest.Scenarios) {
		return errors.New("shortfall fixture pack invalid")
	}
	records := make(map[string][]byte, len(fixture.Records))
	expectedScenarios := make(map[string]generatedScenario, len(manifest.Scenarios))
	for _, scenario := range manifest.Scenarios {
		expectedScenarios[scenario.ID] = scenario
	}
	for _, item := range fixture.Records {
		scenario, exists := expectedScenarios[item.ScenarioID]
		if !exists || item.Response.ID != scenario.ShortfallID || !domain.ValidID(item.Response.ID) ||
			item.Response.CustomerID == nil || item.Response.CardID == nil || item.Response.Currency == nil ||
			item.Response.TransactionID == nil || item.Response.CanonicalEventID != scenario.CanonicalEventID ||
			*item.Response.CustomerID != scenario.CustomerID || item.Response.AccountID != scenario.AccountID ||
			*item.Response.CardID != scenario.CardID || *item.Response.TransactionID != scenario.TransactionID ||
			*item.Response.Currency != "USD" ||
			item.Response.Outcome != "SHORTFALL_DETECTED" || item.Response.AttemptedAmountCents == nil ||
			item.Response.AvailableBalanceCents == nil || item.Response.ShortfallCents == nil ||
			*item.Response.AttemptedAmountCents != 900_000 ||
			*item.Response.AvailableBalanceCents != 200_000 ||
			*item.Response.ShortfallCents != scenario.PrincipalCents ||
			item.Response.AmountSemantics != "current_need_at_balance_observation" || !item.Response.RequiresRecheck {
			return errors.New("shortfall fixture pack invalid")
		}
		body, err := json.Marshal(item.Response)
		if err != nil {
			return errors.New("shortfall fixture pack invalid")
		}
		if _, exists := records[item.Response.ID]; exists {
			return errors.New("shortfall fixture pack invalid")
		}
		records[item.Response.ID] = body
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/shortfalls/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		status := verifyShortfallAssertion(r, []byte(secret), time.Now().UTC())
		if status != 0 {
			message := "unauthorized"
			if status == http.StatusForbidden {
				message = "forbidden"
			}
			writeMockError(w, status, message)
			return
		}
		if r.URL.RawQuery != "" {
			writeMockError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		id := r.PathValue("id")
		body, found := records[id]
		if !domain.ValidID(id) || !found {
			writeMockError(w, http.StatusNotFound, "not_found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeMockError(w, http.StatusNotFound, "not_found")
	})
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", demoShortfallAddress)
	if err != nil {
		return errors.New("shortfall mock listener unavailable")
	}
	defer listener.Close()
	server := &http.Server{
		Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 * 1024,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("source_mode=DEMO synthetic shortfall mock listening at http://%s", demoShortfallAddress)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
		<-done
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("shortfall mock unavailable")
	}
}

func verifyShortfallAssertion(request *http.Request, secret []byte, now time.Time) int {
	values := request.Header.Values("Authorization")
	if len(secret) < 32 || len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return http.StatusUnauthorized
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer "))
	if len(token) > 8192 {
		return http.StatusUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return http.StatusUnauthorized
	}
	encoding := base64.RawURLEncoding.Strict()
	headerBytes, err := encoding.DecodeString(parts[0])
	if err != nil || !validJSONObjects(headerBytes) {
		return http.StatusUnauthorized
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if decodeStrictJSON(headerBytes, &header) != nil || header.Algorithm != "HS256" ||
		(header.Type != "" && header.Type != "JWT") {
		return http.StatusUnauthorized
	}
	signature, err := encoding.DecodeString(parts[2])
	if err != nil {
		return http.StatusUnauthorized
	}
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(parts[0] + "." + parts[1])); err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return http.StatusUnauthorized
	}
	payload, err := encoding.DecodeString(parts[1])
	if err != nil || !validJSONObjects(payload) {
		return http.StatusUnauthorized
	}
	var claims struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		TokenID   string `json:"jti"`
	}
	if decodeStrictJSON(payload, &claims) != nil || claims.Issuer != "flowcard-node-proxy" ||
		claims.Subject == "" || claims.TokenID == "" || claims.IssuedAt <= 0 ||
		claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > 30 ||
		claims.IssuedAt > now.Unix()+5 || claims.ExpiresAt < now.Unix()-5 {
		return http.StatusUnauthorized
	}
	if claims.Audience != "flowcard-shortfall-service" || claims.Subject != "shortfall-reader" {
		return http.StatusForbidden
	}
	return 0
}

func writeMockError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
