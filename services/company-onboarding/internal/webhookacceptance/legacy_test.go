package webhookacceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/legacywebhooks"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
)

func seedLegacy(t *testing.T, e *ownedDatabase) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := e.admin.Exec(ctx, `CREATE TABLE public.webhook_inbox(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,provider_event_id text UNIQUE NOT NULL,event_type text NOT NULL,raw_body bytea NOT NULL);CREATE TABLE public.onboarding_webhook_forwarding(provider_event_id text REFERENCES public.webhook_inbox(provider_event_id),status text NOT NULL);`); err != nil {
		t.Fatal("owned legacy fixture unavailable")
	}
	if _, err := e.admin.Exec(ctx, "GRANT SELECT ON public.webhook_inbox,public.onboarding_webhook_forwarding TO "+pgx.Identifier{e.role}.Sanitize()); err != nil {
		t.Fatal("legacy reader grant failed")
	}
	for _, row := range []struct{ id, kind, status string }{{"fixture-account", "account.active", "queued"}, {"fixture-rfi", "rfi.created", "quarantined"}, {"fixture-other", "connected_account_transfer.settled", "forwarded"}} {
		raw, _ := json.Marshal(map[string]any{"id": row.id, "name": row.kind, "account_id": "fixture-account", "data": map[string]any{"id": "fixture-account"}})
		if _, err := e.admin.Exec(ctx, `INSERT INTO public.webhook_inbox(provider_event_id,event_type,raw_body) VALUES($1,$2,$3)`, row.id, row.kind, raw); err != nil {
			t.Fatal("legacy fixture receipt failed")
		}
		if _, err := e.admin.Exec(ctx, `INSERT INTO public.onboarding_webhook_forwarding VALUES($1,$2)`, row.id, row.status); err != nil {
			t.Fatal("legacy fixture state failed")
		}
	}
}

func TestLegacyTransferOwnsNoSourceWritesAndResumesDurableReceipts(t *testing.T) {
	e := openOwned(t)
	seedLegacy(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	options := legacywebhooks.Options{SourceDatabase: e.name, TargetDatabase: e.name, Environment: "sandbox"}
	audit, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, options)
	if err != nil || audit.Events != 2 || audit.Missing != 2 || audit.Transferred != 0 || audit.Complete {
		t.Fatalf("audit = %+v, err %v", audit, err)
	}
	var count int
	if e.runtime.QueryRow(ctx, `SELECT count(*) FROM onboarding.webhook_inbox`).Scan(&count) != nil || count != 0 {
		t.Fatal("audit wrote receipts")
	}
	wrong := options
	wrong.TargetDatabase = "wrong_database"
	wrong.Apply = true
	wrong.ExpectedSourceHash = audit.SourceHash
	if _, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, wrong); !errors.Is(err, legacywebhooks.ErrTarget) {
		t.Fatal("wrong database was not refused")
	}
	changed := options
	changed.Apply = true
	changed.ExpectedSourceHash = audit.SourceHash
	raw := []byte(`{"id":"fixture-third","name":"account.action_required","account_id":"fixture-account"}`)
	if _, err := e.admin.Exec(ctx, `INSERT INTO public.webhook_inbox(provider_event_id,event_type,raw_body) VALUES('fixture-third','account.action_required',$1)`, raw); err != nil {
		t.Fatal("changed-source fixture failed")
	}
	if _, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, changed); !errors.Is(err, legacywebhooks.ErrChanged) {
		t.Fatal("changed source was not refused")
	}
	if e.runtime.QueryRow(ctx, `SELECT count(*) FROM onboarding.webhook_inbox`).Scan(&count) != nil || count != 0 {
		t.Fatal("changed-source refusal wrote receipts")
	}
	audit, err = legacywebhooks.Transfer(ctx, e.runtime, e.runtime, options)
	if err != nil {
		t.Fatal(err)
	}
	options.Apply = true
	options.ExpectedSourceHash = audit.SourceHash
	applied, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, options)
	if err != nil || !applied.Complete || applied.Transferred != 3 || applied.Missing != 0 || applied.LegacyRowsChanged {
		t.Fatalf("apply = %+v, err %v", applied, err)
	}
	e.restart(t)
	repeated, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, options)
	if err != nil || !repeated.Complete || repeated.Transferred != 0 || repeated.Existing != 3 {
		t.Fatalf("restart repeat = %+v, err %v", repeated, err)
	}
	if e.runtime.QueryRow(ctx, `SELECT count(*) FROM onboarding.webhook_inbox g JOIN public.webhook_inbox n ON n.provider_event_id=g.event_id WHERE g.raw_body=n.raw_body AND g.raw_payload=convert_from(n.raw_body,'UTF8')::jsonb`).Scan(&count) != nil || count != 3 {
		t.Fatal("original receipt bytes were not preserved")
	}
	if e.runtime.QueryRow(ctx, `SELECT count(*) FROM public.onboarding_webhook_forwarding WHERE (provider_event_id='fixture-account' AND status='queued') OR (provider_event_id='fixture-rfi' AND status='quarantined') OR (provider_event_id='fixture-other' AND status='forwarded')`).Scan(&count) != nil || count != 3 {
		t.Fatal("source forwarding history changed")
	}
	if e.runtime.QueryRow(ctx, `SELECT count(*) FROM onboarding.webhook_inbox WHERE event_type='connected_account_transfer.settled'`).Scan(&count) != nil || count != 0 {
		t.Fatal("another event owner's receipt was imported")
	}
	if _, err := e.runtime.Exec(ctx, `DELETE FROM public.webhook_inbox`); err == nil {
		t.Fatal("legacy reader can delete source receipts")
	}
}

