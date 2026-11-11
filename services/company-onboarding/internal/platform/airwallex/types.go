package airwallex

import "time"

// Address represents standard Airwallex address structure.
type Address struct {
	AddressLine1 string `json:"address_line1"`
	AddressLine2 string `json:"address_line2,omitempty"`
	Suburb       string `json:"suburb"`
	State        string `json:"state"`
	Postcode     string `json:"postcode"`
	CountryCode  string `json:"country_code"`
}

// BusinessIdentifier represents registered tax or legal entity identifier.
type BusinessIdentifier struct {
	CountryCode string `json:"country_code"`
	Number      string `json:"number"`
	Type        string `json:"type"` // e.g. "EIN", "BRN"
}

// RevenueEstimate represents estimated monthly business volume.
type RevenueEstimate struct {
	Amount   string `json:"amount,omitempty"`
	Currency string `json:"currency,omitempty"`
}

// AccountUsage captures expected platform utilization and revenue estimates.
type AccountUsage struct {
	EstimatedMonthlyRevenue *RevenueEstimate `json:"estimated_monthly_revenue,omitempty"`
	ProductReference        string           `json:"product_reference,omitempty"`
}

// BusinessAttachment links an uploaded file to business verification.
type BusinessAttachment struct {
	FileID string `json:"file_id"`
	Tag    string `json:"tag,omitempty"`
}

// BusinessAttachments wraps uploaded business documentation.
type BusinessAttachments struct {
	BusinessDocuments []BusinessAttachment `json:"business_documents,omitempty"`
}

// PersonAttachments contains verification documents associated with a business person.
type PersonAttachments struct {
	BusinessPersonDocuments []PersonDocument `json:"business_person_documents,omitempty"`
}

// PersonDocument links an uploaded identity or authorization file to a business person.
type PersonDocument struct {
	FileID string `json:"file_id"`
	Tag    string `json:"tag"` // e.g. "PASSPORT", "DRIVERS_LICENSE", "PERSON_PURPORTING_TO_ACT_AUTHORISATION_LETTER"
}

// BusinessDetails represents business formation, address, revenue, and operating details.
type BusinessDetails struct {
	BusinessName                 string               `json:"business_name"`
	BusinessStructure            string               `json:"business_structure"`
	DescriptionOfGoodsOrServices string               `json:"description_of_goods_or_services,omitempty"`
	IndustryCategoryCode         string               `json:"industry_category_code,omitempty"`
	BusinessIdentifiers          []BusinessIdentifier `json:"business_identifiers,omitempty"`
	RegistrationAddress          Address              `json:"registration_address"`
	OperatingCountry             []string             `json:"operating_country,omitempty"`
	AccountUsage                 *AccountUsage        `json:"account_usage,omitempty"`
	Attachments                  *BusinessAttachments `json:"attachments,omitempty"`
}

// DeviceData captures client audit data for terms consent.
type DeviceData struct {
	IPAddress string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// TermsAndConditions represents explicit agreement details.
type TermsAndConditions struct {
	AgreedAt             string      `json:"agreed_at"`
	ServiceAgreementType string      `json:"service_agreement_type"`
	DeviceData           *DeviceData `json:"device_data,omitempty"`
}

// CustomerAgreements captures required compliance consent checkboxes.
type CustomerAgreements struct {
	AgreedToDataUsage          bool               `json:"agreed_to_data_usage"`
	AgreedToTermsAndConditions bool               `json:"agreed_to_terms_and_conditions"`
	TermsAndConditions         TermsAndConditions `json:"terms_and_conditions"`
}

// PrimaryContact represents the main authorized contact person.
type PrimaryContact struct {
	Email  string `json:"email,omitempty"`
	Mobile string `json:"mobile,omitempty"`
}

// PersonIdentifications represents structured primary and secondary identification for a business person.
// Provenance: https://www.airwallex.com/docs/api/scale/accounts/update
type PersonIdentifications struct {
	Primary   *IdentificationItem `json:"primary,omitempty"`
	Secondary *IdentificationItem `json:"secondary,omitempty"`
}

// IdentificationItem represents a single identity record with type discriminator.
type IdentificationItem struct {
	IdentificationType string                 `json:"identification_type"` // e.g. "PASSPORT", "DRIVERS_LICENCE", "TAX_ID", "PERSONAL_ID"
	IssuingCountryCode string                 `json:"issuing_country_code,omitempty"`
	Passport           *PassportDetails       `json:"passport,omitempty"`
	DriversLicense     *DriversLicenseDetails `json:"drivers_license,omitempty"`
	PersonalID         *PersonalIDDetails     `json:"personal_id,omitempty"`
	TaxID              *TaxIDDetails          `json:"tax_id,omitempty"`
}

// PassportDetails holds passport verification attributes and file references.
type PassportDetails struct {
	Number      string `json:"number,omitempty"`
	EffectiveAt string `json:"effective_at,omitempty"`
	ExpireAt    string `json:"expire_at,omitempty"`
	FrontFileID string `json:"front_file_id,omitempty"`
}

// DriversLicenseDetails holds driver license verification attributes and file references.
type DriversLicenseDetails struct {
	Number       string `json:"number,omitempty"`
	EffectiveAt  string `json:"effective_at,omitempty"`
	ExpireAt     string `json:"expire_at,omitempty"`
	FrontFileID  string `json:"front_file_id,omitempty"`
	BackFileID   string `json:"back_file_id,omitempty"`
	IssuingState string `json:"issuing_state,omitempty"`
}

// PersonalIDDetails holds national/personal ID attributes and file references.
type PersonalIDDetails struct {
	Number      string `json:"number,omitempty"`
	EffectiveAt string `json:"effective_at,omitempty"`
	ExpireAt    string `json:"expire_at,omitempty"`
	FrontFileID string `json:"front_file_id,omitempty"`
	BackFileID  string `json:"back_file_id,omitempty"`
}

// TaxIDDetails holds personal tax identification attributes (must be explicit "SSN" or "ITIN").
type TaxIDDetails struct {
	Type   string `json:"type,omitempty"`
	Number string `json:"number,omitempty"`
}

// PersonDetails represents associated people, platform roles, residential addresses, and ID attachments.
type PersonDetails struct {
	FirstName          string                 `json:"first_name"`
	LastName           string                 `json:"last_name"`
	DateOfBirth        string                 `json:"date_of_birth,omitempty"`
	Nationality        string                 `json:"nationality,omitempty"`
	Email              string                 `json:"email,omitempty"`
	PhoneNumber        string                 `json:"phone_number,omitempty"`
	Roles              []string               `json:"roles,omitempty"`
	ResidentialAddress *Address               `json:"residential_address,omitempty"`
	Identifications    *PersonIdentifications `json:"identifications,omitempty"`
	Attachments        *PersonAttachments     `json:"attachments,omitempty"`
}

// AccountCreateDetails holds nested entity and business details for creation.
type AccountCreateDetails struct {
	LegalEntityType string          `json:"legal_entity_type"`
	BusinessDetails BusinessDetails `json:"business_details"`
}

// AccountCreateRequest represents the outbound body for POST /api/v1/accounts/create.
type AccountCreateRequest struct {
	AccountDetails     AccountCreateDetails `json:"account_details"`
	CustomerAgreements CustomerAgreements   `json:"customer_agreements"`
	PrimaryContact     PrimaryContact       `json:"primary_contact"`
}

// AccountUpdateDetails holds business details, attachments, and people for update.
type AccountUpdateDetails struct {
	BusinessDetails       BusinessDetails `json:"business_details"`
	BusinessPersonDetails []PersonDetails `json:"business_person_details,omitempty"`
}

// AccountUpdateRequest represents the outbound body for POST /api/v1/accounts/{id}/update.
type AccountUpdateRequest struct {
	AccountDetails AccountUpdateDetails `json:"account_details"`
}

// AccountCreateResponse represents the response from POST /api/v1/accounts/create.
type AccountCreateResponse struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	PrimaryContact PrimaryContact `json:"primary_contact"`
}

