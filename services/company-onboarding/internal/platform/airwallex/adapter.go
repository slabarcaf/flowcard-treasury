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

	revenueEst := mapRevenueEstimate(details.EstimatedRevenue)
	productRef := strings.Join(details.IntendedProducts, ",")
	var accountUsage *AccountUsage
	if revenueEst != nil || productRef != "" {
		accountUsage = &AccountUsage{
			EstimatedMonthlyRevenue: revenueEst,
			ProductReference:        productRef,
		}
	}

	var opCountries []string
	if len(profile.OperatingCountries) > 0 {
		opCountries = append([]string(nil), profile.OperatingCountries...)
	} else if regCountry := comp.RegistrationCountry(); regCountry != "" {
		opCountries = []string{regCountry}
	}

	businessDetails := BusinessDetails{
		BusinessName:      comp.LegalName(),
		BusinessStructure: businessStructure,
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
		OperatingCountry: opCountries,
		AccountUsage:     accountUsage,
	}

	// 2. Customer agreements: strictly verify frozen consent (never fabricate agreed_at from CreatedAt or force FULL)
	tc := details.CustomerAgreements.TermsAndConditions
	if tc == nil {
		return nil, fmt.Errorf("%w: terms and conditions agreement record is required", ErrIncompleteSubmissionMapping)
	}
	if tc.AgreedAt.IsZero() {
		return nil, fmt.Errorf("%w: terms and conditions agreement timestamp is required", ErrIncompleteSubmissionMapping)
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

	agreements := CustomerAgreements{
		AgreedToDataUsage:          details.CustomerAgreements.AgreedToDataUsage,
		AgreedToTermsAndConditions: details.CustomerAgreements.AgreedToTermsAndConditions,
		TermsAndConditions: TermsAndConditions{
			AgreedAt:             tc.AgreedAt.UTC().Format(time.RFC3339),
			ServiceAgreementType: serviceAgreementType,
			DeviceData:           devData,
		},
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
				businessDocs = append(businessDocs, BusinessAttachment{
					FileID: fileID,
					Tag:    "BUSINESS_DOCUMENT",
				})
			}
		}
	}

	revenueEst := mapRevenueEstimate(details.EstimatedRevenue)
	productRef := strings.Join(details.IntendedProducts, ",")
	var accountUsage *AccountUsage
	if revenueEst != nil || productRef != "" {
		accountUsage = &AccountUsage{
			EstimatedMonthlyRevenue: revenueEst,
			ProductReference:        productRef,
		}
	}

	var businessAttachments *BusinessAttachments
	if len(businessDocs) > 0 {
		businessAttachments = &BusinessAttachments{
			BusinessDocuments: businessDocs,
		}
	}

	var opCountries []string
	if len(profile.OperatingCountries) > 0 {
		opCountries = append([]string(nil), profile.OperatingCountries...)
	} else if regCountry := comp.RegistrationCountry(); regCountry != "" {
		opCountries = []string{regCountry}
	}

	businessDetails := BusinessDetails{
		BusinessName:                 comp.LegalName(),
		BusinessStructure:            businessStructure,
		DescriptionOfGoodsOrServices: products,
		IndustryCategoryCode:         details.IndustryCategoryCode,
		OperatingCountry:             opCountries,
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
	for _, p := range comp.People() {
		pd, _ := details.FindPersonDetails(p.ID)

		if len(p.Identifications) > 1 {
			// Minimal safe scope per review: single identification maps primary; multiple require explicit
			// application-owned primary/secondary identification IDs (or fail closed >1 until that mapping exists), no silent ordering.
			return nil, fmt.Errorf("%w: person %q has %d identifications; ambiguous ordering without explicit primary/secondary designation (fail closed >1)", ErrIncompleteSubmissionMapping, p.ID, len(p.Identifications))
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
		var personIdents *PersonIdentifications

		if len(p.Identifications) == 1 {
			primaryItem, err := mapIdentificationItem(p.Identifications[0], p.ID, personDocs, docFileIDs, consumedDocIDs)
			if err != nil {
				return nil, err
			}
			personIdents = &PersonIdentifications{
				Primary: primaryItem,
			}
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

		var resAddr *Address
		if pd.ResidentialAddress.Line1 != "" {
			resAddr = &Address{
				AddressLine1: pd.ResidentialAddress.Line1,
				AddressLine2: pd.ResidentialAddress.Line2,
				Suburb:       pd.ResidentialAddress.City,
				State:        pd.ResidentialAddress.State,
				Postcode:     pd.ResidentialAddress.PostalCode,
				CountryCode:  pd.ResidentialAddress.Country,
			}
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
	case "SOLE_PROPRIETORSHIP", "INDIVIDUAL":
		return "SOLE_PROPRIETORSHIP", nil
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
