// Package postgres owns servicing persistence and privileged migrations.
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

// VerifyDatabaseName confirms the connected PostgreSQL target before a CLI
// applies DDL or imports task-owned facts.
func VerifyDatabaseName(ctx context.Context, pool *pgxpool.Pool, expected string) error {
	if pool == nil || expected == "" {
		return errors.New("database_name_invalid")
	}
	var actual string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&actual); err != nil || actual != expected {
		return errors.New("database_name_mismatch")
	}
	return nil
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationLockID int64 = 847294

var migrationNames = []string{"001_initial.sql", "002_imports.sql", "003_accrual_lookup.sql", "004_offer_evidence.sql", "005_offer_evidence_immutable.sql", "006_funding_checks.sql", "007_worker_leases.sql", "008_dispatch_check_ref.sql", "009_servicing_webhook_jobs.sql", "010_deposit_conflict_hold.sql", "011_collections.sql", "012_offer_case_provenance.sql", "013_collection_review_requests.sql", "014_sandbox_actors.sql", "015_sandbox_import_journal.sql"}

func migrationSource(name string) ([]byte, string, error) {
	source, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return nil, "", errors.New("migration_source_unavailable")
	}
	sum := sha256.Sum256(source)
	return source, hex.EncodeToString(sum[:]), nil
}

// RunMigrations applies the checksum-pinned migration on a privileged pool.
// Runtime commands do not call this function.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("migration_database_unavailable")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return errors.New("migration_lock_failed")
	}
	if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS servicing;
		REVOKE ALL ON SCHEMA servicing FROM PUBLIC;
		CREATE TABLE IF NOT EXISTS servicing.schema_migrations (
			version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
		);
		REVOKE ALL ON servicing.schema_migrations FROM PUBLIC;`); err != nil {
		return errors.New("migration_tracking_initialization_failed")
	}
	if _, err := tx.Exec(ctx, `REVOKE ALL ON SCHEMA servicing FROM PUBLIC;
		REVOKE ALL ON servicing.schema_migrations FROM PUBLIC;`); err != nil {
		return errors.New("migration_tracking_privileges_failed")
	}
	for index, migrationName := range migrationNames {
		source, checksum, err := migrationSource(migrationName)
		if err != nil {
			return err
		}
		version := index + 1
		var name, recorded string
		err = tx.QueryRow(ctx, `SELECT name,checksum FROM servicing.schema_migrations WHERE version=$1`, version).Scan(&name, &recorded)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, string(source)); err != nil {
				return errors.New("migration_exec_failed_" + migrationName)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO servicing.schema_migrations(version,name,checksum) VALUES($1,$2,$3)`, version, migrationName, checksum); err != nil {
				return errors.New("migration_record_failed")
			}
		case err != nil:
			return errors.New("migration_tracking_read_failed")
		case name != migrationName || recorded != checksum:
			return errors.New("migration_checksum_mismatch")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migration_commit_failed")
	}
	return nil
}

// VerifyMigration reports whether the installed migration matches source.
func VerifyMigration(ctx context.Context, pool *pgxpool.Pool) error {
	for index, migrationName := range migrationNames {
		_, checksum, err := migrationSource(migrationName)
		if err != nil {
			return err
		}
		var name, recorded string
		if err := pool.QueryRow(ctx, `SELECT name,checksum FROM servicing.schema_migrations WHERE version=$1`, index+1).Scan(&name, &recorded); err != nil {
			return errors.New("database_schema_unavailable")
		}
		if name != migrationName || recorded != checksum {
			return errors.New("database_schema_mismatch")
		}
	}
	return nil
}

// MigrationChecksum returns the source checksum for readiness and reporting.
func MigrationChecksum() (string, error) {
	var hashes string
	for _, name := range migrationNames {
		_, sum, err := migrationSource(name)
		if err != nil {
			return "", errors.New("migration_checksum_unavailable")
		}
		hashes += name + ":" + sum + "\n"
	}
	sum := sha256.Sum256([]byte(hashes))
	return hex.EncodeToString(sum[:]), nil
}
