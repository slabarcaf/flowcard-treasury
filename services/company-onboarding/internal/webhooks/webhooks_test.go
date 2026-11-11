package webhooks_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

// MockRepository implements both webhooks.Repository and webhooks.ProcessorRepository.
type MockRepository struct {
	insertedItems       []postgres.WebhookInboxItem
	insertErr           error
	claimedItems        []postgres.WebhookInboxItem
	claimErr            error
	processedEvents     map[string]postgres.WebhookInboxStatus
	processedErrors     map[string]string
	applications        map[string]*domain.OnboardingApplication
	platformAccounts    map[string]*domain.PlatformAccount
	savedApplications   map[string]*domain.OnboardingApplication
	savedPlatformAccts  map[string]*domain.PlatformAccount
	appliedObservations []postgres.ApplyWebhookObservationParams
}

func newMockRepository() *MockRepository {
	return &MockRepository{
		processedEvents:    make(map[string]postgres.WebhookInboxStatus),
		processedErrors:    make(map[string]string),
		applications:       make(map[string]*domain.OnboardingApplication),
		platformAccounts:   make(map[string]*domain.PlatformAccount),
		savedApplications:  make(map[string]*domain.OnboardingApplication),
		savedPlatformAccts: make(map[string]*domain.PlatformAccount),
	}
}

func (m *MockRepository) InsertWebhookOnce(ctx context.Context, item postgres.WebhookInboxItem) (bool, error) {
	if m.insertErr != nil {
		return false, m.insertErr
	}
	m.insertedItems = append(m.insertedItems, item)
	return true, nil
}

func (m *MockRepository) ClaimPendingWebhooks(ctx context.Context, limit int, leaseDuration time.Duration) ([]postgres.WebhookInboxItem, error) {
	if m.claimErr != nil {
		return nil, m.claimErr
	}
	items := m.claimedItems
	m.claimedItems = nil // drain
	return items, nil
}

func (m *MockRepository) MarkWebhookProcessed(ctx context.Context, eventID, leaseToken string, status postgres.WebhookInboxStatus, lastError string) error {
	m.processedEvents[eventID] = status
	m.processedErrors[eventID] = lastError
	return nil
}

func (m *MockRepository) GetApplicationByPlatformAccountID(ctx context.Context, platformAccountID string) (*domain.OnboardingApplication, error) {
	for _, app := range m.applications {
		if app.Platform().PlatformAccountID == platformAccountID {
			return app, nil
		}
	}
	for _, pa := range m.platformAccounts {
		if pa.ExternalAccountID() == platformAccountID || pa.ID() == platformAccountID {
			for _, app := range m.applications {
				if app.Platform().PlatformAccountID == pa.ID() || (app.Company().ID() == pa.CompanyID() && app.Platform().Name == pa.PlatformName()) {
					return app, nil
				}
			}
		}
	}
	return nil, postgres.ErrApplicationNotFound
}

func (m *MockRepository) GetPlatformAccountByExternalID(ctx context.Context, externalAccountID string) (*domain.PlatformAccount, error) {
	for _, pa := range m.platformAccounts {
		if pa.ExternalAccountID() == externalAccountID {
			return pa, nil
		}
	}
	return nil, postgres.ErrPlatformAccountNotFound
}

func (m *MockRepository) GetPlatformAccountByExternalIDAndEnv(ctx context.Context, externalAccountID, platformName, environment string) (*domain.PlatformAccount, error) {
	cleanEnv := strings.TrimSpace(environment)
	for _, pa := range m.platformAccounts {
		if pa.ExternalAccountID() == externalAccountID {
			if cleanEnv == "" || strings.EqualFold(pa.Environment(), cleanEnv) {
				return pa, nil
			}
		}
	}
	for _, app := range m.applications {
		if app.Platform().PlatformAccountID == externalAccountID {
			if cleanEnv == "" || strings.EqualFold(app.Platform().Environment, cleanEnv) {
				pa, _ := domain.NewPlatformAccount(externalAccountID, app.Company().ID(), app.Platform().Name, app.Platform().Environment)
				_ = pa.SetExternalAccountID(externalAccountID)
				m.platformAccounts[pa.ID()] = pa
				return pa, nil
			}
		}
	}
	return nil, postgres.ErrPlatformAccountNotFound
}

