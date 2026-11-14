# Shortfall detection local runbook

Run the isolated shortfall API/worker without changing the existing stack.
The runtime holds modern declines because their live parser contract is
unverified. Group G verified the injected fixture journey and separate ordinary
runtime behavior on isolated PostgreSQL; live compatibility remains unverified.

[TOC]

## Boundaries and prerequisites

- Use this worktree, Go 1.27.3, Node 20.19+ or 22.12+, and PostgreSQL 16+.
  Existing dependency versions are reused. Docker Compose is optional.
- Keep existing services/listeners running. Use a disposable logical database
  on the existing PostgreSQL instance, never truncate/reset the manual database.
- All credentials/IDs below are synthetic local examples. Do not copy the
  ignored root `.env`, print credentials, or use live provider keys here.
- The privileged migration connection creates schema/role/grants explicitly.
  API, worker, and replay use a distinct restricted login. Runtime never DDLs.
- Existing `customer_roster` and `issuing_card_bindings` must exist before the
  service migration. No live mapping data is copied or enrolled by the service.

## Provision a disposable database

Choose unused names. These example commands intentionally fail when names
already exist; do not reset existing objects to make them pass. Configure
privileged `PGHOST`, `PGPORT`, `PGUSER`, and `PGPASSWORD` in your shell through
your local credential mechanism, without printing their values.

```bash
PGDATABASE=postgres psql -X -v ON_ERROR_STOP=1 <<'SQL'
CREATE DATABASE shortfall_demo_20261114;
CREATE ROLE shortfall_demo_20261114 LOGIN
  PASSWORD 'synthetic-shortfall-db-password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
SQL
```

Set `SHORTFALL_ADMIN_DEMO_URL` to your privileged connection targeting exactly
the newly created `shortfall_demo_20261114` database. Apply existing Node
migrations there to create the shared mapping schema, then shortfall migration:

```bash
DATABASE_URL="$SHORTFALL_ADMIN_DEMO_URL" npm run db:migrate
(
  cd services/shortfall-detection
  DATABASE_URL="$SHORTFALL_ADMIN_DEMO_URL" go run ./cmd/migrate
  DATABASE_URL="$SHORTFALL_ADMIN_DEMO_URL" go run ./cmd/migrate
)
PGDATABASE=shortfall_demo_20261114 psql -X -v ON_ERROR_STOP=1 <<'SQL'
GRANT shortfall_runtime TO shortfall_demo_20261114;
SQL
```

The second migration run checks idempotence/checksum compatibility. The
privileged identity retains ownership, and grants only SELECT on mapping
tables to `shortfall_runtime`. The runtime group must remain NOLOGIN with no
elevated flags/parent memberships. Existing unsafe PUBLIC grants cause the
migration/readiness checks to fail; this runbook does not rewrite shared ACLs.

Group G's automated journey provisions its own uniquely named database/login
and fixture mapping tables rather than using the example names or manual data.
Its mapping tables must mirror the adapter's owning Node schema columns and
constraints. Only the dedicated disposable objects may be removed afterward.

## Prepare fresh local configuration

Create a task-owned `.env` containing only synthetic local settings. This is
a fresh file, not a copy of the root environment. `SHORTFALL_DATABASE_URL` is
the Compose connection; `DATABASE_URL` is the host connection to the same
restricted login/database. A Linux Docker host may need its own host gateway
address instead of Docker Desktop's `host.docker.internal`.

```bash
mkdir -p /tmp/shortfall-local
umask 077
cat > /tmp/shortfall-local/.env <<'ENV'
DATABASE_URL=postgres://shortfall_demo_20261114:synthetic-shortfall-db-password@127.0.0.1:5432/shortfall_demo_20261114
SHORTFALL_DATABASE_URL=postgres://shortfall_demo_20261114:synthetic-shortfall-db-password@host.docker.internal:5432/shortfall_demo_20261114
SHORTFALL_ENVIRONMENT=local
SHORTFALL_WEBHOOK_SOURCE=shortfall-local
SHORTFALL_HTTP_ADDR=127.0.0.1:8082
SHORTFALL_WEBHOOK_SECRET=synthetic-shortfall-webhook-secret
SHORTFALL_SERVICE_SECRET=synthetic-shortfall-service-secret-32-bytes
SHORTFALL_ISSUING_API_VERSION=2026-08-21
AIRWALLEX_BASE_URL=https://api.sandbox.airwallex.com
AIRWALLEX_CLIENT_ID=synthetic-client
AIRWALLEX_API_KEY=synthetic-api-key
ENV
export SHORTFALL_ENV_FILE=/tmp/shortfall-local/.env
set -a
source "$SHORTFALL_ENV_FILE"
set +a
```

