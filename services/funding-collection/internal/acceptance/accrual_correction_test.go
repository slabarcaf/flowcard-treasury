package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestBackdatedReversalHoldsFrozenAccrual(t *testing.T) {
	ctx, h := newJourney(t)
	funding := settledFunding(t, ctx, h)
	repayFunding(t, ctx, h, funding)
	// Reopen fee debt with zero principal, then freeze two zero-basis days.
	if err := reverseCollection(ctx, h, 1, 10_000, "frozen-fee-reversal"); err != nil {
		t.Fatal(err)
	}
	var dueOn time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT due_on FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).Scan(&dueOn); err != nil {
		t.Fatal(err)
	}
	logicalAsOf := dueOn.AddDate(0, 0, 2)
	first, err := accrueBridge(ctx, h, funding.BridgeID, logicalAsOf)
	if err != nil || !first.Current || first.DaysApplied != 2 || first.CorrectionRequired {
		t.Fatalf("zero-principal accrual=%+v err=%v, want two frozen zero-basis days", first, err)
	}
	const frozenDaysSQL = `SELECT jsonb_agg(to_jsonb(d) ORDER BY accrual_on)::text
		FROM servicing.accrual_days d WHERE mode='DEMO' AND bridge_id=$1`
	var frozen string
	if err := h.Pool.QueryRow(ctx, frozenDaysSQL, funding.BridgeID).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	// The newly observed principal reversal is effective before those frozen
	// dates. Only provider facts change; the worker books the real reversal.
	if err := reverseCollection(ctx, h, 3, 350_000, "frozen-principal-reversal"); err != nil {
		t.Fatal(err)
	}
	correction, err := accrueBridge(ctx, h, funding.BridgeID, logicalAsOf)
	if err != nil || correction.Current || !correction.CorrectionRequired || correction.DaysApplied != 0 {
		t.Fatalf("backdated accrual=%+v err=%v, want correction hold without replacement days", correction, err)
	}
	var after string
	if err := h.Pool.QueryRow(ctx, frozenDaysSQL, funding.BridgeID).Scan(&after); err != nil || after != frozen {
		t.Fatalf("frozen accrual changed=%t err=%v, want byte-identical persisted days", after != frozen, err)
	}
	var principal, fee, late int64
	var held bool
	if err := h.Pool.QueryRow(ctx, `SELECT principal_owed_cents,contract_fee_owed_cents,late_fee_owed_cents,
		'ACCRUAL_CORRECTION_REQUIRED'=ANY(hold_reasons) FROM servicing.bridges WHERE mode='DEMO' AND id=$1`, funding.BridgeID).
		Scan(&principal, &fee, &late, &held); err != nil || principal != 350_000 || fee != 10_000 || late != 0 || !held {
		t.Fatalf("backdated debt=%d/%d/%d held=%t err=%v, want actual restored debt and no rewritten fees", principal, fee, late, held, err)
	}
	replay, err := accrueBridge(ctx, h, funding.BridgeID, logicalAsOf)
	if err != nil || !replay.CorrectionRequired || replay.Current || replay.DaysApplied != 0 || h.ProviderHTTP.CreateRequestCount() != 4 {
		t.Fatalf("correction replay=%+v creates=%d err=%v, want held accrual without new money movement", replay, h.ProviderHTTP.CreateRequestCount(), err)
	}
}

func reverseCollection(ctx context.Context, h *servicefixture.Harness, index int, cents int64, eventID string) error {
	request, ok := findCreateRequest(h.ProviderHTTP.Requests(), index)
	if !ok {
		return errors.New("collection create request missing")
	}
	var intent struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(request.Body, &intent); err != nil {
		return err
	}
	objectID, ok := h.ProviderHTTP.ObjectIDForRequest(intent.RequestID)
	if !ok {
		return errors.New("collection provider identity missing")
	}
	settledAt, ok := h.ProviderHTTP.TransferSettledAt(objectID)
	if !ok {
		return errors.New("collection provider settlement missing")
	}
	if err := h.ProviderHTTP.SetTransferReversal(objectID, cents, settledAt.Add(time.Second)); err != nil {
		return err
	}
	if err := sendTransferWake(h, eventID, intent.RequestID); err != nil {
		return err
	}
	if _, err := h.Worker.ProcessOne(ctx); err != nil {
		return fmt.Errorf("verify collection reversal: %w", err)
	}
	return nil
}

func accrueBridge(ctx context.Context, h *servicefixture.Harness, bridgeID string, logicalAsOf time.Time) (postgres.AccrualResult, error) {
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return postgres.AccrualResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := postgres.LockFacilityForCollectionReview(ctx, tx, config.ModeDemo, servicefixture.FacilityID,
		servicefixture.CustomerID, bridgeID); err != nil {
		return postgres.AccrualResult{}, err
	}
	result, err := postgres.AccrueThrough(ctx, tx, postgres.AccrualOptions{Mode: domain.ModeDemo,
		Pricing: h.Evaluator.Pricing(), ExpectedPolicyHash: h.Evaluator.PolicyHash()}, bridgeID, logicalAsOf)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
