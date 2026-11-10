// Package domain defines the core domain entities, value objects, and business invariants
// for the company onboarding service.
package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// CompanyStatus represents the lifecycle state of a registered Company.
type CompanyStatus string

const (
	// CompanyStatusActive indicates an active company entity available for onboarding or operation.
	CompanyStatusActive CompanyStatus = "ACTIVE"
	// CompanyStatusArchived indicates an archived company with no active operations.
	CompanyStatusArchived CompanyStatus = "ARCHIVED"
)

// Domain sentinel errors.
var (
	ErrEmptyCompanyID               = errors.New("company ID must not be empty")
	ErrCompanyArchived              = errors.New("company is archived: modifications are not permitted")
	ErrCompanyAlreadyArchived       = errors.New("company is already archived")
	ErrCannotDeleteSubmittedCompany = errors.New("cannot permanently delete company with submitted history: submitted records must be preserved")
	ErrProductsTooLong              = errors.New("products text exceeds maximum length of 500 characters")
	ErrInvalidCountryCode           = errors.New("invalid country code: must be a 2-letter uppercase ISO 3166-1 alpha-2 code")
	ErrInvalidEmail                 = errors.New("invalid email address format")
	ErrInvalidWebsite               = errors.New("invalid website format: must be an absolute http or https URL")
	ErrInvalidDateFormat            = errors.New("invalid date format: must be YYYY-MM-DD")
	ErrInvalidDateOfBirth           = errors.New("date of birth must be in the past")
	ErrInvalidIdentificationDates   = errors.New("identification expires_on must be after issued_on")
	ErrActorRequired                = errors.New("actor is required for legal identifier correction on submitted company")
	ErrReasonRequired               = errors.New("reason is required for legal identifier correction on submitted company")
	ErrEmptyPersonID                = errors.New("person ID must not be empty")
	ErrPersonNotFound               = errors.New("person not found")
	ErrDuplicatePersonID            = errors.New("person with this ID already exists in company")
	ErrEmptyIdentificationID        = errors.New("identification ID must not be empty")
	ErrIdentificationNotFound       = errors.New("identification not found")
	ErrDuplicateIdentificationID    = errors.New("identification with this ID already exists on person")
	ErrReplacementRequiresNewID     = errors.New("identification replacement must specify a new identification ID")
	ErrInvalidStatus                = errors.New("invalid company status")
	ErrInvalidRevision              = errors.New("company revision must be positive")
)

// Date represents a calendar date in ISO 8601 YYYY-MM-DD format without time or timezone components.
type Date string

// Validate validates that the date string matches YYYY-MM-DD and is a real calendar date.
func (d Date) Validate() error {
	if d == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", string(d))
	if err != nil || t.Format("2006-01-02") != string(d) {
		return ErrInvalidDateFormat
	}
	return nil
}

// Time converts the Date to a UTC time.Time at midnight.
func (d Date) Time() (time.Time, error) {
	if d == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", string(d))
}

// After returns true if date d is chronologically after other.
func (d Date) After(other Date) bool {
	if d == "" || other == "" {
		return false
	}
	return string(d) > string(other)
}

// Before returns true if date d is chronologically before other.
func (d Date) Before(other Date) bool {
	if d == "" || other == "" {
		return false
	}
	return string(d) < string(other)
}

// Address is an immutable value object representing a physical or registered location.
// Absent operating address does not silently imply registered address.
type Address struct {
	Line1      string `json:"line_1,omitempty"`
	Line2      string `json:"line_2,omitempty"`
	City       string `json:"city,omitempty"`
	State      string `json:"state,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"` // ISO 3166-1 alpha-2, e.g. "US"
}

// Validate validates supplied address values. Incomplete fields are permitted in drafts.
func (a Address) Validate() error {
	if a.Country != "" && !isValidCountryCode(a.Country) {
		return ErrInvalidCountryCode
	}
	return nil
}

