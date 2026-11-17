package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

const (
	fixtureMaxBytes         = 2_000_000
	expectedFacilityID      = "facility_underwriting_demo"
	defaultDemoDatabaseName = "flowcard_underwriting_demo"
	standardBookProfile     = "standard"
	knownEmptyBookProfile   = "known-empty"
	standardMonthlyInflows  = int64(10_000_000)
	capacityMonthlyInflows  = int64(3_000_000)
)

type fixturePack struct {
	SourceMode string         `json:"source_mode"`
	Scenarios  []scenarioPlan `json:"scenarios"`
}

type scenarioPlan struct {
	ID                       string         `json:"scenario_id"`
	CustomerID               string         `json:"customer_id"`
	AccountID                string         `json:"account_id"`
	CardID                   string         `json:"card_id"`
	ExpectedStatus           string         `json:"expected_status"`
	PrincipalCents           int64          `json:"principal_cents"`
	TermDays                 int            `json:"term_days"`
	EstimatedFeeCents        int64          `json:"estimated_fee_cents"`
	TotalDueCents            int64          `json:"total_due_cents"`
	CustomerLimitCents       int64          `json:"customer_limit_cents"`
	ExistingExposureCents    int64          `json:"existing_exposure_cents"`
	PostFundingExposureCents int64          `json:"post_funding_exposure_cents"`
	FreeReceivablesCents     int64          `json:"free_receivables_cents"`
	ExpectedRules            []expectedRule `json:"expected_rules"`
	DueDateVerified          *bool          `json:"due_date_verified"`
	Provenance               string         `json:"provenance"`
}

type expectedRule struct {
	RuleID string `json:"rule_id"`
	Status string `json:"status"`
}

type generatedManifest struct {
	FixturePackVersion int                 `json:"fixture_pack_version"`
	RunID              string              `json:"run_id"`
	RunAnchor          time.Time           `json:"run_anchor"`
	SourceMode         string              `json:"source_mode"`
	PolicyID           string              `json:"policy_id"`
	PolicyHash         string              `json:"policy_hash"`
	OutputDirectory    string              `json:"output_directory"`
	DatabaseName       string              `json:"database_name"`
	FacilityID         string              `json:"facility_id"`
	FacilityFile       string              `json:"facility_file"`
	ShortfallsFile     string              `json:"shortfalls_file"`
	MappingSetupSQL    string              `json:"mapping_setup_sql"`
	ExpectedTraceFile  string              `json:"expected_trace_file"`
	FacilityBooks      facilityBooks       `json:"facility_books"`
	Scenarios          []generatedScenario `json:"scenarios"`
	Provenance         string              `json:"provenance"`
}

type facilityBooks struct {
	FacilityTotalCents             int64 `json:"facility_total_cents"`
	ReserveFloorBPS                int64 `json:"reserve_floor_bps"`
	FundedPrincipalCents           int64 `json:"funded_principal_cents"`
	CustomerFundedPrincipalCents   int64 `json:"customer_funded_principal_cents"`
	OtherFundedPrincipalCents      int64 `json:"other_funded_principal_cents"`
	ReservedPrincipalCents         int64 `json:"reserved_principal_cents"`
	CustomerReservedPrincipalCents int64 `json:"customer_reserved_principal_cents"`
	OtherReservedPrincipalCents    int64 `json:"other_reserved_principal_cents"`
	FeesCountedInFacilityCash      bool  `json:"fees_counted_in_facility_cash"`
	AvailableCapitalCents          int64 `json:"available_capital_cents"`
}

type generatedScenario struct {
	ID                       string         `json:"scenario_id"`
	CustomerID               string         `json:"customer_id"`
	AccountID                string         `json:"account_id"`
	CardID                   string         `json:"card_id"`
	ShortfallID              string         `json:"shortfall_id"`
	TransactionID            string         `json:"transaction_id"`
	CanonicalEventID         string         `json:"canonical_event_id"`
	LifecycleID              string         `json:"lifecycle_id"`
	IdempotencyKey           string         `json:"idempotency_key"`
	CustomerFile             string         `json:"customer_file"`
	ExpectedStatus           string         `json:"expected_status"`
	PrincipalCents           int64          `json:"principal_cents"`
	TermDays                 int            `json:"term_days"`
	EstimatedFeeCents        int64          `json:"estimated_fee_cents"`
	TotalDueCents            int64          `json:"total_due_cents"`
	CustomerLimitCents       int64          `json:"customer_limit_cents"`
	ExistingExposureCents    int64          `json:"existing_exposure_cents"`
	PostFundingExposureCents int64          `json:"post_funding_exposure_cents"`
	FreeReceivablesCents     int64          `json:"free_receivables_cents"`
	ExpectedRules            []expectedRule `json:"expected_rules"`
	Provenance               string         `json:"provenance"`
}

type shortfallFixturePack struct {
	SourceMode string                   `json:"source_mode"`
	Provenance string                   `json:"provenance"`
	Records    []shortfallFixtureRecord `json:"records"`
}

