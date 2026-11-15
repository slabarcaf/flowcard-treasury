// Package acceptance_test exercises real HTTP against owned disposable PostgreSQL.
package acceptance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/cli"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/httpapi"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/postgres"
)

const password = "synthetic password for owned tests"
const origin = "http://localhost:5173"

type environment struct {
	admin      *pgxpool.Pool
	store      *postgres.Store
	adminURL   string
	runtimeURL string
	name       string
	role       string
	marker     string
	owner      string
}

func owned(t *testing.T) *environment {
	t.Helper()
	path := os.Getenv("BROWSER_GATEWAY_TEST_ADMIN_URL_FILE")
	if path == "" {
		t.Skip("UNVERIFIED DB ACCEPTANCE: BROWSER_GATEWAY_TEST_ADMIN_URL_FILE not supplied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test admin file unavailable")
	}
	raw := strings.TrimSpace(string(data))
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid test admin configuration")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path != "/postgres" || cfg.ConnConfig.Database != "postgres" {
		t.Fatal("test admin must target local maintenance database")
	}
	checkPeer := func(host string, port uint16) bool {
		ip := net.ParseIP(host)
		return port == 54329 && (host == "localhost" || (ip != nil && ip.IsLoopback()))
	}
	if !checkPeer(cfg.ConnConfig.Host, cfg.ConnConfig.Port) {
		t.Fatal("test admin must use existing localhost PostgreSQL 54329")
	}
	for _, f := range cfg.ConnConfig.Fallbacks {
		if !checkPeer(f.Host, f.Port) {
			t.Fatal("test admin fallback must remain local 54329")
		}
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test maintenance pool unavailable")
	}
	entropy := make([]byte, 24)
	if _, err := rand.Read(entropy); err != nil {
		admin.Close()
		t.Fatal("test entropy unavailable")
	}
	marker := hex.EncodeToString(entropy)
	e := &environment{admin: admin, name: "browser_test_" + marker[:20], role: "browser_rt_" + marker[:20], marker: marker}
	if err := admin.QueryRow(ctx, `SELECT current_user`).Scan(&e.owner); err != nil {
		admin.Close()
		t.Fatal("test maintenance unavailable")
	}
	ident := pgx.Identifier{e.name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		admin.Close()
		t.Fatal("owned database creation failed")
	}
	if _, err := admin.Exec(ctx, "COMMENT ON DATABASE "+ident+" IS '"+marker+"'"); err != nil {
		admin.Close()
		t.Fatal("owned database marking failed, cleanup withheld")
	}
	// Register cleanup immediately after proving the database marker.
	t.Cleanup(func() { e.close(t) })
	u.Path = "/" + e.name
	e.adminURL = u.String()
	e.admin, err = postgres.Admin(ctx, e.adminURL, e.name)
	if err != nil {
		e.admin = admin
		t.Fatal("owned database connection failed")
	}
	// Keep maintenance separate from the owned administrative connection.
	maintenance := admin
	t.Cleanup(maintenance.Close)
	if err := cli.Migrate(ctx, []string{"--database-name", e.name}, e.adminURL); err != nil {
		t.Fatal("owned migrator failed")
	}
	if err := cli.Migrate(ctx, []string{"--database-name", e.name}, e.adminURL); err != nil {
		t.Fatal("second owned migration failed")
	}
	roleIdent := pgx.Identifier{e.role}.Sanitize()
	if _, err := maintenance.Exec(ctx, "CREATE ROLE "+roleIdent+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+marker+"'"); err != nil {
		t.Fatal("owned runtime role creation failed")
	}
	if _, err := maintenance.Exec(ctx, "COMMENT ON ROLE "+roleIdent+" IS '"+marker+"'"); err != nil {
		t.Fatal("owned runtime role marking failed, role cleanup withheld")
	}
	if _, err := e.admin.Exec(ctx, "REVOKE ALL ON DATABASE "+ident+" FROM PUBLIC; REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT CONNECT ON DATABASE "+ident+" TO "+roleIdent+"; GRANT USAGE ON SCHEMA browser_gateway TO "+roleIdent+"; GRANT SELECT ON browser_gateway.users TO "+roleIdent+"; GRANT SELECT,INSERT,DELETE ON browser_gateway.sessions TO "+roleIdent); err != nil {
		t.Fatal("owned runtime grants failed")
	}
	u.User = url.UserPassword(e.role, marker)
	e.runtimeURL = u.String()
	e.store, err = postgres.Open(ctx, e.runtimeURL)
	if err != nil {
		t.Fatal("owned runtime connection failed")
	}
	if err := e.store.Ready(ctx); err != nil {
		t.Fatal("restricted runtime readiness failed")
	}
	t.Log("owned database and runtime login created; migrator applied twice; restricted readiness verified")
	return e
}