// Clone returns a deep copy of the address pointer, or nil if receiver pointer is nil.
func (a *Address) Clone() *Address {
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

// RegistrationIdentifier represents official government registration numbers (e.g. EIN, state number).
type RegistrationIdentifier struct {
	Type           string `json:"type,omitempty"`
	Value          string `json:"value,omitempty"`
	IssuingCountry string `json:"issuing_country,omitempty"` // ISO 3166-1 alpha-2
}

// Validate validates supplied registration identifier values.
func (r RegistrationIdentifier) Validate() error {
	if r.IssuingCountry != "" && !isValidCountryCode(r.IssuingCountry) {
		return ErrInvalidCountryCode
	}
	return nil
}

// BusinessProfile holds non-legal business operational information.
type BusinessProfile struct {
	Description        string   `json:"description,omitempty"`
	Industry           string   `json:"industry,omitempty"` // Internal company classification
	Website            string   `json:"website,omitempty"`
	Email              string   `json:"email,omitempty"`
	Phone              string   `json:"phone,omitempty"`
	RegisteredAddress  Address  `json:"registered_address"`
	OperatingAddress   *Address `json:"operating_address,omitempty"`
	OperatingCountries []string `json:"operating_countries,omitempty"`
	Products           string   `json:"products,omitempty"` // Goods/services description, max 500 Unicode runes
}

// Validate validates supplied business profile values according to domain invariants.
func (bp BusinessProfile) Validate() error {
	if utf8.RuneCountInString(bp.Products) > 500 {
		return ErrProductsTooLong
	}
	if bp.Email != "" && !isValidEmail(bp.Email) {
		return ErrInvalidEmail
	}
	if bp.Website != "" && !isValidWebsite(bp.Website) {
		return ErrInvalidWebsite
	}
	if err := bp.RegisteredAddress.Validate(); err != nil {
		return fmt.Errorf("registered address: %w", err)
	}
	if bp.OperatingAddress != nil {
		if err := bp.OperatingAddress.Validate(); err != nil {
			return fmt.Errorf("operating address: %w", err)
		}
	}
	for _, c := range bp.OperatingCountries {
		if !isValidCountryCode(c) {
			return ErrInvalidCountryCode
		}
	}
	return nil
}

// Clone returns a deep copy of the BusinessProfile preventing slice and pointer aliasing.
func (bp BusinessProfile) Clone() BusinessProfile {
	cp := bp
	cp.OperatingAddress = bp.OperatingAddress.Clone()
	if bp.OperatingCountries != nil {
		cp.OperatingCountries = append([]string(nil), bp.OperatingCountries...)
	}
	return cp
}

// Identification represents an official identity document associated with a Person.
type Identification struct {
	ID             string `json:"identification_id"`
	Type           string `json:"type,omitempty"`
	IssuingCountry string `json:"issuing_country,omitempty"` // ISO 3166-1 alpha-2
	Number         string `json:"number,omitempty"`
	IssuedOn       Date   `json:"issued_on,omitempty"`
	ExpiresOn      Date   `json:"expires_on,omitempty"`
}

// Validate validates supplied identification values.
func (i Identification) Validate() error {
	if strings.TrimSpace(i.ID) == "" {
		return ErrEmptyIdentificationID
	}
	if i.IssuingCountry != "" && !isValidCountryCode(i.IssuingCountry) {
		return ErrInvalidCountryCode
	}
	if err := i.IssuedOn.Validate(); err != nil {
		return err
	}
	if err := i.ExpiresOn.Validate(); err != nil {
		return err
	}
	if i.IssuedOn != "" && i.ExpiresOn != "" {
		if !i.ExpiresOn.After(i.IssuedOn) {
			return ErrInvalidIdentificationDates
		}
	}
	return nil
}

// Clone returns a deep copy of the Identification.
func (i Identification) Clone() Identification {
	return i
}

// Person represents a Company-scoped individual child entity.
// NOTE: Residential address and ownership percentage are owned by the Onboarding Application
// and are intentionally omitted from Company Person per domain boundaries.
type Person struct {
	ID              string           `json:"person_id"`
	FirstName       string           `json:"first_name,omitempty"`
	MiddleName      string           `json:"middle_name,omitempty"`
	LastName        string           `json:"last_name,omitempty"`
	DateOfBirth     Date             `json:"date_of_birth,omitempty"`
	Nationality     string           `json:"nationality,omitempty"` // ISO 3166-1 alpha-2
	Email           string           `json:"email,omitempty"`
	Phone           string           `json:"phone,omitempty"`
	Role            string           `json:"role,omitempty"` // Free-text job title (e.g. "Director")
	Identifications []Identification `json:"identifications,omitempty"`
}

// Validate validates supplied Person values.
func (p Person) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return ErrEmptyPersonID
	}
	if p.Nationality != "" && !isValidCountryCode(p.Nationality) {
		return ErrInvalidCountryCode
	}
	if p.Email != "" && !isValidEmail(p.Email) {
		return ErrInvalidEmail
	}
	if p.DateOfBirth != "" {
		if err := p.DateOfBirth.Validate(); err != nil {
			return err
		}
		dobTime, err := p.DateOfBirth.Time()
		if err != nil {
			return fmt.Errorf("%w: invalid date of birth", ErrInvalidDateFormat)
		}
		now := time.Now().UTC()
		todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		if !dobTime.Before(todayMidnight) {
			return ErrInvalidDateOfBirth
		}
	}
	seenIdentIDs := make(map[string]struct{}, len(p.Identifications))
	for _, ident := range p.Identifications {
		if err := ident.Validate(); err != nil {
			return fmt.Errorf("identification: %w", err)
		}
		if _, exists := seenIdentIDs[ident.ID]; exists {
			return fmt.Errorf("%w: duplicate identification ID %q", ErrDuplicateIdentificationID, ident.ID)
		}
		seenIdentIDs[ident.ID] = struct{}{}
	}
	return nil
}

