package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
)

func TestNewDraftCompany(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		legalName string
		wantErr   error
	}{
		{
			name:      "valid_draft_with_legal_name",
			id:        "comp_123",
			legalName: "Acme Payments Inc",
			wantErr:   nil,
		},
		{
			name:      "valid_incomplete_draft_without_legal_name",
			id:        "comp_456",
			legalName: "",
			wantErr:   nil,
		},
		{
			name:      "empty_id_rejected",
			id:        "   ",
			legalName: "Acme Payments Inc",
			wantErr:   domain.ErrEmptyCompanyID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			comp, err := domain.NewDraftCompany(tt.id, tt.legalName)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("NewDraftCompany() expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("NewDraftCompany() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("NewDraftCompany() unexpected error: %v", err)
			}
			if comp.ID() != tt.id {
				t.Errorf("ID() = %q, want %q", comp.ID(), tt.id)
			}
			if comp.Status() != domain.CompanyStatusActive {
				t.Errorf("Status() = %q, want %q", comp.Status(), domain.CompanyStatusActive)
			}
			if comp.Revision() != 1 {
				t.Errorf("Revision() = %d, want 1", comp.Revision())
			}
			if comp.HasSubmittedHistory() {
				t.Errorf("HasSubmittedHistory() = true, want false for new draft")
			}
			if comp.LegalName() != tt.legalName {
				t.Errorf("LegalName() = %q, want %q", comp.LegalName(), tt.legalName)
			}
			if len(comp.RegistrationIdentifiers()) != 0 {
				t.Errorf("len(RegistrationIdentifiers()) = %d, want 0", len(comp.RegistrationIdentifiers()))
			}
			if len(comp.People()) != 0 {
				t.Errorf("len(People()) = %d, want 0", len(comp.People()))
			}
			if len(comp.LegalIdentifierAuditTrail()) != 0 {
				t.Errorf("len(LegalIdentifierAuditTrail()) = %d, want 0", len(comp.LegalIdentifierAuditTrail()))
			}
		})
	}
}

func TestRegistrationIdentifiersList(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_reg_list", "Dual Registration LLC")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	// Company with both federal EIN and state registration number
	ein := domain.RegistrationIdentifier{
		Type:           "EIN",
		Value:          "12-3456789",
		IssuingCountry: "US",
	}
	stateReg := domain.RegistrationIdentifier{
		Type:           "STATE_REGISTRATION_NUMBER",
		Value:          "DE-SR-987654",
		IssuingCountry: "US",
	}

	params := domain.LegalIdentifierCorrectionParams{
		LegalName:               "Dual Registration LLC",
		EntityType:              "LIMITED_LIABILITY_COMPANY",
		RegistrationCountry:     "US",
		RegistrationState:       "DE",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{ein, stateReg},
	}
	if err := comp.CorrectLegalIdentifiers(params); err != nil {
		t.Fatalf("CorrectLegalIdentifiers() failed: %v", err)
	}

	ids := comp.RegistrationIdentifiers()
	if len(ids) != 2 {
		t.Fatalf("len(RegistrationIdentifiers()) = %d, want 2", len(ids))
	}
	if ids[0].Type != "EIN" || ids[0].Value != "12-3456789" {
		t.Errorf("ids[0] = %+v, want EIN 12-3456789", ids[0])
	}
	if ids[1].Type != "STATE_REGISTRATION_NUMBER" || ids[1].Value != "DE-SR-987654" {
		t.Errorf("ids[1] = %+v, want state number DE-SR-987654", ids[1])
	}

	// Verify deep copy in clone
	clone := comp.DeepCopy()
	if len(clone.RegistrationIdentifiers()) != 2 {
		t.Fatalf("clone.RegistrationIdentifiers() len = %d, want 2", len(clone.RegistrationIdentifiers()))
	}
}

