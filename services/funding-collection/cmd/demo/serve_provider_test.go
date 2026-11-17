package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

func TestStandaloneProviderReads(t *testing.T) {
	const borrower = "synthetic-borrower"
	const funding = "synthetic-funding"
	const parent = "synthetic-parent"
	original := providerPayment{CustomerID: "synthetic-customer", AccountID: borrower,
		AuthorizationEventID: "synthetic-event", AttemptedCents: 900_000, ShortfallCents: 700_000}
	double := &providerDouble{state: providerState{
		Accounts: map[string]int64{borrower: 200_000, funding: 100_000_000},
		Payments: map[string]providerPayment{parent: original},
	}}
	// Exercise the standalone handlers, rather than the in-process service
	// fixture's separate provider implementation.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/authentication/login", double.login)
	mux.HandleFunc("GET /api/v1/balances/current", double.readBalance)
	mux.HandleFunc("GET /__fixture/payments/{id}", double.readPayment)
	server := httptest.NewServer(providerJSONHandler(mux))
	defer server.Close()
	adapter, err := provider.NewAirwallexAdapter(provider.AdapterOptions{
		Mode: "DEMO", ProviderMode: "MOCK_HTTP", BaseURL: server.URL,
		FundingAccountID: funding, ClientID: config.DemoClientID, APIKey: config.DemoAPIKey,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	readStarted := time.Now().UTC()
	payment, err := adapter.Payment(ctx, borrower, parent)
	if err != nil || payment.CustomerID != original.CustomerID || payment.CanonicalAccountID != borrower ||
		payment.ParentTransactionID != parent || payment.AuthorizationEventID != original.AuthorizationEventID ||
		payment.AttemptedAmountCents != 900_000 || payment.AvailableCents != 200_000 || payment.ShortfallCents != 700_000 ||
		payment.ObservedAt.Before(readStarted) || payment.BalanceObservedAt.Before(readStarted) {
		t.Fatalf("Payment(initial) = %+v err %v, want current scoped $7000 need with unchanged event identity", payment, err)
	}
	balance, err := adapter.Balance(ctx, borrower)
	if err != nil || balance.AvailableCents != 200_000 || balance.AccountRef != borrower {
		t.Fatalf("Balance(borrower) = %+v err %v, want exact 200000 cents", balance, err)
	}
	capital, err := adapter.Balance(ctx, funding)
	if err != nil || capital.AvailableCents != 100_000_000 || capital.AccountRef != funding {
		t.Fatalf("Balance(funding) = %+v err %v, want exact 100000000 cents", capital, err)
	}

	// A provider-owned balance change must produce a new current-need read,
	// including a known zero, without changing the original payment facts.
	double.mu.Lock()
	double.state.Accounts[borrower] = 950_000
	double.mu.Unlock()
	readStarted = time.Now().UTC()
	payment, err = adapter.Payment(ctx, borrower, parent)
	if err != nil || payment.AvailableCents != 950_000 || payment.ShortfallCents != 0 ||
		payment.AttemptedAmountCents != original.AttemptedCents || payment.AuthorizationEventID != original.AuthorizationEventID ||
		payment.ObservedAt.Before(readStarted) || payment.BalanceObservedAt.Before(readStarted) {
		t.Fatalf("Payment(after provider balance change) = %+v err %v, want a freshly observed known-zero need", payment, err)
	}
	double.mu.Lock()
	stored := double.state.Payments[parent]
	double.mu.Unlock()
	if stored != original {
		t.Fatalf("stored payment = %+v, want unchanged original %+v", stored, original)
	}
}
