package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
)

// RefreshDelinquency persists the UTC stage and first effective default date.
// Caller holds the common facility/customer/bridge locks.
func RefreshDelinquency(ctx context.Context, tx pgx.Tx, mode domain.Mode, bridgeID string, asOf time.Time) error {
	if mode != domain.ModeDemo && mode != domain.ModeSandbox {
		return errors.New("delinquency_mode_invalid")
	}
	var customerID string
	var dueOn time.Time
	var principal, contractFee, lateFee int64
	var oldStage string
	var oldDefault *time.Time
	var version int64
	err := tx.QueryRow(ctx, `SELECT customer_id,due_on,principal_owed_cents,contract_fee_owed_cents,late_fee_owed_cents,delinquency_stage,first_defaulted_on,version FROM servicing.bridges WHERE mode=$1 AND id=$2`, string(mode), bridgeID).Scan(&customerID, &dueOn, &principal, &contractFee, &lateFee, &oldStage, &oldDefault, &version)
	if err != nil {
		return errors.New("delinquency_bridge_unavailable")
	}
	debt, err := domain.AddMoney(domain.Money(principal), domain.Money(contractFee))
	if err != nil {
		return errors.New("delinquency_balance_invalid")
	}
	debt, err = domain.AddMoney(debt, domain.Money(lateFee))
	if err != nil {
		return errors.New("delinquency_balance_invalid")
	}
	dpd, err := domain.DaysPastDue(asOf, dueOn, debt)
	if err != nil {
		return err
	}
	stage, err := domain.StageForDays(dpd)
	if err != nil {
		return err
	}
	var defaultDate any
	if oldDefault != nil {
		defaultDate = *oldDefault
	} else if debt > 0 && dpd >= 61 {
		defaultDate = domain.FirstDefaultOn(dueOn, nil)
	}
	defaultChanged := oldDefault == nil && defaultDate != nil
	if oldStage == string(stage) && !defaultChanged {
		return nil
	}
	version++
	if _, err = tx.Exec(ctx, `UPDATE servicing.bridges SET delinquency_stage=$3,first_defaulted_on=COALESCE(first_defaulted_on,$4),version=$5,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, string(mode), bridgeID, string(stage), defaultDate, version); err != nil {
		return errors.New("delinquency_update_failed")
	}
	if defaultChanged {
		if _, err = tx.Exec(ctx, `UPDATE servicing.customers SET defaulted_at=COALESCE(defaulted_at,$2),version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$3`, string(mode), defaultDate, customerID); err != nil {
			return errors.New("delinquency_customer_default_update_failed")
		}
	}
	summary, _ := json.Marshal(map[string]any{"customer_id": customerID, "account_ref": "customer:" + customerID, "bridge_id": bridgeID, "bridge_version": version, "delinquency": stage, "days_past_due": dpd, "first_defaulted_on": defaultDate})
	parent := bridgeID
	if _, err = AppendChange(ctx, tx, domain.Change{Mode: mode, ResourceType: "BRIDGE", ResourceID: bridgeID, ResourceVersion: version, ChangeType: "DELINQUENCY_CHANGED", Summary: summary}, &parent, &version); err != nil {
		return err
	}
	return nil
}
