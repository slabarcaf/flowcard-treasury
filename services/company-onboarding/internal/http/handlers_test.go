package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	internalhttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
)

// Safe test database connection resolution identical to repository tests.
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
		t.Fatalf("SAFETY CHECK FAILED: port %d is not 54329", cfg.ConnConfig.Port)
	}
	host := cfg.ConnConfig.Host
	if host != "127.0.0.1" && host != "localhost" {
		t.Fatalf("SAFETY CHECK FAILED: host %q is not local", host)
	}
	if strings.ToLower(cfg.ConnConfig.Database) != "flowcard_onboarding_test" {
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

func setupTestServer(t *testing.T) (*postgres.Repository, http.Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, _ := getTestDatabaseConfig(t)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test db: %v", err)
	}

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Clean test DB
	cleanTestDB(t, pool)

	t.Cleanup(func() {
		pool.Close()
	})

	repo := postgres.NewRepository(pool)
	auth := fakeTestAuthenticator{expectedToken: "valid_internal_test_token"}
	gate := fakeVerifiedPlatformGate{}
	store := storage.NewMemoryStore()
	server := internalhttp.NewServer(repo, auth, gate, store)
	return repo, server.Routes()
}

func setupTestServerWithStore(t *testing.T, store storage.ObjectStore) (*postgres.Repository, http.Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, _ := getTestDatabaseConfig(t)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test db: %v", err)
	}

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to run migrations: %v", err)
	}

	cleanTestDB(t, pool)

	t.Cleanup(func() {
		pool.Close()
	})

	repo := postgres.NewRepository(pool)
	auth := fakeTestAuthenticator{expectedToken: "valid_internal_test_token"}
	gate := fakeVerifiedPlatformGate{}
	server := internalhttp.NewServer(repo, auth, gate, store)
	return repo, server.Routes()
}

type fakeTestAuthenticator struct {
	expectedToken string
}

func (f fakeTestAuthenticator) Authenticate(r *http.Request) (string, error) {
	token := r.Header.Get("X-Internal-Token")
	if token == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if token == "" || token != f.expectedToken {
		return "", internalhttp.ErrUnauthenticated
	}
	actor := r.Header.Get("X-Actor-Id")
	if actor == "" {
		actor = "test_operator"
	}
	return actor, nil
}

type fakeVerifiedPlatformGate struct{}

func (fakeVerifiedPlatformGate) ValidatePlatformContract(comp *domain.Company, app *domain.OnboardingApplication) error {
	return nil
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
		t.Fatalf("SAFETY CHECK: current database is %q, refusing truncate", currentDB)
	}

	tables := []string{
		"onboarding.idempotency_keys",
		"onboarding.outbox_messages",
		"onboarding.submissions",
		"onboarding.applications",
		"onboarding.platform_accounts",
		"onboarding.companies",
	}
	for _, tbl := range tables {
		_, _ = pool.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", tbl))
	}
}

func newAuthRequest(method, url string, body any) *http.Request {
	var bodyReader io.Reader
	if body != nil {
		switch v := body.(type) {
		case io.Reader:
			bodyReader = v
		case []byte:
			bodyReader = bytes.NewReader(v)
		case string:
			bodyReader = strings.NewReader(v)
		default:
			b, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(b)
		}
	}
	req := httptest.NewRequest(method, url, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "valid_internal_test_token")
	req.Header.Set("X-Actor-Id", "operator_jane")
	return req
}

