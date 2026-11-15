// Package worker prepares immutable funding intents for the later network worker.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/underwriting"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
)

// DispatchPreparer owns only read clients and the reviewed policy boundary.
type DispatchPreparer struct {
	cfg          config.Config
	repository   *postgres.Repository
	provider     provider.Boundary
	evaluator    *underwriting.Evaluator
	pricing      domain.Pricing
	pricingReady bool
	now          func() time.Time
}

// PreparedFacts contains only outside-transaction reads or a typed preflight failure.
type PreparedFacts struct {
	Guard        postgres.FundingGuard
	Failure      *postgres.OperationReadOutcome
	CancelReason string
}

// NewDispatchPreparer binds one loaded runtime configuration to one evaluator and provider.
func NewDispatchPreparer(cfg config.Config, repository *postgres.Repository, providerClient provider.Boundary,
	evaluator *underwriting.Evaluator, pricing domain.Pricing, pricingReady bool,
	now func() time.Time) (*DispatchPreparer, error) {
	if repository == nil || providerClient == nil || evaluator == nil ||
		(cfg.Mode != config.ModeDemo && cfg.Mode != config.ModeSandbox) || cfg.FacilityID == "" ||
		cfg.PolicyID != evaluator.PolicyID() || cfg.PolicyHash != evaluator.PolicyHash() ||
		!pricingReady || pricing.Validate() != nil || pricing != evaluator.Pricing() {
		return nil, errors.New("dispatch_preparer_configuration_invalid")
	}
	if now == nil {
		now = time.Now
	}
	return &DispatchPreparer{cfg: cfg, repository: repository, provider: providerClient,
		evaluator: evaluator, pricing: pricing, pricingReady: pricingReady, now: now}, nil
}

