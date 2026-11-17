package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
)

// PromptVersion identifies the immutable investigation instructions.
const PromptVersion = "coordinator-investigation-v1"

type Engine struct {
	Repo      *postgres.Repository
	Provider  Provider
	Servicing *servicing.Client
	Mode      string
	Now       func() time.Time
}
type toolResult struct {
	OK        bool   `json:"ok"`
	ErrorCode string `json:"error_code,omitempty"`
	Value     any    `json:"value,omitempty"`
}
type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}
type sourceReadResult struct {
	Handle         string `json:"handle"`
	SectionID      string `json:"section_id"`
	Locator        string `json:"locator"`
	SourceType     string `json:"source_type"`
	RecordID       string `json:"record_id"`
	SourceVersion  string `json:"source_version"`
	ExportVersion  string `json:"export_version"`
	OriginalSHA256 string `json:"original_source_sha256"`
	VisibleSHA256  string `json:"model_visible_section_sha256"`
	Text           string `json:"untrusted_text"`
	Truncated      bool   `json:"truncated"`
}
type RunDisposition struct {
	Kind            string   `json:"kind"`
	AssessmentID    string   `json:"assessment_id,omitempty"`
	ExplanationMode string   `json:"explanation_mode,omitempty"`
	NextActions     []string `json:"next_actions,omitempty"`
	BridgeID        string   `json:"bridge_id,omitempty"`
	BridgeVersion   *int64   `json:"bridge_version,omitempty"`
	Finish          *Finish  `json:"finish,omitempty"`
	Limitation      string   `json:"limitation,omitempty"`
}

const maxProviderAttempts = 3

