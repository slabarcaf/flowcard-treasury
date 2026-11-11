package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/worker"
)

// --- Safe Database Connection Helpers ---

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

	if cfg.ConnConfig.Port != 54329 {
		t.Fatalf("SAFETY CHECK FAILED: port %d is not 54329; refusing to connect", cfg.ConnConfig.Port)
	}

	host := cfg.ConnConfig.Host
	if host != "127.0.0.1" && host != "localhost" {
		t.Fatalf("SAFETY CHECK FAILED: host %q is not local; refusing remote database connection", host)
	}

	dbName := strings.ToLower(cfg.ConnConfig.Database)
	if dbName != "flowcard_onboarding_test" {
		t.Fatalf("SAFETY CHECK FAILED: database name %q is not flowcard_onboarding_test", cfg.ConnConfig.Database)
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

	var currentDB string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&currentDB); err != nil {
		t.Fatalf("failed to query current database: %v", err)
	}
	if currentDB != "flowcard_onboarding_test" {
		t.Fatalf("SAFETY CHECK FAILED: refusing to truncate non-test database: %s", currentDB)
	}

	_, err := pool.Exec(ctx, `
		TRUNCATE TABLE
			onboarding.platform_creation_coordinators,
			onboarding.submission_dispatch_steps,
			onboarding.submission_document_uploads,
			onboarding.idempotency_keys,
			onboarding.outbox_messages,
			onboarding.submissions,
			onboarding.applications,
			onboarding.platform_accounts,
			onboarding.companies
		CASCADE;
	`)
	if err != nil {
		t.Fatalf("failed to clean test tables: %v", err)
	}
}

// --- Mock Platform Client ---

type mockPlatformClient struct {
	mu                sync.Mutex
	createAccountFunc func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error)
	uploadFileFunc    func(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*airwallex.FileUploadResponse, error)
	updateAccountFunc func(ctx context.Context, accountID string, req airwallex.AccountUpdateRequest) (*airwallex.AccountDetailsResponse, error)
	submitAccountFunc func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error)
	getAccountFunc    func(ctx context.Context, accountID string) (*airwallex.AccountDetailsResponse, error)
	getRfiFunc        func(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error)
	respondRfiFunc    func(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error)

	createCalls     []airwallex.AccountCreateRequest
	uploadCalls     []string
	updateCalls     []airwallex.AccountUpdateRequest
	submitCalls     []string
	getRfiCalls     []string
	respondRfiCalls []airwallex.RFIRespondRequest
}

func (m *mockPlatformClient) CreateAccount(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
	m.mu.Lock()
	m.createCalls = append(m.createCalls, req)
	fn := m.createAccountFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &airwallex.AccountCreateResponse{
		ID:     "acct_mock_default",
		Status: "CREATED",
	}, nil
}

func (m *mockPlatformClient) UploadFile(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*airwallex.FileUploadResponse, error) {
	m.mu.Lock()
	m.uploadCalls = append(m.uploadCalls, fileName)
	fn := m.uploadFileFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, r, fileName, fileSize, contentType)
	}
	return &airwallex.FileUploadResponse{
		FileID:   "file_mock_default",
		Filename: fileName,
		Size:     fileSize,
	}, nil
}

func (m *mockPlatformClient) UpdateAccount(ctx context.Context, accountID string, req airwallex.AccountUpdateRequest) (*airwallex.AccountDetailsResponse, error) {
	m.mu.Lock()
	m.updateCalls = append(m.updateCalls, req)
	fn := m.updateAccountFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, accountID, req)
	}
	return &airwallex.AccountDetailsResponse{
		ID:     accountID,
		Status: "SUBMITTED",
	}, nil
}

func (m *mockPlatformClient) SubmitAccount(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
	m.mu.Lock()
	m.submitCalls = append(m.submitCalls, accountID)
	fn := m.submitAccountFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, accountID)
	}
	return &airwallex.AccountSubmitResponse{
		ID:          accountID,
		Status:      "SUBMITTED",
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (m *mockPlatformClient) GetAccount(ctx context.Context, accountID string) (*airwallex.AccountDetailsResponse, error) {
	m.mu.Lock()
	fn := m.getAccountFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, accountID)
	}
	return &airwallex.AccountDetailsResponse{
		ID:     accountID,
		Status: "SUBMITTED",
	}, nil
}

func (m *mockPlatformClient) GetRFI(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error) {
	m.mu.Lock()
	m.getRfiCalls = append(m.getRfiCalls, rfiID)
	fn := m.getRfiFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, rfiID)
	}
	return &airwallex.RFIResponsePayload{
		ID:        rfiID,
		AccountID: "acct_sandbox_success_001",
		Type:      "KYC",
		Status:    "ACTION_REQUIRED",
		ActiveRequest: &airwallex.RFIActiveRequest{
			Questions: []airwallex.RFIQuestionPrompt{
				{
					ID:     "q1",
					Answer: airwallex.RFIAnswerRequirement{Type: "TEXT"},
				},
			},
		},
	}, nil
}

func (m *mockPlatformClient) RespondRFI(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error) {
	m.mu.Lock()
	m.respondRfiCalls = append(m.respondRfiCalls, req)
	fn := m.respondRfiFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, rfiID, req)
	}
	return &airwallex.RFIRespondResponse{
		StatusCode: 200,
		RawBody:    []byte(`{}`),
	}, nil
}

// --- Test Aggregate Builders ---

func createTestCompanyAndApp(t *testing.T, repo *postgres.Repository, store storage.ObjectStore) (*domain.Company, *domain.OnboardingApplication, *domain.Submission) {
	t.Helper()
	ctx := context.Background()

	nano := time.Now().UnixNano()
	compID := fmt.Sprintf("comp_wk_%d", nano)
	appID := fmt.Sprintf("app_wk_%d", nano)
	subID := fmt.Sprintf("sub_wk_%d", nano)
	docID := fmt.Sprintf("doc_wk_%d", nano)

	comp, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  compID,
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
		LegalName:           "Acme Innovations LLC",
		EntityType:          "LLC",
		RegistrationCountry: "US",
		RegistrationState:   "CA",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "SaaS bridge underwriting infrastructure",
			RegisteredAddress: domain.Address{
				Line1:      "100 Market St",
				Line2:      "Suite 400",
				City:       "San Francisco",
				State:      "CA",
				PostalCode: "94105",
				Country:    "US",
			},
		},
		People: []domain.Person{
			{
				ID:          "person_001",
				FirstName:   "Alex",
				LastName:    "Morgan",
				Email:       "operator.contact@acme-innovations.example.com",
				Phone:       "+14155550199",
				DateOfBirth: domain.Date("1985-06-15"),
				Nationality: "US",
			},
		},
	})
	if err != nil {
		t.Fatalf("ReconstituteCompany failed: %v", err)
	}

	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("CreateCompany failed: %v", err)
	}

	app, err := domain.NewDraftApplication(appID, comp, "operator_001")
	if err != nil {
		t.Fatalf("NewDraftApplication failed: %v", err)
	}

	if err := app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		PrimaryContactPersonID: "person_001",
		IndustryCategoryCode:   "5734",
		EstimatedRevenue: domain.RevenueEstimate{
			Amount:   "100000",
			Currency: "USD",
		},
		CustomerAgreements: domain.CustomerAgreements{
			AgreedToDataUsage:          true,
			AgreedToTermsAndConditions: true,
			TermsAndConditions: &domain.TermsAndConditionsAgreement{
				AgreedAt:             time.Now().UTC(),
				ServiceAgreementType: "FULL",
				DeviceData: &domain.DeviceData{
					IPAddress: "198.51.100.42",
					UserAgent: "Mozilla/5.0",
				},
			},
		},
		People: []domain.PersonPlatformDetails{
			{
				PersonID:      "person_001",
				PlatformRoles: []string{"DIRECTOR"},
				ResidentialAddress: domain.Address{
					Line1:      "100 Market St",
					City:       "San Francisco",
					State:      "CA",
					PostalCode: "94105",
					Country:    "US",
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOnboardingDetails failed: %v", err)
	}

	storageKey, err := storage.GenerateKey(appID, docID)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	docContent := []byte("%PDF-1.4 fake pdf file content")
	if err := store.Put(ctx, storageKey, bytes.NewReader(docContent), int64(len(docContent)), "application/pdf"); err != nil {
		t.Fatalf("store.Put failed: %v", err)
	}

	doc := domain.Document{
		ID:          docID,
		Type:        "CERTIFICATE_OF_INCORPORATION",
		FileName:    "cert.pdf",
		ContentType: "application/pdf",
		SizeBytes:   int64(len(docContent)),
		StorageKey:  storageKey,
		UploadedBy:  "operator_001",
		UploadedAt:  time.Now().UTC(),
	}
	if err := app.AttachDocument(doc); err != nil {
		t.Fatalf("AttachDocument failed: %v", err)
	}

	if err := repo.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication failed: %v", err)
	}

	// Initiate submission to create outbox message
	sub, err := app.InitiateInitialSubmission(subID, "actor_001", []string{docID})
	if err != nil {
		t.Fatalf("InitiateInitialSubmission failed: %v", err)
	}

	outboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: appID,
		SubmissionID:  subID,
	})

	initResult, err := repo.InitiateSubmission(ctx, postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision() - 1,
		SubmissionID:        subID,
		SubmittingActor:     "actor_001",
		DocumentIDs:         []string{docID},
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       outboxPayload,
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("idemp_%d", nano),
		RequestFingerprint:  "fp_sha256",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	})
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}
	if initResult == nil {
		t.Fatal("expected non-nil InitiateSubmissionResult")
	}

	return comp, app, sub
}

