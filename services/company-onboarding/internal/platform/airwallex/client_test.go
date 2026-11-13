package airwallex_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
)

func TestNewClient_Validation(t *testing.T) {
	t.Parallel()

	// Missing credentials
	_, err := airwallex.NewClient(airwallex.ClientConfig{})
	if !errors.Is(err, airwallex.ErrUnconfigured) {
		t.Fatalf("expected ErrUnconfigured for empty config, got %v", err)
	}

	// Remote non-HTTPS rejected
	_, err = airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  "http://api.airwallex.com",
		ClientID: "cid",
		APIKey:   "key",
	})
	if err == nil || !strings.Contains(err.Error(), "strict HTTPS required") {
		t.Fatalf("expected strict HTTPS error for remote base URL, got %v", err)
	}

	// Production endpoint is strictly rejected in demo/sandbox environment
	_, err = airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:             "https://api.airwallex.com",
		ClientID:            "cid",
		APIKey:              "key",
		PlatformEnvironment: "sandbox",
	})
	if err == nil || !strings.Contains(err.Error(), "must target sandbox") {
		t.Fatalf("expected sandbox enforcement error for production base URL in demo, got %v", err)
	}

	// In explicit production platform environment, api.airwallex.com is permitted
	prodClient, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:             "https://api.airwallex.com",
		ClientID:            "cid",
		APIKey:              "key",
		PlatformEnvironment: "production",
	})
	if err != nil || prodClient == nil {
		t.Fatalf("expected api.airwallex.com to be permitted in production environment, got %v", err)
	}

	// In explicit production platform environment, sandbox URL is rejected
	_, err = airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:             "https://api.sandbox.airwallex.com",
		ClientID:            "cid",
		APIKey:              "key",
		PlatformEnvironment: "production",
	})
	if err == nil || !strings.Contains(err.Error(), "must target api.airwallex.com") {
		t.Fatalf("expected error for sandbox base URL in production environment, got %v", err)
	}

	// Remote sandbox endpoint is permitted
	sandboxClient, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  "https://api.sandbox.airwallex.com",
		ClientID: "cid",
		APIKey:   "key",
	})
	if err != nil || sandboxClient == nil {
		t.Fatalf("expected api.sandbox.airwallex.com to be permitted, got %v", err)
	}

	// Localhost HTTP permitted
	client, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  "http://127.0.0.1:8080",
		ClientID: "cid",
		APIKey:   "key",
	})
	if err != nil {
		t.Fatalf("expected localhost HTTP to be permitted, got %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	// Redirect policy prevents following redirects
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/other", http.StatusMovedPermanently)
	}))
	defer redirectServer.Close()

	redirectClient, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  redirectServer.URL,
		ClientID: "cid",
		APIKey:   "key",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	_, err = redirectClient.GetAccount(context.Background(), "acct_test")
	if err == nil || !strings.Contains(err.Error(), "redirects strictly prohibited") {
		t.Fatalf("expected redirect prohibited error, got %v", err)
	}
}

func TestClient_AuthenticationAndCaching(t *testing.T) {
	t.Parallel()

	var loginCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/authentication/login":
			atomic.AddInt32(&loginCalls, 1)
			if r.Header.Get("x-client-id") != "test-cid" || r.Header.Get("x-api-key") != "test-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{
				Token:     "test-token-jwt",
				ExpiresAt: time.Now().Add(10 * time.Minute),
			})

		case "/api/v1/accounts/acct_123":
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-token-jwt" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(airwallex.AccountDetailsResponse{
				ID:     "acct_123",
				Status: "ACTIVE",
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  server.URL,
		ClientID: "test-cid",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Concurrent calls should reuse the token and make only 1 login request
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.GetAccount(context.Background(), "acct_123")
			if err != nil {
				t.Errorf("GetAccount failed: %v", err)
				return
			}
			if resp.ID != "acct_123" {
				t.Errorf("got ID %q, want acct_123", resp.ID)
			}
		}()
	}
	wg.Wait()

	if calls := atomic.LoadInt32(&loginCalls); calls != 1 {
		t.Fatalf("expected exactly 1 login call, got %d", calls)
	}
}

