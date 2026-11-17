package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
)

// Safe test database connection resolution.
// Validates explicitly that port is exactly 54329, host is local (127.0.0.1 or localhost),
// database name is exactly flowcard_onboarding_test, and refuses any remote or unapproved database connections.
// Never logs or prints full DSN credentials.
func getTestDatabaseConfig(t *testing.T) (*pgxpool.Config, string) {
	t.Helper()

	url := os.Getenv("POLICY_ACCEPTANCE_DATABASE_URL")
	if url == "" {
		url = "postgres://test_onboarding:test_password@127.0.0.1:54329/flowcard_onboarding_test?sslmode=disable"
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("SAFETY CHECK FAILED: unable to parse test database configuration")
	}

	// 1. Exact port restriction: must be 54329 (isolated disposable test container)
	if cfg.ConnConfig.Port != 54329 {
		t.Fatalf("SAFETY CHECK FAILED: port %d is not the dedicated disposable test port (54329); refusing to connect", cfg.ConnConfig.Port)
	}

	// 2. Exact local host restriction
	host := cfg.ConnConfig.Host
	if host != "127.0.0.1" && host != "localhost" {
		t.Fatalf("SAFETY CHECK FAILED: host %q is not local; refusing remote database connection", host)
	}

	// 3. Exact database name restriction
	dbName := strings.ToLower(cfg.ConnConfig.Database)
	if dbName != "flowcard_onboarding_test" {
		t.Fatalf("SAFETY CHECK FAILED: database name %q is not flowcard_onboarding_test; refusing to connect to protect other databases", cfg.ConnConfig.Database)
	}

	return cfg, url
}

const testDBPackageLockKey = int64(88888888)

func TestMain(m *testing.M) {
	url := os.Getenv("POLICY_ACCEPTANCE_DATABASE_URL")
	if url == "" {
		url = "postgres://test_onboarding:test_password@127.0.0.1:54329/flowcard_onboarding_test?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil || cfg.ConnConfig.Port != 54329 {
		os.Exit(m.Run())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		os.Exit(m.Run())
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		os.Exit(m.Run())
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", testDBPackageLockKey); err != nil {
		os.Exit(m.Run())
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", testDBPackageLockKey)
	}()

	code := m.Run()
	os.Exit(code)
}

func setupTestDB(t *testing.T) (*pgxpool.Pool, *postgres.Repository) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, _ := getTestDatabaseConfig(t)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test database at %s:%d: %v", cfg.ConnConfig.Host, cfg.ConnConfig.Port, err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping failed to test database at %s:%d: %v", cfg.ConnConfig.Host, cfg.ConnConfig.Port, err)
	}

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to run migrations on test database: %v", err)
	}

	cleanTestDB(t, pool)

	t.Cleanup(func() {
		pool.Close()
	})

	return pool, postgres.NewRepository(pool)
}

func cleanTestDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Double-check connected database name from the server before executing TRUNCATE
	var currentDB string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&currentDB); err != nil {
		t.Fatalf("failed to query current database: %v", err)
	}
	if currentDB != "flowcard_onboarding_test" {
		t.Fatalf("SAFETY CHECK FAILED: refusing to truncate non-test database: %s", currentDB)
	}

	_, err := pool.Exec(ctx, `
		TRUNCATE onboarding.webhook_inbox,
		         onboarding.platform_creation_coordinators,
		         onboarding.submission_dispatch_steps,
		         onboarding.submission_document_uploads,
		         onboarding.outbox_messages,
		         onboarding.idempotency_keys,
		         onboarding.submissions,
		         onboarding.applications,
		         onboarding.platform_accounts,
		         onboarding.companies CASCADE;
	`)
	if err != nil {
		t.Fatalf("failed to clean test schema: %v", err)
	}
}

func helperValidCompany(t *testing.T, id string) *domain.Company {
	t.Helper()
	comp, err := domain.NewDraftCompany(id, "Test Corp "+id)
	if err != nil {
		t.Fatalf("NewDraftCompany failed: %v", err)
	}
	return comp
}

func helperValidDocument(id string) domain.Document {
	return domain.Document{
		ID:          id,
		Type:        "CERTIFICATE_OF_INCORPORATION",
		FileName:    "cert.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024 * 100, // 100 KiB
		StorageKey:  "storage/keys/doc_001.pdf",
		UploadedBy:  "compliance_user",
		UploadedAt:  time.Now().UTC(),
	}
}

func helperValidConsent() domain.CustomerAgreements {
	return domain.CustomerAgreements{
		AgreedToTermsAndConditions: true,
		AgreedToDataUsage:          true,
		TermsAndConditions: &domain.TermsAndConditionsAgreement{
			AgreedAt:             time.Now().UTC(),
			ServiceAgreementType: "FULL",
			DeviceData: &domain.DeviceData{
				IPAddress: "192.168.1.1",
				UserAgent: "Mozilla/5.0 TestBrowser",
			},
		},
	}
}

// 1. Migrations: Idempotency, Advisory Lock Concurrency, and Drift Verification
func TestMigrationsRunAndIdempotency(t *testing.T) {
	pool, _ := setupTestDB(t)
	ctx := context.Background()

	// Running migrations a second time must be completely idempotent
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("second RunMigrations call failed: %v", err)
	}

	var version int
	var checksum string
	err := pool.QueryRow(ctx, "SELECT version, checksum FROM onboarding.schema_migrations WHERE version = 1").Scan(&version, &checksum)
	if err != nil {
		t.Fatalf("failed to query schema_migrations: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}
	if checksum == "" {
		t.Errorf("checksum is empty")
	}
}

func TestConcurrentMigrationsRunSerialized(t *testing.T) {
	pool, _ := setupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := postgres.RunMigrations(ctx, pool); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent RunMigrations error: %v", err)
	}
}

func TestMigrationDriftDetected(t *testing.T) {
	pool, _ := setupTestDB(t)
	ctx := context.Background()

	// Tamper with the recorded checksum in the database with valid 64-character hex hash
	h := sha256.Sum256([]byte("tampered"))
	tamperedChecksum := hex.EncodeToString(h[:])
	_, err := pool.Exec(ctx, "UPDATE onboarding.schema_migrations SET checksum = $1 WHERE version = 1", tamperedChecksum)
	if err != nil {
		t.Fatalf("failed to tamper checksum: %v", err)
	}

	// Re-running migrations must detect drift and fail
	err = postgres.RunMigrations(ctx, pool)
	if err == nil || !errors.Is(err, postgres.ErrMigrationDrift) {
		t.Fatalf("expected ErrMigrationDrift, got: %v", err)
	}

	// Restore valid checksum
	for _, m := range postgres.AllMigrations() {
		if m.Version == 1 {
			_, _ = pool.Exec(ctx, "UPDATE onboarding.schema_migrations SET checksum = $1 WHERE version = 1", m.Checksum)
		}
	}
}

// 2. Runtime Role Least Privilege Verification (using SET ROLE without hardcoded credentials)
func TestRuntimeRoleLeastPrivilege(t *testing.T) {
	pool, _ := setupTestDB(t) // Ensure schema and role exist
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire connection: %v", err)
	}
	defer conn.Release()

	// Grant membership of grant role onboarding_service to current user for testing session
	if _, err := conn.Exec(ctx, "GRANT onboarding_service TO CURRENT_USER"); err != nil {
		t.Fatalf("failed to grant onboarding_service role for test: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "REVOKE onboarding_service FROM CURRENT_USER")
	}()

	// Switch session role to onboarding_service
	if _, err := conn.Exec(ctx, "SET ROLE onboarding_service"); err != nil {
		t.Fatalf("failed to SET ROLE onboarding_service: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "RESET ROLE")
	}()

	// A. Runtime role can insert into onboarding.companies
	compID := fmt.Sprintf("comp_role_%d", time.Now().UnixNano())
	now := time.Now().UTC()
	_, err = conn.Exec(ctx, `
		INSERT INTO onboarding.companies (id, legal_name, status, revision, created_at, updated_at)
		VALUES ($1, 'Role Test Corp', 'DRAFT', 1, $2, $3)
	`, compID, now, now)
	if err != nil {
		t.Fatalf("onboarding_service failed to insert company: %v", err)
	}

	// B. Runtime role CANNOT mutate onboarding.schema_migrations
	_, err = conn.Exec(ctx, `
		INSERT INTO onboarding.schema_migrations (version, name, checksum, applied_at)
		VALUES (999, 'unauthorized', 'hash', now())
	`)
	if err == nil {
		t.Fatalf("onboarding_service unexpectedly succeeded in inserting into onboarding.schema_migrations")
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code != "42501" { // 42501 is insufficient_privilege
		t.Errorf("expected pgcode 42501 (insufficient_privilege), got %s: %v", pgErr.Code, err)
	}

	// C. Runtime role CANNOT create table in onboarding schema
	_, err = conn.Exec(ctx, `CREATE TABLE onboarding.unauthorized_tbl (id INT)`)
	if err == nil {
		t.Fatalf("onboarding_service unexpectedly succeeded in creating table in onboarding schema")
	}
}

// 3. Company Isolation and Duplicate Legal Identities
func TestCompanyIsolationAndDuplicateLegalIdentities(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	c1 := helperValidCompany(t, fmt.Sprintf("comp_iso_1_%d", time.Now().UnixNano()))
	c2 := helperValidCompany(t, fmt.Sprintf("comp_iso_2_%d", time.Now().UnixNano()))

	sharedEIN := domain.RegistrationIdentifier{
		Type:           "EIN",
		Value:          "12-3456789",
		IssuingCountry: "US",
	}
	_ = c1.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
		RegistrationIdentifiers: []domain.RegistrationIdentifier{sharedEIN},
	})
	_ = c2.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
		RegistrationIdentifiers: []domain.RegistrationIdentifier{sharedEIN},
	})

	if err := repo.CreateCompany(ctx, c1); err != nil {
		t.Fatalf("CreateCompany(c1) failed: %v", err)
	}
	if err := repo.CreateCompany(ctx, c2); err != nil {
		t.Fatalf("CreateCompany(c2) failed: %v", err)
	}

	fetched1, err := repo.GetCompany(ctx, c1.ID())
	if err != nil {
		t.Fatalf("GetCompany(c1) failed: %v", err)
	}
	fetched2, err := repo.GetCompany(ctx, c2.ID())
	if err != nil {
		t.Fatalf("GetCompany(c2) failed: %v", err)
	}

	if fetched1.ID() == fetched2.ID() {
		t.Errorf("expected distinct companies, got identical IDs")
	}
}