type shortfallFixtureRecord struct {
	ScenarioID string                 `json:"scenario_id"`
	Response   domain.ShortfallRecord `json:"response"`
}

type prepareSummary struct {
	SourceMode string   `json:"source_mode"`
	RunID      string   `json:"run_id"`
	Manifest   string   `json:"manifest"`
	Scenarios  []string `json:"scenarios"`
}

func runPrepare(args []string) error {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	scenario := flags.String("scenario", "all", "scenario id or all")
	asOf := flags.String("as-of", "now", "now or RFC3339 UTC instant")
	out := flags.String("out", ".dev/demo", "output directory under the repository .dev directory")
	bookProfile := flags.String("book-profile", standardBookProfile, "standard or known-empty opening debt/reservation books")
	databaseName := flags.String("database-name", defaultDemoDatabaseName,
		"task-owned demo database name under the flowcard_underwriting_demo namespace")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid prepare arguments")
	}
	if !validBookProfile(*bookProfile) {
		return errors.New("invalid demo book profile")
	}
	if !validDemoDatabaseName(*databaseName) {
		return errors.New("invalid demo database name")
	}
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	outputDirectory, outputRelative, err := demoOutputDirectory(root, *out)
	if err != nil {
		return err
	}
	systemNow := time.Now().UTC()
	anchor, err := parseAnchor(*asOf, systemNow)
	if err != nil {
		return errors.New("invalid demo anchor")
	}
	if anchor.After(systemNow.Add(5 * time.Second)) {
		return errors.New("demo anchor exceeds the allowed future tolerance")
	}
	plan, err := loadFixturePack(root)
	if err != nil {
		return err
	}
	plans, err := selectScenarios(plan.Scenarios, *scenario)
	if err != nil {
		return err
	}
	policyConfig, err := policy.LoadConfig()
	if err != nil {
		return errors.New("underwriting policy configuration invalid")
	}
	runID, err := randomUUID()
	if err != nil {
		return errors.New("demo run id unavailable")
	}
	if err := checkReplaceableManifest(outputDirectory, outputRelative); err != nil {
		return err
	}
	runsDirectory := filepath.Join(outputDirectory, "runs")
	if ensureNoSymlinkPath(outputDirectory, runsDirectory) != nil {
		return errors.New("demo output path contains a symlink")
	}
	if err := os.MkdirAll(runsDirectory, 0o755); err != nil {
		return errors.New("demo output unavailable")
	}
	if ensureNoSymlinkPath(outputDirectory, runsDirectory) != nil {
		return errors.New("demo output path contains a symlink")
	}
	stagingDirectory := filepath.Join(outputDirectory, "runs", ".prepare-"+runID)
	if err := os.Mkdir(stagingDirectory, 0o755); err != nil {
		return errors.New("demo output unavailable")
	}
	defer os.RemoveAll(stagingDirectory)

	manifest, err := materializeRun(outputRelative, *databaseName, stagingDirectory, runID, anchor,
		policyConfig.PolicyID(), policyConfig.PolicyHash(), *bookProfile, plans)
	if err != nil {
		return err
	}
	runDirectory := filepath.Join(outputDirectory, "runs", runID)
	if err := os.Rename(stagingDirectory, runDirectory); err != nil {
		return errors.New("demo output unavailable")
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New("demo manifest encoding failed")
	}
	if err := writeAtomicManifest(outputDirectory, runID, manifestBytes); err != nil {
		return err
	}
	ids := make([]string, len(manifest.Scenarios))
	for i, item := range manifest.Scenarios {
		ids[i] = item.ID
	}
	return json.NewEncoder(os.Stdout).Encode(prepareSummary{
		SourceMode: "DEMO", RunID: runID,
		Manifest: filepath.ToSlash(filepath.Join(outputRelative, "manifest.json")), Scenarios: ids,
	})
}

