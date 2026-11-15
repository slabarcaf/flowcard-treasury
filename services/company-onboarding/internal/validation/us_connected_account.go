package validation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
)

var (
	monthlyRevenuePattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
	currencyCodePattern   = regexp.MustCompile(`^[A-Z]{3}$`)
)

var supportedUSProductReferences = map[string]struct{}{
	"ACCEPT_ONLINE_PAYMENTS":       {},
	"COLLECT_MARKETPLACE_PROCEEDS": {},
	"RECEIVE_TRANSFERS":            {},
	"GET_PAID":                     {},
	"CONVERT_FUNDS":                {},
	"MAKE_TRANSFERS":               {},
	"CREATE_CARDS":                 {},
	"MANAGE_EXPENSES":              {},
	"USE_AWX_API":                  {},
	"TRANSFER_CNY_INBOUND":         {},
}

var supportedPlatformRoles = map[string]struct{}{
	"AUTHORISED_PERSON":    {},
	"BENEFICIAL_OWNER":     {},
	"CONTROLLING_PERSON":   {},
	"DIRECTOR":             {},
	"LEGAL_REPRESENTATIVE": {},
	"SIGNATORY":            {},
}

var supportedBusinessDocumentTags = map[string]struct{}{
	"ASSUMED_NAME_CERTIFICATE":     {},
	"CERTIFICATE_OF_INCORPORATION": {},
	"CONFIRMATION_STATEMENT":       {},
	"EIN_LETTER":                   {},
	"OPERATING_AGREEMENT":          {},
	"PARTNERSHIP_AGREEMENT":        {},
	"SUPPORTIVE_OTHER":             {},
}