// PrepareInputs loads the bound operation and performs only current GET/quote reads.
func (p *DispatchPreparer) PrepareInputs(ctx context.Context,
	claim postgres.OperationClaim) (PreparedFacts, error) {
	if claim.Mode != domain.Mode(p.cfg.Mode) || claim.State != domain.OperationQueued ||
		claim.Kind != "FUND" || claim.FacilityID != p.cfg.FacilityID || claim.LeaseToken == "" {
		return PreparedFacts{}, postgres.ErrOperationClaimStale
	}
	bindings, err := p.repository.LoadDispatchBindings(ctx, claim)
	if err != nil {
		return PreparedFacts{}, err
	}
	underwritingClient, err := upstream.NewUnderwritingClientWithClock(p.cfg.UnderwritingURL,
		p.cfg.UnderwritingSecret, p.now)
	if err != nil {
		return p.failed("DISPATCH_UNDERWRITING_READER_INVALID", provider.RecoveryHold, err), nil
	}
	shortfallClient, err := upstream.NewShortfallClientForModeWithClock(p.cfg.ShortfallURL,
		p.cfg.ShortfallSecret, p.cfg.Mode, p.now)
	if err != nil {
		return p.failed("DISPATCH_SHORTFALL_READER_INVALID", provider.RecoveryHold, err), nil
	}
	assessment, err := underwritingClient.ReadEvaluation(ctx, bindings.OfferEvaluationID, p.cfg.Mode)
	if err != nil {
		if err.Error() == "underwriting_resource_not_found" {
			return PreparedFacts{CancelReason: "POLICY_RECHECK_FAILED"}, nil
		}
		return p.failed(sourceFailureCode(err), sourceFailureRecovery(err), err), nil
	}
	contextFacts, err := underwritingClient.ReadCustomerContext(ctx, claim.CustomerID, p.cfg.Mode)
	if err != nil {
		if err.Error() == "underwriting_resource_not_found" {
			return PreparedFacts{CancelReason: "POLICY_RECHECK_FAILED"}, nil
		}
		return p.failed(sourceFailureCode(err), sourceFailureRecovery(err), err), nil
	}
	if !contextFacts.Fresh {
		return p.failed("DISPATCH_CONTEXT_STALE", provider.RecoveryRetryRead,
			errors.New("dispatch_customer_context_stale")), nil
	}
	prePolicyBlockCode := ""
	if contextFacts.PolicyID != p.evaluator.PolicyID() || contextFacts.PolicyHash != p.evaluator.PolicyHash() {
		return PreparedFacts{CancelReason: "POLICY_RECHECK_FAILED"}, nil
	}
	if contextFacts.PolicyID != bindings.OfferPolicyID || contextFacts.PolicyHash != bindings.OfferPolicyHash {
		prePolicyBlockCode = "CUSTOMER_POLICY_BINDING_CHANGED"
	}
	var contextMapping struct {
		AccountID string `json:"account_id"`
		CardID    string `json:"card_id"`
	}
	if json.Unmarshal(contextFacts.Data, &contextMapping) == nil &&
		((contextMapping.AccountID != "" && contextMapping.AccountID != bindings.Operation.DestinationAccountRef) ||
			(contextMapping.CardID != "" && contextMapping.CardID != bindings.CardRef)) {
		prePolicyBlockCode = "MAPPING_CONFLICT"
	}
	contextAccountID, contextCardID := bindings.Operation.DestinationAccountRef, bindings.CardRef
	if contextMapping.AccountID != "" {
		contextAccountID = contextMapping.AccountID
	}
	if contextMapping.CardID != "" {
		contextCardID = contextMapping.CardID
	}
	customerSnapshot, err := p.evaluator.DecodeCustomerContext(contextFacts, p.cfg.Mode,
		claim.CustomerID, contextAccountID, contextCardID, p.now().UTC())
	if err != nil {
		return p.failed("DISPATCH_CONTEXT_INVALID", provider.RecoveryHold, err), nil
	}
	savedShortfall, err := shortfallClient.ReadForMode(ctx, assessment.ShortfallID, p.cfg.Mode)
	if err != nil {
		if err.Error() == "shortfall_not_found" {
			return PreparedFacts{CancelReason: "POLICY_RECHECK_FAILED"}, nil
		}
		return p.failed(sourceFailureCode(err), sourceFailureRecovery(err), err), nil
	}
	quoter, ok := p.provider.(provider.FundingFeeQuoter)
	if !ok {
		return p.failed("SOURCE_FEE_CONTRACT_UNAVAILABLE", provider.RecoveryHold,
			errors.New("provider_fee_quoter_unavailable")), nil
	}
	feeQuote, err := quoter.QuoteFundingFee(ctx, bindings.FacilityAccount,
		bindings.Operation.DestinationAccountRef, bindings.Operation.AmountCents)
	if err != nil {
		return p.failedProvider(err), nil
	}
	payment, err := p.provider.Payment(ctx, bindings.Operation.DestinationAccountRef,
		bindings.TransactionID)
	if err != nil {
		return p.failedProvider(err), nil
	}
	borrowerBalance, err := p.provider.Balance(ctx, bindings.Operation.DestinationAccountRef)
	if err != nil {
		return p.failedProvider(err), nil
	}
	facilityBalance, err := p.provider.Balance(ctx, bindings.FacilityAccount)
	if err != nil {
		return p.failedProvider(err), nil
	}
	if feeQuote.Cents < 0 || feeQuote.ContractID == "" {
		return p.failed("SOURCE_FEE_CONTRACT_INVALID", provider.RecoveryHold,
			errors.New("source_fee_contract_invalid")), nil
	}
	knownFee := feeQuote.Cents
	request := postgres.FundingGuard{ActionKind: "FIRST_SEND", ActionID: claim.OperationID,
		ActionVersion:      claim.Version,
		LastFundingCheckID: bindings.LastFundingCheckID, PrePolicyBlockCode: prePolicyBlockCode,
		Mode: p.cfg.Mode, FacilityID: p.cfg.FacilityID,
		OfferID: bindings.OfferID, ExpectedVersion: bindings.OfferVersion,
		Clock: p.now, SavedShortfall: savedShortfall, Assessment: assessment, Payment: payment,
		BorrowerBalance: borrowerBalance, FacilityBalance: facilityBalance,
		KnownSourceFeeCents: &knownFee, SourceFeeContractID: feeQuote.ContractID,
		InitialSourceFeeCents: bindings.FeeQuoteCents, InitialSourceFeeContractID: bindings.FeeContractID,
		MockFeeContract:    feeQuote.ContractID == "MOCK_HTTP_ZERO_FUNDING_FEE_V1" && p.cfg.ProviderMode == config.ProviderMock,
		ExcludeOperationID: claim.OperationID, ExcludeReservationID: bindings.ReservationID,
		ExcludeBridgeID: claim.BridgeID, ExcludeTransactionID: bindings.TransactionID,
		CustomerContext: contextFacts, CustomerSnapshot: customerSnapshot, Evaluator: p.evaluator,
		Pricing: p.pricing, PricingReady: p.pricingReady}
	return PreparedFacts{Guard: request}, nil
}

