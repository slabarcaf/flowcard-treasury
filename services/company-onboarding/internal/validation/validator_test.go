package validation_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
)

type samplePersonDTO struct {
	FirstName   string `json:"first_name" validate:"required,min=2"`
	LastName    string `json:"last_name" validate:"required"`
	DateOfBirth string `json:"date_of_birth" validate:"required,past_date"`
	TaxID       string `json:"tax_id" validate:"required,min=9"`
}

type sampleCompanyDTO struct {
	LegalName           string            `json:"legal_name" validate:"required"`
	RegistrationCountry string            `json:"registration_country" validate:"required,iso3166_alpha2"`
	Website             string            `json:"website" validate:"required,url"`
	People              []samplePersonDTO `json:"people" validate:"required,dive"`
}

func TestValidationFieldPathAndCodeFormatting(t *testing.T) {
	v := validation.NewValidator()

	// Incomplete input: invalid date, short tax ID, missing legal name, invalid country code
	input := sampleCompanyDTO{
		LegalName:           "",
		RegistrationCountry: "USA", // Not 2-letter alpha2
		Website:             "invalid-url",
		People: []samplePersonDTO{
			{
				FirstName:   "J",          // too short (min=2)
				LastName:    "",           // required
				DateOfBirth: "2099-01-01", // not past date
				TaxID:       "123",        // too short (min=9) sensitive value
			},
		},
	}

	errs := v.ValidateStruct(input)
	if len(errs) == 0 {
		t.Fatalf("expected validation errors, got 0")
	}

	foundPaths := make(map[string]validation.FieldError)
	for _, fe := range errs {
		foundPaths[fe.Field] = fe
		// Crucial security requirement: ensure sensitive value (TaxID "123") is NOT echoed in Message
		if strings.Contains(fe.Message, "123") {
			t.Errorf("sensitive value echoed in error message: %q", fe.Message)
		}
	}

	expectedFields := []struct {
		field string
		code  string
	}{
		{"legal_name", "required"},
		{"registration_country", "iso3166_alpha2"},
		{"website", "url"},
		{"people[0].first_name", "min"},
		{"people[0].last_name", "required"},
		{"people[0].date_of_birth", "past_date"},
		{"people[0].tax_id", "min"},
	}

	for _, ef := range expectedFields {
		fe, ok := foundPaths[ef.field]
		if !ok {
			t.Errorf("missing expected field error for %q", ef.field)
			continue
		}
		if fe.Code != ef.code {
			t.Errorf("field %q code = %q, want %q", ef.field, fe.Code, ef.code)
		}
	}
}

func helperValidReadyCompany(t *testing.T, id string) *domain.Company {
	t.Helper()
	comp, err := domain.NewDraftCompany(id, "Acme Technologies Inc.")
	if err != nil {
		t.Fatalf("NewDraftCompany failed: %v", err)
	}
	_ = comp.UpdateTradingName("Acme Tech")
	_ = comp.UpdateProfile(domain.BusinessProfile{
		Website:     "https://acme-tech.example.com",
		Description: "Cloud treasury infrastructure",
		Products:    "SaaS bridge underwriting infrastructure and financial analytics platform",
		RegisteredAddress: domain.Address{
			Line1:      "100 Main St",
			City:       "Wilmington",
			State:      "DE",
			PostalCode: "19801",
			Country:    "US",
		},
	})
	_ = comp.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
		LegalName:           "Acme Technologies Inc.",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationState:   "DE",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{Type: "EIN", Value: "12-3456789", IssuingCountry: "US"},
		},
	})
	_ = comp.AddPerson(domain.Person{
		ID:          "p_1",
		FirstName:   "Jane",
		LastName:    "Smith",
		Email:       "jane.smith@example.com",
		Phone:       "+15551234567",
		Role:        "CEO",
		DateOfBirth: domain.Date("1980-05-15"),
		Identifications: []domain.Identification{
			{
				ID:             "ident_1",
				Type:           "PASSPORT",
				Number:         "P12345678",
				IssuingCountry: "US",
			},
		},
	})
	return comp
}

func helperValidReadyApplication(t *testing.T, id string, comp *domain.Company) *domain.OnboardingApplication {
	t.Helper()
	app, err := domain.NewDraftApplication(id, comp, "creator")
	if err != nil {
		t.Fatalf("NewDraftApplication failed: %v", err)
	}
	now := time.Now().UTC()
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		PrimaryContactPersonID: "p_1",
		IndustryCategoryCode:   "software_saas",
		IntendedProducts:       []string{"card_issuing"},
		EstimatedRevenue: domain.RevenueEstimate{
			Amount:   "100000",
			Currency: "USD",
		},
		People: []domain.PersonPlatformDetails{
			{
				PersonID:      "p_1",
				PlatformRoles: []string{"DIRECTOR"},
				ResidentialAddress: domain.Address{
					Line1:      "200 Elm St",
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
				AgreedAt:             now,
				ServiceAgreementType: "FULL",
			},
		},
	})

	_ = app.AttachDocument(domain.Document{
		ID:          "doc_biz_1",
		Type:        "BUSINESS_REGISTRATION",
		FileName:    "cert.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		StorageKey:  "keys/cert.pdf",
		UploadedBy:  "creator",
		UploadedAt:  now,
	})
	_ = app.AttachDocument(domain.Document{
		ID:               "doc_id_1",
		Type:             "IDENTITY_DOCUMENT",
		PersonID:         "p_1",
		IdentificationID: "ident_1",
		FileName:         "pass.png",
		ContentType:      "image/png",
		SizeBytes:        2048,
		StorageKey:       "keys/pass.png",
		UploadedBy:       "creator",
		UploadedAt:       now,
	})

	return app
}

