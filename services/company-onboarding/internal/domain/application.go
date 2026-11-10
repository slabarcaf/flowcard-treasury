// Package domain defines the core domain entities, value objects, and state machines
// for the company onboarding service.
package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ApplicationStatus represents the state of an Onboarding Application.
type ApplicationStatus string

const (
	ApplicationStatusDraft               ApplicationStatus = "DRAFT"
	ApplicationStatusSubmissionPending   ApplicationStatus = "SUBMISSION_PENDING"
	ApplicationStatusUnderReview         ApplicationStatus = "UNDER_REVIEW"
	ApplicationStatusInformationRequired ApplicationStatus = "INFORMATION_REQUIRED"
	ApplicationStatusCompleted           ApplicationStatus = "COMPLETED"
	ApplicationStatusRejected            ApplicationStatus = "REJECTED"
	ApplicationStatusCancelled           ApplicationStatus = "CANCELLED"
)

// PlatformDecision represents the platform's terminal underwriting decision.
type PlatformDecision string

const (
	PlatformDecisionApproved PlatformDecision = "APPROVED"
	PlatformDecisionRejected PlatformDecision = "REJECTED"
)

// SubmissionType indicates whether a submission is initial or answering an RFI.
type SubmissionType string

const (
	SubmissionTypeInitialApplication  SubmissionType = "INITIAL_APPLICATION"
	SubmissionTypeInformationResponse SubmissionType = "INFORMATION_RESPONSE"
)

// SubmissionStatus represents the transport/processing status of a submission.
type SubmissionStatus string

const (
	SubmissionStatusPending    SubmissionStatus = "PENDING"
	SubmissionStatusAccepted   SubmissionStatus = "ACCEPTED"
	SubmissionStatusFailed     SubmissionStatus = "FAILED"
	SubmissionStatusUnresolved SubmissionStatus = "UNRESOLVED"
)

// InformationRequestStatus represents the status of an RFI inquiry.
type InformationRequestStatus string

const (
	InformationRequestStatusOpen            InformationRequestStatus = "OPEN"
	InformationRequestStatusResponsePending InformationRequestStatus = "RESPONSE_PENDING"
	InformationRequestStatusResponded       InformationRequestStatus = "RESPONDED"
	InformationRequestStatusResolved        InformationRequestStatus = "RESOLVED"
)

// Domain sentinel errors for Onboarding Application.
var (
	ErrEmptyApplicationID                = errors.New("application ID must not be empty")
	ErrEmptyPlatformAccountID            = errors.New("platform account ID must not be empty")
	ErrEmptyCompanyReference             = errors.New("company reference must not be empty")
	ErrInvalidApplicationStatus          = errors.New("invalid application status")
	ErrInvalidSubmissionStatus           = errors.New("invalid submission status")
	ErrInvalidInformationRequestStatus   = errors.New("invalid information request status")
	ErrCancellationNotPermitted          = errors.New("cancellation is permitted only before submission")
	ErrApplicationNotDraft               = errors.New("operation permitted only while application is in DRAFT status")
	ErrApplicationNotInformationRequired = errors.New("operation permitted only while application is in INFORMATION_REQUIRED status")
	ErrApplicationNotUnderReview         = errors.New("operation permitted only while application is in UNDER_REVIEW status")
	ErrConsentRequired                   = errors.New("explicit customer agreement consent is required before submission")
	ErrDocumentNotFound                  = errors.New("document not found on application")
	ErrDuplicateDocumentID               = errors.New("document with this ID already exists on application")
	ErrDocumentExceedsSizeLimit          = errors.New("document exceeds maximum allowed size of 10 MiB")
	ErrInvalidDocumentMimeType           = errors.New("invalid document MIME type: must be PDF, JPEG, or PNG")
	ErrEmptyStorageKey                   = errors.New("document storage key must not be empty")
	ErrSubmissionNotFound                = errors.New("submission not found")
	ErrDuplicateSubmissionID             = errors.New("submission with this ID already exists on application")
	ErrInformationRequestNotFound        = errors.New("information request not found")
	ErrDuplicateInformationRequestID     = errors.New("information request with this ID already exists on application")
	ErrInformationRequestNotOpen         = errors.New("information request is not open for response")
	ErrIncompleteAnswers                 = errors.New("all questions in information request must be answered")
	ErrPlatformAccountNotActive          = errors.New("platform account must be active for application completion")
	ErrTerminalStatusImmutable           = errors.New("terminal application status cannot be modified")
	ErrSubmissionInFlight                = errors.New("cannot create submission while another submission is pending or unresolved")
)

