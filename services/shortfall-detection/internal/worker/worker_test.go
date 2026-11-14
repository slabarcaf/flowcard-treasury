package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
	internalhttp "github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/provider"
	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/testutil"
)

const assertionSecret = "synthetic-shortfall-service-secret-32-bytes"

func readerToken(audience, subject string, expired bool) string {
	now := time.Now().Unix()
	if expired {
		now -= 120
	}
	encode := func(v any) string { data, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(data) }
	input := encode(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." + encode(map[string]any{
		"iss": "flowcard-node-proxy", "aud": audience, "sub": subject, "iat": now, "exp": now + 30, "jti": "synthetic-jti"})
	mac := hmac.New(sha256.New, []byte(assertionSecret))
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func startAPI(t *testing.T, db *testutil.Database, source string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(internalhttp.NewServer(db.Repo, internalhttp.ServerConfig{Environment: "test", Source: source,
		WebhookSecret: "synthetic-webhook-secret", ServiceSecret: assertionSecret, ProviderConfigured: true}).Routes())
	t.Cleanup(s.Close)
	return s
}

func postEnvelope(t *testing.T, server *httptest.Server, id string) []byte {
	t.Helper()
	raw := []byte(fmt.Sprintf(`{"id":%q,"name":"issuing.card_transaction.declined","version":"fixture-only-unverified","data":{}}`, id))
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte("synthetic-webhook-secret"))
	_, _ = mac.Write([]byte(ts))
	_, _ = mac.Write(raw)
	r, err := http.NewRequest("POST", server.URL+"/webhooks/airwallex/shortfalls", bytes.NewReader(raw))
	testutil.Check(t, "create signed request", err)
	r.Header.Set("x-timestamp", ts)
	r.Header.Set("x-signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := server.Client().Do(r)
	testutil.Check(t, "signed HTTP receipt", err)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("signed receipt status=%d, want 200", response.StatusCode)
	}
	return raw
}

func getAPI(t *testing.T, server *httptest.Server, path, token string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequest("GET", server.URL+path, nil)
	testutil.Check(t, "create read request", err)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := server.Client().Do(r)
	testutil.Check(t, "HTTP read", err)
	body, err := io.ReadAll(response.Body)
	testutil.Check(t, "read allowlisted response", err)
	_ = response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	return response.StatusCode, body
}

func step(t *testing.T, w *Worker) {
	t.Helper()
	claimed, err := w.ProcessNext(context.Background())
	testutil.Check(t, "process claimed item", err)
	if !claimed {
		t.Fatal("expected queued job")
	}
}

func newWorker(t *testing.T, db *testutil.Database, mock *testutil.Mock, source string) *Worker {
	t.Helper()
	w, err := New(db.Repo, testutil.Resolver(t, db.Repo), mock.Client, "test", source)
	testutil.Check(t, "configure injected harness worker", err)
	return w
}

