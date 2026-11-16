package acceptance_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func storedTimestampDeposit(t *testing.T) (context.Context, *servicefixture.Harness, fundReceipt, provider.DepositObservation, string) {
	t.Helper()
	ctx, h := newJourney(t)
	funding := settledFunding(t, ctx, h)
	fee := int64(0)
	settled := time.Now().UTC().Truncate(time.Microsecond)
	deposit := provider.DepositObservation{AccountRef: servicefixture.BorrowerAccount,
		CustomerID: servicefixture.CustomerID, DepositID: "timestamp-deposit", Currency: "USD",
		GrossCents: 10000, NetCents: 10000, ProviderFeeCents: &fee,
		Status: "SETTLED", Kind: "EXTERNAL_SETTLED", SettledAt: &settled, ObservedAt: settled}
	h.ProviderHTTP.SetDeposit(deposit)
	wakeTimestampDeposit(t, ctx, h, "timestamp-original")
	var id, status string
	if err := h.Pool.QueryRow(ctx, `SELECT id::text,status FROM servicing.deposits
		WHERE mode='DEMO' AND provider_deposit_id=$1`, deposit.DepositID).Scan(&id, &status); err != nil || status != "SETTLED" {
		t.Fatalf("stored deposit status=%s err=%v", status, err)
	}
	return ctx, h, funding, deposit, id
}

func wakeTimestampDeposit(t *testing.T, ctx context.Context, h *servicefixture.Harness, event string) {
	t.Helper()
	response, err := sendDepositWake(h, event, "timestamp-deposit")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("deposit wake=%v err=%v", journeyResponseStatus(response), err)
	}
	response.Body.Close()
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDepositTimestampChangesAllowVerifiedCollection(t *testing.T) {
	ctx, h, funding, deposit, _ := storedTimestampDeposit(t)
	original := *deposit.SettledAt
	corrected := original.Add(time.Second)
	deposit.SettledAt = &corrected
	h.ProviderHTTP.SetDeposit(deposit)
	// A distinct delivery for the same deposit is a duplicate, despite its time.
	wakeTimestampDeposit(t, ctx, h, "timestamp-corrected")
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM servicing.operations
		WHERE mode='DEMO' AND bridge_id=$1 AND kind='COLLECT_FEE' AND state='QUEUED'`, funding.BridgeID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("planning with corrected time queued=%d err=%v", queued, err)
	}
	// The dispatch recheck also tolerates another timestamp correction.
	corrected = original.Add(2 * time.Second)
	deposit.SettledAt = &corrected
	h.ProviderHTTP.SetDeposit(deposit)
	creates := h.ProviderHTTP.CreateRequestCount()
	if err := driveWorkerUntil(ctx, h, &creates, 12*time.Second, func() bool {
		var fee int64
		return h.Pool.QueryRow(ctx, `SELECT contract_fee_owed_cents FROM servicing.bridges
			WHERE mode='DEMO' AND id=$1`, funding.BridgeID).Scan(&fee) == nil && fee == 9000
	}); err != nil {
		t.Fatal(err)
	}
	var saved time.Time
	var held bool
	var count int
	if err := h.Pool.QueryRow(ctx, `SELECT settled_at,status='HELD' FROM servicing.deposits
		WHERE mode='DEMO' AND provider_deposit_id=$1`, deposit.DepositID).Scan(&saved, &held); err != nil || held || !saved.Equal(original) {
		t.Fatalf("original deposit changed or held: time=%s held=%v err=%v", saved, held, err)
	}
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM servicing.deposits
		WHERE mode='DEMO' AND provider_deposit_id=$1`, deposit.DepositID).Scan(&count); err != nil || count != 1 || creates != 2 {
		t.Fatalf("deposits=%d creates=%d err=%v, want one deposit and funding+one fee", count, creates, err)
	}
}

