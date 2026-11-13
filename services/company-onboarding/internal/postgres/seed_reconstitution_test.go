package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
)

func findSeedSQLFile() (string, error) {
	candidates := []string{
		"../../../../server/src/db/seeds/seed-synthetic-demo.sql",
		"../../../../infra/scripts/seed-synthetic-demo.sql",
		"server/src/db/seeds/seed-synthetic-demo.sql",
		"infra/scripts/seed-synthetic-demo.sql",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs, nil
			}
		}
	}
	return "", os.ErrNotExist
}

func TestSeededEntitiesDomainReconstitution(t *testing.T) {
	cfg, _ := getTestDatabaseConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test postgres: %v", err)
	}
	defer pool.Close()

	// Ensure Go migrations are applied
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Prepare known isolated fixture state by cleaning up owned fixture rows
	// in safe foreign-key order before executing synthetic seed.
	cleanupOwnedFixtures := func() {
		_, _ = pool.Exec(ctx, `
			DELETE FROM onboarding.applications WHERE id IN ('app_synthetic_bloom', 'app_synthetic_atlas');
			DELETE FROM onboarding.companies WHERE id IN ('comp_synthetic_bloom', 'comp_synthetic_atlas');
			DO $$ BEGIN
				IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'facility_configs') THEN
					UPDATE facility_configs SET facility_amount_cents = 5000000 WHERE id = 'usd-primary';
				END IF;
			END $$;
		`)
	}
	cleanupOwnedFixtures()

	// Apply synthetic seed to guarantee seed rows are present in the shared test DB
	seedPath, err := findSeedSQLFile()
	if err != nil {
		t.Fatalf("failed to locate seed SQL file: %v", err)
	}
	seedBytes, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("failed to read seed SQL file %q: %v", seedPath, err)
	}

	t.Cleanup(func() {
		cleanupOwnedFixtures()
		// Re-seed pristine baseline fixtures so downstream suites encounter clean state
		_, _ = pool.Exec(context.Background(), string(seedBytes))
	})

	if _, err := pool.Exec(ctx, string(seedBytes)); err != nil {
		t.Fatalf("failed to execute synthetic seed: %v", err)
	}

	repo := postgres.NewRepository(pool)

	// 1. Reconstitute synthetic Bloom company
	bloom, err := repo.GetCompany(ctx, "comp_synthetic_bloom")
	if err != nil {
		t.Fatalf("failed to reconstitute comp_synthetic_bloom: %v", err)
	}
	if bloom.LegalName() != "Bloom Synthetic Holdings Inc." {
		t.Errorf("bloom legal name mismatch: got %q", bloom.LegalName())
	}
	if bloom.Status() != domain.CompanyStatusActive {
		t.Errorf("bloom status mismatch: got %v", bloom.Status())
	}
	if len(bloom.People()) != 1 {
		t.Fatalf("bloom people count mismatch: got %d", len(bloom.People()))
	}
	if bloom.People()[0].FirstName != "Jane" || bloom.People()[0].LastName != "Doe" {
		t.Errorf("bloom person name mismatch: %s %s", bloom.People()[0].FirstName, bloom.People()[0].LastName)
	}

	// 2. Reconstitute synthetic Atlas company
	atlas, err := repo.GetCompany(ctx, "comp_synthetic_atlas")
	if err != nil {
		t.Fatalf("failed to reconstitute comp_synthetic_atlas: %v", err)
	}
	if atlas.LegalName() != "Atlas Synthetic Software Corp." {
		t.Errorf("atlas legal name mismatch: got %q", atlas.LegalName())
	}
	if atlas.Status() != domain.CompanyStatusActive {
		t.Errorf("atlas status mismatch: got %v", atlas.Status())
	}

	// 3. Reconstitute synthetic Bloom onboarding application
	bloomApp, err := repo.GetApplication(ctx, "app_synthetic_bloom")
	if err != nil {
		t.Fatalf("failed to reconstitute app_synthetic_bloom: %v", err)
	}
	if bloomApp.Status() != domain.ApplicationStatusUnderReview {
		t.Errorf("bloom application status mismatch: got %v", bloomApp.Status())
	}
	if bloomApp.Company() == nil {
		t.Fatalf("bloom application company snapshot is nil")
	}
	if bloomApp.Company().LegalName() != "Bloom Synthetic Holdings Inc." {
		t.Errorf("bloom snapshot legal name mismatch: got %q", bloomApp.Company().LegalName())
	}
	// Assert Bloom application does not claim an unverified live platform account ID
	if bloomApp.Platform().PlatformAccountID != "" {
		t.Errorf("bloom application claims unverified platform account ID: %v", bloomApp.Platform().PlatformAccountID)
	}

	// 4. Reconstitute synthetic Atlas onboarding application
	atlasApp, err := repo.GetApplication(ctx, "app_synthetic_atlas")
	if err != nil {
		t.Fatalf("failed to reconstitute app_synthetic_atlas: %v", err)
	}
	if atlasApp.Status() != domain.ApplicationStatusDraft {
		t.Errorf("atlas application status mismatch: got %v", atlasApp.Status())
	}

	// 5. Verify periodic background worker reconciliation isolation:
	// Synthetic fixture accounts must NEVER be present in the live polling table.
	trackedPAs, err := repo.ListTrackedPlatformAccounts(ctx, 10)
	if err != nil {
		t.Fatalf("failed to query tracked platform accounts: %v", err)
	}
	for _, pa := range trackedPAs {
		if pa.ExternalAccountID() == "acct_bloom001" || pa.CompanyID() == "comp_synthetic_bloom" {
			t.Fatalf("SAFETY DEFECT: synthetic account %s (%s) found in live periodic reconciliation poll table", pa.ID(), pa.ExternalAccountID())
		}
	}

	// Verify no platform account row claims external ID acct_bloom001
	_, err = repo.GetPlatformAccountByExternalID(ctx, "acct_bloom001")
	if !errors.Is(err, postgres.ErrPlatformAccountNotFound) {
		t.Errorf("expected ErrPlatformAccountNotFound for synthetic acct_bloom001, got %v", err)
	}
}
