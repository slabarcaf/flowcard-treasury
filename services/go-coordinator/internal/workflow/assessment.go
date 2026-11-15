package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agent "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/agent"
	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/shortfall"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/sources"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/underwriting"
)

const (
	assessmentRevision = 1
	assessmentTermDays = 30
)

type evaluationFacts struct {
	CustomerID               string   `json:"customer_id"`
	AccountRef               string   `json:"account_ref"`
	PrincipalCents           *int64   `json:"principal_cents"`
	TermDays                 int      `json:"term_days"`
	EstimatedFeeCents        *int64   `json:"estimated_fee_cents"`
	TotalDueCents            *int64   `json:"total_due_cents"`
	CustomerLimitCents       *int64   `json:"customer_limit_cents"`
	ExistingExposureCents    *int64   `json:"existing_exposure_cents"`
	PostFundingExposureCents *int64   `json:"post_funding_exposure_cents"`
	AvailableCapitalCents    *int64   `json:"available_capital_cents"`
	ReasonCodes              []string `json:"reason_codes"`
}

func (e *StageExecutor) evaluate(ctx context.Context, c domain.Case, token string) (StepResult, error) {
	if c.Kind != domain.CaseShortfall {
		return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE"}, nil
	}
	if c.DetectionID == nil || c.CustomerID == nil || c.AccountRef == nil || c.RawAccountID == nil || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
		return StepResult{Status: "HELD", NextStage: "EVALUATE", HeldReason: stringPtr("SHORTFALL_SCOPE_INVALID")}, nil
	}
	linkage := underwriting.Linkage{ShortfallID: *c.DetectionID, CustomerID: *c.CustomerID, AccountID: *c.RawAccountID, SourceMode: e.Mode, TermDays: assessmentTermDays}
	assessment, row, _, err := e.savedEvaluation(ctx, c, token, linkage)
	if err != nil {
		return StepResult{}, err
	}
	if assessment == nil {
		return StepResult{Status: "HELD", NextStage: "EVALUATE", HeldReason: stringPtr("UNDERWRITING_RESULT_UNAVAILABLE")}, nil
	}
	runID, err := domain.StableRunID(c.ID, assessmentRevision)
	if err != nil {
		return StepResult{}, err
	}
	refs, err := e.Repo.ReadRunContextRefs(ctx, c.ID, token, runID)
	if err == nil {
		privateRefs, privateErr := e.Repo.ReadRunPrivateContextRefs(ctx, c.ID, token, runID)
		if errors.Is(privateErr, domain.ErrNotFound) {
			return e.completeRuleTemplate(ctx, c, token, runID, row, *assessment, "PRIVATE_RUN_SCOPE_UNAVAILABLE")
		}
		if privateErr != nil {
			return StepResult{}, privateErr
		}
		return e.resumeOrFallback(ctx, c, token, runID, refs, privateRefs, *assessment, row)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return StepResult{}, err
	}
	packet, limitation := e.buildShortfallPacket(ctx, c, runID, *assessment, row)
	refs, err = agent.RunContextRefs(packet)
	if limitation != "" || err != nil {
		if limitation == "" {
			limitation = "PACKET_INVALID"
		}
		packet, err = e.ruleTemplatePacket(c, runID, *assessment, row)
		if err != nil {
			return StepResult{}, err
		}
		refs, err = agent.RunContextRefs(packet)
		if err != nil {
			return StepResult{}, errors.New("rule_template_packet_invalid")
		}
	}
	privateRefs, err := shortfallPrivateContextRefs(c, runID, packet, *assessment, row, limitation)
	if err != nil {
		return StepResult{}, err
	}
	if _, err := e.Repo.StartRun(ctx, c.ID, token, runID, assessmentRevision, "INVESTIGATE", packet.PromptVersion, packet.ModelVersion, refs, privateRefs); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return StepResult{}, err
		}
		return StepResult{}, err
	}
	if limitation != "" {
		return e.completeRuleTemplate(ctx, c, token, runID, row, *assessment, limitation)
	}
	return StepResult{Status: "INVESTIGATING", NextStage: "INVESTIGATE"}, nil
}

func (e *StageExecutor) resumeOrFallback(ctx context.Context, c domain.Case, token, runID string, refs, privateRefs []byte, evaluation underwriting.Evaluation, row domain.AssessmentRequest) (StepResult, error) {
	_, limitation := e.restorePinnedShortfallPacket(c, runID, refs, privateRefs, evaluation, row)
	if limitation != "" {
		return e.completeRuleTemplate(ctx, c, token, runID, row, evaluation, limitation)
	}
	return StepResult{Status: "INVESTIGATING", NextStage: "INVESTIGATE"}, nil
}

func (e *StageExecutor) restorePinnedShortfallPacket(c domain.Case, runID string, refs, privateRefs []byte, evaluation underwriting.Evaluation, row domain.AssessmentRequest) (agent.Packet, string) {
	privateContext, originalContext, err := validatePrivateShortfallContext(privateRefs, c, runID, e.Mode, evaluation, row)
	if err != nil {
		return agent.Packet{}, "PRIVATE_RUN_SCOPE_INVALID"
	}
	if privateContext.TemplateLimitation != "" {
		packet, err := agent.PacketFromRunContextRefs(refs, nil)
		if err != nil || packet.CaseID != c.ID || packet.SourceBindingID != c.BindingID || packet.RunID != runID || packet.Kind != agent.Shortfall || packet.Mode != e.Mode || packet.CustomerID != *c.CustomerID || packet.AccountRef != *c.AccountRef || packet.AgentMode != "RULE_TEMPLATE" {
			return agent.Packet{}, "PRIVATE_RUN_SCOPE_INVALID"
		}
		return agent.Packet{}, privateContext.TemplateLimitation
	}
	reader, err := e.loadSourceReader(c, valueOrEmpty(row.SourceCardID))
	if err != nil {
		return agent.Packet{}, "SOURCE_MANIFEST_UNAVAILABLE"
	}
	var projectedContext *customercontext.Response
	if originalContext != nil {
		contextValue, err := contextForAgent(*originalContext, *c.CustomerID, *c.RawAccountID, *row.SourceCardID, evaluation.TransactionID, *c.AccountRef)
		if err != nil {
			return agent.Packet{}, "PINNED_CONTEXT_UNAVAILABLE"
		}
		projectedContext = &contextValue
	}
	packet, err := agent.PacketFromRunContextRefsWithContext(refs, reader, projectedContext)
	if err != nil || packet.CaseID != c.ID || packet.SourceBindingID != c.BindingID || packet.RunID != runID || packet.Kind != agent.Shortfall || packet.Mode != e.Mode || packet.CustomerID != *c.CustomerID || packet.AccountRef != *c.AccountRef || packet.ShortfallID != *c.DetectionID || packet.Assessment == nil || packet.Assessment.ID != evaluation.ID {
		return agent.Packet{}, "PINNED_AGENT_PACKET_UNAVAILABLE"
	}
	if packet.AgentMode == "RULE_TEMPLATE" || privateContext.TemplateLimitation != "" {
		if privateContext.TemplateLimitation != "" {
			return agent.Packet{}, privateContext.TemplateLimitation
		}
		return agent.Packet{}, "RULE_TEMPLATE_RECOVERY"
	}
	if originalContext != nil {
		packet.PrivateContextData = append(json.RawMessage(nil), privateContext.CustomerContext...)
	} else if packet.Context != nil {
		return agent.Packet{}, "PRIVATE_CONTEXT_MISSING"
	}
	rebuilt, err := agent.RunContextRefs(packet)
	if err != nil || !sameJSON(rebuilt, refs) {
		return agent.Packet{}, "PINNED_CONTEXT_PROJECTION_MISMATCH"
	}
	return packet, ""
}

