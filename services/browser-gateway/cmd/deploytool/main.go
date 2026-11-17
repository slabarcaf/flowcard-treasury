// Command deploytool performs bounded, quiet deployment checks and provisioning.
// It is never invoked by an application API or a background financial worker.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/auth"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/postgres"
)

type binding struct {
	Name string `json:"name"`
	Env  string `json:"env"`
	Role string `json:"role"`
}

type contract struct {
	Bindings []binding `json:"database_bindings"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{2,62}$`)
var runtimeUser = regexp.MustCompile(`^flowcard_go_[a-z0-9_]{1,45}$`)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "deployment command required")
		os.Exit(2)
	}
	var err error
	if os.Args[1] == "health" && len(os.Args) == 3 {
		err = health(ctx, os.Args[2])
	} else if os.Args[1] == "probe-runtime" && len(os.Args) == 2 {
		err = probeRuntime(ctx)
	} else if len(os.Args) == 2 {
		err = run(ctx, os.Args[1])
	} else {
		err = errors.New("invalid command")
	}
	if err != nil {
		// Never log a URL, password, raw driver error or private bootstrap body.
		fmt.Fprintln(os.Stderr, "Go deployment check failed")
		os.Exit(1)
	}
	fmt.Println("Go deployment check passed")
}

// probeRuntime is restricted to disposable loopback smoke installations.
// It verifies the real production-cookie/API boundary without an external call.
func probeRuntime(ctx context.Context) error {
	if os.Getenv("EXPECTED_DATABASE_HOST") != "127.0.0.1" ||
		!strings.HasPrefix(os.Getenv("EXPECTED_DATABASE_NAME"), "flowcard_deploy_test_") {
		return errors.New("smoke probe requires disposable loopback installation")
	}
	var user struct {
		Username string `json:"operator_username"`
		Password string `json:"operator_password"`
	}
	if json.Unmarshal([]byte(os.Getenv("GO_BOOTSTRAP_JSON")), &user) != nil {
		return errors.New("smoke user missing")
	}
	body, _ := json.Marshal(map[string]string{"username": user.Username, "password": user.Password})
	request, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:3000/api/session/login", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Origin", "https://demo.example.test")
	request.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("smoke login unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("smoke login failed")
	}
	var cookie *http.Cookie
	for _, c := range response.Cookies() {
		if c.Name == "flowcard_session" && c.HttpOnly && c.Secure && c.Path == "/api" && c.SameSite == http.SameSiteLaxMode {
			cookie = c
		}
	}
	if cookie == nil {
		return errors.New("production cookie contract missing")
	}
	for _, route := range []string{"/api/session/me", "/api/companies"} {
		get, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:3000"+route, nil)
		get.Header.Set("Origin", "https://demo.example.test")
		get.AddCookie(cookie)
		read, err := client.Do(get)
		if err != nil {
			return errors.New("smoke authenticated read failed")
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(read.Body, 65536))
		read.Body.Close()
		if read.StatusCode != 200 {
			return errors.New("smoke authenticated read denied")
		}
	}
	return nil
}

func health(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid health URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("health must be local")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("health unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("health unavailable")
	}
	return nil
}

