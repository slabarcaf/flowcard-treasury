package airwallex

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
)

// ErrIncompleteSubmissionMapping indicates that the frozen submission snapshot is missing mandatory platform fields.
var ErrIncompleteSubmissionMapping = errors.New("airwallex: incomplete submission mapping")

// MapSubmissionToAccountCreateRequest constructs the platform AccountCreateRequest
// strictly from the immutable frozen Submission aggregate copy.
// Mutating live company records or fabricating missing fields is strictly prohibited.
func MapSubmissionToAccountCreateRequest(sub domain.Submission) (*AccountCreateRequest, error) {
	comp := sub.CompanyCopy
	if comp == nil {
		return nil, fmt.Errorf("%w: submission company copy is required", ErrIncompleteSubmissionMapping)
	}

	details := sub.OnboardingDetailsCopy

	// 1. Business Identifiers: explicitly lookup EIN by type (never pick first arbitrary identifier)
	var einValue string
	for _, rid := range comp.RegistrationIdentifiers() {
		if strings.EqualFold(strings.TrimSpace(rid.Type), "EIN") && strings.TrimSpace(rid.Value) != "" {
			einValue = strings.TrimSpace(rid.Value)
			break
		}
	}
	if einValue == "" {
		return nil, fmt.Errorf("%w: missing required EIN registration identifier for US platform account", ErrIncompleteSubmissionMapping)
	}

	businessStructure, err := mapBusinessStructure(comp.EntityType())
	if err != nil {
		return nil, err
	}

	profile := comp.BusinessProfile()
	businessAddress, err := mapOperatingAddress(profile.OperatingAddress)
	if err != nil {
		return nil, err
	}
	operatingCountries, err := mapOperatingCountries(profile.OperatingCountries)
	if err != nil {
		return nil, err
	}
	street1 := strings.TrimSpace(profile.RegisteredAddress.Line1)
	if street1 == "" {
		return nil, fmt.Errorf("%w: registered address line 1 is required", ErrIncompleteSubmissionMapping)
	}
	city := strings.TrimSpace(profile.RegisteredAddress.City)
	state := strings.TrimSpace(profile.RegisteredAddress.State)
	postalCode := strings.TrimSpace(profile.RegisteredAddress.PostalCode)
	country := strings.ToUpper(strings.TrimSpace(profile.RegisteredAddress.Country))
	if country != "US" {
		return nil, fmt.Errorf("%w: registration address country must be US, got %q", ErrIncompleteSubmissionMapping, country)
	}

	accountUsage, err := mapAccountUsage(details.EstimatedRevenue, details.IntendedProducts)
	if err != nil {
		return nil, err
	}

	businessDetails := BusinessDetails{
		BusinessName:                 comp.LegalName(),
		BusinessStructure:            businessStructure,
		DescriptionOfGoodsOrServices: strings.TrimSpace(profile.Products),
		IndustryCategoryCode:         strings.TrimSpace(details.IndustryCategoryCode),
		BusinessIdentifiers: []BusinessIdentifier{
			{
				CountryCode: "US",
				Number:      einValue,
				Type:        "EIN",
			},
		},
		RegistrationAddress: Address{
			AddressLine1: street1,
			AddressLine2: strings.TrimSpace(profile.RegisteredAddress.Line2),
			Suburb:       city,
			State:        state,
			Postcode:     postalCode,
			CountryCode:  country,
		},
		BusinessAddress:  businessAddress,
		OperatingCountry: operatingCountries,
		AccountUsage:     accountUsage,
	}

	// 2. Include customer agreements only when the frozen application records complete acceptance.
	tc := details.CustomerAgreements.TermsAndConditions
	var agreements *CustomerAgreements
	customerAgreements := details.CustomerAgreements
	hasAgreementData := customerAgreements.AgreedToTermsAndConditions || customerAgreements.AgreedToDataUsage || tc != nil
	if hasAgreementData {
		if !customerAgreements.AgreedToTermsAndConditions || !customerAgreements.AgreedToDataUsage || tc == nil || tc.AgreedAt.IsZero() {
			return nil, fmt.Errorf("%w: customer agreement record is incomplete", ErrIncompleteSubmissionMapping)
		}
		serviceAgreementType := strings.TrimSpace(tc.ServiceAgreementType)
		if serviceAgreementType == "" {
			return nil, fmt.Errorf("%w: terms service agreement type is required", ErrIncompleteSubmissionMapping)
		}

		var devData *DeviceData
		if tc.DeviceData != nil && (tc.DeviceData.IPAddress != "" || tc.DeviceData.UserAgent != "") {
			devData = &DeviceData{
				IPAddress: tc.DeviceData.IPAddress,
				UserAgent: tc.DeviceData.UserAgent,
			}
		}

		agreements = &CustomerAgreements{
			AgreedToDataUsage:          customerAgreements.AgreedToDataUsage,
			AgreedToTermsAndConditions: customerAgreements.AgreedToTermsAndConditions,
			TermsAndConditions: TermsAndConditions{
				AgreedAt:             tc.AgreedAt.UTC().Format(time.RFC3339),
				ServiceAgreementType: serviceAgreementType,
				DeviceData:           devData,
			},
		}
	}

	// 3. Primary contact (email and mobile only)
	contactID := strings.TrimSpace(details.PrimaryContactPersonID)
	if contactID == "" {
		return nil, fmt.Errorf("%w: submission primary contact person ID is required", ErrIncompleteSubmissionMapping)
	}

	contactPerson, found := comp.Person(contactID)
	if !found {
		return nil, fmt.Errorf("%w: primary contact person %q not found in submission company copy", ErrIncompleteSubmissionMapping, contactID)
	}

	primaryContact := PrimaryContact{
		Email:  contactPerson.Email,
		Mobile: contactPerson.Phone,
	}

	return &AccountCreateRequest{
		AccountDetails: AccountCreateDetails{
			LegalEntityType: "BUSINESS",
			BusinessDetails: businessDetails,
		},
		CustomerAgreements: agreements,
		PrimaryContact:     primaryContact,
	}, nil
}

