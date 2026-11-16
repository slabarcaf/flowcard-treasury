// Package database provides explicitly invoked, owned PostgreSQL test databases.
package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

const ownerPrefix = "funding_test"

const customerRosterFixtureDDL = `CREATE TABLE public.customer_roster (
	customer_id text PRIMARY KEY,
	airwallex_connected_account_id text NOT NULL,
	is_active boolean NOT NULL
)`

const cardBindingFixtureDDL = `CREATE TABLE public.issuing_card_bindings (
	card_id text PRIMARY KEY,
	customer_id text NOT NULL,
	airwallex_account_id text NOT NULL,
	is_active boolean NOT NULL
)`

// Mapping contains only the source identities needed by the verified-mapping
// function. SeedMapping cannot mutate servicing application state.
type Mapping struct {
	CustomerID string
	AccountID  string
	CardID     string
}

// Environment owns one temporary database, a migration connection and its
// restricted login. Runtime callers receive only the restricted pool.
type Environment struct {
	DatabaseURL string
	admin       *pgxpool.Pool
	adminDB     *pgxpool.Pool
	runtime     *pgxpool.Pool
	name        string
	role        string
	owner       string
	marker      string
	password    string
}

// Open creates a unique marked database, installs the synthetic external
// mapping tables, runs the service migrator, then grants its runtime role.
// It does not run unless an owning test explicitly calls Open.
func Open(t testing.TB) (*Environment, error) {
	t.Helper()
	adminURL, err := adminURL()
	if err != nil {
		return nil, err
	}
	adminConfig, err := pgxpool.ParseConfig(adminURL)
	parsedAdminURL, parseURLErr := url.Parse(adminURL)
	if err != nil || parseURLErr != nil ||
		(parsedAdminURL.Scheme != "postgres" && parsedAdminURL.Scheme != "postgresql") ||
		parsedAdminURL.Path != "/postgres" || adminConfig.ConnConfig.Database != "postgres" ||
		!loopbackConfig(adminConfig.ConnConfig.Host, adminConfig.ConnConfig.Fallbacks) ||
		adminConfig.ConnConfig.User == "" {
		return nil, errors.New("funding_test_admin_target_invalid")
	}
	markerBytes := make([]byte, 16)
	if _, err := rand.Read(markerBytes); err != nil {
		return nil, errors.New("funding_test_owner_token_unavailable")
	}
	passwordBytes := make([]byte, 24)
	if _, err := rand.Read(passwordBytes); err != nil {
		return nil, errors.New("funding_test_runtime_password_unavailable")
	}
	marker := hex.EncodeToString(markerBytes)
	password := hex.EncodeToString(passwordBytes)
	env := &Environment{
		name: ownerPrefix + "_" + marker[:20],
		role: "funding_rt_" + marker[:20], marker: marker, password: password,
	}
	setupCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env.admin, err = newAdminPool(setupCtx, adminURL)
	if err != nil {
		return nil, errors.New("funding_test_admin_unavailable")
	}
	if err = env.admin.QueryRow(setupCtx, "SELECT current_user").Scan(&env.owner); err != nil || env.owner == "" {
		env.admin.Close()
		env.admin = nil
		return nil, errors.New("funding_test_admin_identity_unavailable")
	}
	if _, err = env.admin.Exec(setupCtx, "CREATE DATABASE "+pgx.Identifier{env.name}.Sanitize()); err != nil {
		env.admin.Close()
		env.admin = nil
		return nil, errors.New("funding_test_database_create_failed")
	}
	if _, err = env.admin.Exec(setupCtx, "COMMENT ON DATABASE "+pgx.Identifier{env.name}.Sanitize()+" IS '"+marker+"'"); err != nil {
		return nil, env.setupFailure("funding_test_database_mark_failed")
	}
	env.adminDB, err = newAdminPool(setupCtx, databaseURL(adminURL, env.name))
	if err != nil {
		return nil, env.setupFailure("funding_test_database_connection_failed")
	}
	if _, err = env.adminDB.Exec(setupCtx, customerRosterFixtureDDL); err != nil {
		return nil, env.setupFailure("funding_test_mapping_fixture_failed")
	}
	if _, err = env.adminDB.Exec(setupCtx, cardBindingFixtureDDL); err != nil {
		return nil, env.setupFailure("funding_test_mapping_fixture_failed")
	}
	if err = postgres.RunMigrations(setupCtx, env.adminDB); err != nil {
		return nil, env.setupFailure("funding_test_migration_failed: " + err.Error())
	}
	if _, err = env.admin.Exec(setupCtx, "CREATE ROLE "+pgx.Identifier{env.role}.Sanitize()+
		" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT PASSWORD '"+password+"'"); err != nil {
		return nil, env.setupFailure("funding_test_login_create_failed")
	}
	if _, err = env.admin.Exec(setupCtx, "COMMENT ON ROLE "+pgx.Identifier{env.role}.Sanitize()+" IS '"+marker+"'"); err != nil {
		return nil, env.setupFailure("funding_test_login_mark_failed")
	}
	if _, err = env.admin.Exec(setupCtx, "GRANT servicing_runtime TO "+pgx.Identifier{env.role}.Sanitize()); err != nil {
		return nil, env.setupFailure("funding_test_runtime_role_grant_failed")
	}
	env.DatabaseURL = runtimeURL(adminURL, env.name, env.role, password)
	runtimeConfig, err := pgxpool.ParseConfig(env.DatabaseURL)
	if err != nil || runtimeConfig.ConnConfig.Database != env.name || runtimeConfig.ConnConfig.User != env.role ||
		runtimeConfig.ConnConfig.Password != password ||
		!loopbackConfig(runtimeConfig.ConnConfig.Host, runtimeConfig.ConnConfig.Fallbacks) {
		return nil, env.setupFailure("funding_test_runtime_binding_invalid")
	}
	readyCtx, readyCancel := context.WithTimeout(setupCtx, 10*time.Second)
	defer readyCancel()
	env.runtime, err = postgres.NewRuntimePool(readyCtx, env.DatabaseURL)
	if err != nil || postgres.ReadyRuntime(readyCtx, env.runtime) != nil {
		return nil, env.setupFailure("funding_test_runtime_not_ready")
	}
	env.runtime.Close()
	env.runtime = nil
	t.Cleanup(func() {
		if err := env.Close(); err != nil {
			t.Errorf("funding test database cleanup failed: %v", err)
		}
	})
	return env, nil
}

