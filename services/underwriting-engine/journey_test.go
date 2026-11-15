package underwriting_test

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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/evaluation"
	apihttp "github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/shortfall"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

type journeyManifest struct {
	RunID           string            `json:"run_id"`
	RunAnchor       time.Time         `json:"run_anchor"`
	DatabaseName    string            `json:"database_name"`
	SourceMode      string            `json:"source_mode"`
	FacilityID      string            `json:"facility_id"`
	FacilityFile    string            `json:"facility_file"`
	ShortfallsFile  string            `json:"shortfalls_file"`
	MappingSetupSQL string            `json:"mapping_setup_sql"`
	Scenarios       []journeyScenario `json:"scenarios"`
}

type journeyScenario struct {
	ID             string                `json:"scenario_id"`
	CustomerID     string                `json:"customer_id"`
	ShortfallID    string                `json:"shortfall_id"`
	TransactionID  string                `json:"transaction_id"`
	IdempotencyKey string                `json:"idempotency_key"`
	CustomerFile   string                `json:"customer_file"`
	ExpectedStatus string                `json:"expected_status"`
	TermDays       int                   `json:"term_days"`
	ExpectedRules  []journeyExpectedRule `json:"expected_rules"`
}

type journeyExpectedRule struct {
	RuleID string `json:"rule_id"`
	Status string `json:"status"`
}

type journeyShortfall struct {
	ScenarioID string                 `json:"scenario_id"`
	Response   domain.ShortfallRecord `json:"response"`
}

type journeyShortfallPack struct {
	Records []journeyShortfall `json:"records"`
}

type journeyCommandResult struct {
	ScenarioID       string                `json:"scenario_id"`
	EvaluationID     string                `json:"evaluation_id"`
	Status           string                `json:"status"`
	ReasonCodes      []string              `json:"reason_codes"`
	Rules            []journeyExpectedRule `json:"rules"`
	FirstHTTPStatus  int                   `json:"first_http_status"`
	ReplayHTTPStatus int                   `json:"same_key_replay_status"`
}