// MapSubmissionToAccountUpdateRequest constructs the payload for POST /api/v1/accounts/{id}/update.
// Maps uploaded file IDs, business details, products, revenue, category code, and associated people.
func MapSubmissionToAccountUpdateRequest(
	sub domain.Submission,
	appDocs []domain.Document,
	docFileIDs map[string]string,
) (*AccountUpdateRequest, error) {
	comp := sub.CompanyCopy
	if comp == nil {
		return nil, fmt.Errorf("%w: submission company copy is required", ErrIncompleteSubmissionMapping)
	}

	profile := comp.BusinessProfile()
	products := strings.TrimSpace(profile.Products)
	if products == "" {
		// Finding 6: fail incomplete mapping, never fallback products from description silently
		return nil, fmt.Errorf("%w: goods and services description (products) is required", ErrIncompleteSubmissionMapping)
	}

	businessStructure, err := mapBusinessStructure(comp.EntityType())
	if err != nil {
		return nil, err
	}
	businessAddress, err := mapOperatingAddress(profile.OperatingAddress)
	if err != nil {
		return nil, err
	}
	operatingCountries, err := mapOperatingCountries(profile.OperatingCountries)
	if err != nil {
		return nil, err
	}

	var einValue string
	for _, rid := range comp.RegistrationIdentifiers() {
		if strings.EqualFold(strings.TrimSpace(rid.Type), "EIN") && strings.TrimSpace(rid.Value) != "" {
			einValue = strings.TrimSpace(rid.Value)
			break
		}
	}

	details := sub.OnboardingDetailsCopy

	// 1. Business attachments (company formation documents)
	var businessDocs []BusinessAttachment
	docPersonMap := make(map[string]string)
	docTypeMap := make(map[string]string)
	for _, d := range appDocs {
		docPersonMap[d.ID] = d.PersonID
		docTypeMap[d.ID] = d.Type
	}

	for _, docID := range sub.DocumentIDs {
		personID := docPersonMap[docID]
		if personID == "" {
			// Company-level attachment
			if fileID, ok := docFileIDs[docID]; ok && fileID != "" {
				tag, err := mapBusinessDocumentTag(docTypeMap[docID])
				if err != nil {
					return nil, err
				}
				businessDocs = append(businessDocs, BusinessAttachment{
					FileID: fileID,
					Tag:    tag,
				})
			}
		}
	}

	accountUsage, err := mapAccountUsage(details.EstimatedRevenue, details.IntendedProducts)
	if err != nil {
		return nil, err
	}

	var businessAttachments *BusinessAttachments
	if len(businessDocs) > 0 {
		businessAttachments = &BusinessAttachments{
			BusinessDocuments: businessDocs,
		}
	}

	businessDetails := BusinessDetails{
		BusinessName:                 comp.LegalName(),
		BusinessStructure:            businessStructure,
		DescriptionOfGoodsOrServices: products,
		IndustryCategoryCode:         details.IndustryCategoryCode,
		BusinessAddress:              businessAddress,
		OperatingCountry:             operatingCountries,
		AccountUsage:                 accountUsage,
		Attachments:                  businessAttachments,
		RegistrationAddress: Address{
			AddressLine1: profile.RegisteredAddress.Line1,
			AddressLine2: profile.RegisteredAddress.Line2,
			Suburb:       profile.RegisteredAddress.City,
			State:        profile.RegisteredAddress.State,
			Postcode:     profile.RegisteredAddress.PostalCode,
			CountryCode:  profile.RegisteredAddress.Country,
		},
	}
	if einValue != "" {
		businessDetails.BusinessIdentifiers = []BusinessIdentifier{
			{CountryCode: "US", Number: einValue, Type: "EIN"},
		}
	}

	// 2. Business person details with platform roles and identity file attachments
	var businessPersonDetails []PersonDetails
	seenPeople := make(map[string]struct{}, len(details.People))
	for _, pd := range details.People {
		p, found := comp.Person(pd.PersonID)
		if !found {
			return nil, fmt.Errorf("%w: platform person %q is missing from submission company copy", ErrIncompleteSubmissionMapping, pd.PersonID)
		}
		if _, seen := seenPeople[pd.PersonID]; seen {
			return nil, fmt.Errorf("%w: platform person %q is listed more than once", ErrIncompleteSubmissionMapping, pd.PersonID)
		}
		seenPeople[pd.PersonID] = struct{}{}
		if len(pd.PlatformRoles) == 0 {
			return nil, fmt.Errorf("%w: platform roles are required for person %q", ErrIncompleteSubmissionMapping, pd.PersonID)
		}

		// Find documents belonging to this person
		var personDocs []domain.Document
		for _, docID := range sub.DocumentIDs {
			if docPersonMap[docID] == p.ID {
				for _, d := range appDocs {
					if d.ID == docID {
						personDocs = append(personDocs, d)
						break
					}
				}
			}
		}

		consumedDocIDs := make(map[string]bool)
		personIdents, err := mapPersonIdentifications(p, pd, personDocs, docFileIDs, consumedDocIDs)
		if err != nil {
			return nil, err
		}

		// Map remaining unconsumed documents as supplemental person documents
		var supplementalDocs []PersonDocument
		for _, d := range personDocs {
			if consumedDocIDs[d.ID] {
				continue
			}
			fileID := docFileIDs[d.ID]
			if fileID == "" {
				continue
			}
			// Only allow supported supplemental person document tags (e.g. authorization letter)
			// Passport / driver license scans belong to identifications, not business_person_documents
			cleanType := strings.ToUpper(strings.TrimSpace(d.Type))
			if strings.Contains(cleanType, "PASSPORT") || strings.Contains(cleanType, "DRIVERS_LICENSE") || strings.Contains(cleanType, "NATIONAL_ID") {
				continue
			}
			tag := "PERSON_PURPORTING_TO_ACT_AUTHORISATION_LETTER"
			if !strings.Contains(cleanType, "AUTHORI") && cleanType != "" {
				tag = strings.ToUpper(strings.ReplaceAll(cleanType, "-", "_"))
			}
			supplementalDocs = append(supplementalDocs, PersonDocument{
				FileID: fileID,
				Tag:    tag,
			})
		}

		var personAttachments *PersonAttachments
		if len(supplementalDocs) > 0 {
			personAttachments = &PersonAttachments{
				BusinessPersonDocuments: supplementalDocs,
			}
		}

		resAddr := &Address{
			AddressLine1: pd.ResidentialAddress.Line1,
			AddressLine2: pd.ResidentialAddress.Line2,
			Suburb:       pd.ResidentialAddress.City,
			State:        pd.ResidentialAddress.State,
			Postcode:     pd.ResidentialAddress.PostalCode,
			CountryCode:  pd.ResidentialAddress.Country,
		}

		businessPersonDetails = append(businessPersonDetails, PersonDetails{
			FirstName:          p.FirstName,
			LastName:           p.LastName,
			DateOfBirth:        string(p.DateOfBirth),
			Nationality:        p.Nationality,
			Email:              p.Email,
			PhoneNumber:        p.Phone,
			Roles:              pd.PlatformRoles,
			ResidentialAddress: resAddr,
			Identifications:    personIdents,
			Attachments:        personAttachments,
		})
	}

	return &AccountUpdateRequest{
		AccountDetails: AccountUpdateDetails{
			BusinessDetails:       businessDetails,
			BusinessPersonDetails: businessPersonDetails,
		},
	}, nil
}