func (m *MockRepository) RecordReconciliationOutcome(ctx context.Context, paID string, nextCheckIn time.Duration) error {
	return nil
}

func (m *MockRepository) ApplyWebhookObservation(ctx context.Context, params postgres.ApplyWebhookObservationParams) error {
	m.appliedObservations = append(m.appliedObservations, params)
	if params.EventID != "" {
		m.processedEvents[params.EventID] = params.InboxStatus
		m.processedErrors[params.EventID] = params.InboxError
	}

	var targetPA *domain.PlatformAccount
	for _, pa := range m.platformAccounts {
		if pa.ExternalAccountID() == params.AccountID || pa.ID() == params.AccountID ||
			(params.ExternalAccountID != "" && pa.ExternalAccountID() == params.ExternalAccountID) ||
			(params.InternalAccountID != "" && pa.ID() == params.InternalAccountID) {
			targetPA = pa
			break
		}
	}
	if targetPA == nil {
		for _, app := range m.applications {
			if app.Platform().PlatformAccountID == params.AccountID {
				pa, _ := domain.NewPlatformAccount(params.AccountID, app.Company().ID(), app.Platform().Name, app.Platform().Environment)
				_ = pa.SetExternalAccountID(params.AccountID)
				targetPA = pa
				m.platformAccounts[pa.ID()] = pa
				break
			}
		}
	}

	if targetPA != nil {
		syncTime := params.SyncTime
		if syncTime.IsZero() {
			syncTime = time.Now().UTC()
		}
		switch params.ObservedStatus {
		case domain.PlatformAccountStatusActive:
			_ = targetPA.MarkActive(syncTime)
		case domain.PlatformAccountStatusSuspended:
			_ = targetPA.MarkSuspended(syncTime)
		default:
			_ = targetPA.RecordObservedStatus(params.ObservedStatus, syncTime)
		}
		m.savedPlatformAccts[targetPA.ID()] = targetPA
	}

	// Update application
	for _, app := range m.applications {
		if (targetPA != nil && (app.Platform().PlatformAccountID == targetPA.ID() || (app.Company().ID() == targetPA.CompanyID() && app.Platform().Name == targetPA.PlatformName()))) || app.Platform().PlatformAccountID == params.AccountID {
			if params.ObservedStatus == domain.PlatformAccountStatusActive {
				if targetPA != nil {
					_ = app.RecordPlatformAccountActivation(targetPA)
				}
			} else if params.ObservedStatus == domain.PlatformAccountStatusSuspended {
				_ = app.HandlePlatformSuspension(false)
			}
			m.savedApplications[app.ID()] = app
			break
		}
	}

	return nil
}

func (m *MockRepository) ListTrackedPlatformAccounts(ctx context.Context, limit int) ([]domain.PlatformAccount, error) {
	var list []domain.PlatformAccount
	for _, pa := range m.platformAccounts {
		if pa.Status() != domain.PlatformAccountStatusClosed {
			list = append(list, *pa)
		}
	}
	return list, nil
}

type MockPlatformClient struct {
	accountDetails *airwallex.AccountDetailsResponse
	getErr         error
}

func (m *MockPlatformClient) GetAccount(ctx context.Context, platformAccountID string) (*airwallex.AccountDetailsResponse, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.accountDetails != nil {
		return m.accountDetails, nil
	}
	return &airwallex.AccountDetailsResponse{
		ID:     platformAccountID,
		Status: "ACTIVE",
	}, nil
}