func (e *environment) close(t *testing.T) {
	t.Helper()
	if e.store != nil {
		e.store.Pool.Close()
	}
	if e.admin != nil {
		e.admin.Close()
	}
	// Reopen maintenance with the original host and credentials, never runtime.
	u, err := url.Parse(e.adminURL)
	if err != nil || u.User == nil {
		t.Error("cleanup target unavailable")
		return
	}
	u.Path = "/postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Error("cleanup connection unavailable")
		return
	}
	defer p.Close()
	var marker, owner string
	if err := p.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database'),pg_get_userbyid(datdba) FROM pg_database WHERE datname=$1`, e.name).Scan(&marker, &owner); err != nil || marker != e.marker || owner != e.owner {
		t.Error("cleanup refused database ownership mismatch")
		return
	}
	var roleMarker *string
	var login bool
	err = p.QueryRow(ctx, `SELECT shobj_description(oid,'pg_authid'),rolcanlogin FROM pg_roles WHERE rolname=$1`, e.role).Scan(&roleMarker, &login)
	if err != nil && err != pgx.ErrNoRows {
		t.Error("cleanup role inspection failed")
		return
	}
	if err == nil && (roleMarker == nil || *roleMarker != e.marker || !login) {
		t.Error("cleanup refused role ownership mismatch")
		return
	}
	roleExists := err == nil
	if _, err := p.Exec(ctx, "DROP DATABASE "+pgx.Identifier{e.name}.Sanitize()); err != nil {
		t.Error("owned database cleanup failed")
		return
	}
	if roleExists {
		if _, err := p.Exec(ctx, "DROP ROLE "+pgx.Identifier{e.role}.Sanitize()); err != nil {
			t.Error("owned runtime login cleanup failed")
			return
		}
	}
	t.Log("marker/owner checked: exact owned database and login removed")
}

func (e *environment) user(t *testing.T, name, role string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cli.Users(ctx, []string{"create", "--username", name, "--role", role, "--password-stdin", "--database-name", e.name}, e.adminURL, strings.NewReader(password+"\n")); err != nil {
		t.Fatal("owned user provisioning failed")
	}
}

func (e *environment) server(t *testing.T) *httptest.Server {
	t.Helper()
	c, err := config.Parse("127.0.0.1:0", e.runtimeURL, origin, "true")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(httpapi.New(c, e.store))
	t.Cleanup(s.Close)
	return s
}

type result struct {
	status  int
	body    []byte
	cookies []*http.Cookie
	header  http.Header
}

func request(t *testing.T, s *httptest.Server, method, path, body string, cookie *http.Cookie, headers map[string]string) result {
	t.Helper()
	r, err := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", origin)
	for k, v := range headers {
		if v == "<absent>" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("local HTTP request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16384))
	if err != nil {
		t.Fatal("local HTTP response failed")
	}
	if strings.HasPrefix(path, "/api/") && response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("session response missing no-store")
	}
	for _, private := range []string{password, "password_verifier", "password_salt", "postgres://", "digest"} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatal("private field leaked in HTTP response")
		}
	}
	return result{response.StatusCode, data, response.Cookies(), response.Header}
}

func login(t *testing.T, s *httptest.Server, name, role string) (*http.Cookie, postgres.Session) {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"username": name, "password": password})
	got := request(t, s, "POST", "/api/session/login", string(data), nil, nil)
	if got.status != 200 || len(got.cookies) != 1 {
		t.Fatalf("login status=%d cookies=%d, want 200 and 1", got.status, len(got.cookies))
	}
	var session postgres.Session
	if err := json.Unmarshal(got.body, &session); err != nil {
		t.Fatal("invalid public session")
	}
	c := got.cookies[0]
	if c.Name != "flowcard_session" || c.Path != "/api" || c.Domain != "" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || len(c.Value) != 43 || c.Secure {
		t.Fatal("local session cookie contract mismatch")
	}
	if bytes.Contains(got.body, []byte(c.Value)) || session.User.Username != name || session.User.Role != role || session.User.ID == "" || time.Until(session.ExpiresAt) < 7*time.Hour || time.Until(session.ExpiresAt) > 8*time.Hour {
		t.Fatal("public session contract mismatch")
	}
	return c, session
}