func mapBusinessStructure(entityType string) (string, error) {
	clean := strings.ToUpper(strings.TrimSpace(entityType))
	switch clean {
	case "LLC", "LIMITED_LIABILITY_COMPANY":
		return "LIMITED_LIABILITY_COMPANY", nil
	case "CORP", "CORPORATION", "INC":
		return "CORPORATION", nil
	case "PARTNERSHIP":
		return "PARTNERSHIP", nil
	case "SOLE_PROPRIETORSHIP", "SOLE_PROPRIETOR", "INDIVIDUAL":
		return "SOLE_PROPRIETOR", nil
	default:
		// Finding 6: never default missing entityType to LLC; fail incomplete mapping
		return "", fmt.Errorf("%w: unsupported or empty entity type %q", ErrIncompleteSubmissionMapping, entityType)
	}
}

func mapRevenueEstimate(re domain.RevenueEstimate) *RevenueEstimate {
	if strings.TrimSpace(re.Amount) == "" {
		return nil
	}
	return &RevenueEstimate{
		Amount:   re.Amount,
		Currency: re.Currency,
	}
}

func mapPersonIdentifications(
	person domain.Person,
	personDetails domain.PersonPlatformDetails,
	documents []domain.Document,
	docFileIDs map[string]string,
	consumedDocIDs map[string]bool,
) (*PersonIdentifications, error) {
	if len(person.Identifications) == 0 {
		return nil, fmt.Errorf("%w: person %q requires an identification", ErrIncompleteSubmissionMapping, person.ID)
	}
	primary, found := findIdentification(person.Identifications, personDetails.PrimaryIdentificationID)
	if personDetails.PrimaryIdentificationID == "" && len(person.Identifications) == 1 {
		primary = &person.Identifications[0]
		found = true
	}
	if !found {
		return nil, fmt.Errorf("%w: person %q must select a primary identification", ErrIncompleteSubmissionMapping, person.ID)
	}
	secondary, secondaryFound := findIdentification(person.Identifications, personDetails.SecondaryIdentificationID)
	if personDetails.SecondaryIdentificationID != "" && !secondaryFound {
		return nil, fmt.Errorf("%w: person %q selected a missing secondary identification", ErrIncompleteSubmissionMapping, person.ID)
	}
	if secondary != nil && secondary.ID == primary.ID {
		return nil, fmt.Errorf("%w: person %q selected the same primary and secondary identification", ErrIncompleteSubmissionMapping, person.ID)
	}
	for _, identification := range person.Identifications {
		if identification.ID != primary.ID && (secondary == nil || identification.ID != secondary.ID) {
			return nil, fmt.Errorf("%w: person %q has an identification without an explicit primary or secondary designation", ErrIncompleteSubmissionMapping, person.ID)
		}
	}

	primaryItem, err := mapIdentificationItem(*primary, person.ID, documents, docFileIDs, consumedDocIDs)
	if err != nil {
		return nil, err
	}
	identifications := &PersonIdentifications{Primary: primaryItem}
	if secondary != nil {
		secondaryItem, err := mapIdentificationItem(*secondary, person.ID, documents, docFileIDs, consumedDocIDs)
		if err != nil {
			return nil, err
		}
		identifications.Secondary = secondaryItem
	}
	return identifications, nil
}