// 4. Archive vs Application Create Serialization
func TestArchiveVsApplicationCreateSerialization(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	t.Run("CreateApplicationOnArchivedCompanyRejected", func(t *testing.T) {
		compID := fmt.Sprintf("comp_archived_%d", time.Now().UnixNano())
		c := helperValidCompany(t, compID)
		_ = repo.CreateCompany(ctx, c)

		if err := repo.ArchiveCompany(ctx, compID); err != nil {
			t.Fatalf("ArchiveCompany failed: %v", err)
		}

		app, _ := domain.NewDraftApplication(fmt.Sprintf("app_%d", time.Now().UnixNano()), c, "creator")
		err := repo.CreateApplication(ctx, app)
		if err == nil || !errors.Is(err, domain.ErrCompanyArchived) {
			t.Fatalf("CreateApplication on archived company error = %v, want %v", err, domain.ErrCompanyArchived)
		}
	})

	t.Run("ArchiveCompanyWithOpenApplicationRejected", func(t *testing.T) {
		compID := fmt.Sprintf("comp_open_%d", time.Now().UnixNano())
		c := helperValidCompany(t, compID)
		_ = repo.CreateCompany(ctx, c)

		app, _ := domain.NewDraftApplication(fmt.Sprintf("app_open_%d", time.Now().UnixNano()), c, "creator")
		if err := repo.CreateApplication(ctx, app); err != nil {
			t.Fatalf("CreateApplication failed: %v", err)
		}

		err := repo.ArchiveCompany(ctx, compID)
		if err == nil || !errors.Is(err, postgres.ErrCannotArchiveWithOpenApplications) {
			t.Fatalf("ArchiveCompany with open application error = %v, want %v", err, postgres.ErrCannotArchiveWithOpenApplications)
		}
	})

	t.Run("ConcurrentCreateApplicationAndArchiveSerialized", func(t *testing.T) {
		compID := fmt.Sprintf("comp_race_%d", time.Now().UnixNano())
		c := helperValidCompany(t, compID)
		_ = repo.CreateCompany(ctx, c)

		app, _ := domain.NewDraftApplication(fmt.Sprintf("app_race_%d", time.Now().UnixNano()), c, "creator")

		var wg sync.WaitGroup
		wg.Add(2)
		var createErr, archiveErr error

		go func() {
			defer wg.Done()
			createErr = repo.CreateApplication(ctx, app)
		}()

		go func() {
			defer wg.Done()
			archiveErr = repo.ArchiveCompany(ctx, compID)
		}()

		wg.Wait()

		if createErr == nil && archiveErr == nil {
			t.Fatalf("both concurrent operations succeeded; mutual exclusion violated")
		}
	})
}

// 5. One Open Application per Company/Platform
func TestOneOpenApplicationPerCompanyPlatform(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_app_lim_%d", time.Now().UnixNano())
	c := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, c)

	app1, _ := domain.NewDraftApplication(fmt.Sprintf("app1_%d", time.Now().UnixNano()), c, "creator")
	if err := repo.CreateApplication(ctx, app1); err != nil {
		t.Fatalf("CreateApplication(app1) failed: %v", err)
	}

	// Competing open application for same platform rejected by partial index
	app2, _ := domain.NewDraftApplication(fmt.Sprintf("app2_%d", time.Now().UnixNano()), c, "creator")
	err := repo.CreateApplication(ctx, app2)
	if err == nil || !errors.Is(err, postgres.ErrOpenApplicationExists) {
		t.Fatalf("competing open application error = %v, want %v", err, postgres.ErrOpenApplicationExists)
	}

	// Close app1 (CANCELLED is terminal)
	revBefore := app1.Revision()
	_ = app1.Cancel("admin")
	if err := repo.UpdateApplication(ctx, app1, revBefore); err != nil {
		t.Fatalf("UpdateApplication(app1 CANCELLED) failed: %v", err)
	}

	// Now app2 can be created
	if err := repo.CreateApplication(ctx, app2); err != nil {
		t.Fatalf("CreateApplication(app2) failed after app1 was CANCELLED: %v", err)
	}
}

// 6. Platform Account Constraints
func TestPlatformAccountConstraints(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID1 := fmt.Sprintf("comp_pa1_%d", time.Now().UnixNano())
	c1 := helperValidCompany(t, compID1)
	_ = repo.CreateCompany(ctx, c1)

	compID2 := fmt.Sprintf("comp_pa2_%d", time.Now().UnixNano())
	c2 := helperValidCompany(t, compID2)
	_ = repo.CreateCompany(ctx, c2)

	// 1. One platform account per Company/platform/environment
	pa1, _ := domain.NewPlatformAccount(fmt.Sprintf("pa_1_%d", time.Now().UnixNano()), compID1, "AIRWALLEX", "sandbox")
	if err := repo.CreatePlatformAccount(ctx, pa1); err != nil {
		t.Fatalf("CreatePlatformAccount(pa1) failed: %v", err)
	}

	paDuplicate, _ := domain.NewPlatformAccount(fmt.Sprintf("pa_dup_%d", time.Now().UnixNano()), compID1, "AIRWALLEX", "sandbox")
	err := repo.CreatePlatformAccount(ctx, paDuplicate)
	if err == nil || !errors.Is(err, postgres.ErrPlatformAccountExists) {
		t.Fatalf("CreatePlatformAccount duplicate error = %v, want %v", err, postgres.ErrPlatformAccountExists)
	}

	// 2. Unique external account ID per platform/environment
	revBefore := pa1.Revision()
	_ = pa1.SetExternalAccountID("ext_shared_123")
	if err := repo.UpdatePlatformAccount(ctx, pa1, revBefore); err != nil {
		t.Fatalf("UpdatePlatformAccount(pa1) failed: %v", err)
	}

	pa2, _ := domain.NewPlatformAccount(fmt.Sprintf("pa_2_%d", time.Now().UnixNano()), compID2, "AIRWALLEX", "sandbox")
	_ = pa2.SetExternalAccountID("ext_shared_123") // Duplicate external ID
	err = repo.CreatePlatformAccount(ctx, pa2)
	if err == nil || !errors.Is(err, postgres.ErrExternalAccountIDAlreadyLinked) {
		t.Fatalf("CreatePlatformAccount with duplicate external ID error = %v, want %v", err, postgres.ErrExternalAccountIDAlreadyLinked)
	}
}

// 7. Optimistic CAS Lost Update Protection
func TestOptimisticCASLostUpdateProtection(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_cas_%d", time.Now().UnixNano())
	c := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, c)

	copyA, _ := repo.GetCompany(ctx, compID)
	copyB, _ := repo.GetCompany(ctx, compID)

	loadedRev := copyA.Revision()

	// Update copyA (multiple in-memory mutations)
	_ = copyA.UpdateTradingName("First Update")
	_ = copyA.UpdateProfile(domain.BusinessProfile{Website: "https://first.example.com"})
	if err := repo.UpdateCompany(ctx, copyA, loadedRev); err != nil {
		t.Fatalf("UpdateCompany(copyA) failed: %v", err)
	}

	// Update copyB with stale revision -> conflict!
	_ = copyB.UpdateTradingName("Stale Update")
	err := repo.UpdateCompany(ctx, copyB, loadedRev)
	if err == nil || !errors.Is(err, postgres.ErrOptimisticLockConflict) {
		t.Fatalf("UpdateCompany(copyB) with stale revision error = %v, want %v", err, postgres.ErrOptimisticLockConflict)
	}
}

