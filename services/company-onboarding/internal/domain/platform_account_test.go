package domain_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
)

func TestPlatformAccountLifecycle(t *testing.T) {
	t.Parallel()

	pa, err := domain.NewPlatformAccount("pa_test_1", "comp_123", "AIRWALLEX", "sandbox")
	if err != nil {
		t.Fatalf("NewPlatformAccount() failed: %v", err)
	}

	if pa.ID() != "pa_test_1" {
		t.Errorf("ID() = %q, want pa_test_1", pa.ID())
	}
	if pa.CompanyID() != "comp_123" {
		t.Errorf("CompanyID() = %q, want comp_123", pa.CompanyID())
	}
	// Invariant: Initial local state is UNOBSERVED
	if pa.Status() != domain.PlatformAccountStatusUnobserved {
		t.Errorf("Status() = %q, want UNOBSERVED", pa.Status())
	}
	if pa.Revision() != 1 {
		t.Errorf("Revision() = %d, want 1", pa.Revision())
	}

	// 1. Set external account ID
	if err := pa.SetExternalAccountID("act_airwallex_999"); err != nil {
		t.Fatalf("SetExternalAccountID() failed: %v", err)
	}
	if pa.ExternalAccountID() != "act_airwallex_999" {
		t.Errorf("ExternalAccountID() = %q, want act_airwallex_999", pa.ExternalAccountID())
	}
	if pa.Revision() != 2 {
		t.Errorf("Revision() = %d, want 2", pa.Revision())
	}

	// Idempotent re-set to identical ID does not increment revision (zero touch)
	if err := pa.SetExternalAccountID("act_airwallex_999"); err != nil {
		t.Fatalf("SetExternalAccountID() idempotent re-set failed: %v", err)
	}
	if pa.Revision() != 2 {
		t.Errorf("Revision() = %d, want 2 on idempotent ID re-set", pa.Revision())
	}

	// Reassignment to different external ID must be strictly rejected
	err = pa.SetExternalAccountID("act_different_id")
	if err == nil || !errors.Is(err, domain.ErrExternalAccountIDReassignment) {
		t.Errorf("SetExternalAccountID() reassignment error = %v, want %v", err, domain.ErrExternalAccountIDReassignment)
	}

	// 2. Record raw platform statuses (including unknown-safe representations)
	sync1 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	if err := pa.RecordObservedStatus(domain.PlatformAccountStatusCreated, sync1); err != nil {
		t.Fatalf("RecordObservedStatus(CREATED) failed: %v", err)
	}
	if pa.Status() != domain.PlatformAccountStatusCreated {
		t.Errorf("Status() = %q, want CREATED", pa.Status())
	}

	// Raw unknown platform status string is accepted directly without mapping or whitelist rejection
	sync2 := time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC)
	rawStatus := domain.PlatformAccountStatus("REQUIRES_IDENTITY_DOCUMENTS")
	if err := pa.RecordObservedStatus(rawStatus, sync2); err != nil {
		t.Fatalf("RecordObservedStatus(rawStatus) failed: %v", err)
	}
	if pa.Status() != rawStatus {
		t.Errorf("Status() = %q, want %q", pa.Status(), rawStatus)
	}

	// 3. Mark active
	sync3 := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	if err := pa.MarkActive(sync3); err != nil {
		t.Fatalf("MarkActive() failed: %v", err)
	}
	if pa.Status() != domain.PlatformAccountStatusActive {
		t.Errorf("Status() = %q, want ACTIVE", pa.Status())
	}
	if pa.LastSyncTime() == nil || !pa.LastSyncTime().Equal(sync3) {
		t.Errorf("LastSyncTime = %v, want %v", pa.LastSyncTime(), sync3)
	}

	// Invariant: Zero sync timestamp is invalid
	err = pa.RecordObservedStatus(domain.PlatformAccountStatusActive, time.Time{})
	if err == nil || !errors.Is(err, domain.ErrZeroSyncTime) {
		t.Errorf("RecordObservedStatus with zero timestamp error = %v, want %v", err, domain.ErrZeroSyncTime)
	}

	// Invariant: Stale sync timestamp cannot regress known observed state
	staleSync := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	err = pa.RecordObservedStatus(domain.PlatformAccountStatusSubmitted, staleSync)
	if err == nil || !errors.Is(err, domain.ErrStaleSyncTime) {
		t.Errorf("RecordObservedStatus() with stale sync error = %v, want %v", err, domain.ErrStaleSyncTime)
	}
	// Status must remain ACTIVE
	if pa.Status() != domain.PlatformAccountStatusActive {
		t.Errorf("Status() regressed on stale sync: %q", pa.Status())
	}

	// Invariant: Equal-timestamp conflicting status is strictly rejected
	err = pa.RecordObservedStatus(domain.PlatformAccountStatusSuspended, sync3)
	if err == nil || !errors.Is(err, domain.ErrConflictingSyncStatus) {
		t.Errorf("RecordObservedStatus() with equal-timestamp contradiction error = %v, want %v", err, domain.ErrConflictingSyncStatus)
	}
	// Status must remain ACTIVE
	if pa.Status() != domain.PlatformAccountStatusActive {
		t.Errorf("Status() changed on equal-timestamp contradiction: %q", pa.Status())
	}

	// Idempotent duplicate observation at exact same timestamp succeeds with zero touch
	revBefore := pa.Revision()
	if err := pa.RecordObservedStatus(domain.PlatformAccountStatusActive, sync3); err != nil {
		t.Fatalf("RecordObservedStatus() duplicate failed: %v", err)
	}
	if pa.Revision() != revBefore {
		t.Errorf("Revision bumped on idempotent duplicate: %d -> %d", revBefore, pa.Revision())
	}

	// 4. Mark suspended
	sync4 := time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)
	if err := pa.MarkSuspended(sync4); err != nil {
		t.Fatalf("MarkSuspended() failed: %v", err)
	}
	if pa.Status() != domain.PlatformAccountStatusSuspended {
		t.Errorf("Status() = %q, want SUSPENDED", pa.Status())
	}

	// 5. Mark closed
	sync5 := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)
	if err := pa.MarkClosed(sync5); err != nil {
		t.Fatalf("MarkClosed() failed: %v", err)
	}
	if pa.Status() != domain.PlatformAccountStatusClosed {
		t.Errorf("Status() = %q, want CLOSED", pa.Status())
	}

	// 6. DeepCopy produces an isolated deep copy
	clone := pa.DeepCopy()
	if clone.ID() != pa.ID() {
		t.Errorf("clone.ID() = %q, want %q", clone.ID(), pa.ID())
	}
	if err := clone.RecordSync(time.Date(2026, 11, 7, 15, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("clone.RecordSync() failed: %v", err)
	}
	if pa.LastSyncTime().Equal(*clone.LastSyncTime()) {
		t.Errorf("mutating clone LastSyncTime mutated original platform account")
	}
}

