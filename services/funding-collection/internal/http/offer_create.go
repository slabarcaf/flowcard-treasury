package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/upstream"
)

type offerSourceRef struct {
	System      string    `json:"system"`
	RecordID    string    `json:"record_id"`
	Version     string    `json:"version"`
	EffectiveAt time.Time `json:"effective_at"`
}
type offerEvidence struct {
	Evaluation      json.RawMessage `json:"evaluation"`
	CustomerContext json.RawMessage `json:"customer_context"`
	Shortfall       json.RawMessage `json:"shortfall"`
}

var underwritingRuleIDs = []string{"INPUT_FRESH", "CUSTOMER_ACTIVE", "HISTORY_SUFFICIENT", "NO_OVERDUE_OR_DEFAULT", "CUSTOMER_CAPACITY", "PAYMENT_NOT_BRIDGED", "RECEIVABLE_COVERAGE", "CAPITAL_AVAILABLE", "SPEND_ALLOWED", "REGIME_TIER_ALLOWED"}

func (h *handler) createOfferFromSavedEvidence(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.auth(w, r, true)
	if !ok {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !idempotencyPattern.MatchString(keys[0]) {
		writeError(w, 400, "INVALID_REQUEST", "Idempotency-Key is required")
		return
	}
	var req createOfferRequest
	if err := decodeCommand(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if req.EvaluationID == "" {
		writeError(w, 400, "INVALID_REQUEST", "Request is invalid")
		return
	}
	caseID := ""
	if req.OriginatingCaseID != nil {
		if !validUUID(*req.OriginatingCaseID) {
			writeError(w, 400, "INVALID_REQUEST", "Originating case ID is invalid")
			return
		}
		caseID = *req.OriginatingCaseID
	}
	fingerprint := struct {
		EvaluationID      string  `json:"evaluation_id"`
		OriginatingCaseID *string `json:"originating_case_id,omitempty"`
	}{EvaluationID: req.EvaluationID, OriginatingCaseID: req.OriginatingCaseID}
	body, _ := json.Marshal(fingerprint)
	hash := sha256.Sum256(body)
	requestHash := hex.EncodeToString(hash[:])
	route := r.Method + " " + r.URL.Path
	var oldHash string
	var oldStatus int
	var oldBody []byte
	err := h.pool.QueryRow(r.Context(), `SELECT request_hash,response_status,response_body FROM servicing.commands WHERE mode=$1 AND principal=$2 AND http_method=$3 AND resource_path=$4 AND idempotency_key=$5`, h.cfg.Mode, identity.Subject, r.Method, route, keys[0]).Scan(&oldHash, &oldStatus, &oldBody)
	if err == nil {
		if oldHash != requestHash {
			writeError(w, 409, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		status := oldStatus
		if status == 201 || status == 202 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write(oldBody)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	binding := h.policy
	if binding == nil || !binding.Verified || binding.ID == "" || binding.Hash == "" || binding.Pricing.Validate() != nil {
		writeError(w, 503, "POLICY_BINDING_UNAVAILABLE", "Reviewed policy binding is unavailable")
		return
	}
	underwriting, err := upstream.NewUnderwritingClientWithClock(h.cfg.UnderwritingURL, h.cfg.UnderwritingSecret, h.now)
	if err != nil {
		writeError(w, 503, "UPSTREAM_UNAVAILABLE", "Underwriting is unavailable")
		return
	}
	assessment, err := underwriting.ReadEvaluation(r.Context(), req.EvaluationID, h.cfg.Mode)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if assessment.PolicyID != binding.ID || assessment.PolicyHash != binding.Hash {
		h.rejectOffer(w, r, identity, keys[0], requestHash, route, 422, "INPUT_STALE", "Assessment policy binding differs")
		return
	}
	if !validOfferAssessment(assessment) {
		h.rejectOffer(w, r, identity, keys[0], requestHash, route, 422, "HARD_RULE_FAILED", "Assessment cannot produce an offer")
		return
	}
	contextFacts, err := underwriting.ReadCustomerContext(r.Context(), assessment.CustomerID, h.cfg.Mode)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if contextFacts.PolicyID != binding.ID || contextFacts.PolicyHash != binding.Hash || !contextFacts.Fresh {
		h.rejectOffer(w, r, identity, keys[0], requestHash, route, 422, "INPUT_STALE", "Customer context is stale or policy differs")
		return
	}
	shortfalls, err := upstream.NewShortfallClientForModeWithClock(h.cfg.ShortfallURL, h.cfg.ShortfallSecret, h.cfg.Mode, h.now)
	if err != nil {
		writeError(w, 503, "UPSTREAM_UNAVAILABLE", "Shortfall source is unavailable")
		return
	}
	shortfall, err := shortfalls.ReadForMode(r.Context(), assessment.ShortfallID, h.cfg.Mode)
	if err != nil {
		upstreamError(w, err)
		return
	}
	now := h.now().UTC()
	if shortfall.CustomerID != assessment.CustomerID || shortfall.AccountID != assessment.AccountID || shortfall.TransactionID == nil || *shortfall.TransactionID != assessment.TransactionID || assessment.PrincipalCents == nil || assessment.TermDays == nil || shortfall.ShortfallCents == nil || *assessment.PrincipalCents != *shortfall.ShortfallCents || !shortfall.RequiresRecheck || shortfall.AmountSemantics != "current_need_at_balance_observation" || !freshTime(shortfall.BalanceObservedAt, now, 5*time.Minute) || !freshInstant(shortfall.CreatedAt, now, 5*time.Minute) || shortfall.BalanceRequestedAt == nil || shortfall.BalanceObservedAt == nil || shortfall.BalanceRequestedAt.After(*shortfall.BalanceObservedAt) || !utcInstant(*shortfall.BalanceRequestedAt) || !utcInstant(*shortfall.BalanceObservedAt) || !shortfallAmountConsistent(shortfall) {
		h.rejectOffer(w, r, identity, keys[0], requestHash, route, 422, "INPUT_STALE", "Saved shortfall evidence is stale or mismatched")
		return
	}
	principal := domain.Money(*assessment.PrincipalCents)
	fee, total, err := binding.Pricing.TotalDue(principal, *assessment.TermDays)
	if err != nil || assessment.EstimatedFeeCents == nil || assessment.TotalDueCents == nil || int64(fee) != *assessment.EstimatedFeeCents || int64(total) != *assessment.TotalDueCents {
		h.rejectOffer(w, r, identity, keys[0], requestHash, route, 422, "HARD_RULE_FAILED", "Saved pricing does not match configured terms")
		return
	}
	terms, termsHash, authorityID, authorityVersion, sourceRefs, err := h.prepareOfferTerms(r.Context(), binding, assessment, contextFacts, shortfall, fee, now)
	if err != nil {
		if status, code, message, deterministic := deterministicOfferError(err); deterministic {
			h.rejectOffer(w, r, identity, keys[0], requestHash, route, status, code, message)
		} else {
			offerError(w, err)
		}
		return
	}
	evidence, _ := json.Marshal(offerEvidence{Evaluation: assessment.Raw, CustomerContext: contextFacts.Raw, Shortfall: shortfall.Raw})
	result, err := h.persistOffer(r.Context(), identity, keys[0], requestHash, route, req.EvaluationID, caseID, assessment, shortfall, terms, termsHash, authorityID, authorityVersion, sourceRefs, evidence, now)
	if err != nil {
		if status, code, message, deterministic := deterministicOfferError(err); deterministic {
			h.rejectOffer(w, r, identity, keys[0], requestHash, route, status, code, message)
		} else {
			offerError(w, err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	status := 201
	if result.Replayed {
		status = 200
	}
	if result.ReplayBody != nil {
		status = result.ReplayStatus
		w.WriteHeader(status)
		_, _ = w.Write(result.ReplayBody)
		return
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func (h *handler) rejectOffer(w http.ResponseWriter, r *http.Request, id Identity, key, requestHash, route string, status int, code, message string) {
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockName := h.cfg.Mode + "|" + id.Subject + "|POST|" + route + "|" + key
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockName); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	var oldHash string
	var oldStatus int
	var oldBody []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_status,response_body FROM servicing.commands WHERE mode=$1 AND principal=$2 AND http_method='POST' AND resource_path=$3 AND idempotency_key=$4`, h.cfg.Mode, id.Subject, route, key).Scan(&oldHash, &oldStatus, &oldBody)
	if err == nil {
		if oldHash != requestHash {
			writeError(w, 409, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return
		}
		if tx.Commit(ctx) != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if oldStatus == 201 || oldStatus == 202 {
			oldStatus = 200
		}
		w.WriteHeader(oldStatus)
		_, _ = w.Write(oldBody)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	if _, err = tx.Exec(ctx, `INSERT INTO servicing.commands(mode,principal,http_method,resource_path,idempotency_key,request_hash,response_status,response_body) VALUES($1,$2,'POST',$3,$4,$5,$6,$7::jsonb)`, h.cfg.Mode, id.Subject, route, key, requestHash, status, string(body)); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	if tx.Commit(ctx) != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func deterministicOfferError(err error) (int, string, string, bool) {
	switch err.Error() {
	case "offer_mapping_conflict":
		return 422, "MAPPING_CONFLICT", "Current mapping does not match", true
	case "collection_authority_blocked":
		return 422, "COLLECTION_AUTHORITY_BLOCKED", "Collection authority is unavailable", true
	case "payment_already_offered":
		return 422, "HARD_RULE_FAILED", "Payment already has an active offer or bridge", true
	case "offer_midnight_superseded":
		return 422, "TERMS_SUPERSEDED", "Offer date changed before creation", true
	default:
		return 0, "", "", false
	}
}

func validOfferAssessment(a upstream.SavedEvaluation) bool {
	if a.Status != "ELIGIBLE" && a.Status != "REVIEW_REQUIRED" {
		return false
	}
	seen := map[string]string{}
	for _, rule := range a.Rules {
		if _, ok := seen[rule.RuleID]; ok {
			return false
		}
		seen[rule.RuleID] = rule.Status
	}
	if len(seen) != len(underwritingRuleIDs) {
		return false
	}
	for _, id := range underwritingRuleIDs {
		if seen[id] != "PASS" {
			return false
		}
	}
	if a.Status == "ELIGIBLE" {
		return a.ApprovalRequired != nil && !*a.ApprovalRequired
	}
	if a.ApprovalRequired == nil || !*a.ApprovalRequired || len(a.ReasonCodes) != 1 || a.ReasonCodes[0] != "HUMAN_APPROVAL_REQUIRED" {
		return false
	}
	return true
}

func (h *handler) prepareOfferTerms(ctx context.Context, binding *PolicyBinding, a upstream.SavedEvaluation, c upstream.CustomerContext, s upstream.ShortfallObservation, fee domain.Money, now time.Time) (domain.Terms, string, string, int64, []byte, error) {
	if s.CardID == nil {
		return domain.Terms{}, "", "", 0, nil, errors.New("offer_mapping_missing")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return domain.Terms{}, "", "", 0, nil, errors.New("storage_unavailable")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var fundingRef string
	if err = tx.QueryRow(ctx, `SELECT funding_account_ref FROM servicing.facilities WHERE mode=$1 AND id=$2 FOR UPDATE`, h.cfg.Mode, h.cfg.FacilityID).Scan(&fundingRef); err != nil {
		return domain.Terms{}, "", "", 0, nil, errors.New("facility_unavailable")
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM servicing.customers WHERE mode=$1 AND id=$2 FOR UPDATE`, h.cfg.Mode, a.CustomerID); err != nil {
		return domain.Terms{}, "", "", 0, nil, errors.New("customer_unavailable")
	}
	mapping, fingerprint, err := postgres.LockVerifiedMapping(ctx, tx, a.CustomerID, a.AccountID, *s.CardID)
	if err != nil {
		return domain.Terms{}, "", "", 0, nil, errors.New("offer_mapping_conflict")
	}
	authority, authErr := currentAuthority(ctx, tx, h.cfg.Mode, a.CustomerID, mapping.RosterAccountID, fundingRef, now)
	if authErr != nil {
		return domain.Terms{}, "", "", 0, nil, authErr
	}
	authID, authVersion, disclosures := authority.ID, authority.Version, authority.DisclosuresVersion
	if err = tx.Commit(ctx); err != nil {
		return domain.Terms{}, "", "", 0, nil, errors.New("storage_unavailable")
	}
	offerID, err := newUUID()
	if err != nil {
		return domain.Terms{}, "", "", 0, nil, errors.New("offer_identity_failed")
	}
	offered := now.Format("2006-01-02")
	due := now.AddDate(0, 0, *a.TermDays).Format("2006-01-02")
	expiry := now.Add(10 * time.Minute).Truncate(time.Second).Format("2006-01-02T15:04:05Z")
	terms := domain.Terms{TermsVersion: 1, SourceMode: h.cfg.Mode, FacilityID: h.cfg.FacilityID, FundingAccountRef: fundingRef, FeeBeneficiaryRef: fundingRef, CustomerID: a.CustomerID, BorrowerAccountRef: mapping.RosterAccountID, CardRef: mapping.CardID, TransactionID: a.TransactionID, OfferID: offerID, OfferRevision: 1, PrincipalCents: *a.PrincipalCents, ContractFeeCents: int64(fee), TermDays: *a.TermDays, OfferedOn: offered, DueOn: due, ExpiresAt: expiry, PolicyID: binding.ID, PolicyHash: binding.Hash, DisclosuresVersion: disclosures, WaterfallVersion: 1, LateFeeConvention: "OPENING_PRINCIPAL_UTC_DAY", CollectionAuthorityID: authID, CollectionAuthorityVersion: authVersion, MappingFingerprint: fingerprint}
	termsHash, err := terms.Hash()
	if err != nil {
		return domain.Terms{}, "", "", 0, nil, err
	}
	evalSnapshotHash := sha256.Sum256(a.Raw)
	shortfallSnapshotHash := sha256.Sum256(s.Raw)
	refsList := []offerSourceRef{{System: "underwriting-evaluation", RecordID: a.ID, Version: hex.EncodeToString(evalSnapshotHash[:]), EffectiveAt: a.EvaluatedAt}, {System: "underwriting-context", RecordID: c.SnapshotID, Version: fmt.Sprint(c.SnapshotVersion), EffectiveAt: c.CapturedAt}, {System: "shortfall-detection", RecordID: s.ID, Version: hex.EncodeToString(shortfallSnapshotHash[:]), EffectiveAt: *s.BalanceObservedAt}}
	var contextRefs []domain.SourceRef
	if json.Unmarshal(c.SourceRefs, &contextRefs) == nil {
		for _, ref := range contextRefs {
			refsList = append(refsList, offerSourceRef{System: ref.System, RecordID: ref.RecordID, Version: ref.Version, EffectiveAt: ref.EffectiveAt})
		}
	}
	refs, _ := json.Marshal(refsList)
	return terms, termsHash, authID, authVersion, refs, nil
}

type authorityBinding struct {
	ID                 string
	Version            int64
	Source             string
	Destination        string
	DisclosuresVersion string
}

func currentAuthority(ctx context.Context, tx pgx.Tx, mode, customerID, borrowerAccount, fundingAccount string, now time.Time) (authorityBinding, error) {
	rows, err := tx.Query(ctx, `SELECT id,version,source_account_ref,destination_account_ref,currency,disclosures_version,status,valid_from,expires_at,permitted_kinds FROM servicing.authorities WHERE mode=$1 AND customer_id=$2 ORDER BY id,version DESC FOR SHARE`, mode, customerID)
	if err != nil {
		return authorityBinding{}, errors.New("collection_authority_blocked")
	}
	defer rows.Close()
	seen := map[string]bool{}
	var selected authorityBinding
	matches := 0
	for rows.Next() {
		var id, source, destination, currency, disclosure, status string
		var version int64
		var validFrom, expires time.Time
		var kinds []string
		if rows.Scan(&id, &version, &source, &destination, &currency, &disclosure, &status, &validFrom, &expires, &kinds) != nil {
			return authorityBinding{}, errors.New("collection_authority_blocked")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if status != "ACTIVE" || validFrom.After(now) || !now.Before(expires) {
			continue
		}
		if currency != "USD" || source != borrowerAccount || destination != fundingAccount || !containsString(kinds, "COLLECT_FEE") || !containsString(kinds, "COLLECT_PRINCIPAL") {
			return authorityBinding{}, errors.New("collection_authority_blocked")
		}
		matches++
		selected = authorityBinding{ID: id, Version: version, Source: source, Destination: destination, DisclosuresVersion: disclosure}
	}
	if rows.Err() != nil || matches != 1 {
		return authorityBinding{}, errors.New("collection_authority_blocked")
	}
	return selected, nil
}

func (h *handler) persistOffer(ctx context.Context, id Identity, key, requestHash, route, evaluationID, originatingCaseID string, a upstream.SavedEvaluation, shortfall upstream.ShortfallObservation, terms domain.Terms, termsHash, authorityID string, authorityVersion int64, refs []byte, evidence []byte, asOf time.Time) (offerResponse, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return offerResponse{}, errors.New("storage_unavailable")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockName := h.cfg.Mode + "|" + id.Subject + "|POST|" + route + "|" + key
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockName); err != nil {
		return offerResponse{}, errors.New("command_lock_failed")
	}
	var oldHash string
	var oldStatus int
	var oldBody []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_status,response_body FROM servicing.commands WHERE mode=$1 AND principal=$2 AND http_method='POST' AND resource_path=$3 AND idempotency_key=$4 FOR UPDATE`, h.cfg.Mode, id.Subject, route, key).Scan(&oldHash, &oldStatus, &oldBody)
	if err == nil {
		if oldHash != requestHash {
			return offerResponse{}, errors.New("idempotency_conflict")
		}
		if oldStatus == 422 {
			return offerResponse{Replayed: true, ReplayStatus: 422, ReplayBody: oldBody}, nil
		}
		var result offerResponse
		if json.Unmarshal(oldBody, &result) != nil {
			return offerResponse{}, errors.New("idempotency_receipt_invalid")
		}
		if err = tx.Commit(ctx); err != nil {
			return offerResponse{}, errors.New("storage_unavailable")
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return offerResponse{}, errors.New("storage_unavailable")
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM servicing.facilities WHERE mode=$1 AND id=$2 FOR UPDATE`, h.cfg.Mode, h.cfg.FacilityID); err != nil {
		return offerResponse{}, errors.New("facility_unavailable")
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM servicing.customers WHERE mode=$1 AND id=$2 FOR UPDATE`, h.cfg.Mode, a.CustomerID); err != nil {
		return offerResponse{}, errors.New("customer_unavailable")
	}
	var currentFundingRef string
	if err = tx.QueryRow(ctx, `SELECT funding_account_ref FROM servicing.facilities WHERE mode=$1 AND id=$2`, h.cfg.Mode, h.cfg.FacilityID).Scan(&currentFundingRef); err != nil {
		return offerResponse{}, errors.New("facility_unavailable")
	}
	if shortfall.CardID == nil {
		return offerResponse{}, errors.New("offer_mapping_conflict")
	}
	currentMapping, currentFingerprint, mapErr := postgres.LockVerifiedMapping(ctx, tx, a.CustomerID, a.AccountID, *shortfall.CardID)
	if mapErr != nil || currentFingerprint != terms.MappingFingerprint || currentMapping.RosterAccountID != terms.BorrowerAccountRef || currentMapping.CardID != terms.CardRef {
		return offerResponse{}, errors.New("offer_mapping_conflict")
	}
	now := h.now().UTC()
	currentAuth, authErr := currentAuthority(ctx, tx, h.cfg.Mode, a.CustomerID, currentMapping.RosterAccountID, currentFundingRef, now)
	if authErr != nil || currentAuth.ID != authorityID || currentAuth.Version != authorityVersion {
		return offerResponse{}, errors.New("collection_authority_blocked")
	}
	var racedHash string
	var racedBody []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_body FROM servicing.commands WHERE mode=$1 AND principal=$2 AND http_method='POST' AND resource_path=$3 AND idempotency_key=$4`, h.cfg.Mode, id.Subject, route, key).Scan(&racedHash, &racedBody)
	if err == nil {
		if racedHash != requestHash {
			return offerResponse{}, errors.New("idempotency_conflict")
		}
		var prior offerResponse
		if json.Unmarshal(racedBody, &prior) != nil {
			return offerResponse{}, errors.New("idempotency_receipt_invalid")
		}
		prior.Replayed = true
		if err = tx.Commit(ctx); err != nil {
			return offerResponse{}, errors.New("storage_unavailable")
		}
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return offerResponse{}, errors.New("storage_unavailable")
	}
	expiredRows, expireErr := tx.Query(ctx, `UPDATE servicing.offers SET state='EXPIRED',version=version+1 WHERE mode=$1 AND canonical_account_id=$2 AND transaction_id=$3 AND state IN ('OFFERED','AWAITING_ACCEPTANCE','AWAITING_APPROVAL','READY') AND (expires_at<=$4 OR offered_on<$5) RETURNING id::text,version,customer_id,evaluation_id,COALESCE(originating_case_id,'')`, h.cfg.Mode, terms.BorrowerAccountRef, terms.TransactionID, now, now.Format("2006-01-02"))
	if expireErr != nil {
		return offerResponse{}, errors.New("offer_expiry_update_failed")
	}
	type expiredOffer struct {
		id         string
		version    int64
		customer   string
		evaluation string
		caseID     string
	}
	var expired []expiredOffer
	for expiredRows.Next() {
		var e expiredOffer
		if expiredRows.Scan(&e.id, &e.version, &e.customer, &e.evaluation, &e.caseID) != nil {
			expiredRows.Close()
			return offerResponse{}, errors.New("offer_expiry_update_failed")
		}
		expired = append(expired, e)
	}
	if expiredRows.Err() != nil {
		expiredRows.Close()
		return offerResponse{}, errors.New("offer_expiry_update_failed")
	}
	expiredRows.Close()
	for _, e := range expired {
		var caseRef any
		if e.caseID != "" {
			caseRef = e.caseID
		}
		sum, _ := json.Marshal(map[string]any{"customer_id": e.customer, "account_ref": "customer:" + e.customer, "originating_case_id": caseRef, "evaluation_id": e.evaluation, "offer_id": e.id, "bridge_id": nil, "bridge_version": nil, "status": "EXPIRED"})
		if _, err = postgres.AppendChange(ctx, tx, domain.Change{Mode: domain.Mode(h.cfg.Mode), ResourceType: "OFFER", ResourceID: e.id, ResourceVersion: e.version, ChangeType: "OFFER_EXPIRED", Summary: sum}, nil, nil); err != nil {
			return offerResponse{}, err
		}
	}
	var duplicate bool
	if terms.OfferedOn != now.Format("2006-01-02") || !now.Before(mustParseExpiry(terms.ExpiresAt)) {
		if len(expired) > 0 {
			if err = tx.Commit(ctx); err != nil {
				return offerResponse{}, errors.New("storage_unavailable")
			}
		}
		return offerResponse{}, errors.New("offer_midnight_superseded")
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM servicing.offers WHERE mode=$1 AND canonical_account_id=$2 AND transaction_id=$3 AND state NOT IN ('SUPERSEDED','EXPIRED','TERMINATED')) OR EXISTS(SELECT 1 FROM servicing.bridges WHERE mode=$1 AND canonical_account_id=$2 AND transaction_id=$3 AND (ever_funded OR lifecycle<>'VOID_UNFUNDED'))`, h.cfg.Mode, terms.BorrowerAccountRef, terms.TransactionID).Scan(&duplicate); err != nil {
		return offerResponse{}, errors.New("payment_identity_read_failed")
	}
	if duplicate {
		return offerResponse{}, errors.New("payment_already_offered")
	}
	termsJSON, err := terms.CanonicalJSON()
	if err != nil {
		return offerResponse{}, err
	}
	state := "AWAITING_ACCEPTANCE"
	_, err = tx.Exec(ctx, `INSERT INTO servicing.offers(mode,id,revision,version,facility_id,customer_id,canonical_account_id,transaction_id,authorization_event_id,policy_id,policy_hash,terms_hash,terms_json,principal_cents,contract_fee_cents,term_days,offered_on,due_on,expires_at,state,evaluation_id,source_refs,candidate_evidence,originating_case_id) VALUES($1,$2,1,1,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15,$16,$17,$18,$19,$20::jsonb,$21::jsonb,NULLIF($22,''))`, h.cfg.Mode, terms.OfferID, h.cfg.FacilityID, a.CustomerID, terms.BorrowerAccountRef, terms.TransactionID, shortfall.CanonicalEventID, a.PolicyID, terms.PolicyHash, termsHash, string(termsJSON), terms.PrincipalCents, terms.ContractFeeCents, terms.TermDays, terms.OfferedOn, terms.DueOn, terms.ExpiresAt, state, evaluationID, string(refs), string(evidence), originatingCaseID)
	if err != nil {
		return offerResponse{}, errors.New("offer_insert_failed")
	}
	var caseRef any
	if originatingCaseID != "" {
		caseRef = originatingCaseID
	}
	summary, _ := json.Marshal(map[string]any{"customer_id": a.CustomerID, "account_ref": "customer:" + a.CustomerID, "originating_case_id": caseRef, "evaluation_id": evaluationID, "offer_id": terms.OfferID, "bridge_id": nil, "bridge_version": nil, "status": state, "terms_hash": termsHash})
	if _, err = postgres.AppendChange(ctx, tx, domain.Change{Mode: domain.Mode(a.SourceMode), ResourceType: "OFFER", ResourceID: terms.OfferID, ResourceVersion: 1, ChangeType: "OFFER_CREATED", Summary: summary}, nil, nil); err != nil {
		return offerResponse{}, err
	}
	response := offerResponse{ID: terms.OfferID, Version: 1, Revision: 1, TermsHash: termsHash, PrincipalCents: terms.PrincipalCents, ContractFeeCents: terms.ContractFeeCents, TermDays: terms.TermDays, OfferedOn: terms.OfferedOn, DueOn: terms.DueOn, ExpiresAt: terms.ExpiresAt, State: state, ReadURL: "/api/offers/" + terms.OfferID, OriginatingCaseID: nil, EvaluationID: stringPointer(evaluationID)}
	response.Mode, response.CustomerID = h.cfg.Mode, a.CustomerID
	if originatingCaseID != "" {
		response.OriginatingCaseID = stringPointer(originatingCaseID)
	}
	response.DisclosuresVersion = terms.DisclosuresVersion
	response.WaterfallVersion = terms.WaterfallVersion
	response.LateFeeConvention = terms.LateFeeConvention
	response.TotalDueCents = terms.PrincipalCents + terms.ContractFeeCents
	if h.cfg.Mode == "DEMO" {
		response.ConsentLabel = "SYNTHETIC_DEMO_DECISION"
	} else {
		response.ConsentLabel = "CONSENT_NOT_VERIFIED"
	}
	responseBody, _ := json.Marshal(response)
	if _, err = tx.Exec(ctx, `INSERT INTO servicing.commands(mode,principal,http_method,resource_path,idempotency_key,request_hash,response_status,response_body,resource_id) VALUES($1,$2,'POST',$3,$4,$5,201,$6::jsonb,$7)`, h.cfg.Mode, id.Subject, route, key, requestHash, string(responseBody), terms.OfferID); err != nil {
		return offerResponse{}, errors.New("command_receipt_write_failed")
	}
	if err = tx.Commit(ctx); err != nil {
		return offerResponse{}, errors.New("storage_unavailable")
	}
	return response, nil
}

func shortfallAmountConsistent(s upstream.ShortfallObservation) bool {
	if s.AttemptedAmountCents == nil || s.AvailableBalanceCents == nil || s.ShortfallCents == nil {
		return false
	}
	expected := int64(0)
	if *s.AttemptedAmountCents > *s.AvailableBalanceCents {
		expected = *s.AttemptedAmountCents - *s.AvailableBalanceCents
	}
	return expected == *s.ShortfallCents
}
func utcInstant(t time.Time) bool { _, offset := t.Zone(); return offset == 0 }
func mustParseExpiry(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

func upstreamError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case "underwriting_resource_not_found", "shortfall_not_found":
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Source record not found")
	case "underwriting_auth_rejected":
		writeError(w, 403, "FORBIDDEN", "Source authorization failed")
	default:
		writeError(w, 503, "UPSTREAM_UNAVAILABLE", "Source service unavailable")
	}
}
func offerError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case "idempotency_conflict":
		writeError(w, 409, "IDEMPOTENCY_CONFLICT", "Idempotency key conflicts")
	case "collection_authority_blocked":
		writeError(w, 422, "COLLECTION_AUTHORITY_BLOCKED", "Collection authority is unavailable")
	case "offer_mapping_conflict":
		writeError(w, 422, "MAPPING_CONFLICT", "Current mapping does not match")
	case "payment_already_offered":
		writeError(w, 422, "HARD_RULE_FAILED", "Payment already has an active offer or bridge")
	default:
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Offer could not be saved")
	}
}
func freshInstant(observed, now time.Time, maxAge time.Duration) bool {
	return !observed.IsZero() && !observed.After(now.Add(5*time.Second)) && now.Sub(observed.UTC()) <= maxAge
}
func freshTime(observed *time.Time, now time.Time, maxAge time.Duration) bool {
	return observed != nil && !observed.After(now.Add(5*time.Second)) && now.Sub(observed.UTC()) <= maxAge
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
