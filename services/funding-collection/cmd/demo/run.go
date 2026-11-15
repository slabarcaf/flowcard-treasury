package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

const (
	demoJourneyTimeout = 3 * time.Minute
	demoStageTimeout   = 90 * time.Second
	demoPollInterval   = time.Second
	demoAPIBodyLimit   = 1_000_000
	demoFirstDeposit   = int64(10_000)
)

type fundingDemoAPI struct {
	base   string
	client *http.Client
}

type fundingDemoOffer struct {
	ID                string  `json:"id"`
	Version           int64   `json:"version"`
	TermsHash         string  `json:"terms_hash"`
	PrincipalCents    int64   `json:"principal_cents"`
	ContractFeeCents  int64   `json:"contract_fee_cents"`
	TermDays          int     `json:"term_days"`
	State             string  `json:"state"`
	OriginatingCaseID *string `json:"originating_case_id"`
	EvaluationID      *string `json:"evaluation_id"`
}

type fundingDemoReceipt struct {
	BridgeID    string `json:"bridge_id"`
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
}

type fundingDemoOperation struct {
	ID                  string  `json:"id"`
	Kind                string  `json:"kind"`
	State               string  `json:"state"`
	SettlementReference *string `json:"last_verified_settlement_reference"`
}

type fundingDemoBridge struct {
	ID                   string                 `json:"id"`
	Version              int64                  `json:"version"`
	Lifecycle            string                 `json:"lifecycle"`
	PrincipalOwedCents   *int64                 `json:"principal_owed_cents"`
	ContractFeeOwedCents *int64                 `json:"contract_fee_owed_cents"`
	LateFeeOwedCents     *int64                 `json:"late_fee_owed_cents"`
	HoldReasons          []string               `json:"hold_reasons"`
	Operations           []fundingDemoOperation `json:"operations"`
}

type fundingDemoCollectionPage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type fundingDemoChangePage struct {
	Mode         string              `json:"mode"`
	Items        []fundingDemoChange `json:"items"`
	NextAfterSeq uint64              `json:"next_after_seq"`
	HasMore      bool                `json:"has_more"`
}

type fundingDemoChange struct {
	Sequence          uint64  `json:"sequence"`
	ResourceType      string  `json:"resource_type"`
	ResourceID        string  `json:"resource_id"`
	ResourceVersion   int64   `json:"resource_version"`
	OriginatingCaseID *string `json:"originating_case_id"`
	EvaluationID      *string `json:"evaluation_id"`
	OfferID           *string `json:"offer_id"`
	BridgeID          *string `json:"bridge_id"`
	BridgeVersion     *int64  `json:"bridge_version"`
}

type fundingDemoCollection struct {
	ID                string                  `json:"id"`
	OperationID       string                  `json:"operation_id"`
	DepositID         string                  `json:"deposit_id"`
	State             string                  `json:"state"`
	CreatedAt         time.Time               `json:"created_at"`
	SettledAt         *time.Time              `json:"settled_at"`
	SettledDebitCents int64                   `json:"settled_debit_cents"`
	Allocations       []fundingDemoAllocation `json:"allocations"`
}

type fundingDemoAllocation struct {
	Bucket      string `json:"bucket"`
	AmountCents int64  `json:"amount_cents"`
}

type fundingDemoServiceState struct {
	Mode              string                    `json:"mode"`
	SourceBindingID   string                    `json:"source_binding_id"`
	LedgerVersion     int64                     `json:"ledger_version"`
	FundingAuthorized bool                      `json:"funding_authorized"`
	Customer          fundingDemoCustomerState  `json:"customer"`
	Facility          fundingDemoFacilityState  `json:"facility"`
	Invoices          []fundingDemoInvoiceState `json:"invoices"`
	History           []fundingDemoHistory      `json:"history"`
}

type fundingDemoCustomerState struct {
	CustomerID         string `json:"customer_id"`
	PrincipalOwedCents int64  `json:"principal_owed_cents"`
	UnpaidFeeCents     int64  `json:"unpaid_fee_cents"`
	ReservedPrincipal  int64  `json:"reserved_principal_cents"`
	ReservedFees       int64  `json:"reserved_fee_cents"`
	ActiveHold         bool   `json:"active_hold"`
}

type fundingDemoFacilityState struct {
	FacilityID        string `json:"facility_id"`
	ReserveBPS        int    `json:"reserve_bps"`
	PrincipalOwed     int64  `json:"principal_owed_cents"`
	ReservedPrincipal int64  `json:"reserved_principal_cents"`
	OtherCommitments  int64  `json:"other_commitments_cents"`
}

type fundingDemoInvoiceState struct {
	ReservedCents int64 `json:"reserved_cents"`
}

type fundingDemoHistory struct {
	BridgeID                 string     `json:"bridge_id"`
	EvaluationID             string     `json:"evaluation_id"`
	OfferID                  string     `json:"offer_id"`
	Lifecycle                string     `json:"lifecycle"`
	PrincipalOriginalCents   int64      `json:"principal_original_cents"`
	ContractFeeOriginalCents int64      `json:"contract_fee_original_cents"`
	PrincipalOwedCents       int64      `json:"principal_owed_cents"`
	ContractFeeOwedCents     int64      `json:"contract_fee_owed_cents"`
	LateFeeOwedCents         int64      `json:"late_fee_owed_cents"`
	PrincipalPaidCents       int64      `json:"principal_paid_cents"`
	ContractFeePaidCents     int64      `json:"contract_fee_paid_cents"`
	ClosedAt                 *time.Time `json:"closed_at"`
}

type fundingDemoRunSummary struct {
	SourceMode                 string `json:"source_mode"`
	FixtureLabel               string `json:"fixture_label"`
	UpstreamInputKind          string `json:"upstream_input_kind"`
	ProviderKind               string `json:"provider_kind"`
	RunID                      string `json:"run_id"`
	Profile                    string `json:"profile"`
	EvaluationID               string `json:"evaluation_id"`
	OriginatingCaseID          string `json:"originating_case_id,omitempty"`
	OfferID                    string `json:"offer_id"`
	BridgeID                   string `json:"bridge_id,omitempty"`
	PrincipalCents             int64  `json:"principal_cents"`
	ContractFeeCents           int64  `json:"contract_fee_cents"`
	TermDays                   int    `json:"term_days"`
	FundingOperationID         string `json:"funding_operation_id,omitempty"`
	FundingState               string `json:"funding_state"`
	CollectionCount            int    `json:"collection_count"`
	CollectionReplyRecovered   bool   `json:"collection_reply_recovered"`
	FinalLifecycle             string `json:"final_lifecycle,omitempty"`
	ProviderAcceptanceVerified bool   `json:"provider_acceptance_verified"`
	ScarceExecutionSupported   bool   `json:"scarce_execution_supported"`
	ProviderCreateDelta        int    `json:"provider_create_delta"`
	FeedLastSequence           uint64 `json:"feed_last_sequence"`
	CoordinatorReferencesValid bool   `json:"coordinator_references_valid"`
	BorrowerBalanceCents       int64  `json:"borrower_balance_cents,omitempty"`
	FundingAccountBalanceCents int64  `json:"funding_account_balance_cents,omitempty"`
}

