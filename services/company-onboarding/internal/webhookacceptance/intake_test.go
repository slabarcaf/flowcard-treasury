package webhookacceptance_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	onboardingHTTP "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/platform/airwallex"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/validation"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

const notificationSecret = "owned-provider-notification-key-only"
const serviceSecret = "owned-internal-service-assertion-key-only"

type ownedDatabase struct {
	admin, maintenance, runtime *pgxpool.Pool
	runtimeConfig               *pgxpool.Config
	name, role, marker          string
}

func openOwned(t *testing.T) *ownedDatabase {
	t.Helper()
	file := os.Getenv("ONBOARDING_WEBHOOK_TEST_ADMIN_URL_FILE")
	if file == "" {
		t.Skip("UNVERIFIED DB ACCEPTANCE: ONBOARDING_WEBHOOK_TEST_ADMIN_URL_FILE not supplied")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("private admin URL file unavailable")
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid private admin URL")
	}
	if (cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost") || cfg.ConnConfig.Port != 54329 {
		t.Fatal("owned test requires existing loopback PostgreSQL on 54329")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	maintenance, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("maintenance connection unavailable")
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		maintenance.Close()
		t.Fatal("random identity unavailable")
	}
	suffix := hex.EncodeToString(random)
	e := &ownedDatabase{maintenance: maintenance, name: "flowcard_onboarding_webhook_" + suffix, role: "onboarding_wh_" + suffix, marker: "owned-onboarding-webhook-" + suffix}
	createdDB, createdRole := false, false
	t.Cleanup(func() {
		if e.runtime != nil {
			e.runtime.Close()
		}
		if e.admin != nil {
			e.admin.Close()
		}
		defer maintenance.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if createdDB {
			var marker string
			if err := maintenance.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, e.name).Scan(&marker); err != nil || marker != e.marker {
				t.Error("owned database cleanup marker failed")
				return
			}
			if _, err := maintenance.Exec(ctx, "DROP DATABASE "+pgx.Identifier{e.name}.Sanitize()); err != nil {
				t.Error("owned database cleanup failed")
				return
			}
		}
		if createdRole {
			if _, err := maintenance.Exec(ctx, "DROP ROLE "+pgx.Identifier{e.role}.Sanitize()); err != nil {
				t.Error("owned login cleanup failed")
				return
			}
		}
		var count int
		if err := maintenance.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_database WHERE datname=$1)+(SELECT count(*) FROM pg_roles WHERE rolname=$2)`, e.name, e.role).Scan(&count); err != nil || count != 0 {
			t.Error("owned cleanup residual resources")
		} else {
			t.Log("owned database and login removed")
		}
	})
	if _, err = maintenance.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{e.name}.Sanitize()); err != nil {
		t.Fatal("create owned database failed")
	}
	createdDB = true
	if _, err = maintenance.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{e.name}.Sanitize()+" IS '"+e.marker+"'"); err != nil {
		t.Fatal("mark owned database failed")
	}
	ownedConfig := cfg.Copy()
	ownedConfig.ConnConfig.Database = e.name
	e.admin, err = pgxpool.NewWithConfig(ctx, ownedConfig)
	if err != nil {
		t.Fatal("owned admin pool unavailable")
	}
	if err = postgres.RunMigrations(ctx, e.admin); err != nil {
		t.Fatalf("owned migration failed: %v", postgres.SanitizeDBError(err))
	}
	if err = postgres.RunMigrations(ctx, e.admin); err != nil {
		t.Fatal("repeat owned migration failed")
	}
	passwordBytes := make([]byte, 24)
	if _, err = rand.Read(passwordBytes); err != nil {
		t.Fatal("owned login random failed")
	}
	password := hex.EncodeToString(passwordBytes)
	if _, err = maintenance.Exec(ctx, "CREATE ROLE "+pgx.Identifier{e.role}.Sanitize()+" LOGIN PASSWORD '"+password+"' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION"); err != nil {
		t.Fatal("create owned login failed")
	}
	createdRole = true
	role := pgx.Identifier{e.role}.Sanitize()
	grants := []string{"REVOKE ALL ON DATABASE " + pgx.Identifier{e.name}.Sanitize() + " FROM PUBLIC", "GRANT CONNECT ON DATABASE " + pgx.Identifier{e.name}.Sanitize() + " TO " + role, "GRANT USAGE ON SCHEMA onboarding TO " + role, "GRANT SELECT ON ALL TABLES IN SCHEMA onboarding TO " + role, "GRANT INSERT,UPDATE ON onboarding.webhook_inbox TO " + role, "GRANT INSERT ON onboarding.webhook_conflicts TO " + role, "GRANT UPDATE ON onboarding.platform_accounts,onboarding.applications,onboarding.companies TO " + role, "GRANT USAGE,SELECT ON SEQUENCE onboarding.webhook_inbox_id_seq TO " + role}
	for _, sql := range grants {
		if _, err = e.admin.Exec(ctx, sql); err != nil {
			t.Fatal("owned runtime grants failed")
		}
	}
	runtimeConfig := ownedConfig.Copy()
	runtimeConfig.ConnConfig.User = e.role
	runtimeConfig.ConnConfig.Password = password
	runtimeConfig.MaxConns = 4
	e.runtimeConfig = runtimeConfig
	e.restart(t)
	if err = postgres.VerifySchemaReadiness(ctx, e.runtime); err != nil {
		t.Fatal("restricted schema readiness failed")
	}
	return e
}
func (e *ownedDatabase) restart(t *testing.T) {
	t.Helper()
	if e.runtime != nil {
		e.runtime.Close()
	}
	var err error
	e.runtime, err = pgxpool.NewWithConfig(context.Background(), e.runtimeConfig.Copy())
	if err != nil {
		t.Fatal("owned runtime reopen failed")
	}
}
func (e *ownedDatabase) server(t *testing.T) *httptest.Server {
	t.Helper()
	repo := postgres.NewRepository(e.runtime)
	provider, err := webhooks.NewProviderHandler(repo, []byte(notificationSecret), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	internal, err := onboardingHTTP.NewWebhookAuthenticator([]byte(serviceSecret))
	if err != nil {
		t.Fatal(err)
	}
	handler := onboardingHTTP.NewServer(repo, onboardingHTTP.DenyAllAuthenticator{}, validation.VerifiedUSBusinessContractGate{}, storage.NewUnavailableStore("owned test"))
	handler.WithWebhookHandler(webhooks.NewHandler(repo, nil), internal)
	handler.WithProviderWebhookHandler(provider)
	server := httptest.NewServer(handler.Routes())
	t.Cleanup(server.Close)
	return server
}
func signed(url, body string) *http.Request {
	request, _ := http.NewRequest("POST", url, strings.NewReader(body))
	timestamp := "1"
	mac := hmac.New(sha256.New, []byte(notificationSecret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte(body))
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	return request
}
func send(t *testing.T, client *http.Client, request *http.Request) int {
	t.Helper()
	client.Timeout = 5 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("owned HTTP request failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}
func envelope(id, name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"data":{"id":"acct_owned"}}`, id, name)
}
func status(t *testing.T, e *ownedDatabase, id string) string {
	t.Helper()
	var result string
	if err := e.admin.QueryRow(context.Background(), "SELECT status FROM onboarding.webhook_inbox WHERE event_id=$1", id).Scan(&result); err != nil {
		t.Fatal("read owned receipt failed")
	}
	return result
}

