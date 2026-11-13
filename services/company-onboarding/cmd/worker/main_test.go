package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
)

func TestWorkerGracefulShutdown(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Environment: "test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, cfg)
	}()

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("worker run() returned unexpected error on cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not shut down within 2 seconds")
	}
}

func TestBuildWorkerDependencies_UnconfiguredDefaults(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Environment: "test",
	}

	ctx := context.Background()
	client, store, subWorker, whProcessor, closeFn, err := buildWorkerDependencies(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error with unconfigured defaults: %v", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	if client == nil {
		t.Fatal("expected non-nil unconfigured platform client")
	}
	// Calling client method should return ErrUnconfigured
	_, callErr := client.GetAccount(ctx, "acct_test")
	if !errors.Is(callErr, airwallex.ErrUnconfigured) {
		t.Errorf("expected ErrUnconfigured, got %v", callErr)
	}

	if store == nil {
		t.Fatal("expected non-nil unavailable store")
	}
	// Calling store method should return error
	_, _, getErr := store.Get(ctx, "test-key")
	if getErr == nil {
		t.Error("expected error from unavailable store Get, got nil")
	}

	if subWorker != nil {
		t.Error("expected nil subWorker without DATABASE_URL")
	}
	if whProcessor != nil {
		t.Error("expected nil whProcessor without DATABASE_URL")
	}
}

func TestBuildWorkerDependencies_RejectsProductionAirwallexURL(t *testing.T) {
	t.Parallel()

	// In demo runtime, production Airwallex URL must be rejected even when application NODE_ENV is production
	cfg := &config.Config{
		Environment:       "production",
		AirwallexBaseURL:  "https://api.airwallex.com",
		AirwallexClientID: "client_123",
		AirwallexAPIKey:   "key_123",
	}

	ctx := context.Background()
	_, _, _, _, closeFn, err := buildWorkerDependencies(ctx, cfg)
	if closeFn != nil {
		defer closeFn()
	}

	if err == nil {
		t.Fatal("expected error when AirwallexBaseURL is production, got nil")
	}
	if !strings.Contains(err.Error(), "must target sandbox") {
		t.Errorf("expected sandbox error, got %v", err)
	}
}

func TestBuildWorkerDependencies_AcceptsSandboxAirwallexURL(t *testing.T) {
	t.Parallel()

	// In demo runtime, sandbox Airwallex URL succeeds when application NODE_ENV is production
	cfg := &config.Config{
		Environment:       "production",
		AirwallexBaseURL:  "https://api.sandbox.airwallex.com",
		AirwallexClientID: "client_123",
		AirwallexAPIKey:   "key_123",
	}

	ctx := context.Background()
	client, _, _, _, closeFn, err := buildWorkerDependencies(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error initializing sandbox client: %v", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	if client == nil {
		t.Fatal("expected non-nil platform client")
	}
}

func TestBuildWorkerDependencies_ConfiguredS3Storage_CredentialsResolution(t *testing.T) {
	// Subtests cannot run parallel when mutating process environment with t.Setenv
	t.Run("missing_credentials_fails_closed", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "")
		t.Setenv("AWS_SESSION_TOKEN", "")
		t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
		t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")

		cfg := &config.Config{
			Environment:       "test",
			S3BucketDocuments: "test-documents-bucket",
			S3KMSKeyID:        "arn:aws:kms:us-west-2:123456789012:key/test-key",
			AWSRegion:         "us-west-2",
		}

		ctx := context.Background()
		_, _, _, _, closeFn, err := buildWorkerDependencies(ctx, cfg)
		if closeFn != nil {
			defer closeFn()
		}

		if err == nil {
			t.Fatal("expected error with unconfigured S3 credentials, got nil")
		}
		if !strings.Contains(err.Error(), "object store unconfigured") {
			t.Errorf("expected ErrStorageUnconfigured, got: %v", err)
		}
	})

	t.Run("static_environment_credentials_configured_successfully", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKEACCESSKEY1234")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "fakeSecretAccessKey5678901234567890")
		t.Setenv("AWS_SESSION_TOKEN", "fakeSessionToken")
		t.Setenv("AWS_REGION", "us-west-2")

		cfg := &config.Config{
			Environment:       "test",
			S3BucketDocuments: "test-documents-bucket",
			S3KMSKeyID:        "arn:aws:kms:us-west-2:123456789012:key/test-key",
			AWSRegion:         "us-west-2",
		}

		ctx := context.Background()
		_, store, _, _, closeFn, err := buildWorkerDependencies(ctx, cfg)
		if err != nil {
			t.Fatalf("expected successful worker dependency build with static credentials, got error: %v", err)
		}
		if closeFn != nil {
			defer closeFn()
		}
		if store == nil {
			t.Fatal("expected non-nil store")
		}
	})

	t.Run("ecs_task_role_container_credentials_configured_successfully", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "")
		t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "/v2/credentials/fake-task-role")
		t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN", "fake-token")

		cfg := &config.Config{
			Environment:       "test",
			S3BucketDocuments: "test-documents-bucket",
			S3KMSKeyID:        "arn:aws:kms:us-west-2:123456789012:key/test-key",
			AWSRegion:         "us-west-2",
		}

		ctx := context.Background()
		_, store, _, _, closeFn, err := buildWorkerDependencies(ctx, cfg)
		if err != nil {
			t.Fatalf("expected successful worker dependency build with ECS container credentials, got error: %v", err)
		}
		if closeFn != nil {
			defer closeFn()
		}
		if store == nil {
			t.Fatal("expected non-nil store")
		}
	})
}
