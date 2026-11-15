# Go coordinator local demo

This guide packages a synthetic DEMO workflow for the Go coordinator. The
coordinator reads underwriting HTTP, a task-owned shortfall list/detail
adapter, and, when available, the funding read API. It does not call a real
model or move money.

[TOC]

## Profile and service boundaries

The coordinator uses a task-owned profile assembled from the lead's partial
upstream handoff. The lead-owned file is in the main checkout at
`.dev/integration/20261114/runtime/coordinator/upstreams.env`; it is mode
`0600` and deliberately has blank `DATABASE_URL` and source-root fields. It is
not a complete launcher. The value-free proof is `upstream-handoff-proof.json`.
Merge only required credentials into task-owned files; never copy root `.env`,
print credentials, or place secrets in this repository.

Keep coordinator-owned API, source-reader, migration, and Compose path files in
the authorized worktree's ignored
`.dev/integration/20261114/runtime/coordinator-task/` directory. The main
checkout's `.dev/integration/20261114/runtime/` and its lead-owned partial
upstream/underwriting files are inputs; do not overwrite them. Compose mounts
the lead's active underwriting bundle read-only.

Use separate mode `0600` files for the coordinator API, source adapter, and
migration command. The API file contains the runtime database role URL. The
migration file contains the separate task-owned migration role URL. The source
adapter file contains only the shortfall reader key plus the approved mode and
binding. Never use a maintenance URL as runtime `DATABASE_URL`.

The service variables are defined in
`services/go-coordinator/internal/config/config.go` and the command entrypoints:

| Variable | DEMO setting |
| --- | --- |
| `COORDINATOR_HTTP_ADDR` | `127.0.0.1:8090` |
| `COORDINATOR_MODE` | `DEMO` |
| `COORDINATOR_SOURCE_BINDING_ID` | `flowcard-integration-20261114` |
| `DATABASE_URL` | Unique task-owned coordinator database on PostgreSQL `127.0.0.1:54329`; use the runtime role in the API file and the migration role in the migration file. |
| `SHORTFALL_BASE_URL` | `http://127.0.0.1:8092` |
| `SHORTFALL_SERVICE_SECRET` | Source-reader key for the coordinator adapter; the adapter uses the same key to verify the shortfall reader JWT. |
| `UNDERWRITING_BASE_URL` | `http://127.0.0.1:8083` |
| `UNDERWRITING_SERVICE_SECRET` | Underwriting evaluator caller key. |
| `SERVICING_BASE_URL` | `http://127.0.0.1:8085` after the funding runtime is ready. |
| `SERVICING_SERVICE_SECRET` | Must equal the funding service's `SERVICING_COORDINATOR_SECRET`; the existing `flowcard-go` / `flowcard-funding-collection` / `coordinator-reader` assertion is read-only and has a 20-second TTL. |
| `COORDINATOR_READER_SECRET` | Dedicated case-reader key for the API, watch, and evidence commands. |
| `COORDINATOR_AGENT_MODE` | `FIXTURE`; omit Anthropic credentials and model variables. |
| `COORDINATOR_SOURCE_ROOT` | A new task-owned source directory under `.dev`, prepared from the same retained manifest run. |

The shortfall adapter binds only `127.0.0.1:8092` and exits if the port is
occupied. It serves `GET /api/shortfalls` and `GET /api/shortfalls/{id}` from
the selected underwriting bundle. It does not create evaluations or funding
records. The adapter checks the existing shortfall JWT identity and serves
record fields from that bundle.

| Component | Address | Owner and status |
| --- | --- | --- |
| Coordinator API | `127.0.0.1:8090` | Lead-accepted manual profile override. |
| Synthetic shortfall adapter | `127.0.0.1:8092` | Coordinator-owned, loopback-only, same prepared shortfalls file. |
| Underwriting API | `127.0.0.1:8083` | Actual committed HTTP implementation, lead-owned runtime. |
| Underwriting detail-only mock | `127.0.0.1:8084` | Lead-owned; serves detail and has no shortfall list route. |
| Funding API | `127.0.0.1:8085` | Owner-confirmed target; runtime and publisher acceptance remain for G. |
| Funding provider double | `127.0.0.1:8091` | Funding-owner local double; no real provider call. |

