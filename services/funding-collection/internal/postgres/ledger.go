package postgres

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
)

// LockOrder names the required order for rows in a financial transaction.
type LockOrder string

const (
	LockFacility    LockOrder = "FACILITY"
	LockCustomer    LockOrder = "CUSTOMER"
	LockBridgeOffer LockOrder = "BRIDGE_OR_OFFER"
	LockInvoices    LockOrder = "SORTED_INVOICES"
	LockDeposit     LockOrder = "DEPOSIT"
	LockMapping     LockOrder = "MAPPING"
	LockOperation   LockOrder = "OPERATION"
	LockFeed        LockOrder = "FEED"
)

// Repository wraps the restricted servicing runtime pool.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository creates a repository without running DDL.
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// BeginFinancial starts a read committed financial transaction.
func (r *Repository) BeginFinancial(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, errors.New("storage_unavailable")
	}
	return tx, nil
}

// AppendChange increments the locked per-mode counter and inserts the feed row
// in the caller transaction, so rollback and commit order stay coupled.
func AppendChange(ctx context.Context, tx pgx.Tx, change domain.Change, bridgeID *string, bridgeVersion *int64) (int64, error) {
	var sequence int64
	err := tx.QueryRow(ctx, `SELECT servicing.append_change($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`,
		string(change.Mode), change.ResourceType, change.ResourceID,
		change.ResourceVersion, bridgeID, bridgeVersion, change.ChangeType,
		string(change.Summary)).Scan(&sequence)
	if err != nil {
		return 0, errors.New("change_append_failed")
	}
	return sequence, nil
}

// InsertLedgerTransaction writes transaction header and lines atomically. The
// deferred database constraint checks that the final posting is balanced.
func InsertLedgerTransaction(ctx context.Context, tx pgx.Tx, mode domain.Mode, effectKey, kind string, bridgeID, operationID *string, effectiveAt string, provenance []byte, lines []domain.LedgerLine) error {
	if effectKey == "" || len(lines) < 2 {
		return errors.New("ledger_entry_invalid")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO servicing.ledger_transactions
		(mode,effect_key,transaction_kind,bridge_id,operation_id,effective_at,provenance)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`, string(mode), effectKey, kind,
		bridgeID, operationID, effectiveAt, string(provenance)); err != nil {
		return errors.New("ledger_transaction_insert_failed")
	}
	var transactionID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM servicing.ledger_transactions
		WHERE mode=$1 AND effect_key=$2`, string(mode), effectKey).Scan(&transactionID); err != nil {
		return errors.New("ledger_transaction_read_failed")
	}
	var debits, credits int64
	for index, line := range lines {
		if line.Account == "" || line.Debit < 0 || line.Credit < 0 ||
			(line.Debit > 0) == (line.Credit > 0) {
			return errors.New("ledger_line_invalid")
		}
		if int64(line.Debit) > math.MaxInt64-debits || int64(line.Credit) > math.MaxInt64-credits {
			return errors.New("ledger_amount_overflow")
		}
		debits += int64(line.Debit)
		credits += int64(line.Credit)
		if _, err := tx.Exec(ctx, `INSERT INTO servicing.ledger_lines
			(mode,transaction_id,line_number,account,debit_cents,credit_cents)
			VALUES($1,$2,$3,$4,$5,$6)`, string(mode), transactionID, index+1,
			line.Account, int64(line.Debit), int64(line.Credit)); err != nil {
			return errors.New("ledger_line_insert_failed")
		}
	}
	if debits <= 0 || debits != credits {
		return errors.New("ledger_unbalanced")
	}
	return nil
}
