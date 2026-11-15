# Underwriting local

This runbook prepares and runs FlowCard's Go underwriting assessment with
synthetic financial data. It uses the real snapshot importer, authenticated
shortfall read, API, deterministic rules, saved detail response and same-key
replay. It makes no live provider call and cannot authorize funding.

[TOC]

## Scope and prerequisites

Run commands from the repository root. Use an existing local PostgreSQL listener
only if it is already available. The runbook uses a dedicated logical database
named `flowcard_underwriting_demo` and a restricted `underwriting_demo_runtime`
login. It does not use the shared `flowcard_treasury` database or restart
existing services. If the dedicated database or runtime login already exists,
inspect its owner and grants first. Stop if it is not clearly task-owned; never
reset or drop it as routine cleanup.

The opt-in Compose file starts one API/mock container and connects it to the
existing PostgreSQL host through `host.docker.internal`. It does not define a
database service or mount a database volume. The API is exposed only at
`127.0.0.1:8083`. Its mock listens on the container's loopback port `8084`.
For host-run processes, check that both ports are free. Do not restart another
service to claim either port.

## Prepare private local configuration

The tracked examples contain placeholders only. Create ignored local files with
private permissions. Keep the privileged migration URL separate from runtime
configuration. Do not read or copy the repository root `.env`.

```bash
mkdir -p .dev/underwriting
umask 077
cp services/underwriting-engine/.env.example .dev/underwriting/runtime.env
cp services/underwriting-engine/.migration.env.example \
  .dev/underwriting/migration.env
chmod 600 .dev/underwriting/runtime.env .dev/underwriting/migration.env
```

Edit both files locally. Set distinct random values of at least 32 bytes for
`UNDERWRITING_SERVICE_SECRET` and `SHORTFALL_SERVICE_SECRET`. In `runtime.env`,
set `DATABASE_URL` to the host URL for the restricted runtime login and set
`UNDERWRITING_CONTAINER_DATABASE_URL` to that same login through
`host.docker.internal`. Set migration/admin URLs in `migration.env`. Do not
place these values in tracked files, fixtures or shell history. Use URL-safe
database passwords or percent-encode reserved characters in each database URL.

## Create the dedicated database and runtime login

Use an admin connection only for this dedicated demo database. Check whether
the name exists before creating it:

```bash
(
  set -a
  source .dev/underwriting/migration.env
  set +a
  psql "$UNDERWRITING_ADMIN_DATABASE_URL" -Atqc "
    SELECT datname FROM pg_database
    WHERE datname = 'flowcard_underwriting_demo'
  "
)
```

If the query returns no row, create the database:

```bash
(
  set -a
  source .dev/underwriting/migration.env
  set +a
  psql "$UNDERWRITING_ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 \
    -c 'CREATE DATABASE flowcard_underwriting_demo'
)
```

If it already exists, inspect its owner and contents. Keep it intact unless it
is confirmed to be this task's dedicated demo database.

Install the existing roster and card-mapping DDL in this database only, then
apply the underwriting schema migration:

```bash
(
  set -a
  source .dev/underwriting/migration.env
  set +a
  psql "$UNDERWRITING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
    -f server/src/db/migrations/002_customer_roster.sql
  psql "$UNDERWRITING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
    -f server/src/db/migrations/008_remote_authorization.sql
  env -u UNDERWRITING_ADMIN_DATABASE_URL \
    go -C services/underwriting-engine run ./cmd/migrate
)
```

`cmd/migrate` reads only `UNDERWRITING_MIGRATION_DATABASE_URL`. It creates the
NOLOGIN `underwriting_runtime` group and the separate schema/tables. Create a
dedicated runtime login after migration. Use `psql`'s interactive password
prompt so the password is not part of a command argument:

```sql
CREATE ROLE underwriting_demo_runtime LOGIN NOSUPERUSER NOCREATEDB
  NOCREATEROLE NOREPLICATION NOBYPASSRLS;
GRANT underwriting_runtime TO underwriting_demo_runtime;
\password underwriting_demo_runtime
```

