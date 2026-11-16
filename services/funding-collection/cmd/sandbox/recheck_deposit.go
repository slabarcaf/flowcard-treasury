package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

// recheckHeldDeposit is explicit, run-owned maintenance. The adapter performs
// canonical GETs only; ordinary workers decide any subsequent collection.
func recheckHeldDeposit(ctx context.Context, cfg credentials, manifest, runID, adminFile, databaseName string, out io.Writer) error {
	var local localRuntime
	if err := readPrivateJSON(localRuntimePath(manifest), &local, 65536); err != nil {
		return err
	}
	if err := validateLocalProfile(local); err != nil {
		return err
	}
	if local.RunID != runID || local.DatabaseName != databaseName ||
		!config.ValidOwnedDatabaseName(databaseName, "flowcard_funding_demo_sandbox") {
		return errors.New("sandbox_recheck_target_mismatch")
	}
	var saved sandboxManifest
	if err := readPrivateJSON(manifest, &saved, 1_000_000); err != nil {
		return err
	}
	if saved.Owner != manifestOwner || saved.RunID != runID || saved.Environment != "SANDBOX" ||
		saved.Intents["card-borrower"].ResourceID == "" {
		return errors.New("sandbox_recheck_manifest_mismatch")
	}
	target, err := localAdminURL(adminFile)
	if err != nil {
		return err
	}
	target.Path = "/" + databaseName
	pool, err := pgxpool.New(ctx, target.String())
	if err != nil {
		return errors.New("sandbox_recheck_admin_unavailable")
	}
	defer pool.Close()
	var actual, marker string
	if pool.QueryRow(ctx, `SELECT current_database(),COALESCE(shobj_description(oid,'pg_database'),'')
		FROM pg_database WHERE datname=current_database()`).Scan(&actual, &marker) != nil ||
		actual != databaseName || marker != local.Marker {
		return errors.New("sandbox_recheck_database_not_owned")
	}
	if err := postgres.RunMigrations(ctx, pool); err != nil {
		return errors.New("sandbox_recheck_migration_failed")
	}
	// Refuse ambiguity. This command never accepts client-supplied amounts,
	// customer IDs, provider scope, or a replacement deposit.
	rows, err := pool.Query(ctx, `SELECT b.id::text,b.version,d.id::text,d.provider_deposit_id,
		b.canonical_account_id,f.funding_account_ref
		FROM servicing.bridges b JOIN servicing.deposits d ON d.mode=b.mode AND d.customer_id=b.customer_id
		JOIN servicing.facilities f ON f.mode=b.mode AND f.id=b.facility_id
		WHERE b.mode='SANDBOX' AND b.customer_id=$1 AND b.facility_id=$2
		AND b.ever_funded AND b.lifecycle IN ('ACTIVE','COLLECTING','REOPENED_HELD')
		AND d.status='HELD' ORDER BY b.id,d.id LIMIT 2`, local.CustomerID, local.FacilityID)
	if err != nil {
		return errors.New("sandbox_recheck_candidate_unavailable")
	}
	in := postgres.DepositTimestampRecheck{Mode: domain.ModeSandbox,
		FacilityID: local.FacilityID, CustomerID: local.CustomerID,
		CardID: saved.Intents["card-borrower"].ResourceID}
	var providerID, account, fundingAccount string
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&in.BridgeID, &in.BridgeVersion, &in.DepositID, &providerID,
			&account, &fundingAccount); err != nil {
			rows.Close()
			return errors.New("sandbox_recheck_candidate_invalid")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 1 {
		return errors.New("sandbox_recheck_requires_one_held_deposit")
	}
	adapter, err := provider.NewFromConfig(config.Config{Mode: "SANDBOX",
		ProviderMode: "AIRWALLEX_SANDBOX", ProviderBaseURL: sandboxHost,
		FacilityID: local.FacilityID, SourceBindingID: local.SourceBindingID,
		AirwallexClientID: cfg.clientID, AirwallexAPIKey: cfg.apiKey, AirwallexLoginAs: cfg.platform,
		SandboxExecutionContractFile: filepath.Join(filepath.Dir(manifest), "execution-contract.json")},
		fundingAccount, cfg.platform, nil, time.Now)
	if err != nil {
		return errors.New("sandbox_recheck_provider_unavailable")
	}
	in.Observation, err = adapter.Deposit(ctx, account, providerID)
	if err != nil {
		return errors.New("sandbox_recheck_canonical_get_failed")
	}
	in.Now = time.Now().UTC()
	id, err := postgres.NewRepository(pool).RecheckDepositTimestamp(ctx, in)
	if err != nil {
		return err
	}
	receipt := map[string]any{"run_id": runID, "bridge_id": in.BridgeID,
		"deposit_id": in.DepositID, "provider_deposit_id": providerID, "recheck_id": id,
		"financial_movement": false, "original_facts_preserved": true}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil || writePrivateFile(filepath.Join(filepath.Dir(manifest), "deposit-timestamp-recheck.json"), body) != nil {
		return errors.New("sandbox_recheck_receipt_write_failed")
	}
	return json.NewEncoder(out).Encode(receipt)
}
