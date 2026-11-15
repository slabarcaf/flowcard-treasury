package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

const maxDemoArtifactBytes = 4_000_000

type prepareSummary struct {
	SourceMode string   `json:"source_mode"`
	RunID      string   `json:"run_id"`
	Manifest   string   `json:"manifest"`
	Profiles   []string `json:"profiles"`
}

type profileBuild struct {
	profile         upstreamProfileRef
	fundingCustomer domain.FacilityCustomerImport
	receivables     []domain.ReceivableImport
	authority       domain.AuthorityImport
	actor           domain.ActorImport
	operator        domain.ActorImport
}

func runPrepare(args []string) error {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	asOf := flags.String("as-of", "now", "now or a fresh RFC3339 UTC time")
	out := flags.String("out", ".dev/funding-collection", "output directory under repository .dev")
	databaseName := flags.String("database-name", fundingDatabaseName, "owned funding demo database name")
	upstreamDatabase := flags.String("underwriting-database-name", underwritingDatabaseName, "owned underwriting demo database name")
	underwritingManifest := flags.String("underwriting-manifest", "", "optional fresh actual Underwriting DEMO input manifest")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: demo prepare --as-of now --out PATH [--database-name NAME --underwriting-database-name NAME --underwriting-manifest PATH]")
	}
	if !config.ValidOwnedDatabaseName(*databaseName, "flowcard_funding_demo") ||
		!config.ValidOwnedDatabaseName(*upstreamDatabase, "flowcard_underwriting_demo") {
		return errors.New("demo database name must be a safe task-owned name")
	}
	root, err := demoRepositoryRoot()
	if err != nil {
		return err
	}
	output, relative, err := demoOutputDirectory(root, *out)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	anchor, err := parseDemoAnchor(*asOf, now)
	if err != nil || anchor.After(now.Add(5*time.Second)) || now.Sub(anchor) > 4*time.Minute {
		return errors.New("demo anchor must be fresh UTC time")
	}
	policyConfig, err := policy.LoadConfig()
	if err != nil {
		return errors.New("pinned policy unavailable")
	}
	runID, err := randomUUID()
	if err != nil {
		return errors.New("demo identity generation failed")
	}
	if err = checkReplaceableFundingManifest(output); err != nil {
		return err
	}
	var externalProfile *profileBuild
	if *underwritingManifest != "" {
		if *asOf != "now" {
			return errors.New("actual Underwriting manifest preparation requires --as-of now")
		}
		profile, externalRunID, externalAnchor, externalDBName, err := prepareFromUnderwritingManifest(root, *underwritingManifest, policyConfig)
		if err != nil {
			return err
		}
		if externalDBName != *upstreamDatabase {
			return errors.New("underwriting manifest database differs from --underwriting-database-name")
		}
		runID, anchor, externalProfile = externalRunID, externalAnchor, &profile
	}
	runsDir := filepath.Join(output, "runs")
	if err = os.MkdirAll(runsDir, 0o755); err != nil {
		return errors.New("demo output unavailable")
	}
	if ensureNoSymlinkPath(root, runsDir) != nil {
		return errors.New("demo output path contains symlink")
	}
	stage := filepath.Join(runsDir, ".prepare-"+runID)
	if err = os.Mkdir(stage, 0o700); err != nil {
		return errors.New("demo output unavailable")
	}
	defer os.RemoveAll(stage)
	manifest, err := materializeFundingRun(relative, stage, runID, anchor, *databaseName, *upstreamDatabase, policyConfig, externalProfile)
	if err != nil {
		return err
	}
	finalRun := filepath.Join(runsDir, runID)
	if err = os.Rename(stage, finalRun); err != nil {
		return errors.New("demo run publish failed")
	}
	manifest.ArtifactRoot = filepath.ToSlash(filepath.Join("runs", runID))
	if err = writeAtomicJSON(filepath.Join(output, "manifest.json"), manifest, 0o644); err != nil {
		return err
	}
	profiles := make([]string, len(manifest.UpstreamProfiles))
	for i, p := range manifest.UpstreamProfiles {
		profiles[i] = p.Name
	}
	return json.NewEncoder(os.Stdout).Encode(prepareSummary{SourceMode: "DEMO", RunID: runID, Manifest: filepath.ToSlash(filepath.Join(relative, "manifest.json")), Profiles: profiles})
}

