// Command worker runs the serial funding operation worker.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Print("funding worker stopped")
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return errors.New("configuration_unavailable")
	}
	startupCtx, startupCancel := context.WithTimeout(ctx, 15*time.Second)
	defer startupCancel()
	pool, err := postgres.NewRuntimePool(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("database_configuration_invalid")
	}
	defer pool.Close()
	repository := postgres.NewRepository(pool)
	if err := repository.Ready(startupCtx); err != nil {
		return errors.New("database_runtime_not_ready")
	}
	var fundingAccount string
	if err := pool.QueryRow(startupCtx, `SELECT funding_account_ref FROM servicing.facilities WHERE mode=$1 AND id=$2`,
		cfg.Mode, cfg.FacilityID).Scan(&fundingAccount); err != nil {
		return errors.New("funding_facility_unavailable")
	}
	evaluator, err := underwriting.LoadPinned()
	if err != nil {
		return errors.New("policy_initialization_failed")
	}
	// Identity and pricing always come from the same loaded public config.
	cfg.PolicyID, cfg.PolicyHash = evaluator.PolicyID(), evaluator.PolicyHash()
	providerAdapter, err := provider.NewFromConfig(cfg, fundingAccount, "", nil, nil)
	if err != nil {
		return errors.New("provider_configuration_unavailable")
	}
	preparer, err := worker.NewDispatchPreparer(cfg, repository, providerAdapter,
		evaluator, evaluator.Pricing(), true, time.Now)
	if err != nil {
		return errors.New("dispatch_preparer_unavailable")
	}
	serialWorker, err := worker.New(cfg, repository, preparer, providerAdapter, time.Now)
	if err != nil {
		return errors.New("funding_worker_configuration_invalid")
	}
	return serialWorker.Run(ctx)
}