// --- Test Suites ---

// TestWorker_SuccessfulInitialSubmission asserts the complete flow:
// CreateAccount -> UploadFile -> UpdateAccount -> SubmitAccount.
// Verifies internal pa_... aggregate ID linked on app, external acct_... used in HTTP,
// and durable dispatch step states and file uploads persisted in DB.
func TestWorker_SuccessfulInitialSubmission(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	var updateCalled atomic.Bool
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			if req.AccountDetails.BusinessDetails.BusinessName != "Acme Innovations LLC" {
				return nil, errors.New("unexpected business name in snapshot payload")
			}
			return &airwallex.AccountCreateResponse{
				ID:     "acct_sandbox_success_001",
				Status: "CREATED",
			}, nil
		},
		uploadFileFunc: func(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*airwallex.FileUploadResponse, error) {
			b, _ := io.ReadAll(r)
			if !strings.Contains(string(b), "%PDF") {
				return nil, errors.New("file stream corrupted")
			}
			return &airwallex.FileUploadResponse{
				FileID:   "file_uploaded_001",
				Filename: fileName,
				Size:     fileSize,
			}, nil
		},
		updateAccountFunc: func(ctx context.Context, accountID string, req airwallex.AccountUpdateRequest) (*airwallex.AccountDetailsResponse, error) {
			updateCalled.Store(true)
			if accountID != "acct_sandbox_success_001" {
				return nil, fmt.Errorf("unexpected external account ID in update: %s", accountID)
			}
			// Finding 3: verify uploaded file IDs and business details mapped into update
			if req.AccountDetails.BusinessDetails.Attachments == nil || len(req.AccountDetails.BusinessDetails.Attachments.BusinessDocuments) == 0 {
				return nil, errors.New("expected file attachments on business details in update request")
			}
			if req.AccountDetails.BusinessDetails.Attachments.BusinessDocuments[0].FileID != "file_uploaded_001" {
				return nil, fmt.Errorf("expected file_uploaded_001, got %s", req.AccountDetails.BusinessDetails.Attachments.BusinessDocuments[0].FileID)
			}
			if req.AccountDetails.BusinessDetails.DescriptionOfGoodsOrServices != "SaaS bridge underwriting infrastructure" {
				return nil, fmt.Errorf("unexpected goods/services: %s", req.AccountDetails.BusinessDetails.DescriptionOfGoodsOrServices)
			}
			return &airwallex.AccountDetailsResponse{
				ID:     accountID,
				Status: "SUBMITTED",
			}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			if accountID != "acct_sandbox_success_001" {
				return nil, fmt.Errorf("unexpected account ID in submit: %s", accountID)
			}
			return &airwallex.AccountSubmitResponse{
				ID:          accountID,
				Status:      "SUBMITTED",
				SubmittedAt: time.Now().UTC().Format(time.RFC3339),
			}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{
		LeaseDuration: 10 * time.Second,
	})

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected job to be processed")
	}

	if !updateCalled.Load() {
		t.Fatal("expected UpdateAccount to be called during submission sequence")
	}

	// Verify Application state
	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if reloadedApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected application status UNDER_REVIEW, got %s", reloadedApp.Status())
	}

	// Finding 7: Verify internal aggregate ID linked on app, NOT external account ID!
	linkedInternalID := reloadedApp.Platform().PlatformAccountID
	if linkedInternalID == "" {
		t.Fatal("expected linked platform account ID on application")
	}
	if linkedInternalID == "acct_sandbox_success_001" {
		t.Fatalf("Finding 7 violation: application linked external ID %q instead of internal aggregate ID", linkedInternalID)
	}

	// Verify Platform Account record
	pa, err := repo.GetPlatformAccountByCompany(context.Background(), app.Company().ID(), app.Platform().Name, app.Platform().Environment)
	if err != nil {
		t.Fatalf("GetPlatformAccountByCompany failed: %v", err)
	}
	if pa.ID() != linkedInternalID {
		t.Fatalf("expected platform account ID %s to match application link %s", pa.ID(), linkedInternalID)
	}
	if pa.ExternalAccountID() != "acct_sandbox_success_001" {
		t.Fatalf("expected external account ID acct_sandbox_success_001, got %s", pa.ExternalAccountID())
	}

	// Verify durable dispatch steps in DB
	stepCreate, extIDCreate, err := repo.GetDispatchStepState(context.Background(), sub.ID, worker.StepCreateAccount)
	if err != nil || stepCreate != postgres.DispatchStepStateConfirmed || extIDCreate != "acct_sandbox_success_001" {
		t.Fatalf("expected CREATE_ACCOUNT CONFIRMED with acct_sandbox_success_001, got %s, %s (err: %v)", stepCreate, extIDCreate, err)
	}
	stepUpdate, _, err := repo.GetDispatchStepState(context.Background(), sub.ID, worker.StepUpdateAccount)
	if err != nil || stepUpdate != postgres.DispatchStepStateConfirmed {
		t.Fatalf("expected UPDATE_ACCOUNT CONFIRMED, got %s (err: %v)", stepUpdate, err)
	}
	stepSubmit, _, err := repo.GetDispatchStepState(context.Background(), sub.ID, worker.StepSubmitAccount)
	if err != nil || stepSubmit != postgres.DispatchStepStateConfirmed {
		t.Fatalf("expected SUBMIT_ACCOUNT CONFIRMED, got %s (err: %v)", stepSubmit, err)
	}

	// Verify durable document uploads in DB (Finding 3)
	uploads, err := repo.GetDocumentUploads(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GetDocumentUploads failed: %v", err)
	}
	if len(uploads) == 0 {
		t.Fatal("expected recorded document uploads in DB")
	}

	// Verify Submission state
	subs := reloadedApp.Submissions()
	foundSub := subs[len(subs)-1]
	if foundSub.ID != sub.ID {
		t.Fatalf("expected sub ID %s, got %s", sub.ID, foundSub.ID)
	}
	if foundSub.Status != domain.SubmissionStatusAccepted {
		t.Fatalf("expected submission status ACCEPTED, got %s", foundSub.Status)
	}
	if foundSub.AcceptedAt == nil {
		t.Fatal("expected accepted_at timestamp to be set")
	}

	// Verify Outbox is complete
	nextProcessed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob second call failed: %v", err)
	}
	if nextProcessed {
		t.Fatal("expected no remaining jobs in outbox")
	}
}

// TestWorker_CrashAfterCreateRemoteSuccess_ReclaimedJobGoesToHoldNeverCallsCreateAgain asserts Finding 1:
// Crash after remote success before SavePlatformAccountID leaves step in DISPATCHING.
// Reclaimed job must transition to UNRESOLVED / manual hold and NEVER call CreateAccount again!
func TestWorker_CrashAfterCreateRemoteSuccess_ReclaimedJobGoesToHoldNeverCallsCreateAgain(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	// Simulate crash: outbox message is CLAIMED, and Step 1 recorded DISPATCHING
	msg, err := repo.ClaimOutboxMessage(context.Background(), 10*time.Second)
	if err != nil || msg == nil {
		t.Fatalf("ClaimOutboxMessage failed: %v", err)
	}

	maySend, err := repo.StartDispatchStepWithLease(
		context.Background(),
		sub.ID,
		worker.StepCreateAccount,
		msg.ID,
		msg.LeaseToken,
	)
	if err != nil || !maySend {
		t.Fatalf("StartDispatchStepWithLease failed: %v (maySend=%v)", err, maySend)
	}

	// Now simulate worker crashing and lease expiring / reclaimed by new worker
	var createCalled atomic.Int32
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			createCalled.Add(1)
			return nil, errors.New("Finding 1 violation: CreateAccount MUST NOT be called on reclaimed DISPATCHING job")
		},
	}

	// Reset lease so worker can claim it again
	_, err = pool.Exec(context.Background(), `
		UPDATE onboarding.outbox_messages
		SET status = 'PENDING', lease_token = NULL, leased_until = now() - interval '1 second'
		WHERE id = $1
	`, msg.ID)
	if err != nil {
		t.Fatalf("failed to reset outbox message: %v", err)
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected reclaimed job to be processed")
	}

	// Assert CreateAccount was NEVER called
	if calls := createCalled.Load(); calls != 0 {
		t.Fatalf("expected 0 calls to CreateAccount, got %d", calls)
	}

	// Assert dispatch step transitioned to UNRESOLVED
	stepState, _, err := repo.GetDispatchStepState(context.Background(), sub.ID, worker.StepCreateAccount)
	if err != nil {
		t.Fatalf("GetDispatchStepState failed: %v", err)
	}
	if stepState != postgres.DispatchStepStateUnresolved {
		t.Fatalf("expected step state UNRESOLVED, got %s", stepState)
	}

	// Assert submission transitioned to UNRESOLVED
	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if reloadedApp.Status() != domain.ApplicationStatusSubmissionPending {
		t.Fatalf("expected application status SUBMISSION_PENDING (manual hold), got %s", reloadedApp.Status())
	}
	subs := reloadedApp.Submissions()
	foundSub := subs[len(subs)-1]
	if foundSub.Status != domain.SubmissionStatusUnresolved {
		t.Fatalf("expected submission status UNRESOLVED, got %s", foundSub.Status)
	}

	// Assert outbox message completed (no automatic blind retry)
	nextProcessed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("second ProcessNextJob failed: %v", err)
	}
	if nextProcessed {
		t.Fatal("expected no pending outbox job after manual hold transition")
	}
}

