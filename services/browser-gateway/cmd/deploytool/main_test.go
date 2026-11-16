package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeploymentDatabaseTargetAndTLSBoundaries(t *testing.T) {
	password := strings.Repeat("a", 32)
	for _, tc := range []struct {
		url, expected, host string
		ok                  bool
	}{
		{"postgres://flowcard_go_api:" + password + "@127.0.0.1:54329/flowcard_deploy_test_abc?sslmode=disable", "flowcard_deploy_test_abc", "127.0.0.1", true},
		{"postgres://flowcard_go_api:" + password + "@127.0.0.1:54329/flowcard_deploy_test_other?sslmode=disable", "flowcard_deploy_test_abc", "127.0.0.1", false},
		{"postgres://flowcard_go_api:" + password + "@db.rds.amazonaws.com/flowcard_treasury?sslmode=disable", "flowcard_treasury", "db.rds.amazonaws.com", false},
		{"postgres://flowcard_go_api:" + password + "@db.rds.amazonaws.com/flowcard_treasury?sslmode=verify-full", "flowcard_treasury", "db.rds.amazonaws.com", true},
		{"postgres://flowcard_go_api:" + password + "@db.rds.amazonaws.com/flowcard_treasury?sslmode=prefer", "flowcard_treasury", "db.rds.amazonaws.com", false},
		{"postgres://flowcard_go_api:" + password + "@db.rds.amazonaws.com/flowcard_treasury?sslmode=verify-full&host=other.example", "flowcard_treasury", "db.rds.amazonaws.com", false},
	} {
		if got := func() bool { _, err := parseDatabase(tc.url, tc.expected, tc.host); return err == nil }(); got != tc.ok {
			t.Fatalf("database target boundary accepted=%v, want %v", got, tc.ok)
		}
	}
}

func TestHealthCheckIsLocalBoundedAndDoesNotFollowRedirects(t *testing.T) {
	var destinationCalls int
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls++; w.WriteHeader(200) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer redirect.Close()
	if health(context.Background(), redirect.URL) == nil || destinationCalls != 0 {
		t.Fatal("health check followed a redirect")
	}
	if health(context.Background(), "http://untrusted.example/health") == nil ||
		health(context.Background(), "http://user:password@127.0.0.1/health") == nil {
		t.Fatal("health check accepted nonlocal scope or credentials")
	}
	if health(context.Background(), destination.URL) != nil || destinationCalls != 1 {
		t.Fatal("valid local health check failed")
	}
}
