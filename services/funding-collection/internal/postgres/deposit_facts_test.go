package postgres

import (
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

func TestDepositFactsRequireIdentityAndEconomics(t *testing.T) {
	fee := int64(10)
	settled := time.Now().UTC()
	facts := depositFacts{account: "account", customer: "customer", id: "deposit",
		currency: "USD", status: "SETTLED", kind: "EXTERNAL_SETTLED", gross: 10000, net: 9990, fee: fee}
	base := provider.DepositObservation{AccountRef: "account", CustomerID: "customer", DepositID: "deposit",
		Currency: "USD", Status: "SETTLED", Kind: "EXTERNAL_SETTLED", GrossCents: 10000,
		NetCents: 9990, ProviderFeeCents: &fee, SettledAt: &settled}
	for _, tc := range []struct {
		name   string
		change func(*provider.DepositObservation)
		want   bool
	}{
		{"timestamp correction", func(o *provider.DepositObservation) { corrected := settled.Add(time.Second); o.SettledAt = &corrected }, true},
		{"deposit ID", func(o *provider.DepositObservation) { o.DepositID = "other" }, false},
		{"account", func(o *provider.DepositObservation) { o.AccountRef = "other" }, false},
		{"customer", func(o *provider.DepositObservation) { o.CustomerID = "other" }, false},
		{"currency", func(o *provider.DepositObservation) { o.Currency = "EUR" }, false},
		{"gross", func(o *provider.DepositObservation) { o.GrossCents++ }, false},
		{"net", func(o *provider.DepositObservation) { o.NetCents++ }, false},
		{"fee", func(o *provider.DepositObservation) { changed := fee + 1; o.ProviderFeeCents = &changed }, false},
		{"missing fee", func(o *provider.DepositObservation) { o.ProviderFeeCents = nil }, false},
		{"status", func(o *provider.DepositObservation) { o.Status = "REVERSED" }, false},
		{"kind", func(o *provider.DepositObservation) { o.Kind = "OWN_TRANSFER" }, false},
		{"missing time", func(o *provider.DepositObservation) { o.SettledAt = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.change(&o)
			if got := facts.matches(o); got != tc.want {
				t.Fatalf("matching facts=%v, want %v", got, tc.want)
			}
		})
	}
}
