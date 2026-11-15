package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

const maxCommandBody = 16_384

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type createOfferRequest struct {
	EvaluationID      string  `json:"evaluation_id"`
	OriginatingCaseID *string `json:"originating_case_id"`
}
type decisionRequest struct {
	TermsHash       string `json:"terms_hash"`
	ExpectedVersion int64  `json:"expected_version"`
	ReasonCode      string `json:"reason_code,omitempty"`
}
type offerResponse struct {
	ID                 string  `json:"id"`
	Version            int64   `json:"version"`
	Revision           int64   `json:"offer_revision"`
	TermsHash          string  `json:"terms_hash"`
	PrincipalCents     int64   `json:"principal_cents"`
	ContractFeeCents   int64   `json:"contract_fee_cents"`
	TermDays           int     `json:"term_days"`
	OfferedOn          string  `json:"offered_on"`
	DueOn              string  `json:"due_on"`
	ExpiresAt          string  `json:"expires_at"`
	State              string  `json:"state"`
	ReadURL            string  `json:"read_url"`
	DisclosuresVersion string  `json:"disclosures_version"`
	WaterfallVersion   int     `json:"waterfall_version"`
	LateFeeConvention  string  `json:"late_fee_convention"`
	TotalDueCents      int64   `json:"total_due_cents"`
	ConsentLabel       string  `json:"consent_label"`
	OriginatingCaseID  *string `json:"originating_case_id"`
	EvaluationID       *string `json:"evaluation_id"`
	Replayed           bool    `json:"-"`
	ReplayStatus       int     `json:"-"`
	ReplayBody         []byte  `json:"-"`
}

type commandResponse struct {
	CommandID       string `json:"command_id"`
	ResourceID      string `json:"resource_id"`
	ResourceVersion int64  `json:"resource_version"`
	Status          string `json:"status"`
	ReviewID        string `json:"review_id,omitempty"`
}

func decodeCommand(r *http.Request, target any) error {
	if r.ContentLength > maxCommandBody {
		return errors.New("payload_too_large")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCommandBody+1))
	if err != nil || len(body) > maxCommandBody {
		return errors.New("payload_too_large")
	}
	if err = rejectDuplicateCommandKeys(body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errors.New("invalid_request")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid_request")
	}
	return nil
}

func rejectDuplicateCommandKeys(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				k, e := dec.Token()
				if e != nil {
					return e
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return errors.New("invalid_request")
				}
				for i := 0; i < len(key); i++ {
					if key[i] >= 'A' && key[i] <= 'Z' {
						return errors.New("invalid_request")
					}
				}
				seen[key] = true
				if e = walk(); e != nil {
					return e
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if err = walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		default:
			return errors.New("invalid_request")
		}
	}
	if err := walk(); err != nil {
		return errors.New("invalid_request")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("invalid_request")
	}
	return nil
}

func (h *handler) createOffer(w http.ResponseWriter, r *http.Request) {
	h.createOfferFromSavedEvidence(w, r)
}