// TestWorker_DBLookupFailure_NonErrNotFound_HaltsAndDoesNotCallCreate asserts Finding 1 safe DB lookup:
// ONLY ErrPlatformAccountNotFound allows proceeding to create; any other DB error halts execution immediately.
func TestWorker_DBLookupFailure_NonErrNotFound_HaltsAndDoesNotCallCreate(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, _, _ = createTestCompanyAndApp(t, repo, store)

	// Close the DB connection pool abruptly to simulate connection failure during lookup
	pool.Close()

	var createCalled atomic.Int32
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			createCalled.Add(1)
			return nil, errors.New("CreateAccount should NOT be called on DB lookup failure")
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	_, err := w.ProcessNextJob(context.Background())
	if err == nil {
		t.Fatal("expected error from ProcessNextJob due to closed DB connection")
	}

	if calls := createCalled.Load(); calls != 0 {
		t.Fatalf("expected 0 calls to CreateAccount on DB failure, got %d", calls)
	}
}

// TestWorker_PreflightStorageReadFailure_FailsClosedBeforeCreateAccount asserts Finding 5:
// If storage is unreadable or doc missing, fail closed before any account call!
func TestWorker_PreflightStorageReadFailure_FailsClosedBeforeCreateAccount(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	// Delete the object from storage to trigger storage read failure
	docID := sub.DocumentIDs[0]
	var storageKey string
	for _, d := range app.Documents() {
		if d.ID == docID {
			storageKey = d.StorageKey
			break
		}
	}
	_ = store.Delete(context.Background(), storageKey)

	var createCalled atomic.Int32
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			createCalled.Add(1)
			return nil, errors.New("CreateAccount must NOT be called if pre-flight storage read fails")
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected job to be processed")
	}

	if calls := createCalled.Load(); calls != 0 {
		t.Fatalf("Finding 5 violation: CreateAccount was called %d times despite pre-flight storage failure", calls)
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if reloadedApp.Status() != domain.ApplicationStatusRejected {
		t.Fatalf("expected application REJECTED due to missing document, got %s", reloadedApp.Status())
	}
}

// TestWorker_ConcurrentTerminalEventRace_OutcomeCASConflict asserts Finding 7:
// Application is cancelled/rejected while worker in flight -> outcome write fails CAS and does not regress terminal state.
func TestWorker_ConcurrentTerminalEventRace_OutcomeCASConflict(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			// Concurrently cancel the application in the DB while worker is mid-flight!
			_, err := pool.Exec(ctx, `
				UPDATE onboarding.applications
				SET status = 'CANCELLED',
					revision = revision + 1,
					closed_at = now(),
					updated_at = now()
				WHERE id = $1
			`, app.ID())
			if err != nil {
				return nil, err
			}
			return &airwallex.AccountCreateResponse{
				ID:     "acct_cancelled_race",
				Status: "CREATED",
			}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	_, err := w.ProcessNextJob(context.Background())
	// Should fail with terminal immutability error
	if err == nil {
		t.Fatal("expected error when outcome persistence runs on cancelled application")
	}

	finalApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}

	// Status must REMAIN CANCELLED, never regressed to UNDER_REVIEW!
	if finalApp.Status() != domain.ApplicationStatusCancelled {
		t.Fatalf("Finding 7 violation: application status was regressed to %s after cancellation", finalApp.Status())
	}
}

// TestWorker_Ambiguous5xx_AccountCreate asserts Finding 2:
// 5xx on CreateAccount is ambiguous -> transitions to UNRESOLVED, never blind-retried.
func TestWorker_Ambiguous5xx_AccountCreate(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			// Upstream 5xx on non-idempotent create is ambiguous
			return nil, fmt.Errorf("%w: upstream 503 outage", airwallex.ErrAmbiguousRemoteOutcome)
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected job to be processed")
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}

	// Status must remain SUBMISSION_PENDING (manual hold)
	if reloadedApp.Status() != domain.ApplicationStatusSubmissionPending {
		t.Fatalf("expected application status SUBMISSION_PENDING, got %s", reloadedApp.Status())
	}

	subs := reloadedApp.Submissions()
	foundSub := subs[len(subs)-1]
	if foundSub.ID != sub.ID {
		t.Fatalf("expected sub ID %s, got %s", sub.ID, foundSub.ID)
	}
	if foundSub.Status != domain.SubmissionStatusUnresolved {
		t.Fatalf("expected submission status UNRESOLVED, got %s", foundSub.Status)
	}

	// Outbox job must be completed (no blind retry)
	nextProcessed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("second ProcessNextJob failed: %v", err)
	}
	if nextProcessed {
		t.Fatal("expected no remaining jobs in outbox")
	}
}

// TestWorker_Ambiguous5xx_SubmitAccount asserts Finding 2:
// 5xx on SubmitAccount is ambiguous -> transitions to UNRESOLVED, never blind-retried.
func TestWorker_Ambiguous5xx_SubmitAccount(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return &airwallex.AccountCreateResponse{
				ID:     "acct_submit_5xx_001",
				Status: "CREATED",
			}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			return nil, fmt.Errorf("%w: upstream 502 bad gateway on submit", airwallex.ErrAmbiguousRemoteOutcome)
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected job to be processed")
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}

	if reloadedApp.Status() != domain.ApplicationStatusSubmissionPending {
		t.Fatalf("expected application status SUBMISSION_PENDING, got %s", reloadedApp.Status())
	}

	subs := reloadedApp.Submissions()
	foundSub := subs[len(subs)-1]
	if foundSub.ID != sub.ID {
		t.Fatalf("expected sub ID %s, got %s", sub.ID, foundSub.ID)
	}
	if foundSub.Status != domain.SubmissionStatusUnresolved {
		t.Fatalf("expected submission status UNRESOLVED, got %s", foundSub.Status)
	}
}

