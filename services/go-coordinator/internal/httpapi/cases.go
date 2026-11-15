package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	agent "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/agent"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/underwriting"
)

const casePageSize = 25

type detailRetryKey struct{}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type caseHandler struct {
	repo      *postgres.Repository
	bindingID string
	mode      string
	auth      *Auth
}

type caseCursor struct {
	Mode       string    `json:"mode"`
	BindingID  string    `json:"binding_id"`
	CustomerID string    `json:"customer_id"`
	CreatedAt  time.Time `json:"created_at"`
	ID         string    `json:"id"`
}

type listItem struct {
	ID               string     `json:"id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	NextStage        string     `json:"next_stage"`
	CustomerID       *string    `json:"customer_id"`
	AccountRef       *string    `json:"account_ref"`
	DetectionID      *string    `json:"detection_id"`
	BridgeID         *string    `json:"bridge_id"`
	BridgeVersion    *int64     `json:"bridge_version"`
	RefreshNeeded    bool       `json:"refresh_needed"`
	RetryAt          *time.Time `json:"retry_at"`
	HeldReason       *string    `json:"held_reason"`
	AssessmentID     *string    `json:"assessment_id"`
	DecisionStatus   *string    `json:"decision_status"`
	ApprovalRequired *bool      `json:"approval_required"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type listResponse struct {
	Items      []listItem `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

type caseDetail struct {
	CaseID          string             `json:"case_id"`
	Mode            string             `json:"mode"`
	Kind            string             `json:"kind"`
	Status          string             `json:"status"`
	Stage           string             `json:"stage"`
	CustomerID      *string            `json:"customer_id"`
	AccountRef      *string            `json:"account_ref"`
	DetectionID     *string            `json:"detection_id"`
	BridgeID        *string            `json:"bridge_id"`
	BridgeVersion   *int64             `json:"bridge_version"`
	RefreshNeeded   bool               `json:"refresh_needed"`
	TargetVersion   *int64             `json:"target_bridge_version"`
	HeldReason      *string            `json:"held_reason"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Assessment      *publicEvaluation  `json:"assessment,omitempty"`
	NextActions     []string           `json:"next_actions"`
	Investigation   *investigationView `json:"investigation,omitempty"`
	Runs            []runView          `json:"runs"`
	Evidence        []evidenceView     `json:"evidence"`
	Recommendations []map[string]any   `json:"recommendations"`
	Collection      *collectionView    `json:"collection,omitempty"`
}

type publicEvaluation struct {
	ID                       string       `json:"id"`
	ShortfallID              string       `json:"shortfall_id"`
	CustomerID               string       `json:"customer_id"`
	AccountRef               string       `json:"account_ref"`
	SourceMode               string       `json:"source_mode"`
	EvaluatedAt              time.Time    `json:"evaluated_at"`
	CreatedAt                time.Time    `json:"created_at"`
	PolicyID                 string       `json:"policy_id"`
	PolicyHash               string       `json:"policy_hash"`
	CustomerSnapshotID       *string      `json:"customer_snapshot_id"`
	CustomerSnapshotVersion  *int64       `json:"customer_snapshot_version"`
	FacilitySnapshotID       *string      `json:"facility_snapshot_id"`
	FacilitySnapshotVersion  *int64       `json:"facility_snapshot_version"`
	Status                   string       `json:"status"`
	ApprovalRequired         bool         `json:"approval_required"`
	FundingAuthorized        bool         `json:"funding_authorized"`
	RequiresRecheck          bool         `json:"requires_recheck"`
	Tier                     *string      `json:"tier"`
	CustomerLimitCents       *int64       `json:"customer_limit_cents"`
	ExistingExposureCents    *int64       `json:"existing_exposure_cents"`
	PostFundingExposureCents *int64       `json:"post_funding_exposure_cents"`
	AvailableCapitalCents    *int64       `json:"available_capital_cents"`
	PrincipalCents           *int64       `json:"principal_cents"`
	TermDays                 int          `json:"term_days"`
	EstimatedFeeCents        *int64       `json:"estimated_fee_cents"`
	TotalDueCents            *int64       `json:"total_due_cents"`
	Rules                    []publicRule `json:"rules"`
	ReasonCodes              []string     `json:"reason_codes"`
}

type publicRule struct {
	RuleID     string          `json:"rule_id"`
	Status     string          `json:"status"`
	ReasonCode string          `json:"reason_code"`
	Actual     json.RawMessage `json:"actual"`
	Threshold  json.RawMessage `json:"threshold"`
}

type investigationView struct {
	SourceMode         string          `json:"source_mode"`
	AgentMode          string          `json:"agent_mode"`
	ExplanationMode    string          `json:"explanation_mode"`
	Verified           bool            `json:"verified"`
	Current            bool            `json:"current"`
	BridgeVersion      *int64          `json:"bridge_version,omitempty"`
	Summary            string          `json:"summary"`
	Findings           []agent.Finding `json:"findings"`
	MissingInformation []string        `json:"missing_information"`
	Conflicts          []string        `json:"conflicts"`
	CommentarySteps    []string        `json:"commentary_recommendations"`
	NextActions        []string        `json:"next_actions"`
	Limitation         string          `json:"limitation,omitempty"`
}

type runView struct {
	ID              string     `json:"id"`
	Revision        int        `json:"revision"`
	Stage           string     `json:"stage"`
	AgentMode       string     `json:"agent_mode"`
	ModelVersion    string     `json:"model_version"`
	ExplanationMode string     `json:"explanation_mode,omitempty"`
	Superseded      bool       `json:"superseded"`
	SupersededBy    *int64     `json:"superseded_by_bridge_version,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Steps           []stepView `json:"steps"`
}

type stepView struct {
	Sequence        int            `json:"sequence"`
	Kind            string         `json:"kind"`
	ToolName        string         `json:"tool_name,omitempty"`
	Outcome         string         `json:"outcome"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     *time.Time     `json:"completed_at"`
	ArgumentSummary map[string]any `json:"argument_summary,omitempty"`
	ResultSummary   map[string]any `json:"result_summary,omitempty"`
	ModelSummary    map[string]any `json:"model_summary,omitempty"`
}

type evidenceView struct {
	ID                  string     `json:"id"`
	State               string     `json:"state"`
	SourceHandle        string     `json:"source_handle"`
	SourceType          *string    `json:"source_type"`
	SourceVersion       string     `json:"source_version"`
	ModelVisibleHash    *string    `json:"model_visible_source_sha256"`
	OriginalSourceHash  string     `json:"original_source_sha256"`
	SectionID           *string    `json:"section_id"`
	Locator             string     `json:"locator"`
	ClaimField          string     `json:"claim_field"`
	ExtractedAt         *time.Time `json:"extracted_at"`
	ModelVersion        *string    `json:"model_version"`
	PromptVersion       *string    `json:"prompt_version"`
	FinanciallyVerified bool       `json:"financially_verified"`
}

type collectionView struct {
	BridgeVersion         int64                 `json:"bridge_version"`
	ObservedAt            time.Time             `json:"observed_at"`
	Current               bool                  `json:"current"`
	SourceStale           bool                  `json:"source_stale"`
	Lifecycle             string                `json:"lifecycle"`
	Delinquency           string                `json:"delinquency"`
	DaysPastDue           *int                  `json:"days_past_due"`
	PrincipalOwedCents    *int64                `json:"principal_owed_cents"`
	ContractFeeOwedCents  *int64                `json:"contract_fee_owed_cents"`
	LateFeeOwedCents      *int64                `json:"late_fee_owed_cents"`
	DueOn                 *string               `json:"due_on"`
	LastAttemptID         *string               `json:"last_attempt_id"`
	LastAttemptState      *string               `json:"last_attempt_state"`
	NextEligibleRetryAt   *time.Time            `json:"next_eligible_retry_at"`
	AccrualCurrent        *bool                 `json:"accrual_current"`
	AccrualCurrentThrough *string               `json:"accrual_current_through"`
	DebtAsOf              *time.Time            `json:"debt_as_of"`
	DebtStale             bool                  `json:"debt_stale"`
	HoldReasons           []string              `json:"hold_reasons"`
	EffectiveActions      []string              `json:"effective_allowed_actions"`
	Operations            []servicing.Operation `json:"operations"`
	HistoryCount          int                   `json:"history_count"`
	HistoryComplete       bool                  `json:"history_complete"`
	HistoryTruncated      bool                  `json:"history_truncated"`
}

type toolResultSummary struct {
	OK        bool            `json:"ok"`
	ErrorCode string          `json:"error_code,omitempty"`
	Value     json.RawMessage `json:"value"`
}

// NewCaseHandler creates the scoped no-store reader API.
func NewCaseHandler(repo *postgres.Repository, bindingID, mode, secret string) (http.Handler, error) {
	auth, err := NewAuth(secret, nil)
	if err != nil {
		return nil, err
	}
	h := &caseHandler{repo: repo, bindingID: bindingID, mode: mode, auth: auth}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/coordinator/cases", h.list)
	mux.HandleFunc("GET /api/coordinator/cases/{id}", h.detail)
	return mux, nil
}

func (h *caseHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if err := h.auth.Verify(r); err != nil {
		var authErr *AuthError
		if errors.As(err, &authErr) {
			writeError(w, authErr.Status, authErr.Code)
		} else {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		}
		return false
	}
	return true
}

func (h *caseHandler) list(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	for key, values := range query {
		if (key != "customer_id" && key != "cursor") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "INVALID_QUERY")
			return
		}
	}
	var customerID *string
	if values, ok := query["customer_id"]; ok {
		if values[0] == "" || len(values[0]) > 128 {
			writeError(w, http.StatusBadRequest, "INVALID_QUERY")
			return
		}
		value := values[0]
		customerID = &value
	}
	var before *time.Time
	var beforeID string
	if values, ok := query["cursor"]; ok {
		cursor, err := decodeCursor(values[0], h.mode, h.bindingID, valueOrEmpty(customerID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_QUERY")
			return
		}
		before, beforeID = &cursor.CreatedAt, cursor.ID
	}
	items, err := h.repo.ListCases(r.Context(), h.bindingID, h.mode, customerID, before, beforeID, casePageSize+1)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
		return
	}
	response := listResponse{Items: []listItem{}}
	if len(items) > casePageSize {
		last := items[casePageSize-1]
		encoded, err := encodeCursor(caseCursor{Mode: h.mode, BindingID: h.bindingID, CustomerID: valueOrEmpty(customerID), CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "CASE_CURSOR_UNAVAILABLE")
			return
		}
		response.NextCursor = &encoded
		items = items[:casePageSize]
	}
	for _, item := range items {
		response.Items = append(response.Items, listItem{ID: item.ID, Kind: item.Kind, Status: item.Status, NextStage: item.NextStage, CustomerID: item.CustomerID, AccountRef: item.AccountRef, DetectionID: item.DetectionID, BridgeID: item.BridgeID, BridgeVersion: item.BridgeVersion, RefreshNeeded: item.RefreshNeeded, RetryAt: item.RetryAt, HeldReason: item.HeldReason, AssessmentID: item.AssessmentID, DecisionStatus: item.AssessmentStatus, ApprovalRequired: item.ApprovalRequired, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *caseHandler) detail(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	caseID := r.PathValue("id")
	if !validUUID(caseID) {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY")
		return
	}
	view, err := h.repo.ReadCaseScoped(r.Context(), caseID, h.bindingID, h.mode)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "CASE_NOT_FOUND")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
		return
	}
	response := caseDetail{CaseID: view.Case.ID, Mode: view.Mode, Kind: string(view.Case.Kind), Status: string(view.Case.Status), Stage: view.Case.NextStage, CustomerID: view.Case.CustomerID, AccountRef: view.Case.AccountRef, DetectionID: view.Case.DetectionID, BridgeID: view.Case.BridgeID, BridgeVersion: view.Case.BridgeVersion, RefreshNeeded: view.Case.RefreshNeeded, TargetVersion: view.Case.TargetVersion, HeldReason: view.Case.HeldReason, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt, NextActions: []string{}, Runs: []runView{}, Evidence: []evidenceView{}, Recommendations: []map[string]any{}}
	assessment, err := h.repo.ReadLatestAssessment(r.Context(), caseID)
	if err == nil && assessment.SavedAssessmentID != nil && len(assessment.Response) > 0 {
		result, decodeErr := underwriting.DecodeEvaluation(assessment.Response)
		if decodeErr != nil || result.ID != *assessment.SavedAssessmentID || view.Case.CustomerID == nil || view.Case.RawAccountID == nil || view.Case.AccountRef == nil || result.CustomerID != *view.Case.CustomerID || result.AccountID != *view.Case.RawAccountID || result.SourceMode != h.mode || *view.Case.AccountRef != domain.InternalAccountRef(*view.Case.CustomerID) {
			writeError(w, http.StatusServiceUnavailable, "SAVED_ASSESSMENT_INVALID")
			return
		}
		if view.Case.AccountRef == nil {
			writeError(w, http.StatusServiceUnavailable, "CASE_ACCOUNT_ALIAS_UNAVAILABLE")
			return
		}
		publicResult := projectEvaluation(result, *view.Case.AccountRef, *view.Case.RawAccountID, valueOrEmpty(assessment.SourceCardID))
		response.Assessment = &publicResult
		rules := make([]agent.Rule, 0, len(result.Rules))
		for _, rule := range result.Rules {
			rules = append(rules, agent.Rule{ID: rule.ID, Status: rule.Status, Reason: rule.ReasonCode})
		}
		response.NextActions = agent.ActionsForAssessment(agent.SavedAssessment{ID: result.ID, Status: result.Status, ShortfallID: result.ShortfallID, TermDays: result.TermDays, ApprovalRequired: &result.ApprovalRequired, RuleReasons: rules, FundingAuthorized: result.FundingAuthorized, RequiresRecheck: result.RequiresRecheck})
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusServiceUnavailable, "ASSESSMENT_READ_FAILED")
		return
	}
	runs, err := h.repo.ListRunsForCase(r.Context(), caseID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "CASE_RUNS_UNAVAILABLE")
		return
	}
	var latestDisposition *agent.RunDisposition
	for _, run := range runs {
		steps, err := h.repo.ListAttemptsForRun(r.Context(), caseID, run.ID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "CASE_STEPS_UNAVAILABLE")
			return
		}
		runSummary := h.safeRunView(run, steps, view.Case.BridgeVersion)
		response.Runs = append(response.Runs, runSummary)
		if len(run.Publication) > 0 && !runSummary.Superseded {
			var disposition agent.RunDisposition
			if json.Unmarshal(run.Publication, &disposition) == nil {
				latestDisposition = &disposition
			}
		}
	}
	if latestDisposition != nil && latestDisposition.Finish != nil {
		agentMode := "UNKNOWN"
		if len(runs) > 0 {
			var packet struct {
				AgentMode string `json:"agent_mode"`
			}
			_ = json.Unmarshal(runs[len(runs)-1].ContextRefs, &packet)
			if packet.AgentMode != "" {
				agentMode = packet.AgentMode
			}
		}
		response.Investigation = &investigationView{SourceMode: h.mode, AgentMode: agentMode, ExplanationMode: latestDisposition.ExplanationMode, Verified: false, Current: view.Case.Kind != domain.CaseCollection, BridgeVersion: latestDisposition.BridgeVersion, Summary: latestDisposition.Finish.Summary, Findings: latestDisposition.Finish.Findings, MissingInformation: latestDisposition.Finish.MissingInformation, Conflicts: latestDisposition.Finish.Conflicts, CommentarySteps: latestDisposition.Finish.RecommendedNextSteps, NextActions: latestDisposition.NextActions, Limitation: latestDisposition.Limitation}
	}
	evidence, err := h.repo.ListEvidence(r.Context(), caseID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "CASE_EVIDENCE_UNAVAILABLE")
		return
	}
	for _, item := range evidence {
		response.Evidence = append(response.Evidence, evidenceView{ID: item.ID, State: item.State, SourceHandle: item.SourceHandle, SourceType: item.SourceType, SourceVersion: item.SourceVersion, ModelVisibleHash: item.VisibleSourceHash, OriginalSourceHash: item.SourceHash, SectionID: item.SectionID, Locator: item.Locator, ClaimField: item.ClaimField, ExtractedAt: item.ExtractedAt, ModelVersion: item.ModelVersion, PromptVersion: item.PromptVersion, FinanciallyVerified: false})
	}
	if view.Case.Kind == domain.CaseCollection {
		if view.Case.BridgeVersion != nil {
			snapshot, err := h.repo.ReadCollectionSnapshot(r.Context(), caseID, h.bindingID, h.mode)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "COLLECTION_SNAPSHOT_UNAVAILABLE")
				return
			}
			collection, err := safeCollection(snapshot, view.Case.RefreshNeeded, view.ServicingStale)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "COLLECTION_SNAPSHOT_INVALID")
				return
			}
			response.Collection = &collection
			if collection.Current {
				response.NextActions = collection.EffectiveActions
			} else {
				response.NextActions = []string{}
			}
			if response.Investigation != nil && latestDisposition != nil {
				current := collection.Current && view.Case.BridgeID != nil && latestDisposition.BridgeID == *view.Case.BridgeID && latestDisposition.BridgeVersion != nil && *latestDisposition.BridgeVersion == collection.BridgeVersion && sameActionCodes(latestDisposition.NextActions, collection.EffectiveActions)
				response.Investigation.Current = current
				if !current {
					response.Investigation.NextActions = []string{}
				}
			}
		}
		recommendations, err := h.repo.ListRecommendations(r.Context(), caseID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "CASE_RECOMMENDATIONS_UNAVAILABLE")
			return
		}
		for _, recommendation := range recommendations {
			view := safeRecommendation(recommendation)
			view["current"] = response.Collection != nil && response.Collection.Current && recommendation.BridgeVersion == response.Collection.BridgeVersion
			response.Recommendations = append(response.Recommendations, view)
		}
	}
	if view.Case.Kind == domain.CaseCollection {
		stable, err := h.repo.CaseDetailFence(r.Context(), view, h.bindingID, h.mode)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "CASE_DETAIL_FENCE_UNAVAILABLE")
			return
		}
		if !stable {
			retries, _ := r.Context().Value(detailRetryKey{}).(int)
			if retries == 0 {
				h.detail(w, r.WithContext(context.WithValue(r.Context(), detailRetryKey{}, 1)))
				return
			}
			writeError(w, http.StatusServiceUnavailable, "CASE_CHANGED_RETRY")
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func sameActionCodes(left, right []string) bool {
	left = uniqueActionCodes(left)
	right = uniqueActionCodes(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func uniqueActionCodes(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func projectEvaluation(result underwriting.Evaluation, accountRef, rawAccountID, rawCardID string) publicEvaluation {
	rules := make([]publicRule, 0, len(result.Rules))
	for _, rule := range result.Rules {
		rules = append(rules, publicRule{RuleID: rule.ID, Status: rule.Status, ReasonCode: rule.ReasonCode, Actual: redactPrivateAccountData(rule.Actual, []string{rawAccountID, rawCardID, result.TransactionID}, []string{result.CustomerID, accountRef}), Threshold: redactPrivateAccountData(rule.Threshold, []string{rawAccountID, rawCardID, result.TransactionID}, []string{result.CustomerID, accountRef})})
	}
	return publicEvaluation{ID: result.ID, ShortfallID: result.ShortfallID, CustomerID: result.CustomerID, AccountRef: accountRef, SourceMode: result.SourceMode, EvaluatedAt: result.EvaluatedAt, CreatedAt: result.CreatedAt, PolicyID: result.PolicyID, PolicyHash: result.PolicyHash, CustomerSnapshotID: result.CustomerSnapshotID, CustomerSnapshotVersion: result.CustomerSnapshotVersion, FacilitySnapshotID: result.FacilitySnapshotID, FacilitySnapshotVersion: result.FacilitySnapshotVersion, Status: result.Status, ApprovalRequired: result.ApprovalRequired, FundingAuthorized: result.FundingAuthorized, RequiresRecheck: result.RequiresRecheck, Tier: result.Tier, CustomerLimitCents: result.CustomerLimitCents, ExistingExposureCents: result.ExistingExposureCents, PostFundingExposureCents: result.PostFundingExposureCents, AvailableCapitalCents: result.AvailableCapitalCents, PrincipalCents: result.PrincipalCents, TermDays: result.TermDays, EstimatedFeeCents: result.EstimatedFeeCents, TotalDueCents: result.TotalDueCents, Rules: rules, ReasonCodes: result.ReasonCodes}
}

func redactPrivateAccountData(raw json.RawMessage, privateIDs, safeReferences []string) json.RawMessage {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return json.RawMessage("null")
	}
	privateSet := make(map[string]bool, len(privateIDs))
	for _, privateID := range privateIDs {
		if privateID != "" {
			privateSet[privateID] = true
		}
	}
	safeSet := make(map[string]bool, len(safeReferences))
	for _, safeReference := range safeReferences {
		if safeReference != "" {
			safeSet[safeReference] = true
		}
	}
	value, _ = redactPrivateAccountValue(value, "", privateSet, safeSet)
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return encoded
}

func redactPrivateAccountValue(value any, key string, privateIDs, safeReferences map[string]bool) (any, bool) {
	lowerKey := strings.ToLower(key)
	if lowerKey == "customer_id" || lowerKey == "account_ref" {
		textValue, ok := value.(string)
		return value, ok && safeReferences[textValue]
	}
	if privateIdentityField(lowerKey) {
		return nil, false
	}
	switch current := value.(type) {
	case string:
		return current, !privateIDs[current]
	case []any:
		out := make([]any, 0, len(current))
		for _, item := range current {
			cleaned, keep := redactPrivateAccountValue(item, "", privateIDs, safeReferences)
			if keep {
				out = append(out, cleaned)
			}
		}
		return out, true
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			cleaned, keep := redactPrivateAccountValue(item, key, privateIDs, safeReferences)
			if keep {
				out[key] = cleaned
			}
		}
		return out, true
	}
	return value, true
}

func privateIdentityField(key string) bool {
	if key == "account_ref" {
		return false
	}
	if key == "id" || strings.HasSuffix(key, "_id") || strings.Contains(key, "source_ref") || strings.Contains(key, "payment") || strings.Contains(key, "provider_ref") {
		return true
	}
	return false
}

func (h *caseHandler) safeRunView(run postgres.RunView, attempts []postgres.Attempt, currentBridgeVersion *int64) runView {
	mode := "UNKNOWN"
	var packet struct {
		AgentMode string `json:"agent_mode"`
		Bridge    struct {
			Version int64 `json:"version"`
		} `json:"bridge"`
	}
	_ = json.Unmarshal(run.ContextRefs, &packet)
	if packet.AgentMode != "" {
		mode = packet.AgentMode
	}
	view := runView{ID: run.ID, Revision: run.Revision, Stage: run.Stage, AgentMode: mode, ModelVersion: run.ModelVersion, Superseded: run.SupersededByBridgeVersion != nil, SupersededBy: run.SupersededByBridgeVersion, StartedAt: run.StartedAt, UpdatedAt: run.UpdatedAt, Steps: []stepView{}}
	if packet.Bridge.Version != 0 && currentBridgeVersion != nil && packet.Bridge.Version < *currentBridgeVersion {
		view.Superseded = true
	}
	toolNames := make(map[string]string)
	for _, attempt := range attempts {
		if attempt.Kind != "MODEL" || attempt.Outcome != "COMPLETED" {
			continue
		}
		var model agent.Response
		if json.Unmarshal(attempt.ResultBody, &model) != nil {
			continue
		}
		for _, raw := range model.Content {
			var tool struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &tool) == nil && tool.Type == "tool_use" && tool.ID != "" {
				toolNames[tool.ID] = tool.Name
			}
		}
	}
	for _, attempt := range attempts {
		step := stepView{Sequence: attempt.Sequence, Kind: attempt.Kind, Outcome: attempt.Outcome, StartedAt: attempt.CreatedAt, CompletedAt: attempt.CompletedAt}
		if attempt.Kind == "MODEL" {
			var response agent.Response
			if json.Unmarshal(attempt.ResultBody, &response) == nil && response.Type == "message" {
				step.ModelSummary = map[string]any{"stop_reason": response.StopReason, "input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens}
			} else {
				var failure struct {
					Kind agent.ProviderFailureKind `json:"kind"`
					Code string                    `json:"code"`
				}
				if json.Unmarshal(attempt.ResultBody, &failure) == nil && failure.Code != "" {
					step.ModelSummary = map[string]any{"failure_kind": failure.Kind, "error_code": failure.Code}
				}
			}
		} else {
			if attempt.CallID != nil {
				step.ToolName = toolNames[*attempt.CallID]
			}
			step.ArgumentSummary = safeArguments(step.ToolName, attempt.RequestBody)
			step.ResultSummary = safeResult(step.ToolName, attempt.ResultBody)
		}
		view.Steps = append(view.Steps, step)
	}
	if len(view.Steps) > 24 {
		view.Steps = view.Steps[len(view.Steps)-24:]
	}
	if len(run.Publication) > 0 {
		var disposition agent.RunDisposition
		if json.Unmarshal(run.Publication, &disposition) == nil {
			view.ExplanationMode = disposition.ExplanationMode
		}
	}
	return view
}

func safeArguments(toolName string, raw []byte) map[string]any {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"handle", "section", "claim_field", "operation_id", "action_code", "bridge_version"} {
		if value, ok := values[key]; ok {
			var decoded any
			if json.Unmarshal(value, &decoded) == nil {
				out[key] = decoded
			}
		}
	}
	if rawCitations, ok := values["citations"]; ok {
		var citations []json.RawMessage
		if json.Unmarshal(rawCitations, &citations) == nil {
			out["citation_count"] = len(citations)
		}
	}
	if rawFindings, ok := values["findings"]; ok {
		var findings []json.RawMessage
		if json.Unmarshal(rawFindings, &findings) == nil {
			out["finding_count"] = len(findings)
		}
	}
	if rawSummary, ok := values["summary"]; ok {
		var summary string
		if json.Unmarshal(rawSummary, &summary) == nil {
			out["summary_length"] = len(summary)
		}
	}
	if toolName == "record_collection_recommendation" {
		if rawReason, ok := values["reason"]; ok {
			var reason string
			if json.Unmarshal(rawReason, &reason) == nil {
				out["reason_length"] = len(reason)
			}
		}
	}
	return out
}

func safeResult(toolName string, raw []byte) map[string]any {
	var result toolResultSummary
	if json.Unmarshal(raw, &result) != nil {
		return nil
	}
	out := map[string]any{"ok": result.OK}
	if result.ErrorCode != "" {
		out["error_code"] = result.ErrorCode
	}
	if len(result.Value) == 0 {
		return out
	}
	switch toolName {
	case "read_selected_source":
		var value struct {
			Handle       string `json:"handle"`
			Section      string `json:"section_id"`
			Version      string `json:"source_version"`
			OriginalHash string `json:"original_source_sha256"`
			VisibleHash  string `json:"model_visible_section_sha256"`
			Text         string `json:"untrusted_text"`
			Truncated    bool   `json:"truncated"`
		}
		if json.Unmarshal(result.Value, &value) == nil {
			out["value"] = map[string]any{"handle": value.Handle, "section_id": value.Section, "source_version": value.Version, "original_source_sha256": value.OriginalHash, "model_visible_section_sha256": value.VisibleHash, "text_bytes": len(value.Text), "truncated": value.Truncated}
		}
	case "submit_evidence":
		var value struct {
			EvidenceID string `json:"evidence_id"`
			State      string `json:"state"`
			Verified   bool   `json:"financially_verified"`
		}
		if json.Unmarshal(result.Value, &value) == nil {
			out["value"] = value
		}
	case "read_collection_progress":
		var value map[string]json.RawMessage
		if json.Unmarshal(result.Value, &value) == nil {
			var version int64
			_ = json.Unmarshal(value["bridge_version"], &version)
			var complete bool
			_ = json.Unmarshal(value["history_complete"], &complete)
			out["value"] = map[string]any{"bridge_version": version, "history_complete": complete}
		}
	case "read_collection_operation":
		var value struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
			State   string `json:"state"`
			Kind    string `json:"kind"`
		}
		if json.Unmarshal(result.Value, &value) == nil {
			out["value"] = map[string]any{"id": value.ID, "version": value.Version, "state": value.State, "kind": value.Kind}
		}
	case "record_collection_recommendation":
		var value struct {
			ID            string `json:"recommendation_id"`
			ActionCode    string `json:"action_code"`
			BridgeVersion int64  `json:"bridge_version"`
			Executed      bool   `json:"executed"`
		}
		if json.Unmarshal(result.Value, &value) == nil {
			out["value"] = value
		}
	case "finish_investigation":
		var value agent.Finish
		if json.Unmarshal(result.Value, &value) == nil {
			out["value"] = map[string]any{"finding_count": len(value.Findings), "missing_count": len(value.MissingInformation), "conflict_count": len(value.Conflicts), "summary_length": len(value.Summary)}
		}
	}
	return out
}

