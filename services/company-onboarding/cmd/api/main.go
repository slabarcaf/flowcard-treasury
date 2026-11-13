// Package main provides the HTTP API entrypoint for the company-onboarding service.
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
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/apidocs"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
	internalHttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

func main() {
	portFlag := flag.Int("port", 0, "HTTP port for company-onboarding API server (overrides PORT environment variable)")
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

	if err := run(ctx, cfg); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("api server terminated with error: %v", err)
	}
}

func run(ctx context.Context, cfg *config.Config) error {
	// Build handler and verify database connectivity/schema BEFORE opening the TCP listener.
	// This prevents leaking an open network port if database or configuration validation fails.
	handler, closeFn, err := buildHandler(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to build handler: %w", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", cfg.Port, err)
	}
	defer listener.Close()

	log.Printf("company-onboarding API server listening on :%d (%s)", cfg.Port, cfg.Environment)
	return runWithListener(ctx, cfg, listener, handler, 5*time.Second)
}

// buildHandler constructs the production HTTP handler with fail-closed defaults.
func buildHandler(ctx context.Context, cfg *config.Config) (http.Handler, func(), error) {
	return buildHandlerWithAuth(ctx, cfg, nil, nil)
}

// buildHandlerWithAuth constructs the HTTP handler, allowing test harnesses to inject authenticators.
// In accordance with least-privilege security, runtime services do NOT execute schema DDL.
// If DATABASE_URL is configured, connectivity and schema readiness are verified on startup;
// if the database is unreachable or schema migrations are missing, startup fails immediately.
func buildHandlerWithAuth(ctx context.Context, cfg *config.Config, opAuth internalHttp.Authenticator, whAuth internalHttp.Authenticator) (http.Handler, func(), error) {
	return buildHandlerWithDependencies(ctx, cfg, opAuth, whAuth, nil)
}

// buildHandlerWithDependencies constructs the HTTP handler allowing injection of auth and storage adapters.
func buildHandlerWithDependencies(ctx context.Context, cfg *config.Config, opAuth internalHttp.Authenticator, whAuth internalHttp.Authenticator, store storage.ObjectStore) (http.Handler, func(), error) {
	var repo *postgres.Repository
	var closeFn func()

	if strings.TrimSpace(cfg.DatabaseURL) != "" {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, nil, fmt.Errorf("DATABASE_CONNECT_FAILED: %w", postgres.SanitizeDBError(err))
		}

		// Verify schema readiness without modifying database or executing DDL
		if err := postgres.VerifySchemaReadiness(ctx, pool); err != nil {
			pool.Close()
			return nil, nil, fmt.Errorf("DATABASE_SCHEMA_FAILED: %w", err)
		}

		closeFn = func() { pool.Close() }
		repo = postgres.NewRepository(pool)
	} else {
		log.Println("DATABASE_URL not configured; repository running unconfigured (endpoints return safe 503)")
	}

	// 1. Authenticator: configure from ServiceAuthSecret if set, or default to DenyAll (fails closed)
	if opAuth == nil {
		if cfg.ServiceAuthSecret != "" {
			var err error
			opAuth, err = internalHttp.NewOperatorAuthenticator([]byte(cfg.ServiceAuthSecret))
			if err != nil {
				if closeFn != nil {
					closeFn()
				}
				return nil, nil, fmt.Errorf("failed to initialize operator authenticator: %w", err)
			}
		} else {
			opAuth = internalHttp.DenyAllAuthenticator{}
		}
	}
	if whAuth == nil {
		if cfg.ServiceAuthSecret != "" {
			var err error
			whAuth, err = internalHttp.NewWebhookAuthenticator([]byte(cfg.ServiceAuthSecret))
			if err != nil {
				if closeFn != nil {
					closeFn()
				}
				return nil, nil, fmt.Errorf("failed to initialize webhook authenticator: %w", err)
			}
		} else {
			whAuth = internalHttp.DenyAllAuthenticator{}
		}
	}

	// 2. Storage: configure from S3BucketDocuments if set, or default to unavailable store
	if store == nil {
		if cfg.S3BucketDocuments != "" && cfg.S3KMSKeyID != "" {
			region := strings.TrimSpace(cfg.AWSRegion)
			if region == "" {
				region = "us-west-2"
			}
			s3Store, err := storage.NewS3Store(storage.S3Config{
				Bucket:               cfg.S3BucketDocuments,
				ServerSideEncryption: "aws:kms",
				KMSKeyID:             cfg.S3KMSKeyID,
				Region:               region,
			})
			if err != nil {
				if closeFn != nil {
					closeFn()
				}
				return nil, nil, fmt.Errorf("failed to initialize S3 document store: %w", err)
			}
			store = s3Store
		} else {
			store = storage.NewUnavailableStore("production object store unconfigured; live upload/download disabled")
		}
	}

	// 3. Platform contract gate: external gates fail closed
	gate := validation.UnverifiedPlatformContractGate{}

	server := internalHttp.NewServer(repo, opAuth, gate, store)

	// 4. Webhook handler: wired if repository is available
	if repo != nil {
		whHandler := webhooks.NewHandler(repo, nil)
		server.WithWebhookHandler(whHandler, whAuth)
	}

	// 5. Documentation handler: public offline Swagger UI documentation
	server.WithDocsHandler(apidocs.Handler())

	return server.Routes(), closeFn, nil
}

func newHandler() http.Handler {
	server := internalHttp.NewServer(nil, internalHttp.DenyAllAuthenticator{}, validation.UnverifiedPlatformContractGate{}, storage.NewUnavailableStore("storage not configured"))
	return server.Routes()
}

func runWithListener(ctx context.Context, cfg *config.Config, listener net.Listener, handler http.Handler, shutdownTimeout time.Duration) error {
	if handler == nil {
		handler = newHandler()
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		} else {
			serverErr <- nil
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return <-serverErr
	case err := <-serverErr:
		return err
	}
}
