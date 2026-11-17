package webhooks_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

var (
	testDBOnce sync.Once
	testDBPool *pgxpool.Pool
	testDBErr  error
)

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

func getRealPostgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("POLICY_ACCEPTANCE_DATABASE_URL")
	if url == "" {
		url = "postgres://test_onboarding:test_password@127.0.0.1:54329/flowcard_onboarding_test?sslmode=disable"
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("SAFETY CHECK FAILED: unable to parse test database configuration")
	}

	// Safety check 1: Exact port must be 54329
	if cfg.ConnConfig.Port != 54329 {
		t.Fatalf("SAFETY CHECK FAILED: port %d is not 54329; refusing remote connection", cfg.ConnConfig.Port)
	}

	// Safety check 2: Exact local host
	host := cfg.ConnConfig.Host
	if host != "127.0.0.1" && host != "localhost" {
		t.Fatalf("SAFETY CHECK FAILED: host %q is not local; refusing remote connection", host)
	}

	// Safety check 3: Exact database name
	if strings.ToLower(cfg.ConnConfig.Database) != "flowcard_onboarding_test" {
		t.Fatalf("SAFETY CHECK FAILED: database name %q is not flowcard_onboarding_test", cfg.ConnConfig.Database)
	}

	testDBOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		testDBPool, testDBErr = pgxpool.NewWithConfig(ctx, cfg)
		if testDBErr != nil {
			return
		}

		if err := testDBPool.Ping(ctx); err != nil {
			testDBErr = err
			return
		}

		if err := postgres.RunMigrations(ctx, testDBPool); err != nil {
			testDBErr = err
			return
		}
	})

	if testDBErr != nil {
		t.Fatalf("failed to connect or run migrations on test PostgreSQL (port 54329): %v", testDBErr)
	}

	return testDBPool
}

func cleanRealPostgres(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

func createRealPostgresCompanyAndApplication(
	t *testing.T,
	ctx context.Context,
	repo *postgres.Repository,
	compID, appID, internalPAID, extAccountID string,
	appStatus domain.ApplicationStatus,
) (*domain.Company, *domain.OnboardingApplication, *domain.PlatformAccount) {
	t.Helper()

	comp, err := domain.NewDraftCompany(compID, "Real Postgres Test LLC")
	if err != nil {
		t.Fatalf("failed to construct domain company: %v", err)
	}
	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("failed to create company in DB: %v", err)
	}

	pa, err := domain.NewPlatformAccount(internalPAID, compID, "AIRWALLEX", "sandbox")
	if err != nil {
		t.Fatalf("failed to construct domain platform account: %v", err)
	}
	if err := pa.SetExternalAccountID(extAccountID); err != nil {
		t.Fatalf("failed to set external account ID: %v", err)
	}
	if err := repo.CreatePlatformAccount(ctx, pa); err != nil {
		t.Fatalf("failed to create platform account in DB: %v", err)
	}

	app, err := domain.NewDraftApplication(appID, comp, "test-operator")
	if err != nil {
		t.Fatalf("failed to construct domain onboarding application: %v", err)
	}
	if err := repo.CreateApplication(ctx, app); err != nil {
		t.Fatalf("failed to create application in DB: %v", err)
	}

	// Fetch updated application
	loadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to load created application: %v", err)
	}

	return comp, loadedApp, pa
}

func TestWebhookProcessor_RealPostgres_DuplicateProcessingIdempotency(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	// 1. First insert succeeds
	item := postgres.WebhookInboxItem{
		EventID:     "evt_real_dup_100",
		AccountID:   "acct_real_100",
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_real_dup_100","name":"account.active"}`),
	}
	inserted, err := repo.InsertWebhookOnce(ctx, item)
	if err != nil {
		t.Fatalf("InsertWebhookOnce failed: %v", err)
	}
	if !inserted {
		t.Fatalf("expected first insert to return true")
	}

	// 2. Second insert of identical event ID is idempotent duplicate (returns false, no error)
	inserted2, err := repo.InsertWebhookOnce(ctx, item)
	if err != nil {
		t.Fatalf("second InsertWebhookOnce failed: %v", err)
	}
	if inserted2 {
		t.Fatalf("expected second insert to return false (idempotent duplicate)")
	}
}

