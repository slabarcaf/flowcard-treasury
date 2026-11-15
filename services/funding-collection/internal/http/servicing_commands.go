package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

var servicingReasonCode = regexp.MustCompile(`^[A-Z0-9_:-]{1,96}$`)

type servicingCommandRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	ReasonCode      string `json:"reason_code"`
}

type servicingCommand struct {
	identity  Identity
	request   servicingCommandRequest
	method    string
	route     string
	key       string
	hash      string
	commandID string
	tx        pgx.Tx
	cancel    context.CancelFunc
}

func (h *handler) startServicingCommand(w http.ResponseWriter, r *http.Request, targetID string) (servicingCommand, context.Context, bool) {
	var command servicingCommand
	identity, ok := h.auth(w, r, true)
	if !ok {
		return command, nil, false
	}
	if identity.Role != "FUNDING_COORDINATOR" && (identity.Role != "ACTOR" || identity.ActorType != "OPERATOR") {
		writeError(w, 403, "FORBIDDEN", "Operator or funding coordinator scope is required")
		return command, nil, false
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_QUERY", "Command routes do not accept query parameters")
		return command, nil, false
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !idempotencyPattern.MatchString(keys[0]) {
		writeError(w, 400, "INVALID_REQUEST", "Idempotency-Key is required")
		return command, nil, false
	}
	var request servicingCommandRequest
	if err := decodeCommand(r, &request); err != nil {
		writeDecodeError(w, err)
		return command, nil, false
	}
	if request.ExpectedVersion < 1 || !servicingReasonCode.MatchString(request.ReasonCode) {
		writeError(w, 400, "INVALID_REQUEST", "Expected version and reason code are required")
		return command, nil, false
	}
	canonicalPath := strings.ToLower(targetID)
	if strings.Contains(r.URL.Path, "/collection-reviews") {
		canonicalPath = "/api/bridges/" + canonicalPath + "/collection-reviews"
	} else {
		canonicalPath = "/api/operations/" + canonicalPath + "/reconcile"
	}
	method := http.MethodPost
	route := method + " " + canonicalPath
	body, err := json.Marshal(request)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command fingerprint could not be created")
		return command, nil, false
	}
	digest := sha256.Sum256(body)
	hash := hex.EncodeToString(digest[:])
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		cancel()
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command could not start")
		return command, nil, false
	}
	lock := h.cfg.Mode + "|" + identity.Subject + "|" + method + "|" + route + "|" + keys[0]
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
		_ = tx.Rollback(ctx)
		cancel()
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command serialization is unavailable")
		return command, nil, false
	}
	var oldHash string
	var oldStatus int
	var oldBody []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_status,response_body FROM servicing.commands WHERE mode=$1 AND principal=$2 AND http_method=$3 AND resource_path=$4 AND idempotency_key=$5`, h.cfg.Mode, identity.Subject, method, route, keys[0]).Scan(&oldHash, &oldStatus, &oldBody)
	if err == nil {
		_ = tx.Rollback(ctx)
		cancel()
		if oldHash != hash {
			writeError(w, 409, "IDEMPOTENCY_CONFLICT", "Idempotency key was used with another request")
			return command, nil, false
		}
		if oldStatus == 202 {
			oldStatus = 200
		}
		writeJSON(w, oldStatus, json.RawMessage(oldBody))
		return command, nil, false
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		cancel()
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be read")
		return command, nil, false
	}
	commandID, err := newUUID()
	if err != nil {
		_ = tx.Rollback(ctx)
		cancel()
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command ID could not be generated")
		return command, nil, false
	}
	command = servicingCommand{identity: identity, request: request, method: method, route: route, key: keys[0], hash: hash, commandID: commandID, tx: tx, cancel: cancel}
	return command, ctx, true
}

func (h *handler) finishServicingCommand(w http.ResponseWriter, ctx context.Context, command servicingCommand, status int, body []byte, resourceID string) {
	if _, err := command.tx.Exec(ctx, `INSERT INTO servicing.commands(mode,principal,http_method,resource_path,idempotency_key,request_hash,response_status,response_body,resource_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)`, h.cfg.Mode, command.identity.Subject, command.method, command.route, command.key, command.hash, status, string(body), resourceID); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command receipt could not be saved")
		return
	}
	if err := command.tx.Commit(ctx); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Command could not commit")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func commandErrorBody(code, message string) []byte {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return body
}

func (h *handler) reconcileOperation(w http.ResponseWriter, r *http.Request) {
	operationID := r.PathValue("id")
	if !validUUID(operationID) {
		writeError(w, 400, "INVALID_REQUEST", "Operation ID is invalid")
		return
	}
	command, commandCtx, ok := h.startServicingCommand(w, r, operationID)
	if !ok {
		return
	}
	defer command.cancel()
	defer func() { _ = command.tx.Rollback(commandCtx) }()
	var candidate postgres.OperationCandidate
	err := command.tx.QueryRow(commandCtx, `SELECT o.mode,o.id::text,b.facility_id,b.customer_id,b.id::text,o.kind,o.state,o.version FROM servicing.operations o JOIN servicing.bridges b ON b.mode=o.mode AND b.id=o.bridge_id WHERE o.mode=$1 AND o.id=$2`, h.cfg.Mode, operationID).Scan(&candidate.Mode, &candidate.OperationID, &candidate.FacilityID, &candidate.CustomerID, &candidate.BridgeID, &candidate.Kind, &candidate.State, &candidate.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		h.finishServicingCommand(w, commandCtx, command, 404, commandErrorBody("RESOURCE_NOT_FOUND", "Operation not found"), operationID)
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Operation could not be read")
		return
	}
	if candidate.Version != command.request.ExpectedVersion {
		h.finishServicingCommand(w, commandCtx, command, 409, commandErrorBody("VERSION_CONFLICT", "Operation version changed"), operationID)
		return
	}
	if candidate.FacilityID != h.cfg.FacilityID || candidate.State != domain.OperationHeld {
		h.finishServicingCommand(w, commandCtx, command, 422, commandErrorBody("OPERATION_NOT_RECONCILABLE", "Expected version must identify a held operation in the configured facility"), operationID)
		return
	}
	if err = postgres.RequestOperationLookupTx(commandCtx, command.tx, candidate, command.identity.Subject, command.request.ReasonCode); err != nil {
		if errors.Is(err, postgres.ErrOperationLookupNotApplicable) {
			h.finishServicingCommand(w, commandCtx, command, 422, commandErrorBody("OPERATION_NOT_RECONCILABLE", "Funding is proven unsent and does not need reconciliation"), operationID)
			return
		}
		if errors.Is(err, postgres.ErrOperationClaimStale) {
			h.finishServicingCommand(w, commandCtx, command, 409, commandErrorBody("VERSION_CONFLICT", "Operation version changed before lookup was queued"), operationID)
			return
		}
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Lookup request could not be saved")
		return
	}
	var version int64
	if err = command.tx.QueryRow(commandCtx, `SELECT version FROM servicing.operations WHERE mode=$1 AND id=$2`, h.cfg.Mode, operationID).Scan(&version); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Lookup request version could not be read")
		return
	}
	body, _ := json.Marshal(commandResponse{CommandID: command.commandID, ResourceID: operationID, ResourceVersion: version, Status: "QUEUED"})
	h.finishServicingCommand(w, commandCtx, command, 202, body, operationID)
}

func (h *handler) reviewCollections(w http.ResponseWriter, r *http.Request) {
	bridgeID := r.PathValue("id")
	if !validUUID(bridgeID) {
		writeError(w, 400, "INVALID_REQUEST", "Bridge ID is invalid")
		return
	}
	command, commandCtx, ok := h.startServicingCommand(w, r, bridgeID)
	if !ok {
		return
	}
	defer command.cancel()
	defer func() { _ = command.tx.Rollback(commandCtx) }()
	var facilityID, customerID string
	err := command.tx.QueryRow(commandCtx, `SELECT facility_id,customer_id FROM servicing.bridges WHERE mode=$1 AND id=$2`, h.cfg.Mode, bridgeID).Scan(&facilityID, &customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.finishServicingCommand(w, commandCtx, command, 404, commandErrorBody("RESOURCE_NOT_FOUND", "Bridge not found"), bridgeID)
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Bridge could not be read")
		return
	}
	if facilityID != h.cfg.FacilityID {
		h.finishServicingCommand(w, commandCtx, command, 422, commandErrorBody("COLLECTION_REVIEW_NOT_APPLICABLE", "Bridge is outside the configured facility"), bridgeID)
		return
	}
	if err = postgres.LockFacilityForCollectionReview(commandCtx, command.tx, h.cfg.Mode, facilityID, customerID, bridgeID); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Bridge review locks could not be acquired")
		return
	}
	var version, principal, fee, late int64
	var lifecycle string
	var everFunded bool
	var caseID, evaluationID, offerID string
	var holdReasons []string
	err = command.tx.QueryRow(commandCtx, `SELECT version,lifecycle,ever_funded,principal_owed_cents,contract_fee_owed_cents,late_fee_owed_cents,COALESCE(originating_case_id,''),evaluation_id,offer_id::text,hold_reasons FROM servicing.bridges WHERE mode=$1 AND id=$2`, h.cfg.Mode, bridgeID).Scan(&version, &lifecycle, &everFunded, &principal, &fee, &late, &caseID, &evaluationID, &offerID, &holdReasons)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Bridge could not be read")
		return
	}
	if version != command.request.ExpectedVersion {
		h.finishServicingCommand(w, commandCtx, command, 409, commandErrorBody("VERSION_CONFLICT", "Bridge version changed"), bridgeID)
		return
	}
	knownDebt, debtErr := domain.AddMoney(domain.Money(principal), domain.Money(fee))
	if debtErr == nil {
		knownDebt, debtErr = domain.AddMoney(knownDebt, domain.Money(late))
	}
	if debtErr != nil {
		h.finishServicingCommand(w, commandCtx, command, 422, commandErrorBody("COLLECTION_REVIEW_NOT_APPLICABLE", "Bridge debt facts are invalid"), bridgeID)
		return
	}
	if !everFunded || (lifecycle != "ACTIVE" && lifecycle != "COLLECTING" && lifecycle != "REOPENED_HELD") || knownDebt <= 0 {
		h.finishServicingCommand(w, commandCtx, command, 422, commandErrorBody("COLLECTION_REVIEW_NOT_APPLICABLE", "Bridge has no active funded collection debt"), bridgeID)
		return
	}
	var reviewID string
	var existingVersion int64
	err = command.tx.QueryRow(commandCtx, `SELECT id::text,requested_bridge_version FROM servicing.collection_review_requests WHERE mode=$1 AND bridge_id=$2 AND state='QUEUED' ORDER BY requested_at,id LIMIT 1 FOR UPDATE`, h.cfg.Mode, bridgeID).Scan(&reviewID, &existingVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Review queue could not be read")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		reviewID = command.commandID
		version++
		if _, err = command.tx.Exec(commandCtx, `UPDATE servicing.bridges SET version=$3,updated_at=clock_timestamp() WHERE mode=$1 AND id=$2`, h.cfg.Mode, bridgeID, version); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Bridge review could not be scheduled")
			return
		}
		if _, err = command.tx.Exec(commandCtx, `INSERT INTO servicing.collection_review_requests(mode,id,bridge_id,requested_bridge_version,requested_by,reason_code,state) VALUES($1,$2,$3,$4,$5,$6,'QUEUED')`, h.cfg.Mode, reviewID, bridgeID, version, command.identity.Subject, command.request.ReasonCode); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Bridge review could not be queued")
			return
		}
		var caseRef any
		if caseID != "" {
			caseRef = caseID
		}
		summary, _ := json.Marshal(map[string]any{"customer_id": customerID, "account_ref": "customer:" + customerID, "originating_case_id": caseRef, "evaluation_id": evaluationID, "offer_id": offerID, "bridge_id": bridgeID, "bridge_version": version, "state": "QUEUED", "reason_code": command.request.ReasonCode})
		parent, parentVersion := bridgeID, version
		if _, err = postgres.AppendChange(commandCtx, command.tx, domain.Change{Mode: domain.Mode(h.cfg.Mode), ResourceType: "BRIDGE", ResourceID: bridgeID, ResourceVersion: version, ChangeType: "COLLECTION_REVIEW_QUEUED", Summary: summary}, &parent, &parentVersion); err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Review feed event could not be saved")
			return
		}
	} else if existingVersion > version {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Queued review version is inconsistent")
		return
	}
	body, _ := json.Marshal(commandResponse{CommandID: command.commandID, ResourceID: bridgeID, ResourceVersion: version, Status: "QUEUED", ReviewID: reviewID})
	h.finishServicingCommand(w, commandCtx, command, 202, body, bridgeID)
}