// 8. Atomic Submission, Company History Lock, Outbox, and Idempotency
func TestAtomicSubmissionOutboxIdempotency(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_atom_%d", time.Now().UnixNano())
	c := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, c)

	appID := fmt.Sprintf("app_atom_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, c, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	doc := helperValidDocument("doc_at")
	_ = app.AttachDocument(doc)
	_ = repo.CreateApplication(ctx, app)

	subID := fmt.Sprintf("sub_atom_%d", time.Now().UnixNano())
	sub, err := app.InitiateInitialSubmission(subID, "actor", []string{"doc_at"})
	if err != nil {
		t.Fatalf("InitiateInitialSubmission failed: %v", err)
	}

	idempKey := fmt.Sprintf("idemp_%d", time.Now().UnixNano())
	params := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision() - 1, // Application revision before InitiateInitialSubmission
		SubmissionID:        sub.ID,
		SubmittingActor:     sub.SubmittingActor,
		DocumentIDs:         sub.DocumentIDs,
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      idempKey,
		RequestFingerprint:  "fingerprint_sha256_canonical",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}

	// A. Execute atomic submission transaction
	res, err := repo.InitiateSubmission(ctx, params)
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}
	if res.Replayed {
		t.Errorf("res.Replayed = true on first execution, want false")
	}

	// B. Verify Company has_submitted_history is atomically set to true
	companyReloaded, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	if !companyReloaded.HasSubmittedHistory() {
		t.Errorf("company HasSubmittedHistory() = false, want true")
	}

	// C. Verify outbox message was written
	claim, err := repo.ClaimOutboxMessage(ctx, 5*time.Second)
	if err != nil || claim == nil {
		t.Fatalf("ClaimOutboxMessage failed: %v", err)
	}

	// D. Replay with identical key & fingerprint returns durable original response (Replayed: true)
	resReplay, err := repo.InitiateSubmission(ctx, params)
	if err != nil {
		t.Fatalf("InitiateSubmission replay failed: %v", err)
	}
	if !resReplay.Replayed {
		t.Errorf("resReplay.Replayed = false on replay, want true")
	}
	if resReplay.ResponseStatusCode != 201 {
		t.Errorf("ResponseStatusCode = %d, want 201", resReplay.ResponseStatusCode)
	}

	// E. Call with same key but different fingerprint returns ErrIdempotencyKeyPayloadMismatch
	conflictParams := params
	conflictParams.RequestFingerprint = "different_fingerprint_hash"
	_, err = repo.InitiateSubmission(ctx, conflictParams)
	if err == nil || !errors.Is(err, postgres.ErrIdempotencyKeyPayloadMismatch) {
		t.Fatalf("conflict error = %v, want %v", err, postgres.ErrIdempotencyKeyPayloadMismatch)
	}
}

// 9. Concurrent Fresh Key Idempotency Races with Differing Submission IDs
func TestConcurrentFreshIdempotencyKeysWithVaryingSubmissions(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_idemp_race_%d", time.Now().UnixNano())
	c := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, c)

	appID := fmt.Sprintf("app_idemp_race_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, c, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = repo.CreateApplication(ctx, app)

	sharedKey := fmt.Sprintf("shared_key_%d", time.Now().UnixNano())
	sharedFingerprint := "fp_shared_canonical"

	// 5 concurrent attempts with the EXACT SAME idempotency key and fingerprint,
	// but differing generated submission IDs (simulating concurrent retry races)
	var wg sync.WaitGroup
	results := make([]*postgres.InitiateSubmissionResult, 5)
	errs := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sub := &domain.Submission{
				ID:              fmt.Sprintf("sub_race_%d", idx),
				Type:            domain.SubmissionTypeInitialApplication,
				Status:          domain.SubmissionStatusPending,
				SubmittingActor: "actor",
				CreatedAt:       time.Now().UTC(),
			}
			params := postgres.InitiateSubmissionParams{
				ApplicationID:       appID,
				ExpectedAppRevision: app.Revision(),
				SubmissionID:        sub.ID,
				SubmittingActor:     sub.SubmittingActor,
				DocumentIDs:         sub.DocumentIDs,
				OutboxTopic:         "onboarding.submission.v1",
				OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
				IdempotencyScope:    "race_scope",
				IdempotencyKey:      sharedKey,
				RequestFingerprint:  sharedFingerprint,
				ResponseStatusCode:  201,
				ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
			}
			res, err := repo.InitiateSubmission(ctx, params)
			results[idx] = res
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	// Verify all returned without error, exactly one executed fresh (Replayed == false),
	// and the others returned the identical original completed response (Replayed == true)
	firstExecCount := 0
	replayCount := 0
	for i := 0; i < 5; i++ {
		if errs[i] != nil {
			t.Fatalf("call %d failed: %v", i, errs[i])
		}
		if results[i].ResponseStatusCode != 201 {
			t.Errorf("call %d StatusCode = %d, want 201", i, results[i].ResponseStatusCode)
		}
		if results[i].Replayed {
			replayCount++
		} else {
			firstExecCount++
		}
	}

	if firstExecCount != 1 {
		t.Errorf("firstExecCount = %d, want exactly 1", firstExecCount)
	}
	if replayCount != 4 {
		t.Errorf("replayCount = %d, want exactly 4", replayCount)
	}

	// Verify exactly ONE submission was persisted in the database
	reloaded, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if len(reloaded.Submissions()) != 1 {
		t.Fatalf("len(Submissions()) = %d, want 1", len(reloaded.Submissions()))
	}
}

