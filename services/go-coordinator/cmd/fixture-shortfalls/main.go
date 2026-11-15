// Command fixture-shortfalls exposes the selected underwriting bundle as a read-only DEMO source.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/fixturebundle"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/shortfall"
)

const (
	listenAddress        = "127.0.0.1:8092"
	configuredSourceBind = "flowcard-integration-20261114"
	shortfallIssuer      = "flowcard-node-proxy"
	shortfallAudience    = "flowcard-shortfall-service"
	shortfallSubject     = "shortfall-reader"
	listPageSize         = 25
	maxRequestBytes      = 8192
)

type pageCursor struct {
	RunID  string `json:"run_id"`
	Offset int    `json:"offset"`
}

type server struct {
	bundle fixturebundle.Bundle
	secret []byte
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("fixture-shortfalls", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "retained underwriting run manifest")
	if err := flags.Parse(os.Args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*manifestPath) == "" {
		return errors.New("usage: fixture-shortfalls --manifest PATH")
	}
	if strings.TrimSpace(os.Getenv("COORDINATOR_MODE")) != "DEMO" || strings.TrimSpace(os.Getenv("COORDINATOR_SOURCE_BINDING_ID")) != configuredSourceBind {
		return errors.New("fixture shortfall source requires the approved DEMO binding")
	}
	secret := os.Getenv("SHORTFALL_SERVICE_SECRET")
	if len(secret) < 32 {
		return errors.New("fixture shortfall source authentication configuration invalid")
	}
	bundle, err := fixturebundle.Load(*manifestPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddress)
	if err != nil {
		return errors.New("fixture shortfall adapter listener unavailable")
	}
	defer listener.Close()
	service := &server{bundle: bundle, secret: []byte(secret)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/shortfalls", service.list)
	mux.HandleFunc("GET /api/shortfalls/{id}", service.read)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
	})
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	provenanceHash := sha256.Sum256([]byte(bundle.Provenance))
	log.Printf("source_mode=DEMO read_only=true fixture_shortfalls=synthetic run_id=%s provenance_sha256=%s adapter=%s", bundle.Manifest.RunID, hex.EncodeToString(provenanceHash[:]), listenAddress)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_ = httpServer.Close()
		}
		<-done
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("fixture shortfall adapter stopped")
	}
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorize(w, r) {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	for key, values := range query {
		if (key != "outcome" && key != "cursor") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "INVALID_QUERY")
			return
		}
	}
	if values := query["outcome"]; len(values) != 1 || values[0] != "SHORTFALL_DETECTED" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	offset := 0
	if values := query["cursor"]; len(values) == 1 {
		offset, err = decodeCursor(values[0], s.bundle.Manifest.RunID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_CURSOR")
			return
		}
	}
	if offset > len(s.bundle.Records) {
		writeError(w, http.StatusBadRequest, "INVALID_CURSOR")
		return
	}
	end := offset + listPageSize
	if end > len(s.bundle.Records) {
		end = len(s.bundle.Records)
	}
	items := append([]shortfall.Record{}, s.bundle.Records[offset:end]...)
	var nextCursor *string
	if end < len(s.bundle.Records) {
		value := encodeCursor(pageCursor{RunID: s.bundle.Manifest.RunID, Offset: end})
		nextCursor = &value
	}
	writeJSON(w, http.StatusOK, shortfall.Page{Items: items, NextCursor: nextCursor})
}

func (s *server) read(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorize(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	id := r.PathValue("id")
	record, ok := s.bundle.ByID[id]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if verifyAssertion(r, s.secret, time.Now().UTC()) == 0 {
		return true
	}
	writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
	return false
}

func verifyAssertion(request *http.Request, secret []byte, now time.Time) int {
	values := request.Header.Values("Authorization")
	if len(secret) < 32 || len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return http.StatusUnauthorized
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer "))
	if len(token) > maxRequestBytes {
		return http.StatusUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return http.StatusUnauthorized
	}
	encoding := base64.RawURLEncoding.Strict()
	header, err := encoding.DecodeString(parts[0])
	if err != nil || !fixturebundle.ValidJSON(header) {
		return http.StatusUnauthorized
	}
	var headerClaims struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if decodeJSON(header, &headerClaims) != nil || headerClaims.Algorithm != "HS256" || headerClaims.Type != "JWT" {
		return http.StatusUnauthorized
	}
	signature, err := encoding.DecodeString(parts[2])
	if err != nil {
		return http.StatusUnauthorized
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return http.StatusUnauthorized
	}
	payload, err := encoding.DecodeString(parts[1])
	if err != nil || !fixturebundle.ValidJSON(payload) {
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
	if decodeJSON(payload, &claims) != nil || claims.Issuer != shortfallIssuer ||
		claims.Audience != shortfallAudience || claims.Subject != shortfallSubject ||
		claims.TokenID == "" || claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > 20 || claims.IssuedAt > now.Unix()+5 ||
		claims.ExpiresAt < now.Unix()-5 {
		return http.StatusUnauthorized
	}
	return 0
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("fixture_json_invalid")
	}
	return nil
}

func encodeCursor(cursor pageCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value, runID string) (int, error) {
	if len(value) == 0 || len(value) > 512 {
		return 0, errors.New("fixture_cursor_invalid")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || !fixturebundle.ValidJSON(data) {
		return 0, errors.New("fixture_cursor_invalid")
	}
	var cursor pageCursor
	if decodeJSON(data, &cursor) != nil || cursor.RunID != runID || cursor.Offset < 0 || encodeCursor(cursor) != value {
		return 0, errors.New("fixture_cursor_invalid")
	}
	return cursor.Offset, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
