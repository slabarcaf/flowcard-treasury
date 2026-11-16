# Node webhook retirement

The source retirement is prepared in `feat/retire-node-webhooks`, based on
local develop `ae9eaf6`. The running compatibility service and its historical
queue remain untouched. Live callback/backlog acceptance is still pending.

[TOC]

## Source changes

Node returns explicit 410 at the retired callback, including its trailing-slash
form. It reads no body, writes no receipt, starts no forwarding worker and
provides no private redirect. The verifier, inbox writer, forwarder and their
exclusive tests are removed. Historical SQL migrations and delivery records
remain intact. Node onboarding proxy, operator session, docs and roster
administration remain available.

The compatibility configuration no longer consumes or requires Airwallex API
credentials. Existing runtime environments are not rewritten by source edits.
Hosted task secret injection and callback routing must be reviewed with their
actual deployment before rollout; no Terraform or AWS apply was performed.

## Go callback-only listener

The normal Go API retains its existing HMAC endpoint and protected internal JWT
routes. An explicit new launch mode narrows the public callback surface:

```sh
go -C services/company-onboarding run ./cmd/api -webhook-only -port 1234
```

Supply the intended restricted `DATABASE_URL`, paired
`ONBOARDING_WEBHOOK_SECRET` and `ONBOARDING_WEBHOOK_ENVIRONMENT` through the
approved server environment. Callback-only startup refuses missing database,
secret or environment. Normal schema readiness runs before a listener opens;
runtime never migrates. No new environment variable is introduced by this mode.

The restricted mode exposes only:

| Route | Behavior |
| --- | --- |
| `POST /webhooks/airwallex/onboarding` | Existing Go provider HMAC validation and durable inbox receipt |
| `POST /webhooks/airwallex` | Explicit 410 for the retired mixed-owner URL |
| `GET /healthz` | Liveness only |

Business, docs and internal JWT delivery paths return 404 in this mode.
The original request bytes/signature reach the existing provider handler.
Invalid signatures return 401 without storage. Durable duplicate/conflict,
worker lease fencing and restart handling remain owned by Go onboarding.

Use current native events for a new onboarding subscription:
`account.active`, `account.action_required`, `account.suspended`,
`account.submitted`, `rfi.action_required`, `rfi.answered` and `rfi.closed`.
Airwallex documents immutable event lists and URL-only subscription updates.
A separate onboarding-only subscription preserves other event owners.
Unverified RFI questions/correlation remain quarantined, with no invented
application transition or automatic compliance answer.

## Audit-first legacy transfer

Use the normal Go inbox writer to safely receive trusted historical Node
records. This administrative command does not forge an Airwallex signature or
claim a historical event is a new live provider delivery.

The source connection file must be a regular private file (0600), containing
only an explicitly approved legacy PostgreSQL connection URL. Symlinks and
public-readable files are refused. `DATABASE_URL` supplies the intended Go
connection. Both actual database names must match the mandatory flags before
any write. Do not copy root `.env` or print either URL.

```sh
go -C services/company-onboarding run ./cmd/legacy-webhooks \
  --source-database-url-file /private/path/approved-legacy-url.txt \
  --source-database-name EXPECTED_LEGACY_DATABASE \
  --database-name EXPECTED_GO_DATABASE \
  --environment sandbox
```

Without `--apply`, this audits only. It uses a repeatable-read, read-only source
transaction and makes no Go inbox changes. The report contains counts, source
high-water mark and a source digest, with no payloads, provider IDs or secrets.
Only `account.*`/`rfi.*` events qualify. Other event owners remain untouched.

Review the audit, quiesce old intake and use its exact hash to apply:

```sh
go -C services/company-onboarding run ./cmd/legacy-webhooks \
  --source-database-url-file /private/path/approved-legacy-url.txt \
  --source-database-name EXPECTED_LEGACY_DATABASE \
  --database-name EXPECTED_GO_DATABASE \
  --environment sandbox \
  --apply --expected-source-hash EXACT_AUDITED_HASH
```