// 10. Monotonic Company Submitted History Guard
func TestMonotonicHasSubmittedHistory(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_mono_%d", time.Now().UnixNano())
	c := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, c)

	appID := fmt.Sprintf("app_mono_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, c, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = repo.CreateApplication(ctx, app)

	sub, _ := app.InitiateInitialSubmission("sub_mono_1", "actor", nil)
	params := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision() - 1,
		SubmissionID:        sub.ID,
		SubmittingActor:     sub.SubmittingActor,
		DocumentIDs:         sub.DocumentIDs,
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "mono_scope",
		IdempotencyKey:      fmt.Sprintf("key_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_mono",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	if _, err := repo.InitiateSubmission(ctx, params); err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	// Company now has has_submitted_history = TRUE and revision = 2
	compAfterSub, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	if !compAfterSub.HasSubmittedHistory() {
		t.Fatalf("expected HasSubmittedHistory() = true")
	}

	// Attempt to call UpdateCompany using a stale in-memory company where has_submitted_history is false
	err = repo.UpdateCompany(ctx, c, 1) // Stale revision 1
	if err == nil || !errors.Is(err, postgres.ErrOptimisticLockConflict) {
		t.Fatalf("expected ErrOptimisticLockConflict on stale company revision, got: %v", err)
	}

	// Re-verify company has_submitted_history is STILL true
	compCheck, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	if !compCheck.HasSubmittedHistory() {
		t.Errorf("HasSubmittedHistory was cleared! Monotonicity violated")
	}
}

// 11. Deadlock-Free Concurrent Archive vs. InitiateSubmission (Global Lock Order: Company -> Application)
func TestDeadlockFreeConcurrentArchiveAndInitiateSubmission(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	compID := fmt.Sprintf("comp_deadlock_%d", time.Now().UnixNano())
	c := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, c)

	appID := fmt.Sprintf("app_deadlock_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, c, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = repo.CreateApplication(ctx, app)

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	// Launch concurrent Archive and InitiateSubmission operations on the exact same Company and Application
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sub := &domain.Submission{
				ID:              fmt.Sprintf("sub_dl_%d", idx),
				Type:            domain.SubmissionTypeInitialApplication,
				Status:          domain.SubmissionStatusPending,
				SubmittingActor: "actor",
				CreatedAt:       time.Now().UTC(),
			}
			params := postgres.InitiateSubmissionParams{
				ApplicationID:       appID,
				ExpectedAppRevision: app.Revision(),
				SubmissionID:        sub.ID,
				SubmittingActor:     sub.SubmittingActor,
				DocumentIDs:         sub.DocumentIDs,
				OutboxTopic:         "onboarding.submission.v1",
				OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
				IdempotencyScope:    "app_sub_deadlock",
				IdempotencyKey:      fmt.Sprintf("key_dl_%d", idx),
				RequestFingerprint:  fmt.Sprintf("fp_dl_%d", idx),
				ResponseStatusCode:  201,
				ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
			}
			_, err := repo.InitiateSubmission(ctx, params)
			if err != nil && !errors.Is(err, postgres.ErrOptimisticLockConflict) && !errors.Is(err, domain.ErrCompanyArchived) {
				errCh <- err
			}
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := repo.ArchiveCompany(ctx, compID)
			if err != nil && !errors.Is(err, postgres.ErrCannotArchiveWithOpenApplications) {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("unexpected error during concurrent archive and submission: %v", err)
	}
}

// 12. Fenced Idempotency Reservations with Leases and Crash Recovery
func TestFencedIdempotencyReservationsAndCrashRecovery(t *testing.T) {
	pool, repo := setupTestDB(t)
	ctx := context.Background()

	scope := "test_scope"
	key := fmt.Sprintf("key_%d", time.Now().UnixNano())
	fingerprint := "fingerprint_123"

	// 1. Reserve the key with enough lease time for the active-reservation checks.
	res1, err := repo.ReserveIdempotencyKey(ctx, scope, key, fingerprint, time.Minute)
	if err != nil {
		t.Fatalf("ReserveIdempotencyKey failed: %v", err)
	}
	if !res1.Reserved || res1.ReservationToken == "" {
		t.Fatalf("expected active reservation with non-empty token")
	}

	// 2. Competing reservation while active returns ErrConcurrentOperationInProgress
	_, err = repo.ReserveIdempotencyKey(ctx, scope, key, fingerprint, 5*time.Second)
	if err == nil || !errors.Is(err, postgres.ErrConcurrentOperationInProgress) {
		t.Fatalf("competing active reservation error = %v, want %v", err, postgres.ErrConcurrentOperationInProgress)
	}

	// 3. Stale completion with wrong reservation token rejected
	body := []byte(`{"result":"success"}`)
	err = repo.CompleteIdempotencyKey(ctx, scope, key, "wrong_token", 200, body)
	if err == nil || !errors.Is(err, postgres.ErrInvalidIdempotencyReservationToken) {
		t.Fatalf("wrong token error = %v, want %v", err, postgres.ErrInvalidIdempotencyReservationToken)
	}

	// 4. Simulate a crashed worker by expiring its lease directly in the test database.
	tag, err := pool.Exec(ctx, `
		UPDATE onboarding.idempotency_keys
		SET expires_at = now() - interval '1 second'
		WHERE key_scope = $1 AND idempotency_key = $2
	`, scope, key)
	if err != nil {
		t.Fatalf("failed to expire crashed reservation: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expiring crashed reservation updated %d rows, want 1", tag.RowsAffected())
	}

	// 5. Expired reservation reclaimed by new worker
	res2, err := repo.ReserveIdempotencyKey(ctx, scope, key, fingerprint, 5*time.Second)
	if err != nil {
		t.Fatalf("reclaiming expired reservation failed: %v", err)
	}
	if !res2.Reserved {
		t.Fatalf("expected res2.Reserved = true")
	}

	// 6. Complete execution with valid reclaimed token
	if err := repo.CompleteIdempotencyKey(ctx, scope, key, res2.ReservationToken, 200, body); err != nil {
		t.Fatalf("CompleteIdempotencyKey failed: %v", err)
	}

	// 7. Subsequent replay returns durable completed response
	replay, err := repo.ReserveIdempotencyKey(ctx, scope, key, fingerprint, 5*time.Second)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if replay.Reserved {
		t.Errorf("replay Reserved = true, want false")
	}
	var gotBody, wantBody map[string]any
	_ = json.Unmarshal(replay.ResponseBody, &gotBody)
	_ = json.Unmarshal(body, &wantBody)
	if !reflect.DeepEqual(gotBody, wantBody) {
		t.Errorf("ResponseBody = %v, want %v", gotBody, wantBody)
	}
}

// 13. Outbox Lease Expiry Fencing (Before Reclaim and After Reclaim)
func TestOutboxLeaseExpiryFencing(t *testing.T) {
	pool, repo := setupTestDB(t)
	ctx := context.Background()

	t.Run("ExpiredBeforeReclaimRejected", func(t *testing.T) {
		_, _ = pool.Exec(ctx, "DELETE FROM onboarding.outbox_messages")
		msgID := fmt.Sprintf("msg_exp1_%d", time.Now().UnixNano())
		now := time.Now().UTC()
		_, err := pool.Exec(ctx, `
			INSERT INTO onboarding.outbox_messages (id, topic, payload, status, created_at, updated_at)
			VALUES ($1, 'test.topic', '{"payload":1}', 'PENDING', $2, $3)
		`, msgID, now, now)
		if err != nil {
			t.Fatalf("failed to insert test outbox message: %v", err)
		}

		claim, err := repo.ClaimOutboxMessage(ctx, 40*time.Millisecond)
		if err != nil || claim == nil {
			t.Fatalf("ClaimOutboxMessage failed: %v", err)
		}

		// Wait for lease to expire according to server DB clock
		time.Sleep(60 * time.Millisecond)

		// Worker attempts to complete message AFTER expiry without another worker reclaiming -> strictly rejected!
		err = repo.CompleteOutboxMessage(ctx, msgID, claim.LeaseToken)
		if err == nil || !errors.Is(err, postgres.ErrStaleLeaseToken) {
			t.Fatalf("expired completion error = %v, want %v", err, postgres.ErrStaleLeaseToken)
		}
	})

	t.Run("ExpiredAfterReclaimRejected", func(t *testing.T) {
		_, _ = pool.Exec(ctx, "DELETE FROM onboarding.outbox_messages")
		msgID := fmt.Sprintf("msg_exp2_%d", time.Now().UnixNano())
		now := time.Now().UTC()
		_, err := pool.Exec(ctx, `
			INSERT INTO onboarding.outbox_messages (id, topic, payload, status, created_at, updated_at)
			VALUES ($1, 'test.topic', '{"payload":2}', 'PENDING', $2, $3)
		`, msgID, now, now)
		if err != nil {
			t.Fatalf("failed to insert test outbox message: %v", err)
		}

		claimA, err := repo.ClaimOutboxMessage(ctx, 40*time.Millisecond)
		if err != nil || claimA == nil {
			t.Fatalf("claimA failed: %v", err)
		}

		time.Sleep(60 * time.Millisecond)

		// Worker B reclaims message with new token
		claimB, err := repo.ClaimOutboxMessage(ctx, 5*time.Second)
		if err != nil || claimB == nil {
			t.Fatalf("claimB failed: %v", err)
		}

		// Worker A tries to complete -> rejected!
		err = repo.CompleteOutboxMessage(ctx, msgID, claimA.LeaseToken)
		if err == nil || !errors.Is(err, postgres.ErrStaleLeaseToken) {
			t.Fatalf("Worker A stale completion error = %v, want %v", err, postgres.ErrStaleLeaseToken)
		}

		// Worker B completes -> succeeds!
		if err := repo.CompleteOutboxMessage(ctx, msgID, claimB.LeaseToken); err != nil {
			t.Fatalf("Worker B completion failed: %v", err)
		}
	})
}

// 14. Child Submission Status Updates in UpdateApplication
func TestChildSubmissionStatusPersistence(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_sub_stat_%d", time.Now().UnixNano())
	c := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, c)

	appID := fmt.Sprintf("app_sub_stat_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, c, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = repo.CreateApplication(ctx, app)

	subID := fmt.Sprintf("sub_stat_%d", time.Now().UnixNano())
	sub, _ := app.InitiateInitialSubmission(subID, "actor", nil)

	initParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision() - 1,
		SubmissionID:        sub.ID,
		SubmittingActor:     sub.SubmittingActor,
		DocumentIDs:         sub.DocumentIDs,
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("key_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fingerprint_status_test",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	if _, err := repo.InitiateSubmission(ctx, initParams); err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	// Update application domain state: Submission accepted
	revBefore := app.Revision()
	if err := app.RecordSubmissionAccepted(subID); err != nil {
		t.Fatalf("RecordSubmissionAccepted failed: %v", err)
	}

	// Persist via UpdateApplication
	if err := repo.UpdateApplication(ctx, app, revBefore); err != nil {
		t.Fatalf("UpdateApplication failed: %v", err)
	}

	// Re-query from DB and verify child submission status was updated to ACCEPTED
	reloaded, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if len(reloaded.Submissions()) != 1 {
		t.Fatalf("len(Submissions()) = %d, want 1", len(reloaded.Submissions()))
	}
	if reloaded.Submissions()[0].Status != domain.SubmissionStatusAccepted {
		t.Errorf("submission status = %q, want ACCEPTED", reloaded.Submissions()[0].Status)
	}
	if reloaded.Submissions()[0].AcceptedAt == nil {
		t.Errorf("submission accepted_at is nil")
	}
}

// 15. Full Aggregate Reconstitution Round-Trip
func TestAggregateReconstitutionRoundTrip(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_rt_%d", time.Now().UnixNano())
	comp := helperValidCompany(t, compID)
	_ = comp.UpdateTradingName("Trading Name RT")
	_ = comp.UpdateProfile(domain.BusinessProfile{
		Website:     "https://flowcard.example.com",
		Description: "Financial platform",
	})
	_ = comp.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
			{Type: "STATE_REGISTRATION_NUMBER", Value: "SR-98765", IssuingCountry: "US"},
		},
	})

	dob := domain.Date("1985-04-12")
	issued := domain.Date("2020-01-10")
	expires := domain.Date("2030-01-10")
	_ = comp.AddPerson(domain.Person{
		ID:          "person_1",
		FirstName:   "John",
		LastName:    "Doe",
		Role:        "Director",
		DateOfBirth: dob,
		Identifications: []domain.Identification{
			{
				ID:             "id_doc_1",
				Type:           "PASSPORT",
				Number:         "A12345678",
				IssuingCountry: "US",
				IssuedOn:       issued,
				ExpiresOn:      expires,
			},
		},
	})

	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("CreateCompany failed: %v", err)
	}

	fetchedComp, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}

	if fetchedComp.TradingName() != comp.TradingName() {
		t.Errorf("TradingName = %q, want %q", fetchedComp.TradingName(), comp.TradingName())
	}
	if len(fetchedComp.RegistrationIdentifiers()) != 2 {
		t.Fatalf("len(RegistrationIdentifiers) = %d, want 2", len(fetchedComp.RegistrationIdentifiers()))
	}
	if len(fetchedComp.People()) != 1 {
		t.Fatalf("len(People) = %d, want 1", len(fetchedComp.People()))
	}
	if len(fetchedComp.People()[0].Identifications) != 1 {
		t.Fatalf("len(Identifications) = %d, want 1", len(fetchedComp.People()[0].Identifications))
	}
	if fetchedComp.People()[0].DateOfBirth != dob {
		t.Errorf("DOB = %v, want %v", fetchedComp.People()[0].DateOfBirth, dob)
	}
}