func TestOwnedSessionLifecycle(t *testing.T) {
	e := owned(t)
	e.user(t, "operator.one", "OPERATOR")
	e.user(t, "customer.one", "CUSTOMER")
	s := e.server(t)
	op, opSession := login(t, s, "operator.one", "OPERATOR")
	customer, customerSession := login(t, s, "customer.one", "CUSTOMER")
	if opSession.User.ID == customerSession.User.ID {
		t.Fatal("distinct roles share identity")
	}
	secureConfig, err := config.Parse("127.0.0.1:0", e.runtimeURL, origin, "")
	if err != nil {
		t.Fatal("secure cookie configuration rejected")
	}
	secureServer := httptest.NewServer(httpapi.New(secureConfig, e.store))
	defer secureServer.Close()
	secureBody, _ := json.Marshal(map[string]string{"username": "operator.one", "password": password})
	secureResult := request(t, secureServer, "POST", "/api/session/login", string(secureBody), nil, nil)
	if secureResult.status != 200 || len(secureResult.cookies) != 1 || !secureResult.cookies[0].Secure {
		t.Fatal("default session cookie is not Secure")
	}
	if got := request(t, s, "GET", "/api/session/me", "", op, nil); got.status != 200 {
		t.Fatalf("me=%d", got.status)
	}
	// Recreate handler and runtime pool while retaining database sessions.
	s.Close()
	e.store.Pool.Close()
	e.store, err = postgres.Open(context.Background(), e.runtimeURL)
	if err != nil {
		t.Fatal("restart pool unavailable")
	}
	s = e.server(t)
	if got := request(t, s, "GET", "/api/session/me", "", op, nil); got.status != 200 {
		t.Fatalf("persisted session after restart=%d", got.status)
	}
	got := request(t, s, "POST", "/api/session/logout", "", op, nil)
	if got.status != 204 || len(got.cookies) != 1 || got.cookies[0].MaxAge != -1 || got.cookies[0].Value != "" {
		t.Fatal("logout did not revoke and clear cookie")
	}
	if got := request(t, s, "GET", "/api/session/me", "", op, nil); got.status != 401 {
		t.Fatal("logged-out session accepted")
	}
	if got := request(t, s, "GET", "/api/session/me", "", customer, nil); got.status != 200 {
		t.Fatal("logout revoked other user's session")
	}
	if err := cli.Users(context.Background(), []string{"disable", "--username", "customer.one", "--database-name", e.name}, e.adminURL, strings.NewReader("")); err != nil {
		t.Fatal("disable failed")
	}
	if got := request(t, s, "GET", "/api/session/me", "", customer, nil); got.status != 401 {
		t.Fatal("disabled user's session accepted")
	}
	data, _ := json.Marshal(map[string]string{"username": "customer.one", "password": password})
	if got := request(t, s, "POST", "/api/session/login", string(data), nil, nil); got.status != 401 {
		t.Fatal("disabled user logged in")
	}
	// Expiry is enforced by the DB lookup rather than the browser cookie.
	e.user(t, "expired.user", "CUSTOMER")
	expired, _ := login(t, s, "expired.user", "CUSTOMER")
	if _, err := e.admin.Exec(context.Background(), `UPDATE browser_gateway.sessions SET created_at=created_at-interval '9 hours', expires_at=expires_at-interval '9 hours' WHERE user_id=(SELECT id FROM browser_gateway.users WHERE username='expired.user')`); err != nil {
		t.Fatal("expiry fixture failed")
	}
	if got := request(t, s, "GET", "/api/session/me", "", expired, nil); got.status != 401 {
		t.Fatal("expired session accepted")
	}
	// Simulate storage loss only in this scenario's owned runtime pool.
	e.store.Pool.Close()
	loginBody, _ := json.Marshal(map[string]string{"username": "operator.one", "password": password})
	for _, route := range []struct{ method, path, body string }{
		{"POST", "/api/session/login", string(loginBody)},
		{"GET", "/api/session/me", ""},
		{"POST", "/api/session/logout", ""},
	} {
		got := request(t, s, route.method, route.path, route.body, secureResult.cookies[0], nil)
		if got.status != 503 || strings.TrimSpace(string(got.body)) != `{"error":"unavailable"}` || len(got.cookies) != 0 {
			t.Errorf("%s closed-pool response: status=%d cookies=%d, want safe 503 without cookie changes", route.path, got.status, len(got.cookies))
		}
	}
}