Limits are 10,000 source events, 1 MiB per historical body, 64 MiB total and a
two-minute command lifetime. Missing/malformed bodies, mismatched event IDs,
environments or target receipts block completion. There is no destructive
fallback, automatic normalization of event identity, or legacy-row deletion.

Apply retains original body bytes and uses Go's existing duplicate/conflict
semantics. It verifies the source digest again and reconciles every target
receipt. A concurrent source change or storage failure can leave some Go
receipts committed; the report preserves the transferred count and fails
completion. Audit again, then resume idempotently with the newly reviewed hash.
Never treat that uncertainty as permission to recreate provider operations.

`all_durably_received=true` means all eligible receipts are held durably by Go.
It does not mean the account/RFI business processing succeeded. Existing Go
terminal results and quarantines remain preserved. Node forwarding statuses
are historical records and are deliberately not rewritten by this tool.

## Concrete cutover sequence

1. Identify the actual old and Go database targets and capture the source audit.
   Verify intended credentials and current schema without changing shared data.
2. Start the callback-only Go listener on a verified free port using the exact
   new subscription secret/sandbox scope. Preserve unrelated manual services.
3. Create the reviewed onboarding-only sandbox subscription. Verify its ID,
   URL, event list and version with a fresh GET. Store its returned secret
   privately. Preserve the existing financial/issuing subscription unchanged.
4. Prove an actual signed provider notification reaches the intended public
   route and commits a durable Go receipt. Verify invalid signature, duplicate
   and restart behavior separately. Synthetic HMAC tests alone do not establish
   subscription delivery. Preserve pending/unknown outcomes for reconciliation.
5. Quiesce old intake, audit and transfer historical receipts, then verify the
   source high-water/hash and every Go target receipt. Inspect quarantines.
6. Replace the old Node runtime with the checked retirement source only after
   callback/backlog acceptance. Preserve its other compatibility functions.

If cutover fails before acceptance, keep the old Node runtime and records.
Reconcile or remove only the newly created subscription; do not alter shared
financial subscriptions, operator accounts, provider financial objects or
historical records. Existing internal JWT Go delivery remains available for
transition, with its authentication intact.

## Current operational blocker

The root `.env` database URL points at localhost:5432 and did not permit a
successful read-only queue audit. The retained local runtime profile identifies
localhost:54329 and `flowcard_onboarding_test`, but automatic approval review
rejected credential access to that profile as outside the approved scope.
No queue was audited or transferred through that credential file.

The sandbox subscription inventory succeeded with the approved root API
credentials. Its single existing shared subscription has 17 issuing/financial
events, no onboarding events, version `2026-08-21` and an ngrok URL. The tunnel
forwards to localhost:1234. A new onboarding-only subscription payload and exact
local cutover/rollback proposal are saved privately under
`.dev/node-webhook-retirement/`. Neither has been applied.

Before live activation, explicitly resolve the retained runtime credential
scope and approve the reviewed sandbox callback payload/runtime changes.
Source integration alone is not a completed live retirement. It does not
perform a provider mutation, queue transfer, manual-service restart or cloud
deployment. The running system remains unchanged until cutover acceptance.

## Verification

- Focused compatibility tests pass after removing exclusive legacy tests.
- Actual owned PostgreSQL tests cover dry-run with zero writes, database-name
  refusal, changed-source refusal, source SELECT-only access, exact raw bytes,
  restart/idempotent resume, conflicts and malformed-history refusal.
- Direct Go HMAC/durable acceptance and callback-only route guards pass.
- Test resources use unique marker-owned databases/logins on existing 54329 and
  verify cleanup. No database is opened by package initialization or TestMain.
- The safe Node/frontend suite explicitly excludes the two legacy fixed-target
  suites that can mutate the shared manual database. They are not reported as
  passing database acceptance.
- Standard builds, vet, OpenAPI freshness, lint and formatting are recorded in
  the private implementation report. No dependencies or historical migrations
  were changed. No production API or AWS operation was performed.