type providerFailureRecord struct {
	Kind    ProviderFailureKind `json:"kind"`
	Code    string              `json:"code"`
	RetryAt *time.Time          `json:"retry_at,omitempty"`
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Step performs at most one model call and one validated tool dispatch.
func (e *Engine) Step(ctx context.Context, p Packet, lease string) (StepResult, error) {
	if e.Repo == nil || e.Provider == nil {
		return StepResult{}, errors.New("agent_engine_unavailable")
	}
	if p.PromptVersion == "" {
		p.PromptVersion = PromptVersion
	}
	if p.AgentMode == "" {
		p.AgentMode = e.Mode
	}
	if p.ModelVersion == "" {
		if p.AgentMode == "FIXTURE" {
			p.ModelVersion = "fixture-v1"
		} else {
			return StepResult{}, errors.New("agent_model_id_unavailable")
		}
	}
	if e.Mode != "" && p.AgentMode != e.Mode {
		return StepResult{}, errors.New("agent_mode_mismatch")
	}
	if raw, found, err := e.Repo.ReadRunDisposition(ctx, p.CaseID, lease, p.RunID); err != nil {
		return StepResult{}, err
	} else if found {
		return decodeDisposition(raw)
	}
	packetBody, err := buildPacket(p)
	if err != nil {
		return StepResult{}, err
	}
	if err := e.Repo.VerifyRunContextRefs(ctx, p.CaseID, lease, p.RunID, packetBody); err != nil {
		return StepResult{}, err
	}
	if p.Kind == Collection {
		version, state, err := e.Repo.ReadCollectionState(ctx, p.CaseID, lease)
		progress, progressErr := compactProgress(version, state)
		if err != nil || progressErr != nil || p.Bridge == nil || version != p.Bridge.Version || progress.BridgeID != p.Bridge.ID || !sameActions(p.EffectiveActions, progress.AllowedActions) {
			return StepResult{}, domain.ErrConflict
		}
	}
	initial, err := packetMessage(packetBody)
	if err != nil {
		return StepResult{}, errors.New("agent_packet_encode_failed")
	}
	attempts, err := e.Repo.ListAttempts(ctx, p.CaseID, lease, p.RunID)
	if err != nil {
		return StepResult{}, err
	}
	latestModel := -1
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Kind == "MODEL" {
			latestModel = i
			break
		}
	}
	if latestModel >= 0 {
		i := latestModel
		attempt := attempts[i]
		failure, retryable := decodeProviderFailure(attempt.ResultBody)
		if attempt.Outcome == "DISPATCHED" {
			failure = providerFailureRecord{Kind: ProviderAmbiguous, Code: "MODEL_RESPONSE_UNCERTAIN"}
			retryable = true
		}
		if attempt.Outcome == "FAILED" && !retryable {
			return e.limit(ctx, p, lease, "MODEL_PROVIDER_LIMITATION")
		}
		if attempt.Outcome == "DISPATCHED" || retryable {
			if failure.Kind == ProviderPermanent {
				return e.limit(ctx, p, lease, failure.Code)
			}
			if attempt.Outcome == "DISPATCHED" {
				encoded, _ := json.Marshal(failure)
				if err := e.Repo.CompleteAttempt(ctx, p.CaseID, lease, p.RunID, attempt.Sequence, encoded, "FAILED"); err != nil {
					return StepResult{}, err
				}
				attempts[i].Outcome = "FAILED"
				attempts[i].ResultBody = encoded
			}
			if failure.RetryAt != nil && e.now().Before(*failure.RetryAt) {
				return StepResult{Continue: true, Limitation: "MODEL_RETRY_WAIT", RetryAt: failure.RetryAt}, nil
			}
			modelCount := countKind(attempts, "MODEL")
			if countProviderAttempts(attempts) >= maxProviderAttempts || modelCount >= MaxModelCalls {
				return e.limit(ctx, p, lease, "MODEL_RETRY_BUDGET_EXHAUSTED")
			}
			request, err := decodeRequest(attempt.RequestBody)
			if err != nil || validateStoredRequest(request, p.Kind, p.ModelVersion) != nil {
				return e.limit(ctx, p, lease, "STORED_MODEL_REQUEST_INVALID")
			}
			if modelCount < MaxModelCalls-1 || requestForcesFinish(request) {
				id, err := randomID()
				if err != nil {
					return StepResult{}, err
				}
				seq, err := e.Repo.StartAttempt(ctx, p.CaseID, lease, p.RunID, "MODEL", id, "", attempt.RequestBody)
				if err != nil {
					if errors.Is(err, domain.ErrBudgetExhausted) {
						return e.limit(ctx, p, lease, "AGENT_RUN_LIMIT_REACHED")
					}
					return StepResult{}, err
				}
				return e.invoke(ctx, p, lease, attempts, request, id, seq)
			}
		}
	}
	messages, _, finish, pending, err := buildHistory(attempts, initial, p.Kind, p.ModelVersion)
	if err != nil {
		return e.limit(ctx, p, lease, "PROTOCOL_HISTORY_INVALID")
	}
	if pending != nil {
		return e.executeTool(ctx, p, lease, *pending, attempts)
	}
	if finish != nil {
		ids, err := e.Repo.EvidenceIDsForRun(ctx, p.CaseID, lease, p.RunID)
		if err != nil {
			return StepResult{}, err
		}
		if err := validateFinish(p, *finish, ids); err != nil {
			return e.limit(ctx, p, lease, "FINISH_RESULT_INVALID")
		}
		if err := e.persistRunDisposition(ctx, p, lease, RunDisposition{Kind: "FINISH", Finish: finish}); err != nil {
			return StepResult{}, err
		}
		return StepResult{Finished: true, Finish: finish}, nil
	}
	modelCount := countKind(attempts, "MODEL")
	if modelCount >= MaxModelCalls {
		return e.limit(ctx, p, lease, "AGENT_RUN_LIMIT_REACHED")
	}
	forceFinish := modelCount >= MaxModelCalls-1
	request, body, err := buildRequest(p, messages, forceFinish)
	if err != nil {
		return StepResult{}, err
	}
	if len(body) > MaxRequestBytes-FinishReserveBytes && !forceFinish {
		request, body, err = buildRequest(p, messages, true)
		if err != nil {
			return StepResult{}, err
		}
		forceFinish = true
	}
	if len(body) > MaxRequestBytes {
		return e.limit(ctx, p, lease, "AGENT_REQUEST_LIMIT_REACHED")
	}
	id, err := randomID()
	if err != nil {
		return StepResult{}, err
	}
	seq, err := e.Repo.StartAttempt(ctx, p.CaseID, lease, p.RunID, "MODEL", id, "", body)
	if errors.Is(err, domain.ErrBudgetExhausted) {
		return e.limit(ctx, p, lease, "AGENT_RUN_LIMIT_REACHED")
	}
	if err != nil {
		return StepResult{}, err
	}
	return e.invoke(ctx, p, lease, attempts, request, id, seq)
}

func (e *Engine) invoke(ctx context.Context, p Packet, lease string, attempts []postgres.Attempt, request Request, attemptID string, sequence int) (StepResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	response, err := e.Provider.CreateMessage(callCtx, request)
	cancel()
	if err != nil {
		var providerErr *ProviderError
		if !errors.As(err, &providerErr) {
			providerErr = &ProviderError{Kind: ProviderPermanent, Code: "MODEL_PROVIDER_FAILURE"}
		}
		if providerErr.Code == "" || (providerErr.Kind != ProviderAmbiguous && providerErr.Kind != ProviderTransient && providerErr.Kind != ProviderPermanent) {
			providerErr = &ProviderError{Kind: ProviderPermanent, Code: "MODEL_PROVIDER_FAILURE"}
		}
		retryAt := e.providerRetryAt(attempts, providerErr)
		failure := providerFailureRecord{Kind: providerErr.Kind, Code: providerErr.Code, RetryAt: retryAt}
		encoded, _ := json.Marshal(failure)
		if saveErr := e.Repo.CompleteAttempt(ctx, p.CaseID, lease, p.RunID, sequence, encoded, "FAILED"); saveErr != nil {
			return StepResult{}, saveErr
		}
		if providerErr.Kind == ProviderPermanent || countProviderAttempts(append(attempts, postgres.Attempt{Kind: "MODEL", Outcome: "FAILED", ResultBody: encoded})) >= maxProviderAttempts {
			return e.limit(ctx, p, lease, providerErr.Code)
		}
		return StepResult{Continue: true, Limitation: providerErr.Code, RetryAt: retryAt}, nil
	}
	if _, err := validateModelResponse(request, response, p.Kind, p.ModelVersion); err != nil {
		failure := providerFailureRecord{Kind: ProviderPermanent, Code: "MODEL_RESPONSE_INVALID"}
		encoded, _ := json.Marshal(failure)
		if saveErr := e.Repo.CompleteAttempt(ctx, p.CaseID, lease, p.RunID, sequence, encoded, "FAILED"); saveErr != nil {
			return StepResult{}, saveErr
		}
		return e.limit(ctx, p, lease, failure.Code)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return StepResult{}, errors.New("agent_response_encode_failed")
	}
	if err := e.Repo.CompleteAttempt(ctx, p.CaseID, lease, p.RunID, sequence, encoded, "COMPLETED"); err != nil {
		return StepResult{}, err
	}
	attempts, err = e.Repo.ListAttempts(ctx, p.CaseID, lease, p.RunID)
	if err != nil {
		return StepResult{}, err
	}
	return e.consumeResponse(ctx, p, lease, attempts)
}