func TestOwnedBrowserDenials(t *testing.T) {
	e := owned(t)
	e.user(t, "customer.two", "CUSTOMER")
	s := e.server(t)
	cookie, _ := login(t, s, "customer.two", "CUSTOMER")
	valid := `{"username":"customer.two","password":"` + password + `"}`
	cases := []struct {
		name, method, path, body string
		cookie                   *http.Cookie
		headers                  map[string]string
		status                   int
	}{
		{"bad_origin", "POST", "/api/session/login", valid, nil, map[string]string{"Origin": "https://evil.example"}, 403},
		{"null_origin", "GET", "/api/session/me", "", cookie, map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin", "Referer": origin + "/"}, 403},
		{"empty_origin", "GET", "/api/session/me", "", cookie, map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin", "Referer": origin + "/"}, 403},
		{"post_missing_origin", "POST", "/api/session/logout", "", cookie, map[string]string{"Origin": "<absent>", "Sec-Fetch-Site": "same-origin", "Referer": origin + "/"}, 403},
		{"logout_body", "POST", "/api/session/logout", `{"role":"OPERATOR"}`, cookie, nil, 400},
		{"get_missing_origin", "GET", "/api/session/me", "", cookie, map[string]string{"Origin": "<absent>"}, 403},
		{"referer_credentials", "GET", "/api/session/me", "", cookie, map[string]string{"Origin": "<absent>", "Sec-Fetch-Site": "same-origin", "Referer": "http://user:pass@localhost:5173/"}, 403},
		{"referer_allowed", "GET", "/api/session/me", "", cookie, map[string]string{"Origin": "<absent>", "Sec-Fetch-Site": "same-origin", "Referer": origin + "/route"}, 200},
		{"forged_cookie", "GET", "/api/session/me", "", &http.Cookie{Name: "flowcard_session", Value: strings.Repeat("A", 43)}, nil, 401},
		{"forged_identity", "GET", "/api/session/me", "", nil, map[string]string{"Authorization": "Bearer forged", "X-Actor-Role": "OPERATOR", "X-Forwarded-User": "operator"}, 401},
		{"client_role", "POST", "/api/session/login", strings.TrimSuffix(valid, "}") + `,"role":"OPERATOR"}`, nil, nil, 400},
		{"duplicate_key", "POST", "/api/session/login", strings.TrimSuffix(valid, "}") + `,"username":"operator"}`, nil, nil, 400},
		{"trailing_json", "POST", "/api/session/login", valid + ` {}`, nil, nil, 400},
		{"malformed", "POST", "/api/session/login", `{"username":`, nil, nil, 400},
		{"invalid_utf8", "POST", "/api/session/login", "{\"username\":\"alice\",\"password\":\"long password text\xff\"}", nil, nil, 400},
		{"oversize", "POST", "/api/session/login", `{"username":"customer.two","password":"` + strings.Repeat("a", 9000) + `"}`, nil, nil, 400},
		{"unknown_user", "POST", "/api/session/login", `{"username":"unknown.user","password":"` + password + `"}`, nil, nil, 401},
		{"bad_password", "POST", "/api/session/login", `{"username":"customer.two","password":"wrong long password"}`, nil, nil, 401},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := request(t, s, tt.method, tt.path, tt.body, tt.cookie, tt.headers)
			if got.status != tt.status {
				t.Errorf("status=%d, want %d", got.status, tt.status)
			}
		})
	}
	// Fresh limiter, same direct peer. Spoofed forwarding cannot evade either bucket.
	s = e.server(t)
	for i := 0; i < 5; i++ {
		got := request(t, s, "POST", "/api/session/login", `{"username":"limited.user","password":"wrong long password"}`, nil, map[string]string{"X-Forwarded-For": string(rune('a' + i))})
		if got.status != 401 {
			t.Fatalf("user attempt %d=%d", i, got.status)
		}
	}
	got := request(t, s, "POST", "/api/session/login", `{"username":"limited.user","password":"wrong long password"}`, nil, map[string]string{"X-Forwarded-For": "other"})
	if got.status != 429 || got.header.Get("Retry-After") == "" {
		t.Fatal("user rate limit missing")
	}
	for i := 0; i < 15; i++ {
		name := "peer.user" + string(rune('a'+i))
		body := `{"username":"` + name + `","password":"wrong long password"}`
		if got := request(t, s, "POST", "/api/session/login", body, nil, map[string]string{"X-Forwarded-For": name}); got.status != 401 {
			t.Fatalf("peer attempt %d=%d", i, got.status)
		}
	}
	if got := request(t, s, "POST", "/api/session/login", valid, nil, map[string]string{"X-Forwarded-For": "newpeer"}); got.status != 429 {
		t.Fatal("direct peer rate limit bypassed")
	}
}

