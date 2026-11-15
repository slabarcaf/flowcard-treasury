// Package evaluation coordinates shortfall reads, snapshot selection and pure policy assessment.
package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/shortfall"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

// Failure is a controlled API failure with no provider or database details.
type Failure struct {
	Status int
	Code   string
}

func (f *Failure) Error() string { return f.Code }

// ShortfallReader is the authenticated read boundary to the shortfall service.
type ShortfallReader interface {
	Get(context.Context, string, time.Time) (domain.ShortfallRecord, error)
}

// Store contains only the persistence operations needed for synchronous evaluation.
type Store interface {
	FindEvaluation(context.Context, string, string, string) (postgres.ExistingEvaluation, bool, error)
	CurrentSnapshots(context.Context, string, string, string) (*domain.CustomerSnapshotRecord, *domain.FacilitySnapshotRecord, error)
	CurrentCustomerSnapshot(context.Context, string, string) (*domain.CustomerSnapshotRecord, error)
	SaveEvaluation(context.Context, postgres.EvaluationInsert) (json.RawMessage, bool, error)
	GetEvaluation(context.Context, string, string) (json.RawMessage, bool, error)
	ListEvaluations(context.Context, string, string, *domain.EvaluationCursor) (domain.EvaluationPage, error)
}

// Service evaluates requests with a server-owned mode, facility, policy and clock.
type Service struct {
	Mode       string
	FacilityID string
	Policy     policy.Config
	Store      Store
	Shortfalls ShortfallReader
	Now        func() time.Time
}

// Outcome contains the saved response body and the first-write or replay status.
type Outcome struct {
	Body       json.RawMessage
	HTTPStatus int
}

