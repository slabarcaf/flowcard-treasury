package provider

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

// Event is the allowlisted Card Transaction Events GET schema, not webhook JSON.
type Event struct {
	ID                  string          `json:"id"`
	CardID              string          `json:"card_id"`
	TransactionID       string          `json:"card_transaction_id"`
	LifecycleID         string          `json:"lifecycle_id"`
	Type                string          `json:"type"`
	Subtype             string          `json:"subtype"`
	Category            string          `json:"transaction_category"`
	ProcessResult       string          `json:"process_result"`
	FailureReason       string          `json:"failure_reason"`
	TransactionAmount   json.RawMessage `json:"transaction_amount"`
	TransactionCurrency string          `json:"transaction_currency"`
	BillingAmount       json.RawMessage `json:"billing_amount"`
	BillingCurrency     string          `json:"billing_currency"`
	CreatedAt           string          `json:"created_at"`
	TransactedAt        string          `json:"transacted_at"`
	UpdatedAt           string          `json:"updated_at"`
}

// Event retrieves only an already verified event reference in a trusted account scope.
func (a *Attempt) Event(ctx context.Context, accountID, id string) (Event, error) {
	raw, err := a.get(ctx, "/api/v1/issuing/card_transaction_events/"+url.PathEscape(id), accountID, true)
	if err != nil {
		return Event{}, err
	}
	var event Event
	if json.Unmarshal(raw, &event) != nil || event.ID == "" || event.ID != id {
		return Event{}, &domain.Failure{Reason: "EVENT_REFERENCE_MISMATCH"}
	}
	return event, nil
}

// Events follows all page_after cursors, keeping the parent transaction filter.
func (a *Attempt) Events(ctx context.Context, accountID, transactionID string) ([]Event, error) {
	query := url.Values{"card_transaction_id": {transactionID}, "page_size": {"100"}}
	var events []Event
	seen := make(map[string]bool)
	for {
		raw, err := a.get(ctx, "/api/v1/issuing/card_transaction_events?"+query.Encode(), accountID, true)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items     []Event `json:"items"`
			PageAfter string  `json:"page_after"`
		}
		if json.Unmarshal(raw, &page) != nil || page.Items == nil {
			return nil, &domain.Failure{Reason: "PROVIDER_RESPONSE_INVALID"}
		}
		events = append(events, page.Items...)
		if page.PageAfter == "" {
			return events, nil
		}
		if seen[page.PageAfter] {
			return nil, &domain.Failure{Reason: "PROVIDER_PAGINATION_INVALID"}
		}
		seen[page.PageAfter] = true
		query.Set("page", page.PageAfter)
	}
}

// BalanceObservation retains service UTC times around the available cash request.
type BalanceObservation struct {
	AvailableCents int64
	RequestedAt    time.Time
	ObservedAt     time.Time
}

// Balance requires one explicit USD cash entry and exact available_amount cents.
func (a *Attempt) Balance(ctx context.Context, accountID string) (BalanceObservation, error) {
	requested := a.client.now().UTC()
	raw, err := a.get(ctx, "/api/v1/balances/current?account_type=cash", accountID, false)
	if err != nil {
		return BalanceObservation{}, err
	}
	observed := a.client.now().UTC()
	var entries []struct {
		AccountType string          `json:"account_type"`
		Currency    string          `json:"currency"`
		Available   json.RawMessage `json:"available_amount"`
	}
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return BalanceObservation{}, &domain.Failure{Reason: "BALANCE_RESPONSE_INVALID"}
	}
	var amount json.RawMessage
	count := 0
	for _, entry := range entries {
		if entry.Currency == "USD" && entry.AccountType == "" {
			return BalanceObservation{}, &domain.Failure{Reason: "BALANCE_ACCOUNT_TYPE_MISSING"}
		}
		if entry.Currency == "USD" && entry.AccountType == "cash" {
			count++
			amount = entry.Available
		}
	}
	if count != 1 {
		return BalanceObservation{}, &domain.Failure{Reason: "USD_CASH_BALANCE_UNAVAILABLE_OR_AMBIGUOUS"}
	}
	cents, err := domain.USDCents(amount)
	if err != nil || cents < 0 {
		return BalanceObservation{}, &domain.Failure{Reason: "BALANCE_AMOUNT_INVALID"}
	}
	return BalanceObservation{AvailableCents: cents, RequestedAt: requested, ObservedAt: observed}, nil
}
