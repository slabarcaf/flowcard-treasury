package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
)

// SnapshotStore publishes append-only inputs through a restricted runtime pool.
type SnapshotStore struct {
	pool *pgxpool.Pool
}

// ConnectSnapshotStore opens a connection without running DDL.
func ConnectSnapshotStore(ctx context.Context, databaseURL string) (*SnapshotStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("database_configuration_invalid")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("database_configuration_invalid")
	}
	return &SnapshotStore{pool: pool}, nil
}

// Close releases all connections owned by the importer process.
func (s *SnapshotStore) Close() {
	s.pool.Close()
}

// DatabaseName returns the connected database for explicit demo-target checks.
func (s *SnapshotStore) DatabaseName(ctx context.Context) (string, error) {
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return "", errors.New("storage_unavailable")
	}
	return name, nil
}

// Ready verifies the migration identity and runtime grants before import.
func (s *SnapshotStore) Ready(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		var name, checksum string
		if err := s.pool.QueryRow(ctx, `SELECT name, checksum
			FROM underwriting.schema_migrations WHERE version = $1`, migration.version).
			Scan(&name, &checksum); err != nil || name != migration.name || checksum != migration.checksum {
			return errors.New("database_schema_mismatch")
		}
	}
	var recordedCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM underwriting.schema_migrations`).Scan(&recordedCount); err != nil || recordedCount != len(migrations) {
		return errors.New("database_schema_mismatch")
	}
	var restricted bool
	err = s.pool.QueryRow(ctx, `SELECT pg_has_role(current_user, 'underwriting_runtime', 'MEMBER')
		AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
		AND NOT has_schema_privilege(current_user, 'underwriting', 'CREATE')
		AND NOT has_schema_privilege(current_user, 'public', 'CREATE')
		AND NOT has_database_privilege(current_user, current_database(), 'CREATE')
		AND NOT has_table_privilege(current_user, 'underwriting.schema_migrations',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(current_user, 'public.customer_roster',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(current_user, 'public.issuing_card_bindings',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(current_user, 'underwriting.customer_snapshots', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'underwriting.facility_snapshots', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'underwriting.evaluations', 'UPDATE,DELETE,TRUNCATE')
		AND has_table_privilege(current_user, 'underwriting.customer_snapshots', 'SELECT,INSERT')
		AND has_table_privilege(current_user, 'underwriting.facility_snapshots', 'SELECT,INSERT')
		AND has_table_privilege(current_user, 'underwriting.evaluations', 'SELECT,INSERT')
		AND has_table_privilege(current_user, 'public.customer_roster', 'SELECT')
		AND has_table_privilege(current_user, 'public.issuing_card_bindings', 'SELECT')
		FROM pg_roles WHERE rolname = current_user`).Scan(&restricted)
	if err != nil || !restricted {
		return errors.New("database_runtime_privileges_invalid")
	}
	if _, err := s.pool.Exec(ctx, `SELECT 1 FROM underwriting.customer_snapshots,
		underwriting.facility_snapshots, underwriting.evaluations,
		public.customer_roster, public.issuing_card_bindings LIMIT 0`); err != nil {
		return errors.New("database_tables_unavailable")
	}
	return nil
}

// PublishCustomer serializes owner version allocation and verifies active mappings.
func (s *SnapshotStore) PublishCustomer(ctx context.Context, snapshot domain.CustomerSnapshot, digest [32]byte) (domain.ImportResult, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return domain.ImportResult{}, errors.New("invalid_snapshot")
	}
	refsJSON, err := jsonString(snapshot.SourceRefs)
	if err != nil {
		return domain.ImportResult{}, errors.New("invalid_snapshot")
	}
	coverageJSON, err := jsonString(snapshot.Coverage)
	if err != nil {
		return domain.ImportResult{}, errors.New("invalid_snapshot")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	defer func() {
		// Rollback after commit is harmless and closes any failed transaction.
		_ = tx.Rollback(ctx)
	}()
	lockKey := fmt.Sprintf("customer:%s:%s", snapshot.SourceMode, snapshot.CustomerID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 847293))`, lockKey); err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	if result, found, err := existingCustomerSource(ctx, tx, snapshot, digest); err != nil {
		return domain.ImportResult{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return domain.ImportResult{}, errors.New("storage_unavailable")
		}
		return result, nil
	}
	if err := validateActiveMapping(ctx, tx, snapshot); err != nil {
		return domain.ImportResult{}, err
	}
	var lastCaptured time.Time
	var nextVersion int64 = 1
	err = tx.QueryRow(ctx, `SELECT version, captured_at FROM underwriting.customer_snapshots
		WHERE source_mode = $1 AND customer_id = $2 ORDER BY version DESC LIMIT 1`,
		snapshot.SourceMode, snapshot.CustomerID).Scan(&nextVersion, &lastCaptured)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		nextVersion = 1
	} else {
		if snapshot.CapturedAt.Before(lastCaptured) {
			return domain.ImportResult{}, errors.New("snapshot_conflict")
		}
		if nextVersion == math.MaxInt64 {
			return domain.ImportResult{}, errors.New("snapshot_version_exhausted")
		}
		nextVersion++
	}
	var result domain.ImportResult
	err = tx.QueryRow(ctx, `INSERT INTO underwriting.customer_snapshots (
		source_mode, customer_id, account_id, card_id, version, schema_version,
		source_id, source_version, captured_at, source_refs, coverage, snapshot_json, content_hash
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb,$12::jsonb,$13)
	RETURNING id::text, version`, snapshot.SourceMode, snapshot.CustomerID,
		snapshot.AccountID, snapshot.CardID, nextVersion, snapshot.SchemaVersion,
		snapshot.SourceID, snapshot.SourceVersion, snapshot.CapturedAt,
		refsJSON, coverageJSON, string(encoded), digest[:]).
		Scan(&result.SnapshotID, &result.Version)
	if err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	return result, nil
}

