package worker

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
)

// Worker processes one durable operation at a time.
type Worker struct {
	cfg               config.Config
	repository        *postgres.Repository
	preparer          *DispatchPreparer
	provider          provider.Boundary
	now               func() time.Time
	nextServicingScan time.Time
}

// New wires a serial worker to the configured book and real provider adapter.
func New(cfg config.Config, repository *postgres.Repository, preparer *DispatchPreparer,
	providerClient provider.Boundary, now func() time.Time) (*Worker, error) {
	if repository == nil || preparer == nil || providerClient == nil || cfg.FacilityID == "" ||
		(cfg.Mode != config.ModeDemo && cfg.Mode != config.ModeSandbox) {
		return nil, errors.New("funding_worker_configuration_invalid")
	}
	if now == nil {
		now = time.Now
	}
	return &Worker{cfg: cfg, repository: repository, preparer: preparer,
		provider: providerClient, now: now}, nil
}

// Run polls every second while idle and has no detached goroutines.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		worked, err := w.ProcessOne(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("funding_worker_iteration_failed")
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// ProcessOne expires one proven-unsent HELD intent or handles one leased due operation.
func (w *Worker) ProcessOne(ctx context.Context) (bool, error) {
	servicingWorked := false
	now := w.now().UTC()
	if w.nextServicingScan.IsZero() || !now.Before(w.nextServicingScan) {
		w.nextServicingScan = now.Add(time.Minute)
		candidates, err := w.repository.DiscoverServicingBridges(ctx, domain.Mode(w.cfg.Mode), w.cfg.FacilityID, now, 25)
		if err != nil {
			return false, errors.New("servicing_scan_discovery_failed")
		}
		for _, candidate := range candidates {
			if err = w.repository.RefreshServicingBridge(ctx, candidate, w.preparer.pricing, w.preparer.evaluator.PolicyHash(), now); err != nil && !errors.Is(err, postgres.ErrOperationClaimStale) && !errors.Is(err, postgres.ErrServicingPolicyUnavailable) {
				return false, errors.New("servicing_scan_refresh_failed")
			}
			servicingWorked = true
		}
	}
	reviews, err := w.repository.DiscoverCollectionReviews(ctx, domain.Mode(w.cfg.Mode), w.cfg.FacilityID, 1)
	if err != nil {
		return false, errors.New("collection_review_discovery_failed")
	}
	if len(reviews) > 0 {
		did, reviewErr := w.repository.ProcessCollectionReview(ctx, reviews[0], w.preparer.pricing, w.preparer.evaluator.PolicyHash(), w.now)
		if reviewErr != nil && !errors.Is(reviewErr, postgres.ErrOperationClaimStale) {
			return false, errors.New("collection_review_processing_failed")
		}
		servicingWorked = servicingWorked || did
	}
	held, err := w.repository.DiscoverHeldUnsentExpirations(ctx,
		domain.Mode(w.cfg.Mode), w.cfg.FacilityID, 25)
	if err != nil {
		return false, errors.New("held_unsent_discovery_failed")
	}
	for _, candidate := range held {
		proof := postgres.HeldUnsentCancellationProof{Candidate: candidate}
		err := w.repository.CancelHeldUnsentFunding(ctx, proof, "HELD_UNSENT_EXPIRED", w.now)
		if errors.Is(err, postgres.ErrOperationClaimStale) {
			continue
		}
		if err != nil {
			return false, errors.New("held_unsent_cancellation_failed")
		}
		return true, nil
	}
	webhookJobs, err := w.repository.DiscoverWebhookJobs(ctx, domain.Mode(w.cfg.Mode), 25)
	if err != nil {
		return false, errors.New("webhook_job_discovery_failed")
	}
	for _, candidate := range webhookJobs {
		job, err := w.repository.ClaimWebhookJob(ctx, candidate)
		if errors.Is(err, postgres.ErrOperationClaimStale) {
			continue
		}
		if err != nil {
			return false, errors.New("webhook_job_claim_failed")
		}
		return true, w.processWebhookJob(ctx, job)
	}
	collectionCandidates, err := w.repository.DiscoverCollectionCandidates(ctx,
		domain.Mode(w.cfg.Mode), w.cfg.FacilityID, 25)
	if err != nil {
		return false, errors.New("collection_discovery_failed")
	}
	for _, candidate := range collectionCandidates {
		worked, err := w.prepareCollection(ctx, candidate)
		if err != nil {
			return false, errors.New("collection_planning_failed")
		}
		if worked {
			return true, nil
		}
	}
	candidates, err := w.repository.DiscoverOperationCandidates(ctx,
		domain.Mode(w.cfg.Mode), w.cfg.FacilityID, 25)
	if err != nil {
		return false, errors.New("operation_discovery_failed")
	}
	for _, candidate := range candidates {
		claim, err := w.repository.ClaimOperation(ctx, candidate)
		if errors.Is(err, postgres.ErrOperationClaimStale) {
			continue
		}
		if err != nil {
			return false, errors.New("operation_claim_failed")
		}
		if claim.LeaseToken == "" {
			// ClaimOperationTx already committed a conservative hold transition.
			return true, nil
		}
		return true, w.processClaim(ctx, claim)
	}
	return servicingWorked, nil
}

