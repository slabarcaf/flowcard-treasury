// Command worker processes the durable shortfall inbox serially.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	startupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	repo, err := postgres.Open(startupCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer repo.Close()
	resolver := &provider.Resolver{Mappings: repo, References: provider.UnsupportedReferences{}}
	client, providerErr := provider.NewClient(provider.ClientConfig{BaseURL: cfg.ProviderBaseURL,
		Version: cfg.ProviderVersion, ClientID: cfg.ProviderClientID, APIKey: cfg.ProviderAPIKey, LoginAs: cfg.ProviderLoginAs})
	// Construction is side-effect free. Missing config never enables an adapter.
	if providerErr != nil {
		client = nil
	}
	contractConfigured := false
	if cfg.SandboxContractFile != "" {
		contract, err := provider.LoadSandboxContract(cfg.SandboxContractFile, cfg.Environment, cfg.WebhookSource, client)
		if err != nil {
			return err
		}
		resolver.References = contract
		resolver.Evidence = contract
		contractConfigured = true
	}
	log.Printf("shortfall worker sandbox_contract_configured=%t provider_configured=%t", contractConfigured, providerErr == nil)
	w, err := worker.New(repo, resolver, client, cfg.Environment, cfg.WebhookSource)
	if err != nil {
		return err
	}
	return w.Run(ctx)
}
