package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

type readinessStore struct{ failure error }

func (s readinessStore) Ready(context.Context) error                  { return s.failure }
func (readinessStore) Receive(context.Context, domain.Delivery) error { return nil }
func (readinessStore) ListObservations(context.Context, domain.ListQuery) (domain.Page, error) {
	return domain.Page{}, errors.New("unused")
}
func (readinessStore) Observation(context.Context, string, string) (*domain.Record, error) {
	return nil, errors.New("unused")
}

func TestSandboxReadinessExpiresAndRequiresDatabaseAndMode(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, mode string
		expiry     time.Time
		dbErr      error
		status     int
	}{
		{"verified sandbox", "SANDBOX", now.Add(time.Hour), nil, 200},
		{"missing proof", "SANDBOX", time.Time{}, nil, 503},
		{"expired proof", "SANDBOX", now.Add(-time.Second), nil, 503},
		{"database unavailable", "SANDBOX", now.Add(time.Hour), errors.New("private-driver-error"), 503},
		{"production refused", "PRODUCTION", now.Add(time.Hour), nil, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewServer(readinessStore{tc.dbErr}, ServerConfig{Environment: tc.mode, Source: "fixture-subscription", WebhookSecret: "fixture-webhook", ServiceSecret: strings.Repeat("s", 32), ProviderConfigured: true, SandboxContractExpiresAt: tc.expiry, Now: func() time.Time { return now }}).Routes()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil))
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d", response.Code, tc.status)
			}
			var result map[string]any
			if json.Unmarshal(response.Body.Bytes(), &result) != nil || result["live_detection_ready"] != (tc.status == 200) {
				t.Fatal("incorrect live-readiness claim")
			}
			if strings.Contains(response.Body.String(), "private-driver") {
				t.Fatal("driver error leaked")
			}
		})
	}
}
