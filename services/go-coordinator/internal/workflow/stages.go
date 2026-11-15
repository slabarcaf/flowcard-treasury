// Package workflow holds explicit deterministic stage decisions used by the serial scheduler.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	agent "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/agent"
	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/shortfall"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/underwriting"
)

type StepResult struct {
	Status               string
	NextStage            string
	RetryAt              *time.Time
	HeldReason           *string
	DispositionCommitted bool
}
type Executor interface {
	Execute(context.Context, domain.Case, string) (StepResult, error)
}
type StageExecutor struct {
	Repo            *postgres.Repository
	Shortfall       *shortfall.Client
	Underwriting    *underwriting.Client
	CustomerContext *customercontext.Client
	Servicing       *servicing.Client
	AgentProvider   agent.Provider
	AgentMode       string
	ModelVersion    string
	SourceRoot      string
	Mode, BindingID string
	Now             func() time.Time
}

func (e *StageExecutor) Execute(ctx context.Context, c domain.Case, token string) (StepResult, error) {
	if e.BindingID == "" || c.BindingID != e.BindingID || (e.Mode != "DEMO" && e.Mode != "SANDBOX") {
		return StepResult{}, domain.ErrConflict
	}
	switch leaseStage(c) {
	case "REFRESH_BRIDGE":
		return e.refresh(ctx, c, token)
	case "LOAD_DETAIL":
		return e.loadShortfall(ctx, c)
	case "EVALUATE":
		return e.evaluate(ctx, c, token)
	case "INVESTIGATE":
		return e.investigate(ctx, c, token)
	case "HELD":
		return StepResult{Status: "HELD", NextStage: "HELD", HeldReason: stringPtr("SOURCE_SCOPE_INVALID")}, nil
	default:
		reason := "GROUP_C_STAGE_NOT_IMPLEMENTED:" + leaseStage(c)
		return StepResult{Status: "HELD", NextStage: leaseStage(c), HeldReason: &reason}, nil
	}
}

func (e *StageExecutor) loadShortfall(ctx context.Context, c domain.Case) (StepResult, error) {
	if c.DetectionID == nil {
		return StepResult{}, errors.New("shortfall_detection_id_missing")
	}
	if e.Shortfall == nil {
		return StepResult{Status: "HELD", NextStage: "LOAD_DETAIL", HeldReason: stringPtr("SHORTFALL_DETAIL_CLIENT_NOT_BOUND")}, nil
	}
	record, status, err := e.Shortfall.Read(ctx, *c.DetectionID)
	if err != nil {
		if status == 404 {
			return StepResult{Status: "HELD", NextStage: "LOAD_DETAIL", HeldReason: stringPtr("SHORTFALL_DETAIL_NOT_FOUND")}, nil
		}
		return StepResult{}, err
	}
	if c.CustomerID == nil || record.CustomerID == nil || *c.CustomerID != *record.CustomerID || c.RawAccountID == nil || *c.RawAccountID != record.AccountID || c.AccountRef == nil || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
		return StepResult{Status: "HELD", NextStage: "LOAD_DETAIL", HeldReason: stringPtr("SHORTFALL_SCOPE_MISMATCH")}, nil
	}
	if reason := shortfall.HoldReason(record); reason != "" {
		return StepResult{Status: "HELD", NextStage: "LOAD_DETAIL", HeldReason: stringPtr(reason)}, nil
	}
	return StepResult{Status: "EVALUATING", NextStage: "EVALUATE"}, nil
}

