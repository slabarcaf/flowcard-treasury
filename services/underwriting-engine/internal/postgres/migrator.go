// Package postgres owns underwriting persistence and explicit schema migrations.
package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationLockID int64 = 847293

type migration struct {
	version  int
	name     string
	checksum string
	sql      []byte
}

func loadMigrations() ([]migration, error) {
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, errors.New("migration_source_unavailable")
	}
	sort.Strings(files)
	migrations := make([]migration, 0, len(files))
	lastVersion := 0
	for _, name := range files {
		base := filepath.Base(name)
		parts := strings.SplitN(base, "_", 2)
		if len(parts) != 2 || !strings.HasSuffix(base, ".sql") {
			return nil, errors.New("migration_source_unavailable")
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil || version != lastVersion+1 {
			return nil, errors.New("migration_source_unavailable")
		}
		sql, err := migrationFiles.ReadFile(name)
		if err != nil {
			return nil, errors.New("migration_source_unavailable")
		}
		digest := sha256.Sum256(sql)
		migrations = append(migrations, migration{
			version:  version,
			name:     base,
			checksum: hex.EncodeToString(digest[:]),
			sql:      sql,
		})
		lastVersion = version
	}
	if len(migrations) == 0 {
		return nil, errors.New("migration_source_unavailable")
	}
	return migrations, nil
}

// RunMigrations applies numbered DDL through an explicitly privileged pool.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("migration_database_unavailable")
	}
	defer func() {
		// Rollback after commit is harmless and closes a failed transaction.
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return errors.New("migration_lock_failed")
	}
	if _, err := tx.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS underwriting;
		REVOKE ALL ON SCHEMA underwriting FROM PUBLIC;
		CREATE TABLE IF NOT EXISTS underwriting.schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			name text NOT NULL CHECK (length(btrim(name)) > 0),
			checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		REVOKE ALL ON underwriting.schema_migrations FROM PUBLIC;
	`); err != nil {
		return errors.New("migration_tracking_initialization_failed")
	}
	known := make(map[int]migration, len(migrations))
	for _, migration := range migrations {
		known[migration.version] = migration
	}
	rows, err := tx.Query(ctx, `SELECT version, name, checksum FROM underwriting.schema_migrations`)
	if err != nil {
		return errors.New("migration_tracking_read_failed")
	}
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			rows.Close()
			return errors.New("migration_tracking_read_failed")
		}
		migration, exists := known[version]
		if !exists {
			rows.Close()
			return errors.New("migration_source_missing")
		}
		if migration.name != name || migration.checksum != checksum {
			rows.Close()
			return errors.New("migration_checksum_mismatch")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return errors.New("migration_tracking_read_failed")
	}
	rows.Close()
	for _, migration := range migrations {
		var name, checksum string
		err := tx.QueryRow(ctx, `SELECT name, checksum FROM underwriting.schema_migrations
			WHERE version = $1`, migration.version).Scan(&name, &checksum)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, string(migration.sql)); err != nil {
				return errors.New("migration_failed_check_privileges_and_mapping_schema")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO underwriting.schema_migrations
				(version, name, checksum) VALUES ($1, $2, $3)`, migration.version,
				migration.name, migration.checksum); err != nil {
				return errors.New("migration_record_failed")
			}
		case err != nil:
			return errors.New("migration_tracking_read_failed")
		case name != migration.name || checksum != migration.checksum:
			return errors.New("migration_checksum_mismatch")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migration_commit_failed")
	}
	return nil
}
