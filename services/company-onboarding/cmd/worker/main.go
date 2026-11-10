// Package main provides the background worker entrypoint for company-onboarding operations.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("worker process terminated with error: %v", err)
	}
}

// run executes the worker lifecycle, cleanly shutting down when ctx is cancelled.
// Production platform and storage adapters fail-closed by default with ErrUnconfigured.
// In accordance with least-privilege security boundaries, runtime workers do NOT execute DDL.
// If the database is unreachable or schema migrations are missing, startup fails immediately.
func run(ctx context.Context, cfg *config.Config) error {
	log.Printf("starting company-onboarding worker process (%s)", cfg.Environment)

	// Adapters fail-closed by default; production execution requires explicit external credentials.
	platformClient := airwallex.NewUnconfiguredClient("production Airwallex credentials unconfigured; live calls disabled")
	storageStore := storage.NewUnavailableStore("production object store unconfigured; live access disabled")

	var subWorker *worker.Worker
	var whProcessor *webhooks.Processor

	if strings.TrimSpace(cfg.DatabaseURL) != "" {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("DATABASE_CONNECT_FAILED: %w", postgres.SanitizeDBError(err))
		}
		defer pool.Close()

		if err := postgres.VerifySchemaReadiness(ctx, pool); err != nil {
			return fmt.Errorf("DATABASE_SCHEMA_FAILED: %w", err)
		}

		repo := postgres.NewRepository(pool)
		subWorker = worker.NewWorker(repo, storageStore, platformClient, worker.Config{
			LeaseDuration: 30 * time.Second,
			RetryBackoff:  5 * time.Second,
		})
		whProcessor = webhooks.NewProcessor(repo, platformClient)
	} else {
		log.Println("DATABASE_URL not configured; worker running idle / fail-closed")
	}

	jobTicker := time.NewTicker(2 * time.Second)
	defer jobTicker.Stop()

	webhookTicker := time.NewTicker(3 * time.Second)
	defer webhookTicker.Stop()

	reconTicker := time.NewTicker(30 * time.Second)
	defer reconTicker.Stop()

	log.Printf("worker engines initialized (job worker, webhook processor, periodic reconciler)")

	for {
		select {
		case <-ctx.Done():
			log.Printf("worker process stopped cleanly")
			return nil
		case <-jobTicker.C:
			_, _ = subWorker.ProcessNextJob(ctx)
		case <-webhookTicker.C:
			_, _ = whProcessor.ProcessPending(ctx)
		case <-reconTicker.C:
			_, _ = whProcessor.ReconcileAccounts(ctx)
		}
	}
}
