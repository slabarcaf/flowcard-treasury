package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOwnedLocalServiceSetupAndMarkerCleanup(t *testing.T) {
	adminFile := os.Getenv("FUNDING_TEST_ADMIN_URL_FILE")
	if adminFile == "" {
		t.Skip("UNVERIFIED LOCAL DB ACCEPTANCE: FUNDING_TEST_ADMIN_URL_FILE not supplied")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	entropy, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	runID := "fc-sbx-" + entropy[:16]
	databaseName := "flowcard_funding_demo_sandbox_test_" + entropy[:16]
	manifest := filepath.Join(dir, "manifest.json")
	cfg := credentials{platform: "fixture-platform"}
	if err := withManifest(manifest, runID, cfg.platform, true, func(m *sandboxManifest) error {
		m.Intents["account-borrower"] = setupIntent{ResourceID: "fixture-account"}
		m.Intents["account-funding"] = setupIntent{ResourceID: "fixture-funding"}
		m.Intents["callback-decline"] = setupIntent{ParentTransactionID: "fixture-parent"}
		m.Intents["card-borrower"] = setupIntent{ResourceID: "fixture-card", State: "VERIFIED"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	api := &sandboxAPI{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || (r.URL.Path != "/api/v1/accounts/fixture-account" && r.URL.Path != "/api/v1/accounts/fixture-funding") {
			t.Fatal("unexpected sandbox request in owned DB test")
		}
		role := "borrower"
		id := "fixture-account"
		if strings.HasSuffix(r.URL.Path, "fixture-funding") {
			role = "funding"
			id = "fixture-funding"
		}
		var account ownedAccount
		if json.Unmarshal(accountPayload(runID, role, true), &account) != nil {
			t.Fatal("fixture")
		}
		account.ID = id
		account.Status = "ACTIVE"
		raw, _ := json.Marshal(account)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		var profile localRuntime
		if err := readPrivateJSON(localRuntimePath(manifest), &profile, 65536); err != nil {
			t.Error("owned cleanup profile unavailable")
			return
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := cleanupLocalDatabase(cleanupCtx, profile, adminFile); err != nil {
			t.Errorf("owned cleanup failed: %v", err)
		}
	})
	for range 2 {
		if err := prepareLocalDatabase(ctx, api, cfg, manifest, runID, adminFile, databaseName); err != nil {
			t.Fatalf("owned service setup: %v", err)
		}
	}
	for range 2 {
		if err := seedLocalBooks(ctx, api, cfg, manifest, runID, adminFile, databaseName); err != nil {
			t.Fatalf("owned book bootstrap: %v", err)
		}
	}

	var profile localRuntime
	if err := readPrivateJSON(localRuntimePath(manifest), &profile, 65536); err != nil {
		t.Fatal(err)
	}
	if profile.Users["operator"].ID == "" || profile.Users["operator"].ID == profile.Users["customer"].ID {
		t.Fatal("users not distinct")
	}
	gateway, err := pgxpool.New(ctx, profile.URLs["gateway"])
	if err != nil {
		t.Fatal("gateway runtime pool unavailable")
	}
	defer gateway.Close()
	var privileged bool
	if gateway.QueryRow(ctx, `SELECT has_database_privilege(current_user,current_database(),'CREATE,TEMP') OR has_schema_privilege(current_user,'public','CREATE') OR has_table_privilege(current_user,'browser_gateway.users','INSERT,UPDATE,DELETE')`).Scan(&privileged) != nil || privileged {
		t.Fatal("gateway role has provisioning or DDL rights")
	}
	if _, err := gateway.Exec(ctx, `CREATE TABLE browser_gateway.must_not_exist(id int)`); err == nil {
		t.Fatal("gateway performed DDL")
	}
	if _, err := gateway.Exec(ctx, `UPDATE browser_gateway.users SET active=false`); err == nil {
		t.Fatal("runtime modified invited users")
	}
	adminURL, err := localAdminURL(adminFile)
	if err != nil {
		t.Fatal(err)
	}
	adminURL.Path = "/" + databaseName
	admin, err := pgxpool.New(ctx, adminURL.String())
	if err != nil {
		t.Fatal("owned verification pool unavailable")
	}
	defer admin.Close()
	var actors int
	if admin.QueryRow(ctx, `SELECT count(*) FROM servicing.actors WHERE mode='SANDBOX'`).Scan(&actors) != nil || actors != 3 {
		t.Fatal("sandbox actors not seeded exactly once")
	}
	var count int
	if admin.QueryRow(ctx, `SELECT count(*) FROM browser_gateway.users`).Scan(&count) != nil || count != 2 {
		t.Fatal("repeated setup duplicated users")
	}
	var inventory bytes.Buffer
	if _, err := inspectLocal(ctx, manifest, runID, databaseName, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, value := range append([]string{profile.Marker, profile.Users["operator"].Password, profile.Users["customer"].Password}, profile.URLs["funding"], profile.URLs["gateway"]) {
		if strings.Contains(inventory.String(), value) {
			t.Fatal("inventory disclosed a private value")
		}
	}
	if _, err := inspectLocal(ctx, manifest, runID, "foreign_database", io.Discard); err == nil {
		t.Fatal("inventory accepted a different expected database")
	}
	if err := cleanupAcceptanceLocal(ctx, manifest, runID, adminFile, databaseName, io.Discard); err == nil || err.Error() != "sandbox_cleanup_refused_active_runtime" {
		t.Fatal("cleanup did not refuse active owned runtimes")
	}
	if gateway.QueryRow(ctx, `SELECT count(*) FROM browser_gateway.users`).Scan(&count) != nil || count != 2 {
		t.Fatal("cleanup interrupted an active runtime")
	}
	if _, err := admin.Exec(ctx, `WITH t AS (INSERT INTO servicing.ledger_transactions(mode,effect_key,transaction_kind,effective_at,provenance) VALUES('SANDBOX','fixture-audit','FIXTURE_AUDIT',clock_timestamp(),'{}') RETURNING mode,id) INSERT INTO servicing.ledger_lines(mode,transaction_id,line_number,account,debit_cents,credit_cents) SELECT mode,id,1,'FACILITY_CASH',1,0 FROM t UNION ALL SELECT mode,id,2,'FACILITY_CASH',0,1 FROM t`); err != nil {
		t.Fatal("fixture audit setup unavailable")
	}
	if err := cleanupAcceptanceLocal(ctx, manifest, runID, adminFile, databaseName, io.Discard); err == nil || err.Error() != "sandbox_cleanup_refused_financial_audit_retained" {
		t.Fatal("cleanup would erase audit evidence")
	}
	wrong := profile
	wrong.Marker = "flowcard-sandbox:" + runID + ":" + strings.Repeat("0", 64)
	if err := cleanupLocalDatabase(ctx, wrong, adminFile); err == nil {
		t.Fatal("cleanup accepted foreign marker")
	}
	if admin.QueryRow(ctx, `SELECT count(*) FROM browser_gateway.users`).Scan(&count) != nil || count != 2 {
		t.Fatal("marker-denied cleanup changed owned data")
	}
	t.Log("native migrations applied twice; five restricted logins and two distinct real users verified; runtime DDL/user-write denial and marker-gated cleanup verified")
}