func materializeRun(outputRelative, databaseName, stagingDirectory, runID string, anchor time.Time,
	policyID, policyHash, bookProfile string, plans []scenarioPlan) (generatedManifest, error) {
	if !validBookProfile(bookProfile) {
		return generatedManifest{}, errors.New("invalid demo book profile")
	}
	if len(plans) == 0 {
		return generatedManifest{}, errors.New("no demo scenarios selected")
	}
	today := dateOf(anchor)
	windowEnd := today.AddDate(0, 0, -1)
	windowStart := today.AddDate(0, 0, -90)
	manifest := generatedManifest{
		FixturePackVersion: 1, RunID: runID, RunAnchor: anchor.UTC(), SourceMode: "DEMO",
		PolicyID: policyID, PolicyHash: policyHash, OutputDirectory: outputRelative,
		DatabaseName:      databaseName,
		FacilityID:        expectedFacilityID,
		FacilityFile:      filepath.ToSlash(filepath.Join("runs", runID, "facility.json")),
		ShortfallsFile:    filepath.ToSlash(filepath.Join("runs", runID, "shortfalls.json")),
		MappingSetupSQL:   filepath.ToSlash(filepath.Join("runs", runID, "mapping-setup.sql")),
		ExpectedTraceFile: filepath.ToSlash(filepath.Join("runs", runID, "expected-traces.json")),
		Provenance:        "Synthetic DEMO fixtures; no live Airwallex, Salesforce, funding, or repayment records.",
	}
	if bookProfile == knownEmptyBookProfile {
		manifest.Provenance += " Opening bridge and reservation books are explicitly known-empty synthetic facts."
	}
	shortfalls := shortfallFixturePack{
		SourceMode: "DEMO",
		Provenance: "Synthetic final-observation mock responses rendered from one fresh UTC run anchor.",
		Records:    make([]shortfallFixtureRecord, 0, len(plans)),
	}
	var customerFunded, customerReserved int64
	for _, template := range plans {
		plan, monthlyInflowsCents, knownEmptyBooks, err := scenarioForBookProfile(template, bookProfile)
		if err != nil {
			return generatedManifest{}, err
		}
		if plan.ID == "" || plan.CustomerID == "" || plan.AccountID == "" || plan.CardID == "" ||
			plan.ExpectedStatus == "" || plan.PrincipalCents <= 0 || plan.TermDays < 7 || plan.TermDays > 90 ||
			plan.EstimatedFeeCents < 0 || plan.TotalDueCents != plan.PrincipalCents+plan.EstimatedFeeCents ||
			len(plan.ExpectedRules) != 10 {
			return generatedManifest{}, errors.New("invalid checked-in scenario template")
		}
		existingReserve := int64(0)
		if !knownEmptyBooks {
			existingReserve = plan.ExistingExposureCents - 1_100_000
			if existingReserve <= 0 || existingReserve > 3_000_000 {
				return generatedManifest{}, errors.New("invalid checked-in exposure setup")
			}
		}
		shortfallID, err := randomUUID()
		if err != nil {
			return generatedManifest{}, errors.New("demo id generation failed")
		}
		transactionID, err := randomUUID()
		if err != nil {
			return generatedManifest{}, errors.New("demo id generation failed")
		}
		canonicalEventID, err := randomUUID()
		if err != nil {
			return generatedManifest{}, errors.New("demo id generation failed")
		}
		lifecycleID, err := randomUUID()
		if err != nil {
			return generatedManifest{}, errors.New("demo id generation failed")
		}
		bridgeTransactionID, reservationTransactionID := "", ""
		if !knownEmptyBooks {
			bridgeTransactionID, err = randomUUID()
			if err != nil {
				return generatedManifest{}, errors.New("demo id generation failed")
			}
			reservationTransactionID, err = randomUUID()
			if err != nil {
				return generatedManifest{}, errors.New("demo id generation failed")
			}
		}
		customer, err := customerFixture(plan, runID, anchor, today, windowStart, windowEnd,
			transactionID, bridgeTransactionID, reservationTransactionID, existingReserve,
			monthlyInflowsCents, knownEmptyBooks)
		if err != nil {
			return generatedManifest{}, err
		}
		customerPath := filepath.Join(stagingDirectory, "customer-"+plan.ID+".json")
		if err := writeJSONFile(customerPath, customer); err != nil {
			return generatedManifest{}, err
		}
		shortfall := shortfallFixture(plan, shortfallID, transactionID, canonicalEventID,
			lifecycleID, anchor)
		shortfalls.Records = append(shortfalls.Records, shortfallFixtureRecord{ScenarioID: plan.ID, Response: shortfall})
		manifest.Scenarios = append(manifest.Scenarios, generatedScenario{
			ID: plan.ID, CustomerID: plan.CustomerID, AccountID: plan.AccountID, CardID: plan.CardID,
			ShortfallID: shortfallID, TransactionID: transactionID,
			CanonicalEventID: canonicalEventID, LifecycleID: lifecycleID,
			IdempotencyKey: "underwriting-demo:" + runID + ":" + plan.ID,
			CustomerFile:   filepath.ToSlash(filepath.Join("runs", runID, "customer-"+plan.ID+".json")),
			ExpectedStatus: plan.ExpectedStatus, PrincipalCents: plan.PrincipalCents,
			TermDays: plan.TermDays, EstimatedFeeCents: plan.EstimatedFeeCents,
			TotalDueCents: plan.TotalDueCents, CustomerLimitCents: plan.CustomerLimitCents,
			ExistingExposureCents:    plan.ExistingExposureCents,
			PostFundingExposureCents: plan.PostFundingExposureCents,
			FreeReceivablesCents:     plan.FreeReceivablesCents,
			ExpectedRules:            plan.ExpectedRules, Provenance: plan.Provenance,
		})
		if !knownEmptyBooks {
			customerFunded += 1_000_000
			customerReserved += existingReserve
		}
	}
	fundedPrincipalCents, reservedPrincipalCents, availableCapitalCents := int64(8_000_000), int64(2_000_000), int64(70_000_000)
	if bookProfile == knownEmptyBookProfile {
		fundedPrincipalCents, reservedPrincipalCents, availableCapitalCents = 0, 0, 80_000_000
	}
	if customerFunded > fundedPrincipalCents || customerReserved > reservedPrincipalCents {
		return generatedManifest{}, errors.New("facility commitments exceed synthetic books")
	}
	manifest.FacilityBooks = facilityBooks{
		FacilityTotalCents: 100_000_000, ReserveFloorBPS: 2000,
		FundedPrincipalCents: fundedPrincipalCents, CustomerFundedPrincipalCents: customerFunded,
		OtherFundedPrincipalCents: fundedPrincipalCents - customerFunded,
		ReservedPrincipalCents:    reservedPrincipalCents, CustomerReservedPrincipalCents: customerReserved,
		OtherReservedPrincipalCents: reservedPrincipalCents - customerReserved,
		FeesCountedInFacilityCash:   false, AvailableCapitalCents: availableCapitalCents,
	}
	facility, err := facilityFixture(runID, anchor, fundedPrincipalCents, reservedPrincipalCents,
		bookProfile == knownEmptyBookProfile)
	if err != nil {
		return generatedManifest{}, err
	}
	if err := writeJSONFile(filepath.Join(stagingDirectory, "facility.json"), facility); err != nil {
		return generatedManifest{}, err
	}
	if err := writeJSONFile(filepath.Join(stagingDirectory, "shortfalls.json"), shortfalls); err != nil {
		return generatedManifest{}, err
	}
	if err := writeMappingSQL(filepath.Join(stagingDirectory, "mapping-setup.sql"),
		manifest.DatabaseName, manifest.Scenarios); err != nil {
		return generatedManifest{}, err
	}
	if err := writeJSONFile(filepath.Join(stagingDirectory, "expected-traces.json"), manifest.Scenarios); err != nil {
		return generatedManifest{}, err
	}
	if err := writeJSONFile(filepath.Join(stagingDirectory, "manifest.json"), manifest); err != nil {
		return generatedManifest{}, err
	}
	return manifest, nil
}

