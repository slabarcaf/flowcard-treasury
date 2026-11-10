// Package main provides the explicit, separately provisioned schema migration CLI
// for the company-onboarding subsystem.
//
// In accordance with least-privilege security boundaries, runtime API and worker
// processes do NOT execute DDL. Database migrations must be executed explicitly
// using a privileged connection.
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
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
)

func main() {
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		dbURL = strings.TrimSpace(os.Getenv("DATABASE_URL_HOST"))
	}

	if dbURL == "" {
		log.Fatalf("error: migration requires a database connection URL (set DATABASE_URL or DATABASE_URL_HOST environment variable)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runMigrations(ctx, dbURL); err != nil {
		log.Fatalf("migration failed: %v", postgres.SanitizeDBError(err))
	}
	log.Println("all schema migrations (001-006) applied successfully")
}

func runMigrations(ctx context.Context, dbURL string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctxTimeout, dbURL)
	if err != nil {
		return fmt.Errorf("DATABASE_CONNECT_FAILED: %w", postgres.SanitizeDBError(err))
	}
	defer pool.Close()

	if err := pool.Ping(ctxTimeout); err != nil {
		return fmt.Errorf("DATABASE_PING_FAILED: %w", postgres.SanitizeDBError(err))
	}

	return postgres.RunMigrations(ctxTimeout, pool)
}
