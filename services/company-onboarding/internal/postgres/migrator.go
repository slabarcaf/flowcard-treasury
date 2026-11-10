package postgres

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Single canonical embedded migration sources for the onboarding service.
//
//go:embed migrations/001_initial_onboarding_schema.sql
var migration001 string

//go:embed migrations/002_submission_dispatch_steps.sql
var migration002 string

//go:embed migrations/003_platform_creation_coordinator.sql
var migration003 string

//go:embed migrations/004_webhook_inbox.sql
var migration004 string

//go:embed migrations/005_webhook_inbox_lease_token.sql
var migration005 string

//go:embed migrations/006_reconciliation_next_check_and_inbox_processing.sql
var migration006 string

// Migration represents a numbered schema migration.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

func calculateChecksum(sql string) string {
	h := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(h[:])
}

// AllMigrations returns the ordered list of schema migrations owned by this service.
func AllMigrations() []Migration {
	return []Migration{
		{
			Version:  1,
			Name:     "001_initial_onboarding_schema",
			SQL:      migration001,
			Checksum: calculateChecksum(migration001),
		},
		{
			Version:  2,
			Name:     "002_submission_dispatch_steps",
			SQL:      migration002,
			Checksum: calculateChecksum(migration002),
		},
		{
			Version:  3,
			Name:     "003_platform_creation_coordinator",
			SQL:      migration003,
			Checksum: calculateChecksum(migration003),
		},
		{
			Version:  4,
			Name:     "004_webhook_inbox",
			SQL:      migration004,
			Checksum: calculateChecksum(migration004),
		},
		{
			Version:  5,
			Name:     "005_webhook_inbox_lease_token",
			SQL:      migration005,
			Checksum: calculateChecksum(migration005),
		},
		{
			Version:  6,
			Name:     "006_reconciliation_next_check_and_inbox_processing",
			SQL:      migration006,
			Checksum: calculateChecksum(migration006),
		},
	}
}

// Dedicated 64-bit advisory lock key to serialize concurrent migration executions.
const migrationAdvisoryLockID = 847291

// ErrMigrationDrift is returned when an applied migration has been tampered with or differs from code.
var ErrMigrationDrift = errors.New("migration drift detected: applied checksum does not match current code")

// RunMigrations applies all pending migrations in order to the database pool,
// guarded by a PostgreSQL advisory lock on a dedicated acquired connection and checksum verification.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// 1. Acquire a dedicated connection from pool so session-level advisory lock is bound to this exact connection
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for migrations: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationAdvisoryLockID)
		conn.Release()
	}()

	// Acquire advisory lock to serialize concurrent service startup / migration runs
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLockID); err != nil {
		return fmt.Errorf("failed to acquire migration advisory lock: %w", err)
	}

	// 2. Ensure schema and tracking table exist with checksum column
	initSQL := `
		CREATE SCHEMA IF NOT EXISTS onboarding;
		CREATE TABLE IF NOT EXISTS onboarding.schema_migrations (
			version INT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			checksum VARCHAR(64) NOT NULL DEFAULT '',
			applied_at TIMESTAMPTZ NOT NULL
		);
		ALTER TABLE onboarding.schema_migrations ADD COLUMN IF NOT EXISTS checksum VARCHAR(64) NOT NULL DEFAULT '';
	`
	if _, err := conn.Exec(ctx, initSQL); err != nil {
		return fmt.Errorf("failed to initialize schema_migrations table: %w", err)
	}

	// 3. Query applied migrations and their recorded checksums
	rows, err := conn.Query(ctx, "SELECT version, name, checksum FROM onboarding.schema_migrations ORDER BY version ASC")
	if err != nil {
		return fmt.Errorf("failed to query applied migrations: %w", err)
	}
	defer rows.Close()

	appliedChecksums := make(map[int]string)
	for rows.Next() {
		var (
			v        int
			name     string
			checksum string
		)
		if err := rows.Scan(&v, &name, &checksum); err != nil {
			return fmt.Errorf("failed to scan applied migration record: %w", err)
		}
		appliedChecksums[v] = checksum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows error querying applied migrations: %w", err)
	}

	// 4. Verify existing migrations for drift and apply pending migrations
	for _, m := range AllMigrations() {
		recordedChecksum, isApplied := appliedChecksums[m.Version]
		if isApplied {
			if recordedChecksum != "" && recordedChecksum != m.Checksum {
				return fmt.Errorf("%w for version %d (%s): recorded %s, expected %s",
					ErrMigrationDrift, m.Version, m.Name, recordedChecksum, m.Checksum)
			}
			continue
		}

		// Apply pending migration in an isolated transaction on this dedicated connection
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin transaction for migration %d: %w", m.Version, err)
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()

		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return fmt.Errorf("failed executing migration %d (%s): %w", m.Version, m.Name, err)
		}

		recordSQL := `
			INSERT INTO onboarding.schema_migrations (version, name, checksum, applied_at)
			VALUES ($1, $2, $3, $4)
		`
		if _, err := tx.Exec(ctx, recordSQL, m.Version, m.Name, m.Checksum, time.Now().UTC()); err != nil {
			return fmt.Errorf("failed recording migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed committing migration %d: %w", m.Version, err)
		}
	}

	return nil
}