// TestWorker_RFIResponse_FailsClosedIntoManualHoldWithoutGenericSubmit asserts Finding 8:
// Generic SubmitAccount is NOT an RFI response. Fails closed into manual hold (UNRESOLVED),
// never calls generic submit or falsely marks RESPONDED.
func TestWorker_RFIResponse_FailsClosedIntoManualHoldWithoutGenericSubmit(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	// Step 1: Initial submission to reach UNDER_REVIEW
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return &airwallex.AccountCreateResponse{
				ID:     "acct_rfi_gate_001",
				Status: "CREATED",
			}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			return &airwallex.AccountSubmitResponse{
				ID:          accountID,
				Status:      "SUBMITTED",
				SubmittedAt: time.Now().UTC().Format(time.RFC3339),
			}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})
	if _, err := w.ProcessNextJob(context.Background()); err != nil {
		t.Fatalf("initial ProcessNextJob failed: %v", err)
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}

	// Step 2: Receive RFI -> status transitions to INFORMATION_REQUIRED
	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	rfi := domain.InformationRequest{
		ID:                rfiID,
		PlatformReference: "req_airwallex_001",
		Questions: []domain.RFIQuestion{
			{
				ID:                 "q1",
				PlatformQuestionID: "platform_q1",
				Text:               "Please verify business formation",
			},
		},
		ReceivedAt: time.Now().UTC(),
	}
	if err := reloadedApp.ReceiveInformationRequest(rfi); err != nil {
		t.Fatalf("ReceiveInformationRequest failed: %v", err)
	}
	if err := repo.UpdateApplication(context.Background(), reloadedApp, reloadedApp.Revision()-1); err != nil {
		t.Fatalf("UpdateApplication with RFI failed: %v", err)
	}

	// Step 3: Respond to RFI -> creates RFI response submission and outbox message
	rfiSubID := fmt.Sprintf("sub_rfi_%d", time.Now().UnixNano())
	answers := map[string]string{"q1": "Document attached confirms formation"}
	_, err = reloadedApp.RespondToInformationRequest(rfiSubID, rfiID, "actor_001", reloadedApp.Company(), answers, nil)
	if err != nil {
		t.Fatalf("RespondToInformationRequest failed: %v", err)
	}

	rfiOutboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: reloadedApp.ID(),
		SubmissionID:  rfiSubID,
	})

	_, err = repo.InitiateSubmission(context.Background(), postgres.InitiateSubmissionParams{
		ApplicationID:        reloadedApp.ID(),
		ExpectedAppRevision:  reloadedApp.Revision() - 1,
		SubmissionID:         rfiSubID,
		InformationRequestID: rfiID,
		SubmittingActor:      "actor_001",
		Answers:              answers,
		OutboxTopic:          "onboarding.submission.v1",
		OutboxPayload:        rfiOutboxPayload,
		IdempotencyScope:     "rfi_submission",
		IdempotencyKey:       fmt.Sprintf("idemp_rfi_%d", time.Now().UnixNano()),
		RequestFingerprint:   "fp_rfi_sha256",
		ResponseStatusCode:   200,
		ResponseBody:         []byte(`{"status":"SUBMISSION_PENDING"}`),
	})
	if err != nil {
		t.Fatalf("InitiateSubmission RFI failed: %v", err)
	}

	// Configure mock platform with verified Lead Docs MCP RFI handlers
	var capturedRespondReq airwallex.RFIRespondRequest
	var submitCalled atomic.Int32
	var respondCalled atomic.Int32

	mockPlatform.submitAccountFunc = func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
		submitCalled.Add(1)
		return nil, errors.New("Lead Docs MCP violation: generic SubmitAccount MUST NOT be called for RFI responses")
	}

	mockPlatform.getRfiFunc = func(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error) {
		return &airwallex.RFIResponsePayload{
			ID:        rfiID,
			AccountID: "acct_rfi_gate_001", // Matches mapped external account ID
			Type:      "KYC",
			Status:    "ACTION_REQUIRED",
			ActiveRequest: &airwallex.RFIActiveRequest{
				Questions: []airwallex.RFIQuestionPrompt{
					{
						ID:     "platform_q1",
						Answer: airwallex.RFIAnswerRequirement{Type: "TEXT"},
					},
				},
			},
		}, nil
	}

	mockPlatform.respondRfiFunc = func(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error) {
		respondCalled.Add(1)
		capturedRespondReq = req
		return &airwallex.RFIRespondResponse{
			StatusCode: 200,
			RawBody:    []byte(`{}`),
		}, nil
	}

	// Step 4: Worker processes RFI submission job
	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("RFI ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected RFI job to be processed")
	}

	// Invariant 1: generic SubmitAccount was NEVER called
	if calls := submitCalled.Load(); calls != 0 {
		t.Fatalf("Lead Docs MCP violation: SubmitAccount was called %d times for RFI response", calls)
	}

	// Invariant 2: RespondRFI was called exactly once with stable mapped platform question ID
	if calls := respondCalled.Load(); calls != 1 {
		t.Fatalf("expected RespondRFI to be called 1 time, got %d", calls)
	}
	if len(capturedRespondReq.Questions) != 1 ||
		capturedRespondReq.Questions[0].ID != "platform_q1" ||
		capturedRespondReq.Questions[0].Answer.Text != "Document attached confirms formation" {
		t.Fatalf("unexpected mapped RFI respond payload: %+v", capturedRespondReq)
	}

	finalApp, err := repo.GetApplication(context.Background(), reloadedApp.ID())
	if err != nil {
		t.Fatalf("GetApplication final failed: %v", err)
	}

	// Invariant 3: Per Lead review, transport 200/201/204 alone is NOT provider confirmation of acceptance.
	// Default response acceptance gate is false: application remains in INFORMATION_REQUIRED (manual hold).
	if finalApp.Status() != domain.ApplicationStatusInformationRequired {
		t.Fatalf("expected application to remain in INFORMATION_REQUIRED under default gate, got %s", finalApp.Status())
	}

	// Invariant 4: RFI remains in RESPONSE_PENDING status (not falsely marked RESPONDED before provider confirmation)
	rfiFound := false
	for _, req := range finalApp.InformationRequests() {
		if req.ID == rfiID {
			rfiFound = true
			if req.Status != domain.InformationRequestStatusResponsePending {
				t.Fatalf("expected RFI status RESPONSE_PENDING under default gate, got %s", req.Status)
			}
		}
	}
	if !rfiFound {
		t.Fatal("expected RFI to be present on application")
	}

	// Invariant 5: Submission status is UNRESOLVED (held pending platform confirmation)
	var foundRFISub *domain.Submission
	for _, s := range finalApp.Submissions() {
		if s.ID == rfiSubID {
			subCopy := s
			foundRFISub = &subCopy
			break
		}
	}
	if foundRFISub == nil || foundRFISub.Status != domain.SubmissionStatusUnresolved {
		t.Fatalf("expected RFI submission to be UNRESOLVED under default gate, got %+v", foundRFISub)
	}

	// Invariant 6: Dispatch step RESPOND_RFI recorded as CONFIRMED in DB (transport delivery succeeded)
	stepState, _, err := repo.GetDispatchStepState(context.Background(), rfiSubID, worker.StepRespondRFI)
	if err != nil || stepState != postgres.DispatchStepStateConfirmed {
		t.Fatalf("expected dispatch step RESPOND_RFI CONFIRMED, got %s (err: %v)", stepState, err)
	}
}

// TestWorker_RFIResponse_GatedAcceptance_WhenEnabled asserts that when AllowRFIResponseAcceptance
// is explicitly enabled, verified transport response transitions RFI to RESPONDED and app to UNDER_REVIEW.
func TestWorker_RFIResponse_GatedAcceptance_WhenEnabled(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return &airwallex.AccountCreateResponse{
				ID:     "acct_rfi_gate_002",
				Status: "CREATED",
			}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			return &airwallex.AccountSubmitResponse{
				ID:          accountID,
				Status:      "SUBMITTED",
				SubmittedAt: time.Now().UTC().Format(time.RFC3339),
			}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{
		LeaseDuration:              10 * time.Second,
		AllowRFIResponseAcceptance: true,
	})
	if _, err := w.ProcessNextJob(context.Background()); err != nil {
		t.Fatalf("initial ProcessNextJob failed: %v", err)
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}

	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	rfi := domain.InformationRequest{
		ID:                rfiID,
		PlatformReference: "req_airwallex_002",
		Questions: []domain.RFIQuestion{
			{
				ID:                 "q1",
				PlatformQuestionID: "platform_q1",
				Text:               "Please verify business formation",
			},
		},
		ReceivedAt: time.Now().UTC(),
	}
	if err := reloadedApp.ReceiveInformationRequest(rfi); err != nil {
		t.Fatalf("ReceiveInformationRequest failed: %v", err)
	}
	if err := repo.UpdateApplication(context.Background(), reloadedApp, reloadedApp.Revision()-1); err != nil {
		t.Fatalf("UpdateApplication with RFI failed: %v", err)
	}

	rfiSubID := fmt.Sprintf("sub_rfi_%d", time.Now().UnixNano())
	answers := map[string]string{"q1": "Document attached confirms formation"}
	_, err = reloadedApp.RespondToInformationRequest(rfiSubID, rfiID, "actor_001", reloadedApp.Company(), answers, nil)
	if err != nil {
		t.Fatalf("RespondToInformationRequest failed: %v", err)
	}

	rfiOutboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: reloadedApp.ID(),
		SubmissionID:  rfiSubID,
	})

	_, err = repo.InitiateSubmission(context.Background(), postgres.InitiateSubmissionParams{
		ApplicationID:        reloadedApp.ID(),
		ExpectedAppRevision:  reloadedApp.Revision() - 1,
		SubmissionID:         rfiSubID,
		InformationRequestID: rfiID,
		SubmittingActor:      "actor_001",
		Answers:              answers,
		OutboxTopic:          "onboarding.submission.v1",
		OutboxPayload:        rfiOutboxPayload,
		IdempotencyScope:     "rfi_submission",
		IdempotencyKey:       fmt.Sprintf("idemp_rfi_%d", time.Now().UnixNano()),
		RequestFingerprint:   "fp_rfi_sha256_2",
		ResponseStatusCode:   200,
		ResponseBody:         []byte(`{"status":"SUBMISSION_PENDING"}`),
	})
	if err != nil {
		t.Fatalf("InitiateSubmission RFI failed: %v", err)
	}

	mockPlatform.getRfiFunc = func(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error) {
		return &airwallex.RFIResponsePayload{
			ID:        rfiID,
			AccountID: "acct_rfi_gate_002",
			Type:      "KYC",
			Status:    "ACTION_REQUIRED",
			ActiveRequest: &airwallex.RFIActiveRequest{
				Questions: []airwallex.RFIQuestionPrompt{
					{
						ID:     "platform_q1",
						Answer: airwallex.RFIAnswerRequirement{Type: "TEXT"},
					},
				},
			},
		}, nil
	}

	mockPlatform.respondRfiFunc = func(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error) {
		return &airwallex.RFIRespondResponse{
			StatusCode: 200,
			RawBody:    []byte(`{}`),
		}, nil
	}

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("RFI ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected RFI job to be processed")
	}

	finalApp, err := repo.GetApplication(context.Background(), reloadedApp.ID())
	if err != nil {
		t.Fatalf("GetApplication final failed: %v", err)
	}
	if finalApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected application to return to UNDER_REVIEW when gate enabled, got %s", finalApp.Status())
	}
	for _, req := range finalApp.InformationRequests() {
		if req.ID == rfiID && req.Status != domain.InformationRequestStatusResponded {
			t.Fatalf("expected RFI status RESPONDED when gate enabled, got %s", req.Status)
		}
	}
}

