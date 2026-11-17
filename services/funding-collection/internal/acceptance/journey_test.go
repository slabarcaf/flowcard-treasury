package acceptance_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestServicingJourney(t *testing.T) {
	ctx, h := newJourney(t)
	funding := settledFunding(t, ctx, h)
	firstDeposit, secondDeposit := repayFunding(t, ctx, h, funding)
	bridgeResponse, err := h.Request(http.MethodGet, "/api/bridges/"+funding.BridgeID,
		"coordinator-reader", servicefixture.CoordinatorKey, "", nil)
	if err != nil || bridgeResponse.StatusCode != http.StatusOK {
		t.Fatalf("final bridge read status = %v err %v, want 200", journeyResponseStatus(bridgeResponse), err)
	}
	var bridge struct {
		Lifecycle       string     `json:"lifecycle"`
		SourceBindingID string     `json:"source_binding_id"`
		FundedAt        *time.Time `json:"funded_at"`
		PrincipalOwed   *int64     `json:"principal_owed_cents"`
		ContractFeeOwed *int64     `json:"contract_fee_owed_cents"`
		LateFeeOwed     *int64     `json:"late_fee_owed_cents"`
		HoldReasons     []string   `json:"hold_reasons"`
		Operations      []struct {
			Kind                string  `json:"kind"`
			State               string  `json:"state"`
			SettlementReference *string `json:"last_verified_settlement_reference"`
		} `json:"operations"`
	}
	if err := json.NewDecoder(bridgeResponse.Body).Decode(&bridge); err != nil {
		bridgeResponse.Body.Close()
		t.Fatalf("decode final bridge read: %v", err)
	}
	bridgeResponse.Body.Close()
	fundingSettled := false
	for _, operation := range bridge.Operations {
		if operation.Kind == "FUND" && operation.State == "SETTLED" &&
			operation.SettlementReference != nil && *operation.SettlementReference != "" {
			fundingSettled = true
		}
	}
	if bridge.Lifecycle != "REPAID" || bridge.SourceBindingID != h.Config.SourceBindingID ||
		bridge.PrincipalOwed == nil || *bridge.PrincipalOwed != 0 || bridge.ContractFeeOwed == nil ||
		*bridge.ContractFeeOwed != 0 || bridge.LateFeeOwed == nil || *bridge.LateFeeOwed != 0 ||
		bridge.FundedAt == nil || len(bridge.HoldReasons) != 0 || !fundingSettled {
		t.Fatalf("final bridge read = %+v, want source-bound ever-funded zero-debt closure", bridge)
	}
	portfolioResponse, readErr := h.Request(http.MethodGet, "/api/servicing/portfolio", servicefixture.OperatorSubject, servicefixture.OperatorSecret, "", nil)
	if readErr != nil || portfolioResponse.StatusCode != 200 {
		t.Fatal("portfolio read after settlement failed")
	}
	var portfolio struct {
		Collected *int64 `json:"fees_collected_cents"`
	}
	if json.NewDecoder(portfolioResponse.Body).Decode(&portfolio) != nil {
		t.Fatal("portfolio response invalid")
	}
	portfolioResponse.Body.Close()
	if portfolio.Collected == nil || *portfolio.Collected != 19000 {
		t.Fatalf("net settled fee metric=%v, want 19000", portfolio.Collected)
	}
	stateResponse, err := h.Request(http.MethodGet, "/api/servicing/state?customer_id="+servicefixture.CustomerID,
		"underwriting-reader", servicefixture.ReaderKey, "", nil)
	if err != nil || stateResponse.StatusCode != http.StatusOK {
		t.Fatalf("final servicing state status = %v err %v, want 200", journeyResponseStatus(stateResponse), err)
	}
	var finalState struct {
		Facility struct {
			FacilityID string `json:"facility_id"`
		} `json:"facility"`
		Customer struct {
			CustomerID        string `json:"customer_id"`
			PrincipalOwed     int64  `json:"principal_owed_cents"`
			UnpaidFee         int64  `json:"unpaid_fee_cents"`
			ReservedPrincipal int64  `json:"reserved_principal_cents"`
			ReservedFee       int64  `json:"reserved_fee_cents"`
			ActiveHold        bool   `json:"active_hold"`
		} `json:"customer"`
		Invoices []struct {
			InvoiceID     string `json:"invoice_id"`
			ReservedCents int64  `json:"reserved_cents"`
		} `json:"invoices"`
		History []struct {
			BridgeID             string     `json:"bridge_id"`
			Lifecycle            string     `json:"lifecycle"`
			PrincipalPaidCents   int64      `json:"principal_paid_cents"`
			ContractFeePaidCents int64      `json:"contract_fee_paid_cents"`
			ClosedAt             *time.Time `json:"closed_at"`
		} `json:"history"`
		FundingAuthorized bool `json:"funding_authorized"`
	}
	if err := json.NewDecoder(stateResponse.Body).Decode(&finalState); err != nil {
		stateResponse.Body.Close()
		t.Fatalf("decode final servicing state: %v", err)
	}
	stateResponse.Body.Close()
	if finalState.Facility.FacilityID != servicefixture.FacilityID ||
		finalState.Customer.CustomerID != servicefixture.CustomerID || finalState.Customer.PrincipalOwed != 0 ||
		finalState.Customer.UnpaidFee != 0 || finalState.Customer.ReservedPrincipal != 0 ||
		finalState.Customer.ReservedFee != 0 || finalState.Customer.ActiveHold || finalState.FundingAuthorized ||
		len(finalState.Invoices) != 1 || finalState.Invoices[0].InvoiceID != servicefixture.InvoiceID ||
		finalState.Invoices[0].ReservedCents != 0 || len(finalState.History) != 1 ||
		finalState.History[0].BridgeID != funding.BridgeID || finalState.History[0].Lifecycle != "REPAID" ||
		finalState.History[0].PrincipalPaidCents != 700_000 || finalState.History[0].ContractFeePaidCents != 19_000 ||
		finalState.History[0].ClosedAt == nil {
		t.Fatalf("final scoped read model = %+v, want zero debt, released backing and complete $7190 repayment", finalState)
	}

	// Ever-funded canonical payment identity remains blocked after repayment.
	response, err := h.Request(http.MethodPost, "/api/offers", servicefixture.OperatorSubject,
		servicefixture.OperatorSecret, "journey-ever-funded-retry", []byte(`{"evaluation_id":"synthetic-evaluation"}`))
	if err != nil || response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ever-funded payment offer retry status = %v err %v, want 422", journeyResponseStatus(response), err)
	}
	response.Body.Close()

	if !hasPath(h.ProviderHTTP.Requests(), "/__fixture/payments/"+servicefixture.TransactionID) ||
		!hasPath(h.ProviderHTTP.Requests(), "/api/v1/balances/current") ||
		!hasPath(h.ProviderHTTP.Requests(), "/__fixture/deposits/"+firstDeposit.DepositID) ||
		!hasPath(h.ProviderHTTP.Requests(), "/__fixture/deposits/"+secondDeposit.DepositID) ||
		h.ProviderHTTP.CreateRequestCount() != 4 {
		t.Fatalf("provider HTTP evidence is incomplete: creates=%d requests=%+v", h.ProviderHTTP.CreateRequestCount(), h.ProviderHTTP.Requests())
	}
}
