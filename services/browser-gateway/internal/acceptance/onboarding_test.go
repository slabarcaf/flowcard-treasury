package acceptance_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/httpapi"
)

const serviceSecret = "owned-test-service-assertion-secret-32-bytes"

func gateway(t *testing.T, e *environment, upstream string) *httptest.Server {
	t.Helper()
	c, err := config.Parse("127.0.0.1:0", e.runtimeURL, origin, "true")
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.WithOnboarding(upstream, serviceSecret)
	if err != nil {
		t.Fatal("owned upstream config rejected")
	}
	s := httptest.NewServer(httpapi.New(c, e.store))
	t.Cleanup(s.Close)
	return s
}

func TestOwnedOnboardingProxy(t *testing.T) {
	e := owned(t)
	e.user(t, "proxy.operator", "OPERATOR")
	e.user(t, "proxy.customer", "CUSTOMER")
	var mu sync.Mutex
	calls := 0
	seen := make(chan *http.Request, 10)
	bodies := make(chan string, 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		if r.URL.Query().Get("wait") == "true" {
			<-r.Context().Done()
			return
		}
		data, _ := io.ReadAll(r.Body)
		bodies <- string(data)
		seen <- r.Clone(context.Background())
		w.Header().Set("Set-Cookie", "evil=1")
		w.Header().Set("Location", "https://private.invalid/secret")
		w.Header().Set("WWW-Authenticate", "secret")
		w.Header().Set("X-Actor", "forged")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", "\"rev-3\"")
		if r.URL.Query().Get("redirect") == "true" {
			w.WriteHeader(302)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/documents/doc_owned") {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `attachment; filename="owned.pdf"`)
			io.WriteString(w, "owned document stream")
			return
		}
		w.WriteHeader(202)
		io.WriteString(w, `{"code":"OK","submission_id":"sub_owned"}`)
	}))
	defer upstream.Close()
	s := gateway(t, e, upstream.URL)
	op, identity := login(t, s, "proxy.operator", "OPERATOR")
	customer, _ := login(t, s, "proxy.customer", "CUSTOMER")
	if got := request(t, s, "GET", "/api/companies", "", customer, nil); got.status != 403 {
		t.Fatal("customer accessed upstream")
	}
	if got := request(t, s, "GET", "/api/companies", "", nil, nil); got.status != 401 {
		t.Fatal("anonymous accessed upstream")
	}
	for _, path := range []string{"/api/companies/../companies", "/api/companies/%2e%2e/companies", "/api/companies/a%2fb", "/api/companies/a%252fb", "/api//companies", "//api/companies"} {
		got := request(t, s, "GET", path, "", op, nil)
		if got.status != 400 {
			t.Errorf("path guard %q status=%d, want 400", path, got.status)
		}
	}
	if got := request(t, s, "POST", "/api/webhooks/airwallex", "{}", op, nil); got.status != 404 {
		t.Fatal("webhook route exposed")
	}
	mu.Lock()
	before := calls
	mu.Unlock()
	if before != 0 {
		t.Fatal("denied request reached upstream")
	}
	payload := `{"expected_revision":3}`
	got := request(t, s, "POST", "/api/onboarding-applications/app_owned/submit?filter=a%2Bb&filter=c", payload, op, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "stable-owned-key", "If-Match": "3", "Authorization": "Bearer browser-forged", "X-Actor": "forged", "X-Forwarded-For": "evil"})
	if got.status != 202 || !strings.Contains(string(got.body), "sub_owned") || got.header.Get("ETag") != "\"rev-3\"" {
		t.Fatal("upstream receipt/status headers not preserved")
	}
	for _, name := range []string{"Set-Cookie", "Location", "WWW-Authenticate", "X-Actor"} {
		if got.header.Get(name) != "" {
			t.Errorf("unsafe upstream header %s escaped", name)
		}
	}
	observed := <-seen
	if observed.URL.RawQuery != "filter=a%2Bb&filter=c" || <-bodies != payload || observed.Header.Get("Idempotency-Key") != "stable-owned-key" || observed.Header.Get("If-Match") != "3" {
		t.Fatal("request bytes/query/revision/key changed")
	}
	for _, name := range []string{"Cookie", "X-Actor", "X-Forwarded-For", "Origin"} {
		if observed.Header.Get(name) != "" {
			t.Errorf("browser identity header %s forwarded", name)
		}
	}
	token := strings.TrimPrefix(observed.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("missing service assertion")
	}
	mac := hmac.New(sha256.New, []byte(serviceSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if !hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("invalid service signature")
	}
	data, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Issuer   string `json:"iss"`
		Audience string `json:"aud"`
		Subject  string `json:"sub"`
		Issued   int64  `json:"iat"`
		Expiry   int64  `json:"exp"`
		ID       string `json:"jti"`
	}
	if json.Unmarshal(data, &claims) != nil || claims.Issuer != "flowcard-node-proxy" || claims.Audience != "flowcard-onboarding-service" || claims.Subject != identity.User.ID || claims.Expiry-claims.Issued > 30 || claims.Expiry <= claims.Issued || claims.ID == "" {
		t.Fatal("service identity claims invalid")
	}
	if got := request(t, s, "GET", "/api/companies?redirect=true", "", op, nil); got.status != 502 {
		t.Fatal("remote redirect accepted")
	}
	<-seen
	<-bodies
	multipart := "--owned\r\nContent-Disposition: form-data; name=\"file\"; filename=\"owned.pdf\"\r\nContent-Type: application/pdf\r\n\r\nowned bytes\r\n--owned--\r\n"
	if got := request(t, s, "POST", "/api/onboarding-applications/app_owned/documents", multipart, op, map[string]string{"Content-Type": "multipart/form-data; boundary=owned"}); got.status != 202 {
		t.Fatal("multipart proxy failed")
	}
	upload := <-seen
	if <-bodies != multipart || upload.Header.Get("Content-Type") != "multipart/form-data; boundary=owned" || upload.Header.Get("Authorization") == observed.Header.Get("Authorization") {
		t.Fatal("multipart bytes/type or fresh assertion contract failed")
	}
	if got := request(t, s, "GET", "/api/onboarding-applications/app_owned/documents/doc_owned", "", op, nil); got.status != 200 || string(got.body) != "owned document stream" || got.header.Get("Content-Type") != "application/pdf" || got.header.Get("Content-Disposition") == "" {
		t.Fatal("document stream metadata or bytes changed")
	}
	<-seen
	<-bodies
	if got := request(t, s, "POST", "/api/companies", strings.Repeat("x", 1024*1024+1), op, map[string]string{"Content-Type": "application/json"}); got.status != 413 {
		t.Fatal("JSON body limit not enforced")
	}
	if got := request(t, s, "POST", "/api/onboarding-applications/app_owned/documents", strings.Repeat("x", 15*1024*1024+1), op, map[string]string{"Content-Type": "multipart/form-data; boundary=owned"}); got.status != 413 {
		t.Fatal("multipart body limit not enforced")
	}
	// A bounded caller context triggers a controlled timeout without a retry.
	c, _ := config.Parse("127.0.0.1:0", e.runtimeURL, origin, "true")
	c, _ = c.WithOnboarding(upstream.URL, serviceSecret)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "http://gateway/api/companies?wait=true", nil).WithContext(ctx)
	r.Header.Set("Origin", origin)
	r.AddCookie(op)
	recorder := httptest.NewRecorder()
	httpapi.New(c, e.store).ServeHTTP(recorder, r)
	if recorder.Code != 504 {
		t.Errorf("upstream timeout=%d, want 504", recorder.Code)
	}
	mu.Lock()
	after := calls
	mu.Unlock()
	if after != 5 {
		t.Errorf("upstream calls=%d, want 5 without retries or oversized forwarding", after)
	}
	e.store.Pool.Close()
	if got := request(t, s, "GET", "/api/companies", "", op, nil); got.status != 503 {
		t.Fatal("proxy storage outage did not return 503")
	}
}