// Clone returns a deep copy of Person preventing slice and pointer aliasing.
func (p Person) Clone() Person {
	cp := p
	if p.Identifications != nil {
		cp.Identifications = make([]Identification, len(p.Identifications))
		copy(cp.Identifications, p.Identifications)
	}
	return cp
}

// LegalIdentifierAuditRecord records an audited change to a Company's legal identity.
type LegalIdentifierAuditRecord struct {
	Timestamp                  time.Time                `json:"timestamp"`
	Actor                      string                   `json:"actor"`
	Reason                     string                   `json:"reason"`
	OldLegalName               string                   `json:"old_legal_name"`
	NewLegalName               string                   `json:"new_legal_name"`
	OldEntityType              string                   `json:"old_entity_type"`
	NewEntityType              string                   `json:"new_entity_type"`
	OldRegistrationCountry     string                   `json:"old_registration_country"`
	NewRegistrationCountry     string                   `json:"new_registration_country"`
	OldRegistrationState       string                   `json:"old_registration_state"`
	NewRegistrationState       string                   `json:"new_registration_state"`
	OldRegistrationIdentifiers []RegistrationIdentifier `json:"old_registration_identifiers,omitempty"`
	NewRegistrationIdentifiers []RegistrationIdentifier `json:"new_registration_identifiers,omitempty"`
}

// LegalIdentifierCorrectionParams carries parameters for updating legal identifiers.
type LegalIdentifierCorrectionParams struct {
	LegalName               string
	EntityType              string
	RegistrationCountry     string
	RegistrationState       string
	RegistrationIdentifiers []RegistrationIdentifier
	Actor                   string
	Reason                  string
}

// Company represents a registered legal entity aggregate root.
// Duplicate legal identities are permitted without warnings.
type Company struct {
	id                  string
	status              CompanyStatus
	revision            int
	createdAt           time.Time
	updatedAt           time.Time
	hasSubmittedHistory bool

	legalName               string
	tradingName             string
	entityType              string
	registrationCountry     string // ISO 3166-1 alpha-2
	registrationState       string
	registrationIdentifiers []RegistrationIdentifier

	businessProfile BusinessProfile
	people          []Person
	auditTrail      []LegalIdentifierAuditRecord
}

// NewDraftCompany initializes a new draft Company aggregate.
// Incomplete drafts are permitted; supplied values are validated.
func NewDraftCompany(id string, legalName string) (*Company, error) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return nil, ErrEmptyCompanyID
	}

	now := time.Now().UTC()
	return &Company{
		id:                      cleanID,
		status:                  CompanyStatusActive,
		revision:                1,
		createdAt:               now,
		updatedAt:               now,
		hasSubmittedHistory:     false,
		legalName:               strings.TrimSpace(legalName),
		registrationIdentifiers: make([]RegistrationIdentifier, 0),
		people:                  make([]Person, 0),
		auditTrail:              make([]LegalIdentifierAuditRecord, 0),
	}, nil
}

// ReconstituteCompanyParams carries state to reconstitute an existing Company from persistence.
type ReconstituteCompanyParams struct {
	ID                      string
	Status                  CompanyStatus
	Revision                int
	CreatedAt               time.Time
	UpdatedAt               time.Time
	HasSubmittedHistory     bool
	LegalName               string
	TradingName             string
	EntityType              string
	RegistrationCountry     string
	RegistrationState       string
	RegistrationIdentifiers []RegistrationIdentifier
	BusinessProfile         BusinessProfile
	People                  []Person
	AuditTrail              []LegalIdentifierAuditRecord
}