func TestWorkerReadAPI(t *testing.T) {
	db := testutil.NewDatabase(t, "worker")
	ctx := context.Background()
	token := readerToken(internalhttp.AssertionAudience, internalhttp.AssertionSubject, false)
	t.Run("actual_runtime_permissions_and_checksum", func(t *testing.T) {
		var current string
		testutil.Check(t, "read runtime identity", db.RuntimeSQL.QueryRow(ctx, "SELECT current_user").Scan(&current))
		if current != db.Role {
			t.Fatal("test is not using restricted login")
		}
		for _, sql := range []string{
			"UPDATE public.customer_roster SET enrollment_reason='denied' WHERE false",
			"UPDATE public.issuing_card_bindings SET binding_reason='denied' WHERE false",
			"UPDATE shortfall.webhook_inbox SET raw_body='denied'::bytea WHERE false",
			"UPDATE shortfall.detections SET reason='denied' WHERE false",
			"CREATE TABLE shortfall.runtime_must_not_create (id integer)",
		} {
			_, err := db.RuntimeSQL.Exec(ctx, sql)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
				t.Fatal("restricted runtime mutation unexpectedly permitted")
			}
		}
		var checksum string
		testutil.Check(t, "read migration checksum", db.Admin.QueryRow(ctx, "SELECT checksum FROM shortfall.schema_migrations WHERE version=1").Scan(&checksum))
		_, err := db.Admin.Exec(ctx, "UPDATE shortfall.schema_migrations SET checksum='changed-for-drift-test'")
		testutil.Check(t, "set disposable drift fixture", err)
		if postgres.RunMigrations(ctx, db.Admin) == nil {
			t.Fatal("migration drift was accepted")
		}
		_, err = db.Admin.Exec(ctx, "UPDATE shortfall.schema_migrations SET checksum=$1", checksum)
		testutil.Check(t, "restore exact checksum", err)
		testutil.Check(t, "rerun unchanged migration", postgres.RunMigrations(ctx, db.Admin))
		t.Log("restricted SELECT works; five actual write/DDL operations denied with SQLSTATE 42501; migration twice and checksum drift verified")
	})
	mainAPI := startAPI(t, db, "main")
	mock := testutil.NewMock(t)
	w := newWorker(t, db, mock, "main")
	var mainID string
	var stored *domain.StoredObservation
	t.Run("signed_journey_and_duplicate_reuse", func(t *testing.T) {
		postEnvelope(t, mainAPI, "fixture-webhook-1")
		step(t, w)
		var err error
		stored, err = db.Repo.FindObservation(ctx, "test", "fixture-connected-account", "00000000-0000-4000-8000-000000000003")
		testutil.Check(t, "find final journey observation", err)
		if stored == nil || stored.Observation.ShortfallCents == nil || *stored.Observation.ShortfallCents != 700000 {
			t.Fatal("9000/2000 current need is not 700000 cents")
		}
		mainID = stored.ID
		postEnvelope(t, mainAPI, "fixture-webhook-1")
		claimed, err := w.ProcessNext(ctx)
		testutil.Check(t, "duplicate same ID poll", err)
		if claimed {
			t.Fatal("identical delivery queued completed work")
		}
		postEnvelope(t, mainAPI, "fixture-webhook-2")
		step(t, w)
		var detections, links int
		testutil.Check(t, "count canonical observations", db.Admin.QueryRow(ctx, "SELECT count(*) FROM shortfall.detections").Scan(&detections))
		testutil.Check(t, "count delivery evidence", db.Admin.QueryRow(ctx, "SELECT count(*) FROM shortfall.inbox_detections").Scan(&links))
		_, balances, _, bad := mock.Counts()
		if detections != 1 || links != 2 || balances != 1 || bad {
			t.Fatalf("dedup detections=%d links=%d balances=%d bad_scope=%t", detections, links, balances, bad)
		}
		status, data := getAPI(t, mainAPI, "/api/shortfalls/"+mainID, token)
		if status != 200 {
			t.Fatalf("final read status=%d", status)
		}
		var record domain.Record
		testutil.Check(t, "decode allowlisted final read", json.Unmarshal(data, &record))
		if record.ShortfallCents == nil || *record.ShortfallCents != 700000 || !record.RequiresRecheck || record.AmountSemantics != "current_need_at_balance_observation" {
			t.Fatal("authenticated final amount/semantics wrong")
		}
		for _, forbidden := range []string{"raw_body", "synthetic-token", "synthetic-key", "masked_card_number"} {
			if bytes.Contains(data, []byte(forbidden)) {
				t.Fatal("protected material in read response")
			}
		}
		t.Logf("JOURNEY detection=%s attempt=900000 available=200000 shortfall=700000 same/different-webhook detections=1 links=2 balance_calls=1", mainID)
	})
	if stored == nil {
		t.Fatal("main journey failed; dependent fixture unavailable")
	}
	t.Run("ordinary_runtime_and_honest_readiness", func(t *testing.T) {
		api := startAPI(t, db, "runtime")
		runtimeMock := testutil.NewMock(t)
		runtimeWorker, err := New(db.Repo, &provider.Resolver{Mappings: db.Repo, References: provider.UnsupportedReferences{}}, runtimeMock.Client, "test", "runtime")
		testutil.Check(t, "configure ordinary runtime", err)
		postEnvelope(t, api, "runtime-unsupported")
		step(t, runtimeWorker)
		var state, reason string
		testutil.Check(t, "inspect ordinary hold", db.Admin.QueryRow(ctx, "SELECT state,last_reason FROM shortfall.webhook_inbox WHERE source='runtime'").Scan(&state, &reason))
		if state != "HELD" || reason != "UNSUPPORTED_WEBHOOK_VERSION" {
			t.Fatal("ordinary runtime did not hold unsupported parser")
		}
		e, b, l, _ := runtimeMock.Counts()
		if e+b+l != 0 {
			t.Fatal("ordinary runtime called mock provider")
		}
		status, data := getAPI(t, api, "/readyz", "")
		if status != 503 {
			t.Fatal("unsupported runtime reported ready")
		}
		var flags map[string]any
		testutil.Check(t, "decode readiness", json.Unmarshal(data, &flags))
		if flags["live_detection_ready"] != false || flags["service_ready"] != true || flags["database_ready"] != true {
			t.Fatal("readiness conflates local and live readiness")
		}
		if status, _ := getAPI(t, api, "/healthz", ""); status != 200 {
			t.Fatal("configured liveness failed")
		}
		empty := httptest.NewServer(internalhttp.NewServer(nil, internalhttp.ServerConfig{}).Routes())
		defer empty.Close()
		if status, _ := getAPI(t, empty, "/healthz", ""); status != 200 {
			t.Fatal("liveness depends on DB/config")
		}
		if status, _ := getAPI(t, empty, "/readyz", ""); status != 503 {
			t.Fatal("unconfigured readiness accepted")
		}
		t.Log("ordinary runtime HELD/UNSUPPORTED_WEBHOOK_VERSION, provider_calls=0, readyz=503, independent healthz=200")
	})
	t.Run("corrected_unresolved_replay_and_restart", func(t *testing.T) {
		api := startAPI(t, db, "replay")
		m := testutil.NewMock(t)
		m.Mu.Lock()
		m.Events[0].ID = "00000000-0000-4000-8000-000000000007"
		m.BalanceBody = `[]`
		m.Mu.Unlock()
		recoverWorker := newWorker(t, db, m, "replay")
		postEnvelope(t, api, "recover-after-balance")
		step(t, recoverWorker)
		var id, reason, state string
		var rows int
		testutil.Check(t, "inspect held missing balance", db.Admin.QueryRow(ctx, "SELECT id::text,state,last_reason FROM shortfall.webhook_inbox WHERE source='replay'").Scan(&id, &state, &reason))
		testutil.Check(t, "count unresolved observations", db.Admin.QueryRow(ctx, "SELECT count(*) FROM shortfall.detections WHERE canonical_event_id='00000000-0000-4000-8000-000000000007'").Scan(&rows))
		if state != "HELD" || reason != "USD_CASH_BALANCE_UNAVAILABLE_OR_AMBIGUOUS" || rows != 0 {
			t.Fatal("unresolved work created immutable observation or lost reason")
		}
		m.Mu.Lock()
		m.BalanceBody = string(testutil.Fixture(t, "usd-cash-balance.json"))
		m.Mu.Unlock()
		testutil.Check(t, "replay corrected balance", db.Repo.Replay(ctx, id, "corrected synthetic USD cash entry"))
		// A new worker instance resumes the original durable item after correction.
		step(t, newWorker(t, db, m, "replay"))
		result, err := db.Repo.FindObservation(ctx, "test", "fixture-connected-account", "00000000-0000-4000-8000-000000000007")
		testutil.Check(t, "read replay observation", err)
		if result == nil || result.Observation.ShortfallCents == nil || *result.Observation.ShortfallCents != 700000 {
			t.Fatal("corrected replay failed to calculate")
		}
		var audit int
		var previous string
		testutil.Check(t, "inspect replay audit", db.Admin.QueryRow(ctx, "SELECT jsonb_array_length(replay_audit),replay_audit->0->>'previous_reason' FROM shortfall.webhook_inbox WHERE id=$1", id).Scan(&audit, &previous))
		if audit != 1 || previous != reason || db.Repo.Replay(ctx, id, "must refuse completed") == nil {
			t.Fatal("replay audit/refusal incorrect")
		}
		_, balances, _, _ := m.Counts()
		if balances != 2 {
			t.Fatalf("recovery balance calls=%d, want 2", balances)
		}
		t.Logf("REPLAY inbox=%s held=%s no_detection_before=true final=%s shortfall=700000 audit=1 balance_calls=2", id, reason, result.ID)
	})
	t.Run("stale_expired_conflict_and_uniqueness_fences", func(t *testing.T) {
		claim := func(source, id string) domain.Job {
			raw := []byte(fmt.Sprintf(`{"id":%q,"name":"unrelated"}`, id))
			testutil.Check(t, "receive fence fixture", db.Repo.Receive(ctx, domain.Delivery{Environment: "test", Source: source, WebhookID: id, EventName: "unrelated", RawBody: raw}))
			job, err := db.Repo.Claim(ctx, "test", source)
			testutil.Check(t, "claim fence fixture", err)
			if job == nil {
				t.Fatal("fence fixture not claimed")
			}
			return *job
		}
		job := claim("stale", "stale-claim")
		_, err := db.Admin.Exec(ctx, "UPDATE shortfall.webhook_inbox SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", job.ID)
		testutil.Check(t, "expire task lease", err)
		if _, err := db.Repo.Complete(ctx, job, &stored.Observation, ""); !errors.Is(err, postgres.ErrLeaseLost) {
			t.Fatal("expired token completed")
		}
		replacement, err := db.Repo.Claim(ctx, "test", "stale")
		testutil.Check(t, "reclaim expired task", err)
		if replacement == nil || replacement.LeaseToken == job.LeaseToken || replacement.FailedAttempts != 0 {
			t.Fatal("crash recovery token/count incorrect")
		}
		if _, err := db.Repo.Complete(ctx, job, &stored.Observation, ""); !errors.Is(err, postgres.ErrLeaseLost) {
			t.Fatal("replaced token completed")
		}
		_, err = db.Repo.Complete(ctx, *replacement, &stored.Observation, "")
		testutil.Check(t, "replacement completion", err)
		conflict := claim("conflict", "conflict-first")
		testutil.Check(t, "record conflicting receipt", db.Repo.Receive(ctx, domain.Delivery{Environment: "test", Source: "conflict", WebhookID: "conflict-first", EventName: "unrelated", RawBody: []byte(`{"id":"conflict-first","name":"unrelated","changed":true}`)}))
		if _, err := db.Repo.Complete(ctx, conflict, &stored.Observation, ""); !errors.Is(err, postgres.ErrLeaseLost) {
			t.Fatal("conflict-revoked token completed")
		}
		var links int
		testutil.Check(t, "inspect rolled back evidence", db.Admin.QueryRow(ctx, "SELECT count(*) FROM shortfall.inbox_detections WHERE inbox_id=$1", conflict.ID).Scan(&links))
		if links != 0 || db.Repo.Replay(ctx, conflict.ID, "must refuse conflict") == nil {
			t.Fatal("conflict link/replay safety failed")
		}
		completed := claim("complete-first", "complete-first")
		_, err = db.Repo.Complete(ctx, completed, &stored.Observation, "")
		testutil.Check(t, "commit before conflict", err)
		testutil.Check(t, "post-completion conflict", db.Repo.Receive(ctx, domain.Delivery{Environment: "test", Source: "complete-first", WebhookID: "complete-first", EventName: "unrelated", RawBody: []byte(`{"id":"complete-first","name":"unrelated","changed":true}`)}))
		var state string
		testutil.Check(t, "inspect preserved completion", db.Admin.QueryRow(ctx, "SELECT state FROM shortfall.webhook_inbox WHERE id=$1", completed.ID).Scan(&state))
		if state != "PROCESSED" {
			t.Fatal("post-completion conflict rewrote state")
		}
		first := claim("race", "race-1")
		second := claim("race", "race-2")
		facts := stored.Observation.Facts
		facts.CanonicalEventID = "00000000-0000-4000-8000-000000000008"
		one := domain.Observe(facts, 200000, time.Now(), time.Now())
		two := domain.Observe(facts, 300000, time.Now(), time.Now())
		type finish struct {
			id  string
			err error
		}
		results := make(chan finish, 2)
		var group sync.WaitGroup
		for _, pair := range []struct {
			job         domain.Job
			observation domain.Observation
		}{{first, one}, {second, two}} {
			group.Add(1)
			go func(job domain.Job, o domain.Observation) {
				defer group.Done()
				id, err := db.Repo.Complete(ctx, job, &o, "")
				results <- finish{id, err}
			}(pair.job, pair.observation)
		}
		group.Wait()
		close(results)
		winner := ""
		for result := range results {
			testutil.Check(t, "canonical race completion", result.err)
			if winner != "" && winner != result.id {
				t.Fatal("canonical race returned two observations")
			}
			winner = result.id
		}
		var count int
		testutil.Check(t, "count race observations", db.Admin.QueryRow(ctx, "SELECT count(*) FROM shortfall.detections WHERE canonical_event_id=$1", facts.CanonicalEventID).Scan(&count))
		if count != 1 {
			t.Fatal("canonical uniqueness race duplicated detection")
		}
		t.Logf("FENCES stale/expired/conflict rejected; completion-first preserved; canonical race winner=%s rows=1", winner)
	})
	t.Run("bounded_retry_exhaustion_cause_and_replay", func(t *testing.T) {
		api := startAPI(t, db, "exhaust")
		m := testutil.NewMock(t)
		m.Mu.Lock()
		m.EventStatus = 503
		m.RetryAfter = "72"
		m.Mu.Unlock()
		retryWorker := newWorker(t, db, m, "exhaust")
		postEnvelope(t, api, "exhaust-recover")
		var id, state, reason string
		var failed int
		var delay *float64
		for i := 1; i <= 8; i++ {
			step(t, retryWorker)
			testutil.Check(t, "inspect retry metadata", db.Admin.QueryRow(ctx, "SELECT id::text,state,last_reason,failed_attempts,EXTRACT(EPOCH FROM next_attempt_at-clock_timestamp())::float8 FROM shortfall.webhook_inbox WHERE source='exhaust'").Scan(&id, &state, &reason, &failed, &delay))
			if failed != i {
				t.Fatalf("failed attempts=%d, want %d", failed, i)
			}
			if i < 8 {
				if state != "RETRY_WAIT" || delay == nil || *delay < 70 {
					t.Fatal("Retry-After or retry state not persisted")
				}
				_, err := db.Admin.Exec(ctx, "UPDATE shortfall.webhook_inbox SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1", id)
				testutil.Check(t, "advance only disposable retry schedule", err)
			} else if state != "HELD" || reason != "RETRIES_EXHAUSTED:PROVIDER_TEMPORARILY_UNAVAILABLE" || delay != nil {
				t.Fatal("exhaustion lost controlled original cause")
			}
		}
		for _, attempt := range []int{1, 2, 3, 4, 5, 6, 7} {
			base := []time.Duration{5, 10, 20, 40, 80, 160, 300}[attempt-1] * time.Second
			if retryDelay(attempt, 0, 0) != time.Duration(float64(base)*.8) || retryDelay(attempt, 0, 1) != time.Duration(float64(base)*1.2) {
				t.Fatal("retry jitter bounds incorrect")
			}
		}
		testutil.Check(t, "replay exhausted original", db.Repo.Replay(ctx, id, "mock provider restored"))
		m.Mu.Lock()
		m.EventStatus = 0
		m.Events[0].ID = "00000000-0000-4000-8000-000000000009"
		m.Mu.Unlock()
		step(t, newWorker(t, db, m, "exhaust"))
		var previous string
		testutil.Check(t, "exhaustion audit cause", db.Admin.QueryRow(ctx, "SELECT replay_audit->0->>'previous_reason' FROM shortfall.webhook_inbox WHERE id=$1", id).Scan(&previous))
		if previous != reason {
			t.Fatal("replay lost exhaustion cause")
		}
		t.Logf("EXHAUSTION inbox=%s failures=8 reason=%s replay_recovered=true", id, reason)
	})
	t.Run("auth_pagination_scope_and_nulls", func(t *testing.T) {
		_, err := db.Admin.Exec(ctx, `INSERT INTO shortfall.detections
			(environment,account_id,canonical_event_id,customer_id,provider_facts,field_provenance,outcome,reason,created_at)
			SELECT 'test','fixture-page-account','page-'||i,'fixture-page','{}','{}','IGNORED','SYNTHETIC_UNSUPPORTED',now()
			FROM generate_series(1,26) i;
			INSERT INTO shortfall.detections
			(environment,account_id,canonical_event_id,customer_id,provider_facts,field_provenance,outcome,reason)
			VALUES ('other','foreign-account','foreign-event','fixture-page','{}','{}','IGNORED','SYNTHETIC_UNSUPPORTED');`)
		testutil.Check(t, "seed isolated pagination/null fixtures", err)
		for _, tc := range []struct {
			name, path, token string
			status            int
		}{
			{"missing_auth", "/api/shortfalls", "", 401},
			{"bad_auth", "/api/shortfalls", "bad", 401},
			{"expired", "/api/shortfalls", readerToken(internalhttp.AssertionAudience, internalhttp.AssertionSubject, true), 401},
			{"wrong_audience", "/api/shortfalls", readerToken("other-service", internalhttp.AssertionSubject, false), 403},
			{"wrong_subject", "/api/shortfalls", readerToken(internalhttp.AssertionAudience, "other-reader", false), 403},
			{"bad_cursor", "/api/shortfalls?cursor=bad", token, 400},
			{"unknown_query", "/api/shortfalls?limit=100", token, 400},
			{"duplicate_filter", "/api/shortfalls?customer_id=a&customer_id=b", token, 400},
			{"invalid_utf8", "/api/shortfalls?customer_id=%ff", token, 400},
			{"unresolved_filter", "/api/shortfalls?outcome=UNRESOLVED", token, 400},
			{"invalid_detail", "/api/shortfalls/no-uuid", token, 400},
			{"unknown_detail", "/api/shortfalls/00000000-0000-4000-8000-000000000099", token, 404},
		} {
			t.Run(tc.name, func(t *testing.T) {
				status, _ := getAPI(t, mainAPI, tc.path, tc.token)
				if status != tc.status {
					t.Fatalf("API status=%d, want %d", status, tc.status)
				}
			})
		}
		var page struct {
			Items []domain.Record `json:"items"`
			Next  *string         `json:"next_cursor"`
		}
		status, data := getAPI(t, mainAPI, "/api/shortfalls?customer_id=fixture-page&outcome=IGNORED", token)
		testutil.Check(t, "decode first page", json.Unmarshal(data, &page))
		if status != 200 || len(page.Items) != 25 || page.Next == nil {
			t.Fatal("first page size/cursor incorrect")
		}
		seen := map[string]bool{}
		for _, record := range page.Items {
			seen[record.ID] = true
			if record.ShortfallCents != nil || record.Currency != nil || !record.RequiresRecheck {
				t.Fatal("unavailable fields not null or recheck absent")
			}
		}
		firstID := page.Items[0].ID
		cursor := *page.Next
		status, data = getAPI(t, mainAPI, "/api/shortfalls?customer_id=fixture-page&outcome=IGNORED&cursor="+url.QueryEscape(cursor), token)
		testutil.Check(t, "decode second page", json.Unmarshal(data, &page))
		if status != 200 || len(page.Items) != 1 || page.Next != nil || seen[page.Items[0].ID] {
			t.Fatal("pagination repeated or leaked scope")
		}
		if status, _ := getAPI(t, mainAPI, "/api/shortfalls?customer_id=fixture-customer&outcome=IGNORED&cursor="+url.QueryEscape(cursor), token); status != 400 {
			t.Fatal("changed cursor filter accepted")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		testutil.Check(t, "decode cursor fixture", err)
		var changed map[string]any
		testutil.Check(t, "cursor fixture JSON", json.Unmarshal(decoded, &changed))
		changed["environment"] = "other"
		encoded, err := json.Marshal(changed)
		testutil.Check(t, "encode changed cursor", err)
		if status, _ := getAPI(t, mainAPI, "/api/shortfalls?customer_id=fixture-page&outcome=IGNORED&cursor="+base64.RawURLEncoding.EncodeToString(encoded), token); status != 400 {
			t.Fatal("foreign scope cursor accepted")
		}
		status, data = getAPI(t, mainAPI, "/api/shortfalls/"+firstID, token)
		var record map[string]any
		testutil.Check(t, "decode explicit nulls", json.Unmarshal(data, &record))
		if status != 200 {
			t.Fatal("ignored detail unavailable")
		}
		for _, key := range []string{"currency", "attempted_amount_cents", "available_balance_cents", "shortfall_cents", "balance_requested_at", "balance_observed_at"} {
			value, exists := record[key]
			if !exists || value != nil {
				t.Fatal("absent amount/time not explicit null")
			}
		}
		t.Log("API auth 401/403, invalid cursor/filter 400, unknown detail 404, pages 25+1, foreign scope excluded, explicit nulls verified")
	})
	var unresolved int
	testutil.Check(t, "final immutable unresolved count", db.Admin.QueryRow(ctx, "SELECT count(*) FROM shortfall.detections WHERE outcome='UNRESOLVED'").Scan(&unresolved))
	if unresolved != 0 {
		t.Fatal("runtime inserted unresolved observation")
	}
}