var (
	// reURICredentials redacts credentials in postgres:// or postgresql:// URIs preceding @.
	reURICredentials = regexp.MustCompile(`(?i)(postgres(?:ql)?://)[^@\r\n]+?@`)

	// reURINoAt redacts user:password patterns in malformed URIs lacking an @.
	reURINoAt = regexp.MustCompile(`(?i)(postgres(?:ql)?://[^:/\s@]+):[^\s/@?#]+`)

	// reKVPassword redacts keyword-value passwords (password=..., pwd=..., secret=...)
	// supporting arbitrary casing, optional spaces around =, and single/double quoted or unquoted values.
	reKVPassword = regexp.MustCompile(`(?i)\b(password|pwd|secret)\s*=\s*(?:'[^']*'|"[^"]*"|[^\s;,]+)`)
)

// Safe typed sentinel errors for startup and schema verification.
var (
	ErrDatabaseUnreachable = errors.New("DATABASE_UNREACHABLE: failed to establish connection to database")
	ErrSchemaNotReady      = errors.New("SCHEMA_NOT_READY: schema migrations table missing or inaccessible")
	ErrSchemaIncomplete    = errors.New("SCHEMA_INCOMPLETE: required migrations are missing or unapplied")
	ErrSchemaTablesMissing = errors.New("SCHEMA_TABLES_MISSING: required schema tables are missing or inaccessible")
	ErrSchemaMismatch      = errors.New("SCHEMA_MISMATCH: applied migration metadata does not match code")
)

// SanitizeDBError scrubs passwords, credentials, and DSN details from error messages.
// It redacts URI credentials (including percent-encoded or malformed tokens) and
// keyword-value connection string passwords (quoted or unquoted, case-insensitive).
func SanitizeDBError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	msg = reURICredentials.ReplaceAllString(msg, "${1}[redacted]@")
	msg = reURINoAt.ReplaceAllString(msg, "${1}:[redacted]")
	msg = reKVPassword.ReplaceAllString(msg, "${1}=[redacted]")
	return errors.New(msg)
}

// VerifySchemaReadiness checks that the required onboarding schema and durable tables exist
// in PostgreSQL without executing any DDL or modifying database state.
// Derives expected migrations dynamically from AllMigrations() and verifies each version,
// name, and checksum in onboarding.schema_migrations.
// Also verifies that all 10 durable schema tables are accessible.
// Fails startup with safe typed errors if the database is unreachable or schema is incomplete.
func VerifySchemaReadiness(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("DATABASE_POOL_NIL: database pool is nil")
	}

	// 1. Verify connection reachability
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrDatabaseUnreachable, SanitizeDBError(err))
	}

	// 2. Dynamically derive expected migrations from AllMigrations() and verify each in database
	expected := AllMigrations()
	rows, err := pool.Query(ctx, "SELECT version, name, checksum FROM onboarding.schema_migrations ORDER BY version ASC")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaNotReady, SanitizeDBError(err))
	}
	defer rows.Close()

	applied := make(map[int]struct {
		Name     string
		Checksum string
	})
	for rows.Next() {
		var v int
		var name, checksum string
		if err := rows.Scan(&v, &name, &checksum); err != nil {
			return fmt.Errorf("%w: failed scanning migration row: %v", ErrSchemaNotReady, SanitizeDBError(err))
		}
		applied[v] = struct {
			Name     string
			Checksum string
		}{Name: name, Checksum: checksum}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: error reading migration rows: %v", ErrSchemaNotReady, SanitizeDBError(err))
	}

	if len(applied) < len(expected) {
		return fmt.Errorf("%w: found %d of %d required migrations applied in onboarding.schema_migrations", ErrSchemaIncomplete, len(applied), len(expected))
	}

	for _, m := range expected {
		app, ok := applied[m.Version]
		if !ok {
			return fmt.Errorf("%w: migration version %d (%s) is missing", ErrSchemaIncomplete, m.Version, m.Name)
		}
		if app.Name != m.Name {
			return fmt.Errorf("%w: migration version %d has name %q in database, expected %q", ErrSchemaMismatch, m.Version, app.Name, m.Name)
		}
		if app.Checksum != m.Checksum {
			return fmt.Errorf("%w: migration version %d (%s) has checksum %q in database, expected %q", ErrSchemaMismatch, m.Version, m.Name, app.Checksum, m.Checksum)
		}
	}

	// 3. Verify all durable schema tables are accessible via zero-row query
	checkSQL := `
		SELECT 1 FROM onboarding.companies,
		              onboarding.applications,
		              onboarding.submissions,
		              onboarding.platform_accounts,
		              onboarding.outbox_messages,
		              onboarding.idempotency_keys,
		              onboarding.submission_dispatch_steps,
		              onboarding.submission_document_uploads,
		              onboarding.platform_creation_coordinators,
		              onboarding.webhook_inbox
		LIMIT 0;
	`
	if _, err := pool.Exec(ctx, checkSQL); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaTablesMissing, SanitizeDBError(err))
	}

	return nil
}
