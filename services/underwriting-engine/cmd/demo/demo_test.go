package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/evaluation"
	apihttp "github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/shortfall"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/snapshots"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

func TestUnderwritingContract(t *testing.T) {
	secret := strings.Repeat("s", 40)
	for _, value := range []string{"http://127.0.0.1:8084", "https://shortfalls.example"} {
		if _, err := shortfall.NewClient(value, secret); err != nil {
			t.Errorf("NewClient(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{
		"http://example.com", "http://user@127.0.0.1", "http://127.0.0.1?q=1",
		"http://127.0.0.1#fragment",
	} {
		if _, err := shortfall.NewClient(value, secret); err == nil {
			t.Errorf("NewClient(%q) accepted an unsafe URL", value)
		}
	}

	now := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	customerID, cardID, currency, transactionID := "acct_test", "card_test", "USD", "txn_test"
	attempted, available, shortfallAmount := int64(100), int64(0), int64(100)
	requested, observed := now.Add(-10*time.Second), now.Add(-5*time.Second)
	record := domain.ShortfallRecord{
		ID: "33333333-3333-4333-8333-333333333333", CustomerID: &customerID,
		AccountID: "acct_test", CardID: &cardID, Outcome: "SHORTFALL_DETECTED",
		Currency: &currency, AttemptedAmountCents: &attempted,
		AvailableBalanceCents: &available, ShortfallCents: &shortfallAmount,
		CanonicalEventID: "canonical-test", TransactionID: &transactionID,
		CreatedAt: now.Add(-time.Minute), BalanceRequestedAt: &requested,
		BalanceObservedAt: &observed, RequiresRecheck: true,
		AmountSemantics: "current_need_at_balance_observation",
	}
	zeroBody, err := json.Marshal(record)
	if err != nil {
		t.Fatal("marshal zero-balance shortfall:", err)
	}
	serveBody := func(body []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
	}
	zeroServer := serveBody(zeroBody)
	zeroClient, err := shortfall.NewClient(zeroServer.URL, secret)
	if err != nil {
		t.Fatal("NewClient(loopback) error:", err)
	}
	zeroRecord, err := zeroClient.Get(context.Background(), record.ID, now)
	zeroServer.Close()
	if err != nil || zeroRecord.AvailableBalanceCents == nil || *zeroRecord.AvailableBalanceCents != 0 {
		t.Fatalf("shortfall Get(explicit zero) = %v err %v, want known zero", zeroRecord.AvailableBalanceCents, err)
	}
	nullBody := mutateFixtureJSON(t, zeroBody, func(root map[string]any) {
		root["available_balance_cents"] = nil
	})
	missingBody := mutateFixtureJSON(t, zeroBody, func(root map[string]any) {
		delete(root, "available_balance_cents")
	})
	for _, test := range []struct {
		name string
		body []byte
	}{{name: "null balance", body: nullBody}, {name: "missing balance", body: missingBody}} {
		t.Run(test.name, func(t *testing.T) {
			server := serveBody(test.body)
			defer server.Close()
			client, err := shortfall.NewClient(server.URL, secret)
			if err != nil {
				t.Fatal("NewClient(loopback) error:", err)
			}
			if _, err := client.Get(context.Background(), record.ID, now); !errors.Is(err, shortfall.ErrResponseInvalid) {
				t.Fatalf("Get(%s) error = %v, want ErrResponseInvalid", test.name, err)
			}
		})
	}

	if !validDemoDatabaseName(defaultDemoDatabaseName) ||
		!validDemoDatabaseName(defaultDemoDatabaseName+"_task_f_20261114") {
		t.Fatal("validDemoDatabaseName() rejected a supported task-owned name")
	}
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal("repositoryRoot() error:", err)
	}
	invalidRunID, err := randomUUID()
	if err != nil {
		t.Fatal("randomUUID() error:", err)
	}
	invalidOutput := filepath.Join(
		".dev",
		"invalid-book-profile-"+strings.ReplaceAll(invalidRunID, "-", ""),
	)
	invalidOutputPath := filepath.Join(root, invalidOutput)
	t.Cleanup(func() {
		if err := os.RemoveAll(invalidOutputPath); err != nil {
			t.Errorf("remove invalid-profile test output: %v", err)
		}
	})
	if err := runPrepare([]string{"--book-profile", "unsupported", "--out", invalidOutput}); err == nil {
		t.Fatal("runPrepare(unsupported profile) succeeded")
	}
	if _, err := os.Stat(invalidOutputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runPrepare(unsupported profile) created output, stat error = %v", err)
	}
	for _, name := range []string{
		"flowcard_underwriting_demo_test;DROP DATABASE postgres",
		"flowcard_underwriting_demo_UPPER", "other_database",
		defaultDemoDatabaseName + strings.Repeat("x", 64),
	} {
		if validDemoDatabaseName(name) {
			t.Errorf("validDemoDatabaseName(%q) accepted an unsafe name", name)
		}
	}

	bundleRoot, manifest, anchor := makeTestBundle(t, defaultDemoDatabaseName+"_task_f_contract")
	if manifest.DatabaseName != defaultDemoDatabaseName+"_task_f_contract" {
		t.Fatalf("generated database name = %q", manifest.DatabaseName)
	}
	emptyBundleRoot, emptyManifest, _ := makeTestBundleWithProfile(t,
		defaultDemoDatabaseName+"_task_f_known_empty", knownEmptyBookProfile)
	if emptyManifest.Provenance == manifest.Provenance || !strings.Contains(emptyManifest.Provenance, "known-empty") {
		t.Fatalf("known-empty manifest provenance = %q", emptyManifest.Provenance)
	}
	if emptyManifest.FacilityBooks.FacilityTotalCents != 100_000_000 ||
		emptyManifest.FacilityBooks.ReserveFloorBPS != 2000 ||
		emptyManifest.FacilityBooks.FundedPrincipalCents != 0 ||
		emptyManifest.FacilityBooks.CustomerFundedPrincipalCents != 0 ||
		emptyManifest.FacilityBooks.OtherFundedPrincipalCents != 0 ||
		emptyManifest.FacilityBooks.ReservedPrincipalCents != 0 ||
		emptyManifest.FacilityBooks.CustomerReservedPrincipalCents != 0 ||
		emptyManifest.FacilityBooks.OtherReservedPrincipalCents != 0 ||
		emptyManifest.FacilityBooks.AvailableCapitalCents != 80_000_000 {
		t.Fatalf("known-empty facility books = %+v", emptyManifest.FacilityBooks)
	}
	wantEmptyScenarios := map[string]struct {
		limitCents          int64
		monthlyInflowsCents int64
		status              string
		capacityRuleStatus  string
	}{
		"eligible":                 {limitCents: 2_000_000, monthlyInflowsCents: 10_000_000, status: "ELIGIBLE", capacityRuleStatus: "PASS"},
		"capacity_exceeded":        {limitCents: 600_000, monthlyInflowsCents: 3_000_000, status: "INELIGIBLE", capacityRuleStatus: "FAIL"},
		"missing_receivable_dates": {limitCents: 2_000_000, monthlyInflowsCents: 10_000_000, status: "REVIEW_REQUIRED", capacityRuleStatus: "PASS"},
	}
	traceData, err := os.ReadFile(filepath.Join(emptyBundleRoot, filepath.FromSlash(emptyManifest.ExpectedTraceFile)))
	if err != nil {
		t.Fatal("read known-empty expected traces:", err)
	}
	var traces []generatedScenario
	if err := json.Unmarshal(traceData, &traces); err != nil || len(traces) != len(wantEmptyScenarios) {
		t.Fatalf("decode known-empty expected traces: count %d, error %v", len(traces), err)
	}
	if len(emptyManifest.Scenarios) != len(wantEmptyScenarios) {
		t.Fatalf("known-empty scenario count = %d, want %d", len(emptyManifest.Scenarios), len(wantEmptyScenarios))
	}
	for i, scenario := range emptyManifest.Scenarios {
		want, ok := wantEmptyScenarios[scenario.ID]
		if !ok {
			t.Fatalf("unexpected known-empty scenario %q", scenario.ID)
		}
		if scenario.PrincipalCents != 700_000 || scenario.EstimatedFeeCents != 19_000 ||
			scenario.TotalDueCents != 719_000 || scenario.CustomerLimitCents != want.limitCents ||
			scenario.ExistingExposureCents != 0 || scenario.PostFundingExposureCents != 719_000 ||
			scenario.ExpectedStatus != want.status || !strings.Contains(scenario.Provenance, "known-empty") {
			t.Fatalf("known-empty scenario %q = %+v", scenario.ID, scenario)
		}
		expectedRulesMatch := len(traces[i].ExpectedRules) == len(scenario.ExpectedRules)
		for ruleIndex := range scenario.ExpectedRules {
			if !expectedRulesMatch || traces[i].ExpectedRules[ruleIndex] != scenario.ExpectedRules[ruleIndex] {
				expectedRulesMatch = false
				break
			}
		}
		if traces[i].ID != scenario.ID || traces[i].CustomerLimitCents != scenario.CustomerLimitCents ||
			traces[i].ExistingExposureCents != 0 || traces[i].PostFundingExposureCents != 719_000 ||
			traces[i].ExpectedStatus != scenario.ExpectedStatus || !expectedRulesMatch {
			t.Fatalf("known-empty expected trace %q = %+v, scenario = %+v", traces[i].ID, traces[i], scenario)
		}
		capacityRuleStatus, receivableRuleStatus := "", ""
		for _, rule := range scenario.ExpectedRules {
			switch rule.RuleID {
			case "CUSTOMER_CAPACITY":
				capacityRuleStatus = rule.Status
			case "RECEIVABLE_COVERAGE":
				receivableRuleStatus = rule.Status
			}
		}
		if capacityRuleStatus != want.capacityRuleStatus {
			t.Fatalf("known-empty scenario %q capacity rule = %q, want %q", scenario.ID, capacityRuleStatus, want.capacityRuleStatus)
		}
		if scenario.ID == "missing_receivable_dates" && receivableRuleStatus != "UNKNOWN" {
			t.Fatalf("known-empty missing-date receivable rule = %q, want UNKNOWN", receivableRuleStatus)
		}
		customerBytes, err := os.ReadFile(filepath.Join(emptyBundleRoot, filepath.FromSlash(scenario.CustomerFile)))
		if err != nil {
			t.Fatal("read known-empty customer snapshot:", err)
		}
		var customer domain.CustomerSnapshot
		if err := json.Unmarshal(customerBytes, &customer); err != nil {
			t.Fatal("decode known-empty customer snapshot:", err)
		}
		wantCoverage := domain.Coverage{
			Standing: true, BalanceHistory: true, InflowHistory: true,
			BridgeHistory: true, LifetimeDefaults: true, Reservations: true,
			Receivables: true, Classification: true,
		}
		if customer.Bridges == nil || len(customer.Bridges) != 0 ||
			customer.Reservations == nil || len(customer.Reservations) != 0 ||
			customer.Coverage != wantCoverage || !strings.Contains(customer.SourceID, "known-empty-books") ||
			len(customer.SourceRefs) != 5 {
			t.Fatalf("known-empty customer books/source = bridges %+v reservations %+v coverage %+v source %q",
				customer.Bridges, customer.Reservations, customer.Coverage, customer.SourceID)
		}
		if len(customer.InflowHistory) != 3 {
			t.Fatalf("known-empty scenario %q inflow count = %d, want 3", scenario.ID, len(customer.InflowHistory))
		}
		var totalInflows int64
		for _, inflow := range customer.InflowHistory {
			if inflow.AmountCents != want.monthlyInflowsCents {
				t.Fatalf("known-empty scenario %q inflow = %d, want %d",
					scenario.ID, inflow.AmountCents, want.monthlyInflowsCents)
			}
			totalInflows += inflow.AmountCents
		}
		if totalInflows/int64(len(customer.InflowHistory)) != want.monthlyInflowsCents {
			t.Fatalf("known-empty scenario %q average inflows = %d, want %d",
				scenario.ID, totalInflows/int64(len(customer.InflowHistory)), want.monthlyInflowsCents)
		}
		if customer.SourceRefs[1].RecordID != "known-empty-bridge-book-"+scenario.ID ||
			customer.SourceRefs[2].RecordID != "known-empty-reservation-book-"+scenario.ID {
			t.Fatalf("known-empty scenario %q book source refs = %+v", scenario.ID, customer.SourceRefs)
		}
		if scenario.ID == "missing_receivable_dates" {
			if len(customer.Receivables) != 1 || customer.Receivables[0].DueOn != nil || customer.Receivables[0].DueDateVerified {
				t.Fatalf("known-empty missing-date receivable = %+v", customer.Receivables)
			}
		} else if len(customer.Receivables) != 1 || customer.Receivables[0].DueOn == nil || !customer.Receivables[0].DueDateVerified {
			t.Fatalf("known-empty scenario %q receivable facts changed: %+v", scenario.ID, customer.Receivables)
		}
		if len(customer.Classifications) != 1 || customer.Classifications[0].TransactionID != scenario.TransactionID ||
			customer.Classifications[0].MerchantCategory != "suppliers" {
			t.Fatalf("known-empty scenario %q merchant/payment classification changed: %+v",
				scenario.ID, customer.Classifications)
		}
	}
	facilityData, err := os.ReadFile(filepath.Join(emptyBundleRoot, filepath.FromSlash(emptyManifest.FacilityFile)))
	if err != nil {
		t.Fatal("read known-empty facility snapshot:", err)
	}
	var emptyFacility domain.FacilitySnapshot
	if err := json.Unmarshal(facilityData, &emptyFacility); err != nil {
		t.Fatal("decode known-empty facility snapshot:", err)
	}
	if emptyFacility.FacilityTotalCents != 100_000_000 || emptyFacility.ReserveFloorBPS != 2000 ||
		emptyFacility.FundedPrincipalCents != 0 || emptyFacility.ReservedPrincipalCents != 0 ||
		!emptyFacility.LedgerComplete ||
		!strings.Contains(emptyFacility.SourceID, "known-empty-books") {
		t.Fatalf("known-empty facility snapshot = %+v", emptyFacility)
	}
	mappingData, err := os.ReadFile(filepath.Join(emptyBundleRoot, filepath.FromSlash(emptyManifest.MappingSetupSQL)))
	if err != nil {
		t.Fatal("read known-empty mapping setup:", err)
	}
	if !strings.Contains(string(mappingData), "current_database() <> '"+emptyManifest.DatabaseName+"'") {
		t.Fatalf("known-empty mapping SQL does not guard database %q", emptyManifest.DatabaseName)
	}
	shortfallsData, err := os.ReadFile(filepath.Join(emptyBundleRoot, filepath.FromSlash(emptyManifest.ShortfallsFile)))
	if err != nil {
		t.Fatal("read known-empty shortfall fixtures:", err)
	}
	var shortfalls shortfallFixturePack
	if err := json.Unmarshal(shortfallsData, &shortfalls); err != nil || len(shortfalls.Records) != len(wantEmptyScenarios) {
		t.Fatalf("decode known-empty shortfall fixtures: count %d, error %v", len(shortfalls.Records), err)
	}
	for _, record := range shortfalls.Records {
		if record.Response.ShortfallCents == nil || *record.Response.ShortfallCents != 700_000 ||
			record.Response.AttemptedAmountCents == nil || *record.Response.AttemptedAmountCents != 900_000 ||
			record.Response.AvailableBalanceCents == nil || *record.Response.AvailableBalanceCents != 200_000 {
			t.Fatalf("known-empty scenario %q shortfall/payment facts changed: %+v",
				record.ScenarioID, record.Response)
		}
	}
	loadedRoot, loaded, err := loadGeneratedManifest(filepath.Join(bundleRoot, "manifest.json"))
	if err != nil || loadedRoot != bundleRoot || loaded.RunID != manifest.RunID {
		t.Fatalf("loadGeneratedManifest(portable) = root %q run %q err %v",
			loadedRoot, loaded.RunID, err)
	}
	retainedPath := filepath.Join(bundleRoot, "runs", manifest.RunID, "manifest.json")
	if _, _, err := loadGeneratedManifest(retainedPath); err != nil {
		t.Fatalf("loadGeneratedManifest(retained run) error = %v", err)
	}
	listener, err := net.Listen("tcp4", demoShortfallAddress)
	if errors.Is(err, syscall.EADDRINUSE) {
		t.Logf("skip local serve-shortfalls check because %s is already in use", demoShortfallAddress)
	} else if err != nil {
		t.Fatal("DEMO shortfall port preflight:", err)
	} else {
		if err := listener.Close(); err != nil {
			t.Fatal("release shortfall port preflight:", err)
		}
		t.Setenv("UNDERWRITING_MODE", "DEMO")
		t.Setenv("SHORTFALL_SERVICE_SECRET", secret)
		serveContext, stopServe := context.WithCancel(context.Background())
		serveDone := make(chan error, 1)
		go func() {
			serveDone <- runServeShortfalls(serveContext,
				[]string{"--manifest", filepath.Join(bundleRoot, "manifest.json")})
		}()
		if err := waitForDemoShortfall(t, demoShortfallAddress); err != nil {
			stopServe()
			<-serveDone
			t.Fatal("serve-shortfalls did not become available:", err)
		}
		servedClient, err := shortfall.NewClient("http://"+demoShortfallAddress, secret)
		if err != nil {
			stopServe()
			<-serveDone
			t.Fatal("NewClient(DEMO mock) error:", err)
		}
		servedRecord, err := servedClient.Get(context.Background(), manifest.Scenarios[0].ShortfallID, time.Now().UTC())
		if err != nil || servedRecord.ID != manifest.Scenarios[0].ShortfallID {
			stopServe()
			<-serveDone
			t.Fatalf("authenticated serve-shortfalls GET = %q err %v", servedRecord.ID, err)
		}
		stopServe()
		if err := <-serveDone; err != nil {
			t.Fatal("serve-shortfalls shutdown:", err)
		}
	}

	customerPath := filepath.Join(bundleRoot, filepath.FromSlash(manifest.Scenarios[0].CustomerFile))
	customerBytes, err := os.ReadFile(customerPath)
	if err != nil {
		t.Fatal("read generated customer fixture:", err)
	}
	if _, _, err := snapshots.DecodeCustomer(customerBytes, "DEMO", anchor.Add(time.Second)); err != nil {
		t.Fatalf("DecodeCustomer(valid fixture) error = %v", err)
	}
	for _, mutate := range []struct {
		name string
		edit func(map[string]any)
	}{
		{name: "coverage null", edit: func(root map[string]any) {
			coverage := root["coverage"].(map[string]any)
			coverage["standing"] = nil
		}},
		{name: "complete standing null", edit: func(root map[string]any) {
			standing := root["standing"].(map[string]any)
			standing["active"] = nil
		}},
		{name: "bridge fee null", edit: func(root map[string]any) {
			bridges := root["bridges"].([]any)
			bridges[0].(map[string]any)["fees_owed_cents"] = nil
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			changed := mutateFixtureJSON(t, customerBytes, mutate.edit)
			if _, _, err := snapshots.DecodeCustomer(changed, "DEMO", anchor.Add(time.Second)); err == nil {
				t.Fatal("DecodeCustomer() accepted a required null scalar")
			}
		})
	}
	incompleteStanding := mutateFixtureJSON(t, customerBytes, func(root map[string]any) {
		coverage := root["coverage"].(map[string]any)
		coverage["standing"] = false
		standing := root["standing"].(map[string]any)
		standing["active"] = nil
	})
	if _, _, err := snapshots.DecodeCustomer(incompleteStanding, "DEMO", anchor.Add(time.Second)); err != nil {
		t.Fatalf("DecodeCustomer(incomplete nullable standing) error = %v", err)
	}
	closedDefaultDebt := mutateFixtureJSON(t, customerBytes, func(root map[string]any) {
		bridge := root["bridges"].([]any)[0].(map[string]any)
		bridge["status"] = "DEFAULTED"
		bridge["due_on"] = anchor.AddDate(0, 0, -30).Format("2006-01-02")
		bridge["defaulted_on"] = anchor.AddDate(0, 0, -20).Format("2006-01-02")
		bridge["closed_on"] = anchor.AddDate(0, 0, -5).Format("2006-01-02")
		bridge["repayment_outcome"] = "LATE"
		bridge["principal_owed_cents"] = 1
		bridge["fees_owed_cents"] = 0
	})
	if _, _, err := snapshots.DecodeCustomer(closedDefaultDebt, "DEMO", anchor.Add(time.Second)); err == nil {
		t.Fatal("DecodeCustomer() accepted remaining debt on a closed defaulted bridge")
	}
	openDefaultedDebt := mutateFixtureJSON(t, customerBytes, func(root map[string]any) {
		bridge := root["bridges"].([]any)[0].(map[string]any)
		bridge["status"] = "DEFAULTED"
		bridge["due_on"] = anchor.AddDate(0, 0, -30).Format("2006-01-02")
		bridge["defaulted_on"] = anchor.AddDate(0, 0, -20).Format("2006-01-02")
		bridge["closed_on"] = nil
		bridge["repayment_outcome"] = nil
	})
	if _, _, err := snapshots.DecodeCustomer(openDefaultedDebt, "DEMO", anchor.Add(time.Second)); err != nil {
		t.Fatalf("DecodeCustomer(open defaulted debt) error = %v", err)
	}

	facilityPath := filepath.Join(bundleRoot, filepath.FromSlash(manifest.FacilityFile))
	facilityBytes, err := os.ReadFile(facilityPath)
	if err != nil {
		t.Fatal("read generated facility fixture:", err)
	}
	facilityWithNull := mutateFixtureJSON(t, facilityBytes, func(root map[string]any) {
		root["reserve_floor_bps"] = nil
	})
	if _, _, err := snapshots.DecodeFacility(facilityWithNull, "DEMO", manifest.FacilityID,
		anchor.Add(time.Second)); err == nil {
		t.Fatal("DecodeFacility() accepted null reserve_floor_bps")
	}

	missingDatePath := filepath.Join(bundleRoot,
		filepath.FromSlash(manifest.Scenarios[2].CustomerFile))
	missingDateBytes, err := os.ReadFile(missingDatePath)
	if err != nil {
		t.Fatal("read missing-date fixture:", err)
	}
	if _, _, err := snapshots.DecodeCustomer(missingDateBytes, "DEMO", anchor.Add(time.Second)); err != nil {
		t.Fatalf("DecodeCustomer(unverified missing due date) error = %v", err)
	}

	mappingPath := filepath.Join(bundleRoot, filepath.FromSlash(manifest.MappingSetupSQL))
	mappingSQL, err := os.ReadFile(mappingPath)
	if err != nil {
		t.Fatal("read generated mapping SQL:", err)
	}
	if !strings.Contains(string(mappingSQL), "current_database() <> '"+manifest.DatabaseName+"'") {
		t.Fatalf("mapping SQL does not guard captured database %q", manifest.DatabaseName)
	}

	malicious := manifest
	malicious.FacilityFile = "../outside.json"
	writeTestManifestPair(t, bundleRoot, malicious)
	if _, _, err := loadGeneratedManifest(filepath.Join(bundleRoot, "manifest.json")); err == nil {
		t.Fatal("loadGeneratedManifest() accepted an artifact traversal")
	}

	malicious.FacilityFile = filepath.ToSlash(filepath.Join("runs", manifest.RunID, "linked.json"))
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal("write symlink target:", err)
	}
	if err := os.Symlink(outside, filepath.Join(bundleRoot, filepath.FromSlash(malicious.FacilityFile))); err != nil {
		t.Fatal("create bundle symlink:", err)
	}
	writeTestManifestPair(t, bundleRoot, malicious)
	if _, _, err := loadGeneratedManifest(filepath.Join(bundleRoot, "manifest.json")); err == nil {
		t.Fatal("loadGeneratedManifest() accepted a symlinked artifact")
	}

	config, err := policy.LoadConfig()
	if err != nil {
		t.Fatal("LoadConfig() error:", err)
	}
	store := &contractStore{}
	fixedNow := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	evaluator := &evaluation.Service{Mode: "DEMO", FacilityID: "facility_underwriting_demo",
		Policy: config, Store: store, Now: func() time.Time { return fixedNow }}
	handler := apihttp.NewServer(apihttp.ServerConfig{Mode: "DEMO", ServiceSecret: []byte(secret),
		PolicyReady: true, ShortfallClientConfigured: true, Store: store,
		Evaluation: evaluator, Now: func() time.Time { return fixedNow }}).Routes()
	global := serveContractList(t, handler, secret, fixedNow, "/api/underwriting/evaluations")
	if global.Code != http.StatusOK || global.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET global list = %d cache %q, want 200 no-store", global.Code, global.Header().Get("Cache-Control"))
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(global.Body.Bytes(), &page); err != nil || page.NextCursor == "" {
		t.Fatalf("GET global list next cursor = %q err %v", page.NextCursor, err)
	}
	continued := serveContractList(t, handler, secret, fixedNow,
		"/api/underwriting/evaluations?cursor="+url.QueryEscape(page.NextCursor))
	if continued.Code != http.StatusOK || store.lastCustomerID != "" || store.lastMode != "DEMO" || store.lastCursor == nil {
		t.Fatalf("GET unfiltered cursor = %d mode %q customer %q cursor %v",
			continued.Code, store.lastMode, store.lastCustomerID, store.lastCursor)
	}
	listCount := store.listCalls
	changedFilter := serveContractList(t, handler, secret, fixedNow,
		"/api/underwriting/evaluations?customer_id=acct_bloom001&cursor="+url.QueryEscape(page.NextCursor))
	if changedFilter.Code != http.StatusBadRequest || store.listCalls != listCount {
		t.Fatalf("GET cursor with changed filter = %d and calls %d, want 400 and %d",
			changedFilter.Code, store.listCalls, listCount)
	}
	filtered := serveContractList(t, handler, secret, fixedNow,
		"/api/underwriting/evaluations?customer_id=acct_bloom001")
	if filtered.Code != http.StatusOK || store.lastCustomerID != "acct_bloom001" {
		t.Fatalf("GET filtered list = %d customer %q", filtered.Code, store.lastCustomerID)
	}
	readyCalls := store.readyCalls
	unauthorized := httptest.NewRequest(http.MethodGet, "/api/underwriting/evaluations", nil)
	unauthorizedResult := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResult, unauthorized)
	if unauthorizedResult.Code != http.StatusUnauthorized || store.readyCalls != readyCalls {
		t.Fatalf("unauthenticated list = %d readiness calls %d, want 401 and %d",
			unauthorizedResult.Code, store.readyCalls, readyCalls)
	}
}