If the login already exists, inspect it and confirm it is task-owned,
nonprivileged, and a member of `underwriting_runtime`. Do not alter an unknown
role. Update only the ignored `runtime.env` with the restricted login URLs.

## Prepare and import a fixture run

For Compose runs, build the opt-in image before preparing the run anchor. This
keeps the build time outside the five-minute source freshness window. From the
repository root:

```bash
docker compose --env-file .dev/underwriting/runtime.env \
  -f services/underwriting-engine/compose.yaml build
```

`prepare` defaults to one current UTC anchor and creates a fresh run ID,
producer versions and idempotency keys. It keeps each run below
`.dev/demo/runs/`; `.dev/demo/manifest.json` points to the latest run. Previous
run folders and database snapshots remain unchanged.

```bash
go -C services/underwriting-engine run ./cmd/demo prepare \
  --scenario all --as-of now --out .dev/demo
```

The three cases are `eligible`, `capacity_exceeded`, and
`missing_receivable_dates`. The generated bundle contains three customer
snapshots, one facility snapshot, signed shortfall-read mock inputs, mapping
SQL, expected rule traces and a manifest. Every source and financial fact is
labeled synthetic DEMO data. A pinned RFC3339 UTC instant can replace `now`,
but a run with stale facility/shortfall timestamps must be regenerated before
evaluation. Prepare does not run migrations, change mappings or insert
assessments.

The optional `--book-profile` flag defaults to `standard`; the command above
preserves the existing fixture values. For a combined proof that starts from
known-empty debt and reservation books, prepare with:

```bash
go -C services/underwriting-engine run ./cmd/demo prepare \
  --scenario all --as-of now --out .dev/demo \
  --book-profile known-empty
```

This profile emits explicit empty `bridges` and `reservations` arrays, with
complete coverage and synthetic source references that identify the books as
known-empty. It creates no legacy obligation rows. Facility total and reserve
floor remain unchanged; funded, reserved and other commitments are zero, so
available capital is $800,000. All cases retain $7,000 principal, $190 fee,
and $7,190 total due. The eligible and missing-date cases use $100,000 monthly
inflows and a $20,000 line. The capacity case uses $30,000 monthly inflows and
a $6,000 line, so its $7,190 post-funding exposure fails
`CUSTOMER_CAPACITY`. The missing-date case keeps its null, unverified invoice
date and returns `RECEIVABLE_COVERAGE` UNKNOWN. This changes synthetic inputs
only; policy values and rules are unchanged. Unsupported profile names are
rejected before output files are written.

The generated mapping script has a database-name guard and only inserts the
known synthetic roster/card pairs. By default the manifest and script target
`flowcard_underwriting_demo`. For disposable verification, choose a fresh name
in the safe namespace:
`flowcard_underwriting_demo_<lowercase-alphanumeric-or-underscore-suffix>`.
Create that database and a restricted runtime login. Point the host and
container runtime URLs to it with that login; point the privileged migration
URL to the same database using the existing admin identity. Pass the database
name with `--database-name`; the mapping SQL guard uses the captured name. The
helper never creates, resets or drops a database. Apply the script only to that
dedicated target:

```bash
MAPPING_SETUP=$(jq -r '.mapping_setup_sql' .dev/demo/manifest.json)
(
  set -a
  source .dev/underwriting/migration.env
  set +a
  psql "$UNDERWRITING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
    -f ".dev/demo/$MAPPING_SETUP"
)
```

Use the real snapshot CLI with the restricted runtime login. The demo evaluator
also imports the same files, so repeating these commands for one manifest
replays the same snapshot IDs/versions:

```bash
(
  set -a
  source .dev/underwriting/runtime.env
  set +a
  FACILITY_FILE=$(jq -r '.facility_file' .dev/demo/manifest.json)
  go -C services/underwriting-engine run ./cmd/import-snapshot \
    --kind facility --file ".dev/demo/$FACILITY_FILE"
  while IFS= read -r CUSTOMER_FILE; do
    go -C services/underwriting-engine run ./cmd/import-snapshot \
      --kind customer --file ".dev/demo/$CUSTOMER_FILE"
  done < <(jq -r '.scenarios[].customer_file' .dev/demo/manifest.json)
)
```

