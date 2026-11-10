package main

import (
	"context"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
)

func TestWorkerGracefulShutdown(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Environment: "test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, cfg)
	}()

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("worker run() returned unexpected error on cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not shut down within 2 seconds")
	}
}