func validBookProfile(profile string) bool {
	return profile == standardBookProfile || profile == knownEmptyBookProfile
}

func scenarioForBookProfile(plan scenarioPlan, profile string) (scenarioPlan, int64, bool, error) {
	switch profile {
	case standardBookProfile:
		return plan, standardMonthlyInflows, false, nil
	case knownEmptyBookProfile:
		plan.ExistingExposureCents = 0
		plan.PostFundingExposureCents = plan.PrincipalCents + plan.EstimatedFeeCents
		monthlyInflowsCents := standardMonthlyInflows
		if plan.ID == "capacity_exceeded" {
			plan.CustomerLimitCents = 600_000
			monthlyInflowsCents = capacityMonthlyInflows
		}
		plan.Provenance += " Opening bridge and reservation books are explicitly known-empty synthetic facts."
		return plan, monthlyInflowsCents, true, nil
	default:
		return scenarioPlan{}, 0, false, errors.New("invalid demo book profile")
	}
}

func customerFixture(plan scenarioPlan, runID string, anchor time.Time, today, windowStart, windowEnd time.Time,
	shortfallTransactionID, bridgeTransactionID, reservationTransactionID string, reservationPrincipal,
	monthlyInflowsCents int64, knownEmptyBooks bool) (domain.CustomerSnapshot, error) {
	started := addMonthsClamped(today, -9)
	bridgeOpened := today.AddDate(0, 0, -60)
	bridgeDue := today.AddDate(0, 0, 15)
	invoiceDue := today.AddDate(0, 0, 20)
	bridgeBookRecordID := "bridge-" + plan.ID
	reservationBookRecordID := "reservation-" + plan.ID
	sourceID := "underwriting-demo-fixtures"
	if knownEmptyBooks {
		bridgeBookRecordID = "known-empty-bridge-book-" + plan.ID
		reservationBookRecordID = "known-empty-reservation-book-" + plan.ID
		sourceID += "-known-empty-books"
	}
	sourceRefs := []domain.SourceReference{
		sourceRef("synthetic-fixture", "customer-"+plan.ID, runID, anchor),
		sourceRef("synthetic-ledger", bridgeBookRecordID, runID, anchor),
		sourceRef("synthetic-ledger", reservationBookRecordID, runID, anchor),
		sourceRef("synthetic-accounting", "invoice-"+plan.ID, runID, anchor),
		sourceRef("synthetic-fixture", "classification-"+plan.ID, runID, anchor),
	}
	dueDateVerified := true
	if plan.DueDateVerified != nil {
		dueDateVerified = *plan.DueDateVerified
	}
	var dueOn *domain.Date
	if dueDateVerified {
		due := domain.Date(invoiceDue.Format("2006-01-02"))
		dueOn = &due
	}
	if plan.DueDateVerified != nil && *plan.DueDateVerified != dueDateVerified {
		return domain.CustomerSnapshot{}, errors.New("fixture due-date contract mismatch")
	}
	coverage := domain.Coverage{
		Standing: true, BalanceHistory: true, InflowHistory: true,
		BridgeHistory: true, LifetimeDefaults: true, Reservations: true,
		Receivables: true, Classification: true,
	}
	onboarding, active, suspended := true, true, false
	accountStatus := "ACTIVE"
	bridges := []domain.Bridge{{
		ID: runID + "-bridge-" + plan.ID, TransactionID: bridgeTransactionID,
		OriginalPrincipalCents: 1_000_000, PrincipalOwedCents: 1_000_000,
		FeesOwedCents: 100_000, Status: "OPEN",
		OpenedOn: domain.Date(bridgeOpened.Format("2006-01-02")), DueOn: domain.Date(bridgeDue.Format("2006-01-02")),
	}}
	reservations := []domain.Reservation{{
		ID: runID + "-reservation-" + plan.ID, TransactionID: reservationTransactionID,
		PrincipalCents: reservationPrincipal, FeeCents: 0, Status: "ACTIVE",
	}}
	if knownEmptyBooks {
		bridges = make([]domain.Bridge, 0)
		reservations = make([]domain.Reservation, 0)
	}
	snapshot := domain.CustomerSnapshot{
		SnapshotMetadata: domain.SnapshotMetadata{
			SchemaVersion: domain.SchemaVersion, SourceMode: "DEMO",
			SourceID: sourceID, SourceVersion: runID + "-" + plan.ID,
			CapturedAt: anchor.UTC(), SourceRefs: sourceRefs,
		},
		CustomerID: plan.CustomerID, AccountID: plan.AccountID, CardID: plan.CardID,
		Coverage: coverage,
		Standing: &domain.Standing{
			RelationshipStartedOn: domainDatePointer(domain.Date(started.Format("2006-01-02"))),
			OnboardingComplete:    &onboarding, Active: &active, Suspended: &suspended,
			ProviderAccountStatus: &accountStatus,
		},
		HistoryWindow: domain.HistoryWindow{
			WindowStart: domain.Date(windowStart.Format("2006-01-02")),
			WindowEnd:   domain.Date(windowEnd.Format("2006-01-02")),
		},
		BalanceHistory: make([]domain.Balance, 0, 90),
		InflowHistory: []domain.Inflow{
			{ID: runID + "-" + plan.ID + "-inflow-1", Date: domain.Date(today.AddDate(0, 0, -20).Format("2006-01-02")), AmountCents: monthlyInflowsCents, Kind: "EXTERNAL_SETTLED", PayerID: "synthetic-payer-1"},
			{ID: runID + "-" + plan.ID + "-inflow-2", Date: domain.Date(today.AddDate(0, 0, -50).Format("2006-01-02")), AmountCents: monthlyInflowsCents, Kind: "EXTERNAL_SETTLED", PayerID: "synthetic-payer-1"},
			{ID: runID + "-" + plan.ID + "-inflow-3", Date: domain.Date(today.AddDate(0, 0, -80).Format("2006-01-02")), AmountCents: monthlyInflowsCents, Kind: "EXTERNAL_SETTLED", PayerID: "synthetic-payer-1"},
		},
		Bridges:      bridges,
		Reservations: reservations,
		Receivables: []domain.Invoice{{
			InvoiceID: runID + "-invoice-" + plan.ID, PayerID: "synthetic-payer-1", Currency: "USD",
			EligibleUnpaidCents: 800_000, AllocatedCents: 0, DueOn: dueOn,
			DueDateVerified: dueDateVerified, AllocationComplete: true,
			SourceRef: sourceRefs[3],
		}},
		Classifications: []domain.Classification{{
			TransactionID: shortfallTransactionID, MerchantCategory: "suppliers", SourceRef: sourceRefs[4],
		}},
	}
	for date := windowStart; !date.After(windowEnd); date = date.AddDate(0, 0, 1) {
		snapshot.BalanceHistory = append(snapshot.BalanceHistory, domain.Balance{
			Date: domain.Date(date.Format("2006-01-02")), BalanceCents: 5_000_000,
		})
	}
	if len(snapshot.BalanceHistory) != 90 {
		return domain.CustomerSnapshot{}, errors.New("demo balance window is not 90 days")
	}
	return snapshot, nil
}