// 16. Terminal Applications Reject Submissions with Zero Mutations
func TestSubmitTerminalApplicationRejectedNoMutation(t *testing.T) {
	pool, repo := setupTestDB(t)
	ctx := context.Background()

	terminalStatuses := []struct {
		name       string
		setupApp   func(app *domain.OnboardingApplication, subID string)
		wantStatus domain.ApplicationStatus
	}{
		{
			name: "CANCELLED",
			setupApp: func(app *domain.OnboardingApplication, subID string) {
				_ = app.Cancel("actor")
			},
			wantStatus: domain.ApplicationStatusCancelled,
		},
		{
			name: "REJECTED_VIA_DEFINITIVE_FAILURE",
			setupApp: func(app *domain.OnboardingApplication, subID string) {
				_, _ = app.InitiateInitialSubmission(subID, "actor", nil)
				_ = app.RecordSubmissionFailed(subID, true)
			},
			wantStatus: domain.ApplicationStatusRejected,
		},
		{
			name: "REJECTED_VIA_VERIFICATION_REJECTION",
			setupApp: func(app *domain.OnboardingApplication, subID string) {
				_, _ = app.InitiateInitialSubmission(subID, "actor", nil)
				_ = app.RecordSubmissionAccepted(subID)
				_ = app.RecordPlatformVerificationRejection("actor")
			},
			wantStatus: domain.ApplicationStatusRejected,
		},
		{
			name: "COMPLETED",
			setupApp: func(app *domain.OnboardingApplication, subID string) {
				_, _ = app.InitiateInitialSubmission(subID, "actor", nil)
				_ = app.RecordSubmissionAccepted(subID)
				pa, _ := domain.ReconstitutePlatformAccount(domain.ReconstitutePlatformAccountParams{
					ID:           "pa_term_" + subID,
					CompanyID:    app.Company().ID(),
					PlatformName: app.Platform().Name,
					Environment:  app.Platform().Environment,
					Status:       domain.PlatformAccountStatusActive,
					Revision:     1,
					CreatedAt:    time.Now().UTC(),
					UpdatedAt:    time.Now().UTC(),
				})
				_ = app.RecordPlatformAccountActivation(pa)
			},
			wantStatus: domain.ApplicationStatusCompleted,
		},
	}

	for i, tc := range terminalStatuses {
		t.Run(tc.name, func(t *testing.T) {
			compID := fmt.Sprintf("comp_t%d_%d", i, time.Now().UnixNano())
			c := helperValidCompany(t, compID)
			if err := repo.CreateCompany(ctx, c); err != nil {
				t.Fatalf("CreateCompany failed: %v", err)
			}

			appID := fmt.Sprintf("app_t%d_%d", i, time.Now().UnixNano())
			app, _ := domain.NewDraftApplication(appID, c, "creator")
			_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})

			subID := fmt.Sprintf("init_sub_%d_%d", i, time.Now().UnixNano())
			tc.setupApp(app, subID)

			if app.Status() != tc.wantStatus {
				t.Fatalf("setup status = %v, want %v", app.Status(), tc.wantStatus)
			}

			// Persist terminal application
			if err := repo.CreateApplication(ctx, app); err != nil {
				t.Fatalf("CreateApplication failed: %v", err)
			}

			// Capture table row counts and aggregate snapshot before illegal submission attempt
			var compRevBefore int
			var compHasHistoryBefore bool
			err := pool.QueryRow(ctx, "SELECT revision, has_submitted_history FROM onboarding.companies WHERE id = $1", compID).Scan(&compRevBefore, &compHasHistoryBefore)
			if err != nil {
				t.Fatalf("query company before failed: %v", err)
			}

			var appRevBefore int
			var appStatusBefore string
			err = pool.QueryRow(ctx, "SELECT revision, status FROM onboarding.applications WHERE id = $1", appID).Scan(&appRevBefore, &appStatusBefore)
			if err != nil {
				t.Fatalf("query application before failed: %v", err)
			}

			var subCountBefore, outboxCountBefore, idempCountBefore int
			_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.submissions WHERE application_id = $1", appID).Scan(&subCountBefore)
			_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.outbox_messages").Scan(&outboxCountBefore)
			_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.idempotency_keys").Scan(&idempCountBefore)

			// Attempt InitiateSubmission on terminal application
			attemptSubID := fmt.Sprintf("ill_sub_%d_%d", i, time.Now().UnixNano())
			params := postgres.InitiateSubmissionParams{
				ApplicationID:       appID,
				ExpectedAppRevision: appRevBefore,
				SubmissionID:        attemptSubID,
				SubmittingActor:     "actor",
				OutboxTopic:         "onboarding.submission.v1",
				OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
				IdempotencyScope:    "application_submission",
				IdempotencyKey:      fmt.Sprintf("key_%s_%d", tc.name, time.Now().UnixNano()),
				RequestFingerprint:  "fingerprint_terminal_test",
				ResponseStatusCode:  201,
				ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
			}

			_, err = repo.InitiateSubmission(ctx, params)
			if err == nil {
				t.Fatalf("InitiateSubmission on terminal application %s unexpectedly succeeded", tc.name)
			}
			if !errors.Is(err, domain.ErrApplicationNotDraft) {
				t.Logf("InitiateSubmission on terminal application returned expected domain error: %v", err)
			}

			// Assert ZERO mutations occurred across all database tables
			var compRevAfter int
			var compHasHistoryAfter bool
			err = pool.QueryRow(ctx, "SELECT revision, has_submitted_history FROM onboarding.companies WHERE id = $1", compID).Scan(&compRevAfter, &compHasHistoryAfter)
			if err != nil {
				t.Fatalf("query company after failed: %v", err)
			}
			if compRevAfter != compRevBefore || compHasHistoryAfter != compHasHistoryBefore {
				t.Errorf("company mutated: rev %d -> %d, hasHistory %v -> %v", compRevBefore, compRevAfter, compHasHistoryBefore, compHasHistoryAfter)
			}

			var appRevAfter int
			var appStatusAfter string
			err = pool.QueryRow(ctx, "SELECT revision, status FROM onboarding.applications WHERE id = $1", appID).Scan(&appRevAfter, &appStatusAfter)
			if err != nil {
				t.Fatalf("query application after failed: %v", err)
			}
			if appRevAfter != appRevBefore || appStatusAfter != appStatusBefore {
				t.Errorf("application mutated: rev %d -> %d, status %s -> %s", appRevBefore, appRevAfter, appStatusBefore, appStatusAfter)
			}

			var subCountAfter, outboxCountAfter, idempCountAfter int
			_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.submissions WHERE application_id = $1", appID).Scan(&subCountAfter)
			_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.outbox_messages").Scan(&outboxCountAfter)
			_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.idempotency_keys").Scan(&idempCountAfter)

			if subCountAfter != subCountBefore {
				t.Errorf("submissions count changed: %d -> %d", subCountBefore, subCountAfter)
			}
			if outboxCountAfter != outboxCountBefore {
				t.Errorf("outbox count changed: %d -> %d", outboxCountBefore, outboxCountAfter)
			}
			if idempCountAfter != idempCountBefore {
				t.Errorf("idempotency keys count changed: %d -> %d", idempCountBefore, idempCountAfter)
			}
		})
	}
}