## Start the local API and shortfall mock

Check host port `8083` before starting the opt-in Compose project. The explicit
`--env-file` prevents Compose from reading the repository root `.env`. The
container starts the same `cmd/demo serve-shortfalls` mock and the API in one
private network namespace. Only the API is published to the host; its mock
remains on container loopback at `127.0.0.1:8084`.

```bash
docker compose --env-file .dev/underwriting/runtime.env \
  -f services/underwriting-engine/compose.yaml config --quiet
docker compose --env-file .dev/underwriting/runtime.env \
  -f services/underwriting-engine/compose.yaml up --detach
curl --fail http://127.0.0.1:8083/healthz
curl --fail http://127.0.0.1:8083/readyz
```

For host-run development instead of Compose, check that both `8083` and `8084`
are free. In one terminal start the mock; in another start the API. Load the
ignored runtime environment in each terminal:

```bash
(
  set -a
  source .dev/underwriting/runtime.env
  set +a
  go -C services/underwriting-engine run ./cmd/demo serve-shortfalls \
    --manifest .dev/demo/manifest.json
)
```

```bash
(
  set -a
  source .dev/underwriting/runtime.env
  set +a
  go -C services/underwriting-engine run ./cmd/api
)
```

The mock accepts only the shortfall service's signed HS256 assertion and serves
records from this manifest. It does not write shortfall or underwriting rows,
and normal API evaluation runs the same policy used outside the demo.
The complete generated bundle is mounted read-only at `/demo`; runtime manifest
and artifact resolution uses that bundle root and does not require source docs
or the repository checkout inside the image.

## Evaluate, retrieve and replay

The evaluator imports snapshots through the real importer, then sends normal
signed API requests and reads each saved detail response:

```bash
(
  set -a
  source .dev/underwriting/runtime.env
  set +a
  go -C services/underwriting-engine run ./cmd/demo evaluate \
    --manifest .dev/demo/manifest.json \
    --api-url http://127.0.0.1:8083
)
```

Expected results:

- `eligible`: ELIGIBLE. The $7,000 principal and $190 fee produce $7,190 due.
  Total exposure is $19,190 against a $20,000 Tier B line, with $8,000 free
  receivables. All ten rules PASS.
- `capacity_exceeded`: INELIGIBLE. $14,000 existing exposure plus $7,190 gives
  $21,190; `CUSTOMER_CAPACITY` FAIL.
- `missing_receivable_dates`: REVIEW_REQUIRED. The unverified/null due date
  makes `RECEIVABLE_COVERAGE` UNKNOWN. Other rules PASS.

Output includes each saved evaluation ID, status, reason codes and replay
status. The command performs an authenticated GET after POST, then repeats the
same POST/key and verifies the saved response is unchanged. A new preparation
uses new versions and keys; it does not rewrite old snapshots or evaluations.
Prepare a fresh run after the five-minute facility/shortfall window expires.

## Coordinator handoff

For one shared coordinator walkthrough, prepare once and pass the same
`.dev/demo/manifest.json` bundle to the importer, mock and presenter. The exact
prepare command is:

```bash
go -C services/underwriting-engine run ./cmd/demo prepare \
  --scenario all --as-of now --out .dev/demo
go -C services/underwriting-engine run ./cmd/demo serve-shortfalls \
  --manifest .dev/demo/manifest.json
go -C services/underwriting-engine run ./cmd/demo evaluate \
  --manifest .dev/demo/manifest.json --api-url http://127.0.0.1:8083
```