func TestWebhookProcessor_RealPostgres_StaleLeaseTokenFencing(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	// Insert item
	item := postgres.WebhookInboxItem{
		EventID:     "evt_real_fence_1",
		AccountID:   "acct_real_fence_1",
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_real_fence_1","name":"account.active"}`),
	}
	if _, err := repo.InsertWebhookOnce(ctx, item); err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	// Claim item to acquire lease
	claimed, err := repo.ClaimPendingWebhooks(ctx, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to claim webhooks: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed item, got %d", len(claimed))
	}
	claimedToken := claimed[0].LeaseToken
	if claimedToken == "" {
		t.Fatalf("expected non-empty lease token on claimed item")
	}

	// Expire the lease explicitly in the database to simulate worker timeout
	_, err = pool.Exec(ctx, `
		UPDATE onboarding.webhook_inbox
		SET lease_expires_at = now() - interval '1 second'
		WHERE event_id = $1
	`, "evt_real_fence_1")
	if err != nil {
		t.Fatalf("failed to expire lease: %v", err)
	}

	// Stale worker tries to complete processing with expired lease token -> MUST be rejected with ErrStaleLeaseToken
	err = repo.MarkWebhookProcessed(ctx, "evt_real_fence_1", claimedToken, postgres.WebhookStatusProcessed, "")
	if !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken for expired lease, got: %v", err)
	}

	// Verify row in database remains unprocessed
	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM onboarding.webhook_inbox WHERE event_id = $1", "evt_real_fence_1").Scan(&status)
	if err != nil {
		t.Fatalf("failed to query webhook status: %v", err)
	}
	if status == string(postgres.WebhookStatusProcessed) {
		t.Fatalf("prohibited state change: expired worker updated status to PROCESSED")
	}
}

func TestWebhookProcessor_RealPostgres_RealPlatformAccountPreservation(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	// Seed Company, PlatformAccount (pa_internal_999), and Application
	compID := "comp_real_pa_1"
	appID := "app_real_pa_1"
	internalPAID := "pa_internal_999"
	extAccountID := "acct_airwallex_ext_888"

	_, app, pa := createRealPostgresCompanyAndApplication(t, ctx, repo, compID, appID, internalPAID, extAccountID, domain.ApplicationStatusUnderReview)

	// Update application to link internal platform account ID and transition to UNDER_REVIEW
	_, err := pool.Exec(ctx, `
		UPDATE onboarding.applications
		SET platform_account_id = $1, status = 'UNDER_REVIEW'
		WHERE id = $2
	`, internalPAID, appID)
	if err != nil {
		t.Fatalf("failed to update application: %v", err)
	}

	// Mock platform client returns ACTIVE for external account ID
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     extAccountID,
			Status: "ACTIVE",
		},
	}

	processor := webhooks.NewProcessor(repo, client)

	// Insert webhook event for external account ID
	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_real_pa_active_1",
		AccountID:   extAccountID,
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_real_pa_active_1","name":"account.active"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	// Process pending webhook
	count, err := processor.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 processed event, got %d", count)
	}

	// CRITICAL ASSERTION:
	// 1. Platform Account internal ID MUST remain pa_internal_999 (NOT overwritten with acct_airwallex_ext_888)
	loadedPA, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("failed to get platform account by internal ID: %v", err)
	}
	if loadedPA.ID() != internalPAID {
		t.Fatalf("internal PA ID was corrupted: expected %q, got %q", internalPAID, loadedPA.ID())
	}
	if loadedPA.ExternalAccountID() != extAccountID {
		t.Fatalf("external account ID mismatch: expected %q, got %q", extAccountID, loadedPA.ExternalAccountID())
	}
	if loadedPA.Status() != domain.PlatformAccountStatusActive {
		var lastErr string
		_ = pool.QueryRow(ctx, "SELECT COALESCE(last_error, '') FROM onboarding.webhook_inbox WHERE event_id = $1", "evt_real_pa_active_1").Scan(&lastErr)
		t.Fatalf("expected PA status ACTIVE, got %s (last_error: %s)", loadedPA.Status(), lastErr)
	}

	// 2. Application status MUST be COMPLETED and platform_account_id MUST remain internalPAID
	loadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to get application: %v", err)
	}
	if loadedApp.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("expected application COMPLETED, got %s", loadedApp.Status())
	}
	if loadedApp.Platform().PlatformAccountID != internalPAID {
		t.Fatalf("application platform_account_id corrupted: expected %q, got %q", internalPAID, loadedApp.Platform().PlatformAccountID)
	}

	// 3. Webhook inbox status MUST be PROCESSED
	var inboxStatus string
	err = pool.QueryRow(ctx, "SELECT status FROM onboarding.webhook_inbox WHERE event_id = $1", "evt_real_pa_active_1").Scan(&inboxStatus)
	if err != nil {
		t.Fatalf("failed to query webhook inbox: %v", err)
	}
	if inboxStatus != string(postgres.WebhookStatusProcessed) {
		t.Fatalf("expected webhook inbox status PROCESSED, got %s", inboxStatus)
	}

	_ = app
	_ = pa
}

