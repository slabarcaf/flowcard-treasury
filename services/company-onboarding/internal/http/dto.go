// Package http provides the HTTP resource handlers, DTO definitions,
// authentication gating, and validation formatting for the company onboarding service.
package http

import (
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
)

// --- Common Response Envelopes ---

type ErrorResponse struct {
	Code    string                  `json:"code"`
	Message string                  `json:"message"`
	Details string                  `json:"details,omitempty"`
	Errors  []validation.FieldError `json:"errors,omitempty"`
}

type ActionResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- Request DTOs (Strict Allowlist; never unmarshal into persistence fields) ---

type AddressDTO struct {
	Line1      string `json:"line_1" validate:"required,max=200"`
	Line2      string `json:"line_2,omitempty" validate:"omitempty,max=200"`
	City       string `json:"city" validate:"required,max=100"`
	State      string `json:"state,omitempty" validate:"omitempty,max=100"`
	PostalCode string `json:"postal_code" validate:"required,max=20"`
	Country    string `json:"country" validate:"required,iso3166_alpha2"`
}

type BusinessProfileDTO struct {
	Website           string      `json:"website" validate:"required,url,max=500"`
	Description       string      `json:"description" validate:"required,max=2000"`
	RegisteredAddress *AddressDTO `json:"registered_address,omitempty"`
}

type RegistrationIdentifierDTO struct {
	Type           string `json:"type" validate:"required,max=50"`
	Value          string `json:"value" validate:"required,max=100"`
	IssuingCountry string `json:"issuing_country" validate:"required,iso3166_alpha2"`
}

type IdentificationDTO struct {
	ID             string `json:"id,omitempty" validate:"omitempty,max=64"`
	Type           string `json:"type" validate:"required,max=50"`
	Number         string `json:"number" validate:"required,max=100"`
	IssuingCountry string `json:"issuing_country" validate:"required,iso3166_alpha2"`
	IssuedOn       string `json:"issued_on,omitempty"`
	ExpiresOn      string `json:"expires_on,omitempty"`
}

type PersonDTO struct {
	ID              string              `json:"id,omitempty" validate:"omitempty,max=64"`
	FirstName       string              `json:"first_name" validate:"required,max=100"`
	LastName        string              `json:"last_name" validate:"required,max=100"`
	Role            string              `json:"role" validate:"required,max=100"`
	DateOfBirth     string              `json:"date_of_birth" validate:"required,past_date"`
	Identifications []IdentificationDTO `json:"identifications,omitempty" validate:"dive"`
}

type CreateCompanyRequest struct {
	LegalName               string                      `json:"legal_name" validate:"required,min=2,max=200"`
	TradingName             string                      `json:"trading_name,omitempty" validate:"omitempty,max=200"`
	EntityType              string                      `json:"entity_type,omitempty" validate:"omitempty,max=50"`
	RegistrationCountry     string                      `json:"registration_country,omitempty" validate:"omitempty,iso3166_alpha2"`
	RegistrationState       string                      `json:"registration_state,omitempty" validate:"omitempty,max=100"`
	BusinessProfile         *BusinessProfileDTO         `json:"business_profile,omitempty"`
	RegistrationIdentifiers []RegistrationIdentifierDTO `json:"registration_identifiers,omitempty" validate:"dive"`
	People                  []PersonDTO                 `json:"people,omitempty" validate:"dive"`
}

type EditBusinessProfileDTO struct {
	Website           *string     `json:"website,omitempty"`
	Description       *string     `json:"description,omitempty"`
	RegisteredAddress *AddressDTO `json:"registered_address,omitempty"`
}

type EditCompanyRequest struct {
	ExpectedRevision        int                          `json:"expected_revision" validate:"required,min=1"`
	LegalName               *string                      `json:"legal_name,omitempty"`
	TradingName             *string                      `json:"trading_name,omitempty"`
	EntityType              *string                      `json:"entity_type,omitempty"`
	RegistrationCountry     *string                      `json:"registration_country,omitempty"`
	RegistrationState       *string                      `json:"registration_state,omitempty"`
	BusinessProfile         *EditBusinessProfileDTO      `json:"business_profile,omitempty"`
	RegistrationIdentifiers *[]RegistrationIdentifierDTO `json:"registration_identifiers,omitempty"`
	People                  *[]PersonDTO                 `json:"people,omitempty"`
	Reason                  string                       `json:"reason,omitempty"`
}

type ArchiveCompanyRequest struct {
	ExpectedRevision int    `json:"expected_revision" validate:"required,min=1"`
	Reason           string `json:"reason,omitempty"`
}