func facilityFixture(runID string, anchor time.Time, fundedPrincipalCents, reservedPrincipalCents int64,
	knownEmptyBooks bool) (domain.FacilitySnapshot, error) {
	sourceID := "underwriting-demo-fixtures"
	recordID := "facility-underwriting-demo"
	if knownEmptyBooks {
		sourceID += "-known-empty-books"
		recordID += "-known-empty-books"
	}
	return domain.FacilitySnapshot{
		SnapshotMetadata: domain.SnapshotMetadata{
			SchemaVersion: domain.SchemaVersion, SourceMode: "DEMO",
			SourceID: sourceID, SourceVersion: runID,
			CapturedAt: anchor.UTC(), SourceRefs: []domain.SourceReference{
				sourceRef("synthetic-fixture", recordID, runID, anchor),
			},
		},
		FacilityID: expectedFacilityID, Currency: "USD", FacilityTotalCents: 100_000_000,
		ReserveFloorBPS: 2000, FundedPrincipalCents: fundedPrincipalCents,
		ReservedPrincipalCents: reservedPrincipalCents, LedgerVersion: runID, LedgerComplete: true,
	}, nil
}

func shortfallFixture(plan scenarioPlan, id, transactionID, eventID, lifecycleID string,
	anchor time.Time) domain.ShortfallRecord {
	createdAt := anchor.Add(-30 * time.Second)
	providerAt := anchor.Add(-60 * time.Second)
	balanceRequested := anchor.Add(-10 * time.Second)
	balanceObserved := anchor.Add(-5 * time.Second)
	customerID, cardID := plan.CustomerID, plan.CardID
	reason, currency := (*string)(nil), "USD"
	attempted, available, shortfallAmount := int64(900_000), int64(200_000), int64(700_000)
	transaction, lifecycle := transactionID, lifecycleID
	return domain.ShortfallRecord{
		ID: id, CustomerID: &customerID, AccountID: plan.AccountID, CardID: &cardID,
		Outcome: "SHORTFALL_DETECTED", Reason: reason, Currency: &currency,
		AttemptedAmountCents: &attempted, AvailableBalanceCents: &available,
		ShortfallCents: &shortfallAmount, CanonicalEventID: eventID,
		TransactionID: &transaction, LifecycleID: &lifecycle, CreatedAt: createdAt.UTC(),
		ProviderCreatedAt: timePointer(providerAt), ProviderTransactedAt: timePointer(providerAt),
		ProviderUpdatedAt: timePointer(providerAt), BalanceRequestedAt: timePointer(balanceRequested),
		BalanceObservedAt: timePointer(balanceObserved), RequiresRecheck: true,
		AmountSemantics: "current_need_at_balance_observation",
	}
}