func createTestApplication(t *testing.T, appID, companyID, platformAccountID, env string, status domain.ApplicationStatus) *domain.OnboardingApplication {
	t.Helper()
	now := time.Now().UTC()

	comp, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:         companyID,
		Status:     domain.CompanyStatusActive,
		Revision:   1,
		LegalName:  "Acme Onboarding Test Inc",
		EntityType: "CORPORATION",
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create test company: %v", err)
	}

	var closedAt *time.Time
	var decision *domain.PlatformDecision
	if status == domain.ApplicationStatusCompleted {
		closedAt = &now
		d := domain.PlatformDecisionApproved
		decision = &d
	} else if status == domain.ApplicationStatusRejected {
		closedAt = &now
		d := domain.PlatformDecisionRejected
		decision = &d
	}

	app, err := domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:      appID,
		Company: comp,
		Status:  status,
		Platform: domain.ApplicationPlatform{
			Name:              "AIRWALLEX",
			Environment:       env,
			PlatformAccountID: platformAccountID,
			Decision:          decision,
		},
		Revision:  1,
		Creator:   "test-operator",
		CreatedAt: now,
		UpdatedAt: now,
		ClosedAt:  closedAt,
	})
	if err != nil {
		t.Fatalf("failed to create test application: %v", err)
	}
	return app
}

func TestWebhookHandler_SuccessAndDurableAcknowledgement(t *testing.T) {
	repo := newMockRepository()
	nudgeCh := make(chan struct{}, 1)
	handler := webhooks.NewHandler(repo, func() {
		select {
		case nudgeCh <- struct{}{}:
		default:
		}
	})

	payload := webhooks.InboundWebhookPayload{
		ID:          "evt_active_100",
		Name:        "account.active",
		AccountID:   "acct_plat_123",
		Environment: "sandbox",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/airwallex", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp webhooks.WebhookAcknowledgeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if !resp.Received || resp.EventID != "evt_active_100" {
		t.Errorf("unexpected acknowledge payload: %+v", resp)
	}

	// Verify durable insertion
	if len(repo.insertedItems) != 1 {
		t.Fatalf("expected 1 inserted item, got %d", len(repo.insertedItems))
	}
	item := repo.insertedItems[0]
	if item.EventID != "evt_active_100" || item.AccountID != "acct_plat_123" || item.EventType != "account.active" {
		t.Errorf("unexpected inserted item: %+v", item)
	}

	select {
	case <-nudgeCh:
	case <-time.After(500 * time.Millisecond):
		t.Errorf("expected nudge to be called upon receipt")
	}
}

func TestWebhookHandler_FailsClosedOnDatabaseError_NoFalseAck(t *testing.T) {
	repo := newMockRepository()
	repo.insertErr = errors.New("database connection refused")
	handler := webhooks.NewHandler(repo, nil)

	payload := webhooks.InboundWebhookPayload{
		ID:        "evt_active_101",
		Name:      "account.active",
		AccountID: "acct_plat_123",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/airwallex", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"received":true`) {
		t.Errorf("prohibited false acknowledgement returned on DB failure: %s", rec.Body.String())
	}
}

func TestWebhookHandler_RejectsInvalidPayloads(t *testing.T) {
	repo := newMockRepository()
	handler := webhooks.NewHandler(repo, nil)

	// 1. Missing event ID and Name
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/airwallex", strings.NewReader(`{}`))
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty payload, got %d", rec1.Code)
	}

	// 2. Non-POST method
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/webhooks/airwallex", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", rec2.Code)
	}
}

