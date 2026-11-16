// Command migrate applies servicing DDL through a privileged database URL.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

func main() {
	databaseName := flag.String("database-name", "", "expected owned funding database name")
	flag.Parse()
	if flag.NArg() != 0 || !config.ValidOwnedDatabaseName(*databaseName, "flowcard") {
		log.Print("funding migration requires a safe --database-name")
		os.Exit(2)
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Print("funding migration requires DATABASE_URL")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Print("funding migration database configuration invalid")
		os.Exit(1)
	}
	defer pool.Close()
	if err := postgres.VerifyDatabaseName(ctx, pool, *databaseName); err != nil {
		log.Print(err)
		os.Exit(1)
	}
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Print(err)
		os.Exit(1)
	}
	log.Print("funding migrations applied")
}