func (w *Worker) prepareCollection(ctx context.Context,
	candidate postgres.CollectionCandidate) (bool, error) {
	if w.cfg.Mode != config.ModeDemo || w.cfg.ProviderMode != config.ProviderMock {
		if err := w.repository.RecordCollectionHold(ctx, candidate, "LIVE_COLLECTION_EXTRACTION_UNSUPPORTED", nil); err != nil {
			return false, err
		}
		return true, nil
	}
	facts, err := w.repository.LoadCollectionPlanFacts(ctx, candidate)
	if err != nil {
		if err.Error() == "collection_authority_unavailable" || err.Error() == "collection_terms_binding_invalid" ||
			err.Error() == "authority_usage_read_failed" || err.Error() == "authority_usage_overflow" {
			if holdErr := w.repository.RecordCollectionHold(ctx, candidate,
				"COLLECTION_AUTHORITY_BLOCKED", nil); holdErr != nil {
				return false, holdErr
			}
			return true, nil
		}
		return false, err
	}
	scope, err := w.repository.ResolveTrustedDepositScope(ctx, candidate.Mode,
		candidate.CustomerID, candidate.CardID)
	if err != nil || scope.AccountRef != candidate.AccountRef {
		if holdErr := w.repository.RecordCollectionHold(ctx, candidate, "MAPPING_CONFLICT", nil); holdErr != nil {
			return false, holdErr
		}
		return true, nil
	}
	if facts.AuthorityStatus != "ACTIVE" || facts.ProductCode != "CONNECTED_ACCOUNT_TRANSFERS" ||
		facts.MandateRef == "" || facts.Source != candidate.AccountRef || facts.Destination != facts.FundingAccount ||
		facts.ValidFrom.After(w.now().UTC()) || !w.now().UTC().Before(facts.ExpiresAt) {
		if err := w.repository.RecordCollectionHold(ctx, candidate, "COLLECTION_AUTHORITY_BLOCKED", nil); err != nil {
			return false, err
		}
		return true, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deposit, err := w.provider.Deposit(readCtx, candidate.AccountRef, candidate.ProviderDepositID)
	if err != nil {
		providerError := typedReadError(err, "COLLECTION_DEPOSIT_RETRY")
		if providerError.Recovery == provider.RecoveryRetryRead {
			return false, nil
		}
		if holdErr := w.repository.RecordCollectionDepositEvidenceHold(ctx, candidate,
			providerError.Code, providerError.RawEvidence); holdErr != nil {
			return false, holdErr
		}
		return true, nil
	}
	balance, err := w.provider.Balance(readCtx, candidate.AccountRef)
	if err != nil {
		providerError := typedReadError(err, "COLLECTION_BALANCE_RETRY")
		if providerError.Recovery == provider.RecoveryRetryRead {
			return false, nil
		}
		if holdErr := w.repository.RecordCollectionBalanceEvidenceHold(ctx, candidate,
			providerError.Code, providerError.RawEvidence); holdErr != nil {
			return false, holdErr
		}
		return true, nil
	}
	quoter, ok := w.provider.(provider.CollectionFeeQuoter)
	if !ok {
		if err := w.repository.RecordCollectionHold(ctx, candidate,
			"COLLECTION_FEE_CONTRACT_UNAVAILABLE", nil); err != nil {
			return false, err
		}
		return true, nil
	}
	depositRemaining := facts.DepositNet - facts.DepositConsumed
	authorityRemaining := facts.AuthorityLimit - facts.AuthorityUsed - facts.AuthorityReserved
	if depositRemaining <= 0 || authorityRemaining <= 0 || facts.DebtCents <= 0 || balance.AvailableCents <= 0 {
		evidence := postgres.CollectionReadEvidence{Deposit: deposit, Balance: balance,
			Fee: provider.FundingFeeQuote{ContractID: "MOCK_HTTP_ZERO_COLLECTION_FEE_V1"}}
		facts.QuotedAmount = 0
		result, err := w.repository.PlanCollection(ctx, facts, evidence, w.preparer.pricing,
			w.preparer.evaluator.PolicyHash(), w.now)
		return result.Created, err
	}
	quoteAmount := collectionWorkerMin(depositRemaining, authorityRemaining, facts.DebtCents,
		balance.AvailableCents)
	feeQuote, err := quoter.QuoteCollectionFee(readCtx, candidate.AccountRef, facts.FundingAccount, quoteAmount)
	if err != nil || feeQuote.ContractID != "MOCK_HTTP_ZERO_COLLECTION_FEE_V1" || feeQuote.Cents != 0 {
		if err != nil {
			providerError := typedReadError(err, "COLLECTION_FEE_CONTRACT_UNAVAILABLE")
			if providerError.Recovery == provider.RecoveryRetryRead {
				return false, nil
			}
		}
		if holdErr := w.repository.RecordCollectionHold(ctx, candidate,
			"COLLECTION_FEE_CONTRACT_UNAVAILABLE", nil); holdErr != nil {
			return false, holdErr
		}
		return true, nil
	}
	facts.QuotedAmount = quoteAmount
	result, err := w.repository.PlanCollection(ctx, facts,
		postgres.CollectionReadEvidence{Deposit: deposit, Balance: balance, Fee: feeQuote},
		w.preparer.pricing, w.preparer.evaluator.PolicyHash(), w.now)
	return result.Created, err
}

func collectionWorkerMin(values ...int64) int64 {
	if len(values) == 0 {
		return 0
	}
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func (w *Worker) processWebhookJob(ctx context.Context, job postgres.WebhookJobClaim) error {
	switch job.TargetKind {
	case "FUND_REQUEST":
		candidate, err := w.repository.ResolveWebhookOperation(ctx, job, w.cfg.FacilityID)
		if errors.Is(err, pgx.ErrNoRows) {
			return w.repository.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_OPERATION_NOT_FOUND")
		}
		if err != nil {
			return errors.New("webhook_operation_resolution_failed")
		}
		if candidate.State == domain.OperationSettled || candidate.State == domain.OperationFailedConfirmed ||
			candidate.State == domain.OperationCancelledUnsent {
			return w.processTerminalWebhook(ctx, job, candidate)
		}
		claim, claimed, err := w.repository.ClaimWebhookOperation(ctx, job, w.cfg.FacilityID)
		if errors.Is(err, postgres.ErrOperationClaimStale) {
			return nil
		}
		if err != nil {
			return errors.New("webhook_operation_wakeup_failed")
		}
		if !claimed {
			return nil
		}
		return w.processClaim(ctx, claim)
	case "DEPOSIT":
		if w.cfg.Mode != config.ModeDemo || w.cfg.ProviderMode != config.ProviderMock {
			return w.repository.CompleteWebhookJob(ctx, job, "HELD", "LIVE_DEPOSIT_EXTRACTION_UNSUPPORTED")
		}
		scope, err := w.repository.ResolveTrustedDepositScope(ctx, job.Mode, job.CustomerID, job.CardID)
		if err != nil {
			if err.Error() == "webhook_deposit_customer_read_failed" || err.Error() == "webhook_deposit_mapping_read_failed" {
				return w.repository.CompleteWebhookJob(ctx, job, "RETRY", "WEBHOOK_DEPOSIT_MAPPING_RETRY")
			}
			return w.repository.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_DEPOSIT_MAPPING_BLOCKED")
		}
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		observation, err := w.provider.Deposit(readCtx, scope.AccountRef, job.DepositID)
		if err != nil {
			providerError := typedReadError(err, "PROVIDER_DEPOSIT_RETRY")
			if providerError.Recovery == provider.RecoveryRetryRead {
				return w.repository.CompleteWebhookJob(ctx, job, "RETRY", providerError.Code)
			}
			return w.repository.CompleteWebhookJob(ctx, job, "HELD", providerError.Code)
		}
		if err := w.repository.StoreVerifiedDepositWebhook(ctx, job, w.cfg.FacilityID, scope,
			observation, w.now); err != nil {
			if errors.Is(err, postgres.ErrOperationClaimStale) {
				return nil
			}
			return errors.New("webhook_deposit_store_failed")
		}
		return nil
	case "UNSUPPORTED":
		return w.repository.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_EVENT_UNSUPPORTED")
	default:
		return w.repository.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_TARGET_UNSUPPORTED")
	}
}

func (w *Worker) processTerminalWebhook(ctx context.Context, job postgres.WebhookJobClaim,
	candidate postgres.OperationCandidate) error {
	intent, err := w.repository.LoadTerminalOperationReadIntent(ctx, candidate)
	if errors.Is(err, postgres.ErrOperationClaimStale) {
		return w.repository.CompleteWebhookJob(ctx, job, "DONE", "WEBHOOK_OPERATION_CHANGED")
	}
	if err != nil {
		return w.repository.CompleteWebhookJob(ctx, job, "HELD", "WEBHOOK_TERMINAL_SCOPE_UNAVAILABLE")
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var outcome postgres.OperationReadOutcome
	if intent.ProviderObjectID != nil && *intent.ProviderObjectID != "" {
		result, readErr := w.provider.Retrieve(readCtx, intent.Operation, *intent.ProviderObjectID)
		outcome = postgres.OperationReadOutcome{Source: "RETRIEVE"}
		if readErr != nil {
			outcome.ProviderError = typedReadError(readErr, "PROVIDER_RETRIEVAL_RETRY")
		} else if result.Receipt.Status == "UNKNOWN" && result.Recovery == provider.RecoveryLookupOnly {
			outcome.ProviderError = &provider.ProviderError{Code: "PROVIDER_RETRIEVAL_LOOKUP_REQUIRED",
				Recovery: provider.RecoveryLookupOnly, Cause: provider.RecoveryRetryRead,
				Certainty:   provider.CertaintyUncertain,
				RawEvidence: append([]byte(nil), result.Receipt.RawEvidence...)}
		} else if result.Receipt.Status == "REVERSED" &&
			(candidate.Kind == "COLLECT_FEE" || candidate.Kind == "COLLECT_PRINCIPAL") {
			outcome.Result = &result
			if reversalReader, ok := w.provider.(provider.CollectionReversalReader); ok {
				reversal, reversalErr := reversalReader.NormalizeCollectionReversal(intent.Operation, result.Receipt)
				if reversalErr == nil {
					outcome.CollectionReversal = &reversal
				}
			}
		} else {
			outcome.Result = &result
		}
	} else {
		lookup, readErr := w.provider.LookupByRequest(readCtx, intent.Operation)
		outcome = postgres.OperationReadOutcome{Source: "LOOKUP", Lookup: &lookup}
		if readErr != nil {
			outcome.ProviderError = typedReadError(readErr, "PROVIDER_LOOKUP_RETRY")
		}
	}
	if err := w.repository.RecordTerminalWebhookRead(ctx, job, candidate, outcome, w.now,
		w.preparer.pricing, w.preparer.evaluator.PolicyHash()); err != nil {
		if errors.Is(err, postgres.ErrOperationClaimStale) {
			return w.repository.CompleteWebhookJob(ctx, job, "DONE", "WEBHOOK_OPERATION_CHANGED")
		}
		return errors.New("terminal_webhook_reconcile_failed_" + err.Error())
	}
	return nil
}

func (w *Worker) processClaim(ctx context.Context, claim postgres.OperationClaim) error {
	if claim.State == domain.OperationQueued {
		return w.processFirstSend(ctx, claim)
	}
	if !claim.LookupOnly {
		return errors.New("nonqueued_operation_claim_not_lookup_only")
	}
	intent, err := w.repository.LoadClaimedReadIntent(ctx, claim)
	if errors.Is(err, postgres.ErrOperationClaimStale) {
		return nil
	}
	if err != nil {
		return errors.New("operation_read_intent_unavailable")
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var outcome postgres.OperationReadOutcome
	lookupRequired := intent.ProviderStatus != nil && *intent.ProviderStatus == "LOOKUP_REQUIRED"
	if intent.ProviderObjectID != nil && *intent.ProviderObjectID != "" && !lookupRequired {
		result, readErr := w.provider.Retrieve(readCtx, intent.Operation, *intent.ProviderObjectID)
		outcome = postgres.OperationReadOutcome{Source: "RETRIEVE"}
		if readErr != nil {
			outcome.ProviderError = typedReadError(readErr, "PROVIDER_RETRIEVAL_RETRY")
		} else if result.Receipt.Status == "UNKNOWN" && result.Recovery == provider.RecoveryLookupOnly {
			outcome.ProviderError = &provider.ProviderError{Code: "PROVIDER_RETRIEVAL_LOOKUP_REQUIRED",
				Recovery: provider.RecoveryLookupOnly, Cause: provider.RecoveryRetryRead,
				Certainty:   provider.CertaintyUncertain,
				RawEvidence: append([]byte(nil), result.Receipt.RawEvidence...)}
		} else {
			outcome.Result = &result
		}
	} else {
		lookup, readErr := w.provider.LookupByRequest(readCtx, intent.Operation)
		outcome = postgres.OperationReadOutcome{Source: "LOOKUP"}
		if readErr != nil {
			outcome.Lookup = &lookup
			outcome.ProviderError = typedReadError(readErr, "PROVIDER_LOOKUP_RETRY")
		} else {
			outcome.Lookup = &lookup
		}
	}
	return w.finishRead(ctx, claim, outcome)
}

func (w *Worker) processFirstSend(ctx context.Context, claim postgres.OperationClaim) error {
	if w.cfg.Mode != config.ModeDemo || w.cfg.ProviderMode != config.ProviderMock {
		return w.finishRead(ctx, claim, postgres.OperationReadOutcome{Source: "GUARD",
			NoDispatchCode: "SANDBOX_CREATE_DISABLED"})
	}
	var prepared postgres.DispatchPreparation
	var err error
	if claim.Kind == "FUND" {
		prepared, err = w.preparer.AuthorizeFirstSend(ctx, claim)
	} else {
		prepared, err = w.preparer.AuthorizeCollectionFirstSend(ctx, claim)
	}
	if errors.Is(err, postgres.ErrOperationClaimStale) {
		return nil
	}
	if err != nil {
		return errors.New("first_send_prepare_failed")
	}
	if prepared.Intent == nil {
		return nil
	}
	if err := w.repository.CheckPreparedDispatchLease(ctx, *prepared.Intent); err != nil {
		// Do not call Create if the committed lease no longer has a full bounded
		// request window. Its expired DISPATCHING state will recover by lookup.
		return nil
	}
	createCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, createErr := w.provider.Create(createCtx, prepared.Intent.Operation)
	outcome := postgres.OperationReadOutcome{Source: "CREATE"}
	if createErr != nil {
		outcome.ProviderError = typedCreateError(createErr, result)
	} else {
		outcome.Result = &result
	}
	return w.finishRead(ctx, prepared.Intent.Claim, outcome)
}

func (w *Worker) finishRead(ctx context.Context, claim postgres.OperationClaim,
	outcome postgres.OperationReadOutcome) error {
	var err error
	if claim.Kind == "FUND" {
		err = w.repository.FinishFundingOperation(ctx, claim, outcome, w.now,
			w.preparer.pricing, w.preparer.evaluator.PolicyHash())
	} else if claim.Kind == "COLLECT_FEE" || claim.Kind == "COLLECT_PRINCIPAL" {
		err = w.repository.FinishCollectionOperation(ctx, claim, outcome, w.now,
			w.preparer.pricing, w.preparer.evaluator.PolicyHash())
	} else {
		err = w.repository.FinishOperation(ctx, claim, outcome)
	}
	if err != nil {
		if errors.Is(err, postgres.ErrOperationClaimStale) {
			return nil
		}
		return errors.New("operation_finish_failed")
	}
	return nil
}

func typedReadError(err error, fallback string) *provider.ProviderError {
	var providerError *provider.ProviderError
	if errors.As(err, &providerError) {
		copy := *providerError
		copy.RawEvidence = append([]byte(nil), providerError.RawEvidence...)
		return &copy
	}
	if errors.Is(err, provider.ErrProviderUnavailable) {
		return &provider.ProviderError{Code: fallback, Recovery: provider.RecoveryHold,
			Cause: provider.RecoveryHold, Certainty: provider.CertaintyUncertain}
	}
	if errors.Is(err, provider.ErrFundingExecutionDisabled) || err.Error() == "provider_retrieval_binding_invalid" ||
		err.Error() == "provider_lookup_binding_invalid" || err.Error() == "provider_operation_mode_mismatch" ||
		err.Error() == "provider_payload_hash_mismatch" {
		return &provider.ProviderError{Code: "PROVIDER_READ_BINDING_HOLD", Recovery: provider.RecoveryHold,
			Cause: provider.RecoveryHold, Certainty: provider.CertaintyUncertain}
	}
	return &provider.ProviderError{Code: fallback, Recovery: provider.RecoveryRetryRead,
		Cause: provider.RecoveryRetryRead, Certainty: provider.CertaintyUncertain}
}

func typedCreateError(err error, result provider.Result) *provider.ProviderError {
	var providerError *provider.ProviderError
	if errors.As(err, &providerError) {
		copy := *providerError
		copy.RawEvidence = append([]byte(nil), providerError.RawEvidence...)
		return &copy
	}
	if errors.Is(err, provider.ErrFundingExecutionDisabled) || result.Recovery == provider.RecoveryHold ||
		err.Error() == "provider_payload_hash_mismatch" || err.Error() == "provider_operation_invalid" {
		return &provider.ProviderError{Code: "PROVIDER_CREATE_BINDING_HOLD", Recovery: provider.RecoveryHold,
			Cause: provider.RecoveryHold, Certainty: provider.CertaintyDefinitive}
	}
	return &provider.ProviderError{Code: "PROVIDER_CREATE_UNCERTAIN", Recovery: provider.RecoveryLookupOnly,
		Cause: provider.RecoveryRetryRead, Certainty: provider.CertaintyUncertain}
}