// SeedMapping inserts only the two synthetic source mappings owned by this
// disposable database. The service runtime role cannot call this method.
func (e *Environment) SeedMapping(ctx context.Context, mapping Mapping) error {
	if e == nil || e.adminDB == nil || mapping.CustomerID == "" || mapping.AccountID == "" || mapping.CardID == "" {
		return errors.New("funding_test_mapping_invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := e.adminDB.Begin(ctx)
	if err != nil {
		return errors.New("funding_test_mapping_seed_failed")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO public.customer_roster
		(customer_id,airwallex_connected_account_id,is_active) VALUES($1,$2,true)`,
		mapping.CustomerID, mapping.AccountID); err != nil {
		return errors.New("funding_test_mapping_seed_failed")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.issuing_card_bindings
		(card_id,customer_id,airwallex_account_id,is_active) VALUES($1,$2,$3,true)`,
		mapping.CardID, mapping.CustomerID, mapping.AccountID); err != nil {
		return errors.New("funding_test_mapping_seed_failed")
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("funding_test_mapping_seed_failed")
	}
	return nil
}

// MaintenancePool exposes only this test-owned database's migration owner for
// explicit privileged-maintenance acceptance checks. Runtime URLs stay restricted.
func (e *Environment) MaintenancePool() *pgxpool.Pool { return e.adminDB }

// OpenRuntimePool returns the checked, least-privileged service pool and keeps
// ownership so Close shuts it before removing the owned database.
func (e *Environment) OpenRuntimePool(ctx context.Context) (*pgxpool.Pool, error) {
	if e == nil || e.DatabaseURL == "" || e.runtime != nil {
		return nil, errors.New("funding_test_runtime_pool_state_invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := postgres.NewRuntimePool(ctx, e.DatabaseURL)
	if err != nil {
		return nil, errors.New("funding_test_runtime_pool_unavailable")
	}
	if err := postgres.ReadyRuntime(ctx, pool); err != nil {
		pool.Close()
		return nil, errors.New("funding_test_runtime_not_ready")
	}
	e.runtime = pool
	return pool, nil
}

// Close closes owned connections and drops only the database and login whose
// owner/comments still match this helper's generated identity.
func (e *Environment) Close() error {
	if e == nil || e.admin == nil {
		return nil
	}
	if e.runtime != nil {
		e.runtime.Close()
		e.runtime = nil
	}
	if e.adminDB != nil {
		e.adminDB.Close()
		e.adminDB = nil
	}
	admin := e.admin
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var dbMarker, owner string
	err := admin.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database'),pg_get_userbyid(datdba)
		FROM pg_database WHERE datname=$1`, e.name).Scan(&dbMarker, &owner)
	if err != nil || dbMarker != e.marker || owner != e.owner {
		e.admin = nil
		return errors.New("funding_test_database_ownership_unverified")
	}
	var roleExists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, e.role).Scan(&roleExists); err != nil {
		e.admin = nil
		return errors.New("funding_test_login_ownership_unverified")
	}
	if roleExists {
		var roleMarker *string
		err = admin.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1`, e.role).Scan(&roleMarker)
		if err != nil || roleMarker == nil || *roleMarker != e.marker {
			e.admin = nil
			return errors.New("funding_test_login_ownership_unverified")
		}
		if _, err := admin.Exec(ctx, "REVOKE servicing_runtime FROM "+pgx.Identifier{e.role}.Sanitize()); err != nil {
			e.admin = nil
			return errors.New("funding_test_runtime_role_revoke_failed")
		}
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{e.name}.Sanitize()+" WITH (FORCE)"); err != nil {
		e.admin = nil
		return errors.New("funding_test_database_cleanup_failed")
	}
	if roleExists {
		if _, err := admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{e.role}.Sanitize()); err != nil {
			e.admin = nil
			return errors.New("funding_test_login_cleanup_failed")
		}
	}
	e.admin = nil
	return nil
}

func (e *Environment) setupFailure(code string) error {
	if err := e.Close(); err != nil {
		return errors.New(code + "; cleanup: " + err.Error())
	}
	return errors.New(code)
}

func adminURL() (string, error) {
	if value := strings.TrimSpace(os.Getenv("FUNDING_TEST_ADMIN_URL")); value != "" {
		return value, nil
	}
	path := strings.TrimSpace(os.Getenv("FUNDING_TEST_ADMIN_URL_FILE"))
	if path == "" {
		return "", errors.New("funding_test_admin_not_configured")
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(body)) == "" {
		return "", errors.New("funding_test_admin_file_unavailable")
	}
	return strings.TrimSpace(string(body)), nil
}

func newAdminPool(ctx context.Context, connectionString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, errors.New("funding_test_admin_configuration_invalid")
	}
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("funding_test_admin_connection_failed")
	}
	return pool, nil
}

func databaseURL(adminURL, name string) string {
	parsed, _ := url.Parse(adminURL)
	parsed.Path = "/" + name
	query := parsed.Query()
	query.Set("dbname", name)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func runtimeURL(adminURL, name, role, password string) string {
	parsed, _ := url.Parse(adminURL)
	parsed.Path = "/" + name
	parsed.User = url.UserPassword(role, password)
	query := parsed.Query()
	for _, key := range []string{"user", "username", "password", "passfile", "service", "servicefile", "dbname", "sslpassword", "options", "role", "session_authorization"} {
		query.Del(key)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func loopbackConfig(host string, fallbacks []*pgconn.FallbackConfig) bool {
	if !loopback(host) {
		return false
	}
	for _, fallback := range fallbacks {
		if fallback == nil || !loopback(fallback.Host) {
			return false
		}
	}
	return true
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() || strings.EqualFold(host, "localhost")
}