func makeTestBundle(t *testing.T, databaseName string) (string, generatedManifest, time.Time) {
	return makeTestBundleWithProfile(t, databaseName, standardBookProfile)
}

func makeTestBundleWithProfile(t *testing.T, databaseName, bookProfile string) (string, generatedManifest, time.Time) {
	t.Helper()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal("repositoryRoot() error:", err)
	}
	pack, err := loadFixturePack(root)
	if err != nil {
		t.Fatal("loadFixturePack() error:", err)
	}
	config, err := policy.LoadConfig()
	if err != nil {
		t.Fatal("LoadConfig() error:", err)
	}
	runID := "11111111-1111-4111-8111-111111111111"
	anchor := time.Now().UTC().Truncate(time.Second)
	bundleRoot := t.TempDir()
	runDirectory := filepath.Join(bundleRoot, "runs", runID)
	if err := os.MkdirAll(runDirectory, 0o755); err != nil {
		t.Fatal("create fixture bundle:", err)
	}
	manifest, err := materializeRun(".dev/demo", databaseName, runDirectory, runID,
		anchor, config.PolicyID(), config.PolicyHash(), bookProfile, pack.Scenarios)
	if err != nil {
		t.Fatal("materializeRun() error:", err)
	}
	writeTestManifestPair(t, bundleRoot, manifest)
	return bundleRoot, manifest, anchor
}