Leave onboarding `5175`, gateway `8003`, and the existing Go service `8004`
running. Do not stop or replace another owner's process if a bind fails. The
adapter does not use `8084`: the coordinator requires the list route at `8092`.
The coordinator consumes underwriting through HTTP; it does not import the
underwriting policy package.

The coordinator's `account_ref` is exactly `customer:` plus the unchanged
trusted `customer_id`. Raw account/card/payment identifiers remain private and
are checked against the shortfall, saved evaluation, and customer-context
relation. Neither the alias nor a source summary establishes ownership.

The source binding is `flowcard-integration-20261114` in DEMO. Funding's
optional `originating_case_id` on the existing offer request is approved and
remains attributed provenance, not authority. Funding owns that publisher. G
must verify the actual event carries the exact originating case, saved
evaluation, offer, customer, alias, mode, and binding. Do not add an endpoint,
tool, SQL shortcut, or fake event.

## Prepare one fresh underwriting run

The committed underwriting implementation is
`e608a4556f83dde4c45e51d389d7d24da32835bd`. Its demo handoff is recorded in
`docs/demo/underwriting-local.md` at commit
`ae02dc5d1de098993ab62a7753410057636d3492`. The source tree is lead-owned; do
not copy it into this coordinator change or modify its routes.

Build the underwriting container before preparation so the five-minute source
freshness window is not consumed by the build. The committed prepare command
shape is:

```sh
docker compose --env-file "$UNDERWRITING_RUNTIME_ENV_FILE" \
  -f services/underwriting-engine/compose.yaml build
go -C services/underwriting-engine run ./cmd/demo prepare \
  --scenario all --as-of now --out .dev/demo \
  --database-name flowcard_underwriting_demo_<safe_suffix>
```

These are reference commands, not run in this F checkpoint. For combined G
proof, coordinate a new run with the lead and use the active bundle under
`.dev/integration/20261114/runtime/underwriting/bundle/`. Select its retained
manifest at `runs/<run_id>/manifest.json`. Keep that run ID, anchor, IDs,
shortfalls file, auth profile, and provenance across the adapter and callers.
Do not copy chat IDs, edit timestamps, re-key saved decisions, or rewrite prior
cases with a new run.

The adapter reads the `shortfalls_file` path from that retained manifest,
validates it against the scenarios and run anchor, and returns the generated
records without changing IDs or timestamps. The underwriting-owned detail mock
at `8084` and coordinator adapter at `8092` must read the same shortfalls file.
`UNDERWRITING_SERVICE_SECRET` authenticates coordinator calls to the API. The
underwriting process's `SHORTFALL_BASE_URL` and `SHORTFALL_SERVICE_SECRET`
point to its detail-only mock at `8084`; those credentials are separate from
the coordinator-to-`8092` source key.

## Prepare coordinator source manifests

For each source run, set a new empty task-owned path in
`COORDINATOR_SOURCE_ROOT` beneath the worktree's ignored `.dev` directory.
Prepare files using the retained manifest and shared case-ID function:

```sh
export COORDINATOR_MODE=DEMO
export COORDINATOR_SOURCE_BINDING_ID=flowcard-integration-20261114
export COORDINATOR_SOURCE_ROOT="$PWD/.dev/integration/20261114/runtime/coordinator-task/sources/$COORDINATOR_RUN_ID"
go -C services/go-coordinator run -mod=readonly ./cmd/prepare-sources \
  --manifest "$UNDERWRITING_MANIFEST"
```

Set `COORDINATOR_RUN_ID` from the retained manifest before using this command.
The helper calls `domain.StableCaseID` for each shortfall and writes
`<case_id>.json`; it does not reimplement the hash or touch coordinator
storage. It refuses a nonempty source root and creates files with mode `0600`.
For `RECEIVABLE_COVERAGE=UNKNOWN`, it adds a labeled coordinator-owned
synthetic invoice source. The date remains unverified evidence and does not
amend the upstream snapshot or saved evaluation. PASS scenarios have an empty
source list and finish without an evidence claim.

Existing stored case IDs and prior source files are retained. A new prepared
run has new shortfall IDs and therefore new deterministic case IDs and source
directory. Do not overwrite or refresh an old source manifest to make an old
decision current.

## Build and run the coordinator

Build the coordinator image from the service directory:

```sh
docker build -f services/go-coordinator/Dockerfile \
  -t flowcard-go-coordinator:local services/go-coordinator
```

The service `.dockerignore` excludes `.dev`, private env/key files, and other
local runtime artifacts from the build context while retaining tracked
`*.example` templates.

The opt-in `compose.coordinator.yaml` starts only the coordinator API and the
synthetic adapter. It uses `network_mode: host` so the code can keep its
loopback-only URLs. Docker Desktop host networking requires version 4.34 or
later and must be enabled in Settings; see [Docker host networking](https://docs.docker.com/engine/network/drivers/host/).
Compose declares no database, volume, port mapping, or automatic cleanup.

Copy `services/go-coordinator/.compose.env.example` to a task-owned nonsecret
Compose path file and replace its run ID and task-owned env paths. Copy
`services/go-coordinator/.env.example` into separate mode-`0600` API/source/
migration files, filling only the values required for each process. The tracked
templates intentionally have blank database/source values and nonfunctional
secret placeholders.

For Compose, set `COORDINATOR_SOURCE_HOST_ROOT` to the same task-owned host
directory configured as `COORDINATOR_SOURCE_ROOT` in the API file. Compose
overrides the container value to `/coordinator-sources` and mounts that host
directory read-only.

Apply the schema before starting the API. The migration service is behind the
explicit `migrate` profile and reads a separate privileged env file:

```sh
docker compose --env-file "$COORDINATOR_COMPOSE_PATHS" \
  -f compose.coordinator.yaml --profile migrate run --rm coordinator-migrate
```

After migration succeeds, start the adapter, then the API:

```sh
docker compose --env-file "$COORDINATOR_COMPOSE_PATHS" \
  -f compose.coordinator.yaml up -d fixture-shortfalls
docker compose --env-file "$COORDINATOR_COMPOSE_PATHS" \
  -f compose.coordinator.yaml up -d coordinator-api
```

The adapter serves one retained run. The coordinator scheduler polls while its
API process is running, so stop the task-owned API and adapter before that
run's five-minute freshness window expires. If it expires first, stop both
owned processes and report the timing; do not keep serving the old bundle or
edit its timestamps. Coordinate any later run with the lead.

## Run the focused G verification

The G wrapper creates a unique marker-owned logical database and restricted
runtime login on the existing local PostgreSQL instance. It applies migrations
twice, verifies the runtime grants, runs only the three focused coordinator
scenarios, then removes only its marked database, login, and private profile.
It does not start or reset a container or volume.

Provide only the local admin URL file. The wrapper creates a synthetic
DEMO/FIXTURE profile with loopback-only settings and test-only keys; it does not
read the lead's upstream profile or require real service credentials:

```sh
COORDINATOR_TEST_ADMIN_URL_FILE="$PWD/.dev/integration/20261114/runtime/local-test-admin-url.txt" \
services/go-coordinator/scripts/run-g-local.sh
```

The admin file must be a regular mode-`0600` file for local PostgreSQL on port
`54329`. The wrapper writes complete mode-`0600` profiles under the worktree's
ignored `.dev` directory and never reads the repository root `.env`. For the
separate manual actual-HTTP proof, set `COORDINATOR_UPSTREAMS_FILE` only on an
explicit `cmd/testdb setup` command. The automated wrapper clears that variable
and always selects synthetic settings.

Do not allow missing database variables to fall back to pgx defaults. Use the
lead's local test-admin handoff only for G's uniquely named task-owned
database/login. The API profile uses only its restricted runtime URL. Do not
use the underwriting manual database or change shared volumes.

If running directly on the host, use the same private files without enabling
shell tracing:

```sh
set -a
source "$COORDINATOR_SOURCE_ENV_FILE"
set +a
go -C services/go-coordinator run -mod=readonly ./cmd/fixture-shortfalls \
  --manifest "$UNDERWRITING_MANIFEST"
```

Start the adapter in its own task terminal, then start `cmd/api` from the API
profile. It binds the configured loopback address, normally
`127.0.0.1:8090`. Never print env values or use `set -x`. A failed bind means
stop and report the conflict; do not kill a listener.

The equivalent host API command is:

```sh
PROFILE_DIR=.dev/integration/20261114/runtime/coordinator-task
set -a
source "$PROFILE_DIR/api.env"
set +a
go -C services/go-coordinator run -mod=readonly ./cmd/api
```

## Walkthrough and evidence limits

The shortfall path is evaluation-first. The real coordinator discovers
positive shortfalls from its `8092` source, POSTs the fixed
`{shortfall_id,term_days:30}` request to underwriting `8083`, reads the saved
response through its HTTP client, and then performs bounded investigation.
The demo does not precreate a decision through SQL or mark source data as a
saved assessment.

For the missing-receivable-date scenario, the deterministic `FIXTURE` agent
reads coordinator-owned synthetic invoice text, submits an attributed
`PENDING_REVIEW` evidence item, and finishes with a citation. The underwriting
evaluation remains immutable and unchanged. Evidence can be exported for
review with `cmd/evidence export`; link remains an operator action and never
updates the underwriting snapshot. Cases with no allowed due-date claim can
finish with a cited source or no claim. The run remains labeled `FIXTURE`.

After a run completes, the operator may export an attributed review packet:

```sh
go -C services/go-coordinator run -mod=readonly ./cmd/evidence export \
  --case "$CASE_ID" --output "$REVIEW_PACKET"
```

`cmd/evidence link` is a separate, explicit source-reference audit after a
human validator supplies the current accepted snapshot ID and version. It
does not validate a claim or publish an underwriting snapshot.

The existing configuration also supports `COORDINATOR_AGENT_MODE=ANTHROPIC`,
`ANTHROPIC_MODEL`, and `ANTHROPIC_API_KEY`. A future synthetic-data model
profile must pin a model selected through a separate data/spend review and
keep the API key in a mode-`0600` server-only file. This F profile sets
`COORDINATOR_AGENT_MODE=FIXTURE`, omits both Anthropic variables, and makes no
model request.

Use `cmd/watch --case <case_id>` to display safe progress from the
authenticated case API. Use `cmd/replay --case <case_id> --reason
<operator_reason>` only for a held or interrupted case at its existing
revision. Replay preserves the saved assessment key, call budget, and outcome;
it never creates a new assessment. A new source snapshot requires a new
prepared run and IDs.

The coordinator's collection client reads the funding change feed, bridge,
collection pages, and referenced operation details from the API at `8085`. It
issues no money-movement command. The funding provider double at `8091` is a
funding-owned local test dependency, not a real provider. The owner's final
publisher/runtime command and actual linked event are still required in G. If
the publisher is not available, mark the linked collection walkthrough blocked
rather than inventing a bridge or claiming acceptance.

The origin link for G must be emitted through the funding owner's existing
offer command with the exact saved `originating_case_id`, `evaluation_id`, and
resulting `offer_id`. Verify the source event and bridge preserve customer ID,
canonical alias, private raw-account relation, source binding, and mode. This
is attributed provenance only and grants no coordinator write authority.

The G collection walkthrough uses one source-owned bridge and case. Funding
must provide successive authoritative states with two partial settlements,
an uncertain operation/recovery state, missing collection authority, and
source-confirmed closure. The coordinator resumes the retained change-feed
sequence, refreshes that bridge, and keeps each model explanation scoped to
its source version and allowed actions. If the funding provider double or
publisher cannot produce one of those real HTTP states, record that step as
blocked by the owner runtime. Do not create a coordinator-side funding route,
insert saved results with SQL, or infer settlement from a pending operation.

## Stop and preserve data

Stop only task-owned API and adapter containers or terminals. Do not stop the
lead-owned underwriting API/detail mock, funding API/provider double, onboarding
UI, gateway, or existing Go service. Do not remove Compose volumes, reset or
truncate databases, delete snapshots/evaluations, or clear feed checkpoints.
The F Compose file declares no volume to remove.

At G, run only the three approved focused tests: `TestDiscoveryRecovery`,
`TestAgentAuthority`, and `TestCoordinatorRecovery`. Use unique marker-owned
database/login resources on the existing PostgreSQL test port, the real saved
evaluation API, the actual funding HTTP service and its local provider double
when available, and no live model/provider operation. No migration, database,
HTTP, or behavior test was run in this F checkpoint.

Live model/provider/data-spend approval, modern Airwallex decline extraction,
production CRM mapping, funding runtime readiness, and complete publisher
acceptance remain outside F. This fixture path establishes no live financial
integration or movement of funds.