func runJourney(parent context.Context, args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "prepared DEMO manifest path")
	apiURL := flags.String("api-url", "", "loopback funding API URL")
	profileName := flags.String("profile", "abundant", "abundant or tight")
	evaluationID := flags.String("evaluation-id", "", "optional saved Underwriting evaluation ID")
	originatingCaseID := flags.String("originating-case-id", "", "optional real coordinator case UUID")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *manifestPath == "" || *apiURL == "" {
		return errors.New("usage: demo run --manifest PATH --api-url URL [--profile abundant|tight] [--evaluation-id ID] [--originating-case-id UUID]")
	}
	if *profileName != "abundant" && *profileName != "tight" {
		return errors.New("demo profile must be abundant or tight")
	}
	if *originatingCaseID != "" && !demoUUID(*originatingCaseID) {
		return errors.New("originating coordinator case ID is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, demoJourneyTimeout)
	defer cancel()
	return executeJourney(ctx, *manifestPath, *apiURL, *profileName, *evaluationID, *originatingCaseID)
}

func executeJourney(ctx context.Context, manifestPath, apiURL, profileName, evaluationID, originatingCaseID string) error {
	manifest, statePath, err := loadFundingManifest(manifestPath)
	if err != nil {
		return err
	}
	if manifest.SourceMode != config.ModeDemo || manifest.FacilityID != fixtureFacilityID ||
		manifest.SourceBindingID != fixtureSourceBindingID || manifest.FundingOpeningCents <= 0 {
		return errors.New("demo journey manifest binding invalid")
	}
	now := time.Now().UTC()
	if manifest.RunAnchor.After(now.Add(5*time.Second)) || now.Sub(manifest.RunAnchor) > 4*time.Minute {
		return errors.New("prepared DEMO run expired; prepare a fresh run")
	}
	cfg, err := config.Load()
	if err != nil || cfg.Mode != config.ModeDemo || cfg.FacilityID != manifest.FacilityID ||
		cfg.SourceBindingID != manifest.SourceBindingID || cfg.ProviderMode != config.ProviderMock ||
		cfg.ProviderBaseURL != "http://"+manifest.ProviderAddress ||
		cfg.UnderwritingURL != "http://"+underwritingAddress || cfg.ShortfallURL != "http://"+shortfallAddress ||
		cfg.LiveDispatchEnabled {
		return errors.New("funding DEMO runtime configuration does not match manifest")
	}
	policyConfig, err := policy.LoadConfig()
	if err != nil || policyConfig.PolicyID() != manifest.PolicyID || policyConfig.PolicyHash() != manifest.PolicyHash {
		return errors.New("prepared policy binding does not match runtime policy")
	}
	apiEndpoint, err := validateFundingDemoAPIURL(apiURL, manifest.APIAddress)
	if err != nil {
		return err
	}
	api := &fundingDemoAPI{base: apiEndpoint, client: &http.Client{Timeout: 8 * time.Second,
		Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}}
	if err = verifyFundingDemoReadiness(ctx, api, manifest.FundingDatabaseName, manifest.PolicyID, manifest.PolicyHash); err != nil {
		return err
	}
	var profile *upstreamProfileRef
	for i := range manifest.UpstreamProfiles {
		if manifest.UpstreamProfiles[i].Name == profileName {
			profile = &manifest.UpstreamProfiles[i]
			break
		}
	}
	if profile == nil || profile.CustomerID == "" || profile.AccountID == "" || profile.ShortfallID == "" ||
		profile.TransactionID == "" || profile.CustomerSubject == "" || profile.OperatorSubject == "" ||
		profile.TermDays < 7 || profile.TermDays > 90 || profile.ShortfallCents <= 0 ||
		profile.PrincipalCents <= 0 || profile.ContractFeeCents <= 0 || profile.TotalDueCents != profile.PrincipalCents+profile.ContractFeeCents {
		return errors.New("prepared DEMO profile is incomplete")
	}
	underwritingClient, err := upstream.NewUnderwritingClientWithClock(cfg.UnderwritingURL, cfg.UnderwritingSecret, time.Now)
	if err != nil {
		return errors.New("underwriting reader configuration unavailable")
	}
	var assessment upstream.SavedEvaluation
	if evaluationID == "" {
		assessment, err = underwritingClient.FindEvaluation(ctx, profile.CustomerID, profile.ShortfallID, profile.TermDays, config.ModeDemo)
	} else {
		assessment, err = underwritingClient.ReadEvaluation(ctx, evaluationID, config.ModeDemo)
	}
	if err != nil || !evaluationMatchesProfile(assessment, manifest, *profile) {
		return errors.New("no unique fresh saved evaluation matches the prepared DEMO profile")
	}
	if !freshSavedEvaluation(assessment, time.Now().UTC()) {
		return errors.New("saved Underwriting evaluation is stale; prepare and evaluate a fresh run")
	}
	if assessment.Status != profile.ExpectedStatus {
		return errors.New("saved evaluation status differs from prepared DEMO profile")
	}
	initialState, err := readFundingDemoServiceState(ctx, api, cfg, profile.CustomerID)
	if err != nil {
		return err
	}
	expectedReserveBPS := 2000
	if profileName == "tight" {
		expectedReserveBPS = 8000
	}
	if initialState.Mode != config.ModeDemo || initialState.SourceBindingID != manifest.SourceBindingID || initialState.FundingAuthorized ||
		initialState.Customer.CustomerID != profile.CustomerID || initialState.Facility.FacilityID != manifest.FacilityID ||
		initialState.Facility.ReserveBPS != expectedReserveBPS ||
		!knownEmptyOrSameEvaluationState(initialState, assessment.ID) {
		return errors.New("funding database is neither known-empty nor a matching resumable DEMO run")
	}
	feedAfter := initialState.LedgerVersion
	caseRef := (*string)(nil)
	if originatingCaseID != "" {
		caseRef = &originatingCaseID
	}
	createRequest := map[string]any{"evaluation_id": assessment.ID, "originating_case_id": caseRef}
	createBody, err := api.request(ctx, http.MethodPost, "/api/offers", "funding-coordinator", cfg.CoordinatorSecret,
		demoIdempotencyKey(manifest.RunID, profileName, "offer"), createRequest)
	if err != nil || (createBody.status != http.StatusCreated && createBody.status != http.StatusOK) {
		return errors.New("funding API did not create or replay the bound offer")
	}
	var created fundingDemoOffer
	if json.Unmarshal(createBody.body, &created) != nil || created.ID == "" || created.TermsHash == "" ||
		created.PrincipalCents != *assessment.PrincipalCents || created.ContractFeeCents != *assessment.EstimatedFeeCents ||
		created.TermDays != *assessment.TermDays || (created.EvaluationID != nil && *created.EvaluationID != assessment.ID) {
		return errors.New("funding offer differs from the saved Underwriting assessment")
	}
	if caseRef != nil && (created.OriginatingCaseID == nil || *created.OriginatingCaseID != *caseRef) {
		return errors.New("funding offer did not retain the host case reference")
	}
	if len(initialState.History) > 0 && initialState.History[0].OfferID != created.ID {
		return errors.New("funding history belongs to another offer")
	}
	offer, err := ensureFundingDemoDecisions(ctx, api, cfg, manifest.RunID, created, *profile, *assessment.ApprovalRequired)
	if err != nil {
		return err
	}
	if offer.State != "READY" && offer.State != "CONSUMED" &&
		!(offer.State == "AWAITING_APPROVAL" && !*assessment.ApprovalRequired) {
		return errors.New("funding offer did not reach an authorized decision state")
	}
	providerStateBefore, err := readPreparedProviderState(statePath, manifest)
	if err != nil {
		return err
	}
	providerCreateCountBefore := len(providerStateBefore.Transfers)
	fundVersion := offer.Version
	if offer.State == "CONSUMED" {
		fundVersion--
	}
	if fundVersion < 1 {
		return errors.New("funding offer version is invalid")
	}
	fundBody, err := json.Marshal(map[string]int64{"expected_version": fundVersion})
	if err != nil {
		return errors.New("funding command encoding failed")
	}
	fundResult, err := api.requestRaw(ctx, http.MethodPost, "/api/offers/"+url.PathEscape(offer.ID)+"/fund",
		"funding-coordinator", cfg.CoordinatorSecret, demoIdempotencyKey(manifest.RunID, profileName, "fund"), fundBody)
	if err != nil {
		return errors.New("funding API request failed")
	}
	if profileName == "tight" {
		code := apiErrorCode(fundResult.body)
		fundReplay, replayErr := api.requestRaw(ctx, http.MethodPost, "/api/offers/"+url.PathEscape(offer.ID)+"/fund",
			"funding-coordinator", cfg.CoordinatorSecret, demoIdempotencyKey(manifest.RunID, profileName, "fund"), fundBody)
		providerStateAfter, readErr := readPreparedProviderState(statePath, manifest)
		stateAfter, stateErr := readFundingDemoServiceState(ctx, api, cfg, profile.CustomerID)
		if fundResult.status != http.StatusUnprocessableEntity || code != "SCARCE_CAPITAL_RANKING_UNSUPPORTED" ||
			replayErr != nil || fundReplay.status != http.StatusUnprocessableEntity ||
			apiErrorCode(fundReplay.body) != code || !bytes.Equal(fundReplay.body, fundResult.body) ||
			readErr != nil || len(providerStateAfter.Transfers) != providerCreateCountBefore || stateErr != nil ||
			!knownEmptyDemoOpeningState(stateAfter) {
			return errors.New("tight profile did not block scarce funding before provider create")
		}
		changes, lastSequence, feedErr := readFundingDemoChanges(ctx, api, cfg, feedAfter)
		if feedErr != nil || !validDemoChangeReferences(changes, []string{offer.ID}, caseRef, assessment.ID, offer.ID, nil) {
			return errors.New("coordinator change feed did not retain the TIGHT offer references")
		}
		return writeFundingDemoSummary(fundingDemoRunSummary{SourceMode: config.ModeDemo,
			FixtureLabel: demoRunFixtureLabel(manifest), UpstreamInputKind: manifest.UpstreamInputKind,
			ProviderKind: "SYNTHETIC_LOCAL_PROVIDER_DOUBLE", RunID: manifest.RunID, Profile: profileName,
			EvaluationID: assessment.ID, OriginatingCaseID: originatingCaseID, OfferID: offer.ID,
			PrincipalCents: *assessment.PrincipalCents, ContractFeeCents: *assessment.EstimatedFeeCents,
			TermDays: *assessment.TermDays, FundingState: code, ProviderAcceptanceVerified: false,
			ScarceExecutionSupported: false, ProviderCreateDelta: 0, FeedLastSequence: lastSequence,
			CoordinatorReferencesValid: true})
	}
	if fundResult.status != http.StatusAccepted && fundResult.status != http.StatusOK {
		return errors.New("abundant funding command was blocked; inspect its saved 422 receipt")
	}
	var receipt fundingDemoReceipt
	if json.Unmarshal(fundResult.body, &receipt) != nil || receipt.BridgeID == "" || receipt.OperationID == "" ||
		(receipt.Status != "RESERVED" && receipt.Status != "QUEUED") {
		return errors.New("funding command receipt is invalid")
	}
	if err = waitForDemoOperation(ctx, api, cfg, receipt.OperationID, "FUND"); err != nil {
		return err
	}
	bridge, err := readFundingDemoBridge(ctx, api, cfg, receipt.BridgeID)
	if err != nil || (bridge.Lifecycle != "ACTIVE" && bridge.Lifecycle != "REPAID") ||
		int64Value(bridge.PrincipalOwedCents) != *assessment.PrincipalCents ||
		int64Value(bridge.ContractFeeOwedCents) != *assessment.EstimatedFeeCents || int64Value(bridge.LateFeeOwedCents) != 0 {
		return errors.New("confirmed funding did not produce the expected settled bridge debt")
	}
	providerAdapter, err := provider.NewFromConfig(cfg, manifest.FundingAccountRef, "", nil, time.Now)
	if err != nil {
		return errors.New("provider reader configuration unavailable")
	}
	if len(profile.ProviderDepositIDs) != 2 {
		return errors.New("abundant profile requires two synthetic provider deposits")
	}
	firstDepositCents := min(demoFirstDeposit, *assessment.EstimatedFeeCents)
	if assessment.TotalDueCents == nil || *assessment.TotalDueCents <= firstDepositCents {
		return errors.New("saved assessment total cannot be represented by the deposit fixture")
	}
	secondDepositCents := *assessment.TotalDueCents - firstDepositCents
	if firstDepositCents <= 0 || secondDepositCents <= 0 {
		return errors.New("saved evaluation cannot be represented by the two-deposit fixture")
	}
	if err = configureSyntheticDeposit(ctx, api, cfg, manifest, statePath, profile.ProviderDepositIDs[0], firstDepositCents); err != nil {
		return err
	}
	if err = configureSyntheticDeposit(ctx, api, cfg, manifest, statePath, profile.ProviderDepositIDs[1], secondDepositCents); err != nil {
		return err
	}
	if err = settleSyntheticDeposit(ctx, api, cfg, manifest, statePath, profile.ProviderDepositIDs[0]); err != nil {
		return err
	}
	if _, err = readVerifiedSyntheticDeposit(ctx, providerAdapter, manifest, statePath,
		*profile, profile.ProviderDepositIDs[0], firstDepositCents); err != nil {
		return err
	}
	if err = api.sendDemoDepositWake(ctx, cfg.AirwallexWebhookSecret, *profile, profile.ProviderDepositIDs[0]); err != nil {
		return err
	}
	firstPage, err := waitForDemoCollectionCount(ctx, api, cfg, receipt.BridgeID, 1)
	if err != nil {
		return err
	}
	firstItems, err := settledCollections(firstPage)
	if err != nil {
		return errors.New("first synthetic deposit collection is not settled")
	}
	firstInternalDepositID, ok := oneInternalDepositID(firstItems)
	if !ok || !validFirstDepositCollection(firstItems, firstInternalDepositID, firstDepositCents) {
		return errors.New("first synthetic deposit did not settle against the contracted fee")
	}
	if err = settleSyntheticDeposit(ctx, api, cfg, manifest, statePath, profile.ProviderDepositIDs[1]); err != nil {
		return err
	}
	if _, err = readVerifiedSyntheticDeposit(ctx, providerAdapter, manifest, statePath,
		*profile, profile.ProviderDepositIDs[1], secondDepositCents); err != nil {
		return err
	}
	if err = api.sendDemoDepositWake(ctx, cfg.AirwallexWebhookSecret, *profile, profile.ProviderDepositIDs[1]); err != nil {
		return err
	}
	expectedCollectionCount := 2
	if *assessment.EstimatedFeeCents > firstDepositCents {
		expectedCollectionCount++
	}
	page, err := waitForDemoCollectionCount(ctx, api, cfg, receipt.BridgeID, expectedCollectionCount)
	if err != nil {
		return err
	}
	items, err := settledCollections(page)
	if err != nil {
		return errors.New("provider deposit collection group is not settled")
	}
	secondInternalDepositID, ok := oneDistinctInternalDepositID(items, firstInternalDepositID)
	if !ok || !validDemoCollectionSequence(items, []string{firstInternalDepositID, secondInternalDepositID}, firstDepositCents,
		secondDepositCents, *assessment.PrincipalCents, *assessment.EstimatedFeeCents) {
		return errors.New("synthetic deposits did not settle in fee-first principal order")
	}
	bridge, err = waitForDemoBridgeClosure(ctx, api, cfg, receipt.BridgeID)
	if err != nil || bridge.Lifecycle != "REPAID" || int64Value(bridge.PrincipalOwedCents) != 0 ||
		int64Value(bridge.ContractFeeOwedCents) != 0 || int64Value(bridge.LateFeeOwedCents) != 0 ||
		len(bridge.HoldReasons) != 0 {
		return errors.New("bridge did not close with zero debt and holds")
	}
	if len(bridge.Operations) != len(items)+1 {
		return errors.New("closed bridge operation history is incomplete")
	}
	for _, operation := range bridge.Operations {
		if operation.State != "SETTLED" {
			return errors.New("closed bridge contains a nonsettled operation")
		}
	}
	if err = verifyFundingDemoReadModels(ctx, api, cfg, profile.CustomerID, receipt.BridgeID,
		*assessment.PrincipalCents, *assessment.EstimatedFeeCents); err != nil {
		return err
	}
	feedResourceIDs := []string{offer.ID, receipt.BridgeID, receipt.OperationID}
	for _, item := range items {
		feedResourceIDs = append(feedResourceIDs, item.OperationID)
	}
	changes, lastSequence, err := readFundingDemoChanges(ctx, api, cfg, feedAfter)
	if err != nil || !validDemoChangeReferences(changes, feedResourceIDs, caseRef, assessment.ID, offer.ID, &receipt.BridgeID) {
		return errors.New("coordinator feed does not contain the exact funding journey references")
	}
	balanceCtx, cancelBalance := context.WithTimeout(ctx, 12*time.Second)
	borrowerBalance, borrowerErr := providerAdapter.Balance(balanceCtx, profile.AccountID)
	fundingBalance, fundingErr := providerAdapter.Balance(balanceCtx, manifest.FundingAccountRef)
	cancelBalance()
	if *assessment.PrincipalCents > math.MaxInt64-profile.AvailableCents ||
		*assessment.PrincipalCents > manifest.FundingOpeningCents ||
		manifest.FundingOpeningCents-*assessment.PrincipalCents > math.MaxInt64-*assessment.TotalDueCents {
		return errors.New("saved assessment exceeds synthetic provider balance bounds")
	}
	expectedBorrowerBalance := profile.AvailableCents + *assessment.PrincipalCents
	expectedFundingBalance := manifest.FundingOpeningCents - *assessment.PrincipalCents + *assessment.TotalDueCents
	if borrowerErr != nil || fundingErr != nil || borrowerBalance.AvailableCents != expectedBorrowerBalance ||
		fundingBalance.AvailableCents != expectedFundingBalance {
		return errors.New("final provider balances do not preserve borrower funds and collection totals")
	}
	providerStateAfter, err := readPreparedProviderState(statePath, manifest)
	var firstFeeOperationID string
	for _, item := range items {
		if item.DepositID == firstInternalDepositID {
			firstFeeOperationID = item.OperationID
		}
	}
	if err != nil || !verifyDemoProviderTransferEvidence(providerStateAfter, manifest, *profile, receipt.BridgeID,
		receipt.OperationID, firstFeeOperationID, items, expectedCollectionCount,
		firstDepositCents, *assessment.PrincipalCents) {
		return errors.New("dropped fee-create receipt was not recovered through final provider reads")
	}
	recovered := true
	return writeFundingDemoSummary(fundingDemoRunSummary{SourceMode: config.ModeDemo,
		FixtureLabel: demoRunFixtureLabel(manifest), UpstreamInputKind: manifest.UpstreamInputKind,
		ProviderKind: "SYNTHETIC_LOCAL_PROVIDER_DOUBLE", RunID: manifest.RunID, Profile: profileName,
		EvaluationID: assessment.ID, OriginatingCaseID: originatingCaseID, OfferID: offer.ID,
		BridgeID: receipt.BridgeID, PrincipalCents: *assessment.PrincipalCents,
		ContractFeeCents: *assessment.EstimatedFeeCents, TermDays: *assessment.TermDays,
		FundingOperationID: receipt.OperationID, FundingState: "SETTLED", CollectionCount: len(items),
		CollectionReplyRecovered: recovered, FinalLifecycle: bridge.Lifecycle,
		ProviderAcceptanceVerified: false, ScarceExecutionSupported: false,
		ProviderCreateDelta: len(providerStateAfter.Transfers) - providerCreateCountBefore,
		FeedLastSequence:    lastSequence, CoordinatorReferencesValid: true,
		BorrowerBalanceCents: borrowerBalance.AvailableCents, FundingAccountBalanceCents: fundingBalance.AvailableCents})
}

type fundingDemoAPIResult struct {
	status int
	body   []byte
}

func (a *fundingDemoAPI) request(ctx context.Context, method, path, subject, secret, key string, body any) (fundingDemoAPIResult, error) {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return fundingDemoAPIResult{}, errors.New("demo API request encoding failed")
		}
	}
	return a.requestRaw(ctx, method, path, subject, secret, key, raw)
}

