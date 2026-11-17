// Command api serves authenticated offer and actor-decision routes.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	fundinghttp "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Print("funding api stopped")
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
	if err = postgres.NewRepository(pool).Ready(startupCtx); err != nil {
		return errors.New("database_runtime_not_ready")
	}
	var fundingAccountRef string
	facilityErr := pool.QueryRow(startupCtx, `SELECT funding_account_ref FROM servicing.facilities WHERE mode=$1 AND id=$2`, cfg.Mode, cfg.FacilityID).Scan(&fundingAccountRef)
	providerAdapter, err := apiProvider(cfg, fundingAccountRef, facilityErr)
	if err != nil {
		return errors.New("provider_configuration_unavailable")
	}
	evaluator, err := underwriting.LoadPinned()
	if err != nil {
		return errors.New("policy_initialization_failed")
	}
	cfg.PolicyID, cfg.PolicyHash = evaluator.PolicyID(), evaluator.PolicyHash()
	policyBinding := &fundinghttp.PolicyBinding{ID: evaluator.PolicyID(), Hash: evaluator.PolicyHash(),
		Pricing: evaluator.Pricing(), Verified: true}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddress)
	if err != nil {
		return errors.New("funding_listener_unavailable")
	}
	defer listener.Close()
	server := &http.Server{Handler: fundinghttp.NewHandlerWithFunding(pool, cfg, policyBinding, evaluator, providerAdapter, time.Now), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
		<-done
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("funding_server_unavailable")
	}
}

// An empty, migrated installation can expose truthful read/health routes before
// its operator imports a facility. It gains no provider or financial authority.
func apiProvider(cfg config.Config, account string, facilityErr error) (provider.Boundary, error) {
	if errors.Is(facilityErr, pgx.ErrNoRows) && cfg.Mode == config.ModeSandbox &&
		!cfg.LiveDispatchEnabled && !cfg.SandboxFundingEnabled && !cfg.SandboxCollectionEnabled {
		return provider.Unsupported{}, nil
	}
	if facilityErr != nil {
		return nil, errors.New("funding_facility_unavailable")
	}
	return provider.NewFromConfig(cfg, account, "", nil, nil)
}