// ReconstituteCompany recreates a Company aggregate from storage without generating new audit events.
// It verifies invariants including status enum, positive revision, country codes, profile, and duplicate child IDs.
func ReconstituteCompany(params ReconstituteCompanyParams) (*Company, error) {
	cleanID := strings.TrimSpace(params.ID)
	if cleanID == "" {
		return nil, ErrEmptyCompanyID
	}
	if params.Status != CompanyStatusActive && params.Status != CompanyStatusArchived {
		return nil, fmt.Errorf("%w: received %q", ErrInvalidStatus, params.Status)
	}
	if params.Revision <= 0 {
		return nil, fmt.Errorf("%w: received %d", ErrInvalidRevision, params.Revision)
	}
	if params.RegistrationCountry != "" && !isValidCountryCode(params.RegistrationCountry) {
		return nil, ErrInvalidCountryCode
	}
	for _, id := range params.RegistrationIdentifiers {
		if err := id.Validate(); err != nil {
			return nil, err
		}
	}
	if err := params.BusinessProfile.Validate(); err != nil {
		return nil, err
	}

	seenPersonIDs := make(map[string]struct{}, len(params.People))
	clonedPeople := make([]Person, len(params.People))
	for i, p := range params.People {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("reconstitute person: %w", err)
		}
		if _, exists := seenPersonIDs[p.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate person ID %q", ErrDuplicatePersonID, p.ID)
		}
		seenPersonIDs[p.ID] = struct{}{}
		clonedPeople[i] = p.Clone()
	}

	clonedAuditTrail := make([]LegalIdentifierAuditRecord, len(params.AuditTrail))
	for i, a := range params.AuditTrail {
		cp := a
		cp.OldRegistrationIdentifiers = append([]RegistrationIdentifier(nil), a.OldRegistrationIdentifiers...)
		cp.NewRegistrationIdentifiers = append([]RegistrationIdentifier(nil), a.NewRegistrationIdentifiers...)
		clonedAuditTrail[i] = cp
	}

	return &Company{
		id:                      cleanID,
		status:                  params.Status,
		revision:                params.Revision,
		createdAt:               params.CreatedAt,
		updatedAt:               params.UpdatedAt,
		hasSubmittedHistory:     params.HasSubmittedHistory,
		legalName:               params.LegalName,
		tradingName:             params.TradingName,
		entityType:              params.EntityType,
		registrationCountry:     params.RegistrationCountry,
		registrationState:       params.RegistrationState,
		registrationIdentifiers: append([]RegistrationIdentifier(nil), params.RegistrationIdentifiers...),
		businessProfile:         params.BusinessProfile.Clone(),
		people:                  clonedPeople,
		auditTrail:              clonedAuditTrail,
	}, nil
}

// ID returns the company's unique identifier.
func (c *Company) ID() string {
	return c.id
}

// Status returns the current lifecycle status (ACTIVE or ARCHIVED).
func (c *Company) Status() CompanyStatus {
	return c.status
}

// Revision returns the aggregate revision number.
func (c *Company) Revision() int {
	return c.revision
}

// CreatedAt returns the initial creation timestamp.
func (c *Company) CreatedAt() time.Time {
	return c.createdAt
}

// UpdatedAt returns the timestamp of the most recent modification.
func (c *Company) UpdatedAt() time.Time {
	return c.updatedAt
}

// HasSubmittedHistory indicates whether this company has ever entered platform onboarding.
func (c *Company) HasSubmittedHistory() bool {
	return c.hasSubmittedHistory
}

// LegalName returns the official registered legal name.
func (c *Company) LegalName() string {
	return c.legalName
}

// TradingName returns the optional DBA or trading name.
func (c *Company) TradingName() string {
	return c.tradingName
}

// EntityType returns the legal entity structure (e.g. LLC, Corporation).
func (c *Company) EntityType() string {
	return c.entityType
}

// RegistrationCountry returns the country of legal registration (ISO 3166-1 alpha-2).
func (c *Company) RegistrationCountry() string {
	return c.registrationCountry
}

// RegistrationState returns the jurisdiction state or province of registration.
func (c *Company) RegistrationState() string {
	return c.registrationState
}

// RegistrationIdentifiers returns a deep copy of the company's registration identifiers.
func (c *Company) RegistrationIdentifiers() []RegistrationIdentifier {
	return append([]RegistrationIdentifier(nil), c.registrationIdentifiers...)
}

// BusinessProfile returns a deep copy of the BusinessProfile to protect internal state.
func (c *Company) BusinessProfile() BusinessProfile {
	return c.businessProfile.Clone()
}

// People returns a deep copy of all associated people entities.
func (c *Company) People() []Person {
	out := make([]Person, len(c.people))
	for i, p := range c.people {
		out[i] = p.Clone()
	}
	return out
}