// 1. Auth Gating: Public health endpoints vs. Private/auth-gated internal API endpoints
func TestAuthGating(t *testing.T) {
	repo, handler := setupTestServer(t)

	// Public health endpoints must succeed without auth headers
	for _, path := range []string{"/healthz", "/livez"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, rec.Code)
		}
	}

	// Private internal API endpoints must return 401 UNAUTHENTICATED without auth headers
	apiEndpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/companies"},
		{"POST", "/api/companies"},
		{"GET", "/api/onboarding-applications"},
		{"POST", "/api/onboarding-applications"},
	}

	for _, ep := range apiEndpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401 UNAUTHENTICATED", ep.method, ep.path, rec.Code)
		}

		var errResp internalhttp.ErrorResponse
		_ = json.NewDecoder(rec.Body).Decode(&errResp)
		if errResp.Code != "UNAUTHENTICATED" {
			t.Errorf("%s %s error code = %q, want UNAUTHENTICATED", ep.method, ep.path, errResp.Code)
		}
	}

	// Calling with invalid token must be rejected with 401
	req := httptest.NewRequest("GET", "/api/companies", nil)
	req.Header.Set("X-Internal-Token", "invalid_random_token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET with invalid token status = %d, want 401", rec.Code)
	}

	// Calling with valid X-Internal-Token and authorized actor succeeds
	req = httptest.NewRequest("GET", "/api/companies", nil)
	req.Header.Set("X-Internal-Token", "valid_internal_test_token")
	req.Header.Set("X-Actor-Id", "operator_jane")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/companies with valid token status = %d, want 200", rec.Code)
	}

	// Deny-by-default: server with nil authenticator defaults to DenyAllAuthenticator and denies all access
	defaultDenyServer := internalhttp.NewServer(repo, nil, nil, nil)
	defaultDenyHandler := defaultDenyServer.Routes()
	req = httptest.NewRequest("GET", "/api/companies", nil)
	req.Header.Set("X-Internal-Token", "valid_internal_test_token")
	req.Header.Set("X-Actor-Id", "operator_jane")
	rec = httptest.NewRecorder()
	defaultDenyHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("default deny-all auth status = %d, want 401", rec.Code)
	}

	// Explicit DenyAllAuthenticator denies all private access regardless of headers
	denyAllServer := internalhttp.NewServer(repo, internalhttp.DenyAllAuthenticator{}, nil, nil)
	denyAllHandler := denyAllServer.Routes()
	for _, h := range []struct {
		header string
		value  string
	}{
		{"Authorization", "Bearer arbitrary_token_12345"},
		{"X-Internal-Token", "valid_internal_test_token"},
		{"X-Actor-Id", "malicious_spoofed_operator"},
		{"Cookie", "kit5_session=some_session_cookie"},
	} {
		req = httptest.NewRequest("GET", "/api/companies", nil)
		req.Header.Set(h.header, h.value)
		rec = httptest.NewRecorder()
		denyAllHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("explicit DenyAllAuthenticator with %s status = %d, want 401", h.header, rec.Code)
		}
	}
}

