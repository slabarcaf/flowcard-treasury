// Package main provides a test server launcher for integration and end-to-end test harnesses.
// It allows test harnesses to inject authenticators and test configurations without
// exposing secret-holding command-line flags or unapproved environment variables in
// the production cmd/api service.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
	internalHttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

func main() {
	portFlag := flag.Int("port", 0, "HTTP port for test server")
	authSecretFlag := flag.String("auth-secret", "", "Test-only secret for authenticators (test harness injection only)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	if *portFlag > 0 {
		cfg.Port = *portFlag
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var opAuth internalHttp.Authenticator = internalHttp.DenyAllAuthenticator{}
	var whAuth internalHttp.Authenticator = internalHttp.DenyAllAuthenticator{}

	if cleanSecret := strings.TrimSpace(*authSecretFlag); len(cleanSecret) >= 32 {
		if a, err := internalHttp.NewOperatorAuthenticator([]byte(cleanSecret)); err == nil {
			opAuth = a
		}
		if wa, err := internalHttp.NewWebhookAuthenticator([]byte(cleanSecret)); err == nil {
			whAuth = wa
		}
	}

	var repo *postgres.Repository
	var pool *pgxpool.Pool
	if strings.TrimSpace(cfg.DatabaseURL) != "" {
		p, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("DATABASE_CONNECT_FAILED: testserver failed to connect to database: %v", postgres.SanitizeDBError(err))
		}
		pool = p
		defer pool.Close()
		repo = postgres.NewRepository(pool)
	}

	gate := validation.UnverifiedPlatformContractGate{}
	store := storage.NewUnavailableStore("test storage unconfigured")
	server := internalHttp.NewServer(repo, opAuth, gate, store)

	if repo != nil {
		whHandler := webhooks.NewHandler(repo, nil)
		server.WithWebhookHandler(whHandler, whAuth)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatalf("testserver failed to listen: %v", err)
	}
	defer listener.Close()

	srv := &http.Server{
		Handler:           server.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("testserver terminated: %v", err)
	}
}