// DeviceData records non-person device metadata at consent time.
type DeviceData struct {
	IPAddress string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// Clone returns a deep copy of DeviceData.
func (d *DeviceData) Clone() *DeviceData {
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

// TermsAndConditionsAgreement represents the detailed acceptance record.
type TermsAndConditionsAgreement struct {
	AgreedAt             time.Time   `json:"agreed_at"`
	ServiceAgreementType string      `json:"service_agreement_type"` // e.g. "FULL"
	DeviceData           *DeviceData `json:"device_data,omitempty"`
}

// Clone returns a deep copy of TermsAndConditionsAgreement.
func (t *TermsAndConditionsAgreement) Clone() *TermsAndConditionsAgreement {
	if t == nil {
		return nil
	}
	cp := *t
	cp.DeviceData = t.DeviceData.Clone()
	return &cp
}

// CustomerAgreements captures verified consent booleans and metadata.
// NOTE: Accepting Person ID is intentionally omitted per platform contract.
type CustomerAgreements struct {
	AgreedToTermsAndConditions bool                         `json:"agreed_to_terms_and_conditions"`
	AgreedToDataUsage          bool                         `json:"agreed_to_data_usage"`
	TermsAndConditions         *TermsAndConditionsAgreement `json:"terms_and_conditions,omitempty"`
}

// Clone returns a deep copy of CustomerAgreements.
func (c CustomerAgreements) Clone() CustomerAgreements {
	cp := c
	cp.TermsAndConditions = c.TermsAndConditions.Clone()
	return cp
}

// RevenueEstimate represents estimated monthly revenue.
type RevenueEstimate struct {
	Amount   string `json:"amount,omitempty"`
	Currency string `json:"currency,omitempty"`
}

// PersonPlatformDetails contains platform-specific person attributes owned by the Application.
// Company Person does NOT store residential address or regulated platform roles.
type PersonPlatformDetails struct {
	PersonID           string   `json:"person_id"`
	PlatformRoles      []string `json:"platform_roles,omitempty"`
	ResidentialAddress Address  `json:"residential_address"`
}

// Clone returns a deep copy of PersonPlatformDetails.
func (p PersonPlatformDetails) Clone() PersonPlatformDetails {
	cp := p
	if p.PlatformRoles != nil {
		cp.PlatformRoles = append([]string(nil), p.PlatformRoles...)
	}
	return cp
}

// PlatformOnboardingDetails contains provider-specific onboarding parameters.
type PlatformOnboardingDetails struct {
	IndustryCategoryCode   string                  `json:"industry_category_code,omitempty"`
	IntendedProducts       []string                `json:"intended_products,omitempty"`
	EstimatedRevenue       RevenueEstimate         `json:"estimated_monthly_revenue"`
	PrimaryContactPersonID string                  `json:"primary_contact_person_id,omitempty"`
	People                 []PersonPlatformDetails `json:"people,omitempty"`
	CustomerAgreements     CustomerAgreements      `json:"customer_agreements"`
}

// Clone returns a deep copy of PlatformOnboardingDetails.
func (p PlatformOnboardingDetails) Clone() PlatformOnboardingDetails {
	cp := p
	if p.IntendedProducts != nil {
		cp.IntendedProducts = append([]string(nil), p.IntendedProducts...)
	}
	if p.People != nil {
		cp.People = make([]PersonPlatformDetails, len(p.People))
		for i, pd := range p.People {
			cp.People[i] = pd.Clone()
		}
	}
	cp.CustomerAgreements = p.CustomerAgreements.Clone()
	return cp
}

// FindPersonDetails retrieves platform details for a specific person.
func (p PlatformOnboardingDetails) FindPersonDetails(personID string) (PersonPlatformDetails, bool) {
	for _, pd := range p.People {
		if pd.PersonID == personID {
			return pd.Clone(), true
		}
	}
	return PersonPlatformDetails{}, false
}

// Document represents an immutable application-owned uploaded file record.
type Document struct {
	ID               string    `json:"document_id"`
	Type             string    `json:"type"`
	PersonID         string    `json:"person_id,omitempty"`
	IdentificationID string    `json:"identification_id,omitempty"`
	FileName         string    `json:"file_name"`
	ContentType      string    `json:"content_type"`
	SizeBytes        int64     `json:"size_bytes"`
	StorageKey       string    `json:"storage_key"`
	UploadedBy       string    `json:"uploaded_by"`
	UploadedAt       time.Time `json:"uploaded_at"`
}

// Validate validates document size (max 10 MiB) and allowed container MIME types.
func (d Document) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("document ID must not be empty")
	}
	if strings.TrimSpace(d.StorageKey) == "" {
		return ErrEmptyStorageKey
	}
	if d.SizeBytes <= 0 || d.SizeBytes > 10*1024*1024 {
		return ErrDocumentExceedsSizeLimit
	}
	switch d.ContentType {
	case "application/pdf", "image/jpeg", "image/png":
		// valid local policy
	default:
		return ErrInvalidDocumentMimeType
	}
	return nil
}

// RFIQuestion is an individual inquiry in an Information Request.
type RFIQuestion struct {
	ID                 string `json:"question_id"`
	PlatformQuestionID string `json:"platform_question_id,omitempty"`
	Text               string `json:"text"`
	PersonID           string `json:"person_id,omitempty"`
}

// InformationRequest represents an RFI issued by the platform.
type InformationRequest struct {
	ID                string                   `json:"information_request_id"`
	PlatformReference string                   `json:"platform_reference,omitempty"`
	Status            InformationRequestStatus `json:"status"`
	Questions         []RFIQuestion            `json:"questions"`
	ReceivedAt        time.Time                `json:"received_at"`
	ResolvedAt        *time.Time               `json:"resolved_at,omitempty"`
}

// QuestionInternalID resolves an internal question ID given a platform question ID.
func (r InformationRequest) QuestionInternalID(platformQID string) (string, bool) {
	for _, q := range r.Questions {
		if q.PlatformQuestionID == platformQID || (q.PlatformQuestionID == "" && q.ID == platformQID) {
			return q.ID, true
		}
	}
	return "", false
}

// QuestionPlatformID resolves a stable platform question ID given an internal question ID.
func (r InformationRequest) QuestionPlatformID(questionID string) string {
	for _, q := range r.Questions {
		if q.ID == questionID {
			if q.PlatformQuestionID != "" {
				return q.PlatformQuestionID
			}
			return q.ID
		}
	}
	return questionID
}

// Clone returns a deep copy of InformationRequest.
func (r InformationRequest) Clone() InformationRequest {
	cp := r
	if r.Questions != nil {
		cp.Questions = append([]RFIQuestion(nil), r.Questions...)
	}
	if r.ResolvedAt != nil {
		t := *r.ResolvedAt
		cp.ResolvedAt = &t
	}
	return cp
}

// Submission represents an immutable historical payload sent to the platform.
type Submission struct {
	ID                    string                    `json:"submission_id"`
	Type                  SubmissionType            `json:"type"`
	Status                SubmissionStatus          `json:"status"`
	DefinitiveInvalidData bool                      `json:"definitive_invalid_data,omitempty"`
	CompanyCopy           *Company                  `json:"company"`
	OnboardingDetailsCopy PlatformOnboardingDetails `json:"onboarding_details"`
	DocumentIDs           []string                  `json:"document_ids,omitempty"`
	InformationRequestID  string                    `json:"information_request_id,omitempty"`
	Answers               map[string]string         `json:"answers,omitempty"` // Text-only answers by question ID
	SubmittingActor       string                    `json:"submitting_actor"`
	CreatedAt             time.Time                 `json:"created_at"`
	AcceptedAt            *time.Time                `json:"accepted_at,omitempty"`
}

// Clone returns a deep copy of Submission.
func (s Submission) Clone() Submission {
	cp := s
	if s.CompanyCopy != nil {
		cp.CompanyCopy = s.CompanyCopy.DeepCopy()
	}
	cp.OnboardingDetailsCopy = s.OnboardingDetailsCopy.Clone()
	if s.DocumentIDs != nil {
		cp.DocumentIDs = append([]string(nil), s.DocumentIDs...)
	}
	if s.Answers != nil {
		cp.Answers = make(map[string]string, len(s.Answers))
		for k, v := range s.Answers {
			cp.Answers[k] = v
		}
	}
	if s.AcceptedAt != nil {
		t := *s.AcceptedAt
		cp.AcceptedAt = &t
	}
	return cp
}

