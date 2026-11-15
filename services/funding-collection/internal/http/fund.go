package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
)

type fundOfferRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type fundOfferSource struct {
	id, facilityID, customerID, accountID, transactionID string
	authorizationID, evaluationID, policyID, policyHash  string
	termsHash                                            string
	termsJSON                                            []byte
}

type commandRowReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (h *handler) fundOffer(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.auth(w, r, true)
	if !ok {
		return
	}
	if identity.Role != "FUNDING_COORDINATOR" && (identity.Role != "ACTOR" || identity.ActorType != "OPERATOR") {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "Operator or funding coordinator scope is required")
		return
	}
	offerID := r.PathValue("id")
	if !validUUID(offerID) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Offer ID is invalid")
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !idempotencyPattern.MatchString(keys[0]) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key is required")
		return
	}
	var command fundOfferRequest
	if err := decodeCommand(r, &command); err != nil {
		writeDecodeError(w, err)
		return
	}
	if command.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Expected version is invalid")
		return
	}
	canonicalBody, _ := json.Marshal(command)
	requestDigest := sha256.Sum256(canonicalBody)
	requestHash := hex.EncodeToString(requestDigest[:])
	route := r.Method + " " + r.URL.Path
	base := postgres.FundingGuard{Mode: h.cfg.Mode, FacilityID: h.cfg.FacilityID, OfferID: offerID,
		ExpectedVersion: command.ExpectedVersion, Principal: identity.Subject, HTTPMethod: r.Method,
		ResourcePath: route, IdempotencyKey: keys[0], RequestHash: requestHash, Evaluator: h.evaluator}

	// An optimistic receipt read makes exact retries cheap without holding a lock during HTTP reads.
	oldStatus, oldBody, exists, err := readFundingReceipt(r.Context(), h.pool, base)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Command receipt could not be read")
		return
	}
	if exists {
		if oldStatus == -1 {
			writeError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return
		}
		writeFundingResponse(w, oldStatus, oldBody, true)
		return
	}

	// The pricing accessor and provider fee contract are explicit dependencies.
	if h.evaluator == nil || h.policy == nil || !h.policy.Verified || h.policy.ID != h.evaluator.PolicyID() ||
		h.policy.Hash != h.evaluator.PolicyHash() || h.policy.Pricing.Validate() != nil {
		writeError(w, http.StatusServiceUnavailable, "POLICY_PRICING_BINDING_UNAVAILABLE", "Reviewed policy pricing binding is unavailable")
		return
	}
	if h.provider == nil {
		writeError(w, http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE", "Provider reads are unavailable")
		return
	}
	source, err := readFundingOfferSource(r.Context(), h.pool, h.cfg.Mode, offerID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.persistFundingPrecheckBlock(w, r.Context(), base, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Offer not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Funding offer could not be read")
		return
	}
	if source.facilityID != h.cfg.FacilityID {
		retireRequest := base
		retireRequest.FacilityID = source.facilityID
		h.persistFundingPrecheckSupersede(w, r.Context(), retireRequest, "FACILITY_BINDING_MISMATCH", "Offer facility differs from the configured facility")
		return
	}
	var terms domain.Terms
	if json.Unmarshal(source.termsJSON, &terms) != nil || terms.Validate() != nil {
		h.persistFundingPrecheckSupersede(w, r.Context(), base, "TERMS_BINDING_MISMATCH", "Stored offer terms are invalid")
		return
	}
	shortfalls, err := upstream.NewShortfallClientForModeWithClock(h.cfg.ShortfallURL, h.cfg.ShortfallSecret, h.cfg.Mode, h.now)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Shortfall reader is unavailable")
		return
	}
	underwritingClient, err := upstream.NewUnderwritingClientWithClock(h.cfg.UnderwritingURL, h.cfg.UnderwritingSecret, h.now)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Underwriting reader is unavailable")
		return
	}
	assessment, err := underwritingClient.ReadEvaluation(r.Context(), source.evaluationID, h.cfg.Mode)
	if err != nil {
		h.handleFundingReadError(w, r.Context(), base, err, "Saved assessment is unavailable")
		return
	}
	if assessment.ID != source.evaluationID || assessment.PolicyID != source.policyID || assessment.PolicyHash != source.policyHash ||
		assessment.PolicyID != h.evaluator.PolicyID() || assessment.PolicyHash != h.evaluator.PolicyHash() ||
		assessment.CustomerID != source.customerID || assessment.AccountID != source.accountID ||
		assessment.TransactionID != source.transactionID || !validOfferAssessment(assessment) ||
		assessment.PrincipalCents == nil || *assessment.PrincipalCents != terms.PrincipalCents ||
		assessment.TermDays == nil || *assessment.TermDays != terms.TermDays ||
		assessment.EstimatedFeeCents == nil || *assessment.EstimatedFeeCents != terms.ContractFeeCents {
		h.persistFundingPrecheckSupersede(w, r.Context(), base, "CANDIDATE_ASSESSMENT_INCOMPATIBLE", "Saved assessment differs from the accepted candidate")
		return
	}
	contextFacts, err := underwritingClient.ReadCustomerContext(r.Context(), source.customerID, h.cfg.Mode)
	if err != nil {
		h.handleFundingReadError(w, r.Context(), base, err, "Customer context is unavailable")
		return
	}
	if contextFacts.PolicyID != source.policyID || contextFacts.PolicyHash != source.policyHash ||
		contextFacts.PolicyID != h.evaluator.PolicyID() || contextFacts.PolicyHash != h.evaluator.PolicyHash() {
		h.persistFundingPrecheckSupersede(w, r.Context(), base, "CUSTOMER_POLICY_BINDING_CHANGED", "Current context policy differs from accepted terms")
		return
	}
	if !contextFacts.Fresh {
		h.persistFundingPrecheckBlock(w, r.Context(), base, http.StatusUnprocessableEntity, "INPUT_STALE", "Current customer context is stale")
		return
	}
	var contextScope struct {
		AccountID string `json:"account_id"`
		CardID    string `json:"card_id"`
	}
	if json.Unmarshal(contextFacts.Data, &contextScope) == nil &&
		((contextScope.AccountID != "" && contextScope.AccountID != source.accountID) ||
			(contextScope.CardID != "" && contextScope.CardID != terms.CardRef)) {
		h.persistFundingPrecheckSupersede(w, r.Context(), base, "MAPPING_CONFLICT", "Current context mapping differs from accepted terms")
		return
	}
	customerSnapshot, err := h.evaluator.DecodeCustomerContext(contextFacts, h.cfg.Mode, source.customerID, source.accountID, terms.CardRef, h.now().UTC())
	if err != nil {
		h.persistFundingPrecheckBlock(w, r.Context(), base, http.StatusUnprocessableEntity, "CUSTOMER_CONTEXT_INVALID", "Normalized customer context failed typed validation")
		return
	}
	savedShortfall, err := shortfalls.ReadForMode(r.Context(), assessment.ShortfallID, h.cfg.Mode)
	if err != nil {
		h.handleFundingReadError(w, r.Context(), base, err, "Shortfall observation is unavailable")
		return
	}
	var fundingAccount string
	if err := h.pool.QueryRow(r.Context(), `SELECT funding_account_ref FROM servicing.facilities WHERE mode=$1 AND id=$2`, h.cfg.Mode, source.facilityID).Scan(&fundingAccount); err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Funding facility is unavailable")
		return
	}
	quoter, ok := h.provider.(provider.FundingFeeQuoter)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "SOURCE_FEE_CONTRACT_UNAVAILABLE", "Funding transfer fee contract is unavailable")
		return
	}
	feeQuote, err := quoter.QuoteFundingFee(r.Context(), fundingAccount, source.accountID, terms.PrincipalCents)
	if err != nil || feeQuote.Cents < 0 || feeQuote.ContractID == "" {
		writeError(w, http.StatusServiceUnavailable, "SOURCE_FEE_CONTRACT_UNAVAILABLE", "Funding transfer fee contract is unavailable")
		return
	}
	payment, err := h.provider.Payment(r.Context(), source.accountID, source.transactionID)
	if err != nil {
		h.writeFundingProviderError(w, r.Context(), base, err)
		return
	}
	borrowerBalance, err := h.provider.Balance(r.Context(), source.accountID)
	if err != nil {
		h.writeFundingProviderError(w, r.Context(), base, err)
		return
	}
	facilityBalance, err := h.provider.Balance(r.Context(), fundingAccount)
	if err != nil {
		h.writeFundingProviderError(w, r.Context(), base, err)
		return
	}
	base.SavedShortfall = savedShortfall
	base.Assessment = assessment
	base.Payment = payment
	base.BorrowerBalance = borrowerBalance
	base.FacilityBalance = facilityBalance
	base.KnownSourceFeeCents = &feeQuote.Cents
	base.SourceFeeContractID = feeQuote.ContractID
	base.MockFeeContract = feeQuote.ContractID == "MOCK_HTTP_ZERO_FUNDING_FEE_V1" && h.cfg.ProviderMode == config.ProviderMock
	base.CustomerContext = contextFacts
	base.CustomerSnapshot = customerSnapshot
	base.Clock = h.now
	base.Pricing = h.policy.Pricing
	base.PricingReady = h.policy.Verified

	// The short command transaction serializes receipt creation, then acquires financial locks.
	txCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	conn, err := h.pool.Acquire(txCtx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Funding transaction could not start")
		return
	}
	defer conn.Release()
	tx, err := conn.BeginTx(txCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Funding transaction could not start")
		return
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	lockName := base.Mode + "|" + base.Principal + "|" + base.HTTPMethod + "|" + base.ResourcePath + "|" + base.IdempotencyKey
	if _, err := tx.Exec(txCtx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockName); err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Command serialization is unavailable")
		return
	}
	oldStatus, oldBody, exists, err = readFundingReceipt(txCtx, tx, base)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Command receipt could not be read")
		return
	}
	if exists {
		if oldStatus == -1 {
			writeError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return
		}
		if err = tx.Commit(txCtx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Command receipt could not be committed")
			return
		}
		writeFundingResponse(w, oldStatus, oldBody, true)
		return
	}
	outcome, err := postgres.ReserveFundingTx(txCtx, tx, base)
	if err != nil {
		if err.Error() == "funding_pricing_binding_unavailable" {
			writeError(w, http.StatusServiceUnavailable, "POLICY_PRICING_BINDING_UNAVAILABLE", "Reviewed policy pricing binding is unavailable")
			return
		}
		if err.Error() == "funding_fresh_evidence_stale" {
			writeError(w, http.StatusServiceUnavailable, "PROVIDER_EVIDENCE_STALE", "Fresh provider observations are unavailable")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Funding recheck could not be saved")
		return
	}
	if err := tx.Commit(txCtx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Funding transaction could not commit")
		return
	}
	writeFundingResponse(w, outcome.Status, outcome.Body, false)
}