func TestPlatformAccountValidationAndReconstitution(t *testing.T) {
	t.Parallel()

	// Empty ID rejected
	_, err := domain.NewPlatformAccount("", "comp_1", "AIRWALLEX", "sandbox")
	if err == nil || !errors.Is(err, domain.ErrEmptyPlatformAccountID) {
		t.Errorf("NewPlatformAccount() with empty ID error = %v, want %v", err, domain.ErrEmptyPlatformAccountID)
	}

	// Empty Company ID rejected
	_, err = domain.NewPlatformAccount("pa_1", "", "AIRWALLEX", "sandbox")
	if err == nil || !errors.Is(err, domain.ErrEmptyCompanyID) {
		t.Errorf("NewPlatformAccount() with empty company ID error = %v, want %v", err, domain.ErrEmptyCompanyID)
	}

	// Empty platform name rejected
	_, err = domain.NewPlatformAccount("pa_1", "comp_1", "", "sandbox")
	if err == nil || !errors.Is(err, domain.ErrEmptyPlatformName) {
		t.Errorf("NewPlatformAccount() with empty platform name error = %v, want %v", err, domain.ErrEmptyPlatformName)
	}

	// Empty environment rejected
	_, err = domain.NewPlatformAccount("pa_1", "comp_1", "AIRWALLEX", "")
	if err == nil || !errors.Is(err, domain.ErrEmptyEnvironment) {
		t.Errorf("NewPlatformAccount() with empty env error = %v, want %v", err, domain.ErrEmptyEnvironment)
	}

	// Empty external ID rejected on SetExternalAccountID
	pa, _ := domain.NewPlatformAccount("pa_val", "comp_val", "AIRWALLEX", "sandbox")
	if err := pa.SetExternalAccountID("  "); err == nil || !errors.Is(err, domain.ErrEmptyExternalAccountID) {
		t.Errorf("SetExternalAccountID(empty) error = %v, want %v", err, domain.ErrEmptyExternalAccountID)
	}

	// RecordObservedStatus rejects UNOBSERVED
	if err := pa.RecordObservedStatus(domain.PlatformAccountStatusUnobserved, time.Now().UTC()); err == nil || !errors.Is(err, domain.ErrInvalidPlatformAccountStatus) {
		t.Errorf("RecordObservedStatus(UNOBSERVED) error = %v, want %v", err, domain.ErrInvalidPlatformAccountStatus)
	}

	// Empty status during reconstitution rejected
	now := time.Now().UTC()
	_, err = domain.ReconstitutePlatformAccount(domain.ReconstitutePlatformAccountParams{
		ID:           "pa_rec_1",
		CompanyID:    "comp_rec_1",
		PlatformName: "AIRWALLEX",
		Environment:  "sandbox",
		Status:       "   ",
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidPlatformAccountStatus) {
		t.Errorf("ReconstitutePlatformAccount() with empty status error = %v, want %v", err, domain.ErrInvalidPlatformAccountStatus)
	}

	// Raw unknown platform status during reconstitution accepted
	recRaw, err := domain.ReconstitutePlatformAccount(domain.ReconstitutePlatformAccountParams{
		ID:           "pa_rec_raw",
		CompanyID:    "comp_rec_raw",
		PlatformName: "AIRWALLEX",
		Environment:  "sandbox",
		Status:       "RAW_PLATFORM_SPECIFIC_STATUS",
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("ReconstitutePlatformAccount with raw status failed: %v", err)
	}
	if recRaw.Status() != "RAW_PLATFORM_SPECIFIC_STATUS" {
		t.Errorf("Status() = %q, want RAW_PLATFORM_SPECIFIC_STATUS", recRaw.Status())
	}

	// Non-positive revision rejected
	_, err = domain.ReconstitutePlatformAccount(domain.ReconstitutePlatformAccountParams{
		ID:           "pa_rec_2",
		CompanyID:    "comp_rec_2",
		PlatformName: "AIRWALLEX",
		Environment:  "sandbox",
		Status:       domain.PlatformAccountStatusActive,
		Revision:     0,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err == nil || !errors.Is(err, domain.ErrInvalidRevision) {
		t.Errorf("ReconstitutePlatformAccount() with revision 0 error = %v, want %v", err, domain.ErrInvalidRevision)
	}
}

func TestPlatformAccountJSONRoundTrip(t *testing.T) {
	t.Parallel()

	pa, err := domain.NewPlatformAccount("pa_json_1", "comp_json_1", "AIRWALLEX", "production")
	if err != nil {
		t.Fatalf("NewPlatformAccount() failed: %v", err)
	}
	if err := pa.SetExternalAccountID("act_ext_12345"); err != nil {
		t.Fatalf("SetExternalAccountID() failed: %v", err)
	}
	syncTime := time.Date(2026, 11, 7, 8, 30, 0, 0, time.UTC)
	if err := pa.MarkActive(syncTime); err != nil {
		t.Fatalf("MarkActive() failed: %v", err)
	}

	data, err := json.Marshal(pa)
	if err != nil {
		t.Fatalf("json.Marshal() failed: %v", err)
	}

	var restored domain.PlatformAccount
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() failed: %v", err)
	}

	if restored.ID() != pa.ID() {
		t.Errorf("restored.ID() = %q, want %q", restored.ID(), pa.ID())
	}
	if restored.CompanyID() != pa.CompanyID() {
		t.Errorf("restored.CompanyID() = %q, want %q", restored.CompanyID(), pa.CompanyID())
	}
	if restored.Status() != pa.Status() {
		t.Errorf("restored.Status() = %q, want %q", restored.Status(), pa.Status())
	}
	if restored.ExternalAccountID() != pa.ExternalAccountID() {
		t.Errorf("restored.ExternalAccountID() = %q, want %q", restored.ExternalAccountID(), pa.ExternalAccountID())
	}
	if restored.LastSyncTime() == nil || !restored.LastSyncTime().Equal(syncTime) {
		t.Errorf("restored.LastSyncTime() = %v, want %v", restored.LastSyncTime(), syncTime)
	}

	// Corrupted JSON does not modify receiver
	corrupted := []byte(`{"platform_account_id": "", "status": ""}`)
	err = restored.UnmarshalJSON(corrupted)
	if err == nil {
		t.Fatalf("UnmarshalJSON with corrupted JSON succeeded, want error")
	}
	if restored.ID() != "pa_json_1" {
		t.Errorf("restored.ID() was mutated by failed UnmarshalJSON: %q", restored.ID())
	}
}
