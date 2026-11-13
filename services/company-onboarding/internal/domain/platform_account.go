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

// PlatformAccountStatus represents the connected account status observed on the platform.
type PlatformAccountStatus string

const (
	PlatformAccountStatusUnobserved     PlatformAccountStatus = "UNOBSERVED"
	PlatformAccountStatusCreated        PlatformAccountStatus = "CREATED"
	PlatformAccountStatusSubmitted      PlatformAccountStatus = "SUBMITTED"
	PlatformAccountStatusPending        PlatformAccountStatus = "PENDING"
	PlatformAccountStatusActive         PlatformAccountStatus = "ACTIVE"
	PlatformAccountStatusActionRequired PlatformAccountStatus = "ACTION_REQUIRED"
	PlatformAccountStatusSuspended      PlatformAccountStatus = "SUSPENDED"
	PlatformAccountStatusClosed         PlatformAccountStatus = "CLOSED"
)

// IsValidPlatformAccountStatus returns true if s is a non-empty string.
// Raw unknown platform statuses are accepted without mapping to closed whitelist enums.
func IsValidPlatformAccountStatus(s PlatformAccountStatus) bool {
	return strings.TrimSpace(string(s)) != ""
}

// Sentinel errors for PlatformAccount.
var (
	ErrInvalidPlatformAccountStatus  = errors.New("invalid platform account status")
	ErrEmptyPlatformName             = errors.New("platform name must not be empty")
	ErrEmptyEnvironment              = errors.New("environment must not be empty")
	ErrEmptyExternalAccountID        = errors.New("external account ID must not be empty")
	ErrExternalAccountIDReassignment = errors.New("external account ID cannot be reassigned once set")
	ErrStaleSyncTime                 = errors.New("sync time cannot regress earlier sync time")
	ErrZeroSyncTime                  = errors.New("sync timestamp must not be zero")
	ErrConflictingSyncStatus         = errors.New("conflicting platform status received with identical sync timestamp")
)

// PlatformAccount is an independent aggregate representing FlowCard's connected account on a platform.
// NOTE: It is linked to Company by companyID, survives onboarding applications, and has NO application ID ownership.
type PlatformAccount struct {
	id                string
	companyID         string
	platformName      string
	environment       string
	externalAccountID string
	status            PlatformAccountStatus
	lastSyncTime      *time.Time
	createdAt         time.Time
	updatedAt         time.Time
	revision          int
}

// NewPlatformAccount creates a new PlatformAccount aggregate with initial UNOBSERVED status.
func NewPlatformAccount(id, companyID, platformName, environment string) (*PlatformAccount, error) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return nil, ErrEmptyPlatformAccountID
	}
	cleanCompanyID := strings.TrimSpace(companyID)
	if cleanCompanyID == "" {
		return nil, ErrEmptyCompanyID
	}
	cleanPlatform := strings.TrimSpace(platformName)
	if cleanPlatform == "" {
		return nil, ErrEmptyPlatformName
	}
	cleanEnv := strings.TrimSpace(environment)
	if cleanEnv == "" {
		return nil, ErrEmptyEnvironment
	}

	now := time.Now().UTC()
	return &PlatformAccount{
		id:           cleanID,
		companyID:    cleanCompanyID,
		platformName: cleanPlatform,
		environment:  cleanEnv,
		status:       PlatformAccountStatusUnobserved,
		createdAt:    now,
		updatedAt:    now,
		revision:     1,
	}, nil
}

// ReconstitutePlatformAccountParams carries state to reconstruct a PlatformAccount from storage.
type ReconstitutePlatformAccountParams struct {
	ID                string
	CompanyID         string
	PlatformName      string
	Environment       string
	ExternalAccountID string
	Status            PlatformAccountStatus
	LastSyncTime      *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Revision          int
}