func parseDatabase(raw, expected, host string) (*pgxpool.Config, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil ||
		u.Path != "/"+expected || !identifier.MatchString(expected) || u.Fragment != "" {
		return nil, errors.New("database target invalid")
	}
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	password, ok := u.User.Password()
	if !ok || password == "" || (len(password) < 24 && !(local && strings.HasPrefix(expected, "flowcard_deploy_test_"))) {
		return nil, errors.New("database password required")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Database != expected || cfg.ConnConfig.Host != host ||
		cfg.ConnConfig.User != u.User.Username() || cfg.ConnConfig.Password != password || len(cfg.ConnConfig.Fallbacks) != 0 {
		return nil, errors.New("database connection target invalid")
	}
	if !local && (cfg.ConnConfig.Port != 5432 || u.Query().Get("sslmode") != "verify-full" || cfg.ConnConfig.TLSConfig == nil || cfg.ConnConfig.TLSConfig.InsecureSkipVerify) {
		return nil, errors.New("verified database TLS required")
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConns = 2
	return cfg, nil
}

func loadContract() (contract, error) {
	file := os.Getenv("GO_STACK_CONTRACT_FILE")
	if file == "" {
		file = "/etc/flowcard/go-stack.json"
	}
	raw, err := os.ReadFile(file)
	var c contract
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &c) != nil || len(c.Bindings) != 7 {
		return c, errors.New("deployment contract invalid")
	}
	for _, b := range c.Bindings {
		if !identifier.MatchString(b.Name) || !regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`).MatchString(b.Env) ||
			(b.Role != "" && !identifier.MatchString(b.Role)) {
			return c, errors.New("deployment binding invalid")
		}
	}
	return c, nil
}

func run(ctx context.Context, operation string) error {
	if operation != "preflight" && operation != "check-admin" && operation != "bootstrap" {
		return errors.New("unknown deployment command")
	}
	c, err := loadContract()
	if err != nil {
		return err
	}
	expected, host := os.Getenv("EXPECTED_DATABASE_NAME"), os.Getenv("EXPECTED_DATABASE_HOST")
	seen := map[string]bool{}
	for _, b := range c.Bindings {
		cfg, err := parseDatabase(os.Getenv(b.Env), expected, host)
		if err != nil || !runtimeUser.MatchString(cfg.ConnConfig.User) || seen[cfg.ConnConfig.User] {
			return errors.New("distinct restricted logins required")
		}
		seen[cfg.ConnConfig.User] = true
	}
	if operation == "preflight" {
		keys := []string{"GO_SHORTFALL_SECRET", "GO_UNDERWRITING_SECRET", "GO_COORDINATOR_READER_SECRET",
			"GO_SERVICING_COORDINATOR_SECRET", "GO_SERVICING_READER_SECRET", "GO_OPERATOR_SECRET", "GO_CUSTOMER_SECRET", "ONBOARDING_SERVICE_SECRET"}
		values := map[string]bool{}
		for _, key := range keys {
			value := os.Getenv(key)
			if len(value) < 32 || strings.TrimSpace(value) != value || values[value] {
				return errors.New("distinct service secrets required")
			}
			values[value] = true
		}
		for _, key := range []string{"AIRWALLEX_CLIENT_ID", "AIRWALLEX_API_KEY", "AIRWALLEX_ACCOUNT_ID", "SHORTFALL_WEBHOOK_SECRET", "SERVICING_WEBHOOK_SECRET", "ONBOARDING_WEBHOOK_SECRET"} {
			if strings.TrimSpace(os.Getenv(key)) == "" {
				return errors.New("provider configuration missing")
			}
		}
		return nil
	}
	admin, err := parseDatabase(os.Getenv("MIGRATION_DATABASE_URL"), expected, host)
	if err != nil || seen[admin.ConnConfig.User] {
		return errors.New("separate migration login required")
	}
	pool, err := pgxpool.NewWithConfig(ctx, admin)
	if err != nil {
		return errors.New("migration connection unavailable")
	}
	defer pool.Close()
	var actual string
	var mappings bool
	if pool.QueryRow(ctx, `SELECT current_database(),to_regclass('public.customer_roster') IS NOT NULL
		AND to_regclass('public.issuing_card_bindings') IS NOT NULL`).Scan(&actual, &mappings) != nil || actual != expected || !mappings {
		return errors.New("foundation database unavailable")
	}
	var canMigrate bool
	if pool.QueryRow(ctx, `SELECT r.rolcreaterole
		AND has_database_privilege(current_user,current_database(),'CREATE')
		AND has_table_privilege(current_user,'public.customer_roster','SELECT WITH GRANT OPTION')
		AND has_table_privilege(current_user,'public.issuing_card_bindings','SELECT WITH GRANT OPTION')
		AND NOT EXISTS(SELECT 1 FROM pg_database d,
		 LATERAL aclexplode(COALESCE(d.datacl,acldefault('d',d.datdba))) a
		 WHERE d.datname=current_database() AND a.grantee=0
		 AND a.privilege_type IN ('CREATE','TEMPORARY'))
		FROM pg_roles r WHERE r.rolname=current_user`).Scan(&canMigrate) != nil || !canMigrate {
		return errors.New("reviewed DBA preparation required")
	}
	if operation == "check-admin" {
		return nil
	}
	if err := provision(ctx, pool, c, expected, host); err != nil {
		return err
	}
	return operator(ctx, pool)
}

func scram(password string) (string, error) {
	for _, b := range []byte(password) {
		if b < 33 || b > 126 {
			return "", errors.New("database passwords require printable ASCII")
		}
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 4096, 32)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("Client Key"))
	stored := sha256.Sum256(mac.Sum(nil))
	mac.Reset()
	mac.Write([]byte("Server Key"))
	b64 := base64.StdEncoding.EncodeToString
	return "SCRAM-SHA-256$4096:" + b64(salt) + "$" + b64(stored[:]) + ":" + b64(mac.Sum(nil)), nil
}

func provision(ctx context.Context, pool *pgxpool.Pool, c contract, expected, host string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("runtime provisioning unavailable")
	}
	defer tx.Rollback(ctx)
	for _, b := range c.Bindings {
		cfg, _ := parseDatabase(os.Getenv(b.Env), expected, host)
		name, password := cfg.ConnConfig.User, cfg.ConnConfig.Password
		owner := "flowcard-go-runtime:" + expected + ":" + b.Name
		var exists, safe bool
		var comment *string
		err := tx.QueryRow(ctx, `SELECT true,NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls),
			shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname=$1`, name).Scan(&exists, &safe, &comment)
		if errors.Is(err, pgx.ErrNoRows) {
			verifier, err := scram(password)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "CREATE ROLE "+pgx.Identifier{name}.Sanitize()+
				" LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+verifier+"'"); err != nil {
				return errors.New("runtime login creation failed")
			}
			if _, err := tx.Exec(ctx, "COMMENT ON ROLE "+pgx.Identifier{name}.Sanitize()+" IS '"+owner+"'"); err != nil {
				return errors.New("runtime login ownership failed")
			}
		} else if err != nil || !exists || !safe || comment == nil || *comment != owner {
			return errors.New("existing runtime login is not owned")
		}
		role := pgx.Identifier{name}.Sanitize()
		if b.Role != "" {
			if !identifier.MatchString(b.Role) {
				return errors.New("invalid runtime role")
			}
			if _, err := tx.Exec(ctx, "GRANT "+pgx.Identifier{b.Role}.Sanitize()+" TO "+role); err != nil {
				return errors.New("runtime membership failed")
			}
			if b.Role == "onboarding_service" {
				if _, err := tx.Exec(ctx, "GRANT SELECT ON onboarding.schema_migrations TO "+role); err != nil {
					return errors.New("onboarding migration metadata grant failed")
				}
			}
		} else {
			for _, sql := range []string{
				"GRANT USAGE ON SCHEMA browser_gateway TO ",
				"GRANT SELECT ON browser_gateway.users,browser_gateway.sessions,browser_gateway.servicing_bindings,browser_gateway.financial_commands,browser_gateway.financial_receipts,browser_gateway.customer_offers TO ",
				"GRANT INSERT ON browser_gateway.sessions,browser_gateway.financial_commands,browser_gateway.financial_receipts,browser_gateway.customer_offers TO ",
				"GRANT DELETE ON browser_gateway.sessions TO ",
			} {
				if _, err := tx.Exec(ctx, sql+role); err != nil {
					return errors.New("gateway runtime grant failed")
				}
			}
		}
	}
	if tx.Commit(ctx) != nil {
		return errors.New("runtime provisioning commit failed")
	}
	for _, b := range c.Bindings {
		cfg, _ := parseDatabase(os.Getenv(b.Env), expected, host)
		check, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return errors.New("restricted login unavailable")
		}
		var unsafe bool
		err = check.QueryRow(ctx, `SELECT has_database_privilege(current_user,current_database(),'CREATE,TEMP')
			OR has_schema_privilege(current_user,'public','CREATE')`).Scan(&unsafe)
		check.Close()
		if err != nil || unsafe {
			return errors.New("restricted database privilege check failed")
		}
	}
	return nil
}

func operator(ctx context.Context, pool *pgxpool.Pool) error {
	var count int
	if pool.QueryRow(ctx, `SELECT count(*) FROM browser_gateway.users WHERE role='OPERATOR' AND active`).Scan(&count) != nil {
		return errors.New("operator check unavailable")
	}
	if count > 0 {
		return nil
	}
	var initial struct {
		Username string `json:"operator_username"`
		Password string `json:"operator_password"`
	}
	raw := os.Getenv("GO_BOOTSTRAP_JSON")
	if len(raw) > 8192 || json.Unmarshal([]byte(raw), &initial) != nil || !auth.ValidUsername(initial.Username) || !auth.ValidPassword(initial.Password) {
		return errors.New("initial invited operator required")
	}
	if postgres.CreateUser(ctx, pool, initial.Username, "OPERATOR", initial.Password) != nil {
		return errors.New("initial operator creation failed")
	}
	return nil
}
