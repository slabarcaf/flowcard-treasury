package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
)

// pipeListener implements net.Listener using net.Pipe for in-memory, deterministic testing
// without loopback network socket requirements.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (p *pipeListener) Accept() (net.Conn, error) {
	select {
	case <-p.closed:
		return nil, net.ErrClosed
	case c, ok := <-p.conns:
		if !ok {
			return nil, net.ErrClosed
		}
		return c, nil
	}
}

func (p *pipeListener) Close() error {
	p.once.Do(func() {
		close(p.closed)
	})
	return nil
}

func (p *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
}

func (p *pipeListener) Dial() (net.Conn, error) {
	select {
	case <-p.closed:
		return nil, net.ErrClosed
	default:
	}
	serverConn, clientConn := net.Pipe()
	select {
	case p.conns <- serverConn:
		return clientConn, nil
	case <-p.closed:
		_ = serverConn.Close()
		_ = clientConn.Close()
		return nil, net.ErrClosed
	}
}

// closedOnAcceptListener immediately returns http.ErrServerClosed on Accept
// to deterministically test Serve returning clean exit without sending on errCh.
type closedOnAcceptListener struct{}

func (closedOnAcceptListener) Accept() (net.Conn, error) {
	return nil, http.ErrServerClosed
}

func (closedOnAcceptListener) Close() error {
	return nil
}

func (closedOnAcceptListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	handler := newHandler()

	tests := []struct {
		name string
		path string
	}{
		{name: "healthz_endpoint", path: "/healthz"},
		{name: "livez_endpoint", path: "/livez"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("GET %s status = %v, want %v", tt.path, rec.Code, http.StatusOK)
			}

			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if body["status"] != "ok" {
				t.Errorf("status = %q, want ok", body["status"])
			}
			if body["service"] != "company-onboarding" {
				t.Errorf("service = %q, want company-onboarding", body["service"])
			}
		})
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := newHandler()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz status = %v, want %v", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestRunWithListenerGracefulShutdown(t *testing.T) {
	t.Parallel()

	pl := newPipeListener()
	t.Cleanup(func() {
		_ = pl.Close()
	})

	cfg := &config.Config{
		Port:        8080,
		Environment: "test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithListener(ctx, cfg, pl, nil, 2*time.Second)
	}()

	// Signal graceful shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runWithListener() unexpected error on cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithListener() did not shut down within 3 seconds")
	}
}

func TestRunWithListenerErrChannelCloseReturnsNil(t *testing.T) {
	t.Parallel()

	// closedOnAcceptListener causes server.Serve to immediately return http.ErrServerClosed.
	// This closes errCh without sending an error.
	// Verifies that receiving from closed errCh cleanly returns nil rather than a false wrapped error.
	l := closedOnAcceptListener{}
	cfg := &config.Config{
		Port:        8080,
		Environment: "test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithListener(ctx, cfg, l, nil, 2*time.Second)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runWithListener() expected nil error on closed channel, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithListener() did not complete within 3 seconds")
	}
}

func TestRunWithListenerShutdownTimeoutForcedClose(t *testing.T) {
	t.Parallel()

	pl := newPipeListener()
	t.Cleanup(func() {
		_ = pl.Close()
	})

	cfg := &config.Config{
		Port:        8080,
		Environment: "test",
	}

	handlerEntered := make(chan struct{})
	handlerRelease := make(chan struct{})
	handlerCtxCancelled := make(chan struct{})

	blockedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerEntered)
		select {
		case <-handlerRelease:
		case <-r.Context().Done():
			close(handlerCtxCancelled)
			// Remain blocked until explicitly released to force server.Shutdown deadline timeout
			<-handlerRelease
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		select {
		case <-handlerRelease:
		default:
			close(handlerRelease)
		}
	})

	errCh := make(chan error, 1)
	go func() {
		// Use short 20ms shutdown timeout to deterministically exercise forced server.Close path
		errCh <- runWithListener(ctx, cfg, pl, blockedHandler, 20*time.Millisecond)
	}()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return pl.Dial()
			},
		},
	}

	reqErrCh := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://pipe.local/blocked")
		if resp != nil {
			_ = resp.Body.Close()
		}
		reqErrCh <- err
	}()

	// Wait deterministically for handler to receive request via synchronization (no sleeps)
	select {
	case <-handlerEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler was not reached within 3 seconds")
	}

	// Trigger shutdown while handler is blocked
	cancel()

	// Verify BaseContext cancelled handler context
	select {
	case <-handlerCtxCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("BaseContext cancellation was not received by handler within 3 seconds")
	}

	// runWithListener should time out during graceful shutdown, invoke server.Close(), and return context.DeadlineExceeded
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("runWithListener() expected shutdown timeout error, got nil")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("runWithListener() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithListener() did not shut down within 3 seconds")
	}

	// Release handler goroutine cleanly
	close(handlerRelease)
}
