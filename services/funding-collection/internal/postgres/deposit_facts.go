package postgres

import "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"

// depositFacts contains the identity and economic facts that must remain stable.
// Provider timestamp corrections are retained in observations, not treated as
// changes to the deposit's identity or value. Callers still check time eligibility.
type depositFacts struct {
	account, customer, id, currency, status, kind string
	gross, net, fee                               int64
}

func (d depositFacts) matches(o provider.DepositObservation) bool {
	return d.account != "" && d.customer != "" && d.id != "" &&
		d.account == o.AccountRef && d.customer == o.CustomerID && d.id == o.DepositID &&
		d.currency == "USD" && o.Currency == d.currency &&
		d.status == "SETTLED" && o.Status == d.status &&
		d.kind == "EXTERNAL_SETTLED" && o.Kind == d.kind &&
		d.gross > 0 && d.net > 0 && d.fee >= 0 &&
		d.gross == o.GrossCents && d.net == o.NetCents &&
		o.ProviderFeeCents != nil && d.fee == *o.ProviderFeeCents &&
		o.SettledAt != nil && !o.SettledAt.IsZero()
}
