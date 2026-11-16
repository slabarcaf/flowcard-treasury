package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

type localInventory struct {
	Environment      string `json:"environment"`
	RunID            string `json:"run_id"`
	Database         string `json:"owned_database"`
	RestrictedLogins int    `json:"restricted_logins"`
	BrowserUsers     int    `json:"browser_users"`
	Operations       int    `json:"operations"`
	PreserveAudit    bool   `json:"preserve_financial_audit"`
	ProviderCleanup  string `json:"provider_cleanup"`
}

func inspectLocal(ctx context.Context, manifest, runID, databaseName string, out io.Writer) (localRuntime, error) {
	var local localRuntime
	if readPrivateJSON(localRuntimePath(manifest), &local, 65536) != nil || validateLocalProfile(local) != nil || local.RunID != runID || local.DatabaseName != databaseName {
		return local, errors.New("sandbox_inventory_target_invalid")
	}
	pool, err := pgxpool.New(ctx, local.URLs["funding"])
	if err != nil {
		return local, errors.New("sandbox_inventory_storage_unavailable")
	}
	defer pool.Close()
	var actual, marker string
	if pool.QueryRow(ctx, `SELECT current_database(),COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=current_database()`).Scan(&actual, &marker) != nil || actual != databaseName || marker != local.Marker {
		return local, errors.New("sandbox_inventory_marker_mismatch")
	}
	var operations int
	var audit bool
	if pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM servicing.operations), EXISTS(SELECT 1 FROM servicing.operations) OR EXISTS(SELECT 1 FROM servicing.ledger_transactions) OR EXISTS(SELECT 1 FROM servicing.bridges)`).Scan(&operations, &audit) != nil {
		return local, errors.New("sandbox_inventory_unavailable")
	}
	inventory := localInventory{"SANDBOX", runID, databaseName, len(local.Roles), len(local.Users), operations, audit, "RETAIN_ALL_PROVIDER_OBJECTS_AND_TEST_FUNDS"}
	if json.NewEncoder(out).Encode(inventory) != nil {
		return local, errors.New("sandbox_report_write_failed")
	}
	return local, nil
}

func cleanupAcceptanceLocal(ctx context.Context, manifest, runID, adminFile, databaseName string, out io.Writer) error {
	local, err := inspectLocal(ctx, manifest, runID, databaseName, out)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, local.URLs["funding"])
	if err != nil {
		return errors.New("sandbox_cleanup_storage_unavailable")
	}
	defer pool.Close()
	var audit bool
	var readerPID int
	query := `SELECT pg_backend_pid(),EXISTS(SELECT 1 FROM servicing.operations) OR EXISTS(SELECT 1 FROM servicing.ledger_transactions) OR EXISTS(SELECT 1 FROM servicing.bridges)`
	if pool.QueryRow(ctx, query).Scan(&readerPID, &audit) != nil {
		return errors.New("sandbox_cleanup_storage_unavailable")
	}
	if audit {
		return errors.New("sandbox_cleanup_refused_financial_audit_retained")
	}
	target, err := localAdminURL(adminFile)
	if err != nil {
		return err
	}
	admin, err := pgxpool.New(ctx, target.String())
	if err != nil {
		return errors.New("sandbox_cleanup_admin_unavailable")
	}
	defer admin.Close()
	var owner, marker string
	if admin.QueryRow(ctx, `SELECT current_user,COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, databaseName).Scan(&owner, &marker) != nil || owner != local.AdminOwner || marker != local.Marker {
		return errors.New("sandbox_cleanup_database_not_owned")
	}
	// Prevent new connections before the final audit check. Existing runtimes
	// cause refusal rather than termination. Only the exact owned database is gated.
	quoted := pgx.Identifier{databaseName}.Sanitize()
	if _, err := admin.Exec(ctx, "ALTER DATABASE "+quoted+" ALLOW_CONNECTIONS false"); err != nil {
		return errors.New("sandbox_cleanup_connection_gate_failed")
	}
	cleaned := false
	defer func() {
		if !cleaned {
			restore, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = admin.Exec(restore, "ALTER DATABASE "+quoted+" ALLOW_CONNECTIONS true")
		}
	}()
	var other int
	if admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND pid<>$2`, databaseName, readerPID).Scan(&other) != nil || other != 0 {
		return errors.New("sandbox_cleanup_refused_active_runtime")
	}
	if pool.QueryRow(ctx, query).Scan(&readerPID, &audit) != nil {
		return errors.New("sandbox_cleanup_storage_unavailable")
	}
	if audit {
		return errors.New("sandbox_cleanup_refused_financial_audit_retained")
	}
	pool.Close()
	if err := cleanupLocalDatabase(ctx, local, adminFile); err != nil {
		return err
	}
	cleaned = true
	return nil
}

type journeyProof struct {
	Environment        string    `json:"environment"`
	VerifiedAt         time.Time `json:"verified_at"`
	CaseID             string    `json:"case_id"`
	AssessmentID       string    `json:"assessment_id"`
	OfferID            string    `json:"offer_id"`
	BridgeID           string    `json:"bridge_id"`
	Lifecycle          string    `json:"lifecycle"`
	PrincipalPaid      int64     `json:"principal_paid_cents"`
	ContractFeePaid    int64     `json:"contract_fee_paid_cents"`
	Outstanding        int64     `json:"outstanding_cents"`
	Reserved           int64     `json:"reserved_cents"`
	SettledTransfers   int       `json:"provider_get_verified_settled_transfers"`
	CancelledUnsent    int       `json:"retained_cancelled_unsent_operations"`
	LedgerTransactions int       `json:"balanced_ledger_transactions"`
	DistinctActors     bool      `json:"distinct_customer_operator_verified"`
	DeclineVerified    bool      `json:"original_decline_get_verified"`
	DepositVerified    bool      `json:"repayment_external_deposit_get_verified"`
	FundingCash        int64     `json:"funding_available_cents"`
	BorrowerCash       int64     `json:"borrower_available_cents"`
	CardRetried        bool      `json:"card_automatically_retried"`
}

// verifyJourney uses restricted reads and fresh provider GETs only. It never
// creates a transfer, changes a book, or converts a pending receipt to success.
func verifyJourney(ctx context.Context, cfg credentials, manifest, runID, databaseName string, out io.Writer) error {
	local, err := inspectLocal(ctx, manifest, runID, databaseName, io.Discard)
	if err != nil {
		return err
	}
	var payment provider.SandboxPaymentContract
	var execution provider.SandboxExecutionContract
	dir := filepath.Dir(manifest)
	if readPrivateJSON(filepath.Join(dir, "shortfall-contract.json"), &payment, 131072) != nil || readPrivateJSON(filepath.Join(dir, "execution-contract.json"), &execution, 131072) != nil || payment.RunID != runID || execution.RunID != runID || execution.CustomerID != local.CustomerID || execution.FacilityID != local.FacilityID {
		return errors.New("sandbox_journey_profile_mismatch")
	}
	adapter, err := provider.NewAirwallexAdapter(provider.AdapterOptions{Mode: "SANDBOX", ProviderMode: "AIRWALLEX_SANDBOX", BaseURL: "https://api.sandbox.airwallex.com", FundingAccountID: execution.FundingAccountID, ClientID: cfg.clientID, APIKey: cfg.apiKey, LoginAs: cfg.platform, SandboxPaymentContract: &payment, SandboxExecutionContract: &execution})
	if err != nil {
		return errors.New("sandbox_journey_reader_unavailable")
	}
	pool, err := pgxpool.New(ctx, local.URLs["funding"])
	if err != nil {
		return errors.New("sandbox_journey_storage_unavailable")
	}
	defer pool.Close()
	proof := journeyProof{Environment: "SANDBOX", VerifiedAt: time.Now().UTC()}
	var parent, customer, account string
	var holds []string
	var activeHold bool
	if pool.QueryRow(ctx, `SELECT b.id::text,b.originating_case_id,b.evaluation_id,b.offer_id::text,b.lifecycle,b.principal_paid_cents,b.contract_fee_paid_cents,b.principal_owed_cents+b.contract_fee_owed_cents+b.late_fee_owed_cents,b.hold_reasons,c.active_hold,b.transaction_id,b.customer_id,b.canonical_account_id FROM servicing.bridges b JOIN servicing.customers c ON c.mode=b.mode AND c.id=b.customer_id WHERE b.mode='SANDBOX' AND b.customer_id=$1 AND b.ever_funded`, local.CustomerID).Scan(&proof.BridgeID, &proof.CaseID, &proof.AssessmentID, &proof.OfferID, &proof.Lifecycle, &proof.PrincipalPaid, &proof.ContractFeePaid, &proof.Outstanding, &holds, &activeHold, &parent, &customer, &account) != nil || proof.Lifecycle != "REPAID" || proof.Outstanding != 0 || proof.PrincipalPaid != 700000 || proof.ContractFeePaid != 19000 || activeHold || len(holds) != 0 {
		return errors.New("sandbox_journey_debt_or_hold_unverified")
	}
	if pool.QueryRow(ctx, `SELECT reserved_principal_cents+reserved_fee_cents FROM servicing.customers WHERE mode='SANDBOX' AND id=$1`, customer).Scan(&proof.Reserved) != nil || proof.Reserved != 0 {
		return errors.New("sandbox_journey_reservations_unreleased")
	}
	var actorCount int
	if pool.QueryRow(ctx, `SELECT count(DISTINCT authentication_subject) FROM servicing.actor_decisions WHERE mode='SANDBOX' AND offer_id=$1 AND ((actor_type='CUSTOMER' AND decision='ACCEPT') OR (actor_type='OPERATOR' AND decision='APPROVE'))`, proof.OfferID).Scan(&actorCount) != nil || actorCount != 2 {
		return errors.New("sandbox_journey_actors_unverified")
	}
	var released int64
	if pool.QueryRow(ctx, `SELECT (SELECT reserved_principal_cents FROM servicing.facilities WHERE mode='SANDBOX' AND id=$1)+(SELECT COALESCE(sum(reserved_debit_cents),0) FROM servicing.authorities WHERE mode='SANDBOX' AND customer_id=$2)+(SELECT COALESCE(sum(reserved_cents),0) FROM servicing.receivables WHERE mode='SANDBOX' AND customer_id=$2)+(SELECT COALESCE(sum(active_cents),0) FROM servicing.bridge_invoice_allocations WHERE mode='SANDBOX' AND bridge_id=$3)`, local.FacilityID, customer, proof.BridgeID).Scan(&released) != nil || released != 0 {
		return errors.New("sandbox_journey_budget_or_invoice_reservations_unreleased")
	}
	proof.DistinctActors = true
	observation, err := adapter.Payment(ctx, account, parent)
	if err != nil || observation.AttemptedAmountCents != 900000 || observation.AuthorizationEventID == "" {
		return errors.New("sandbox_journey_decline_unverified")
	}
	coordinator, err := pgxpool.New(ctx, local.URLs["coordinator"])
	if err != nil {
		return errors.New("sandbox_journey_case_unavailable")
	}
	var detectionID string
	err = coordinator.QueryRow(ctx, `SELECT c.detection_id FROM coordinator.cases c JOIN coordinator.assessments a ON a.case_id=c.id WHERE c.id=$1 AND c.customer_id=$2 AND a.saved_assessment_id=$3`, proof.CaseID, customer, proof.AssessmentID).Scan(&detectionID)
	coordinator.Close()
	if err != nil {
		return errors.New("sandbox_journey_assessment_link_unverified")
	}
	shortfall, err := pgxpool.New(ctx, local.URLs["shortfall"])
	if err != nil {
		return errors.New("sandbox_journey_detection_unavailable")
	}
	var matched bool
	err = shortfall.QueryRow(ctx, `SELECT count(*)=1 FROM shortfall.detections WHERE id::text=$1 AND environment='SANDBOX' AND customer_id=$2 AND account_id=$3 AND transaction_id=$4 AND canonical_event_id=$5 AND outcome='SHORTFALL_DETECTED' AND attempted_amount_cents=900000 AND shortfall_cents=700000`, detectionID, customer, account, parent, observation.AuthorizationEventID).Scan(&matched)
	shortfall.Close()
	if err != nil || !matched {
		return errors.New("sandbox_journey_original_detection_unverified")
	}
	proof.DeclineVerified = true
	rows, err := pool.Query(ctx, `SELECT id::text,kind,source_account_ref,destination_account_ref,amount_cents,currency,request_id,payload_hash,bridge_id::text,COALESCE(provider_object_id,''),state,late_fee_cents,contract_fee_cents,principal_cents FROM servicing.operations WHERE mode='SANDBOX' AND bridge_id=$1 ORDER BY created_at,id`, proof.BridgeID)
	if err != nil {
		return errors.New("sandbox_journey_operations_unavailable")
	}
	type savedOperation struct {
		op            provider.Operation
		object, state string
	}
	var operations []savedOperation
	for rows.Next() {
		var saved savedOperation
		saved.op.Mode = "SANDBOX"
		if rows.Scan(&saved.op.ID, &saved.op.Kind, &saved.op.SourceAccountRef, &saved.op.DestinationAccountRef, &saved.op.AmountCents, &saved.op.Currency, &saved.op.RequestID, &saved.op.PayloadHash, &saved.op.BridgeID, &saved.object, &saved.state, &saved.op.LateFeeCents, &saved.op.ContractFeeCents, &saved.op.PrincipalCents) != nil {
			rows.Close()
			return errors.New("sandbox_journey_operation_invalid")
		}
		operations = append(operations, saved)
	}
	rows.Close()
	if rows.Err() != nil {
		return errors.New("sandbox_journey_operations_unavailable")
	}
	kinds := map[provider.OperationKind]int{}
	for _, saved := range operations {
		if saved.state == "CANCELLED_UNSENT" && saved.object == "" {
			proof.CancelledUnsent++
			continue
		}
		if saved.state != "SETTLED" || saved.object == "" {
			return errors.New("sandbox_journey_operation_unresolved")
		}
		result, err := adapter.Retrieve(ctx, saved.op, saved.object)
		if err != nil || result.Receipt.Status != "SETTLED" || result.Receipt.ProviderFeeCents == nil || *result.Receipt.ProviderFeeCents != 0 {
			return errors.New("sandbox_journey_transfer_get_unverified")
		}
		kinds[saved.op.Kind]++
		proof.SettledTransfers++
	}
	if proof.SettledTransfers != 3 || kinds[provider.OperationFund] != 1 || kinds[provider.OperationCollectFee] != 1 || kinds[provider.OperationCollectPrincipal] != 1 {
		return errors.New("sandbox_journey_transfer_count_invalid")
	}
	var depositID string
	if pool.QueryRow(ctx, `SELECT DISTINCT d.provider_deposit_id FROM servicing.deposits d JOIN servicing.collection_attempts a ON a.mode=d.mode AND a.deposit_id=d.id WHERE a.mode='SANDBOX' AND a.bridge_id=$1 AND a.state='SETTLED'`, proof.BridgeID).Scan(&depositID) != nil {
		return errors.New("sandbox_journey_collection_source_unavailable")
	}
	deposit, err := adapter.Deposit(ctx, account, depositID)
	if err != nil || deposit.NetCents != 719000 {
		return errors.New("sandbox_journey_deposit_get_unverified")
	}
	proof.DepositVerified = true
	if pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT t.id FROM servicing.ledger_transactions t JOIN servicing.ledger_lines l ON l.mode=t.mode AND l.transaction_id=t.id WHERE t.mode='SANDBOX' AND t.bridge_id=$1 GROUP BY t.id HAVING sum(l.debit_cents)=sum(l.credit_cents)) balanced`, proof.BridgeID).Scan(&proof.LedgerTransactions) != nil || proof.LedgerTransactions < 3 {
		return errors.New("sandbox_journey_balanced_ledger_unverified")
	}
	for _, scope := range []string{execution.FundingAccountID, execution.BorrowerAccountID} {
		balance, err := adapter.Balance(ctx, scope)
		if err != nil {
			return errors.New("sandbox_journey_balance_unavailable")
		}
		if scope == execution.FundingAccountID {
			proof.FundingCash = balance.AvailableCents
		} else {
			proof.BorrowerCash = balance.AvailableCents
		}
	}
	raw, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return errors.New("sandbox_report_write_failed")
	}
	if err := writePrivateFile(filepath.Join(dir, "journey-proof.json"), raw); err != nil {
		return err
	}
	if _, err := out.Write(append(raw, '\n')); err != nil {
		return errors.New("sandbox_report_write_failed")
	}
	return nil
}