// Evaluate replays a completed key before reading dependencies and persists one result.
func (s *Service) Evaluate(ctx context.Context, caller, idempotencyKey string, request domain.EvaluationRequest) (Outcome, error) {
	if !domain.ValidMode(s.Mode) || s.FacilityID == "" || s.Store == nil || s.Shortfalls == nil || s.Now == nil {
		return Outcome{}, failure(503, "STORAGE_UNAVAILABLE")
	}
	if caller != "underwriting-evaluator" || !validIdempotencyKey(idempotencyKey) ||
		!domain.ValidID(request.ShortfallID) || request.TermDays < 7 || request.TermDays > 90 {
		return Outcome{}, failure(400, "INVALID_REQUEST")
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return Outcome{}, failure(400, "INVALID_REQUEST")
	}
	requestHash := sha256.Sum256(requestJSON)

	lookupCtx, cancelLookup := context.WithTimeout(ctx, 5*time.Second)
	existing, found, err := s.Store.FindEvaluation(lookupCtx, s.Mode, caller, idempotencyKey)
	cancelLookup()
	if err != nil {
		return Outcome{}, failure(503, "STORAGE_UNAVAILABLE")
	}
	if found {
		if existing.RequestHash != requestHash {
			return Outcome{}, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return Outcome{Body: existing.Body, HTTPStatus: 200}, nil
	}

	shortfallReadAt := s.Now().UTC()
	shortfallRecord, err := s.Shortfalls.Get(ctx, request.ShortfallID, shortfallReadAt)
	if err != nil {
		return Outcome{}, mapShortfallFailure(err)
	}
	if err := validateShortfall(request.ShortfallID, shortfallRecord); err != nil {
		return Outcome{}, err
	}

	snapshotCtx, cancelSnapshots := context.WithTimeout(ctx, 5*time.Second)
	customerRecord, facilityRecord, err := s.Store.CurrentSnapshots(
		snapshotCtx, s.Mode, *shortfallRecord.CustomerID, s.FacilityID)
	cancelSnapshots()
	if err != nil {
		return Outcome{}, failure(503, "STORAGE_UNAVAILABLE")
	}
	if customerRecord != nil && (customerRecord.Snapshot.CustomerID != *shortfallRecord.CustomerID ||
		customerRecord.Snapshot.AccountID != shortfallRecord.AccountID ||
		customerRecord.Snapshot.CardID != *shortfallRecord.CardID ||
		customerRecord.Snapshot.SourceMode != s.Mode) {
		return Outcome{}, failure(422, "SHORTFALL_LINKAGE_INVALID")
	}
	if facilityRecord != nil && facilityRecord.Snapshot.SourceMode != s.Mode {
		return Outcome{}, failure(422, "SHORTFALL_LINKAGE_INVALID")
	}

	evaluatedAt := s.Now().UTC()
	input := policy.Input{
		EvaluatedAt: evaluatedAt,
		Mode:        s.Mode,
		FacilityID:  s.FacilityID,
		TermDays:    request.TermDays,
		Shortfall:   policyShortfall(shortfallRecord),
		Customer:    policyCustomer(customerRecord, s.Mode),
		Facility:    policyFacility(facilityRecord, s.Mode, s.FacilityID),
	}
	decision := policy.Evaluate(input, s.Policy)
	response := responseFromDecision(decision, request, shortfallRecord, s.Mode,
		customerRecord, facilityRecord)
	inputFacts, err := json.Marshal(input)
	if err != nil {
		return Outcome{}, failure(503, "STORAGE_UNAVAILABLE")
	}

	insert := postgres.EvaluationInsert{
		Mode: s.Mode, Caller: caller, CustomerID: *shortfallRecord.CustomerID,
		ShortfallID: request.ShortfallID, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, Request: request, Shortfall: shortfallRecord,
		CustomerSnapshot: customerRecord, FacilitySnapshot: facilityRecord,
		InputFacts: inputFacts, EvaluatedAt: evaluatedAt,
		PolicyID: decision.PolicyID, PolicyHash: decision.PolicyHash, Response: response,
	}
	saveCtx, cancelSave := context.WithTimeout(ctx, 5*time.Second)
	body, inserted, err := s.Store.SaveEvaluation(saveCtx, insert)
	cancelSave()
	if errors.Is(err, postgres.ErrIdempotencyConflict) {
		return Outcome{}, failure(409, "IDEMPOTENCY_CONFLICT")
	}
	if err != nil {
		return Outcome{}, failure(503, "STORAGE_UNAVAILABLE")
	}
	if inserted {
		return Outcome{Body: body, HTTPStatus: 201}, nil
	}
	return Outcome{Body: body, HTTPStatus: 200}, nil
}

// Get returns one saved body without rerunning rules or dependencies.
func (s *Service) Get(ctx context.Context, id string) (json.RawMessage, bool, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, found, err := s.Store.GetEvaluation(lookupCtx, s.Mode, id)
	if err != nil {
		return nil, false, failure(503, "STORAGE_UNAVAILABLE")
	}
	return body, found, nil
}

// List returns one customer-scoped page of immutable saved assessments.
func (s *Service) List(ctx context.Context, customerID string, cursor *domain.EvaluationCursor) (domain.EvaluationPage, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	page, err := s.Store.ListEvaluations(lookupCtx, s.Mode, customerID, cursor)
	if err != nil {
		return domain.EvaluationPage{}, failure(503, "STORAGE_UNAVAILABLE")
	}
	return page, nil
}

// Context returns bounded normalized current inputs without publishing or reevaluating.
func (s *Service) Context(ctx context.Context, customerID string) (domain.CustomerContextResponse, bool, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	record, err := s.Store.CurrentCustomerSnapshot(lookupCtx, s.Mode, customerID)
	if err != nil {
		return domain.CustomerContextResponse{}, false, failure(503, "STORAGE_UNAVAILABLE")
	}
	if record == nil {
		return domain.CustomerContextResponse{}, false, nil
	}
	now := s.Now().UTC()
	age := now.Sub(record.Snapshot.CapturedAt)
	fresh := !record.Snapshot.CapturedAt.IsZero() && age <= 24*time.Hour &&
		age >= -5*time.Second && record.Snapshot.SourceMode == s.Mode
	snapshot := record.Snapshot
	return domain.CustomerContextResponse{
		SchemaVersion: domain.SchemaVersion, SourceMode: snapshot.SourceMode,
		CustomerID: snapshot.CustomerID, SnapshotID: record.ID, SnapshotVersion: record.Version,
		CapturedAt: snapshot.CapturedAt.UTC(), PolicyID: s.Policy.PolicyID(),
		PolicyHash: s.Policy.PolicyHash(), Coverage: snapshot.Coverage, Fresh: fresh,
		Data: domain.CustomerContextData{
			AccountID: snapshot.AccountID, CardID: snapshot.CardID, Standing: snapshot.Standing,
			HistoryWindow: snapshot.HistoryWindow, BalanceHistory: snapshot.BalanceHistory,
			InflowHistory: snapshot.InflowHistory, Bridges: snapshot.Bridges,
			Reservations: snapshot.Reservations, Receivables: snapshot.Receivables,
			Classifications: snapshot.Classifications,
		}, SourceRefs: snapshot.SourceRefs,
	}, true, nil
}

func mapShortfallFailure(err error) error {
	switch {
	case errors.Is(err, shortfall.ErrNotFound):
		return failure(404, "SHORTFALL_NOT_FOUND")
	case errors.Is(err, shortfall.ErrResponseInvalid):
		return failure(503, "SHORTFALL_RESPONSE_INVALID")
	default:
		return failure(503, "SHORTFALL_UNAVAILABLE")
	}
}

func validateShortfall(requestID string, record domain.ShortfallRecord) error {
	if record.ID != requestID || !validIdentifier(record.AccountID) {
		return failure(422, "SHORTFALL_LINKAGE_INVALID")
	}
	if record.Outcome != "SHORTFALL_DETECTED" {
		return failure(422, "SHORTFALL_NOT_ELIGIBLE")
	}
	if record.CustomerID == nil || record.CardID == nil ||
		!validIdentifier(*record.CustomerID) || !validIdentifier(*record.CardID) {
		return failure(422, "SHORTFALL_LINKAGE_INVALID")
	}
	if record.Currency == nil || *record.Currency != "USD" ||
		record.AttemptedAmountCents == nil || record.AvailableBalanceCents == nil ||
		record.ShortfallCents == nil || record.BalanceRequestedAt == nil ||
		record.BalanceObservedAt == nil || record.BalanceRequestedAt.IsZero() ||
		record.BalanceObservedAt.IsZero() || record.BalanceObservedAt.Before(*record.BalanceRequestedAt) ||
		!record.RequiresRecheck || record.AmountSemantics != "current_need_at_balance_observation" {
		return failure(503, "SHORTFALL_RESPONSE_INVALID")
	}
	if *record.AttemptedAmountCents <= 0 || *record.AvailableBalanceCents < 0 || *record.ShortfallCents <= 0 {
		return failure(422, "SHORTFALL_NOT_ELIGIBLE")
	}
	expected := *record.AttemptedAmountCents - *record.AvailableBalanceCents
	if expected < 0 {
		expected = 0
	}
	if expected != *record.ShortfallCents {
		return failure(422, "SHORTFALL_NOT_ELIGIBLE")
	}
	if record.TransactionID == nil || !validIdentifier(*record.TransactionID) ||
		!validIdentifier(record.CanonicalEventID) {
		return failure(422, "SHORTFALL_LINKAGE_INVALID")
	}
	return nil
}

func policyShortfall(record domain.ShortfallRecord) policy.Shortfall {
	return policy.Shortfall{
		ID: record.ID, CustomerID: *record.CustomerID, AccountID: record.AccountID,
		CardID: *record.CardID, Outcome: record.Outcome, Currency: *record.Currency,
		AttemptedAmountCents:  *record.AttemptedAmountCents,
		AvailableBalanceCents: *record.AvailableBalanceCents,
		ShortfallCents:        *record.ShortfallCents, CanonicalEventID: record.CanonicalEventID,
		TransactionID: *record.TransactionID, BalanceObservedAt: record.BalanceObservedAt.UTC(),
	}
}

func policyCustomer(record *domain.CustomerSnapshotRecord, mode string) policy.CustomerSnapshot {
	if record == nil {
		return policy.CustomerSnapshot{Mode: mode}
	}
	snapshot := record.Snapshot
	out := policy.CustomerSnapshot{
		ID: record.ID, Version: record.Version, Mode: snapshot.SourceMode,
		CapturedAt: snapshot.CapturedAt.UTC(), CustomerID: snapshot.CustomerID,
		AccountID: snapshot.AccountID, CardID: snapshot.CardID,
		Coverage: policy.Coverage{
			Standing: snapshot.Coverage.Standing, BalanceHistory: snapshot.Coverage.BalanceHistory,
			InflowHistory: snapshot.Coverage.InflowHistory, BridgeHistory: snapshot.Coverage.BridgeHistory,
			LifetimeDefaults: snapshot.Coverage.LifetimeDefaults, Reservations: snapshot.Coverage.Reservations,
			Receivables: snapshot.Coverage.Receivables, Classification: snapshot.Coverage.Classification,
		},
		Standing: policyStanding(snapshot.Standing),
		HistoryWindow: policy.HistoryWindow{
			WindowStart: policy.Date(snapshot.HistoryWindow.WindowStart),
			WindowEnd:   policy.Date(snapshot.HistoryWindow.WindowEnd),
		},
	}
	out.SourceRefs = mapSourceRefs(snapshot.SourceRefs)
	if snapshot.BalanceHistory != nil {
		out.BalanceHistory = make([]policy.Balance, len(snapshot.BalanceHistory))
		for i, row := range snapshot.BalanceHistory {
			out.BalanceHistory[i] = policy.Balance{Date: policy.Date(row.Date), BalanceCents: row.BalanceCents}
		}
	}
	if snapshot.InflowHistory != nil {
		out.InflowHistory = make([]policy.Inflow, len(snapshot.InflowHistory))
		for i, row := range snapshot.InflowHistory {
			out.InflowHistory[i] = policy.Inflow{ID: row.ID, Date: policy.Date(row.Date),
				AmountCents: row.AmountCents, Kind: row.Kind, PayerID: row.PayerID}
		}
	}
	if snapshot.Bridges != nil {
		out.Bridges = make([]policy.Bridge, len(snapshot.Bridges))
		for i, row := range snapshot.Bridges {
			out.Bridges[i] = policy.Bridge{ID: row.ID, TransactionID: row.TransactionID,
				OriginalPrincipalCents: row.OriginalPrincipalCents,
				PrincipalOwedCents:     row.PrincipalOwedCents, FeesOwedCents: row.FeesOwedCents,
				Status: row.Status, OpenedOn: policy.Date(row.OpenedOn), DueOn: policy.Date(row.DueOn),
				ClosedOn: policyDate(row.ClosedOn), DefaultedOn: policyDate(row.DefaultedOn),
				RepaymentOutcome: policyString(row.RepaymentOutcome)}
		}
	}
	if snapshot.Reservations != nil {
		out.Reservations = make([]policy.Reservation, len(snapshot.Reservations))
		for i, row := range snapshot.Reservations {
			out.Reservations[i] = policy.Reservation{ID: row.ID, TransactionID: row.TransactionID,
				PrincipalCents: row.PrincipalCents, FeeCents: row.FeeCents, Status: row.Status,
				BridgeID: policyString(row.BridgeID)}
		}
	}
	if snapshot.Receivables != nil {
		out.Receivables = make([]policy.Receivable, len(snapshot.Receivables))
		for i, row := range snapshot.Receivables {
			out.Receivables[i] = policy.Receivable{InvoiceID: row.InvoiceID, PayerID: row.PayerID,
				Currency: row.Currency, EligibleUnpaidCents: row.EligibleUnpaidCents,
				AllocatedCents: row.AllocatedCents, DueOn: policyDate(row.DueOn),
				DueDateVerified: row.DueDateVerified, AllocationComplete: row.AllocationComplete,
				SourceRef: mapSourceRef(row.SourceRef)}
		}
	}
	if snapshot.Classifications != nil {
		out.Classifications = make([]policy.Classification, len(snapshot.Classifications))
		for i, row := range snapshot.Classifications {
			out.Classifications[i] = policy.Classification{TransactionID: row.TransactionID,
				MerchantCategory: row.MerchantCategory, SourceRef: mapSourceRef(row.SourceRef)}
		}
	}
	return out
}

func policyFacility(record *domain.FacilitySnapshotRecord, mode, facilityID string) policy.FacilitySnapshot {
	if record == nil {
		return policy.FacilitySnapshot{Mode: mode, FacilityID: facilityID}
	}
	snapshot := record.Snapshot
	return policy.FacilitySnapshot{
		ID: record.ID, Version: record.Version, Mode: snapshot.SourceMode,
		CapturedAt: snapshot.CapturedAt.UTC(), FacilityID: snapshot.FacilityID,
		SourceRefs: mapSourceRefs(snapshot.SourceRefs), Currency: snapshot.Currency,
		FacilityTotalCents: snapshot.FacilityTotalCents, ReserveFloorBPS: int64(snapshot.ReserveFloorBPS),
		FundedPrincipalCents:   snapshot.FundedPrincipalCents,
		ReservedPrincipalCents: snapshot.ReservedPrincipalCents,
		LedgerVersion:          snapshot.LedgerVersion, LedgerComplete: snapshot.LedgerComplete,
	}
}

func policyStanding(value *domain.Standing) *policy.Standing {
	if value == nil {
		return nil
	}
	return &policy.Standing{
		RelationshipStartedOn: policyDate(value.RelationshipStartedOn),
		OnboardingComplete:    policyBool(value.OnboardingComplete), Active: policyBool(value.Active),
		Suspended: policyBool(value.Suspended), ProviderAccountStatus: policyString(value.ProviderAccountStatus),
	}
}

func policyDate(value *domain.Date) *policy.Date {
	if value == nil {
		return nil
	}
	converted := policy.Date(*value)
	return &converted
}

func policyString(value *string) *string {
	if value == nil {
		return nil
	}
	converted := *value
	return &converted
}

func policyBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	converted := *value
	return &converted
}

