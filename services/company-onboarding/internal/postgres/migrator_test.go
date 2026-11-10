package postgres

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeDBError_Comprehensive(t *testing.T) {
	markerSecret := "SUPER_SENSITIVE_SECRET_12345"

	tests := []struct {
		name     string
		inputErr error
		expected string
	}{
		{
			name:     "nil error returns nil",
			inputErr: nil,
			expected: "",
		},
		{
			name:     "clean error with no secrets unchanged",
			inputErr: errors.New("connection reset by peer"),
			expected: "connection reset by peer",
		},
		{
			name:     "standard postgres URI credentials redacted",
			inputErr: errors.New("failed connecting to postgres://app_user:" + markerSecret + "@db.example.com:5432/flowcard?sslmode=require"),
			expected: "failed connecting to postgres://[redacted]@db.example.com:5432/flowcard?sslmode=require",
		},
		{
			name:     "postgresql scheme with uppercase credentials redacted",
			inputErr: errors.New("dial tcp postgresql://ADMIN:" + markerSecret + "@10.0.0.5:5432/db failed"),
			expected: "dial tcp postgresql://[redacted]@10.0.0.5:5432/db failed",
		},
		{
			name:     "URI with percent-encoded password redacted",
			inputErr: errors.New("parse error on postgres://user:p%40ss" + markerSecret + "%21@localhost:5432/test"),
			expected: "parse error on postgres://[redacted]@localhost:5432/test",
		},
		{
			name:     "URI with spaces in password redacted",
			inputErr: errors.New("invalid URI: postgres://user:secret with spaces " + markerSecret + "@host:5432/db"),
			expected: "invalid URI: postgres://[redacted]@host:5432/db",
		},
		{
			name:     "URI with no username redacted",
			inputErr: errors.New("connecting postgres://:" + markerSecret + "@host:5432/db"),
			expected: "connecting postgres://[redacted]@host:5432/db",
		},
		{
			name:     "malformed URI lacking host and @ redacted",
			inputErr: errors.New("cannot parse postgres://user:" + markerSecret + "/dbname"),
			expected: "cannot parse postgres://user:[redacted]/dbname",
		},
		{
			name:     "keyword password lowercase bare redacted",
			inputErr: errors.New("connection failed: host=localhost port=5432 user=flowcard password=" + markerSecret + " dbname=app"),
			expected: "connection failed: host=localhost port=5432 user=flowcard password=[redacted] dbname=app",
		},
		{
			name:     "keyword password mixed case with spaces around equals",
			inputErr: errors.New("driver error: Password = " + markerSecret + " invalid auth"),
			expected: "driver error: Password=[redacted] invalid auth",
		},
		{
			name:     "keyword password single-quoted with spaces",
			inputErr: errors.New("failed config: user='admin' password = 'secret with spaces " + markerSecret + "' sslmode=disable"),
			expected: "failed config: user='admin' password=[redacted] sslmode=disable",
		},
		{
			name:     "keyword password double-quoted with special chars",
			inputErr: errors.New("failed config: PASSWORD=\"my special " + markerSecret + "!!!\" sslmode=disable"),
			expected: "failed config: PASSWORD=[redacted] sslmode=disable",
		},
		{
			name:     "keyword pwd variant",
			inputErr: errors.New("failed: host=127.0.0.1 pwd=" + markerSecret + "; user=app"),
			expected: "failed: host=127.0.0.1 pwd=[redacted]; user=app",
		},
		{
			name:     "keyword secret variant with spaces",
			inputErr: errors.New("connect: secret = 'top_secret_" + markerSecret + "' invalid"),
			expected: "connect: secret=[redacted] invalid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sanitized := SanitizeDBError(tc.inputErr)
			if tc.inputErr == nil {
				if sanitized != nil {
					t.Fatalf("expected nil error, got %v", sanitized)
				}
				return
			}
			msg := sanitized.Error()
			if strings.Contains(msg, markerSecret) {
				t.Fatalf("SECURITY VIOLATION: sanitized error still contains marker secret!\nInput:  %s\nOutput: %s", tc.inputErr.Error(), msg)
			}
			if msg != tc.expected {
				t.Fatalf("sanitized message mismatch:\nGot:  %s\nWant: %s", msg, tc.expected)
			}
		})
	}
}