// 17. Edited Company Draft is Frozen Consistently in Application and Submission
func TestEditedCompanyDraftFreezeConsistency(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_freeze_%d", time.Now().UnixNano())
	comp := helperValidCompany(t, compID)
	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("CreateCompany failed: %v", err)
	}

	appID := fmt.Sprintf("app_freeze_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	if err := repo.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication failed: %v", err)
	}

	// Edit company draft details in persistence while application is in DRAFT
	editedTradingName := "Acme Logistics Global Trading"
	editedWebsite := "https://acme-logistics-global.example.com"
	if err := comp.UpdateTradingName(editedTradingName); err != nil {
		t.Fatalf("UpdateTradingName failed: %v", err)
	}
	if err := comp.UpdateProfile(domain.BusinessProfile{
		Website:     editedWebsite,
		Description: "Global freight logistics and financial settlement",
	}); err != nil {
		t.Fatalf("UpdateProfile failed: %v", err)
	}
	if err := repo.UpdateCompany(ctx, comp, 1); err != nil {
		t.Fatalf("UpdateCompany failed: %v", err)
	}

	// Execute InitiateSubmission
	subID := fmt.Sprintf("sub_freeze_%d", time.Now().UnixNano())
	params := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        subID,
		SubmittingActor:     "ops_user",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("key_freeze_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fingerprint_freeze_test",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}

	res, err := repo.InitiateSubmission(ctx, params)
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}
	if res.Replayed {
		t.Errorf("expected fresh execution, got replay")
	}

	// Query reloaded Application
	reloadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}

	// Assert application frozen company snapshot has edited draft details
	if reloadedApp.Company().TradingName() != editedTradingName {
		t.Errorf("application frozen trading name = %q, want %q", reloadedApp.Company().TradingName(), editedTradingName)
	}
	if reloadedApp.Company().BusinessProfile().Website != editedWebsite {
		t.Errorf("application frozen website = %q, want %q", reloadedApp.Company().BusinessProfile().Website, editedWebsite)
	}

	// Assert submission copy has exact same edited draft details
	if len(reloadedApp.Submissions()) != 1 {
		t.Fatalf("len(Submissions) = %d, want 1", len(reloadedApp.Submissions()))
	}
	sub := reloadedApp.Submissions()[0]
	if sub.CompanyCopy == nil {
		t.Fatalf("submission CompanyCopy is nil")
	}
	if sub.CompanyCopy.TradingName() != editedTradingName {
		t.Errorf("submission frozen trading name = %q, want %q", sub.CompanyCopy.TradingName(), editedTradingName)
	}
	if sub.CompanyCopy.BusinessProfile().Website != editedWebsite {
		t.Errorf("submission frozen website = %q, want %q", sub.CompanyCopy.BusinessProfile().Website, editedWebsite)
	}

	// Assert complete semantic equality between application company snapshot and submission company copy
	appCompJSON, _ := json.Marshal(reloadedApp.Company())
	subCompJSON, _ := json.Marshal(sub.CompanyCopy)
	var appCompMap, subCompMap map[string]interface{}
	_ = json.Unmarshal(appCompJSON, &appCompMap)
	_ = json.Unmarshal(subCompJSON, &subCompMap)
	if !reflect.DeepEqual(appCompMap, subCompMap) {
		t.Errorf("application company snapshot and submission company copy drift:\napp: %s\nsub: %s", appCompJSON, subCompJSON)
	}
}

// 18. Missing SubmittingActor Rejected with Zero Mutation & Wrong Company Input Rejected
func TestWrongCompanyInputRejected(t *testing.T) {
	pool, repo := setupTestDB(t)
	ctx := context.Background()

	// Create Company A and Company B
	compAID := fmt.Sprintf("comp_a_%d", time.Now().UnixNano())
	compA := helperValidCompany(t, compAID)
	if err := repo.CreateCompany(ctx, compA); err != nil {
		t.Fatalf("CreateCompany A failed: %v", err)
	}

	compBID := fmt.Sprintf("comp_b_%d", time.Now().UnixNano())
	compB := helperValidCompany(t, compBID)
	if err := repo.CreateCompany(ctx, compB); err != nil {
		t.Fatalf("CreateCompany B failed: %v", err)
	}

	appAID := fmt.Sprintf("app_a_%d", time.Now().UnixNano())
	appA, _ := domain.NewDraftApplication(appAID, compA, "creator")
	_ = appA.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	if err := repo.CreateApplication(ctx, appA); err != nil {
		t.Fatalf("CreateApplication A failed: %v", err)
	}

	// 1. Missing SubmittingActor must return explicit error and perform zero database mutations
	var compRevBefore int
	var compHasHistoryBefore bool
	_ = pool.QueryRow(ctx, "SELECT revision, has_submitted_history FROM onboarding.companies WHERE id = $1", compAID).Scan(&compRevBefore, &compHasHistoryBefore)
	var appRevBefore int
	var appStatusBefore string
	_ = pool.QueryRow(ctx, "SELECT revision, status FROM onboarding.applications WHERE id = $1", appAID).Scan(&appRevBefore, &appStatusBefore)
	var subCountBefore, outboxCountBefore, idempCountBefore int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.submissions WHERE application_id = $1", appAID).Scan(&subCountBefore)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.outbox_messages").Scan(&outboxCountBefore)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.idempotency_keys").Scan(&idempCountBefore)

	emptyActorParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appAID,
		ExpectedAppRevision: appA.Revision(),
		SubmissionID:        fmt.Sprintf("sub_no_actor_%d", time.Now().UnixNano()),
		SubmittingActor:     "", // Explicitly empty actor
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appAID + `"}`),
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("key_no_actor_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fingerprint_no_actor",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	_, err := repo.InitiateSubmission(ctx, emptyActorParams)
	if err == nil {
		t.Fatalf("InitiateSubmission unexpectedly succeeded with empty SubmittingActor")
	}
	if !strings.Contains(err.Error(), "submitting actor must not be empty") {
		t.Errorf("error = %v, want 'submitting actor must not be empty'", err)
	}

	// Verify ZERO mutations occurred across all tables
	var compRevAfter int
	var compHasHistoryAfter bool
	_ = pool.QueryRow(ctx, "SELECT revision, has_submitted_history FROM onboarding.companies WHERE id = $1", compAID).Scan(&compRevAfter, &compHasHistoryAfter)
	if compRevAfter != compRevBefore || compHasHistoryAfter != compHasHistoryBefore {
		t.Errorf("company mutated on empty actor error")
	}
	var appRevAfter int
	var appStatusAfter string
	_ = pool.QueryRow(ctx, "SELECT revision, status FROM onboarding.applications WHERE id = $1", appAID).Scan(&appRevAfter, &appStatusAfter)
	if appRevAfter != appRevBefore || appStatusAfter != appStatusBefore {
		t.Errorf("application mutated on empty actor error")
	}
	var subCountAfter, outboxCountAfter, idempCountAfter int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.submissions WHERE application_id = $1", appAID).Scan(&subCountAfter)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.outbox_messages").Scan(&outboxCountAfter)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM onboarding.idempotency_keys").Scan(&idempCountAfter)
	if subCountAfter != subCountBefore || outboxCountAfter != outboxCountBefore || idempCountAfter != idempCountBefore {
		t.Errorf("row counts changed on empty actor error: subs %d->%d, outbox %d->%d, idemp %d->%d",
			subCountBefore, subCountAfter, outboxCountBefore, outboxCountAfter, idempCountBefore, idempCountAfter)
	}

	// 2. Domain RespondToInformationRequest rejects wrong currentCompany aggregate
	subInitID := fmt.Sprintf("init_sub_a_%d", time.Now().UnixNano())
	initParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appAID,
		ExpectedAppRevision: appA.Revision(),
		SubmissionID:        subInitID,
		SubmittingActor:     "actor",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appAID + `"}`),
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("key_init_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fingerprint_init_a",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	_, err = repo.InitiateSubmission(ctx, initParams)
	if err != nil {
		t.Fatalf("initial InitiateSubmission failed: %v", err)
	}

	reloadedA, _ := repo.GetApplication(ctx, appAID)
	revBeforeA := reloadedA.Revision()
	_ = reloadedA.RecordSubmissionAccepted(subInitID)
	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	_ = reloadedA.ReceiveInformationRequest(domain.InformationRequest{
		ID:        rfiID,
		Questions: []domain.RFIQuestion{{ID: "q1", Text: "Question 1"}},
	})
	_ = repo.UpdateApplication(ctx, reloadedA, revBeforeA)

	// Attempt RFI response passing mismatched company aggregate
	_, err = reloadedA.RespondToInformationRequest(
		"rfi_sub_fail",
		rfiID,
		"actor",
		compB, // Mismatched Company B
		map[string]string{"q1": "Answer"},
		nil,
	)
	if err == nil {
		t.Fatalf("RespondToInformationRequest unexpectedly succeeded with mismatched company B")
	}
	if !strings.Contains(err.Error(), "does not match application company ID") {
		t.Errorf("error = %v, want 'does not match application company ID'", err)
	}
}