// ApplicationPlatform contains the provider identity and decision status.
type ApplicationPlatform struct {
	Name              string                    `json:"name"` // "AIRWALLEX"
	Environment       string                    `json:"environment,omitempty"`
	PlatformAccountID string                    `json:"platform_account_id,omitempty"`
	Decision          *PlatformDecision         `json:"decision,omitempty"` // null, APPROVED, REJECTED
	OnboardingDetails PlatformOnboardingDetails `json:"onboarding_details"`
}

// Clone returns a deep copy of ApplicationPlatform.
func (p ApplicationPlatform) Clone() ApplicationPlatform {
	cp := p
	if p.Decision != nil {
		d := *p.Decision
		cp.Decision = &d
	}
	cp.OnboardingDetails = p.OnboardingDetails.Clone()
	return cp
}

// OnboardingApplication is the aggregate root managing the onboarding lifecycle.
type OnboardingApplication struct {
	id                    string
	company               *Company
	platform              ApplicationPlatform
	status                ApplicationStatus
	documents             []Document
	submissions           []Submission
	informationRequests   []InformationRequest
	creator               string
	createdAt             time.Time
	updatedAt             time.Time
	closedAt              *time.Time
	revision              int
	operatorReviewFlagged bool
}

// NewDraftApplication creates a new OnboardingApplication in DRAFT status.
func NewDraftApplication(id string, company *Company, creator string) (*OnboardingApplication, error) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return nil, ErrEmptyApplicationID
	}
	if company == nil {
		return nil, ErrEmptyCompanyReference
	}

	now := time.Now().UTC()
	return &OnboardingApplication{
		id:      cleanID,
		company: company.DeepCopy(),
		platform: ApplicationPlatform{
			Name:        "AIRWALLEX",
			Environment: "sandbox",
		},
		status:              ApplicationStatusDraft,
		documents:           make([]Document, 0),
		submissions:         make([]Submission, 0),
		informationRequests: make([]InformationRequest, 0),
		creator:             strings.TrimSpace(creator),
		createdAt:           now,
		updatedAt:           now,
		revision:            1,
	}, nil
}

// ReconstituteApplicationParams carries state for reconstructing an application.
type ReconstituteApplicationParams struct {
	ID                    string
	Company               *Company
	LiveCompany           *Company // Optional live company context for validating documents attached post-draft
	Platform              ApplicationPlatform
	Status                ApplicationStatus
	Documents             []Document
	Submissions           []Submission
	InformationRequests   []InformationRequest
	Creator               string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ClosedAt              *time.Time
	Revision              int
	OperatorReviewFlagged bool
}

