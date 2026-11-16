package httpapi

import (
	"encoding/json"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
)

func TestClosedCollectionEncodesEmptyActions(t *testing.T) {
	bridge := servicing.Bridge{ID: "bridge-closed", ObligationID: "bridge-closed", Mode: "DEMO", SourceBindingID: "test", CustomerID: "customer", AccountRef: "customer:customer", Version: 28, AsOf: time.Now().UTC(), Currency: "USD", Lifecycle: "REPAID", HoldReasons: []string{}, AllowedNextActions: []string{}, Operations: []servicing.Operation{}}
	rawBridge, err := json.Marshal(bridge)
	if err != nil {
		t.Fatal(err)
	}
	rawState, err := json.Marshal(map[string]any{"bridge": json.RawMessage(rawBridge), "allowed_next_actions": nil})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := postgres.CollectionSnapshot{BridgeVersion: 28, State: rawState}
	view, err := safeCollection(snapshot, false, false)
	if err != nil {
		t.Fatal(err)
	}
	response := caseDetail{NextActions: append([]string{}, view.EffectiveActions...), Collection: &view}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		t.Fatal("response encoding failed")
	}
	actions, ok := decoded["next_actions"].([]any)
	if !ok || len(actions) != 0 {
		t.Fatalf("closed actions = %v, want empty array", decoded["next_actions"])
	}
	nested := decoded["collection"].(map[string]any)
	if _, ok := nested["effective_allowed_actions"].([]any); !ok {
		t.Fatal("closed collection actions are null")
	}
}