// TestWorker_RFIResponse_AccountIDMismatch_FailsClosed asserts:
// MUST retrieve/check account_id equals mapped EXTERNAL connected account.
// Mismatch fails closed before sending response.
func TestWorker_RFIResponse_AccountIDMismatch_FailsClosed(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return &airwallex.AccountCreateResponse{
				ID:     "acct_real_001",
				Status: "CREATED",
			}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			return &airwallex.AccountSubmitResponse{ID: accountID, Status: "SUBMITTED"}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})
	_, _ = w.ProcessNextJob(context.Background())

	reloadedApp, _ := repo.GetApplication(context.Background(), app.ID())
	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	rfi := domain.InformationRequest{
		ID:                rfiID,
		PlatformReference: "req_foreign_001",
		Questions: []domain.RFIQuestion{
			{ID: "q1", Text: "Question"},
		},
		ReceivedAt: time.Now().UTC(),
	}
	_ = reloadedApp.ReceiveInformationRequest(rfi)
	_ = repo.UpdateApplication(context.Background(), reloadedApp, reloadedApp.Revision()-1)

	rfiSubID := fmt.Sprintf("sub_rfi_%d", time.Now().UnixNano())
	answers := map[string]string{"q1": "Answer"}
	_, _ = reloadedApp.RespondToInformationRequest(rfiSubID, rfiID, "actor_001", reloadedApp.Company(), answers, nil)
	rfiOutboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: reloadedApp.ID(),
		SubmissionID:  rfiSubID,
	})
	_, _ = repo.InitiateSubmission(context.Background(), postgres.InitiateSubmissionParams{
		ApplicationID:        reloadedApp.ID(),
		ExpectedAppRevision:  reloadedApp.Revision() - 1,
		SubmissionID:         rfiSubID,
		InformationRequestID: rfiID,
		SubmittingActor:      "actor_001",
		Answers:              answers,
		OutboxTopic:          "onboarding.submission.v1",
		OutboxPayload:        rfiOutboxPayload,
		IdempotencyScope:     "rfi_sub",
		IdempotencyKey:       fmt.Sprintf("idemp_rfi_%d", time.Now().UnixNano()),
		RequestFingerprint:   "fp",
		ResponseStatusCode:   200,
		ResponseBody:         []byte(`{"status":"SUBMISSION_PENDING"}`),
	})

	var respondCalled atomic.Int32
	mockPlatform.getRfiFunc = func(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error) {
		return &airwallex.RFIResponsePayload{
			ID:        rfiID,
			AccountID: "acct_FOREIGN_SPOOFED", // Does NOT match acct_real_001!
			Type:      "KYC",
			Status:    "ACTION_REQUIRED",
			ActiveRequest: &airwallex.RFIActiveRequest{
				Questions: []airwallex.RFIQuestionPrompt{
					{ID: "q1", Answer: airwallex.RFIAnswerRequirement{Type: "TEXT"}},
				},
			},
		}, nil
	}
	mockPlatform.respondRfiFunc = func(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error) {
		respondCalled.Add(1)
		return nil, errors.New("RespondRFI must NOT be called on account mismatch")
	}

	_, _ = w.ProcessNextJob(context.Background())

	if calls := respondCalled.Load(); calls != 0 {
		t.Fatalf("expected 0 calls to RespondRFI on account mismatch, got %d", calls)
	}

	finalApp, _ := repo.GetApplication(context.Background(), reloadedApp.ID())
	if finalApp.Status() != domain.ApplicationStatusRejected {
		t.Fatalf("expected application REJECTED on security validation failure, got %s", finalApp.Status())
	}
}

// TestWorker_RFIResponse_DocumentaryType_StaysManualHandling asserts:
// Documentary type RFIs (or non-TEXT question answers) stay in manual handling (UNRESOLVED).
func TestWorker_RFIResponse_DocumentaryType_StaysManualHandling(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return &airwallex.AccountCreateResponse{
				ID:     "acct_doc_001",
				Status: "CREATED",
			}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			return &airwallex.AccountSubmitResponse{ID: accountID, Status: "SUBMITTED"}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})
	_, _ = w.ProcessNextJob(context.Background())

	reloadedApp, _ := repo.GetApplication(context.Background(), app.ID())
	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	rfi := domain.InformationRequest{
		ID:                rfiID,
		PlatformReference: "req_doc_001",
		Questions: []domain.RFIQuestion{
			{ID: "q1", Text: "Please upload bank statement"},
		},
		ReceivedAt: time.Now().UTC(),
	}
	_ = reloadedApp.ReceiveInformationRequest(rfi)
	_ = repo.UpdateApplication(context.Background(), reloadedApp, reloadedApp.Revision()-1)

	rfiSubID := fmt.Sprintf("sub_rfi_%d", time.Now().UnixNano())
	answers := map[string]string{"q1": "See attached"}
	_, _ = reloadedApp.RespondToInformationRequest(rfiSubID, rfiID, "actor_001", reloadedApp.Company(), answers, nil)
	rfiOutboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: reloadedApp.ID(),
		SubmissionID:  rfiSubID,
	})
	_, _ = repo.InitiateSubmission(context.Background(), postgres.InitiateSubmissionParams{
		ApplicationID:        reloadedApp.ID(),
		ExpectedAppRevision:  reloadedApp.Revision() - 1,
		SubmissionID:         rfiSubID,
		InformationRequestID: rfiID,
		SubmittingActor:      "actor_001",
		Answers:              answers,
		OutboxTopic:          "onboarding.submission.v1",
		OutboxPayload:        rfiOutboxPayload,
		IdempotencyScope:     "rfi_sub",
		IdempotencyKey:       fmt.Sprintf("idemp_rfi_%d", time.Now().UnixNano()),
		RequestFingerprint:   "fp",
		ResponseStatusCode:   200,
		ResponseBody:         []byte(`{"status":"SUBMISSION_PENDING"}`),
	})

	var respondCalled atomic.Int32
	mockPlatform.getRfiFunc = func(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error) {
		return &airwallex.RFIResponsePayload{
			ID:        rfiID,
			AccountID: "acct_doc_001",
			Type:      "DOCUMENTARY", // Documentary type!
			Status:    "ACTION_REQUIRED",
			ActiveRequest: &airwallex.RFIActiveRequest{
				Questions: []airwallex.RFIQuestionPrompt{
					{ID: "q1", Answer: airwallex.RFIAnswerRequirement{Type: "FILE"}},
				},
			},
		}, nil
	}
	mockPlatform.respondRfiFunc = func(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error) {
		respondCalled.Add(1)
		return nil, errors.New("RespondRFI must NOT be called for documentary RFI")
	}

	_, _ = w.ProcessNextJob(context.Background())

	if calls := respondCalled.Load(); calls != 0 {
		t.Fatalf("expected 0 calls to RespondRFI for documentary RFI, got %d", calls)
	}

	finalApp, _ := repo.GetApplication(context.Background(), reloadedApp.ID())
	// Application MUST remain in INFORMATION_REQUIRED (manual hold)
	if finalApp.Status() != domain.ApplicationStatusInformationRequired {
		t.Fatalf("expected application to remain in INFORMATION_REQUIRED, got %s", finalApp.Status())
	}
	subs := finalApp.Submissions()
	if subs[len(subs)-1].Status != domain.SubmissionStatusUnresolved {
		t.Fatalf("expected submission UNRESOLVED (manual hold), got %s", subs[len(subs)-1].Status)
	}
}

