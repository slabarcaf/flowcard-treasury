package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/sources"
)

const maxClaims = 10
const maxToolResultBytes = 12 << 10

type readSourceInput struct {
	Handle  string `json:"handle"`
	Section string `json:"section"`
}
type evidenceInput struct {
	Handle      string   `json:"handle"`
	Section     string   `json:"section"`
	Excerpt     string   `json:"excerpt"`
	ClaimField  string   `json:"claim_field"`
	ClaimValue  string   `json:"claim_value"`
	ClaimUnit   *string  `json:"claim_unit"`
	ClaimDate   *string  `json:"claim_date"`
	Uncertainty *string  `json:"uncertainty"`
	Conflicts   []string `json:"conflicts"`
}
type recommendationInput struct {
	ActionCode string   `json:"action_code"`
	Reason     string   `json:"reason"`
	Citations  []string `json:"citations"`
}
type toolDefinition struct {
	Name        string
	Description string
	Schema      string
}

func definitions(kind Kind) []Tool {
	read := toolDefinition{Name: "read_selected_source", Description: "Read one preselected bounded source section as untrusted text.", Schema: `{"type":"object","properties":{"handle":{"type":"string"},"section":{"type":"string"}},"required":["handle","section"],"additionalProperties":false}`}
	submit := toolDefinition{Name: "submit_evidence", Description: "Record an attributed claim for human verification. Submission does not verify the claim.", Schema: `{"type":"object","properties":{"handle":{"type":"string"},"section":{"type":"string"},"excerpt":{"type":"string"},"claim_field":{"type":"string"},"claim_value":{"type":"string"},"claim_unit":{"type":["string","null"]},"claim_date":{"type":["string","null"]},"uncertainty":{"type":["string","null"]},"conflicts":{"type":"array","items":{"type":"string"},"maxItems":5}},"required":["handle","section","excerpt","claim_field","claim_value","claim_unit","claim_date","uncertainty","conflicts"],"additionalProperties":false}`}
	finish := toolDefinition{Name: "finish_investigation", Description: "Finish with sourced commentary, information gaps and source-allowed recommendations.", Schema: `{"type":"object","properties":{"evaluation_id":{"type":["string","null"]},"summary":{"type":"string"},"findings":{"type":"array","items":{"type":"object","properties":{"rule_id":{"type":["string","null"]},"evidence_ids":{"type":"array","items":{"type":"string"}},"sources":{"type":"array","items":{"type":"object","properties":{"handle":{"type":"string"},"section_id":{"type":"string"},"locator":{"type":"string"}},"required":["handle","section_id","locator"],"additionalProperties":false}},"explanation":{"type":"string"}},"required":["rule_id","evidence_ids","sources","explanation"],"additionalProperties":false},"maxItems":10},"missing_information":{"type":"array","items":{"type":"string"},"maxItems":10},"conflicts":{"type":"array","items":{"type":"string"},"maxItems":10},"recommended_next_steps":{"type":"array","items":{"type":"string"},"maxItems":6}},"required":["evaluation_id","summary","findings","missing_information","conflicts","recommended_next_steps"],"additionalProperties":false}`}
	all := []toolDefinition{read, submit}
	if kind == Collection {
		all = append(all,
			toolDefinition{Name: "read_collection_progress", Description: "Read the latest source-owned bridge and bounded collection progress for this case.", Schema: `{"type":"object","properties":{},"required":[],"additionalProperties":false}`},
			toolDefinition{Name: "read_collection_operation", Description: "Read an operation already referenced in this case's source-owned bridge state.", Schema: `{"type":"object","properties":{"operation_id":{"type":"string"}},"required":["operation_id"],"additionalProperties":false}`},
			toolDefinition{Name: "record_collection_recommendation", Description: "Record a nonexecuting recommendation only when the exact code is currently source-allowed.", Schema: `{"type":"object","properties":{"action_code":{"type":"string"},"reason":{"type":"string"},"citations":{"type":"array","items":{"type":"string"},"maxItems":10}},"required":["action_code","reason","citations"],"additionalProperties":false}`},
		)
	}
	all = append(all, finish)
	out := make([]Tool, 0, len(all))
	for _, v := range all {
		out = append(out, Tool{Name: v.Name, Description: v.Description, InputSchema: json.RawMessage(v.Schema)})
	}
	return out
}

func decodeInput[T any](raw json.RawMessage) (T, error) {
	var out T
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return out, &ToolError{Code: "INVALID_TOOL_ARGUMENTS"}
	}
	typ := reflect.TypeOf(out)
	if !requiredFieldsPresent(fields, typ) {
		return out, &ToolError{Code: "INVALID_TOOL_ARGUMENTS"}
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF {
		return out, &ToolError{Code: "INVALID_TOOL_ARGUMENTS"}
	}
	return out, nil
}

