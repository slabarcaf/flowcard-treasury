package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
)

var ErrIdempotencyConflict = errors.New("idempotency_conflict")

// EvaluationInsert contains a complete immutable decision and all copied evidence.
type EvaluationInsert struct {
	Mode             string
	Caller           string
	CustomerID       string
	ShortfallID      string
	IdempotencyKey   string
	RequestHash      [32]byte
	Request          domain.EvaluationRequest
	Shortfall        domain.ShortfallRecord
	CustomerSnapshot *domain.CustomerSnapshotRecord
	FacilitySnapshot *domain.FacilitySnapshotRecord
	InputFacts       json.RawMessage
	EvaluatedAt      time.Time
	PolicyID         string
	PolicyHash       string
	Response         domain.EvaluationResponse
}

// ExistingEvaluation is a completed result saved under one scoped key.
type ExistingEvaluation struct {
	RequestHash [32]byte
	Body        json.RawMessage
}

// FindEvaluation returns a saved key before dependency reads begin.
func (s *SnapshotStore) FindEvaluation(ctx context.Context, mode, caller, key string) (ExistingEvaluation, bool, error) {
	var hash []byte
	var body []byte
	err := s.pool.QueryRow(ctx, `SELECT request_hash, result_json
		FROM underwriting.evaluations
		WHERE source_mode = $1 AND caller = $2 AND idempotency_key = $3`, mode, caller, key).
		Scan(&hash, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExistingEvaluation{}, false, nil
	}
	if err != nil || len(hash) != 32 {
		return ExistingEvaluation{}, false, errors.New("storage_unavailable")
	}
	var result ExistingEvaluation
	copy(result.RequestHash[:], hash)
	result.Body = append(json.RawMessage(nil), body...)
	return result, true, nil
}

// SaveEvaluation atomically stores the response and returns the winning saved body.
func (s *SnapshotStore) SaveEvaluation(ctx context.Context, record EvaluationInsert) (json.RawMessage, bool, error) {
	requestJSON, err := json.Marshal(record.Request)
	if err != nil {
		return nil, false, errors.New("storage_unavailable")
	}
	shortfallJSON, err := json.Marshal(record.Shortfall)
	if err != nil {
		return nil, false, errors.New("storage_unavailable")
	}
	if !json.Valid(record.InputFacts) {
		return nil, false, errors.New("storage_unavailable")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, false, errors.New("storage_unavailable")
	}
	defer func() {
		// Rollback after commit is harmless and closes any failed transaction.
		_ = tx.Rollback(ctx)
	}()
	var id string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text, transaction_timestamp()`).
		Scan(&id, &createdAt); err != nil {
		return nil, false, errors.New("storage_unavailable")
	}
	response := record.Response
	response.ID = id
	response.CreatedAt = createdAt.UTC()
	responseBody, err := json.Marshal(response)
	if err != nil {
		return nil, false, errors.New("storage_unavailable")
	}

	var customerSnapshotID, customerSnapshotVersion any
	if record.CustomerSnapshot != nil {
		customerSnapshotID = record.CustomerSnapshot.ID
		customerSnapshotVersion = record.CustomerSnapshot.Version
	}
	var facilitySnapshotID, facilitySnapshotVersion any
	if record.FacilitySnapshot != nil {
		facilitySnapshotID = record.FacilitySnapshot.ID
		facilitySnapshotVersion = record.FacilitySnapshot.Version
	}
	var savedBody []byte
	err = tx.QueryRow(ctx, `INSERT INTO underwriting.evaluations (
		id, source_mode, caller, customer_id, shortfall_id, idempotency_key,
		request_hash, request_json, shortfall_json, customer_snapshot_id,
		customer_snapshot_version, facility_snapshot_id, facility_snapshot_version,
		input_facts, evaluated_at, policy_id, policy_hash, result_json, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10,$11,$12,$13,
		$14::jsonb,$15,$16,$17,$18::jsonb,$19)
	ON CONFLICT (source_mode, caller, idempotency_key) DO NOTHING
	RETURNING result_json`, id, record.Mode, record.Caller, record.CustomerID,
		record.ShortfallID, record.IdempotencyKey, record.RequestHash[:], string(requestJSON),
		string(shortfallJSON), customerSnapshotID, customerSnapshotVersion,
		facilitySnapshotID, facilitySnapshotVersion, string(record.InputFacts),
		record.EvaluatedAt.UTC(), record.PolicyID, record.PolicyHash, string(responseBody), createdAt).
		Scan(&savedBody)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, errors.New("storage_unavailable")
		}
		return append(json.RawMessage(nil), savedBody...), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, errors.New("storage_unavailable")
	}

	var existingHash []byte
	err = tx.QueryRow(ctx, `SELECT request_hash, result_json
		FROM underwriting.evaluations
		WHERE source_mode = $1 AND caller = $2 AND idempotency_key = $3`,
		record.Mode, record.Caller, record.IdempotencyKey).Scan(&existingHash, &savedBody)
	if err != nil || len(existingHash) != len(record.RequestHash) {
		return nil, false, errors.New("storage_unavailable")
	}
	if !bytes.Equal(existingHash, record.RequestHash[:]) {
		return nil, false, ErrIdempotencyConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, errors.New("storage_unavailable")
	}
	return append(json.RawMessage(nil), savedBody...), false, nil
}

// GetEvaluation reads a saved response without reevaluation.
func (s *SnapshotStore) GetEvaluation(ctx context.Context, mode, id string) (json.RawMessage, bool, error) {
	var body []byte
	err := s.pool.QueryRow(ctx, `SELECT result_json FROM underwriting.evaluations
		WHERE source_mode = $1 AND id = $2::uuid`, mode, id).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("storage_unavailable")
	}
	return append(json.RawMessage(nil), body...), true, nil
}

// ListEvaluations reads one mode-scoped page with an optional customer filter.
func (s *SnapshotStore) ListEvaluations(ctx context.Context, mode, customerID string, cursor *domain.EvaluationCursor) (domain.EvaluationPage, error) {
	var afterTime *time.Time
	afterID := "00000000-0000-0000-0000-000000000000"
	if cursor != nil {
		afterTime = &cursor.CreatedAt
		afterID = cursor.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text, created_at, result_json
		FROM underwriting.evaluations
		WHERE source_mode = $1 AND ($2::text = '' OR customer_id = $2)
		AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY created_at DESC, id DESC LIMIT 26`, mode, customerID, afterTime, afterID)
	if err != nil {
		return domain.EvaluationPage{}, errors.New("storage_unavailable")
	}
	defer rows.Close()
	type row struct {
		id        string
		createdAt time.Time
		body      []byte
	}
	pageRows := make([]row, 0, 26)
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.createdAt, &item.body); err != nil {
			return domain.EvaluationPage{}, errors.New("storage_unavailable")
		}
		pageRows = append(pageRows, item)
	}
	if rows.Err() != nil {
		return domain.EvaluationPage{}, errors.New("storage_unavailable")
	}
	page := domain.EvaluationPage{Items: make([]json.RawMessage, 0, 25)}
	if len(pageRows) > 25 {
		pageRows = pageRows[:25]
		last := pageRows[len(pageRows)-1]
		page.NextCursor = &domain.EvaluationCursor{Mode: mode, CustomerID: customerID,
			CreatedAt: last.createdAt.UTC(), ID: last.id}
	}
	for _, item := range pageRows {
		page.Items = append(page.Items, append(json.RawMessage(nil), item.body...))
	}
	return page, nil
}