func materializeFundingRun(outputRelative, stage, runID string, anchor time.Time, dbName, upstreamDBName string,
	policyConfig policy.Config, externalProfile *profileBuild) (fundingManifest, error) {
	var profiles []profileBuild
	var err error
	if externalProfile != nil {
		profiles = []profileBuild{*externalProfile}
	} else {
		profiles, err = buildProfiles(stage, runID, anchor, outputRelative, upstreamDBName, policyConfig)
		if err != nil {
			return fundingManifest{}, err
		}
	}
	fundingAccount := "acct_demo_funding_" + uuidCompact(runID)[:12]
	customers := make([]domain.FacilityCustomerImport, 0, len(profiles))
	receivables := make([]domain.ReceivableImport, 0, len(profiles))
	authorities := make([]domain.AuthorityImport, 0, len(profiles))
	actors := make([]domain.ActorImport, 0, len(profiles)*2+1)
	// Automatic approval still needs one imported service actor for its audit
	// identity. It is shared across profiles, rather than duplicated per customer.
	systemID := "system-" + uuidCompact(runID)[:12]
	actors = append(actors, domain.ActorImport{ActorID: systemID, ActorType: "SYSTEM",
		AuthenticationSubject: "system:funding-collection", Active: true,
		SourceRef: domain.SourceRef{System: "synthetic-demo", RecordID: systemID,
			Version: runID, EffectiveAt: anchor.UTC()}})
	providerData := providerState{SchemaVersion: fixtureVersion, RunID: runID, Provenance: "Synthetic DEMO provider facts only; app ledger state is never written by this fixture.", FundingAccountRef: fundingAccount, Accounts: map[string]int64{fundingAccount: fundingOpeningCents}, Payments: map[string]providerPayment{}, Deposits: map[string]providerDeposit{}, Transfers: map[string]providerTransfer{}, RequestIndex: map[string]string{}, DropFirstCollectionFeeReply: true}
	for _, built := range profiles {
		customers = append(customers, built.fundingCustomer)
		receivables = append(receivables, built.receivables...)
		authority := built.authority
		authority.DestinationAccountRef = fundingAccount
		authorities = append(authorities, authority)
		actors = append(actors, built.actor, built.operator)
		if built.profile.AvailableCents < 0 {
			return fundingManifest{}, errors.New("synthetic provider opening balance invalid")
		}
		providerData.Accounts[built.profile.AccountID] = built.profile.AvailableCents
		providerData.Payments[built.profile.TransactionID] = providerPayment{CustomerID: built.profile.CustomerID, AccountID: built.profile.AccountID, AuthorizationEventID: built.profile.CanonicalEventID, AttemptedCents: built.profile.AttemptedCents, ShortfallCents: built.profile.ShortfallCents}
		for _, deposit := range built.profile.ProviderDepositIDs {
			amount := int64(10_000)
			if strings.HasSuffix(deposit, "-2") {
				amount = 709_000
			}
			providerData.Deposits[deposit] = providerDeposit{ID: deposit, CustomerID: built.profile.CustomerID, AccountRef: built.profile.AccountID, GrossCents: amount, NetCents: amount, FeeCents: 0}
		}
	}
	metadata := func(sourceID, version string, recordID string) domain.ImportMetadata {
		return domain.ImportMetadata{SchemaVersion: 1, SourceMode: "DEMO", SourceID: sourceID, SourceVersion: version, CapturedAt: anchor.UTC(), SourceRefs: []domain.SourceRef{{System: "synthetic-demo", RecordID: recordID, Version: version, EffectiveAt: anchor.UTC()}}}
	}
	facility := func(name string, reserveBps int) domain.FacilityImport {
		return domain.FacilityImport{ImportMetadata: metadata("funding-demo-facility", runID+"-"+name, "facility-"+name), FacilityID: fixtureFacilityID, FundingAccountRef: fundingAccount, Currency: "USD", FacilityTotalCents: 100_000_000, ReserveFloorBPS: reserveBps, OtherCommitmentsCents: 0, KnownEmptyBooks: true, Customers: customers}
	}
	if err = writeJSONFile(filepath.Join(stage, "facility-abundant.json"), facility("abundant", 2000), 0o644); err != nil {
		return fundingManifest{}, err
	}
	if err = writeJSONFile(filepath.Join(stage, "facility-tight.json"), facility("tight", 8000), 0o644); err != nil {
		return fundingManifest{}, err
	}
	if err = writeJSONFile(filepath.Join(stage, "receivables.json"), domain.ReceivablesImport{ImportMetadata: metadata("funding-demo-receivables", runID, "receivables"), Receivables: receivables}, 0o644); err != nil {
		return fundingManifest{}, err
	}
	if err = writeJSONFile(filepath.Join(stage, "authority.json"), domain.AuthoritiesImport{ImportMetadata: metadata("funding-demo-authorities", runID, "authorities"), Authorities: authorities}, 0o644); err != nil {
		return fundingManifest{}, err
	}
	if err = writeJSONFile(filepath.Join(stage, "actors.json"), domain.ActorsImport{ImportMetadata: metadata("funding-demo-actors", runID, "actors"), Actors: actors}, 0o644); err != nil {
		return fundingManifest{}, err
	}
	if err = writeArtifactFile(filepath.Join(stage, "mapping-funding.sql"), []byte(fundingMappingSQL(dbName, profiles)), 0o644); err != nil {
		return fundingManifest{}, err
	}
	if err = writeJSONFile(filepath.Join(stage, "provider-state.json"), providerData, 0o600); err != nil {
		return fundingManifest{}, err
	}
	privateEnv, err := makePrivateRuntimeEnv()
	if err != nil {
		return fundingManifest{}, err
	}
	if err = writeArtifactFile(filepath.Join(stage, "runtime.env"), []byte(privateEnv), 0o600); err != nil {
		return fundingManifest{}, err
	}
	upstreamInputKind := upstreamInputComponent
	provenance := "Synthetic component DEMO inputs and known-empty funding books; no captured real provider/customer information."
	if externalProfile != nil {
		upstreamInputKind = upstreamInputOwnedBundle
		provenance = "The owned known-empty Underwriting input bundle is referenced without changing its captured inputs. Funding actors, authority, receivables and provider facts remain synthetic DEMO fixtures."
	}
	manifest := fundingManifest{SchemaVersion: fixtureVersion, RunID: runID, RunAnchor: anchor.UTC(), SourceMode: "DEMO", SourceBindingID: fixtureSourceBindingID, FacilityID: fixtureFacilityID, FundingAccountRef: fundingAccount, FundingDatabaseName: dbName, UnderwritingDatabaseName: upstreamDBName, FundingOpeningCents: fundingOpeningCents, APIAddress: apiAddress, ProviderAddress: providerAddress, PolicyID: policyConfig.PolicyID(), PolicyHash: policyConfig.PolicyHash(), UpstreamInputKind: upstreamInputKind, ArtifactRoot: ".", PrivateEnvFile: "runtime.env", ProviderStateFile: "provider-state.json", FacilityAbundantFile: "facility-abundant.json", FacilityTightFile: "facility-tight.json", ReceivablesFile: "receivables.json", AuthorityFile: "authority.json", ActorsFile: "actors.json", MappingSQLFile: "mapping-funding.sql", UpstreamProfiles: make([]upstreamProfileRef, 0, len(profiles)), Provenance: provenance}
	for _, p := range profiles {
		manifest.UpstreamProfiles = append(manifest.UpstreamProfiles, p.profile)
	}
	if err = writeJSONFile(filepath.Join(stage, "manifest.json"), manifest, 0o644); err != nil {
		return fundingManifest{}, err
	}
	return manifest, nil
}