func (e *StageExecutor) savedEvaluation(ctx context.Context, c domain.Case, token string, linkage underwriting.Linkage) (*underwriting.Evaluation, domain.AssessmentRequest, *shortfall.Record, error) {
	row, err := e.Repo.ReadAssessment(ctx, c.ID, assessmentRevision)
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrNotFound):
		row = domain.AssessmentRequest{}
	case err != nil:
		return nil, domain.AssessmentRequest{}, nil, err
	}

	key := assessmentIdempotencyKey(c.ID, assessmentRevision)
	body, err := underwriting.EvaluationRequestBody(*c.DetectionID, assessmentTermDays)
	if err != nil {
		return nil, row, nil, err
	}
	if row.SavedAssessmentID != nil {
		if len(row.Response) != 0 {
			evaluation, err := underwriting.DecodeEvaluation(row.Response)
			if err == nil && row.SourceTransactionID != nil {
				linkage.TransactionID = *row.SourceTransactionID
			} else if err == nil {
				linkage.TransactionID = evaluation.TransactionID
			}
			if err != nil || underwriting.ValidateEvaluation(evaluation, linkage) != nil || evaluation.ID != *row.SavedAssessmentID {
				return nil, row, nil, &underwriting.ContractError{Code: "SAVED_ASSESSMENT_INVALID"}
			}
			return &evaluation, row, nil, nil
		}
		if e.Underwriting == nil {
			return nil, row, nil, errors.New("underwriting_client_unavailable_for_saved_id")
		}
		if row.SourceTransactionID != nil {
			linkage.TransactionID = *row.SourceTransactionID
		}
		evaluation, response, _, err := e.Underwriting.Read(ctx, *row.SavedAssessmentID, linkage)
		if err != nil {
			return nil, row, nil, err
		}
		if err := e.Repo.SaveAssessmentResult(ctx, c.ID, token, row.ID, evaluation.ID, response); err != nil {
			return nil, row, nil, err
		}
		row.SavedAssessmentID = &evaluation.ID
		row.Response = response
		return &evaluation, row, nil, nil
	}
	if row.ID != "" {
		if row.IdempotencyKey != key || !bytes.Equal(row.Body, body) || row.SourceTransactionID == nil || strings.TrimSpace(*row.SourceTransactionID) == "" || row.SourceCardID == nil || strings.TrimSpace(*row.SourceCardID) == "" {
			return nil, row, nil, &underwriting.ContractError{Code: "PREPARED_ASSESSMENT_LINKAGE_INCOMPLETE"}
		}
		if e.Underwriting == nil {
			return nil, row, nil, &underwriting.ContractError{Code: "ASSESSMENT_CLIENT_UNAVAILABLE"}
		}
		linkage.TransactionID = *row.SourceTransactionID
		evaluation, response, _, err := e.Underwriting.EvaluateBody(ctx, row.Body, row.IdempotencyKey, linkage)
		if err != nil {
			return nil, row, nil, err
		}
		if err := e.Repo.SaveAssessmentResult(ctx, c.ID, token, row.ID, evaluation.ID, response); err != nil {
			return nil, row, nil, err
		}
		row.SavedAssessmentID = &evaluation.ID
		row.Response = response
		return &evaluation, row, nil, nil
	}
	if e.Shortfall == nil || e.Underwriting == nil {
		return nil, row, nil, &underwriting.ContractError{Code: "ASSESSMENT_CLIENT_UNAVAILABLE"}
	}
	record, status, err := e.Shortfall.Read(ctx, *c.DetectionID)
	if err != nil {
		if status == 404 {
			return nil, row, nil, &shortfall.ProtocolError{Code: "shortfall_detail_not_found"}
		}
		return nil, row, nil, err
	}
	if record.ID != *c.DetectionID || c.CustomerID == nil || c.AccountRef == nil || c.RawAccountID == nil || record.CustomerID == nil || *record.CustomerID != *c.CustomerID || record.AccountID != *c.RawAccountID || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
		return nil, row, &record, &shortfall.ProtocolError{Code: "shortfall_scope_mismatch"}
	}
	if reason := shortfall.HoldReason(record); reason != "" {
		return nil, row, &record, &shortfall.ProtocolError{Code: reason}
	}
	linkage.TransactionID = valueOrEmpty(record.TransactionID)
	if linkage.TransactionID == "" {
		return nil, row, &record, &shortfall.ProtocolError{Code: "SOURCE_TRANSACTION_ID_MISSING"}
	}
	if record.CardID == nil || strings.TrimSpace(*record.CardID) == "" {
		return nil, row, &record, &shortfall.ProtocolError{Code: "SOURCE_CARD_ID_MISSING"}
	}
	row.ID, err = e.Repo.PrepareAssessment(ctx, c.ID, token, assessmentRevision, key, linkage.TransactionID, *record.CardID, body)
	if err != nil {
		return nil, row, &record, err
	}
	row, err = e.Repo.ReadAssessment(ctx, c.ID, assessmentRevision)
	if err != nil {
		return nil, domain.AssessmentRequest{}, &record, err
	}
	if row.IdempotencyKey != key || !bytes.Equal(row.Body, body) || row.SourceTransactionID == nil || *row.SourceTransactionID != linkage.TransactionID || row.SourceCardID == nil || *row.SourceCardID != *record.CardID {
		return nil, row, &record, domain.ErrConflict
	}
	evaluation, response, _, err := e.Underwriting.EvaluateBody(ctx, row.Body, row.IdempotencyKey, linkage)
	if err != nil {
		return nil, row, &record, err
	}
	if err := e.Repo.SaveAssessmentResult(ctx, c.ID, token, row.ID, evaluation.ID, response); err != nil {
		return nil, row, &record, err
	}
	row.SavedAssessmentID = &evaluation.ID
	row.Response = response
	return &evaluation, row, &record, nil
}

func assessmentIdempotencyKey(caseID string, revision int) string {
	return fmt.Sprintf("coordinator:%s:r%d", caseID, revision)
}