func TestClient_CreateAccount_Scenarios(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/authentication/login" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{
					Token:     "jwt",
					ExpiresAt: time.Now().Add(10 * time.Minute),
				})
				return
			}
			if r.URL.Path == "/api/v1/accounts/create" {
				if r.Header.Get("Authorization") != "Bearer jwt" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(airwallex.AccountCreateResponse{
					ID:     "acct_sandbox_test001",
					Status: "CREATED",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client, err := airwallex.NewClient(airwallex.ClientConfig{
			BaseURL:  server.URL,
			ClientID: "cid",
			APIKey:   "key",
		})
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}

		resp, err := client.CreateAccount(context.Background(), airwallex.AccountCreateRequest{})
		if err != nil {
			t.Fatalf("CreateAccount failed: %v", err)
		}
		if resp.ID != "acct_sandbox_test001" || resp.Status != "CREATED" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("400 invalid data", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/authentication/login" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{Token: "jwt"})
				return
			}
			if r.URL.Path == "/api/v1/accounts/create" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(airwallex.APIError{
					Code:    "invalid_argument",
					Message: "EIN format invalid",
					Source:  "business_registration_number",
				})
				return
			}
		}))
		defer server.Close()

		client, _ := airwallex.NewClient(airwallex.ClientConfig{
			BaseURL:  server.URL,
			ClientID: "cid",
			APIKey:   "key",
		})

		_, err := client.CreateAccount(context.Background(), airwallex.AccountCreateRequest{})
		if !errors.Is(err, airwallex.ErrInvalidData) {
			t.Fatalf("expected ErrInvalidData, got %v", err)
		}
		if !strings.Contains(err.Error(), "invalid_argument") {
			t.Fatalf("expected error to contain API code, got %v", err)
		}
	})

	t.Run("429 rate limit with Retry-After", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/authentication/login" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{Token: "jwt"})
				return
			}
			if r.URL.Path == "/api/v1/accounts/create" {
				w.Header().Set("Retry-After", "12")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
		}))
		defer server.Close()

		client, _ := airwallex.NewClient(airwallex.ClientConfig{
			BaseURL:  server.URL,
			ClientID: "cid",
			APIKey:   "key",
		})

		_, err := client.CreateAccount(context.Background(), airwallex.AccountCreateRequest{})
		if !errors.Is(err, airwallex.ErrRateLimited) {
			t.Fatalf("expected ErrRateLimited, got %v", err)
		}
		if !strings.Contains(err.Error(), "12s") {
			t.Fatalf("expected retry after 12s, got %v", err)
		}
	})

	t.Run("500 server error is ambiguous on create", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/authentication/login" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{Token: "jwt"})
				return
			}
			if r.URL.Path == "/api/v1/accounts/create" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}))
		defer server.Close()

		client, _ := airwallex.NewClient(airwallex.ClientConfig{
			BaseURL:  server.URL,
			ClientID: "cid",
			APIKey:   "key",
		})

		_, err := client.CreateAccount(context.Background(), airwallex.AccountCreateRequest{})
		if !errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
			t.Fatalf("expected ErrAmbiguousRemoteOutcome on 500 create, got %v", err)
		}
	})

	t.Run("ambiguous network timeout", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/authentication/login" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{Token: "jwt"})
				return
			}
			if r.URL.Path == "/api/v1/accounts/create" {
				// Simulate slow response exceeding client timeout
				time.Sleep(100 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
				return
			}
		}))
		defer server.Close()

		client, _ := airwallex.NewClient(airwallex.ClientConfig{
			BaseURL:  server.URL,
			ClientID: "cid",
			APIKey:   "key",
			HTTPClient: &http.Client{
				Timeout: 20 * time.Millisecond,
			},
		})

		_, err := client.CreateAccount(context.Background(), airwallex.AccountCreateRequest{})
		if !errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
			t.Fatalf("expected ErrAmbiguousRemoteOutcome on timeout, got %v", err)
		}
	})
}

func TestClient_UploadFile_Scenarios(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/authentication/login" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{Token: "jwt"})
			return
		}
		if r.URL.Path == "/api/v1/files/upload" {
			// Check multipart parsing
			err := r.ParseMultipartForm(10 << 20)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			purpose := r.FormValue("purpose")
			if purpose != "business_registration" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer file.Close()
			if header.Filename != "certificate.pdf" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(airwallex.FileUploadResponse{
				FileID:   "file_sandbox_test001",
				Filename: "certificate.pdf",
				Size:     1024,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  server.URL,
		ClientID: "cid",
		APIKey:   "key",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	resp, err := client.UploadFile(context.Background(), strings.NewReader("fake-pdf-content"), "certificate.pdf", 16, "application/pdf")
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}
	if resp.FileID != "file_sandbox_test001" {
		t.Fatalf("expected file ID file_sandbox_test001, got %q", resp.FileID)
	}
}

func TestClient_SubmitAccount_Scenarios(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/authentication/login" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{Token: "jwt"})
			return
		}
		if r.URL.Path == "/api/v1/accounts/acct_001/submit" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(airwallex.AccountSubmitResponse{
				ID:          "acct_001",
				Status:      "SUBMITTED",
				SubmittedAt: "2026-11-06T10:05:00Z",
			})
			return
		}
		if r.URL.Path == "/api/v1/accounts/acct_timeout/submit" {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  server.URL,
		ClientID: "cid",
		APIKey:   "key",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Success
	resp, err := client.SubmitAccount(context.Background(), "acct_001")
	if err != nil {
		t.Fatalf("SubmitAccount failed: %v", err)
	}
	if resp.Status != "SUBMITTED" {
		t.Fatalf("got status %q, want SUBMITTED", resp.Status)
	}

	// Timeout -> ambiguous outcome
	timeoutClient, _ := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  server.URL,
		ClientID: "cid",
		APIKey:   "key",
		HTTPClient: &http.Client{
			Timeout: 20 * time.Millisecond,
		},
	})
	_, err = timeoutClient.SubmitAccount(context.Background(), "acct_timeout")
	if !errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
		t.Fatalf("expected ErrAmbiguousRemoteOutcome on timeout, got %v", err)
	}
}