// PublishFacility serializes selected-facility version allocation.
func (s *SnapshotStore) PublishFacility(ctx context.Context, snapshot domain.FacilitySnapshot, digest [32]byte) (domain.ImportResult, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return domain.ImportResult{}, errors.New("invalid_snapshot")
	}
	refsJSON, err := jsonString(snapshot.SourceRefs)
	if err != nil {
		return domain.ImportResult{}, errors.New("invalid_snapshot")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	defer func() {
		// Rollback after commit is harmless and closes any failed transaction.
		_ = tx.Rollback(ctx)
	}()
	lockKey := fmt.Sprintf("facility:%s:%s", snapshot.SourceMode, snapshot.FacilityID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 847293))`, lockKey); err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	if result, found, err := existingFacilitySource(ctx, tx, snapshot, digest); err != nil {
		return domain.ImportResult{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return domain.ImportResult{}, errors.New("storage_unavailable")
		}
		return result, nil
	}
	var nextVersion int64 = 1
	var lastCaptured time.Time
	err = tx.QueryRow(ctx, `SELECT version, captured_at FROM underwriting.facility_snapshots
		WHERE source_mode = $1 AND facility_id = $2 ORDER BY version DESC LIMIT 1`,
		snapshot.SourceMode, snapshot.FacilityID).Scan(&nextVersion, &lastCaptured)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		nextVersion = 1
	} else {
		if snapshot.CapturedAt.Before(lastCaptured) {
			return domain.ImportResult{}, errors.New("snapshot_conflict")
		}
		if nextVersion == math.MaxInt64 {
			return domain.ImportResult{}, errors.New("snapshot_version_exhausted")
		}
		nextVersion++
	}
	var result domain.ImportResult
	err = tx.QueryRow(ctx, `INSERT INTO underwriting.facility_snapshots (
		source_mode, facility_id, version, schema_version, source_id, source_version,
		captured_at, source_refs, ledger_version, ledger_complete, facility_total_cents,
		reserve_floor_bps, funded_principal_cents, reserved_principal_cents,
		snapshot_json, content_hash
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14,$15::jsonb,$16)
	RETURNING id::text, version`, snapshot.SourceMode, snapshot.FacilityID, nextVersion,
		snapshot.SchemaVersion, snapshot.SourceID, snapshot.SourceVersion, snapshot.CapturedAt,
		refsJSON, snapshot.LedgerVersion, snapshot.LedgerComplete,
		snapshot.FacilityTotalCents, snapshot.ReserveFloorBPS, snapshot.FundedPrincipalCents,
		snapshot.ReservedPrincipalCents, string(encoded), digest[:]).Scan(&result.SnapshotID, &result.Version)
	if err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ImportResult{}, errors.New("storage_unavailable")
	}
	return result, nil
}

func existingCustomerSource(ctx context.Context, tx pgx.Tx, snapshot domain.CustomerSnapshot, digest [32]byte) (domain.ImportResult, bool, error) {
	var result domain.ImportResult
	var existingHash []byte
	err := tx.QueryRow(ctx, `SELECT id::text, version, content_hash
		FROM underwriting.customer_snapshots WHERE source_mode = $1 AND customer_id = $2
		AND source_id = $3 AND source_version = $4`, snapshot.SourceMode,
		snapshot.CustomerID, snapshot.SourceID, snapshot.SourceVersion).
		Scan(&result.SnapshotID, &result.Version, &existingHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ImportResult{}, false, nil
	}
	if err != nil {
		return domain.ImportResult{}, false, errors.New("storage_unavailable")
	}
	if !bytes.Equal(existingHash, digest[:]) {
		return domain.ImportResult{}, false, errors.New("snapshot_conflict")
	}
	result.Replayed = true
	return result, true, nil
}

func existingFacilitySource(ctx context.Context, tx pgx.Tx, snapshot domain.FacilitySnapshot, digest [32]byte) (domain.ImportResult, bool, error) {
	var result domain.ImportResult
	var existingHash []byte
	err := tx.QueryRow(ctx, `SELECT id::text, version, content_hash
		FROM underwriting.facility_snapshots WHERE source_mode = $1 AND facility_id = $2
		AND source_id = $3 AND source_version = $4`, snapshot.SourceMode,
		snapshot.FacilityID, snapshot.SourceID, snapshot.SourceVersion).
		Scan(&result.SnapshotID, &result.Version, &existingHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ImportResult{}, false, nil
	}
	if err != nil {
		return domain.ImportResult{}, false, errors.New("storage_unavailable")
	}
	if !bytes.Equal(existingHash, digest[:]) {
		return domain.ImportResult{}, false, errors.New("snapshot_conflict")
	}
	result.Replayed = true
	return result, true, nil
}

func validateActiveMapping(ctx context.Context, tx pgx.Tx, snapshot domain.CustomerSnapshot) error {
	rows, err := tx.Query(ctx, `SELECT b.customer_id, b.airwallex_account_id,
		b.is_active, COALESCE(r.airwallex_connected_account_id, ''),
		COALESCE(r.is_active, false)
		FROM public.issuing_card_bindings b
		LEFT JOIN public.customer_roster r ON r.customer_id = b.customer_id
		WHERE b.card_id = $1 LIMIT 2`, snapshot.CardID)
	if err != nil {
		return errors.New("mapping_unavailable")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var customerID, accountID, rosterAccount string
		var bindingActive, rosterActive bool
		if err := rows.Scan(&customerID, &accountID, &bindingActive, &rosterAccount, &rosterActive); err != nil {
			return errors.New("mapping_unavailable")
		}
		count++
		if count > 1 || !bindingActive || !rosterActive || customerID != snapshot.CustomerID ||
			accountID != snapshot.AccountID || rosterAccount != snapshot.AccountID {
			return errors.New("mapping_unavailable")
		}
	}
	if rows.Err() != nil || count != 1 {
		return errors.New("mapping_unavailable")
	}
	return nil
}

func jsonString(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("snapshot_encoding_failed")
	}
	return string(encoded), nil
}

// CurrentSnapshots reads the newest compatible customer and facility pictures atomically.
func (s *SnapshotStore) CurrentSnapshots(ctx context.Context, mode, customerID, facilityID string) (*domain.CustomerSnapshotRecord, *domain.FacilitySnapshotRecord, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, errors.New("storage_unavailable")
	}
	defer func() {
		// Rollback after commit is harmless and closes any failed transaction.
		_ = tx.Rollback(ctx)
	}()
	customer, err := currentCustomer(ctx, tx, mode, customerID)
	if err != nil {
		return nil, nil, err
	}
	facility, err := currentFacility(ctx, tx, mode, facilityID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, errors.New("storage_unavailable")
	}
	return customer, facility, nil
}

// CurrentCustomerSnapshot reads one current customer picture without requiring facility data.
func (s *SnapshotStore) CurrentCustomerSnapshot(ctx context.Context, mode, customerID string) (*domain.CustomerSnapshotRecord, error) {
	return currentCustomer(ctx, s.pool, mode, customerID)
}

type snapshotQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// PostgreSQL stores microseconds. Compare the immutable JSON instant at that
// precision so sub-microsecond source digits do not make valid rows unreadable.
func currentCustomer(ctx context.Context, querier snapshotQuerier, mode, customerID string) (*domain.CustomerSnapshotRecord, error) {
	var record domain.CustomerSnapshotRecord
	var sourceMode, storedCustomerID, accountID, cardID string
	var capturedAt time.Time
	var snapshotJSON []byte
	err := querier.QueryRow(ctx, `SELECT id::text, source_mode, customer_id, account_id, card_id,
		version, captured_at, snapshot_json
		FROM underwriting.customer_snapshots
		WHERE source_mode = $1 AND customer_id = $2
		ORDER BY version DESC LIMIT 1`, mode, customerID).
		Scan(&record.ID, &sourceMode, &storedCustomerID, &accountID, &cardID,
			&record.Version, &capturedAt, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("storage_unavailable")
	}
	if json.Unmarshal(snapshotJSON, &record.Snapshot) != nil ||
		record.Snapshot.SourceMode != sourceMode || record.Snapshot.CustomerID != storedCustomerID ||
		record.Snapshot.AccountID != accountID || record.Snapshot.CardID != cardID ||
		!record.Snapshot.CapturedAt.Truncate(time.Microsecond).Equal(capturedAt) {
		return nil, errors.New("storage_unavailable")
	}
	record.Snapshot.CapturedAt = capturedAt.UTC()
	return &record, nil
}

func currentFacility(ctx context.Context, querier snapshotQuerier, mode, facilityID string) (*domain.FacilitySnapshotRecord, error) {
	var record domain.FacilitySnapshotRecord
	var sourceMode, storedFacilityID string
	var capturedAt time.Time
	var snapshotJSON []byte
	err := querier.QueryRow(ctx, `SELECT id::text, source_mode, facility_id, version,
		captured_at, snapshot_json FROM underwriting.facility_snapshots
		WHERE source_mode = $1 AND facility_id = $2
		ORDER BY version DESC LIMIT 1`, mode, facilityID).
		Scan(&record.ID, &sourceMode, &storedFacilityID, &record.Version,
			&capturedAt, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("storage_unavailable")
	}
	if json.Unmarshal(snapshotJSON, &record.Snapshot) != nil ||
		record.Snapshot.SourceMode != sourceMode || record.Snapshot.FacilityID != storedFacilityID ||
		!record.Snapshot.CapturedAt.Truncate(time.Microsecond).Equal(capturedAt) {
		return nil, errors.New("storage_unavailable")
	}
	record.Snapshot.CapturedAt = capturedAt.UTC()
	return &record, nil
}