// TestWorker_RFIResponse_Ambiguous5xx_TransitionsToUnresolved asserts:
// Upstream 5xx on RespondRFI transitions submission to UNRESOLVED, completed outbox, no blind retry.
func TestWorker_RFIResponse_Ambiguous5xx_TransitionsToUnresolved(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return &airwallex.AccountCreateResponse{ID: "acct_500_rfi", Status: "CREATED"}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			return &airwallex.AccountSubmitResponse{ID: accountID, Status: "SUBMITTED"}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})
	_, _ = w.ProcessNextJob(context.Background())

	reloadedApp, _ := repo.GetApplication(context.Background(), app.ID())
	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	rfi := domain.InformationRequest{
		ID:                rfiID,
		PlatformReference: "req_500_001",
		Questions: []domain.RFIQuestion{
			{ID: "q1", Text: "Question"},
		},
		ReceivedAt: time.Now().UTC(),
	}
	_ = reloadedApp.ReceiveInformationRequest(rfi)
	_ = repo.UpdateApplication(context.Background(), reloadedApp, reloadedApp.Revision()-1)

	rfiSubID := fmt.Sprintf("sub_rfi_%d", time.Now().UnixNano())
	answers := map[string]string{"q1": "Answer"}
	_, _ = reloadedApp.RespondToInformationRequest(rfiSubID, rfiID, "actor_001", reloadedApp.Company(), answers, nil)
	rfiOutboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: reloadedApp.ID(),
		SubmissionID:  rfiSubID,
	})
	_, _ = repo.InitiateSubmission(context.Background(), postgres.InitiateSubmissionParams{
		ApplicationID:        reloadedApp.ID(),
		ExpectedAppRevision:  reloadedApp.Revision() - 1,
		SubmissionID:         rfiSubID,
		InformationRequestID: rfiID,
		SubmittingActor:      "actor_001",
		Answers:              answers,
		OutboxTopic:          "onboarding.submission.v1",
		OutboxPayload:        rfiOutboxPayload,
		IdempotencyScope:     "rfi_sub",
		IdempotencyKey:       fmt.Sprintf("idemp_rfi_%d", time.Now().UnixNano()),
		RequestFingerprint:   "fp",
		ResponseStatusCode:   200,
		ResponseBody:         []byte(`{"status":"SUBMISSION_PENDING"}`),
	})

	mockPlatform.getRfiFunc = func(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error) {
		return &airwallex.RFIResponsePayload{
			ID:        rfiID,
			AccountID: "acct_500_rfi",
			Type:      "KYC",
			Status:    "ACTION_REQUIRED",
			ActiveRequest: &airwallex.RFIActiveRequest{
				Questions: []airwallex.RFIQuestionPrompt{
					{ID: "q1", Answer: airwallex.RFIAnswerRequirement{Type: "TEXT"}},
				},
			},
		}, nil
	}
	mockPlatform.respondRfiFunc = func(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error) {
		return nil, airwallex.ErrAmbiguousRemoteOutcome
	}

	_, _ = w.ProcessNextJob(context.Background())

	finalApp, _ := repo.GetApplication(context.Background(), reloadedApp.ID())
	if finalApp.Status() != domain.ApplicationStatusInformationRequired {
		t.Fatalf("expected application to remain in INFORMATION_REQUIRED on ambiguous outcome, got %s", finalApp.Status())
	}
	subs := finalApp.Submissions()
	if subs[len(subs)-1].Status != domain.SubmissionStatusUnresolved {
		t.Fatalf("expected submission UNRESOLVED, got %s", subs[len(subs)-1].Status)
	}
}

func TestWorker_StaleLeaseToken_Fencing(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	msg1, err := repo.ClaimOutboxMessage(context.Background(), 10*time.Second)
	if err != nil || msg1 == nil {
		t.Fatalf("ClaimOutboxMessage failed: %v", err)
	}

	// Worker 2 steals the job
	_, err = pool.Exec(context.Background(), `
		UPDATE onboarding.outbox_messages
		SET lease_token = 'stolen_token_xyz',
			leased_until = now() + interval '1 minute'
		WHERE id = $1
	`, msg1.ID)
	if err != nil {
		t.Fatalf("failed to update lease_token: %v", err)
	}

	// Worker 1 attempts to save platform account ID with stale lease token
	err = repo.SavePlatformAccountIDWithLease(context.Background(), app.ID(), "acct_stale_001", msg1.ID, msg1.LeaseToken)
	if !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken on SavePlatformAccountIDWithLease, got %v", err)
	}

	// Worker 1 attempts to record outcome with stale lease token
	err = repo.RecordWorkerSubmissionOutcomeWithLease(context.Background(), postgres.WorkerOutcomeParams{
		OutboxID:              msg1.ID,
		LeaseToken:            msg1.LeaseToken,
		ApplicationID:         app.ID(),
		SubmissionID:          sub.ID,
		SubmissionStatus:      domain.SubmissionStatusAccepted,
		DefinitiveInvalidData: false,
		ApplicationStatus:     domain.ApplicationStatusUnderReview,
		CompleteJob:           true,
	})
	if !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken on RecordWorkerSubmissionOutcomeWithLease, got %v", err)
	}

	freshApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if freshApp.Status() != domain.ApplicationStatusSubmissionPending {
		t.Fatalf("expected application status SUBMISSION_PENDING, got %s", freshApp.Status())
	}
	if freshApp.Platform().PlatformAccountID != "" {
		t.Fatalf("expected empty platform account ID, got %s", freshApp.Platform().PlatformAccountID)
	}
}

func TestWorker_CrashRecovery_ExistingPlatformAccount(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	comp, app, _ := createTestCompanyAndApp(t, repo, store)

	existingPA, err := domain.NewPlatformAccount(
		fmt.Sprintf("pa_rec_%d", time.Now().UnixNano()),
		comp.ID(),
		app.Platform().Name,
		app.Platform().Environment,
	)
	if err != nil {
		t.Fatalf("NewPlatformAccount failed: %v", err)
	}
	syncTime := time.Now().UTC()
	if err := existingPA.SetExternalAccountID("acct_recovered_from_prior_run"); err != nil {
		t.Fatalf("SetExternalAccountID failed: %v", err)
	}
	if err := existingPA.RecordObservedStatus(domain.PlatformAccountStatusCreated, syncTime); err != nil {
		t.Fatalf("RecordObservedStatus failed: %v", err)
	}
	if err := repo.CreatePlatformAccount(context.Background(), existingPA); err != nil {
		t.Fatalf("CreatePlatformAccount failed: %v", err)
	}

	var createCalled int32
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			atomic.AddInt32(&createCalled, 1)
			return nil, errors.New("CreateAccount should NOT be called during crash recovery when account exists")
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			if accountID != "acct_recovered_from_prior_run" {
				return nil, fmt.Errorf("unexpected account ID: %s", accountID)
			}
			return &airwallex.AccountSubmitResponse{
				ID:          accountID,
				Status:      "SUBMITTED",
				SubmittedAt: time.Now().UTC().Format(time.RFC3339),
			}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{
		LeaseDuration: 10 * time.Second,
	})

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected job to be processed")
	}

	if calls := atomic.LoadInt32(&createCalled); calls != 0 {
		t.Fatalf("expected 0 calls to CreateAccount, got %d", calls)
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if reloadedApp.Platform().PlatformAccountID != existingPA.ID() {
		t.Fatalf("expected platform account ID %s, got %s", existingPA.ID(), reloadedApp.Platform().PlatformAccountID)
	}
	if reloadedApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected UNDER_REVIEW, got %s", reloadedApp.Status())
	}
}

func TestWorker_Definitive400_Rejection(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return nil, fmt.Errorf("%w: invalid tax identifier format", airwallex.ErrInvalidData)
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{
		LeaseDuration: 10 * time.Second,
	})

	processed, err := w.ProcessNextJob(context.Background())
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !processed {
		t.Fatal("expected job to be processed")
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}

	if reloadedApp.Status() != domain.ApplicationStatusRejected {
		t.Fatalf("expected application status REJECTED, got %s", reloadedApp.Status())
	}
	if reloadedApp.Platform().Decision != nil {
		t.Fatalf("expected null decision on data refusal per design, got %v", *reloadedApp.Platform().Decision)
	}

	subs := reloadedApp.Submissions()
	foundSub := subs[len(subs)-1]
	if foundSub.ID != sub.ID {
		t.Fatalf("expected sub ID %s, got %s", sub.ID, foundSub.ID)
	}
	if foundSub.Status != domain.SubmissionStatusFailed {
		t.Fatalf("expected submission status FAILED, got %s", foundSub.Status)
	}
	if !foundSub.DefinitiveInvalidData {
		t.Fatal("expected DefinitiveInvalidData to be true")
	}
}

func TestWorker_RateLimited429_Backoff(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return nil, airwallex.ErrRateLimited
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{
		LeaseDuration: 10 * time.Second,
		RetryBackoff:  2 * time.Second,
	})

	_, err := w.ProcessNextJob(context.Background())
	if !errors.Is(err, airwallex.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}

	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if reloadedApp.Status() != domain.ApplicationStatusSubmissionPending {
		t.Fatalf("expected application status SUBMISSION_PENDING, got %s", reloadedApp.Status())
	}

	subs := reloadedApp.Submissions()
	foundSub := subs[len(subs)-1]
	if foundSub.ID != sub.ID {
		t.Fatalf("expected sub ID %s, got %s", sub.ID, foundSub.ID)
	}
	if foundSub.Status != domain.SubmissionStatusPending {
		t.Fatalf("expected submission status PENDING, got %s", foundSub.Status)
	}
}