func (e *StageExecutor) investigate(ctx context.Context, c domain.Case, token string) (StepResult, error) {
	if c.Kind == domain.CaseCollection {
		return e.investigateCollection(ctx, c, token)
	}
	return e.investigateShortfall(ctx, c, token)
}

type collectionObservation struct {
	Bridge          servicing.Bridge  `json:"bridge"`
	AllowedActions  []string          `json:"allowed_next_actions"`
	HistoryComplete bool              `json:"history_complete"`
	Attempts        []json.RawMessage `json:"attempts"`
	NextCursor      *string           `json:"next_cursor"`
	DebtStale       bool              `json:"debt_stale"`
	DebtAsOf        *time.Time        `json:"debt_as_of"`
}

func (e *StageExecutor) investigateCollection(ctx context.Context, c domain.Case, token string) (StepResult, error) {
	if c.BridgeID == nil || c.PendingTrigger == nil || c.PendingTriggerVersion == nil {
		return StepResult{Status: string(c.Status), NextStage: "MONITORING"}, nil
	}
	if c.RefreshNeeded || c.BridgeVersion == nil || *c.BridgeVersion != *c.PendingTriggerVersion {
		bridgeID, runID, _, err := e.Repo.LatestCollectionAdmission(ctx, c.ID, token)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE"}, nil
			}
			return StepResult{}, err
		}
		disposition := marshalDisposition(agent.RunDisposition{Kind: "SUPERSEDED", Limitation: "NEWER_SOURCE_REFRESH_PENDING"})
		if err := e.Repo.SupersedeCollectionRun(ctx, c.ID, token, runID, bridgeID, c.TargetVersion, disposition); err != nil {
			return StepResult{}, err
		}
		return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE", DispositionCommitted: true}, nil
	}
	version, state, err := e.Repo.ReadCollectionState(ctx, c.ID, token)
	if errors.Is(err, domain.ErrCollectionRefreshPending) {
		bridgeID, runID, _, latestErr := e.Repo.LatestCollectionAdmission(ctx, c.ID, token)
		if latestErr != nil {
			if errors.Is(latestErr, domain.ErrNotFound) {
				return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE"}, nil
			}
			return StepResult{}, latestErr
		}
		disposition := marshalDisposition(agent.RunDisposition{Kind: "SUPERSEDED", Limitation: "NEWER_SOURCE_REFRESH_PENDING"})
		if err := e.Repo.SupersedeCollectionRun(ctx, c.ID, token, runID, bridgeID, c.TargetVersion, disposition); err != nil {
			return StepResult{}, err
		}
		return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE", DispositionCommitted: true}, nil
	}
	if err != nil {
		return StepResult{}, err
	}
	var observation collectionObservation
	if json.Unmarshal(state, &observation) != nil || observation.Bridge.ID != *c.BridgeID || observation.Bridge.Version != version || version != *c.PendingTriggerVersion {
		return StepResult{}, &servicing.ContractError{Code: "collection_observation_invalid"}
	}
	plan, err := e.Repo.CollectionAdmission(ctx, c.ID, token, observation.Bridge.ID, *c.PendingTrigger, version)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE"}, nil
		}
		return StepResult{}, err
	}
	if plan.Skipped {
		return StepResult{Status: string(c.Status), NextStage: "MONITORING"}, nil
	}
	var packet agent.Packet
	var runID string
	if plan.RunID != "" {
		runID = plan.RunID
		refs, err := e.Repo.ReadRunContextRefs(ctx, c.ID, token, runID)
		if err != nil {
			return StepResult{}, err
		}
		privateRefs, err := e.Repo.ReadRunPrivateContextRefs(ctx, c.ID, token, runID)
		if errors.Is(err, domain.ErrNotFound) {
			return e.completeCollectionTemplate(ctx, c, token, runID, observation, nil, "PRIVATE_RUN_SCOPE_UNAVAILABLE")
		}
		if err != nil {
			return StepResult{}, err
		}
		privateContext, err := validatePrivateCollectionContext(privateRefs, c, runID, e.Mode, observation.Bridge.ID, version, observation.AllowedActions)
		if err != nil {
			return e.completeCollectionTemplate(ctx, c, token, runID, observation, nil, "PRIVATE_RUN_SCOPE_INVALID")
		}
		if privateContext.TemplateLimitation != "" {
			return e.completeCollectionTemplate(ctx, c, token, runID, observation, nil, privateContext.TemplateLimitation)
		}
		reader, sourceErr := e.loadSourceReader(c)
		if sourceErr != nil {
			return e.completeCollectionTemplate(ctx, c, token, runID, observation, nil, "SOURCE_MANIFEST_UNAVAILABLE")
		}
		packet, err = agent.PacketFromRunContextRefs(refs, reader)
		if err != nil || packet.CaseID != c.ID || packet.SourceBindingID != c.BindingID || packet.Mode != e.Mode || packet.Kind != agent.Collection || packet.Bridge == nil || packet.Bridge.ID != observation.Bridge.ID || packet.Bridge.Version != version || packet.CustomerID != observation.Bridge.CustomerID || packet.AccountRef != observation.Bridge.AccountRef || !sameActionSet(packet.EffectiveActions, observation.AllowedActions) {
			packet = e.collectionPacket(c, runID, observation, reader)
			return e.completeCollectionTemplate(ctx, c, token, runID, observation, &packet, "PINNED_AGENT_PACKET_UNAVAILABLE")
		}
	} else {
		if plan.RetryAt != nil {
			return StepResult{Status: string(c.Status), NextStage: "INVESTIGATE", RetryAt: plan.RetryAt}, nil
		}
		runID, err = domain.StableRunID(c.ID, plan.Revision)
		if err != nil {
			return StepResult{}, err
		}
		reader, sourceErr := e.loadSourceReader(c)
		limitation := ""
		if sourceErr != nil {
			limitation = "SOURCE_MANIFEST_UNAVAILABLE"
			reader = nil
		}
		packet = e.collectionPacket(c, runID, observation, reader)
		refs, err := agent.RunContextRefs(packet)
		if err != nil {
			return StepResult{}, err
		}
		privateRefs, err := collectionPrivateContextRefs(c, runID, packet, version, limitation)
		if err != nil {
			return StepResult{}, err
		}
		admittedID, created, retryAt, err := e.Repo.StartCollectionRun(ctx, c.ID, token, observation.Bridge.ID, *c.PendingTrigger, runID, version, plan.Revision, "INVESTIGATE", packet.PromptVersion, packet.ModelVersion, refs, privateRefs)
		if err != nil {
			return StepResult{}, err
		}
		if retryAt != nil {
			return StepResult{Status: string(c.Status), NextStage: "INVESTIGATE", RetryAt: retryAt}, nil
		}
		if admittedID == "" {
			return StepResult{Status: string(c.Status), NextStage: "MONITORING"}, nil
		}
		runID = admittedID
		if !created {
			pinned, err := e.Repo.ReadRunContextRefs(ctx, c.ID, token, runID)
			if err != nil {
				return StepResult{}, err
			}
			pinnedPrivate, err := e.Repo.ReadRunPrivateContextRefs(ctx, c.ID, token, runID)
			if errors.Is(err, domain.ErrNotFound) {
				return e.completeCollectionTemplate(ctx, c, token, runID, observation, nil, "PRIVATE_RUN_SCOPE_INVALID")
			}
			if err != nil {
				return StepResult{}, err
			}
			pinnedContext, validationErr := validatePrivateCollectionContext(pinnedPrivate, c, runID, e.Mode, observation.Bridge.ID, version, observation.AllowedActions)
			if validationErr != nil {
				return e.completeCollectionTemplate(ctx, c, token, runID, observation, nil, "PRIVATE_RUN_SCOPE_INVALID")
			}
			if pinnedContext.TemplateLimitation != "" {
				return e.completeCollectionTemplate(ctx, c, token, runID, observation, nil, pinnedContext.TemplateLimitation)
			}
			packet, err = agent.PacketFromRunContextRefs(pinned, reader)
			if err != nil || packet.CaseID != c.ID || packet.SourceBindingID != c.BindingID || packet.Mode != e.Mode || packet.Kind != agent.Collection || packet.Bridge == nil || packet.Bridge.ID != observation.Bridge.ID || packet.Bridge.Version != version || packet.CustomerID != observation.Bridge.CustomerID || packet.AccountRef != observation.Bridge.AccountRef || !sameActionSet(packet.EffectiveActions, observation.AllowedActions) {
				return e.completeCollectionTemplate(ctx, c, token, runID, observation, &packet, "PINNED_AGENT_PACKET_UNAVAILABLE")
			}
		}
		if limitation != "" {
			return e.completeCollectionTemplate(ctx, c, token, runID, observation, &packet, limitation)
		}
	}
	provider, err := e.agentProvider(packet)
	if err != nil {
		return e.completeCollectionTemplate(ctx, c, token, runID, observation, &packet, "AGENT_PROVIDER_UNAVAILABLE")
	}
	engine := &agent.Engine{Repo: e.Repo, Provider: provider, Servicing: e.Servicing, Mode: packet.AgentMode, Now: e.Now}
	result, err := engine.Step(ctx, packet, token)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrCollectionRefreshPending) {
			disposition := marshalDisposition(agent.RunDisposition{Kind: "SUPERSEDED", BridgeID: packet.Bridge.ID, BridgeVersion: int64Pointer(packet.Bridge.Version), Limitation: "NEWER_SOURCE_REFRESH_PENDING"})
			if err := e.Repo.SupersedeCollectionRun(ctx, c.ID, token, runID, packet.Bridge.ID, c.TargetVersion, disposition); err != nil {
				return StepResult{}, err
			}
			return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE", DispositionCommitted: true}, nil
		}
		return StepResult{}, err
	}
	if !result.Finished {
		return StepResult{Status: string(c.Status), NextStage: "INVESTIGATE", RetryAt: result.RetryAt}, nil
	}
	if result.Finish == nil {
		return e.completeCollectionTemplate(ctx, c, token, runID, observation, &packet, result.Limitation)
	}
	return e.completeCollectionRun(ctx, c, token, runID, observation, packet, *result.Finish, "AI_COMMENTARY", "")
}

