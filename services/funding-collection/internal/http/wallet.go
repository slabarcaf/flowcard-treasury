package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

type walletDTO struct {
	productSource
	CustomerID   string    `json:"customer_id"`
	Currency     string    `json:"currency"`
	Available    int64     `json:"available_balance_cents"`
	Requested    time.Time `json:"requested_at"`
	Observed     time.Time `json:"observed_at"`
	ProviderMode string    `json:"provider_mode"`
}

// readProductWallet performs only a scoped provider balance read. It never
// evaluates credit, moves funds, or treats wallet cash as facility capital.
func (h *handler) readProductWallet(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.productActor(w, r, false)
	if !ok {
		return
	}
	if identity.ActorType != "CUSTOMER" {
		writeError(w, 403, "FORBIDDEN", "Customer scope is required")
		return
	}
	if _, err := strictProductQuery(r); err != nil {
		writeError(w, 400, "INVALID_QUERY", "Wallet scope cannot be selected")
		return
	}
	mapping := func() (string, error) {
		var account string
		err := h.pool.QueryRow(r.Context(), `SELECT r.airwallex_connected_account_id FROM public.customer_roster r JOIN servicing.customers c ON c.mode=$1 AND c.id=r.customer_id JOIN servicing.facilities f ON f.mode=$1 AND f.id=$2 WHERE r.customer_id=$3 AND r.is_active AND r.airwallex_connected_account_id<>f.funding_account_ref`, h.cfg.Mode, h.cfg.FacilityID, identity.CustomerID).Scan(&account)
		return account, err
	}
	account, err := mapping()
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Active wallet mapping is unavailable")
		return
	}
	if err != nil {
		productUnavailable(w)
		return
	}
	observation, err := h.provider.Balance(r.Context(), account)
	now := h.now().UTC()
	if err != nil || observation.AccountRef != account || observation.Currency != "USD" || observation.AvailableCents < 0 || observation.RequestedAt.IsZero() || observation.ObservedAt.Before(observation.RequestedAt) || observation.ObservedAt.After(now.Add(5*time.Second)) || now.Sub(observation.ObservedAt) > 30*time.Second {
		writeError(w, 503, "PROVIDER_BALANCE_UNAVAILABLE", "Current provider balance is unavailable")
		return
	}
	current, err := mapping()
	if err != nil || current != account {
		writeError(w, 409, "MAPPING_CHANGED", "Wallet mapping changed; refresh before reading")
		return
	}
	writeJSON(w, 200, walletDTO{productSource: h.productSource(), CustomerID: identity.CustomerID, Currency: "USD", Available: observation.AvailableCents, Requested: observation.RequestedAt, Observed: observation.ObservedAt, ProviderMode: h.cfg.ProviderMode})
}
