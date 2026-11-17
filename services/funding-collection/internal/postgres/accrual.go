package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
)

const maxAccrualDaysPerPass = 90

var errAccrualCorrection = errors.New("accrual_correction_required")

// AccrualOptions contains only server-owned original contract pricing facts.
type AccrualOptions struct {
	Mode               domain.Mode
	Pricing            domain.Pricing
	ExpectedPolicyHash string
}

// AccrualResult describes persisted catch-up state, not a final payoff quote.
type AccrualResult struct {
	Current            bool
	CurrentThrough     time.Time
	RecordedAsOf       time.Time
	DaysApplied        int
	Pending            bool
	CorrectionRequired bool
}

// AccrueThrough reconstructs each opening principal from immutable ledger legs
// and persists accrual in the caller transaction. Caller holds facility,
// customer and bridge locks. It performs no external I/O.
func AccrueThrough(ctx context.Context, tx pgx.Tx, options AccrualOptions, bridgeID string, asOf time.Time) (AccrualResult, error) {
	if options.Mode != domain.ModeDemo && options.Mode != domain.ModeSandbox {
		return AccrualResult{}, errors.New("accrual_mode_invalid")
	}
	if err := options.Pricing.Validate(); err != nil {
		return AccrualResult{}, errors.New("accrual_pricing_invalid")
	}
	if !validHash(options.ExpectedPolicyHash) {
		return AccrualResult{}, errors.New("accrual_policy_hash_invalid")
	}
	asOf = utcDate(asOf)
	var mode, customerID, lifecycle, policyHash string
	var dueOn time.Time
	var closedAt *time.Time
	var assessed int64
	var lastAccrued *time.Time
	var version int64
	var holdReasons []string
	err := tx.QueryRow(ctx, `SELECT b.mode,b.customer_id,b.lifecycle,b.due_on,b.closed_at,b.cumulative_late_fee_assessed_cents,b.last_accrued_on,b.version,b.hold_reasons,o.policy_hash FROM servicing.bridges b JOIN servicing.offers o ON o.mode=b.mode AND o.id=b.offer_id WHERE b.id=$1 AND b.mode=$2`, bridgeID, string(options.Mode)).Scan(&mode, &customerID, &lifecycle, &dueOn, &closedAt, &assessed, &lastAccrued, &version, &holdReasons, &policyHash)
	if err != nil {
		return AccrualResult{}, errors.New("accrual_bridge_unavailable")
	}
	if policyHash != options.ExpectedPolicyHash {
		return AccrualResult{}, errors.New("accrual_original_pricing_unavailable")
	}
	result := AccrualResult{CurrentThrough: asOf, RecordedAsOf: asOf}
	cutoff := asOf
	if lifecycle == string(domain.Repaid) {
		if closedAt == nil {
			return result, errors.New("accrual_closed_time_missing")
		}
		if utcDate(*closedAt).Before(cutoff) {
			cutoff = utcDate(*closedAt)
		}
	}
	if cutoff.Before(utcDate(dueOn).AddDate(0, 0, 1)) {
		result.Current = true
		return result, nil
	}
	correctionHeld := containsReason(holdReasons, "ACCRUAL_CORRECTION_REQUIRED")
	if correctionHeld {
		result.CorrectionRequired = true
		return result, nil
	}
	if err := checkFrozenAccrualBases(ctx, tx, options.Mode, bridgeID); err != nil {
		if !errors.Is(err, errAccrualCorrection) {
			return result, err
		}
		version++
		if _, e := tx.Exec(ctx, `UPDATE servicing.bridges SET hold_reasons=array_append(hold_reasons,'ACCRUAL_CORRECTION_REQUIRED'),version=$3,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, string(options.Mode), bridgeID, version); e != nil {
			return result, errors.New("accrual_correction_hold_failed")
		}
		if e := appendAccrualChange(ctx, tx, options.Mode, bridgeID, customerID, version, "ACCRUAL_CORRECTION_REQUIRED", 0); e != nil {
			return result, e
		}
		result.CorrectionRequired = true
		return result, nil
	}
	start := utcDate(dueOn).AddDate(0, 0, 1)
	if lastAccrued != nil {
		start = utcDate(*lastAccrued).AddDate(0, 0, 1)
	}
	lastToProcess := cutoff
	if days := int(cutoff.Sub(start)/(24*time.Hour)) + 1; days > maxAccrualDaysPerPass {
		lastToProcess = start.AddDate(0, 0, maxAccrualDaysPerPass-1)
		result.Pending = true
	}
	var numerator big.Int
	var priorNumerator string
	err = tx.QueryRow(ctx, `SELECT cumulative_numerator::text FROM servicing.accrual_days WHERE mode=$1 AND bridge_id=$2 ORDER BY accrual_on DESC LIMIT 1`, string(options.Mode), bridgeID).Scan(&priorNumerator)
	if err == nil {
		if _, ok := numerator.SetString(priorNumerator, 10); !ok {
			return result, errors.New("accrual_numerator_invalid")
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, errors.New("accrual_read_failed")
	}
	for day := start; !day.After(lastToProcess); day = day.AddDate(0, 0, 1) {
		opening, refs, e := openingPrincipal(ctx, tx, options.Mode, bridgeID, day)
		if e != nil {
			return result, e
		}
		if opening < 0 {
			return result, errors.New("accrual_principal_history_invalid")
		}
		increment := new(big.Int).Mul(big.NewInt(opening), big.NewInt(options.Pricing.MonthlyBasisPoints))
		numerator.Add(&numerator, increment)
		denominator := new(big.Int).Mul(big.NewInt(10000), big.NewInt(options.Pricing.DaysPerMonth))
		assessedTotal := new(big.Int).Add(new(big.Int).Set(&numerator), new(big.Int).Quo(new(big.Int).Set(denominator), big.NewInt(2)))
		assessedTotal.Quo(assessedTotal, denominator)
		if !assessedTotal.IsInt64() {
			return result, errors.New("accrual_amount_overflow")
		}
		total := assessedTotal.Int64()
		delta := total - assessed
		if delta < 0 {
			return result, errors.New("accrual_assessed_regression")
		}
		effectKey := "accrual:" + string(options.Mode) + ":" + bridgeID + ":" + day.Format("2006-01-02")
		refsJSON, _ := json.Marshal(refs)
		if _, e = tx.Exec(ctx, `INSERT INTO servicing.accrual_days(mode,bridge_id,accrual_on,opening_principal_cents,settlement_refs,cumulative_numerator,assessed_delta_cents,ledger_effect_key) VALUES($1,$2,$3,$4,$5::jsonb,$6::numeric,$7,$8)`, string(options.Mode), bridgeID, day, opening, string(refsJSON), numerator.String(), delta, effectKey); e != nil {
			return result, errors.New("accrual_day_write_failed")
		}
		if delta > 0 {
			provenance, _ := json.Marshal(map[string]any{"basis": "OPENING_PRINCIPAL_UTC_DAY", "accrual_on": day.Format("2006-01-02"), "opening_principal_cents": opening, "settlement_refs": refs})
			bridgeRef := bridgeID
			lines := []domain.LedgerLine{{Account: "LATE_FEE_RECEIVABLE", Debit: domain.Money(delta)}, {Account: "FEE_INCOME", Credit: domain.Money(delta)}}
			if e = InsertLedgerTransaction(ctx, tx, options.Mode, effectKey, "LATE_FEE_ACCRUAL", &bridgeRef, nil, day.Format("2006-01-02T00:00:00Z"), provenance, lines); e != nil {
				return result, e
			}
		}
		assessed = total
		version++
		if _, e = tx.Exec(ctx, `UPDATE servicing.bridges SET late_fee_owed_cents=late_fee_owed_cents+$3,cumulative_late_fee_assessed_cents=$4,last_accrued_on=$5,version=$6,collection_debt_version=collection_debt_version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, string(options.Mode), bridgeID, delta, assessed, day, version); e != nil {
			return result, errors.New("accrual_bridge_update_failed")
		}
		if delta > 0 {
			if _, e = tx.Exec(ctx, `UPDATE servicing.customers SET unpaid_fee_cents=unpaid_fee_cents+$3,version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, string(options.Mode), customerID, delta); e != nil {
				return result, errors.New("accrual_customer_update_failed")
			}
		}
		if e = appendAccrualChange(ctx, tx, options.Mode, bridgeID, customerID, version, "LATE_FEE_ACCRUED", delta); e != nil {
			return result, e
		}
		result.DaysApplied++
	}
	result.CurrentThrough = lastToProcess
	hasPending := containsReason(holdReasons, "ACCRUAL_CATCHUP_PENDING")
	if result.Pending && !hasPending {
		version++
		if _, err = tx.Exec(ctx, `UPDATE servicing.bridges SET hold_reasons=array_append(hold_reasons,'ACCRUAL_CATCHUP_PENDING'),version=$3,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, string(options.Mode), bridgeID, version); err != nil {
			return result, errors.New("accrual_pending_hold_failed")
		}
		if err = appendAccrualChange(ctx, tx, options.Mode, bridgeID, customerID, version, "WAIT_FOR_ACCRUAL", 0); err != nil {
			return result, err
		}
	}
	if !result.Pending && hasPending {
		version++
		if _, err = tx.Exec(ctx, `UPDATE servicing.bridges SET hold_reasons=array_remove(hold_reasons,'ACCRUAL_CATCHUP_PENDING'),version=$3,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, string(options.Mode), bridgeID, version); err != nil {
			return result, errors.New("accrual_pending_hold_clear_failed")
		}
		if err = appendAccrualChange(ctx, tx, options.Mode, bridgeID, customerID, version, "ACCRUAL_CAUGHT_UP", 0); err != nil {
			return result, err
		}
	}
	result.Current = !result.Pending
	return result, nil
}

func checkFrozenAccrualBases(ctx context.Context, tx pgx.Tx, mode domain.Mode, bridgeID string) error {
	var changed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.accrual_days d JOIN servicing.ledger_transactions t ON t.mode=d.mode AND t.bridge_id=d.bridge_id AND t.effective_at < (d.accrual_on::timestamp AT TIME ZONE 'UTC') JOIN servicing.ledger_lines l ON l.mode=t.mode AND l.transaction_id=t.id WHERE d.mode=$1 AND d.bridge_id=$2 AND l.account='PRINCIPAL_RECEIVABLE' AND l.debit_cents<>l.credit_cents AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(d.settlement_refs) x(value) WHERE x.value=t.id::text))`, string(mode), bridgeID).Scan(&changed)
	if err != nil {
		return errors.New("accrual_history_read_failed")
	}
	if changed {
		return errAccrualCorrection
	}
	return nil
}

func openingPrincipal(ctx context.Context, tx pgx.Tx, mode domain.Mode, bridgeID string, day time.Time) (int64, []string, error) {
	midnight := utcDate(day)
	var amountText string
	var refs []string
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN l.account='PRINCIPAL_RECEIVABLE' THEN l.debit_cents-l.credit_cents ELSE 0 END),0)::text,COALESCE(array_agg(DISTINCT t.id::text ORDER BY t.id::text) FILTER (WHERE l.account='PRINCIPAL_RECEIVABLE' AND l.debit_cents<>l.credit_cents),'{}'::text[]) FROM servicing.ledger_transactions t JOIN servicing.ledger_lines l ON l.mode=t.mode AND l.transaction_id=t.id WHERE t.mode=$1 AND t.bridge_id=$2 AND t.effective_at<$3`, string(mode), bridgeID, midnight).Scan(&amountText, &refs)
	if err != nil {
		return 0, nil, errors.New("accrual_principal_history_read_failed")
	}
	if refs == nil {
		refs = []string{}
	}
	amount, err := strconv.ParseInt(amountText, 10, 64)
	if err != nil {
		return 0, nil, errors.New("accrual_principal_history_read_failed")
	}
	return amount, refs, nil
}

func appendAccrualChange(ctx context.Context, tx pgx.Tx, mode domain.Mode, bridgeID, customerID string, version int64, kind string, delta int64) error {
	summary, _ := json.Marshal(map[string]any{"customer_id": customerID, "account_ref": "customer:" + customerID, "bridge_id": bridgeID, "bridge_version": version, "late_fee_delta_cents": delta})
	parent := bridgeID
	if _, err := AppendChange(ctx, tx, domain.Change{Mode: mode, ResourceType: "BRIDGE", ResourceID: bridgeID, ResourceVersion: version, ChangeType: kind, Summary: summary}, &parent, &version); err != nil {
		return err
	}
	return nil
}
func containsReason(reasons []string, reason string) bool {
	for _, item := range reasons {
		if item == reason {
			return true
		}
	}
	return false
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
func utcDate(value time.Time) time.Time {
	v := value.UTC()
	return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, time.UTC)
}