// ReconstituteOnboardingApplication restores an application from persistence, enforcing full aggregate integrity.
func ReconstituteOnboardingApplication(params ReconstituteApplicationParams) (*OnboardingApplication, error) {
	cleanID := strings.TrimSpace(params.ID)
	if cleanID == "" {
		return nil, ErrEmptyApplicationID
	}
	if params.Company == nil {
		return nil, ErrEmptyCompanyReference
	}
	if params.LiveCompany != nil && params.LiveCompany.ID() != params.Company.ID() {
		return nil, errors.New("live company ID does not match application company ID")
	}
	for _, sub := range params.Submissions {
		if sub.CompanyCopy != nil && sub.CompanyCopy.ID() != params.Company.ID() {
			return nil, fmt.Errorf("submission %q company copy ID does not match application company ID", sub.ID)
		}
	}
	if params.Revision <= 0 {
		return nil, ErrInvalidRevision
	}

	switch params.Status {
	case ApplicationStatusDraft, ApplicationStatusSubmissionPending, ApplicationStatusUnderReview,
		ApplicationStatusInformationRequired, ApplicationStatusCompleted, ApplicationStatusRejected,
		ApplicationStatusCancelled:
		// valid status
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidApplicationStatus, params.Status)
	}

	// Validate terminal status consistency with ClosedAt and Decision
	switch params.Status {
	case ApplicationStatusCompleted:
		if params.ClosedAt == nil {
			return nil, errors.New("completed application must have non-nil closed_at")
		}
		if params.Platform.Decision == nil || *params.Platform.Decision != PlatformDecisionApproved {
			return nil, errors.New("completed application must have APPROVED platform decision")
		}
	case ApplicationStatusRejected:
		if params.ClosedAt == nil {
			return nil, errors.New("rejected application must have non-nil closed_at")
		}
		if params.Platform.Decision != nil && *params.Platform.Decision != PlatformDecisionRejected {
			return nil, errors.New("rejected application decision must be nil or REJECTED")
		}
	case ApplicationStatusCancelled:
		if params.ClosedAt == nil {
			return nil, errors.New("cancelled application must have non-nil closed_at")
		}
		if params.Platform.Decision != nil {
			return nil, errors.New("cancelled application must not have platform decision")
		}
	default:
		// Non-terminal states must not have closedAt or Decision
		if params.ClosedAt != nil {
			return nil, fmt.Errorf("non-terminal status %s must have nil closed_at", params.Status)
		}
		if params.Platform.Decision != nil {
			return nil, fmt.Errorf("non-terminal status %s must have nil platform decision", params.Status)
		}
	}

	// Validate unique document IDs and reference integrity
	seenDocIDs := make(map[string]struct{}, len(params.Documents))
	clonedDocs := make([]Document, len(params.Documents))
	for i, d := range params.Documents {
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("document %q: %w", d.ID, err)
		}
		if _, exists := seenDocIDs[d.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate document ID %q", ErrDuplicateDocumentID, d.ID)
		}
		if d.PersonID != "" {
			validRef := false

			// 1. Check against the initial/frozen application company snapshot
			if p, ok := params.Company.Person(d.PersonID); ok {
				if d.IdentificationID == "" {
					validRef = true
				} else {
					for _, ident := range p.Identifications {
						if ident.ID == d.IdentificationID {
							validRef = true
							break
						}
					}
				}
			}

			// 2. Check against LiveCompany context (for documents attached during RFI/post-draft)
			if !validRef && params.LiveCompany != nil {
				if p, ok := params.LiveCompany.Person(d.PersonID); ok {
					if d.IdentificationID == "" {
						validRef = true
					} else {
						for _, ident := range p.Identifications {
							if ident.ID == d.IdentificationID {
								validRef = true
								break
							}
						}
					}
				}
			}

			// 3. Check against historical submission snapshots (e.g. RFI response submissions)
			if !validRef {
				for _, sub := range params.Submissions {
					if sub.CompanyCopy != nil {
						if p, ok := sub.CompanyCopy.Person(d.PersonID); ok {
							if d.IdentificationID == "" {
								validRef = true
								break
							}
							for _, ident := range p.Identifications {
								if ident.ID == d.IdentificationID {
									validRef = true
									break
								}
							}
							if validRef {
								break
							}
						}
					}
				}
			}

			if !validRef {
				return nil, fmt.Errorf("document %q references person %q (identification %q) not found in application company, live company, or submission snapshots",
					d.ID, d.PersonID, d.IdentificationID)
			}
		}
		seenDocIDs[d.ID] = struct{}{}
		clonedDocs[i] = d
	}

	// Validate unique RFI IDs and question integrity
	seenRFIIDs := make(map[string]struct{}, len(params.InformationRequests))
	clonedRFIs := make([]InformationRequest, len(params.InformationRequests))
	for i, r := range params.InformationRequests {
		if strings.TrimSpace(r.ID) == "" {
			return nil, errors.New("information request ID must not be empty")
		}
		if _, exists := seenRFIIDs[r.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate information request ID %q", ErrDuplicateInformationRequestID, r.ID)
		}
		switch r.Status {
		case InformationRequestStatusOpen, InformationRequestStatusResponsePending,
			InformationRequestStatusResponded, InformationRequestStatusResolved:
			// valid
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidInformationRequestStatus, r.Status)
		}
		for _, q := range r.Questions {
			if strings.TrimSpace(q.ID) == "" {
				return nil, errors.New("question ID must not be empty")
			}
			if strings.TrimSpace(q.Text) == "" {
				return nil, errors.New("question text must not be empty")
			}
			if q.PersonID != "" {
				if _, ok := params.Company.Person(q.PersonID); !ok {
					return nil, fmt.Errorf("question %q references person %q not found in company", q.ID, q.PersonID)
				}
			}
		}
		seenRFIIDs[r.ID] = struct{}{}
		clonedRFIs[i] = r.Clone()
	}

	// Validate unique submission IDs and cross-references
	seenSubIDs := make(map[string]struct{}, len(params.Submissions))
	clonedSubs := make([]Submission, len(params.Submissions))
	for i, s := range params.Submissions {
		if strings.TrimSpace(s.ID) == "" {
			return nil, errors.New("submission ID must not be empty")
		}
		if _, exists := seenSubIDs[s.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate submission ID %q", ErrDuplicateSubmissionID, s.ID)
		}
		switch s.Status {
		case SubmissionStatusPending, SubmissionStatusAccepted,
			SubmissionStatusFailed, SubmissionStatusUnresolved:
			// valid
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidSubmissionStatus, s.Status)
		}
		if s.InformationRequestID != "" {
			if _, exists := seenRFIIDs[s.InformationRequestID]; !exists {
				return nil, fmt.Errorf("submission %q references non-existent information request %q", s.ID, s.InformationRequestID)
			}
		}
		for _, docID := range s.DocumentIDs {
			if _, exists := seenDocIDs[docID]; !exists {
				return nil, fmt.Errorf("submission %q references non-existent document %q", s.ID, docID)
			}
		}
		seenSubIDs[s.ID] = struct{}{}
		clonedSubs[i] = s.Clone()
	}

	// Validate OnboardingDetails person references against Company
	if params.Platform.OnboardingDetails.PrimaryContactPersonID != "" {
		if _, ok := params.Company.Person(params.Platform.OnboardingDetails.PrimaryContactPersonID); !ok {
			return nil, fmt.Errorf("primary contact person %q does not exist in company", params.Platform.OnboardingDetails.PrimaryContactPersonID)
		}
	}
	for _, pd := range params.Platform.OnboardingDetails.People {
		if _, ok := params.Company.Person(pd.PersonID); !ok {
			return nil, fmt.Errorf("platform details person %q does not exist in company", pd.PersonID)
		}
	}

	app := &OnboardingApplication{
		id:                    cleanID,
		company:               params.Company.DeepCopy(),
		platform:              params.Platform.Clone(),
		status:                params.Status,
		creator:               params.Creator,
		createdAt:             params.CreatedAt,
		updatedAt:             params.UpdatedAt,
		revision:              params.Revision,
		operatorReviewFlagged: params.OperatorReviewFlagged,
		documents:             clonedDocs,
		submissions:           clonedSubs,
		informationRequests:   clonedRFIs,
	}

	if params.ClosedAt != nil {
		t := *params.ClosedAt
		app.closedAt = &t
	}

	return app, nil
}

// ID returns the application ID.
func (a *OnboardingApplication) ID() string {
	return a.id
}

// Company returns a deep copy of the embedded Company.
// While DRAFT, reflects current details. When submitted, reflects frozen snapshot.
func (a *OnboardingApplication) Company() *Company {
	return a.company.DeepCopy()
}

// Status returns current lifecycle status.
func (a *OnboardingApplication) Status() ApplicationStatus {
	return a.status
}

// Platform returns a deep copy of the platform information and decision.
func (a *OnboardingApplication) Platform() ApplicationPlatform {
	return a.platform.Clone()
}

// Revision returns current revision number.
func (a *OnboardingApplication) Revision() int {
	return a.revision
}

// Creator returns the creator actor identity.
func (a *OnboardingApplication) Creator() string {
	return a.creator
}

// CreatedAt returns creation timestamp.
func (a *OnboardingApplication) CreatedAt() time.Time {
	return a.createdAt
}

// UpdatedAt returns modification timestamp.
func (a *OnboardingApplication) UpdatedAt() time.Time {
	return a.updatedAt
}

// ClosedAt returns closure timestamp if closed.
func (a *OnboardingApplication) ClosedAt() *time.Time {
	if a.closedAt == nil {
		return nil
	}
	t := *a.closedAt
	return &t
}