func TestLegacyTransferHoldsConflictingAndMalformedHistory(t *testing.T) {
	e := openOwned(t)
	seedLegacy(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := postgres.NewRepository(e.runtime)
	original := postgres.WebhookInboxItem{EventID: "fixture-account", EventType: "account.active", AccountID: "fixture-account", Environment: "sandbox", RawPayload: json.RawMessage(`{"id":"fixture-account","name":"account.active","account_id":"fixture-account","data":{"id":"different-account"}}`)}
	if _, err := repo.InsertWebhookOnce(ctx, original); err != nil {
		t.Fatal("conflict fixture unavailable")
	}
	options := legacywebhooks.Options{SourceDatabase: e.name, TargetDatabase: e.name, Environment: "sandbox"}
	audit, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, options)
	if !errors.Is(err, legacywebhooks.ErrEvidence) || audit.Conflicts != 1 || audit.Transferred != 0 || audit.Complete {
		t.Fatalf("conflict audit = %+v, err %v", audit, err)
	}
	if _, err := e.admin.Exec(ctx, `INSERT INTO public.webhook_inbox(provider_event_id,event_type,raw_body) VALUES('fixture-bad','rfi.created',$1)`, []byte(`{"id":"wrong-id","name":"rfi.created"}`)); err != nil {
		t.Fatal("malformed fixture unavailable")
	}
	audit, err = legacywebhooks.Transfer(ctx, e.runtime, e.runtime, options)
	if !errors.Is(err, legacywebhooks.ErrEvidence) || audit.Invalid != 1 || audit.Conflicts != 1 {
		t.Fatalf("invalid audit = %+v, err %v", audit, err)
	}
	options.Apply = true
	options.ExpectedSourceHash = audit.SourceHash
	if _, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, options); !errors.Is(err, legacywebhooks.ErrEvidence) {
		t.Fatal("apply accepted conflicting history")
	}
	var count int
	if e.runtime.QueryRow(ctx, `SELECT count(*) FROM onboarding.webhook_inbox`).Scan(&count) != nil || count != 1 {
		t.Fatal("blocked transfer changed target receipt count")
	}
}

func TestLegacyTransferRefusesForwardingWorkWithoutCanonicalBody(t *testing.T) {
	e := openOwned(t)
	seedLegacy(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := e.admin.Exec(ctx, `ALTER TABLE public.onboarding_webhook_forwarding DROP CONSTRAINT onboarding_webhook_forwarding_provider_event_id_fkey;INSERT INTO public.onboarding_webhook_forwarding VALUES('fixture-lost-body','quarantined')`); err != nil {
		t.Fatal("orphan fixture unavailable")
	}
	report, err := legacywebhooks.Transfer(ctx, e.runtime, e.runtime, legacywebhooks.Options{SourceDatabase: e.name, TargetDatabase: e.name, Environment: "sandbox"})
	if !errors.Is(err, legacywebhooks.ErrEvidence) || report.Complete || report.Transferred != 0 {
		t.Fatal("missing canonical body was treated as safely transferred")
	}
	var count int
	if e.runtime.QueryRow(ctx, `SELECT count(*) FROM onboarding.webhook_inbox`).Scan(&count) != nil || count != 0 {
		t.Fatal("orphan refusal changed target receipts")
	}
}