// AuthorizeFirstSend returns a typed intent only after its fenced transaction commits.
// It deliberately stops at DISPATCHING and never calls Create.
func (p *DispatchPreparer) AuthorizeFirstSend(ctx context.Context,
	claim postgres.OperationClaim) (postgres.DispatchPreparation, error) {
	facts, err := p.PrepareInputs(ctx, claim)
	if err != nil {
		return postgres.DispatchPreparation{}, err
	}
	if facts.Failure != nil {
		if err := p.repository.RecordDispatchPreflightFailure(ctx, claim, *facts.Failure); err != nil {
			return postgres.DispatchPreparation{}, err
		}
		return postgres.DispatchPreparation{RetryScheduled: facts.Failure.ProviderError != nil &&
			facts.Failure.ProviderError.Recovery == provider.RecoveryRetryRead,
			BlockedCode: safeFailureCode(*facts.Failure)}, nil
	}
	if facts.CancelReason != "" {
		if err := p.repository.CancelClaimedUnsentFunding(ctx, claim, facts.CancelReason, p.now().UTC()); err != nil {
			return postgres.DispatchPreparation{}, err
		}
		return postgres.DispatchPreparation{Cancelled: true, BlockedCode: facts.CancelReason}, nil
	}
	return p.repository.PrepareFundingDispatch(ctx, claim, facts.Guard, p.now)
}