func findIdentification(identifications []domain.Identification, id string) (*domain.Identification, bool) {
	if id == "" {
		return nil, false
	}
	for i := range identifications {
		if identifications[i].ID == id {
			return &identifications[i], true
		}
	}
	return nil, false
}

func mapOperatingAddress(address *domain.Address) (*Address, error) {
	if address == nil {
		return nil, fmt.Errorf("%w: business operating address is required", ErrIncompleteSubmissionMapping)
	}
	country := strings.ToUpper(strings.TrimSpace(address.Country))
	if country != "US" {
		return nil, fmt.Errorf("%w: business operating address country must be US", ErrIncompleteSubmissionMapping)
	}
	if strings.TrimSpace(address.Line1) == "" || strings.TrimSpace(address.City) == "" || strings.TrimSpace(address.State) == "" || strings.TrimSpace(address.PostalCode) == "" {
		return nil, fmt.Errorf("%w: business operating address requires street, city, state, and postal code", ErrIncompleteSubmissionMapping)
	}
	return &Address{
		AddressLine1: strings.TrimSpace(address.Line1),
		AddressLine2: strings.TrimSpace(address.Line2),
		Suburb:       strings.TrimSpace(address.City),
		State:        strings.TrimSpace(address.State),
		Postcode:     strings.TrimSpace(address.PostalCode),
		CountryCode:  country,
	}, nil
}

