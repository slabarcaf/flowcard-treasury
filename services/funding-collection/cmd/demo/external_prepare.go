package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

type externalReceivable struct {
	InvoiceID           string          `json:"invoice_id"`
	PayerID             string          `json:"payer_id"`
	Currency            string          `json:"currency"`
	EligibleUnpaidCents int64           `json:"eligible_unpaid_cents"`
	AllocatedCents      int64           `json:"allocated_cents"`
	DueOn               *string         `json:"due_on"`
	DueDateVerified     *bool           `json:"due_date_verified"`
	AllocationComplete  *bool           `json:"allocation_complete"`
	SourceRef           sourceReference `json:"source_ref"`
}

func prepareFromUnderwritingManifest(repoRoot, path string, policyConfig policy.Config) (profileBuild, string, time.Time, string, error) {
	var empty profileBuild
	absPath, err := filepath.Abs(path)
	if err != nil {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest path invalid")
	}
	devRoot := filepath.Join(repoRoot, ".dev")
	relative, err := filepath.Rel(devRoot, absPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		ensureNoSymlinkPath(repoRoot, absPath) != nil {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest must be under .dev without symlinks")
	}
	info, err := os.Lstat(absPath)
	if err != nil || !info.Mode().IsRegular() {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest unavailable")
	}
	bundleRoot := filepath.Dir(absPath)
	if filepath.Base(filepath.Dir(bundleRoot)) == "runs" {
		bundleRoot = filepath.Dir(filepath.Dir(bundleRoot))
	}
	if ensureNoSymlinkPath(devRoot, bundleRoot) != nil {
		return empty, "", time.Time{}, "", errors.New("underwriting bundle path invalid")
	}
	manifestBytes, err := readDemoFile(absPath, maxDemoArtifactBytes)
	if err != nil {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest unavailable")
	}
	var manifest upstreamManifest
	if decodeStrict(manifestBytes, &manifest) != nil || manifest.FixturePackVersion != 1 ||
		manifest.SourceMode != config.ModeDemo || !demoUUID(manifest.RunID) ||
		manifest.FacilityID != fixtureFacilityID ||
		!config.ValidOwnedDatabaseName(manifest.DatabaseName, "flowcard_underwriting_demo") ||
		manifest.PolicyID != policyConfig.PolicyID() || manifest.PolicyHash != policyConfig.PolicyHash() ||
		len(manifest.Scenarios) == 0 || manifest.FacilityBooks.FundedPrincipalCents != 0 ||
		manifest.FacilityBooks.ReservedPrincipalCents != 0 ||
		manifest.FacilityBooks.CustomerFundedPrincipalCents != 0 ||
		manifest.FacilityBooks.OtherFundedPrincipalCents != 0 ||
		manifest.FacilityBooks.CustomerReservedPrincipalCents != 0 ||
		manifest.FacilityBooks.OtherReservedPrincipalCents != 0 ||
		manifest.FacilityBooks.FeesCountedInFacilityCash || manifest.FacilityBooks.AvailableCapitalCents <= 0 {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest is not a compatible known-empty DEMO profile")
	}
	rootRelative, err := filepath.Rel(repoRoot, bundleRoot)
	if err != nil || filepath.ToSlash(rootRelative) != manifest.OutputDirectory {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest output directory binding invalid")
	}
	mappingPath, err := demoArtifactPath(bundleRoot, manifest.MappingSetupSQL)
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	mappingSQL, err := readDemoFile(mappingPath, maxDemoArtifactBytes)
	if err != nil || !bytes.Contains(mappingSQL, []byte("current_database() <> '"+manifest.DatabaseName+"'")) {
		return empty, "", time.Time{}, "", errors.New("underwriting mapping SQL target does not match its manifest")
	}
	now := time.Now().UTC()
	if manifest.RunAnchor.After(now.Add(5*time.Second)) || now.Sub(manifest.RunAnchor) > 4*time.Minute {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest is stale; ask its owner to prepare a fresh run")
	}
	manifestRelative, err := filepath.Rel(bundleRoot, absPath)
	if err != nil || (manifestRelative != "manifest.json" &&
		manifestRelative != filepath.Join("runs", manifest.RunID, "manifest.json")) {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest location invalid")
	}
	retainedPath, err := demoArtifactPath(bundleRoot, filepath.ToSlash(filepath.Join("runs", manifest.RunID, "manifest.json")))
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	retainedBytes, err := readDemoFile(retainedPath, maxDemoArtifactBytes)
	if err != nil || !bytes.Equal(retainedBytes, manifestBytes) {
		return empty, "", time.Time{}, "", errors.New("underwriting retained manifest differs")
	}
	var selected *upstreamScenario
	for i := range manifest.Scenarios {
		if manifest.Scenarios[i].ExpectedStatus == "ELIGIBLE" {
			if selected != nil {
				return empty, "", time.Time{}, "", errors.New("underwriting bundle has multiple eligible scenarios")
			}
			selected = &manifest.Scenarios[i]
		}
	}
	if selected == nil || selected.CustomerID == "" || selected.AccountID == "" || selected.CardID == "" ||
		selected.ShortfallID == "" || selected.TransactionID == "" || selected.TermDays < 7 || selected.TermDays > 90 ||
		selected.PrincipalCents <= 0 || selected.EstimatedFeeCents <= 0 ||
		selected.TotalDueCents != selected.PrincipalCents+selected.EstimatedFeeCents {
		return empty, "", time.Time{}, "", errors.New("underwriting eligible scenario is incomplete")
	}
	facilityPath, err := demoArtifactPath(bundleRoot, manifest.FacilityFile)
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	facilityBytes, err := readDemoFile(facilityPath, maxDemoArtifactBytes)
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	var facility facilitySnapshot
	if decodeStrict(facilityBytes, &facility) != nil || facility.SourceMode != config.ModeDemo ||
		facility.FacilityID != fixtureFacilityID || facility.FundedPrincipalCents != 0 ||
		facility.ReservedPrincipalCents != 0 || facility.ReserveFloorBPS != 2000 ||
		facility.FacilityTotalCents != fundingOpeningCents || !facility.LedgerComplete ||
		!facility.CapturedAt.Equal(manifest.RunAnchor) || facility.FacilityTotalCents != manifest.FacilityBooks.FacilityTotalCents ||
		int64(facility.ReserveFloorBPS) != manifest.FacilityBooks.ReserveFloorBPS {
		return empty, "", time.Time{}, "", errors.New("underwriting facility snapshot is not known-empty")
	}
	customerPath, err := demoArtifactPath(bundleRoot, selected.CustomerFile)
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	customerBytes, err := readDemoFile(customerPath, maxDemoArtifactBytes)
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	var customer customerSnapshot
	if decodeStrict(customerBytes, &customer) != nil || customer.SourceMode != config.ModeDemo ||
		customer.CustomerID != selected.CustomerID || customer.AccountID != selected.AccountID || customer.CardID != selected.CardID ||
		!customer.CapturedAt.Equal(manifest.RunAnchor) ||
		!customer.Coverage["standing"] || !customer.Coverage["balance_history"] ||
		!customer.Coverage["inflow_history"] || !customer.Coverage["bridge_history"] ||
		!customer.Coverage["lifetime_defaults"] || !customer.Coverage["reservations"] ||
		!customer.Coverage["receivables"] || !customer.Coverage["classification"] ||
		len(customer.Bridges) != 0 || len(customer.Reservations) != 0 || len(customer.Receivables) == 0 {
		return empty, "", time.Time{}, "", errors.New("underwriting customer snapshot is incomplete or not known-empty")
	}
	shortfallPath, err := demoArtifactPath(bundleRoot, manifest.ShortfallsFile)
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	shortfallBytes, err := readDemoFile(shortfallPath, maxDemoArtifactBytes)
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	var shortfalls shortfallPack
	if decodeStrict(shortfallBytes, &shortfalls) != nil || shortfalls.SourceMode != config.ModeDemo {
		return empty, "", time.Time{}, "", errors.New("underwriting shortfall fixture invalid")
	}
	var selectedShortfall *shortfallRecord
	for i := range shortfalls.Records {
		if shortfalls.Records[i].ScenarioID == selected.ID {
			selectedShortfall = &shortfalls.Records[i].Response
			break
		}
	}
	if selectedShortfall == nil || selectedShortfall.ID != selected.ShortfallID ||
		selectedShortfall.CustomerID == nil || *selectedShortfall.CustomerID != selected.CustomerID ||
		selectedShortfall.Outcome != "SHORTFALL_DETECTED" || selectedShortfall.Currency == nil || *selectedShortfall.Currency != "USD" ||
		selectedShortfall.AccountID != selected.AccountID || selectedShortfall.TransactionID == nil ||
		*selectedShortfall.TransactionID != selected.TransactionID || selectedShortfall.ShortfallCents == nil ||
		*selectedShortfall.ShortfallCents != selected.PrincipalCents || selectedShortfall.AttemptedAmountCents == nil ||
		selectedShortfall.AvailableBalanceCents == nil || selectedShortfall.CardID == nil ||
		*selectedShortfall.CardID != selected.CardID || selectedShortfall.CanonicalEventID != selected.CanonicalEventID ||
		selectedShortfall.LifecycleID == nil || *selectedShortfall.LifecycleID != selected.LifecycleID ||
		*selectedShortfall.AttemptedAmountCents <= 0 || *selectedShortfall.AvailableBalanceCents < 0 ||
		!selectedShortfall.RequiresRecheck || selectedShortfall.AmountSemantics != "current_need_at_balance_observation" ||
		!freshSourceInstant(selectedShortfall.CreatedAt, manifest.RunAnchor) ||
		!freshSourcePointer(selectedShortfall.BalanceRequestedAt, manifest.RunAnchor) ||
		!freshSourcePointer(selectedShortfall.BalanceObservedAt, manifest.RunAnchor) ||
		selectedShortfall.BalanceRequestedAt.After(*selectedShortfall.BalanceObservedAt) {
		return empty, "", time.Time{}, "", errors.New("underwriting shortfall does not match its eligible scenario")
	}
	if *selectedShortfall.AvailableBalanceCents > math.MaxInt64-*selectedShortfall.ShortfallCents ||
		*selectedShortfall.AttemptedAmountCents != *selectedShortfall.AvailableBalanceCents+*selectedShortfall.ShortfallCents {
		return empty, "", time.Time{}, "", errors.New("underwriting shortfall amounts are inconsistent")
	}
	var policyCustomer policy.CustomerSnapshot
	var policyFacility policy.FacilitySnapshot
	if json.Unmarshal(customerBytes, &policyCustomer) != nil || json.Unmarshal(facilityBytes, &policyFacility) != nil {
		return empty, "", time.Time{}, "", errors.New("underwriting snapshot conversion failed")
	}
	customerSnapshotID, err := randomUUID()
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	facilitySnapshotID, err := randomUUID()
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	policyCustomer.ID, policyCustomer.Version = customerSnapshotID, 1
	policyFacility.ID, policyFacility.Version = facilitySnapshotID, 1
	cardID := ""
	if selectedShortfall.CardID != nil {
		cardID = *selectedShortfall.CardID
	}
	policyShortfall := policy.Shortfall{ID: selected.ShortfallID, CustomerID: selected.CustomerID,
		AccountID: selected.AccountID, CardID: cardID, Outcome: selectedShortfall.Outcome,
		Currency: stringValue(selectedShortfall.Currency), AttemptedAmountCents: *selectedShortfall.AttemptedAmountCents,
		AvailableBalanceCents: *selectedShortfall.AvailableBalanceCents, ShortfallCents: *selectedShortfall.ShortfallCents,
		CanonicalEventID: selected.CanonicalEventID, TransactionID: selected.TransactionID,
		BalanceObservedAt: timeValue(selectedShortfall.BalanceObservedAt)}
	result := policy.Evaluate(policy.Input{EvaluatedAt: manifest.RunAnchor.UTC(), Mode: config.ModeDemo,
		FacilityID: fixtureFacilityID, TermDays: selected.TermDays, Shortfall: policyShortfall,
		Customer: policyCustomer, Facility: policyFacility}, policyConfig)
	if result.PolicyHash != policyConfig.PolicyHash() || string(result.Status) != selected.ExpectedStatus ||
		result.PrincipalCents == nil || *result.PrincipalCents != selected.PrincipalCents ||
		result.EstimatedFeeCents == nil || *result.EstimatedFeeCents != selected.EstimatedFeeCents ||
		result.TotalDueCents == nil || *result.TotalDueCents != selected.TotalDueCents ||
		result.ApprovalRequired != (selected.ExpectedStatus == "REVIEW_REQUIRED") {
		return empty, "", time.Time{}, "", errors.New("pinned policy result differs from the saved-input profile")
	}
	rules := make(map[string]string, len(result.Rules))
	for _, rule := range result.Rules {
		rules[string(rule.RuleID)] = string(rule.Status)
	}
	if len(rules) != len(selected.ExpectedRules) {
		return empty, "", time.Time{}, "", errors.New("pinned policy rule trace differs from the saved-input profile")
	}
	for _, expected := range selected.ExpectedRules {
		if rules[expected.RuleID] != expected.Status {
			return empty, "", time.Time{}, "", errors.New("pinned policy rule trace differs from the saved-input profile")
		}
	}
	receivables, freeBacking, err := externalFundingReceivables(customer.Receivables, selected.CustomerID)
	if err != nil || freeBacking != selected.FreeReceivablesCents {
		return empty, "", time.Time{}, "", errors.New("underwriting receivable facts differ from the saved-input profile")
	}
	profileName := "abundant"
	operatorSubject := "demo:operator-" + uuidCompact(manifest.RunID)[:8] + "-" + profileName
	customerSubject := "demo:customer-" + uuidCompact(manifest.RunID)[:8] + "-" + profileName
	customerID := selected.CustomerID
	authorityID := "demo-authority-" + uuidCompact(manifest.RunID)[:8] + "-" + profileName
	depositOne, err := syntheticID("deposit-demo-", manifest.RunID, profileName+"-1")
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	depositTwo, err := syntheticID("deposit-demo-", manifest.RunID, profileName+"-2")
	if err != nil {
		return empty, "", time.Time{}, "", err
	}
	customerPtr := customerID
	authorityEvidence := "synthetic-mandate-" + uuidCompact(manifest.RunID)[:12]
	if selected.TotalDueCents > math.MaxInt64-10_000 {
		return empty, "", time.Time{}, "", errors.New("underwriting scenario amount overflows authority limit")
	}
	authority := domain.AuthorityImport{ID: authorityID, Version: 1, CustomerID: customerID,
		SourceAccountRef: selected.AccountID, ProductCode: "CONNECTED_ACCOUNT_TRANSFERS",
		MandateReference: authorityEvidence, Currency: "USD", PermittedKinds: []string{"COLLECT_FEE", "COLLECT_PRINCIPAL"},
		PermittedPurposes: []string{"professional_business_services", "loan_credit_repayment"},
		MaximumDebitCents: selected.TotalDueCents + 10_000, ValidFrom: manifest.RunAnchor.Add(-time.Minute).UTC(),
		ExpiresAt: manifest.RunAnchor.Add(365 * 24 * time.Hour).UTC(), Status: "ACTIVE",
		DisclosuresVersion: "synthetic-demo-v1", EvidenceRefs: []domain.SourceRef{{System: "synthetic-demo",
			RecordID: authorityEvidence, Version: manifest.RunID, EffectiveAt: manifest.RunAnchor.UTC()}}}
	profile := upstreamProfileRef{Name: profileName, UnderwritingManifest: filepath.ToSlash(filepath.Join(".dev", "external")),
		CustomerID: customerID, AccountID: selected.AccountID, CardID: selected.CardID,
		CustomerSubject: customerSubject, OperatorSubject: operatorSubject, ShortfallID: selected.ShortfallID,
		TransactionID: selected.TransactionID, CanonicalEventID: selected.CanonicalEventID,
		ExpectedStatus: selected.ExpectedStatus, ApprovalRequired: result.ApprovalRequired,
		PrincipalCents: selected.PrincipalCents, TermDays: selected.TermDays,
		ContractFeeCents: selected.EstimatedFeeCents, TotalDueCents: selected.TotalDueCents,
		AttemptedCents:     *selectedShortfall.AttemptedAmountCents,
		AvailableCents:     *selectedShortfall.AvailableBalanceCents,
		ShortfallCents:     *selectedShortfall.ShortfallCents,
		ProviderDepositIDs: []string{depositOne, depositTwo}}
	rootRelativeManifest, err := filepath.Rel(repoRoot, absPath)
	if err != nil || strings.HasPrefix(rootRelativeManifest, ".."+string(filepath.Separator)) {
		return empty, "", time.Time{}, "", errors.New("underwriting manifest is outside repository")
	}
	profile.UnderwritingManifest = filepath.ToSlash(rootRelativeManifest)
	actor := domain.ActorImport{ActorID: "customer-" + uuidCompact(manifest.RunID)[:8] + "-" + profileName,
		ActorType: "CUSTOMER", AuthenticationSubject: customerSubject, CustomerID: &customerPtr, Active: true,
		SourceRef: domain.SourceRef{System: "synthetic-demo", RecordID: "actor-" + profileName,
			Version: manifest.RunID, EffectiveAt: manifest.RunAnchor.UTC()}}
	operator := domain.ActorImport{ActorID: "operator-" + uuidCompact(manifest.RunID)[:8] + "-" + profileName,
		ActorType: "OPERATOR", AuthenticationSubject: operatorSubject, Active: true,
		SourceRef: domain.SourceRef{System: "synthetic-demo", RecordID: "operator-" + profileName,
			Version: manifest.RunID, EffectiveAt: manifest.RunAnchor.UTC()}}
	return profileBuild{profile: profile, fundingCustomer: domain.FacilityCustomerImport{
			CustomerID: customerID, AccountID: selected.AccountID, CardID: selected.CardID,
			HistoryComplete: customer.Coverage["bridge_history"] && customer.Coverage["lifetime_defaults"] && customer.Coverage["reservations"]},
			receivables: receivables, authority: authority, actor: actor, operator: operator},
		manifest.RunID, manifest.RunAnchor.UTC(), manifest.DatabaseName, nil
}

func externalFundingReceivables(values []map[string]any, customerID string) ([]domain.ReceivableImport, int64, error) {
	result := make([]domain.ReceivableImport, 0, len(values))
	var total int64
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, 0, errors.New("underwriting receivable conversion failed")
		}
		var invoice externalReceivable
		if decodeStrict(data, &invoice) != nil || invoice.InvoiceID == "" || invoice.PayerID == "" ||
			invoice.Currency != "USD" || invoice.EligibleUnpaidCents <= 0 || invoice.AllocatedCents < 0 ||
			invoice.AllocatedCents > invoice.EligibleUnpaidCents || invoice.DueOn == nil || invoice.DueDateVerified == nil ||
			!(*invoice.DueDateVerified) || invoice.AllocationComplete == nil || !(*invoice.AllocationComplete) ||
			invoice.SourceRef.System == "" || invoice.SourceRef.RecordID == "" ||
			invoice.SourceRef.Version == "" || invoice.SourceRef.EffectiveAt.IsZero() {
			return nil, 0, errors.New("underwriting receivable facts are incomplete or unverified")
		}
		if _, err := time.Parse("2006-01-02", *invoice.DueOn); err != nil {
			return nil, 0, errors.New("underwriting receivable date is invalid")
		}
		available := invoice.EligibleUnpaidCents - invoice.AllocatedCents
		if available <= 0 {
			continue
		}
		if _, ok := seen[invoice.SourceRef.System+"\x00"+invoice.InvoiceID]; ok || total > math.MaxInt64-available {
			return nil, 0, errors.New("underwriting receivable identities or amounts are invalid")
		}
		seen[invoice.SourceRef.System+"\x00"+invoice.InvoiceID] = struct{}{}
		dueOn := *invoice.DueOn
		result = append(result, domain.ReceivableImport{CustomerID: customerID,
			SourceSystem: invoice.SourceRef.System, InvoiceID: invoice.InvoiceID, PayerID: invoice.PayerID,
			Currency: invoice.Currency, EligibleUnpaidCents: available, DueOn: &dueOn,
			DueDateVerified: *invoice.DueDateVerified, AllocationComplete: *invoice.AllocationComplete,
			SourceComplete: true, SourceRef: domain.SourceRef{System: invoice.SourceRef.System,
				RecordID: invoice.SourceRef.RecordID, Version: invoice.SourceRef.Version,
				EffectiveAt: invoice.SourceRef.EffectiveAt.UTC()}})
		total += available
	}
	if len(result) == 0 || total <= 0 {
		return nil, 0, errors.New("underwriting profile has no available verified receivable")
	}
	return result, total, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

func freshSourcePointer(value *time.Time, anchor time.Time) bool {
	if value == nil {
		return false
	}
	return freshSourceInstant(*value, anchor)
}

func freshSourceInstant(value, anchor time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	if offset != 0 {
		return false
	}
	age := anchor.UTC().Sub(value.UTC())
	return age >= -5*time.Second && age <= 5*time.Minute
}
