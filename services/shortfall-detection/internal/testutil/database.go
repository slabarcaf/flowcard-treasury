// Package testutil provides isolated PostgreSQL and loopback fixtures for the
// three focused shortfall verification functions. It is never runtime wiring.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/postgres"
)

// Database owns only a uniquely named disposable database and login.
type Database struct {
	Admin      *pgxpool.Pool
	RuntimeSQL *pgxpool.Pool
	Repo       *postgres.Repository
	Name       string
	Role       string
}

// ServiceDir returns the service source directory for immutable fixture/DDL reads.
func ServiceDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

// Check reports stage and SQLSTATE only, never a credential-bearing driver error.
func Check(t *testing.T, stage string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if pgerr, ok := err.(*pgconn.PgError); ok {
		t.Fatalf("%s: SQLSTATE %s", stage, pgerr.Code)
	}
	t.Fatalf("%s failed", stage)
}

// NewDatabase provisions against the existing isolated test PostgreSQL only.
// It never connects to root .env, resets another database, or drops shared roles.
func NewDatabase(t *testing.T, label string) *Database {
	t.Helper()
	dsn := os.Getenv("SHORTFALL_TEST_ADMIN_URL")
	if dsn == "" {
		dsn = "postgres://test_onboarding:test_password@127.0.0.1:54329/postgres?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	Check(t, "parse isolated admin config", err)
	if (cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost") ||
		cfg.ConnConfig.Port != 54329 || cfg.ConnConfig.Database != "postgres" {
		t.Fatal("refusing admin connection outside loopback:54329/postgres")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	Check(t, "open isolated admin", err)
	var bytes [8]byte
	_, err = rand.Read(bytes[:])
	Check(t, "generate disposable suffix", err)
	name := "shortfall_g_" + label + "_" + hex.EncodeToString(bytes[:])
	role := name + "_login"
	if len(role) > 63 || !strings.HasPrefix(name, "shortfall_g_") {
		t.Fatal("unsafe disposable identifier")
	}
	db := &Database{Name: name, Role: role}
	createdDB, createdLogin := false, false
	t.Cleanup(func() {
		if db.Repo != nil {
			db.Repo.Close()
		}
		if db.RuntimeSQL != nil {
			db.RuntimeSQL.Close()
		}
		if db.Admin != nil {
			db.Admin.Close()
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		// Exact captured names and prefix guard, no FORCE, shared role, or volume cleanup.
		if name != db.Name || role != db.Role || !strings.HasPrefix(name, "shortfall_g_"+label+"_") {
			t.Error("cleanup ownership guard failed")
			return
		}
		if createdDB {
			if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Error("disposable DB cleanup failed")
			}
		}
		if createdLogin {
			if _, err := admin.Exec(cleanupCtx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("disposable login cleanup failed")
			}
		}
		admin.Close()
		t.Logf("cleanup database=%s login=%s; shared shortfall_runtime preserved", name, role)
	})
	var version int
	Check(t, "read server version", admin.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version))
	if version < 160000 {
		t.Fatal("PostgreSQL 16+ required")
	}
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	Check(t, "create disposable DB", err)
	createdDB = true
	_, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+
		" LOGIN PASSWORD 'synthetic-shortfall-test-password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
	Check(t, "create disposable login", err)
	createdLogin = true
	dbConfig := cfg.Copy()
	dbConfig.ConnConfig.Database = name
	db.Admin, err = pgxpool.NewWithConfig(ctx, dbConfig)
	Check(t, "connect disposable DB", err)
	for _, filename := range []string{"002_customer_roster.sql", "008_remote_authorization.sql"} {
		data, err := os.ReadFile(filepath.Join(ServiceDir(), "../../server/src/db/migrations", filename))
		Check(t, "read actual Node DDL", err)
		sql := string(data)
		end := strings.Index(sql, ");")
		if end < 0 {
			t.Fatal("mapping DDL boundary missing")
		}
		_, err = db.Admin.Exec(ctx, sql[:end+2])
		Check(t, "apply exact Node mapping table DDL", err)
	}
	// Serialize first-time cluster role creation across all test packages through
	// one connection in the same admin database, not separate per-database locks.
	connection, err := admin.Acquire(ctx)
	Check(t, "acquire bootstrap lock connection", err)
	_, err = connection.Exec(ctx, "SELECT pg_advisory_lock(847293)")
	Check(t, "lock shared role bootstrap", err)
	defer func() {
		unlockCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, unlockErr := connection.Exec(unlockCtx, "SELECT pg_advisory_unlock(847293)")
		if unlockErr != nil {
			t.Error("bootstrap unlock failed")
		}
		connection.Release()
	}()
	var rolePreexisting bool
	Check(t, "inspect shared role", connection.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='shortfall_runtime')").Scan(&rolePreexisting))
	Check(t, "first migration", postgres.RunMigrations(ctx, db.Admin))
	Check(t, "second migration/checksum", postgres.RunMigrations(ctx, db.Admin))
	_, err = db.Admin.Exec(ctx, "GRANT shortfall_runtime TO "+pgx.Identifier{role}.Sanitize())
	Check(t, "grant restricted runtime membership", err)
	runtimeConfig := dbConfig.Copy()
	runtimeConfig.ConnConfig.User = role
	runtimeConfig.ConnConfig.Password = "synthetic-shortfall-test-password"
	db.RuntimeSQL, err = pgxpool.NewWithConfig(ctx, runtimeConfig)
	Check(t, "connect restricted SQL", err)
	runtimeURL, err := url.Parse(dsn)
	Check(t, "parse isolated runtime URL", err)
	if runtimeURL.Scheme != "postgres" && runtimeURL.Scheme != "postgresql" {
		t.Fatal("isolated admin URL must be a postgres URI")
	}
	runtimeURL.Path = "/" + name
	runtimeURL.User = url.UserPassword(role, "synthetic-shortfall-test-password")
	db.Repo, err = postgres.Open(ctx, runtimeURL.String())
	Check(t, "restricted schema/role readiness", err)
	_, err = db.Admin.Exec(ctx, `INSERT INTO public.customer_roster
		(customer_id,salesforce_account_id,airwallex_connected_account_id,enrolled_by,enrollment_reason)
		VALUES ('fixture-customer','fixture-salesforce','fixture-connected-account','test','synthetic');
		INSERT INTO public.issuing_card_bindings
		(card_id,customer_id,airwallex_account_id,bound_by,binding_reason)
		VALUES ('00000000-0000-4000-8000-000000000001','fixture-customer','fixture-connected-account','test','synthetic');`)
	Check(t, "seed disposable mappings", err)
	t.Logf("database=%s login=%s PostgreSQL=%d migrations=twice runtime_role_preexisting=%t", name, role, version, rolePreexisting)
	return db
}