func mapOperatingCountries(countries []string) ([]string, error) {
	if len(countries) == 0 {
		return nil, fmt.Errorf("%w: at least one actual operating country is required", ErrIncompleteSubmissionMapping)
	}
	result := make([]string, 0, len(countries))
	seen := make(map[string]struct{}, len(countries))
	for _, country := range countries {
		code := strings.ToUpper(strings.TrimSpace(country))
		if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
			return nil, fmt.Errorf("%w: operating countries must be 2-letter ISO codes", ErrIncompleteSubmissionMapping)
		}
		if _, duplicate := seen[code]; duplicate {
			return nil, fmt.Errorf("%w: operating country %q is listed more than once", ErrIncompleteSubmissionMapping, code)
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	return result, nil
}

func mapAccountUsage(revenue domain.RevenueEstimate, products []string) (*AccountUsage, error) {
	revenueEstimate := mapRevenueEstimate(revenue)
	if revenueEstimate == nil || strings.TrimSpace(revenueEstimate.Currency) == "" {
		return nil, fmt.Errorf("%w: estimated monthly revenue amount and currency are required", ErrIncompleteSubmissionMapping)
	}
	if len(products) == 0 {
		return nil, fmt.Errorf("%w: at least one intended Airwallex product is required", ErrIncompleteSubmissionMapping)
	}

	productReferences := make([]string, 0, len(products))
	seen := make(map[string]struct{}, len(products))
	for _, product := range products {
		code := strings.ToUpper(strings.TrimSpace(product))
		if !isSupportedProductReference(code) {
			return nil, fmt.Errorf("%w: unsupported intended product %q", ErrIncompleteSubmissionMapping, code)
		}
		if _, duplicate := seen[code]; duplicate {
			return nil, fmt.Errorf("%w: intended product %q is listed more than once", ErrIncompleteSubmissionMapping, code)
		}
		seen[code] = struct{}{}
		productReferences = append(productReferences, code)
	}
	return &AccountUsage{
		EstimatedMonthlyRevenue: revenueEstimate,
		ProductReference:        productReferences,
	}, nil
}

func isSupportedProductReference(code string) bool {
	switch code {
	case "ACCEPT_ONLINE_PAYMENTS", "COLLECT_MARKETPLACE_PROCEEDS", "RECEIVE_TRANSFERS", "GET_PAID", "CONVERT_FUNDS", "MAKE_TRANSFERS", "CREATE_CARDS", "MANAGE_EXPENSES", "USE_AWX_API", "TRANSFER_CNY_INBOUND":
		return true
	default:
		return false
	}
}

func mapBusinessDocumentTag(raw string) (string, error) {
	tag := strings.ToUpper(strings.TrimSpace(raw))
	switch tag {
	case "ACRA_COMPANY_PROFILE_DOCUMENT", "ANNUAL_REPORT", "ANNUAL_RETURN", "ARTICLES_OF_ASSOCIATION", "ASIC_CURRENT_COMPANY_EXTRACT", "ASSUMED_NAME_CERTIFICATE", "AUTHORIZED_SIGNATORY_DISCLOSURE", "BUSINESS_LICENSE", "CERTIFICATE_OF_INCORPORATION", "CERTIFICATION_REGISTRATION", "COMPANY_CERTIFICATE", "COMPANY_CONSTITUTION", "COMPANY_PROFILE", "CONFIRMATION_STATEMENT", "DIRECTOR_LIST", "EIN_LETTER", "LEGAL_NAME_AND_ADDRESS", "OPERATING_AGREEMENT", "PARTNERSHIP_AGREEMENT", "REGISTRATION_CERTIFICATE", "SHAREHOLDING_STRUCTURE_CHART", "SUPPORTIVE_OTHER", "TRUST_DEED", "UNIT_HOLDER_REGISTER", "UBO_SUPPORTIVE", "THIRD_PARTY_SHAREHOLDING_DOCUMENT":
		return tag, nil
	default:
		return "", fmt.Errorf("%w: unsupported Airwallex business document tag %q", ErrIncompleteSubmissionMapping, tag)
	}
}

func mapIdentificationItem(
	ident domain.Identification,
	personID string,
	personDocs []domain.Document,
	docFileIDs map[string]string,
	consumedDocIDs map[string]bool,
) (*IdentificationItem, error) {
	cleanType := strings.ToUpper(strings.TrimSpace(ident.Type))
	issuingCountry := strings.TrimSpace(ident.IssuingCountry)

	switch cleanType {
	case "PASSPORT":
		var frontFileID string
		for _, d := range personDocs {
			// Must use exact person and identification IDs; no substring fallback
			if d.IdentificationID == ident.ID {
				cleanDocType := strings.ToUpper(strings.TrimSpace(d.Type))
				if cleanDocType == "PASSPORT" || cleanDocType == "PASSPORT_FRONT" {
					if fileID := docFileIDs[d.ID]; fileID != "" {
						if frontFileID != "" {
							return nil, fmt.Errorf("%w: multiple front document images found for identification %s of person %s", ErrIncompleteSubmissionMapping, ident.ID, personID)
						}
						frontFileID = fileID
						consumedDocIDs[d.ID] = true
					}
				}
			}
		}
		return &IdentificationItem{
			IdentificationType: "PASSPORT",
			IssuingCountryCode: issuingCountry,
			Passport: &PassportDetails{
				Number:      ident.Number,
				EffectiveAt: string(ident.IssuedOn),
				ExpireAt:    string(ident.ExpiresOn),
				FrontFileID: frontFileID,
			},
		}, nil

	case "DRIVERS_LICENSE", "DRIVERS_LICENCE":
		var frontFileID, backFileID string
		for _, d := range personDocs {
			// Must use exact person and identification IDs; no substring fallback
			if d.IdentificationID == ident.ID {
				cleanDocType := strings.ToUpper(strings.TrimSpace(d.Type))
				fileID := docFileIDs[d.ID]
				if fileID != "" {
					if cleanDocType == "DRIVERS_LICENSE_BACK" || cleanDocType == "DRIVERS_LICENCE_BACK" {
						if backFileID != "" {
							return nil, fmt.Errorf("%w: multiple back document images found for identification %s of person %s", ErrIncompleteSubmissionMapping, ident.ID, personID)
						}
						backFileID = fileID
						consumedDocIDs[d.ID] = true
					} else if cleanDocType == "DRIVERS_LICENSE_FRONT" || cleanDocType == "DRIVERS_LICENCE_FRONT" || cleanDocType == "DRIVERS_LICENSE" || cleanDocType == "DRIVERS_LICENCE" {
						if frontFileID != "" {
							return nil, fmt.Errorf("%w: multiple front document images found for identification %s of person %s", ErrIncompleteSubmissionMapping, ident.ID, personID)
						}
						frontFileID = fileID
						consumedDocIDs[d.ID] = true
					}
				}
			}
		}
		return &IdentificationItem{
			IdentificationType: "DRIVERS_LICENCE",
			IssuingCountryCode: issuingCountry,
			DriversLicense: &DriversLicenseDetails{
				Number:      ident.Number,
				EffectiveAt: string(ident.IssuedOn),
				ExpireAt:    string(ident.ExpiresOn),
				FrontFileID: frontFileID,
				BackFileID:  backFileID,
			},
		}, nil

	case "PERSONAL_ID", "NATIONAL_ID":
		var frontFileID, backFileID string
		for _, d := range personDocs {
			// Must use exact person and identification IDs; no substring fallback
			if d.IdentificationID == ident.ID {
				cleanDocType := strings.ToUpper(strings.TrimSpace(d.Type))
				fileID := docFileIDs[d.ID]
				if fileID != "" {
					if cleanDocType == "PERSONAL_ID_BACK" || cleanDocType == "NATIONAL_ID_BACK" {
						if backFileID != "" {
							return nil, fmt.Errorf("%w: multiple back document images found for identification %s of person %s", ErrIncompleteSubmissionMapping, ident.ID, personID)
						}
						backFileID = fileID
						consumedDocIDs[d.ID] = true
					} else if cleanDocType == "PERSONAL_ID_FRONT" || cleanDocType == "NATIONAL_ID_FRONT" || cleanDocType == "PERSONAL_ID" || cleanDocType == "NATIONAL_ID" {
						if frontFileID != "" {
							return nil, fmt.Errorf("%w: multiple front document images found for identification %s of person %s", ErrIncompleteSubmissionMapping, ident.ID, personID)
						}
						frontFileID = fileID
						consumedDocIDs[d.ID] = true
					}
				}
			}
		}
		return &IdentificationItem{
			IdentificationType: "PERSONAL_ID",
			IssuingCountryCode: issuingCountry,
			PersonalID: &PersonalIDDetails{
				Number:      ident.Number,
				EffectiveAt: string(ident.IssuedOn),
				ExpireAt:    string(ident.ExpiresOn),
				FrontFileID: frontFileID,
				BackFileID:  backFileID,
			},
		}, nil

	case "SSN", "ITIN":
		return &IdentificationItem{
			IdentificationType: "TAX_ID",
			IssuingCountryCode: issuingCountry,
			TaxID: &TaxIDDetails{
				Type:   cleanType,
				Number: ident.Number,
			},
		}, nil

	case "TAX_ID":
		// Generic TAX_ID is rejected per review: prohibited invented identity info; requires explicit SSN or ITIN
		return nil, fmt.Errorf("%w: generic TAX_ID is ambiguous; explicit tax type (SSN or ITIN) is required", ErrIncompleteSubmissionMapping)

	default:
		return nil, fmt.Errorf("%w: unsupported identification type %q for person %q", ErrIncompleteSubmissionMapping, ident.Type, personID)
	}
}

// MapSubmissionToRFIRespondRequest maps an immutable frozen submission and internal RFI
// into the platform RFIRespondRequest.
//
// Verification & Invariants (from Lead Docs MCP):
// 1. Remote RFI must have active questions requiring response.
// 2. All active questions must be type TEXT (documentary type stays manual handling).
// 3. Stable frozen mapping between internal question IDs and platform IDs must be resolved.
// 4. All active questions must have non-empty text answers in the frozen submission.
func MapSubmissionToRFIRespondRequest(
	sub domain.Submission,
	internalRFI domain.InformationRequest,
	remoteRFI RFIResponsePayload,
) (*RFIRespondRequest, error) {
	if remoteRFI.ActiveRequest == nil || len(remoteRFI.ActiveRequest.Questions) == 0 {
		return nil, errors.New("remote RFI has no active questions to answer")
	}

	var answeredQuestions []RFIQuestionAnswer
	for _, activeQ := range remoteRFI.ActiveRequest.Questions {
		// Documentary type stays manual handling
		if !strings.EqualFold(strings.TrimSpace(activeQ.Answer.Type), "TEXT") {
			return nil, fmt.Errorf("unsupported RFI question answer type %q (only TEXT is supported; documentary requires manual handling)", activeQ.Answer.Type)
		}

		// Resolve internal question ID using stable frozen mapping
		internalQID, found := internalRFI.QuestionInternalID(activeQ.ID)
		if !found {
			// Direct fallback if submission answers are keyed directly by platform question ID
			if _, ok := sub.Answers[activeQ.ID]; ok {
				internalQID = activeQ.ID
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("active platform question %q has no matching question on internal RFI %s", activeQ.ID, internalRFI.ID)
		}

		ans, answered := sub.Answers[internalQID]
		if !answered || strings.TrimSpace(ans) == "" {
			return nil, fmt.Errorf("missing frozen answer for platform question %q (internal %q)", activeQ.ID, internalQID)
		}

		answeredQuestions = append(answeredQuestions, RFIQuestionAnswer{
			ID: activeQ.ID,
			Answer: RFIAnswerValue{
				Text: strings.TrimSpace(ans),
			},
		})
	}

	return &RFIRespondRequest{
		Questions: answeredQuestions,
	}, nil
}
