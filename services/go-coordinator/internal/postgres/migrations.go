// Package postgres implements durable coordinator state and migrations.
package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationLockID int64 = 928401

type migration struct {
	version int
	name    string
}

var migrations = []migration{{1, "001_coordinator.sql"}, {2, "002_coordinator_state.sql"}, {3, "003_scheduler_retries.sql"}, {4, "004_feed_holds.sql"}, {5, "005_agent_evidence.sql"}, {6, "006_evidence_visible_hash.sql"}, {7, "007_workflow_admission_audit.sql"}, {8, "008_private_raw_account_id.sql"}, {9, "009_run_private_context_publication.sql"}}

func migrationSource(name string) ([]byte, string, error) {
	sql, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return nil, "", errors.New("migration_source_unavailable")
	}
	sum := sha256.Sum256(sql)
	return sql, hex.EncodeToString(sum[:]), nil
}

// RunMigrations applies all numbered migrations in one serialized transaction.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("migration_database_unavailable")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return errors.New("migration_lock_failed")
	}
	if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS coordinator; REVOKE ALL ON SCHEMA coordinator FROM PUBLIC`); err != nil {
		return errors.New("migration_schema_initialization_failed")
	}
	var trackingExists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('coordinator.schema_migrations') IS NOT NULL`).Scan(&trackingExists); err != nil {
		return errors.New("migration_tracking_probe_failed")
	}
	if !trackingExists {
		sql, checksum, err := migrationSource(migrations[0].name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return errors.New("migration_001_failed")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO coordinator.schema_migrations(version,name,checksum) VALUES($1,$2,$3)`, migrations[0].version, migrations[0].name, checksum); err != nil {
			return errors.New("migration_001_record_failed")
		}
		if _, err := tx.Exec(ctx, `GRANT SELECT ON coordinator.schema_migrations TO coordinator_runtime`); err != nil {
			return errors.New("migration_tracking_grant_failed")
		}
	} else {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM coordinator.schema_migrations`).Scan(&count); err != nil {
			return errors.New("migration_history_read_failed")
		}
		if count == 0 {
			return errors.New("migration_history_inconsistent")
		}
	}
	for _, item := range migrations {
		sql, checksum, err := migrationSource(item.name)
		if err != nil {
			return err
		}
		var name, recorded string
		err = tx.QueryRow(ctx, `SELECT name,checksum FROM coordinator.schema_migrations WHERE version=$1`, item.version).Scan(&name, &recorded)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if item.version == 1 {
				return errors.New("migration_history_inconsistent")
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return errors.New("migration_forward_failed")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO coordinator.schema_migrations(version,name,checksum) VALUES($1,$2,$3)`, item.version, item.name, checksum); err != nil {
				return errors.New("migration_record_failed")
			}
		case err != nil:
			return errors.New("migration_history_read_failed")
		case name != item.name || recorded != checksum:
			return errors.New("migration_checksum_mismatch")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migration_commit_failed")
	}
	return nil
}

// OpenPrivilegedPool opens a pool for the explicit migration command only.
func OpenPrivilegedPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, errors.New("database_configuration_invalid")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("database_configuration_invalid")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database_unavailable")
	}
	return pool, nil
}