func TestWorker_SnapshotPayloadImmutability(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	comp, _, _ := createTestCompanyAndApp(t, repo, store)

	// Mutate live company AFTER submission was created!
	freshComp, err := repo.GetCompany(context.Background(), comp.ID())
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	err = freshComp.UpdateTradingName("Brand New Shiny Trading Name")
	if err != nil {
		t.Fatalf("UpdateTradingName failed: %v", err)
	}
	if err := repo.UpdateCompany(context.Background(), freshComp, freshComp.Revision()-1); err != nil {
		t.Fatalf("UpdateCompany failed: %v", err)
	}

	var capturedReq airwallex.AccountCreateRequest
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			capturedReq = req
			return &airwallex.AccountCreateResponse{
				ID:     "acct_snapshot_immutability",
				Status: "CREATED",
			}, nil
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})
	if _, err := w.ProcessNextJob(context.Background()); err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}

	if capturedReq.AccountDetails.BusinessDetails.BusinessName != "Acme Innovations LLC" {
		t.Fatalf("expected legal name Acme Innovations LLC, got %s", capturedReq.AccountDetails.BusinessDetails.BusinessName)
	}
}

func TestWorker_ConcurrentTwoWorkersRacingSameJob(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			return &airwallex.AccountCreateResponse{
				ID:     "acct_race_001",
				Status: "CREATED",
			}, nil
		},
	}

	w1 := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})
	w2 := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	var wg sync.WaitGroup
	results := make([]bool, 2)
	errs := make([]error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], errs[0] = w1.ProcessNextJob(context.Background())
	}()
	go func() {
		defer wg.Done()
		results[1], errs[1] = w2.ProcessNextJob(context.Background())
	}()
	wg.Wait()

	successCount := 0
	if results[0] && errs[0] == nil {
		successCount++
	}
	if results[1] && errs[1] == nil {
		successCount++
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 worker to process the job successfully, got %d (w1: %v/%v, w2: %v/%v)",
			successCount, results[0], errs[0], results[1], errs[1])
	}

	finalApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if finalApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected final application status UNDER_REVIEW, got %s", finalApp.Status())
	}
}

func TestWorker_MalformedPayload_Deadletters(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	// Insert malformed payload into outbox
	msgID := fmt.Sprintf("outbox_malformed_%d", time.Now().UnixNano())
	_, err := pool.Exec(context.Background(), `
		INSERT INTO onboarding.outbox_messages (
			id, topic, payload, status, retry_count, created_at, updated_at
		) VALUES ($1, 'onboarding.submission.v1', '{"application_id": 12345}', 'PENDING', 0, now(), now())
	`, msgID)
	if err != nil {
		t.Fatalf("failed to insert malformed outbox message: %v", err)
	}

	w := worker.NewWorker(repo, storage.NewMemoryStore(), &mockPlatformClient{}, worker.Config{
		LeaseDuration: 10 * time.Second,
	})

	processed, err := w.ProcessNextJob(context.Background())
	if err == nil {
		t.Fatal("expected error on malformed payload")
	}
	if !processed {
		t.Fatal("expected malformed job to be processed (and deadlettered)")
	}

	// Verify outbox message is marked complete (no retry loop)
	var status string
	err = pool.QueryRow(context.Background(), "SELECT status FROM onboarding.outbox_messages WHERE id = $1", msgID).Scan(&status)
	if err != nil {
		t.Fatalf("failed to query outbox message: %v", err)
	}
	if status != "DELIVERED" {
		t.Fatalf("expected outbox message status DELIVERED (deadlettered), got %s", status)
	}
}

// faultInjectingRepo wraps a Repository to inject failures into specific operations.
type faultInjectingRepo struct {
	worker.Repository
	saveAccountErr  error
	saveAccountHits atomic.Int32
}

func (f *faultInjectingRepo) SavePlatformAccountWithLease(ctx context.Context, params postgres.SavePlatformAccountParams) (string, error) {
	hits := f.saveAccountHits.Add(1)
	if hits == 1 && f.saveAccountErr != nil {
		return "", f.saveAccountErr
	}
	return f.Repository.SavePlatformAccountWithLease(ctx, params)
}

func TestWorker_CreateAccount_RemoteSuccess_SaveIDFails_PreservesIDWithoutDuplicateCreate(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, _ := createTestCompanyAndApp(t, repo, store)

	var createCalls atomic.Int32
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			createCalls.Add(1)
			return &airwallex.AccountCreateResponse{
				ID:     "acct_preserved_test_999",
				Status: "CREATED",
			}, nil
		},
		uploadFileFunc: func(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*airwallex.FileUploadResponse, error) {
			return &airwallex.FileUploadResponse{FileID: "file_123"}, nil
		},
		updateAccountFunc: func(ctx context.Context, accountID string, req airwallex.AccountUpdateRequest) (*airwallex.AccountDetailsResponse, error) {
			return &airwallex.AccountDetailsResponse{ID: accountID, Status: "UPDATED"}, nil
		},
		submitAccountFunc: func(ctx context.Context, accountID string) (*airwallex.AccountSubmitResponse, error) {
			return &airwallex.AccountSubmitResponse{ID: accountID, Status: "SUBMITTED"}, nil
		},
	}

	simulatedErr := errors.New("simulated DB failure on SavePlatformAccount")
	faultRepo := &faultInjectingRepo{
		Repository:     repo,
		saveAccountErr: simulatedErr,
	}

	w := worker.NewWorker(faultRepo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	// First execution: remote create succeeds, but SavePlatformAccountWithLease fails!
	processed, err := w.ProcessNextJob(context.Background())
	if err == nil || !errors.Is(err, simulatedErr) {
		t.Fatalf("expected simulatedErr, got %v", err)
	}
	if !processed {
		t.Fatal("expected job to be processed")
	}

	// Verify CreateAccount was called once
	if calls := createCalls.Load(); calls != 1 {
		t.Fatalf("expected 1 call to CreateAccount, got %d", calls)
	}

	// Verify coordinator preserved CONFIRMED and the external account ID in DB
	var coordState, coordExtID string
	err = pool.QueryRow(context.Background(), `
		SELECT state, external_account_id
		FROM onboarding.platform_creation_coordinators
		WHERE company_id = $1
	`, app.Company().ID()).Scan(&coordState, &coordExtID)
	if err != nil {
		t.Fatalf("failed to query coordinator: %v", err)
	}
	if coordState != "CONFIRMED" || coordExtID != "acct_preserved_test_999" {
		t.Fatalf("expected coordinator CONFIRMED with acct_preserved_test_999, got state=%s extID=%s", coordState, coordExtID)
	}

	// Reset outbox lease so reclaimed worker can re-process
	_, err = pool.Exec(context.Background(), `
		UPDATE onboarding.outbox_messages
		SET status = 'PENDING', lease_token = NULL, leased_until = now() - interval '1 second'
	`)
	if err != nil {
		t.Fatalf("failed to reset outbox: %v", err)
	}

	// Second execution: reclaimed job must reuse preserved external ID and NEVER call CreateAccount again!
	faultRepo.saveAccountErr = nil // DB recovers
	processed2, err2 := w.ProcessNextJob(context.Background())
	if err2 != nil {
		t.Fatalf("second ProcessNextJob failed: %v", err2)
	}
	if !processed2 {
		t.Fatal("expected reclaimed job to be processed")
	}

	// Assert CreateAccount was STILL called only 1 time!
	if calls := createCalls.Load(); calls != 1 {
		t.Fatalf("CRITICAL: CreateAccount was called %d times; expected exactly 1 (duplicate creation prevention failed!)", calls)
	}

	// Assert application successfully linked the preserved platform account
	reloadedApp, err := repo.GetApplication(context.Background(), app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if reloadedApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected application status UNDER_REVIEW, got %s", reloadedApp.Status())
	}
}

func TestWorker_LeaseExpiry_DuringRequest_SecondWorkerFencing(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, app, sub := createTestCompanyAndApp(t, repo, store)

	// Worker 1 claims job with short lease duration
	msg, err := repo.ClaimOutboxMessage(context.Background(), 200*time.Millisecond)
	if err != nil || msg == nil {
		t.Fatalf("ClaimOutboxMessage failed: %v", err)
	}

	// Simulate slow remote call during which lease expires on server
	time.Sleep(300 * time.Millisecond)

	// Worker 2 reclaims the expired job
	msg2, err := repo.ClaimOutboxMessage(context.Background(), 10*time.Second)
	if err != nil || msg2 == nil {
		t.Fatalf("Worker 2 failed to claim expired message: %v", err)
	}
	if msg2.LeaseToken == msg.LeaseToken {
		t.Fatal("expected Worker 2 to obtain fresh distinct lease token")
	}

	// Worker 1 returns from remote call and attempts to record step state with expired token
	err = repo.RecordDispatchStepStateWithLease(
		context.Background(),
		sub.ID,
		worker.StepCreateAccount,
		postgres.DispatchStepStateConfirmed,
		"acct_stale_1",
		msg.ID,
		msg.LeaseToken,
	)
	if err == nil || !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken for Worker 1 with expired lease, got %v", err)
	}

	// Worker 1 attempts to record outcome with expired token: rejected
	err = repo.RecordWorkerSubmissionOutcomeWithLease(context.Background(), postgres.WorkerOutcomeParams{
		OutboxID:            msg.ID,
		LeaseToken:          msg.LeaseToken,
		ApplicationID:       app.ID(),
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        sub.ID,
		SubmissionStatus:    domain.SubmissionStatusAccepted,
		ApplicationStatus:   domain.ApplicationStatusUnderReview,
		CompleteJob:         true,
	})
	if err == nil || !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatalf("expected ErrStaleLeaseToken on outcome recording for Worker 1, got %v", err)
	}
}