func TestUnavailableClient(t *testing.T) {
	t.Parallel()

	u := airwallex.NewUnconfiguredClient("not configured in test")
	if _, err := u.CreateAccount(context.Background(), airwallex.AccountCreateRequest{}); !errors.Is(err, airwallex.ErrUnconfigured) {
		t.Fatalf("expected ErrUnconfigured, got %v", err)
	}
	if _, err := u.UploadFile(context.Background(), nil, "", 0, ""); !errors.Is(err, airwallex.ErrUnconfigured) {
		t.Fatalf("expected ErrUnconfigured, got %v", err)
	}
	if _, err := u.SubmitAccount(context.Background(), "123"); !errors.Is(err, airwallex.ErrUnconfigured) {
		t.Fatalf("expected ErrUnconfigured, got %v", err)
	}
	if _, err := u.GetAccount(context.Background(), "123"); !errors.Is(err, airwallex.ErrUnconfigured) {
		t.Fatalf("expected ErrUnconfigured, got %v", err)
	}
}

func TestMapSubmissionToAccountCreateRequest(t *testing.T) {
	t.Parallel()

	// Missing CompanyCopy
	_, err := airwallex.MapSubmissionToAccountCreateRequest(domain.Submission{})
	if err == nil || !strings.Contains(err.Error(), "company copy is required") {
		t.Fatalf("expected company copy required error, got %v", err)
	}

	// Build valid company
	comp, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_123",
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
			Products: "SaaS bridge underwriting infrastructure and financial analytics platform",
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
				ID:        "person_001",
				FirstName: "Alex",
				LastName:  "Morgan",
				Email:     "operator.contact@acme-innovations.example.com",
				Phone:     "+14155550199",
			},
		},
	})
	if err != nil {
		t.Fatalf("ReconstituteCompany failed: %v", err)
	}

	agreedAt := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	sub := domain.Submission{
		ID:          "sub_001",
		Type:        domain.SubmissionTypeInitialApplication,
		Status:      domain.SubmissionStatusPending,
		CompanyCopy: comp,
		OnboardingDetailsCopy: domain.PlatformOnboardingDetails{
			PrimaryContactPersonID: "person_001",
			CustomerAgreements: domain.CustomerAgreements{
				AgreedToDataUsage:          true,
				AgreedToTermsAndConditions: true,
				TermsAndConditions: &domain.TermsAndConditionsAgreement{
					AgreedAt:             agreedAt,
					ServiceAgreementType: "FULL",
					DeviceData: &domain.DeviceData{
						IPAddress: "198.51.100.42",
						UserAgent: "Mozilla/5.0",
					},
				},
			},
		},
	}

	req, err := airwallex.MapSubmissionToAccountCreateRequest(sub)
	if err != nil {
		t.Fatalf("MapSubmissionToAccountCreateRequest failed: %v", err)
	}

	if req.AccountDetails.BusinessDetails.BusinessName != "Acme Innovations LLC" {
		t.Errorf("got name %q, want Acme Innovations LLC", req.AccountDetails.BusinessDetails.BusinessName)
	}
	if req.AccountDetails.BusinessDetails.BusinessStructure != "LIMITED_LIABILITY_COMPANY" {
		t.Errorf("got structure %q, want LIMITED_LIABILITY_COMPANY", req.AccountDetails.BusinessDetails.BusinessStructure)
	}
	if len(req.AccountDetails.BusinessDetails.BusinessIdentifiers) != 1 {
		t.Fatalf("expected 1 business identifier, got %d", len(req.AccountDetails.BusinessDetails.BusinessIdentifiers))
	}
	if req.AccountDetails.BusinessDetails.BusinessIdentifiers[0].Type != "EIN" || req.AccountDetails.BusinessDetails.BusinessIdentifiers[0].Number != "12-3456789" {
		t.Errorf("got identifier %+v, want EIN 12-3456789", req.AccountDetails.BusinessDetails.BusinessIdentifiers[0])
	}
	if req.AccountDetails.BusinessDetails.RegistrationAddress.AddressLine1 != "100 Market St" {
		t.Errorf("got address line 1 %q, want 100 Market St", req.AccountDetails.BusinessDetails.RegistrationAddress.AddressLine1)
	}
	if req.AccountDetails.BusinessDetails.RegistrationAddress.AddressLine2 != "Suite 400" {
		t.Errorf("got address line 2 %q, want Suite 400", req.AccountDetails.BusinessDetails.RegistrationAddress.AddressLine2)
	}
	if req.PrimaryContact.Mobile != "+14155550199" {
		t.Errorf("got mobile %q, want +14155550199", req.PrimaryContact.Mobile)
	}
	if !req.CustomerAgreements.AgreedToTermsAndConditions || !req.CustomerAgreements.AgreedToDataUsage {
		t.Errorf("expected customer agreements to be true")
	}
	if req.CustomerAgreements.TermsAndConditions.ServiceAgreementType != "FULL" {
		t.Errorf("got service agreement type %q, want FULL", req.CustomerAgreements.TermsAndConditions.ServiceAgreementType)
	}

	if req.AccountDetails.BusinessDetails.RegistrationAddress.Suburb != "San Francisco" {
		t.Errorf("got suburb %q, want San Francisco", req.AccountDetails.BusinessDetails.RegistrationAddress.Suburb)
	}
	if len(req.AccountDetails.BusinessDetails.OperatingCountry) != 1 || req.AccountDetails.BusinessDetails.OperatingCountry[0] != "US" {
		t.Errorf("got operating country %v, want [US]", req.AccountDetails.BusinessDetails.OperatingCountry)
	}

	// Verify exact wire JSON tags (suburb, account_usage, primary_contact without first/last name)
	reqJSON, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal AccountCreateRequest: %v", err)
	}
	reqStr := string(reqJSON)
	if !strings.Contains(reqStr, `"suburb":"San Francisco"`) {
		t.Errorf("expected wire JSON to contain 'suburb', got: %s", reqStr)
	}
	if strings.Contains(reqStr, `"city":`) {
		t.Errorf("expected wire JSON not to contain invented 'city', got: %s", reqStr)
	}
	if strings.Contains(reqStr, `"first_name":`) {
		t.Errorf("expected PrimaryContact not to contain first_name, got: %s", reqStr)
	}

	// Test Update Request mapping with file IDs
	docComp, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_123",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "Acme Innovations LLC",
		EntityType:          "LLC",
		RegistrationCountry: "US",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "SaaS bridge underwriting infrastructure and financial analytics platform",
			RegisteredAddress: domain.Address{
				Line1: "100 Market St", City: "San Francisco", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
		People: []domain.Person{
			{
				ID:          "person_001",
				FirstName:   "Alex",
				LastName:    "Morgan",
				Email:       "alex@example.com",
				Phone:       "+14155550199",
				Nationality: "US",
				DateOfBirth: domain.Date("1985-06-15"),
				Identifications: []domain.Identification{
					{
						ID:             "ident_001",
						Type:           "PASSPORT",
						IssuingCountry: "US",
						Number:         "P12345678",
					},
				},
			},
		},
	})
	subWithDoc := sub
	subWithDoc.CompanyCopy = docComp
	subWithDoc.DocumentIDs = []string{"doc_comp_001", "doc_person_001"}
	subWithDoc.OnboardingDetailsCopy.People = []domain.PersonPlatformDetails{
		{
			PersonID:      "person_001",
			PlatformRoles: []string{"BENEFICIAL_OWNER"},
			ResidentialAddress: domain.Address{
				Line1: "123 Home St", City: "Oakland", State: "CA", PostalCode: "94612", Country: "US",
			},
		},
	}

	appDocs := []domain.Document{
		{
			ID:   "doc_comp_001",
			Type: "CERTIFICATE_OF_INCORPORATION",
		},
		{
			ID:               "doc_person_001",
			PersonID:         "person_001",
			IdentificationID: "ident_001",
			Type:             "PASSPORT",
		},
	}

	updateReq, err := airwallex.MapSubmissionToAccountUpdateRequest(subWithDoc, appDocs, map[string]string{
		"doc_comp_001":   "file_uploaded_001",
		"doc_person_001": "file_person_uploaded_001",
	})
	if err != nil {
		t.Fatalf("MapSubmissionToAccountUpdateRequest failed: %v", err)
	}

	updateJSON, err := json.Marshal(updateReq)
	if err != nil {
		t.Fatalf("failed to marshal AccountUpdateRequest: %v", err)
	}
	updateStr := string(updateJSON)
	if !strings.Contains(updateStr, `"business_person_details":`) {
		t.Errorf("expected wire JSON to contain 'business_person_details', got: %s", updateStr)
	}
	if strings.Contains(updateStr, `"authorised_persons":`) {
		t.Errorf("expected wire JSON not to contain invented 'authorised_persons', got: %s", updateStr)
	}
	if !strings.Contains(updateStr, `"roles":["BENEFICIAL_OWNER"]`) {
		t.Errorf("expected wire JSON to contain 'roles', got: %s", updateStr)
	}
	if strings.Contains(updateStr, `"platform_roles":`) {
		t.Errorf("expected wire JSON not to contain 'platform_roles', got: %s", updateStr)
	}
	if !strings.Contains(updateStr, `"phone_number":"+14155550199"`) {
		t.Errorf("expected wire JSON to contain 'phone_number', got: %s", updateStr)
	}
	if !strings.Contains(updateStr, `"business_documents":[{"file_id":"file_uploaded_001","tag":"BUSINESS_DOCUMENT"}]`) {
		t.Errorf("expected wire JSON to contain 'business_documents', got: %s", updateStr)
	}
	// Invariant: Structured identifications object (primary passport with front_file_id)
	if !strings.Contains(updateStr, `"identifications":{"primary":{`) {
		t.Errorf("expected wire JSON to contain structured identifications object, got: %s", updateStr)
	}
	if !strings.Contains(updateStr, `"identification_type":"PASSPORT"`) {
		t.Errorf("expected wire JSON to contain identification_type PASSPORT, got: %s", updateStr)
	}
	if !strings.Contains(updateStr, `"front_file_id":"file_person_uploaded_001"`) {
		t.Errorf("expected wire JSON to contain front_file_id under passport, got: %s", updateStr)
	}
	if strings.Contains(updateStr, `"tag":"PASSPORT"`) {
		t.Errorf("unsupported tag PASSPORT must not appear in business_person_documents; passport scan belongs to identifications.primary.passport.front_file_id, got: %s", updateStr)
	}

	// Test supplemental person documents (e.g. authorization letter)
	appDocsWithAuth := append(appDocs, domain.Document{
		ID:       "doc_auth_001",
		PersonID: "person_001",
		Type:     "AUTHORIZATION_LETTER",
	})
	subWithAuth := subWithDoc
	subWithAuth.DocumentIDs = append(subWithAuth.DocumentIDs, "doc_auth_001")
	updateReqAuth, err := airwallex.MapSubmissionToAccountUpdateRequest(subWithAuth, appDocsWithAuth, map[string]string{
		"doc_comp_001":   "file_uploaded_001",
		"doc_person_001": "file_person_uploaded_001",
		"doc_auth_001":   "file_auth_001",
	})
	if err != nil {
		t.Fatalf("MapSubmissionToAccountUpdateRequest with auth failed: %v", err)
	}
	updateAuthJSON, _ := json.Marshal(updateReqAuth)
	updateAuthStr := string(updateAuthJSON)
	if !strings.Contains(updateAuthStr, `"business_person_documents":[{"file_id":"file_auth_001","tag":"PERSON_PURPORTING_TO_ACT_AUTHORISATION_LETTER"}]`) {
		t.Errorf("expected supplemental person document to map to allowed tag, got: %s", updateAuthStr)
	}

	// Test Driver License with Front and Back file IDs and issuing_state
	dlComp, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_dl",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "DL Inc",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "Some products",
			RegisteredAddress: domain.Address{
				Line1: "123 Main", City: "SF", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
		People: []domain.Person{
			{
				ID:          "person_dl",
				FirstName:   "Sam",
				LastName:    "Rivera",
				Email:       "sam@example.com",
				Nationality: "US",
				Identifications: []domain.Identification{
					{
						ID:             "id_dl_1",
						Type:           "DRIVERS_LICENCE",
						IssuingCountry: "US",
						Number:         "DL9999",
					},
				},
			},
		},
	})
	subDL := sub
	subDL.CompanyCopy = dlComp
	subDL.DocumentIDs = []string{"doc_dl_f", "doc_dl_b"}
	subDL.OnboardingDetailsCopy.People = []domain.PersonPlatformDetails{
		{
			PersonID:      "person_dl",
			PlatformRoles: []string{"DIRECTOR"},
			ResidentialAddress: domain.Address{
				State: "CA",
			},
		},
	}
	dlDocs := []domain.Document{
		{ID: "doc_dl_f", PersonID: "person_dl", IdentificationID: "id_dl_1", Type: "DRIVERS_LICENCE_FRONT"},
		{ID: "doc_dl_b", PersonID: "person_dl", IdentificationID: "id_dl_1", Type: "DRIVERS_LICENCE_BACK"},
	}
	updateReqDL, err := airwallex.MapSubmissionToAccountUpdateRequest(subDL, dlDocs, map[string]string{
		"doc_dl_f": "file_dl_front",
		"doc_dl_b": "file_dl_back",
	})
	if err != nil {
		t.Fatalf("MapSubmissionToAccountUpdateRequest for DL failed: %v", err)
	}
	dlJSON, _ := json.Marshal(updateReqDL)
	dlStr := string(dlJSON)
	if !strings.Contains(dlStr, `"identification_type":"DRIVERS_LICENCE"`) ||
		!strings.Contains(dlStr, `"drivers_license":{`) ||
		!strings.Contains(dlStr, `"front_file_id":"file_dl_front"`) ||
		!strings.Contains(dlStr, `"back_file_id":"file_dl_back"`) {
		t.Errorf("expected wire JSON to contain driver license discriminator, drivers_license object, and file IDs, got: %s", dlStr)
	}
	if strings.Contains(dlStr, `"drivers_licence":`) {
		t.Errorf("expected wire JSON to NOT contain British spelling 'drivers_licence' for object property name (must be 'drivers_license'), got: %s", dlStr)
	}
	if strings.Contains(dlStr, `"issuing_state":`) {
		t.Errorf("expected wire JSON to NOT fabricate issuing_state from residential address state, got: %s", dlStr)
	}

	// Test Personal ID with wire assertions (personal_id, not national_id)
	pidComp, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_pid",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "PID Inc",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "Some products",
			RegisteredAddress: domain.Address{
				Line1: "123 Main", City: "SF", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
		People: []domain.Person{
			{
				ID:          "person_pid",
				FirstName:   "Pat",
				LastName:    "Lee",
				Nationality: "US",
				Identifications: []domain.Identification{
					{
						ID:             "id_pid_1",
						Type:           "PERSONAL_ID",
						IssuingCountry: "US",
						Number:         "PID123",
					},
				},
			},
		},
	})
	subPID := sub
	subPID.CompanyCopy = pidComp
	subPID.DocumentIDs = []string{"doc_pid_f", "doc_pid_b"}
	subPID.OnboardingDetailsCopy.People = []domain.PersonPlatformDetails{
		{PersonID: "person_pid", PlatformRoles: []string{"DIRECTOR"}},
	}
	pidDocs := []domain.Document{
		{ID: "doc_pid_f", PersonID: "person_pid", IdentificationID: "id_pid_1", Type: "PERSONAL_ID_FRONT"},
		{ID: "doc_pid_b", PersonID: "person_pid", IdentificationID: "id_pid_1", Type: "PERSONAL_ID_BACK"},
	}
	updateReqPID, err := airwallex.MapSubmissionToAccountUpdateRequest(subPID, pidDocs, map[string]string{
		"doc_pid_f": "file_pid_front",
		"doc_pid_b": "file_pid_back",
	})
	if err != nil {
		t.Fatalf("MapSubmissionToAccountUpdateRequest for PID failed: %v", err)
	}
	pidJSON, _ := json.Marshal(updateReqPID)
	pidStr := string(pidJSON)
	if !strings.Contains(pidStr, `"identification_type":"PERSONAL_ID"`) ||
		!strings.Contains(pidStr, `"personal_id":{`) {
		t.Errorf("expected wire JSON to contain personal_id, got: %s", pidStr)
	}
	if strings.Contains(pidStr, `"national_id":`) {
		t.Errorf("wire JSON must NOT contain national_id, got: %s", pidStr)
	}

	// Test generic TAX_ID rejected (requires explicit SSN or ITIN)
	taxIDComp, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_tax_id",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "Tax Inc",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "Some products",
			RegisteredAddress: domain.Address{
				Line1: "123 Main", City: "SF", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
		People: []domain.Person{
			{
				ID:          "person_tax",
				FirstName:   "Robin",
				LastName:    "Hood",
				Nationality: "US",
				Identifications: []domain.Identification{
					{ID: "id_generic_tax", Type: "TAX_ID", IssuingCountry: "US", Number: "999-99-9999"},
				},
			},
		},
	})
	subTaxID := sub
	subTaxID.CompanyCopy = taxIDComp
	subTaxID.OnboardingDetailsCopy.People = []domain.PersonPlatformDetails{
		{PersonID: "person_tax", PlatformRoles: []string{"DIRECTOR"}},
	}
	if _, err := airwallex.MapSubmissionToAccountUpdateRequest(subTaxID, nil, nil); !errors.Is(err, airwallex.ErrIncompleteSubmissionMapping) {
		t.Errorf("expected ErrIncompleteSubmissionMapping for generic TAX_ID, got: %v", err)
	}

	// Test multiple identifications (>1) fail closed so reversed list order cannot change selection unnoticed
	multiIDComp1, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_multi_1",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "Multi 1 Inc",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "Some products",
			RegisteredAddress: domain.Address{
				Line1: "123 Main", City: "SF", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
		People: []domain.Person{
			{
				ID:          "person_multi_1",
				FirstName:   "Taylor",
				LastName:    "Swift",
				Nationality: "US",
				Identifications: []domain.Identification{
					{ID: "id_1", Type: "PASSPORT", IssuingCountry: "US", Number: "P1"},
					{ID: "id_2", Type: "DRIVERS_LICENCE", IssuingCountry: "US", Number: "DL1"},
				},
			},
		},
	})
	subMulti1 := sub
	subMulti1.CompanyCopy = multiIDComp1
	subMulti1.OnboardingDetailsCopy.People = []domain.PersonPlatformDetails{
		{PersonID: "person_multi_1", PlatformRoles: []string{"DIRECTOR"}},
	}
	if _, err := airwallex.MapSubmissionToAccountUpdateRequest(subMulti1, nil, nil); !errors.Is(err, airwallex.ErrIncompleteSubmissionMapping) {
		t.Errorf("expected ErrIncompleteSubmissionMapping for >1 identifications (fail closed), got: %v", err)
	}

	// Test duplicate document ambiguity rejected (multiple front images for same identification)
	subDup := subWithDoc
	dupDocs := []domain.Document{
		{ID: "doc_p_1", PersonID: "person_001", IdentificationID: "ident_001", Type: "PASSPORT_FRONT"},
		{ID: "doc_p_2", PersonID: "person_001", IdentificationID: "ident_001", Type: "PASSPORT_FRONT"},
	}
	subDup.DocumentIDs = []string{"doc_p_1", "doc_p_2"}
	if _, err := airwallex.MapSubmissionToAccountUpdateRequest(subDup, dupDocs, map[string]string{
		"doc_p_1": "file_p_1",
		"doc_p_2": "file_p_2",
	}); !errors.Is(err, airwallex.ErrIncompleteSubmissionMapping) {
		t.Errorf("expected ErrIncompleteSubmissionMapping on duplicate front document images, got: %v", err)
	}

	// Test unsupported identification type returns ErrIncompleteSubmissionMapping
	unsupportedIDComp, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_bad_id",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "Bad ID Inc",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "Some products",
			RegisteredAddress: domain.Address{
				Line1: "123 Main", City: "SF", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
		People: []domain.Person{
			{
				ID:          "person_bad",
				FirstName:   "Bad",
				LastName:    "Type",
				Nationality: "US",
				Identifications: []domain.Identification{
					{ID: "id_unsupported", Type: "VOTER_REGISTRATION_CARD", IssuingCountry: "US", Number: "V123"},
				},
			},
		},
	})
	subBadID := sub
	subBadID.CompanyCopy = unsupportedIDComp
	subBadID.OnboardingDetailsCopy.People = []domain.PersonPlatformDetails{
		{PersonID: "person_bad", PlatformRoles: []string{"DIRECTOR"}},
	}
	if _, err := airwallex.MapSubmissionToAccountUpdateRequest(subBadID, nil, nil); !errors.Is(err, airwallex.ErrIncompleteSubmissionMapping) {
		t.Errorf("expected ErrIncompleteSubmissionMapping for unsupported identification type, got: %v", err)
	}

	// Test incomplete mapping validations
	// 1. Missing EIN
	noEINComp, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_no_ein",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "No EIN Inc",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		BusinessProfile: domain.BusinessProfile{
			Products: "Some products",
			RegisteredAddress: domain.Address{
				Line1: "123 Main", City: "SF", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
	})
	subNoEIN := sub
	subNoEIN.CompanyCopy = noEINComp
	if _, err := airwallex.MapSubmissionToAccountCreateRequest(subNoEIN); !errors.Is(err, airwallex.ErrIncompleteSubmissionMapping) {
		t.Errorf("expected ErrIncompleteSubmissionMapping for missing EIN, got %v", err)
	}

	// 2. Missing entity type (never default to LLC)
	noTypeComp, _ := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_no_type",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		LegalName:           "No Type Inc",
		EntityType:          "",
		RegistrationCountry: "US",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
		BusinessProfile: domain.BusinessProfile{
			Products: "Some products",
			RegisteredAddress: domain.Address{
				Line1: "123 Main", City: "SF", State: "CA", PostalCode: "94105", Country: "US",
			},
		},
	})
	subNoType := sub
	subNoType.CompanyCopy = noTypeComp
	if _, err := airwallex.MapSubmissionToAccountCreateRequest(subNoType); !errors.Is(err, airwallex.ErrIncompleteSubmissionMapping) {
		t.Errorf("expected ErrIncompleteSubmissionMapping for empty entity type, got %v", err)
	}
}