// 19. RFI Response Freezes New Company and Preserves INFORMATION_REQUIRED Status
func TestRFIResponseFreezesNewCompanyAndPreservesInformationRequired(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_rfi_freeze_%d", time.Now().UnixNano())
	comp := helperValidCompany(t, compID)
	_ = comp.UpdateTradingName("Original Trading Name")
	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("CreateCompany failed: %v", err)
	}

	appID := fmt.Sprintf("app_rfi_freeze_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	if err := repo.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication failed: %v", err)
	}

	// 1. Initial submission
	initSubID := fmt.Sprintf("init_sub_%d", time.Now().UnixNano())
	initParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        initSubID,
		SubmittingActor:     "creator",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("key_init_rfi_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_init_rfi",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	if _, err := repo.InitiateSubmission(ctx, initParams); err != nil {
		t.Fatalf("initial InitiateSubmission failed: %v", err)
	}

	// 2. Application advances to UNDER_REVIEW then INFORMATION_REQUIRED
	reloaded, _ := repo.GetApplication(ctx, appID)
	revBeforeRFI := reloaded.Revision()
	if err := reloaded.RecordSubmissionAccepted(initSubID); err != nil {
		t.Fatalf("RecordSubmissionAccepted failed: %v", err)
	}
	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	if err := reloaded.ReceiveInformationRequest(domain.InformationRequest{
		ID:        rfiID,
		Questions: []domain.RFIQuestion{{ID: "q1", Text: "Please verify new address"}},
	}); err != nil {
		t.Fatalf("ReceiveInformationRequest failed: %v", err)
	}
	if err := repo.UpdateApplication(ctx, reloaded, revBeforeRFI); err != nil {
		t.Fatalf("UpdateApplication to INFORMATION_REQUIRED failed: %v", err)
	}

	appUnderRFI, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if appUnderRFI.Status() != domain.ApplicationStatusInformationRequired {
		t.Fatalf("status = %v, want INFORMATION_REQUIRED", appUnderRFI.Status())
	}

	// 3. Edit company while in INFORMATION_REQUIRED
	updatedTradingName := "Amended Trading Name After RFI"
	if err := comp.UpdateTradingName(updatedTradingName); err != nil {
		t.Fatalf("UpdateTradingName failed: %v", err)
	}
	// Company revision is now 2 (was 1, advanced to 2 on initial submission)
	compFromDB, _ := repo.GetCompany(ctx, compID)
	if err := repo.UpdateCompany(ctx, comp, compFromDB.Revision()); err != nil {
		t.Fatalf("UpdateCompany failed: %v", err)
	}

	// 4. Submit RFI response via InitiateSubmission
	rfiSubID := fmt.Sprintf("rfi_sub_%d", time.Now().UnixNano())
	rfiParams := postgres.InitiateSubmissionParams{
		ApplicationID:        appID,
		ExpectedAppRevision:  appUnderRFI.Revision(),
		SubmissionID:         rfiSubID,
		SubmittingActor:      "compliance_officer",
		InformationRequestID: rfiID,
		Answers:              map[string]string{"q1": "New address documented and verified"},
		OutboxTopic:          "onboarding.submission.v1",
		OutboxPayload:        []byte(`{"application_id":"` + appID + `","rfi_id":"` + rfiID + `"}`),
		IdempotencyScope:     "application_submission",
		IdempotencyKey:       fmt.Sprintf("key_rfi_resp_%d", time.Now().UnixNano()),
		RequestFingerprint:   "fp_rfi_resp",
		ResponseStatusCode:   200,
		ResponseBody:         []byte(`{"status":"INFORMATION_REQUIRED"}`),
	}

	res, err := repo.InitiateSubmission(ctx, rfiParams)
	if err != nil {
		t.Fatalf("RFI InitiateSubmission failed: %v", err)
	}
	if res.Replayed {
		t.Errorf("expected fresh execution, got replay")
	}

	// 5. Query reloaded Application and assert state integrity
	finalApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication final failed: %v", err)
	}

	// Application status MUST PRESERVE INFORMATION_REQUIRED (NOT forced to SUBMISSION_PENDING!)
	if finalApp.Status() != domain.ApplicationStatusInformationRequired {
		t.Errorf("application status = %v, want INFORMATION_REQUIRED (preserved!)", finalApp.Status())
	}

	// Application's embedded initial company snapshot MUST PRESERVE original trading name
	if finalApp.Company().TradingName() != "Original Trading Name" {
		t.Errorf("application initial company snapshot trading name = %q, want 'Original Trading Name'", finalApp.Company().TradingName())
	}

	// Information request status MUST BE RESPONSE_PENDING
	if len(finalApp.InformationRequests()) != 1 {
		t.Fatalf("len(InformationRequests) = %d, want 1", len(finalApp.InformationRequests()))
	}
	if finalApp.InformationRequests()[0].Status != domain.InformationRequestStatusResponsePending {
		t.Errorf("RFI status = %v, want RESPONSE_PENDING", finalApp.InformationRequests()[0].Status)
	}

	// Submissions check:
	// - Initial submission has "Original Trading Name"
	// - RFI submission has "Amended Trading Name After RFI"
	subs := finalApp.Submissions()
	if len(subs) != 2 {
		t.Fatalf("len(Submissions) = %d, want 2", len(subs))
	}

	initialSub := subs[0]
	if initialSub.Type != domain.SubmissionTypeInitialApplication {
		t.Errorf("initial sub type = %v, want INITIAL_APPLICATION", initialSub.Type)
	}
	if initialSub.CompanyCopy.TradingName() != "Original Trading Name" {
		t.Errorf("initial sub company trading name = %q, want 'Original Trading Name'", initialSub.CompanyCopy.TradingName())
	}

	rfiSub := subs[1]
	if rfiSub.Type != domain.SubmissionTypeInformationResponse {
		t.Errorf("RFI sub type = %v, want INFORMATION_RESPONSE", rfiSub.Type)
	}
	if rfiSub.InformationRequestID != rfiID {
		t.Errorf("RFI sub request ID = %q, want %q", rfiSub.InformationRequestID, rfiID)
	}
	if rfiSub.Answers["q1"] != "New address documented and verified" {
		t.Errorf("RFI sub answer = %q, want 'New address documented and verified'", rfiSub.Answers["q1"])
	}
	if rfiSub.CompanyCopy.TradingName() != updatedTradingName {
		t.Errorf("RFI sub company trading name = %q, want %q (new frozen snapshot!)", rfiSub.CompanyCopy.TradingName(), updatedTradingName)
	}
}

// 20. Eligible Company Deletion Rules
func TestCompanyEligibleDeletion(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	// A. Deletion of unsubmitted company with no apps/accounts succeeds
	compID := fmt.Sprintf("comp_del_%d", time.Now().UnixNano())
	comp := helperValidCompany(t, compID)
	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("CreateCompany failed: %v", err)
	}
	if err := repo.DeleteCompany(ctx, compID); err != nil {
		t.Fatalf("DeleteCompany failed: %v", err)
	}
	// Verify it is gone
	_, err := repo.GetCompany(ctx, compID)
	if !errors.Is(err, postgres.ErrCompanyNotFound) {
		t.Errorf("GetCompany after deletion error = %v, want ErrCompanyNotFound", err)
	}

	// B. Company with submitted history cannot be deleted
	compID2 := fmt.Sprintf("comp_del_sub_%d", time.Now().UnixNano())
	comp2 := helperValidCompany(t, compID2)
	_ = repo.CreateCompany(ctx, comp2)
	appID2 := fmt.Sprintf("app_del_sub_%d", time.Now().UnixNano())
	app2, _ := domain.NewDraftApplication(appID2, comp2, "creator")
	_ = app2.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = repo.CreateApplication(ctx, app2)

	subID2 := fmt.Sprintf("sub_del_%d", time.Now().UnixNano())
	subParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appID2,
		ExpectedAppRevision: app2.Revision(),
		SubmissionID:        subID2,
		SubmittingActor:     "actor",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID2 + `"}`),
		IdempotencyScope:    "app_sub",
		IdempotencyKey:      fmt.Sprintf("key_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_del",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	_, _ = repo.InitiateSubmission(ctx, subParams)

	err = repo.DeleteCompany(ctx, compID2)
	if !errors.Is(err, postgres.ErrCompanyDeletionIneligible) {
		t.Errorf("DeleteCompany on submitted company error = %v, want ErrCompanyDeletionIneligible", err)
	}

	// C. Archived company cannot be deleted
	compID3 := fmt.Sprintf("comp_del_arch_%d", time.Now().UnixNano())
	comp3 := helperValidCompany(t, compID3)
	_ = repo.CreateCompany(ctx, comp3)
	_ = repo.ArchiveCompany(ctx, compID3)

	err = repo.DeleteCompany(ctx, compID3)
	if !errors.Is(err, postgres.ErrCompanyDeletionIneligible) {
		t.Errorf("DeleteCompany on archived company error = %v, want ErrCompanyDeletionIneligible", err)
	}

	// D. Company with an existing application cannot be deleted
	compID4 := fmt.Sprintf("comp_del_app_%d", time.Now().UnixNano())
	comp4 := helperValidCompany(t, compID4)
	_ = repo.CreateCompany(ctx, comp4)
	appID4 := fmt.Sprintf("app_del_draft_%d", time.Now().UnixNano())
	app4, _ := domain.NewDraftApplication(appID4, comp4, "creator")
	_ = repo.CreateApplication(ctx, app4)

	err = repo.DeleteCompany(ctx, compID4)
	if !errors.Is(err, postgres.ErrCompanyDeletionIneligible) {
		t.Errorf("DeleteCompany on company with application error = %v, want ErrCompanyDeletionIneligible", err)
	}
}

