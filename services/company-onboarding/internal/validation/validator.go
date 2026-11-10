// Package validation provides standardized request and saved-data validation
// using go-playground/validator/v10, formatting native rule codes and JSON paths
// while strictly omitting sensitive values from error outputs.
package validation

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
)

// FieldError represents a standardized field validation failure.
// Sensitive user input is strictly omitted to prevent credential/PII leaks.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}

// ValidationResult represents the output of a saved-data completeness verification.
// Success code (e.g. VALIDATION_PASSED) drives contract without redundant boolean property.
type ValidationResult struct {
	Code                string       `json:"code"`
	Message             string       `json:"message,omitempty"`
	ApplicationRevision int          `json:"application_revision,omitempty"`
	CompanyRevision     int          `json:"company_revision,omitempty"`
	Errors              []FieldError `json:"errors,omitempty"`
}

// PlatformContractValidator validates external platform gates (e.g. US regulated roles,
// country document requirement matrix) before an application is declared ready.
type PlatformContractValidator interface {
	ValidatePlatformContract(comp *domain.Company, app *domain.OnboardingApplication) error
}

// ErrPlatformContractIncomplete is returned when required platform rules remain unverified.
var ErrPlatformContractIncomplete = errors.New("platform contract incomplete: US regulated roles and country document requirement matrix remain unverified pending external platform evidence")

// UnverifiedPlatformContractGate is the default production gate enforcing Task 1 external gates:
// US regulated roles and country-specific document requirement matrix remain unverified.
type UnverifiedPlatformContractGate struct{}

// ValidatePlatformContract always returns ErrPlatformContractIncomplete by default.
func (UnverifiedPlatformContractGate) ValidatePlatformContract(comp *domain.Company, app *domain.OnboardingApplication) error {
	return ErrPlatformContractIncomplete
}

// Validator wraps go-playground validator with JSON tag name awareness and custom rules.
type Validator struct {
	validate *validator.Validate
}

var iso3166Alpha2Regex = regexp.MustCompile(`^[A-Z]{2}$`)

// NewValidator initializes a Validator configured with JSON tag mapping and sanitized rules.
func NewValidator() *Validator {
	v := validator.New(validator.WithRequiredStructEnabled())

	// Use json tag names for field paths in error messages
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		tag := fld.Tag.Get("json")
		if tag == "-" {
			return ""
		}
		parts := strings.Split(tag, ",")
		return parts[0]
	})

	// Custom validation for ISO 3166-1 alpha-2 country codes
	_ = v.RegisterValidation("iso3166_alpha2", func(fl validator.FieldLevel) bool {
		s := strings.TrimSpace(fl.Field().String())
		return iso3166Alpha2Regex.MatchString(strings.ToUpper(s))
	})

	// Custom validation for calendar date YYYY-MM-DD in the past
	_ = v.RegisterValidation("past_date", func(fl validator.FieldLevel) bool {
		s := strings.TrimSpace(fl.Field().String())
		if s == "" {
			return false
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return false
		}
		now := time.Now().UTC()
		todayUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return t.Before(todayUTC)
	})

	return &Validator{validate: v}
}

// ValidateStruct validates a struct and formats all field errors using JSON paths.
func (v *Validator) ValidateStruct(s interface{}) []FieldError {
	err := v.validate.Struct(s)
	if err == nil {
		return nil
	}
	return v.FormatValidationErrors(err)
}

// FormatValidationErrors converts validator.ValidationErrors into sanitized FieldErrors.
// Values are never echoed to prevent leakage of PII or sensitive credentials.
func (v *Validator) FormatValidationErrors(err error) []FieldError {
	if err == nil {
		return nil
	}

	var valErrs validator.ValidationErrors
	if !errors.As(err, &valErrs) {
		return []FieldError{{
			Field:   "body",
			Code:    "malformed_input",
			Message: "invalid or malformed input format",
		}}
	}

	out := make([]FieldError, 0, len(valErrs))
	for _, fe := range valErrs {
		fieldPath := cleanFieldNamespace(fe.Namespace())
		rule := fe.Tag()
		param := fe.Param()

		msg := ruleMessage(rule, param)
		out = append(out, FieldError{
			Field:   fieldPath,
			Code:    rule,
			Param:   param,
			Message: msg,
		})
	}
	return out
}