func TestArchivedCompanyMutationsGuarded(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_arch_guard", "Archived Guard Co")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	person := domain.Person{
		ID:        "person_01",
		FirstName: "Jane",
		LastName:  "Doe",
	}
	if err := comp.AddPerson(person); err != nil {
		t.Fatalf("AddPerson() failed: %v", err)
	}

	ident := domain.Identification{
		ID:             "id_01",
		Type:           "PASSPORT",
		IssuingCountry: "US",
		Number:         "11223344",
	}
	if err := comp.AddIdentification("person_01", ident); err != nil {
		t.Fatalf("AddIdentification() failed: %v", err)
	}

	// Archive the company
	if err := comp.Archive(); err != nil {
		t.Fatalf("Archive() failed: %v", err)
	}
	expectedRevision := comp.Revision()

	// Every mutation attempted on an ARCHIVED company must fail with ErrCompanyArchived
	// without changing revision or mutating state.

	if err := comp.UpdateTradingName("New DBA"); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("UpdateTradingName() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.UpdateProfile(domain.BusinessProfile{Description: "New"}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("UpdateProfile() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{LegalName: "Changed"}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("CorrectLegalIdentifiers() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.AddPerson(domain.Person{ID: "p2"}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("AddPerson() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.UpdatePerson(domain.Person{ID: "person_01", FirstName: "Mutated"}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("UpdatePerson() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.RemovePerson("person_01"); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("RemovePerson() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.AddIdentification("person_01", domain.Identification{ID: "id_02", IssuingCountry: "US"}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("AddIdentification() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.ReplaceIdentification("person_01", "id_01", domain.Identification{ID: "id_new", IssuingCountry: "US"}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("ReplaceIdentification() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.RemoveIdentification("person_01", "id_01"); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("RemoveIdentification() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.RecordSubmission(); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("RecordSubmission() error = %v, want %v", err, domain.ErrCompanyArchived)
	}
	if err := comp.Archive(); !errors.Is(err, domain.ErrCompanyAlreadyArchived) {
		t.Errorf("Repeated Archive() error = %v, want %v", err, domain.ErrCompanyAlreadyArchived)
	}

	// Verify revision was not incremented by any failed mutator
	if comp.Revision() != expectedRevision {
		t.Errorf("comp.Revision() = %d, want %d (revision must not increment on guarded errors)", comp.Revision(), expectedRevision)
	}
}

func TestReconstituteCompanyInvariants(t *testing.T) {
	t.Parallel()

	validProfile := domain.BusinessProfile{
		RegisteredAddress: domain.Address{
			Country: "US",
		},
	}
	now := time.Now().UTC()

	// 1. Invalid status enum rejected
	_, err := domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_rec_1",
		Status:              "INVALID_STATUS",
		Revision:            1,
		CreatedAt:           now,
		UpdatedAt:           now,
		BusinessProfile:     validProfile,
		RegistrationCountry: "US",
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidStatus) {
		t.Errorf("ReconstituteCompany() with invalid status error = %v, want %v", err, domain.ErrInvalidStatus)
	}

	// 2. Non-positive revision rejected
	_, err = domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_rec_2",
		Status:              domain.CompanyStatusActive,
		Revision:            0,
		CreatedAt:           now,
		UpdatedAt:           now,
		BusinessProfile:     validProfile,
		RegistrationCountry: "US",
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidRevision) {
		t.Errorf("ReconstituteCompany() with revision 0 error = %v, want %v", err, domain.ErrInvalidRevision)
	}

	// 3. Invalid registration country rejected (non-ISO "ZZ")
	_, err = domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_rec_3",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		CreatedAt:           now,
		UpdatedAt:           now,
		BusinessProfile:     validProfile,
		RegistrationCountry: "ZZ",
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidCountryCode) {
		t.Errorf("ReconstituteCompany() with country 'ZZ' error = %v, want %v", err, domain.ErrInvalidCountryCode)
	}

	// 4. Duplicate Person IDs across company rejected
	_, err = domain.ReconstituteCompany(domain.ReconstituteCompanyParams{
		ID:                  "comp_rec_4",
		Status:              domain.CompanyStatusActive,
		Revision:            1,
		CreatedAt:           now,
		UpdatedAt:           now,
		BusinessProfile:     validProfile,
		RegistrationCountry: "US",
		People: []domain.Person{
			{ID: "person_dup", FirstName: "First"},
			{ID: "person_dup", FirstName: "Second"},
		},
	})
	if err == nil || !errors.Is(err, domain.ErrDuplicatePersonID) {
		t.Errorf("ReconstituteCompany() with duplicate person IDs error = %v, want %v", err, domain.ErrDuplicatePersonID)
	}
}

func TestReplaceIdentificationInvariants(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_replace_test", "Replace Invariant Co")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	person := domain.Person{
		ID:        "p_rep_1",
		FirstName: "John",
		LastName:  "Doe",
	}
	if err := comp.AddPerson(person); err != nil {
		t.Fatalf("AddPerson() failed: %v", err)
	}

	origIdent := domain.Identification{
		ID:             "id_passport_v1",
		Type:           "PASSPORT",
		IssuingCountry: "US",
		Number:         "P10000001",
		IssuedOn:       domain.Date("2015-01-01"),
		ExpiresOn:      domain.Date("2025-01-01"),
	}
	if err := comp.AddIdentification("p_rep_1", origIdent); err != nil {
		t.Fatalf("AddIdentification() failed: %v", err)
	}

	// 1. Same-ID replacement is rejected (replacement must create a new record with a new ID)
	sameIDReplacement := domain.Identification{
		ID:             "id_passport_v1", // Same ID as original
		Type:           "PASSPORT",
		IssuingCountry: "US",
		Number:         "P20000002",
		IssuedOn:       domain.Date("2025-01-01"),
		ExpiresOn:      domain.Date("2035-01-01"),
	}
	err = comp.ReplaceIdentification("p_rep_1", "id_passport_v1", sameIDReplacement)
	if err == nil || !errors.Is(err, domain.ErrReplacementRequiresNewID) {
		t.Errorf("ReplaceIdentification() with same ID error = %v, want %v", err, domain.ErrReplacementRequiresNewID)
	}

	// 2. Snapshot isolation before valid replacement
	snapshot := comp.DeepCopy()

	// 3. Valid replacement with new ID succeeds
	newIDReplacement := domain.Identification{
		ID:             "id_passport_v2", // New ID
		Type:           "PASSPORT",
		IssuingCountry: "US",
		Number:         "P20000002",
		IssuedOn:       domain.Date("2025-01-01"),
		ExpiresOn:      domain.Date("2035-01-01"),
	}
	if err := comp.ReplaceIdentification("p_rep_1", "id_passport_v1", newIDReplacement); err != nil {
		t.Fatalf("ReplaceIdentification() with new ID failed: %v", err)
	}

	// Verify current company has the new identification
	currPerson, _ := comp.Person("p_rep_1")
	if len(currPerson.Identifications) != 1 {
		t.Fatalf("currPerson.Identifications len = %d, want 1", len(currPerson.Identifications))
	}
	if currPerson.Identifications[0].ID != "id_passport_v2" {
		t.Errorf("current ID = %q, want id_passport_v2", currPerson.Identifications[0].ID)
	}

	// Verify prior snapshot retained the original identification
	snapPerson, _ := snapshot.Person("p_rep_1")
	if snapPerson.Identifications[0].ID != "id_passport_v1" {
		t.Errorf("snapshot ID = %q, want id_passport_v1", snapPerson.Identifications[0].ID)
	}
}

func TestValidationWebsiteAndCountryCodesNoPIIEcho(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_val_test", "Val Co")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	// 1. Website validation: must be absolute http/https URL with host
	validSites := []string{
		"https://flowcard.example.com",
		"http://api.flowcard.com/v1",
		"https://sub.domain.co.uk",
	}
	for _, site := range validSites {
		if err := comp.UpdateProfile(domain.BusinessProfile{Website: site}); err != nil {
			t.Errorf("UpdateProfile() failed for valid site %q: %v", site, err)
		}
	}

	invalidSites := []string{
		"javascript:foo.bar",
		"ftp://flowcard.example.com",
		"flowcard.example.com", // missing http/https scheme
		"http://",              // missing host
		"not-a-url",
	}
	for _, site := range invalidSites {
		err := comp.UpdateProfile(domain.BusinessProfile{Website: site})
		if err == nil || !errors.Is(err, domain.ErrInvalidWebsite) {
			t.Errorf("UpdateProfile() for invalid site %q error = %v, want %v", site, err, domain.ErrInvalidWebsite)
		}
	}

	// 2. Country code validation: rejects non-ISO codes like "ZZ" or "12" or lowercase "usa"
	invalidCountries := []string{"ZZ", "12", "usa", "XX", "QQ"}
	for _, c := range invalidCountries {
		err := comp.UpdateProfile(domain.BusinessProfile{
			RegisteredAddress: domain.Address{Country: c},
		})
		if err == nil || !errors.Is(err, domain.ErrInvalidCountryCode) {
			t.Errorf("UpdateProfile() for country %q error = %v, want %v", c, err, domain.ErrInvalidCountryCode)
		}
	}

	// 3. Sensitive PII must NOT be echoed in error strings
	secretEmail := "sensitive-ceo-personal@secret-domain.com"
	err = comp.AddPerson(domain.Person{
		ID:    "p_pii_check",
		Email: "invalid-email-with-no-at",
	})
	if err == nil {
		t.Fatal("AddPerson() expected email error, got nil")
	}
	if strings.Contains(err.Error(), secretEmail) || strings.Contains(err.Error(), "invalid-email-with-no-at") {
		t.Errorf("error string echoed raw email value: %q", err.Error())
	}
}

func TestPersonDateOfBirthValidation(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_dob_test", "DOB Test Co")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	// 1. Valid DOB in past succeeds
	validPastDOB := domain.Date("1990-01-15")
	if err := comp.AddPerson(domain.Person{
		ID:          "p_past",
		DateOfBirth: validPastDOB,
	}); err != nil {
		t.Errorf("AddPerson() with valid past DOB failed: %v", err)
	}

	// 2. Today's date is not "past" -> must be rejected with ErrInvalidDateOfBirth
	now := time.Now().UTC()
	todayDOB := domain.Date(now.Format("2006-01-02"))
	err = comp.AddPerson(domain.Person{
		ID:          "p_today",
		DateOfBirth: todayDOB,
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidDateOfBirth) {
		t.Errorf("AddPerson() with today's date (%s) error = %v, want %v", todayDOB, err, domain.ErrInvalidDateOfBirth)
	}

	// 3. Future date is not "past" -> must be rejected with ErrInvalidDateOfBirth
	futureDOB := domain.Date(now.Add(24 * time.Hour).Format("2006-01-02"))
	err = comp.AddPerson(domain.Person{
		ID:          "p_future",
		DateOfBirth: futureDOB,
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidDateOfBirth) {
		t.Errorf("AddPerson() with future date (%s) error = %v, want %v", futureDOB, err, domain.ErrInvalidDateOfBirth)
	}

	// 4. Malformed date format rejected with ErrInvalidDateFormat
	badFormatDOB := domain.Date("01-15-1990")
	err = comp.AddPerson(domain.Person{
		ID:          "p_bad_format",
		DateOfBirth: badFormatDOB,
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidDateFormat) {
		t.Errorf("AddPerson() with malformed date format error = %v, want %v", err, domain.ErrInvalidDateFormat)
	}
}

func TestDateAndSerializationFormats(t *testing.T) {
	t.Parallel()

	// 1. Date value type parsing and validation
	validDate := domain.Date("2026-11-06")
	if err := validDate.Validate(); err != nil {
		t.Errorf("validDate.Validate() error: %v", err)
	}

	invalidDateFormats := []domain.Date{
		"2026/11/06",
		"06-11-2026",
		"2026-02-31", // invalid calendar date
		"invalid",
	}
	for _, badDate := range invalidDateFormats {
		if err := badDate.Validate(); err == nil || !errors.Is(err, domain.ErrInvalidDateFormat) {
			t.Errorf("Validate() for bad date %q error = %v, want %v", badDate, err, domain.ErrInvalidDateFormat)
		}
	}

	// 2. Full JSON serialization schema contract
	comp, err := domain.NewDraftCompany("comp_schema_check", "Schema Test LLC")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	opAddr := &domain.Address{
		Line1:      "Suite 500",
		Line2:      "Building B",
		City:       "New York",
		State:      "NY",
		PostalCode: "10001",
		Country:    "US",
	}
	profile := domain.BusinessProfile{
		Description:        "Underwriting Platform",
		Website:            "https://flowcard.example.com",
		OperatingAddress:   opAddr,
		OperatingCountries: []string{"US"},
		Products:           "Working capital lending API",
	}
	if err := comp.UpdateProfile(profile); err != nil {
		t.Fatalf("UpdateProfile() failed: %v", err)
	}

	if err := comp.CorrectLegalIdentifiers(domain.LegalIdentifierCorrectionParams{
		LegalName:           "Schema Test LLC",
		EntityType:          "LIMITED_LIABILITY_COMPANY",
		RegistrationCountry: "US",
		RegistrationState:   "DE",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{
				Type:           "EIN",
				Value:          "12-3456789",
				IssuingCountry: "US",
			},
		},
	}); err != nil {
		t.Fatalf("CorrectLegalIdentifiers() failed: %v", err)
	}

	p := domain.Person{
		ID:          "p_json_1",
		FirstName:   "Ada",
		LastName:    "Lovelace",
		DateOfBirth: domain.Date("1985-12-10"),
		Role:        "Chief Technology Officer",
		Identifications: []domain.Identification{
			{
				ID:             "id_pass_1",
				Type:           "PASSPORT",
				IssuingCountry: "US",
				Number:         "L12345678",
				IssuedOn:       domain.Date("2020-05-01"),
				ExpiresOn:      domain.Date("2030-05-01"),
			},
		},
	}
	if err := comp.AddPerson(p); err != nil {
		t.Fatalf("AddPerson() failed: %v", err)
	}

	data, err := json.Marshal(comp)
	if err != nil {
		t.Fatalf("json.Marshal() failed: %v", err)
	}
	rawJSON := string(data)

	// Assert schema names per agreed design
	requiredKeys := []string{
		`"business_profile":`,
		`"line_1":`,
		`"line_2":`,
		`"date_of_birth":"1985-12-10"`,
		`"issued_on":"2020-05-01"`,
		`"expires_on":"2030-05-01"`,
		`"registration_identifiers":`,
	}
	for _, key := range requiredKeys {
		if !strings.Contains(rawJSON, key) {
			t.Errorf("JSON output missing required schema key %s: raw JSON = %s", key, rawJSON)
		}
	}

	// Assert drifted names are absent
	forbiddenKeys := []string{
		`"profile":`,
		`"line1":`,
		`"line2":`,
		`"issued_date":`,
		`"expiry_date":`,
	}
	for _, key := range forbiddenKeys {
		if strings.Contains(rawJSON, key) {
			t.Errorf("JSON output contains drifted/forbidden key %s", key)
		}
	}

	// 3. Unmarshal round-trip restores exact state
	var restored domain.Company
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() failed: %v", err)
	}
	if restored.ID() != comp.ID() {
		t.Errorf("restored.ID() = %q, want %q", restored.ID(), comp.ID())
	}
	if restored.BusinessProfile().Products != comp.BusinessProfile().Products {
		t.Errorf("restored Products = %q, want %q", restored.BusinessProfile().Products, comp.BusinessProfile().Products)
	}
	restoredPerson, ok := restored.Person("p_json_1")
	if !ok {
		t.Fatalf("restored Person p_json_1 not found")
	}
	if restoredPerson.DateOfBirth != domain.Date("1985-12-10") {
		t.Errorf("restored DateOfBirth = %q, want 1985-12-10", restoredPerson.DateOfBirth)
	}
	if len(restoredPerson.Identifications) != 1 {
		t.Fatalf("restored Identifications len = %d, want 1", len(restoredPerson.Identifications))
	}
	if restoredPerson.Identifications[0].IssuedOn != domain.Date("2020-05-01") {
		t.Errorf("restored IssuedOn = %q, want 2020-05-01", restoredPerson.Identifications[0].IssuedOn)
	}
	if restoredPerson.Identifications[0].ExpiresOn != domain.Date("2030-05-01") {
		t.Errorf("restored ExpiresOn = %q, want 2030-05-01", restoredPerson.Identifications[0].ExpiresOn)
	}
}

func TestProductsLengthUnicodeBoundary(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_prod_test", "Product Testing LLC")
	if err != nil {
		t.Fatalf("unexpected error creating company: %v", err)
	}

	// 500 ASCII characters (exact limit)
	exact500ASCII := strings.Repeat("A", 500)
	profile := domain.BusinessProfile{
		Products: exact500ASCII,
	}
	if err := comp.UpdateProfile(profile); err != nil {
		t.Errorf("UpdateProfile() failed for exact 500 ASCII characters: %v", err)
	}

	// 501 ASCII characters (must fail)
	over501ASCII := strings.Repeat("A", 501)
	profile.Products = over501ASCII
	if err := comp.UpdateProfile(profile); err == nil || !errors.Is(err, domain.ErrProductsTooLong) {
		t.Errorf("UpdateProfile() error = %v, want %v", err, domain.ErrProductsTooLong)
	}

	// Multi-byte Unicode: '€' is 3 bytes in UTF-8, but 1 rune.
	// 500 '€' runes = 1500 bytes, but exactly 500 Unicode characters.
	exact500Unicode := strings.Repeat("€", 500)
	profile.Products = exact500Unicode
	if err := comp.UpdateProfile(profile); err != nil {
		t.Errorf("UpdateProfile() failed for 500 multi-byte Unicode characters (1500 bytes): %v", err)
	}

	// 501 multi-byte Unicode runes (must fail)
	over501Unicode := strings.Repeat("€", 501)
	profile.Products = over501Unicode
	if err := comp.UpdateProfile(profile); err == nil || !errors.Is(err, domain.ErrProductsTooLong) {
		t.Errorf("UpdateProfile() for 501 Unicode runes error = %v, want %v", err, domain.ErrProductsTooLong)
	}
}

func TestAllowDuplicateLegalIdentities(t *testing.T) {
	t.Parallel()

	comp1, err := domain.NewDraftCompany("comp_001", "Acme Treasury LLC")
	if err != nil {
		t.Fatalf("failed to create comp1: %v", err)
	}
	comp2, err := domain.NewDraftCompany("comp_002", "Acme Treasury LLC")
	if err != nil {
		t.Fatalf("failed to create comp2: %v", err)
	}

	registrationList := []domain.RegistrationIdentifier{
		{
			Type:           "EIN",
			Value:          "12-3456789",
			IssuingCountry: "US",
		},
	}

	params1 := domain.LegalIdentifierCorrectionParams{
		LegalName:               "Acme Treasury LLC",
		EntityType:              "LIMITED_LIABILITY_COMPANY",
		RegistrationCountry:     "US",
		RegistrationState:       "DE",
		RegistrationIdentifiers: registrationList,
	}
	params2 := domain.LegalIdentifierCorrectionParams{
		LegalName:               "Acme Treasury LLC",
		EntityType:              "LIMITED_LIABILITY_COMPANY",
		RegistrationCountry:     "US",
		RegistrationState:       "DE",
		RegistrationIdentifiers: registrationList,
	}

	if err := comp1.CorrectLegalIdentifiers(params1); err != nil {
		t.Fatalf("comp1.CorrectLegalIdentifiers() failed: %v", err)
	}
	if err := comp2.CorrectLegalIdentifiers(params2); err != nil {
		t.Fatalf("comp2.CorrectLegalIdentifiers() failed: %v", err)
	}

	if comp1.LegalName() != comp2.LegalName() {
		t.Errorf("comp1 and comp2 legal names do not match: %q vs %q", comp1.LegalName(), comp2.LegalName())
	}
	if len(comp1.RegistrationIdentifiers()) != len(comp2.RegistrationIdentifiers()) {
		t.Errorf("comp1 and comp2 registration identifiers count does not match")
	}
}

func TestFreeTextRole(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_role_test", "Role Co")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	roles := []string{
		"Head of Treasury & Capital Markets",
		"VP of Engineering and Founder",
		"Managing Director (US & APAC)",
	}

	for i, r := range roles {
		person := domain.Person{
			ID:        string(rune('A' + i)),
			FirstName: "Jane",
			LastName:  "Doe",
			Role:      r,
		}
		if err := comp.AddPerson(person); err != nil {
			t.Fatalf("AddPerson() failed for role %q: %v", r, err)
		}
		stored, ok := comp.Person(person.ID)
		if !ok {
			t.Fatalf("Person(%q) not found", person.ID)
		}
		if stored.Role != r {
			t.Errorf("stored.Role = %q, want %q", stored.Role, r)
		}
	}
}

func TestDeepCopyAndAliasingSafety(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_alias_test", "Safe Aliasing Corp")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	opAddr := &domain.Address{
		Line1:   "100 Market St",
		City:    "San Francisco",
		State:   "CA",
		Country: "US",
	}
	countries := []string{"US", "GB"}
	profile := domain.BusinessProfile{
		OperatingAddress:   opAddr,
		OperatingCountries: countries,
	}

	if err := comp.UpdateProfile(profile); err != nil {
		t.Fatalf("UpdateProfile() failed: %v", err)
	}

	// 1. External modification of input variables after UpdateProfile must not affect Company
	opAddr.Line1 = "MUTATED EXTERNAL"
	countries[0] = "CA"
	if comp.BusinessProfile().OperatingAddress.Line1 == "MUTATED EXTERNAL" {
		t.Errorf("Company internal operating address was aliased to external pointer")
	}
	if comp.BusinessProfile().OperatingCountries[0] == "CA" {
		t.Errorf("Company internal operating countries slice was aliased to external slice")
	}

	// 2. Modifying the return value from BusinessProfile() must not affect internal state
	prof1 := comp.BusinessProfile()
	prof1.OperatingAddress.Line1 = "MUTATED FROM GETTER"
	prof1.OperatingCountries[0] = "FR"
	if comp.BusinessProfile().OperatingAddress.Line1 == "MUTATED FROM GETTER" {
		t.Errorf("Company internal operating address was mutated via getter return pointer")
	}
	if comp.BusinessProfile().OperatingCountries[0] == "FR" {
		t.Errorf("Company internal operating countries was mutated via getter return slice")
	}

	// 3. Person and Identifications slice aliasing
	ident := domain.Identification{
		ID:             "ident_01",
		Type:           "PASSPORT",
		IssuingCountry: "US",
		Number:         "12345678",
		IssuedOn:       domain.Date("2020-01-01"),
		ExpiresOn:      domain.Date("2030-01-01"),
	}
	p := domain.Person{
		ID:              "person_01",
		FirstName:       "Alice",
		LastName:        "Smith",
		Identifications: []domain.Identification{ident},
	}
	if err := comp.AddPerson(p); err != nil {
		t.Fatalf("AddPerson() failed: %v", err)
	}

	// Modifying getter People() slice must not mutate Company
	peopleSlice := comp.People()
	peopleSlice[0].FirstName = "HACKED_NAME"
	peopleSlice[0].Identifications[0].Number = "HACKED_NUMBER"

	recheckedPerson, _ := comp.Person("person_01")
	if recheckedPerson.FirstName == "HACKED_NAME" {
		t.Errorf("Person firstName was mutated via People() getter slice")
	}
	if recheckedPerson.Identifications[0].Number == "HACKED_NUMBER" {
		t.Errorf("Identification number was mutated via People() getter slice")
	}

	// 4. DeepCopy() creates completely detached clone
	clone := comp.DeepCopy()
	if clone.ID() != comp.ID() {
		t.Errorf("clone.ID() = %q, want %q", clone.ID(), comp.ID())
	}
	_ = clone.UpdateTradingName("New Clone DBA")
	if comp.TradingName() == "New Clone DBA" {
		t.Errorf("Mutating clone trading name mutated original company")
	}
}