func writeMappingSQL(path, databaseName string, scenarios []generatedScenario) error {
	if !validDemoDatabaseName(databaseName) {
		return errors.New("invalid demo database name")
	}
	var body strings.Builder
	body.WriteString("-- Synthetic roster/card rows for the captured demo database only.\n")
	body.WriteString("BEGIN;\nDO $$ BEGIN IF current_database() <> ")
	body.WriteString(sqlLiteral(databaseName))
	body.WriteString(" THEN RAISE EXCEPTION 'wrong demo database'; END IF; END $$;\n")
	for _, scenario := range scenarios {
		salesforceID := "demo-sf-" + strings.TrimPrefix(scenario.CustomerID, "acct_")
		body.WriteString("INSERT INTO public.customer_roster (customer_id, salesforce_account_id, airwallex_connected_account_id, enrolled_by, enrollment_reason) VALUES (")
		body.WriteString(sqlLiteral(scenario.CustomerID) + "," + sqlLiteral(salesforceID) + "," +
			sqlLiteral(scenario.AccountID) + ",'underwriting-demo','Synthetic underwriting fixture') ON CONFLICT (customer_id) DO NOTHING;\n")
		body.WriteString("INSERT INTO public.customer_roster_audit (customer_id, action, actor, reason) SELECT ")
		body.WriteString(sqlLiteral(scenario.CustomerID) + ",'enrolled','underwriting-demo','Synthetic underwriting fixture' WHERE NOT EXISTS (SELECT 1 FROM public.customer_roster_audit WHERE customer_id = ")
		body.WriteString(sqlLiteral(scenario.CustomerID) + " AND action = 'enrolled');\n")
		body.WriteString("INSERT INTO public.issuing_card_bindings (card_id, customer_id, airwallex_account_id, bound_by, binding_reason, is_active) VALUES (")
		body.WriteString(sqlLiteral(scenario.CardID) + "," + sqlLiteral(scenario.CustomerID) + "," +
			sqlLiteral(scenario.AccountID) + ",'underwriting-demo','Synthetic underwriting fixture',true) ON CONFLICT (card_id) DO NOTHING;\n")
		body.WriteString("INSERT INTO public.issuing_card_binding_audit (card_id, customer_id, airwallex_account_id, action, actor, reason) SELECT ")
		body.WriteString(sqlLiteral(scenario.CardID) + "," + sqlLiteral(scenario.CustomerID) + "," +
			sqlLiteral(scenario.AccountID) + ",'bound','underwriting-demo','Synthetic underwriting fixture' WHERE NOT EXISTS (SELECT 1 FROM public.issuing_card_binding_audit WHERE card_id = ")
		body.WriteString(sqlLiteral(scenario.CardID) + " AND action = 'bound');\n")
	}
	body.WriteString("DO $$ BEGIN IF EXISTS (SELECT 1 FROM (VALUES\n")
	for i, scenario := range scenarios {
		if i > 0 {
			body.WriteString(",\n")
		}
		salesforceID := "demo-sf-" + strings.TrimPrefix(scenario.CustomerID, "acct_")
		body.WriteString("(")
		body.WriteString(sqlLiteral(scenario.CustomerID) + "," + sqlLiteral(salesforceID) + "," +
			sqlLiteral(scenario.AccountID) + "," + sqlLiteral(scenario.CardID) + ")")
	}
	body.WriteString("\n) AS expected(customer_id,salesforce_account_id,account_id,card_id)\n")
	body.WriteString("LEFT JOIN public.customer_roster r USING (customer_id)\n")
	body.WriteString("LEFT JOIN public.issuing_card_bindings b USING (card_id)\n")
	body.WriteString("WHERE r.customer_id IS NULL OR r.salesforce_account_id <> expected.salesforce_account_id\n")
	body.WriteString("OR r.airwallex_connected_account_id <> expected.account_id OR NOT r.is_active\n")
	body.WriteString("OR b.card_id IS NULL OR b.customer_id <> expected.customer_id\n")
	body.WriteString("OR b.airwallex_account_id <> expected.account_id OR NOT b.is_active)\n")
	body.WriteString("THEN RAISE EXCEPTION 'synthetic mapping verification failed'; END IF; END $$;\nCOMMIT;\n")
	return writeArtifactFile(path, []byte(body.String()))
}

