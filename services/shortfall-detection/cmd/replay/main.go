// Command replay requeues eligible held inbox items using restricted SQL access.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/postgres"
)

func main() {
	id := flag.String("inbox-id", "", "held inbox UUID to requeue")
	reason := flag.String("reason", "", "required operator reason, at most 500 bytes")
	flag.Parse()
	if *id == "" || *reason == "" || flag.NArg() != 0 {
		log.Print("shortfall replay requires --inbox-id ID --reason TEXT")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := run(ctx, *id, *reason); err != nil {
		log.Print(err)
		os.Exit(1)
	}
	log.Print("shortfall replay enqueued")
}

func run(ctx context.Context, id, reason string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	repo, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer repo.Close()
	return repo.Replay(ctx, id, reason)
}