func TestClient_GetRFI(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/authentication/login":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{
				Token:     "test-token",
				ExpiresAt: time.Now().Add(10 * time.Minute),
			})

		case "/api/v1/rfis/rfi_test_001":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(airwallex.RFIResponsePayload{
				ID:        "rfi_test_001",
				AccountID: "acct_test_001",
				Type:      "KYC",
				Status:    "ACTION_REQUIRED",
				ActiveRequest: &airwallex.RFIActiveRequest{
					Questions: []airwallex.RFIQuestionPrompt{
						{
							ID: "q_platform_1",
							Answer: airwallex.RFIAnswerRequirement{
								Type: "TEXT",
							},
						},
					},
				},
			})

		case "/api/v1/rfis/rfi_not_found":
			w.WriteHeader(http.StatusNotFound)

		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  server.URL,
		ClientID: "cid",
		APIKey:   "key",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Success
	rfi, err := client.GetRFI(context.Background(), "rfi_test_001")
	if err != nil {
		t.Fatalf("GetRFI failed: %v", err)
	}
	if rfi.ID != "rfi_test_001" || rfi.AccountID != "acct_test_001" || rfi.Status != "ACTION_REQUIRED" {
		t.Fatalf("unexpected RFI payload: %+v", rfi)
	}

	// Not Found
	_, err = client.GetRFI(context.Background(), "rfi_not_found")
	if !errors.Is(err, airwallex.ErrResourceNotFound) {
		t.Fatalf("expected ErrResourceNotFound, got %v", err)
	}
}