func sourceRef(system, recordID, version string, effectiveAt time.Time) domain.SourceReference {
	return domain.SourceReference{System: system, RecordID: recordID, Version: version, EffectiveAt: effectiveAt.UTC()}
}

func domainDatePointer(value domain.Date) *domain.Date { return &value }

func timePointer(value time.Time) *time.Time { return &value }

func addMonthsClamped(date time.Time, months int) time.Time {
	first := time.Date(date.Year(), date.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	lastDay := first.AddDate(0, 1, -1).Day()
	day := date.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

func dateOf(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func validDemoDatabaseName(value string) bool {
	if value == defaultDemoDatabaseName {
		return true
	}
	prefix := defaultDemoDatabaseName + "_"
	if len(value) > 63 || !strings.HasPrefix(value, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	if suffix == "" || suffix[len(suffix)-1] == '_' {
		return false
	}
	for _, char := range suffix {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("demo artifact encoding failed")
	}
	return writeArtifactFile(path, data)
}

func writeArtifactFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return errors.New("demo artifact write failed")
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return errors.New("demo artifact write failed")
	}
	if err := file.Close(); err != nil {
		return errors.New("demo artifact write failed")
	}
	return nil
}

func writeAtomicManifest(outputDirectory, runID string, data []byte) error {
	temporary := filepath.Join(outputDirectory, ".manifest-"+runID+".tmp")
	if err := writeArtifactFile(temporary, data); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(outputDirectory, "manifest.json")); err != nil {
		return errors.New("demo manifest publish failed")
	}
	return nil
}

func checkReplaceableManifest(outputDirectory, outputRelative string) error {
	path := filepath.Join(outputDirectory, "manifest.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("demo manifest path is not a regular task-owned file")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > fixtureMaxBytes {
		return errors.New("existing demo manifest is not recognized")
	}
	var previous generatedManifest
	if json.Unmarshal(data, &previous) != nil || previous.FixturePackVersion != 1 ||
		previous.RunID == "" || previous.OutputDirectory != outputRelative {
		return errors.New("existing demo manifest is not recognized")
	}
	previousManifest := filepath.Join(outputDirectory, "runs", previous.RunID, "manifest.json")
	if _, err := os.Stat(previousManifest); err != nil {
		return errors.New("prior demo run is missing its retained manifest")
	}
	return nil
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", errors.New("repository root unavailable")
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "docs", "api", "underwriting-contract.md")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("repository root unavailable")
		}
		current = parent
	}
}

func demoOutputDirectory(root, output string) (string, string, error) {
	if strings.TrimSpace(output) == "" {
		return "", "", errors.New("demo output must be under .dev")
	}
	path := output
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", "", errors.New("demo output path invalid")
	}
	devRoot := filepath.Join(root, ".dev")
	relative, err := filepath.Rel(devRoot, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", errors.New("demo output must be under .dev")
	}
	if ensureNoSymlinkPath(root, path) != nil {
		return "", "", errors.New("demo output path contains a symlink")
	}
	return path, filepath.ToSlash(filepath.Join(".dev", relative)), nil
}

