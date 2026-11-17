package validation_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	internalhttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
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

	empty := ""
	validNationality := "US"
	validEmail := "person@example.test"
	invalidNationality := "USA"
	invalidEmail := "not-an-email"
	personCases := []struct {
		name        string
		nationality *string
		email       *string
		wantErrors  map[string]string
	}{
		{name: "nil means omitted"},
		{name: "empty means clear", nationality: &empty, email: &empty},
		{name: "valid values", nationality: &validNationality, email: &validEmail},
		{
			name:        "invalid values still fail",
			nationality: &invalidNationality,
			email:       &invalidEmail,
			wantErrors: map[string]string{
				"nationality": "iso3166_alpha2",
				"email":       "email",
			},
		},
	}
	for _, tc := range personCases {
		t.Run(tc.name, func(t *testing.T) {
			person := internalhttp.PersonDTO{
				FirstName:   "Ada",
				LastName:    "Lovelace",
				Nationality: tc.nationality,
				Email:       tc.email,
				Role:        "Director",
				DateOfBirth: "1990-01-01",
			}
			personErrors := v.ValidateStruct(person)
			if len(personErrors) != len(tc.wantErrors) {
				t.Fatalf("PersonDTO validation errors = %+v, want %d errors", personErrors, len(tc.wantErrors))
			}
			for _, fieldErr := range personErrors {
				if wantCode, ok := tc.wantErrors[fieldErr.Field]; !ok || fieldErr.Code != wantCode {
					t.Errorf("PersonDTO field error = %+v, want expected field/code", fieldErr)
				}
			}
		})
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
		OperatingAddress: &domain.Address{
			Line1:      "200 Elm St",
			City:       "Wilmington",
			State:      "DE",
			PostalCode: "19801",
			Country:    "US",
		},
		OperatingCountries: []string{"US"},
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
		Nationality: "US",
		Email:       "jane.smith@example.com",
		Phone:       "+15551234567",
		Role:        "CEO",
		DateOfBirth: domain.Date("1980-05-15"),
		Identifications: []domain.Identification{
			{
				ID:             "ident_1",
				Type:           "SSN",
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
		IntendedProducts:       []string{"CREATE_CARDS"},
		EstimatedRevenue: domain.RevenueEstimate{
			Amount:   "100000",
			Currency: "USD",
		},
		People: []domain.PersonPlatformDetails{
			{
				PersonID:                "p_1",
				PlatformRoles:           []string{"AUTHORISED_PERSON", "BENEFICIAL_OWNER", "CONTROLLING_PERSON"},
				PrimaryIdentificationID: "ident_1",
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
		Type:        "CERTIFICATE_OF_INCORPORATION",
		FileName:    "cert.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		StorageKey:  "keys/cert.pdf",
		UploadedBy:  "creator",
		UploadedAt:  now,
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

func TestVerifyOnboardingReadiness_DefaultUSContractPassesCompleteSyntheticApplication(t *testing.T) {
	comp := helperValidReadyCompany(t, "comp_ready")
	app := helperValidReadyApplication(t, "app_ready", comp)
	operatingAgreement := domain.Document{
		ID:          "doc_operating_agreement",
		Type:        "OPERATING_AGREEMENT",
		FileName:    "operating-agreement.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		StorageKey:  "keys/operating-agreement.pdf",
		UploadedBy:  "creator",
		UploadedAt:  time.Now().UTC(),
	}
	if err := app.AttachDocument(operatingAgreement); err != nil {
		t.Fatalf("AttachDocument() optional operating agreement: %v", err)
	}
	details := app.Platform().OnboardingDetails
	details.CustomerAgreements = domain.CustomerAgreements{}
	if err := app.UpdateOnboardingDetails(details); err != nil {
		t.Fatalf("UpdateOnboardingDetails() without sandbox consent: %v", err)
	}

	// The hackathon sandbox path allows omitted agreements without inventing consent.
	result := validation.VerifyOnboardingReadiness(comp, app, nil)
	if result.Code != "VALIDATION_PASSED" {
		t.Fatalf("expected VALIDATION_PASSED, got %q: %+v", result.Code, result.Errors)
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

	platform := app.Platform()
	platform.Environment = "production"
	productionApp, err := domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:        "app_production_no_consent",
		Company:   comp,
		Platform:  platform,
		Status:    domain.ApplicationStatusDraft,
		Creator:   "creator",
		CreatedAt: time.Now().UTC(),
		Revision:  app.Revision(),
	})
	if err != nil {
		t.Fatalf("ReconstituteOnboardingApplication() production case: %v", err)
	}
	productionResult := validation.VerifyOnboardingReadiness(comp, productionApp, nil)
	for _, field := range []string{
		"platform.onboarding_details.customer_agreements.agreed_to_terms_and_conditions",
		"platform.onboarding_details.customer_agreements.agreed_to_data_usage",
		"platform.onboarding_details.customer_agreements.terms_and_conditions.agreed_at",
	} {
		found := false
		for _, fieldErr := range productionResult.Errors {
			if fieldErr.Field == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("production validation missing consent field error %q", field)
		}
	}

	optionalOnlyApp, err := domain.ReconstituteOnboardingApplication(domain.ReconstituteApplicationParams{
		ID:        "app_optional_doc_only",
		Company:   comp,
		Platform:  app.Platform(),
		Status:    domain.ApplicationStatusDraft,
		Documents: []domain.Document{operatingAgreement},
		Creator:   "creator",
		CreatedAt: app.CreatedAt(),
		Revision:  app.Revision(),
	})
	if err != nil {
		t.Fatalf("ReconstituteOnboardingApplication() optional document case: %v", err)
	}
	optionalOnlyResult := validation.VerifyOnboardingReadiness(comp, optionalOnlyApp, nil)
	var requiredFormationDocument, unsupportedDocument bool
	for _, fieldErr := range optionalOnlyResult.Errors {
		if fieldErr.Field == "documents" && fieldErr.Code == "required_business_document" {
			requiredFormationDocument = true
		}
		if fieldErr.Field == "documents" && fieldErr.Code == "unsupported_business_document" {
			unsupportedDocument = true
		}
	}
	if !requiredFormationDocument || unsupportedDocument {
		t.Errorf("optional operating agreement errors = %+v, want required formation document only", optionalOnlyResult.Errors)
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
		"company.business_profile.operating_address",
		"company.business_profile.operating_countries",
		"company.registration_identifiers",
		"company.people",
		"platform.onboarding_details.primary_contact_person_id",
		"platform.onboarding_details.industry_category_code",
		"platform.onboarding_details.intended_products",
		"platform.onboarding_details.estimated_monthly_revenue.amount",
		"platform.onboarding_details.estimated_monthly_revenue.currency",
		"platform.onboarding_details.people",
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