type fakeVerifiedPlatformGate struct{}

func (fakeVerifiedPlatformGate) ValidatePlatformContract(comp *domain.Company, app *domain.OnboardingApplication) error {
	return nil
}

func TestVerifyOnboardingReadiness_SuccessWithVerifiedGate(t *testing.T) {
	comp := helperValidReadyCompany(t, "comp_ready")
	app := helperValidReadyApplication(t, "app_ready", comp)

	// Verified stub passes external platform contract
	result := validation.VerifyOnboardingReadiness(comp, app, fakeVerifiedPlatformGate{})
	if result.Code != "VALIDATION_PASSED" {
		t.Fatalf("expected validation to pass, but got code %q, errors: %+v", result.Code, result.Errors)
	}
	if result.ApplicationRevision != app.Revision() {
		t.Errorf("ApplicationRevision = %d, want %d", result.ApplicationRevision, app.Revision())
	}
	if result.CompanyRevision != comp.Revision() {
		t.Errorf("CompanyRevision = %d, want %d", result.CompanyRevision, comp.Revision())
	}
}

func TestVerifyOnboardingReadiness_DefaultGateFailsClosed(t *testing.T) {
	comp := helperValidReadyCompany(t, "comp_ready")
	app := helperValidReadyApplication(t, "app_ready", comp)

	// Default gate (nil or UnverifiedPlatformContractGate) must fail closed on external unverified gates
	result := validation.VerifyOnboardingReadiness(comp, app, nil)
	if result.Code != "PLATFORM_CONTRACT_INCOMPLETE" {
		t.Fatalf("expected PLATFORM_CONTRACT_INCOMPLETE, got %q", result.Code)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 local field errors for complete-looking app, got %d: %+v", len(result.Errors), result.Errors)
	}
	if result.ApplicationRevision != app.Revision() {
		t.Errorf("ApplicationRevision = %d, want %d", result.ApplicationRevision, app.Revision())
	}
	if result.CompanyRevision != comp.Revision() {
		t.Errorf("CompanyRevision = %d, want %d", result.CompanyRevision, comp.Revision())
	}
}

func TestVerifyOnboardingReadiness_ExhaustiveFieldErrors(t *testing.T) {
	// Create bare company and bare application
	comp, _ := domain.NewDraftCompany("comp_empty", "Empty Corp")
	// No profile, no address, no identifiers, no people

	app, _ := domain.NewDraftApplication("app_empty", comp, "creator")
	// No contact person, no agreements

	result := validation.VerifyOnboardingReadiness(comp, app, fakeVerifiedPlatformGate{})
	if result.Code != "VALIDATION_FAILED" {
		t.Fatalf("result.Code = %q, want VALIDATION_FAILED", result.Code)
	}

	// Verify exhaustive errors accumulated
	errorFields := make(map[string]bool)
	for _, e := range result.Errors {
		errorFields[e.Field] = true
	}

	expectedMissing := []string{
		"company.entity_type",
		"company.registration_country",
		"company.business_profile.products",
		"company.business_profile.website",
		"company.business_profile.description",
		"company.business_profile.registered_address.line_1",
		"company.registration_identifiers",
		"company.people",
		"platform.onboarding_details.primary_contact_person_id",
		"platform.onboarding_details.customer_agreements.agreed_to_terms_and_conditions",
		"platform.onboarding_details.customer_agreements.agreed_to_data_usage",
		"platform.onboarding_details.customer_agreements.terms_and_conditions.agreed_at",
	}

	for _, f := range expectedMissing {
		if !errorFields[f] {
			t.Errorf("expected exhaustive error field %q, but missing from results", f)
		}
	}
}

func TestVerifyOnboardingReadiness_UnsupportedPlatformFailClosed(t *testing.T) {
	comp := helperValidReadyCompany(t, "comp_p")
	// Reconstitute with an unverified/unsupported platform name
	appRecon, err := domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:      "app_unsupp",
		Company: comp,
		Platform: domain.ApplicationPlatform{
			Name: "STRIPE", // unsupported provider in current scope
		},
		Status:    domain.ApplicationStatusDraft,
		Creator:   "creator",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Revision:  1,
	})
	if err != nil {
		t.Fatalf("reconstitute failed: %v", err)
	}

	result := validation.VerifyOnboardingReadiness(comp, appRecon, fakeVerifiedPlatformGate{})
	if result.Code != "VALIDATION_FAILED" {
		t.Fatalf("expected unsupported platform to fail with VALIDATION_FAILED, got %q", result.Code)
	}

	foundUnsupported := false
	for _, e := range result.Errors {
		if e.Field == "platform.name" && e.Code == "unsupported_platform" {
			foundUnsupported = true
			break
		}
	}
	if !foundUnsupported {
		t.Errorf("expected error on platform.name with code unsupported_platform")
	}
}

func TestValidationResult_JSONOmissionOfPassed(t *testing.T) {
	res := validation.ValidationResult{
		Code:                "PLATFORM_CONTRACT_INCOMPLETE",
		Message:             "platform contract incomplete",
		ApplicationRevision: 2,
		CompanyRevision:     3,
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	s := string(b)
	if strings.Contains(s, `"passed"`) {
		t.Fatalf("expected JSON to omit 'passed' boolean property, got: %s", s)
	}
}