func TestLegalIdentifierCorrectionAuditing(t *testing.T) {
	t.Parallel()

	comp, err := domain.NewDraftCompany("comp_audit_test", "Initial Legal Name")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}

	// 1. Before submission: legal identifier correction is free without actor or reason
	initialParams := domain.LegalIdentifierCorrectionParams{
		LegalName:           "Corrected Name Draft",
		EntityType:          "LLC",
		RegistrationCountry: "US",
		RegistrationState:   "NY",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{
				Type:           "EIN",
				Value:          "99-8887777",
				IssuingCountry: "US",
			},
		},
	}
	if err := comp.CorrectLegalIdentifiers(initialParams); err != nil {
		t.Fatalf("CorrectLegalIdentifiers() before submission failed: %v", err)
	}
	if comp.LegalName() != "Corrected Name Draft" {
		t.Errorf("LegalName = %q, want Corrected Name Draft", comp.LegalName())
	}
	if len(comp.LegalIdentifierAuditTrail()) != 0 {
		t.Errorf("len(LegalIdentifierAuditTrail()) = %d, want 0 before submission", len(comp.LegalIdentifierAuditTrail()))
	}

	// 2. Mark submission: Company has entered platform onboarding
	if err := comp.RecordSubmission(); err != nil {
		t.Fatalf("RecordSubmission() failed: %v", err)
	}
	if !comp.HasSubmittedHistory() {
		t.Fatalf("HasSubmittedHistory() = false, want true")
	}

	// 3. After submission: attempting correction without actor returns ErrActorRequired
	submittedParamsMissingActor := domain.LegalIdentifierCorrectionParams{
		LegalName: "Submitted Name Change",
		Actor:     "",
		Reason:    "Typo correction in charter",
	}
	if err := comp.CorrectLegalIdentifiers(submittedParamsMissingActor); err == nil || !errors.Is(err, domain.ErrActorRequired) {
		t.Errorf("CorrectLegalIdentifiers() without actor error = %v, want %v", err, domain.ErrActorRequired)
	}

	// 4. After submission: attempting correction without reason returns ErrReasonRequired
	submittedParamsMissingReason := domain.LegalIdentifierCorrectionParams{
		LegalName: "Submitted Name Change",
		Actor:     "compliance_officer_1",
		Reason:    "   ",
	}
	if err := comp.CorrectLegalIdentifiers(submittedParamsMissingReason); err == nil || !errors.Is(err, domain.ErrReasonRequired) {
		t.Errorf("CorrectLegalIdentifiers() without reason error = %v, want %v", err, domain.ErrReasonRequired)
	}

	// 5. Valid audited correction after submission
	validAuditedParams := domain.LegalIdentifierCorrectionParams{
		LegalName:           "Official Legal Name LLC",
		EntityType:          "CORPORATION",
		RegistrationCountry: "US",
		RegistrationState:   "DE",
		RegistrationIdentifiers: []domain.RegistrationIdentifier{
			{
				Type:           "EIN",
				Value:          "11-2223333",
				IssuingCountry: "US",
			},
		},
		Actor:  "compliance_officer_1",
		Reason: "Delaware certificate of amendment filed",
	}
	if err := comp.CorrectLegalIdentifiers(validAuditedParams); err != nil {
		t.Fatalf("CorrectLegalIdentifiers() with actor and reason failed: %v", err)
	}

	if comp.LegalName() != "Official Legal Name LLC" {
		t.Errorf("LegalName = %q, want Official Legal Name LLC", comp.LegalName())
	}
	if comp.EntityType() != "CORPORATION" {
		t.Errorf("EntityType = %q, want CORPORATION", comp.EntityType())
	}

	auditTrail := comp.LegalIdentifierAuditTrail()
	if len(auditTrail) != 1 {
		t.Fatalf("len(LegalIdentifierAuditTrail) = %d, want 1", len(auditTrail))
	}
	record := auditTrail[0]
	if record.Actor != "compliance_officer_1" {
		t.Errorf("record.Actor = %q, want compliance_officer_1", record.Actor)
	}
	if record.Reason != "Delaware certificate of amendment filed" {
		t.Errorf("record.Reason = %q, want Delaware certificate of amendment filed", record.Reason)
	}
	if record.OldLegalName != "Corrected Name Draft" {
		t.Errorf("record.OldLegalName = %q, want Corrected Name Draft", record.OldLegalName)
	}
	if record.NewLegalName != "Official Legal Name LLC" {
		t.Errorf("record.NewLegalName = %q, want Official Legal Name LLC", record.NewLegalName)
	}
	if len(record.OldRegistrationIdentifiers) != 1 || record.OldRegistrationIdentifiers[0].Value != "99-8887777" {
		t.Errorf("record.OldRegistrationIdentifiers not preserved properly")
	}
	if len(record.NewRegistrationIdentifiers) != 1 || record.NewRegistrationIdentifiers[0].Value != "11-2223333" {
		t.Errorf("record.NewRegistrationIdentifiers not preserved properly")
	}
}

