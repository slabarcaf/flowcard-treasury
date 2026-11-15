package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/domain"
)

type EvidenceInput struct {
	ID, CaseID, RunID, SourceHandle, SourceVersion string
	SourceHash, VisibleSourceHash                  []byte
	Locator, Excerpt, ClaimField, ClaimValue       string
	ClaimUnit, ClaimDate, Uncertainty              *string
	CanonicalHash                                  []byte
	ToolCallID                                     string
	SourceType                                     string
	SourceBindingID                                string
	SourceRecordID                                 string
	SourceExportVersion                            string
	SectionID                                      string
	ExtractedAt                                    time.Time
	ModelVersion                                   string
	PromptVersion                                  string
	Conflicts                                      []byte
}

// CanonicalEvidenceHash derives the deduplication identity from retained source and claim fields.
func CanonicalEvidenceHash(e EvidenceInput) ([]byte, error) {
	canonical, err := json.Marshal(struct {
		SourceBindingID, SourceHandle, SourceType, SourceRecordID string
		SourceVersion, SourceExportVersion, SectionID, Locator    string
		SourceHash, VisibleSourceHash                             string
		Excerpt, ClaimField, ClaimValue                           string
		ClaimUnit, ClaimDate, Uncertainty                         *string
	}{
		e.SourceBindingID, e.SourceHandle, e.SourceType, e.SourceRecordID,
		e.SourceVersion, e.SourceExportVersion, e.SectionID, e.Locator,
		hex.EncodeToString(e.SourceHash), hex.EncodeToString(e.VisibleSourceHash),
		e.Excerpt, e.ClaimField, e.ClaimValue, e.ClaimUnit, e.ClaimDate, e.Uncertainty,
	})
	if err != nil {
		return nil, errors.New("evidence_canonicalization_failed")
	}
	sum := sha256.Sum256(canonical)
	return sum[:], nil
}

