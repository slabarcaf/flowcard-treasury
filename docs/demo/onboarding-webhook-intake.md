# Direct onboarding webhook intake

Local implementation is ready in `feat/go-onboarding-webhook-intake`, based
on develop `7ac6b92`. Source is uncommitted and unmerged. No callback routing,
provider subscription, existing process, manual database or root `.env` changed.

[TOC]

## Configuration and contract

Apply normal Go onboarding migrations with the existing privileged migration
command before starting the API/worker. Migration 007 adds original request
bytes and append-only conflict evidence; migrations 001–006 are unchanged.
Runtime does not run DDL.

| Setting | Meaning |
| --- | --- |
| `ONBOARDING_WEBHOOK_SECRET` | Exact notification-URL HMAC secret, private to the API process |
| `ONBOARDING_WEBHOOK_ENVIRONMENT` | `sandbox` or `production`, matching the worker/provider deployment |

Both must be configured together. Leaving both blank keeps intake disabled
with 503. The Node `AIRWALLEX_WEBHOOK_SECRET` and internal
`ONBOARDING_SERVICE_SECRET` do not silently enable this endpoint.
Reusing the internal service-secret value as the provider key is rejected.

`POST /webhooks/airwallex` on the **Go onboarding listener** verifies the exact
`x-timestamp` string plus original request bytes with HMAC-SHA256. Require
one timestamp/signature header, positive millisecond timestamp, at most 10 MiB
and a 15-second transport body-read deadline. Timestamps over five minutes
ahead are refused; old timestamps remain accepted under Ramil's policy.
These size/time tolerances are application choices.

Require body `id` and `name`, one JSON object and unique JSON keys. Do not
supply event identity through headers. Only `account.*` and `rfi.*` belong
here. The deployment owns environment scope; conflicting payload scope fails.
The wire/signature definition follows
[Airwallex's delivery contract](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events).

The generated `webhooks.json` documents this HMAC route separately from the
two existing internal JWT routes. The public route is not authenticated by
browser sessions or service JWTs. Existing Node/CloudFront routing still goes
to Node until an explicit cutover; its old limits remain until then.

## Receipt, conflict and recovery behavior

- 200 follows a committed original receipt or unchanged duplicate. Exact new
  raw bytes are retained alongside JSONB. Formatting-only changes do not
  create conflicts or replace the original bytes.
- Reusing an event ID with changed content commits conflict evidence and
  returns 409. Unfinished work becomes QUARANTINED and its lease is cleared
  atomically. A stale worker cannot finish it. Existing PROCESSED/IGNORED
  results remain preserved.
- Runtime can insert/read conflict evidence, but cannot update/delete it.
  A compatible old forwarded duplicate with absent environment does not
  overwrite stored scope or revive quarantined work.
- The existing worker polls durable pending/failed rows and reclaims expired
  leases. Recovery does not depend on an API-process nudge.
- Account changes still require the existing authoritative-read/state guards.
  Unverified RFI correlation stays quarantined; no questions or state changes
  are guessed. Actual provider subscription/version and account acceptance
  remain unverified by these synthetic tests.

## Local verification

Owned database acceptance is opt-in through a private admin-URL file pointing
to existing loopback PostgreSQL on port 54329:

```sh
ONBOARDING_WEBHOOK_TEST_ADMIN_URL_FILE=/private/path/local-admin-url.txt \
  go -C services/company-onboarding test -race \
  ./internal/webhookacceptance -count=1 -v
```

The tests create unique marker-owned logical databases and restricted LOGIN
roles, migrate twice, use real loopback HTTP and production handlers/repository,
then remove and verify their exact owned resources. The existing Node and
manual test database are not reset. Do not run the old fixed-target truncating
webhook suites against shared/manual data.

Final evidence:

- Signature/config/processor/API checks: 25 top-level tests, 36 subtests, race
  detection, zero failed/skipped scenarios.
- Owned HTTP/PostgreSQL: two race-enabled tests passed, both cleanup checks
  passed. Covered concurrent duplicate receipt, formatting-only replay,
  conflict deduplication, worker fencing, terminal-result preservation, API/
  pool restart, expired-lease recovery, RFI hold and actual storage-loss
  refusal.
  The account read was an explicit fixture client, not a live provider call.
- OpenAPI parity: 27 top-level tests, 37 subtests passed. Generation/check
  mode passes and provider signature auth is distinct from internal JWT auth.
- Full module build/vet pass. Changed Go files pass gofmt. The broader style
  helper found pre-existing formatting in `internal/worker/submission_test.go`;
  that unrelated file was left unchanged. Installed golangci-lint targets
  Go 1.26 and cannot load module Go 1.27.3; goimports/staticcheck are absent.

Logs: `/tmp/flowcard-onboarding-webhook-final-units.jsonl`,
`/tmp/flowcard-onboarding-webhook-final-owned.jsonl`,
`/tmp/flowcard-onboarding-webhook-spec-tests.jsonl` and
`/tmp/flowcard-onboarding-webhook-final-build-vet-spec.log`.

Earlier failure evidence is preserved. An overly broad baseline selector hit
old fixed-target tests; the explicit port-1 safety guard stopped them before
DB access. Final runs used exact unit selectors and the separate owned package.
The first owned recovery run exposed a missing company row-lock permission
in the test login. Matching its grants to the existing worker fixed that
fixture; its failed run also verified cleanup. No production guard was relaxed.

## Hosted cutover remains separate

1. Review/migrate the target database, configure the private secret/scope and
   verify the new Go receiver with the intended deployment settings.
2. Confirm subscription event ownership, version, account scope and raw-body
   routing. Switch only the onboarding callback, preserving shortfall and
   servicing ownership. Deployment/provider changes require their own scope.
3. Drain or safely transfer outstanding Node forwarding work. Retain event
   IDs, evidence and historical records; do not delete the Node queue.
4. Remove Node intake/forwarding only after delivery, restart and backlog
   acceptance. The internal JWT endpoint remains available during transition.

No commit, merge, provider/model request, cloud operation or persistent service
setup was performed in this implementation.