func TestClient_RespondRFI(t *testing.T) {
	t.Parallel()

	var capturedBody airwallex.RFIRespondRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/authentication/login":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(airwallex.AuthLoginResponse{
				Token:     "test-token",
				ExpiresAt: time.Now().Add(10 * time.Minute),
			})

		case "/api/v1/rfis/rfi_test_001/respond":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))

		case "/api/v1/rfis/rfi_invalid_state/respond":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(airwallex.APIError{
				Code:    "invalid_state_for_operation",
				Message: "rfi is not in action required state",
			})

		case "/api/v1/rfis/rfi_500/respond":
			w.WriteHeader(http.StatusInternalServerError)

		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client, err := airwallex.NewClient(airwallex.ClientConfig{
		BaseURL:  server.URL,
		ClientID: "cid",
		APIKey:   "key",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Success: verified without asserting invented status
	req := airwallex.RFIRespondRequest{
		Questions: []airwallex.RFIQuestionAnswer{
			{
				ID: "q_platform_1",
				Answer: airwallex.RFIAnswerValue{
					Text: "Verified operating description",
				},
			},
		},
	}
	resp, err := client.RespondRFI(context.Background(), "rfi_test_001", req)
	if err != nil {
		t.Fatalf("RespondRFI failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if len(capturedBody.Questions) != 1 || capturedBody.Questions[0].Answer.Text != "Verified operating description" {
		t.Fatalf("unexpected captured request body: %+v", capturedBody)
	}

	// Error: invalid_state_for_operation
	_, err = client.RespondRFI(context.Background(), "rfi_invalid_state", req)
	if !errors.Is(err, airwallex.ErrInvalidStateForOperation) {
		t.Fatalf("expected ErrInvalidStateForOperation, got %v", err)
	}

	// Ambiguity: 5xx on non-idempotent respond returns ErrAmbiguousRemoteOutcome
	_, err = client.RespondRFI(context.Background(), "rfi_500", req)
	if !errors.Is(err, airwallex.ErrAmbiguousRemoteOutcome) {
		t.Fatalf("expected ErrAmbiguousRemoteOutcome for 500, got %v", err)
	}
}

func TestMapSubmissionToRFIRespondRequest(t *testing.T) {
	t.Parallel()

	internalRFI := domain.InformationRequest{
		ID:                "rfi_int_01",
		PlatformReference: "rfi_airwallex_01",
		Questions: []domain.RFIQuestion{
			{
				ID:                 "q_int_1",
				PlatformQuestionID: "q_plat_1",
				Text:               "What is your business model?",
			},
		},
	}

	sub := domain.Submission{
		ID:                   "sub_001",
		Type:                 domain.SubmissionTypeInformationResponse,
		InformationRequestID: "rfi_int_01",
		Answers: map[string]string{
			"q_int_1": "B2B bridge credit underwriting platform",
		},
	}

	remoteRFI := airwallex.RFIResponsePayload{
		ID:        "rfi_airwallex_01",
		AccountID: "acct_001",
		Type:      "KYC",
		Status:    "ACTION_REQUIRED",
		ActiveRequest: &airwallex.RFIActiveRequest{
			Questions: []airwallex.RFIQuestionPrompt{
				{
					ID: "q_plat_1",
					Answer: airwallex.RFIAnswerRequirement{
						Type: "TEXT",
					},
				},
			},
		},
	}

	// Success
	req, err := airwallex.MapSubmissionToRFIRespondRequest(sub, internalRFI, remoteRFI)
	if err != nil {
		t.Fatalf("MapSubmissionToRFIRespondRequest failed: %v", err)
	}
	if len(req.Questions) != 1 || req.Questions[0].ID != "q_plat_1" || req.Questions[0].Answer.Text != "B2B bridge credit underwriting platform" {
		t.Fatalf("unexpected mapped request: %+v", req)
	}

	// Documentary type fails: only TEXT supported
	docRemoteRFI := remoteRFI
	docRemoteRFI.ActiveRequest = &airwallex.RFIActiveRequest{
		Questions: []airwallex.RFIQuestionPrompt{
			{
				ID: "q_plat_1",
				Answer: airwallex.RFIAnswerRequirement{
					Type: "FILE",
				},
			},
		},
	}
	_, err = airwallex.MapSubmissionToRFIRespondRequest(sub, internalRFI, docRemoteRFI)
	if err == nil || !strings.Contains(err.Error(), "documentary requires manual handling") {
		t.Fatalf("expected documentary manual handling error, got %v", err)
	}

	// Missing answer fails closed
	subMissing := sub
	subMissing.Answers = map[string]string{}
	_, err = airwallex.MapSubmissionToRFIRespondRequest(subMissing, internalRFI, remoteRFI)
	if err == nil || !strings.Contains(err.Error(), "missing frozen answer") {
		t.Fatalf("expected missing answer error, got %v", err)
	}
}