// FileUploadResponse represents the response from POST /api/v1/files/upload.
type FileUploadResponse struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// AccountSubmitResponse represents the response from POST /api/v1/accounts/{id}/submit.
type AccountSubmitResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	SubmittedAt string `json:"submitted_at"`
}

// AccountDetailsResponse represents the response from GET /api/v1/accounts/{id} or POST update.
type AccountDetailsResponse struct {
	ID             string                `json:"id"`
	Status         string                `json:"status"`
	AccountDetails *AccountCreateDetails `json:"account_details,omitempty"`
	PrimaryContact *PrimaryContact       `json:"primary_contact,omitempty"`
}

// AuthLoginResponse represents the response from POST /api/v1/authentication/login.
type AuthLoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// APIError represents the standard Airwallex error payload.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
}

// RFIAnswerRequirement defines the expected answer format for an RFI question.
type RFIAnswerRequirement struct {
	Type string `json:"type"` // e.g. "TEXT", "FILE"
}

// RFIQuestionPrompt represents an individual inquiry prompt from the platform.
type RFIQuestionPrompt struct {
	ID     string               `json:"id"`
	Answer RFIAnswerRequirement `json:"answer"`
}

// RFIActiveRequest contains the current open questions requiring answers.
type RFIActiveRequest struct {
	Questions []RFIQuestionPrompt `json:"questions"`
}

// RFIAnsweredRequest contains historical answered questions.
type RFIAnsweredRequest struct {
	Questions []RFIQuestionPrompt `json:"questions,omitempty"`
}

// RFIResponsePayload represents the response from GET /api/v1/rfis/{platformRfiId}.
// Provenance: https://www.airwallex.com/docs/api/risk/request_for_information_rfi/retrieve.md (2026-11-08)
type RFIResponsePayload struct {
	ID               string               `json:"id"`
	AccountID        string               `json:"account_id"`
	Type             string               `json:"type"`   // e.g. "KYC"
	Status           string               `json:"status"` // e.g. "ACTION_REQUIRED"
	ActiveRequest    *RFIActiveRequest    `json:"active_request,omitempty"`
	AnsweredRequests []RFIAnsweredRequest `json:"answered_requests,omitempty"`
}

// RFIAnswerValue contains the answer content for a text question.
type RFIAnswerValue struct {
	Text string `json:"text"`
}

// RFIQuestionAnswer represents an answered question in an RFI response.
type RFIQuestionAnswer struct {
	ID     string         `json:"id"`
	Answer RFIAnswerValue `json:"answer"`
}

// RFIRespondRequest represents the request body for POST /api/v1/rfis/{platformRfiId}/respond.
// Provenance: https://www.airwallex.com/docs/api/risk/request_for_information_rfi/respond_rfis.md (2026-11-08)
type RFIRespondRequest struct {
	Questions []RFIQuestionAnswer `json:"questions"`
}

// RFIRespondResponse represents the response from POST /api/v1/rfis/{platformRfiId}/respond.
// Note: Success response schema/status is not yet verified in Docs MCP; client does not assert unverified fields.
type RFIRespondResponse struct {
	StatusCode int    `json:"-"`
	RawBody    []byte `json:"-"`
}