func (a *fundingDemoAPI) requestRaw(ctx context.Context, method, path, subject, secret, key string, body []byte) (fundingDemoAPIResult, error) {
	requestURL := a.base + path
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return fundingDemoAPIResult{}, errors.New("demo API request invalid")
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if subject != "" {
		token, tokenErr := signFundingDemoAssertion(secret, subject)
		if tokenErr != nil {
			return fundingDemoAPIResult{}, tokenErr
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response, err := a.client.Do(req)
	if err != nil {
		return fundingDemoAPIResult{}, errors.New("funding API unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, demoAPIBodyLimit+1))
	if err != nil || len(data) > demoAPIBodyLimit {
		return fundingDemoAPIResult{}, errors.New("funding API response invalid")
	}
	return fundingDemoAPIResult{status: response.StatusCode, body: data}, nil
}

func (a *fundingDemoAPI) sendDemoDepositWake(ctx context.Context, secret string,
	profile upstreamProfileRef, providerDepositID string) error {
	if len(secret) < 32 || profile.CustomerID == "" || profile.CardID == "" || providerDepositID == "" {
		return errors.New("signed DEMO deposit wake configuration invalid")
	}
	event := struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Data struct {
			CustomerID string `json:"customer_id"`
			CardID     string `json:"card_id"`
			DepositID  string `json:"deposit_id"`
		} `json:"data"`
	}{ID: "demo-deposit-settled:" + providerDepositID, Name: "DEMO_DEPOSIT_SETTLED"}
	event.Data.CustomerID, event.Data.CardID, event.Data.DepositID = profile.CustomerID, profile.CardID, providerDepositID
	body, err := json.Marshal(event)
	if err != nil {
		return errors.New("signed DEMO deposit wake encoding failed")
	}
	timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err = mac.Write([]byte(timestamp)); err != nil {
		return errors.New("signed DEMO deposit wake signing failed")
	}
	if _, err = mac.Write(body); err != nil {
		return errors.New("signed DEMO deposit wake signing failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.base+"/webhooks/airwallex/servicing", bytes.NewReader(body))
	if err != nil {
		return errors.New("signed DEMO deposit wake request invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := a.client.Do(req)
	if err != nil {
		return errors.New("signed DEMO deposit wake unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16_385))
	if err != nil || len(data) > 16_384 || response.StatusCode != http.StatusOK {
		return errors.New("signed DEMO deposit wake was not durably accepted")
	}
	return nil
}

func signFundingDemoAssertion(secret, subject string) (string, error) {
	if len(secret) < 32 || subject == "" {
		return "", errors.New("demo API assertion unavailable")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", errors.New("demo API assertion unavailable")
	}
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", errors.New("demo API assertion unavailable")
	}
	now := time.Now().UTC()
	claims, err := json.Marshal(map[string]any{"iss": "flowcard-go", "aud": "flowcard-funding-collection",
		"sub": subject, "jti": base64.RawURLEncoding.EncodeToString(nonce[:]),
		"iat": now.Unix(), "exp": now.Add(20 * time.Second).Unix()})
	if err != nil {
		return "", errors.New("demo API assertion unavailable")
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(input)); err != nil {
		return "", errors.New("demo API assertion unavailable")
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func validateFundingDemoAPIURL(value, expectedAddress string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Host != expectedAddress || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("funding demo API must use the prepared loopback address")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("funding demo API must use a loopback address")
	}
	return "http://" + expectedAddress, nil
}

func verifyFundingDemoReadiness(ctx context.Context, api *fundingDemoAPI, expectedDatabase, expectedPolicyID, expectedPolicyHash string) error {
	response, err := api.request(ctx, http.MethodGet, "/health/ready", "", "", "", nil)
	if err != nil || response.status != http.StatusOK {
		return errors.New("funding API is not ready")
	}
	var ready struct {
		Mode                       string `json:"mode"`
		RuntimeReady               bool   `json:"runtime_ready"`
		DatabaseName               string `json:"database_name"`
		PolicyID                   string `json:"policy_id"`
		PolicyHash                 string `json:"policy_hash"`
		FixtureOnly                bool   `json:"fixture_only"`
		ProviderAcceptanceVerified bool   `json:"provider_acceptance_verified"`
		FundingEnabled             bool   `json:"funding_enabled"`
		CollectionEnabled          bool   `json:"collection_enabled"`
		LiveDispatchEnabled        bool   `json:"live_dispatch_enabled"`
	}
	if json.Unmarshal(response.body, &ready) != nil || ready.Mode != config.ModeDemo || !ready.RuntimeReady ||
		ready.DatabaseName != expectedDatabase || ready.PolicyID != expectedPolicyID || ready.PolicyHash != expectedPolicyHash ||
		!ready.FixtureOnly || ready.ProviderAcceptanceVerified || !ready.FundingEnabled || !ready.CollectionEnabled || ready.LiveDispatchEnabled {
		return errors.New("funding API readiness does not match the synthetic DEMO profile")
	}
	return nil
}

func evaluationMatchesProfile(evaluation upstream.SavedEvaluation, manifest fundingManifest, profile upstreamProfileRef) bool {
	return evaluation.ID != "" && evaluation.SourceMode == config.ModeDemo && evaluation.PolicyID == manifest.PolicyID &&
		evaluation.PolicyHash == manifest.PolicyHash && evaluation.CustomerID == profile.CustomerID &&
		evaluation.AccountID == profile.AccountID && evaluation.TransactionID == profile.TransactionID &&
		evaluation.ShortfallID == profile.ShortfallID && evaluation.PrincipalCents != nil &&
		*evaluation.PrincipalCents == profile.PrincipalCents && evaluation.TermDays != nil &&
		*evaluation.TermDays == profile.TermDays && evaluation.EstimatedFeeCents != nil &&
		*evaluation.EstimatedFeeCents == profile.ContractFeeCents && evaluation.TotalDueCents != nil &&
		*evaluation.TotalDueCents == profile.TotalDueCents && evaluation.ApprovalRequired != nil &&
		*evaluation.ApprovalRequired == profile.ApprovalRequired && evaluation.FundingAuthorized != nil &&
		!*evaluation.FundingAuthorized && evaluation.RequiresRecheck != nil && *evaluation.RequiresRecheck
}

func freshSavedEvaluation(evaluation upstream.SavedEvaluation, now time.Time) bool {
	if evaluation.CreatedAt.IsZero() || evaluation.EvaluatedAt.IsZero() || evaluation.CreatedAt.After(now.Add(5*time.Second)) ||
		evaluation.EvaluatedAt.After(now.Add(5*time.Second)) {
		return false
	}
	return now.Sub(evaluation.CreatedAt) <= 5*time.Minute && now.Sub(evaluation.EvaluatedAt) <= 5*time.Minute
}

func ensureFundingDemoDecisions(ctx context.Context, api *fundingDemoAPI, cfg config.Config, runID string, created fundingDemoOffer,
	profile upstreamProfileRef, approvalRequired bool) (fundingDemoOffer, error) {
	current, err := readFundingDemoOffer(ctx, api, created.ID, "funding-coordinator", cfg.CoordinatorSecret)
	if err != nil {
		return fundingDemoOffer{}, err
	}
	if current.State == "OFFERED" || current.State == "AWAITING_ACCEPTANCE" {
		customerRead, readErr := readFundingDemoOffer(ctx, api, created.ID, profile.CustomerSubject, cfg.CustomerSecret)
		if readErr != nil || customerRead.TermsHash != current.TermsHash || customerRead.Version != current.Version {
			return fundingDemoOffer{}, errors.New("synthetic customer cannot verify current offer terms")
		}
		body := map[string]any{"terms_hash": customerRead.TermsHash, "expected_version": customerRead.Version}
		response, requestErr := api.request(ctx, http.MethodPost, "/api/offers/"+url.PathEscape(created.ID)+"/acceptances",
			profile.CustomerSubject, cfg.CustomerSecret, demoIdempotencyKey(runID, profile.Name, "accept"), body)
		if requestErr != nil || (response.status != http.StatusCreated && response.status != http.StatusOK) {
			return fundingDemoOffer{}, errors.New("synthetic customer acceptance was not recorded")
		}
		current, err = readFundingDemoOffer(ctx, api, created.ID, "funding-coordinator", cfg.CoordinatorSecret)
		if err != nil {
			return fundingDemoOffer{}, err
		}
	}
	if current.State == "AWAITING_APPROVAL" {
		if !approvalRequired {
			return current, nil
		}
		operatorRead, readErr := readFundingDemoOffer(ctx, api, created.ID, profile.OperatorSubject, cfg.OperatorSecret)
		if readErr != nil || operatorRead.TermsHash != current.TermsHash || operatorRead.Version != current.Version {
			return fundingDemoOffer{}, errors.New("synthetic operator cannot verify current offer terms")
		}
		body := map[string]any{"terms_hash": operatorRead.TermsHash, "expected_version": operatorRead.Version,
			"reason_code": "SYNTHETIC_DEMO_APPROVED_AFTER_REVIEW"}
		response, requestErr := api.request(ctx, http.MethodPost, "/api/offers/"+url.PathEscape(created.ID)+"/approvals",
			profile.OperatorSubject, cfg.OperatorSecret,
			demoIdempotencyKey(runID, profile.Name, "approve"), body)
		if requestErr != nil || (response.status != http.StatusCreated && response.status != http.StatusOK) {
			return fundingDemoOffer{}, errors.New("synthetic operator approval was not recorded")
		}
		current, err = readFundingDemoOffer(ctx, api, created.ID, "funding-coordinator", cfg.CoordinatorSecret)
		if err != nil {
			return fundingDemoOffer{}, err
		}
	}
	if current.State == "READY" && approvalRequired {
		return current, nil
	}
	if current.State == "READY" && !approvalRequired || current.State == "CONSUMED" {
		return current, nil
	}
	return fundingDemoOffer{}, errors.New("offer is not in a decision-complete state")
}

func readFundingDemoOffer(ctx context.Context, api *fundingDemoAPI, id, subject, secret string) (fundingDemoOffer, error) {
	response, err := api.request(ctx, http.MethodGet, "/api/offers/"+url.PathEscape(id), subject, secret, "", nil)
	if err != nil || response.status != http.StatusOK {
		return fundingDemoOffer{}, errors.New("funding offer read failed")
	}
	var offer fundingDemoOffer
	if json.Unmarshal(response.body, &offer) != nil || offer.ID != id || offer.Version < 1 || offer.TermsHash == "" {
		return fundingDemoOffer{}, errors.New("funding offer response invalid")
	}
	return offer, nil
}

func demoIdempotencyKey(runID, profile, action string) string {
	return "demo:" + runID + ":" + profile + ":" + action
}

func apiErrorCode(body []byte) string {
	var value struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	return value.Error.Code
}

func readPreparedProviderState(path string, manifest fundingManifest) (providerState, error) {
	data, err := readDemoFile(path, maxDemoArtifactBytes)
	if err != nil {
		return providerState{}, errors.New("provider fixture state unavailable")
	}
	var state providerState
	if decodeStrict(data, &state) != nil || state.SchemaVersion != fixtureVersion || state.RunID != manifest.RunID ||
		state.FundingAccountRef != manifest.FundingAccountRef || state.Transfers == nil || state.RequestIndex == nil ||
		state.Accounts == nil || state.Deposits == nil {
		return providerState{}, errors.New("provider fixture state invalid")
	}
	return state, nil
}

func waitForDemoOperation(ctx context.Context, api *fundingDemoAPI, cfg config.Config, id, kind string) error {
	stageCtx, cancel := context.WithTimeout(ctx, demoStageTimeout)
	defer cancel()
	for {
		response, err := api.request(stageCtx, http.MethodGet, "/api/operations/"+url.PathEscape(id),
			"funding-coordinator", cfg.CoordinatorSecret, "", nil)
		if err == nil && response.status == http.StatusOK {
			var operation fundingDemoOperation
			if json.Unmarshal(response.body, &operation) != nil || operation.ID != id || operation.Kind != kind {
				return errors.New("funding operation readback is invalid")
			}
			switch operation.State {
			case "SETTLED":
				if operation.SettlementReference == nil || *operation.SettlementReference == "" {
					return errors.New("settled provider operation has no ledger reference")
				}
				return nil
			case "HELD", "FAILED_CONFIRMED", "CANCELLED_UNSENT":
				return errors.New("provider operation reached a held or terminal nonsettled state")
			}
		}
		if !waitForDemoPoll(stageCtx) {
			return errors.New("provider operation did not reach verified settlement before the bounded wait expired")
		}
	}
}

func waitForDemoCollectionCount(ctx context.Context, api *fundingDemoAPI, cfg config.Config,
	bridgeID string, expected int) ([]fundingDemoCollection, error) {
	stageCtx, cancel := context.WithTimeout(ctx, demoStageTimeout)
	defer cancel()
	for {
		page, err := readFundingDemoCollections(stageCtx, api, cfg, bridgeID)
		if err == nil && len(page) >= expected {
			allSettled := true
			for _, item := range page {
				if item.State != "SETTLED" {
					allSettled = false
					break
				}
			}
			if allSettled {
				return page, nil
			}
		}
		if !waitForDemoPoll(stageCtx) {
			return nil, errors.New("collections did not reach verified settlement before the bounded wait expired")
		}
	}
}

func waitForDemoBridgeClosure(ctx context.Context, api *fundingDemoAPI, cfg config.Config, id string) (fundingDemoBridge, error) {
	stageCtx, cancel := context.WithTimeout(ctx, demoStageTimeout)
	defer cancel()
	for {
		bridge, err := readFundingDemoBridge(stageCtx, api, cfg, id)
		if err == nil && bridge.Lifecycle == "REPAID" {
			return bridge, nil
		}
		if err == nil && len(bridge.HoldReasons) > 0 {
			return fundingDemoBridge{}, errors.New("bridge entered a review hold instead of closing")
		}
		if !waitForDemoPoll(stageCtx) {
			return fundingDemoBridge{}, errors.New("bridge did not close before the bounded wait expired")
		}
	}
}

func waitForDemoPoll(ctx context.Context) bool {
	timer := time.NewTimer(demoPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func readFundingDemoBridge(ctx context.Context, api *fundingDemoAPI, cfg config.Config, id string) (fundingDemoBridge, error) {
	response, err := api.request(ctx, http.MethodGet, "/api/bridges/"+url.PathEscape(id),
		"funding-coordinator", cfg.CoordinatorSecret, "", nil)
	if err != nil || response.status != http.StatusOK {
		return fundingDemoBridge{}, errors.New("bridge readback unavailable")
	}
	var bridge fundingDemoBridge
	if json.Unmarshal(response.body, &bridge) != nil || bridge.ID != id || bridge.Version < 1 ||
		bridge.HoldReasons == nil || bridge.Operations == nil {
		return fundingDemoBridge{}, errors.New("bridge readback invalid")
	}
	return bridge, nil
}

func readFundingDemoCollections(ctx context.Context, api *fundingDemoAPI, cfg config.Config,
	bridgeID string) ([]fundingDemoCollection, error) {
	response, err := api.request(ctx, http.MethodGet, "/api/bridges/"+url.PathEscape(bridgeID)+"/collections",
		"funding-coordinator", cfg.CoordinatorSecret, "", nil)
	if err != nil || response.status != http.StatusOK {
		return nil, errors.New("collection readback unavailable")
	}
	var page fundingDemoCollectionPage
	if json.Unmarshal(response.body, &page) != nil || page.Items == nil || page.NextCursor != nil {
		return nil, errors.New("collection readback invalid")
	}
	items := make([]fundingDemoCollection, 0, len(page.Items))
	for _, raw := range page.Items {
		var item fundingDemoCollection
		if json.Unmarshal(raw, &item) != nil || item.ID == "" || item.OperationID == "" ||
			item.DepositID == "" || item.Allocations == nil {
			return nil, errors.New("collection item readback invalid")
		}
		items = append(items, item)
	}
	return items, nil
}

func settledCollections(items []fundingDemoCollection) ([]fundingDemoCollection, error) {
	for _, item := range items {
		if item.State != "SETTLED" || item.SettledAt == nil || item.SettledDebitCents <= 0 {
			return nil, errors.New("collection movement lacks final settlement proof")
		}
	}
	return items, nil
}

func oneInternalDepositID(items []fundingDemoCollection) (string, bool) {
	if len(items) != 1 || !demoUUID(items[0].DepositID) {
		return "", false
	}
	return items[0].DepositID, true
}

func oneDistinctInternalDepositID(items []fundingDemoCollection, firstID string) (string, bool) {
	groupIDs := make(map[string]struct{}, 2)
	for _, item := range items {
		if !demoUUID(item.DepositID) {
			return "", false
		}
		groupIDs[item.DepositID] = struct{}{}
	}
	if _, ok := groupIDs[firstID]; !ok || len(groupIDs) != 2 {
		return "", false
	}
	for id := range groupIDs {
		if id != firstID {
			return id, true
		}
	}
	return "", false
}

func readVerifiedSyntheticDeposit(ctx context.Context, adapter provider.Boundary, manifest fundingManifest,
	statePath string, profile upstreamProfileRef, providerDepositID string, expectedNetCents int64) (provider.DepositObservation, error) {
	state, err := readPreparedProviderState(statePath, manifest)
	if err != nil {
		return provider.DepositObservation{}, err
	}
	fixtureDeposit, exists := state.Deposits[providerDepositID]
	if !exists || !fixtureDeposit.Settled || fixtureDeposit.AccountRef != profile.AccountID ||
		fixtureDeposit.CustomerID != profile.CustomerID || fixtureDeposit.GrossCents != expectedNetCents ||
		fixtureDeposit.NetCents != expectedNetCents || fixtureDeposit.FeeCents != 0 || fixtureDeposit.SettledAt == nil {
		return provider.DepositObservation{}, errors.New("synthetic provider deposit control evidence mismatches manifest")
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	observation, err := adapter.Deposit(readCtx, profile.AccountID, providerDepositID)
	if err != nil || observation.AccountRef != profile.AccountID || observation.DepositID != providerDepositID ||
		observation.CustomerID != profile.CustomerID || observation.Currency != "USD" ||
		observation.GrossCents != expectedNetCents || observation.NetCents != expectedNetCents ||
		observation.ProviderFeeCents == nil || *observation.ProviderFeeCents != 0 ||
		observation.Status != "SETTLED" || observation.Kind != "EXTERNAL_SETTLED" ||
		observation.SettledAt == nil || !observation.SettledAt.Equal(*fixtureDeposit.SettledAt) ||
		observation.ObservedAt.Before(*observation.SettledAt) {
		return provider.DepositObservation{}, errors.New("synthetic provider deposit read evidence is invalid")
	}
	return observation, nil
}

func validFirstDepositCollection(items []fundingDemoCollection, depositID string, amount int64) bool {
	if len(items) == 0 {
		return false
	}
	var matches []fundingDemoCollection
	for _, item := range items {
		if item.DepositID == depositID {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 || matches[0].SettledDebitCents != amount {
		return false
	}
	return exactDemoAllocations(matches[0].Allocations, map[string]int64{"CONTRACT_FEE": amount})
}

func validDemoCollectionSequence(items []fundingDemoCollection, depositIDs []string,
	firstDeposit, secondDeposit, principal, contractFee int64) bool {
	if len(depositIDs) != 2 {
		return false
	}
	var firstItems, secondItems []fundingDemoCollection
	for _, item := range items {
		if item.DepositID == depositIDs[0] {
			firstItems = append(firstItems, item)
		} else if item.DepositID == depositIDs[1] {
			secondItems = append(secondItems, item)
		} else {
			return false
		}
	}
	feeAfterFirst := contractFee - firstDeposit
	if feeAfterFirst < 0 {
		feeAfterFirst = 0
	}
	wantSecondCount := 1
	if feeAfterFirst > 0 {
		wantSecondCount++
	}
	if len(firstItems) != 1 || len(secondItems) != wantSecondCount || firstItems[0].SettledDebitCents != firstDeposit {
		return false
	}
	if !exactDemoAllocations(firstItems[0].Allocations, map[string]int64{"CONTRACT_FEE": firstDeposit}) {
		return false
	}
	var collectedFee, collectedPrincipal, collectedSecond int64
	var secondFee *fundingDemoCollection
	var secondPrincipal *fundingDemoCollection
	for index := range secondItems {
		item := &secondItems[index]
		collectedSecond += item.SettledDebitCents
		for _, allocation := range item.Allocations {
			switch allocation.Bucket {
			case "CONTRACT_FEE":
				if secondFee != nil || allocation.AmountCents != feeAfterFirst ||
					item.SettledDebitCents != feeAfterFirst ||
					!exactDemoAllocations(item.Allocations, map[string]int64{"CONTRACT_FEE": feeAfterFirst}) {
					return false
				}
				secondFee = item
				collectedFee += allocation.AmountCents
			case "PRINCIPAL":
				if secondPrincipal != nil || allocation.AmountCents != principal ||
					item.SettledDebitCents != principal ||
					!exactDemoAllocations(item.Allocations, map[string]int64{"PRINCIPAL": principal}) {
					return false
				}
				secondPrincipal = item
				collectedPrincipal += allocation.AmountCents
			default:
				return false
			}
		}
	}
	if feeAfterFirst > 0 {
		if secondFee == nil || secondPrincipal == nil || secondFee.SettledAt == nil ||
			secondPrincipal.CreatedAt.Before(*secondFee.SettledAt) {
			return false
		}
	} else if secondFee != nil || secondPrincipal == nil {
		return false
	}
	return collectedSecond == secondDeposit && collectedFee == feeAfterFirst &&
		collectedPrincipal == principal && firstDeposit+secondDeposit == principal+contractFee
}

func exactDemoAllocations(actual []fundingDemoAllocation, expected map[string]int64) bool {
	if len(actual) != len(expected) {
		return false
	}
	for _, allocation := range actual {
		if allocation.AmountCents <= 0 || expected[allocation.Bucket] != allocation.AmountCents {
			return false
		}
	}
	return true
}

func verifyDemoProviderTransferEvidence(state providerState, manifest fundingManifest,
	profile upstreamProfileRef, bridgeID, fundingOperationID, firstFeeOperationID string,
	items []fundingDemoCollection, expectedCollectionCount int, firstFeeAmount, principalCents int64) bool {
	fundingRequestID := "bridge-" + bridgeID
	fundingTransfer, ok := lookupUniqueDemoTransfer(state, fundingRequestID)
	if !ok || fundingTransfer.SourceAccountID != manifest.FundingAccountRef ||
		fundingTransfer.Destination != profile.AccountID || fundingTransfer.AmountCents != principalCents ||
		fundingTransfer.Reason != "loan_disbursement" || fundingTransfer.Reference != bridgeID {
		return false
	}
	if fundingOperationID == "" || fundingTransfer.RequestID != fundingRequestID ||
		state.DroppedReplyRequestID != "fee-"+firstFeeOperationID || firstFeeOperationID == "" ||
		state.DropFirstCollectionFeeReply || len(items) != expectedCollectionCount {
		return false
	}
	firstFeeFound := false
	for _, item := range items {
		requestPrefix, reason := "", ""
		amount := int64(0)
		for _, allocation := range item.Allocations {
			if allocation.AmountCents <= 0 || amount > math.MaxInt64-allocation.AmountCents {
				return false
			}
			amount += allocation.AmountCents
			switch allocation.Bucket {
			case "CONTRACT_FEE":
				if requestPrefix != "" && requestPrefix != "fee-" {
					return false
				}
				requestPrefix, reason = "fee-", "professional_business_services"
			case "PRINCIPAL":
				if requestPrefix != "" && requestPrefix != "principal-" {
					return false
				}
				requestPrefix, reason = "principal-", "loan_credit_repayment"
			default:
				return false
			}
		}
		if requestPrefix == "" || amount != item.SettledDebitCents || item.OperationID == "" {
			return false
		}
		requestID := requestPrefix + item.OperationID
		transfer, found := lookupUniqueDemoTransfer(state, requestID)
		if !found || transfer.SourceAccountID != profile.AccountID || transfer.Destination != manifest.FundingAccountRef ||
			transfer.AmountCents != item.SettledDebitCents || transfer.Reason != reason || transfer.Reference != bridgeID {
			return false
		}
		if item.OperationID == firstFeeOperationID {
			firstFeeFound = requestPrefix == "fee-" && item.DepositID != "" && amount == firstFeeAmount
		}
	}
	return firstFeeFound
}

func lookupUniqueDemoTransfer(state providerState, requestID string) (providerTransfer, bool) {
	var empty providerTransfer
	transferID, ok := state.RequestIndex[requestID]
	if !ok || transferID == "" {
		return empty, false
	}
	transfer, ok := state.Transfers[transferID]
	if !ok || transfer.ID != transferID || transfer.RequestID != requestID || transfer.Status != "SETTLED" ||
		transfer.CreatedAt.IsZero() || transfer.SettledAt.IsZero() || transfer.SettledAt.Before(transfer.CreatedAt) {
		return empty, false
	}
	count := 0
	for _, candidate := range state.Transfers {
		if candidate.RequestID == requestID {
			count++
		}
	}
	return transfer, count == 1
}

func verifyFundingDemoReadModels(ctx context.Context, api *fundingDemoAPI, cfg config.Config,
	customerID, bridgeID string, principal, contractFee int64) error {
	path := "/api/servicing/state?customer_id=" + url.QueryEscape(customerID)
	response, err := api.request(ctx, http.MethodGet, path, "underwriting-reader", cfg.ReaderSecret, "", nil)
	if err != nil || response.status != http.StatusOK {
		return errors.New("funding servicing state readback unavailable")
	}
	var state fundingDemoServiceState
	if json.Unmarshal(response.body, &state) != nil || state.Mode != config.ModeDemo || state.FundingAuthorized ||
		state.Customer.PrincipalOwedCents != 0 || state.Customer.UnpaidFeeCents != 0 ||
		state.Customer.ReservedPrincipal != 0 || state.Customer.ReservedFees != 0 || state.Customer.ActiveHold ||
		state.Facility.PrincipalOwed != 0 || state.Facility.ReservedPrincipal != 0 || state.Facility.OtherCommitments != 0 ||
		len(state.Invoices) == 0 || state.History == nil || len(state.History) != 1 {
		return errors.New("funding customer or facility did not close with zero debt, backing and holds")
	}
	for _, invoice := range state.Invoices {
		if invoice.ReservedCents != 0 {
			return errors.New("receivable backing remains reserved after bridge closure")
		}
	}
	found := false
	for _, bridge := range state.History {
		if bridge.BridgeID == bridgeID {
			if bridge.Lifecycle != "REPAID" || bridge.ClosedAt == nil || bridge.PrincipalOwedCents != 0 ||
				bridge.ContractFeeOwedCents != 0 || bridge.LateFeeOwedCents != 0 ||
				bridge.PrincipalOriginalCents != principal || bridge.ContractFeeOriginalCents != contractFee ||
				bridge.PrincipalPaidCents != principal || bridge.ContractFeePaidCents != contractFee {
				return errors.New("funding history does not show a closed zero-debt bridge")
			}
			found = true
		}
	}
	if !found || principal <= 0 || contractFee <= 0 {
		return errors.New("closed bridge history is unavailable")
	}
	return nil
}

func configureSyntheticDeposit(ctx context.Context, api *fundingDemoAPI, cfg config.Config, manifest fundingManifest,
	statePath, depositID string, netCents int64) error {
	state, err := readPreparedProviderState(statePath, manifest)
	if err != nil {
		return err
	}
	deposit, exists := state.Deposits[depositID]
	if !exists || netCents <= 0 {
		return errors.New("synthetic deposit missing or invalid")
	}
	if deposit.Settled {
		if deposit.NetCents != netCents || deposit.GrossCents != netCents || deposit.FeeCents != 0 {
			return errors.New("settled synthetic deposit conflicts with saved assessment amount")
		}
		return nil
	}
	if deposit.NetCents == netCents && deposit.GrossCents == netCents && deposit.FeeCents == 0 {
		return nil
	}
	body := map[string]int64{"gross_cents": netCents, "net_cents": netCents, "provider_fee_cents": 0}
	raw, err := json.Marshal(body)
	if err != nil {
		return errors.New("synthetic deposit control encoding failed")
	}
	return api.providerControl(ctx, cfg, "/__control/deposits/"+url.PathEscape(depositID)+"/configure", raw)
}

func settleSyntheticDeposit(ctx context.Context, api *fundingDemoAPI, cfg config.Config,
	manifest fundingManifest, statePath, depositID string) error {
	state, err := readPreparedProviderState(statePath, manifest)
	if err != nil {
		return err
	}
	deposit, exists := state.Deposits[depositID]
	if !exists {
		return errors.New("synthetic provider deposit unavailable")
	}
	if deposit.Settled {
		return nil
	}
	return api.providerControl(ctx, cfg, "/__control/deposits/"+url.PathEscape(depositID)+"/settle", []byte("{}"))
}

func (a *fundingDemoAPI) providerControl(ctx context.Context, cfg config.Config, path string, body []byte) error {
	controlKey := os.Getenv("DEMO_PROVIDER_CONTROL_KEY")
	if len(controlKey) < 32 || cfg.ProviderMode != config.ProviderMock || cfg.ProviderBaseURL != "http://"+providerAddress {
		return errors.New("provider fixture control configuration invalid")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.ProviderBaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("provider fixture control request invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Demo-Control-Key", controlKey)
	response, err := a.client.Do(req)
	if err != nil {
		return errors.New("provider fixture control unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16_385))
	if err != nil || len(data) > 16_384 || response.StatusCode != http.StatusOK {
		return errors.New("provider fixture control rejected")
	}
	return nil
}

func readFundingDemoServiceState(ctx context.Context, api *fundingDemoAPI, cfg config.Config,
	customerID string) (fundingDemoServiceState, error) {
	path := "/api/servicing/state?customer_id=" + url.QueryEscape(customerID)
	response, err := api.request(ctx, http.MethodGet, path, "underwriting-reader", cfg.ReaderSecret, "", nil)
	if err != nil || response.status != http.StatusOK {
		return fundingDemoServiceState{}, errors.New("funding servicing state readback unavailable")
	}
	var state fundingDemoServiceState
	if json.Unmarshal(response.body, &state) != nil || state.Customer.CustomerID != customerID ||
		state.Invoices == nil || state.History == nil {
		return fundingDemoServiceState{}, errors.New("funding servicing state response invalid")
	}
	return state, nil
}

func readFundingDemoChanges(ctx context.Context, api *fundingDemoAPI, cfg config.Config,
	after int64) ([]fundingDemoChange, uint64, error) {
	if after < 0 {
		return nil, 0, errors.New("funding change checkpoint invalid")
	}
	cursor := uint64(after)
	changes := make([]fundingDemoChange, 0)
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		path := "/api/servicing/changes?after_seq=" + strconv.FormatUint(cursor, 10)
		response, err := api.request(ctx, http.MethodGet, path, "funding-coordinator", cfg.CoordinatorSecret, "", nil)
		if err != nil || response.status != http.StatusOK {
			return nil, 0, errors.New("funding change feed readback unavailable")
		}
		var page fundingDemoChangePage
		if json.Unmarshal(response.body, &page) != nil || page.Mode != config.ModeDemo || page.Items == nil {
			return nil, 0, errors.New("funding change feed response invalid")
		}
		for _, change := range page.Items {
			if change.Sequence <= cursor || change.ResourceID == "" || change.ResourceVersion <= 0 ||
				(change.ResourceType != "OFFER" && change.ResourceType != "BRIDGE" && change.ResourceType != "OPERATION") {
				return nil, 0, errors.New("funding change feed sequence invalid")
			}
			changes = append(changes, change)
		}
		if !page.HasMore {
			if page.NextAfterSeq < cursor || (len(page.Items) > 0 && page.NextAfterSeq != page.Items[len(page.Items)-1].Sequence) {
				return nil, 0, errors.New("funding change feed checkpoint invalid")
			}
			return changes, page.NextAfterSeq, nil
		}
		if len(page.Items) == 0 || page.NextAfterSeq <= cursor || page.NextAfterSeq != page.Items[len(page.Items)-1].Sequence {
			return nil, 0, errors.New("funding change feed page did not advance")
		}
		cursor = page.NextAfterSeq
	}
	return nil, 0, errors.New("funding change feed page limit reached")
}

func validDemoChangeReferences(changes []fundingDemoChange, resourceIDs []string,
	originatingCaseID *string, evaluationID, offerID string, bridgeID *string) bool {
	required := make(map[string]string, len(resourceIDs))
	for index, id := range resourceIDs {
		if id == "" {
			return false
		}
		resourceType := "OPERATION"
		if index == 0 {
			resourceType = "OFFER"
		} else if bridgeID != nil && id == *bridgeID {
			resourceType = "BRIDGE"
		}
		required[id] = resourceType
	}
	found := make(map[string]bool, len(required))
	for _, change := range changes {
		if change.OfferID == nil || *change.OfferID != offerID {
			continue
		}
		if !sameOptionalID(change.OriginatingCaseID, originatingCaseID) ||
			change.EvaluationID == nil || *change.EvaluationID != evaluationID {
			return false
		}
		wantType, expected := required[change.ResourceID]
		if !expected {
			if bridgeID == nil && change.ResourceType != "OFFER" {
				return false
			}
			continue
		}
		if change.ResourceType != wantType {
			return false
		}
		if wantType == "BRIDGE" {
			if change.BridgeID == nil || *change.BridgeID != *bridgeID || change.BridgeVersion == nil ||
				*change.BridgeVersion != change.ResourceVersion || *change.BridgeVersion <= 0 {
				return false
			}
		} else if wantType == "OPERATION" {
			if bridgeID == nil || change.BridgeID == nil || *change.BridgeID != *bridgeID || change.BridgeVersion == nil || *change.BridgeVersion <= 0 {
				return false
			}
		} else if change.BridgeID != nil || change.BridgeVersion != nil {
			if bridgeID == nil || change.BridgeID == nil || *change.BridgeID != *bridgeID ||
				change.BridgeVersion == nil || *change.BridgeVersion <= 0 {
				return false
			}
		}
		found[change.ResourceID] = true
	}
	for id := range required {
		if !found[id] {
			return false
		}
	}
	return len(required) > 0
}

func sameOptionalID(actual, expected *string) bool {
	if expected == nil {
		return actual == nil || *actual == ""
	}
	return actual != nil && *actual == *expected
}

func knownEmptyDemoOpeningState(state fundingDemoServiceState) bool {
	if state.Customer.PrincipalOwedCents != 0 || state.Customer.UnpaidFeeCents != 0 ||
		state.Customer.ReservedPrincipal != 0 || state.Customer.ReservedFees != 0 || state.Customer.ActiveHold ||
		state.Facility.PrincipalOwed != 0 || state.Facility.ReservedPrincipal != 0 ||
		state.Facility.OtherCommitments != 0 || len(state.History) != 0 {
		return false
	}
	for _, invoice := range state.Invoices {
		if invoice.ReservedCents != 0 {
			return false
		}
	}
	return true
}

func knownEmptyOrSameEvaluationState(state fundingDemoServiceState, evaluationID string) bool {
	if knownEmptyDemoOpeningState(state) {
		return true
	}
	if len(state.History) != 1 || evaluationID == "" {
		return false
	}
	bridge := state.History[0]
	if bridge.BridgeID == "" || bridge.EvaluationID != evaluationID || bridge.OfferID == "" ||
		(bridge.Lifecycle != "ACTIVE" && bridge.Lifecycle != "COLLECTING" && bridge.Lifecycle != "REPAID") ||
		state.Customer.ActiveHold || state.Facility.OtherCommitments != 0 || state.Facility.ReservedPrincipal != 0 ||
		state.Facility.PrincipalOwed != bridge.PrincipalOwedCents ||
		bridge.ContractFeeOwedCents > math.MaxInt64-bridge.LateFeeOwedCents ||
		state.Customer.PrincipalOwedCents != bridge.PrincipalOwedCents ||
		state.Customer.UnpaidFeeCents != bridge.ContractFeeOwedCents+bridge.LateFeeOwedCents {
		return false
	}
	if bridge.Lifecycle == "REPAID" {
		if bridge.PrincipalOwedCents != 0 || bridge.ContractFeeOwedCents != 0 || bridge.LateFeeOwedCents != 0 ||
			state.Customer.ReservedPrincipal != 0 || state.Customer.ReservedFees != 0 {
			return false
		}
		for _, invoice := range state.Invoices {
			if invoice.ReservedCents != 0 {
				return false
			}
		}
	}
	return true
}

func int64Value(value *int64) int64 {
	if value == nil {
		return math.MinInt64
	}
	return *value
}

func writeFundingDemoSummary(summary fundingDemoRunSummary) error {
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		return errors.New("demo run summary unavailable")
	}
	return nil
}

func demoRunFixtureLabel(manifest fundingManifest) string {
	if manifest.UpstreamInputKind == upstreamInputOwnedBundle {
		return "OWNED_KNOWN_EMPTY_UNDERWRITING_INPUT_WITH_SYNTHETIC_FUNDING_PROVIDER"
	}
	return "SYNTHETIC_COMPONENT_INPUT_WITH_ACTUAL_UNDERWRITING_API"
}
