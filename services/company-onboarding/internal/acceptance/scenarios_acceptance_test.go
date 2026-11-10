package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/worker"
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
	if err != nil || cfg.ConnConfig.Port != 54329 {
		t.Fatalf("SAFETY CHECK: connection port %d must be 54329", cfg.ConnConfig.Port)
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
		t.Fatalf("failed to initialize test database: %v", testDBErr)
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
		t.Fatalf("failed to truncate test tables: %v", err)
	}
}

// helper to create a valid Company, Application, and Submission with all required snapshot data
func createCompleteCompanyAppAndSub(
	t *testing.T,
	ctx context.Context,
	repo *postgres.Repository,
	store storage.ObjectStore,
	compID, appID, subID, docID string,
) (*domain.Company, *domain.OnboardingApplication, *domain.Submission) {
	t.Helper()

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

	sub, err := app.InitiateInitialSubmission(subID, "actor_001", []string{docID})
	if err != nil {
		t.Fatalf("InitiateInitialSubmission failed: %v", err)
	}

	outboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: appID,
		SubmissionID:  subID,
	})

	_, err = repo.InitiateSubmission(ctx, postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision() - 1,
		SubmissionID:        subID,
		SubmittingActor:     "actor_001",
		DocumentIDs:         []string{docID},
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       outboxPayload,
		IdempotencyScope:    "application_submission",
		IdempotencyKey:      fmt.Sprintf("idemp_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_sha256",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	})
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	return comp, app, sub
}

// FakeAirwallexTransport implements airwallex.PlatformClient and webhooks.PlatformAccountClient.
type FakeAirwallexTransport struct {
	mu           sync.Mutex
	createCalls  atomic.Int32
	submitCalls  atomic.Int32
	respondCalls atomic.Int32
	createdAccts map[string]string // extID -> status
	failCreate   error
	failSubmit   error

	rfiAccountID  string
	rfiQuestionID string
	lastRespond   airwallex.RFIRespondRequest
}

func newFakeAirwallexTransport() *FakeAirwallexTransport {
	return &FakeAirwallexTransport{
		createdAccts: make(map[string]string),
	}
}