func safeCollection(snapshot postgres.CollectionSnapshot, refreshNeeded, sourceStale bool) (collectionView, error) {
	var state struct {
		Bridge          servicing.Bridge  `json:"bridge"`
		Attempts        []json.RawMessage `json:"attempts"`
		NextCursor      *string           `json:"next_cursor"`
		HistoryComplete bool              `json:"history_complete"`
		DebtStale       bool              `json:"debt_stale"`
		DebtAsOf        *time.Time        `json:"debt_as_of"`
		AllowedActions  []string          `json:"allowed_next_actions"`
	}
	if json.Unmarshal(snapshot.State, &state) != nil || state.Bridge.Version != snapshot.BridgeVersion {
		return collectionView{}, errors.New("collection_state_invalid")
	}
	operations := state.Bridge.Operations
	if len(operations) > 20 {
		operations = operations[:20]
	}
	return collectionView{BridgeVersion: snapshot.BridgeVersion, ObservedAt: snapshot.ObservedAt, Current: !refreshNeeded && !sourceStale, SourceStale: sourceStale, Lifecycle: state.Bridge.Lifecycle, Delinquency: state.Bridge.Delinquency, DaysPastDue: state.Bridge.DaysPastDue, PrincipalOwedCents: state.Bridge.PrincipalOwedCents, ContractFeeOwedCents: state.Bridge.ContractFeeOwedCents, LateFeeOwedCents: state.Bridge.LateFeeOwedCents, DueOn: state.Bridge.DueOn, LastAttemptID: state.Bridge.LastAttemptID, LastAttemptState: state.Bridge.LastAttemptState, NextEligibleRetryAt: state.Bridge.NextEligibleRetryAt, AccrualCurrent: state.Bridge.AccrualCurrent, AccrualCurrentThrough: state.Bridge.AccrualCurrentThrough, DebtAsOf: state.DebtAsOf, DebtStale: state.DebtStale, HoldReasons: state.Bridge.HoldReasons, EffectiveActions: state.AllowedActions, Operations: operations, HistoryCount: len(state.Attempts), HistoryComplete: state.HistoryComplete, HistoryTruncated: !state.HistoryComplete || state.NextCursor != nil}, nil
}