func (e *StageExecutor) collectionPacket(c domain.Case, runID string, observation collectionObservation, reader *sources.Reader) agent.Packet {
	agentMode := e.AgentMode
	if agentMode == "" {
		agentMode = "FIXTURE"
	}
	model := e.ModelVersion
	if agentMode == "FIXTURE" {
		model = "fixture-v1"
	}
	packet := agent.Packet{CaseID: c.ID, SourceBindingID: c.BindingID, RunID: runID, Kind: agent.Collection, Mode: e.Mode, CustomerID: observation.Bridge.CustomerID, AccountRef: observation.Bridge.AccountRef, EffectiveActions: uniqueStrings(observation.AllowedActions), SourceReader: reader, Bridge: &observation.Bridge, PromptVersion: agent.PromptVersion, ModelVersion: model, AgentMode: agentMode}
	if reader != nil {
		for _, section := range reader.SectionRefs() {
			packet.Sources = append(packet.Sources, agent.SourceRef{Handle: section.Handle, SectionID: section.SectionID, Locator: section.Locator})
		}
	}
	return packet
}

func (e *StageExecutor) completeCollectionTemplate(ctx context.Context, c domain.Case, token, runID string, observation collectionObservation, packet *agent.Packet, limitation string) (StepResult, error) {
	if limitation == "" {
		limitation = "AGENT_LIMITATION"
	}
	bridge := observation.Bridge
	if packet != nil && packet.Bridge != nil {
		bridge = *packet.Bridge
	}
	finish := agent.Finish{Summary: fmt.Sprintf("Deterministic source summary for bridge version %d. Agent commentary was unavailable; source state remains authoritative.", bridge.Version), Findings: []agent.Finding{}, MissingInformation: []string{"No verified agent investigation was published for this bridge version."}, Conflicts: []string{}, RecommendedNextSteps: []string{}}
	for _, action := range observation.AllowedActions {
		finish.RecommendedNextSteps = append(finish.RecommendedNextSteps, action)
	}
	if packet == nil {
		packet = ptrPacket(e.collectionPacket(c, runID, observation, nil))
	}
	return e.completeCollectionRun(ctx, c, token, runID, observation, *packet, finish, "RULE_TEMPLATE", limitation)
}