func TestWorker_ForeignSubmission_ValidToken_CannotMutate(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	_, _, subA := createTestCompanyAndApp(t, repo, store)
	_ = subA.ID
	_, appB, subB := createTestCompanyAndApp(t, repo, store)

	// Claim Job for Application A / Submission A
	msgA, err := repo.ClaimOutboxMessage(context.Background(), 10*time.Second)
	if err != nil || msgA == nil {
		t.Fatalf("ClaimOutboxMessage for Job A failed: %v", err)
	}

	// Attempt 1: Mutate dispatch step for Submission B using Job A's valid lease token -> Linkage mismatch
	err = repo.RecordDispatchStepStateWithLease(
		context.Background(),
		subB.ID,
		worker.StepCreateAccount,
		postgres.DispatchStepStateConfirmed,
		"acct_spoofed",
		msgA.ID,
		msgA.LeaseToken,
	)
	if err == nil || !errors.Is(err, postgres.ErrOutboxJobLinkageMismatch) {
		t.Fatalf("expected ErrOutboxJobLinkageMismatch on foreign dispatch step mutation, got %v", err)
	}

	// Attempt 2: Record document upload for Submission B using Job A's valid lease token -> Linkage mismatch
	err = repo.RecordDocumentUploadWithLease(
		context.Background(),
		subB.ID,
		"doc_spoofed",
		"file_spoofed",
		msgA.ID,
		msgA.LeaseToken,
	)
	if err == nil || !errors.Is(err, postgres.ErrOutboxJobLinkageMismatch) {
		t.Fatalf("expected ErrOutboxJobLinkageMismatch on foreign document upload mutation, got %v", err)
	}

	// Attempt 3: Record outcome for Submission B / App B using Job A's valid lease token -> Linkage mismatch
	err = repo.RecordWorkerSubmissionOutcomeWithLease(context.Background(), postgres.WorkerOutcomeParams{
		OutboxID:            msgA.ID,
		LeaseToken:          msgA.LeaseToken,
		ApplicationID:       appB.ID(),
		ExpectedAppRevision: appB.Revision(),
		SubmissionID:        subB.ID,
		SubmissionStatus:    domain.SubmissionStatusAccepted,
		ApplicationStatus:   domain.ApplicationStatusUnderReview,
		CompleteJob:         true,
	})
	if err == nil || !errors.Is(err, postgres.ErrOutboxJobLinkageMismatch) {
		t.Fatalf("expected ErrOutboxJobLinkageMismatch on foreign outcome persistence, got %v", err)
	}
}

func TestWorker_DistinctApplications_SameCompany_CreationCoordinated(t *testing.T) {
	pool, repo := setupTestDB(t)
	defer pool.Close()

	store := storage.NewMemoryStore()
	comp, app1, sub1 := createTestCompanyAndApp(t, repo, store)

	// In the real system, only one open application exists per company/platform at a time.
	// Mark app1 REJECTED in DB so app2 can be created as a subsequent application for the same company.
	_, err := pool.Exec(context.Background(), `UPDATE onboarding.applications SET status = 'REJECTED' WHERE id = $1`, app1.ID())
	if err != nil {
		t.Fatalf("failed to update app1 to terminal status: %v", err)
	}

	// Create a second distinct application for the exact SAME company
	nano2 := time.Now().UnixNano()
	appID2 := fmt.Sprintf("app_coord_%d", nano2)
	subID2 := fmt.Sprintf("sub_coord_%d", nano2)

	app2, err := domain.NewDraftApplication(appID2, comp, "test-user-2")
	if err != nil {
		t.Fatalf("NewDraftApplication failed: %v", err)
	}

	if err := app2.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		PrimaryContactPersonID: "person_001",
		IndustryCategoryCode:   "5734",
		EstimatedRevenue:       domain.RevenueEstimate{Amount: "250000", Currency: "USD"},
		CustomerAgreements: domain.CustomerAgreements{
			AgreedToDataUsage:          true,
			AgreedToTermsAndConditions: true,
			TermsAndConditions: &domain.TermsAndConditionsAgreement{
				AgreedAt:             time.Now().UTC(),
				ServiceAgreementType: "FULL",
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOnboardingDetails failed: %v", err)
	}

	if err := repo.CreateApplication(context.Background(), app2); err != nil {
		t.Fatalf("CreateApplication 2 failed: %v", err)
	}

	sub2, err := app2.InitiateInitialSubmission(subID2, "test-user-2", nil)
	if err != nil {
		t.Fatalf("InitiateInitialSubmission 2 failed: %v", err)
	}

	outboxPayload2, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: appID2,
		SubmissionID:  subID2,
	})

	_, err = repo.InitiateSubmission(context.Background(), postgres.InitiateSubmissionParams{
		ApplicationID:       appID2,
		ExpectedAppRevision: app2.Revision() - 1,
		SubmissionID:        subID2,
		SubmittingActor:     "test-user-2",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       outboxPayload2,
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("idem_coord_app2_%d", nano2),
		RequestFingerprint:  "fp_coord_2",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	})
	if err != nil {
		t.Fatalf("InitiateSubmission 2 failed: %v", err)
	}

	// Worker 1 claims Job 1 for App 1 and sets coordinator to DISPATCHING
	msg1, err := repo.ClaimOutboxMessage(context.Background(), 10*time.Second)
	if err != nil || msg1 == nil {
		t.Fatalf("ClaimOutboxMessage 1 failed: %v", err)
	}

	coord1, err := repo.ClaimPlatformCreationCoordinationWithLease(
		context.Background(),
		comp.ID(),
		app1.Platform().Name,
		app1.Platform().Environment,
		sub1.ID,
		msg1.ID,
		msg1.LeaseToken,
	)
	if err != nil {
		t.Fatalf("ClaimPlatformCreationCoordinationWithLease 1 failed: %v", err)
	}
	if coord1.State != postgres.PlatformCreationStateDispatching {
		t.Fatalf("expected coordinator 1 DISPATCHING, got %s", coord1.State)
	}

	// Now Job 2 for App 2 runs while Job 1 is dispatching
	var createCallsApp2 atomic.Int32
	mockPlatform := &mockPlatformClient{
		createAccountFunc: func(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
			createCallsApp2.Add(1)
			return nil, errors.New("CRITICAL VIOLATION: CreateAccount called by Job 2 while Job 1 is dispatching for same Company")
		},
	}

	w := worker.NewWorker(repo, store, mockPlatform, worker.Config{LeaseDuration: 10 * time.Second})

	// Claim Job 2
	msg2, err := repo.ClaimOutboxMessage(context.Background(), 10*time.Second)
	if err != nil || msg2 == nil {
		t.Fatalf("ClaimOutboxMessage 2 failed: %v", err)
	}

	// Process Job 2
	err = w.ProcessSubmissionJob(context.Background(), msg2)
	if err != nil {
		t.Fatalf("ProcessSubmissionJob 2 failed: %v", err)
	}

	// Assert CreateAccount was NEVER called by Job 2
	if calls := createCallsApp2.Load(); calls != 0 {
		t.Fatalf("expected 0 calls to CreateAccount by Job 2, got %d", calls)
	}

	// Assert Job 2 transitioned to manual hold / UNRESOLVED
	reloadedApp2, err := repo.GetApplication(context.Background(), app2.ID())
	if err != nil {
		t.Fatalf("GetApplication 2 failed: %v", err)
	}
	if reloadedApp2.Status() != domain.ApplicationStatusSubmissionPending {
		t.Fatalf("expected App 2 to be in SUBMISSION_PENDING (manual hold), got %s", reloadedApp2.Status())
	}
	subs2 := reloadedApp2.Submissions()
	if subs2[len(subs2)-1].Status != domain.SubmissionStatusUnresolved {
		t.Fatalf("expected App 2 submission to be UNRESOLVED, got %s", subs2[len(subs2)-1].Status)
	}

	// Assert dispatch step for sub2 is UNRESOLVED
	stepState2, _, err := repo.GetDispatchStepState(context.Background(), sub2.ID, worker.StepCreateAccount)
	if err != nil {
		t.Fatalf("GetDispatchStepState failed: %v", err)
	}
	if stepState2 != postgres.DispatchStepStateUnresolved {
		t.Fatalf("expected sub 2 StepCreateAccount to be UNRESOLVED, got %s", stepState2)
	}
}