// 2. Request Body Limits, Malformed Input, and Unknown Fields Rejection
func TestRequestBodyLimitsAndUnknownFields(t *testing.T) {
	_, handler := setupTestServer(t)

	// A. Oversized payload (> 1 MiB) must be rejected with 400
	oversizedBody := strings.Repeat("A", 1024*1024+10)
	req := newAuthRequest("POST", "/api/companies", bytes.NewReader([]byte(`{"legal_name":"`+oversizedBody+`"}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body status = %d, want 400", rec.Code)
	}

	// B. Unknown fields injection (e.g. attempting to inject status or revision) must be rejected with 400
	injectedPayload := map[string]any{
		"legal_name":            "Acme Legal Corp",
		"status":                "COMPLETED", // Forbidden injected persistence field
		"revision":              99,          // Forbidden injected persistence field
		"has_submitted_history": true,        // Forbidden injected persistence field
	}
	req = newAuthRequest("POST", "/api/companies", injectedPayload)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("injected unknown fields status = %d, want 400 BAD_REQUEST", rec.Code)
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "MALFORMED_INPUT" {
		t.Errorf("error code = %q, want MALFORMED_INPUT", errResp.Code)
	}

	// C. Malformed JSON returns 400
	req = newAuthRequest("POST", "/api/companies", "{not valid json}")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON status = %d, want 400", rec.Code)
	}
}

// 3. Company CRUD, Eligible Deletion vs Ineligible Deletion, and Partial Edits
func TestCompanyCRUDAndPartialEdits(t *testing.T) {
	_, handler := setupTestServer(t)

	// A. Create Company
	createReq := internalhttp.CreateCompanyRequest{
		LegalName:           "Acme Technologies Inc.",
		TradingName:         "Acme Tech",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationState:   "DE",
		BusinessProfile: &internalhttp.BusinessProfileDTO{
			Website:     "https://acme-tech.example.com",
			Description: "Enterprise cloud treasury",
			RegisteredAddress: &internalhttp.AddressDTO{
				Line1:      "123 Market St",
				City:       "Wilmington",
				State:      "DE",
				PostalCode: "19801",
				Country:    "US",
			},
		},
		RegistrationIdentifiers: []internalhttp.RegistrationIdentifierDTO{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		People: []internalhttp.PersonDTO{
			{
				FirstName:   "Alice",
				LastName:    "Smith",
				Role:        "Director",
				DateOfBirth: "1982-03-24",
				Identifications: []internalhttp.IdentificationDTO{
					{
						Type:           "PASSPORT",
						Number:         "P98765432",
						IssuingCountry: "US",
					},
				},
			},
		},
	}

	req := newAuthRequest("POST", "/api/companies", createReq)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/companies status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}

	var created internalhttp.CompanyResponse
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if created.ID == "" || created.LegalName != "Acme Technologies Inc." {
		t.Fatalf("invalid created company: %+v", created)
	}
	compID := created.ID

	// B. Fetch Company
	req = newAuthRequest("GET", "/api/companies/"+compID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/companies/%s status = %d, want 200", compID, rec.Code)
	}

	// C. Partial Edit (PATCH) allows partial fields without zeroing omitted fields
	newTradingName := "Acme Global Treasury"
	editReq := internalhttp.EditCompanyRequest{
		ExpectedRevision: created.Revision,
		TradingName:      &newTradingName,
		// LegalName, EntityType, BusinessProfile, People are omitted!
	}
	req = newAuthRequest("PATCH", "/api/companies/"+compID, editReq)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/companies/%s status = %d, want 200; body: %s", compID, rec.Code, rec.Body.String())
	}

	var edited internalhttp.CompanyResponse
	_ = json.NewDecoder(rec.Body).Decode(&edited)
	if edited.TradingName != newTradingName {
		t.Errorf("trading name = %q, want %q", edited.TradingName, newTradingName)
	}
	// Verify omitted fields were NOT zeroed out
	if edited.LegalName != "Acme Technologies Inc." {
		t.Errorf("legal name was zeroed out: %q", edited.LegalName)
	}
	if edited.BusinessProfile.Website != "https://acme-tech.example.com" {
		t.Errorf("website was zeroed out: %q", edited.BusinessProfile.Website)
	}
	if len(edited.People) != 1 {
		t.Errorf("people collection was wiped: len = %d", len(edited.People))
	}

	// D. Concurrent revision conflict returns 409
	req = newAuthRequest("PATCH", "/api/companies/"+compID, internalhttp.EditCompanyRequest{
		ExpectedRevision: 1, // Stale revision
		TradingName:      &newTradingName,
	})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("stale revision status = %d, want 409 CONFLICT", rec.Code)
	}

	// E. Eligible Deletion of unsubmitted company
	req = newAuthRequest("DELETE", "/api/companies/"+compID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/companies/%s status = %d, want 204", compID, rec.Code)
	}

	// Verify company is gone
	req = newAuthRequest("GET", "/api/companies/"+compID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET deleted company status = %d, want 404", rec.Code)
	}
}

// 4. Application Live vs. Frozen Company Resolution
func TestApplicationLiveVsFrozenCompanyResolution(t *testing.T) {
	repo, handler := setupTestServer(t)
	ctx := context.Background()

	// Create Company
	compID := fmt.Sprintf("comp_res_%d", time.Now().UnixNano())
	comp, _ := domain.NewDraftCompany(compID, "Original Legal Name Corp")
	_ = comp.UpdateTradingName("Initial Trading Name")
	_ = comp.AddPerson(domain.Person{
		ID:          "p1",
		FirstName:   "Alice",
		LastName:    "Smith",
		Role:        "Director",
		DateOfBirth: "1980-01-01",
	})
	_ = repo.CreateCompany(ctx, comp)

	// Create Application
	createAppReq := internalhttp.CreateApplicationRequest{
		CompanyID:    compID,
		PlatformName: "AIRWALLEX",
		Environment:  "sandbox",
		OnboardingDetails: &internalhttp.PlatformOnboardingDetailsDTO{
			PrimaryContactPersonID: "p1",
			CustomerAgreements: internalhttp.CustomerAgreementsDTO{
				AgreedToTermsAndConditions: true,
				AgreedToDataUsage:          true,
				TermsAndConditions: &internalhttp.TermsAgreementDataDTO{
					AgreedAt:             time.Now().UTC(),
					ServiceAgreementType: "FULL",
				},
			},
		},
	}
	req := newAuthRequest("POST", "/api/onboarding-applications", createAppReq)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/onboarding-applications status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var createdApp internalhttp.ApplicationResponse
	_ = json.NewDecoder(rec.Body).Decode(&createdApp)
	appID := createdApp.ID

	// While application is in DRAFT: edit live company trading name
	newDraftTradingName := "Live Amended Trading Name While Draft"
	revBefore := comp.Revision()
	if err := comp.UpdateTradingName(newDraftTradingName); err != nil {
		t.Fatalf("UpdateTradingName failed: %v", err)
	}
	if err := repo.UpdateCompany(ctx, comp, revBefore); err != nil {
		t.Fatalf("UpdateCompany failed: %v", err)
	}

	// GET application must resolve the live updated company!
	req = newAuthRequest("GET", "/api/onboarding-applications/"+appID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/onboarding-applications/%s status = %d, want 200", appID, rec.Code)
	}
	var draftFetchedApp internalhttp.ApplicationResponse
	_ = json.NewDecoder(rec.Body).Decode(&draftFetchedApp)
	if draftFetchedApp.Company.TradingName != newDraftTradingName {
		t.Errorf("draft application GET company trading name = %q, want %q (live resolved!)",
			draftFetchedApp.Company.TradingName, newDraftTradingName)
	}

	// Now initiate submission on the application (advances to SUBMISSION_PENDING)
	subID := fmt.Sprintf("sub_res_%d", time.Now().UnixNano())
	subParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: createdApp.Revision,
		SubmissionID:        subID,
		SubmittingActor:     "ops_user",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "app_sub",
		IdempotencyKey:      fmt.Sprintf("key_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_res",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	_, err := repo.InitiateSubmission(ctx, subParams)
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	// Edit live company again in persistence (revision advances, new trading name)
	afterSubTradingName := "Post Submission Live Edit Name"
	compFromDB, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	revPostSub := compFromDB.Revision()
	if err := compFromDB.UpdateTradingName(afterSubTradingName); err != nil {
		t.Fatalf("UpdateTradingName failed: %v", err)
	}
	if err := repo.UpdateCompany(ctx, compFromDB, revPostSub); err != nil {
		t.Fatalf("UpdateCompany post-sub failed: %v", err)
	}

	// GET application must now return the FROZEN company copy (not the post-submission live edit!)
	req = newAuthRequest("GET", "/api/onboarding-applications/"+appID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/onboarding-applications/%s post-submit status = %d, want 200", appID, rec.Code)
	}
	var submittedFetchedApp internalhttp.ApplicationResponse
	_ = json.NewDecoder(rec.Body).Decode(&submittedFetchedApp)

	if submittedFetchedApp.Status != "SUBMISSION_PENDING" {
		t.Errorf("status = %q, want SUBMISSION_PENDING", submittedFetchedApp.Status)
	}
	if submittedFetchedApp.Company.TradingName != newDraftTradingName {
		t.Errorf("submitted application GET company trading name = %q, want %q (frozen snapshot preserved!)",
			submittedFetchedApp.Company.TradingName, newDraftTradingName)
	}
}

// 5. Saved-data POST verify (200 VALIDATION_PASSED vs. 422 VALIDATION_FAILED with field paths)
func TestVerifyApplicationEndpoint(t *testing.T) {
	repo, handler := setupTestServer(t)
	ctx := context.Background()

	// A. Incomplete Company & Application returns 422 VALIDATION_FAILED
	compID := fmt.Sprintf("comp_inc_%d", time.Now().UnixNano())
	comp, _ := domain.NewDraftCompany(compID, "Incomplete Corp")
	_ = repo.CreateCompany(ctx, comp)

	appID := fmt.Sprintf("app_inc_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, comp, "creator")
	_ = repo.CreateApplication(ctx, app)

	req := newAuthRequest("POST", "/api/onboarding-applications/"+appID+"/verify", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /verify on incomplete app status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	var failResp validation.ValidationResult
	_ = json.NewDecoder(rec.Body).Decode(&failResp)
	if failResp.Code != "VALIDATION_FAILED" {
		t.Errorf("failResp = %+v, want VALIDATION_FAILED", failResp)
	}
	if len(failResp.Errors) == 0 {
		t.Fatalf("expected exhaustive field errors, got 0")
	}
	if strings.Contains(rec.Body.String(), `"passed"`) {
		t.Errorf("wire format contains forbidden 'passed' boolean: %s", rec.Body.String())
	}
	for _, fe := range failResp.Errors {
		if strings.HasPrefix(fe.Field, "application.onboarding_details") {
			t.Errorf("error path uses forbidden 'application.onboarding_details': %s", fe.Field)
		}
		if strings.HasPrefix(fe.Field, "application.documents") {
			t.Errorf("error path uses forbidden 'application.documents': %s", fe.Field)
		}
	}

	// B. Complete Company & Application returns 200 VALIDATION_PASSED
	compReadyID := fmt.Sprintf("comp_ready_%d", time.Now().UnixNano())
	compReady, _ := domain.NewDraftCompany(compReadyID, "Ready Treasury Corp")
	_ = compReady.UpdateTradingName("Ready Treasury")
	_ = compReady.UpdateProfile(domain.BusinessProfile{
		Website:     "https://ready-treasury.example.com",
		Description: "Complete enterprise bridge financing and treasury infrastructure",
		Products:    "Complete enterprise bridge financing and treasury infrastructure",
		RegisteredAddress: domain.Address{
			Line1:      "500 Pine St",
			City:       "Wilmington",
			State:      "DE",
			PostalCode: "19801",
			Country:    "US",
		},
	})
	_ = compReady.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
		LegalName:           "Ready Treasury Corp",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationState:   "DE",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
	})
	_ = compReady.AddPerson(domain.Person{
		ID:          "person_ceo",
		FirstName:   "Carol",
		LastName:    "Danvers",
		Email:       "carol.danvers@example.com",
		Phone:       "+15551234567",
		Role:        "CEO",
		DateOfBirth: domain.Date("1980-01-01"),
		Identifications: []domain.Identification{
			{
				ID:             "id_passport",
				Type:           "PASSPORT",
				Number:         "P11223344",
				IssuingCountry: "US",
			},
		},
	})
	_ = repo.CreateCompany(ctx, compReady)

	appReadyID := fmt.Sprintf("app_ready_%d", time.Now().UnixNano())
	appReady, _ := domain.NewDraftApplication(appReadyID, compReady, "compliance_admin")
	nowUTC := time.Now().UTC()
	_ = appReady.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		PrimaryContactPersonID: "person_ceo",
		IndustryCategoryCode:   "saas_software",
		IntendedProducts:       []string{"commercial_card"},
		EstimatedRevenue: domain.RevenueEstimate{
			Amount:   "250000",
			Currency: "USD",
		},
		People: []domain.PersonPlatformDetails{
			{
				PersonID:      "person_ceo",
				PlatformRoles: []string{"DIRECTOR"},
				ResidentialAddress: domain.Address{
					Line1:      "100 Sunset Blvd",
					City:       "Wilmington",
					State:      "DE",
					PostalCode: "19801",
					Country:    "US",
				},
			},
		},
		CustomerAgreements: domain.CustomerAgreements{
			AgreedToTermsAndConditions: true,
			AgreedToDataUsage:          true,
			TermsAndConditions: &domain.TermsAndConditionsAgreement{
				AgreedAt:             nowUTC,
				ServiceAgreementType: "FULL",
			},
		},
	})
	_ = appReady.AttachDocument(domain.Document{
		ID:          "doc_b1",
		Type:        "BUSINESS_REGISTRATION",
		FileName:    "de_cert.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		StorageKey:  "keys/de_cert.pdf",
		UploadedBy:  "compliance_admin",
		UploadedAt:  nowUTC,
	})
	_ = appReady.AttachDocument(domain.Document{
		ID:               "doc_p1",
		Type:             "IDENTITY_DOCUMENT",
		PersonID:         "person_ceo",
		IdentificationID: "id_passport",
		FileName:         "pass.png",
		ContentType:      "image/png",
		SizeBytes:        2048,
		StorageKey:       "keys/pass.png",
		UploadedBy:       "compliance_admin",
		UploadedAt:       nowUTC,
	})
	_ = repo.CreateApplication(ctx, appReady)

	// B. Complete-looking app against default gate (UnverifiedPlatformContractGate) MUST return 422 PLATFORM_CONTRACT_INCOMPLETE
	defaultGateServer := internalhttp.NewServer(repo, fakeTestAuthenticator{expectedToken: "valid_internal_test_token"}, nil, nil)
	defaultGateHandler := defaultGateServer.Routes()
	req = newAuthRequest("POST", "/api/onboarding-applications/"+appReadyID+"/verify", nil)
	rec = httptest.NewRecorder()
	defaultGateHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /verify with default gate status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	var gateResp validation.ValidationResult
	_ = json.NewDecoder(rec.Body).Decode(&gateResp)
	if gateResp.Code != "PLATFORM_CONTRACT_INCOMPLETE" {
		t.Errorf("gateResp.Code = %q, want PLATFORM_CONTRACT_INCOMPLETE", gateResp.Code)
	}
	if len(gateResp.Errors) != 0 {
		t.Errorf("expected 0 local field errors for complete data under default gate, got %d", len(gateResp.Errors))
	}
	if strings.Contains(rec.Body.String(), `"passed"`) {
		t.Errorf("wire format contains forbidden 'passed' property: %s", rec.Body.String())
	}

	// C. Complete-looking app with verified gate stub returns 200 VALIDATION_PASSED
	req = newAuthRequest("POST", "/api/onboarding-applications/"+appReadyID+"/verify", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /verify on complete app with verified gate status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var passResp validation.ValidationResult
	_ = json.NewDecoder(rec.Body).Decode(&passResp)
	if passResp.Code != "VALIDATION_PASSED" {
		t.Errorf("passResp = %+v, want VALIDATION_PASSED", passResp)
	}
	if passResp.ApplicationRevision != appReady.Revision() {
		t.Errorf("ApplicationRevision = %d, want %d", passResp.ApplicationRevision, appReady.Revision())
	}
	if strings.Contains(rec.Body.String(), `"passed"`) {
		t.Errorf("wire format contains forbidden 'passed' property: %s", rec.Body.String())
	}
}

// 6. Submit requires an idempotency key before resource processing.
func TestSubmitApplicationRequiresIdempotencyKey(t *testing.T) {
	_, handler := setupTestServer(t)

	req := newAuthRequest("POST", "/api/onboarding-applications/app_dummy/submit", map[string]any{
		"expected_revision": 1,
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /submit without Idempotency-Key status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Errorf("error code = %q, want IDEMPOTENCY_KEY_REQUIRED", errResp.Code)
	}
}

func TestSubmitApplicationDefaultGateRejectsWithoutQueueing(t *testing.T) {
	repo, _ := setupTestServer(t)
	ctx := context.Background()
	company, err := domain.NewDraftCompany(fmt.Sprintf("comp_gate_%d", time.Now().UnixNano()), "Readiness Gate Company")
	if err != nil {
		t.Fatal(err)
	}
	if err := company.UpdateProfile(domain.BusinessProfile{Website: "https://example.test", Description: "Payments", Products: "Treasury software", RegisteredAddress: domain.Address{Line1: "1 Main", City: "Wilmington", State: "DE", PostalCode: "19801", Country: "US"}}); err != nil {
		t.Fatal(err)
	}
	if err := company.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{LegalName: "Readiness Gate Company", EntityType: "CORPORATION", RegistrationCountry: "US", RegistrationState: "DE", RegistrationIdentifiers: []domain.RegistrationIdentifier{{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"}}}); err != nil {
		t.Fatal(err)
	}
	if err := company.AddPerson(domain.Person{ID: "person_gate", FirstName: "Synthetic", LastName: "Operator", Email: "operator@example.test", Phone: "+15555550100", Role: "Director", DateOfBirth: domain.Date("1985-01-01"), Identifications: []domain.Identification{{ID: "ident_gate", Type: "PASSPORT", Number: "P123456", IssuingCountry: "US"}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateCompany(ctx, company); err != nil {
		t.Fatal(err)
	}
	app, err := domain.NewDraftApplication(fmt.Sprintf("app_gate_%d", time.Now().UnixNano()), company, "creator")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{PrimaryContactPersonID: "person_gate", IndustryCategoryCode: "software_saas", IntendedProducts: []string{"card_issuing"}, EstimatedRevenue: domain.RevenueEstimate{Amount: "100000", Currency: "USD"}, People: []domain.PersonPlatformDetails{{PersonID: "person_gate", PlatformRoles: []string{"DIRECTOR"}, ResidentialAddress: domain.Address{Line1: "1 Main", City: "Wilmington", State: "DE", PostalCode: "19801", Country: "US"}}}, CustomerAgreements: domain.CustomerAgreements{AgreedToTermsAndConditions: true, AgreedToDataUsage: true, TermsAndConditions: &domain.TermsAndConditionsAgreement{AgreedAt: time.Now().UTC(), ServiceAgreementType: "FULL"}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateApplication(ctx, app); err != nil {
		t.Fatal(err)
	}

	// A repository whose readiness callback models the production default gate.
	defaultGate := internalhttp.NewServer(repo, fakeTestAuthenticator{expectedToken: "valid_internal_test_token"}, validation.UnverifiedPlatformContractGate{}, nil).Routes()
	req := newAuthRequest("POST", "/api/onboarding-applications/"+app.ID()+"/submit", map[string]any{"expected_revision": app.Revision()})
	req.Header.Set("Idempotency-Key", fmt.Sprintf("gate_%d", time.Now().UnixNano()))
	rec := httptest.NewRecorder()
	defaultGate.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "PLATFORM_CONTRACT_INCOMPLETE") {
		t.Fatalf("submit with default gate status/body = %d %s, want 422 PLATFORM_CONTRACT_INCOMPLETE", rec.Code, rec.Body.String())
	}
	latest, err := repo.GetApplication(ctx, app.ID())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status() != domain.ApplicationStatusDraft {
		t.Fatalf("status = %s, want DRAFT", latest.Status())
	}
	if submissions, err := repo.ListSubmissions(ctx, app.ID()); err != nil || len(submissions) != 0 {
		t.Fatalf("submissions = %d, err=%v, want none", len(submissions), err)
	}
}

func TestDocumentUploadDefaultStorageReturnsExplicitUnavailable(t *testing.T) {
	_, handler := setupTestServerWithStore(t, storage.NewUnavailableStore("D1 storage intentionally unconfigured"))
	companyReq := newAuthRequest("POST", "/api/companies", internalhttp.CreateCompanyRequest{LegalName: "Upload Gate Company"})
	companyRec := httptest.NewRecorder()
	handler.ServeHTTP(companyRec, companyReq)
	if companyRec.Code != http.StatusCreated {
		t.Fatalf("create company status=%d body=%s", companyRec.Code, companyRec.Body.String())
	}
	var company internalhttp.CompanyResponse
	if err := json.Unmarshal(companyRec.Body.Bytes(), &company); err != nil {
		t.Fatal(err)
	}
	appReq := newAuthRequest("POST", "/api/onboarding-applications", internalhttp.CreateApplicationRequest{CompanyID: company.ID, PlatformName: "AIRWALLEX", Environment: "sandbox"})
	appRec := httptest.NewRecorder()
	handler.ServeHTTP(appRec, appReq)
	if appRec.Code != http.StatusCreated {
		t.Fatalf("create application status=%d body=%s", appRec.Code, appRec.Body.String())
	}
	var app internalhttp.ApplicationResponse
	if err := json.Unmarshal(appRec.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("expected_revision", fmt.Sprint(app.Revision))
	_ = writer.WriteField("type", "BUSINESS_DOCUMENT")
	part, err := writer.CreateFormFile("file", "synthetic.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("%PDF-1.4 synthetic document"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadReq := newAuthRequest("POST", "/api/onboarding-applications/"+app.ID+"/documents", nil)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	uploadRec := httptest.NewRecorder()
	handler.ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusServiceUnavailable || !strings.Contains(uploadRec.Body.String(), "STORAGE_UNAVAILABLE") {
		t.Fatalf("upload status/body=%d %s, want explicit 503 STORAGE_UNAVAILABLE", uploadRec.Code, uploadRec.Body.String())
	}
}

// 7. Cursor Pagination, Filters, and Malformed Cursor Handling
func TestPaginationAndFilters(t *testing.T) {
	_, handler := setupTestServer(t)

	// Create 4 companies via HTTP API
	for i := 0; i < 4; i++ {
		req := newAuthRequest("POST", "/api/companies", internalhttp.CreateCompanyRequest{
			LegalName:           fmt.Sprintf("Page Corp %d", i),
			EntityType:          "LLC",
			RegistrationCountry: "US",
		})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create company %d failed: %s", i, rec.Body.String())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Fetch page 1 (limit 2)
	req := newAuthRequest("GET", "/api/companies?limit=2", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/companies?limit=2 status = %d, want 200", rec.Code)
	}
	var page1 internalhttp.CompanyListResponse
	_ = json.NewDecoder(rec.Body).Decode(&page1)
	if len(page1.Companies) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(page1.Companies))
	}
	if page1.NextCursor == "" {
		t.Fatalf("page 1 NextCursor is empty")
	}

	// Fetch page 2 using cursor
	req = newAuthRequest("GET", "/api/companies?limit=2&cursor="+page1.NextCursor, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/companies page 2 status = %d, want 200", rec.Code)
	}
	var page2 internalhttp.CompanyListResponse
	_ = json.NewDecoder(rec.Body).Decode(&page2)
	if len(page2.Companies) != 2 {
		t.Fatalf("page 2 len = %d, want 2", len(page2.Companies))
	}

	// Tampered cursor must return 400 MALFORMED_CURSOR
	req = newAuthRequest("GET", "/api/companies?cursor=invalid_base64_tampered_cursor", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("tampered cursor status = %d, want 400", rec.Code)
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "MALFORMED_CURSOR" {
		t.Errorf("error code = %q, want MALFORMED_CURSOR", errResp.Code)
	}
}

// 8. Application Cancellation and Incompatible State Rejections
func TestApplicationCancellation(t *testing.T) {
	repo, handler := setupTestServer(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_canc_%d", time.Now().UnixNano())
	comp, _ := domain.NewDraftCompany(compID, "Cancel Corp")
	_ = repo.CreateCompany(ctx, comp)

	appID := fmt.Sprintf("app_canc_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, comp, "creator")
	_ = repo.CreateApplication(ctx, app)

	// Cancel draft application returns 200
	cancelReq := internalhttp.CancelApplicationRequest{
		ExpectedRevision: 1,
		Reason:           "User requested cancellation",
	}
	req := newAuthRequest("POST", "/api/onboarding-applications/"+appID+"/cancel", cancelReq)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /cancel status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Verify application is cancelled
	reloaded, _ := repo.GetApplication(ctx, appID)
	if reloaded.Status() != domain.ApplicationStatusCancelled {
		t.Errorf("status = %q, want CANCELLED", reloaded.Status())
	}
}

// 9. Submissions and Platform Account Read Endpoints
func TestSubmissionsAndPlatformAccountReadEndpoints(t *testing.T) {
	repo, handler := setupTestServer(t)
	ctx := context.Background()

	compID := fmt.Sprintf("comp_read_%d", time.Now().UnixNano())
	comp, _ := domain.NewDraftCompany(compID, "Read Corp")
	_ = repo.CreateCompany(ctx, comp)

	appID := fmt.Sprintf("app_read_%d", time.Now().UnixNano())
	app, _ := domain.NewDraftApplication(appID, comp, "creator")
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		CustomerAgreements: domain.CustomerAgreements{
			AgreedToTermsAndConditions: true,
			AgreedToDataUsage:          true,
			TermsAndConditions: &domain.TermsAndConditionsAgreement{
				AgreedAt:             time.Now().UTC(),
				ServiceAgreementType: "FULL",
			},
		},
	})
	_ = repo.CreateApplication(ctx, app)

	subID := fmt.Sprintf("sub_read_%d", time.Now().UnixNano())
	subParams := postgres.InitiateSubmissionParams{
		ApplicationID:       appID,
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        subID,
		SubmittingActor:     "actor",
		OutboxTopic:         "onboarding.submission.v1",
		OutboxPayload:       []byte(`{"application_id":"` + appID + `"}`),
		IdempotencyScope:    "app_sub",
		IdempotencyKey:      fmt.Sprintf("key_%d", time.Now().UnixNano()),
		RequestFingerprint:  "fp_read",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{"status":"SUBMISSION_PENDING"}`),
	}
	_, err := repo.InitiateSubmission(ctx, subParams)
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	// GET /api/onboarding-applications/{id}/submissions
	req := newAuthRequest("GET", "/api/onboarding-applications/"+appID+"/submissions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET submissions status = %d, want 200", rec.Code)
	}
	var subsResp []internalhttp.SubmissionResponse
	_ = json.NewDecoder(rec.Body).Decode(&subsResp)
	if len(subsResp) != 1 || subsResp[0].ID != subID {
		t.Errorf("subsResp = %+v, want 1 submission with ID %q", subsResp, subID)
	}

	// GET /api/submissions/{id}
	req = newAuthRequest("GET", "/api/submissions/"+subID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/submissions/%s status = %d, want 200", subID, rec.Code)
	}

	// Create and read platform account
	paID := fmt.Sprintf("pa_read_%d", time.Now().UnixNano())
	pa, _ := domain.NewPlatformAccount(paID, compID, "AIRWALLEX", "sandbox")
	_ = repo.CreatePlatformAccount(ctx, pa)

	req = newAuthRequest("GET", "/api/platform-accounts/"+paID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/platform-accounts/%s status = %d, want 200", paID, rec.Code)
	}

	req = newAuthRequest("GET", "/api/companies/"+compID+"/platform-accounts", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/companies/%s/platform-accounts status = %d, want 200", compID, rec.Code)
	}
}