// ReconstitutePlatformAccount recreates a PlatformAccount aggregate from persistence.
func ReconstitutePlatformAccount(params ReconstitutePlatformAccountParams) (*PlatformAccount, error) {
	cleanID := strings.TrimSpace(params.ID)
	if cleanID == "" {
		return nil, ErrEmptyPlatformAccountID
	}
	cleanCompanyID := strings.TrimSpace(params.CompanyID)
	if cleanCompanyID == "" {
		return nil, ErrEmptyCompanyID
	}
	cleanPlatform := strings.TrimSpace(params.PlatformName)
	if cleanPlatform == "" {
		return nil, ErrEmptyPlatformName
	}
	cleanEnv := strings.TrimSpace(params.Environment)
	if cleanEnv == "" {
		return nil, ErrEmptyEnvironment
	}
	if params.Revision <= 0 {
		return nil, ErrInvalidRevision
	}
	cleanStatus := PlatformAccountStatus(strings.TrimSpace(string(params.Status)))
	if cleanStatus == "" {
		return nil, fmt.Errorf("%w: status must not be empty", ErrInvalidPlatformAccountStatus)
	}
	if params.LastSyncTime != nil && params.LastSyncTime.IsZero() {
		return nil, ErrZeroSyncTime
	}

	pa := &PlatformAccount{
		id:                cleanID,
		companyID:         cleanCompanyID,
		platformName:      cleanPlatform,
		environment:       cleanEnv,
		externalAccountID: strings.TrimSpace(params.ExternalAccountID),
		status:            cleanStatus,
		createdAt:         params.CreatedAt,
		updatedAt:         params.UpdatedAt,
		revision:          params.Revision,
	}

	if params.LastSyncTime != nil {
		t := *params.LastSyncTime
		pa.lastSyncTime = &t
	}

	return pa, nil
}

// ID returns the internal platform account ID.
func (p *PlatformAccount) ID() string {
	return p.id
}

// CompanyID returns the associated Company ID.
func (p *PlatformAccount) CompanyID() string {
	return p.companyID
}

// PlatformName returns the platform name (e.g. "AIRWALLEX").
func (p *PlatformAccount) PlatformName() string {
	return p.platformName
}

// Environment returns the operating environment ("sandbox" or "production").
func (p *PlatformAccount) Environment() string {
	return p.environment
}

// ExternalAccountID returns the provider's external account ID.
func (p *PlatformAccount) ExternalAccountID() string {
	return p.externalAccountID
}

// Status returns current platform status.
func (p *PlatformAccount) Status() PlatformAccountStatus {
	return p.status
}

// LastSyncTime returns the timestamp of the last successful platform status sync.
func (p *PlatformAccount) LastSyncTime() *time.Time {
	if p.lastSyncTime == nil {
		return nil
	}
	t := *p.lastSyncTime
	return &t
}

// CreatedAt returns creation timestamp.
func (p *PlatformAccount) CreatedAt() time.Time {
	return p.createdAt
}

// UpdatedAt returns modification timestamp.
func (p *PlatformAccount) UpdatedAt() time.Time {
	return p.updatedAt
}

// Revision returns current revision number.
func (p *PlatformAccount) Revision() int {
	return p.revision
}

// SetExternalAccountID assigns the provider external account ID.
// Rejects empty ID and prevents reassignment to a different external account ID.
// Idempotent if re-set to the same ID.
func (p *PlatformAccount) SetExternalAccountID(extID string) error {
	cleanID := strings.TrimSpace(extID)
	if cleanID == "" {
		return ErrEmptyExternalAccountID
	}
	if p.externalAccountID != "" && p.externalAccountID != cleanID {
		return ErrExternalAccountIDReassignment
	}
	if p.externalAccountID == cleanID {
		return nil // idempotent duplicate, zero touch
	}
	p.externalAccountID = cleanID
	p.touch()
	return nil
}

