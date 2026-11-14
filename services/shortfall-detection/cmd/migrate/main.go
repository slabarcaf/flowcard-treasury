// Command migrate explicitly applies shortfall DDL through a privileged connection.
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
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/postgres"
)

func main() {
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		dbURL = strings.TrimSpace(os.Getenv("DATABASE_URL_HOST"))
	}
	if dbURL == "" {
		log.Print("shortfall migration requires DATABASE_URL or DATABASE_URL_HOST")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if !migrate(ctx, dbURL) {
		os.Exit(1)
	}
	log.Print("shortfall migrations applied")
}

func migrate(ctx context.Context, dbURL string) bool {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		// Connection errors may embed the DSN. Never print the underlying error.
		log.Print("shortfall migration database configuration invalid")
		return false
	}
	defer pool.Close()
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		// Migration errors expose only fixed operation reasons, never raw SQL errors.
		log.Print(err)
		return false
	}
	return true
}
