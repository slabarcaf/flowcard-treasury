package agent

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/sources"
)

var fixtureDatePattern = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
var fixtureDuePattern = regexp.MustCompile(`(?i)\bdue(?:\s+date|\s+on)?\b`)

// Fixture provides a deterministic source-to-pending-evidence investigation.
type Fixture struct{ Packet Packet }

type fixtureToolUse struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type fixtureToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

type fixtureEvidenceReceipt struct {
	EvidenceID string `json:"evidence_id"`
}

func (f *Fixture) CreateMessage(_ context.Context, request Request) (Response, error) {
	if request.Model != "fixture-v1" && request.Model != "" {
		return Response{}, &ProviderError{Kind: ProviderPermanent, Code: "FIXTURE_MODEL_MISMATCH"}
	}
	_, results, err := fixtureProtocolState(request.Messages)
	if err != nil {
		return Response{}, &ProviderError{Kind: ProviderPermanent, Code: "FIXTURE_HISTORY_INVALID"}
	}
	if f.Packet.Kind == Shortfall {
		return f.shortfallMessage(request, results)
	}
	return f.collectionMessage(results)
}

func (f *Fixture) shortfallMessage(request Request, results map[string]fixtureToolResult) (Response, error) {
	if _, ok := results["fixture-read-invoice-1"]; !ok {
		if requestForcesFinish(request) {
			return f.finishMessage(nil, nil, nil)
		}
		section, ok := f.invoiceSection()
		if !ok {
			if f.Packet.SourceReader != nil && len(f.Packet.SourceReader.Handles()) == 0 {
				return f.finishMessage(nil, nil, nil)
			}
			return Response{}, &ProviderError{Kind: ProviderPermanent, Code: "FIXTURE_INVOICE_SOURCE_UNAVAILABLE"}
		}
		input, _ := json.Marshal(readSourceInput{Handle: section.Handle, Section: section.SectionID})
		return fixtureResponse(fixtureToolUse{Type: "tool_use", ID: "fixture-read-invoice-1", Name: "read_selected_source", Input: input}), nil
	}
	readResult, ok := results["fixture-read-invoice-1"]
	if !ok || readResult.IsError {
		return Response{}, &ProviderError{Kind: ProviderPermanent, Code: "FIXTURE_INVOICE_READ_FAILED"}
	}
	var envelope struct {
		OK        bool             `json:"ok"`
		ErrorCode string           `json:"error_code"`
		Value     sourceReadResult `json:"value"`
	}
	if json.Unmarshal([]byte(readResult.Content), &envelope) != nil || !envelope.OK {
		return Response{}, &ProviderError{Kind: ProviderPermanent, Code: "FIXTURE_INVOICE_READ_INVALID"}
	}
	if _, ok := results["fixture-submit-evidence-1"]; !ok {
		if requestForcesFinish(request) {
			return f.finishMessage(nil, nil, nil)
		}
		line, date, ok := fixtureDueDateExcerpt(envelope.Value)
		if !ok {
			return f.finishMessage(nil, nil, &SourceRef{Handle: envelope.Value.Handle, SectionID: envelope.Value.SectionID, Locator: envelope.Value.Locator})
		}
		claimField := fixtureDueDateField(f.Packet.AllowedClaimFields)
		if claimField == "" {
			return f.finishMessage(nil, nil, &SourceRef{Handle: envelope.Value.Handle, SectionID: envelope.Value.SectionID, Locator: envelope.Value.Locator})
		}
		input, _ := json.Marshal(evidenceInput{Handle: envelope.Value.Handle, Section: envelope.Value.SectionID, Excerpt: line, ClaimField: claimField, ClaimValue: date, ClaimUnit: nil, ClaimDate: nil, Uncertainty: stringPointer("Fixture extraction; semantic verification remains pending."), Conflicts: []string{}})
		return fixtureResponse(fixtureToolUse{Type: "tool_use", ID: "fixture-submit-evidence-1", Name: "submit_evidence", Input: input}), nil
	}
	submitResult := results["fixture-submit-evidence-1"]
	if submitResult.IsError {
		return f.finishMessage(nil, nil, nil)
	}
	var receiptEnvelope struct {
		OK    bool                   `json:"ok"`
		Value fixtureEvidenceReceipt `json:"value"`
	}
	if json.Unmarshal([]byte(submitResult.Content), &receiptEnvelope) != nil || !receiptEnvelope.OK || receiptEnvelope.Value.EvidenceID == "" {
		return f.finishMessage(nil, nil, nil)
	}
	section, err := f.Packet.SourceReader.Read(envelope.Value.Handle, envelope.Value.SectionID)
	if err != nil {
		return f.finishMessage(nil, nil, nil)
	}
	ruleID := fixtureUnknownRule(f.Packet)
	finding := Finding{RuleID: ruleID, EvidenceIDs: []string{receiptEnvelope.Value.EvidenceID}, Sources: []SourceRef{{Handle: section.Handle, SectionID: section.SectionID, Locator: section.Locator}}, Explanation: "The selected invoice contains this due-date claim. The evidence receipt remains pending human verification."}
	return f.finishMessage(&finding, &receiptEnvelope.Value.EvidenceID, &SourceRef{Handle: section.Handle, SectionID: section.SectionID, Locator: section.Locator})
}