func mapSourceRefs(references []domain.SourceReference) []policy.SourceReference {
	if references == nil {
		return nil
	}
	mapped := make([]policy.SourceReference, len(references))
	for i, reference := range references {
		mapped[i] = mapSourceRef(reference)
	}
	return mapped
}

func mapSourceRef(reference domain.SourceReference) policy.SourceReference {
	return policy.SourceReference{System: reference.System, RecordID: reference.RecordID,
		Version: reference.Version, EffectiveAt: reference.EffectiveAt.UTC()}
}

func responseFromDecision(decision policy.Result, request domain.EvaluationRequest,
	shortfallRecord domain.ShortfallRecord, mode string,
	customerRecord *domain.CustomerSnapshotRecord,
	facilityRecord *domain.FacilitySnapshotRecord) domain.EvaluationResponse {
	response := domain.EvaluationResponse{
		ShortfallID: request.ShortfallID, CustomerID: *shortfallRecord.CustomerID,
		AccountID: shortfallRecord.AccountID, TransactionID: *shortfallRecord.TransactionID,
		SourceMode: mode, EvaluatedAt: decision.EvaluatedAt.UTC(),
		PolicyID: decision.PolicyID, PolicyHash: decision.PolicyHash,
		Status: string(decision.Status), ApprovalRequired: decision.ApprovalRequired,
		FundingAuthorized: false, RequiresRecheck: true,
		CustomerLimitCents:       copyInt64Pointer(decision.CustomerLimitCents),
		ExistingExposureCents:    copyInt64Pointer(decision.ExistingExposureCents),
		PostFundingExposureCents: copyInt64Pointer(decision.PostFundingExposureCents),
		AvailableCapitalCents:    copyInt64Pointer(decision.AvailableCapitalCents),
		PrincipalCents:           copyInt64Pointer(decision.PrincipalCents),
		TermDays:                 decision.TermDays, EstimatedFeeCents: copyInt64Pointer(decision.EstimatedFeeCents),
		TotalDueCents: copyInt64Pointer(decision.TotalDueCents),
		Rules:         make([]domain.EvaluationRule, len(decision.Rules)),
		ReasonCodes:   append([]string{}, decision.ReasonCodes...),
	}
	if decision.Tier != nil {
		tier := string(*decision.Tier)
		response.Tier = &tier
	}
	if customerRecord != nil {
		response.CustomerSnapshotID = stringPointer(customerRecord.ID)
		response.CustomerSnapshotVersion = int64Pointer(customerRecord.Version)
	}
	if facilityRecord != nil {
		response.FacilitySnapshotID = stringPointer(facilityRecord.ID)
		response.FacilitySnapshotVersion = int64Pointer(facilityRecord.Version)
	}
	for i, rule := range decision.Rules {
		response.Rules[i] = domain.EvaluationRule{
			RuleID: string(rule.RuleID), Status: string(rule.Status), ReasonCode: rule.ReasonCode,
			Actual: rule.Actual, Threshold: rule.Threshold,
			SourceRefs: make([]domain.SourceReference, len(rule.SourceRefs)),
		}
		for j, reference := range rule.SourceRefs {
			response.Rules[i].SourceRefs[j] = domain.SourceReference{
				System: reference.System, RecordID: reference.RecordID,
				Version: reference.Version, EffectiveAt: reference.EffectiveAt.UTC(),
			}
		}
	}
	return response
}

func stringPointer(value string) *string { return &value }

func int64Pointer(value int64) *int64 { return &value }

func copyInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	return int64Pointer(*value)
}

func validIdempotencyKey(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'A' && char <= 'Z') && !(char >= 'a' && char <= 'z') &&
			!(char >= '0' && char <= '9') && char != '.' && char != '_' && char != ':' && char != '-' {
			return false
		}
	}
	return true
}

func validIdentifier(value string) bool {
	return value != "" && len(value) <= 200 && value == strings.TrimSpace(value) &&
		utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func failure(status int, code string) error { return &Failure{Status: status, Code: code} }