func (e *StageExecutor) completeCollectionRun(ctx context.Context, c domain.Case, token, runID string, observation collectionObservation, packet agent.Packet, finish agent.Finish, explanationMode, limitation string) (StepResult, error) {
	if e.Servicing == nil || packet.Bridge == nil {
		return StepResult{}, errors.New("servicing_client_unavailable")
	}
	current, _, err := e.Servicing.ReadBridge(ctx, packet.Bridge.ID)
	if err != nil {
		return StepResult{}, err
	}
	if current.ID != packet.Bridge.ID || current.Mode != packet.Mode || current.SourceBindingID != packet.SourceBindingID || current.CustomerID != packet.CustomerID || current.AccountRef != packet.AccountRef {
		return StepResult{}, &servicing.ContractError{Code: "collection_publication_scope_mismatch"}
	}
	if current.Version != packet.Bridge.Version {
		version := current.Version
		disposition := marshalDisposition(agent.RunDisposition{Kind: "SUPERSEDED", ExplanationMode: explanationMode, BridgeID: packet.Bridge.ID, BridgeVersion: int64Pointer(packet.Bridge.Version), Finish: &finish, Limitation: "AUTHORITATIVE_BRIDGE_VERSION_ADVANCED"})
		if err := e.Repo.SupersedeCollectionRun(ctx, c.ID, token, runID, packet.Bridge.ID, &version, disposition); err != nil {
			return StepResult{}, err
		}
		return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE", DispositionCommitted: true}, nil
	}
	effectiveActions, _ := projectActions(current)
	effectiveActions = uniqueStrings(effectiveActions)
	if !sameActionSet(effectiveActions, packet.EffectiveActions) {
		return StepResult{}, &servicing.ContractError{Code: "collection_same_version_action_drift"}
	}
	bridgeVersion := packet.Bridge.Version
	dispositionKind := "FINISH"
	if limitation != "" {
		dispositionKind = "LIMITATION"
	}
	disposition := marshalDisposition(agent.RunDisposition{Kind: dispositionKind, ExplanationMode: explanationMode, BridgeID: packet.Bridge.ID, BridgeVersion: &bridgeVersion, NextActions: packet.EffectiveActions, Finish: &finish, Limitation: limitation})
	if err := e.Repo.CompleteCollectionRun(ctx, c.ID, token, runID, packet.Bridge.ID, bridgeVersion, packet.EffectiveActions, disposition); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			observed := current.Version
			superseded := marshalDisposition(agent.RunDisposition{Kind: "SUPERSEDED", ExplanationMode: explanationMode, BridgeID: packet.Bridge.ID, BridgeVersion: &bridgeVersion, Finish: &finish, Limitation: "LOCAL_SOURCE_VERSION_ADVANCED"})
			if supersedeErr := e.Repo.SupersedeCollectionRun(ctx, c.ID, token, runID, packet.Bridge.ID, &observed, superseded); supersedeErr != nil {
				return StepResult{}, supersedeErr
			}
			return StepResult{Status: string(c.Status), NextStage: "REFRESH_BRIDGE", DispositionCommitted: true}, nil
		}
		return StepResult{}, err
	}
	return StepResult{Status: string(c.Status), NextStage: "MONITORING", DispositionCommitted: true}, nil
}

func sameActionSet(left, right []string) bool {
	return strings.Join(uniqueStrings(left), "\x00") == strings.Join(uniqueStrings(right), "\x00")
}

func uniqueStrings(values []string) []string {
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

func marshalDisposition(disposition agent.RunDisposition) []byte {
	encoded, _ := json.Marshal(disposition)
	return encoded
}

func int64Pointer(value int64) *int64 { return &value }

func ptrPacket(packet agent.Packet) *agent.Packet { return &packet }

func (e *StageExecutor) investigateShortfall(ctx context.Context, c domain.Case, token string) (StepResult, error) {
	if c.DetectionID == nil || c.CustomerID == nil || c.AccountRef == nil || c.RawAccountID == nil || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
		return StepResult{}, domain.ErrConflict
	}
	row, err := e.Repo.ReadAssessment(ctx, c.ID, assessmentRevision)
	if errors.Is(err, domain.ErrNotFound) || err == nil && row.SavedAssessmentID == nil {
		return StepResult{Status: "EVALUATING", NextStage: "EVALUATE"}, nil
	}
	if err != nil {
		return StepResult{}, err
	}
	if row.SavedAssessmentID == nil || len(row.Response) == 0 {
		return StepResult{}, &underwriting.ContractError{Code: "SAVED_ASSESSMENT_INCOMPLETE"}
	}
	evaluation, err := underwriting.DecodeEvaluation(row.Response)
	if err != nil || evaluation.ID != *row.SavedAssessmentID {
		return StepResult{}, &underwriting.ContractError{Code: "SAVED_ASSESSMENT_INVALID"}
	}
	linkage := underwriting.Linkage{ShortfallID: *c.DetectionID, CustomerID: *c.CustomerID, AccountID: *c.RawAccountID, SourceMode: e.Mode, TermDays: assessmentTermDays}
	if err := underwriting.ValidateEvaluation(evaluation, linkage); err != nil {
		return StepResult{}, err
	}
	runID, err := domain.StableRunID(c.ID, assessmentRevision)
	if err != nil {
		return StepResult{}, err
	}
	refs, err := e.Repo.ReadRunContextRefs(ctx, c.ID, token, runID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return StepResult{Status: "EVALUATING", NextStage: "EVALUATE"}, nil
		}
		return StepResult{}, err
	}
	privateRefs, err := e.Repo.ReadRunPrivateContextRefs(ctx, c.ID, token, runID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return e.completeRuleTemplate(ctx, c, token, runID, row, evaluation, "PRIVATE_RUN_SCOPE_UNAVAILABLE")
		}
		return StepResult{}, err
	}
	packet, limitation := e.restorePinnedShortfallPacket(c, runID, refs, privateRefs, evaluation, row)
	if limitation != "" {
		return e.completeRuleTemplate(ctx, c, token, runID, row, evaluation, limitation)
	}
	provider, err := e.agentProvider(packet)
	if err != nil {
		return e.completeRuleTemplate(ctx, c, token, runID, row, evaluation, "AGENT_PROVIDER_UNAVAILABLE")
	}
	engine := &agent.Engine{Repo: e.Repo, Provider: provider, Servicing: e.Servicing, Mode: packet.AgentMode, Now: e.Now}
	result, err := engine.Step(ctx, packet, token)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return e.completeRuleTemplate(ctx, c, token, runID, row, evaluation, "PINNED_AGENT_PACKET_CHANGED")
		}
		return StepResult{}, err
	}
	if !result.Finished {
		return StepResult{Status: "INVESTIGATING", NextStage: "INVESTIGATE", RetryAt: result.RetryAt}, nil
	}
	if result.Finish == nil {
		return e.completeRuleTemplate(ctx, c, token, runID, row, evaluation, result.Limitation)
	}
	return e.completeShortfallWithFinish(ctx, c, token, runID, row, evaluation, *result.Finish, "AI_COMMENTARY", "")
}

func (e *StageExecutor) loadSourceReader(c domain.Case, privateIDs ...string) (*sources.Reader, error) {
	if e.SourceRoot == "" {
		return nil, nil
	}
	if c.CustomerID == nil || c.AccountRef == nil || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
		return nil, domain.ErrConflict
	}
	reader, err := sources.LoadForCase(e.SourceRoot, c.ID, sources.Scope{Mode: e.Mode, CustomerID: *c.CustomerID, AccountRef: *c.AccountRef, BindingID: c.BindingID})
	if err != nil {
		return nil, err
	}
	if c.RawAccountID != nil && readerContainsIdentifier(reader, *c.RawAccountID, valueOrEmpty(c.AccountRef)) {
		return nil, errors.New("source_manifest_contains_private_account_id")
	}
	for _, privateID := range privateIDs {
		if privateID != "" && readerContainsIdentifier(reader, privateID, valueOrEmpty(c.AccountRef)) {
			return nil, errors.New("source_manifest_contains_private_account_id")
		}
	}
	return reader, nil
}