func (f *Fixture) collectionMessage(results map[string]fixtureToolResult) (Response, error) {
	if _, ok := results["fixture-collection-progress-1"]; !ok {
		input := json.RawMessage(`{}`)
		return fixtureResponse(fixtureToolUse{Type: "tool_use", ID: "fixture-collection-progress-1", Name: "read_collection_progress", Input: input}), nil
	}
	return f.finishMessage(nil, nil, nil)
}

func (f *Fixture) invoiceSection() (sources.RetainedSection, bool) {
	if f.Packet.SourceReader == nil {
		return sources.RetainedSection{}, false
	}
	for _, ref := range f.Packet.SourceReader.SectionRefs() {
		if !strings.Contains(strings.ToUpper(ref.SourceType), "INVOICE") {
			continue
		}
		return ref, true
	}
	return sources.RetainedSection{}, false
}

func fixtureDueDateExcerpt(result sourceReadResult) (string, string, bool) {
	prefix := "<<<BEGIN UNTRUSTED SOURCE " + result.Handle + "/" + result.SectionID + ">>>\n"
	suffix := "\n<<<END UNTRUSTED SOURCE " + result.Handle + "/" + result.SectionID + ">>>"
	text := strings.TrimPrefix(result.Text, prefix)
	text = strings.TrimSuffix(text, suffix)
	for _, line := range strings.Split(text, "\n") {
		if !fixtureDuePattern.MatchString(line) {
			continue
		}
		date := fixtureDatePattern.FindString(line)
		if date == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", date); err == nil {
			return line, date, true
		}
	}
	return "", "", false
}

func fixtureDueDateField(fields []string) string {
	for _, field := range fields {
		if strings.Contains(strings.ToUpper(field), "DUE_DATE") {
			return field
		}
	}
	return ""
}

func fixtureUnknownRule(packet Packet) string {
	if packet.Assessment == nil {
		return ""
	}
	for _, rule := range packet.Assessment.RuleReasons {
		if rule.Status == "UNKNOWN" && strings.Contains(strings.ToUpper(rule.ID), "RECEIVABLE") {
			return rule.ID
		}
	}
	for _, rule := range packet.Assessment.RuleReasons {
		if rule.Status == "UNKNOWN" {
			return rule.ID
		}
	}
	return ""
}

func (f *Fixture) finishMessage(finding *Finding, evidenceID *string, source *SourceRef) (Response, error) {
	finish := Finish{Summary: "Fixture investigation records only source-attributed pending evidence; it does not change the saved financial decision.", Findings: []Finding{}, MissingInformation: []string{"The extracted claim requires independent semantic and source verification."}, Conflicts: []string{}, RecommendedNextSteps: []string{}}
	if finding != nil {
		finish.Findings = append(finish.Findings, *finding)
	}
	if evidenceID == nil {
		finish.MissingInformation = append(finish.MissingInformation, "No cited pending evidence receipt was produced by this fixture run.")
	}
	if source == nil && f.Packet.SourceReader != nil {
		refs := f.Packet.SourceReader.SectionRefs()
		if len(refs) > 0 {
			ref := refs[0]
			source = &SourceRef{Handle: ref.Handle, SectionID: ref.SectionID, Locator: ref.Locator}
		}
	}
	if source != nil && len(finish.Findings) == 0 {
		finish.Findings = append(finish.Findings, Finding{Sources: []SourceRef{*source}, Explanation: "The selected source was read, but no validated due-date claim was available for submission."})
	}
	if f.Packet.Kind == Shortfall && f.Packet.Assessment != nil {
		finish.EvaluationID = &f.Packet.Assessment.ID
	}
	for _, action := range f.Packet.EffectiveActions {
		if action == "REQUEST_OPERATOR_REVIEW" {
			finish.RecommendedNextSteps = append(finish.RecommendedNextSteps, action)
			break
		}
	}
	input, _ := json.Marshal(finish)
	return fixtureResponse(fixtureToolUse{Type: "tool_use", ID: "fixture-finish-1", Name: "finish_investigation", Input: input}), nil
}

func fixtureProtocolState(messages []Message) (map[string]fixtureToolUse, map[string]fixtureToolResult, error) {
	uses := make(map[string]fixtureToolUse)
	results := make(map[string]fixtureToolResult)
	for _, message := range messages {
		var blocks []json.RawMessage
		if json.Unmarshal(message.Content, &blocks) != nil {
			continue
		}
		for _, raw := range blocks {
			var header struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				ToolUseID string `json:"tool_use_id"`
				Content   string `json:"content"`
				IsError   bool   `json:"is_error"`
			}
			if json.Unmarshal(raw, &header) != nil {
				return nil, nil, errors.New("fixture_message_invalid")
			}
			switch header.Type {
			case "tool_use":
				block, err := toolBlock(raw)
				if err != nil {
					return nil, nil, err
				}
				uses[block.ID] = fixtureToolUse{Type: block.Type, ID: block.ID, Name: block.Name, Input: block.Input}
			case "tool_result":
				results[header.ToolUseID] = fixtureToolResult{Type: header.Type, ToolUseID: header.ToolUseID, Content: header.Content, IsError: header.IsError}
			}
		}
	}
	return uses, results, nil
}

func fixtureResponse(call fixtureToolUse) Response {
	content, _ := json.Marshal(call)
	return Response{ID: "fixture-message", Type: "message", Role: "assistant", Model: "fixture-v1", Content: []json.RawMessage{content}, StopReason: "tool_use"}
}

func stringPointer(value string) *string { return &value }
