// Command api serves the authenticated local underwriting assessment API.
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

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/evaluation"
	apihttp "github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/shortfall"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
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
	cfg, err := config.LoadRuntime()
	if err != nil {
		return errors.New("underwriting_configuration_invalid")
	}
	policyConfig, err := policy.LoadConfig()
	if err != nil {
		return errors.New("underwriting_policy_configuration_invalid")
	}
	shortfallClient, err := shortfall.NewClient(cfg.ShortfallBaseURL, cfg.ShortfallServiceSecret)
	if err != nil {
		return errors.New("underwriting_shortfall_configuration_invalid")
	}
	store, err := postgres.ConnectSnapshotStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("underwriting_database_configuration_invalid")
	}
	defer store.Close()

	evaluator := &evaluation.Service{
		Mode: cfg.Mode, FacilityID: cfg.FacilityID, Policy: policyConfig,
		Store: store, Shortfalls: shortfallClient, Now: time.Now,
	}
	handler := apihttp.NewServer(apihttp.ServerConfig{
		Mode: cfg.Mode, ServiceSecret: []byte(cfg.ServiceSecret), PolicyReady: true,
		ShortfallClientConfigured: true, Store: store, Evaluation: evaluator, Now: time.Now,
	}).Routes()
	listenCtx, cancelListen := context.WithTimeout(ctx, 5*time.Second)
	listener, err := (&net.ListenConfig{}).Listen(listenCtx, "tcp", cfg.HTTPAddress)
	cancelListen()
	if err != nil {
		return errors.New("underwriting_listener_unavailable")
	}
	defer listener.Close()
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	// Serve exits when shutdown closes this task-owned listener.
	go func() { done <- server.Serve(listener) }()
	log.Printf("underwriting api started mode=%s", cfg.Mode)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			// Force-close only this task-owned listener after graceful shutdown expires.
			_ = server.Close()
		}
		<-done
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("underwriting_server_unavailable")
	}
}