type RevenueEstimateDTO struct {
	Amount   string `json:"amount,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type PersonPlatformDetailsDTO struct {
	PersonID           string      `json:"person_id"`
	PlatformRoles      []string    `json:"platform_roles,omitempty"`
	ResidentialAddress *AddressDTO `json:"residential_address,omitempty"`
}

type DeviceDataDTO struct {
	IPAddress string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

type TermsAgreementDataDTO struct {
	AgreedAt             time.Time      `json:"agreed_at"`
	ServiceAgreementType string         `json:"service_agreement_type"`
	DeviceData           *DeviceDataDTO `json:"device_data,omitempty"`
}

type CustomerAgreementsDTO struct {
	AgreedToTermsAndConditions bool                   `json:"agreed_to_terms_and_conditions"`
	AgreedToDataUsage          bool                   `json:"agreed_to_data_usage"`
	TermsAndConditions         *TermsAgreementDataDTO `json:"terms_and_conditions,omitempty"`
}

type PlatformOnboardingDetailsDTO struct {
	IndustryCategoryCode   string                     `json:"industry_category_code,omitempty"`
	IntendedProducts       []string                   `json:"intended_products,omitempty"`
	EstimatedRevenue       RevenueEstimateDTO         `json:"estimated_monthly_revenue"`
	PrimaryContactPersonID string                     `json:"primary_contact_person_id,omitempty"`
	People                 []PersonPlatformDetailsDTO `json:"people,omitempty"`
	CustomerAgreements     CustomerAgreementsDTO      `json:"customer_agreements"`
}

type CreateApplicationRequest struct {
	CompanyID         string                        `json:"company_id" validate:"required"`
	PlatformName      string                        `json:"platform_name" validate:"required"`
	Environment       string                        `json:"environment,omitempty"`
	OnboardingDetails *PlatformOnboardingDetailsDTO `json:"onboarding_details,omitempty"`
}

type EditApplicationRequest struct {
	ExpectedRevision  int                          `json:"expected_revision" validate:"required,min=1"`
	OnboardingDetails PlatformOnboardingDetailsDTO `json:"onboarding_details" validate:"required"`
}

type CancelApplicationRequest struct {
	ExpectedRevision int    `json:"expected_revision" validate:"required,min=1"`
	Reason           string `json:"reason,omitempty"`
}

type SubmitApplicationRequest struct {
	ExpectedRevision int `json:"expected_revision" validate:"required,min=1"`
}

// --- Response DTOs ---

type CompanyResponse struct {
	ID                      string                          `json:"id"`
	LegalName               string                          `json:"legal_name"`
	TradingName             string                          `json:"trading_name,omitempty"`
	EntityType              string                          `json:"entity_type,omitempty"`
	RegistrationCountry     string                          `json:"registration_country,omitempty"`
	RegistrationState       string                          `json:"registration_state,omitempty"`
	Status                  string                          `json:"status"`
	HasSubmittedHistory     bool                            `json:"has_submitted_history"`
	BusinessProfile         domain.BusinessProfile          `json:"business_profile"`
	RegistrationIdentifiers []domain.RegistrationIdentifier `json:"registration_identifiers"`
	People                  []domain.Person                 `json:"people"`
	Revision                int                             `json:"revision"`
	CreatedAt               time.Time                       `json:"created_at"`
	UpdatedAt               time.Time                       `json:"updated_at"`
}

type ApplicationResponse struct {
	ID                    string                      `json:"id"`
	CompanyID             string                      `json:"company_id"`
	Status                string                      `json:"status"`
	Platform              domain.ApplicationPlatform  `json:"platform"`
	Company               *CompanyResponse            `json:"company"`
	Documents             []domain.Document           `json:"documents"`
	InformationRequests   []domain.InformationRequest `json:"information_requests"`
	Creator               string                      `json:"creator"`
	OperatorReviewFlagged bool                        `json:"operator_review_flagged"`
	Revision              int                         `json:"revision"`
	CreatedAt             time.Time                   `json:"created_at"`
	UpdatedAt             time.Time                   `json:"updated_at"`
	ClosedAt              *time.Time                  `json:"closed_at,omitempty"`
}

type CompanyListResponse struct {
	Companies  []CompanyResponse `json:"companies"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type ApplicationListResponse struct {
	Applications []ApplicationResponse `json:"applications"`
	NextCursor   string                `json:"next_cursor,omitempty"`
}

type PlatformAccountResponse struct {
	ID                string     `json:"id"`
	CompanyID         string     `json:"company_id"`
	PlatformName      string     `json:"platform_name"`
	Environment       string     `json:"environment"`
	ExternalAccountID string     `json:"external_account_id,omitempty"`
	Status            string     `json:"status"`
	LastSyncTime      *time.Time `json:"last_sync_time,omitempty"`
	Revision          int        `json:"revision"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type SubmissionResponse struct {
	ID                    string                           `json:"submission_id"`
	Type                  string                           `json:"type"`
	Status                string                           `json:"status"`
	DefinitiveInvalidData bool                             `json:"definitive_invalid_data,omitempty"`
	Company               *CompanyResponse                 `json:"company"`
	OnboardingDetails     domain.PlatformOnboardingDetails `json:"onboarding_details"`
	DocumentIDs           []string                         `json:"document_ids,omitempty"`
	InformationRequestID  string                           `json:"information_request_id,omitempty"`
	Answers               map[string]string                `json:"answers,omitempty"`
	SubmittingActor       string                           `json:"submitting_actor"`
	CreatedAt             time.Time                        `json:"created_at"`
	AcceptedAt            *time.Time                       `json:"accepted_at,omitempty"`
}

type DocumentResponse struct {
	ID               string    `json:"document_id"`
	Type             string    `json:"type"`
	PersonID         string    `json:"person_id,omitempty"`
	IdentificationID string    `json:"identification_id,omitempty"`
	FileName         string    `json:"file_name"`
	ContentType      string    `json:"content_type"`
	SizeBytes        int64     `json:"size_bytes"`
	UploadedBy       string    `json:"uploaded_by"`
	UploadedAt       time.Time `json:"uploaded_at"`
}

func toDocumentResponse(d domain.Document) DocumentResponse {
	return DocumentResponse{
		ID:               d.ID,
		Type:             d.Type,
		PersonID:         d.PersonID,
		IdentificationID: d.IdentificationID,
		FileName:         d.FileName,
		ContentType:      d.ContentType,
		SizeBytes:        d.SizeBytes,
		UploadedBy:       d.UploadedBy,
		UploadedAt:       d.UploadedAt,
	}
}

// Convert domain.Company to CompanyResponse
func toCompanyResponse(c *domain.Company) CompanyResponse {
	if c == nil {
		return CompanyResponse{}
	}
	return CompanyResponse{
		ID:                      c.ID(),
		LegalName:               c.LegalName(),
		TradingName:             c.TradingName(),
		EntityType:              c.EntityType(),
		RegistrationCountry:     c.RegistrationCountry(),
		RegistrationState:       c.RegistrationState(),
		Status:                  string(c.Status()),
		HasSubmittedHistory:     c.HasSubmittedHistory(),
		BusinessProfile:         c.BusinessProfile(),
		RegistrationIdentifiers: c.RegistrationIdentifiers(),
		People:                  c.People(),
		Revision:                c.Revision(),
		CreatedAt:               c.CreatedAt(),
		UpdatedAt:               c.UpdatedAt(),
	}
}

// Convert domain.OnboardingApplication to ApplicationResponse, embedding supplied company
func toApplicationResponse(app *domain.OnboardingApplication, comp *domain.Company) ApplicationResponse {
	var compResp *CompanyResponse
	if comp != nil {
		cr := toCompanyResponse(comp)
		compResp = &cr
	} else if app.Company() != nil {
		cr := toCompanyResponse(app.Company())
		compResp = &cr
	}

	return ApplicationResponse{
		ID:                    app.ID(),
		CompanyID:             app.Company().ID(),
		Status:                string(app.Status()),
		Platform:              app.Platform(),
		Company:               compResp,
		Documents:             app.Documents(),
		InformationRequests:   app.InformationRequests(),
		Creator:               app.Creator(),
		OperatorReviewFlagged: app.OperatorReviewFlagged(),
		Revision:              app.Revision(),
		CreatedAt:             app.CreatedAt(),
		UpdatedAt:             app.UpdatedAt(),
		ClosedAt:              app.ClosedAt(),
	}
}

// Convert domain.PlatformAccount to PlatformAccountResponse
func toPlatformAccountResponse(pa *domain.PlatformAccount) PlatformAccountResponse {
	if pa == nil {
		return PlatformAccountResponse{}
	}
	return PlatformAccountResponse{
		ID:                pa.ID(),
		CompanyID:         pa.CompanyID(),
		PlatformName:      pa.PlatformName(),
		Environment:       pa.Environment(),
		ExternalAccountID: pa.ExternalAccountID(),
		Status:            string(pa.Status()),
		LastSyncTime:      pa.LastSyncTime(),
		Revision:          pa.Revision(),
		CreatedAt:         pa.CreatedAt(),
		UpdatedAt:         pa.UpdatedAt(),
	}
}

// Convert domain.Submission to SubmissionResponse
func toSubmissionResponse(sub domain.Submission) SubmissionResponse {
	var compResp *CompanyResponse
	if sub.CompanyCopy != nil {
		cr := toCompanyResponse(sub.CompanyCopy)
		compResp = &cr
	}
	return SubmissionResponse{
		ID:                    sub.ID,
		Type:                  string(sub.Type),
		Status:                string(sub.Status),
		DefinitiveInvalidData: sub.DefinitiveInvalidData,
		Company:               compResp,
		OnboardingDetails:     sub.OnboardingDetailsCopy,
		DocumentIDs:           sub.DocumentIDs,
		InformationRequestID:  sub.InformationRequestID,
		Answers:               sub.Answers,
		SubmittingActor:       sub.SubmittingActor,
		CreatedAt:             sub.CreatedAt,
		AcceptedAt:            sub.AcceptedAt,
	}
}
