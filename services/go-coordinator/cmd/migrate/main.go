// Command migrate applies coordinator schema changes using a privileged database URL.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(url) == "" {
		log.Fatal("migration requires DATABASE_URL")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	pool, err := postgres.OpenPrivilegedPool(ctx, url)
	if err != nil {
		log.Fatalf("migration connection failed: %v", err)
	}
	defer pool.Close()
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}