func (r *Repository) RecordEvidence(ctx context.Context, e EvidenceInput, token string) (string, error) {
	canonicalHash, err := CanonicalEvidenceHash(e)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(e.CanonicalHash, canonicalHash) {
		return "", domain.ErrConflict
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", errors.New("evidence_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, e.CaseID, token); err != nil {
		return "", err
	}
	var bindingID string
	if err := tx.QueryRow(ctx, `SELECT binding_id FROM coordinator.cases WHERE id=$1`, e.CaseID).Scan(&bindingID); err != nil || bindingID != e.SourceBindingID {
		return "", domain.ErrConflict
	}
	var runCase string
	var disposition *string
	if err := tx.QueryRow(ctx, `SELECT case_id,disposition FROM coordinator.runs WHERE id=$1 FOR UPDATE`, e.RunID).Scan(&runCase, &disposition); err != nil || runCase != e.CaseID {
		return "", domain.ErrLeaseLost
	}
	if disposition != nil {
		return "", domain.ErrConflict
	}
	if e.SourceType == "" || e.SourceRecordID == "" || e.SourceBindingID == "" || e.SectionID == "" || e.ExtractedAt.IsZero() || e.ModelVersion == "" || e.PromptVersion == "" || len(e.Conflicts) == 0 || len(e.SourceHash) != 32 || len(e.VisibleSourceHash) != 32 {
		return "", errors.New("evidence_provenance_missing")
	}
	if e.ID == "" || e.ToolCallID == "" {
		return "", domain.ErrConflict
	}
	var existingID string
	var existing EvidenceInput
	var sameConflicts bool
	err = tx.QueryRow(ctx, `SELECT id::text,source_handle,source_type,source_record_id,source_binding_id,source_version,source_export_version,source_hash,model_visible_source_hash,section_id,locator,excerpt,claim_field,claim_value,claim_unit,claim_date::text,uncertainty,canonical_hash,tool_call_id,extracted_at,model_version,prompt_version,conflicts=$4::jsonb FROM coordinator.evidence WHERE case_id=$1 AND run_id=$2 AND tool_call_id=$3 FOR UPDATE`, e.CaseID, e.RunID, e.ToolCallID, e.Conflicts).Scan(&existingID, &existing.SourceHandle, &existing.SourceType, &existing.SourceRecordID, &existing.SourceBindingID, &existing.SourceVersion, &existing.SourceExportVersion, &existing.SourceHash, &existing.VisibleSourceHash, &existing.SectionID, &existing.Locator, &existing.Excerpt, &existing.ClaimField, &existing.ClaimValue, &existing.ClaimUnit, &existing.ClaimDate, &existing.Uncertainty, &existing.CanonicalHash, &existing.ToolCallID, &existing.ExtractedAt, &existing.ModelVersion, &existing.PromptVersion, &sameConflicts)
	if err == nil {
		if !sameEvidenceCall(existing, e, sameConflicts) {
			return "", domain.ErrConflict
		}
		if err := finalLeaseFence(ctx, tx, e.CaseID, token); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", errors.New("evidence_replay_commit_failed")
		}
		return existingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("evidence_call_replay_read_failed")
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO coordinator.evidence(id,case_id,run_id,source_handle,source_version,source_hash,locator,excerpt,claim_field,claim_value,claim_unit,claim_date,uncertainty,canonical_hash,tool_call_id,source_type,source_record_id,source_binding_id,source_export_version,section_id,extracted_at,model_version,prompt_version,conflicts,model_visible_source_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25) ON CONFLICT(case_id,run_id,canonical_hash) DO NOTHING RETURNING id`, e.ID, e.CaseID, e.RunID, e.SourceHandle, e.SourceVersion, e.SourceHash, e.Locator, e.Excerpt, e.ClaimField, e.ClaimValue, e.ClaimUnit, e.ClaimDate, e.Uncertainty, e.CanonicalHash, e.ToolCallID, e.SourceType, e.SourceRecordID, e.SourceBindingID, e.SourceExportVersion, e.SectionID, e.ExtractedAt, e.ModelVersion, e.PromptVersion, e.Conflicts, e.VisibleSourceHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `SELECT id::text,source_handle,source_type,source_record_id,source_binding_id,source_version,source_export_version,source_hash,model_visible_source_hash,section_id,locator,excerpt,claim_field,claim_value,claim_unit,claim_date::text,uncertainty,canonical_hash,tool_call_id,extracted_at,model_version,prompt_version FROM coordinator.evidence WHERE case_id=$1 AND run_id=$2 AND canonical_hash=$3 FOR UPDATE`, e.CaseID, e.RunID, e.CanonicalHash).Scan(&id, &existing.SourceHandle, &existing.SourceType, &existing.SourceRecordID, &existing.SourceBindingID, &existing.SourceVersion, &existing.SourceExportVersion, &existing.SourceHash, &existing.VisibleSourceHash, &existing.SectionID, &existing.Locator, &existing.Excerpt, &existing.ClaimField, &existing.ClaimValue, &existing.ClaimUnit, &existing.ClaimDate, &existing.Uncertainty, &existing.CanonicalHash, &existing.ToolCallID, &existing.ExtractedAt, &existing.ModelVersion, &existing.PromptVersion); err != nil {
			return "", errors.New("evidence_dedup_read_failed")
		}
		if !sameEvidenceClaim(existing, e) {
			return "", domain.ErrConflict
		}
		if err := finalLeaseFence(ctx, tx, e.CaseID, token); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", errors.New("evidence_replay_commit_failed")
		}
		return id, nil
	} else if err != nil {
		return "", errors.New("evidence_insert_failed")
	}
	var claimCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM coordinator.evidence WHERE run_id=$1`, e.RunID).Scan(&claimCount); err != nil {
		return "", errors.New("evidence_count_read_failed")
	}
	if claimCount > 10 {
		return "", domain.ErrBudgetExhausted
	}
	if err := finalLeaseFence(ctx, tx, e.CaseID, token); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", errors.New("evidence_commit_failed")
	}
	return id, nil
}

func sameEvidenceCall(stored, incoming EvidenceInput, sameConflicts bool) bool {
	return sameEvidenceClaim(stored, incoming) && sameConflicts && stored.ToolCallID == incoming.ToolCallID && stored.ExtractedAt.Equal(incoming.ExtractedAt) && stored.ModelVersion == incoming.ModelVersion && stored.PromptVersion == incoming.PromptVersion
}

