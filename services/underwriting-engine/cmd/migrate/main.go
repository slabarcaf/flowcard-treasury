// Command migrate applies underwriting DDL using a privileged database URL.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/postgres"
)

func main() {
	dbURL := strings.TrimSpace(os.Getenv("UNDERWRITING_MIGRATION_DATABASE_URL"))
	if dbURL == "" {
		log.Print("underwriting migration requires UNDERWRITING_MIGRATION_DATABASE_URL")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Print("underwriting migration database configuration invalid")
		os.Exit(1)
	}
	defer pool.Close()
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Print(err)
		os.Exit(1)
	}
	log.Print("underwriting migrations applied")
}
