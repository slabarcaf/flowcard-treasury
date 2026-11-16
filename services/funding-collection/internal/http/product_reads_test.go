package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	fundinghttp "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/database"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestProductReadsScopeAndPagination(t *testing.T) {
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	book, err := database.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := book.OpenRuntimePool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	customers := []domain.FacilityCustomerImport{}
	for i := 0; i < 27; i++ {
		id := fmt.Sprintf("product-customer-%02d", i)
		account := "product-account-" + id
		card := "product-card-" + id
		if err := book.SeedMapping(ctx, database.Mapping{CustomerID: id, AccountID: account, CardID: card}); err != nil {
			t.Fatal(err)
		}
		if i < 26 {
			customers = append(customers, domain.FacilityCustomerImport{CustomerID: id, AccountID: account, CardID: card, HistoryComplete: true})
		}
	}
	ref := domain.SourceRef{System: "synthetic-product-fixture", RecordID: "read-input", Version: "1", EffectiveAt: now}
	metadata := func(id string) domain.ImportMetadata {
		return domain.ImportMetadata{SchemaVersion: 1, SourceMode: "DEMO", SourceID: id, SourceVersion: "1", CapturedAt: now, SourceRefs: []domain.SourceRef{ref}}
	}
	repo := postgres.NewRepository(pool)
	if _, err := repo.ImportFacility(ctx, domain.FacilityImport{ImportMetadata: metadata("product-facility"), FacilityID: "product-facility", FundingAccountRef: "product-funding-wallet", Currency: "USD", FacilityTotalCents: 100000000, ReserveFloorBPS: 2000, KnownEmptyBooks: true, Customers: customers}, "product-facility", now); err != nil {
		t.Fatal(err)
	}
	customerID := "product-customer-00"
	if _, err := repo.ImportActors(ctx, domain.ActorsImport{ImportMetadata: metadata("product-actors"), Actors: []domain.ActorImport{
		{ActorID: "product-op", ActorType: "OPERATOR", AuthenticationSubject: servicefixture.OperatorSubject, Active: true, SourceRef: ref},
		{ActorID: "product-customer", ActorType: "CUSTOMER", AuthenticationSubject: servicefixture.CustomerSubject, CustomerID: &customerID, Active: true, SourceRef: ref},
	}}, "product-facility", now); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Mode: "DEMO", DatabaseURL: book.DatabaseURL, FacilityID: "product-facility", SourceBindingID: "product-installation", OperatorSecret: servicefixture.OperatorSecret, CustomerSecret: servicefixture.CustomerSecret, ReaderSecret: servicefixture.ReaderKey, CoordinatorSecret: servicefixture.CoordinatorKey}
	api := httptest.NewServer(fundinghttp.NewHandler(pool, cfg))
	defer api.Close()
	get := func(path, subject, secret string, want int) map[string]any {
		t.Helper()
		req, err := http.NewRequest("GET", api.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+servicefixture.Token(subject, secret, time.Now().UTC()))
		response, err := api.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("GET %s status=%d, want %d", path, response.StatusCode, want)
		}
		var body map[string]any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	first := get("/api/servicing/customers", servicefixture.OperatorSubject, servicefixture.OperatorSecret, 200)
	if len(first["items"].([]any)) != 25 {
		t.Fatal("customer page is not bounded to 25")
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("next cursor missing")
	}
	second := get("/api/servicing/customers?cursor="+url.QueryEscape(cursor), servicefixture.OperatorSubject, servicefixture.OperatorSecret, 200)
	rows := second["items"].([]any)
	if len(rows) != 2 || second["next_cursor"] != nil {
		t.Fatal("second customer page incomplete")
	}
	missing := rows[1].(map[string]any)
	if missing["accounting_available"] != false || missing["principal_owed_cents"] != nil {
		t.Fatal("missing accounting fabricated zero")
	}
	get("/api/bridges?customer_id="+customerID+"&cursor="+url.QueryEscape(cursor), servicefixture.OperatorSubject, servicefixture.OperatorSecret, 400)
	get("/api/servicing/customers", servicefixture.CustomerSubject, servicefixture.CustomerSecret, 403)
	get("/api/servicing/portfolio", "coordinator-reader", servicefixture.CoordinatorKey, 403)
	get("/api/bridges?customer_id=foreign", servicefixture.CustomerSubject, servicefixture.CustomerSecret, 400)
	own := get("/api/bridges", servicefixture.CustomerSubject, servicefixture.CustomerSecret, 200)
	if len(own["items"].([]any)) != 0 {
		t.Fatal("empty customer has bridges")
	}
	portfolio := get("/api/servicing/portfolio", servicefixture.OperatorSubject, servicefixture.OperatorSecret, 200)
	if portfolio["available_capital_cents"] != float64(80000000) || portfolio["current_regime"] != "ABUNDANT" {
		t.Fatal("capital read differs from shared evaluator")
	}
	var financialRows int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM servicing.operations").Scan(&financialRows); err != nil || financialRows != 0 {
		t.Fatal("read projection changed financial state")
	}
}