func cleanFieldNamespace(ns string) string {
	// Namespace comes in as "StructName.field.subfield" or "StructName.array[0].field"
	dot := strings.Index(ns, ".")
	if dot == -1 {
		return strings.ToLower(ns)
	}
	return ns[dot+1:]
}

func ruleMessage(rule, param string) string {
	switch rule {
	case "required":
		return "field is required"
	case "min":
		return fmt.Sprintf("value must be at least %s in length", param)
	case "max":
		return fmt.Sprintf("value must be at most %s in length", param)
	case "len":
		return fmt.Sprintf("value must be exactly %s in length", param)
	case "email":
		return "must be a valid email address"
	case "url":
		return "must be a valid URL"
	case "iso3166_alpha2":
		return "must be a valid 2-letter ISO 3166-1 alpha-2 country code"
	case "past_date":
		return "must be a valid calendar date (YYYY-MM-DD) in the past"
	case "oneof":
		return fmt.Sprintf("must be one of [%s]", param)
	default:
		return fmt.Sprintf("failed validation rule: %s", rule)
	}
}

// VerifyOnboardingReadiness performs local completeness validation on saved Company,
// Application, and Documents, followed by an explicit PlatformContractValidator gate.
// Returns VALIDATION_PASSED (200), VALIDATION_FAILED (422), or PLATFORM_CONTRACT_INCOMPLETE (422).
func VerifyOnboardingReadiness(comp *domain.Company, app *domain.OnboardingApplication, gate PlatformContractValidator) *ValidationResult {
	if gate == nil {
		gate = UnverifiedPlatformContractGate{}
	}

	if comp == nil {
		return &ValidationResult{
			Code:    "VALIDATION_FAILED",
			Message: "company reference is missing",
			Errors:  []FieldError{{Field: "company", Code: "required", Message: "company reference is missing"}},
		}
	}
	if app == nil {
		return &ValidationResult{
			Code:    "VALIDATION_FAILED",
			Message: "application reference is missing",
			Errors:  []FieldError{{Field: "application", Code: "required", Message: "application reference is missing"}},
		}
	}

	var errs []FieldError

	// 1. Company completeness checks
	if strings.TrimSpace(comp.LegalName()) == "" {
		errs = append(errs, FieldError{Field: "company.legal_name", Code: "required", Message: "legal name is required"})
	}
	if strings.TrimSpace(comp.EntityType()) == "" {
		errs = append(errs, FieldError{Field: "company.entity_type", Code: "required", Message: "entity type is required"})
	}
	regCountry := strings.ToUpper(strings.TrimSpace(comp.RegistrationCountry()))
	if regCountry == "" {
		errs = append(errs, FieldError{Field: "company.registration_country", Code: "required", Message: "registration country is required"})
	} else if !iso3166Alpha2Regex.MatchString(regCountry) {
		errs = append(errs, FieldError{Field: "company.registration_country", Code: "iso3166_alpha2", Message: "must be a valid 2-letter ISO 3166-1 alpha-2 country code"})
	} else if regCountry != "US" {
		errs = append(errs, FieldError{Field: "company.registration_country", Code: "unsupported_jurisdiction", Message: "only US registered businesses are supported for platform onboarding"})
	}

	profile := comp.BusinessProfile()
	if strings.TrimSpace(profile.Products) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.products", Code: "required", Message: "goods and services description is required for platform onboarding"})
	}
	if strings.TrimSpace(profile.Website) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.website", Code: "required", Message: "business website is required"})
	} else {
		u, err := url.ParseRequestURI(profile.Website)
		if err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, FieldError{Field: "company.business_profile.website", Code: "url", Message: "must be a valid URL"})
		}
	}
	if strings.TrimSpace(profile.Description) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.description", Code: "required", Message: "business description is required"})
	}

	// Registered Address
	addr := profile.RegisteredAddress
	if strings.TrimSpace(addr.Line1) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.registered_address.line_1", Code: "required", Message: "address line 1 is required"})
	}
	if strings.TrimSpace(addr.City) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.registered_address.city", Code: "required", Message: "city is required"})
	}
	if strings.TrimSpace(addr.State) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.registered_address.state", Code: "required", Message: "state is required"})
	}
	if strings.TrimSpace(addr.PostalCode) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.registered_address.postal_code", Code: "required", Message: "postal code is required"})
	}
	if strings.TrimSpace(addr.Country) == "" {
		errs = append(errs, FieldError{Field: "company.business_profile.registered_address.country", Code: "required", Message: "country is required"})
	}

	// Registration Identifiers
	regIDs := comp.RegistrationIdentifiers()
	if len(regIDs) == 0 {
		errs = append(errs, FieldError{Field: "company.registration_identifiers", Code: "required", Message: "at least one registration identifier is required"})
	} else {
		for i, rid := range regIDs {
			prefix := fmt.Sprintf("company.registration_identifiers[%d]", i)
			if strings.TrimSpace(rid.Type) == "" {
				errs = append(errs, FieldError{Field: prefix + ".type", Code: "required", Message: "identifier type is required"})
			}
			if strings.TrimSpace(rid.Value) == "" {
				errs = append(errs, FieldError{Field: prefix + ".value", Code: "required", Message: "identifier value is required"})
			}
			if strings.TrimSpace(rid.IssuingCountry) == "" {
				errs = append(errs, FieldError{Field: prefix + ".issuing_country", Code: "required", Message: "issuing country is required"})
			}
		}
	}

	// People completeness
	people := comp.People()
	if len(people) == 0 {
		errs = append(errs, FieldError{Field: "company.people", Code: "required", Message: "at least one associated person is required"})
	} else {
		for i, p := range people {
			prefix := fmt.Sprintf("company.people[%d]", i)
			if strings.TrimSpace(p.FirstName) == "" {
				errs = append(errs, FieldError{Field: prefix + ".first_name", Code: "required", Message: "first name is required"})
			}
			if strings.TrimSpace(p.LastName) == "" {
				errs = append(errs, FieldError{Field: prefix + ".last_name", Code: "required", Message: "last name is required"})
			}
			if strings.TrimSpace(p.Role) == "" {
				errs = append(errs, FieldError{Field: prefix + ".role", Code: "required", Message: "role is required"})
			}
			if p.DateOfBirth == "" {
				errs = append(errs, FieldError{Field: prefix + ".date_of_birth", Code: "required", Message: "date of birth is required"})
			} else {
				t, err := time.Parse("2006-01-02", string(p.DateOfBirth))
				if err != nil {
					errs = append(errs, FieldError{Field: prefix + ".date_of_birth", Code: "past_date", Message: "must be a valid calendar date (YYYY-MM-DD)"})
				} else {
					now := time.Now().UTC()
					todayUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
					if !t.Before(todayUTC) {
						errs = append(errs, FieldError{Field: prefix + ".date_of_birth", Code: "past_date", Message: "date of birth must be in the past"})
					}
				}
			}

			if len(p.Identifications) == 0 {
				errs = append(errs, FieldError{Field: prefix + ".identifications", Code: "required", Message: "at least one identification is required"})
			} else {
				for j, ident := range p.Identifications {
					idPrefix := fmt.Sprintf("%s.identifications[%d]", prefix, j)
					if strings.TrimSpace(ident.Type) == "" {
						errs = append(errs, FieldError{Field: idPrefix + ".type", Code: "required", Message: "identification type is required"})
					}
					if strings.TrimSpace(ident.Number) == "" {
						errs = append(errs, FieldError{Field: idPrefix + ".number", Code: "required", Message: "identification number is required"})
					}
					if strings.TrimSpace(ident.IssuingCountry) == "" {
						errs = append(errs, FieldError{Field: idPrefix + ".issuing_country", Code: "required", Message: "issuing country is required"})
					}
				}
			}
		}
	}

	// 2. Application Onboarding Details & Customer Agreements checks (paths: platform.onboarding_details...)
	details := app.Platform().OnboardingDetails
	if strings.TrimSpace(details.PrimaryContactPersonID) == "" {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.primary_contact_person_id", Code: "required", Message: "primary contact person ID is required"})
	} else {
		p, ok := comp.Person(details.PrimaryContactPersonID)
		if !ok {
			errs = append(errs, FieldError{Field: "platform.onboarding_details.primary_contact_person_id", Code: "exists", Message: "primary contact person does not exist in company"})
		} else {
			if strings.TrimSpace(p.Email) == "" {
				errs = append(errs, FieldError{Field: "platform.onboarding_details.primary_contact.email", Code: "required", Message: "primary contact email is required"})
			}
			if strings.TrimSpace(p.Phone) == "" {
				errs = append(errs, FieldError{Field: "platform.onboarding_details.primary_contact.phone", Code: "required", Message: "primary contact phone number is required"})
			}
		}
	}

	agreements := details.CustomerAgreements
	if !agreements.AgreedToTermsAndConditions {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.customer_agreements.agreed_to_terms_and_conditions", Code: "required", Message: "terms and conditions must be explicitly agreed to"})
	}
	if !agreements.AgreedToDataUsage {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.customer_agreements.agreed_to_data_usage", Code: "required", Message: "data usage policy must be explicitly agreed to"})
	}
	if agreements.TermsAndConditions == nil || agreements.TermsAndConditions.AgreedAt.IsZero() {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.customer_agreements.terms_and_conditions.agreed_at", Code: "required", Message: "agreement timestamp is required"})
	}

	// 3. Document format checks for any uploaded documents (path: documents...)
	for i, d := range app.Documents() {
		dPrefix := fmt.Sprintf("documents[%d]", i)
		if err := d.Validate(); err != nil {
			errs = append(errs, FieldError{Field: dPrefix, Code: "invalid_document", Message: "document fails local format or size policy"})
		}
	}

	// 4. Fail closed on unsupported platform checks
	platformName := strings.ToUpper(strings.TrimSpace(app.Platform().Name))
	if platformName != "AIRWALLEX" {
		errs = append(errs, FieldError{Field: "platform.name", Code: "unsupported_platform", Message: "platform is unsupported; fail-closed on unverified platform checks"})
	}

	if len(errs) > 0 {
		return &ValidationResult{
			Code:                "VALIDATION_FAILED",
			Message:             "onboarding data validation failed",
			ApplicationRevision: app.Revision(),
			CompanyRevision:     comp.Revision(),
			Errors:              errs,
		}
	}

	// 5. Injected PlatformContractValidator gate
	// Default gate enforces Task 1 external gates: US regulated roles and country-specific
	// document matrix remain unverified, so verification cannot claim success.
	if err := gate.ValidatePlatformContract(comp, app); err != nil {
		return &ValidationResult{
			Code:                "PLATFORM_CONTRACT_INCOMPLETE",
			Message:             err.Error(),
			ApplicationRevision: app.Revision(),
			CompanyRevision:     comp.Revision(),
		}
	}

	return &ValidationResult{
		Code:                "VALIDATION_PASSED",
		Message:             "onboarding data and platform contract readiness verified",
		ApplicationRevision: app.Revision(),
		CompanyRevision:     comp.Revision(),
	}
}
