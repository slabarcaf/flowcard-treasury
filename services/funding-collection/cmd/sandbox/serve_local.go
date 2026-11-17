package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type localChild struct {
	command *exec.Cmd
	done    chan error
}

func baseChildEnvironment(values map[string]string) []string {
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR"), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off"}
	for key, value := range values {
		environment = append(environment, key+"="+value)
	}
	return environment
}

// safeChildLog redacts all known secrets/DSNs before bounded diagnostics reach
// disk. The original root credential file and child environment are never logged.
func safeChildLog(reader io.Reader, path string, privateValues []string, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		_, _ = io.Copy(io.Discard, reader)
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	written := 0
	for scanner.Scan() {
		line := scanner.Text()
		for _, value := range privateValues {
			if value != "" {
				line = strings.ReplaceAll(line, value, "[redacted]")
			}
		}
		if written+len(line) > 1<<20 {
			continue
		}
		n, _ := io.WriteString(file, line+"\n")
		written += n
	}
}

func startLocalChild(ctx context.Context, root, path, logPath string, args []string, environment map[string]string, privateValues []string) (*localChild, error) {
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = root
	command.Env = baseChildEnvironment(environment)
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, errors.New("sandbox_child_output_unavailable")
	}
	command.Stderr = command.Stdout
	if command.Start() != nil {
		return nil, errors.New("sandbox_child_start_failed")
	}
	doneLogs := make(chan struct{}, 1)
	go safeChildLog(output, logPath, privateValues, doneLogs)
	child := &localChild{command: command, done: make(chan error, 1)}
	go func() { err := command.Wait(); <-doneLogs; child.done <- err }()
	return child, nil
}

func stopLocalChildren(children []*localChild) {
	for _, child := range children {
		if child.command.Process != nil {
			_ = child.command.Process.Signal(syscall.SIGTERM)
		}
	}
	for _, child := range children {
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			_ = child.command.Process.Kill()
			<-child.done
		}
	}
}

func webhookIngress() http.Handler {
	mux := http.NewServeMux()
	for path, target := range map[string]string{"/webhooks/airwallex/shortfalls": "http://127.0.0.1:18082", "/webhooks/airwallex/servicing": "http://127.0.0.1:18091"} {
		destination, _ := url.Parse(target)
		proxy := httputil.NewSingleHostReverseProxy(destination)
		proxy.Transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 20 * time.Second}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "webhook upstream unavailable", 503)
		}
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.Header().Set("Allow", "POST")
				w.WriteHeader(405)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 10_000_000)
			proxy.ServeHTTP(w, r)
		})
	}
	return mux
}