// RecordObservedStatus records an observed status from the platform.
// Rejects empty or UNOBSERVED status (which is reserved for local initial state).
// Rejects zero or regressing timestamps. Rejects equal-time contradictions.
func (p *PlatformAccount) RecordObservedStatus(status PlatformAccountStatus, syncTime time.Time) error {
	cleanStatus := PlatformAccountStatus(strings.TrimSpace(string(status)))
	if cleanStatus == "" || cleanStatus == PlatformAccountStatusUnobserved {
		return fmt.Errorf("%w: %q", ErrInvalidPlatformAccountStatus, status)
	}
	if syncTime.IsZero() {
		return ErrZeroSyncTime
	}
	if p.lastSyncTime != nil {
		if syncTime.Before(*p.lastSyncTime) {
			return ErrStaleSyncTime
		}
		if syncTime.Equal(*p.lastSyncTime) {
			if p.status == cleanStatus {
				return nil // idempotent duplicate, zero touch
			}
			return ErrConflictingSyncStatus
		}
	}
	if p.status == cleanStatus && p.lastSyncTime != nil && p.lastSyncTime.Equal(syncTime) {
		return nil // idempotent duplicate, zero touch
	}
	p.status = cleanStatus
	t := syncTime.UTC()
	p.lastSyncTime = &t
	p.touch()
	return nil
}

// MarkActive marks the connected account ACTIVE following platform confirmation.
func (p *PlatformAccount) MarkActive(syncTime time.Time) error {
	return p.RecordObservedStatus(PlatformAccountStatusActive, syncTime)
}

// MarkSuspended records platform suspension.
func (p *PlatformAccount) MarkSuspended(syncTime time.Time) error {
	return p.RecordObservedStatus(PlatformAccountStatusSuspended, syncTime)
}

// MarkClosed records platform closure.
func (p *PlatformAccount) MarkClosed(syncTime time.Time) error {
	return p.RecordObservedStatus(PlatformAccountStatusClosed, syncTime)
}

// RecordSync updates the last successful synchronization time without altering status.
func (p *PlatformAccount) RecordSync(syncTime time.Time) error {
	if syncTime.IsZero() {
		return ErrZeroSyncTime
	}
	if p.lastSyncTime != nil && syncTime.Before(*p.lastSyncTime) {
		return ErrStaleSyncTime
	}
	if p.lastSyncTime != nil && syncTime.Equal(*p.lastSyncTime) {
		return nil // idempotent duplicate, zero touch
	}
	t := syncTime.UTC()
	p.lastSyncTime = &t
	p.touch()
	return nil
}

// DeepCopy produces an isolated deep copy.
func (p *PlatformAccount) DeepCopy() *PlatformAccount {
	cp := *p
	if p.lastSyncTime != nil {
		t := *p.lastSyncTime
		cp.lastSyncTime = &t
	}
	return &cp
}

func (p *PlatformAccount) touch() {
	p.revision++
	p.updatedAt = time.Now().UTC()
}

type platformAccountJSON struct {
	ID                string                `json:"platform_account_id"`
	CompanyID         string                `json:"company_id"`
	PlatformName      string                `json:"platform_name"`
	Environment       string                `json:"environment"`
	ExternalAccountID string                `json:"external_account_id,omitempty"`
	Status            PlatformAccountStatus `json:"status"`
	LastSyncTime      *time.Time            `json:"last_sync_time,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	Revision          int                   `json:"revision"`
}

// MarshalJSON provides standardized serialization.
func (p *PlatformAccount) MarshalJSON() ([]byte, error) {
	return json.Marshal(platformAccountJSON{
		ID:                p.id,
		CompanyID:         p.companyID,
		PlatformName:      p.platformName,
		Environment:       p.environment,
		ExternalAccountID: p.externalAccountID,
		Status:            p.status,
		LastSyncTime:      p.lastSyncTime,
		CreatedAt:         p.createdAt,
		UpdatedAt:         p.updatedAt,
		Revision:          p.revision,
	})
}

// UnmarshalJSON reconstitutes a PlatformAccount from JSON.
// Failure does not mutate the receiver.
func (p *PlatformAccount) UnmarshalJSON(data []byte) error {
	var j platformAccountJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	recon, err := ReconstitutePlatformAccount(ReconstitutePlatformAccountParams{
		ID:                j.ID,
		CompanyID:         j.CompanyID,
		PlatformName:      j.PlatformName,
		Environment:       j.Environment,
		ExternalAccountID: j.ExternalAccountID,
		Status:            j.Status,
		LastSyncTime:      j.LastSyncTime,
		CreatedAt:         j.CreatedAt,
		UpdatedAt:         j.UpdatedAt,
		Revision:          j.Revision,
	})
	if err != nil {
		return err
	}
	*p = *recon
	return nil
}