If the integration uses a uniquely named task-owned database, append
`--database-name flowcard_underwriting_demo_<safe_suffix>` and apply the
generated mapping SQL only to that captured database. `prepare` returns a
fresh `run_id`. Top-level manifest fields are `fixture_pack_version`,
`run_id`, `run_anchor`, `source_mode`, `policy_id`, `policy_hash`,
`output_directory`, `database_name`, `facility_id`, `facility_file`,
`shortfalls_file`, `mapping_setup_sql`, `expected_trace_file`,
`facility_books`, `scenarios`, and `provenance`. Each scenario includes
`scenario_id`, `customer_id`, `account_id`, `card_id`, `shortfall_id`,
`transaction_id`, `canonical_event_id`, `lifecycle_id`, `idempotency_key`,
`customer_file`, `term_days`, `principal_cents`, `estimated_fee_cents`,
`total_due_cents`, `customer_limit_cents`, `existing_exposure_cents`,
`post_funding_exposure_cents`, `free_receivables_cents`, `expected_status`,
`expected_rules`, and provenance.
The complete retained run manifest is
`.dev/demo/runs/<run_id>/manifest.json`; the top-level manifest points to the
latest prepared run. Use one run's manifest, customer files and shortfall
records throughout the walkthrough, without changing its anchor or IDs.

If an existing local manifest points to a database/login removed during a
prior test run, that bundle is not usable. Create the coordinator's owned
database, then run `prepare` with its `--database-name` to produce fresh IDs
and captures. Build the container before preparing; the shortfall/facility
window is five minutes, and `cmd/demo evaluate` rejects a stale prepared run.
Never edit timestamps or clone a saved decision.

For coordinator requests, use the normal API endpoints:

- `POST /api/underwriting/evaluations` with the generated `shortfall_id`,
  requested term, and a new idempotency key for a new decision.
- `GET /api/underwriting/evaluations/{evaluation_id}` for the saved result.
- `GET /api/underwriting/evaluations?customer_id=<customer_id>` for customer
  history, or omit the filter for the mode-wide list.
- `GET /api/underwriting/customers/{customer_id}/context` for normalized
  context and coverage/freshness flags.

Incoming API auth uses `UNDERWRITING_SERVICE_SECRET` from the private runtime
configuration. The JWT uses HS256, issuer `flowcard-node-proxy`, audience
`flowcard-underwriting-service`, subject `underwriting-evaluator`, a `jti`,
and a 30-second maximum lifetime. There is no separate read-only secret or
snapshot/evidence publication endpoint. `POST /api/underwriting/evaluations`
creates an authenticated assessment only. `SHORTFALL_SERVICE_SECRET` and
`SHORTFALL_BASE_URL` configure the API's outbound shortfall read; they are not
coordinator caller credentials. `UNDERWRITING_MODE`,
`UNDERWRITING_FACILITY_ID`, `DATABASE_URL`, and `UNDERWRITING_SERVICE_SECRET`
are runtime settings; the container also uses
`UNDERWRITING_CONTAINER_DATABASE_URL`. Keep all values in the ignored
mode-0600 configuration and do not transmit the secret value.

The `customer_id` is the stable coordinator key. Service DTOs and synthetic
manifest records can also contain internal `account_id`/`card_id` fields for
server linkage; keep them out of model context and use the coordinator's
`customer:<customer_id>` alias for funding references. All IDs and source rows
in this bundle are synthetic DEMO fixtures. Prepare a fresh run immediately
before presenting if the shortfall/facility freshness window expires; do not
clone a saved decision or use a stale manifest as a success fixture.

## Stop and preserve data

Stop only the opt-in Compose project, without removing volumes or database data:

```bash
docker compose --env-file .dev/underwriting/runtime.env \
  -f services/underwriting-engine/compose.yaml down
```

For host-run processes, send Ctrl-C in the task-owned terminals. Keep the
dedicated database, snapshots, evaluations and `.dev` run artifacts. Do not
truncate/reset shared data or use provider, model, AWS, publication or funding
operations as part of this demo.

## Source limitations

Every customer, facility, invoice, shortfall and payment fact in this journey
is synthetic and labeled DEMO. The fixture generator is a local presentation
tool; production evaluation uses the normal imported snapshots and authenticated
shortfall read, and has no fixture bypass. The demonstration does not provide
continuously refreshed customer data.

Future source work must establish verified receivable due dates and allocation
completeness, lifetime default history, complete historical balances and
inflows, and current funded/reserved exposure. Until those structured sources
are available and complete, policy facts remain unknown or synthetic. The
older Node policy family and existing product UI are unchanged by this service.