type platformFixture struct{ calls atomic.Int32 }

func (p *platformFixture) GetAccount(_ context.Context, id string) (*airwallex.AccountDetailsResponse, error) {
	p.calls.Add(1)
	return &airwallex.AccountDetailsResponse{ID: id, Status: "ACTIVE"}, nil
}

func TestDirectWebhookOwnedRecovery(t *testing.T) {
	e := openOwned(t)
	ctx := context.Background()
	adminRepo := postgres.NewRepository(e.admin)
	comp, _ := domain.NewDraftCompany("comp_owned", "Owned webhook company")
	if err := adminRepo.CreateCompany(ctx, comp); err != nil {
		t.Fatal("owned company setup failed")
	}
	pa, _ := domain.NewPlatformAccount("pa_owned", comp.ID(), "AIRWALLEX", "sandbox")
	if err := pa.SetExternalAccountID("acct_owned"); err != nil {
		t.Fatal(err)
	}
	if err := adminRepo.CreatePlatformAccount(ctx, pa); err != nil {
		t.Fatal("owned platform setup failed")
	}
	server := e.server(t)
	body := envelope("evt_recovery", "account.active")
	if got := send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", body)); got != 200 {
		t.Fatalf("receipt status=%d", got)
	}
	var original []byte
	var env string
	if err := e.admin.QueryRow(ctx, "SELECT raw_body,environment FROM onboarding.webhook_inbox WHERE event_id='evt_recovery'").Scan(&original, &env); err != nil || string(original) != body || env != "sandbox" || status(t, e, "evt_recovery") != "PENDING" {
		t.Fatal("durable original bytes/scope missing before worker")
	}
	server.Close()
	e.restart(t)
	server = e.server(t)
	if got := send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", body)); got != 200 {
		t.Fatal("restart duplicate failed")
	}
	repo := postgres.NewRepository(e.runtime)
	claims, err := repo.ClaimPendingWebhooks(ctx, 1, 15*time.Millisecond)
	if err != nil || len(claims) != 1 {
		t.Fatal("owned crashed lease setup failed")
	}
	time.Sleep(time.Until(*claims[0].LeaseExpiresAt) + 10*time.Millisecond)
	platform := &platformFixture{}
	processor := webhooks.NewProcessor(repo, platform)
	n, err := processor.ProcessPending(ctx)
	if err != nil || n != 1 || status(t, e, "evt_recovery") != "PROCESSED" {
		var message string
		_ = e.admin.QueryRow(ctx, "SELECT COALESCE(last_error,'') FROM onboarding.webhook_inbox WHERE event_id='evt_recovery'").Scan(&message)
		t.Fatalf("restart processing failed: n=%d state=%s detail=%v", n, status(t, e, "evt_recovery"), postgres.SanitizeDBError(errors.New(message)))
	}
	loaded, err := repo.GetPlatformAccountByExternalIDAndEnv(ctx, "acct_owned", "AIRWALLEX", "sandbox")
	if err != nil || loaded.Status() != domain.PlatformAccountStatusActive {
		t.Fatal("authoritative active observation not persisted")
	}
	if err := repo.MarkWebhookProcessed(ctx, "evt_recovery", claims[0].LeaseToken, postgres.WebhookStatusFailed, "stale worker"); !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatal("stale crash worker was not fenced")
	}
	if n, err = webhooks.NewProcessor(repo, platform).ProcessPending(ctx); err != nil || n != 0 || platform.calls.Load() != 1 {
		t.Fatal("duplicate caused processing churn")
	}
	rfi := `{"id":"evt_rfi","name":"rfi.created","data":{"accountId":"acct_owned"}}`
	if send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", rfi)) != 200 {
		t.Fatal("RFI receipt failed")
	}
	if _, err = processor.ProcessPending(ctx); err != nil || status(t, e, "evt_rfi") != "QUARANTINED" {
		t.Fatal("unverified RFI was not held")
	}
	// Exact legacy JWT contract stays required; public HMAC never bypasses it.
	if send(t, server.Client(), signed(server.URL+"/api/webhooks/airwallex", body)) != 401 {
		t.Fatal("public signature bypassed internal JWT")
	}
	now := time.Now().Unix()
	claimsJSON, _ := json.Marshal(map[string]any{"iss": "flowcard-node-proxy", "aud": "flowcard-onboarding-webhooks", "sub": "node-webhook-forwarder", "iat": now, "exp": now + 20, "jti": "owned-forwarding-duplicate"})
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	input := header + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, []byte(serviceSecret))
	_, _ = mac.Write([]byte(input))
	token := input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	request, _ := http.NewRequest("POST", server.URL+"/api/webhooks/airwallex", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if send(t, server.Client(), request) != 200 || status(t, e, "evt_recovery") != "PROCESSED" {
		t.Fatal("legacy duplicate contract changed stored result")
	}
	e.runtime.Close()
	if send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", envelope("evt_outage", "account.active"))) != 500 {
		t.Fatal("storage loss falsely acknowledged")
	}
	var count int
	if err := e.admin.QueryRow(ctx, "SELECT count(*) FROM onboarding.webhook_inbox WHERE event_id='evt_outage'").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed receipt persisted")
	}
}