func (e *Engine) consumeResponse(ctx context.Context, p Packet, lease string, attempts []postgres.Attempt) (StepResult, error) {
	packet, err := buildPacket(p)
	if err != nil {
		return StepResult{}, err
	}
	initial, err := packetMessage(packet)
	if err != nil {
		return StepResult{}, errors.New("agent_packet_encode_failed")
	}
	_, _, finish, pending, err := buildHistory(attempts, initial, p.Kind, p.ModelVersion)
	if err != nil {
		return e.limit(ctx, p, lease, "PROTOCOL_HISTORY_INVALID")
	}
	if pending != nil {
		return e.executeTool(ctx, p, lease, *pending, attempts)
	}
	if finish != nil {
		ids, err := e.Repo.EvidenceIDsForRun(ctx, p.CaseID, lease, p.RunID)
		if err != nil {
			return StepResult{}, err
		}
		if err := validateFinish(p, *finish, ids); err != nil {
			return e.limit(ctx, p, lease, "FINISH_RESULT_INVALID")
		}
		if err := e.persistRunDisposition(ctx, p, lease, RunDisposition{Kind: "FINISH", Finish: finish}); err != nil {
			return StepResult{}, err
		}
		return StepResult{Finished: true, Finish: finish}, nil
	}
	return StepResult{Continue: true}, nil
}

func (e *Engine) executeTool(ctx context.Context, p Packet, lease string, block ContentBlock, attempts []postgres.Attempt) (StepResult, error) {
	sequence, err := e.Repo.StartAttempt(ctx, p.CaseID, lease, p.RunID, "TOOL", block.ID, block.ID, block.Input)
	if errors.Is(err, domain.ErrBudgetExhausted) {
		return e.limit(ctx, p, lease, "AGENT_RUN_LIMIT_REACHED")
	}
	if err != nil {
		return StepResult{}, err
	}
	value, isError, dispatchErr := e.dispatch(ctx, p, lease, block, attempts)
	if dispatchErr != nil {
		return StepResult{}, dispatchErr
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return StepResult{}, errors.New("tool_result_encode_failed")
	}
	encoded, err = limitResult(encoded)
	if err != nil {
		return StepResult{}, errors.New("agent_tool_result_limit_reached")
	}
	if block.Name != "finish_investigation" && e.wouldOverflowNextTurn(p, attempts, encoded, isError) {
		if !isError {
			encoded, err = compactSuccessfulToolResult(block.Name, value)
			if err != nil {
				return StepResult{}, err
			}
		}
	}
	if err := e.Repo.CompleteAttempt(ctx, p.CaseID, lease, p.RunID, sequence, encoded, completedOutcome(isError)); err != nil {
		return StepResult{}, err
	}
	if block.Name == "finish_investigation" && !isError {
		input, err := decodeInput[Finish](block.Input)
		if err != nil {
			return e.limit(ctx, p, lease, "FINISH_RESULT_INVALID")
		}
		if err := e.persistRunDisposition(ctx, p, lease, RunDisposition{Kind: "FINISH", Finish: &input}); err != nil {
			return StepResult{}, err
		}
		return StepResult{Finished: true, Finish: &input}, nil
	}
	return StepResult{Continue: true}, nil
}

func compactSuccessfulToolResult(name string, value toolResult) ([]byte, error) {
	var compact toolResult
	switch name {
	case "submit_evidence":
		var receipt struct {
			EvidenceID string `json:"evidence_id"`
			State      string `json:"state"`
			Verified   bool   `json:"financially_verified"`
		}
		body, err := json.Marshal(value.Value)
		if err != nil || json.Unmarshal(body, &receipt) != nil || receipt.EvidenceID == "" {
			return nil, errors.New("evidence_receipt_compaction_failed")
		}
		compact = toolResult{OK: true, Value: receipt}
	case "record_collection_recommendation":
		var receipt struct {
			RecommendationID string `json:"recommendation_id"`
			ActionCode       string `json:"action_code"`
			BridgeVersion    int64  `json:"bridge_version"`
			Executed         bool   `json:"executed"`
		}
		body, err := json.Marshal(value.Value)
		if err != nil || json.Unmarshal(body, &receipt) != nil || receipt.RecommendationID == "" {
			return nil, errors.New("recommendation_receipt_compaction_failed")
		}
		compact = toolResult{OK: true, Value: receipt}
	default:
		compact = toolResult{OK: true, Value: map[string]bool{"result_omitted_for_context_limit": true}}
	}
	return json.Marshal(compact)
}