func readerContainsIdentifier(reader *sources.Reader, identifier string, verifiedAliases ...string) bool {
	if reader == nil || identifier == "" {
		return false
	}
	for _, section := range reader.SectionRefs() {
		for _, value := range []string{section.Handle, section.SectionID, section.SourceType, section.RecordID, section.SourceVersion, section.ExportVersion, section.Locator, section.Text} {
			isAlias := false
			for _, alias := range verifiedAliases {
				if alias != "" && value == alias {
					isAlias = true
					break
				}
			}
			if !isAlias && strings.Contains(value, identifier) {
				return true
			}
		}
	}
	return false
}

func (e *StageExecutor) agentProvider(packet agent.Packet) (agent.Provider, error) {
	switch packet.AgentMode {
	case "FIXTURE":
		return &agent.Fixture{Packet: packet}, nil
	case "ANTHROPIC":
		if e.AgentProvider == nil {
			return nil, errors.New("anthropic_provider_not_configured")
		}
		return e.AgentProvider, nil
	default:
		return nil, errors.New("agent_mode_invalid")
	}
}

func (e *StageExecutor) completeRuleTemplate(ctx context.Context, c domain.Case, token, runID string, row domain.AssessmentRequest, evaluation underwriting.Evaluation, limitation string) (StepResult, error) {
	if limitation == "" {
		limitation = "AGENT_LIMITATION"
	}
	finish := ruleTemplateFinish(evaluation)
	return e.completeShortfallWithFinish(ctx, c, token, runID, row, evaluation, finish, "RULE_TEMPLATE", limitation)
}

func (e *StageExecutor) completeShortfallWithFinish(ctx context.Context, c domain.Case, token, runID string, row domain.AssessmentRequest, evaluation underwriting.Evaluation, finish agent.Finish, explanationMode, limitation string) (StepResult, error) {
	kind := "FINISH"
	if limitation != "" {
		kind = "LIMITATION"
	}
	disposition, err := json.Marshal(agent.RunDisposition{Kind: kind, AssessmentID: evaluation.ID, ExplanationMode: explanationMode, NextActions: evaluationNextActions(evaluation), Finish: &finish, Limitation: limitation})
	if err != nil {
		return StepResult{}, errors.New("shortfall_disposition_encode_failed")
	}
	if err := e.Repo.CompleteShortfallRun(ctx, c.ID, token, runID, row.ID, evaluation.ID, disposition); err != nil {
		return StepResult{}, err
	}
	return StepResult{Status: "COMPLETED", NextStage: "COMPLETED", DispositionCommitted: true}, nil
}

func ruleTemplateFinish(evaluation underwriting.Evaluation) agent.Finish {
	evaluationID := evaluation.ID
	finish := agent.Finish{EvaluationID: &evaluationID, Summary: "The saved underwriting result remains unchanged. This rule-based explanation does not verify pending source claims.", Findings: []agent.Finding{}, MissingInformation: []string{}, Conflicts: []string{}, RecommendedNextSteps: evaluationNextActions(evaluation)}
	for _, rule := range evaluation.Rules {
		if rule.Status == "PASS" {
			continue
		}
		finding := agent.Finding{RuleID: rule.ID, Explanation: rule.ReasonCode}
		finish.Findings = append(finish.Findings, finding)
		if rule.Status == "UNKNOWN" {
			finish.MissingInformation = append(finish.MissingInformation, rule.ReasonCode)
		}
		if rule.Status == "FAIL" {
			finish.Conflicts = append(finish.Conflicts, rule.ReasonCode)
		}
	}
	return finish
}

func evaluationNextActions(evaluation underwriting.Evaluation) []string {
	assessment := agent.SavedAssessment{Status: evaluation.Status, ApprovalRequired: &evaluation.ApprovalRequired, RuleReasons: make([]agent.Rule, 0, len(evaluation.Rules))}
	for _, rule := range evaluation.Rules {
		assessment.RuleReasons = append(assessment.RuleReasons, agent.Rule{ID: rule.ID, Status: rule.Status, Reason: rule.ReasonCode})
	}
	return agent.ActionsForAssessment(assessment)
}

func collectionTriggerCode(admittedRuns int, status string, actions []string, previous *collectionObservation, current servicing.Bridge) string {
	if status == "CLOSED" {
		return ""
	}
	if admittedRuns == 0 {
		return "INITIAL"
	}
	if previous == nil {
		return ""
	}
	for _, candidate := range []string{"VERIFY_COLLECTION_AUTHORITY", "RECONCILE_OPERATION", "REQUEST_OPERATOR_REVIEW"} {
		if hasActionCode(actions, candidate) && !hasActionCode(previous.AllowedActions, candidate) {
			return "ACTION_" + candidate
		}
	}
	if hasActionCode(actions, "VERIFY_COLLECTION_AUTHORITY") && !sameOptionalString(previous.Bridge.CollectionAuthorityStatus, current.CollectionAuthorityStatus) {
		return "ACTION_VERIFY_COLLECTION_AUTHORITY"
	}
	if hasActionCode(actions, "VERIFY_COLLECTION_AUTHORITY") && newHoldReason(previous.Bridge.HoldReasons, current.HoldReasons) {
		return "ACTION_VERIFY_COLLECTION_AUTHORITY"
	}
	if hasActionCode(actions, "RECONCILE_OPERATION") && unresolvedOperationChanged(previous.Bridge.Operations, current.Operations) {
		return "ACTION_RECONCILE_OPERATION"
	}
	if hasActionCode(actions, "REQUEST_OPERATOR_REVIEW") && (newHoldReason(previous.Bridge.HoldReasons, current.HoldReasons) || previous.Bridge.Lifecycle != current.Lifecycle || previous.Bridge.Delinquency != current.Delinquency) {
		return "ACTION_REQUEST_OPERATOR_REVIEW"
	}
	return ""
}