func validateUSConnectedAccountData(company *domain.Company, application *domain.OnboardingApplication) []FieldError {
	var errs []FieldError
	profile := company.BusinessProfile()
	app := application.Platform().OnboardingDetails

	if !isSupportedUSBusinessStructure(company.EntityType()) {
		errs = append(errs, FieldError{
			Field:   "company.entity_type",
			Code:    "unsupported_entity_type",
			Message: "Airwallex US onboarding is configured for LLC, corporation, partnership, and registered sole proprietor structures",
		})
	}

	if profile.OperatingAddress == nil {
		errs = append(errs, FieldError{Field: "company.business_profile.operating_address", Code: "required", Message: "business operating address is required"})
	} else {
		address := profile.OperatingAddress
		for _, field := range []struct {
			name  string
			value string
		}{
			{"line_1", address.Line1},
			{"city", address.City},
			{"state", address.State},
			{"postal_code", address.PostalCode},
		} {
			if strings.TrimSpace(field.value) == "" {
				errs = append(errs, FieldError{
					Field:   "company.business_profile.operating_address." + field.name,
					Code:    "required",
					Message: "business operating address field is required",
				})
			}
		}
		if strings.ToUpper(strings.TrimSpace(address.Country)) != "US" {
			errs = append(errs, FieldError{Field: "company.business_profile.operating_address.country", Code: "unsupported_jurisdiction", Message: "Airwallex US onboarding requires a US business operating address"})
		}
	}

	if len(profile.OperatingCountries) == 0 {
		errs = append(errs, FieldError{Field: "company.business_profile.operating_countries", Code: "required", Message: "select at least one country where the business actually operates"})
	} else {
		for i, country := range profile.OperatingCountries {
			if !iso3166Alpha2Regex.MatchString(strings.ToUpper(strings.TrimSpace(country))) {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("company.business_profile.operating_countries[%d]", i),
					Code:    "iso3166_alpha2",
					Message: "operating country must be a 2-letter ISO country code",
				})
			}
		}
	}

	if !hasUSBusinessIdentifier(company.RegistrationIdentifiers()) {
		errs = append(errs, FieldError{Field: "company.registration_identifiers", Code: "required_ein", Message: "a US business registration identifier of type EIN is required"})
	}

	if strings.TrimSpace(app.IndustryCategoryCode) == "" {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.industry_category_code", Code: "required", Message: "select the Airwallex industry category"})
	}
	if len(app.IntendedProducts) == 0 {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.intended_products", Code: "required", Message: "select at least one intended Airwallex product"})
	} else {
		for i, product := range app.IntendedProducts {
			if _, ok := supportedUSProductReferences[strings.ToUpper(strings.TrimSpace(product))]; !ok {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("platform.onboarding_details.intended_products[%d]", i),
					Code:    "unsupported_product",
					Message: "select a supported Airwallex product code",
				})
			}
		}
	}
	amount := strings.TrimSpace(app.EstimatedRevenue.Amount)
	if !monthlyRevenuePattern.MatchString(amount) {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.estimated_monthly_revenue.amount", Code: "required", Message: "enter a non-negative monthly revenue amount"})
	}
	currency := strings.ToUpper(strings.TrimSpace(app.EstimatedRevenue.Currency))
	if !currencyCodePattern.MatchString(currency) {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.estimated_monthly_revenue.currency", Code: "required", Message: "select a 3-letter revenue currency"})
	}

	platformPeople := make(map[string]struct{}, len(app.People))
	roleCoverage := make(map[string]bool)
	if len(app.People) == 0 {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.people", Code: "required", Message: "add the people who own or control the business and an authorized representative"})
	}
	for i, details := range app.People {
		prefix := fmt.Sprintf("platform.onboarding_details.people[%d]", i)
		if _, duplicate := platformPeople[details.PersonID]; duplicate {
			errs = append(errs, FieldError{Field: prefix + ".person_id", Code: "duplicate", Message: "a person may appear only once in the Airwallex business-person list"})
		}
		platformPeople[details.PersonID] = struct{}{}
		person, found := company.Person(details.PersonID)
		if !found {
			errs = append(errs, FieldError{Field: prefix + ".person_id", Code: "exists", Message: "select a person saved on this company"})
			continue
		}
		if len(details.PlatformRoles) == 0 {
			errs = append(errs, FieldError{Field: prefix + ".platform_roles", Code: "required", Message: "select at least one Airwallex business-person role"})
		}
		for roleIndex, role := range details.PlatformRoles {
			code := strings.ToUpper(strings.TrimSpace(role))
			if _, ok := supportedPlatformRoles[code]; !ok {
				errs = append(errs, FieldError{
					Field:   fmt.Sprintf("%s.platform_roles[%d]", prefix, roleIndex),
					Code:    "unsupported_role",
					Message: "select a supported Airwallex business-person role",
				})
				continue
			}
			roleCoverage[code] = true
		}
		if strings.TrimSpace(person.Nationality) == "" {
			errs = append(errs, FieldError{Field: "company.people.nationality", Code: "required", Message: "nationality is required for each person submitted to Airwallex"})
		}
		if strings.TrimSpace(person.Phone) == "" {
			errs = append(errs, FieldError{Field: "company.people.phone", Code: "required", Message: "phone number is required for each person submitted to Airwallex"})
		}
		if len(person.Identifications) == 0 {
			errs = append(errs, FieldError{Field: "company.people.identifications", Code: "required", Message: "add a government-issued identification for each person submitted to Airwallex"})
		}
		if len(person.Identifications) > 2 {
			errs = append(errs, FieldError{Field: "company.people.identifications", Code: "unsupported_count", Message: "select at most one primary and one secondary identification"})
		}
		primaryID := details.PrimaryIdentificationID
		if primaryID == "" && len(person.Identifications) == 1 {
			primaryID = person.Identifications[0].ID
		}
		if _, found := findDomainIdentification(person.Identifications, primaryID); !found {
			errs = append(errs, FieldError{Field: prefix + ".primary_identification_id", Code: "required", Message: "select the identification to send as primary"})
		}
		if len(person.Identifications) == 2 && details.SecondaryIdentificationID == "" {
			errs = append(errs, FieldError{Field: prefix + ".secondary_identification_id", Code: "required", Message: "select the secondary identification explicitly"})
		}
		if details.SecondaryIdentificationID != "" {
			if _, found := findDomainIdentification(person.Identifications, details.SecondaryIdentificationID); !found {
				errs = append(errs, FieldError{Field: prefix + ".secondary_identification_id", Code: "exists", Message: "select a saved identification for the secondary ID"})
			} else if details.SecondaryIdentificationID == primaryID {
				errs = append(errs, FieldError{Field: prefix + ".secondary_identification_id", Code: "distinct", Message: "primary and secondary identifications must differ"})
			}
		}
		if person.Nationality == "US" {
			if !hasUSPersonTaxID(person.Identifications) {
				errs = append(errs, FieldError{Field: "company.people.identifications", Code: "required_tax_id", Message: "a US person must provide an SSN or ITIN"})
			}
			if primary, found := findDomainIdentification(person.Identifications, primaryID); found && !isUSPersonTaxID(primary.Type) {
				errs = append(errs, FieldError{Field: prefix + ".primary_identification_id", Code: "tax_id_required", Message: "select the US person’s SSN or ITIN as the primary identification"})
			}
		}
		address := details.ResidentialAddress
		for _, field := range []struct {
			name  string
			value string
		}{
			{"line_1", address.Line1},
			{"city", address.City},
			{"postal_code", address.PostalCode},
			{"country", address.Country},
		} {
			if strings.TrimSpace(field.value) == "" {
				errs = append(errs, FieldError{Field: prefix + ".residential_address." + field.name, Code: "required", Message: "residential address field is required"})
			}
		}
		if strings.EqualFold(address.Country, "US") && strings.TrimSpace(address.State) == "" {
			errs = append(errs, FieldError{Field: prefix + ".residential_address.state", Code: "required", Message: "US residential address state is required"})
		}
	}

	if !roleCoverage["AUTHORISED_PERSON"] {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.people.platform_roles", Code: "required_authorised_person", Message: "include one authorized representative"})
	}
	if !roleCoverage["BENEFICIAL_OWNER"] {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.people.platform_roles", Code: "required_beneficial_owner", Message: "include all beneficial owners"})
	}
	if isUSCompanyStructure(company.EntityType()) && !roleCoverage["CONTROLLING_PERSON"] {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.people.platform_roles", Code: "required_controlling_person", Message: "include a controlling person for this business structure"})
	}

	if requiresUSPrimaryContactMobile(app.IntendedProducts) && strings.TrimSpace(primaryContactPhone(company, app.PrimaryContactPersonID)) == "" {
		errs = append(errs, FieldError{Field: "platform.onboarding_details.primary_contact_person_id", Code: "primary_contact_mobile_required", Message: "select a primary contact with a mobile number for the selected Airwallex products"})
	}

	errs = append(errs, validateRequiredUSBusinessDocuments(company, application)...)
	for _, document := range application.Documents() {
		if document.PersonID == "" {
			if _, ok := supportedBusinessDocumentTags[strings.ToUpper(strings.TrimSpace(document.Type))]; !ok {
				errs = append(errs, FieldError{Field: "documents", Code: "unsupported_business_document", Message: "select a supported Airwallex business document classification"})
				break
			}
		}
	}

	if address := profile.OperatingAddress; address != nil && requiresSecondaryID(address.State) {
		for i, details := range app.People {
			person, found := company.Person(details.PersonID)
			if !found || details.SecondaryIdentificationID == "" {
				errs = append(errs, FieldError{Field: fmt.Sprintf("platform.onboarding_details.people[%d].secondary_identification_id", i), Code: "required_secondary_id", Message: "this operating state requires a secondary photo identification"})
				continue
			}
			identification, found := findDomainIdentification(person.Identifications, details.SecondaryIdentificationID)
			if !found || !isPhotoIdentification(identification.Type) {
				errs = append(errs, FieldError{Field: fmt.Sprintf("platform.onboarding_details.people[%d].secondary_identification_id", i), Code: "invalid_secondary_id", Message: "select a passport, driver's license, or personal ID as the secondary identification"})
				continue
			}
			if !hasIdentificationPhoto(application.Documents(), person.ID, identification.ID, identification.Type) {
				errs = append(errs, FieldError{Field: "documents", Code: "required_secondary_id_photo", Message: "upload the required secondary identification images"})
			}
		}
	}

	return errs
}

