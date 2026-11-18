package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	fundinghttp "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/http"
	servicefixture "github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/testutil/service"
)

func TestOwnedSandboxActorDelegationRequiresExplicitProfileAndCorrectRoleKey(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	h, err := servicefixture.New(t, now, 2000)
	if err != nil {
		t.Fatalf("owned fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := h.Pool.Exec(ctx, `INSERT INTO servicing.customers(mode,id,history_complete) VALUES('SANDBOX',$1,true)`, servicefixture.CustomerID); err != nil {
		t.Fatal("sandbox customer fixture unavailable")
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO servicing.actors(mode,actor_id,customer_id,actor_type,authentication_subject,active,source_ref) VALUES('SANDBOX',$1,$2,'CUSTOMER',$3,true,'fixture'),('SANDBOX',$4,NULL,'OPERATOR',$5,true,'fixture')`, servicefixture.CustomerActorID, servicefixture.CustomerID, servicefixture.CustomerSubject, servicefixture.OperatorActorID, servicefixture.OperatorSubject); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			t.Fatalf("sandbox actor fixture unavailable: code=%s constraint=%s", pgErr.Code, pgErr.ConstraintName)
		}
		t.Fatal("sandbox actor fixture unavailable")
	}
	cfg := h.Config
	cfg.Mode = "SANDBOX"
	cfg.ProviderMode = "AIRWALLEX_SANDBOX"
	cfg.ProviderBaseURL = "https://api.sandbox.airwallex.com"
	disabled := httptest.NewServer(fundinghttp.NewHandler(h.Pool, cfg))
	defer disabled.Close()
	h.API = disabled
	response, err := h.Request("GET", "/api/servicing/actor", servicefixture.CustomerSubject, servicefixture.CustomerSecret, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("sandbox actor enabled implicitly")
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO servicing.facilities(mode,id,funding_account_ref,currency,total_cents,reserve_bps)
		SELECT 'SANDBOX',id,funding_account_ref,currency,total_cents,reserve_bps FROM servicing.facilities WHERE mode='DEMO' AND id=$1`, cfg.FacilityID); err != nil {
		t.Fatal("sandbox facility fixture unavailable")
	}
	readConfig := cfg
	readConfig.SandboxReadDelegationEnabled = true
	readOnly := httptest.NewServer(fundinghttp.NewHandler(h.Pool, readConfig))
	defer readOnly.Close()
	h.API = readOnly
	for _, tc := range []struct {
		method, path, subject, key string
		status                     int
	}{
		{http.MethodGet, "/api/servicing/actor", servicefixture.OperatorSubject, servicefixture.OperatorSecret, 200},
		{http.MethodGet, "/api/servicing/portfolio", servicefixture.OperatorSubject, servicefixture.OperatorSecret, 200},
		{http.MethodGet, "/api/servicing/portfolio", "unknown-operator", servicefixture.OperatorSecret, 403},
		{http.MethodGet, "/api/servicing/actor", servicefixture.CustomerSubject, servicefixture.CustomerSecret, 401},
		{http.MethodPost, "/api/offers", servicefixture.OperatorSubject, servicefixture.OperatorSecret, 401},
	} {
		resp, err := h.Request(tc.method, tc.path, tc.subject, tc.key, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("read-only %s %s: status = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.status)
		}
	}
	cfg.SandboxDelegationEnabled = true
	enabled := httptest.NewServer(fundinghttp.NewHandler(h.Pool, cfg))
	defer enabled.Close()
	h.API = enabled
	for _, tc := range []struct {
		subject, key, role string
		status             int
	}{
		{servicefixture.CustomerSubject, servicefixture.CustomerSecret, "CUSTOMER", 200},
		{servicefixture.OperatorSubject, servicefixture.OperatorSecret, "OPERATOR", 200},
		{servicefixture.CustomerSubject, servicefixture.OperatorSecret, "", 403},
		{servicefixture.OperatorSubject, servicefixture.CustomerSecret, "", 403},
	} {
		response, err := h.Request("GET", "/api/servicing/actor", tc.subject, tc.key, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Role    string `json:"actor_type"`
			Mode    string `json:"mode"`
			Sandbox bool   `json:"sandbox_delegation"`
			Fixture bool   `json:"fixture_only"`
		}
		err = json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("role-key boundary returned %d, want %d", response.StatusCode, tc.status)
		}
		if tc.status == 200 && (err != nil || body.Role != tc.role || body.Mode != "SANDBOX" || !body.Sandbox || body.Fixture) {
			t.Fatal("incorrect sandbox delegation profile")
		}
	}
	request, err := http.NewRequestWithContext(ctx, "GET", enabled.URL+"/api/servicing/actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Actor-ID", servicefixture.OperatorActorID)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("browser-controlled identity supplied authority")
	}
}