func (e *Engine) dispatch(ctx context.Context, p Packet, lease string, b ContentBlock, attempts []postgres.Attempt) (toolResult, bool, error) {
	switch b.Name {
	case "read_selected_source":
		input, err := decodeInput[readSourceInput](b.Input)
		if err != nil {
			return failResult(err), true, nil
		}
		if p.SourceReader == nil {
			return failResult(&ToolError{Code: "SOURCE_UNAVAILABLE"}), true, nil
		}
		section, err := p.SourceReader.Read(input.Handle, input.Section)
		if err != nil {
			return failResult(&ToolError{Code: "SOURCE_UNAVAILABLE"}), true, nil
		}
		label := fmt.Sprintf("<<<BEGIN UNTRUSTED SOURCE %s/%s>>>\n%s\n<<<END UNTRUSTED SOURCE %s/%s>>>", section.Handle, section.SectionID, section.Text, section.Handle, section.SectionID)
		return toolResult{OK: true, Value: sourceReadResult{Handle: section.Handle, SectionID: section.SectionID, Locator: section.Locator, SourceType: section.SourceType, RecordID: section.RecordID, SourceVersion: section.SourceVersion, ExportVersion: section.ExportVersion, OriginalSHA256: section.OriginalSHA256, VisibleSHA256: section.VisibleSHA256, Text: label, Truncated: section.Truncated}}, false, nil
	case "submit_evidence":
		extractedAt, ok := modelCallTime(attempts, b.ID)
		if !ok {
			return failResult(&ToolError{Code: "MODEL_CALL_TIME_UNAVAILABLE"}), true, nil
		}
		return e.submitEvidence(ctx, p, lease, b, extractedAt)
	case "read_collection_progress":
		if p.Kind != Collection {
			return failResult(&ToolError{Code: "TOOL_NOT_ALLOWED_FOR_CASE"}), true, nil
		}
		version, state, err := e.Repo.ReadCollectionState(ctx, p.CaseID, lease)
		if err != nil {
			if errors.Is(err, domain.ErrCollectionRefreshPending) {
				return failResult(&ToolError{Code: "COLLECTION_REFRESH_PENDING"}), true, nil
			}
			return toolResult{}, false, err
		}
		summary, err := compactProgress(version, state)
		if err != nil {
			return failResult(&ToolError{Code: "COLLECTION_PROGRESS_INVALID"}), true, nil
		}
		return toolResult{OK: true, Value: summary}, false, nil
	case "read_collection_operation":
		input, err := decodeInput[struct {
			OperationID string `json:"operation_id"`
		}](b.Input)
		if err != nil {
			return failResult(err), true, nil
		}
		if p.Kind != Collection || p.Bridge == nil || e.Servicing == nil || !knownOperation(p.Bridge, input.OperationID) {
			return failResult(&ToolError{Code: "OPERATION_HANDLE_NOT_IN_CASE"}), true, nil
		}
		version, _, err := e.Repo.ReadCollectionState(ctx, p.CaseID, lease)
		if err != nil {
			if errors.Is(err, domain.ErrCollectionRefreshPending) {
				return failResult(&ToolError{Code: "COLLECTION_REFRESH_PENDING"}), true, nil
			}
			return toolResult{}, false, err
		}
		if version != p.Bridge.Version {
			return failResult(&ToolError{Code: "COLLECTION_VERSION_STALE"}), true, nil
		}
		op, status, err := e.Servicing.ReadOperation(ctx, input.OperationID)
		if err != nil {
			return failResult(&ToolError{Code: sourceToolError(err, status)}), true, nil
		}
		return toolResult{OK: true, Value: op}, false, nil
	case "record_collection_recommendation":
		return e.recordRecommendation(ctx, p, lease, b)
	case "finish_investigation":
		input, err := decodeInput[Finish](b.Input)
		if err != nil {
			return failResult(err), true, nil
		}
		ids, err := e.Repo.EvidenceIDsForRun(ctx, p.CaseID, lease, p.RunID)
		if err != nil {
			return toolResult{}, false, err
		}
		if err := validateFinish(p, input, ids); err != nil {
			return failResult(err), true, nil
		}
		return toolResult{OK: true, Value: input}, false, nil
	default:
		return failResult(&ToolError{Code: "TOOL_NOT_ALLOWED"}), true, nil
	}
}