func (h *handler) readOffer(w http.ResponseWriter, r *http.Request) {
	if !validUUID(r.PathValue("id")) {
		writeError(w, 400, "INVALID_REQUEST", "Offer ID is invalid")
		return
	}
	id, ok := h.auth(w, r, false)
	if !ok {
		return
	}
	var response offerResponse
	var customerID string
	err := h.pool.QueryRow(r.Context(), `SELECT id::text,version,revision,terms_hash,principal_cents,contract_fee_cents,term_days,to_char(offered_on,'YYYY-MM-DD'),to_char(due_on,'YYYY-MM-DD'),to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),state,customer_id,COALESCE(terms_json->>'disclosures_version',''),COALESCE((terms_json->>'waterfall_version')::integer,0),COALESCE(terms_json->>'late_fee_convention',''),NULLIF(originating_case_id,''),evaluation_id FROM servicing.offers WHERE mode=$1 AND id=$2`, h.cfg.Mode, r.PathValue("id")).Scan(&response.ID, &response.Version, &response.Revision, &response.TermsHash, &response.PrincipalCents, &response.ContractFeeCents, &response.TermDays, &response.OfferedOn, &response.DueOn, &response.ExpiresAt, &response.State, &customerID, &response.DisclosuresVersion, &response.WaterfallVersion, &response.LateFeeConvention, &response.OriginatingCaseID, &response.EvaluationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Offer not found")
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	if id.Role == "ACTOR" && id.ActorType == "CUSTOMER" && id.CustomerID != customerID {
		writeError(w, 404, "RESOURCE_NOT_FOUND", "Offer not found")
		return
	}
	response.ReadURL = "/api/offers/" + response.ID
	if response.ContractFeeCents > math.MaxInt64-response.PrincipalCents {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Offer terms are unavailable")
		return
	}
	response.TotalDueCents = response.PrincipalCents + response.ContractFeeCents
	if h.cfg.Mode == "DEMO" {
		response.ConsentLabel = "SYNTHETIC_DEMO_DECISION"
	} else {
		response.ConsentLabel = "CONSENT_NOT_VERIFIED"
	}
	writeJSON(w, 200, response)
}

func (h *handler) acceptOffer(w http.ResponseWriter, r *http.Request) {
	h.recordDecision(w, r, "ACCEPT")
}
func (h *handler) approveOffer(w http.ResponseWriter, r *http.Request) {
	h.recordDecision(w, r, "APPROVE")
}

func (h *handler) recordDecision(w http.ResponseWriter, r *http.Request, decision string) {
	identity, ok := h.auth(w, r, true)
	if !ok {
		return
	}
	if identity.Role != "ACTOR" || (decision == "ACCEPT" && identity.ActorType != "CUSTOMER") || (decision == "APPROVE" && identity.ActorType != "OPERATOR") {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "Actor scope does not allow this decision")
		return
	}
	offerID := r.PathValue("id")
	if !validUUID(offerID) {
		writeError(w, 400, "INVALID_REQUEST", "Offer ID is invalid")
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !idempotencyPattern.MatchString(keys[0]) {
		writeError(w, 400, "INVALID_REQUEST", "Idempotency-Key is required")
		return
	}
	key := keys[0]
	var termsHash string
	var expectedVersion int64
	reasonCode := ""
	var typed any
	if decision == "ACCEPT" {
		request := struct {
			TermsHash       string `json:"terms_hash"`
			ExpectedVersion int64  `json:"expected_version"`
		}{}
		if err := decodeCommand(r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		termsHash, expectedVersion = request.TermsHash, request.ExpectedVersion
		typed = request
	} else {
		request := struct {
			TermsHash       string `json:"terms_hash"`
			ExpectedVersion int64  `json:"expected_version"`
			ReasonCode      string `json:"reason_code"`
		}{}
		if err := decodeCommand(r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		termsHash, expectedVersion, reasonCode = request.TermsHash, request.ExpectedVersion, request.ReasonCode
		if reasonCode == "" {
			writeError(w, 400, "INVALID_REQUEST", "Reason code is required")
			return
		}
		typed = request
	}
	if termsHash == "" || expectedVersion < 1 {
		writeError(w, 400, "INVALID_REQUEST", "Decision request is invalid")
		return
	}
	body, _ := json.Marshal(typed)
	sum := sha256.Sum256(body)
	requestHash := hex.EncodeToString(sum[:])
	route := r.Method + " " + r.URL.Path
	mode := h.cfg.Mode
	decisionAt := h.now().UTC()
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	lockName := mode + "|" + identity.Subject + "|" + route + "|" + keys[0]
	if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockName); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	var oldHash string
	var oldStatus int
	var oldBody []byte
	err = tx.QueryRow(r.Context(), `SELECT request_hash,response_status,response_body FROM servicing.commands WHERE mode=$1 AND principal=$2 AND http_method=$3 AND resource_path=$4 AND idempotency_key=$5`, mode, identity.Subject, r.Method, route, keys[0]).Scan(&oldHash, &oldStatus, &oldBody)
	if err == nil {
		if oldHash != requestHash {
			writeError(w, 409, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
			return
		}
		if oldStatus == 201 || oldStatus == 202 {
			oldStatus = 200
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(oldStatus)
		_, _ = w.Write(oldBody)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	var offerCustomer, termsHashStored, state, disclosures, evaluationID, caseID, offeredOn string
	var revision, version int64
	var expires time.Time
	var sourceRefs []byte
	err = tx.QueryRow(r.Context(), `SELECT id::text,customer_id,terms_hash,state,version,revision,expires_at,terms_json->>'disclosures_version',evaluation_id,COALESCE(originating_case_id,''),to_char(offered_on,'YYYY-MM-DD'),source_refs FROM servicing.offers WHERE mode=$1 AND id=$2 FOR UPDATE`, mode, offerID).Scan(new(string), &offerCustomer, &termsHashStored, &state, &version, &revision, &expires, &disclosures, &evaluationID, &caseID, &offeredOn, &sourceRefs)
	if errors.Is(err, pgx.ErrNoRows) {
		persistDecisionGuard(w, r.Context(), tx, identity, key, route, requestHash, 404, "RESOURCE_NOT_FOUND", "Offer not found", offerID)
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	// Check tenant ownership before exposing version, hash, expiry or lifecycle.
	if decision == "ACCEPT" && offerCustomer != identity.CustomerID {
		persistDecisionGuard(w, r.Context(), tx, identity, key, route, requestHash, 404, "RESOURCE_NOT_FOUND", "Offer not found", offerID)
		return
	}
	status, code, message := http.StatusCreated, "", ""
	switch {
	case expectedVersion != version:
		status, code, message = 409, "VERSION_CONFLICT", "Offer version changed"
	case termsHash != termsHashStored:
		status, code, message = 409, "TERMS_SUPERSEDED", "Terms changed"
	case !decisionAt.Before(expires):
		status, code, message = 422, "OFFER_EXPIRED", "Offer expired"
	case offeredOn != decisionAt.Format("2006-01-02"):
		status, code, message = 422, "TERMS_SUPERSEDED", "Offer date changed"
	case decision == "ACCEPT" && (state != "OFFERED" && state != "AWAITING_ACCEPTANCE"):
		status, code, message = 422, "TERMS_SUPERSEDED", "Offer is not awaiting acceptance"
	case decision == "APPROVE" && state != "AWAITING_APPROVAL":
		status, code, message = 422, "APPROVAL_REQUIRED", "Offer is not awaiting operator approval"
	}
	if status == http.StatusCreated && decision == "APPROVE" {
		var accepted bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM servicing.actor_decisions WHERE mode=$1 AND offer_id=$2 AND decision='ACCEPT' AND terms_hash=$3)`, mode, offerID, termsHashStored).Scan(&accepted); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
			return
		}
		if !accepted {
			status, code, message = 422, "ACCEPTANCE_REQUIRED", "Customer acceptance is required"
		}
	}
	if status != http.StatusCreated {
		persistDecisionGuard(w, r.Context(), tx, identity, key, route, requestHash, status, code, message, offerID)
		return
	}
	decisionID, err := newUUID()
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	decisionRefs := sourceRefs
	if len(decisionRefs) == 0 || !json.Valid(decisionRefs) {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Stored offer evidence is unavailable")
		return
	}
	actorDecisionType := identity.ActorType
	newState := "AWAITING_APPROVAL"
	if decision == "APPROVE" {
		newState = "READY"
	}
	newVersion := version + 1
	if _, err = tx.Exec(r.Context(), `INSERT INTO servicing.actor_decisions(mode,id,offer_id,offer_revision,terms_hash,actor_id,actor_type,decision,reason_code,recorded_at,authentication_subject,disclosures_version,source_refs) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb)`, mode, decisionID, offerID, revision, termsHashStored, identity.ActorID, actorDecisionType, decision, reasonCode, decisionAt, identity.Subject, disclosures, string(decisionRefs)); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE servicing.offers SET state=$3,version=$4 WHERE mode=$1 AND id=$2`, mode, offerID, newState, newVersion); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	var caseRef any
	if caseID != "" {
		caseRef = caseID
	}
	summary, _ := json.Marshal(map[string]any{"customer_id": offerCustomer, "account_ref": "customer:" + offerCustomer, "originating_case_id": caseRef, "evaluation_id": evaluationID, "offer_id": offerID, "bridge_id": nil, "bridge_version": nil, "status": newState, "terms_hash": termsHashStored, "evidence_refs": json.RawMessage(decisionRefs)})
	if _, err = postgres.AppendChange(r.Context(), tx, domain.Change{Mode: domain.Mode(mode), ResourceType: "OFFER", ResourceID: offerID, ResourceVersion: newVersion, ChangeType: "ACTOR_DECISION_" + decision, Summary: summary}, nil, nil); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	response := map[string]any{"id": offerID, "version": newVersion, "terms_hash": termsHashStored, "state": newState, "read_url": "/api/offers/" + offerID}
	responseBody, _ := json.Marshal(response)
	if err = storeDecisionReceipt(r.Context(), tx, identity, route, key, requestHash, 201, responseBody, offerID); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	_, _ = w.Write(responseBody)
}

func persistDecisionGuard(w http.ResponseWriter, ctx context.Context, tx pgx.Tx, id Identity, key, route, hash string, status int, code, message, resourceID string) {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	if err := storeDecisionReceipt(ctx, tx, id, route, key, hash, status, body, resourceID); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func storeDecisionReceipt(ctx context.Context, tx pgx.Tx, id Identity, route, key, hash string, status int, body []byte, resourceID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO servicing.commands(mode,principal,http_method,resource_path,idempotency_key,request_hash,response_status,response_body,resource_id) VALUES($1,$2,'POST',$3,$4,$5,$6,$7::jsonb,$8)`, id.Mode, id.Subject, route, key, hash, status, string(body), resourceID)
	if err != nil {
		return errors.New("decision_receipt_write_failed")
	}
	return nil
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if err.Error() == "payload_too_large" {
		writeError(w, 413, "PAYLOAD_TOO_LARGE", "Request is too large")
		return
	}
	writeError(w, 400, "INVALID_REQUEST", "Request is invalid")
}
func validUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