func TestPermanentDeletionEligibility(t *testing.T) {
	t.Parallel()

	// 1. Draft company that has never entered onboarding can be permanently deleted
	draftComp, err := domain.NewDraftCompany("comp_draft_del", "Deleteable Draft Co")
	if err != nil {
		t.Fatalf("failed to create draft company: %v", err)
	}

	if !draftComp.CanDelete() {
		t.Errorf("draftComp.CanDelete() = false, want true")
	}
	if err := draftComp.Delete(); err != nil {
		t.Errorf("draftComp.Delete() unexpected error: %v", err)
	}

	// 2. Company with submitted history can NEVER be permanently deleted
	submittedComp, err := domain.NewDraftCompany("comp_sub_del", "Submitted Co")
	if err != nil {
		t.Fatalf("failed to create company: %v", err)
	}
	if err := submittedComp.RecordSubmission(); err != nil {
		t.Fatalf("RecordSubmission() failed: %v", err)
	}

	if submittedComp.CanDelete() {
		t.Errorf("submittedComp.CanDelete() = true, want false")
	}
	if err := submittedComp.Delete(); err == nil || !errors.Is(err, domain.ErrCannotDeleteSubmittedCompany) {
		t.Errorf("submittedComp.Delete() error = %v, want %v", err, domain.ErrCannotDeleteSubmittedCompany)
	}
}