func TestUnderwritingJourney(t *testing.T) {
	adminURL := strings.TrimSpace(os.Getenv("UNDERWRITING_TEST_ADMIN_DATABASE_URL"))
	if adminURL == "" {
		t.Fatal("UNDERWRITING_TEST_ADMIN_DATABASE_URL must point to the existing isolated local test PostgreSQL")
	}
	adminParsed, err := url.Parse(adminURL)
	if err != nil || (adminParsed.Scheme != "postgres" && adminParsed.Scheme != "postgresql") {
		t.Fatal("UNDERWRITING_TEST_ADMIN_DATABASE_URL must be a PostgreSQL URL")
	}
	host := strings.Trim(adminParsed.Hostname(), "[]")
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("test PostgreSQL must use a local loopback listener")
		}
	}
	root := journeyRepositoryRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	nameSuffix, err := randomHex(8)
	if err != nil {
		t.Fatal("cannot generate task database identity")
	}
	databaseName := "flowcard_underwriting_demo_task_f_" + nameSuffix
	if err := runGoCommand(ctx, root, nil, "run", "./cmd/demo", "prepare", "--scenario", "all",
		"--as-of", "now", "--out", ".dev/demo", "--database-name", databaseName); err != nil {
		t.Fatal("actual cmd/demo prepare failed")
	}
	manifestPath := filepath.Join(root, ".dev", "demo", "manifest.json")
	bundleRoot := filepath.Dir(manifestPath)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil || len(manifestBytes) > 2_000_000 {
		t.Fatal("prepared DEMO manifest is unavailable")
	}
	var manifest journeyManifest
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.RunID == "" ||
		manifest.SourceMode != "DEMO" || manifest.DatabaseName == "" ||
		len(manifest.Scenarios) != 3 {
		t.Fatal("prepared DEMO manifest is invalid")
	}
	if _, err := os.Stat(filepath.Join(bundleRoot, filepath.FromSlash(manifest.MappingSetupSQL))); err != nil {
		t.Fatal("generated mapping SQL is unavailable")
	}

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal("cannot connect to isolated test PostgreSQL")
	}
	defer adminPool.Close()
	var adminUser string
	if err := adminPool.QueryRow(ctx, `SELECT current_user`).Scan(&adminUser); err != nil {
		t.Fatal("cannot identify isolated test PostgreSQL owner")
	}
	var serverVersion int
	if err := adminPool.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		t.Fatal("cannot identify isolated test PostgreSQL version")
	}
	t.Logf("isolated_postgres host=127.0.0.1 port=54329 version_num=%d", serverVersion)

	if manifest.DatabaseName != databaseName || !validJourneyDatabaseName(databaseName) {
		t.Fatalf("manifest database %q is outside the task-owned namespace", databaseName)
	}
	t.Logf("prepared_run=%s database=%s scenarios=%d", manifest.RunID,
		manifest.DatabaseName, len(manifest.Scenarios))
	roleSuffix, err := randomHex(8)
	if err != nil {
		t.Fatal("cannot create task ownership marker")
	}
	roleName := "underwriting_demo_runtime_task_f_" + roleSuffix
	marker := "UNDERWRITING-GO-TEST:" + roleSuffix
	composeProject := "flowcard-underwriting-task-f"
	composeFile := filepath.Join(root, "services", "underwriting-engine", "compose.yaml")
	runtimeEnvPath := filepath.Join(root, ".dev", "underwriting", "task-f-runtime-"+roleSuffix+".env")
	composeStarted := false
	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`,
		databaseName).Scan(&exists); err != nil || exists {
		t.Fatalf("task database %q already exists or cannot be checked; refusing reuse", databaseName)
	}
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`,
		roleName).Scan(&exists); err != nil || exists {
		t.Fatalf("task role %q already exists or cannot be checked; refusing reuse", roleName)
	}
	databaseIdentifier := pgx.Identifier{databaseName}.Sanitize()
	t.Logf("task_database=%s runtime_role=%s", databaseName, roleName)
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+databaseIdentifier); err != nil {
		t.Fatal("cannot create uniquely named task test database")
	}
	if _, err := adminPool.Exec(ctx, "COMMENT ON DATABASE "+databaseIdentifier+" IS '"+marker+"'"); err != nil {
		t.Fatal("cannot mark task test database ownership")
	}

	adminDatabaseURL := databaseURL(adminParsed, databaseName, "", "")
	var adminTargetPool *pgxpool.Pool
	var runtimePool *pgxpool.Pool
	var runtimeStore *postgres.SnapshotStore
	var adminStore *postgres.SnapshotStore
	var apiProcess *exec.Cmd
	var shortfallServer *httptest.Server
	cleanup := func() {
		stopJourneyProcess(t, apiProcess)
		if shortfallServer != nil {
			shortfallServer.Close()
		}
		if composeStarted {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
			_, cleanupErr := runDockerCommand(cleanupCtx, runtimeEnvPath, composeProject,
				composeFile, "down")
			cleanupCancel()
			if cleanupErr != nil {
				t.Errorf("task-owned Compose project cleanup failed: %v", cleanupErr)
			}
		}
		if adminStore != nil {
			adminStore.Close()
		}
		if runtimeStore != nil {
			runtimeStore.Close()
		}
		if runtimePool != nil {
			runtimePool.Close()
		}
		if adminTargetPool != nil {
			adminTargetPool.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		var owner, comment string
		if err := adminPool.QueryRow(cleanupCtx, `SELECT pg_get_userbyid(datdba),
			shobj_description(oid, 'pg_database') FROM pg_database WHERE datname=$1`,
			databaseName).Scan(&owner, &comment); err != nil || owner != adminUser || comment != marker {
			t.Errorf("refusing DB cleanup: task owner marker or database owner mismatch for %s", databaseName)
			return
		}
		var otherConnections int
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname=$1 AND pid <> pg_backend_pid()`, databaseName).Scan(&otherConnections); err != nil || otherConnections != 0 {
			t.Errorf("refusing DB cleanup: %d connections remain for task database %s", otherConnections, databaseName)
			return
		}
		if _, err := adminPool.Exec(cleanupCtx, "DROP DATABASE "+databaseIdentifier); err != nil {
			t.Errorf("could not drop verified task database %s", databaseName)
			return
		}
		var roleExists bool
		if err := adminPool.QueryRow(cleanupCtx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`,
			roleName).Scan(&roleExists); err != nil {
			t.Errorf("cannot inspect task role %s for cleanup", roleName)
			return
		}
		if !roleExists {
			return
		}
		var roleComment string
		if err := adminPool.QueryRow(cleanupCtx, `SELECT shobj_description(oid, 'pg_authid')
			FROM pg_roles WHERE rolname=$1`, roleName).Scan(&roleComment); err != nil || roleComment != marker {
			t.Errorf("refusing role cleanup: task ownership marker mismatch for %s", roleName)
			return
		}
		if _, err := adminPool.Exec(cleanupCtx, "REVOKE underwriting_runtime FROM "+pgx.Identifier{roleName}.Sanitize()); err != nil {
			t.Errorf("could not revoke verified task runtime membership for %s", roleName)
			return
		}
		var memberships int
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM pg_auth_members
			WHERE member=(SELECT oid FROM pg_roles WHERE rolname=$1)`, roleName).Scan(&memberships); err != nil || memberships != 0 {
			t.Errorf("refusing role cleanup: memberships remain for %s", roleName)
			return
		}
		if _, err := adminPool.Exec(cleanupCtx, "DROP ROLE "+pgx.Identifier{roleName}.Sanitize()); err != nil {
			t.Errorf("could not drop verified task role %s", roleName)
			return
		}
		t.Logf("cleanup task database=%s runtime_role=%s result=removed", databaseName, roleName)
	}
	defer cleanup()
	adminTargetPool, err = pgxpool.New(ctx, adminDatabaseURL)
	if err != nil {
		t.Fatal("cannot connect to task test database")
	}

	for _, path := range []string{
		filepath.Join(root, "server", "src", "db", "migrations", "002_customer_roster.sql"),
		filepath.Join(root, "server", "src", "db", "migrations", "008_remote_authorization.sql"),
	} {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read existing mapping DDL %s: %v", filepath.Base(path), err)
		}
		if _, err := adminTargetPool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply existing mapping DDL %s: %v", filepath.Base(path), err)
		}
	}
	var migrationDigest string
	for i := 0; i < 2; i++ {
		if err := runGoCommand(ctx, root, map[string]string{
			"UNDERWRITING_MIGRATION_DATABASE_URL": adminDatabaseURL,
		}, "run", "./cmd/migrate"); err != nil {
			t.Fatalf("actual cmd/migrate run %d failed", i+1)
		}
		var currentDigest string
		if err := adminTargetPool.QueryRow(ctx, `SELECT string_agg(version::text || ':' || name || ':' || checksum,
			'|' ORDER BY version) FROM underwriting.schema_migrations`).Scan(&currentDigest); err != nil || currentDigest == "" {
			t.Fatal("migrations did not record versioned checksums")
		}
		if i == 0 {
			migrationDigest = currentDigest
		} else if currentDigest != migrationDigest {
			t.Fatal("second migration run changed recorded checksums")
		}
	}
	t.Logf("migration_runs=2 checksums_stable=true checksum_manifest=%s", migrationDigest)

	generatedMapping, err := os.ReadFile(filepath.Join(bundleRoot, filepath.FromSlash(manifest.MappingSetupSQL)))
	if err != nil {
		t.Fatal("read generated mapping SQL:", err)
	}
	wrongDatabasePool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal("cannot connect wrong-target mapping guard probe")
	}
	_, guardErr := wrongDatabasePool.Exec(ctx, string(generatedMapping))
	wrongDatabasePool.Close()
	if guardErr == nil {
		t.Fatal("generated mapping SQL succeeded against the wrong database")
	}
	if _, err := adminTargetPool.Exec(ctx, string(generatedMapping)); err != nil {
		t.Fatal("generated mapping SQL failed against its captured database:", err)
	}

	runtimePassword, err := randomHex(24)
	if err != nil {
		t.Fatal("cannot generate task runtime password")
	}
	runtimeIdentifier := pgx.Identifier{roleName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE ROLE "+runtimeIdentifier+
		" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+
		runtimePassword+"'"); err != nil {
		t.Fatal("cannot create task runtime role")
	}
	if _, err := adminPool.Exec(ctx, "COMMENT ON ROLE "+runtimeIdentifier+" IS '"+marker+"'"); err != nil {
		t.Fatal("cannot mark task runtime role ownership")
	}
	if _, err := adminPool.Exec(ctx, "GRANT underwriting_runtime TO "+runtimeIdentifier); err != nil {
		t.Fatal("cannot grant restricted runtime role")
	}
	runtimeURL := databaseURL(adminParsed, databaseName, roleName, runtimePassword)
	runtimePool, err = pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal("cannot connect using restricted runtime role")
	}
	runtimeStore, err = postgres.ConnectSnapshotStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal("cannot construct runtime snapshot store")
	}
	if err := runtimeStore.Ready(ctx); err != nil {
		t.Fatal("restricted runtime identity failed readiness checks")
	}
	if _, err := runtimePool.Exec(ctx, `INSERT INTO public.customer_roster
		(customer_id,salesforce_account_id,airwallex_connected_account_id,enrolled_by,enrollment_reason)
		VALUES ('denied-test','denied-test','denied-test','test','must be denied')`); err == nil {
		t.Fatal("runtime role wrote to the customer roster")
	} else if postgresSQLState(err) != "42501" {
		t.Fatalf("runtime roster insert SQLSTATE = %s, want 42501", postgresSQLState(err))
	}
	if _, err := runtimePool.Exec(ctx, `UPDATE underwriting.schema_migrations SET name=name`); err == nil {
		t.Fatal("runtime role updated migration metadata")
	} else if postgresSQLState(err) != "42501" {
		t.Fatalf("runtime metadata update SQLSTATE = %s, want 42501", postgresSQLState(err))
	}
	t.Log("runtime_permission_checks roster_insert=42501 metadata_update=42501 ready=true")

	policyConfig, err := policy.LoadConfig()
	if err != nil {
		t.Fatal("load embedded policy:", err)
	}
	shortfallSecret, err := randomHex(32)
	if err != nil {
		t.Fatal("cannot generate shortfall secret")
	}
	serviceSecret, err := randomHex(32)
	if err != nil {
		t.Fatal("cannot generate task service secrets")
	}
	if err := requireFreeLoopbackPorts(8083); err != nil {
		t.Fatal("task API port unavailable:", err)
	}
	binaryDirectory := t.TempDir()
	apiBinary := filepath.Join(binaryDirectory, "underwriting-api")
	if err := buildJourneyBinary(ctx, root, apiBinary, "./cmd/api"); err != nil {
		t.Fatal("build task API command:", err)
	}
	if err := runGoCommand(ctx, root, nil, "run", "./cmd/demo", "prepare", "--scenario", "all",
		"--as-of", "now", "--out", ".dev/demo", "--database-name", databaseName); err != nil {
		t.Fatal("actual fresh cmd/demo prepare failed")
	}
	manifestPath = filepath.Join(root, ".dev", "demo", "manifest.json")
	bundleRoot, manifest = loadJourneyManifest(t, manifestPath)
	t.Logf("host_prepared_run=%s database=%s scenarios=%d", manifest.RunID,
		manifest.DatabaseName, len(manifest.Scenarios))
	if manifest.DatabaseName != databaseName {
		t.Fatalf("fresh manifest database name = %q, want %q", manifest.DatabaseName, databaseName)
	}
	freshMapping, err := os.ReadFile(filepath.Join(bundleRoot, filepath.FromSlash(manifest.MappingSetupSQL)))
	if err != nil {
		t.Fatal("read fresh generated mapping SQL:", err)
	}
	if _, err := adminTargetPool.Exec(ctx, string(freshMapping)); err != nil {
		t.Fatal("fresh generated mapping SQL failed against its captured database:", err)
	}
	shortfallPackPath := filepath.Join(bundleRoot, filepath.FromSlash(manifest.ShortfallsFile))
	shortfallData, err := os.ReadFile(shortfallPackPath)
	if err != nil {
		t.Fatal("read generated shortfall fixtures:", err)
	}
	var shortfallPack journeyShortfallPack
	if json.Unmarshal(shortfallData, &shortfallPack) != nil || len(shortfallPack.Records) != len(manifest.Scenarios) {
		t.Fatal("generated shortfall fixture pack is invalid")
	}
	shortfallRecords := make(map[string]domain.ShortfallRecord, len(shortfallPack.Records))
	for _, item := range shortfallPack.Records {
		shortfallRecords[item.Response.ID] = item.Response
	}
	shortfallServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RawQuery != "" ||
			!verifyJourneyAssertion(r.Header.Get("Authorization"), shortfallSecret,
				"flowcard-shortfall-service", "shortfall-reader", time.Now().UTC()) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		record, ok := shortfallRecords[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(record)
	}))
	runtimeConfig := map[string]string{
		"DATABASE_URL": runtimeURL, "UNDERWRITING_MODE": "DEMO",
		"UNDERWRITING_FACILITY_ID":    manifest.FacilityID,
		"UNDERWRITING_HTTP_ADDR":      "127.0.0.1:8083",
		"UNDERWRITING_SERVICE_SECRET": serviceSecret,
		"SHORTFALL_BASE_URL":          shortfallServer.URL,
		"SHORTFALL_SERVICE_SECRET":    shortfallSecret,
	}
	containerConfig := make(map[string]string, len(runtimeConfig)+1)
	for key, value := range runtimeConfig {
		containerConfig[key] = value
	}
	containerDatabaseURL, err := containerDatabaseURL(runtimeURL)
	if err != nil {
		t.Fatal("construct container database URL:", err)
	}
	containerConfig["UNDERWRITING_CONTAINER_DATABASE_URL"] = containerDatabaseURL
	containerConfig["SHORTFALL_BASE_URL"] = "http://127.0.0.1:8084"
	if err := writeJourneyRuntimeConfig(root, roleSuffix, containerConfig); err != nil {
		t.Fatal("write private task runtime config:", err)
	}
	apiProcess = exec.Command(apiBinary)
	apiProcess.Env = mergeJourneyEnv(os.Environ(), runtimeConfig)
	apiProcess.Stdout, apiProcess.Stderr = io.Discard, io.Discard
	if err := apiProcess.Start(); err != nil {
		t.Fatal("start task-owned underwriting API:", err)
	}
	if err := waitForAPIReady(ctx, "http://127.0.0.1:8083"); err != nil {
		t.Fatal("task API did not become ready:", err)
	}
	t.Log("host_api health=200 readiness=200")
	shortfallClient, err := shortfall.NewClient(shortfallServer.URL, shortfallSecret)
	if err != nil {
		t.Fatal("construct authenticated shortfall client:", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	endpoint := "http://127.0.0.1:8083/api/underwriting/evaluations"
	results := make(map[string]domain.EvaluationResponse, len(manifest.Scenarios))
	facilityImports := 0
	for _, scenario := range manifest.Scenarios {
		facilityFile := filepath.Join(bundleRoot, filepath.FromSlash(manifest.FacilityFile))
		facilityImport, err := importWithCLI(ctx, root, runtimeURL, manifest.FacilityID, "facility", facilityFile)
		if err != nil {
			t.Fatal("actual facility import CLI failed")
		}
		facilityImports++
		if facilityImports == 1 && facilityImport.Replayed || facilityImports > 1 && !facilityImport.Replayed {
			t.Fatalf("facility import replayed=%t on import %d", facilityImport.Replayed, facilityImports)
		}
		t.Logf("snapshot kind=facility id=%s version=%d replayed=%t",
			facilityImport.SnapshotID, facilityImport.Version, facilityImport.Replayed)
		customerFile := filepath.Join(bundleRoot, filepath.FromSlash(scenario.CustomerFile))
		customerImport, err := importWithCLI(ctx, root, runtimeURL, manifest.FacilityID, "customer", customerFile)
		if err != nil {
			t.Fatalf("actual customer import CLI failed for %s", scenario.ID)
		}
		if customerImport.Replayed {
			t.Fatalf("initial import of customer %s replayed unexpectedly", scenario.CustomerID)
		}
		t.Logf("snapshot kind=customer customer=%s id=%s version=%d replayed=%t",
			scenario.CustomerID, customerImport.SnapshotID,
			customerImport.Version, customerImport.Replayed)
		requestBody, _ := json.Marshal(domain.EvaluationRequest{ShortfallID: scenario.ShortfallID,
			TermDays: scenario.TermDays})
		firstBody, firstStatus := journeyAPIRequest(t, ctx, client, serviceSecret, endpoint,
			http.MethodPost, requestBody, scenario.IdempotencyKey)
		if firstStatus != http.StatusCreated {
			t.Fatalf("scenario %s first POST status = %d, want 201", scenario.ID, firstStatus)
		}
		var response domain.EvaluationResponse
		if json.Unmarshal(firstBody, &response) != nil || response.SourceMode != "DEMO" ||
			response.Status != scenario.ExpectedStatus || len(response.Rules) != len(scenario.ExpectedRules) {
			t.Fatalf("scenario %s API response does not match prepared expectation", scenario.ID)
		}
		for i, expected := range scenario.ExpectedRules {
			if response.Rules[i].RuleID != expected.RuleID || response.Rules[i].Status != expected.Status {
				t.Fatalf("scenario %s rule %s = %s, want %s", scenario.ID,
					response.Rules[i].RuleID, response.Rules[i].Status, expected.Status)
			}
		}
		t.Logf("scenario=%s id=%s mode=%s status=%s reasons=%v", scenario.ID,
			response.ID, response.SourceMode, response.Status, response.ReasonCodes)
		results[scenario.ID] = response

		detail, detailStatus := journeyAPIRequest(t, ctx, client, serviceSecret,
			"http://127.0.0.1:8083/api/underwriting/evaluations/"+response.ID, http.MethodGet, nil, "")
		if detailStatus != http.StatusOK || !sameJourneyJSON(firstBody, detail) {
			t.Fatalf("scenario %s saved detail differs from first response", scenario.ID)
		}
		replay, replayStatus := journeyAPIRequest(t, ctx, client, serviceSecret,
			endpoint, http.MethodPost, requestBody, scenario.IdempotencyKey)
		if replayStatus != http.StatusOK || !sameJourneyJSON(firstBody, replay) {
			t.Fatalf("scenario %s same-key replay changed the saved response", scenario.ID)
		}
		t.Logf("saved_key scenario=%s first_status=%d replay_status=%d unchanged=true",
			scenario.ID, firstStatus, replayStatus)
	}
	cliOutput, err := runGoOutput(ctx, root, runtimeConfig,
		"run", "./cmd/demo", "evaluate", "--manifest", manifestPath,
		"--api-url", "http://127.0.0.1:8083")
	if err != nil {
		t.Fatal("actual cmd/demo evaluate failed")
	}
	var cliSummary struct {
		SourceMode string `json:"source_mode"`
		RunID      string `json:"run_id"`
		Results    []struct {
			ScenarioID   string `json:"scenario_id"`
			EvaluationID string `json:"evaluation_id"`
			Status       string `json:"status"`
			ReplayStatus int    `json:"same_key_replay_status"`
		} `json:"results"`
	}
	if json.Unmarshal(cliOutput, &cliSummary) != nil || cliSummary.SourceMode != "DEMO" ||
		cliSummary.RunID != manifest.RunID || len(cliSummary.Results) != len(manifest.Scenarios) {
		t.Fatal("actual cmd/demo evaluate returned an invalid summary")
	}
	for _, result := range cliSummary.Results {
		if result.Status != manifestStatus(manifest, result.ScenarioID) || result.ReplayStatus != http.StatusOK {
			t.Fatalf("actual cmd/demo evaluate scenario %s status %s replay %d",
				result.ScenarioID, result.Status, result.ReplayStatus)
		}
		t.Logf("cmd/demo evaluate scenario=%s id=%s status=%s replay=%d",
			result.ScenarioID, result.EvaluationID, result.Status, result.ReplayStatus)
	}

	capacityScenario := manifest.Scenarios[1]
	capacityBody, _ := json.Marshal(domain.EvaluationRequest{ShortfallID: capacityScenario.ShortfallID,
		TermDays: capacityScenario.TermDays + 1})
	_, conflictStatus := journeyAPIRequest(t, ctx, client, serviceSecret, endpoint,
		http.MethodPost, capacityBody, capacityScenario.IdempotencyKey)
	if conflictStatus != http.StatusConflict {
		t.Fatalf("changed-body idempotency status = %d, want 409", conflictStatus)
	}
	t.Logf("changed_body_idempotency status=%d", conflictStatus)

	eligible := manifest.Scenarios[0]
	eligiblePath := filepath.Join(bundleRoot, filepath.FromSlash(eligible.CustomerFile))
	newSnapshotPath := filepath.Join(bundleRoot, "runs", manifest.RunID, "customer-eligible-revised.json")
	if err := makeNewerCustomerSnapshot(eligiblePath, newSnapshotPath, time.Now().UTC()); err != nil {
		t.Fatal("create revised customer snapshot:", err)
	}
	revisedImport, err := importWithCLI(ctx, root, runtimeURL, manifest.FacilityID, "customer", newSnapshotPath)
	if err != nil {
		t.Fatal("actual revised customer import CLI failed")
	}
	if revisedImport.Replayed || revisedImport.Version != 2 {
		t.Fatalf("revised snapshot version=%d replayed=%t, want version 2 and new row",
			revisedImport.Version, revisedImport.Replayed)
	}
	t.Logf("snapshot kind=customer customer=%s id=%s version=%d replayed=%t revised=true",
		eligible.CustomerID, revisedImport.SnapshotID, revisedImport.Version, revisedImport.Replayed)
	eligibleBody, _ := json.Marshal(domain.EvaluationRequest{ShortfallID: eligible.ShortfallID,
		TermDays: eligible.TermDays})
	afterSnapshot, afterSnapshotStatus := journeyAPIRequest(t, ctx, client, serviceSecret,
		endpoint, http.MethodPost, eligibleBody, eligible.IdempotencyKey)
	if afterSnapshotStatus != http.StatusOK || !sameJourneyJSON(afterSnapshot, mustMarshal(t, results[eligible.ID])) {
		t.Fatal("completed key changed after a newer customer snapshot")
	}
	t.Logf("saved_key_after_new_snapshot status=%d unchanged=true", afterSnapshotStatus)
	staleSnapshotPath := filepath.Join(bundleRoot, "runs", manifest.RunID, "customer-eligible-stale.json")
	if err := makeStaleCustomerSnapshot(eligiblePath, staleSnapshotPath, manifest.RunAnchor); err != nil {
		t.Fatal("create stale customer snapshot:", err)
	}
	if _, err := importWithCLI(ctx, root, runtimeURL, manifest.FacilityID, "customer", staleSnapshotPath); err == nil {
		t.Fatal("actual import CLI accepted an older capture as current")
	}
	var currentVersion int64
	if err := runtimePool.QueryRow(ctx, `SELECT version FROM underwriting.customer_snapshots
		WHERE source_mode='DEMO' AND customer_id=$1 ORDER BY version DESC LIMIT 1`,
		eligible.CustomerID).Scan(&currentVersion); err != nil || currentVersion != 2 {
		t.Fatalf("stale import left current snapshot version %d, want 2", currentVersion)
	}
	t.Logf("stale_capture_rejected=true current_customer_version=%d", currentVersion)

	concurrentKey := "task-f-concurrent:" + manifest.RunID
	concurrentBodies := make([][]byte, 4)
	for i := range concurrentBodies {
		concurrentBodies[i] = eligibleBody
	}
	barrier := make(chan struct{})
	type postResult struct {
		status int
		body   []byte
		err    error
	}
	concurrentResults := make(chan postResult, len(concurrentBodies))
	var wait sync.WaitGroup
	for range concurrentBodies {
		wait.Add(1)
		go func(body []byte) {
			defer wait.Done()
			<-barrier
			request, err := newJourneyRequest(ctx, serviceSecret, endpoint, http.MethodPost, body, concurrentKey)
			if err != nil {
				concurrentResults <- postResult{err: err}
				return
			}
			response, err := client.Do(request)
			if err != nil {
				concurrentResults <- postResult{err: err}
				return
			}
			defer response.Body.Close()
			data, err := io.ReadAll(io.LimitReader(response.Body, 4_000_001))
			concurrentResults <- postResult{status: response.StatusCode, body: data, err: err}
		}(eligibleBody)
	}
	close(barrier)
	wait.Wait()
	close(concurrentResults)
	created, replayed, firstConcurrentID := 0, 0, ""
	for result := range concurrentResults {
		if result.err != nil {
			t.Fatalf("concurrent evaluation request failed: %v", result.err)
		}
		var response domain.EvaluationResponse
		if json.Unmarshal(result.body, &response) != nil {
			t.Fatal("concurrent response is invalid")
		}
		switch result.status {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			replayed++
		default:
			t.Fatalf("concurrent response status = %d", result.status)
		}
		if firstConcurrentID == "" {
			firstConcurrentID = response.ID
		} else if response.ID != firstConcurrentID {
			t.Fatalf("concurrent key returned different result IDs %s and %s", firstConcurrentID, response.ID)
		}
	}
	if created != 1 || replayed != len(concurrentBodies)-1 {
		t.Fatalf("concurrent key outcomes = %d created, %d replayed, want one winner", created, replayed)
	}
	var concurrentRows int
	if err := runtimePool.QueryRow(ctx, `SELECT count(*) FROM underwriting.evaluations
		WHERE source_mode='DEMO' AND caller='underwriting-evaluator' AND idempotency_key=$1`,
		concurrentKey).Scan(&concurrentRows); err != nil || concurrentRows != 1 {
		t.Fatalf("concurrent key saved %d rows, want exactly one", concurrentRows)
	}
	t.Logf("concurrent_idempotency created=%d replayed=%d rows=%d result_id=%s",
		created, replayed, concurrentRows, firstConcurrentID)
	for i := 0; i < 25; i++ {
		key := fmt.Sprintf("task-f-page:%s:%02d", manifest.RunID, i)
		_, status := journeyAPIRequest(t, ctx, client, serviceSecret, endpoint,
			http.MethodPost, eligibleBody, key)
		if status != http.StatusCreated {
			t.Fatalf("pagination seed %d POST status = %d", i, status)
		}
	}
	globalPage := journeyList(t, ctx, client, serviceSecret, "http://127.0.0.1:8083/api/underwriting/evaluations")
	if globalPage.Code != http.StatusOK {
		t.Fatalf("global list status = %d", globalPage.Code)
	}
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	if json.Unmarshal(globalPage.Body.Bytes(), &page) != nil || len(page.Items) != 25 || page.NextCursor == nil {
		t.Fatalf("global list page has %d items and cursor %v, want 25 and cursor", len(page.Items), page.NextCursor)
	}
	t.Logf("global_cursor first_page_items=%d next_cursor=true mode=DEMO", len(page.Items))
	secondPage := journeyList(t, ctx, client, serviceSecret,
		"http://127.0.0.1:8083/api/underwriting/evaluations?cursor="+url.QueryEscape(*page.NextCursor))
	if secondPage.Code != http.StatusOK {
		t.Fatalf("global second page status = %d", secondPage.Code)
	}
	t.Logf("global_cursor second_page_status=%d", secondPage.Code)
	changedFilter := journeyList(t, ctx, client, serviceSecret,
		"http://127.0.0.1:8083/api/underwriting/evaluations?customer_id=acct_atlas001&cursor="+
			url.QueryEscape(*page.NextCursor))
	if changedFilter.Code != http.StatusBadRequest {
		t.Fatalf("cursor changed-filter status = %d, want 400", changedFilter.Code)
	}
	t.Logf("changed_cursor_filter status=%d", changedFilter.Code)
	filtered := journeyList(t, ctx, client, serviceSecret,
		"http://127.0.0.1:8083/api/underwriting/evaluations?customer_id=acct_bloom001")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list status = %d", filtered.Code)
	}
	t.Logf("filtered_cursor customer=acct_bloom001 status=%d", filtered.Code)

	adminStore, err = postgres.ConnectSnapshotStore(ctx, adminDatabaseURL)
	if err != nil {
		t.Fatal("cannot construct admin-role readiness probe")
	}
	adminEvaluator := &evaluation.Service{Mode: "DEMO", FacilityID: manifest.FacilityID,
		Policy: policyConfig, Store: adminStore, Shortfalls: shortfallClient, Now: time.Now}
	adminHandler := apihttp.NewServer(apihttp.ServerConfig{Mode: "DEMO", ServiceSecret: []byte(serviceSecret),
		PolicyReady: true, ShortfallClientConfigured: true, Store: adminStore,
		Evaluation: adminEvaluator, Now: time.Now}).Routes()
	readyResponse := httptest.NewRecorder()
	adminHandler.ServeHTTP(readyResponse, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("admin /readyz status = %d, want 503", readyResponse.Code)
	}
	adminList := serveJourneyRequest(t, adminHandler, serviceSecret,
		"/api/underwriting/evaluations", http.MethodGet, nil, "")
	if adminList.Code != http.StatusServiceUnavailable || !strings.Contains(adminList.Body.String(), "STORAGE_UNAVAILABLE") {
		t.Fatalf("admin protected list = %d %s", adminList.Code, adminList.Body.String())
	}
	t.Logf("privileged_runtime_guard ready=%d protected_list=%d error=STORAGE_UNAVAILABLE",
		readyResponse.Code, adminList.Code)
	adminStore.Close()
	adminStore = nil
	shortfallServer.Close()
	shortfallServer = nil
	stopJourneyProcess(t, apiProcess)
	apiProcess = nil
	if err := requireFreeLoopbackPorts(8083); err != nil {
		t.Fatal("host API did not release task port:", err)
	}
	if err := runGoCommand(ctx, root, nil, "run", "./cmd/demo", "prepare", "--scenario", "all",
		"--as-of", "now", "--out", ".dev/demo", "--database-name", databaseName); err != nil {
		t.Fatal("prepare fresh container run:", err)
	}
	manifestPath = filepath.Join(root, ".dev", "demo", "manifest.json")
	bundleRoot, manifest = loadJourneyManifest(t, manifestPath)
	t.Logf("container_prepared_run=%s database=%s scenarios=%d", manifest.RunID,
		manifest.DatabaseName, len(manifest.Scenarios))
	freshMapping, err = os.ReadFile(filepath.Join(bundleRoot, filepath.FromSlash(manifest.MappingSetupSQL)))
	if err != nil {
		t.Fatal("read container run mapping SQL:", err)
	}
	if _, err := adminTargetPool.Exec(ctx, string(freshMapping)); err != nil {
		t.Fatal("container run mapping SQL failed against its captured database:", err)
	}
	composeStatus, err := runDockerCommand(ctx, runtimeEnvPath, composeProject,
		composeFile, "ps", "--quiet")
	if err != nil || strings.TrimSpace(composeStatus) != "" {
		t.Fatal("task Compose project is occupied; refusing to alter it")
	}
	composeStarted = true
	if _, err := runDockerCommand(ctx, runtimeEnvPath, composeProject, composeFile,
		"up", "--detach", "--no-build"); err != nil {
		t.Fatal("start task-owned container journey:", err)
	}
	if err := waitForAPIReady(ctx, "http://127.0.0.1:8083"); err != nil {
		t.Fatal("container API did not become ready against the restricted DB:", err)
	}
	t.Log("container_api health=200 readiness=200 runtime=restricted")
	containerEvaluation, err := runGoOutput(ctx, root, runtimeConfig,
		"run", "./cmd/demo", "evaluate", "--manifest", manifestPath,
		"--api-url", "http://127.0.0.1:8083")
	if err != nil {
		t.Fatal("actual cmd/demo evaluate against container failed")
	}
	var containerSummary struct {
		SourceMode string                 `json:"source_mode"`
		RunID      string                 `json:"run_id"`
		Results    []journeyCommandResult `json:"results"`
	}
	if json.Unmarshal(containerEvaluation, &containerSummary) != nil ||
		containerSummary.SourceMode != "DEMO" || containerSummary.RunID != manifest.RunID ||
		len(containerSummary.Results) != len(manifest.Scenarios) {
		t.Fatal("container cmd/demo evaluate summary is invalid")
	}
	for _, actual := range containerSummary.Results {
		var expected *journeyScenario
		for i := range manifest.Scenarios {
			if manifest.Scenarios[i].ID == actual.ScenarioID {
				expected = &manifest.Scenarios[i]
				break
			}
		}
		if expected == nil || actual.Status != expected.ExpectedStatus ||
			actual.FirstHTTPStatus != http.StatusCreated || actual.ReplayHTTPStatus != http.StatusOK ||
			len(actual.Rules) != len(expected.ExpectedRules) {
			t.Fatalf("container scenario %s response does not match the prepared case", actual.ScenarioID)
		}
		for i, rule := range actual.Rules {
			if rule.RuleID != expected.ExpectedRules[i].RuleID ||
				rule.Status != expected.ExpectedRules[i].Status {
				t.Fatalf("container scenario %s rule %s = %s, want %s", actual.ScenarioID,
					rule.RuleID, rule.Status, expected.ExpectedRules[i].Status)
			}
		}
		t.Logf("container scenario=%s id=%s status=%s reasons=%v", actual.ScenarioID,
			actual.EvaluationID, actual.Status, actual.ReasonCodes)
	}
	containerLogs, err := runDockerCommand(ctx, runtimeEnvPath, composeProject,
		composeFile, "logs", "--no-color", "underwriting-demo")
	if err != nil || !strings.Contains(containerLogs, "source_mode=DEMO synthetic shortfall mock listening") ||
		!strings.Contains(containerLogs, "underwriting api started mode=DEMO") {
		t.Fatal("container logs do not confirm the fixture mock and API startup")
	}
}

func journeyRepositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot resolve repository root")
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "docs", "api", "underwriting-contract.md")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("repository root not found")
		}
		current = parent
	}
}

func validJourneyDatabaseName(value string) bool {
	const prefix = "flowcard_underwriting_demo_task_f_"
	if len(value) <= len(prefix) || len(value) > 63 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, char := range value[len(prefix):] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func databaseURL(original *url.URL, databaseName, username, password string) string {
	updated := *original
	updated.Path = "/" + databaseName
	updated.RawPath = ""
	if username != "" {
		updated.User = url.UserPassword(username, password)
	}
	return updated.String()
}

func randomHex(length int) (string, error) {
	data := make([]byte, length)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func runGoCommand(ctx context.Context, root string, variables map[string]string, args ...string) error {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = filepath.Join(root, "services", "underwriting-engine")
	command.Env = mergeJourneyEnv(os.Environ(), variables)
	return command.Run()
}

func runGoOutput(ctx context.Context, root string, variables map[string]string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = filepath.Join(root, "services", "underwriting-engine")
	command.Env = mergeJourneyEnv(os.Environ(), variables)
	return command.Output()
}

func runDockerCommand(ctx context.Context, envFile, project, composeFile string,
	args ...string) (string, error) {
	commandArgs := []string{"compose", "--project-name", project, "--env-file", envFile,
		"-f", composeFile}
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, "docker", commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker compose %s failed", args[0])
	}
	return string(output), nil
}

func buildJourneyBinary(ctx context.Context, root, output, packagePath string) error {
	return runGoCommand(ctx, root, nil, "build", "-o", output, packagePath)
}

func requireFreeLoopbackPorts(ports ...int) error {
	for _, port := range ports {
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return fmt.Errorf("127.0.0.1:%d is occupied", port)
		}
		if err := listener.Close(); err != nil {
			return fmt.Errorf("cannot release preflight listener on 127.0.0.1:%d", port)
		}
	}
	return nil
}

func waitForAPIReady(ctx context.Context, baseURL string) error {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		healthReady := journeyEndpointReady(client, baseURL+"/healthz")
		applicationReady := journeyEndpointReady(client, baseURL+"/readyz")
		if healthReady && applicationReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("API readiness timed out")
		case <-ticker.C:
		}
	}
}

func journeyEndpointReady(client *http.Client, endpoint string) bool {
	response, err := client.Get(endpoint)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
	return response.StatusCode == http.StatusOK
}

func stopJourneyProcess(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if command == nil || command.Process == nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	if err := command.Process.Signal(os.Interrupt); err != nil {
		_ = command.Process.Kill()
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("task-owned process did not stop cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		if err := <-done; err != nil {
			t.Errorf("task-owned process required forced stop: %v", err)
		}
	}
}

func writeJourneyRuntimeConfig(root, suffix string, values map[string]string) error {
	directory := filepath.Join(root, ".dev", "underwriting")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, "task-f-runtime-"+suffix+".env")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(file, "%s=%s\n", key, values[key]); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func containerDatabaseURL(runtimeURL string) (string, error) {
	parsed, err := url.Parse(runtimeURL)
	if err != nil || parsed.User == nil || parsed.Hostname() == "" {
		return "", errors.New("runtime database URL invalid")
	}
	if port := parsed.Port(); port != "" {
		parsed.Host = net.JoinHostPort("host.docker.internal", port)
	} else {
		parsed.Host = "host.docker.internal"
	}
	return parsed.String(), nil
}

func loadJourneyManifest(t *testing.T, path string) (string, journeyManifest) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 2_000_000 {
		t.Fatal("prepared DEMO manifest is unavailable")
	}
	var manifest journeyManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.RunID == "" || manifest.SourceMode != "DEMO" ||
		manifest.DatabaseName == "" || len(manifest.Scenarios) != 3 {
		t.Fatal("prepared DEMO manifest is invalid")
	}
	root := filepath.Dir(path)
	return root, manifest
}

func manifestStatus(manifest journeyManifest, scenarioID string) string {
	for _, scenario := range manifest.Scenarios {
		if scenario.ID == scenarioID {
			return scenario.ExpectedStatus
		}
	}
	return ""
}

func mergeJourneyEnv(source []string, updates map[string]string) []string {
	values := make(map[string]string, len(source)+len(updates))
	for _, entry := range source {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	for key, value := range updates {
		values[key] = value
	}
	merged := make([]string, 0, len(values))
	for key, value := range values {
		merged = append(merged, key+"="+value)
	}
	return merged
}

func importWithCLI(ctx context.Context, root, databaseURL, facilityID, kind, path string) (domain.ImportResult, error) {
	data, err := runGoOutput(ctx, root, map[string]string{
		"DATABASE_URL": databaseURL, "UNDERWRITING_MODE": "DEMO",
		"UNDERWRITING_FACILITY_ID": facilityID,
	}, "run", "./cmd/import-snapshot", "--kind", kind, "--file", path)
	if err != nil {
		return domain.ImportResult{}, err
	}
	var result domain.ImportResult
	if err := json.Unmarshal(data, &result); err != nil || result.SnapshotID == "" || result.Version < 1 {
		return domain.ImportResult{}, errors.New("snapshot import CLI response invalid")
	}
	return result, nil
}

func postgresSQLState(err error) string {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return pgError.Code
	}
	return "non_postgres_error"
}

func journeyAPIRequest(t *testing.T, ctx context.Context, client *http.Client, secret, endpoint,
	method string, body []byte, idempotencyKey string) ([]byte, int) {
	t.Helper()
	request, err := newJourneyRequest(ctx, secret, endpoint, method, body, idempotencyKey)
	if err != nil {
		t.Fatal("create authenticated API request:", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("underwriting API request failed:", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4_000_001))
	if err != nil || len(data) > 4_000_000 {
		t.Fatal("underwriting API response unavailable")
	}
	return data, response.StatusCode
}

func newJourneyRequest(ctx context.Context, secret, endpoint, method string, body []byte,
	idempotencyKey string) (*http.Request, error) {
	token, err := signJourneyAssertion(secret, "flowcard-underwriting-service",
		"underwriting-evaluator", time.Now().UTC())
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return request, nil
}

func signJourneyAssertion(secret, audience, subject string, now time.Time) (string, error) {
	identifier, err := randomHex(16)
	if err != nil {
		return "", err
	}
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{"iss": "flowcard-node-proxy", "aud": audience,
		"sub": subject, "iat": now.Unix(), "exp": now.Add(30 * time.Second).Unix(), "jti": identifier})
	if err != nil {
		return "", err
	}
	encoding := base64.RawURLEncoding
	unsigned := encoding.EncodeToString(header) + "." + encoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + encoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyJourneyAssertion(header, secret, audience, subject string, now time.Time) bool {
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(header, "Bearer "), ".")
	if len(parts) != 3 {
		return false
	}
	encoding := base64.RawURLEncoding.Strict()
	signature, err := encoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	claimsBytes, err := encoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		TokenID   string `json:"jti"`
	}
	return json.Unmarshal(claimsBytes, &claims) == nil && claims.Issuer == "flowcard-node-proxy" &&
		claims.Audience == audience && claims.Subject == subject && claims.TokenID != "" &&
		claims.IssuedAt <= now.Unix()+5 && claims.ExpiresAt >= now.Unix()-5 &&
		claims.ExpiresAt > claims.IssuedAt && claims.ExpiresAt-claims.IssuedAt <= 30
}

func journeyList(t *testing.T, ctx context.Context, client *http.Client, secret, endpoint string) *httptest.ResponseRecorder {
	t.Helper()
	request, err := newJourneyRequest(ctx, secret, endpoint, http.MethodGet, nil, "")
	if err != nil {
		t.Fatal("create list request:", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("list request failed:", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("read list response:", err)
	}
	recorder := httptest.NewRecorder()
	recorder.WriteHeader(response.StatusCode)
	_, _ = recorder.Write(data)
	return recorder
}

func serveJourneyRequest(t *testing.T, handler http.Handler, secret, path, method string,
	body []byte, key string) *httptest.ResponseRecorder {
	t.Helper()
	request, err := newJourneyRequest(context.Background(), secret, "http://underwriting.local"+path,
		method, body, key)
	if err != nil {
		t.Fatal("create admin probe request:", err)
	}
	request.URL = &url.URL{Path: path}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func sameJourneyJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftJSON, err := json.Marshal(leftValue)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(rightValue)
	return err == nil && bytes.Equal(leftJSON, rightJSON)
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal("marshal saved test response:", err)
	}
	return data
}

func makeNewerCustomerSnapshot(sourcePath, destination string, captured time.Time) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		return errors.New("customer snapshot invalid")
	}
	object["source_version"] = object["source_version"].(string) + "-revised"
	object["captured_at"] = captured.UTC().Format(time.RFC3339Nano)
	updated, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(destination, updated, 0o644)
}

func makeStaleCustomerSnapshot(sourcePath, destination string, captured time.Time) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		return errors.New("customer snapshot invalid")
	}
	object["source_version"] = object["source_version"].(string) + "-stale"
	object["captured_at"] = captured.UTC().Format(time.RFC3339Nano)
	updated, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(destination, updated, 0o644)
}