func sameEvidenceClaim(stored, incoming EvidenceInput) bool {
	storedHash, err := CanonicalEvidenceHash(stored)
	if err != nil || !bytes.Equal(storedHash, stored.CanonicalHash) || !bytes.Equal(storedHash, incoming.CanonicalHash) {
		return false
	}
	return stored.SourceHandle == incoming.SourceHandle && stored.SourceType == incoming.SourceType && stored.SourceRecordID == incoming.SourceRecordID && stored.SourceBindingID == incoming.SourceBindingID && stored.SourceVersion == incoming.SourceVersion && stored.SourceExportVersion == incoming.SourceExportVersion && bytes.Equal(stored.SourceHash, incoming.SourceHash) && bytes.Equal(stored.VisibleSourceHash, incoming.VisibleSourceHash) && stored.SectionID == incoming.SectionID && stored.Locator == incoming.Locator && stored.Excerpt == incoming.Excerpt && stored.ClaimField == incoming.ClaimField && stored.ClaimValue == incoming.ClaimValue && same(stored.ClaimUnit, incoming.ClaimUnit) && same(stored.ClaimDate, incoming.ClaimDate) && same(stored.Uncertainty, incoming.Uncertainty)
}

// AuditEvidenceLink records an operator-controlled link without granting model lease bypass.
func (r *Repository) AuditEvidenceLink(ctx context.Context, evidenceID, snapshotID, snapshotVersion, actorID string, sourceRefs []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("evidence_link_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM coordinator.evidence WHERE id=$1 FOR UPDATE`, evidenceID).Scan(&state); err != nil {
		return errors.New("evidence_not_found")
	}
	if state != "PENDING_REVIEW" {
		return domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO coordinator.evidence_link_audits(evidence_id,snapshot_id,snapshot_version,actor_id,linked_at,source_refs) VALUES($1,$2,$3,$4,clock_timestamp(),$5)`, evidenceID, snapshotID, snapshotVersion, actorID, sourceRefs); err != nil {
		return errors.New("evidence_link_audit_failed")
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.evidence SET state='LINKED_TO_SNAPSHOT' WHERE id=$1`, evidenceID); err != nil {
		return errors.New("evidence_link_state_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("evidence_link_commit_failed")
	}
	return nil
}

type RecommendationInput struct {
	CaseID, RunID, ToolCallID string
	BridgeVersion             int64
	ActionCode, Reason        string
	Citations                 []byte
}

type RecommendationView struct {
	ID            string
	RunID         string
	ToolCallID    string
	BridgeVersion int64
	ActionCode    string
	Reason        string
	Citations     []byte
	CreatedAt     time.Time
}

// ListRecommendations returns nonexecuting collection recommendations for one case.
func (r *Repository) ListRecommendations(ctx context.Context, caseID string) ([]RecommendationView, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,run_id::text,tool_call_id,bridge_version,action_code,reason,citations,created_at FROM coordinator.collection_recommendations WHERE case_id=$1 ORDER BY created_at,id`, caseID)
	if err != nil {
		return nil, errors.New("recommendations_read_failed")
	}
	defer rows.Close()
	var result []RecommendationView
	for rows.Next() {
		var item RecommendationView
		if err := rows.Scan(&item.ID, &item.RunID, &item.ToolCallID, &item.BridgeVersion, &item.ActionCode, &item.Reason, &item.Citations, &item.CreatedAt); err != nil {
			return nil, errors.New("recommendations_decode_failed")
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, errors.New("recommendations_read_failed")
	}
	return result, nil
}

func (r *Repository) RecordRecommendation(ctx context.Context, token string, input RecommendationInput) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", errors.New("recommendation_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, input.CaseID, token); err != nil {
		return "", err
	}
	var runCase string
	var runDisposition *string
	if err := tx.QueryRow(ctx, `SELECT case_id::text,disposition FROM coordinator.runs WHERE id=$1 FOR UPDATE`, input.RunID).Scan(&runCase, &runDisposition); err != nil || runCase != input.CaseID {
		return "", domain.ErrLeaseLost
	}
	if runDisposition != nil {
		return "", domain.ErrConflict
	}
	var version *int64
	var dirty bool
	var bridgeID string
	if err := tx.QueryRow(ctx, `SELECT bridge_id,bridge_version,refresh_needed FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION'`, input.CaseID).Scan(&bridgeID, &version, &dirty); err != nil || version == nil || *version != input.BridgeVersion || dirty {
		return "", domain.ErrConflict
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT o.state->'allowed_next_actions' @> jsonb_build_array($4::text) FROM coordinator.collection_observations o JOIN coordinator.cases c ON c.id=o.case_id AND c.bridge_id=o.resource_id WHERE o.case_id=$1 AND o.bridge_version=$2 AND o.resource_type='BRIDGE' AND o.resource_id=$3 ORDER BY o.observed_at DESC LIMIT 1`, input.CaseID, input.BridgeVersion, bridgeID, input.ActionCode).Scan(&allowed); err != nil || !allowed {
		return "", domain.ErrConflict
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO coordinator.collection_recommendations(case_id,run_id,tool_call_id,bridge_version,action_code,reason,citations) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run_id,tool_call_id) DO NOTHING RETURNING id::text`, input.CaseID, input.RunID, input.ToolCallID, input.BridgeVersion, input.ActionCode, input.Reason, input.Citations).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var savedVersion int64
		var action, reason string
		var refs []byte
		if err := tx.QueryRow(ctx, `SELECT id::text,bridge_version,action_code,reason,citations FROM coordinator.collection_recommendations WHERE run_id=$1 AND tool_call_id=$2 FOR UPDATE`, input.RunID, input.ToolCallID).Scan(&id, &savedVersion, &action, &reason, &refs); err != nil {
			return "", domain.ErrConflict
		}
		if savedVersion != input.BridgeVersion || action != input.ActionCode || reason != input.Reason || !bytesEqualJSON(refs, input.Citations) {
			return "", domain.ErrConflict
		}
		if err := finalLeaseFence(ctx, tx, input.CaseID, token); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", errors.New("recommendation_replay_commit_failed")
		}
		return id, nil
	} else if err != nil {
		return "", errors.New("recommendation_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, input.CaseID, token); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", errors.New("recommendation_commit_failed")
	}
	return id, nil
}