func TestOwnedPrivilegeAndMigrationBoundary(t *testing.T) {
	e := owned(t)
	e.user(t, "role.fixed", "CUSTOMER")
	ctx := context.Background()
	// Exercise the executable entry point against the same owned target too.
	for range 2 {
		commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		command := exec.CommandContext(commandCtx, "go", "run", "./cmd/migrate", "--database-name", e.name)
		command.Dir = "../.."
		command.Env = append(os.Environ(), "BROWSER_GATEWAY_ADMIN_DATABASE_URL="+e.adminURL)
		err := command.Run()
		cancel()
		if err != nil {
			t.Fatal("owned migration executable failed")
		}
	}
	for _, sql := range []string{
		`UPDATE browser_gateway.users SET active=false`,
		`INSERT INTO browser_gateway.users SELECT * FROM browser_gateway.users`,
		`DELETE FROM browser_gateway.users`,
		`UPDATE browser_gateway.sessions SET expires_at=expires_at`,
		`TRUNCATE browser_gateway.sessions`,
		`CREATE TABLE browser_gateway.forbidden(id int)`,
		`CREATE SCHEMA forbidden`,
		`CREATE TEMP TABLE forbidden(id int)`,
	} {
		if _, err := e.store.Pool.Exec(ctx, sql); err == nil {
			t.Errorf("restricted runtime unexpectedly allowed %s", sql)
		}
	}
	if err := (&postgres.Store{Pool: e.admin}).Ready(ctx); err == nil {
		t.Fatal("administrative runtime marked ready")
	}
	c, _ := config.Parse("127.0.0.1:0", e.adminURL, origin, "true")
	s := httptest.NewServer(httpapi.New(c, &postgres.Store{Pool: e.admin}))
	defer s.Close()
	if got := request(t, s, "GET", "/readyz", "", nil, nil); got.status != 503 {
		t.Fatal("admin readyz accepted")
	}
	if err := cli.Migrate(ctx, []string{"--database-name", "wrong_database"}, e.adminURL); err == nil {
		t.Fatal("migrator accepted wrong database")
	}
	if err := cli.Users(ctx, []string{"create", "--username", "wrong.target", "--role", "OPERATOR", "--password-stdin", "--database-name", "wrong_database"}, e.adminURL, strings.NewReader(password)); err == nil {
		t.Fatal("user CLI accepted wrong database")
	}
	if err := cli.Users(ctx, []string{"disable", "--username", "role.fixed"}, e.adminURL, strings.NewReader("")); err == nil {
		t.Fatal("user CLI accepted missing database")
	}
	if _, err := e.admin.Exec(ctx, `UPDATE browser_gateway.users SET role='OPERATOR' WHERE username='role.fixed'`); err == nil {
		t.Fatal("user role mutation accepted")
	}
	if _, err := e.admin.Exec(ctx, `UPDATE browser_gateway.schema_migrations SET checksum='tampered'`); err != nil {
		t.Fatal("checksum fixture failed")
	}
	if err := cli.Migrate(ctx, []string{"--database-name", e.name}, e.adminURL); err == nil {
		t.Fatal("tampered checksum accepted")
	}
	// Readiness must also reject an accidental extra runtime write grant.
	if _, err := e.admin.Exec(ctx, "GRANT UPDATE(active) ON browser_gateway.users TO "+pgx.Identifier{e.role}.Sanitize()); err != nil {
		t.Fatal("excess grant fixture failed")
	}
	if err := e.store.Ready(ctx); err == nil {
		t.Fatal("runtime with user write grant marked ready")
	}
}