// OperatorReviewFlagged indicates if mid-review suspension flagged operator review.
func (a *OnboardingApplication) OperatorReviewFlagged() bool {
	return a.operatorReviewFlagged
}

// Documents returns a copy of uploaded documents.
func (a *OnboardingApplication) Documents() []Document {
	return append([]Document(nil), a.documents...)
}

// Submissions returns deep copies of all submissions.
func (a *OnboardingApplication) Submissions() []Submission {
	out := make([]Submission, len(a.submissions))
	for i, s := range a.submissions {
		out[i] = s.Clone()
	}
	return out
}

// InformationRequests returns deep copies of all information requests.
func (a *OnboardingApplication) InformationRequests() []InformationRequest {
	out := make([]InformationRequest, len(a.informationRequests))
	for i, r := range a.informationRequests {
		out[i] = r.Clone()
	}
	return out
}

// UpdateDraftCompany updates the embedded company while in DRAFT status.
func (a *OnboardingApplication) UpdateDraftCompany(company *Company) error {
	if a.status != ApplicationStatusDraft {
		return ErrApplicationNotDraft
	}
	if company == nil {
		return ErrEmptyCompanyReference
	}
	a.company = company.DeepCopy()
	a.touch()
	return nil
}

// UpdateOnboardingDetails updates the provider onboarding details.
func (a *OnboardingApplication) UpdateOnboardingDetails(details PlatformOnboardingDetails) error {
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}
	if a.status != ApplicationStatusDraft && a.status != ApplicationStatusInformationRequired {
		return errors.New("onboarding details can only be edited in DRAFT or INFORMATION_REQUIRED status")
	}

	// Validate person references against embedded company
	if details.PrimaryContactPersonID != "" {
		if _, ok := a.company.Person(details.PrimaryContactPersonID); !ok {
			return fmt.Errorf("primary contact person %q does not exist in company", details.PrimaryContactPersonID)
		}
	}
	for _, pd := range details.People {
		if _, ok := a.company.Person(pd.PersonID); !ok {
			return fmt.Errorf("platform details person %q does not exist in company", pd.PersonID)
		}
	}

	a.platform.OnboardingDetails = details.Clone()
	a.touch()
	return nil
}

// LinkPlatformAccount associates a PlatformAccount ID with this application.
// Cannot link on terminal application, cannot provide empty ID, and cannot relink to a different ID.
func (a *OnboardingApplication) LinkPlatformAccount(accountID string) error {
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}
	cleanID := strings.TrimSpace(accountID)
	if cleanID == "" {
		return ErrEmptyPlatformAccountID
	}
	if a.platform.PlatformAccountID != "" && a.platform.PlatformAccountID != cleanID {
		return errors.New("cannot change already linked platform account ID")
	}
	if a.platform.PlatformAccountID == cleanID {
		return nil // idempotent duplicate, zero touch
	}
	a.platform.PlatformAccountID = cleanID
	a.touch()
	return nil
}

// AttachDocument adds a new document record to the application.
// Validates file size, allowed MIME types, and person/identification references.
func (a *OnboardingApplication) AttachDocument(doc Document) error {
	return a.AttachDocumentWithCompany(doc, nil)
}

// AttachDocumentWithCompany adds a new document record, validating person references
// against the live company (used especially during INFORMATION_REQUIRED or DRAFT updates).
func (a *OnboardingApplication) AttachDocumentWithCompany(doc Document, liveCompany *Company) error {
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}
	if a.status != ApplicationStatusDraft && a.status != ApplicationStatusInformationRequired {
		return errors.New("documents can only be attached in DRAFT or INFORMATION_REQUIRED status")
	}
	if err := doc.Validate(); err != nil {
		return err
	}
	comp := a.company
	if liveCompany != nil {
		if liveCompany.ID() != a.company.ID() {
			return errors.New("live company ID does not match application company ID")
		}
		comp = liveCompany
	}
	if doc.PersonID != "" {
		p, ok := comp.Person(doc.PersonID)
		if !ok {
			return fmt.Errorf("document references person %q not found in company", doc.PersonID)
		}
		if doc.IdentificationID != "" {
			foundIdent := false
			for _, ident := range p.Identifications {
				if ident.ID == doc.IdentificationID {
					foundIdent = true
					break
				}
			}
			if !foundIdent {
				return fmt.Errorf("document references identification %q not found on person %q", doc.IdentificationID, doc.PersonID)
			}
		}
	}

	for _, existing := range a.documents {
		if existing.ID == doc.ID {
			return fmt.Errorf("%w: %q", ErrDuplicateDocumentID, doc.ID)
		}
	}
	a.documents = append(a.documents, doc)
	a.touch()
	return nil
}

// Cancel cancels an application. Cancellation is permitted ONLY before submission.
func (a *OnboardingApplication) Cancel(actor string) error {
	if a.status == ApplicationStatusCancelled {
		return nil // duplicate, no touch
	}
	if a.status != ApplicationStatusDraft {
		return ErrCancellationNotPermitted
	}
	a.status = ApplicationStatusCancelled
	now := time.Now().UTC()
	a.closedAt = &now
	a.touch()
	return nil
}

// InitiateInitialSubmission submits the application for platform processing.
// Immediately before initial submission, freezes an immutable deep copy of Company
// in that same field and creates an immutable initial Submission record.
func (a *OnboardingApplication) InitiateInitialSubmission(submissionID string, actor string, docIDs []string) (*Submission, error) {
	if a.status != ApplicationStatusDraft {
		return nil, ErrApplicationNotDraft
	}
	cleanSubID := strings.TrimSpace(submissionID)
	if cleanSubID == "" {
		return nil, errors.New("submission ID must not be empty")
	}
	for _, s := range a.submissions {
		if s.ID == cleanSubID {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateSubmissionID, cleanSubID)
		}
		if s.Status == SubmissionStatusPending || s.Status == SubmissionStatusUnresolved {
			return nil, ErrSubmissionInFlight
		}
	}

	// Validate consent requirements
	agreements := a.platform.OnboardingDetails.CustomerAgreements
	if !agreements.AgreedToTermsAndConditions || !agreements.AgreedToDataUsage || agreements.TermsAndConditions == nil {
		return nil, ErrConsentRequired
	}

	// Validate referenced document IDs
	for _, docID := range docIDs {
		if !a.hasDocument(docID) {
			return nil, fmt.Errorf("%w: %q", ErrDocumentNotFound, docID)
		}
	}

	// Freeze immutable deep copy of company in application
	frozenCompany := a.company.DeepCopy()
	a.company = frozenCompany

	now := time.Now().UTC()
	submission := Submission{
		ID:                    cleanSubID,
		Type:                  SubmissionTypeInitialApplication,
		Status:                SubmissionStatusPending,
		CompanyCopy:           frozenCompany.DeepCopy(),
		OnboardingDetailsCopy: a.platform.OnboardingDetails.Clone(),
		DocumentIDs:           append([]string(nil), docIDs...),
		SubmittingActor:       strings.TrimSpace(actor),
		CreatedAt:             now,
	}

	a.submissions = append(a.submissions, submission.Clone())
	a.status = ApplicationStatusSubmissionPending
	a.touch()

	return &submission, nil
}