func (r *Repository) EvidenceIDsForRun(ctx context.Context, caseID, token, runID string) (map[string]bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, errors.New("evidence_list_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return nil, err
	}
	var runCase string
	if err := tx.QueryRow(ctx, `SELECT case_id::text FROM coordinator.runs WHERE id=$1`, runID).Scan(&runCase); err != nil || runCase != caseID {
		return nil, domain.ErrLeaseLost
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM coordinator.evidence WHERE case_id=$1 AND run_id=$2 ORDER BY created_at,id`, caseID, runID)
	if err != nil {
		return nil, errors.New("evidence_list_failed")
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, errors.New("evidence_list_decode_failed")
		}
		out[id] = true
	}
	if rows.Err() != nil {
		return nil, errors.New("evidence_list_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.New("evidence_list_commit_failed")
	}
	return out, nil
}

func (r *Repository) ReadCollectionState(ctx context.Context, caseID, token string) (int64, []byte, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, nil, errors.New("collection_state_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return 0, nil, err
	}
	var version *int64
	var dirty bool
	var bridgeID string
	if err := tx.QueryRow(ctx, `SELECT bridge_id,bridge_version,refresh_needed FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION'`, caseID).Scan(&bridgeID, &version, &dirty); err != nil || version == nil {
		return 0, nil, domain.ErrLeaseLost
	}
	var state []byte
	if err := tx.QueryRow(ctx, `SELECT o.state FROM coordinator.collection_observations o JOIN coordinator.cases c ON c.id=o.case_id AND c.bridge_id=o.resource_id WHERE o.case_id=$1 AND o.bridge_version=$2 AND o.resource_type='BRIDGE' AND o.resource_id=$3 ORDER BY o.observed_at DESC LIMIT 1`, caseID, *version, bridgeID).Scan(&state); err != nil {
		return 0, nil, errors.New("collection_observation_unavailable")
	}
	if dirty {
		return *version, nil, domain.ErrCollectionRefreshPending
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, errors.New("collection_state_commit_failed")
	}
	return *version, state, nil
}

func (r *Repository) FinishRun(ctx context.Context, caseID, token, runID string, result []byte, bridgeID string, bridgeVersion *int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("run_finish_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, caseID, token); err != nil {
		return err
	}
	var runCase string
	var previous *string
	if err := tx.QueryRow(ctx, `SELECT case_id::text,disposition FROM coordinator.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&runCase, &previous); err != nil || runCase != caseID {
		return domain.ErrLeaseLost
	}
	if bridgeVersion != nil {
		var storedVersion *int64
		var refreshNeeded bool
		var storedBridgeID string
		if err := tx.QueryRow(ctx, `SELECT bridge_id,bridge_version,refresh_needed FROM coordinator.cases WHERE id=$1 AND kind='COLLECTION'`, caseID).Scan(&storedBridgeID, &storedVersion, &refreshNeeded); err != nil || storedBridgeID != bridgeID || storedVersion == nil || *storedVersion != *bridgeVersion || refreshNeeded {
			return domain.ErrConflict
		}
	}
	encoded := string(result)
	if previous != nil {
		if *previous != encoded {
			return domain.ErrConflict
		}
		if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("run_finish_replay_commit_failed")
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.runs SET disposition=$2,stage='FINISHED',updated_at=clock_timestamp() WHERE id=$1 AND disposition IS NULL`, runID, encoded); err != nil {
		return errors.New("run_finish_write_failed")
	}
	if err := finalLeaseFence(ctx, tx, caseID, token); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("run_finish_commit_failed")
	}
	return nil
}