func serveLocalStack(ctx context.Context, cfg credentials, manifest, runID string, history bool) error {
	var local localRuntime
	if err := readPrivateJSON(localRuntimePath(manifest), &local, 65536); err != nil {
		return err
	}
	if err := validateLocalProfile(local); err != nil {
		return err
	}
	if local.RunID != runID {
		return errors.New("sandbox_local_run_mismatch")
	}
	root, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	directory := filepath.Dir(manifest)
	var subscriptions captureSubscriptions
	if err := readPrivateJSON(filepath.Join(directory, "capture-subscriptions.json"), &subscriptions, 65536); err != nil {
		return err
	}
	if subscriptions.Owner != manifestOwner || subscriptions.RunID != runID || subscriptions.Environment != "SANDBOX" {
		return errors.New("sandbox_subscription_owner_mismatch")
	}
	for _, path := range []string{"shortfall-contract.json", "execution-contract.json"} {
		if _, err := os.Stat(filepath.Join(directory, path)); err != nil {
			return errors.New("sandbox_execution_profiles_required")
		}
	}
	if err := os.MkdirAll(filepath.Join(directory, "bin"), 0700); err != nil {
		return errors.New("sandbox_binary_directory_failed")
	}
	if err := os.MkdirAll(filepath.Join(directory, "logs"), 0700); err != nil {
		return errors.New("sandbox_log_directory_failed")
	}
	binaries := []struct{ name, service, command string }{{"shortfall-api", "shortfall-detection", "api"}, {"shortfall-worker", "shortfall-detection", "worker"}, {"underwriting-api", "underwriting-engine", "api"}, {"funding-api", "funding-collection", "api"}, {"funding-worker", "funding-collection", "worker"}, {"coordinator-api", "go-coordinator", "api"}, {"gateway-api", "browser-gateway", "api"}}
	for _, binary := range binaries {
		command := exec.CommandContext(ctx, "go", "-C", filepath.Join(root, "services", binary.service), "build", "-o", filepath.Join(directory, "bin", binary.name), "./cmd/"+binary.command)
		command.Env = baseChildEnvironment(nil)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		if command.Run() != nil {
			return errors.New("sandbox_binary_build_failed_" + binary.name)
		}
	}
	rootEnvironment := map[string]string{"AIRWALLEX_BASE_URL": sandboxHost, "AIRWALLEX_CLIENT_ID": cfg.clientID, "AIRWALLEX_API_KEY": cfg.apiKey, "AIRWALLEX_ACCOUNT_ID": cfg.platform}
	environmentFor := func(service string) map[string]string {
		environment := map[string]string{"DATABASE_URL": local.URLs[service]}
		if service == "funding" || service == "shortfall" {
			for k, v := range rootEnvironment {
				environment[k] = v
			}
		}
		return environment
	}
	shortfall := environmentFor("shortfall")
	shortfall["SHORTFALL_ENVIRONMENT"] = "SANDBOX"
	shortfall["SHORTFALL_HTTP_ADDR"] = "127.0.0.1:18082"
	shortfall["SHORTFALL_WEBHOOK_SOURCE"] = subscriptions.Subscriptions["SHORTFALL"].ID
	shortfall["SHORTFALL_WEBHOOK_SECRET"] = subscriptions.Subscriptions["SHORTFALL"].Secret
	shortfall["SHORTFALL_SERVICE_SECRET"] = local.Secrets["shortfall"]
	shortfall["SHORTFALL_ISSUING_API_VERSION"] = "2026-08-21"
	shortfall["SHORTFALL_SANDBOX_CONTRACT_FILE"] = filepath.Join(directory, "shortfall-contract.json")
	if history {
		delete(shortfall, "SHORTFALL_SANDBOX_CONTRACT_FILE")
	}
	underwriting := environmentFor("underwriting")
	underwriting["UNDERWRITING_MODE"] = "SANDBOX"
	underwriting["UNDERWRITING_HTTP_ADDR"] = "127.0.0.1:18083"
	underwriting["UNDERWRITING_FACILITY_ID"] = local.FacilityID
	underwriting["UNDERWRITING_SERVICE_SECRET"] = local.Secrets["underwriting"]
	underwriting["SHORTFALL_BASE_URL"] = "http://127.0.0.1:18082"
	underwriting["SHORTFALL_SERVICE_SECRET"] = local.Secrets["shortfall"]
	funding := environmentFor("funding")
	funding["SERVICING_MODE"] = "SANDBOX"
	funding["SERVICING_HTTP_ADDR"] = "127.0.0.1:18091"
	funding["SERVICING_FACILITY_ID"] = local.FacilityID
	funding["SERVICING_SOURCE_BINDING_ID"] = local.SourceBindingID
	funding["SERVICING_COORDINATOR_SECRET"] = local.Secrets["servicing-reader"]
	funding["SERVICING_OPERATOR_SECRET"] = local.Secrets["operator"]
	funding["SERVICING_CUSTOMER_SECRET"] = local.Secrets["customer"]
	funding["SERVICING_READER_SECRET"] = local.Secrets["underwriting-reader"]
	funding["UNDERWRITING_BASE_URL"] = "http://127.0.0.1:18083"
	funding["UNDERWRITING_SERVICE_SECRET"] = local.Secrets["underwriting"]
	funding["SHORTFALL_BASE_URL"] = "http://127.0.0.1:18082"
	funding["SHORTFALL_SERVICE_SECRET"] = local.Secrets["shortfall"]
	funding["SERVICING_PROVIDER_MODE"] = "AIRWALLEX_SANDBOX"
	funding["SERVICING_PROVIDER_BASE_URL"] = sandboxHost
	funding["AIRWALLEX_WEBHOOK_SECRET"] = subscriptions.Subscriptions["SERVICING"].Secret
	funding["SERVICING_SANDBOX_DELEGATION_ENABLED"] = "true"
	funding["SERVICING_LIVE_DISPATCH_ENABLED"] = "true"
	funding["SERVICING_SANDBOX_FUNDING_ENABLED"] = "true"
	funding["SERVICING_SANDBOX_COLLECTION_ENABLED"] = "true"
	if history {
		funding["SERVICING_LIVE_DISPATCH_ENABLED"] = "false"
		funding["SERVICING_SANDBOX_FUNDING_ENABLED"] = "false"
		funding["SERVICING_SANDBOX_COLLECTION_ENABLED"] = "false"
	}
	if !history {
		funding["SERVICING_SANDBOX_PAYMENT_CONTRACT_FILE"] = filepath.Join(directory, "shortfall-contract.json")
		funding["SERVICING_SANDBOX_EXECUTION_CONTRACT_FILE"] = filepath.Join(directory, "execution-contract.json")
	}
	coordinator := environmentFor("coordinator")
	coordinator["COORDINATOR_MODE"] = "SANDBOX"
	coordinator["COORDINATOR_HTTP_ADDR"] = "127.0.0.1:18090"
	coordinator["COORDINATOR_SOURCE_BINDING_ID"] = local.SourceBindingID
	coordinator["COORDINATOR_READER_SECRET"] = local.Secrets["coordinator-reader"]
	coordinator["COORDINATOR_AGENT_MODE"] = "FIXTURE"
	coordinator["SHORTFALL_BASE_URL"] = "http://127.0.0.1:18082"
	coordinator["SHORTFALL_SERVICE_SECRET"] = local.Secrets["shortfall"]
	coordinator["UNDERWRITING_BASE_URL"] = "http://127.0.0.1:18083"
	coordinator["UNDERWRITING_SERVICE_SECRET"] = local.Secrets["underwriting"]
	coordinator["SERVICING_BASE_URL"] = "http://127.0.0.1:18091"
	coordinator["SERVICING_SERVICE_SECRET"] = local.Secrets["servicing-reader"]
	gateway := gatewayRuntimeEnvironment(local)
	onboardingURL := strings.TrimSpace(os.Getenv("BROWSER_GATEWAY_ONBOARDING_URL"))
	onboardingSecret := os.Getenv("BROWSER_GATEWAY_ONBOARDING_SECRET")
	if (onboardingURL == "") != (onboardingSecret == "") {
		return errors.New("sandbox_onboarding_upstream_pair_required")
	}
	if onboardingURL != "" {
		// The gateway applies its existing URL and secret validation at startup.
		gateway["BROWSER_GATEWAY_ONBOARDING_URL"] = onboardingURL
		gateway["BROWSER_GATEWAY_ONBOARDING_SECRET"] = onboardingSecret
	}
	environments := map[string]map[string]string{"shortfall-api": shortfall, "shortfall-worker": shortfall, "underwriting-api": underwriting, "funding-api": funding, "funding-worker": funding, "coordinator-api": coordinator, "gateway-api": gateway}
	privateValues := []string{cfg.apiKey, cfg.clientID, subscriptions.Subscriptions["SHORTFALL"].Secret, subscriptions.Subscriptions["SERVICING"].Secret, onboardingSecret}
	for _, value := range local.URLs {
		privateValues = append(privateValues, value)
		if parsed, err := url.Parse(value); err == nil && parsed.User != nil {
			password, _ := parsed.User.Password()
			privateValues = append(privateValues, password)
		}
	}
	for _, value := range local.Secrets {
		privateValues = append(privateValues, value)
	}
	for _, value := range local.Users {
		privateValues = append(privateValues, value.Password)
	}
	children := []*localChild{}
	defer func() { stopLocalChildren(children) }()
	for _, binary := range binaries {
		if history && strings.HasSuffix(binary.name, "-worker") {
			continue
		}
		child, err := startLocalChild(ctx, root, filepath.Join(directory, "bin", binary.name), filepath.Join(directory, "logs", binary.name+".log"), nil, environments[binary.name], privateValues)
		if err != nil {
			return err
		}
		children = append(children, child)
	}
	frontendEnvironment := map[string]string{"VITE_BROWSER_GATEWAY_ENABLED": "true", "BROWSER_GATEWAY_PROXY_TARGET": "http://127.0.0.1:18094"}
	if history {
		frontendEnvironment["VITE_SANDBOX_HISTORY"] = "true"
	}
	frontend, err := startLocalChild(ctx, root, "node", filepath.Join(directory, "logs", "frontend.log"), []string{filepath.Join(root, "node_modules", "vite", "bin", "vite.js"), "--host", "127.0.0.1", "--port", "18073", "--strictPort"}, frontendEnvironment, privateValues)
	if err != nil {
		return err
	}
	children = append(children, frontend)
	if !history {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:1234")
		if err != nil {
			return errors.New("sandbox_owned_ingress_port_unavailable")
		}
		defer listener.Close()
		ingress := &http.Server{Handler: webhookIngress(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 * 1024}
		serverDone := make(chan error, 1)
		go func() { serverDone <- ingress.Serve(listener) }()
		var once sync.Once
		closeIngress := func() {
			once.Do(func() {
				shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if ingress.Shutdown(shutdown) != nil {
					_ = ingress.Close()
				}
				<-serverDone
			})
		}
		defer closeIngress()
	}
	for _, endpoint := range []string{"http://127.0.0.1:18082/healthz", "http://127.0.0.1:18083/healthz", "http://127.0.0.1:18091/health/live", "http://127.0.0.1:18090/healthz", "http://127.0.0.1:18094/healthz", "http://127.0.0.1:18073/"} {
		if err := waitLocalEndpoint(ctx, endpoint); err != nil {
			return err
		}
	}
	control, _ := json.Marshal(map[string]any{"owner": manifestOwner, "run_id": runID, "pid": os.Getpid()})
	if err := writePrivateFile(filepath.Join(directory, "stack-control.json"), control); err != nil {
		return err
	}
	refreshDone := make(chan struct{})
	if history {
		close(refreshDone)
	} else {
		go func() {
			defer close(refreshDone)
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					_ = refreshLocalSnapshots(refreshCtx, manifest, runID, false)
					cancel()
				}
			}
		}()
	}
	defer func() { /* the ticker exits with the bounded parent context */ }()
	_, _ = io.WriteString(os.Stdout, "owned sandbox stack ready; browser frontend on loopback port 18073; lifetime bounded to this command\n")
	if history {
		_, _ = io.WriteString(os.Stdout, "history review: provider dispatch, financial workers and public webhook ingress disabled\n")
	}
	<-ctx.Done()
	<-refreshDone
	return nil
}

func waitLocalEndpoint(ctx context.Context, endpoint string) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return errors.New("sandbox_local_health_url_invalid")
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("sandbox_local_start_cancelled")
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("sandbox_local_service_not_healthy")
}