func isSupportedUSBusinessStructure(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "LLC", "LIMITED_LIABILITY_COMPANY", "CORP", "CORPORATION", "INC", "PARTNERSHIP", "SOLE_PROPRIETORSHIP", "SOLE_PROPRIETOR", "INDIVIDUAL":
		return true
	default:
		return false
	}
}

func isUSCompanyStructure(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "LLC", "LIMITED_LIABILITY_COMPANY", "CORP", "CORPORATION", "INC", "PARTNERSHIP":
		return true
	default:
		return false
	}
}

func hasUSBusinessIdentifier(identifiers []domain.RegistrationIdentifier) bool {
	for _, identifier := range identifiers {
		if strings.EqualFold(strings.TrimSpace(identifier.Type), "EIN") && strings.EqualFold(strings.TrimSpace(identifier.IssuingCountry), "US") && strings.TrimSpace(identifier.Value) != "" {
			return true
		}
	}
	return false
}

func hasUSPersonTaxID(identifications []domain.Identification) bool {
	for _, identification := range identifications {
		if (strings.EqualFold(identification.Type, "SSN") || strings.EqualFold(identification.Type, "ITIN")) && strings.EqualFold(identification.IssuingCountry, "US") && strings.TrimSpace(identification.Number) != "" {
			return true
		}
	}
	return false
}