func hasActionCode(actions []string, target string) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func newHoldReason(previous, current []string) bool {
	for _, reason := range current {
		found := false
		for _, oldReason := range previous {
			if reason == oldReason {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func unresolvedOperationChanged(previous, current []servicing.Operation) bool {
	for _, operation := range current {
		if terminalOperation(operation.State) && (operation.HoldReason == nil || *operation.HoldReason == "") {
			continue
		}
		found := false
		for _, oldOperation := range previous {
			if oldOperation.ID == operation.ID && oldOperation.Kind == operation.Kind && oldOperation.State == operation.State && sameOptionalString(oldOperation.HoldReason, operation.HoldReason) && sameOptionalInt64(oldOperation.ReservedAmountCents, operation.ReservedAmountCents) && sameOptionalString(oldOperation.SettlementReference, operation.SettlementReference) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func hasSavedEvaluation(row domain.AssessmentRequest) bool {
	return row.SavedAssessmentID != nil && len(row.Response) > 0
}

func assessmentTransactionID(record *shortfall.Record) string {
	if record == nil || record.TransactionID == nil {
		return ""
	}
	return *record.TransactionID
}

func shortfallRecordScope(record shortfall.Record, c domain.Case) bool {
	return c.DetectionID != nil && c.CustomerID != nil && c.AccountRef != nil && c.RawAccountID != nil && record.ID == *c.DetectionID && record.CustomerID != nil && *record.CustomerID == *c.CustomerID && record.AccountID == *c.RawAccountID && *c.AccountRef == domain.InternalAccountRef(*c.CustomerID) && shortfall.HoldReason(record) == ""
}

func (e *StageExecutor) buildShortfallPacket(ctx context.Context, c domain.Case, runID string, evaluation underwriting.Evaluation, row domain.AssessmentRequest) (agent.Packet, string) {
	agentMode := e.AgentMode
	if agentMode == "" {
		agentMode = "FIXTURE"
	}
	model := e.ModelVersion
	if agentMode == "FIXTURE" {
		model = "fixture-v1"
	}
	if model == "" {
		return agent.Packet{}, "AGENT_MODEL_ID_UNAVAILABLE"
	}
	rules := make([]agent.Rule, 0, len(evaluation.Rules))
	for _, rule := range evaluation.Rules {
		rules = append(rules, agent.Rule{ID: rule.ID, Status: rule.Status, Reason: rule.ReasonCode})
	}
	if c.CustomerID == nil || c.AccountRef == nil || c.RawAccountID == nil || evaluation.AccountID != *c.RawAccountID || evaluation.CustomerID != *c.CustomerID || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) || row.SourceTransactionID == nil || *row.SourceTransactionID != evaluation.TransactionID || row.SourceCardID == nil || strings.TrimSpace(*row.SourceCardID) == "" {
		return agent.Packet{}, "SAVED_ASSESSMENT_SCOPE_INVALID"
	}
	facts, err := json.Marshal(evaluationFacts{CustomerID: evaluation.CustomerID, AccountRef: *c.AccountRef, PrincipalCents: evaluation.PrincipalCents, TermDays: evaluation.TermDays, EstimatedFeeCents: evaluation.EstimatedFeeCents, TotalDueCents: evaluation.TotalDueCents, CustomerLimitCents: evaluation.CustomerLimitCents, ExistingExposureCents: evaluation.ExistingExposureCents, PostFundingExposureCents: evaluation.PostFundingExposureCents, AvailableCapitalCents: evaluation.AvailableCapitalCents, ReasonCodes: evaluation.ReasonCodes})
	if err != nil {
		return agent.Packet{}, "SAVED_FACTS_ENCODE_FAILED"
	}
	assessment := &agent.SavedAssessment{ID: evaluation.ID, Status: evaluation.Status, ShortfallID: evaluation.ShortfallID, TermDays: evaluation.TermDays, ApprovalRequired: &evaluation.ApprovalRequired, RuleReasons: rules, FundingAuthorized: evaluation.FundingAuthorized, RequiresRecheck: evaluation.RequiresRecheck}
	packet := agent.Packet{CaseID: c.ID, SourceBindingID: c.BindingID, RunID: runID, Kind: agent.Shortfall, Mode: e.Mode, CustomerID: evaluation.CustomerID, AccountRef: *c.AccountRef, ShortfallID: evaluation.ShortfallID, Assessment: assessment, Rules: rules, SavedFacts: facts, EffectiveActions: agent.ActionsForAssessment(*assessment), PromptVersion: agent.PromptVersion, ModelVersion: model, AgentMode: agentMode, AllowedClaimFields: claimFields(rules)}
	if e.SourceRoot != "" {
		reader, err := e.loadSourceReader(c, valueOrEmpty(row.SourceCardID))
		if err != nil {
			return packet, "SOURCE_MANIFEST_UNAVAILABLE"
		}
		packet.SourceReader = reader
	}
	contextFields := contextFields(rules)
	if len(contextFields) > 0 && e.CustomerContext != nil {
		ctxData, _, err := e.CustomerContext.ReadScoped(ctx, evaluation.CustomerID, evaluation.AccountID, *row.SourceCardID)
		if err != nil {
			if errors.Is(err, customercontext.ErrMissing) {
				return packet, ""
			}
			return packet, "CUSTOMER_CONTEXT_UNAVAILABLE"
		}
		packet.PrivateContextData, err = json.Marshal(ctxData)
		if err != nil {
			return packet, "CUSTOMER_CONTEXT_UNAVAILABLE"
		}
		rawCardID := valueOrEmpty(row.SourceCardID)
		ctxData, err = contextForAgent(ctxData, evaluation.CustomerID, evaluation.AccountID, rawCardID, evaluation.TransactionID, *c.AccountRef)
		if err != nil {
			return packet, "CUSTOMER_CONTEXT_SCOPE_UNSAFE"
		}
		packet.Context = &ctxData
		packet.RequiredContextFields = contextFields
	}
	return packet, ""
}

func (e *StageExecutor) ruleTemplatePacket(c domain.Case, runID string, evaluation underwriting.Evaluation, row domain.AssessmentRequest) (agent.Packet, error) {
	if c.CustomerID == nil || c.AccountRef == nil || c.RawAccountID == nil || c.DetectionID == nil || evaluation.AccountID != *c.RawAccountID || evaluation.TransactionID == "" || row.SourceTransactionID != nil && evaluation.TransactionID != *row.SourceTransactionID || evaluation.CustomerID != *c.CustomerID || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) {
		return agent.Packet{}, &underwriting.ContractError{Code: "SAVED_ASSESSMENT_SCOPE_INVALID"}
	}
	rules := make([]agent.Rule, 0, len(evaluation.Rules))
	for _, rule := range evaluation.Rules {
		rules = append(rules, agent.Rule{ID: rule.ID, Status: rule.Status, Reason: rule.ReasonCode})
	}
	assessment := &agent.SavedAssessment{ID: evaluation.ID, Status: evaluation.Status, ShortfallID: evaluation.ShortfallID, TermDays: evaluation.TermDays, ApprovalRequired: &evaluation.ApprovalRequired, RuleReasons: rules, FundingAuthorized: evaluation.FundingAuthorized, RequiresRecheck: evaluation.RequiresRecheck}
	facts, err := json.Marshal(evaluationFacts{CustomerID: evaluation.CustomerID, AccountRef: *c.AccountRef, PrincipalCents: evaluation.PrincipalCents, TermDays: evaluation.TermDays, EstimatedFeeCents: evaluation.EstimatedFeeCents, TotalDueCents: evaluation.TotalDueCents, CustomerLimitCents: evaluation.CustomerLimitCents, ExistingExposureCents: evaluation.ExistingExposureCents, PostFundingExposureCents: evaluation.PostFundingExposureCents, AvailableCapitalCents: evaluation.AvailableCapitalCents, ReasonCodes: evaluation.ReasonCodes})
	if err != nil {
		return agent.Packet{}, errors.New("saved_facts_encode_failed")
	}
	return agent.Packet{CaseID: c.ID, SourceBindingID: c.BindingID, RunID: runID, Kind: agent.Shortfall, Mode: e.Mode, CustomerID: *c.CustomerID, AccountRef: *c.AccountRef, ShortfallID: *c.DetectionID, Assessment: assessment, Rules: rules, SavedFacts: facts, EffectiveActions: agent.ActionsForAssessment(*assessment), PromptVersion: agent.PromptVersion, ModelVersion: "rule-template-v1", AgentMode: "RULE_TEMPLATE"}, nil
}

func claimFields(rules []agent.Rule) []string {
	for _, rule := range rules {
		if rule.Status == "UNKNOWN" && strings.Contains(strings.ToUpper(rule.ID), "RECEIVABLE") {
			return []string{"RECEIVABLE_DUE_DATE"}
		}
	}
	return nil
}

func contextFields(rules []agent.Rule) []string {
	fields := map[string]bool{}
	for _, rule := range rules {
		if rule.Status != "UNKNOWN" {
			continue
		}
		id := strings.ToUpper(rule.ID)
		switch {
		case strings.Contains(id, "RECEIVABLE"):
			fields["coverage"] = true
			fields["receivables"] = true
		case strings.Contains(id, "BALANCE"):
			fields["coverage"] = true
			fields["balance_history"] = true
		case strings.Contains(id, "INFLOW"):
			fields["coverage"] = true
			fields["inflow_history"] = true
		case strings.Contains(id, "STANDING"):
			fields["coverage"] = true
			fields["standing"] = true
		}
	}
	out := make([]string, 0, len(fields))
	for field := range fields {
		out = append(out, field)
	}
	return out
}

func contextForAgent(response customercontext.Response, customerID, rawAccountID, rawCardID, rawTransactionID, accountRef string) (customercontext.Response, error) {
	var data map[string]json.RawMessage
	if json.Unmarshal(response.Data, &data) != nil || data == nil || customerID == "" || rawAccountID == "" || rawCardID == "" || rawTransactionID == "" || accountRef != domain.InternalAccountRef(customerID) || response.CustomerID != customerID {
		return customercontext.Response{}, errors.New("customer_context_data_invalid")
	}
	if err := validateContextIdentity(data, "account_id", rawAccountID); err != nil {
		return customercontext.Response{}, err
	}
	if err := validateContextIdentity(data, "card_id", rawCardID); err != nil {
		return customercontext.Response{}, err
	}
	if err := validateOptionalContextIdentity(data, "customer_id", customerID); err != nil {
		return customercontext.Response{}, err
	}
	if err := validateOptionalContextIdentity(data, "account_ref", accountRef); err != nil {
		return customercontext.Response{}, err
	}
	privateIDs := map[string]bool{rawAccountID: true, rawCardID: true, rawTransactionID: true}
	for key := range data {
		lowerKey := strings.ToLower(key)
		if lowerKey == "customer_id" || lowerKey == "account_ref" || privateContextField(lowerKey) {
			delete(data, key)
		}
	}
	for key, value := range data {
		cleaned, keep, err := sanitizeContextValue(value, privateIDs)
		if err != nil {
			return customercontext.Response{}, err
		}
		if !keep {
			delete(data, key)
			continue
		}
		data[key] = cleaned
	}
	refs := response.SourceRefs[:0]
	for _, ref := range response.SourceRefs {
		trustedRecord := ref.RecordID == accountRef
		privateReference := containsPrivateIdentifier(ref.System, privateIDs) ||
			containsPrivateIdentifier(ref.Version, privateIDs) ||
			!trustedRecord && containsPrivateIdentifier(ref.RecordID, privateIDs)
		if !privateReference {
			refs = append(refs, ref)
		}
	}
	customerJSON, err := json.Marshal(customerID)
	if err != nil {
		return customercontext.Response{}, errors.New("customer_context_data_invalid")
	}
	aliasJSON, err := json.Marshal(accountRef)
	if err != nil {
		return customercontext.Response{}, errors.New("customer_context_data_invalid")
	}
	data["customer_id"] = customerJSON
	data["account_ref"] = aliasJSON
	response.Data, err = json.Marshal(data)
	if err != nil {
		return customercontext.Response{}, errors.New("customer_context_data_invalid")
	}
	response.SourceRefs = refs
	return response, nil
}

func validateContextIdentity(data map[string]json.RawMessage, key, expected string) error {
	raw, ok := data[key]
	if !ok {
		return errors.New("customer_context_identity_missing")
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value != expected {
		return errors.New("customer_context_identity_mismatch")
	}
	return nil
}

func validateOptionalContextIdentity(data map[string]json.RawMessage, key, expected string) error {
	raw, ok := data[key]
	if !ok {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value != expected {
		return errors.New("customer_context_identity_mismatch")
	}
	return nil
}

func privateContextField(key string) bool {
	switch key {
	case "account_id", "provider_account_id", "card_id", "provider_card_id", "transaction_id", "payment_id", "provider_payment_id", "canonical_event_id", "payer_id", "record_id", "source_record_id", "source_refs":
		return true
	default:
		return false
	}
}

func sanitizeContextValue(raw json.RawMessage, privateIDs map[string]bool) (json.RawMessage, bool, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, false, errors.New("customer_context_data_invalid")
	}
	projected, keep := sanitizeContextValueNode(value, "", privateIDs, false)
	if !keep {
		return nil, false, nil
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return nil, false, errors.New("customer_context_data_invalid")
	}
	return encoded, true, nil
}

func sanitizeContextValueNode(value any, key string, privateIDs map[string]bool, nested bool) (any, bool) {
	if privateContextField(strings.ToLower(key)) || nested && (strings.EqualFold(key, "customer_id") || strings.EqualFold(key, "account_ref")) {
		return nil, false
	}
	switch current := value.(type) {
	case string:
		return current, !containsPrivateIdentifier(current, privateIDs)
	case []any:
		out := make([]any, 0, len(current))
		for _, item := range current {
			projected, keep := sanitizeContextValueNode(item, "", privateIDs, true)
			if keep {
				out = append(out, projected)
			}
		}
		return out, true
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			projected, keep := sanitizeContextValueNode(item, key, privateIDs, true)
			if keep {
				out[key] = projected
			}
		}
		return out, true
	}
	return value, true
}

func containsPrivateIdentifier(value string, privateIDs map[string]bool) bool {
	for identifier := range privateIDs {
		if identifier != "" && strings.Contains(value, identifier) {
			return true
		}
	}
	return false
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