// RecordSubmissionAccepted records that the platform accepted a submission for processing.
// Enforces complete validation prior to any field changes.
// Terminal applications preserve complete state on duplicate and reject non-duplicate observations.
func (a *OnboardingApplication) RecordSubmissionAccepted(submissionID string) error {
	subIndex := a.findSubmissionIndex(submissionID)
	if subIndex == -1 {
		return ErrSubmissionNotFound
	}

	sub := a.submissions[subIndex]
	if sub.Status == SubmissionStatusAccepted {
		// Idempotent duplicate: zero state modification, zero touch
		return nil
	}
	if sub.Status == SubmissionStatusFailed {
		return errors.New("cannot accept previously failed submission")
	}

	// Terminal check before any state changes
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}

	if a.status == ApplicationStatusDraft {
		return errors.New("cannot record submission acceptance on draft application")
	}

	now := time.Now().UTC()
	a.submissions[subIndex].Status = SubmissionStatusAccepted
	a.submissions[subIndex].AcceptedAt = &now

	if sub.Type == SubmissionTypeInitialApplication {
		if a.status == ApplicationStatusSubmissionPending {
			a.status = ApplicationStatusUnderReview
		}
	} else if sub.Type == SubmissionTypeInformationResponse {
		if sub.InformationRequestID != "" {
			for i, r := range a.informationRequests {
				if r.ID == sub.InformationRequestID {
					a.informationRequests[i].Status = InformationRequestStatusResponded
					break
				}
			}
		}
		// Return to UNDER_REVIEW only if no active RFIs remain and app was in INFORMATION_REQUIRED
		if !a.hasActiveInformationRequests() && a.status == ApplicationStatusInformationRequired {
			a.status = ApplicationStatusUnderReview
		}
	}

	a.touch()
	return nil
}

// RecordSubmissionFailed records definitive invalid-data refusal or transport failure.
// Enforces complete precondition validation before any field changes.
// Distinguishes exact duplicate failure from contradictory definitive flag.
func (a *OnboardingApplication) RecordSubmissionFailed(submissionID string, definitiveInvalidData bool) error {
	subIndex := a.findSubmissionIndex(submissionID)
	if subIndex == -1 {
		return ErrSubmissionNotFound
	}

	sub := a.submissions[subIndex]
	if sub.Status == SubmissionStatusFailed {
		if sub.DefinitiveInvalidData == definitiveInvalidData {
			// Idempotent duplicate failure with identical definitive flag: zero mutation, zero touch
			return nil
		}
		// Contradictory definitive flag on duplicate failure
		return errors.New("contradictory submission failure: definitive flag does not match existing failure")
	}
	if sub.Status == SubmissionStatusAccepted {
		return errors.New("cannot fail previously accepted submission")
	}

	// Terminal check before any state changes
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}

	if a.status == ApplicationStatusDraft {
		return errors.New("cannot record submission failure on draft application")
	}

	a.submissions[subIndex].Status = SubmissionStatusFailed
	a.submissions[subIndex].DefinitiveInvalidData = definitiveInvalidData

	if definitiveInvalidData {
		a.status = ApplicationStatusRejected
		now := time.Now().UTC()
		a.closedAt = &now
		a.platform.Decision = nil // decision remains null per design
	}

	a.touch()
	return nil
}

// RecordSubmissionUnresolved records transport uncertainty.
// An accepted or failed submission can NEVER transition to unresolved.
func (a *OnboardingApplication) RecordSubmissionUnresolved(submissionID string) error {
	subIndex := a.findSubmissionIndex(submissionID)
	if subIndex == -1 {
		return ErrSubmissionNotFound
	}

	sub := a.submissions[subIndex]
	if sub.Status == SubmissionStatusUnresolved {
		// Idempotent duplicate: zero mutation, zero touch
		return nil
	}
	if sub.Status == SubmissionStatusAccepted {
		return fmt.Errorf("%w: cannot transition accepted submission to unresolved", ErrInvalidSubmissionStatus)
	}
	if sub.Status == SubmissionStatusFailed {
		return fmt.Errorf("%w: cannot transition failed submission to unresolved", ErrInvalidSubmissionStatus)
	}

	// Terminal check before any state changes
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}

	if a.status == ApplicationStatusDraft {
		return errors.New("cannot record submission uncertainty on draft application")
	}

	a.submissions[subIndex].Status = SubmissionStatusUnresolved
	a.touch()
	return nil
}

// ReceiveInformationRequest creates an OPEN information request and transitions application to INFORMATION_REQUIRED.
// Multiple information requests can coexist in parallel.
func (a *OnboardingApplication) ReceiveInformationRequest(req InformationRequest) error {
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}
	if a.status != ApplicationStatusUnderReview && a.status != ApplicationStatusInformationRequired {
		return fmt.Errorf("information request requires post-submission review state, got %s", a.status)
	}
	cleanID := strings.TrimSpace(req.ID)
	if cleanID == "" {
		return errors.New("information request ID must not be empty")
	}
	if len(req.Questions) == 0 {
		return errors.New("information request must have at least one question")
	}

	seenQIDs := make(map[string]struct{}, len(req.Questions))
	for _, q := range req.Questions {
		if strings.TrimSpace(q.ID) == "" {
			return errors.New("question ID must not be empty")
		}
		if strings.TrimSpace(q.Text) == "" {
			return errors.New("question text must not be empty")
		}
		if _, exists := seenQIDs[q.ID]; exists {
			return fmt.Errorf("duplicate question ID %q in request", q.ID)
		}
		if q.PersonID != "" {
			if _, ok := a.company.Person(q.PersonID); !ok {
				return fmt.Errorf("question references person %q not found in company", q.PersonID)
			}
		}
		seenQIDs[q.ID] = struct{}{}
	}

	for _, existing := range a.informationRequests {
		if existing.ID == cleanID {
			return fmt.Errorf("%w: %q", ErrDuplicateInformationRequestID, cleanID)
		}
	}

	clonedReq := req.Clone()
	clonedReq.ID = cleanID
	clonedReq.Status = InformationRequestStatusOpen
	if clonedReq.ReceivedAt.IsZero() {
		clonedReq.ReceivedAt = time.Now().UTC()
	}

	a.informationRequests = append(a.informationRequests, clonedReq)
	a.status = ApplicationStatusInformationRequired
	a.touch()
	return nil
}

