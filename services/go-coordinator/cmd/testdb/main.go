// Command testdb provisions and removes one marker-owned coordinator G database.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/postgres"
)

const (
	adminURLFileEnv  = "COORDINATOR_TEST_ADMIN_URL_FILE"
	upstreamsFileEnv = "COORDINATOR_UPSTREAMS_FILE"
	databasePrefix   = "flowcard_coordinator_g_"
	runtimePrefix    = "flowcard_coord_runtime_"
	markerLabel      = "flowcard-coordinator-g:"
)

var markerPattern = regexp.MustCompile(`^r6g_[a-z0-9_]{8,32}$`)

type ownerRecord struct {
	Marker      string `json:"marker"`
	Database    string `json:"database"`
	RuntimeRole string `json:"runtime_role"`
	AdminRole   string `json:"admin_role"`
	SourceRoot  string `json:"source_root"`
	ProfileDir  string `json:"profile_dir"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: testdb setup|finalize|cleanup --marker MARKER --profile-dir PATH")
	}
	flags := flag.NewFlagSet("testdb", flag.ContinueOnError)
	marker := flags.String("marker", "", "unique task-owned G resource marker")
	profileDir := flags.String("profile-dir", "", "task-owned ignored profile directory")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !markerPattern.MatchString(*marker) || strings.TrimSpace(*profileDir) == "" {
		return errors.New("testdb marker or profile path invalid")
	}
	root, dir, err := validateProfilePath(*profileDir, *marker)
	if err != nil {
		return err
	}
	adminURL, err := readAdminURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch args[0] {
	case "setup":
		upstreams, err := readUpstreamsProfile()
		if err != nil {
			return err
		}
		return setup(ctx, root, dir, *marker, adminURL, upstreams)
	case "finalize":
		return finalize(ctx, dir, *marker, adminURL)
	case "cleanup":
		return cleanup(ctx, dir, *marker, adminURL)
	default:
		return errors.New("testdb command invalid")
	}
}

func setup(ctx context.Context, root, profileDir, marker string, adminURL *url.URL, upstreams map[string]string) error {
	databaseName, runtimeRole := resourceNames(marker)
	if !validIdentifier(databaseName) || !validIdentifier(runtimeRole) {
		return errors.New("coordinator test resource identity invalid")
	}
	adminPool, err := connectPool(ctx, adminURL.String())
	if err != nil {
		return errors.New("coordinator test administrator unavailable")
	}
	defer adminPool.Close()
	var adminRole string
	if err := adminPool.QueryRow(ctx, `SELECT current_user`).Scan(&adminRole); err != nil || !validIdentifier(adminRole) {
		return errors.New("coordinator test administrator unavailable")
	}
	var databaseExists, roleExists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, databaseName).Scan(&databaseExists); err != nil {
		return errors.New("coordinator test resource check failed")
	}
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, runtimeRole).Scan(&roleExists); err != nil || databaseExists || roleExists {
		return errors.New("coordinator test resource name is already in use")
	}
	if err := os.MkdirAll(filepath.Dir(profileDir), 0o700); err != nil {
		return errors.New("coordinator test profile parent unavailable")
	}
	if err := ensureNoSymlinkPath(root, filepath.Dir(profileDir)); err != nil {
		return errors.New("coordinator test profile path invalid")
	}
	if err := os.Mkdir(profileDir, 0o700); err != nil {
		return errors.New("coordinator test profile directory unavailable")
	}
	if err := os.Mkdir(filepath.Join(profileDir, "sources"), 0o700); err != nil {
		_ = os.Remove(profileDir)
		return errors.New("coordinator test source directory unavailable")
	}
	owner := ownerRecord{Marker: marker, Database: databaseName, RuntimeRole: runtimeRole, AdminRole: adminRole, SourceRoot: filepath.Join(profileDir, "sources"), ProfileDir: profileDir}
	ownerBytes, err := json.Marshal(owner)
	if err != nil {
		_ = os.Remove(profileDir)
		return errors.New("coordinator test owner record encode failed")
	}
	if err := writePrivate(filepath.Join(profileDir, "owner.json"), append(ownerBytes, '\n')); err != nil {
		_ = os.Remove(profileDir)
		return err
	}
	password, err := randomPassword()
	if err != nil {
		return errors.New("coordinator test password generation failed")
	}
	roleSQL := "CREATE ROLE " + quoteIdentifier(runtimeRole) + " LOGIN PASSWORD " + quoteLiteral(password) + " NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT"
	if _, err := adminPool.Exec(ctx, roleSQL); err != nil {
		return errors.New("coordinator test runtime role creation failed")
	}
	if _, err := adminPool.Exec(ctx, "COMMENT ON ROLE "+quoteIdentifier(runtimeRole)+" IS "+quoteLiteral(markerLabel+marker)); err != nil {
		return errors.New("coordinator test runtime role marker failed")
	}
	databaseSQL := "CREATE DATABASE " + quoteIdentifier(databaseName) + " OWNER " + quoteIdentifier(adminRole)
	if _, err := adminPool.Exec(ctx, databaseSQL); err != nil {
		return errors.New("coordinator test database creation failed")
	}
	if _, err := adminPool.Exec(ctx, "COMMENT ON DATABASE "+quoteIdentifier(databaseName)+" IS "+quoteLiteral(markerLabel+marker)); err != nil {
		return errors.New("coordinator test database marker failed")
	}
	migrationURL := databaseURL(adminURL, databaseName, adminURL.User)
	runtimeURL := databaseURL(adminURL, databaseName, url.UserPassword(runtimeRole, password))
	if err := writePrivate(filepath.Join(profileDir, "migration-url"), []byte(migrationURL+"\n")); err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(profileDir, "runtime-url"), []byte(runtimeURL+"\n")); err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(profileDir, "migration.env"), []byte("DATABASE_URL="+shellQuote(migrationURL)+"\n")); err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(profileDir, "runtime.env"), []byte("DATABASE_URL="+shellQuote(runtimeURL)+"\nCOORDINATOR_TEST_DATABASE_URL="+shellQuote(runtimeURL)+"\n")); err != nil {
		return err
	}
	apiProfile := strings.TrimSuffix(upstreams["api"], "\n")
	if !strings.HasSuffix(apiProfile, "COORDINATOR_SOURCE_ROOT=") {
		return errors.New("coordinator API profile source root field missing")
	}
	apiProfile = strings.TrimSuffix(apiProfile, "COORDINATOR_SOURCE_ROOT=") + "COORDINATOR_SOURCE_ROOT=" + shellQuote(owner.SourceRoot) + "\n"
	if err := writePrivate(filepath.Join(profileDir, "api.env"), []byte("DATABASE_URL="+shellQuote(runtimeURL)+"\n"+apiProfile)); err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(profileDir, "source.env"), []byte(upstreams["source"])); err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(profileDir, "source-prep.env"), []byte("COORDINATOR_MODE=DEMO\nCOORDINATOR_SOURCE_BINDING_ID="+shellQuote(upstreams["binding"])+"\nCOORDINATOR_SOURCE_ROOT="+shellQuote(owner.SourceRoot)+"\n")); err != nil {
		return err
	}
	watchProfile := "COORDINATOR_HTTP_ADDR=" + shellQuote(extractEnvValue(apiProfile, "COORDINATOR_HTTP_ADDR")) + "\nCOORDINATOR_READER_SECRET=" + shellQuote(extractEnvValue(apiProfile, "COORDINATOR_READER_SECRET")) + "\n"
	if err := writePrivate(filepath.Join(profileDir, "watch.env"), []byte(watchProfile)); err != nil {
		return err
	}
	fmt.Printf("created marker-owned coordinator test database %s and runtime role %s\n", databaseName, runtimeRole)
	return nil
}

func finalize(ctx context.Context, profileDir, marker string, adminURL *url.URL) error {
	owner, err := readOwner(profileDir, marker)
	if err != nil {
		return err
	}
	migrationURL, err := readPrivateURL(filepath.Join(profileDir, "migration-url"), owner.Database)
	if err != nil {
		return err
	}
	if migrationURL != databaseURL(adminURL, owner.Database, adminURL.User) {
		return errors.New("coordinator test migration profile mismatch")
	}
	migrationPool, err := connectPool(ctx, migrationURL)
	if err != nil {
		return errors.New("coordinator test migration connection unavailable")
	}
	grant := "GRANT coordinator_runtime TO " + quoteIdentifier(owner.RuntimeRole)
	if _, err := migrationPool.Exec(ctx, grant); err != nil {
		migrationPool.Close()
		return errors.New("coordinator test runtime membership grant failed")
	}
	if _, err := migrationPool.Exec(ctx, "GRANT CONNECT ON DATABASE "+quoteIdentifier(owner.Database)+" TO "+quoteIdentifier(owner.RuntimeRole)); err != nil {
		migrationPool.Close()
		return errors.New("coordinator test runtime connection grant failed")
	}
	migrationPool.Close()
	runtimeURL, err := readPrivateURL(filepath.Join(profileDir, "runtime-url"), owner.Database)
	if err != nil {
		return err
	}
	repo, err := postgres.Connect(ctx, runtimeURL)
	if err != nil {
		return errors.New("coordinator test runtime connection unavailable")
	}
	defer repo.Close()
	if err := repo.Ready(ctx); err != nil {
		return errors.New("coordinator test runtime grants invalid")
	}
	fmt.Printf("verified restricted coordinator runtime role %s\n", owner.RuntimeRole)
	return nil
}

func cleanup(ctx context.Context, profileDir, marker string, adminURL *url.URL) error {
	if _, err := os.Stat(profileDir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	owner, err := readOwner(profileDir, marker)
	if err != nil {
		return err
	}
	adminPool, err := connectPool(ctx, adminURL.String())
	if err != nil {
		return errors.New("coordinator test administrator unavailable")
	}
	defer adminPool.Close()
	var adminRole string
	if err := adminPool.QueryRow(ctx, `SELECT current_user`).Scan(&adminRole); err != nil || adminRole != owner.AdminRole {
		return errors.New("coordinator test administrator changed")
	}
	databaseExists, err := ownedDatabase(ctx, adminPool, owner)
	if err != nil {
		return err
	}
	if databaseExists {
		var connections int
		if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, owner.Database).Scan(&connections); err != nil || connections != 0 {
			return errors.New("coordinator test database still has connections")
		}
		if _, err := adminPool.Exec(ctx, "DROP DATABASE "+quoteIdentifier(owner.Database)); err != nil {
			return errors.New("coordinator test database cleanup failed")
		}
	}
	roleExists, err := ownedRuntimeRole(ctx, adminPool, owner)
	if err != nil {
		return err
	}
	if roleExists {
		if _, err := adminPool.Exec(ctx, "DROP ROLE "+quoteIdentifier(owner.RuntimeRole)); err != nil {
			return errors.New("coordinator test role cleanup failed")
		}
	}
	if err := removeOwnedProfile(profileDir); err != nil {
		return err
	}
	fmt.Printf("cleaned marker-owned coordinator test database %s and runtime role %s\n", owner.Database, owner.RuntimeRole)
	return nil
}

func readAdminURL() (*url.URL, error) {
	path := strings.TrimSpace(os.Getenv(adminURLFileEnv))
	if path == "" {
		return nil, errors.New("coordinator test admin URL file variable required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() > 8192 {
		return nil, errors.New("coordinator test admin URL file must be a regular 0600 file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("coordinator test admin URL unavailable")
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return nil, errors.New("coordinator test admin URL invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil || parsed.Hostname() == "" || parsed.Port() != "54329" ||
		parsed.Path != "/postgres" || parsed.RawQuery == "" && parsed.ForceQuery || parsed.Fragment != "" {
		return nil, errors.New("coordinator test admin URL must target local postgres on 54329")
	}
	host := parsed.Hostname()
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return nil, errors.New("coordinator test admin URL must be loopback")
	}
	password, ok := parsed.User.Password()
	if !ok || password == "" {
		return nil, errors.New("coordinator test admin URL credentials missing")
	}
	return parsed, nil
}

func readUpstreamsProfile() (map[string]string, error) {
	path := strings.TrimSpace(os.Getenv(upstreamsFileEnv))
	if path == "" {
		return syntheticUpstreamsProfile(), nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() > 32768 {
		return nil, errors.New("coordinator upstream profile must be a regular 0600 file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("coordinator upstream profile unavailable")
	}
	defer file.Close()
	values := make(map[string]string)
	allowed := map[string]bool{
		"COORDINATOR_AGENT_MODE": true, "COORDINATOR_HTTP_ADDR": true, "COORDINATOR_MODE": true,
		"COORDINATOR_READER_SECRET": true, "COORDINATOR_SOURCE_BINDING_ID": true, "COORDINATOR_SOURCE_ROOT": true,
		"DATABASE_URL": true, "SERVICING_BASE_URL": true, "SERVICING_SERVICE_SECRET": true,
		"SHORTFALL_BASE_URL": true, "SHORTFALL_SERVICE_SECRET": true, "UNDERWRITING_BASE_URL": true,
		"UNDERWRITING_SERVICE_SECRET": true,
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 8192)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !allowed[key] {
			return nil, errors.New("coordinator upstream profile key invalid")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("coordinator upstream profile contains duplicate key")
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("coordinator upstream profile value invalid")
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && len(value) >= 2 {
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("coordinator upstream profile read failed")
	}
	if strings.TrimSpace(values["DATABASE_URL"]) != "" || strings.TrimSpace(values["COORDINATOR_SOURCE_ROOT"]) != "" || values["COORDINATOR_MODE"] != "DEMO" || values["COORDINATOR_AGENT_MODE"] != "FIXTURE" || strings.TrimSpace(values["COORDINATOR_SOURCE_BINDING_ID"]) == "" {
		return nil, errors.New("coordinator upstream profile is not the approved partial fixture profile")
	}
	for _, key := range []string{"COORDINATOR_READER_SECRET", "SHORTFALL_SERVICE_SECRET", "UNDERWRITING_SERVICE_SECRET"} {
		if len(values[key]) < 32 {
			return nil, errors.New("coordinator upstream profile required credentials missing")
		}
	}
	if values["UNDERWRITING_BASE_URL"] != "http://127.0.0.1:8083" || values["SHORTFALL_BASE_URL"] != "http://127.0.0.1:8092" || values["COORDINATOR_HTTP_ADDR"] != "127.0.0.1:8090" {
		return nil, errors.New("coordinator upstream profile endpoints do not match approved local profile")
	}
	api := "COORDINATOR_MODE=DEMO\nCOORDINATOR_AGENT_MODE=FIXTURE\nCOORDINATOR_HTTP_ADDR=" + shellQuote(values["COORDINATOR_HTTP_ADDR"]) + "\nCOORDINATOR_SOURCE_BINDING_ID=" + shellQuote(values["COORDINATOR_SOURCE_BINDING_ID"]) + "\nCOORDINATOR_READER_SECRET=" + shellQuote(values["COORDINATOR_READER_SECRET"]) + "\nUNDERWRITING_BASE_URL=" + shellQuote(values["UNDERWRITING_BASE_URL"]) + "\nUNDERWRITING_SERVICE_SECRET=" + shellQuote(values["UNDERWRITING_SERVICE_SECRET"]) + "\nSHORTFALL_BASE_URL=" + shellQuote(values["SHORTFALL_BASE_URL"]) + "\nSHORTFALL_SERVICE_SECRET=" + shellQuote(values["SHORTFALL_SERVICE_SECRET"]) + "\nCOORDINATOR_SOURCE_ROOT="
	source := "COORDINATOR_MODE=DEMO\nCOORDINATOR_SOURCE_BINDING_ID=" + shellQuote(values["COORDINATOR_SOURCE_BINDING_ID"]) + "\nSHORTFALL_SERVICE_SECRET=" + shellQuote(values["SHORTFALL_SERVICE_SECRET"]) + "\n"
	return map[string]string{"binding": values["COORDINATOR_SOURCE_BINDING_ID"], "api": api, "source": source}, nil
}

func syntheticUpstreamsProfile() map[string]string {
	const binding = "flowcard-coordinator-g-fixture"
	api := "COORDINATOR_MODE=DEMO\nCOORDINATOR_AGENT_MODE=FIXTURE\nCOORDINATOR_HTTP_ADDR='127.0.0.1:18090'\nCOORDINATOR_SOURCE_BINDING_ID='" + binding + "'\nCOORDINATOR_READER_SECRET='synthetic-only-reader-secret-for-coordinator-g-tests'\nUNDERWRITING_BASE_URL=\nUNDERWRITING_SERVICE_SECRET='synthetic-only-underwriting-secret-for-coordinator-g-tests'\nSHORTFALL_BASE_URL=\nSHORTFALL_SERVICE_SECRET='synthetic-only-shortfall-secret-for-coordinator-g-tests'\nCOORDINATOR_SOURCE_ROOT=\n"
	source := "COORDINATOR_MODE=DEMO\nCOORDINATOR_SOURCE_BINDING_ID='" + binding + "'\nSHORTFALL_SERVICE_SECRET='synthetic-only-shortfall-secret-for-coordinator-g-tests'\n"
	return map[string]string{"binding": binding, "api": api, "source": source}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func extractEnvValue(profile, key string) string {
	for _, line := range strings.Split(profile, "\n") {
		name, value, ok := strings.Cut(line, "=")
		if ok && name == key {
			value = strings.TrimSpace(value)
			if len(value) >= 2 && (value[0] == '\'' && value[len(value)-1] == '\'' || value[0] == '"' && value[len(value)-1] == '"') {
				return value[1 : len(value)-1]
			}
			return value
		}
	}
	return ""
}

func resourceNames(marker string) (string, string) {
	return databasePrefix + marker, runtimePrefix + marker
}

func databaseURL(base *url.URL, database string, user *url.Userinfo) string {
	copy := *base
	copy.Path = "/" + database
	copy.RawPath = ""
	copy.User = user
	return copy.String()
}

func connectPool(ctx context.Context, connectionURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, connectionURL)
	if err != nil {
		return nil, errors.New("coordinator test database configuration invalid")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("coordinator test database unavailable")
	}
	return pool, nil
}

func randomPassword() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func quoteIdentifier(value string) string {
	return pgx.Identifier{value}.Sanitize()
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validateProfilePath(path, marker string) (string, string, error) {
	if !markerPattern.MatchString(marker) {
		return "", "", errors.New("coordinator test marker invalid")
	}
	root, err := findRepositoryRoot()
	if err != nil {
		return "", "", err
	}
	profileDir, err := filepath.Abs(path)
	if err != nil || filepath.Clean(profileDir) != filepath.Join(root, ".dev", "integration", "20261114", "runtime", "coordinator-task", "g", marker) {
		return "", "", errors.New("coordinator test profile path must be task-owned under worktree .dev")
	}
	return root, profileDir, nil
}

func findRepositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", errors.New("coordinator repository root unavailable")
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "docs", "designs", "go-coordinator.md")); err == nil {
			root, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", errors.New("coordinator repository root unavailable")
			}
			return root, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("coordinator repository root unavailable")
		}
		current = parent
	}
}

func ensureNoSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("coordinator test profile path outside repository")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("coordinator test profile path invalid")
		}
	}
	return nil
}

func writePrivate(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("coordinator test profile file create failed")
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return errors.New("coordinator test profile file write failed")
	}
	if err := file.Close(); err != nil {
		return errors.New("coordinator test profile file close failed")
	}
	return nil
}

func readOwner(profileDir, marker string) (ownerRecord, error) {
	root, expectedDir, err := validateProfilePath(profileDir, marker)
	profileAbs, absErr := filepath.Abs(profileDir)
	if err != nil || absErr != nil || expectedDir != profileAbs {
		return ownerRecord{}, errors.New("coordinator test owner profile path invalid")
	}
	if err := ensureNoSymlinkPath(root, expectedDir); err != nil {
		return ownerRecord{}, errors.New("coordinator test owner profile path invalid")
	}
	path := filepath.Join(expectedDir, "owner.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() > 4096 {
		return ownerRecord{}, errors.New("coordinator test owner record unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ownerRecord{}, errors.New("coordinator test owner record unavailable")
	}
	var owner ownerRecord
	if json.Unmarshal(data, &owner) != nil || owner.Marker != marker || owner.AdminRole == "" {
		return ownerRecord{}, errors.New("coordinator test owner record invalid")
	}
	expectedDatabase, expectedRole := resourceNames(marker)
	expectedProfile := filepath.Join(root, ".dev", "integration", "20261114", "runtime", "coordinator-task", "g", marker)
	if owner.Database != expectedDatabase || owner.RuntimeRole != expectedRole || owner.ProfileDir != expectedProfile || owner.SourceRoot != filepath.Join(expectedProfile, "sources") {
		return ownerRecord{}, errors.New("coordinator test owner identity invalid")
	}
	return owner, nil
}

func readPrivateURL(path, database string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() > 8192 {
		return "", errors.New("coordinator test URL file unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("coordinator test URL file unavailable")
	}
	value := strings.TrimSpace(string(data))
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || parsed.Path != "/"+database || parsed.Fragment != "" || parsed.User == nil {
		return "", errors.New("coordinator test URL file invalid")
	}
	return value, nil
}

func ownedDatabase(ctx context.Context, pool *pgxpool.Pool, owner ownerRecord) (bool, error) {
	var databaseOwner, description string
	err := pool.QueryRow(ctx, `SELECT pg_get_userbyid(datdba), COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, owner.Database).Scan(&databaseOwner, &description)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || databaseOwner != owner.AdminRole || description != markerLabel+owner.Marker {
		return false, errors.New("coordinator test database ownership marker mismatch")
	}
	return true, nil
}