func (e *Engine) submitEvidence(ctx context.Context, p Packet, lease string, b ContentBlock, extractedAt time.Time) (toolResult, bool, error) {
	input, err := decodeInput[evidenceInput](b.Input)
	if err != nil {
		return failResult(err), true, nil
	}
	if p.SourceReader == nil || !contains(p.AllowedClaimFields, input.ClaimField) || len(input.ClaimValue) > 256 || len(input.ClaimField) > 64 || len(input.Excerpt) == 0 || len(input.Excerpt) > 1000 || len(input.Conflicts) > 5 {
		return failResult(&ToolError{Code: "CLAIM_SCHEMA_INVALID"}), true, nil
	}
	section, err := p.SourceReader.Read(input.Handle, input.Section)
	if err != nil {
		return failResult(&ToolError{Code: "SOURCE_UNAVAILABLE"}), true, nil
	}
	if !strings.Contains(section.Text, input.Excerpt) {
		return failResult(&ToolError{Code: "EXCERPT_NOT_IN_SOURCE"}), true, nil
	}
	date, err := parseClaimDate(input.ClaimDate)
	if err != nil {
		return failResult(err), true, nil
	}
	hash, err := hex.DecodeString(section.OriginalSHA256)
	if err != nil {
		return failResult(&ToolError{Code: "SOURCE_HASH_INVALID"}), true, nil
	}
	visibleHash, err := hex.DecodeString(section.VisibleSHA256)
	if err != nil {
		return failResult(&ToolError{Code: "SOURCE_HASH_INVALID"}), true, nil
	}
	conflicts, _ := json.Marshal(input.Conflicts)
	id, err := randomID()
	if err != nil {
		return toolResult{}, false, err
	}
	evidence := postgres.EvidenceInput{ID: id, CaseID: p.CaseID, RunID: p.RunID, SourceBindingID: p.SourceBindingID, SourceHandle: section.Handle, SourceType: section.SourceType, SourceRecordID: section.RecordID, SourceVersion: section.SourceVersion, SourceExportVersion: section.ExportVersion, SourceHash: hash, VisibleSourceHash: visibleHash, SectionID: section.SectionID, Locator: section.Locator, Excerpt: input.Excerpt, ClaimField: input.ClaimField, ClaimValue: input.ClaimValue, ClaimUnit: input.ClaimUnit, ClaimDate: date, Uncertainty: input.Uncertainty, ToolCallID: b.ID, ExtractedAt: extractedAt.UTC(), ModelVersion: p.ModelVersion, PromptVersion: p.PromptVersion, Conflicts: conflicts}
	evidence.CanonicalHash, err = postgres.CanonicalEvidenceHash(evidence)
	if err != nil {
		return toolResult{}, false, err
	}
	stored, err := e.Repo.RecordEvidence(ctx, evidence, lease)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrBudgetExhausted) {
			return failResult(&ToolError{Code: "EVIDENCE_SUBMISSION_REJECTED"}), true, nil
		}
		return toolResult{}, false, err
	}
	return toolResult{OK: true, Value: map[string]any{"evidence_id": stored, "state": "PENDING_REVIEW", "financially_verified": false}}, false, nil
}

func (e *Engine) recordRecommendation(ctx context.Context, p Packet, lease string, b ContentBlock) (toolResult, bool, error) {
	input, err := decodeInput[recommendationInput](b.Input)
	if err != nil {
		return failResult(err), true, nil
	}
	if p.Kind != Collection || p.Bridge == nil || !allowedNextStep(p, input.ActionCode) || len(input.Reason) == 0 || len(input.Reason) > 2000 || len(input.Citations) > 10 {
		return failResult(&ToolError{Code: "RECOMMENDATION_INVALID"}), true, nil
	}
	ids, err := e.Repo.EvidenceIDsForRun(ctx, p.CaseID, lease, p.RunID)
	if err != nil {
		return toolResult{}, false, err
	}
	for _, citation := range input.Citations {
		if !ids[citation] && !knownOperation(p.Bridge, citation) && citation != p.Bridge.ID {
			return failResult(&ToolError{Code: "RECOMMENDATION_CITATION_INVALID"}), true, nil
		}
	}
	refs, _ := json.Marshal(input.Citations)
	id, err := e.Repo.RecordRecommendation(ctx, lease, postgres.RecommendationInput{CaseID: p.CaseID, RunID: p.RunID, ToolCallID: b.ID, BridgeVersion: p.Bridge.Version, ActionCode: input.ActionCode, Reason: input.Reason, Citations: refs})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrBudgetExhausted) {
			return failResult(&ToolError{Code: "RECOMMENDATION_VERSION_OR_ACTION_STALE"}), true, nil
		}
		return toolResult{}, false, err
	}
	return toolResult{OK: true, Value: map[string]any{"recommendation_id": id, "action_code": input.ActionCode, "bridge_version": p.Bridge.Version, "executed": false}}, false, nil
}

func (e *Engine) limit(ctx context.Context, p Packet, lease, code string) (StepResult, error) {
	if err := e.persistRunDisposition(ctx, p, lease, RunDisposition{Kind: "LIMITATION", Limitation: code}); err != nil {
		return StepResult{}, err
	}
	return StepResult{Finished: true, Limitation: code}, nil
}