// RespondToInformationRequest submits text answers for an OPEN RFI.
// Accepts an optional amended currentCompany: validates matching Company ID and freezes a separate new snapshot
// in the response Submission without altering earlier initial/submission copies or a.company.
// Validates that no submissions are currently pending or unresolved across all requests.
func (a *OnboardingApplication) RespondToInformationRequest(
	submissionID string,
	requestID string,
	actor string,
	currentCompany *Company,
	answers map[string]string,
	docIDs []string,
) (*Submission, error) {
	if a.status != ApplicationStatusInformationRequired {
		return nil, ErrApplicationNotInformationRequired
	}
	cleanSubID := strings.TrimSpace(submissionID)
	if cleanSubID == "" {
		return nil, errors.New("submission ID must not be empty")
	}
	for _, s := range a.submissions {
		if s.ID == cleanSubID {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateSubmissionID, cleanSubID)
		}
		if s.Status == SubmissionStatusPending || s.Status == SubmissionStatusUnresolved {
			return nil, ErrSubmissionInFlight
		}
	}

	rfiIndex := a.findInformationRequestIndex(requestID)
	if rfiIndex == -1 {
		return nil, ErrInformationRequestNotFound
	}
	rfi := a.informationRequests[rfiIndex]
	if rfi.Status != InformationRequestStatusOpen {
		return nil, ErrInformationRequestNotOpen
	}

	// Validate questions have non-empty text answers
	questionMap := make(map[string]struct{}, len(rfi.Questions))
	for _, q := range rfi.Questions {
		questionMap[q.ID] = struct{}{}
		ans, ok := answers[q.ID]
		if !ok || strings.TrimSpace(ans) == "" {
			return nil, fmt.Errorf("%w: question %q unanswered", ErrIncompleteAnswers, q.ID)
		}
	}

	// Reject extraneous answer IDs not present on this RFI
	for ansID := range answers {
		if _, ok := questionMap[ansID]; !ok {
			return nil, fmt.Errorf("extraneous answer ID %q not found on information request %q", ansID, requestID)
		}
	}

	// Validate document IDs
	for _, docID := range docIDs {
		if !a.hasDocument(docID) {
			return nil, fmt.Errorf("%w: %q", ErrDocumentNotFound, docID)
		}
	}

	// Resolve company copy to freeze: use currentCompany if provided, otherwise fallback to a.company
	var companyToFreeze *Company
	if currentCompany != nil {
		if currentCompany.ID() != a.company.ID() {
			return nil, errors.New("current company ID does not match application company ID")
		}
		companyToFreeze = currentCompany.DeepCopy()
	} else {
		companyToFreeze = a.company.DeepCopy()
	}

	now := time.Now().UTC()
	subAnswers := make(map[string]string, len(answers))
	for k, v := range answers {
		subAnswers[k] = strings.TrimSpace(v)
	}

	sub := Submission{
		ID:                    cleanSubID,
		Type:                  SubmissionTypeInformationResponse,
		Status:                SubmissionStatusPending,
		CompanyCopy:           companyToFreeze,
		OnboardingDetailsCopy: a.platform.OnboardingDetails.Clone(),
		DocumentIDs:           append([]string(nil), docIDs...),
		InformationRequestID:  requestID,
		Answers:               subAnswers,
		SubmittingActor:       strings.TrimSpace(actor),
		CreatedAt:             now,
	}

	a.informationRequests[rfiIndex].Status = InformationRequestStatusResponsePending
	a.submissions = append(a.submissions, sub.Clone())
	a.touch()

	return &sub, nil
}

// ResolveInformationRequest marks an RFI as RESOLVED following platform confirmation.
// Guarded against terminal state mutation; idempotent on already-resolved requests.
func (a *OnboardingApplication) ResolveInformationRequest(requestID string) error {
	rfiIndex := a.findInformationRequestIndex(requestID)
	if rfiIndex == -1 {
		return ErrInformationRequestNotFound
	}

	if a.informationRequests[rfiIndex].Status == InformationRequestStatusResolved {
		return nil // idempotent duplicate, zero touch
	}

	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}

	if a.status != ApplicationStatusInformationRequired && a.status != ApplicationStatusUnderReview {
		return errors.New("operation permitted only while application is in INFORMATION_REQUIRED or UNDER_REVIEW status")
	}

	now := time.Now().UTC()
	a.informationRequests[rfiIndex].Status = InformationRequestStatusResolved
	a.informationRequests[rfiIndex].ResolvedAt = &now

	if a.status == ApplicationStatusInformationRequired && !a.hasActiveInformationRequests() {
		a.status = ApplicationStatusUnderReview
	}

	a.touch()
	return nil
}

// RecordPlatformVerificationRejection closes application as REJECTED.
// Original submissions remain ACCEPTED; platform decision is set to REJECTED.
// Must enforce legal post-submission state; rejects DRAFT applications.
func (a *OnboardingApplication) RecordPlatformVerificationRejection(actor string) error {
	if a.status == ApplicationStatusRejected && a.platform.Decision != nil && *a.platform.Decision == PlatformDecisionRejected {
		return nil // duplicate, zero touch
	}
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}
	if a.status != ApplicationStatusUnderReview && a.status != ApplicationStatusInformationRequired && a.status != ApplicationStatusSubmissionPending {
		return fmt.Errorf("verification rejection requires post-submission review state, got %s", a.status)
	}

	now := time.Now().UTC()
	a.status = ApplicationStatusRejected
	rejDecision := PlatformDecisionRejected
	a.platform.Decision = &rejDecision
	a.closedAt = &now
	a.touch()
	return nil
}