func ownedRuntimeRole(ctx context.Context, pool *pgxpool.Pool, owner ownerRecord) (bool, error) {
	var canLogin, superuser, createDB, createRole, replication, bypassRLS bool
	var description string
	err := pool.QueryRow(ctx, `SELECT rolcanlogin,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls,COALESCE(d.description,'') FROM pg_roles r LEFT JOIN pg_shdescription d ON d.objoid=r.oid AND d.classoid='pg_authid'::regclass WHERE r.rolname=$1`, owner.RuntimeRole).Scan(&canLogin, &superuser, &createDB, &createRole, &replication, &bypassRLS, &description)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || !canLogin || superuser || createDB || createRole || replication || bypassRLS || description != markerLabel+owner.Marker {
		return false, errors.New("coordinator test runtime role ownership marker mismatch")
	}
	return true, nil
}

func removeOwnedProfile(profileDir string) error {
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return errors.New("coordinator test profile cleanup unavailable")
	}
	allowed := map[string]bool{"owner.json": true, "migration-url": true, "runtime-url": true, "migration.env": true, "runtime.env": true, "api.env": true, "source.env": true, "source-prep.env": true, "watch.env": true, "sources": true}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !allowed[entry.Name()] || entry.Name() == "sources" && !entry.IsDir() || entry.Name() != "sources" && entry.IsDir() {
			return errors.New("coordinator test profile contains unowned files")
		}
		path := filepath.Join(profileDir, entry.Name())
		if entry.Name() == "sources" {
			if err := validateOwnedSourceTree(path); err != nil {
				return errors.New("coordinator test source directory contains unowned files")
			}
			paths = append(paths, path)
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
			return errors.New("coordinator test profile file ownership invalid")
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		if filepath.Base(path) == "sources" {
			if err := removeOwnedSourceTree(path); err != nil {
				return errors.New("coordinator test source directory cleanup failed")
			}
		} else if err := os.Remove(path); err != nil {
			return errors.New("coordinator test profile file cleanup failed")
		}
	}
	if err := os.Remove(profileDir); err != nil {
		return errors.New("coordinator test profile directory cleanup failed")
	}
	return nil
}

func validateOwnedSourceTree(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("coordinator test source root ownership invalid")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return errors.New("coordinator test source root unavailable")
	}
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		childInfo, err := os.Lstat(child)
		if err != nil || childInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("coordinator test source path invalid")
		}
		if childInfo.IsDir() {
			if err := validateOwnedSourceTree(child); err != nil {
				return err
			}
			continue
		}
		if !childInfo.Mode().IsRegular() || childInfo.Mode().Perm() != 0o600 {
			return errors.New("coordinator test source file ownership invalid")
		}
	}
	return nil
}

func removeOwnedSourceTree(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		info, err := os.Lstat(child)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("coordinator test source path changed")
		}
		if info.IsDir() {
			if err := removeOwnedSourceTree(child); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return errors.New("coordinator test source file changed")
		}
		if err := os.Remove(child); err != nil {
			return err
		}
	}
	return os.Remove(path)
}