func readFundingOfferSource(ctx context.Context, pool *pgxpool.Pool, mode, offerID string) (fundOfferSource, error) {
	var source fundOfferSource
	err := pool.QueryRow(ctx, `SELECT id::text,facility_id,customer_id,canonical_account_id,transaction_id,
		authorization_event_id,evaluation_id,policy_id,policy_hash,terms_hash,terms_json
		FROM servicing.offers WHERE mode=$1 AND id=$2`, mode, offerID).Scan(&source.id, &source.facilityID,
		&source.customerID, &source.accountID, &source.transactionID, &source.authorizationID,
		&source.evaluationID, &source.policyID, &source.policyHash, &source.termsHash, &source.termsJSON)
	return source, err
}

func readFundingReceipt(ctx context.Context, reader commandRowReader, request postgres.FundingGuard) (int, []byte, bool, error) {
	var oldHash string
	var status int
	var body []byte
	err := reader.QueryRow(ctx, `SELECT request_hash,response_status,response_body FROM servicing.commands
		WHERE mode=$1 AND principal=$2 AND http_method=$3 AND resource_path=$4 AND idempotency_key=$5`,
		request.Mode, request.Principal, request.HTTPMethod, request.ResourcePath, request.IdempotencyKey).Scan(&oldHash, &status, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	if oldHash != request.RequestHash {
		return -1, nil, true, nil
	}
	return status, body, true, nil
}

func (h *handler) persistFundingPrecheckBlock(w http.ResponseWriter, ctx context.Context, request postgres.FundingGuard,
	status int, code, message string) {
	txCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := h.pool.Acquire(txCtx)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be saved")
		return
	}
	defer conn.Release()
	tx, err := conn.BeginTx(txCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be saved")
		return
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	lockName := request.Mode + "|" + request.Principal + "|" + request.HTTPMethod + "|" + request.ResourcePath + "|" + request.IdempotencyKey
	if _, err := tx.Exec(txCtx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockName); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command serialization is unavailable")
		return
	}
	oldStatus, oldBody, exists, err := readFundingReceipt(txCtx, tx, request)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be read")
		return
	}
	if exists {
		if oldStatus == -1 {
			writeError(w, 409, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return
		}
		if err = tx.Commit(txCtx); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be committed")
			return
		}
		writeFundingResponse(w, oldStatus, oldBody, true)
		return
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	if err := postgres.InsertFundingCommand(txCtx, tx, request, status, body, request.OfferID); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be saved")
		return
	}
	if err := tx.Commit(txCtx); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be committed")
		return
	}
	writeFundingResponse(w, status, body, false)
}