// Person retrieves a deep copy of a specific Person by ID.
func (c *Company) Person(personID string) (Person, bool) {
	for _, p := range c.people {
		if p.ID == personID {
			return p.Clone(), true
		}
	}
	return Person{}, false
}

// LegalIdentifierAuditTrail returns a copy of the legal identifier correction audit records.
func (c *Company) LegalIdentifierAuditTrail() []LegalIdentifierAuditRecord {
	out := make([]LegalIdentifierAuditRecord, len(c.auditTrail))
	for i, a := range c.auditTrail {
		cp := a
		cp.OldRegistrationIdentifiers = append([]RegistrationIdentifier(nil), a.OldRegistrationIdentifiers...)
		cp.NewRegistrationIdentifiers = append([]RegistrationIdentifier(nil), a.NewRegistrationIdentifiers...)
		out[i] = cp
	}
	return out
}

// UpdateTradingName updates the optional trading / DBA name.
func (c *Company) UpdateTradingName(tradingName string) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	c.tradingName = strings.TrimSpace(tradingName)
	c.touch()
	return nil
}

// UpdateProfile validates and updates the business profile.
func (c *Company) UpdateProfile(profile BusinessProfile) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	c.businessProfile = profile.Clone()
	c.touch()
	return nil
}

// CorrectLegalIdentifiers updates legal identifiers.
// When the company has submitted history, actor and reason are strictly required for auditability.
// Before submission, corrections are freely permitted without mandatory reason.
func (c *Company) CorrectLegalIdentifiers(params LegalIdentifierCorrectionParams) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	if params.RegistrationCountry != "" && !isValidCountryCode(params.RegistrationCountry) {
		return ErrInvalidCountryCode
	}
	for _, id := range params.RegistrationIdentifiers {
		if err := id.Validate(); err != nil {
			return err
		}
	}

	actor := strings.TrimSpace(params.Actor)
	reason := strings.TrimSpace(params.Reason)

	if c.hasSubmittedHistory {
		if actor == "" {
			return ErrActorRequired
		}
		if reason == "" {
			return ErrReasonRequired
		}

		auditEntry := LegalIdentifierAuditRecord{
			Timestamp:                  time.Now().UTC(),
			Actor:                      actor,
			Reason:                     reason,
			OldLegalName:               c.legalName,
			NewLegalName:               strings.TrimSpace(params.LegalName),
			OldEntityType:              c.entityType,
			NewEntityType:              strings.TrimSpace(params.EntityType),
			OldRegistrationCountry:     c.registrationCountry,
			NewRegistrationCountry:     strings.TrimSpace(params.RegistrationCountry),
			OldRegistrationState:       c.registrationState,
			NewRegistrationState:       strings.TrimSpace(params.RegistrationState),
			OldRegistrationIdentifiers: append([]RegistrationIdentifier(nil), c.registrationIdentifiers...),
			NewRegistrationIdentifiers: append([]RegistrationIdentifier(nil), params.RegistrationIdentifiers...),
		}
		c.auditTrail = append(c.auditTrail, auditEntry)
	}

	c.legalName = strings.TrimSpace(params.LegalName)
	c.entityType = strings.TrimSpace(params.EntityType)
	c.registrationCountry = strings.TrimSpace(params.RegistrationCountry)
	c.registrationState = strings.TrimSpace(params.RegistrationState)
	c.registrationIdentifiers = append([]RegistrationIdentifier(nil), params.RegistrationIdentifiers...)

	c.touch()
	return nil
}

// AddPerson validates and appends a new Person child entity.
func (c *Company) AddPerson(p Person) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, existing := range c.people {
		if existing.ID == p.ID {
			return fmt.Errorf("%w: %q", ErrDuplicatePersonID, p.ID)
		}
	}
	c.people = append(c.people, p.Clone())
	c.touch()
	return nil
}

// UpdatePerson updates an existing Person entity.
func (c *Company) UpdatePerson(p Person) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for i, existing := range c.people {
		if existing.ID == p.ID {
			c.people[i] = p.Clone()
			c.touch()
			return nil
		}
	}
	return ErrPersonNotFound
}

// RemovePerson removes a Person entity by ID.
func (c *Company) RemovePerson(personID string) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	for i, existing := range c.people {
		if existing.ID == personID {
			c.people = append(c.people[:i], c.people[i+1:]...)
			c.touch()
			return nil
		}
	}
	return ErrPersonNotFound
}