func TestDirectWebhookOwnedConcurrencyAndConflict(t *testing.T) {
	e := openOwned(t)
	server := e.server(t)
	ctx := context.Background()
	body := envelope("evt_concurrent", "account.active")
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() {
			response, err := server.Client().Do(signed(server.URL+"/webhooks/airwallex", body))
			if err != nil {
				results <- 0
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			results <- response.StatusCode
		}()
	}
	for i := 0; i < 8; i++ {
		if got := <-results; got != 200 {
			t.Fatalf("concurrent receipt status=%d", got)
		}
	}
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(decoded, "", "  ")
	if send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", string(pretty))) != 200 {
		t.Fatal("format-only duplicate was treated as a conflict")
	}
	var count int
	if err := e.admin.QueryRow(ctx, "SELECT count(*) FROM onboarding.webhook_inbox").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate receipts created extra rows")
	}
	repo := postgres.NewRepository(e.runtime)
	claims, err := repo.ClaimPendingWebhooks(ctx, 1, time.Minute)
	if err != nil || len(claims) != 1 {
		t.Fatal("conflict lease setup failed")
	}
	changed := envelope("evt_concurrent", "account.suspended")
	for i := 0; i < 2; i++ {
		if send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", changed)) != 409 {
			t.Fatal("changed event was acknowledged")
		}
	}
	if status(t, e, "evt_concurrent") != "QUARANTINED" {
		t.Fatal("conflict did not quarantine leased work")
	}
	if err = repo.MarkWebhookProcessed(ctx, "evt_concurrent", claims[0].LeaseToken, postgres.WebhookStatusProcessed, ""); !errors.Is(err, postgres.ErrStaleLeaseToken) {
		t.Fatal("conflicted worker was not fenced")
	}
	var raw, conflict []byte
	if err = e.admin.QueryRow(ctx, "SELECT raw_body FROM onboarding.webhook_inbox WHERE event_id='evt_concurrent'").Scan(&raw); err != nil || !bytes.Equal(raw, []byte(body)) {
		t.Fatal("original event overwritten")
	}
	if err = e.admin.QueryRow(ctx, "SELECT raw_body FROM onboarding.webhook_conflicts WHERE event_id='evt_concurrent'").Scan(&conflict); err != nil || !bytes.Equal(conflict, []byte(changed)) {
		t.Fatal("conflict bytes missing")
	}
	if err = e.admin.QueryRow(ctx, "SELECT count(*) FROM onboarding.webhook_conflicts WHERE event_id='evt_concurrent'").Scan(&count); err != nil || count != 1 {
		t.Fatal("repeated conflict created duplicate evidence")
	}
	if send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", body)) != 200 || status(t, e, "evt_concurrent") != "QUARANTINED" {
		t.Fatal("duplicate revived conflicted work")
	}
	terminal := envelope("evt_terminal", "account.updated")
	if send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", terminal)) != 200 {
		t.Fatal("terminal fixture receipt failed")
	}
	claims, err = repo.ClaimPendingWebhooks(ctx, 1, time.Minute)
	if err != nil || len(claims) != 1 {
		t.Fatal("terminal fixture lease failed")
	}
	if err = repo.MarkWebhookProcessed(ctx, "evt_terminal", claims[0].LeaseToken, postgres.WebhookStatusProcessed, ""); err != nil {
		t.Fatal("terminal processing failed")
	}
	if send(t, server.Client(), signed(server.URL+"/webhooks/airwallex", envelope("evt_terminal", "account.suspended"))) != 409 || status(t, e, "evt_terminal") != "PROCESSED" {
		t.Fatal("conflict changed processed result")
	}
	_, err = e.runtime.Exec(ctx, "UPDATE onboarding.webhook_conflicts SET event_type='tampered'")
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "42501" {
		t.Fatal("conflict evidence update did not fail with permission denial")
	}
}