The candidate issuing version and synthetic credentials configure a client;
they establish no live subscription/key/funding compatibility. Runtime uses
`UnsupportedReferences`, so signed modern declines hold before provider reads.
No environment variable, header, or request parameter enables a fixture parser.

## Start and stop only shortfall processes

For host execution, use two separate terminals with the fresh local settings:

```bash
cd services/shortfall-detection
go run ./cmd/api
```

```bash
cd services/shortfall-detection
go run ./cmd/worker
```

Stop these foreground processes with Ctrl-C. API shutdown is bounded to five
seconds; worker cancellation leaves uncommitted jobs recoverable after lease
expiry. API defaults to a new loopback listener on port 8082. Startup does not
stop or replace an existing listener if the port is occupied.

Alternatively run the standalone opt-in Compose file from the worktree root:

```bash
docker compose --env-file "$SHORTFALL_ENV_FILE" \
  -p shortfall-local -f compose.shortfall.yaml --profile shortfall \
  up -d --build shortfall-api shortfall-worker
docker compose --env-file "$SHORTFALL_ENV_FILE" \
  -p shortfall-local -f compose.shortfall.yaml --profile shortfall \
  logs --tail=50 shortfall-api shortfall-worker
```

The file loads configuration as an `env_file`, overrides only its own runtime
database URL, creates no database/container volume, and publishes only its own
loopback port 8082. It does not merge or modify the existing Compose stack.
Its healthcheck uses liveness, since live readiness is intentionally false.
For shutdown, stop/remove only this dedicated project, without `--volumes`:

```bash
docker compose --env-file "$SHORTFALL_ENV_FILE" \
  -p shortfall-local -f compose.shortfall.yaml --profile shortfall \
  down
```

## Liveness and honest readiness

```bash
curl -i http://127.0.0.1:8082/healthz
curl -i http://127.0.0.1:8082/readyz
```

`/healthz` returns 200 independently of DB/configuration. `/readyz` currently
returns 503 in all cases, including a correctly configured local service:

```json
{
  "ready": false,
  "service_ready": true,
  "database_ready": true,
  "configuration_ready": true,
  "live_detection_ready": false,
  "live_parser": "unsupported",
  "reason": "UNSUPPORTED_WEBHOOK_VERSION"
}
```

`service_ready` describes DB/schema/runtime-role and local configuration only.
It is not a provider connectivity/permission check. Unavailable DB/configuration
sets the corresponding flags false and reason to DATABASE_UNAVAILABLE or
CONFIGURATION_UNAVAILABLE. Protected reads/receipt reuse the existing runtime
readiness checks, so an accidentally privileged DB URL cannot mutate the inbox.
Signature failures still return 401 before any storage readiness query.

## Signed receipt and final-observation reads

From the worktree root with fresh local settings loaded:

```bash
node services/shortfall-detection/scripts/request.mjs webhook
node services/shortfall-detection/scripts/request.mjs webhook
node services/shortfall-detection/scripts/request.mjs list
```

Each signed delivery gets 200 after durable insertion or duplicate lookup.
The identical payload reuses its inbox row. Runtime then holds it with
UNSUPPORTED_WEBHOOK_VERSION and creates no observation. On a fresh isolated
database, authenticated list returns `items: []` and `next_cursor: null`.

The helper targets loopback only, signs original fixture bytes, and keeps
signing/auth secrets and bearer tokens out of output. It mints the existing
HS256 assertion shape with issuer `flowcard-node-proxy`, audience
`flowcard-shortfall-service`, subject `shortfall-reader`, nonempty random `jti`,
and a 30-second TTL. Verification permits five seconds of clock skew.
Invalid authentication returns 401; a valid assertion with an unauthorized
audience/subject returns 403. No browser/gateway integration is added.

After the injected G journey creates an observation, inspect it with:

```bash
node services/shortfall-detection/scripts/request.mjs list \
  'customer_id=fixture-customer&outcome=SHORTFALL_DETECTED'
node services/shortfall-detection/scripts/request.mjs get DETECTION_UUID
```

Replace `DETECTION_UUID` with its actual returned ID. The API always returns
25 records at most, ordered by `(created_at DESC, id DESC)`. Reuse the returned
opaque `next_cursor` as `cursor`, keeping the same filters/environment. Invalid
encoding, fields, timestamps, UUID, changed filters, duplicates, or unknown
query keys return 400. Unknown valid detail UUID returns 404. All responses
are `Cache-Control: no-store`.

Only SHORTFALL_DETECTED, NO_CURRENT_SHORTFALL, and IGNORED are final API
outcomes.
UNRESOLVED is not an accepted list filter or a synthesized observation row.
Null amount/time/reference fields stay null. Each record includes
`requires_recheck: true` and `amount_semantics` describing current need at
balance observation time. Raw payloads, card secrets, and provider credentials
are excluded.