// 21. Deterministic Cursor Pagination on Companies and Applications
func TestDeterministicCursorPagination(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	// Create 5 companies
	compIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		compIDs[i] = fmt.Sprintf("comp_page_%d_%d", i, time.Now().UnixNano())
		c := helperValidCompany(t, compIDs[i])
		_ = repo.CreateCompany(ctx, c)
		time.Sleep(5 * time.Millisecond) // Ensure unique timestamps
	}

	// Page 1 with limit 2
	page1, err := repo.ListCompanies(ctx, postgres.ListCompaniesFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListCompanies page 1 failed: %v", err)
	}
	if len(page1.Companies) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(page1.Companies))
	}
	if page1.NextCursor == "" {
		t.Fatalf("page 1 NextCursor is empty")
	}

	// Page 2 with limit 2
	page2, err := repo.ListCompanies(ctx, postgres.ListCompaniesFilter{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("ListCompanies page 2 failed: %v", err)
	}
	if len(page2.Companies) != 2 {
		t.Fatalf("page 2 len = %d, want 2", len(page2.Companies))
	}
	if page2.NextCursor == "" {
		t.Fatalf("page 2 NextCursor is empty")
	}

	// Page 3 with limit 2 (should have 1 item left)
	page3, err := repo.ListCompanies(ctx, postgres.ListCompaniesFilter{Limit: 2, Cursor: page2.NextCursor})
	if err != nil {
		t.Fatalf("ListCompanies page 3 failed: %v", err)
	}
	if len(page3.Companies) != 1 {
		t.Fatalf("page 3 len = %d, want 1", len(page3.Companies))
	}
	if page3.NextCursor != "" {
		t.Errorf("page 3 NextCursor = %q, want empty", page3.NextCursor)
	}

	// Tampered cursor must return ErrInvalidCursor
	_, err = repo.ListCompanies(ctx, postgres.ListCompaniesFilter{Limit: 2, Cursor: "not_a_valid_base64_cursor!!!"})
	if !errors.Is(err, postgres.ErrInvalidCursor) {
		t.Errorf("tampered cursor error = %v, want ErrInvalidCursor", err)
	}

	// Test applications pagination and filters
	appCompID := compIDs[0]
	appComp, _ := repo.GetCompany(ctx, appCompID)
	app1, _ := domain.NewDraftApplication(fmt.Sprintf("app_p1_%d", time.Now().UnixNano()), appComp, "user1")
	_ = repo.CreateApplication(ctx, app1)

	appPage, err := repo.ListApplications(ctx, postgres.ListApplicationsFilter{
		CompanyID: &appCompID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListApplications failed: %v", err)
	}
	if len(appPage.Applications) != 1 {
		t.Errorf("len(appPage.Applications) = %d, want 1", len(appPage.Applications))
	}
}

// 22. Submissions and Platform Account Query Operations
func TestSubmissionsAndPlatformAccountsQueries(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_q_%d", time.Now().UnixNano())
	comp := helperValidCompany(t, compID)
	_ = repo.CreateCompany(ctx, comp)

	appID := fmt.Sprintf("app_q_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})
	_ = repo.CreateApplication(ctx, app)

	subID := fmt.Sprintf("sub_q_%d", time.Now().UnixNano())
	subParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        subID,
		SubmittingActor:     "actor",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "app_sub",
		IdempotencyKey:      fmt.Sprintf("key_q_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_q",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	_, err := repo.InitiateSubmission(ctx, subParams)
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	// Fetch single submission
	fetchedSub, err := repo.GetSubmission(ctx, subID)
	if err != nil {
		t.Fatalf("GetSubmission failed: %v", err)
	}
	if fetchedSub.ID != subID {
		t.Errorf("fetchedSub.ID = %q, want %q", fetchedSub.ID, subID)
	}

	// List submissions for application
	subs, err := repo.ListSubmissions(ctx, appID)
	if err != nil {
		t.Fatalf("ListSubmissions failed: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len(subs) = %d, want 1", len(subs))
	}

	// Create and list platform accounts
	paID := fmt.Sprintf("pa_q_%d", time.Now().UnixNano())
	pa, _ := domain.NewPlatformAccount(paID, compID, "AIRWALLEX", "sandbox")
	if err := repo.CreatePlatformAccount(ctx, pa); err != nil {
		t.Fatalf("CreatePlatformAccount failed: %v", err)
	}
	pas, err := repo.ListPlatformAccountsByCompany(ctx, compID)
	if err != nil {
		t.Fatalf("ListPlatformAccountsByCompany failed: %v", err)
	}
	if len(pas) != 1 {
		t.Fatalf("len(pas) = %d, want 1", len(pas))
	}
	if pas[0].ID() != paID {
		t.Errorf("pas[0].ID = %q, want %q", pas[0].ID(), paID)
	}
}

// 23. Durable Document Upload Immutability
func TestRepository_RecordDocumentUploadWithLease_Immutability(t *testing.T) {
	_, repo := setupTestDB(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_doc_%d", time.Now().UnixNano())
	comp := helperValidCompany(t, compID)
	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("CreateCompany failed: %v", err)
	}

	appID := fmt.Sprintf("app_doc_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{CustomerAgreements: helperValidConsent()})

	docID := fmt.Sprintf("doc_%d", time.Now().UnixNano())
	testDoc := domain.Document{
		ID:          docID,
		Type:        "CERTIFICATE_OF_INCORPORATION",
		FileName:    "cert.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		StorageKey:  "keys/cert.pdf",
		UploadedBy:  "actor",
		UploadedAt:  time.Now().UTC(),
	}
	if err := app.AttachDocument(testDoc); err != nil {
		t.Fatalf("AttachDocument failed: %v", err)
	}
	if err := repo.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication failed: %v", err)
	}

	subID := fmt.Sprintf("sub_doc_%d", time.Now().UnixNano())
	subParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        subID,
		SubmittingActor:     "actor",
		DocumentIDs:         []string{docID},
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(fmt.Sprintf(`{"application_id":"%s","submission_id":"%s"}`, appID, subID)),
		IdempotencyScope:    "doc_sub",
		IdempotencyKey:      fmt.Sprintf("key_doc_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_doc",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	if _, err := repo.InitiateSubmission(ctx, subParams); err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	msg, err := repo.ClaimOutboxMessage(ctx, 30*time.Second)
	if err != nil || msg == nil {
		t.Fatalf("ClaimOutboxMessage failed: %v (msg=%+v)", err, msg)
	}

	// 1. Initial upload recording succeeds
	if err := repo.RecordDocumentUploadWithLease(ctx, subID, docID, "platform_file_aaa", msg.ID, msg.LeaseToken); err != nil {
		t.Fatalf("initial RecordDocumentUploadWithLease failed: %v", err)
	}

	// 2. Same file ID succeeds idempotently
	if err := repo.RecordDocumentUploadWithLease(ctx, subID, docID, "platform_file_aaa", msg.ID, msg.LeaseToken); err != nil {
		t.Fatalf("idempotent RecordDocumentUploadWithLease failed: %v", err)
	}

	// 3. Different file ID is rejected to guarantee immutability
	err = repo.RecordDocumentUploadWithLease(ctx, subID, docID, "platform_file_DIFFERENT", msg.ID, msg.LeaseToken)
	if err == nil || !errors.Is(err, postgres.ErrDocumentFileIDMismatch) {
		t.Fatalf("expected ErrDocumentFileIDMismatch when attempting to overwrite file ID, got: %v", err)
	}
}