func TestWebhookProcessor_RealPostgres_TransactionalRollback(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_real_rollback"
	appID := "app_real_rollback"
	internalPAID := "pa_rollback"
	extAccountID := "acct_rollback_ext"

	// Create company, PA in CREATED, and application in DRAFT
	createRealPostgresCompanyAndApplication(t, ctx, repo, compID, appID, internalPAID, extAccountID, domain.ApplicationStatusDraft)

	// Insert pending webhook item
	item := postgres.WebhookInboxItem{
		EventID:     "evt_rollback_1",
		AccountID:   extAccountID,
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_rollback_1","name":"account.active"}`),
	}
	if _, err := repo.InsertWebhookOnce(ctx, item); err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	// Claim to get valid lease token
	claimed, err := repo.ClaimPendingWebhooks(ctx, 10, 30*time.Second)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("failed to claim webhook: %v", err)
	}
	leaseToken := claimed[0].LeaseToken

	// Attempt ApplyWebhookObservation with ACTIVE status
	// Since application is in DRAFT (not post-submission review state),
	// RecordPlatformAccountActivation will FAIL and trigger a transaction rollback!
	err = repo.ApplyWebhookObservation(ctx, postgres.ApplyWebhookObservationParams{
		EventID:        "evt_rollback_1",
		LeaseToken:     leaseToken,
		AccountID:      extAccountID,
		ObservedStatus: domain.PlatformAccountStatusActive,
		SyncTime:       time.Now().UTC(),
		InboxStatus:    postgres.WebhookStatusProcessed,
	})
	if err == nil {
		t.Fatalf("expected ApplyWebhookObservation to fail on draft application, got nil")
	}

	// VERIFY TRANSACTIONAL ROLLBACK:
	// 1. Platform Account status MUST NOT have updated to ACTIVE
	loadedPA, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("failed to load platform account: %v", err)
	}
	if loadedPA.Status() == domain.PlatformAccountStatusActive {
		t.Fatalf("prohibited partial write: platform account was updated to ACTIVE despite rollback")
	}

	// 2. Application status MUST remain DRAFT
	loadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to load application: %v", err)
	}
	if loadedApp.Status() != domain.ApplicationStatusDraft {
		t.Fatalf("prohibited partial write: application status changed despite rollback")
	}

	// 3. Webhook inbox status MUST NOT be PROCESSED
	var inboxStatus string
	err = pool.QueryRow(ctx, "SELECT status FROM onboarding.webhook_inbox WHERE event_id = $1", "evt_rollback_1").Scan(&inboxStatus)
	if err != nil {
		t.Fatalf("failed to query webhook inbox: %v", err)
	}
	if inboxStatus == string(postgres.WebhookStatusProcessed) {
		t.Fatalf("prohibited partial write: webhook inbox was marked PROCESSED despite rollback")
	}
}

func TestWebhookProcessor_RealPostgres_MissingPlatformClientFailClosed(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_real_noclient"
	appID := "app_real_noclient"
	internalPAID := "pa_noclient"
	extAccountID := "acct_noclient_ext"

	createRealPostgresCompanyAndApplication(t, ctx, repo, compID, appID, internalPAID, extAccountID, domain.ApplicationStatusUnderReview)

	_, err := pool.Exec(ctx, `
		UPDATE onboarding.applications
		SET platform_account_id = $1, status = 'UNDER_REVIEW'
		WHERE id = $2
	`, internalPAID, appID)
	if err != nil {
		t.Fatalf("failed to update application: %v", err)
	}

	// Missing platform client: MUST FAIL CLOSED!
	processor := webhooks.NewProcessor(repo, nil)

	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_real_noclient_1",
		AccountID:   extAccountID,
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_real_noclient_1","name":"account.active"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	_, _ = processor.ProcessPending(ctx)

	// Verify inbox is FAILED
	var inboxStatus string
	err = pool.QueryRow(ctx, "SELECT status FROM onboarding.webhook_inbox WHERE event_id = $1", "evt_real_noclient_1").Scan(&inboxStatus)
	if err != nil {
		t.Fatalf("failed to query webhook inbox: %v", err)
	}
	if inboxStatus != string(postgres.WebhookStatusFailed) {
		t.Fatalf("expected inbox status FAILED on missing client, got %s", inboxStatus)
	}

	// Verify application was NOT completed
	loadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to load application: %v", err)
	}
	if loadedApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("prohibited status change: application changed to %s on missing client", loadedApp.Status())
	}
}