func buildProfiles(stage, runID string, anchor time.Time, outputRelative, upstreamDBName string, policyConfig policy.Config) ([]profileBuild, error) {
	profiles := []profileBuild{}
	for _, profileName := range []string{"abundant", "tight"} {
		customerID, err := syntheticID("acct_demo_", runID, profileName)
		if err != nil {
			return nil, err
		}
		accountID, err := syntheticID("acct_demo_account_", runID, profileName)
		if err != nil {
			return nil, err
		}
		cardID, err := syntheticID("card_demo_", runID, profileName)
		if err != nil {
			return nil, err
		}
		shortfallID, err := randomUUID()
		if err != nil {
			return nil, err
		}
		transactionID, err := randomUUID()
		if err != nil {
			return nil, err
		}
		eventID, err := randomUUID()
		if err != nil {
			return nil, err
		}
		lifecycleID, err := randomUUID()
		if err != nil {
			return nil, err
		}
		authorityID, err := syntheticID("demo-authority-", runID, profileName)
		if err != nil {
			return nil, err
		}
		invoiceID, err := syntheticID("invoice-demo-", runID, profileName)
		if err != nil {
			return nil, err
		}
		depositOne, err := syntheticID("deposit-demo-", runID, profileName+"-1")
		if err != nil {
			return nil, err
		}
		depositTwo, err := syntheticID("deposit-demo-", runID, profileName+"-2")
		if err != nil {
			return nil, err
		}
		customerSnapshotID, err := randomUUID()
		if err != nil {
			return nil, err
		}
		facilitySnapshotID, err := randomUUID()
		if err != nil {
			return nil, err
		}
		built, err := buildProfile(stage, runID, anchor, outputRelative, upstreamDBName, profileName, customerID, accountID, cardID, shortfallID, transactionID, eventID, lifecycleID, authorityID, invoiceID, depositOne, depositTwo, customerSnapshotID, facilitySnapshotID, policyConfig)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, built)
	}
	return profiles, nil
}

