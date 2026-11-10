package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrate_DatabaseMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := runMigrations(ctx, "postgres://invalid:54321/nonexistent?connect_timeout=1")
	if err == nil {
		t.Fatal("expected error on invalid database connection")
	}
}

func TestMigrate_LocalDatabase(t *testing.T) {
	url := os.Getenv("POLICY_ACCEPTANCE_DATABASE_URL")
	if url == "" {
		url = "postgres://test_onboarding:test_password@127.0.0.1:54329/flowcard_onboarding_test?sslmode=disable"
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil || cfg.ConnConfig.Port != 54329 {
		t.Skip("local test database not available on port 54329")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	testPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("local test database not available: %v", err)
	}
	if err := testPool.Ping(ctx); err != nil {
		testPool.Close()
		t.Skipf("local test database ping failed (sandbox or offline): %v", err)
	}
	testPool.Close()

	if err := runMigrations(ctx, url); err != nil {
		t.Fatalf("runMigrations failed against local test db: %v", err)
	}
}