// AddIdentification appends an Identification to a Person.
func (c *Company) AddIdentification(personID string, ident Identification) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	if err := ident.Validate(); err != nil {
		return err
	}
	for i, p := range c.people {
		if p.ID == personID {
			for _, existing := range p.Identifications {
				if existing.ID == ident.ID {
					return fmt.Errorf("%w: %q", ErrDuplicateIdentificationID, ident.ID)
				}
			}
			c.people[i].Identifications = append(c.people[i].Identifications, ident.Clone())
			c.touch()
			return nil
		}
	}
	return ErrPersonNotFound
}

// ReplaceIdentification replaces an existing Identification on a Person.
// In accordance with domain rules, replacement creates a new record and must specify a new identification ID.
// Prior snapshots remain unaffected as they hold independent deep copies.
func (c *Company) ReplaceIdentification(personID string, oldIdentID string, newIdent Identification) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	if strings.TrimSpace(oldIdentID) == "" {
		return ErrIdentificationNotFound
	}
	if err := newIdent.Validate(); err != nil {
		return err
	}
	if newIdent.ID == oldIdentID {
		return ErrReplacementRequiresNewID
	}

	for pi, p := range c.people {
		if p.ID == personID {
			foundIndex := -1
			for ii, existing := range p.Identifications {
				if existing.ID == oldIdentID {
					foundIndex = ii
				} else if existing.ID == newIdent.ID {
					return fmt.Errorf("%w: %q", ErrDuplicateIdentificationID, newIdent.ID)
				}
			}
			if foundIndex == -1 {
				return ErrIdentificationNotFound
			}
			c.people[pi].Identifications[foundIndex] = newIdent.Clone()
			c.touch()
			return nil
		}
	}
	return ErrPersonNotFound
}

// RemoveIdentification removes an Identification from a Person.
func (c *Company) RemoveIdentification(personID string, identID string) error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	for pi, p := range c.people {
		if p.ID == personID {
			for ii, existing := range p.Identifications {
				if existing.ID == identID {
					c.people[pi].Identifications = append(c.people[pi].Identifications[:ii], c.people[pi].Identifications[ii+1:]...)
					c.touch()
					return nil
				}
			}
			return ErrIdentificationNotFound
		}
	}
	return ErrPersonNotFound
}

// RecordSubmission flags that the company has entered platform onboarding.
// Future legal identifier corrections will require an audited actor and reason.
func (c *Company) RecordSubmission() error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyArchived
	}
	c.hasSubmittedHistory = true
	c.touch()
	return nil
}

// CanArchive indicates whether the company is currently eligible to be archived.
// NOTE: Application-layer open application check must coordinate transactionally in Task 5.
func (c *Company) CanArchive() bool {
	return c.status == CompanyStatusActive
}

// Archive transitions the company status from ACTIVE to ARCHIVED.
// Coordinating that no open applications exist must be executed in the application/repository layer.
func (c *Company) Archive() error {
	if c.status == CompanyStatusArchived {
		return ErrCompanyAlreadyArchived
	}
	c.status = CompanyStatusArchived
	c.touch()
	return nil
}

// CanDelete returns true only if the company has never entered platform onboarding.
func (c *Company) CanDelete() bool {
	return !c.hasSubmittedHistory
}

// Delete permanently marks a draft eligible for deletion.
// Attempting to delete a company that has entered platform onboarding is strictly forbidden.
func (c *Company) Delete() error {
	if !c.CanDelete() {
		return ErrCannotDeleteSubmittedCompany
	}
	return nil
}

// DeepCopy produces a completely detached copy of Company, ensuring zero shared pointer or slice state.
func (c *Company) DeepCopy() *Company {
	clonedAudit := make([]LegalIdentifierAuditRecord, len(c.auditTrail))
	for i, a := range c.auditTrail {
		cp := a
		cp.OldRegistrationIdentifiers = append([]RegistrationIdentifier(nil), a.OldRegistrationIdentifiers...)
		cp.NewRegistrationIdentifiers = append([]RegistrationIdentifier(nil), a.NewRegistrationIdentifiers...)
		clonedAudit[i] = cp
	}

	cp := &Company{
		id:                      c.id,
		status:                  c.status,
		revision:                c.revision,
		createdAt:               c.createdAt,
		updatedAt:               c.updatedAt,
		hasSubmittedHistory:     c.hasSubmittedHistory,
		legalName:               c.legalName,
		tradingName:             c.tradingName,
		entityType:              c.entityType,
		registrationCountry:     c.registrationCountry,
		registrationState:       c.registrationState,
		registrationIdentifiers: append([]RegistrationIdentifier(nil), c.registrationIdentifiers...),
		businessProfile:         c.businessProfile.Clone(),
		people:                  make([]Person, len(c.people)),
		auditTrail:              clonedAudit,
	}
	for i, p := range c.people {
		cp.people[i] = p.Clone()
	}
	return cp
}