func onboardingHarness(t *testing.T, e *environment, lifetime ...time.Duration) string {
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
	source, err := filepath.Abs("testdata/onboarding_harness_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(temp, "overlay.json")
	virtual := filepath.Join(root, "services/company-onboarding/cmd/api/rooty8_gateway_owned_test.go")
	mapping, _ := json.Marshal(map[string]any{"Replace": map[string]string{virtual: source}})
	if err := os.WriteFile(overlay, mapping, 0600); err != nil {
		t.Fatal(err)
	}
	limit := 100 * time.Second
	if len(lifetime) == 1 && lifetime[0] >= 100*time.Second && lifetime[0] <= 15*time.Minute {
		limit = lifetime[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	command := exec.CommandContext(ctx, "go", "test", "-overlay", overlay, "./cmd/api", "-run", "^TestRooty8OwnedGatewayHarness$", "-count=1", "-timeout="+(limit-5*time.Second).String())
	command.Dir = filepath.Join(root, "services/company-onboarding")
	command.Env = append(os.Environ(), "OWNED_ONBOARDING_ADMIN_URL="+e.adminURL, "OWNED_ONBOARDING_RUNTIME_URL="+e.runtimeURL, "OWNED_ONBOARDING_ROLE="+e.role, "OWNED_ONBOARDING_DATABASE="+e.name, "OWNED_ONBOARDING_MARKER="+e.marker, "OWNED_ONBOARDING_SECRET="+serviceSecret, "OWNED_ONBOARDING_ADDRESS_FILE="+address, "OWNED_ONBOARDING_CONTROL_FILE="+control)
	command.Env = append(command.Env, "OWNED_ONBOARDING_HARNESS_LIFETIME="+(limit-10*time.Second).String())
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	// Subprocess output remains private: existing service errors may contain internals.
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal("owned onboarding harness start failed")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = os.Remove(control)
		select {
		case err := <-done:
			if err != nil {
				t.Error("owned onboarding harness failed")
			}
		case <-time.After(10 * time.Second):
			cancel()
			<-done
			t.Error("owned onboarding harness shutdown timed out")
		}
		cancel()
	})
	deadline := time.NewTimer(70 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			done <- err
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, "onboarding_harness") || strings.Contains(line, "rooty8_gateway_owned") || strings.Contains(line, "no required module") {
					t.Log(strings.NewReplacer(e.adminURL, "[private]", e.runtimeURL, "[private]", serviceSecret, "[private]").Replace(line))
				}
			}
			t.Fatal("owned onboarding harness exited before readiness")
		case <-deadline.C:
			t.Fatal("owned onboarding harness readiness timed out")
		case <-ticker.C:
			if data, err := os.ReadFile(address); err == nil {
				return string(data)
			}
		}
	}
}