func (f *FakeAirwallexTransport) CreateAccount(ctx context.Context, req airwallex.AccountCreateRequest) (*airwallex.AccountCreateResponse, error) {
	callIdx := f.createCalls.Add(1)
	if f.failCreate != nil {
		return nil, f.failCreate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	acctID := fmt.Sprintf("acct_fake_%d", callIdx)
	f.createdAccts[acctID] = "CREATED"
	return &airwallex.AccountCreateResponse{
		ID:     acctID,
		Status: "CREATED",
	}, nil
}

func (f *FakeAirwallexTransport) UploadFile(ctx context.Context, r io.Reader, fileName string, fileSize int64, contentType string) (*airwallex.FileUploadResponse, error) {
	return &airwallex.FileUploadResponse{
		FileID:   "file_fake_upload_123",
		Filename: fileName,
		Size:     fileSize,
	}, nil
}

func (f *FakeAirwallexTransport) UpdateAccount(ctx context.Context, platformAccountID string, req airwallex.AccountUpdateRequest) (*airwallex.AccountDetailsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdAccts[platformAccountID] = "SUBMITTED"
	return &airwallex.AccountDetailsResponse{
		ID:     platformAccountID,
		Status: "SUBMITTED",
	}, nil
}

func (f *FakeAirwallexTransport) SubmitAccount(ctx context.Context, platformAccountID string) (*airwallex.AccountSubmitResponse, error) {
	f.submitCalls.Add(1)
	if f.failSubmit != nil {
		return nil, f.failSubmit
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdAccts[platformAccountID] = "SUBMITTED"
	return &airwallex.AccountSubmitResponse{
		ID:     platformAccountID,
		Status: "SUBMITTED",
	}, nil
}

func (f *FakeAirwallexTransport) GetAccount(ctx context.Context, platformAccountID string) (*airwallex.AccountDetailsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.createdAccts[platformAccountID]
	if !ok {
		status = "ACTIVE"
	}
	return &airwallex.AccountDetailsResponse{
		ID:     platformAccountID,
		Status: status,
	}, nil
}

func (f *FakeAirwallexTransport) GetRFI(ctx context.Context, rfiID string) (*airwallex.RFIResponsePayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	acctID := f.rfiAccountID
	if acctID == "" {
		acctID = "acct_fake_rfi"
	}
	qID := f.rfiQuestionID
	if qID == "" {
		qID = "platform_q1"
	}
	return &airwallex.RFIResponsePayload{
		ID:        rfiID,
		AccountID: acctID,
		Type:      "KYC",
		Status:    "ACTION_REQUIRED",
		ActiveRequest: &airwallex.RFIActiveRequest{
			Questions: []airwallex.RFIQuestionPrompt{
				{
					ID: qID,
					Answer: airwallex.RFIAnswerRequirement{
						Type: "TEXT",
					},
				},
			},
		},
	}, nil
}

func (f *FakeAirwallexTransport) RespondRFI(ctx context.Context, rfiID string, req airwallex.RFIRespondRequest) (*airwallex.RFIRespondResponse, error) {
	f.respondCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRespond = req
	return &airwallex.RFIRespondResponse{
		StatusCode: 200,
		RawBody:    []byte(`{"status":"SUCCESS"}`),
	}, nil
}

func TestAcceptance_Scenario1_SuccessLifecycle(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_scen1"
	appID := "app_scen1"
	subID := "sub_scen1"
	docID := "doc_scen1"

	memStore := storage.NewMemoryStore()
	_, app, _ := createCompleteCompanyAppAndSub(t, ctx, repo, memStore, compID, appID, subID, docID)

	fakeTransport := newFakeAirwallexTransport()
	w := worker.NewWorker(repo, memStore, fakeTransport, worker.Config{
		LeaseDuration: 30 * time.Second,
		RetryBackoff:  5 * time.Second,
	})

	// Dispatch initial submission
	dispatched, err := w.ProcessNextJob(ctx)
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !dispatched {
		t.Fatalf("expected job to be dispatched")
	}

	// Assert: Application entered UNDER_REVIEW
	loadedApp, err := repo.GetApplication(ctx, app.ID())
	if err != nil {
		t.Fatalf("failed to load application: %v", err)
	}
	if loadedApp.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected application to be UNDER_REVIEW, got %s", loadedApp.Status())
	}

	internalPAID := loadedApp.Platform().PlatformAccountID
	if internalPAID == "" {
		t.Fatalf("expected platform account ID to be populated on app")
	}

	pa, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("failed to load PA: %v", err)
	}
	extAccountID := pa.ExternalAccountID()
	if extAccountID == "" {
		t.Fatalf("expected external account ID to be populated on PA")
	}

	// Inbound Webhook: account.active arrives
	fakeTransport.mu.Lock()
	fakeTransport.createdAccts[extAccountID] = "ACTIVE"
	fakeTransport.mu.Unlock()

	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_scen1_active",
		AccountID:   extAccountID,
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_scen1_active","name":"account.active"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	// Process webhook
	whProcessor := webhooks.NewProcessor(repo, fakeTransport)
	processed, err := whProcessor.ProcessPending(ctx)
	if err != nil || processed != 1 {
		t.Fatalf("expected 1 processed webhook, got %d (err: %v)", processed, err)
	}

	// Assert: Final state is COMPLETED with decision APPROVED
	finalApp, err := repo.GetApplication(ctx, app.ID())
	if err != nil {
		t.Fatalf("failed to load final application: %v", err)
	}
	if finalApp.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("expected application to be COMPLETED, got %s", finalApp.Status())
	}
	if finalApp.Platform().Decision == nil || *finalApp.Platform().Decision != domain.PlatformDecisionApproved {
		t.Fatalf("expected decision APPROVED, got %v", finalApp.Platform().Decision)
	}
}