func (c *Company) touch() {
	c.revision++
	c.updatedAt = time.Now().UTC()
}

// companyJSON is an internal serialization envelope for JSON representation matching the agreed design schema.
type companyJSON struct {
	ID                      string                       `json:"company_id"`
	Status                  CompanyStatus                `json:"status"`
	Revision                int                          `json:"revision"`
	CreatedAt               time.Time                    `json:"created_at"`
	UpdatedAt               time.Time                    `json:"updated_at"`
	HasSubmittedHistory     bool                         `json:"has_submitted_history"`
	LegalName               string                       `json:"legal_name,omitempty"`
	TradingName             string                       `json:"trading_name,omitempty"`
	EntityType              string                       `json:"entity_type,omitempty"`
	RegistrationCountry     string                       `json:"registration_country,omitempty"`
	RegistrationState       string                       `json:"registration_state,omitempty"`
	RegistrationIdentifiers []RegistrationIdentifier     `json:"registration_identifiers"`
	BusinessProfile         BusinessProfile              `json:"business_profile"`
	People                  []Person                     `json:"people"`
	AuditTrail              []LegalIdentifierAuditRecord `json:"legal_identifier_audit_trail,omitempty"`
}

// MarshalJSON provides a standardized JSON serialization.
func (c *Company) MarshalJSON() ([]byte, error) {
	return json.Marshal(companyJSON{
		ID:                      c.id,
		Status:                  c.status,
		Revision:                c.revision,
		CreatedAt:               c.createdAt,
		UpdatedAt:               c.updatedAt,
		HasSubmittedHistory:     c.hasSubmittedHistory,
		LegalName:               c.legalName,
		TradingName:             c.tradingName,
		EntityType:              c.entityType,
		RegistrationCountry:     c.registrationCountry,
		RegistrationState:       c.registrationState,
		RegistrationIdentifiers: c.RegistrationIdentifiers(),
		BusinessProfile:         c.BusinessProfile(),
		People:                  c.People(),
		AuditTrail:              c.LegalIdentifierAuditTrail(),
	})
}

// UnmarshalJSON reconstitutes a Company from JSON.
func (c *Company) UnmarshalJSON(data []byte) error {
	var j companyJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	recon, err := ReconstituteCompany(ReconstituteCompanyParams{
		ID:                      j.ID,
		Status:                  j.Status,
		Revision:                j.Revision,
		CreatedAt:               j.CreatedAt,
		UpdatedAt:               j.UpdatedAt,
		HasSubmittedHistory:     j.HasSubmittedHistory,
		LegalName:               j.LegalName,
		TradingName:             j.TradingName,
		EntityType:              j.EntityType,
		RegistrationCountry:     j.RegistrationCountry,
		RegistrationState:       j.RegistrationState,
		RegistrationIdentifiers: j.RegistrationIdentifiers,
		BusinessProfile:         j.BusinessProfile,
		People:                  j.People,
		AuditTrail:              j.AuditTrail,
	})
	if err != nil {
		return err
	}
	*c = *recon
	return nil
}

// Helper validation functions.

// isValidCountryCode checks whether the country is in the official ISO 3166-1 alpha-2 registry.
func isValidCountryCode(s string) bool {
	_, ok := iso3166Alpha2[s]
	return ok
}

// isValidEmail checks for standard RFC 5322 email syntax with a dotted domain without echoing PII.
func isValidEmail(s string) bool {
	clean := strings.TrimSpace(s)
	if clean == "" || strings.Contains(clean, " ") {
		return false
	}
	addr, err := mail.ParseAddress(clean)
	if err != nil || addr.Address != clean {
		return false
	}
	parts := strings.Split(clean, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	domain := parts[1]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	return true
}

// isValidWebsite validates that the URL is an absolute http or https URL with a dotted host.
func isValidWebsite(s string) bool {
	clean := strings.TrimSpace(s)
	if clean == "" {
		return false
	}
	u, err := url.ParseRequestURI(clean)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" || !strings.Contains(u.Host, ".") {
		return false
	}
	return !strings.ContainsAny(u.Host, " \t\r\n")
}