func TestWebhookProcessor_RealPostgres_FreshReadSuspendedContradictionQuarantined(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_real_contra"
	appID := "app_real_contra"
	internalPAID := "pa_contra"
	extAccountID := "acct_contra_ext"

	createRealPostgresCompanyAndApplication(t, ctx, repo, compID, appID, internalPAID, extAccountID, domain.ApplicationStatusUnderReview)

	_, err := pool.Exec(ctx, `
		UPDATE onboarding.applications
		SET platform_account_id = $1, status = 'UNDER_REVIEW'
		WHERE id = $2
	`, internalPAID, appID)
	if err != nil {
		t.Fatalf("failed to update application: %v", err)
	}

	// Fresh read reports SUSPENDED
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     extAccountID,
			Status: "SUSPENDED",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	// Inbound event claims account.active
	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_real_contra_1",
		AccountID:   extAccountID,
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_real_contra_1","name":"account.active"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	_, err = processor.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	// 1. Platform Account must be marked SUSPENDED
	loadedPA, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("failed to load platform account: %v", err)
	}
	if loadedPA.Status() != domain.PlatformAccountStatusSuspended {
		t.Fatalf("expected PA status SUSPENDED, got %s", loadedPA.Status())
	}

	// 2. Application must NOT be completed, but operator review must be flagged
	loadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to load application: %v", err)
	}
	if loadedApp.Status() == domain.ApplicationStatusCompleted {
		t.Fatalf("prohibited completion: application completed despite platform being SUSPENDED")
	}
	if !loadedApp.OperatorReviewFlagged() {
		t.Fatalf("expected operatorReviewFlagged to be true")
	}

	// 3. Webhook event must be QUARANTINED
	var inboxStatus string
	err = pool.QueryRow(ctx, "SELECT status FROM onboarding.webhook_inbox WHERE event_id = $1", "evt_real_contra_1").Scan(&inboxStatus)
	if err != nil {
		t.Fatalf("failed to query webhook inbox: %v", err)
	}
	if inboxStatus != string(postgres.WebhookStatusQuarantined) {
		t.Fatalf("expected webhook inbox status QUARANTINED, got %s", inboxStatus)
	}
}

func TestWebhookProcessor_RealPostgres_StaleSuspendedEventIgnored(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_real_stalesusp"
	appID := "app_real_stalesusp"
	internalPAID := "pa_stalesusp"
	extAccountID := "acct_stalesusp_ext"

	createRealPostgresCompanyAndApplication(t, ctx, repo, compID, appID, internalPAID, extAccountID, domain.ApplicationStatusUnderReview)

	// Platform account is already ACTIVE in DB
	var err error
	if _, err = pool.Exec(ctx, "UPDATE onboarding.platform_accounts SET status = 'ACTIVE' WHERE id = $1", internalPAID); err != nil {
		t.Fatalf("failed to update platform account status: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE onboarding.applications SET platform_account_id = $1, status = 'UNDER_REVIEW' WHERE id = $2", internalPAID, appID); err != nil {
		t.Fatalf("failed to update application: %v", err)
	}

	// Fresh read confirms ACTIVE
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     extAccountID,
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	// Inbound event is stale account.suspended
	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_real_stale_susp_1",
		AccountID:   extAccountID,
		EventType:   "account.suspended",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_real_stale_susp_1","name":"account.suspended"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	_, err = processor.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	// 1. Platform Account remains ACTIVE
	loadedPA, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("failed to load platform account: %v", err)
	}
	if loadedPA.Status() != domain.PlatformAccountStatusActive {
		t.Fatalf("expected PA to remain ACTIVE, got %s", loadedPA.Status())
	}

	// 2. Application operator review NOT flagged
	loadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to load application: %v", err)
	}
	if loadedApp.OperatorReviewFlagged() {
		t.Fatalf("prohibited operator review flag on stale suspended event")
	}

	// 3. Stale event is QUARANTINED
	var inboxStatus string
	err = pool.QueryRow(ctx, "SELECT status FROM onboarding.webhook_inbox WHERE event_id = $1", "evt_real_stale_susp_1").Scan(&inboxStatus)
	if err != nil {
		t.Fatalf("failed to query webhook inbox: %v", err)
	}
	if inboxStatus != string(postgres.WebhookStatusQuarantined) {
		t.Fatalf("expected webhook inbox status QUARANTINED, got %s", inboxStatus)
	}
}

