// Command capture records signed, owned sandbox webhook evidence. It never
// creates detections, offers or financial operations.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
	shortfallhttp "github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/http"
)

const owner = "flowcard-airwallex-sandbox-v1"

type captureConfig struct {
	Owner         string   `json:"owner"`
	RunID         string   `json:"run_id"`
	Environment   string   `json:"environment"`
	Accounts      []string `json:"accounts"`
	Subscriptions map[string]struct {
		ID      string `json:"id"`
		Secret  string `json:"secret"`
		Version string `json:"version"`
	} `json:"subscriptions"`
}

type capturedDelivery struct {
	RunID      string    `json:"run_id"`
	Source     string    `json:"source"`
	EventID    string    `json:"event_id"`
	EventName  string    `json:"event_name"`
	ReceivedAt time.Time `json:"received_at"`
	Timestamp  string    `json:"x_timestamp"`
	Signature  string    `json:"x_signature"`
	RawBody    []byte    `json:"raw_body_base64"`
}

type captureWriter struct {
	mu    sync.Mutex
	dir   string
	runID string
}
type captureStore struct {
	writer   *captureWriter
	headers  http.Header
	accounts []string
}

func (s *captureStore) Receive(_ context.Context, delivery domain.Delivery) error {
	var envelope struct {
		AccountID string `json:"account_id"`
	}
	if json.Unmarshal(delivery.RawBody, &envelope) != nil {
		return errors.New("capture_envelope_invalid")
	}
	if envelope.AccountID == "" {
		return errors.New("capture_account_scope_unresolved")
	}
	owned := false
	for _, id := range s.accounts {
		if envelope.AccountID == id {
			owned = true
		}
	}
	// This observer is never a writer for any other customer's events.
	if !owned {
		return nil
	}
	s.writer.mu.Lock()
	defer s.writer.mu.Unlock()
	digest := sha256.Sum256([]byte(delivery.Source + "\x00" + delivery.WebhookID))
	path := filepath.Join(s.writer.dir, hex.EncodeToString(digest[:])+".json")
	if old, err := os.ReadFile(path); err == nil {
		var prior capturedDelivery
		if json.Unmarshal(old, &prior) != nil || !bytes.Equal(prior.RawBody, delivery.RawBody) {
			return errors.New("capture_event_conflict")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return errors.New("capture_read_unavailable")
	}
	files, err := os.ReadDir(s.writer.dir)
	if err != nil || len(files) >= 100 {
		return errors.New("capture_capacity_reached")
	}
	raw, err := json.Marshal(capturedDelivery{s.writer.runID, delivery.Source, delivery.WebhookID, delivery.EventName, time.Now().UTC(), s.headers.Get("X-Timestamp"), s.headers.Get("X-Signature"), delivery.RawBody})
	if err != nil {
		return errors.New("capture_encode_failed")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("capture_write_failed")
	}
	if _, err = f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return errors.New("capture_write_failed")
	}
	if f.Sync() != nil {
		f.Close()
		return errors.New("capture_sync_failed")
	}
	if f.Close() != nil {
		return errors.New("capture_write_failed")
	}
	directory, err := os.Open(s.writer.dir)
	if err != nil {
		return errors.New("capture_sync_failed")
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return errors.New("capture_sync_failed")
	}
	return nil
}

func captureHandler(configFile, runID, directory string) (http.Handler, error) {
	if !strings.HasPrefix(runID, "fc-sbx-") || !filepath.IsAbs(configFile) || !filepath.IsAbs(directory) {
		return nil, errors.New("capture_arguments_invalid")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("capture_directory_unavailable")
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("capture_directory_not_private")
	}
	writer := &captureWriter{dir: directory, runID: runID}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source := ""
		switch r.URL.Path {
		case "/webhooks/airwallex/shortfalls":
			source = "SHORTFALL"
		case "/webhooks/airwallex/servicing":
			source = "SERVICING"
		default:
			http.NotFound(w, r)
			return
		}
		f, err := os.OpenFile(configFile, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			http.Error(w, "capture configuration unavailable", 503)
			return
		}
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
			f.Close()
			http.Error(w, "capture configuration unavailable", 503)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(f, 65537))
		f.Close()
		var cfg captureConfig
		if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &cfg) != nil || cfg.Owner != owner || cfg.RunID != runID || cfg.Environment != "SANDBOX" || len(cfg.Accounts) != 2 {
			http.Error(w, "capture configuration unavailable", 503)
			return
		}
		subscription, ok := cfg.Subscriptions[source]
		if !ok || subscription.ID == "" || subscription.Secret == "" || subscription.Version != "2026-08-21" {
			http.Error(w, "capture configuration unavailable", 503)
			return
		}
		store := &captureStore{writer: writer, headers: r.Header.Clone(), accounts: cfg.Accounts}
		receiver, err := shortfallhttp.NewWebhookReceiver(store, shortfallhttp.WebhookConfig{Environment: "SANDBOX", Source: source, Secret: subscription.Secret})
		if err != nil {
			http.Error(w, "capture configuration unavailable", 503)
			return
		}
		request := r.Clone(r.Context())
		target := *r.URL
		target.Path = "/webhooks/airwallex/shortfalls"
		request.URL = &target
		receiver.ServeHTTP(w, request)
	}), nil
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configFile := flags.String("subscriptions-file", "", "Private owned subscription configuration")
	directory := flags.String("output-directory", "", "Private evidence directory")
	runID := flags.String("run-id", "", "Expected run marker")
	address := flags.String("http-addr", "127.0.0.1:18095", "Loopback-only address")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("capture_arguments_invalid")
	}
	host, port, err := net.SplitHostPort(*address)
	ip := net.ParseIP(host)
	if err != nil || port == "" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return errors.New("capture_listener_must_be_loopback")
	}
	handler, err := captureHandler(*configFile, *runID, *directory)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *address)
	if err != nil {
		return errors.New("capture_listener_unavailable")
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		<-done
		return nil
	case <-done:
		return errors.New("capture_server_stopped")
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error()+"\n")
		os.Exit(1)
	}
}