func TestAcceptance_Scenario2_DefinitiveRejectionLifecycle(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_scen2"
	appID := "app_scen2"
	subID := "sub_scen2"
	docID := "doc_scen2"

	memStore := storage.NewMemoryStore()
	_, app, sub := createCompleteCompanyAppAndSub(t, ctx, repo, memStore, compID, appID, subID, docID)

	// Platform fails with definitive rejection
	fakeTransport := newFakeAirwallexTransport()
	fakeTransport.failCreate = fmt.Errorf("%w: invalid tax identifier format", airwallex.ErrInvalidData)

	w := worker.NewWorker(repo, memStore, fakeTransport, worker.Config{
		LeaseDuration: 30 * time.Second,
		RetryBackoff:  5 * time.Second,
	})

	dispatched, err := w.ProcessNextJob(ctx)
	if err != nil {
		t.Fatalf("ProcessNextJob failed: %v", err)
	}
	if !dispatched {
		t.Fatalf("expected job to be processed")
	}

	// Assert: Application transitions to REJECTED, Submission marked FAILED
	loadedApp, err := repo.GetApplication(ctx, app.ID())
	if err != nil {
		t.Fatalf("failed to get app: %v", err)
	}
	if loadedApp.Status() != domain.ApplicationStatusRejected {
		t.Fatalf("expected application to be REJECTED on definitive refusal, got %s", loadedApp.Status())
	}

	loadedSub, err := repo.GetSubmission(ctx, sub.ID)
	if err != nil {
		t.Fatalf("failed to get sub: %v", err)
	}
	if loadedSub.Status != domain.SubmissionStatusFailed {
		t.Fatalf("expected submission to be FAILED, got %s", loadedSub.Status)
	}
}

func TestAcceptance_Scenario3_UnresolvedAmbiguityAndCrashRecovery_NoDuplicate(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_scen3"
	appID := "app_scen3"
	subID := "sub_scen3"
	docID := "doc_scen3"

	memStore := storage.NewMemoryStore()
	_, app, sub := createCompleteCompanyAppAndSub(t, ctx, repo, memStore, compID, appID, subID, docID)

	fakeTransport := newFakeAirwallexTransport()
	// Simulate ambiguous 500 network failure during CreateAccount
	fakeTransport.failCreate = fmt.Errorf("%w: upstream 503 outage", airwallex.ErrAmbiguousRemoteOutcome)

	w := worker.NewWorker(repo, memStore, fakeTransport, worker.Config{
		LeaseDuration: 30 * time.Second,
		RetryBackoff:  5 * time.Second,
	})

	// Dispatch attempt encounters ambiguous error
	dispatched, err := w.ProcessNextJob(ctx)
	if err != nil {
		t.Fatalf("ProcessNextJob error: %v", err)
	}
	if !dispatched {
		t.Fatalf("expected job to be processed")
	}

	// Verify step state is UNRESOLVED
	stepState, _, err := repo.GetDispatchStepState(ctx, sub.ID, worker.StepCreateAccount)
	if err != nil {
		t.Fatalf("failed to load step: %v", err)
	}
	if stepState != postgres.DispatchStepStateUnresolved {
		t.Fatalf("expected dispatch state UNRESOLVED, got %s", stepState)
	}

	// Status must remain SUBMISSION_PENDING (manual hold)
	reloadedApp, err := repo.GetApplication(ctx, app.ID())
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if reloadedApp.Status() != domain.ApplicationStatusSubmissionPending {
		t.Fatalf("expected status SUBMISSION_PENDING on ambiguity hold, got %s", reloadedApp.Status())
	}

	initialCreateCalls := fakeTransport.createCalls.Load()

	// A second worker pass (or crash recovery) must NEVER blind re-dispatch create!
	dispatchedAgain, _ := w.ProcessNextJob(ctx)
	if dispatchedAgain {
		t.Fatalf("prohibited redispatch of UNRESOLVED job step")
	}
	if fakeTransport.createCalls.Load() != initialCreateCalls {
		t.Fatalf("CRITICAL BUG: worker made duplicate CreateAccount call on unresolved job! calls=%d", fakeTransport.createCalls.Load())
	}
}

