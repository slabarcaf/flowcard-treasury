package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	fundingconfig "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	fundingpg "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

type localLogin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	ID       string `json:"id,omitempty"`
}
type localRuntime struct {
	Owner           string                `json:"owner"`
	Environment     string                `json:"environment"`
	RunID           string                `json:"run_id"`
	DatabaseName    string                `json:"database_name"`
	Marker          string                `json:"marker"`
	AdminOwner      string                `json:"admin_owner"`
	Roles           map[string]string     `json:"roles"`
	URLs            map[string]string     `json:"database_urls"`
	Secrets         map[string]string     `json:"service_secrets"`
	Users           map[string]localLogin `json:"users"`
	CustomerID      string                `json:"customer_id"`
	FacilityID      string                `json:"facility_id"`
	SourceBindingID string                `json:"source_binding_id"`
	CapturedAt      time.Time             `json:"captured_at"`
}

func randomSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("sandbox_entropy_unavailable")
	}
	return hex.EncodeToString(raw), nil
}
func localRuntimePath(manifest string) string {
	return filepath.Join(filepath.Dir(manifest), "local-runtime.json")
}

func localAdminURL(path string) (*url.URL, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("sandbox_test_admin_file_unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(raw) > 16384 {
		return nil, errors.New("sandbox_test_admin_file_invalid")
	}
	value, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil || value.User == nil || (value.Scheme != "postgres" && value.Scheme != "postgresql") || value.Path != "/postgres" || value.Port() != "54329" {
		return nil, errors.New("sandbox_test_admin_target_invalid")
	}
	ip := net.ParseIP(value.Hostname())
	if value.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("sandbox_test_admin_target_invalid")
	}
	parsed, err := pgxpool.ParseConfig(value.String())
	if err != nil || parsed.ConnConfig.Database != "postgres" || parsed.ConnConfig.Port != 54329 {
		return nil, errors.New("sandbox_test_admin_target_invalid")
	}
	for _, fallback := range parsed.ConnConfig.Fallbacks {
		ip := net.ParseIP(fallback.Host)
		if fallback.Port != 54329 || (fallback.Host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("sandbox_test_admin_target_invalid")
		}
	}
	return value, nil
}

func findRepositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", errors.New("sandbox_source_root_unavailable")
	}
	for depth := 0; depth < 8; depth++ {
		if _, err := os.Stat(filepath.Join(dir, "services", "funding-collection", "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("sandbox_source_root_unavailable")
}

// runLocalCommand carries credentials only in the child environment/stdin.
// Child diagnostics are suppressed because older service tools may emit DSNs.
func runLocalCommand(ctx context.Context, root, service string, args []string, environment map[string]string, stdin string) error {
	command := exec.CommandContext(ctx, "go", append([]string{"-C", filepath.Join(root, "services", service), "run"}, args...)...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR"), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off"}
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Stdin = strings.NewReader(stdin)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if command.Run() != nil {
		return errors.New("sandbox_service_command_failed_" + service)
	}
	return nil
}

func prepareLocalDatabase(ctx context.Context, api *sandboxAPI, cfg credentials, manifest, runID, adminFile, databaseName string) error {
	if !fundingconfig.ValidOwnedDatabaseName(databaseName, "flowcard_funding_demo_sandbox") {
		return errors.New("sandbox_expected_database_required")
	}
	adminURL, err := localAdminURL(adminFile)
	if err != nil {
		return err
	}
	root, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	var profile localRuntime
	path := localRuntimePath(manifest)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		nonce, err := randomSecret()
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(runID + nonce))
		suffix := hex.EncodeToString(digest[:8])
		profile = localRuntime{Owner: manifestOwner, Environment: "SANDBOX", RunID: runID, DatabaseName: databaseName, Marker: "flowcard-sandbox:" + runID + ":" + nonce, Roles: map[string]string{}, URLs: map[string]string{}, Secrets: map[string]string{}, Users: map[string]localLogin{}, CustomerID: "sandbox-customer-" + runID, FacilityID: "sandbox-facility-" + runID, SourceBindingID: "sandbox-binding-" + runID, CapturedAt: time.Now().UTC()}
		for _, service := range []string{"funding", "shortfall", "underwriting", "coordinator", "gateway"} {
			password, err := randomSecret()
			if err != nil {
				return err
			}
			role := "fc_sbx_" + suffix + "_" + service
			profile.Roles[service] = role
			target := *adminURL
			target.Path = "/" + databaseName
			target.User = url.UserPassword(role, password)
			profile.URLs[service] = target.String()
		}
		for _, key := range []string{"operator", "customer", "servicing-reader", "coordinator-reader", "underwriting", "underwriting-reader", "shortfall"} {
			secret, err := randomSecret()
			if err != nil {
				return err
			}
			profile.Secrets[key] = secret
		}
		for _, role := range []string{"operator", "customer"} {
			password, err := randomSecret()
			if err != nil {
				return err
			}
			profile.Users[role] = localLogin{Username: "sbx." + role + "." + suffix, Password: password}
		}
		encoded, err := json.MarshalIndent(profile, "", "  ")
		if err != nil {
			return errors.New("sandbox_local_profile_invalid")
		}
		if err := writePrivateFile(path, encoded); err != nil {
			return err
		}
	} else {
		if err := readPrivateJSON(path, &profile, 65536); err != nil {
			return err
		}
	}
	if profile.Owner != manifestOwner || profile.RunID != runID || profile.Environment != "SANDBOX" || profile.DatabaseName != databaseName || profile.Marker == "" || len(profile.Roles) != 5 || len(profile.URLs) != 5 {
		return errors.New("sandbox_local_profile_ownership_mismatch")
	}
	if err := validateLocalProfile(profile); err != nil {
		return err
	}
	maintenance, err := pgxpool.New(ctx, adminURL.String())
	if err != nil {
		return errors.New("sandbox_test_admin_unavailable")
	}
	defer maintenance.Close()
	var adminOwner string
	if maintenance.QueryRow(ctx, `SELECT current_user`).Scan(&adminOwner) != nil {
		return errors.New("sandbox_test_admin_unavailable")
	}
	if profile.AdminOwner != "" && profile.AdminOwner != adminOwner {
		return errors.New("sandbox_test_admin_owner_changed")
	}
	profile.AdminOwner = adminOwner
	ownedProfile, encodeErr := json.MarshalIndent(profile, "", "  ")
	if encodeErr != nil {
		return errors.New("sandbox_local_profile_invalid")
	}
	if err := writePrivateFile(path, ownedProfile); err != nil {
		return err
	}
	var marker string
	err = maintenance.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, databaseName).Scan(&marker)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := maintenance.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
			return errors.New("sandbox_database_create_failed")
		}
		if _, err := maintenance.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" IS '"+profile.Marker+"'"); err != nil {
			return errors.New("sandbox_database_marker_failed")
		}
	} else if err != nil || marker != profile.Marker {
		return errors.New("sandbox_database_not_owned")
	}
	target := *adminURL
	target.Path = "/" + databaseName
	admin, err := pgxpool.New(ctx, target.String())
	if err != nil {
		return errors.New("sandbox_database_connection_failed")
	}
	defer admin.Close()
	var actual string
	if admin.QueryRow(ctx, `SELECT current_database()`).Scan(&actual) != nil || actual != databaseName {
		return errors.New("sandbox_database_target_mismatch")
	}
	if _, err := admin.Exec(ctx, "REVOKE ALL ON DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" FROM PUBLIC; REVOKE CREATE ON SCHEMA public FROM PUBLIC"); err != nil {
		return errors.New("sandbox_database_restriction_failed")
	}
	if _, err := admin.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.customer_roster(customer_id text PRIMARY KEY,airwallex_connected_account_id text NOT NULL,is_active boolean NOT NULL);CREATE TABLE IF NOT EXISTS public.issuing_card_bindings(card_id text PRIMARY KEY,customer_id text NOT NULL,airwallex_account_id text NOT NULL,is_active boolean NOT NULL)`); err != nil {
		return errors.New("sandbox_mapping_schema_failed")
	}
	if err := fundingpg.VerifyDatabaseName(ctx, admin, databaseName); err != nil {
		return errors.New("sandbox_database_target_mismatch")
	}
	if err := fundingpg.RunMigrations(ctx, admin); err != nil {
		return errors.New("sandbox_funding_migration_failed")
	}
	for _, service := range []string{"shortfall-detection", "underwriting-engine", "go-coordinator", "browser-gateway"} {
		environment := map[string]string{"DATABASE_URL": target.String(), "UNDERWRITING_MIGRATION_DATABASE_URL": target.String(), "BROWSER_GATEWAY_ADMIN_DATABASE_URL": target.String()}
		args := []string{"./cmd/migrate"}
		if service == "browser-gateway" {
			args = append(args, "--database-name", databaseName)
		}
		if err := runLocalCommand(ctx, root, service, args, environment, ""); err != nil {
			return err
		}
	}
	for service, role := range profile.Roles {
		roleMarker := ""
		err := maintenance.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_authid'),'') FROM pg_roles WHERE rolname=$1`, role).Scan(&roleMarker)
		if errors.Is(err, pgx.ErrNoRows) {
			runtimeURL, err := url.Parse(profile.URLs[service])
			if err != nil {
				return errors.New("sandbox_runtime_role_invalid")
			}
			password, _ := runtimeURL.User.Password()
			decoded, decodeErr := hex.DecodeString(password)
			if decodeErr != nil || len(decoded) != 32 {
				return errors.New("sandbox_runtime_role_invalid")
			}
			if _, err := maintenance.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT PASSWORD '"+password+"'"); err != nil {
				return errors.New("sandbox_runtime_role_create_failed")
			}
			if _, err := maintenance.Exec(ctx, "COMMENT ON ROLE "+pgx.Identifier{role}.Sanitize()+" IS '"+profile.Marker+"'"); err != nil {
				return errors.New("sandbox_runtime_role_marker_failed")
			}
		} else if err != nil || roleMarker != profile.Marker {
			return errors.New("sandbox_runtime_role_not_owned")
		}
		if _, err := maintenance.Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
			return errors.New("sandbox_runtime_connect_grant_failed")
		}
		if service != "gateway" {
			base := map[string]string{"funding": "servicing_runtime", "shortfall": "shortfall_runtime", "underwriting": "underwriting_runtime", "coordinator": "coordinator_runtime"}[service]
			if _, err := maintenance.Exec(ctx, "GRANT "+pgx.Identifier{base}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
				return errors.New("sandbox_runtime_role_grant_failed")
			}
		} else {
			quoted := pgx.Identifier{role}.Sanitize()
			if _, err := admin.Exec(ctx, "REVOKE ALL ON browser_gateway.schema_migrations FROM "+quoted); err != nil {
				return errors.New("sandbox_gateway_migration_grant_revoke_failed")
			}
			if _, err := admin.Exec(ctx, "GRANT USAGE ON SCHEMA browser_gateway TO "+quoted+"; GRANT SELECT ON browser_gateway.users,browser_gateway.servicing_bindings TO "+quoted+"; GRANT SELECT,INSERT,DELETE ON browser_gateway.sessions TO "+quoted+"; GRANT SELECT,INSERT ON browser_gateway.financial_commands,browser_gateway.financial_receipts,browser_gateway.customer_offers TO "+quoted); err != nil {
				return errors.New("sandbox_gateway_grant_failed")
			}
		}
	}
	borrower, err := ownedScopedAPI(ctx, api, cfg, manifest, runID, "borrower")
	if err != nil {
		return err
	}
	card, err := savedIntent(manifest, runID, cfg.platform, "card-borrower")
	if err != nil || card.State != "VERIFIED" {
		return errors.New("sandbox_card_mapping_unverified")
	}
	if _, err := admin.Exec(ctx, `INSERT INTO public.customer_roster VALUES($1,$2,true) ON CONFLICT DO NOTHING`, profile.CustomerID, borrower.scope); err != nil {
		return errors.New("sandbox_roster_seed_failed")
	}
	if _, err := admin.Exec(ctx, `INSERT INTO public.issuing_card_bindings VALUES($1,$2,$3,true) ON CONFLICT DO NOTHING`, card.ResourceID, profile.CustomerID, borrower.scope); err != nil {
		return errors.New("sandbox_card_binding_seed_failed")
	}
	var exactMapping bool
	if admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.customer_roster WHERE customer_id=$1 AND airwallex_connected_account_id=$2 AND is_active) AND EXISTS(SELECT 1 FROM public.issuing_card_bindings WHERE card_id=$3 AND customer_id=$1 AND airwallex_account_id=$2 AND is_active)`, profile.CustomerID, borrower.scope, card.ResourceID).Scan(&exactMapping) != nil || !exactMapping {
		return errors.New("sandbox_mapping_changed")
	}
	for _, role := range []string{"operator", "customer"} {
		login := profile.Users[role]
		var id, actualRole string
		err := admin.QueryRow(ctx, `SELECT id::text,role FROM browser_gateway.users WHERE username=$1 AND active`, login.Username).Scan(&id, &actualRole)
		if errors.Is(err, pgx.ErrNoRows) {
			args := []string{"./cmd/users", "create", "--username", login.Username, "--role", strings.ToUpper(role), "--password-stdin", "--database-name", databaseName}
			if err := runLocalCommand(ctx, root, "browser-gateway", args, map[string]string{"BROWSER_GATEWAY_ADMIN_DATABASE_URL": target.String()}, login.Password+"\n"); err != nil {
				return err
			}
			if admin.QueryRow(ctx, `SELECT id::text,role FROM browser_gateway.users WHERE username=$1 AND active`, login.Username).Scan(&id, &actualRole) != nil {
				return errors.New("sandbox_user_provisioning_unverified")
			}
		} else if err != nil {
			return errors.New("sandbox_user_lookup_failed")
		}
		if actualRole != strings.ToUpper(role) {
			return errors.New("sandbox_user_role_mismatch")
		}
		login.ID = id
		profile.Users[role] = login
	}
	encoded, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return errors.New("sandbox_local_profile_invalid")
	}
	return writePrivateFile(path, encoded)
}

func validateLocalProfile(profile localRuntime) error {
	if profile.Owner != manifestOwner || profile.Environment != "SANDBOX" || !runPattern.MatchString(profile.RunID) || !fundingconfig.ValidOwnedDatabaseName(profile.DatabaseName, "flowcard_funding_demo_sandbox") {
		return errors.New("sandbox_local_profile_invalid")
	}
	prefix := "flowcard-sandbox:" + profile.RunID + ":"
	if !strings.HasPrefix(profile.Marker, prefix) {
		return errors.New("sandbox_local_marker_invalid")
	}
	nonce, err := hex.DecodeString(strings.TrimPrefix(profile.Marker, prefix))
	if err != nil || len(nonce) != 32 {
		return errors.New("sandbox_local_marker_invalid")
	}
	if len(profile.Roles) != 5 || len(profile.URLs) != 5 {
		return errors.New("sandbox_local_roles_invalid")
	}
	for _, service := range []string{"funding", "shortfall", "underwriting", "coordinator", "gateway"} {
		role := profile.Roles[service]
		if !strings.HasPrefix(role, "fc_sbx_") || !strings.HasSuffix(role, "_"+service) || len(role) > 63 {
			return errors.New("sandbox_local_role_invalid")
		}
		target, err := url.Parse(profile.URLs[service])
		if err != nil || target.User == nil || target.User.Username() != role || target.Path != "/"+profile.DatabaseName || target.Port() != "54329" {
			return errors.New("sandbox_local_role_url_invalid")
		}
		ip := net.ParseIP(target.Hostname())
		if target.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return errors.New("sandbox_local_role_url_invalid")
		}
		password, _ := target.User.Password()
		decoded, err := hex.DecodeString(password)
		if err != nil || len(decoded) != 32 {
			return errors.New("sandbox_local_role_password_invalid")
		}
	}
	return nil
}

func cleanupLocalDatabase(ctx context.Context, profile localRuntime, adminFile string) error {
	if err := validateLocalProfile(profile); err != nil {
		return err
	}
	target, err := localAdminURL(adminFile)
	if err != nil {
		return err
	}
	admin, err := pgxpool.New(ctx, target.String())
	if err != nil {
		return errors.New("sandbox_cleanup_admin_unavailable")
	}
	defer admin.Close()
	var current string
	if admin.QueryRow(ctx, `SELECT current_user`).Scan(&current) != nil || current != profile.AdminOwner {
		return errors.New("sandbox_cleanup_admin_owner_mismatch")
	}
	var marker, owner string
	err = admin.QueryRow(ctx, `SELECT COALESCE(shobj_description(d.oid,'pg_database'),''),r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=$1`, profile.DatabaseName).Scan(&marker, &owner)
	databaseExists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) || databaseExists && (marker != profile.Marker || owner != profile.AdminOwner) {
		return errors.New("sandbox_cleanup_database_not_owned")
	}
	roles := []string{}
	for _, role := range profile.Roles {
		var marker string
		var login bool
		err := admin.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_authid'),''),rolcanlogin FROM pg_roles WHERE rolname=$1`, role).Scan(&marker, &login)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil || marker != profile.Marker || !login {
			return errors.New("sandbox_cleanup_role_not_owned")
		}
		roles = append(roles, role)
	}
	if databaseExists {
		if _, err := admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, profile.DatabaseName); err != nil {
			return errors.New("sandbox_cleanup_connections_failed")
		}
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{profile.DatabaseName}.Sanitize()); err != nil {
			return errors.New("sandbox_cleanup_database_failed")
		}
	}
	for _, role := range roles {
		if _, err := admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			return errors.New("sandbox_cleanup_role_failed")
		}
	}
	return nil
}