func TestWebhookProcessor_RealPostgres_PeriodicReconciliationRecoversMissedWebhook(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_real_recon"
	appID := "app_real_recon"
	internalPAID := "pa_recon"
	extAccountID := "acct_recon_ext"

	createRealPostgresCompanyAndApplication(t, ctx, repo, compID, appID, internalPAID, extAccountID, domain.ApplicationStatusUnderReview)

	_, err := pool.Exec(ctx, `
		UPDATE onboarding.applications
		SET platform_account_id = $1, status = 'UNDER_REVIEW'
		WHERE id = $2
	`, internalPAID, appID)
	if err != nil {
		t.Fatalf("failed to update application: %v", err)
	}

	// NOTICE: NO webhook is ever inserted into webhook_inbox!
	// Simulates a dropped / unreceived webhook!

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     extAccountID,
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	// Run periodic account reconciliation
	reconciled, err := processor.ReconcileAccounts(ctx)
	if err != nil {
		t.Fatalf("ReconcileAccounts failed: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("expected 1 reconciled account, got %d", reconciled)
	}

	// Assert: Both PlatformAccount and Application successfully recovered to ACTIVE / COMPLETED!
	loadedPA, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("failed to load platform account: %v", err)
	}
	if loadedPA.Status() != domain.PlatformAccountStatusActive {
		t.Fatalf("expected PA to become ACTIVE via reconciliation, got %s", loadedPA.Status())
	}

	loadedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to load application: %v", err)
	}
	if loadedApp.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("expected application to become COMPLETED via reconciliation, got %s", loadedApp.Status())
	}
}