func TestHeldTimestampRecheckPreservesEvidenceAndIsIdempotent(t *testing.T) {
	ctx, h, funding, deposit, id := storedTimestampDeposit(t)
	original := *deposit.SettledAt
	// Reproduce the previous version's persisted timestamp-only refusal.
	if _, err := h.Pool.Exec(ctx, `UPDATE servicing.deposits SET status='HELD' WHERE mode='DEMO' AND id=$1`, id); err != nil {
		t.Fatal(err)
	}
	candidate := postgres.CollectionCandidate{Mode: domain.ModeDemo, FacilityID: servicefixture.FacilityID,
		CustomerID: servicefixture.CustomerID, BridgeID: funding.BridgeID, DepositID: id}
	if err := postgres.NewRepository(h.Pool).RecordCollectionHold(ctx, candidate, "DEPOSIT_SOURCE_CONFLICT",
		map[string]any{"deposit_id": id, "movement_certainty": "VERIFIED_CHANGED_DEPOSIT"}); err != nil {
		t.Fatal(err)
	}
	corrected := original.Add(time.Second)
	deposit.SettledAt = &corrected
	h.ProviderHTTP.SetDeposit(deposit)
	observed, err := h.Provider.Deposit(ctx, servicefixture.BorrowerAccount, deposit.DepositID)
	if err != nil {
		t.Fatal(err)
	}
	in := postgres.DepositTimestampRecheck{Mode: domain.ModeDemo, FacilityID: servicefixture.FacilityID,
		CustomerID: servicefixture.CustomerID, CardID: servicefixture.CardID, BridgeID: funding.BridgeID,
		DepositID: id, Observation: observed, Now: time.Now().UTC()}
	if err := h.Pool.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).Scan(&in.BridgeVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewRepository(h.Pool).RecheckDepositTimestamp(ctx, in); err == nil {
		t.Fatal("runtime credentials released a privileged hold")
	}
	bad := in
	bad.Observation.GrossCents++
	bad.Observation.NetCents++
	if _, err := postgres.NewRepository(h.Maintenance).RecheckDepositTimestamp(ctx, bad); err == nil {
		t.Fatal("changed monetary facts were accepted")
	}
	recheckID, err := postgres.NewRepository(h.Maintenance).RecheckDepositTimestamp(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	replayID, err := postgres.NewRepository(h.Maintenance).RecheckDepositTimestamp(ctx, in)
	if err != nil || replayID != recheckID {
		t.Fatalf("recheck replay=%s err=%v", replayID, err)
	}
	var saved time.Time
	var status string
	var holds []string
	var active bool
	if err := h.Pool.QueryRow(ctx, `SELECT d.settled_at,d.status,b.hold_reasons,c.active_hold
		FROM servicing.deposits d JOIN servicing.bridges b ON b.mode=d.mode AND b.customer_id=d.customer_id
		JOIN servicing.customers c ON c.mode=d.mode AND c.id=d.customer_id
		WHERE d.mode='DEMO' AND d.id=$1 AND b.id=$2`, id, funding.BridgeID).Scan(&saved, &status, &holds, &active); err != nil || status != "SETTLED" || len(holds) != 0 || active || !saved.Equal(original) {
		t.Fatalf("rechecked state=%s holds=%v active=%v original=%v err=%v", status, holds, active, saved.Equal(original), err)
	}
	if h.ProviderHTTP.CreateRequestCount() != 1 {
		t.Fatal("rechecking sent a financial command")
	}
	// An old audit row cannot release a subsequent hold or authorize runtime writes.
	if _, err := h.Pool.Exec(ctx, `UPDATE servicing.deposits SET status='HELD' WHERE mode='DEMO' AND id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Maintenance.Exec(ctx, `UPDATE servicing.deposits SET status='SETTLED' WHERE mode='DEMO' AND id=$1`, id); err == nil {
		t.Fatal("old maintenance evidence released a later hold")
	}
	if _, err := postgres.NewRepository(h.Maintenance).RecheckDepositTimestamp(ctx, in); err == nil {
		t.Fatal("recheck replay released a later hold")
	}
}

func TestEconomicDepositConflictStaysHeld(t *testing.T) {
	ctx, h, funding, deposit, id := storedTimestampDeposit(t)
	changed := deposit
	changed.GrossCents++
	changed.NetCents++
	h.ProviderHTTP.SetDeposit(changed)
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	// Even if the latest GET reverts, historical monetary conflict stays held.
	corrected := deposit.SettledAt.Add(time.Second)
	deposit.SettledAt = &corrected
	h.ProviderHTTP.SetDeposit(deposit)
	observed, err := h.Provider.Deposit(ctx, servicefixture.BorrowerAccount, deposit.DepositID)
	if err != nil {
		t.Fatal(err)
	}
	in := postgres.DepositTimestampRecheck{Mode: domain.ModeDemo, FacilityID: servicefixture.FacilityID,
		CustomerID: servicefixture.CustomerID, CardID: servicefixture.CardID, BridgeID: funding.BridgeID,
		DepositID: id, Observation: observed, Now: time.Now().UTC()}
	if err := h.Pool.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).Scan(&in.BridgeVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewRepository(h.Maintenance).RecheckDepositTimestamp(ctx, in); err == nil || err.Error() != "deposit_recheck_historical_economic_conflict" {
		t.Fatalf("historical economic conflict refusal=%v", err)
	}
	var status string
	if err := h.Pool.QueryRow(ctx, `SELECT status FROM servicing.deposits WHERE mode='DEMO' AND id=$1`, id).Scan(&status); err != nil || status != "HELD" || h.ProviderHTTP.CreateRequestCount() != 1 {
		t.Fatalf("economic conflict status=%s err=%v, provider creates=%d", status, err, h.ProviderHTTP.CreateRequestCount())
	}
}

func TestTimestampRecheckPreservesUnrelatedHold(t *testing.T) {
	ctx, h, funding, deposit, id := storedTimestampDeposit(t)
	if _, err := h.Pool.Exec(ctx, `UPDATE servicing.deposits SET status='HELD' WHERE mode='DEMO' AND id=$1`, id); err != nil {
		t.Fatal(err)
	}
	candidate := postgres.CollectionCandidate{Mode: domain.ModeDemo, FacilityID: servicefixture.FacilityID,
		CustomerID: servicefixture.CustomerID, BridgeID: funding.BridgeID, DepositID: id}
	for _, reason := range []string{"DEPOSIT_SOURCE_CONFLICT", "COLLECTION_AUTHORITY_BLOCKED"} {
		if err := postgres.NewRepository(h.Pool).RecordCollectionHold(ctx, candidate, reason,
			map[string]any{"movement_certainty": "VERIFIED_CHANGED_DEPOSIT"}); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := h.Provider.Deposit(ctx, servicefixture.BorrowerAccount, deposit.DepositID)
	if err != nil {
		t.Fatal(err)
	}
	in := postgres.DepositTimestampRecheck{Mode: domain.ModeDemo, FacilityID: servicefixture.FacilityID,
		CustomerID: servicefixture.CustomerID, CardID: servicefixture.CardID, BridgeID: funding.BridgeID,
		DepositID: id, Observation: observed, Now: time.Now().UTC()}
	if err := h.Pool.QueryRow(ctx, `SELECT version FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).Scan(&in.BridgeVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewRepository(h.Maintenance).RecheckDepositTimestamp(ctx, in); err == nil || err.Error() != "deposit_recheck_other_hold_or_movement" {
		t.Fatalf("unrelated hold refusal=%v", err)
	}
	var count int
	if err := h.Maintenance.QueryRow(ctx, `SELECT count(*) FROM servicing.deposit_timestamp_rechecks`).Scan(&count); err != nil || count != 0 || h.ProviderHTTP.CreateRequestCount() != 1 {
		t.Fatalf("refused recheck count=%d err=%v creates=%d", count, err, h.ProviderHTTP.CreateRequestCount())
	}
}