func (h *handler) persistFundingPrecheckSupersede(w http.ResponseWriter, ctx context.Context,
	request postgres.FundingGuard, code, message string) {
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := h.pool.Acquire(txCtx)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Offer retirement could not start")
		return
	}
	defer conn.Release()
	tx, err := conn.BeginTx(txCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Offer retirement could not start")
		return
	}
	defer func() { _ = tx.Rollback(txCtx) }()
	lockName := request.Mode + "|" + request.Principal + "|" + request.HTTPMethod + "|" + request.ResourcePath + "|" + request.IdempotencyKey
	if _, err := tx.Exec(txCtx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockName); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command serialization is unavailable")
		return
	}
	oldStatus, oldBody, exists, err := readFundingReceipt(txCtx, tx, request)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be read")
		return
	}
	if exists {
		if oldStatus == -1 {
			writeError(w, 409, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return
		}
		if err := tx.Commit(txCtx); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be committed")
			return
		}
		writeFundingResponse(w, oldStatus, oldBody, true)
		return
	}
	request.Clock = h.now
	outcome, err := postgres.RetirePrebridgeOfferTx(txCtx, tx, request, code, message)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Offer retirement could not be committed")
		return
	}
	if err := tx.Commit(txCtx); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Offer retirement could not be committed")
		return
	}
	writeFundingResponse(w, outcome.Status, outcome.Body, false)
}