func (r *Repository) AuditEvidenceLinkScoped(ctx context.Context, evidenceID, snapshotID, snapshotVersion, actorID, mode, customerID, accountRef, sourceBindingID string, sourceRefs []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errors.New("evidence_link_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state, caseMode, caseCustomer, caseAccount, bindingID, evidenceBindingID string
	err = tx.QueryRow(ctx, `SELECT e.state,b.mode,c.customer_id,c.account_ref,c.binding_id,e.source_binding_id FROM coordinator.evidence e JOIN coordinator.cases c ON c.id=e.case_id JOIN coordinator.source_bindings b ON b.id=c.binding_id WHERE e.id=$1 FOR UPDATE OF e,c`, evidenceID).Scan(&state, &caseMode, &caseCustomer, &caseAccount, &bindingID, &evidenceBindingID)
	if err != nil {
		return errors.New("evidence_not_found")
	}
	if state != "PENDING_REVIEW" || caseMode != mode || caseCustomer != customerID || caseAccount != accountRef || bindingID != sourceBindingID || evidenceBindingID != bindingID {
		return domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO coordinator.evidence_link_audits(evidence_id,snapshot_id,snapshot_version,actor_id,linked_at,source_refs,source_binding_id) VALUES($1,$2,$3,$4,clock_timestamp(),$5,$6)`, evidenceID, snapshotID, snapshotVersion, actorID, sourceRefs, sourceBindingID); err != nil {
		return errors.New("evidence_link_audit_failed")
	}
	if _, err := tx.Exec(ctx, `UPDATE coordinator.evidence SET state='LINKED_TO_SNAPSHOT' WHERE id=$1 AND state='PENDING_REVIEW'`, evidenceID); err != nil {
		return errors.New("evidence_link_state_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("evidence_link_commit_failed")
	}
	return nil
}

type EvidenceRecord struct {
	ID                  string     `json:"id"`
	CaseID              string     `json:"case_id"`
	RunID               string     `json:"run_id"`
	SourceHandle        string     `json:"source_handle"`
	SourceType          *string    `json:"source_type"`
	SourceRecordID      *string    `json:"source_record_id"`
	SourceVersion       string     `json:"source_version"`
	SourceExportVersion *string    `json:"source_export_version"`
	SourceHash          string     `json:"source_sha256"`
	VisibleSourceHash   *string    `json:"model_visible_source_sha256"`
	SectionID           *string    `json:"section_id"`
	Locator             string     `json:"locator"`
	Excerpt             string     `json:"excerpt"`
	ClaimField          string     `json:"claim_field"`
	ClaimValue          string     `json:"claim_value"`
	ClaimUnit           *string    `json:"claim_unit"`
	ClaimDate           *string    `json:"claim_date"`
	Uncertainty         *string    `json:"uncertainty"`
	Conflicts           []byte     `json:"conflicts"`
	State               string     `json:"state"`
	ToolCallID          string     `json:"tool_call_id"`
	ExtractedAt         *time.Time `json:"extracted_at"`
	ModelVersion        *string    `json:"model_version"`
	PromptVersion       *string    `json:"prompt_version"`
}

type EvidenceLinkScope struct{ Mode, CustomerID, AccountRef, RawAccountID, RawCardID, SourceBindingID string }

// ReadCaseEvidenceScope returns the immutable scope for one evidence export.
func (r *Repository) ReadCaseEvidenceScope(ctx context.Context, caseID string) (EvidenceLinkScope, error) {
	var scope EvidenceLinkScope
	err := r.pool.QueryRow(ctx, `SELECT b.mode,c.customer_id,c.account_ref,COALESCE(c.raw_account_id,''),COALESCE(a.source_card_id,''),c.binding_id FROM coordinator.cases c JOIN coordinator.source_bindings b ON b.id=c.binding_id LEFT JOIN LATERAL (SELECT source_card_id FROM coordinator.assessments WHERE case_id=c.id ORDER BY revision DESC LIMIT 1) a ON true WHERE c.id=$1`, caseID).Scan(&scope.Mode, &scope.CustomerID, &scope.AccountRef, &scope.RawAccountID, &scope.RawCardID, &scope.SourceBindingID)
	if err != nil {
		return EvidenceLinkScope{}, errors.New("case_not_found")
	}
	return scope, nil
}

func (r *Repository) ReadEvidence(ctx context.Context, id string) (EvidenceRecord, EvidenceLinkScope, error) {
	var e EvidenceRecord
	var scope EvidenceLinkScope
	var hash, visibleHash []byte
	err := r.pool.QueryRow(ctx, `SELECT e.id::text,e.case_id::text,e.run_id::text,e.source_handle,e.source_type,e.source_record_id,e.source_version,e.source_export_version,e.source_hash,e.model_visible_source_hash,e.section_id,e.locator,e.excerpt,e.claim_field,e.claim_value,e.claim_unit,e.claim_date::text,e.uncertainty,e.conflicts,e.state,e.tool_call_id,e.extracted_at,e.model_version,e.prompt_version,b.mode,c.customer_id,c.account_ref,COALESCE(c.raw_account_id,''),COALESCE(a.source_card_id,''),c.binding_id FROM coordinator.evidence e JOIN coordinator.cases c ON c.id=e.case_id JOIN coordinator.source_bindings b ON b.id=c.binding_id LEFT JOIN LATERAL (SELECT source_card_id FROM coordinator.assessments WHERE case_id=c.id ORDER BY revision DESC LIMIT 1) a ON true WHERE e.id=$1`, id).Scan(&e.ID, &e.CaseID, &e.RunID, &e.SourceHandle, &e.SourceType, &e.SourceRecordID, &e.SourceVersion, &e.SourceExportVersion, &hash, &visibleHash, &e.SectionID, &e.Locator, &e.Excerpt, &e.ClaimField, &e.ClaimValue, &e.ClaimUnit, &e.ClaimDate, &e.Uncertainty, &e.Conflicts, &e.State, &e.ToolCallID, &e.ExtractedAt, &e.ModelVersion, &e.PromptVersion, &scope.Mode, &scope.CustomerID, &scope.AccountRef, &scope.RawAccountID, &scope.RawCardID, &scope.SourceBindingID)
	if err != nil {
		return EvidenceRecord{}, EvidenceLinkScope{}, errors.New("evidence_not_found")
	}
	e.SourceHash = hex.EncodeToString(hash)
	if len(visibleHash) == 32 {
		visible := hex.EncodeToString(visibleHash)
		e.VisibleSourceHash = &visible
	}
	return e, scope, nil
}
func (r *Repository) ListEvidence(ctx context.Context, caseID string) ([]EvidenceRecord, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,case_id::text,run_id::text,source_handle,source_type,source_record_id,source_version,source_export_version,source_hash,model_visible_source_hash,section_id,locator,excerpt,claim_field,claim_value,claim_unit,claim_date::text,uncertainty,conflicts,state,tool_call_id,extracted_at,model_version,prompt_version FROM coordinator.evidence WHERE case_id=$1 ORDER BY created_at,id`, caseID)
	if err != nil {
		return nil, errors.New("evidence_list_failed")
	}
	defer rows.Close()
	var out []EvidenceRecord
	for rows.Next() {
		var e EvidenceRecord
		var hash, visibleHash []byte
		if err := rows.Scan(&e.ID, &e.CaseID, &e.RunID, &e.SourceHandle, &e.SourceType, &e.SourceRecordID, &e.SourceVersion, &e.SourceExportVersion, &hash, &visibleHash, &e.SectionID, &e.Locator, &e.Excerpt, &e.ClaimField, &e.ClaimValue, &e.ClaimUnit, &e.ClaimDate, &e.Uncertainty, &e.Conflicts, &e.State, &e.ToolCallID, &e.ExtractedAt, &e.ModelVersion, &e.PromptVersion); err != nil {
			return nil, errors.New("evidence_list_decode_failed")
		}
		e.SourceHash = hex.EncodeToString(hash)
		if len(visibleHash) == 32 {
			visible := hex.EncodeToString(visibleHash)
			e.VisibleSourceHash = &visible
		}
		out = append(out, e)
	}
	if rows.Err() != nil {
		return nil, errors.New("evidence_list_failed")
	}
	return out, nil
}