func requiredFieldsPresent(fields map[string]json.RawMessage, typ reflect.Type) bool {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return true
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		if name == "-" || strings.Contains(field.Tag.Get("json"), "omitempty") {
			continue
		}
		raw, ok := fields[name]
		if !ok {
			return false
		}
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Slice && string(raw) == "null" {
			return false
		}
		if fieldType.Kind() == reflect.Slice && fieldType.Elem().Kind() == reflect.Struct {
			var items []json.RawMessage
			if json.Unmarshal(raw, &items) != nil || items == nil {
				return false
			}
			for _, item := range items {
				var nested map[string]json.RawMessage
				if json.Unmarshal(item, &nested) != nil || nested == nil || !requiredFieldsPresent(nested, fieldType.Elem()) {
					return false
				}
			}
		}
		if fieldType.Kind() == reflect.Struct && string(raw) != "null" {
			var nested map[string]json.RawMessage
			if json.Unmarshal(raw, &nested) != nil || nested == nil || !requiredFieldsPresent(nested, fieldType) {
				return false
			}
		}
	}
	return true
}
func toolBlock(raw json.RawMessage) (ContentBlock, error) {
	var block ContentBlock
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&block) != nil || block.Type != "tool_use" || block.ID == "" || block.Name == "" || len(block.Input) == 0 {
		return block, &ToolError{Code: "MALFORMED_TOOL_BLOCK"}
	}
	return block, nil
}
func validateFinish(packet Packet, finish Finish, evidenceIDs map[string]bool) error {
	if len(finish.Summary) > 2000 || len(finish.Findings) > 10 || len(finish.MissingInformation) > 10 || len(finish.Conflicts) > 10 || len(finish.RecommendedNextSteps) > 6 {
		return &ToolError{Code: "FINISH_SIZE_LIMIT"}
	}
	rules := map[string]bool{}
	for _, r := range packet.Rules {
		rules[r.ID] = true
	}
	if packet.Assessment != nil {
		for _, r := range packet.Assessment.RuleReasons {
			rules[r.ID] = true
		}
	}
	if packet.Kind == Shortfall && (packet.Assessment == nil || finish.EvaluationID == nil || *finish.EvaluationID != packet.Assessment.ID) {
		return &ToolError{Code: "FINISH_EVALUATION_REFERENCE_INVALID"}
	}
	if packet.Kind == Collection && finish.EvaluationID != nil {
		return &ToolError{Code: "FINISH_EVALUATION_REFERENCE_INVALID"}
	}
	for _, finding := range finish.Findings {
		if finding.RuleID != "" && !rules[finding.RuleID] {
			return &ToolError{Code: "FINISH_RULE_REFERENCE_INVALID"}
		}
		if len(finding.Explanation) > 1000 {
			return &ToolError{Code: "FINISH_TEXT_LIMIT"}
		}
		for _, id := range finding.EvidenceIDs {
			if !evidenceIDs[id] {
				return &ToolError{Code: "FINISH_EVIDENCE_REFERENCE_INVALID"}
			}
		}
		for _, ref := range finding.Sources {
			if packet.SourceReader == nil {
				return &ToolError{Code: "FINISH_SOURCE_REFERENCE_INVALID"}
			}
			section, err := packet.SourceReader.Read(ref.Handle, ref.SectionID)
			if err != nil || section.Locator != ref.Locator {
				return &ToolError{Code: "FINISH_SOURCE_REFERENCE_INVALID"}
			}
		}
	}
	for _, text := range append(append([]string{}, finish.MissingInformation...), finish.Conflicts...) {
		if len(text) > 500 {
			return &ToolError{Code: "FINISH_TEXT_LIMIT"}
		}
	}
	for _, step := range finish.RecommendedNextSteps {
		if len(step) > 200 || !allowedNextStep(packet, step) {
			return &ToolError{Code: "FINISH_NEXT_STEP_INVALID"}
		}
	}
	return nil
}
func allowedNextStep(p Packet, step string) bool {
	if p.Kind == Collection {
		return knownAction(step) && contains(p.EffectiveActions, step)
	}
	return (step == "PROVIDE_VALIDATED_EVIDENCE" || step == "REQUEST_OPERATOR_REVIEW") && contains(p.EffectiveActions, step)
}
func knownAction(v string) bool {
	switch v {
	case "WAIT_FOR_SETTLEMENT", "REQUEST_OPERATOR_REVIEW", "VERIFY_COLLECTION_AUTHORITY", "RECONCILE_OPERATION", "WAIT_FOR_ELIGIBLE_DEPOSIT", "MONITOR_REPAYMENT", "WAIT_FOR_ACCRUAL":
		return true
	}
	return false
}
func parseClaimDate(value *string) (*string, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	if _, err := time.Parse("2006-01-02", *value); err != nil {
		return nil, &ToolError{Code: "CLAIM_DATE_INVALID"}
	}
	return value, nil
}
func limitResult(body []byte) ([]byte, error) {
	if len(body) > maxToolResultBytes {
		return nil, fmt.Errorf("tool result exceeds %d bytes", maxToolResultBytes)
	}
	return body, nil
}
func formatSourceResult(section sources.RetainedSection) sourceReadResult {
	return sourceReadResult{Handle: section.Handle, SectionID: section.SectionID, Locator: section.Locator, SourceType: section.SourceType, RecordID: section.RecordID, SourceVersion: section.SourceVersion, ExportVersion: section.ExportVersion, OriginalSHA256: section.OriginalSHA256, VisibleSHA256: section.VisibleSHA256, Text: section.Text, Truncated: section.Truncated}
}