func TestWebhookProcessor_AccountActive_CompletesApplication(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "ACTIVE",
		},
	}

	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_act_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.active",
			Environment: "sandbox",
			RawPayload:  json.RawMessage(`{"id":"evt_act_1","name":"account.active"}`),
			Status:      postgres.WebhookStatusPending,
		},
	}

	count, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 processed item, got %d", count)
	}

	// Verify application transitioned to COMPLETED
	if app.Status() != domain.ApplicationStatusCompleted {
		t.Errorf("expected application status COMPLETED, got %s", app.Status())
	}
	if app.ClosedAt() == nil {
		t.Errorf("expected closedAt to be set upon activation")
	}

	// Verify status marked PROCESSED in repository
	if repo.processedEvents["evt_act_1"] != postgres.WebhookStatusProcessed {
		t.Errorf("expected webhook status PROCESSED, got %s", repo.processedEvents["evt_act_1"])
	}

	// Verify active platform account saved
	if repo.savedPlatformAccts["acct_plat_1"] == nil {
		t.Errorf("expected active platform account to be saved")
	}
}

func TestWebhookProcessor_CompletedApplicationImmutability(t *testing.T) {
	repo := newMockRepository()
	// Already completed application
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusCompleted)
	initialRev := app.Revision()
	repo.applications["app_1"] = app

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	// 1. Repeat account.active event -> zero touch, idempotent PROCESSED
	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_act_repeat",
			AccountID:   "acct_plat_1",
			EventType:   "account.active",
			Environment: "sandbox",
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusCompleted {
		t.Errorf("expected status to remain COMPLETED, got %s", app.Status())
	}
	if app.Revision() != initialRev {
		t.Errorf("expected revision to be unchanged on duplicate event, got %d vs %d", app.Revision(), initialRev)
	}
	if repo.processedEvents["evt_act_repeat"] != postgres.WebhookStatusProcessed {
		t.Errorf("expected repeat event to be marked PROCESSED, got %s", repo.processedEvents["evt_act_repeat"])
	}

	// 2. Later account.suspended event -> never rewrites COMPLETED application
	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          2,
			EventID:     "evt_susp_completed",
			AccountID:   "acct_plat_1",
			EventType:   "account.suspended",
			Environment: "sandbox",
		},
	}

	_, err = processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}
	if app.Status() != domain.ApplicationStatusCompleted {
		t.Errorf("prohibited rewrite: COMPLETED application was rewritten to %s", app.Status())
	}
}

func TestWebhookProcessor_AccountSuspended_MidReviewFlagsOperator(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "SUSPENDED",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_susp_midreview",
			AccountID:   "acct_plat_1",
			EventType:   "account.suspended",
			Environment: "sandbox",
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	// Must NOT blindly reject; mid-review suspension flags operator review
	if app.Status() == domain.ApplicationStatusRejected {
		t.Errorf("prohibited blind rejection of mid-review application on suspension")
	}
	if !app.OperatorReviewFlagged() {
		t.Errorf("expected operatorReviewFlagged to be true")
	}
	if repo.processedEvents["evt_susp_midreview"] != postgres.WebhookStatusProcessed {
		t.Errorf("expected event to be PROCESSED, got %s", repo.processedEvents["evt_susp_midreview"])
	}
}

func TestWebhookProcessor_ActionRequired_QuarantinedUntilVerified(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "ACTION_REQUIRED",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_act_req_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.action_required",
			Environment: "sandbox",
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	// Must NOT blindly map to RFI or transition application
	if app.Status() != domain.ApplicationStatusUnderReview {
		t.Errorf("prohibited blind status transition: expected UNDER_REVIEW, got %s", app.Status())
	}
	if repo.processedEvents["evt_act_req_1"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected action_required to be QUARANTINED, got %s", repo.processedEvents["evt_act_req_1"])
	}
}