func writeTestManifestPair(t *testing.T, bundleRoot string, manifest generatedManifest) {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal("marshal test manifest:", err)
	}
	for _, path := range []string{
		filepath.Join(bundleRoot, "manifest.json"),
		filepath.Join(bundleRoot, "runs", manifest.RunID, "manifest.json"),
	} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal("write test manifest:", err)
		}
	}
}

func mutateFixtureJSON(t *testing.T, data []byte, change func(map[string]any)) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal("decode fixture for mutation:", err)
	}
	change(object)
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal("encode mutated fixture:", err)
	}
	return encoded
}

func serveContractList(t *testing.T, handler http.Handler, secret string, now time.Time, path string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := serviceAssertion(secret, now)
	if err != nil {
		t.Fatal("serviceAssertion() error:", err)
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func waitForDemoShortfall(t *testing.T, address string) error {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("listener start timed out")
}

type contractStore struct {
	mu             sync.Mutex
	readyCalls     int
	listCalls      int
	lastMode       string
	lastCustomerID string
	lastCursor     *domain.EvaluationCursor
}

func (s *contractStore) Ready(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readyCalls++
	return nil
}

func (*contractStore) FindEvaluation(context.Context, string, string, string) (postgres.ExistingEvaluation, bool, error) {
	return postgres.ExistingEvaluation{}, false, nil
}

func (*contractStore) CurrentSnapshots(context.Context, string, string, string) (*domain.CustomerSnapshotRecord, *domain.FacilitySnapshotRecord, error) {
	return nil, nil, nil
}

func (*contractStore) CurrentCustomerSnapshot(context.Context, string, string) (*domain.CustomerSnapshotRecord, error) {
	return nil, nil
}

func (*contractStore) SaveEvaluation(context.Context, postgres.EvaluationInsert) (json.RawMessage, bool, error) {
	return nil, false, errors.New("unexpected save")
}

func (*contractStore) GetEvaluation(context.Context, string, string) (json.RawMessage, bool, error) {
	return nil, false, nil
}

func (s *contractStore) ListEvaluations(_ context.Context, mode, customerID string,
	cursor *domain.EvaluationCursor) (domain.EvaluationPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	s.lastMode, s.lastCustomerID, s.lastCursor = mode, customerID, cursor
	page := domain.EvaluationPage{Items: []json.RawMessage{}}
	if cursor == nil {
		page.NextCursor = &domain.EvaluationCursor{Mode: mode, CustomerID: customerID,
			CreatedAt: time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC),
			ID:        "22222222-2222-4222-8222-222222222222"}
	}
	return page, nil
}

var _ evaluation.Store = (*contractStore)(nil)
var _ apihttpReadyStore = (*contractStore)(nil)

type apihttpReadyStore interface {
	Ready(context.Context) error
}