func TestOwnedGoOnboardingJourney(t *testing.T) {
	authDB := owned(t)
	onboardingDB := owned(t)
	upstream := onboardingHarness(t, onboardingDB)
	authDB.user(t, "journey.operator", "OPERATOR")
	authDB.user(t, "journey.customer", "CUSTOMER")
	s := gateway(t, authDB, upstream)
	op, identity := login(t, s, "journey.operator", "OPERATOR")
	customer, _ := login(t, s, "journey.customer", "CUSTOMER")
	company := request(t, s, "POST", "/api/companies", `{"legal_name":"Owned Gateway Synthetic Company","registration_country":"US","entity_type":"LLC"}`, op, map[string]string{"Content-Type": "application/json"})
	if company.status != 201 {
		t.Fatalf("create company=%d, want 201", company.status)
	}
	var comp struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
		People   []struct {
			ID string `json:"person_id"`
		} `json:"people"`
	}
	if json.Unmarshal(company.body, &comp) != nil || comp.ID == "" {
		t.Fatal("company response invalid")
	}
	draft := request(t, s, "POST", "/api/onboarding-applications", `{"company_id":"`+comp.ID+`","platform_name":"AIRWALLEX","environment":"sandbox","onboarding_details":{}}`, op, map[string]string{"Content-Type": "application/json"})
	if draft.status != 201 {
		t.Fatalf("create draft=%d, want 201", draft.status)
	}
	var app struct {
		ID        string `json:"id"`
		Revision  int    `json:"revision"`
		CreatedBy string `json:"created_by"`
	}
	if json.Unmarshal(draft.body, &app) != nil || app.ID == "" {
		t.Fatal("draft response invalid")
	}
	patch, _ := json.Marshal(map[string]any{"expected_revision": comp.Revision, "people": []map[string]string{{"first_name": "Synthetic", "last_name": "Operator", "role": "Director", "date_of_birth": "1990-01-01"}}})
	updated := request(t, s, "PATCH", "/api/companies/"+comp.ID, string(patch), op, map[string]string{"Content-Type": "application/json"})
	if updated.status != 200 || json.Unmarshal(updated.body, &comp) != nil || len(comp.People) != 1 || comp.People[0].ID == "" {
		t.Fatalf("company person PATCH=%d, missing returned person ID", updated.status)
	}
	appPatch, _ := json.Marshal(map[string]any{"expected_revision": app.Revision, "onboarding_details": map[string]any{"primary_contact_person_id": comp.People[0].ID, "people": []map[string]any{{"person_id": comp.People[0].ID, "platform_roles": []string{"DIRECTOR"}}}}})
	result := request(t, s, "PATCH", "/api/onboarding-applications/"+app.ID, string(appPatch), op, map[string]string{"Content-Type": "application/json"})
	if result.status != 200 || !strings.Contains(string(result.body), comp.People[0].ID) {
		t.Fatalf("same draft person PATCH=%d", result.status)
	}
	for _, path := range []string{"/api/onboarding-applications/" + app.ID, "/api/onboarding-applications?company_id=" + comp.ID, "/api/companies/" + comp.ID} {
		if got := request(t, s, "GET", path, "", op, nil); got.status != 200 || !strings.Contains(string(got.body), comp.ID) {
			t.Fatalf("confirmation %s=%d", path, got.status)
		}
	}
	var creator string
	if err := onboardingDB.admin.QueryRow(context.Background(), `SELECT creator FROM onboarding.applications WHERE id=$1`, app.ID).Scan(&creator); err != nil {
		t.Fatal("owned creator lookup failed")
	}
	if creator != identity.User.ID {
		t.Errorf("persisted public creator UUID=%q, want %q", creator, identity.User.ID)
	}
	if got := request(t, s, "GET", "/api/companies", "", customer, nil); got.status != 403 {
		t.Fatal("customer accessed onboarding")
	}
	if got := request(t, s, "POST", "/api/session/logout", "", op, nil); got.status != 204 {
		t.Fatal("operator logout failed")
	}
	if got := request(t, s, "GET", "/api/companies", "", op, nil); got.status != 401 {
		t.Fatal("logged-out operator accessed onboarding")
	}
	fresh, _ := login(t, s, "journey.operator", "OPERATOR")
	if _, err := authDB.admin.Exec(context.Background(), `UPDATE browser_gateway.sessions SET created_at=created_at-interval '9 hours',expires_at=expires_at-interval '9 hours'`); err != nil {
		t.Fatal("owned session expiry fixture failed")
	}
	if got := request(t, s, "GET", "/api/companies", "", fresh, nil); got.status != 401 {
		t.Fatal("expired operator accessed onboarding")
	}
	t.Log("real production onboarding handler/repository/JWT, normal migrations, same draft and returned person ID verified; no worker/provider calls")
}
