package upstream

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Mapping is a verified active mapping from the existing public read-only tables.
type Mapping struct {
	CustomerID string
	AccountID  string
	CardID     string
}

// ResolveMapping checks one exact mapping without writing to public tables.
func ResolveMapping(ctx context.Context, tx pgx.Tx, customerID, accountID, cardID string) (Mapping, error) {
	rows, err := tx.Query(ctx, `SELECT r.customer_id,r.airwallex_connected_account_id,
		r.is_active,b.card_id,b.airwallex_account_id,b.is_active
		FROM public.customer_roster r JOIN public.issuing_card_bindings b
		ON b.customer_id=r.customer_id WHERE r.customer_id=$1 AND b.card_id=$2`, customerID, cardID)
	if err != nil {
		return Mapping{}, errors.New("mapping_read_failed")
	}
	defer rows.Close()
	var result Mapping
	var rosterAccount, cardAccount string
	var rosterActive, cardActive bool
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&result.CustomerID, &rosterAccount, &rosterActive, &result.CardID, &cardAccount, &cardActive); err != nil {
			return Mapping{}, errors.New("mapping_read_failed")
		}
	}
	if err := rows.Err(); err != nil || count != 1 || !rosterActive || !cardActive ||
		result.CustomerID != customerID || rosterAccount != accountID || cardAccount != accountID || result.CardID != cardID {
		return Mapping{}, errors.New("mapping_conflict")
	}
	result.AccountID = accountID
	return result, nil
}
