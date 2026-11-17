// Command api serves health endpoints and runs the serial coordinator scheduler.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	agent "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/agent"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/config"
	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/discovery"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/httpapi"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/shortfall"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/sources"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/workflow"
)

func main() {
	if err := run(); err != nil {
		log.Printf("coordinator stopped: %v", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	repo, err := postgres.Connect(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer repo.Close()
	if err := repo.Ready(dbCtx); err != nil {
		return err
	}
	if err := repo.EnsureBinding(dbCtx, cfg.SourceBindingID, cfg.Mode, cfg.Fingerprint(), cfg.ShortfallURL+"\n"+cfg.ServicingURL); err != nil {
		return err
	}
	var shortfallClient *shortfall.Client
	if cfg.ShortfallURL != "" && cfg.ShortfallSecret != "" {
		shortfallClient, err = shortfall.New(cfg.ShortfallURL, cfg.ShortfallSecret, cfg.SourceBindingID, cfg.Mode, nil)
		if err != nil {
			return err
		}
	}
	var servicingClient *servicing.Client
	if cfg.ServicingURL != "" && cfg.ServicingSecret != "" {
		servicingClient, err = servicing.New(cfg.ServicingURL, cfg.ServicingSecret, cfg.Mode, cfg.SourceBindingID, nil)
		if err != nil {
			return err
		}
	}
	var underwritingClient *underwriting.Client
	var customerContextClient *customercontext.Client
	if cfg.UnderwritingURL != "" && cfg.UnderwritingSecret != "" {
		underwritingClient, err = underwriting.New(cfg.UnderwritingURL, cfg.UnderwritingSecret, cfg.Mode, nil)
		if err != nil {
			return err
		}
		customerContextClient, err = customercontext.New(cfg.UnderwritingURL, cfg.UnderwritingSecret, cfg.Mode, nil)
		if err != nil {
			return err
		}
	}
	var modelProvider agent.Provider
	if cfg.AgentMode == "ANTHROPIC" {
		modelProvider, err = agent.NewAnthropic("", cfg.AnthropicModel, cfg.AnthropicAPIKey, nil)
		if err != nil {
			return err
		}
	}
	poller := &discovery.Poller{Repo: repo, Shortfall: shortfallClient, Servicing: servicingClient, BindingID: cfg.SourceBindingID, Mode: cfg.Mode}
	executor := &workflow.StageExecutor{Repo: repo, Shortfall: shortfallClient, Underwriting: underwritingClient, CustomerContext: customerContextClient, Servicing: servicingClient, AgentProvider: modelProvider, AgentMode: cfg.AgentMode, ModelVersion: cfg.AnthropicModel, SourceRoot: cfg.SourceRoot, Mode: cfg.Mode, BindingID: cfg.SourceBindingID}
	scheduler := &workflow.Scheduler{Repo: repo, Poller: poller, Executor: executor, BindingID: cfg.SourceBindingID, Mode: cfg.Mode}
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		if err := scheduler.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("scheduler stopped: %v", err)
			stop()
		}
	}()
	caseHandler, err := httpapi.NewCaseHandler(repo, cfg.SourceBindingID, cfg.Mode, cfg.CoordinatorReaderSecret)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		rctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		readyErr := repo.Ready(rctx)
		feeds, feedErr := poller.Health(rctx)
		sourceFeedsReady := readyErr == nil && feedErr == nil && shortfallClient != nil && !feeds.DiscoveryDegraded && !feeds.ShortfallStale && !feeds.ShortfallHeld && !feeds.ServicingStale && !feeds.ServicingHeld
		underwritingReady, underwritingErr := false, error(nil)
		if underwritingClient != nil {
			underwritingReady, underwritingErr = underwritingClient.Readiness(rctx)
		}
		agentConfigured := cfg.AgentMode == "FIXTURE" || modelProvider != nil
		agentReady := cfg.AgentMode == "FIXTURE"
		sourceManifestConfigured := cfg.SourceRoot != ""
		manifestCount, sourceManifestErr := 0, error(nil)
		if sourceManifestConfigured {
			manifestCount, sourceManifestErr = sources.ValidateManifestRoot(cfg.SourceRoot, cfg.Mode, cfg.SourceBindingID)
		}
		sourceManifestReady := sourceManifestErr == nil && manifestCount > 0
		workflowReady := sourceFeedsReady && underwritingReady && agentReady && sourceManifestReady
		serviceReady := readyErr == nil
		status := http.StatusServiceUnavailable
		if serviceReady && workflowReady {
			status = http.StatusOK
		}
		sourceMode := "HTTP_WITHOUT_MANIFEST"
		if sourceManifestConfigured && !sourceManifestReady {
			sourceMode = "HTTP_WITH_UNAVAILABLE_MANIFEST_ROOT"
		} else if sourceManifestReady {
			sourceMode = "HTTP_WITH_VALIDATED_MANIFEST_ROOT"
		}
		writeJSON(w, status, map[string]any{"mode": cfg.Mode, "service_ready": serviceReady, "runtime_ready": workflowReady, "source_feeds_ready": sourceFeedsReady, "underwriting_configured": underwritingClient != nil, "underwriting_ready_observed": underwritingReady, "agent_mode": cfg.AgentMode, "agent_configured": agentConfigured, "agent_ready": agentReady, "source_mode": sourceMode, "source_manifest_root_configured": sourceManifestConfigured, "source_manifest_root_ready": sourceManifestReady, "source_manifest_count": manifestCount, "workflow_ready": workflowReady, "live_model_verified": false, "live_crm_verified": false, "live_detection_ready": false, "live_workflow_ready": false, "workflow_blocker": readinessBlocker(readyErr, feedErr, sourceFeedsReady, underwritingClient != nil, underwritingReady, underwritingErr, agentConfigured, agentReady, sourceManifestConfigured, sourceManifestErr), "funding_enabled": false, "collection_monitoring_enabled": servicingClient != nil, "provider_acceptance": "UNVERIFIED", "discovery_degraded": feeds.DiscoveryDegraded, "shortfall_stale": feeds.ShortfallStale, "shortfall_held": feeds.ShortfallHeld, "servicing_stale": feeds.ServicingStale, "servicing_held": feeds.ServicingHeld, "shortfall_age_seconds": feeds.ShortfallAge.Seconds(), "oldest_unfinished_scan_age_seconds": feeds.OldestUnfinishedScanAge.Seconds(), "servicing_age_seconds": feeds.ServicingAge.Seconds()})
	})
	mux.Handle("/api/coordinator/", caseHandler)
	server := &http.Server{Addr: cfg.HTTPAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		serverErr := server.Shutdown(shutdown)
		<-schedulerDone
		return serverErr
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
		<-schedulerDone
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func readinessBlocker(databaseErr, healthErr error, sourceReady, underwritingConfigured, underwritingReady bool, underwritingErr error, agentConfigured, agentReady, manifestConfigured bool, manifestErr error) []string {
	var blockers []string
	if databaseErr != nil {
		blockers = append(blockers, "DATABASE_UNAVAILABLE")
	}
	if healthErr != nil || !sourceReady {
		blockers = append(blockers, "SOURCE_FEEDS_UNAVAILABLE")
	}
	if !underwritingConfigured {
		blockers = append(blockers, "UNDERWRITING_CLIENT_UNCONFIGURED")
	} else if underwritingErr != nil || !underwritingReady {
		blockers = append(blockers, "UNDERWRITING_DEPENDENCY_NOT_READY")
	}
	if !agentConfigured {
		blockers = append(blockers, "AGENT_PROVIDER_UNCONFIGURED")
	} else if !agentReady {
		blockers = append(blockers, "LIVE_AGENT_MODE_UNVERIFIED")
	}
	if !manifestConfigured {
		blockers = append(blockers, "SOURCE_MANIFEST_ROOT_UNCONFIGURED")
	} else if manifestErr != nil {
		blockers = append(blockers, "SOURCE_MANIFEST_ROOT_UNAVAILABLE")
	}
	return blockers
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
