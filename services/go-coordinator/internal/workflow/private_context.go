package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	agent "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/agent"
	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/underwriting"
)

type pinnedRunContext struct {
	SchemaVersion      int             `json:"schema_version"`
	CaseID             string          `json:"case_id"`
	BindingID          string          `json:"source_binding_id"`
	RunID              string          `json:"run_id"`
	Kind               agent.Kind      `json:"case_kind"`
	Mode               string          `json:"mode"`
	CustomerID         string          `json:"customer_id"`
	AccountRef         string          `json:"account_ref"`
	RawAccountID       string          `json:"raw_account_id,omitempty"`
	RawCardID          string          `json:"raw_card_id,omitempty"`
	ShortfallID        string          `json:"shortfall_id,omitempty"`
	AssessmentID       string          `json:"assessment_id,omitempty"`
	TransactionID      string          `json:"transaction_id,omitempty"`
	CustomerContext    json.RawMessage `json:"customer_context,omitempty"`
	TemplateLimitation string          `json:"template_limitation,omitempty"`
	BridgeID           string          `json:"bridge_id,omitempty"`
	BridgeVersion      *int64          `json:"bridge_version,omitempty"`
	EffectiveActions   []string        `json:"effective_actions,omitempty"`
}

func shortfallPrivateContextRefs(c domain.Case, runID string, packet agent.Packet, evaluation underwriting.Evaluation, row domain.AssessmentRequest, limitation string) ([]byte, error) {
	if c.CustomerID == nil || c.AccountRef == nil || c.RawAccountID == nil || packet.AgentMode != "RULE_TEMPLATE" && (row.SourceTransactionID == nil || row.SourceCardID == nil) || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) || evaluation.AccountID != *c.RawAccountID || evaluation.CustomerID != *c.CustomerID || evaluation.TransactionID == "" || row.SourceTransactionID != nil && evaluation.TransactionID != *row.SourceTransactionID || runID == "" {
		return nil, errors.New("private_run_scope_invalid")
	}
	if len(packet.PrivateContextData) > 0 && !json.Valid(packet.PrivateContextData) {
		return nil, errors.New("private_run_context_invalid")
	}
	value := pinnedRunContext{SchemaVersion: 1, CaseID: c.ID, BindingID: c.BindingID, RunID: runID, Kind: agent.Shortfall, Mode: packet.Mode, CustomerID: *c.CustomerID, AccountRef: *c.AccountRef, RawAccountID: *c.RawAccountID, RawCardID: valueOrEmpty(row.SourceCardID), ShortfallID: *c.DetectionID, AssessmentID: evaluation.ID, TransactionID: evaluation.TransactionID, CustomerContext: append(json.RawMessage(nil), packet.PrivateContextData...), TemplateLimitation: limitation}
	return json.Marshal(value)
}

func collectionPrivateContextRefs(c domain.Case, runID string, packet agent.Packet, version int64, limitation string) ([]byte, error) {
	if c.CustomerID == nil || c.AccountRef == nil || c.BridgeID == nil || *c.AccountRef != domain.InternalAccountRef(*c.CustomerID) || packet.Bridge == nil || packet.Bridge.ID != *c.BridgeID || packet.Bridge.Version != version {
		return nil, errors.New("private_collection_scope_invalid")
	}
	return json.Marshal(pinnedRunContext{SchemaVersion: 1, CaseID: c.ID, BindingID: c.BindingID, RunID: runID, Kind: agent.Collection, Mode: packet.Mode, CustomerID: *c.CustomerID, AccountRef: *c.AccountRef, BridgeID: *c.BridgeID, BridgeVersion: &version, EffectiveActions: append([]string(nil), packet.EffectiveActions...), TemplateLimitation: limitation})
}

func decodePinnedRunContext(raw []byte) (pinnedRunContext, error) {
	var value pinnedRunContext
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.SchemaVersion != 1 || value.CaseID == "" || value.BindingID == "" || value.RunID == "" || (value.Mode != "DEMO" && value.Mode != "SANDBOX") || value.CustomerID == "" || value.AccountRef == "" {
		return pinnedRunContext{}, errors.New("private_run_context_invalid")
	}
	return value, nil
}

func validatePrivateShortfallContext(raw []byte, c domain.Case, runID, mode string, evaluation underwriting.Evaluation, row domain.AssessmentRequest) (pinnedRunContext, *customercontext.Response, error) {
	value, err := decodePinnedRunContext(raw)
	if err != nil {
		return pinnedRunContext{}, nil, err
	}
	if c.CustomerID == nil || c.AccountRef == nil || c.RawAccountID == nil || c.DetectionID == nil || value.TemplateLimitation == "" && (row.SourceTransactionID == nil || row.SourceCardID == nil) || value.Kind != agent.Shortfall || value.CaseID != c.ID || value.BindingID != c.BindingID || value.RunID != runID || value.Mode != mode || value.CustomerID != *c.CustomerID || value.AccountRef != *c.AccountRef || value.AccountRef != domain.InternalAccountRef(value.CustomerID) || value.RawAccountID != *c.RawAccountID || value.RawCardID != valueOrEmpty(row.SourceCardID) || value.ShortfallID != *c.DetectionID || value.AssessmentID != evaluation.ID || value.TransactionID != evaluation.TransactionID || row.SourceTransactionID != nil && value.TransactionID != *row.SourceTransactionID {
		return pinnedRunContext{}, nil, errors.New("private_run_scope_mismatch")
	}
	if len(value.CustomerContext) == 0 {
		return value, nil, nil
	}
	var contextValue customercontext.Response
	if json.Unmarshal(value.CustomerContext, &contextValue) != nil || contextValue.SourceMode != mode || contextValue.CustomerID != value.CustomerID {
		return pinnedRunContext{}, nil, errors.New("private_customer_context_invalid")
	}
	return value, &contextValue, nil
}

func validatePrivateCollectionContext(raw []byte, c domain.Case, runID, mode string, bridgeID string, version int64, actions []string) (pinnedRunContext, error) {
	value, err := decodePinnedRunContext(raw)
	if err != nil {
		return pinnedRunContext{}, err
	}
	if c.CustomerID == nil || c.AccountRef == nil || c.BridgeID == nil || value.Kind != agent.Collection || value.CaseID != c.ID || value.BindingID != c.BindingID || value.RunID != runID || value.Mode != mode || value.CustomerID != *c.CustomerID || value.AccountRef != *c.AccountRef || value.AccountRef != domain.InternalAccountRef(value.CustomerID) || value.BridgeID != bridgeID || value.BridgeVersion == nil || *value.BridgeVersion != version || !sameActionSet(value.EffectiveActions, actions) {
		return pinnedRunContext{}, errors.New("private_collection_scope_mismatch")
	}
	return value, nil
}

func sameJSON(left, right []byte) bool {
	decode := func(raw []byte) (any, bool) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil, false
		}
		return value, true
	}
	leftValue, leftOK := decode(left)
	rightValue, rightOK := decode(right)
	return leftOK && rightOK && reflect.DeepEqual(leftValue, rightValue)
}