func TestWebhookProcessor_UnknownAccountAndCrossEnvironment_Quarantined(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_prod", "production", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_prod",
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	// 1. Unknown platform account ID
	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_unknown_acct",
			AccountID:   "acct_does_not_exist",
			EventType:   "account.active",
			Environment: "sandbox",
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}
	if repo.processedEvents["evt_unknown_acct"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected unknown account event to be QUARANTINED, got %s", repo.processedEvents["evt_unknown_acct"])
	}

	// 2. Cross-environment event (sandbox event for production app)
	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          2,
			EventID:     "evt_cross_env",
			AccountID:   "acct_plat_prod",
			EventType:   "account.active",
			Environment: "sandbox", // mismatch with production app
		},
	}

	_, err = processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}
	if repo.processedEvents["evt_cross_env"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected cross-environment event to be QUARANTINED, got %s", repo.processedEvents["evt_cross_env"])
	}
	if app.Status() != domain.ApplicationStatusUnderReview {
		t.Errorf("cross-environment event corrupted application status: got %s", app.Status())
	}
}

func TestReconciler_CancellableWithContext(t *testing.T) {
	repo := newMockRepository()
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)
	reconciler := webhooks.NewReconciler(processor, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- reconciler.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("reconciler failed to stop upon context cancellation")
	}
}

func TestWebhookProcessor_AccountActive_FreshReadSuspendedNeverCompletes(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	// Authoritative fresh read indicates SUSPENDED despite active event
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "SUSPENDED",
		},
	}

	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_act_stale_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.active",
			Environment: "sandbox",
			RawPayload:  json.RawMessage(`{"id":"evt_act_stale_1","name":"account.active"}`),
			Status:      postgres.WebhookStatusPending,
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	// Must NOT complete application!
	if app.Status() == domain.ApplicationStatusCompleted {
		t.Fatalf("prohibited completion: application completed despite fresh read being SUSPENDED")
	}
	// Must flag operator review
	if !app.OperatorReviewFlagged() {
		t.Errorf("expected operatorReviewFlagged to be true when fresh read is SUSPENDED")
	}
	if repo.processedEvents["evt_act_stale_1"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected event to be QUARANTINED, got %s", repo.processedEvents["evt_act_stale_1"])
	}
}

func TestWebhookProcessor_AccountActive_ReturnedAccountIDMismatchQuarantined(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	// Returned account ID from platform does not match event account ID
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_different_999",
			Status: "ACTIVE",
		},
	}

	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_mismatch_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.active",
			Environment: "sandbox",
			RawPayload:  json.RawMessage(`{"id":"evt_mismatch_1","name":"account.active"}`),
			Status:      postgres.WebhookStatusPending,
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	if app.Status() == domain.ApplicationStatusCompleted {
		t.Fatalf("prohibited completion: application completed despite platform returned account ID mismatch")
	}
	if repo.processedEvents["evt_mismatch_1"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected event to be QUARANTINED, got %s", repo.processedEvents["evt_mismatch_1"])
	}
	if !strings.Contains(repo.processedErrors["evt_mismatch_1"], "account ID mismatch") {
		t.Errorf("expected mismatch error message, got: %s", repo.processedErrors["evt_mismatch_1"])
	}
}

func TestWebhookProcessor_AccountActive_AuthoritativeNonActiveStatusQuarantined(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	// Returned status is ACTION_REQUIRED, not ACTIVE
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "ACTION_REQUIRED",
		},
	}

	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_nonactive_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.active",
			Environment: "sandbox",
			RawPayload:  json.RawMessage(`{"id":"evt_nonactive_1","name":"account.active"}`),
			Status:      postgres.WebhookStatusPending,
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	if app.Status() == domain.ApplicationStatusCompleted {
		t.Fatalf("prohibited completion: application completed despite platform status ACTION_REQUIRED")
	}
	if repo.processedEvents["evt_nonactive_1"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected event to be QUARANTINED, got %s", repo.processedEvents["evt_nonactive_1"])
	}
}

