package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

// DepositTimestampRecheck identifies one held deposit in a known installation.
// Only the privileged maintenance CLI may invoke this recovery. It neither
// sends money nor changes original deposit facts or existing operation identities.
type DepositTimestampRecheck struct {
	Mode                                                domain.Mode
	FacilityID, CustomerID, CardID, BridgeID, DepositID string
	BridgeVersion                                       int64
	Observation                                         provider.DepositObservation
	Now                                                 time.Time
}

// RecheckDepositTimestamp releases only a verified timestamp-only source hold.
// Any economic change, unrelated hold, or unresolved movement refuses recovery.
func (r *Repository) RecheckDepositTimestamp(ctx context.Context, in DepositTimestampRecheck) (string, error) {
	if (in.Mode != domain.ModeDemo && in.Mode != domain.ModeSandbox) || in.FacilityID == "" ||
		in.CustomerID == "" || in.CardID == "" || in.BridgeID == "" || in.DepositID == "" ||
		in.BridgeVersion < 1 || in.Now.IsZero() || in.Observation.SettledAt == nil ||
		in.Observation.ObservedAt.After(in.Now.Add(5*time.Second)) ||
		in.Observation.ObservedAt.Before(in.Now.Add(-30*time.Second)) ||
		in.Observation.SettledAt.After(in.Now.Add(5*time.Second)) ||
		len(in.Observation.RawEvidence) == 0 || len(in.Observation.RawEvidence) > 4_000_000 {
		return "", errors.New("deposit_recheck_input_invalid")
	}
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.BeginFinancial(txCtx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(txCtx)
	var privileged bool
	if err := tx.QueryRow(txCtx, `SELECT has_table_privilege(current_user,
		'servicing.deposit_timestamp_rechecks','INSERT')`).Scan(&privileged); err != nil || !privileged {
		return "", errors.New("deposit_recheck_maintenance_only")
	}
	if err := LockFacilityForCollectionReview(txCtx, tx, string(in.Mode), in.FacilityID, in.CustomerID, in.BridgeID); err != nil {
		return "", err
	}
	var version int64
	var account, caseID, evaluationID, offerID string
	var fundedAt time.Time
	if err := tx.QueryRow(txCtx, `SELECT version,canonical_account_id,funded_at,
		COALESCE(originating_case_id,''),evaluation_id,offer_id::text
		FROM servicing.bridges WHERE mode=$1 AND id=$2 AND customer_id=$3 AND facility_id=$4
		AND ever_funded AND lifecycle IN ('ACTIVE','COLLECTING','REOPENED_HELD')`,
		string(in.Mode), in.BridgeID, in.CustomerID, in.FacilityID).Scan(&version, &account,
		&fundedAt, &caseID, &evaluationID, &offerID); err != nil {
		return "", errors.New("deposit_recheck_bridge_unavailable")
	}
	var facts depositFacts
	var originalObservation string
	var originalSettled time.Time
	if err := tx.QueryRow(txCtx, `SELECT account_ref,customer_id,provider_deposit_id,currency,
		status,kind,gross_cents,net_cents,provider_fee_cents,source_observation_id::text,settled_at
		FROM servicing.deposits WHERE mode=$1 AND id=$2 AND customer_id=$3 FOR UPDATE`,
		string(in.Mode), in.DepositID, in.CustomerID).Scan(&facts.account, &facts.customer,
		&facts.id, &facts.currency, &facts.status, &facts.kind, &facts.gross, &facts.net,
		&facts.fee, &originalObservation, &originalSettled); err != nil {
		return "", errors.New("deposit_recheck_deposit_unavailable")
	}
	status := facts.status
	facts.status = "SETTLED"
	if !facts.matches(in.Observation) || facts.account != account ||
		!originalSettled.After(fundedAt) || !in.Observation.SettledAt.After(fundedAt) {
		return "", errors.New("deposit_recheck_facts_mismatch")
	}
	var priorID string
	err = tx.QueryRow(txCtx, `SELECT id::text FROM servicing.deposit_timestamp_rechecks
		WHERE mode=$1 AND deposit_id=$2`, string(in.Mode), in.DepositID).Scan(&priorID)
	if err == nil {
		if status != "SETTLED" {
			return "", errors.New("deposit_recheck_subsequent_hold")
		}
		return priorID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("deposit_recheck_receipt_read_failed")
	}
	if status != "HELD" || version != in.BridgeVersion {
		return "", ErrOperationClaimStale
	}
	// Never remove a hold from a different deposit, reason or unresolved debit.
	var safe bool
	if err := tx.QueryRow(txCtx, `SELECT
		EXISTS(SELECT 1 FROM servicing.changes WHERE mode=$1 AND parent_bridge_id=$2
		  AND change_type='DEPOSIT_SOURCE_CONFLICT' AND summary->>'deposit_id'=$4
		  AND summary->>'movement_certainty'='VERIFIED_CHANGED_DEPOSIT')
		AND NOT EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND customer_id=$3
		  AND (hold_reasons <> ARRAY[]::text[] AND hold_reasons <> ARRAY['DEPOSIT_SOURCE_CONFLICT']::text[]))
		AND NOT EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND customer_id=$3
		  AND id<>$2 AND hold_reasons <> ARRAY[]::text[])
		AND EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND id=$2
		  AND hold_reasons=ARRAY['DEPOSIT_SOURCE_CONFLICT']::text[])
		AND NOT EXISTS(SELECT 1 FROM servicing.deposits WHERE mode=$1 AND customer_id=$3
		  AND id<>$4::uuid AND status='HELD')
		AND NOT EXISTS(SELECT 1 FROM servicing.operations o JOIN servicing.bridges b
		  ON b.mode=o.mode AND b.id=o.bridge_id WHERE o.mode=$1 AND b.customer_id=$3
		  AND o.state IN ('QUEUED','DISPATCHING','PENDING','UNKNOWN','HELD'))
		AND NOT EXISTS(SELECT 1 FROM servicing.customers WHERE mode=$1 AND id=$3 AND defaulted_at IS NOT NULL)`,
		string(in.Mode), in.BridgeID, in.CustomerID, in.DepositID).Scan(&safe); err != nil || !safe {
		return "", errors.New("deposit_recheck_other_hold_or_movement")
	}
	// A past amount/account/status conflict must not be mistaken for a timestamp
	// correction just because the provider's latest value happens to match again.
	if err := tx.QueryRow(txCtx, `SELECT NOT EXISTS(SELECT 1 FROM servicing.provider_observations
		WHERE mode=$1 AND observation_kind='DEPOSIT' AND provider_object_id=$2
		AND (source_account_ref IS DISTINCT FROM $3 OR amount_cents IS DISTINCT FROM $4
		 OR currency IS DISTINCT FROM 'USD' OR provider_fee_cents IS DISTINCT FROM $5
		 OR status IS DISTINCT FROM 'SETTLED'
		 OR normalized_evidence->>'account_ref' IS DISTINCT FROM $3
		 OR normalized_evidence->>'customer_id' IS DISTINCT FROM $6
		 OR normalized_evidence->>'deposit_id' IS DISTINCT FROM $2
		 OR normalized_evidence->>'kind' IS DISTINCT FROM 'EXTERNAL_SETTLED'
		 OR (normalized_evidence->>'gross_cents')::bigint IS DISTINCT FROM $4
		 OR (normalized_evidence->>'net_cents')::bigint IS DISTINCT FROM $7
		 OR (normalized_evidence->>'provider_fee_cents')::bigint IS DISTINCT FROM $5))`,
		string(in.Mode), facts.id, facts.account, facts.gross, facts.fee, facts.customer,
		facts.net).Scan(&safe); err != nil || !safe {
		return "", errors.New("deposit_recheck_historical_economic_conflict")
	}
	if _, _, err := LockVerifiedMapping(txCtx, tx, in.CustomerID, account, in.CardID); err != nil {
		return "", errors.New("deposit_recheck_mapping_changed")
	}
	observationID, err := insertCollectionDepositObservation(txCtx, tx, in.Mode, in.Observation)
	if err != nil {
		return "", err
	}
	var recheckID string
	if err := tx.QueryRow(txCtx, `INSERT INTO servicing.deposit_timestamp_rechecks
		(mode,deposit_id,bridge_id,original_observation_id,verified_observation_id,
		original_settled_at,observed_settled_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`,
		string(in.Mode), in.DepositID, in.BridgeID, originalObservation, observationID,
		originalSettled, in.Observation.SettledAt.UTC()).Scan(&recheckID); err != nil {
		return "", errors.New("deposit_recheck_audit_insert_failed")
	}
	if _, err := tx.Exec(txCtx, `UPDATE servicing.deposits SET status='SETTLED'
		WHERE mode=$1 AND id=$2 AND status='HELD'`, string(in.Mode), in.DepositID); err != nil {
		return "", errors.New("deposit_recheck_release_failed")
	}
	if _, err := tx.Exec(txCtx, `UPDATE servicing.customers SET active_hold=false,
		version=version+1,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`,
		string(in.Mode), in.CustomerID); err != nil {
		return "", errors.New("deposit_recheck_customer_release_failed")
	}
	rows, err := tx.Query(txCtx, `UPDATE servicing.bridges SET
		hold_reasons=array_remove(hold_reasons,'DEPOSIT_SOURCE_CONFLICT'),
		version=version+1,updated_at=clock_timestamp()
		WHERE mode=$1 AND customer_id=$2 AND hold_reasons=ARRAY['DEPOSIT_SOURCE_CONFLICT']::text[]
		RETURNING id::text,version,COALESCE(originating_case_id,''),evaluation_id,offer_id::text`,
		string(in.Mode), in.CustomerID)
	if err != nil {
		return "", errors.New("deposit_recheck_bridge_release_failed")
	}
	type changedBridge struct {
		id, caseID, evaluationID, offerID string
		version                           int64
	}
	var changed []changedBridge
	for rows.Next() {
		var b changedBridge
		if err := rows.Scan(&b.id, &b.version, &b.caseID, &b.evaluationID, &b.offerID); err != nil {
			rows.Close()
			return "", errors.New("deposit_recheck_bridge_release_failed")
		}
		changed = append(changed, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", errors.New("deposit_recheck_bridge_release_failed")
	}
	for _, b := range changed {
		var caseRef any
		if b.caseID != "" {
			caseRef = b.caseID
		}
		body, _ := json.Marshal(map[string]any{"customer_id": in.CustomerID,
			"account_ref": "customer:" + in.CustomerID, "originating_case_id": caseRef,
			"evaluation_id": b.evaluationID, "offer_id": b.offerID, "bridge_id": b.id,
			"bridge_version": b.version, "deposit_id": in.DepositID, "recheck_id": recheckID,
			"provider_observation_id": observationID, "financial_movement": false})
		if _, err := AppendChange(txCtx, tx, domain.Change{Mode: in.Mode, ResourceType: "BRIDGE",
			ResourceID: b.id, ResourceVersion: b.version, ChangeType: "DEPOSIT_TIMESTAMP_RECHECKED",
			Summary: body}, &b.id, &b.version); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(txCtx); err != nil {
		return "", errors.New("deposit_recheck_commit_failed")
	}
	return recheckID, nil
}
