// Command api serves shortfall receipt and authenticated final observations.
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

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/config"
	internalhttp "github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/provider"
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
		log.Print("shortfall configuration_unavailable")
	}
	var store internalhttp.ServerStore
	if cfg.DatabaseURL != "" {
		startupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		repo, err := postgres.Connect(startupCtx, cfg.DatabaseURL)
		cancel()
		if err != nil {
			log.Print("shortfall database_configuration_unavailable")
		} else {
			defer repo.Close()
			store = repo
		}
	}
	client, providerErr := provider.NewClient(provider.ClientConfig{BaseURL: cfg.ProviderBaseURL,
		Version: cfg.ProviderVersion, ClientID: cfg.ProviderClientID, APIKey: cfg.ProviderAPIKey, LoginAs: cfg.ProviderLoginAs})
	var contractExpiresAt time.Time
	if cfg.SandboxContractFile != "" && providerErr == nil {
		contract, err := provider.LoadSandboxContract(cfg.SandboxContractFile, cfg.Environment, cfg.WebhookSource, client)
		if err == nil {
			contractExpiresAt = contract.ExpiresAt
		} else {
			log.Print("shortfall sandbox_contract_unavailable")
		}
	}
	handler := internalhttp.NewServer(store, internalhttp.ServerConfig{Environment: cfg.Environment,
		Source: cfg.WebhookSource, WebhookSecret: cfg.WebhookSecret, ServiceSecret: cfg.ServiceSecret,
		ProviderConfigured: providerErr == nil, SandboxContractExpiresAt: contractExpiresAt}).Routes()
	listenCtx, cancelListen := context.WithTimeout(ctx, 5*time.Second)
	listener, err := (&net.ListenConfig{}).Listen(listenCtx, "tcp", cfg.HTTPAddress)
	cancelListen()
	if err != nil {
		return errors.New("shortfall_listener_unavailable")
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	// The goroutine exits when Serve returns after shutdown or listener failure.
	go func() { done <- server.Serve(listener) }()
	log.Printf("shortfall api started sandbox_contract_configured=%t", !contractExpiresAt.IsZero())
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			// Force-close only this task-owned server when graceful shutdown expires.
			_ = server.Close()
		}
		<-done
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("shortfall_server_unavailable")
	}
}