func buildProfile(stage, runID string, anchor time.Time, outputRelative, upstreamDBName, profileName, customerID, accountID, cardID, shortfallID, transactionID, eventID, lifecycleID, authorityID, invoiceID, depositOne, depositTwo, customerSnapshotID, facilitySnapshotID string, policyConfig policy.Config) (profileBuild, error) {
	today := dateOf(anchor)
	sourceRef := func(system, id, version string) sourceReference {
		return sourceReference{System: system, RecordID: id, Version: version, EffectiveAt: anchor.UTC()}
	}
	windowStart, windowEnd := today.AddDate(0, 0, -90), today.AddDate(0, 0, -1)
	invoiceDue := today.AddDate(0, 0, 20)
	started := today.AddDate(0, -9, 0)
	customerRefs := []sourceReference{sourceRef("synthetic-demo", "customer-"+profileName, runID), sourceRef("synthetic-demo", "ledger-"+profileName, runID), sourceRef("synthetic-demo", "invoice-"+profileName, runID), sourceRef("synthetic-demo", "classification-"+profileName, runID)}
	onboarding, active, suspended, accountStatus := true, true, false, "ACTIVE"
	coverage := map[string]bool{"standing": true, "balance_history": true, "inflow_history": true, "bridge_history": true, "lifetime_defaults": true, "reservations": true, "receivables": true, "classification": true}
	customer := customerSnapshot{SchemaVersion: 1, SourceMode: "DEMO", SourceID: "funding-demo-fixtures", SourceVersion: runID + "-" + profileName, CapturedAt: anchor.UTC(), SourceRefs: customerRefs, CustomerID: customerID, AccountID: accountID, CardID: cardID, Coverage: coverage, Standing: map[string]any{"relationship_started_on": started.Format("2006-01-02"), "onboarding_complete": onboarding, "active": active, "suspended": suspended, "provider_account_status": accountStatus}, HistoryWindow: map[string]string{"window_start": windowStart.Format("2006-01-02"), "window_end": windowEnd.Format("2006-01-02")}, BalanceHistory: []map[string]any{}, InflowHistory: []map[string]any{}, Bridges: []map[string]any{}, Reservations: []map[string]any{}, Receivables: []map[string]any{}, Classifications: []map[string]any{}}
	for date := windowStart; !date.After(windowEnd); date = date.AddDate(0, 0, 1) {
		customer.BalanceHistory = append(customer.BalanceHistory, map[string]any{"date": date.Format("2006-01-02"), "balance_cents": 5_000_000})
	}
	for i, offset := range []int{-20, -50, -80} {
		customer.InflowHistory = append(customer.InflowHistory, map[string]any{"id": fmt.Sprintf("%s-inflow-%d", runID, i+1), "date": today.AddDate(0, 0, offset).Format("2006-01-02"), "amount_cents": 10_000_000, "kind": "EXTERNAL_SETTLED", "payer_id": "synthetic-payer"})
	}
	invoiceRef := sourceRef("synthetic-accounting", invoiceID, runID)
	customer.Receivables = append(customer.Receivables, map[string]any{"invoice_id": invoiceID, "payer_id": "synthetic-payer", "currency": "USD", "eligible_unpaid_cents": 1_000_000, "allocated_cents": 0, "due_on": invoiceDue.Format("2006-01-02"), "due_date_verified": true, "allocation_complete": true, "source_ref": invoiceRef})
	customer.Classifications = append(customer.Classifications, map[string]any{"transaction_id": transactionID, "merchant_category": "suppliers", "source_ref": sourceRef("synthetic-demo", "classification-"+profileName, runID)})
	facilityRefs := []sourceReference{sourceRef("synthetic-demo", "facility-"+profileName, runID)}
	reserveFloor := int64(2000)
	if profileName == "tight" {
		reserveFloor = 8000
	}
	facility := facilitySnapshot{SchemaVersion: 1, SourceMode: "DEMO", SourceID: "funding-demo-fixtures", SourceVersion: runID + "-" + profileName, CapturedAt: anchor.UTC(), SourceRefs: facilityRefs, FacilityID: fixtureFacilityID, Currency: "USD", FacilityTotalCents: 100_000_000, ReserveFloorBPS: int32(reserveFloor), FundedPrincipalCents: 0, ReservedPrincipalCents: 0, LedgerVersion: runID + "-empty", LedgerComplete: true}
	customerWire, _ := json.Marshal(customer)
	facilityWire, _ := json.Marshal(facility)
	var policyCustomer policy.CustomerSnapshot
	var policyFacility policy.FacilitySnapshot
	if json.Unmarshal(customerWire, &policyCustomer) != nil || json.Unmarshal(facilityWire, &policyFacility) != nil {
		return profileBuild{}, errors.New("upstream fixture conversion failed")
	}
	policyCustomer.ID = customerSnapshotID
	policyCustomer.Version = 1
	policyFacility.ID = facilitySnapshotID
	policyFacility.Version = 1
	observed, requested, created := anchor.Add(-5*time.Second), anchor.Add(-10*time.Second), anchor.Add(-30*time.Second)
	attempted, available, need := int64(900_000), int64(200_000), int64(700_000)
	customerPtr, cardPtr, transactionPtr, lifecyclePtr, currencyPtr := customerID, cardID, transactionID, lifecycleID, "USD"
	shortfall := shortfallRecord{ID: shortfallID, CustomerID: &customerPtr, AccountID: accountID, CardID: &cardPtr, Outcome: "SHORTFALL_DETECTED", Currency: &currencyPtr, AttemptedAmountCents: &attempted, AvailableBalanceCents: &available, ShortfallCents: &need, CanonicalEventID: eventID, TransactionID: &transactionPtr, LifecycleID: &lifecyclePtr, CreatedAt: created.UTC(), ProviderCreatedAt: timePointer(anchor.Add(-60 * time.Second)), ProviderTransactedAt: timePointer(anchor.Add(-60 * time.Second)), ProviderUpdatedAt: timePointer(anchor.Add(-60 * time.Second)), BalanceRequestedAt: timePointer(requested.UTC()), BalanceObservedAt: timePointer(observed.UTC()), RequiresRecheck: true, AmountSemantics: "current_need_at_balance_observation"}
	policyShortfall := policy.Shortfall{ID: shortfallID, CustomerID: customerID, AccountID: accountID, CardID: cardID, Outcome: "SHORTFALL_DETECTED", Currency: "USD", AttemptedAmountCents: attempted, AvailableBalanceCents: available, ShortfallCents: need, CanonicalEventID: eventID, TransactionID: transactionID, BalanceObservedAt: observed.UTC()}
	result := policy.Evaluate(policy.Input{EvaluatedAt: anchor.UTC(), Mode: "DEMO", FacilityID: fixtureFacilityID, TermDays: 30, Shortfall: policyShortfall, Customer: policyCustomer, Facility: policyFacility}, policyConfig)
	if result.PolicyHash != policyConfig.PolicyHash() || result.PrincipalCents == nil || result.EstimatedFeeCents == nil || result.TotalDueCents == nil || result.Tier == nil || *result.Tier != "B" {
		return profileBuild{}, errors.New("fixture policy evaluation incomplete")
	}
	wantStatus := "ELIGIBLE"
	wantApproval := false
	if profileName == "tight" {
		wantStatus = "REVIEW_REQUIRED"
		wantApproval = true
	}
	if string(result.Status) != wantStatus || result.ApprovalRequired != wantApproval {
		return profileBuild{}, errors.New("fixture policy profile does not match expected guard")
	}
	for _, rule := range result.Rules {
		if rule.Status != policy.RulePass {
			return profileBuild{}, errors.New("fixture contains an unexpected nonpassing policy rule")
		}
	}
	upstreamDir := filepath.Join(stage, "upstream", profileName)
	runIDDir := filepath.Join(upstreamDir, "runs", runID)
	if err := os.MkdirAll(runIDDir, 0o755); err != nil {
		return profileBuild{}, errors.New("upstream fixture directory unavailable")
	}
	if err := writeJSONFile(filepath.Join(runIDDir, "customer.json"), customer, 0o644); err != nil {
		return profileBuild{}, err
	}
	if err := writeJSONFile(filepath.Join(runIDDir, "facility.json"), facility, 0o644); err != nil {
		return profileBuild{}, err
	}
	shortfallPath := filepath.Join(runIDDir, "shortfalls.json")
	pack := shortfallPack{SourceMode: "DEMO", Provenance: "Synthetic fresh shortfall source fixture; no live decline extraction or real payment records.", Records: []shortfallEntry{{ScenarioID: profileName, Response: shortfall}}}
	if err := writeJSONFile(shortfallPath, pack, 0o644); err != nil {
		return profileBuild{}, err
	}
	profileMapping := filepath.Join(runIDDir, "mapping-setup.sql")
	if err := writeArtifactFile(profileMapping, []byte(fundingMappingSQL(upstreamDBName, []profileBuild{{profile: upstreamProfileRef{CustomerID: customerID, AccountID: accountID, CardID: cardID}}})), 0o644); err != nil {
		return profileBuild{}, err
	}
	rules := make([]expectedRule, len(result.Rules))
	for i, rule := range result.Rules {
		rules[i] = expectedRule{RuleID: string(rule.RuleID), Status: string(rule.Status)}
	}
	upScenario := upstreamScenario{ID: profileName, CustomerID: customerID, AccountID: accountID, CardID: cardID, ShortfallID: shortfallID, TransactionID: transactionID, CanonicalEventID: eventID, LifecycleID: lifecycleID, IdempotencyKey: "funding-demo:" + runID + ":" + profileName, CustomerFile: filepath.ToSlash(filepath.Join("runs", runID, "customer.json")), ExpectedStatus: wantStatus, PrincipalCents: need, TermDays: 30, EstimatedFeeCents: *result.EstimatedFeeCents, TotalDueCents: *result.TotalDueCents, CustomerLimitCents: valueOrZero(result.CustomerLimitCents), ExistingExposureCents: valueOrZero(result.ExistingExposureCents), PostFundingExposureCents: valueOrZero(result.PostFundingExposureCents), FreeReceivablesCents: 1_000_000, ExpectedRules: rules, Provenance: "Synthetic input evaluated by the pinned public underwriting policy; no saved decision is fabricated."}
	books := upstreamFacilityBooks{FacilityTotalCents: facility.FacilityTotalCents, ReserveFloorBPS: int64(facility.ReserveFloorBPS), FeesCountedInFacilityCash: false, AvailableCapitalCents: valueOrZero(result.AvailableCapitalCents)}
	manifestRelative := filepath.ToSlash(filepath.Join(outputRelative, "runs", runID, "upstream", profileName))
	upManifest := upstreamManifest{FixturePackVersion: 1, RunID: runID, RunAnchor: anchor.UTC(), SourceMode: "DEMO", PolicyID: policyConfig.PolicyID(), PolicyHash: policyConfig.PolicyHash(), OutputDirectory: manifestRelative, DatabaseName: upstreamDBName, FacilityID: fixtureFacilityID, FacilityFile: filepath.ToSlash(filepath.Join("runs", runID, "facility.json")), ShortfallsFile: filepath.ToSlash(filepath.Join("runs", runID, "shortfalls.json")), MappingSetupSQL: filepath.ToSlash(filepath.Join("runs", runID, "mapping-setup.sql")), ExpectedTraceFile: filepath.ToSlash(filepath.Join("runs", runID, "expected-traces.json")), FacilityBooks: books, Scenarios: []upstreamScenario{upScenario}, Provenance: "Synthetic DEMO input fixtures only. The underwriting API creates and saves the real evaluator result."}
	if err := writeJSONFile(filepath.Join(runIDDir, "expected-traces.json"), []upstreamScenario{upScenario}, 0o644); err != nil {
		return profileBuild{}, err
	}
	if err := writeJSONFile(filepath.Join(runIDDir, "manifest.json"), upManifest, 0o644); err != nil {
		return profileBuild{}, err
	}
	if err := writeJSONFile(filepath.Join(upstreamDir, "manifest.json"), upManifest, 0o644); err != nil {
		return profileBuild{}, err
	}
	profile := upstreamProfileRef{Name: profileName, UnderwritingManifest: filepath.ToSlash(filepath.Join(outputRelative, "runs", runID, "upstream", profileName, "manifest.json")), CustomerID: customerID, AccountID: accountID, CardID: cardID, ShortfallID: shortfallID, TransactionID: transactionID, CanonicalEventID: eventID, ExpectedStatus: wantStatus, ApprovalRequired: wantApproval, PrincipalCents: *result.PrincipalCents, TermDays: 30, ContractFeeCents: *result.EstimatedFeeCents, TotalDueCents: *result.TotalDueCents, AttemptedCents: attempted, AvailableCents: available, ShortfallCents: need}
	depositIDs := []string{}
	if profileName == "abundant" {
		depositIDs = []string{depositOne, depositTwo}
	}
	profile.ProviderDepositIDs = depositIDs
	operatorSubject := "demo:operator-" + uuidCompact(runID)[:8] + "-" + profileName
	profile.OperatorSubject = operatorSubject
	profile.CustomerSubject = "demo:customer-" + uuidCompact(runID)[:8] + "-" + profileName
	operatorActor := domain.ActorImport{ActorID: "operator-" + uuidCompact(runID)[:8] + "-" + profileName, ActorType: "OPERATOR", AuthenticationSubject: operatorSubject, Active: true, SourceRef: domain.SourceRef{System: "synthetic-demo", RecordID: "operator-" + profileName, Version: runID, EffectiveAt: anchor.UTC()}}
	profileBuild := profileBuild{profile: profile, fundingCustomer: domain.FacilityCustomerImport{CustomerID: customerID, AccountID: accountID, CardID: cardID, HistoryComplete: true}, receivables: []domain.ReceivableImport{{CustomerID: customerID, SourceSystem: "synthetic-accounting", InvoiceID: invoiceID, PayerID: customerID, Currency: "USD", EligibleUnpaidCents: 1_000_000, DueOn: stringPtr(invoiceDue.Format("2006-01-02")), DueDateVerified: true, AllocationComplete: true, SourceComplete: true, SourceRef: domain.SourceRef{System: "synthetic-accounting", RecordID: invoiceID, Version: runID, EffectiveAt: anchor.UTC()}}}, authority: domain.AuthorityImport{ID: authorityID, Version: 1, CustomerID: customerID, SourceAccountRef: accountID, ProductCode: "CONNECTED_ACCOUNT_TRANSFERS", MandateReference: "synthetic-mandate-" + uuidCompact(runID)[:12], Currency: "USD", PermittedKinds: []string{"COLLECT_FEE", "COLLECT_PRINCIPAL"}, PermittedPurposes: []string{"professional_business_services", "loan_credit_repayment"}, MaximumDebitCents: 1_000_000, ValidFrom: anchor.Add(-time.Minute).UTC(), ExpiresAt: anchor.Add(365 * 24 * time.Hour).UTC(), Status: "ACTIVE", DisclosuresVersion: "synthetic-demo-v1", EvidenceRefs: []domain.SourceRef{{System: "synthetic-demo", RecordID: "synthetic-mandate-" + uuidCompact(runID)[:12], Version: runID, EffectiveAt: anchor.UTC()}}}, actor: domain.ActorImport{ActorID: "customer-" + uuidCompact(runID)[:8] + "-" + profileName, ActorType: "CUSTOMER", AuthenticationSubject: profile.CustomerSubject, CustomerID: &customerPtr, Active: true, SourceRef: domain.SourceRef{System: "synthetic-demo", RecordID: "actor-" + profileName, Version: runID, EffectiveAt: anchor.UTC()}}, operator: operatorActor}
	return profileBuild, nil
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
func stringPtr(value string) *string         { return &value }
func timePointer(value time.Time) *time.Time { return &value }
func dateOf(value time.Time) time.Time {
	date := value.UTC()
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
}

func fundingMappingSQL(databaseName string, profiles []profileBuild) string {
	var sql strings.Builder
	sql.WriteString("-- Synthetic mappings for the task-owned DEMO database only.\nBEGIN;\nDO $$ BEGIN IF current_database() <> ")
	sql.WriteString(sqlQuote(databaseName))
	sql.WriteString(" THEN RAISE EXCEPTION 'wrong demo database'; END IF; END $$;\n")
	for _, p := range profiles {
		customerID, accountID, cardID := p.profile.CustomerID, p.profile.AccountID, p.profile.CardID
		salesforceID := "demo-sf-" + strings.TrimPrefix(p.profile.CustomerID, "acct_demo_")
		sql.WriteString("INSERT INTO public.customer_roster (customer_id,salesforce_account_id,airwallex_connected_account_id,enrolled_by,enrollment_reason) VALUES (")
		sql.WriteString(joinSQL(customerID, salesforceID, accountID, "funding-demo", "Synthetic DEMO mapping"))
		sql.WriteString(") ON CONFLICT (customer_id) DO NOTHING;\n")
		sql.WriteString("INSERT INTO public.customer_roster_audit (customer_id,action,actor,reason) SELECT ")
		sql.WriteString(joinSQL(customerID, "enrolled", "funding-demo", "Synthetic DEMO mapping"))
		sql.WriteString(" WHERE NOT EXISTS (SELECT 1 FROM public.customer_roster_audit WHERE customer_id=")
		sql.WriteString(sqlQuote(customerID))
		sql.WriteString(" AND action='enrolled');\n")
		sql.WriteString("INSERT INTO public.issuing_card_bindings (card_id,customer_id,airwallex_account_id,bound_by,binding_reason,is_active) VALUES (")
		sql.WriteString(joinSQL(cardID, customerID, accountID, "funding-demo", "Synthetic DEMO mapping"))
		sql.WriteString(",true) ON CONFLICT (card_id) DO NOTHING;\n")
		sql.WriteString("INSERT INTO public.issuing_card_binding_audit (card_id,customer_id,airwallex_account_id,action,actor,reason) SELECT ")
		sql.WriteString(joinSQL(cardID, customerID, accountID, "bound", "funding-demo", "Synthetic DEMO mapping"))
		sql.WriteString(" WHERE NOT EXISTS (SELECT 1 FROM public.issuing_card_binding_audit WHERE card_id=")
		sql.WriteString(sqlQuote(cardID))
		sql.WriteString(" AND action='bound');\n")
	}
	for _, p := range profiles {
		sql.WriteString("DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM public.customer_roster WHERE customer_id=")
		sql.WriteString(sqlQuote(p.profile.CustomerID))
		sql.WriteString(" AND airwallex_connected_account_id=")
		sql.WriteString(sqlQuote(p.profile.AccountID))
		sql.WriteString(" AND is_active) OR NOT EXISTS (SELECT 1 FROM public.issuing_card_bindings WHERE card_id=")
		sql.WriteString(sqlQuote(p.profile.CardID))
		sql.WriteString(" AND customer_id=")
		sql.WriteString(sqlQuote(p.profile.CustomerID))
		sql.WriteString(" AND airwallex_account_id=")
		sql.WriteString(sqlQuote(p.profile.AccountID))
		sql.WriteString(" AND is_active) THEN RAISE EXCEPTION 'synthetic mapping verification failed'; END IF; END $$;\n")
	}
	sql.WriteString("COMMIT;\n")
	return sql.String()
}

func joinSQL(values ...string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = sqlQuote(v)
	}
	return strings.Join(parts, ",")
}
func sqlQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func makePrivateRuntimeEnv() (string, error) {
	keys := make([]string, 9)
	seen := map[string]bool{}
	for i := range keys {
		for {
			key, err := randomSecret()
			if err != nil {
				return "", err
			}
			if !seen[key] {
				seen[key] = true
				keys[i] = key
				break
			}
		}
	}
	return strings.Join([]string{
		"# Private synthetic DEMO configuration. Keep mode, host, and keys isolated.",
		"SERVICING_MODE=DEMO", "SERVICING_HTTP_ADDR=" + apiAddress, "SERVICING_FACILITY_ID=" + fixtureFacilityID, "SERVICING_SOURCE_BINDING_ID=" + fixtureSourceBindingID,
		"SERVICING_COORDINATOR_SECRET=" + keys[0], "SERVICING_OPERATOR_SECRET=" + keys[1], "SERVICING_CUSTOMER_SECRET=" + keys[2], "SERVICING_READER_SECRET=" + keys[3],
		"UNDERWRITING_MODE=DEMO", "UNDERWRITING_HTTP_ADDR=" + underwritingAddress, "UNDERWRITING_FACILITY_ID=" + fixtureFacilityID, "UNDERWRITING_BASE_URL=http://" + underwritingAddress, "UNDERWRITING_SERVICE_SECRET=" + keys[4],
		"SHORTFALL_ENVIRONMENT=DEMO", "SHORTFALL_HTTP_ADDR=" + shortfallAddress, "SHORTFALL_BASE_URL=http://" + shortfallAddress, "SHORTFALL_SERVICE_SECRET=" + keys[5], "SHORTFALL_WEBHOOK_SECRET=" + keys[6],
		"SERVICING_PROVIDER_MODE=MOCK_HTTP", "SERVICING_PROVIDER_BASE_URL=http://" + providerAddress, "SERVICING_LIVE_DISPATCH_ENABLED=false",
		"AIRWALLEX_CLIENT_ID=demo-client-id", "AIRWALLEX_API_KEY=demo-api-key", "AIRWALLEX_WEBHOOK_SECRET=" + keys[7],
		"DEMO_PROVIDER_CONTROL_KEY=" + keys[8],
	}, "\n") + "\n", nil
}

