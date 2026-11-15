package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/httpapi"
)

const coordinatorReadSecret = "owned-coordinator-reader-key-for-fixture-only"
const servicingReadSecret = "owned-servicing-reader-key-for-fixture-only"
const readCaseID = "11111111-1111-4111-8111-111111111111"
const readOfferID = "22222222-2222-4222-8222-222222222222"
const readBridgeID = "33333333-3333-4333-8333-333333333333"

func readHarness(t *testing.T, service string, e *environment, lifetime ...time.Duration) string {
	t.Helper()
	temp := t.TempDir()
	control := filepath.Join(temp, "running")
	address := filepath.Join(temp, "address")
	if err := os.WriteFile(control, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	module, packagePath, sourceName, secret := "go-coordinator", "internal/httpapi", "coordinator_reads_test.go.txt", coordinatorReadSecret
	if service == "servicing" {
		module, packagePath, sourceName, secret = "funding-collection", "internal/testutil/database", "servicing_reads_test.go.txt", servicingReadSecret
	}
	source, err := filepath.Abs("testdata/" + sourceName)
	if err != nil {
		t.Fatal(err)
	}
	virtual := filepath.Join(root, "services", module, packagePath, "rooty8_financial_owned_test.go")
	mapping, _ := json.Marshal(map[string]any{"Replace": map[string]string{virtual: source}})
	overlay := filepath.Join(temp, "overlay.json")
	if err := os.WriteFile(overlay, mapping, 0600); err != nil {
		t.Fatal(err)
	}
	limit := 110 * time.Second
	if len(lifetime) == 1 && lifetime[0] >= 110*time.Second && lifetime[0] <= 15*time.Minute {
		limit = lifetime[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	command := exec.CommandContext(ctx, "go", "test", "-overlay", overlay, "./"+packagePath, "-run", "^TestOwnedBrowserFinancialReads$", "-count=1", "-timeout="+(limit-5*time.Second).String(), "-v")
	command.Dir = filepath.Join(root, "services", module)
	command.Env = append(os.Environ(), "OWNED_READ_SECRET="+secret, "OWNED_READ_ADDRESS_FILE="+address, "OWNED_READ_CONTROL_FILE="+control, "FUNDING_TEST_ADMIN_URL_FILE="+os.Getenv("BROWSER_GATEWAY_TEST_ADMIN_URL_FILE"))
	command.Env = append(command.Env, "OWNED_READ_LIFETIME="+(limit-10*time.Second).String())
	if e != nil {
		command.Env = append(command.Env, "OWNED_READ_ADMIN_URL="+e.adminURL, "OWNED_READ_RUNTIME_URL="+e.runtimeURL, "OWNED_READ_ROLE="+e.role, "OWNED_READ_DATABASE="+e.name, "OWNED_READ_MARKER="+e.marker)
	}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal("owned financial reader harness failed to start")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = os.Remove(control)
		select {
		case err := <-done:
			if err != nil {
				t.Error("owned financial reader harness failed")
			}
			if service == "servicing" && strings.Contains(output.String(), "exact owned servicing database and login removed after marker checks") {
				t.Log("exact owned servicing database and login removed after marker checks")
			}
		case <-time.After(12 * time.Second):
			cancel()
			<-done
			t.Error("owned financial reader harness cleanup timed out")
		}
		cancel()
	})
	timer := time.NewTimer(80 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			done <- err
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, "rooty8_financial_owned_test.go:") {
					t.Log(line)
				}
			}
			t.Fatal("owned financial read harness exited before readiness")
		case <-timer.C:
			t.Fatal("owned financial read harness readiness timed out")
		case <-ticker.C:
			if raw, err := os.ReadFile(address); err == nil {
				return string(raw)
			}
		}
	}
}