func TestWebhookProcessor_RealPostgres_CrossEnvironmentIsolation(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	sharedExtID := "acct_shared_ext_123"

	// 1. Sandbox Company & PA
	compSand, err := domain.NewDraftCompany("comp_sand", "Sandbox Corp")
	if err != nil {
		t.Fatalf("failed to create sandbox company: %v", err)
	}
	if err := repo.CreateCompany(ctx, compSand); err != nil {
		t.Fatalf("failed to insert sandbox company: %v", err)
	}
	paSand, err := domain.NewPlatformAccount("pa_sand", "comp_sand", "AIRWALLEX", "sandbox")
	if err != nil {
		t.Fatalf("failed to create sandbox PA: %v", err)
	}
	_ = paSand.SetExternalAccountID(sharedExtID)
	if err := repo.CreatePlatformAccount(ctx, paSand); err != nil {
		t.Fatalf("failed to insert sandbox PA: %v", err)
	}
	appSand, err := domain.NewDraftApplication("app_sand", compSand, "op")
	if err != nil {
		t.Fatalf("failed to create sandbox app: %v", err)
	}
	if err := repo.CreateApplication(ctx, appSand); err != nil {
		t.Fatalf("failed to insert sandbox app: %v", err)
	}
	_, _ = pool.Exec(ctx, "UPDATE onboarding.applications SET platform_account_id = 'pa_sand', platform_name = 'AIRWALLEX', platform_environment = 'sandbox', status = 'UNDER_REVIEW' WHERE id = 'app_sand'")

	// 2. Production Company & PA (identical shared external ID!)
	compProd, err := domain.NewDraftCompany("comp_prod", "Production Corp")
	if err != nil {
		t.Fatalf("failed to create prod company: %v", err)
	}
	if err := repo.CreateCompany(ctx, compProd); err != nil {
		t.Fatalf("failed to insert prod company: %v", err)
	}
	paProd, err := domain.NewPlatformAccount("pa_prod", "comp_prod", "AIRWALLEX", "production")
	if err != nil {
		t.Fatalf("failed to create prod PA: %v", err)
	}
	_ = paProd.SetExternalAccountID(sharedExtID)
	if err := repo.CreatePlatformAccount(ctx, paProd); err != nil {
		t.Fatalf("failed to insert prod PA: %v", err)
	}
	appProd, err := domain.NewDraftApplication("app_prod", compProd, "op")
	if err != nil {
		t.Fatalf("failed to create prod app: %v", err)
	}
	if err := repo.CreateApplication(ctx, appProd); err != nil {
		t.Fatalf("failed to insert prod app: %v", err)
	}
	_, _ = pool.Exec(ctx, "UPDATE onboarding.applications SET platform_account_id = 'pa_prod', platform_name = 'AIRWALLEX', platform_environment = 'production', status = 'UNDER_REVIEW' WHERE id = 'app_prod'")

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     sharedExtID,
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	// Inbound webhook event explicitly for PRODUCTION
	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_prod_activate",
		AccountID:   sharedExtID,
		EventType:   "account.active",
		Environment: "production",
		RawPayload:  json.RawMessage(`{"id":"evt_prod_activate","name":"account.active"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert prod webhook: %v", err)
	}

	processed, err := processor.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	// Assert: Production PA and App became ACTIVE / COMPLETED
	prodPA, err := repo.GetPlatformAccount(ctx, "pa_prod")
	if err != nil || prodPA.Status() != domain.PlatformAccountStatusActive {
		t.Fatalf("expected pa_prod to be ACTIVE, got %v (err: %v)", prodPA.Status(), err)
	}
	prodApp, err := repo.GetApplication(ctx, "app_prod")
	if err != nil || prodApp.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("expected app_prod to be COMPLETED, got %v (err: %v)", prodApp.Status(), err)
	}

	// Assert: Sandbox PA and App remained completely UNTOUCHED
	sandPA, err := repo.GetPlatformAccount(ctx, "pa_sand")
	if err != nil || sandPA.Status() == domain.PlatformAccountStatusActive {
		t.Fatalf("expected pa_sand to remain unactivated, got %v", sandPA.Status())
	}
	sandApp, err := repo.GetApplication(ctx, "app_sand")
	if err != nil || sandApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected app_sand to remain UNDER_REVIEW, got %v", sandApp.Status())
	}
}

func TestWebhookProcessor_RealPostgres_LinkedCompletedAndNewDraft_DraftUntouched(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_multi_app"
	paID := "pa_multi_app"
	extAccountID := "acct_multi_app_ext"
	appCompID := "app_completed_1"
	appDraftID := "app_draft_2"

	comp, err := domain.NewDraftCompany(compID, "Multi App Corp")
	if err != nil {
		t.Fatalf("failed to construct company: %v", err)
	}
	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("failed to insert company: %v", err)
	}

	pa, err := domain.NewPlatformAccount(paID, compID, "AIRWALLEX", "sandbox")
	if err != nil {
		t.Fatalf("failed to construct PA: %v", err)
	}
	_ = pa.SetExternalAccountID(extAccountID)
	if err := repo.CreatePlatformAccount(ctx, pa); err != nil {
		t.Fatalf("failed to insert PA: %v", err)
	}

	// App 1: COMPLETED, linked to paID
	app1, err := domain.NewDraftApplication(appCompID, comp, "op")
	if err != nil {
		t.Fatalf("failed to construct app1: %v", err)
	}
	if err := repo.CreateApplication(ctx, app1); err != nil {
		t.Fatalf("failed to insert app1: %v", err)
	}
	_, _ = pool.Exec(ctx, `
		UPDATE onboarding.applications
		SET platform_account_id = $1, platform_name = 'AIRWALLEX', platform_environment = 'sandbox',
		    status = 'COMPLETED', decision = 'APPROVED', closed_at = now(), updated_at = now() - interval '10 minutes'
		WHERE id = $2
	`, paID, appCompID)

	// App 2: Newly created DRAFT for same company, unlinked (platform_account_id = NULL)
	app2, err := domain.NewDraftApplication(appDraftID, comp, "op")
	if err != nil {
		t.Fatalf("failed to construct app2: %v", err)
	}
	if err := repo.CreateApplication(ctx, app2); err != nil {
		t.Fatalf("failed to insert app2: %v", err)
	}
	initialDraftApp, err := repo.GetApplication(ctx, appDraftID)
	if err != nil {
		t.Fatalf("failed to load draft app: %v", err)
	}
	initialDraftRev := initialDraftApp.Revision()

	// Authoritative event for PA arrives
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     extAccountID,
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_act_multi",
		AccountID:   extAccountID,
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_act_multi","name":"account.active"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	_, err = processor.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	var inboxStatus, inboxErr string
	_ = pool.QueryRow(ctx, "SELECT status, COALESCE(last_error, '') FROM onboarding.webhook_inbox WHERE event_id = 'evt_act_multi'").Scan(&inboxStatus, &inboxErr)
	t.Logf("inbox status: %s, last_error: %s", inboxStatus, inboxErr)

	// 1. PA became ACTIVE
	loadedPA, err := repo.GetPlatformAccount(ctx, paID)
	if err != nil {
		t.Fatalf("failed to load PA: %v", err)
	}
	if loadedPA.Status() != domain.PlatformAccountStatusActive {
		t.Fatalf("expected PA to become ACTIVE, got %s", loadedPA.Status())
	}

	// 2. App 1 (COMPLETED) remains COMPLETED
	loadedApp1, err := repo.GetApplication(ctx, appCompID)
	if err != nil {
		t.Fatalf("failed to load app1: %v", err)
	}
	if loadedApp1.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("expected app1 to remain COMPLETED, got %s", loadedApp1.Status())
	}

	// 3. App 2 (DRAFT) remains strictly untouched: status DRAFT, revision unchanged, platform_account_id NULL
	loadedDraft, err := repo.GetApplication(ctx, appDraftID)
	if err != nil {
		t.Fatalf("failed to load draft app: %v", err)
	}
	if loadedDraft.Status() != domain.ApplicationStatusDraft {
		t.Fatalf("expected draft app to remain DRAFT, got %s", loadedDraft.Status())
	}
	if loadedDraft.Revision() != initialDraftRev {
		t.Fatalf("expected draft app revision %d unchanged, got %d", initialDraftRev, loadedDraft.Revision())
	}
	if loadedDraft.Platform().PlatformAccountID != "" {
		t.Fatalf("prohibited assignment of platform_account_id to unlinked draft: got %q", loadedDraft.Platform().PlatformAccountID)
	}
}

func TestWebhookProcessor_RealPostgres_StaleConcurrentReads_DelayedDoesNotOverwriteNewer(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_stale_reads"
	appID := "app_stale_reads"
	internalPAID := "pa_stale_reads"
	extAccountID := "acct_stale_reads_ext"

	createRealPostgresCompanyAndApplication(t, ctx, repo, compID, appID, internalPAID, extAccountID, domain.ApplicationStatusUnderReview)

	// Simulate: Read B was processed at T2 with newer status SUSPENDED
	// PostgreSQL stores TIMESTAMPTZ at microsecond precision.
	t2 := time.Now().UTC().Truncate(time.Microsecond)
	t1 := t2.Add(-10 * time.Minute) // Read A happened 10 minutes earlier at T1 with ACTIVE

	// Apply Read B (newer: SUSPENDED at T2)
	err := repo.ApplyWebhookObservation(ctx, postgres.ApplyWebhookObservationParams{
		InternalAccountID: internalPAID,
		ExternalAccountID: extAccountID,
		PlatformName:      "AIRWALLEX",
		Environment:       "sandbox",
		ObservedStatus:    domain.PlatformAccountStatusSuspended,
		SyncTime:          t2,
	})
	if err != nil {
		t.Fatalf("failed to apply newer observation B: %v", err)
	}

	// Verify DB state after B
	paAfterB, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil || paAfterB.Status() != domain.PlatformAccountStatusSuspended {
		t.Fatalf("expected PA to be SUSPENDED after B, got %v", paAfterB.Status())
	}
	revAfterB := paAfterB.Revision()

	// Delayed Read A returns NOW with SyncTime = T1 < T2 and ACTIVE
	// Should produce 0 aggregate changes, 0 revision churn, and not overwrite SUSPENDED
	err = repo.ApplyWebhookObservation(ctx, postgres.ApplyWebhookObservationParams{
		InternalAccountID: internalPAID,
		ExternalAccountID: extAccountID,
		PlatformName:      "AIRWALLEX",
		Environment:       "sandbox",
		ObservedStatus:    domain.PlatformAccountStatusActive,
		SyncTime:          t1,
	})
	if err != nil {
		t.Fatalf("stale observation A should be gracefully acknowledged without error, got: %v", err)
	}

	// Assert: PA remains SUSPENDED, last_sync_time remains T2, revision churn is 0
	paFinal, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("failed to get PA: %v", err)
	}
	if paFinal.Status() != domain.PlatformAccountStatusSuspended {
		t.Fatalf("CRITICAL BUG: delayed older read A overwrote newer status B! status=%s", paFinal.Status())
	}
	if paFinal.Revision() != revAfterB {
		t.Fatalf("revision churn detected on stale read: expected revision %d, got %d", revAfterB, paFinal.Revision())
	}
	if paFinal.LastSyncTime() == nil || !paFinal.LastSyncTime().Equal(t2) {
		t.Fatalf("last_sync_time was regressed: expected %v, got %v", t2, paFinal.LastSyncTime())
	}
}

func TestWebhookProcessor_RealPostgres_MissingOrExpiredLeaseFails(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	// Insert item
	item := postgres.WebhookInboxItem{
		EventID:     "evt_lease_test_1",
		AccountID:   "acct_lease_test_1",
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_lease_test_1"}`),
	}
	if _, err := repo.InsertWebhookOnce(ctx, item); err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	// 1. MarkWebhookProcessed with empty lease token MUST fail with ErrStaleLeaseToken
	err := repo.MarkWebhookProcessed(ctx, "evt_lease_test_1", "", postgres.WebhookStatusProcessed, "")
	if !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken for empty token, got: %v", err)
	}

	// 2. MarkWebhookProcessed on unleased (PENDING) item MUST fail with ErrStaleLeaseToken
	err = repo.MarkWebhookProcessed(ctx, "evt_lease_test_1", "some_token", postgres.WebhookStatusProcessed, "")
	if !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken for unleased row, got: %v", err)
	}

	// Claim the webhook
	claimed, err := repo.ClaimPendingWebhooks(ctx, 10, 30*time.Second)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("failed to claim webhook: %v", err)
	}
	validToken := claimed[0].LeaseToken

	// 3. MarkWebhookProcessed with wrong lease token MUST fail
	err = repo.MarkWebhookProcessed(ctx, "evt_lease_test_1", "wrong_token", postgres.WebhookStatusProcessed, "")
	if !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken for wrong token, got: %v", err)
	}

	// 4. Expire the lease in the database
	_, err = pool.Exec(ctx, "UPDATE onboarding.webhook_inbox SET lease_expires_at = now() - interval '10 seconds' WHERE event_id = $1", "evt_lease_test_1")
	if err != nil {
		t.Fatalf("failed to expire lease: %v", err)
	}

	// 5. MarkWebhookProcessed with expired lease MUST fail
	err = repo.MarkWebhookProcessed(ctx, "evt_lease_test_1", validToken, postgres.WebhookStatusProcessed, "")
	if !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken for expired lease, got: %v", err)
	}
}