func (e *Engine) persistRunDisposition(ctx context.Context, p Packet, lease string, disposition RunDisposition) error {
	encoded, err := json.Marshal(disposition)
	if err != nil {
		return errors.New("agent_disposition_encode_failed")
	}
	bridgeID := ""
	var bridgeVersion *int64
	if p.Kind == Collection {
		if p.Bridge == nil {
			return &ToolError{Code: "COLLECTION_PACKET_SCOPE_INVALID"}
		}
		bridgeID = p.Bridge.ID
		version := p.Bridge.Version
		bridgeVersion = &version
	}
	return e.Repo.FinishRun(ctx, p.CaseID, lease, p.RunID, encoded, bridgeID, bridgeVersion)
}

func buildHistory(attempts []postgres.Attempt, initial Message, kind Kind, model string) ([]Message, int, *Finish, *ContentBlock, error) {
	messages := []Message{initial}
	endTurns := 0
	for i := 0; i < len(attempts); i++ {
		a := attempts[i]
		if a.Kind != "MODEL" {
			continue
		}
		if a.Outcome == "FAILED" {
			if _, ok := decodeProviderFailure(a.ResultBody); !ok {
				return nil, endTurns, nil, nil, &ToolError{Code: "MODEL_FAILURE_HISTORY_INVALID"}
			}
			continue
		}
		if a.Outcome == "DISPATCHED" {
			return nil, endTurns, nil, nil, &ToolError{Code: "MODEL_ATTEMPT_UNRESOLVED"}
		}
		if a.Outcome != "COMPLETED" {
			return nil, endTurns, nil, nil, &ToolError{Code: "UNRESOLVED_MODEL_ATTEMPT"}
		}
		request, err := decodeRequest(a.RequestBody)
		if err != nil || validateStoredRequest(request, kind, model) != nil {
			return nil, endTurns, nil, nil, &ToolError{Code: "MODEL_REQUEST_HISTORY_INVALID"}
		}
		var response Response
		if json.Unmarshal(a.ResultBody, &response) != nil {
			return nil, endTurns, nil, nil, &ToolError{Code: "MODEL_HISTORY_INVALID"}
		}
		block, err := validateModelResponse(request, response, kind, model)
		if err != nil {
			return nil, endTurns, nil, nil, err
		}
		content, _ := json.Marshal(response.Content)
		messages = append(messages, Message{Role: "assistant", Content: content})
		switch response.StopReason {
		case "tool_use":
			var found *postgres.Attempt
			for j := i + 1; j < len(attempts); j++ {
				if attempts[j].Kind == "TOOL" && attempts[j].AttemptID == block.ID {
					found = &attempts[j]
					break
				}
			}
			if found == nil {
				return messages, endTurns, nil, block, nil
			}
			if found.CallID == nil || *found.CallID != block.ID || !bytes.Equal(found.RequestBody, block.Input) {
				return nil, endTurns, nil, nil, &ToolError{Code: "TOOL_REQUEST_HISTORY_MISMATCH"}
			}
			if found.Outcome == "DISPATCHED" {
				return messages, endTurns, nil, block, nil
			}
			if found.Outcome != "COMPLETED" && found.Outcome != "FAILED" {
				return nil, endTurns, nil, nil, &ToolError{Code: "TOOL_HISTORY_INVALID"}
			}
			if block.Name == "finish_investigation" && found.Outcome == "COMPLETED" {
				var wrapped toolResult
				var finish Finish
				if json.Unmarshal(found.ResultBody, &wrapped) == nil && wrapped.OK {
					if body, err := json.Marshal(wrapped.Value); err == nil && json.Unmarshal(body, &finish) == nil {
						return messages, endTurns, &finish, nil, nil
					}
				}
				return nil, endTurns, nil, nil, &ToolError{Code: "FINISH_RESULT_INVALID"}
			}
			toolMsg, err := toolResultMessage(block.ID, found.ResultBody, found.Outcome == "FAILED")
			if err != nil {
				return nil, endTurns, nil, nil, err
			}
			messages = append(messages, toolMsg)
			i = findSequenceIndex(attempts, found.Sequence)
		case "end_turn":
			endTurns++
			if endTurns > 1 {
				return nil, endTurns, nil, nil, &ToolError{Code: "CORRECTIVE_TURN_EXHAUSTED"}
			}
			correction, _ := json.Marshal([]map[string]string{{"type": "text", "text": "Continue with case-scoped tools. Prose does not complete the investigation; use finish_investigation."}})
			messages = append(messages, Message{Role: "user", Content: correction})
		default:
			return nil, endTurns, nil, nil, &ToolError{Code: "MODEL_HISTORY_INCOMPLETE"}
		}
	}
	return messages, endTurns, nil, nil, nil
}

