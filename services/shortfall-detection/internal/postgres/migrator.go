// Package postgres owns shortfall persistence and explicit schema migrations.
package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration serialization is separate from the onboarding migration lock.
const migrationLockID int64 = 847292

const migrationName = "001_shortfall.sql"

func migrationSource() ([]byte, string, error) {
	sql, err := migrationFiles.ReadFile("migrations/" + migrationName)
	if err != nil {
		return nil, "", errors.New("migration_source_unavailable")
	}
	sum := sha256.Sum256(sql)
	return sql, hex.EncodeToString(sum[:]), nil
}

// RunMigrations applies numbered DDL and grants using a privileged connection.
// Applied SQL is checksum-verified on reruns; runtime never calls this function.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	sql, checksum, err := migrationSource()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("migration_database_unavailable")
	}
	defer func() {
		// Rollback after commit is harmless; pgx closes a failed connection if needed.
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return errors.New("migration_lock_failed")
	}
	if _, err := tx.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS shortfall;
		REVOKE ALL ON SCHEMA shortfall FROM PUBLIC;
		CREATE TABLE IF NOT EXISTS shortfall.schema_migrations (
			version integer PRIMARY KEY,
			name text NOT NULL,
			checksum text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		REVOKE ALL ON shortfall.schema_migrations FROM PUBLIC;
	`); err != nil {
		return errors.New("migration_tracking_initialization_failed")
	}
	var recordedName, recordedChecksum string
	err = tx.QueryRow(ctx, `SELECT name, checksum FROM shortfall.schema_migrations
		WHERE version = 1`).Scan(&recordedName, &recordedChecksum)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return errors.New("migration_001_failed_check_privileges_and_mapping_schema")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shortfall.schema_migrations
			(version, name, checksum) VALUES (1, $1, $2)`, migrationName, checksum); err != nil {
			return errors.New("migration_record_failed")
		}
	case err != nil:
		return errors.New("migration_tracking_read_failed")
	case recordedName != migrationName || recordedChecksum != checksum:
		return errors.New("migration_checksum_mismatch")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migration_commit_failed")
	}
	return nil
}