func TestWebhookProcessor_MissingPlatformClient_FailsClosed(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	// Missing platform client: MUST fail closed!
	processor := webhooks.NewProcessor(repo, nil)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_noclient_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.active",
			Environment: "sandbox",
			RawPayload:  json.RawMessage(`{"id":"evt_noclient_1","name":"account.active"}`),
			Status:      postgres.WebhookStatusPending,
		},
	}

	_, _ = processor.ProcessPending(context.Background())

	// Inbox item must be marked FAILED
	if repo.processedEvents["evt_noclient_1"] != postgres.WebhookStatusFailed {
		t.Errorf("expected event to be marked FAILED on missing client, got %s", repo.processedEvents["evt_noclient_1"])
	}
	// Application must remain untouched
	if app.Status() != domain.ApplicationStatusUnderReview {
		t.Errorf("prohibited application mutation when platform client is nil: got %s", app.Status())
	}
}

func TestWebhookProcessor_AccountSuspended_FreshReadActiveDoesNotSuspend(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	// Authoritative fresh read indicates ACTIVE (stale suspended event received)
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_plat_1",
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_stale_susp_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.suspended",
			Environment: "sandbox",
			RawPayload:  json.RawMessage(`{"id":"evt_stale_susp_1","name":"account.suspended"}`),
			Status:      postgres.WebhookStatusPending,
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	// Must NOT suspend or flag operator review on stale event!
	if app.OperatorReviewFlagged() {
		t.Errorf("prohibited flagging operator review on stale account.suspended event when platform is ACTIVE")
	}
	if repo.processedEvents["evt_stale_susp_1"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected stale suspended event to be QUARANTINED, got %s", repo.processedEvents["evt_stale_susp_1"])
	}
}

func TestWebhookProcessor_EmptyAuthoritativeAccountID_Quarantined(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "acct_plat_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	// Returned account ID from platform is empty!
	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "",
			Status: "ACTIVE",
		},
	}
	processor := webhooks.NewProcessor(repo, client)

	repo.claimedItems = []postgres.WebhookInboxItem{
		{
			ID:          1,
			EventID:     "evt_empty_id_1",
			AccountID:   "acct_plat_1",
			EventType:   "account.active",
			Environment: "sandbox",
			RawPayload:  json.RawMessage(`{"id":"evt_empty_id_1","name":"account.active"}`),
			Status:      postgres.WebhookStatusPending,
		},
	}

	_, err := processor.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending failed: %v", err)
	}

	if app.Status() == domain.ApplicationStatusCompleted {
		t.Fatalf("prohibited completion: application completed despite platform returning empty account ID")
	}
	if repo.processedEvents["evt_empty_id_1"] != postgres.WebhookStatusQuarantined {
		t.Errorf("expected empty account ID event to be QUARANTINED, got %s", repo.processedEvents["evt_empty_id_1"])
	}
}

func TestReconciler_ReconcilesAccountsAuthoritatively(t *testing.T) {
	repo := newMockRepository()
	app := createTestApplication(t, "app_1", "comp_1", "pa_1", "sandbox", domain.ApplicationStatusUnderReview)
	repo.applications["app_1"] = app

	pa, err := domain.NewPlatformAccount("pa_1", "comp_1", "AIRWALLEX", "sandbox")
	if err != nil {
		t.Fatalf("failed to create platform account: %v", err)
	}
	_ = pa.SetExternalAccountID("acct_ext_1")
	repo.platformAccounts["pa_1"] = pa

	client := &MockPlatformClient{
		accountDetails: &airwallex.AccountDetailsResponse{
			ID:     "acct_ext_1",
			Status: "ACTIVE",
		},
	}

	processor := webhooks.NewProcessor(repo, client)

	count, err := processor.ReconcileAccounts(context.Background())
	if err != nil {
		t.Fatalf("ReconcileAccounts failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 reconciled account, got %d", count)
	}

	// Verify application became COMPLETED via periodic reconciliation without webhook!
	if app.Status() != domain.ApplicationStatusCompleted {
		t.Errorf("expected application to be COMPLETED, got %s", app.Status())
	}
	if pa.Status() != domain.PlatformAccountStatusActive {
		t.Errorf("expected platform account to be ACTIVE, got %s", pa.Status())
	}
}
