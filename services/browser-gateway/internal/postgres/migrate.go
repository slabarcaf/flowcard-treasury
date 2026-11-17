package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Admin opens a separate administrative pool and verifies the target before writes.
func Admin(ctx context.Context, databaseURL, expected string) (*pgxpool.Pool, error) {
	if databaseURL == "" || expected == "" {
		return nil, errors.New("administrative URL and expected database name required")
	}
	s, err := Open(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	var actual string
	err = s.Pool.QueryRow(ctx, `SELECT current_database()`).Scan(&actual)
	if err != nil || actual != expected {
		s.Pool.Close()
		return nil, errors.New("administrative database target mismatch")
	}
	return s.Pool, nil
}

// Migrate serializes checksum-verified, forward-only migrations in one transaction.
func Migrate(ctx context.Context, pool *pgxpool.Pool, expected string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // Rollback after Commit is harmless.
	var actual string
	if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&actual); err != nil {
		return err
	}
	if expected == "" || actual != expected {
		return errors.New("migration database target mismatch")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(729415689214)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS browser_gateway; REVOKE ALL ON SCHEMA browser_gateway FROM PUBLIC; CREATE TABLE IF NOT EXISTS browser_gateway.schema_migrations(name text PRIMARY KEY,checksum text NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	known := make(map[string]string)
	for _, e := range entries {
		data, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		known[e.Name()] = hex.EncodeToString(sum[:])
	}
	rows, err := tx.Query(ctx, `SELECT name,checksum FROM browser_gateway.schema_migrations`)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			rows.Close()
			return err
		}
		expectedChecksum, knownMigration := known[name]
		if !knownMigration || expectedChecksum != checksum {
			rows.Close()
			return errors.New("migration checksum mismatch")
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range entries {
		if applied[e.Name()] {
			continue
		}
		data, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(data)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO browser_gateway.schema_migrations(name,checksum) VALUES($1,$2)`, e.Name(), known[e.Name()]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