func buildRequest(p Packet, messages []Message, forceFinish bool) (Request, []byte, error) {
	req := Request{Model: p.ModelVersion, System: systemPromptVersioned, Messages: messages, Tools: definitions(p.Kind), DisableParallel: true, MaxTokens: NormalMaxTokens, ToolChoice: map[string]any{"type": "auto", "disable_parallel_tool_use": true}}
	if forceFinish {
		req.MaxTokens = FinishMaxTokens
		req.ToolChoice = map[string]any{"type": "tool", "name": "finish_investigation", "disable_parallel_tool_use": true}
	}
	body, err := json.Marshal(req)
	return req, body, err
}
func toolResultMessage(id string, body []byte, isError bool) (Message, error) {
	block, _ := json.Marshal(toolResultBlock{Type: "tool_result", ToolUseID: id, Content: string(body), IsError: isError})
	content, err := json.Marshal([]json.RawMessage{block})
	if err != nil {
		return Message{}, err
	}
	return Message{Role: "user", Content: content}, nil
}
func modelCallTime(attempts []postgres.Attempt, toolCallID string) (time.Time, bool) {
	for _, attempt := range attempts {
		if attempt.Kind != "MODEL" || attempt.Outcome != "COMPLETED" || attempt.CreatedAt.IsZero() {
			continue
		}
		var response Response
		if json.Unmarshal(attempt.ResultBody, &response) != nil {
			continue
		}
		for _, raw := range response.Content {
			block, err := toolBlock(raw)
			if err == nil && block.ID == toolCallID {
				return attempt.CreatedAt, true
			}
		}
	}
	return time.Time{}, false
}

func findSequenceIndex(attempts []postgres.Attempt, sequence int) int {
	for i, a := range attempts {
		if a.Sequence == sequence {
			return i
		}
	}
	return -1
}
func countKind(attempts []postgres.Attempt, kind string) int {
	n := 0
	for _, a := range attempts {
		if a.Kind == kind {
			n++
		}
	}
	return n
}
func completedOutcome(isError bool) string {
	if isError {
		return "FAILED"
	}
	return "COMPLETED"
}
func failResult(err error) toolResult {
	var te *ToolError
	if errors.As(err, &te) {
		return toolResult{OK: false, ErrorCode: te.Code}
	}
	return toolResult{OK: false, ErrorCode: "TOOL_FAILED"}
}
func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
func knownOperation(b *servicing.Bridge, id string) bool {
	if b == nil {
		return false
	}
	for _, op := range b.Operations {
		if op.ID == id {
			return true
		}
	}
	return false
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("agent_id_generation_failed")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(b[:])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:], nil
}
func decodeRequest(raw []byte) (Request, error) {
	var r Request
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&r) != nil || dec.Decode(new(any)) != io.EOF || r.Model == "" || r.MaxTokens <= 0 {
		return Request{}, errors.New("stored_model_request_invalid")
	}
	return r, nil
}

func validateStoredRequest(request Request, kind Kind, model string) error {
	if request.Model != model || request.System != systemPromptVersioned || (request.MaxTokens != NormalMaxTokens && request.MaxTokens != FinishMaxTokens) || len(request.Tools) != len(definitions(kind)) || len(request.Messages) == 0 {
		return &ToolError{Code: "MODEL_REQUEST_INVALID"}
	}
	for i, expected := range definitions(kind) {
		got := request.Tools[i]
		if got.Name != expected.Name || got.Description != expected.Description || !bytes.Equal(got.InputSchema, expected.InputSchema) {
			return &ToolError{Code: "MODEL_TOOL_REGISTRY_INVALID"}
		}
	}
	choice, ok := request.ToolChoice.(map[string]any)
	if !ok {
		return &ToolError{Code: "MODEL_TOOL_CHOICE_INVALID"}
	}
	typ, _ := choice["type"].(string)
	disableParallel, _ := choice["disable_parallel_tool_use"].(bool)
	if !disableParallel {
		return &ToolError{Code: "MODEL_PARALLEL_TOOLS_NOT_DISABLED"}
	}
	switch typ {
	case "auto":
		if len(choice) != 2 || request.MaxTokens != NormalMaxTokens {
			return &ToolError{Code: "MODEL_TOOL_CHOICE_INVALID"}
		}
	case "tool":
		name, _ := choice["name"].(string)
		if len(choice) != 3 || name != "finish_investigation" || request.MaxTokens != FinishMaxTokens {
			return &ToolError{Code: "MODEL_TOOL_CHOICE_INVALID"}
		}
	default:
		return &ToolError{Code: "MODEL_TOOL_CHOICE_INVALID"}
	}
	for i, message := range request.Messages {
		if (i == 0 && message.Role != "user") || (message.Role != "user" && message.Role != "assistant") || !json.Valid(message.Content) {
			return &ToolError{Code: "MODEL_MESSAGE_HISTORY_INVALID"}
		}
	}
	return nil
}