func randomSecret() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", errors.New("demo secret generation failed")
	}
	return hex.EncodeToString(data[:]), nil
}
func randomUUID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", errors.New("demo id generation failed")
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(data[:])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:], nil
}
func uuidCompact(value string) string { return strings.ReplaceAll(value, "-", "") }
func syntheticID(prefix, runID, profile string) (string, error) {
	return prefix + uuidCompact(runID)[:8] + "_" + profile, nil
}

func parseDemoAnchor(value string, now time.Time) (time.Time, error) {
	if value == "now" {
		return now.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, errors.New("demo anchor must use UTC")
	}
	return parsed.UTC(), nil
}

func demoRepositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", errors.New("repository root unavailable")
	}
	for {
		gitInfo, gitErr := os.Lstat(filepath.Join(current, ".git"))
		underwritingInfo, underwritingErr := os.Stat(filepath.Join(current, "services", "underwriting-engine", "go.mod"))
		if gitErr == nil && (gitInfo.IsDir() || gitInfo.Mode().IsRegular()) &&
			underwritingErr == nil && underwritingInfo.Mode().IsRegular() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("repository root unavailable")
		}
		current = parent
	}
}

func demoOutputDirectory(root, value string) (string, string, error) {
	if strings.TrimSpace(value) == "" {
		return "", "", errors.New("demo output must be under .dev")
	}
	path := value
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
		return "", "", errors.New("demo output path contains symlink")
	}
	return path, filepath.ToSlash(filepath.Join(".dev", relative)), nil
}

func ensureNoSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path outside root")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("path contains symlink")
		}
	}
	return nil
}

func writeJSONFile(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("demo artifact encode failed")
	}
	return writeArtifactFile(path, data, mode)
}
func writeArtifactFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return errors.New("demo artifact write failed")
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return errors.New("demo artifact permissions failed")
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return errors.New("demo artifact write failed")
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return errors.New("demo artifact sync failed")
	}
	if err = f.Close(); err != nil {
		return errors.New("demo artifact close failed")
	}
	return nil
}
func writeAtomicJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("manifest encoding failed")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*.tmp")
	if err != nil {
		return errors.New("manifest publish failed")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return errors.New("manifest publish failed")
	}
	if _, err = temp.Write(data); err != nil {
		_ = temp.Close()
		return errors.New("manifest publish failed")
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		return errors.New("manifest publish failed")
	}
	if err = temp.Close(); err != nil {
		return errors.New("manifest publish failed")
	}
	if err = os.Rename(tempPath, path); err != nil {
		return errors.New("manifest publish failed")
	}
	return nil
}

func checkReplaceableFundingManifest(output string) error {
	path := filepath.Join(output, "manifest.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("existing manifest is not a regular funding demo manifest")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxDemoArtifactBytes {
		return errors.New("existing manifest is not recognized")
	}
	var previous fundingManifest
	if decodeStrict(data, &previous) != nil || previous.SchemaVersion != fixtureVersion || previous.SourceMode != "DEMO" || previous.RunID == "" || !strings.HasPrefix(previous.ArtifactRoot, "runs/") {
		return errors.New("existing manifest is not a recognized funding DEMO manifest")
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	if !utf8.Valid(data) || rejectDuplicateJSONKeys(data) != nil {
		return errors.New("demo artifact JSON invalid")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if dec.Decode(target) != nil {
		return errors.New("demo artifact JSON invalid")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("demo artifact JSON invalid")
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	var scan func(int) error
	scan = func(depth int) error {
		if depth > 64 {
			return errors.New("json nesting too deep")
		}
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyToken, keyErr := dec.Token()
				if keyErr != nil {
					return keyErr
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate JSON key")
				}
				seen[key] = true
				if err = scan(depth + 1); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			count := 0
			for dec.More() {
				count++
				if count > 10000 {
					return errors.New("json array too large")
				}
				if err = scan(depth + 1); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		default:
			return errors.New("invalid json delimiter")
		}
	}
	if err := scan(0); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}