func (h *handler) handleFundingReadError(w http.ResponseWriter, ctx context.Context, request postgres.FundingGuard, err error, message string) {
	switch err.Error() {
	case "underwriting_resource_not_found", "shortfall_not_found":
		h.persistFundingPrecheckSupersede(w, ctx, request, "SOURCE_RECORD_NOT_FOUND", "A source record bound to the offer is no longer available")
	case "underwriting_evaluation_response_invalid", "underwriting_context_response_invalid", "shortfall_response_invalid", "shortfall_amount_semantics_invalid":
		h.persistFundingPrecheckBlock(w, ctx, request, http.StatusUnprocessableEntity, "SOURCE_EVIDENCE_INVALID", "Current source evidence is invalid")
	default:
		writeError(w, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", message)
	}
}

func (h *handler) writeFundingProviderError(w http.ResponseWriter, ctx context.Context, request postgres.FundingGuard, err error) {
	var providerErr *provider.ProviderError
	if errors.As(err, &providerErr) && providerErr.Recovery == provider.RecoveryHold {
		h.persistFundingPrecheckBlock(w, ctx, request, http.StatusUnprocessableEntity, "PROVIDER_READ_HELD", "Fresh provider evidence is incomplete or held")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE", "Provider reads are unavailable")
}

func writeFundingResponse(w http.ResponseWriter, status int, body []byte, replayed bool) {
	if replayed && status == http.StatusAccepted {
		status = http.StatusOK
	}
	var compact bytes.Buffer
	if json.Compact(&compact, body) == nil {
		body = compact.Bytes()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