## Inspect and replay unresolved inbox work

Use the restricted login to inspect metadata only. Do not select `raw_body`,
provider responses, or signing credentials into logs/chat:

```bash
PGUSER=shortfall_demo_20261114 \
PGPASSWORD=synthetic-shortfall-db-password \
PGDATABASE=shortfall_demo_20261114 \
psql -X -c 'SELECT id, state, failed_attempts, last_reason,
  next_attempt_at, conflicted FROM shortfall.webhook_inbox ORDER BY received_at;'
```

Transient failures retry up to eight failures. Delays are 5/10/20/40/80/160/300
seconds with 20 percent jitter and any valid Retry-After minimum. Exhaustion
uses HELD and exact `last_reason` representation
`RETRIES_EXHAUSTED:<original-sanitized-code>`, for example
`RETRIES_EXHAUSTED:PROVIDER_NETWORK_ERROR`. No raw provider/DB error text is
stored there. Failed claims/crashes do not consume the failure counter.

After correcting missing evidence/configuration or a failed lookup, replay an
eligible HELD item using its actual inbox UUID:

```bash
(
  cd services/shortfall-detection
  go run ./cmd/replay --inbox-id INBOX_UUID --reason 'corrected local fixture evidence'
)
```

Replay appends reason/time/previous reason/failure count to audit, resets failed
attempts, and requeues the same original bytes/references. It refuses conflicts,
completed/link-bearing items, and currently leased work. No separate FAILED
state exists. Unsupported runtime parsers will hold again until verified;
replay does not enable one. No immutable UNRESOLVED detection blocks recovery.

## Injected local journey and verification

Group G executed three focused test functions: 79 subtest nodes (77 leaf cases),
all passing with no skipped test cases. The worker journey uses a disposable
database/login, actual signed receiver/inbox, real mapping SELECT, mocked
provider HTTP/auth/pagination/balance, real worker fencing/persistence, and
authenticated read API. Reference extraction and funding/sign evidence are
injected Go test dependencies. They supply facts/references, never final
eligibility/results.

The focused commands from the service directory are:

```bash
go test ./internal/http -run TestReceiverContract -count=1 -v
go test ./internal/provider -run TestProviderDetector -count=1 -v
go test ./internal/worker -run TestWorkerReadAPI -count=1 -v
```

The DB helper defaults to synthetic test credentials on loopback port 54329,
database `postgres`, and refuses other ports/nonloopback targets. Optional
`SHORTFALL_TEST_ADMIN_URL` must satisfy those same guards. It extracts the exact
mapping-table CREATE definitions from Node migrations 002/008, migrates twice,
and creates/cleans only unique `shortfall_g_*` databases/logins. It never drops
a shared runtime role, including one it finds preexisting. No root environment
file is loaded. The local verification used PostgreSQL 16.15, Go 1.27.4 on the
host, and Go 1.27.3 for the successful container build.

The combined verified command was `go test ./... -count=1 -json`; build, vet,
gofmt, Compose interpolation and Docker image build also passed. The optional
installed golangci-lint binary could not load this module because it was built
with Go 1.26. No tool/dependency upgrade was made.

The journey recorded USD 9,000 / 2,000 / 700,000 cents, same/different webhook
duplicates with one balance call, corrected held-balance replay, lease/conflict
fencing, and exhaustion retaining its original controlled cause. Separate
actual API/worker/migrate/replay binaries verified runtime holds, empty final
reads, readiness 503, liveness 200, and zero provider calls. All task-owned
processes/databases/logins were cleaned; the review image/artifacts remain.
Exact result IDs and command logs are in the local handoff/report. Standalone
runtime receipt intentionally produces a hold and does not establish live
detection readiness.

## Remaining live enablement gates

Recorded sanitized modern decline delivery/subscription version, canonical
reference paths, version-matched event retrieval, key permissions, USD wallet
funding configuration, and outgoing amount sign remain unverified. Differing
USD transaction/billing values are held until their semantics are verified.
No simulation response is reused as a webhook schema. No live operations or
subscription changes are part of this runbook.

After those gates and a separately authorized cutover, route only decline
notifications to `/webhooks/airwallex/shortfalls`, preserving Node's other
webhook responsibilities and excluding automatic scenario funding. Do not
enable a second notification family until separately verified. No deployment,
underwriting, offers, transfer, or later automatic resolution is implemented.

## See also

- [Provider contract](/docs/api/shortfall-provider-contract.md)
- [Shortfall design](/docs/designs/shortfall-detection.md)
- [Completed execution plan](/docs/plans/completed/20261113-shortfall-detection.md)
