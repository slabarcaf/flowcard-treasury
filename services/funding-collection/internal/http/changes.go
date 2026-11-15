package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

type changeDTO struct {
	Sequence          uint64          `json:"sequence"`
	ResourceType      string          `json:"resource_type"`
	ResourceID        string          `json:"resource_id"`
	ResourceVersion   int64           `json:"resource_version"`
	ChangeType        string          `json:"change_type"`
	RecordedAt        time.Time       `json:"recorded_at"`
	Summary           json.RawMessage `json:"summary"`
	CustomerID        *string         `json:"customer_id"`
	AccountRef        *string         `json:"account_ref"`
	BridgeID          *string         `json:"bridge_id"`
	BridgeVersion     *int64          `json:"bridge_version"`
	OriginatingCaseID *string         `json:"originating_case_id"`
	EvaluationID      *string         `json:"evaluation_id"`
	OfferID           *string         `json:"offer_id"`
}
type changePageDTO struct {
	Mode         string      `json:"mode"`
	Items        []changeDTO `json:"items"`
	NextAfterSeq uint64      `json:"next_after_seq"`
	HasMore      bool        `json:"has_more"`
}

func (h *handler) readChanges(w http.ResponseWriter, r *http.Request) {
	id, ok := h.auth(w, r, false)
	if !ok {
		return
	}
	if id.Role != "COORDINATOR_READER" && id.Role != "FUNDING_COORDINATOR" {
		writeError(w, 403, "FORBIDDEN", "Scope does not allow this feed")
		return
	}
	values := r.URL.Query()["after_seq"]
	if len(r.URL.Query()) != 1 || len(values) != 1 {
		writeError(w, 400, "INVALID_REQUEST", "after_seq is required")
		return
	}
	after, err := strconv.ParseUint(values[0], 10, 63)
	if err != nil {
		writeError(w, 400, "INVALID_REQUEST", "after_seq is invalid")
		return
	}
	tx, err := postgres.BeginReadSnapshot(r.Context(), h.pool)
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	_, records, err := postgres.ReadChangeWindow(r.Context(), tx, h.cfg.Mode, int64(after))
	if errors.Is(err, postgres.ErrChangeCheckpointAhead) {
		writeError(w, 400, "INVALID_REQUEST", "after_seq is beyond the committed feed")
		return
	}
	if err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	page := changePageDTO{Mode: h.cfg.Mode, Items: []changeDTO{}}
	for _, record := range records {
		it := changeDTO{Sequence: uint64(record.Sequence), ResourceType: record.ResourceType, ResourceID: record.ResourceID, ResourceVersion: record.ResourceVersion, ChangeType: record.ChangeType, RecordedAt: record.RecordedAt, CustomerID: record.CustomerID, EvaluationID: record.EvaluationID, OriginatingCaseID: record.OriginatingCaseID, OfferID: record.OfferID}
		it.BridgeID = record.ParentBridgeID
		it.BridgeVersion = record.ParentBridgeVersion
		if it.ResourceType == "BRIDGE" {
			it.BridgeID = &it.ResourceID
			v := it.ResourceVersion
			it.BridgeVersion = &v
		}
		it.AccountRef = nil
		if it.CustomerID != nil {
			alias := "customer:" + *it.CustomerID
			it.AccountRef = &alias
		}
		if it.CustomerID == nil || *it.CustomerID == "" || it.ResourceID == "" || it.ChangeType == "" || it.ResourceVersion <= 0 || it.RecordedAt.IsZero() || (it.ResourceType != "OFFER" && it.BridgeID == nil) || ((it.BridgeID == nil) != (it.BridgeVersion == nil)) {
			writeError(w, 503, "SERVICING_FEED_INVALID", "A committed change lacks required scope")
			return
		}
		it.Summary = safeChangeSummary(record.Summary)
		page.Items = append(page.Items, it)
	}
	if len(page.Items) > 25 {
		page.Items = page.Items[:25]
		page.HasMore = true
	}
	if len(page.Items) > 0 {
		page.NextAfterSeq = page.Items[len(page.Items)-1].Sequence
	} else {
		page.NextAfterSeq = after
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "STORAGE_UNAVAILABLE", "Storage is unavailable")
		return
	}
	writeJSON(w, 200, page)
}

func safeChangeSummary(raw []byte) json.RawMessage {
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		return json.RawMessage(`{}`)
	}
	allowed := map[string]bool{"status": true, "state": true, "lifecycle": true, "delinquency": true, "reason_code": true, "hold_reason": true, "debt_delta_cents": true, "principal_delta_cents": true, "fee_delta_cents": true, "late_fee_delta_cents": true, "kind": true, "terms_hash": true, "backing_deficit_cents": true}
	out := map[string]json.RawMessage{}
	for key, value := range input {
		if allowed[key] && json.Valid(value) {
			out[key] = value
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