// RecordPlatformAccountActivation completes application upon confirmed PlatformAccount activation.
// Enforces post-submission review state, matching Company association, platform name, environment, and existing linked ID.
func (a *OnboardingApplication) RecordPlatformAccountActivation(account *PlatformAccount) error {
	if account == nil {
		return errors.New("platform account must not be nil")
	}
	if a.status == ApplicationStatusCompleted && a.platform.PlatformAccountID == account.ID() {
		return nil // duplicate, zero touch
	}
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}
	if a.status != ApplicationStatusUnderReview && a.status != ApplicationStatusInformationRequired {
		return fmt.Errorf("activation requires post-submission review state, got %s", a.status)
	}
	if account.Status() != PlatformAccountStatusActive {
		return ErrPlatformAccountNotActive
	}
	if account.CompanyID() != a.company.ID() {
		return errors.New("platform account company ID does not match application company ID")
	}
	if account.PlatformName() != a.platform.Name {
		return errors.New("platform account platform name does not match application platform")
	}
	if a.platform.Environment != "" && account.Environment() != "" && account.Environment() != a.platform.Environment {
		return errors.New("platform account environment does not match application platform environment")
	}
	if a.platform.PlatformAccountID != "" && a.platform.PlatformAccountID != account.ID() {
		return errors.New("platform account ID does not match already linked platform account ID")
	}

	now := time.Now().UTC()
	a.status = ApplicationStatusCompleted
	appDecision := PlatformDecisionApproved
	a.platform.Decision = &appDecision
	a.platform.PlatformAccountID = account.ID()
	a.closedAt = &now
	a.touch()
	return nil
}

// HandlePlatformSuspension processes a platform account suspension event.
// COMPLETED status is completely immutable and never rewritten.
// Mid-review suspension only rejects on explicit terminal refusal; otherwise flags for operator review.
func (a *OnboardingApplication) HandlePlatformSuspension(explicitTerminalRefusal bool) error {
	if a.status == ApplicationStatusCompleted {
		// Later suspension never rewrites COMPLETED history, zero touch
		return nil
	}
	if explicitTerminalRefusal && a.status == ApplicationStatusRejected && a.platform.Decision != nil && *a.platform.Decision == PlatformDecisionRejected {
		return nil // duplicate, zero touch
	}
	if a.isTerminal() {
		return ErrTerminalStatusImmutable
	}
	if a.status == ApplicationStatusDraft {
		return errors.New("cannot process platform suspension on draft application")
	}

	if explicitTerminalRefusal {
		now := time.Now().UTC()
		a.status = ApplicationStatusRejected
		rejDecision := PlatformDecisionRejected
		a.platform.Decision = &rejDecision
		a.closedAt = &now
	} else {
		if a.operatorReviewFlagged {
			return nil // duplicate, zero touch
		}
		a.operatorReviewFlagged = true
	}

	a.touch()
	return nil
}

func (a *OnboardingApplication) isTerminal() bool {
	return a.status == ApplicationStatusCompleted ||
		a.status == ApplicationStatusRejected ||
		a.status == ApplicationStatusCancelled
}

func (a *OnboardingApplication) hasDocument(docID string) bool {
	for _, d := range a.documents {
		if d.ID == docID {
			return true
		}
	}
	return false
}

func (a *OnboardingApplication) findSubmissionIndex(id string) int {
	for i, s := range a.submissions {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func (a *OnboardingApplication) findInformationRequestIndex(id string) int {
	for i, r := range a.informationRequests {
		if r.ID == id {
			return i
		}
	}
	return -1
}

func (a *OnboardingApplication) hasActiveInformationRequests() bool {
	for _, r := range a.informationRequests {
		if r.Status == InformationRequestStatusOpen || r.Status == InformationRequestStatusResponsePending {
			return true
		}
	}
	return false
}

func (a *OnboardingApplication) touch() {
	a.revision++
	a.updatedAt = time.Now().UTC()
}

// applicationJSON represents the serialization envelope.
type applicationJSON struct {
	ID                    string               `json:"application_id"`
	Company               *Company             `json:"company"`
	Platform              ApplicationPlatform  `json:"platform"`
	Status                ApplicationStatus    `json:"status"`
	Documents             []Document           `json:"documents"`
	Submissions           []Submission         `json:"submissions"`
	InformationRequests   []InformationRequest `json:"information_requests"`
	Creator               string               `json:"creator"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
	ClosedAt              *time.Time           `json:"closed_at,omitempty"`
	Revision              int                  `json:"revision"`
	OperatorReviewFlagged bool                 `json:"operator_review_flagged"`
}

// MarshalJSON provides standardized serialization.
func (a *OnboardingApplication) MarshalJSON() ([]byte, error) {
	return json.Marshal(applicationJSON{
		ID:                    a.id,
		Company:               a.Company(),
		Platform:              a.Platform(),
		Status:                a.status,
		Documents:             a.Documents(),
		Submissions:           a.Submissions(),
		InformationRequests:   a.InformationRequests(),
		Creator:               a.creator,
		CreatedAt:             a.createdAt,
		UpdatedAt:             a.updatedAt,
		ClosedAt:              a.closedAt,
		Revision:              a.revision,
		OperatorReviewFlagged: a.operatorReviewFlagged,
	})
}

// UnmarshalJSON reconstitutes an OnboardingApplication from JSON.
// Failure does not mutate receiver state.
func (a *OnboardingApplication) UnmarshalJSON(data []byte) error {
	var j applicationJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	recon, err := ReconstituteOnboardingApplication(ReconstituteApplicationParams{
		ID:                    j.ID,
		Company:               j.Company,
		Platform:              j.Platform,
		Status:                j.Status,
		Documents:             j.Documents,
		Submissions:           j.Submissions,
		InformationRequests:   j.InformationRequests,
		Creator:               j.Creator,
		CreatedAt:             j.CreatedAt,
		UpdatedAt:             j.UpdatedAt,
		ClosedAt:              j.ClosedAt,
		Revision:              j.Revision,
		OperatorReviewFlagged: j.OperatorReviewFlagged,
	})
	if err != nil {
		return err
	}
	*a = *recon
	return nil
}