func validateModelResponse(request Request, response Response, kind Kind, model string) (*ContentBlock, error) {
	if err := validateStoredRequest(request, kind, model); err != nil {
		return nil, err
	}
	if response.Type != "message" || response.Role != "assistant" || response.ID == "" || response.Model == "" || len(response.Content) == 0 || response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 {
		return nil, &ToolError{Code: "MODEL_RESPONSE_INVALID"}
	}
	var tool *ContentBlock
	for _, raw := range response.Content {
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &header) != nil {
			return nil, &ToolError{Code: "MODEL_BLOCK_INVALID"}
		}
		switch header.Type {
		case "text":
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil {
				return nil, &ToolError{Code: "MODEL_BLOCK_INVALID"}
			}
			if _, ok := fields["text"]; !ok {
				return nil, &ToolError{Code: "MODEL_BLOCK_INVALID"}
			}
			var block struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&block) != nil || decoder.Decode(new(any)) != io.EOF || block.Type != "text" {
				return nil, &ToolError{Code: "MODEL_BLOCK_INVALID"}
			}
		case "tool_use":
			block, err := toolBlock(raw)
			if err != nil || tool != nil {
				return nil, &ToolError{Code: "MODEL_TOOL_BLOCK_INVALID"}
			}
			var input map[string]json.RawMessage
			if json.Unmarshal(block.Input, &input) != nil || input == nil {
				return nil, &ToolError{Code: "MODEL_TOOL_INPUT_INVALID"}
			}
			tool = &block
		default:
			// Reject thinking and server-tool blocks before persisting their content.
			return nil, &ToolError{Code: "MODEL_BLOCK_TYPE_FORBIDDEN"}
		}
	}
	forced := requestForcesFinish(request)
	switch response.StopReason {
	case "tool_use":
		if tool == nil || forced && tool.Name != "finish_investigation" {
			return nil, &ToolError{Code: "MODEL_TOOL_CHOICE_VIOLATED"}
		}
	case "end_turn":
		if tool != nil || forced {
			return nil, &ToolError{Code: "MODEL_END_TURN_INVALID"}
		}
	default:
		return nil, &ToolError{Code: "MODEL_STOP_REASON_INVALID"}
	}
	return tool, nil
}

func requestForcesFinish(r Request) bool {
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	b, _ := json.Marshal(r.ToolChoice)
	_ = json.Unmarshal(b, &choice)
	return choice.Type == "tool" && choice.Name == "finish_investigation"
}

func decodeProviderFailure(raw []byte) (providerFailureRecord, bool) {
	var failure providerFailureRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&failure) != nil || decoder.Decode(new(any)) != io.EOF || failure.Code == "" {
		return providerFailureRecord{}, false
	}
	switch failure.Kind {
	case ProviderAmbiguous, ProviderTransient, ProviderPermanent:
		return failure, true
	default:
		return providerFailureRecord{}, false
	}
}

func countProviderAttempts(attempts []postgres.Attempt) int {
	count := 0
	for _, attempt := range attempts {
		if attempt.Kind != "MODEL" || attempt.Outcome != "FAILED" {
			continue
		}
		failure, ok := decodeProviderFailure(attempt.ResultBody)
		if ok && failure.Kind != ProviderPermanent {
			count++
		}
	}
	return count
}

func (e *Engine) providerRetryAt(attempts []postgres.Attempt, failure *ProviderError) *time.Time {
	if failure.Kind == ProviderPermanent {
		return nil
	}
	wait := failure.RetryAfter
	if failure.Kind == ProviderTransient {
		backoff := 5 * time.Second
		if countProviderAttempts(attempts) > 0 {
			backoff = 20 * time.Second
		}
		if wait < backoff {
			wait = backoff
		}
	}
	retryAt := e.now().Add(wait)
	return &retryAt
}
func sourceToolError(err error, status int) string {
	if status == 404 {
		return "OPERATION_NOT_FOUND"
	}
	var permanent interface{ Permanent() bool }
	if errors.As(err, &permanent) && permanent.Permanent() {
		return "OPERATION_CONTRACT_INVALID"
	}
	return "OPERATION_UNAVAILABLE"
}
func (e *Engine) wouldOverflowNextTurn(p Packet, attempts []postgres.Attempt, nextResult []byte, isError bool) bool {
	packet, err := buildPacket(p)
	if err != nil {
		return true
	}
	initial, err := packetMessage(packet)
	if err != nil {
		return true
	}
	messages, _, _, pending, err := buildHistory(attempts, initial, p.Kind, p.ModelVersion)
	if err != nil || pending == nil || len(attempts) == 0 {
		return true
	}
	toolMessage, err := toolResultMessage(pending.ID, nextResult, isError)
	if err != nil {
		return true
	}
	messages = append(messages, toolMessage)
	request, body, err := buildRequest(p, messages, true)
	_ = request
	return err != nil || len(body)+FinishReserveBytes > MaxRequestBytes
}
func decodeDisposition(raw []byte) (StepResult, error) {
	var result RunDisposition
	if json.Unmarshal(raw, &result) != nil {
		return StepResult{}, errors.New("run_disposition_invalid")
	}
	switch result.Kind {
	case "FINISH":
		if result.Finish == nil {
			return StepResult{}, errors.New("run_disposition_invalid")
		}
		return StepResult{Finished: true, Finish: result.Finish}, nil
	case "LIMITATION":
		return StepResult{Finished: true, Limitation: result.Limitation}, nil
	case "SUPERSEDED":
		return StepResult{Finished: true, Limitation: "RUN_SUPERSEDED"}, nil
	default:
		return StepResult{}, errors.New("run_disposition_invalid")
	}
}
func safeErrorCode(err error) string {
	var te *ToolError
	if errors.As(err, &te) {
		return te.Code
	}
	return "AGENT_FAILURE"
}