func TestOwnedFinancialReads(t *testing.T) {
	authDB := owned(t)
	coordinatorDB := owned(t)
	coordinator := readHarness(t, "coordinator", coordinatorDB)
	servicing := readHarness(t, "servicing", nil)
	authDB.user(t, "financial.operator", "OPERATOR")
	authDB.user(t, "financial.customer", "CUSTOMER")
	cfg, err := config.Parse("127.0.0.1:0", authDB.runtimeURL, origin, "true")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = cfg.WithFinancial(coordinator, coordinatorReadSecret, servicing, servicingReadSecret)
	if err != nil {
		t.Fatal("financial reader config rejected")
	}
	s := httptest.NewServer(httpapi.New(cfg, authDB.store))
	defer s.Close()
	op, _ := login(t, s, "financial.operator", "OPERATOR")
	customer, _ := login(t, s, "financial.customer", "CUSTOMER")
	for _, path := range []string{"/api/coordinator/cases", "/api/coordinator/cases/" + readCaseID, "/api/servicing/changes?after_seq=0", "/api/offers/" + readOfferID, "/api/bridges/" + readBridgeID, "/api/bridges/" + readBridgeID + "/collections"} {
		got := request(t, s, "GET", path, "", op, map[string]string{"Authorization": "Bearer browser-forged", "X-Actor": "forged", "X-Forwarded-User": "forged"})
		if got.status != 200 {
			t.Errorf("real read %s status=%d, want 200", path, got.status)
			continue
		}
		if strings.Contains(string(got.body), "private-owned-") || strings.Contains(string(got.body), "account_id") {
			t.Error("private financial identity escaped")
		}
		var body map[string]any
		if json.Unmarshal(got.body, &body) != nil {
			t.Fatal("financial read returned malformed JSON")
		}
		switch path {
		case "/api/coordinator/cases/" + readCaseID:
			assessment, ok := body["assessment"].(map[string]any)
			if !ok || assessment["id"] != "owned-evaluation" || assessment["funding_authorized"] != false || assessment["requires_recheck"] != true || assessment["available_capital_cents"] != nil {
				t.Fatal("saved assessment or unavailable facts changed")
			}
		case "/api/offers/" + readOfferID:
			if body["version"] != float64(1) || body["offer_revision"] != float64(1) || body["total_due_cents"] != float64(719000) {
				t.Fatal("saved offer cents/version changed")
			}
		case "/api/bridges/" + readBridgeID:
			if body["lifecycle"] != "VOID_UNFUNDED" || body["principal_owed_cents"] != float64(0) || body["version"] != float64(1) {
				t.Fatal("explicit unfunded read fixture changed")
			}
		case "/api/bridges/" + readBridgeID + "/collections":
			if body["bridge_version"] != float64(1) || len(body["items"].([]any)) != 0 {
				t.Fatal("collection history version changed")
			}
		}
	}
	for _, path := range []string{"/api/coordinator/cases", "/api/servicing/changes?after_seq=0", "/api/bridges/" + readBridgeID} {
		if got := request(t, s, "GET", path, "", customer, nil); got.status != 403 {
			t.Errorf("customer read %s=%d", path, got.status)
		}
		if got := request(t, s, "GET", path, "", nil, nil); got.status != 401 {
			t.Errorf("anonymous read %s=%d", path, got.status)
		}
	}
	for _, path := range []string{"/api/offers", "/api/offers/" + readOfferID + "/fund", "/api/offers/" + readOfferID + "/acceptances", "/api/offers/" + readOfferID + "/approvals", "/api/bridges/" + readBridgeID + "/collection-reviews", "/api/operations/" + readOfferID + "/reconcile"} {
		got := request(t, s, "POST", path, `{}`, op, nil)
		if got.status != 404 && got.status != 405 {
			t.Errorf("financial command %s unexpectedly routed: %d", path, got.status)
		}
	}
	if got := request(t, s, "GET", "/api/offers/44444444-4444-4444-8444-444444444444", "", op, nil); got.status != 404 {
		t.Fatal("missing offer changed into a fabricated record")
	}
	badCfg, _ := cfg.WithFinancial(coordinator, servicingReadSecret, servicing, servicingReadSecret)
	bad := httptest.NewServer(httpapi.New(badCfg, authDB.store))
	defer bad.Close()
	if got := request(t, bad, "GET", "/api/coordinator/cases", "", op, nil); got.status != 503 || len(got.cookies) != 0 {
		t.Fatal("upstream reader-key failure confused with browser credentials")
	}
	missingCfg, _ := cfg.WithFinancial("", "", "", "")
	missing := httptest.NewServer(httpapi.New(missingCfg, authDB.store))
	defer missing.Close()
	if got := request(t, missing, "GET", "/api/coordinator/cases", "", op, nil); got.status != 503 {
		t.Fatal("unconfigured source fabricated empty results")
	}
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2*1024*1024+1)))
	}))
	defer oversized.Close()
	oversizedCfg, _ := cfg.WithFinancial(oversized.URL, coordinatorReadSecret, servicing, servicingReadSecret)
	bounded := httptest.NewServer(httpapi.New(oversizedCfg, authDB.store))
	defer bounded.Close()
	if got := request(t, bounded, "GET", "/api/coordinator/cases", "", op, nil); got.status != 502 {
		t.Fatal("oversized financial response was not bounded")
	}
	t.Log("actual Coordinator/Funding JWT readers and repositories verified on synthetic saved-read fixtures; no financial commands/worker/provider/model")
}