// AuthorizeCollectionFirstSend repeats canonical deposit, balance, mapping,
// mandate, debt and budget checks before committing a B-to-A send.
func (p *DispatchPreparer) AuthorizeCollectionFirstSend(ctx context.Context,
	claim postgres.OperationClaim) (postgres.DispatchPreparation, error) {
	bindings, err := p.repository.LoadCollectionDispatchBindings(ctx, claim)
	if err != nil {
		return postgres.DispatchPreparation{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deposit, err := p.provider.Deposit(readCtx, bindings.Candidate.AccountRef,
		bindings.Candidate.ProviderDepositID)
	if err != nil {
		providerError := typedReadError(err, "COLLECTION_DEPOSIT_RETRY")
		if providerError.Recovery == provider.RecoveryRetryRead {
			return p.recordCollectionPreflightRetry(ctx, claim, providerError)
		}
		holdReason := "DEPOSIT_SOURCE_CONFLICT"
		if cancelErr := p.repository.CancelCollectionUnsent(ctx, claim,
			"COLLECTION_PROVIDER_DEPOSIT_INVALID", holdReason, providerError.Code,
			providerError.RawEvidence); cancelErr != nil {
			return postgres.DispatchPreparation{}, cancelErr
		}
		return postgres.DispatchPreparation{Cancelled: true, BlockedCode: providerError.Code}, nil
	}
	balance, err := p.provider.Balance(readCtx, bindings.Candidate.AccountRef)
	if err != nil {
		providerError := typedReadError(err, "COLLECTION_BALANCE_RETRY")
		if providerError.Recovery == provider.RecoveryRetryRead {
			return p.recordCollectionPreflightRetry(ctx, claim, providerError)
		}
		if cancelErr := p.repository.CancelCollectionUnsent(ctx, claim,
			"COLLECTION_PROVIDER_BALANCE_INVALID", "COLLECTION_REVIEW_REQUIRED",
			providerError.Code, providerError.RawEvidence); cancelErr != nil {
			return postgres.DispatchPreparation{}, cancelErr
		}
		return postgres.DispatchPreparation{Cancelled: true, BlockedCode: providerError.Code}, nil
	}
	quoter, ok := p.provider.(provider.CollectionFeeQuoter)
	if !ok {
		if cancelErr := p.repository.CancelCollectionUnsent(ctx, claim,
			"COLLECTION_FEE_CONTRACT_UNAVAILABLE", "COLLECTION_REVIEW_REQUIRED",
			"COLLECTION_FEE_CONTRACT_UNAVAILABLE", nil); cancelErr != nil {
			return postgres.DispatchPreparation{}, cancelErr
		}
		return postgres.DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_FEE_CONTRACT_UNAVAILABLE"}, nil
	}
	feeQuote, err := quoter.QuoteCollectionFee(readCtx, bindings.Operation.SourceAccountRef,
		bindings.Operation.DestinationAccountRef, bindings.Operation.AmountCents)
	if err != nil {
		providerError := typedReadError(err, "COLLECTION_FEE_CONTRACT_RETRY")
		if providerError.Recovery == provider.RecoveryRetryRead {
			return p.recordCollectionPreflightRetry(ctx, claim, providerError)
		}
		if cancelErr := p.repository.CancelCollectionUnsent(ctx, claim,
			"COLLECTION_FEE_CONTRACT_UNAVAILABLE", "COLLECTION_REVIEW_REQUIRED",
			providerError.Code, providerError.RawEvidence); cancelErr != nil {
			return postgres.DispatchPreparation{}, cancelErr
		}
		return postgres.DispatchPreparation{Cancelled: true, BlockedCode: "COLLECTION_FEE_CONTRACT_UNAVAILABLE"}, nil
	}
	evidence := postgres.CollectionReadEvidence{Deposit: deposit, Balance: balance, Fee: feeQuote}
	return p.repository.PrepareCollectionDispatch(ctx, claim, bindings, evidence,
		p.pricing, p.evaluator.PolicyHash(), p.now)
}

func (p *DispatchPreparer) recordCollectionPreflightRetry(ctx context.Context,
	claim postgres.OperationClaim, providerError *provider.ProviderError) (postgres.DispatchPreparation, error) {
	if err := p.repository.RecordCollectionPreflightFailure(ctx, claim, postgres.OperationReadOutcome{
		Source: "GUARD", ProviderError: providerError}); err != nil {
		return postgres.DispatchPreparation{}, err
	}
	return postgres.DispatchPreparation{RetryScheduled: true, BlockedCode: providerError.Code}, nil
}

func (p *DispatchPreparer) failed(code string, recovery provider.RecoveryClass, cause error) PreparedFacts {
	return PreparedFacts{Failure: &postgres.OperationReadOutcome{Source: "GUARD",
		ProviderError: &provider.ProviderError{Code: code, Recovery: recovery, Cause: recovery,
			Certainty: provider.CertaintyUncertain}}}
}

func (p *DispatchPreparer) failedProvider(err error) PreparedFacts {
	if errors.Is(err, provider.ErrProviderUnavailable) {
		return p.failed("DISPATCH_PROVIDER_CAPABILITY_UNAVAILABLE", provider.RecoveryHold, err)
	}
	var providerError *provider.ProviderError
	if errors.As(err, &providerError) {
		copy := *providerError
		copy.RawEvidence = append([]byte(nil), providerError.RawEvidence...)
		return PreparedFacts{Failure: &postgres.OperationReadOutcome{Source: "GUARD", ProviderError: &copy}}
	}
	return p.failed("DISPATCH_PROVIDER_READ_RETRY", provider.RecoveryRetryRead, err)
}

func sourceFailureCode(err error) string {
	switch err.Error() {
	case "underwriting_resource_not_found", "shortfall_not_found":
		return "DISPATCH_BOUND_SOURCE_MISSING"
	case "underwriting_auth_rejected":
		return "DISPATCH_SOURCE_PERMISSION_HOLD"
	case "underwriting_evaluation_response_invalid", "underwriting_context_response_invalid",
		"shortfall_response_invalid", "shortfall_amount_semantics_invalid":
		return "DISPATCH_SOURCE_SCHEMA_HOLD"
	default:
		return "DISPATCH_SOURCE_READ_RETRY"
	}
}

func sourceFailureRecovery(err error) provider.RecoveryClass {
	code := sourceFailureCode(err)
	if code == "DISPATCH_SOURCE_READ_RETRY" {
		return provider.RecoveryRetryRead
	}
	return provider.RecoveryHold
}

func safeFailureCode(outcome postgres.OperationReadOutcome) string {
	if outcome.ProviderError == nil {
		return "DISPATCH_PREFLIGHT_HELD"
	}
	return outcome.ProviderError.Code
}