func (e *StageExecutor) refresh(ctx context.Context, c domain.Case, token string) (StepResult, error) {
	if e.Servicing == nil || c.BridgeID == nil {
		return StepResult{}, errors.New("servicing_client_unavailable")
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	for restart := 0; restart < 2; restart++ {
		bridge, status, err := e.Servicing.ReadBridge(ctx, *c.BridgeID)
		if err != nil {
			if status == 404 {
				return StepResult{Status: "HELD", NextStage: "REFRESH_BRIDGE", HeldReason: stringPtr("SERVICING_BRIDGE_NOT_FOUND")}, nil
			}
			return StepResult{}, err
		}
		if c.CustomerID == nil || c.AccountRef == nil || bridge.CustomerID != *c.CustomerID || bridge.AccountRef != *c.AccountRef || bridge.SourceBindingID != c.BindingID || bridge.Mode != e.Mode {
			return StepResult{}, &servicing.ContractError{Code: "servicing_bridge_case_scope_mismatch"}
		}
		origin, err := e.Repo.ReadCollectionOriginLink(ctx, c.ID, token)
		if err != nil {
			return StepResult{}, err
		}
		if origin.CaseID == nil {
			if bridge.OriginatingCaseID != nil {
				return StepResult{}, &servicing.ContractError{Code: "servicing_bridge_origin_unproven"}
			}
		} else if bridge.OriginatingCaseID == nil || *bridge.OriginatingCaseID != *origin.CaseID || bridge.EvaluationID == nil || *bridge.EvaluationID != *origin.EvaluationID || bridge.OfferID == nil || *bridge.OfferID != *origin.OfferID {
			return StepResult{}, &servicing.ContractError{Code: "servicing_bridge_origin_mismatch"}
		}
		progress, err := e.Repo.ReadCollectionProgress(ctx, c.ID, bridge.Version)
		if err != nil {
			return StepResult{}, err
		}
		cursor := progress.CursorOut
		pageNo := progress.PageNumber
		var histories []json.RawMessage
		if len(progress.Items) > 0 {
			if err := json.Unmarshal(progress.Items, &histories); err != nil {
				return StepResult{}, errors.New("collection_progress_invalid")
			}
		}
		versionRestart := false
		historyComplete := pageNo > 0 && cursor == nil
		for pageNo < 2 && !historyComplete {
			p, st, err := e.Servicing.Collections(ctx, *c.BridgeID, bridge.Version, cursor)
			if err != nil {
				if st == 409 || errors.Is(err, servicing.ErrVersionChanged) {
					versionRestart = true
					break
				}
				return StepResult{}, err
			}
			pageNo++
			histories = append(histories, p.Items...)
			encoded, err := json.Marshal(histories)
			if err != nil {
				return StepResult{}, errors.New("collection_progress_encode_failed")
			}
			if err := e.Repo.SaveCollectionProgress(ctx, c.ID, token, bridge.Version, pageNo, cursor, p.NextCursor, encoded); err != nil {
				return StepResult{}, err
			}
			cursor = p.NextCursor
			if cursor == nil {
				historyComplete = true
				break
			}
		}
		if versionRestart {
			if restart == 0 && c.TransientFailures == 0 {
				continue
			}
			return StepResult{}, errors.New("servicing_version_unstable")
		}
		if len(histories) > 50 {
			histories = histories[:50]
			historyComplete = false
		}
		effectiveActions, unsupportedActions := projectActions(bridge)
		result := refreshDisposition(bridge, historyComplete, effectiveActions)
		var previousObservation *collectionObservation
		if c.BridgeVersion != nil {
			previous, err := e.Repo.ReadCollectionSnapshot(ctx, c.ID, c.BindingID, e.Mode)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return StepResult{}, err
			}
			if err == nil && previous.BridgeVersion == *c.BridgeVersion {
				var saved collectionObservation
				if json.Unmarshal(previous.State, &saved) != nil || saved.Bridge.ID != bridge.ID || saved.Bridge.Version != previous.BridgeVersion {
					return StepResult{}, errors.New("prior_collection_observation_invalid")
				}
				previousObservation = &saved
			}
		}
		runCount, err := e.Repo.CountCollectionRuns(ctx, c.ID, token, bridge.ID)
		if err != nil {
			return StepResult{}, err
		}
		triggerCode := collectionTriggerCode(runCount, result.Status, effectiveActions, previousObservation, bridge)
		var pendingTrigger *string
		var pendingTriggerVersion *int64
		if runCount < 3 && triggerCode != "" {
			result.NextStage = "INVESTIGATE"
			pendingTrigger = &triggerCode
			pendingTriggerVersion = &bridge.Version
		}
		responseAsOf := bridge.AsOf
		bridge.AsOf = time.Time{} // response time is stored separately from semantic versioned state
		observation := struct {
			Bridge             servicing.Bridge  `json:"bridge"`
			Attempts           []json.RawMessage `json:"attempts"`
			NextCursor         *string           `json:"next_cursor"`
			HistoryComplete    bool              `json:"history_complete"`
			DebtStale          bool              `json:"debt_stale"`
			DebtAsOf           *time.Time        `json:"debt_as_of"`
			AllowedActions     []string          `json:"allowed_next_actions"`
			UnsupportedActions []string          `json:"unsupported_action_codes"`
		}{bridge, histories, cursor, historyComplete, bridge.AccrualCurrent == nil || bridge.DebtAsOf == nil || !*bridge.AccrualCurrent, bridge.DebtAsOf, effectiveActions, unsupportedActions}
		body, err := json.Marshal(observation)
		if err != nil {
			return StepResult{}, errors.New("collection_observation_encode_failed")
		}
		if err := e.Repo.CompleteRefreshDisposition(ctx, c.ID, token, bridge.Version, bridge.ID, responseAsOf, body, postgres.RefreshDisposition{Status: result.Status, NextStage: result.NextStage, RetryAt: result.RetryAt, HeldReason: result.HeldReason, InvestigationTriggerCode: pendingTrigger, InvestigationTriggerVersion: pendingTriggerVersion}); err != nil {
			return StepResult{}, err
		}
		result.DispositionCommitted = true
		return result, nil
	}
	return StepResult{}, errors.New("servicing_version_unstable")
}
func refreshDisposition(b servicing.Bridge, historyComplete bool, effectiveActions []string) StepResult {
	if b.AccrualCurrent == nil || b.DebtAsOf == nil {
		return StepResult{Status: "ACTION_REQUIRED", NextStage: "MONITORING", HeldReason: stringPtr("ACCRUAL_FRESHNESS_UNKNOWN")}
	}
	if !*b.AccrualCurrent {
		for _, action := range effectiveActions {
			if action == "WAIT_FOR_ACCRUAL" {
				return StepResult{Status: "MONITORING", NextStage: "MONITORING", HeldReason: stringPtr("WAIT_FOR_ACCRUAL")}
			}
		}
		return StepResult{Status: "ACTION_REQUIRED", NextStage: "MONITORING", HeldReason: stringPtr("WAIT_FOR_ACCRUAL_NOT_ALLOWED_BY_SOURCE")}
	}
	if len(b.HoldReasons) > 0 {
		return StepResult{Status: "ACTION_REQUIRED", NextStage: "MONITORING", HeldReason: stringPtr("SERVICING_HOLD_PRESENT")}
	}
	if b.LastAttemptID != nil && !terminalAttempt(b.LastAttemptState) {
		return StepResult{Status: "ACTION_REQUIRED", NextStage: "MONITORING", HeldReason: stringPtr("UNRESOLVED_COLLECTION_ATTEMPT")}
	}
	for _, op := range b.Operations {
		if !terminalOperation(op.State) || op.HoldReason != nil && *op.HoldReason != "" {
			return StepResult{Status: "ACTION_REQUIRED", NextStage: "MONITORING", HeldReason: stringPtr("UNRESOLVED_OPERATION")}
		}
	}
	if b.Lifecycle == "REPAID" {
		if b.PrincipalOwedCents == nil || b.ContractFeeOwedCents == nil || b.LateFeeOwedCents == nil {
			return StepResult{Status: "ACTION_REQUIRED", NextStage: "MONITORING", HeldReason: stringPtr("REPAYMENT_FACTS_INCOMPLETE")}
		}
		if *b.PrincipalOwedCents == 0 && *b.ContractFeeOwedCents == 0 && *b.LateFeeOwedCents == 0 {
			return StepResult{Status: "CLOSED", NextStage: "MONITORING"}
		}
		return StepResult{Status: "ACTION_REQUIRED", NextStage: "MONITORING", HeldReason: stringPtr("REPAID_WITH_RECORDED_DEBT")}
	}
	if !historyComplete {
		return StepResult{Status: "MONITORING", NextStage: "MONITORING", HeldReason: stringPtr("COLLECTION_HISTORY_INCOMPLETE")}
	}
	return StepResult{Status: "MONITORING", NextStage: "MONITORING"}
}
func projectActions(b servicing.Bridge) ([]string, []string) {
	known := map[string]bool{"WAIT_FOR_SETTLEMENT": true, "REQUEST_OPERATOR_REVIEW": true, "VERIFY_COLLECTION_AUTHORITY": true, "RECONCILE_OPERATION": true, "WAIT_FOR_ELIGIBLE_DEPOSIT": true, "MONITOR_REPAYMENT": true, "WAIT_FOR_ACCRUAL": true}
	var effective, unsupported []string
	for _, code := range b.AllowedNextActions {
		if !known[code] {
			unsupported = append(unsupported, code)
		}
	}
	if b.AccrualCurrent == nil || b.DebtAsOf == nil {
		return nil, unsupported
	}
	if !*b.AccrualCurrent {
		for _, code := range b.AllowedNextActions {
			if code == "WAIT_FOR_ACCRUAL" {
				return []string{code}, unsupported
			}
		}
		return nil, unsupported
	}
	for _, code := range b.AllowedNextActions {
		if known[code] {
			effective = append(effective, code)
		}
	}
	return effective, unsupported
}

func terminalAttempt(state *string) bool {
	if state == nil {
		return false
	}
	return *state == "SETTLED" || *state == "FAILED_CONFIRMED" || *state == "CANCELLED_UNSENT"
}
func terminalOperation(state string) bool {
	return state == "SETTLED" || state == "FAILED_CONFIRMED" || state == "CANCELLED_UNSENT"
}

func stringPtr(v string) *string { return &v }

func leaseStage(c domain.Case) string {
	if c.LeaseStage != nil {
		return *c.LeaseStage
	}
	return c.NextStage
}
