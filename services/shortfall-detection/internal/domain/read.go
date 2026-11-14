package domain

import "time"

// Record is the internal read API allowlist, with unavailable fields explicitly null.
type Record struct {
	ID                    string     `json:"id"`
	CustomerID            *string    `json:"customer_id"`
	AccountID             string     `json:"account_id"`
	CardID                *string    `json:"card_id"`
	Outcome               Outcome    `json:"outcome"`
	Reason                *string    `json:"reason"`
	Currency              *string    `json:"currency"`
	AttemptedAmountCents  *int64     `json:"attempted_amount_cents"`
	AvailableBalanceCents *int64     `json:"available_balance_cents"`
	ShortfallCents        *int64     `json:"shortfall_cents"`
	CanonicalEventID      string     `json:"canonical_event_id"`
	TransactionID         *string    `json:"transaction_id"`
	LifecycleID           *string    `json:"lifecycle_id"`
	CreatedAt             time.Time  `json:"created_at"`
	ProviderCreatedAt     *time.Time `json:"provider_created_at"`
	ProviderTransactedAt  *time.Time `json:"provider_transacted_at"`
	ProviderUpdatedAt     *time.Time `json:"provider_updated_at"`
	BalanceRequestedAt    *time.Time `json:"balance_requested_at"`
	BalanceObservedAt     *time.Time `json:"balance_observed_at"`
	RequiresRecheck       bool       `json:"requires_recheck"`
	AmountSemantics       string     `json:"amount_semantics"`
}

// ReadCursor anchors descending keyset pagination without exposing raw evidence.
type ReadCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// ListQuery contains authenticated server scope and validated optional filters.
type ListQuery struct {
	Environment string
	CustomerID  string
	Outcome     Outcome
	After       *ReadCursor
}

// Page returns at most 25 observations and an optional next-page anchor.
type Page struct {
	Items []Record
	Next  *ReadCursor
}

// ValidID recognizes canonical UUID syntax without adding a UUID dependency.
func ValidID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, ch := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') && !(ch >= 'A' && ch <= 'F') {
			return false
		}
	}
	return true
}