func encodeCursor(cursor caseCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(value, mode, bindingID, customerID string) (caseCursor, error) {
	var cursor caseCursor
	if value == "" || len(value) > 2048 {
		return cursor, errors.New("case_cursor_invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, errors.New("case_cursor_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Mode != mode || cursor.BindingID != bindingID || cursor.CustomerID != customerID || cursor.CreatedAt.IsZero() || !validUUID(cursor.ID) {
		return caseCursor{}, errors.New("case_cursor_invalid")
	}
	return cursor, nil
}

func validUUID(value string) bool { return uuidPattern.MatchString(value) }

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func rulesForAgent(evaluation underwriting.Evaluation) []agent.Rule {
	rules := make([]agent.Rule, 0, len(evaluation.Rules))
	for _, rule := range evaluation.Rules {
		rules = append(rules, agent.Rule{ID: rule.ID, Status: rule.Status, Reason: rule.ReasonCode})
	}
	return rules
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func evidenceProjection(records []postgres.EvidenceRecord) []evidenceView {
	result := make([]evidenceView, 0, len(records))
	for _, record := range records {
		result = append(result, evidenceView{ID: record.ID, State: record.State, SourceHandle: record.SourceHandle, SourceType: record.SourceType, SourceVersion: record.SourceVersion, ModelVisibleHash: record.VisibleSourceHash, OriginalSourceHash: record.SourceHash, SectionID: record.SectionID, Locator: record.Locator, ClaimField: record.ClaimField, ExtractedAt: record.ExtractedAt, ModelVersion: record.ModelVersion, PromptVersion: record.PromptVersion, FinanciallyVerified: false})
	}
	return result
}

func safeRecommendation(item postgres.RecommendationView) map[string]any {
	var citations []string
	_ = json.Unmarshal(item.Citations, &citations)
	return map[string]any{"recommendation_id": item.ID, "run_id": item.RunID, "bridge_version": item.BridgeVersion, "action_code": item.ActionCode, "citation_ids": citations, "executed": false, "source": "AI_COMMENTARY", "verified": false, "created_at": item.CreatedAt}
}
