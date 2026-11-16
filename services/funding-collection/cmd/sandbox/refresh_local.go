package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	upstreamfixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

// refreshLocalSnapshots publishes fresh immutable snapshots. Facility values
// come from the actual owned servicing ledger; policy thresholds stay intact.
func refreshLocalSnapshots(ctx context.Context, manifest, runID string, customerContext bool) error {
	var local localRuntime
	if err := readPrivateJSON(localRuntimePath(manifest), &local, 65536); err != nil {
		return err
	}
	if err := validateLocalProfile(local); err != nil {
		return err
	}
	if local.RunID != runID {
		return errors.New("sandbox_refresh_run_mismatch")
	}
	root, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, local.URLs["funding"])
	if err != nil {
		return errors.New("sandbox_ledger_reader_unavailable")
	}
	defer pool.Close()
	var total, reserve, principal, reserved, version int64
	if pool.QueryRow(ctx, `SELECT total_cents,reserve_bps,principal_owed_cents,reserved_principal_cents,version FROM servicing.facilities WHERE mode='SANDBOX' AND id=$1`, local.FacilityID).Scan(&total, &reserve, &principal, &reserved, &version) != nil {
		return errors.New("sandbox_current_ledger_unavailable")
	}
	now := time.Now().UTC()
	sourceVersion := now.Format(time.RFC3339Nano)
	reference := policy.SourceReference{System: "servicing-owned-ledger", RecordID: local.FacilityID, Version: strconv.FormatInt(version, 10), EffectiveAt: now}
	facility, _ := json.MarshalIndent(map[string]any{"schema_version": 1, "source_mode": "SANDBOX", "source_id": "sandbox-live-facility-" + runID, "source_version": sourceVersion, "captured_at": now, "source_refs": []policy.SourceReference{reference}, "facility_id": local.FacilityID, "currency": "USD", "facility_total_cents": total, "reserve_floor_bps": reserve, "funded_principal_cents": principal, "reserved_principal_cents": reserved, "ledger_version": strconv.FormatInt(version, 10), "ledger_complete": true}, "", "  ")
	facilityPath := filepath.Join(filepath.Dir(manifest), "underwriting-live-facility.json")
	if err := writePrivateFile(facilityPath, facility); err != nil {
		return err
	}
	environment := map[string]string{"DATABASE_URL": local.URLs["underwriting"], "UNDERWRITING_MODE": "SANDBOX", "UNDERWRITING_FACILITY_ID": local.FacilityID}
	if err := runLocalCommand(ctx, root, "underwriting-engine", []string{"./cmd/import-snapshot", "--kind", "facility", "--file", facilityPath}, environment, ""); err != nil {
		return err
	}
	if !customerContext {
		return nil
	}
	var funded bool
	if pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.bridges WHERE mode='SANDBOX' AND customer_id=$1 AND ever_funded)`, local.CustomerID).Scan(&funded) != nil || funded {
		return errors.New("sandbox_test_history_refresh_requires_unfunded_customer")
	}
	var manifestData sandboxManifest
	if err := readPrivateJSON(manifest, &manifestData, 1_000_000); err != nil {
		return err
	}
	if manifestData.Owner != manifestOwner || manifestData.RunID != runID || manifestData.Environment != "SANDBOX" {
		return errors.New("sandbox_refresh_manifest_mismatch")
	}
	borrower := manifestData.Intents["account-borrower"].ResourceID
	card := manifestData.Intents["card-borrower"].ResourceID
	if borrower == "" || card == "" {
		return errors.New("sandbox_refresh_mapping_unavailable")
	}
	synthetic, err := upstreamfixture.New(now, "SANDBOX")
	if err != nil {
		return errors.New("sandbox_test_context_unavailable")
	}
	snapshot := synthetic.Input.Customer
	snapshot.CustomerID = local.CustomerID
	snapshot.AccountID = borrower
	snapshot.CardID = card
	invoiceID := "sandbox-invoice-" + runID
	snapshot.SourceRefs = []policy.SourceReference{{System: "sandbox-synthetic-history", RecordID: runID, Version: "1", EffectiveAt: now}, {System: "sandbox-synthetic-invoicing", RecordID: invoiceID, Version: "1", EffectiveAt: local.CapturedAt}, {System: "sandbox-test-purchase-classification", RecordID: runID + "-classification", Version: sourceVersion, EffectiveAt: now}}
	snapshot.Receivables[0].InvoiceID = invoiceID
	snapshot.Receivables[0].SourceRef = snapshot.SourceRefs[1]
	snapshot.Classifications = []policy.Classification{}
	parents := []string{}
	for _, name := range []string{"contract-decline", "callback-decline", "journey-decline", "journey-recovery-decline"} {
		if id := manifestData.Intents[name].ParentTransactionID; id != "" {
			parents = append(parents, id)
		}
	}
	sort.Strings(parents)
	for _, parent := range parents {
		snapshot.Classifications = append(snapshot.Classifications, policy.Classification{TransactionID: parent, MerchantCategory: "suppliers", SourceRef: snapshot.SourceRefs[2]})
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return errors.New("sandbox_test_context_invalid")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil {
		return errors.New("sandbox_test_context_invalid")
	}
	delete(document, "snapshot_id")
	delete(document, "snapshot_version")
	document["schema_version"] = json.RawMessage(`1`)
	document["source_id"], _ = json.Marshal("sandbox-synthetic-current-customer-" + runID)
	document["source_version"], _ = json.Marshal(sourceVersion)
	encoded, _ := json.MarshalIndent(document, "", "  ")
	path := filepath.Join(filepath.Dir(manifest), "underwriting-current-customer.json")
	if err := writePrivateFile(path, encoded); err != nil {
		return err
	}
	return runLocalCommand(ctx, root, "underwriting-engine", []string{"./cmd/import-snapshot", "--kind", "customer", "--file", path}, environment, "")
}