func TestWebhookProcessor_RealPostgres_ReconciliationStarvationPrevention(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	// Create 7 platform accounts.
	// pa_1 will always fail when read from Airwallex.
	// pa_2 through pa_7 will succeed.
	// With batchLimit = 3, pa_1 must NOT starve pa_2..pa_7!
	for i := 1; i <= 7; i++ {
		compID := fmt.Sprintf("comp_starve_%d", i)
		paID := fmt.Sprintf("pa_starve_%d", i)
		extID := fmt.Sprintf("acct_starve_%d", i)

		comp, err := domain.NewDraftCompany(compID, fmt.Sprintf("Starve Co %d", i))
		if err != nil {
			t.Fatalf("failed to construct company: %v", err)
		}
		if err := repo.CreateCompany(ctx, comp); err != nil {
			t.Fatalf("failed to create company: %v", err)
		}
		pa, err := domain.NewPlatformAccount(paID, compID, "AIRWALLEX", "sandbox")
		if err != nil {
			t.Fatalf("failed to construct PA: %v", err)
		}
		_ = pa.SetExternalAccountID(extID)
		if err := repo.CreatePlatformAccount(ctx, pa); err != nil {
			t.Fatalf("failed to create PA: %v", err)
		}
		// Explicitly set next_check_at in the past so all 7 are eligible
		_, _ = pool.Exec(ctx, "UPDATE onboarding.platform_accounts SET next_check_at = now() - interval '1 minute' WHERE id = $1", paID)
	}

	failingClient := &StarvationMockPlatformClient{
		failExtID: "acct_starve_1",
	}

	processor := webhooks.NewProcessor(repo, failingClient)

	// Run 3 rounds of reconciliation (batch size = 3)
	totalReconciled := 0
	for round := 1; round <= 3; round++ {
		reconciled, err := processor.ReconcileAccounts(ctx)
		if err != nil {
			t.Fatalf("round %d failed: %v", round, err)
		}
		totalReconciled += reconciled
	}

	if totalReconciled != 6 {
		t.Fatalf("expected 6 healthy accounts reconciled across 3 batches, got %d", totalReconciled)
	}

	// Verify all healthy accounts (pa_2 through pa_7) were updated to ACTIVE
	for i := 2; i <= 7; i++ {
		paID := fmt.Sprintf("pa_starve_%d", i)
		pa, err := repo.GetPlatformAccount(ctx, paID)
		if err != nil {
			t.Fatalf("failed to load %s: %v", paID, err)
		}
		if pa.Status() != domain.PlatformAccountStatusActive {
			t.Fatalf("account %s was starved! status=%s", paID, pa.Status())
		}
	}
}

type StarvationMockPlatformClient struct {
	failExtID string
}

func (c *StarvationMockPlatformClient) GetAccount(ctx context.Context, platformAccountID string) (*airwallex.AccountDetailsResponse, error) {
	if platformAccountID == c.failExtID {
		return nil, errors.New("simulated network timeout / 500 error on provider")
	}
	return &airwallex.AccountDetailsResponse{
		ID:     platformAccountID,
		Status: "ACTIVE",
	}, nil
}