func TestAcceptance_Scenario4_RFILifecycle_PreservedSnapshotsAndGatedAcceptance(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	compID := "comp_scen4_rfi"
	appID := "app_scen4_rfi"
	subID := "sub_scen4_init"
	docID := "doc_scen4_formation"

	memStore := storage.NewMemoryStore()
	_, _, _ = createCompleteCompanyAppAndSub(t, ctx, repo, memStore, compID, appID, subID, docID)

	fakeTransport := newFakeAirwallexTransport()

	// Initial submission worker pass: DRAFT -> SUBMISSION_PENDING -> UNDER_REVIEW
	wInit := worker.NewWorker(repo, memStore, fakeTransport, worker.Config{
		LeaseDuration: 30 * time.Second,
		RetryBackoff:  5 * time.Second,
	})
	dispatched, err := wInit.ProcessNextJob(ctx)
	if err != nil || !dispatched {
		t.Fatalf("initial dispatch failed: %v", err)
	}

	// Verify initial submission is ACCEPTED and app is UNDER_REVIEW
	appAfterInit, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if appAfterInit.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected app UNDER_REVIEW after initial submission, got %s", appAfterInit.Status())
	}
	internalPAID := appAfterInit.Platform().PlatformAccountID
	pa, err := repo.GetPlatformAccount(ctx, internalPAID)
	if err != nil {
		t.Fatalf("GetPlatformAccount failed: %v", err)
	}
	externalAcctID := pa.ExternalAccountID()

	// Capture initial submission snapshot: confirms original company trading name
	initialSub, err := repo.GetSubmission(ctx, subID)
	if err != nil {
		t.Fatalf("GetSubmission failed: %v", err)
	}
	if initialSub.Type != domain.SubmissionTypeInitialApplication {
		t.Fatalf("expected initial submission type INITIAL_APPLICATION, got %s", initialSub.Type)
	}
	if initialSub.Status != domain.SubmissionStatusAccepted {
		t.Fatalf("expected initial submission status ACCEPTED, got %s", initialSub.Status)
	}
	initialTradingName := initialSub.CompanyCopy.TradingName()

	// Step 2: Platform raises an RFI inquiry -> Application receives RFI -> INFORMATION_REQUIRED
	rfiID := "rfi_scen4_inquiry_01"
	rfi := domain.InformationRequest{
		ID:                rfiID,
		PlatformReference: "plat_rfi_ref_999",
		Questions: []domain.RFIQuestion{
			{
				ID:                 "q_dba_1",
				PlatformQuestionID: "platform_q1",
				Text:               "Please provide documentation for amended trading name",
			},
		},
		ReceivedAt: time.Now().UTC(),
	}
	if err := appAfterInit.ReceiveInformationRequest(rfi); err != nil {
		t.Fatalf("ReceiveInformationRequest failed: %v", err)
	}
	if err := repo.UpdateApplication(ctx, appAfterInit, appAfterInit.Revision()-1); err != nil {
		t.Fatalf("UpdateApplication with RFI failed: %v", err)
	}

	appWithRFI, err := repo.GetApplication(ctx, appID)
	if err != nil || appWithRFI.Status() != domain.ApplicationStatusInformationRequired {
		t.Fatalf("expected app to transition to INFORMATION_REQUIRED, got %v", appWithRFI.Status())
	}

	// Step 3: Operator updates trading name on Company and initiates RFI response submission
	// This freezes an updated snapshot in the response submission WITHOUT rewriting the initial snapshot.
	liveComp, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	amendedCompany := liveComp.DeepCopy()
	_ = amendedCompany.UpdateTradingName("Acme Treasury Pro Solutions")
	if err := repo.UpdateCompany(ctx, amendedCompany, liveComp.Revision()); err != nil {
		t.Fatalf("UpdateCompany failed: %v", err)
	}

	rfiSubID := "sub_scen4_rfi_resp"
	answers := map[string]string{
		"q_dba_1": "DBA amended with state registry to Acme Treasury Pro Solutions",
	}

	_, err = appWithRFI.RespondToInformationRequest(rfiSubID, rfiID, "operator_ramil", amendedCompany, answers, nil)
	if err != nil {
		t.Fatalf("RespondToInformationRequest failed: %v", err)
	}

	rfiOutboxPayload, _ := json.Marshal(worker.SubmissionJobPayload{
		ApplicationID: appID,
		SubmissionID:  rfiSubID,
	})

	_, err = repo.InitiateSubmission(ctx, postgres.InitiateSubmissionParams{
		ApplicationID:        appID,
		ExpectedAppRevision:  appWithRFI.Revision() - 1,
		SubmissionID:         rfiSubID,
		InformationRequestID: rfiID,
		SubmittingActor:      "operator_ramil",
		Answers:              answers,
		OutboxTopic:          "onboarding.submission.v1",
		OutboxPayload:        rfiOutboxPayload,
		IdempotencyScope:     "rfi_submission",
		IdempotencyKey:       "idemp_scen4_rfi_key",
		RequestFingerprint:   "fp_scen4_rfi_sha",
		ResponseStatusCode:   200,
		ResponseBody:         []byte(`{"status":"SUBMISSION_PENDING"}`),
	})
	if err != nil {
		t.Fatalf("repo.InitiateSubmission for RFI failed: %v", err)
	}

	// Step 4: Worker executes RFI dispatch
	// Configure fake platform with matching external account ID and question ID
	fakeTransport.rfiAccountID = externalAcctID
	fakeTransport.rfiQuestionID = "platform_q1"

	// Worker uses explicit test-only gate AllowRFIResponseAcceptance: true (production default is false)
	wRFI := worker.NewWorker(repo, memStore, fakeTransport, worker.Config{
		LeaseDuration:              30 * time.Second,
		RetryBackoff:               5 * time.Second,
		AllowRFIResponseAcceptance: true,
	})

	rfiDispatched, err := wRFI.ProcessNextJob(ctx)
	if err != nil || !rfiDispatched {
		t.Fatalf("RFI ProcessNextJob failed: %v (dispatched=%v)", err, rfiDispatched)
	}

	// Assertions for Invariant 1: RespondRFI called exactly once; SubmitAccount NOT called for RFI
	if fakeTransport.respondCalls.Load() != 1 {
		t.Fatalf("expected RespondRFI to be called 1 time, got %d", fakeTransport.respondCalls.Load())
	}
	if fakeTransport.submitCalls.Load() != 1 { // 1 from initial submission, 0 from RFI
		t.Fatalf("expected generic SubmitAccount to NOT be called for RFI, got total %d", fakeTransport.submitCalls.Load())
	}

	// Assertions for Invariant 2: Dispatch step RESPOND_RFI marked CONFIRMED
	rfiStepState, _, err := repo.GetDispatchStepState(ctx, rfiSubID, worker.StepRespondRFI)
	if err != nil || rfiStepState != postgres.DispatchStepStateConfirmed {
		t.Fatalf("expected RESPOND_RFI step state CONFIRMED, got %s (err: %v)", rfiStepState, err)
	}

	// Assertions for Invariant 3: Application returned to UNDER_REVIEW, RFI is RESPONDED
	appAfterRFI, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	if appAfterRFI.Status() != domain.ApplicationStatusUnderReview {
		t.Fatalf("expected app UNDER_REVIEW after RFI response, got %s", appAfterRFI.Status())
	}

	// Assertions for Invariant 4: Dual Snapshots Preserved and Distinct!
	// Snapshot 1 (Initial):
	loadedInitialSub, err := repo.GetSubmission(ctx, subID)
	if err != nil {
		t.Fatalf("failed to reload initial sub: %v", err)
	}
	if loadedInitialSub.CompanyCopy.TradingName() != initialTradingName {
		t.Fatalf("SNAPSHOT CORRUPTION: initial submission trading name changed from %q to %q",
			initialTradingName, loadedInitialSub.CompanyCopy.TradingName())
	}

	// Snapshot 2 (RFI Response):
	loadedRFISub, err := repo.GetSubmission(ctx, rfiSubID)
	if err != nil {
		t.Fatalf("failed to reload RFI sub: %v", err)
	}
	if loadedRFISub.Type != domain.SubmissionTypeInformationResponse {
		t.Fatalf("expected RFI submission type %s, got %s", domain.SubmissionTypeInformationResponse, loadedRFISub.Type)
	}
	if loadedRFISub.CompanyCopy.TradingName() != "Acme Treasury Pro Solutions" {
		t.Fatalf("expected RFI submission company copy to have updated trading name, got %q",
			loadedRFISub.CompanyCopy.TradingName())
	}
	if loadedRFISub.Answers["q_dba_1"] != answers["q_dba_1"] {
		t.Fatalf("expected RFI submission answers to match, got %v", loadedRFISub.Answers)
	}

	// Step 5: Webhook confirms account active -> Application COMPLETED, Decision APPROVED
	fakeTransport.mu.Lock()
	fakeTransport.createdAccts[externalAcctID] = "ACTIVE"
	fakeTransport.mu.Unlock()

	_, err = repo.InsertWebhookOnce(ctx, postgres.WebhookInboxItem{
		EventID:     "evt_scen4_active",
		AccountID:   externalAcctID,
		EventType:   "account.active",
		Environment: "sandbox",
		RawPayload:  json.RawMessage(`{"id":"evt_scen4_active","name":"account.active"}`),
	})
	if err != nil {
		t.Fatalf("failed to insert webhook: %v", err)
	}

	whProcessor := webhooks.NewProcessor(repo, fakeTransport)
	processed, err := whProcessor.ProcessPending(ctx)
	if err != nil || processed != 1 {
		t.Fatalf("expected 1 processed webhook, got %d (err: %v)", processed, err)
	}

	completedApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("failed to reload completed app: %v", err)
	}
	if completedApp.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("expected app COMPLETED after platform activation, got %s", completedApp.Status())
	}
	if completedApp.Platform().Decision == nil || *completedApp.Platform().Decision != domain.PlatformDecisionApproved {
		t.Fatalf("expected approved decision on COMPLETED app, got %+v", completedApp.Platform().Decision)
	}
}

