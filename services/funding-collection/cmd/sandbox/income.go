package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// simulateIncome creates one external test deposit only after the browser loan
// has settled. Repeated invocation retrieves the original deposit, never sends
// another simulation when an intent already exists.
func simulateIncome(ctx context.Context, api *sandboxAPI, cfg credentials, manifest, runID, name string) error {
	if name != "journey-income" && name != "journey-recovery-income" {
		return errors.New("sandbox_income_scenario_invalid")
	}
	var local localRuntime
	if err := readPrivateJSON(localRuntimePath(manifest), &local, 65536); err != nil {
		return err
	}
	if validateLocalProfile(local) != nil || local.RunID != runID {
		return errors.New("sandbox_income_installation_mismatch")
	}
	intent, err := savedIntent(manifest, runID, cfg.platform, name)
	if err != nil {
		return err
	}
	if intent.RequestID == "" {
		pool, err := pgxpool.New(ctx, local.URLs["funding"])
		if err != nil {
			return errors.New("sandbox_income_ledger_unavailable")
		}
		defer pool.Close()
		var verified bool
		if pool.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(lifecycle='ACTIVE' AND ever_funded AND principal_owed_cents=700000 AND contract_fee_owed_cents=19000 AND late_fee_owed_cents=0) FROM servicing.bridges WHERE mode='SANDBOX' AND customer_id=$1`, local.CustomerID).Scan(&verified) != nil || !verified {
			return errors.New("sandbox_income_requires_exact_settled_browser_loan")
		}
	}
	return simulateOwnedDeposit(ctx, api, cfg, manifest, runID, name, 7190)
}
