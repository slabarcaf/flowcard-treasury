package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// VerifiedMapping is the locked roster/card tuple used in terms fingerprints.
type VerifiedMapping struct {
	CustomerID      string `json:"customer_id"`
	RosterAccountID string `json:"roster_account_id"`
	RosterActive    bool   `json:"roster_active"`
	CardID          string `json:"card_id"`
	CardAccountID   string `json:"card_account_id"`
	CardActive      bool   `json:"card_active"`
}

// LockVerifiedMapping calls the fixed SECURITY DEFINER helper in the caller's
// financial transaction. The caller must already hold facility/customer,
// bridge/offer and sorted invoice locks in the prescribed order.
func LockVerifiedMapping(ctx context.Context, tx pgx.Tx, customerID, accountID, cardID string) (VerifiedMapping, string, error) {
	if customerID == "" || accountID == "" || cardID == "" {
		return VerifiedMapping{}, "", errors.New("mapping_unavailable")
	}
	var mapping VerifiedMapping
	err := tx.QueryRow(ctx, `SELECT customer_id,roster_account_id,roster_active,
		card_id,card_account_id,card_active
		FROM servicing.lock_verified_mapping($1,$2,$3)`, customerID, accountID, cardID).Scan(
		&mapping.CustomerID, &mapping.RosterAccountID, &mapping.RosterActive,
		&mapping.CardID, &mapping.CardAccountID, &mapping.CardActive)
	if err != nil {
		return VerifiedMapping{}, "", errors.New("mapping_unavailable")
	}
	if mapping.CustomerID != customerID || mapping.CardID != cardID ||
		mapping.RosterAccountID != accountID || mapping.CardAccountID != accountID ||
		!mapping.RosterActive || !mapping.CardActive {
		return VerifiedMapping{}, "", errors.New("mapping_conflict")
	}
	encoded, err := json.Marshal(mapping)
	if err != nil {
		return VerifiedMapping{}, "", errors.New("mapping_fingerprint_failed")
	}
	sum := sha256.Sum256(encoded)
	return mapping, hex.EncodeToString(sum[:]), nil
}
