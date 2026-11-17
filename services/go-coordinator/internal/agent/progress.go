package agent

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
)

type collectionProgress struct {
	BridgeID      string `json:"bridge_id"`
	BridgeVersion int64  `json:"bridge_version"`
	// Lifecycle preserves the exact owner value without coordinator inference.
	Lifecycle          string     `json:"lifecycle"`
	Delinquency        string     `json:"delinquency"`
	DaysPastDue        *int       `json:"days_past_due"`
	PrincipalOwedCents *int64     `json:"principal_owed_cents"`
	ContractFeeOwed    *int64     `json:"contract_fee_owed_cents"`
	LateFeeOwed        *int64     `json:"late_fee_owed_cents"`
	AccrualCurrent     *bool      `json:"accrual_current"`
	DebtStale          bool       `json:"debt_stale"`
	DebtAsOf           *time.Time `json:"debt_as_of"`
	HistoryRowCount    int        `json:"history_row_count"`
	HistoryComplete    bool       `json:"history_complete"`
	HistoryTruncated   bool       `json:"history_truncated"`
	OperationCount     int        `json:"operation_count"`
	AllowedActions     []string   `json:"effective_allowed_actions"`
}

func compactProgress(version int64, state []byte) (collectionProgress, error) {
	var observation struct {
		Bridge          servicing.Bridge  `json:"bridge"`
		Attempts        []json.RawMessage `json:"attempts"`
		NextCursor      *string           `json:"next_cursor"`
		HistoryComplete bool              `json:"history_complete"`
		DebtStale       bool              `json:"debt_stale"`
		DebtAsOf        *time.Time        `json:"debt_as_of"`
		AllowedActions  []string          `json:"allowed_next_actions"`
	}
	if err := json.Unmarshal(state, &observation); err != nil || observation.Bridge.Version != version {
		return collectionProgress{}, errors.New("collection_state_invalid")
	}
	return collectionProgress{
		BridgeID:           observation.Bridge.ID,
		BridgeVersion:      version,
		Lifecycle:          observation.Bridge.Lifecycle,
		Delinquency:        observation.Bridge.Delinquency,
		DaysPastDue:        observation.Bridge.DaysPastDue,
		PrincipalOwedCents: observation.Bridge.PrincipalOwedCents,
		ContractFeeOwed:    observation.Bridge.ContractFeeOwedCents,
		LateFeeOwed:        observation.Bridge.LateFeeOwedCents,
		AccrualCurrent:     observation.Bridge.AccrualCurrent,
		DebtStale:          observation.DebtStale,
		DebtAsOf:           observation.DebtAsOf,
		HistoryRowCount:    len(observation.Attempts),
		HistoryComplete:    observation.HistoryComplete,
		HistoryTruncated:   !observation.HistoryComplete || observation.NextCursor != nil,
		OperationCount:     len(observation.Bridge.Operations),
		AllowedActions:     uniqueSorted(observation.AllowedActions),
	}, nil
}
