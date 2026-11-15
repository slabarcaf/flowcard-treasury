// Package domain defines coordinator-owned durable workflow values.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

type CaseKind string

const (
	CaseShortfall  CaseKind = "SHORTFALL"
	CaseCollection CaseKind = "COLLECTION"
)

type CaseStatus string

const (
	CaseNew            CaseStatus = "NEW"
	CaseInvestigating  CaseStatus = "INVESTIGATING"
	CaseEvaluating     CaseStatus = "EVALUATING"
	CaseMonitoring     CaseStatus = "MONITORING"
	CaseActionRequired CaseStatus = "ACTION_REQUIRED"
	CaseCompleted      CaseStatus = "COMPLETED"
	CaseClosed         CaseStatus = "CLOSED"
	CaseRetryWait      CaseStatus = "RETRY_WAIT"
	CaseHeld           CaseStatus = "HELD"
)

type FailureCode string

const (
	FailureInvalidInput      FailureCode = "INVALID_INPUT"
	FailureSourceUnavailable FailureCode = "SOURCE_UNAVAILABLE"
	FailureAuthentication    FailureCode = "AUTHENTICATION_FAILED"
	FailureBudgetExhausted   FailureCode = "BUDGET_EXHAUSTED"
	FailureLeaseLost         FailureCode = "LEASE_LOST"
)

type SourceBinding struct {
	ID          string
	Mode        string
	Fingerprint string
	EndpointID  string
}

type Case struct {
	ID                    string
	BindingID             string
	Kind                  CaseKind
	DetectionID           *string
	BridgeID              *string
	CustomerID            *string
	AccountRef            *string
	RawAccountID          *string
	Status                CaseStatus
	NextStage             string
	RetryAt               *time.Time
	HeldReason            *string
	LeaseToken            *string
	LeaseExpiresAt        *time.Time
	LeaseStage            *string
	TransientFailures     int
	BridgeVersion         *int64
	RefreshNeeded         bool
	TargetVersion         *int64
	PendingTrigger        *string
	PendingTriggerVersion *int64
}

// InternalAccountRef returns the funding-owned alias for a trusted customer ID.
func InternalAccountRef(customerID string) string {
	return "customer:" + customerID
}

// StableCaseID returns a deterministic UUID-shaped identity for one source case.
func StableCaseID(bindingID string, kind CaseKind, sourceID string) (string, error) {
	if bindingID == "" || sourceID == "" || (kind != CaseShortfall && kind != CaseCollection) {
		return "", errors.New("case_identity_input_invalid")
	}
	sum := sha256.Sum256([]byte("flowcard-go-coordinator-case\x00" + bindingID + "\x00" + string(kind) + "\x00" + sourceID))
	value := sum[:16]
	value[6] = (value[6] & 0x0f) | 0x80
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

type Run struct {
	ID             string
	CaseID         string
	Revision       int
	Stage          string
	PromptVersion  string
	ModelVersion   string
	ContextRefs    []byte
	Disposition    *string
	ModelCallCount int
	ToolCallCount  int
}

type Step struct {
	RunID       string
	Sequence    int
	Kind        string
	CallID      *string
	RequestHash []byte
	Result      []byte
	CreatedAt   time.Time
}

type Evidence struct {
	ID            string
	CaseID        string
	RunID         string
	SourceHandle  string
	SourceVersion string
	SourceHash    []byte
	Locator       string
	Excerpt       string
	ClaimField    string
	ClaimValue    string
	ClaimUnit     *string
	ClaimDate     *time.Time
	Uncertainty   *string
	State         string
	CanonicalHash []byte
	ToolCallID    string
}

type AssessmentRequest struct {
	ID                  string
	CaseID              string
	Revision            int
	IdempotencyKey      string
	Body                []byte
	BodyHash            []byte
	SourceTransactionID *string
	SourceCardID        *string
	SavedAssessmentID   *string
	Response            []byte
}

// StableRunID returns a deterministic UUID-shaped identity for one case revision.
func StableRunID(caseID string, revision int) (string, error) {
	if caseID == "" || revision <= 0 {
		return "", errors.New("run_identity_input_invalid")
	}
	sum := sha256.Sum256([]byte("flowcard-go-coordinator-run\x00" + caseID + "\x00" + strconv.Itoa(revision)))
	value := sum[:16]
	value[6] = (value[6] & 0x0f) | 0x80
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(value)
	return hexValue[:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:], nil
}

type StepRecord struct {
	RunID       string
	Sequence    int
	Kind        string
	CallID      *string
	RequestHash []byte
	RequestBody []byte
	ResultBody  []byte
	Outcome     string
}

var ErrConflict = errors.New("coordinator_conflict")
var ErrNotFound = errors.New("coordinator_not_found")
var ErrLeaseLost = errors.New("coordinator_lease_lost")
var ErrBudgetExhausted = errors.New("agent_run_budget_exhausted")

// ErrCollectionRefreshPending means a newer authoritative bridge read is required.
var ErrCollectionRefreshPending = errors.New("collection_refresh_pending")