func parseAnchor(value string, now time.Time) (time.Time, error) {
	if value == "now" {
		return now.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() == nil {
		return time.Time{}, errors.New("invalid demo anchor")
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, errors.New("demo anchor must be UTC")
	}
	return parsed.UTC(), nil
}

func loadFixturePack(root string) (fixturePack, error) {
	path := filepath.Join(root, "services", "underwriting-engine", "demo", "fixtures", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil || len(data) > fixtureMaxBytes {
		return fixturePack{}, errors.New("checked-in fixture manifest unavailable")
	}
	var pack fixturePack
	if json.Unmarshal(data, &pack) != nil || pack.SourceMode != "DEMO" || len(pack.Scenarios) != 3 {
		return fixturePack{}, errors.New("checked-in fixture manifest invalid")
	}
	return pack, nil
}

func loadGeneratedManifest(path string) (string, generatedManifest, error) {
	if strings.TrimSpace(path) == "" {
		return "", generatedManifest{}, errors.New("demo manifest path invalid")
	}
	manifestPath, err := filepath.Abs(path)
	if err != nil {
		return "", generatedManifest{}, errors.New("demo manifest path invalid")
	}
	bundleRoot := filepath.Dir(manifestPath)
	if filepath.Base(filepath.Dir(bundleRoot)) == "runs" {
		bundleRoot = filepath.Dir(filepath.Dir(bundleRoot))
	}
	if ensureNoSymlinkPath(bundleRoot, manifestPath) != nil {
		return "", generatedManifest{}, errors.New("demo manifest path invalid")
	}
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() {
		return "", generatedManifest{}, errors.New("demo manifest path invalid")
	}
	data, err := readLimitedFile(manifestPath, fixtureMaxBytes)
	if err != nil {
		return "", generatedManifest{}, err
	}
	var manifest generatedManifest
	if decodeStrictJSON(data, &manifest) != nil || manifest.FixturePackVersion != 1 ||
		!domain.ValidID(manifest.RunID) || manifest.SourceMode != "DEMO" ||
		manifest.PolicyID == "" || manifest.PolicyHash == "" ||
		!validDemoDatabaseName(manifest.DatabaseName) || manifest.FacilityID != expectedFacilityID ||
		len(manifest.Scenarios) == 0 {
		return "", generatedManifest{}, errors.New("demo manifest invalid")
	}
	manifestRelative, err := filepath.Rel(bundleRoot, manifestPath)
	if err != nil || (manifestRelative != "manifest.json" &&
		manifestRelative != filepath.Join("runs", manifest.RunID, "manifest.json")) {
		return "", generatedManifest{}, errors.New("demo manifest path invalid")
	}
	retainedManifest, err := artifactPath(bundleRoot,
		filepath.Join("runs", manifest.RunID, "manifest.json"))
	if err != nil {
		return "", generatedManifest{}, err
	}
	retainedInfo, err := os.Lstat(retainedManifest)
	if err != nil || !retainedInfo.Mode().IsRegular() {
		return "", generatedManifest{}, errors.New("retained demo manifest unavailable")
	}
	retainedData, err := readLimitedFile(retainedManifest, fixtureMaxBytes)
	if err != nil || !bytes.Equal(retainedData, data) {
		return "", generatedManifest{}, errors.New("retained demo manifest mismatch")
	}
	for _, artifact := range []string{manifest.FacilityFile, manifest.ShortfallsFile,
		manifest.MappingSetupSQL, manifest.ExpectedTraceFile} {
		if _, err := artifactPath(bundleRoot, artifact); err != nil {
			return "", generatedManifest{}, err
		}
	}
	seen := make(map[string]struct{}, len(manifest.Scenarios))
	for _, scenario := range manifest.Scenarios {
		if scenario.ID == "" || !domain.ValidID(scenario.ShortfallID) ||
			!domain.ValidID(scenario.TransactionID) || !domain.ValidID(scenario.CanonicalEventID) ||
			!domain.ValidID(scenario.LifecycleID) || scenario.IdempotencyKey == "" {
			return "", generatedManifest{}, errors.New("demo manifest scenario invalid")
		}
		if _, exists := seen[scenario.ID]; exists {
			return "", generatedManifest{}, errors.New("demo manifest scenario invalid")
		}
		seen[scenario.ID] = struct{}{}
		if _, err := artifactPath(bundleRoot, scenario.CustomerFile); err != nil {
			return "", generatedManifest{}, err
		}
	}
	return bundleRoot, manifest, nil
}

func artifactPath(outputDirectory, relative string) (string, error) {
	localPath := filepath.FromSlash(relative)
	if relative == "" || filepath.IsAbs(localPath) || !filepath.IsLocal(localPath) {
		return "", errors.New("demo artifact path invalid")
	}
	path := filepath.Join(outputDirectory, localPath)
	if ensureNoSymlinkPath(outputDirectory, path) != nil {
		return "", errors.New("demo artifact path invalid")
	}
	return path, nil
}

func ensureNoSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("path escapes root")
	}
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("path root is a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("path contains symlink")
		}
	}
	return nil
}

func selectScenarios(plans []scenarioPlan, selection string) ([]scenarioPlan, error) {
	if selection == "all" {
		return append([]scenarioPlan(nil), plans...), nil
	}
	for _, plan := range plans {
		if plan.ID == selection {
			return []scenarioPlan{plan}, nil
		}
	}
	return nil, errors.New("unknown demo scenario")
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