// iso3166Alpha2 contains all officially assigned ISO 3166-1 alpha-2 country codes.
var iso3166Alpha2 = map[string]struct{}{
	"AD": {}, "AE": {}, "AF": {}, "AG": {}, "AI": {}, "AL": {}, "AM": {}, "AO": {}, "AQ": {}, "AR": {},
	"AS": {}, "AT": {}, "AU": {}, "AW": {}, "AX": {}, "AZ": {}, "BA": {}, "BB": {}, "BD": {}, "BE": {},
	"BF": {}, "BG": {}, "BH": {}, "BI": {}, "BJ": {}, "BL": {}, "BM": {}, "BN": {}, "BO": {}, "BQ": {},
	"BR": {}, "BS": {}, "BT": {}, "BV": {}, "BW": {}, "BY": {}, "BZ": {}, "CA": {}, "CC": {}, "CD": {},
	"CF": {}, "CG": {}, "CH": {}, "CI": {}, "CK": {}, "CL": {}, "CM": {}, "CN": {}, "CO": {}, "CR": {},
	"CU": {}, "CV": {}, "CW": {}, "CX": {}, "CY": {}, "CZ": {}, "DE": {}, "DJ": {}, "DK": {}, "DM": {},
	"DO": {}, "DZ": {}, "EC": {}, "EE": {}, "EG": {}, "EH": {}, "ER": {}, "ES": {}, "ET": {}, "FI": {},
	"FJ": {}, "FK": {}, "FM": {}, "FO": {}, "FR": {}, "GA": {}, "GB": {}, "GD": {}, "GE": {}, "GF": {},
	"GG": {}, "GH": {}, "GI": {}, "GL": {}, "GM": {}, "GN": {}, "GP": {}, "GQ": {}, "GR": {}, "GS": {},
	"GT": {}, "GU": {}, "GW": {}, "GY": {}, "HK": {}, "HM": {}, "HN": {}, "HR": {}, "HT": {}, "HU": {},
	"ID": {}, "IE": {}, "IL": {}, "IM": {}, "IN": {}, "IO": {}, "IQ": {}, "IR": {}, "IS": {}, "IT": {},
	"JE": {}, "JM": {}, "JO": {}, "JP": {}, "KE": {}, "KG": {}, "KH": {}, "KI": {}, "KM": {}, "KN": {},
	"KP": {}, "KR": {}, "KW": {}, "KY": {}, "KZ": {}, "LA": {}, "LB": {}, "LC": {}, "LI": {}, "LK": {},
	"LR": {}, "LS": {}, "LT": {}, "LU": {}, "LV": {}, "LY": {}, "MA": {}, "MC": {}, "MD": {}, "ME": {},
	"MF": {}, "MG": {}, "MH": {}, "MK": {}, "ML": {}, "MM": {}, "MN": {}, "MO": {}, "MP": {}, "MQ": {},
	"MR": {}, "MS": {}, "MT": {}, "MU": {}, "MV": {}, "MW": {}, "MX": {}, "MY": {}, "MZ": {}, "NA": {},
	"NC": {}, "NE": {}, "NF": {}, "NG": {}, "NI": {}, "NL": {}, "NO": {}, "NP": {}, "NR": {}, "NU": {},
	"NZ": {}, "OM": {}, "PA": {}, "PE": {}, "PF": {}, "PG": {}, "PH": {}, "PK": {}, "PL": {}, "PM": {},
	"PN": {}, "PR": {}, "PS": {}, "PT": {}, "PW": {}, "PY": {}, "QA": {}, "RE": {}, "RO": {}, "RS": {},
	"RU": {}, "RW": {}, "SA": {}, "SB": {}, "SC": {}, "SD": {}, "SE": {}, "SG": {}, "SH": {}, "SI": {},
	"SJ": {}, "SK": {}, "SL": {}, "SM": {}, "SN": {}, "SO": {}, "SR": {}, "SS": {}, "ST": {}, "SV": {},
	"SX": {}, "SY": {}, "SZ": {}, "TC": {}, "TD": {}, "TF": {}, "TG": {}, "TH": {}, "TJ": {}, "TK": {},
	"TL": {}, "TM": {}, "TN": {}, "TO": {}, "TR": {}, "TT": {}, "TV": {}, "TW": {}, "TZ": {}, "UA": {},
	"UG": {}, "UM": {}, "US": {}, "UY": {}, "UZ": {}, "VA": {}, "VC": {}, "VE": {}, "VG": {}, "VI": {},
	"VN": {}, "VU": {}, "WF": {}, "WS": {}, "YE": {}, "YT": {}, "ZA": {}, "ZM": {}, "ZW": {},
}
