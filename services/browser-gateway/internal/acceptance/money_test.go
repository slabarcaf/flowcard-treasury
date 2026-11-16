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

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/cli"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/httpapi"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/postgres"
)

const moneyOperatorKey = "synthetic-operator-secret-00000000000000000000000000000000"
const moneyCustomerKey = "synthetic-customer-secret-00000000000000000000000000000000"
const moneyReaderKey = "synthetic-coordinator-secret-00000000000000000000000000000000"

type moneyFixture struct {
	URL, Operator, Customer, Other, Control string
	Case                                    json.RawMessage
}

func moneyHarness(t *testing.T, lifetime ...time.Duration) moneyFixture {
	t.Helper()
	dir := t.TempDir()
	control := filepath.Join(dir, "control")
	address := filepath.Join(dir, "address")
	if os.WriteFile(control, []byte("owned"), 0600) != nil {
		t.Fatal("owned money control failed")
	}
	root, _ := filepath.Abs("../../../..")
	source, _ := filepath.Abs("testdata/servicing_writes_test.go.txt")
	virtual := filepath.Join(root, "services/funding-collection/internal/testutil/service/rooty8_browser_money_test.go")
	raw, _ := json.Marshal(map[string]any{"Replace": map[string]string{virtual: source}})
	overlay := filepath.Join(dir, "overlay.json")
	if os.WriteFile(overlay, raw, 0600) != nil {
		t.Fatal("owned money overlay failed")
	}
	limit := 240 * time.Second
	if len(lifetime) == 1 && lifetime[0] >= 240*time.Second && lifetime[0] <= 15*time.Minute {
		limit = lifetime[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	command := exec.CommandContext(ctx, "go", "test", "-overlay", overlay, "./internal/testutil/service", "-run", "^TestOwnedBrowserMoneyHarness$", "-v", "-count=1", "-timeout="+(limit-10*time.Second).String())
	command.Dir = filepath.Join(root, "services/funding-collection")
	command.Env = append(os.Environ(), "FUNDING_TEST_ADMIN_URL_FILE="+os.Getenv("BROWSER_GATEWAY_TEST_ADMIN_URL_FILE"), "OWNED_MONEY_CONTROL="+control, "OWNED_MONEY_ADDRESS="+address, "OWNED_MONEY_LIFETIME="+(limit-20*time.Second).String())
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if command.Start() != nil {
		cancel()
		t.Fatal("owned money process failed")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = os.Remove(control)
		select {
		case err := <-done:
			if err != nil {
				t.Error("owned money harness failed")
			}
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, "owned final counts") || strings.Contains(line, "exact marked funding") {
					t.Log(line)
				}
			}
		case <-time.After(15 * time.Second):
			cancel()
			<-done
			t.Error("owned money harness cleanup timed out")
		}
		cancel()
	})
	deadline := time.Now().Add(80 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			done <- err
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, "rooty8_browser_money_test.go:") || strings.Contains(line, "imported and not used") {
					t.Log(line)
				}
			}
			t.Fatal("owned money harness exited before readiness")
		default:
		}
		if raw, err := os.ReadFile(address); err == nil {
			var ready struct {
				URL      string          `json:"url"`
				Operator string          `json:"operator_subject"`
				Customer string          `json:"customer_subject"`
				Other    string          `json:"other_subject"`
				Case     json.RawMessage `json:"case"`
			}
			if json.Unmarshal(raw, &ready) != nil || ready.URL == "" {
				t.Fatal("owned money readiness invalid")
			}
			return moneyFixture{ready.URL, ready.Operator, ready.Customer, ready.Other, control, ready.Case}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("owned money readiness deadline")
	return moneyFixture{}
}
func moneyControl(t *testing.T, f moneyFixture, kind string, amount int64, deposit string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"kind": kind, "amount_cents": amount, "deposit_id": deposit})
	if os.WriteFile(f.Control, raw, 0600) != nil {
		t.Fatal("owned fixture control failed")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(f.Control)
		if string(raw) == "owned" {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("owned fixture control acknowledgement failed")
}
func moneyJSON(t *testing.T, result result, statuses ...int) map[string]any {
	t.Helper()
	ok := false
	for _, status := range statuses {
		if result.status == status {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("command/read status=%d, want %v", result.status, statuses)
	}
	var body map[string]any
	if json.Unmarshal(result.body, &body) != nil {
		t.Fatal("command/read JSON invalid")
	}
	return body
}
func TestOwnedBoundCustomerMoney(t *testing.T) {
	e := owned(t)
	fixture := moneyHarness(t)
	coordinator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"case_id": readCaseID, "mode": "DEMO", "customer_id": "synthetic-customer", "assessment": map[string]any{"id": "synthetic-evaluation", "source_mode": "DEMO", "customer_id": "synthetic-customer", "funding_authorized": false, "requires_recheck": true}})
	}))
	defer coordinator.Close()
	cfg, _ := config.Parse("127.0.0.1:0", e.runtimeURL, origin, "true")
	var err error
	cfg, err = cfg.WithFinancial(coordinator.URL, coordinatorReadSecret, fixture.URL, moneyReaderKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = cfg.WithDemoWrites("DEMO", moneyOperatorKey, moneyCustomerKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ name, role, subject string }{{"money.operator", "OPERATOR", fixture.Operator}, {"money.customer", "CUSTOMER", fixture.Customer}, {"money.other", "CUSTOMER", fixture.Other}} {
		e.user(t, u.name, u.role)
		if err := cli.Bindings(context.Background(), []string{"create", "--username", u.name, "--subject", u.subject, "--database-name", e.name}, e.adminURL, cfg); err != nil {
			t.Fatalf("verified %s binding failed", u.role)
		}
	}
	e.user(t, "money.unlinked", "CUSTOMER")
	if err := cli.Bindings(context.Background(), []string{"create", "--username", "money.unlinked", "--subject", fixture.Operator, "--database-name", e.name}, e.adminURL, cfg); err == nil {
		t.Fatal("customer was bound to operator actor")
	}
	if err := cli.Bindings(context.Background(), []string{"create", "--username", "money.unlinked", "--subject", fixture.Customer, "--database-name", "wrong-target"}, e.adminURL, cfg); err == nil {
		t.Fatal("binding CLI accepted wrong database")
	}
	if _, err := e.admin.Exec(context.Background(), `UPDATE browser_gateway.servicing_bindings SET customer_id='changed' WHERE actor_type='CUSTOMER'`); err == nil {
		t.Fatal("verified immutable binding was changed")
	}
	for _, sql := range []string{`UPDATE browser_gateway.servicing_bindings SET active=false`, `DELETE FROM browser_gateway.financial_commands`, `UPDATE browser_gateway.financial_receipts SET status=200`} {
		if _, err := e.store.Pool.Exec(context.Background(), sql); err == nil {
			t.Fatal("runtime altered bindings or durable history")
		}
	}
	s := httptest.NewServer(httpapi.New(cfg, e.store))
	defer func() { s.Close() }()
	op, _ := login(t, s, "money.operator", "OPERATOR")
	customer, _ := login(t, s, "money.customer", "CUSTOMER")
	other, _ := login(t, s, "money.other", "CUSTOMER")
	unlinked, _ := login(t, s, "money.unlinked", "CUSTOMER")
	if body := moneyJSON(t, request(t, s, "GET", "/api/servicing/session", "", customer, nil), 200); body["linked"] != true || body["mode"] != "DEMO" {
		t.Fatal("bound customer status invalid")
	}
	if got := request(t, s, "GET", "/api/customer/offers", "", unlinked, nil); got.status != 403 {
		t.Fatal("unlinked customer got offers")
	}
	payload := `{"evaluation_id":"synthetic-evaluation","originating_case_id":"` + readCaseID + `"}`
	first := request(t, s, "POST", "/api/offers", payload, op, map[string]string{"Idempotency-Key": "owned-offer"})
	if first.status != 502 || first.header.Get("X-FlowCard-Command-ID") == "" {
		t.Fatal("lost upstream receipt not retained as unknown")
	}
	history := moneyJSON(t, request(t, s, "GET", "/api/financial/commands", "", op, nil), 200)
	if history["commands"].([]any)[0].(map[string]any)["state"] != "UNKNOWN" {
		t.Fatal("unknown command lost")
	}
	// Actual session, binding, canonical request and key survive gateway restart.
	s.Close()
	e.store.Pool.Close()
	e.store, err = postgres.Open(context.Background(), e.runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	s = httptest.NewServer(httpapi.New(cfg, e.store))
	repeated := request(t, s, "POST", "/api/offers", payload, op, map[string]string{"Idempotency-Key": "owned-offer"})
	offer := moneyJSON(t, repeated, 200, 201)
	id, ok := offer["id"].(string)
	if !ok || id == "" || offer["customer_id"] != "synthetic-customer" || offer["source_mode"] != "DEMO" {
		t.Fatal("recovered offer scope invalid")
	}
	if got := request(t, s, "POST", "/api/offers", strings.Replace(payload, "synthetic-evaluation", "changed-evaluation", 1), op, map[string]string{"Idempotency-Key": "owned-offer"}); got.status != 409 {
		t.Fatal("changed retry body accepted")
	}
	listed := moneyJSON(t, request(t, s, "GET", "/api/customer/offers", "", customer, nil), 200)
	if len(listed["offer_ids"].([]any)) != 1 || listed["offer_ids"].([]any)[0] != id {
		t.Fatal("verified customer offer index missing")
	}
	if listed := moneyJSON(t, request(t, s, "GET", "/api/customer/offers", "", other, nil), 200); len(listed["offer_ids"].([]any)) != 0 {
		t.Fatal("other customer saw foreign offer")
	}
	if got := request(t, s, "GET", "/api/offers/"+id, "", other, nil); got.status != 404 {
		t.Fatal("other customer read foreign offer")
	}
	terms := moneyJSON(t, request(t, s, "GET", "/api/offers/"+id, "", customer, nil), 200)
	hash := terms["terms_hash"].(string)
	accept, _ := json.Marshal(map[string]any{"terms_hash": hash, "expected_version": terms["version"]})
	for _, badBody := range []string{strings.TrimSuffix(string(accept), "}") + `,"actor_id":"forged"}`, strings.TrimSuffix(string(accept), "}") + `,"Expected_Version":7}`} {
		if got := request(t, s, "POST", "/api/offers/"+id+"/acceptances", badBody, customer, map[string]string{"Idempotency-Key": "body-rejected"}); got.status != 400 {
			t.Fatal("browser supplied authority/ambiguous body accepted")
		}
	}
	if got := request(t, s, "POST", "/api/offers/"+id+"/acceptances", string(accept), op, map[string]string{"Idempotency-Key": "wrong-role"}); got.status != 403 {
		t.Fatal("operator accepted as customer")
	}
	foreign := request(t, s, "POST", "/api/offers/"+id+"/acceptances", string(accept), other, map[string]string{"Idempotency-Key": "foreign-accept"})
	if foreign.status != 403 && foreign.status != 404 {
		t.Fatal("foreign customer acceptance accepted")
	}
	wrong, _ := json.Marshal(map[string]any{"terms_hash": strings.Repeat("0", 64), "expected_version": terms["version"]})
	bad := request(t, s, "POST", "/api/offers/"+id+"/acceptances", string(wrong), customer, map[string]string{"Idempotency-Key": "wrong-terms"})
	if bad.status < 400 {
		t.Fatal("wrong terms accepted")
	}
	moneyJSON(t, request(t, s, "POST", "/api/offers/"+id+"/acceptances", string(accept), customer, map[string]string{"Idempotency-Key": "owned-accept"}), 200, 201)
	terms = moneyJSON(t, request(t, s, "GET", "/api/offers/"+id, "", op, nil), 200)
	approve, _ := json.Marshal(map[string]any{"terms_hash": hash, "expected_version": terms["version"], "reason_code": "REVIEWED"})
	moneyJSON(t, request(t, s, "POST", "/api/offers/"+id+"/approvals", string(approve), op, map[string]string{"Idempotency-Key": "owned-approve"}), 200, 201)
	terms = moneyJSON(t, request(t, s, "GET", "/api/offers/"+id, "", op, nil), 200)
	fund, _ := json.Marshal(map[string]any{"expected_version": terms["version"]})
	receipt := moneyJSON(t, request(t, s, "POST", "/api/offers/"+id+"/fund", string(fund), op, map[string]string{"Idempotency-Key": "owned-fund"}), 200, 202)
	bridgeID, ok := receipt["bridge_id"].(string)
	if !ok || bridgeID == "" || receipt["status"] != "RESERVED" {
		t.Fatal("fund receipt did not preserve queued/reserved meaning")
	}
	moneyControl(t, fixture, "settle", 0, "")
	deadline := time.Now().Add(25 * time.Second)
	var bridge map[string]any
	for time.Now().Before(deadline) {
		bridge = moneyJSON(t, request(t, s, "GET", "/api/bridges/"+bridgeID, "", customer, nil), 200)
		if bridge["lifecycle"] == "ACTIVE" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if bridge["lifecycle"] != "ACTIVE" || bridge["principal_owed_cents"] != float64(700000) {
		t.Fatal("actual worker/adapter did not fund bridge")
	}
	// Only the mock provider's facts change. Normal collection review commands,
	// the actual worker and adapter must produce every allocation/debt movement.
	for _, deposit := range []struct {
		id     string
		amount int64
		fee    float64
		state  string
	}{{"owned-deposit-1", 10000, 9000, ""}, {"owned-deposit-2", 709000, 0, "REPAID"}} {
		time.Sleep(1100 * time.Millisecond)
		moneyControl(t, fixture, "deposit", deposit.amount, deposit.id)
		bridge = moneyJSON(t, request(t, s, "GET", "/api/bridges/"+bridgeID, "", op, nil), 200)
		review, _ := json.Marshal(map[string]any{"expected_version": bridge["version"], "reason_code": "PROVIDER_DEPOSIT_OBSERVED"})
		moneyJSON(t, request(t, s, "POST", "/api/bridges/"+bridgeID+"/collection-reviews", string(review), op, map[string]string{"Idempotency-Key": deposit.id + "-review"}), 200, 202)
		deadline = time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			bridge = moneyJSON(t, request(t, s, "GET", "/api/bridges/"+bridgeID, "", customer, nil), 200)
			if bridge["contract_fee_owed_cents"] == deposit.fee && (deposit.state == "" || bridge["lifecycle"] == deposit.state) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if bridge["contract_fee_owed_cents"] != deposit.fee || deposit.state != "" && bridge["lifecycle"] != deposit.state {
			t.Fatalf("normal collection did not reach expected state for %s", deposit.id)
		}
	}
	if bridge["principal_owed_cents"] != float64(0) || bridge["late_fee_owed_cents"] != float64(0) {
		t.Fatal("repaid bridge retained debt")
	}
	operationID, ok := receipt["operation_id"].(string)
	if !ok {
		t.Fatal("funding receipt operation missing")
	}
	operation := moneyJSON(t, request(t, s, "GET", "/api/operations/"+operationID, "", op, nil), 200)
	reconcile, _ := json.Marshal(map[string]any{"expected_version": operation["version"], "reason_code": "VERIFY_SETTLEMENT"})
	if got := request(t, s, "POST", "/api/operations/"+operationID+"/reconcile", string(reconcile), customer, map[string]string{"Idempotency-Key": "customer-reconcile"}); got.status != 403 {
		t.Fatal("customer reconciled operation")
	}
	if got := request(t, s, "POST", "/api/operations/"+operationID+"/reconcile", string(reconcile), op, map[string]string{"Idempotency-Key": "settled-reconcile"}); got.status != 422 {
		t.Fatal("settled operation incorrectly scheduled as held reconciliation")
	}
	historyPage := moneyJSON(t, request(t, s, "GET", "/api/bridges/"+bridgeID+"/collections", "", customer, nil), 200)
	deposits := map[string]bool{}
	for _, item := range historyPage["items"].([]any) {
		deposits[item.(map[string]any)["deposit_id"].(string)] = true
	}
	if len(deposits) != 2 || historyPage["bridge_version"] != bridge["version"] {
		t.Fatalf("history distinct deposits=%d items=%d; expected two deposits at current bridge version", len(deposits), len(historyPage["items"].([]any)))
	}
	moneyControl(t, fixture, "rebind", 0, "")
	if got := request(t, s, "GET", "/api/offers/"+id, "", customer, nil); got.status != 403 {
		t.Fatal("changed backend actor binding silently granted authority")
	}
	moneyControl(t, fixture, "restore", 0, "")
	if got := request(t, s, "POST", "/api/session/logout", "", customer, nil); got.status != 204 {
		t.Fatal("customer logout failed")
	}
	if got := request(t, s, "GET", "/api/customer/offers", "", customer, nil); got.status != 401 {
		t.Fatal("logged-out customer retained authority")
	}
	if err := cli.Users(context.Background(), []string{"disable", "--username", "money.operator", "--database-name", e.name}, e.adminURL, strings.NewReader("")); err != nil {
		t.Fatal("owned operator disable failed")
	}
	if got := request(t, s, "GET", "/api/financial/commands", "", op, nil); got.status != 401 {
		t.Fatal("disabled operator retained command access")
	}
	t.Log("real bindings/decisions, durable replay, role/hash/drift/disable denial and actual DEMO worker fee-first repayment closure verified")
}