func TestAcceptance_Scenario5_LostEventPeriodicRecovery(t *testing.T) {
	pool := getRealPostgresPool(t)
	cleanRealPostgres(t, pool)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	comp, _ := domain.NewDraftCompany("comp_lost", "Lost Event Corp")
	_ = repo.CreateCompany(ctx, comp)
	pa, _ := domain.NewPlatformAccount("pa_lost", comp.ID(), "AIRWALLEX", "sandbox")
	_ = pa.SetExternalAccountID("acct_lost_ext_1")
	_ = repo.CreatePlatformAccount(ctx, pa)
	app, _ := domain.NewDraftApplication("app_lost", comp, "op")
	_ = repo.CreateApplication(ctx, app)

	// Link application in UNDER_REVIEW
	_, _ = pool.Exec(ctx, `
		UPDATE onboarding.applications
		SET platform_account_id = 'pa_lost', platform_name = 'AIRWALLEX', platform_environment = 'sandbox', status = 'UNDER_REVIEW'
		WHERE id = 'app_lost'
	`)

	// Provider is ACTIVE, but NO webhook was delivered (dropped event)
	fakeTransport := newFakeAirwallexTransport()
	fakeTransport.createdAccts["acct_lost_ext_1"] = "ACTIVE"

	processor := webhooks.NewProcessor(repo, fakeTransport)

	// Periodic reconciliation executes
	reconciled, err := processor.ReconcileAccounts(ctx)
	if err != nil {
		t.Fatalf("ReconcileAccounts failed: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("expected 1 reconciled account, got %d", reconciled)
	}

	// Assert: Application recovered to COMPLETED and PA to ACTIVE
	loadedPA, err := repo.GetPlatformAccount(ctx, "pa_lost")
	if err != nil || loadedPA.Status() != domain.PlatformAccountStatusActive {
		t.Fatalf("expected PA to be recovered to ACTIVE, got %v", loadedPA.Status())
	}
	loadedApp, err := repo.GetApplication(ctx, "app_lost")
	if err != nil || loadedApp.Status() != domain.ApplicationStatusCompleted {
		t.Fatalf("expected app to be recovered to COMPLETED, got %v", loadedApp.Status())
	}
}
