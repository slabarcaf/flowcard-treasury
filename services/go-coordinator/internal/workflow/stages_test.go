package workflow

import (
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
)

func TestRepaidCollectionWithCancelledUnsentOperation(t *testing.T) {
	now := time.Now().UTC()
	current, zero := true, int64(0)
	historicalReason := "OPERATION_SCOPE_INVALID"
	for _, test := range []struct {
		name  string
		state string
		want  string
	}{
		{name: "cancelled unsent preserves historical reason", state: "CANCELLED_UNSENT", want: "CLOSED"},
		{name: "held operation still blocks closure", state: "HELD", want: "ACTION_REQUIRED"},
		{name: "settled operation with conflicting hold still blocks closure", state: "SETTLED", want: "ACTION_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := servicing.Bridge{
				Mode: "SANDBOX", Lifecycle: "REPAID", AccrualCurrent: &current, DebtAsOf: &now,
				PrincipalOwedCents: &zero, ContractFeeOwedCents: &zero, LateFeeOwedCents: &zero,
				Operations: []servicing.Operation{{State: test.state, HoldReason: &historicalReason}},
			}
			got := refreshDisposition(bridge, true, []string{})
			if got.Status != test.want || got.NextStage != "MONITORING" {
				t.Fatalf("refreshDisposition(%s with historical reason) = %s/%s, want %s/MONITORING", test.state, got.Status, got.NextStage, test.want)
			}
		})
	}
}