func isUSPersonTaxID(identificationType string) bool {
	switch strings.ToUpper(strings.TrimSpace(identificationType)) {
	case "SSN", "ITIN":
		return true
	default:
		return false
	}
}

func requiresUSPrimaryContactMobile(products []string) bool {
	for _, product := range products {
		switch strings.ToUpper(strings.TrimSpace(product)) {
		case "ACCEPT_ONLINE_PAYMENTS", "GET_PAID", "COLLECT_MARKETPLACE_PROCEEDS":
			return true
		}
	}
	return false
}

func primaryContactPhone(company *domain.Company, personID string) string {
	if person, ok := company.Person(personID); ok {
		return person.Phone
	}
	return ""
}

func validateRequiredUSBusinessDocuments(company *domain.Company, application *domain.OnboardingApplication) []FieldError {
	present := make(map[string]bool)
	for _, document := range application.Documents() {
		if document.PersonID == "" {
			present[strings.ToUpper(strings.TrimSpace(document.Type))] = true
		}
	}
	require := func(ok bool) []FieldError {
		if ok {
			return nil
		}
		return []FieldError{{Field: "documents", Code: "required_business_document", Message: "attach the company formation documents required for this business structure"}}
	}
	switch strings.ToUpper(strings.TrimSpace(company.EntityType())) {
	case "LLC", "LIMITED_LIABILITY_COMPANY", "CORP", "CORPORATION", "INC":
		return require(present["CERTIFICATE_OF_INCORPORATION"])
	case "PARTNERSHIP":
		return require(present["PARTNERSHIP_AGREEMENT"] && (present["CERTIFICATE_OF_INCORPORATION"] || present["CONFIRMATION_STATEMENT"]))
	case "SOLE_PROPRIETORSHIP", "SOLE_PROPRIETOR", "INDIVIDUAL":
		required := present["CONFIRMATION_STATEMENT"]
		if strings.TrimSpace(company.TradingName()) != "" {
			required = required && present["ASSUMED_NAME_CERTIFICATE"]
		}
		return require(required)
	default:
		return nil
	}
}

func hasIdentificationPhoto(documents []domain.Document, personID, identificationID, identificationType string) bool {
	wanted := []string{"PASSPORT_FRONT"}
	switch strings.ToUpper(strings.TrimSpace(identificationType)) {
	case "DRIVERS_LICENCE":
		wanted = []string{"DRIVERS_LICENSE_FRONT", "DRIVERS_LICENSE_BACK"}
	case "PERSONAL_ID":
		wanted = []string{"NATIONAL_ID_FRONT", "NATIONAL_ID_BACK"}
	}
	found := make(map[string]bool, len(wanted))
	for _, document := range documents {
		if document.PersonID == personID && document.IdentificationID == identificationID {
			found[strings.ToUpper(strings.TrimSpace(document.Type))] = true
		}
	}
	for _, docType := range wanted {
		if !found[docType] {
			return false
		}
	}
	return true
}

func findDomainIdentification(identifications []domain.Identification, id string) (domain.Identification, bool) {
	for _, identification := range identifications {
		if identification.ID == id {
			return identification, true
		}
	}
	return domain.Identification{}, false
}

func isPhotoIdentification(identificationType string) bool {
	switch strings.ToUpper(strings.TrimSpace(identificationType)) {
	case "PASSPORT", "DRIVERS_LICENCE", "PERSONAL_ID":
		return true
	default:
		return false
	}
}

func requiresSecondaryID(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "NY", "HI", "NV", "IN":
		return true
	default:
		return false
	}
}
