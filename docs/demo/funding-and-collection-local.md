# Funding and collection local fixture

This runbook describes the local DEMO journey using synthetic funding and
provider facts, the committed public underwriting policy and authenticated
service APIs. The real API, worker and HTTP adapter passed component tests
and the actual joined Underwriting/Coordinator/Funding run on November 15,
2026. See the [verification report](/docs/demo/funding-and-collection-verification.md)
for exact source, amounts and evidence. The local provider double is not
Airwallex acceptance.

[TOC]

## Scope and ownership

Run standalone component commands from the assigned funding worktree root. The
shared actual-profile handoff below runs feature-built binaries from MAIN.
Use these distinct loopback endpoints:

| Component | Address | Owner |
| --- | --- | --- |
| Funding API | `127.0.0.1:8085` | Funding and Collection |
| Provider double | `127.0.0.1:8091` | Funding and Collection fixture |
| Underwriting API | `127.0.0.1:8083` | Underwriting Engine |
| Shortfall detail fixture | `127.0.0.1:8084` | Underwriting fixture |
| Coordinator API | `127.0.0.1:8090` | Coordinator |
| Coordinator adapter | `127.0.0.1:8092` | Coordinator |

Check ownership and availability before starting any process. A command fails
closed when its fixed listener is occupied. Do not stop or replace another
owner's process. The coordinator addresses are integration references, not
services started by this runbook.

The funding fixture facility ID is `facility_underwriting_demo` and the
retained source binding is `flowcard-integration-20261114`. Database names
must be fresh, task-owned names under the prefixes
`flowcard_funding_demo_` and `flowcard_underwriting_demo_`. Preparation,
migration, import and API readiness bind to the exact manifest database name.
No root `.env` is read or copied. Never use the shared `flowcard_treasury`
database or reset a database volume as cleanup.

The public underwriting dependency is commit
`d2aeb7ff1987fb7ae6ac4c47fc40e5ca8bae3766`, policy ID
`bridge-capital-go-v1`, with SHA-256
`0a0cf42f72e101a458c260be8fbca315704d9a093264049acbf366f8cd4d38f0`.
Funding loads identity and pricing from the same public policy config.

All customer, payment, invoice, consent, actor, source and provider facts in a
prepared run are synthetic. Opening funding bridge/reservation books are
declared known-empty; this declaration must be confirmed against the dedicated
database before import. The DEMO authority and customer consent fixtures are
not live consent. The provider double is not Airwallex acceptance. The saved
underwriting decision must be created by the actual Underwriting API; the
fixture bundle contains inputs and expected diagnostics only.

## Provision owned databases before the fresh anchor

Provision both dedicated logical databases, the existing roster/card schema,
the Funding migrations and the restricted runtime logins before preparing a
source bundle. Build the service binaries before the fresh-source window too.
This keeps the prepared shortfall/context observations fresh through actual
evaluation and offer creation. The commands below are documentation only and
were not run during the source checkpoint.

```sh
funding_repo_root=$(pwd -P)
funding_suffix="$(date -u +%Y%m%d%H%M%S)_$$"
funding_database_name="flowcard_funding_demo_$funding_suffix"
underwriting_database_name="flowcard_underwriting_demo_$funding_suffix"
funding_runtime_role="funding_demo_runtime_$funding_suffix"
```

Use a private admin URL for database creation and a privileged URL that
connects to the new Funding database for base mapping DDL and service
migrations. A read must show the name absent before creation; inspect and stop
if it already exists. Apply the Underwriting database setup using
[Underwriting local](underwriting-local.md) with the matching
`underwriting_database_name`.

```sh
psql "$FUNDING_ADMIN_DATABASE_URL" -Atqc \
  "SELECT datname FROM pg_database WHERE datname = '$funding_database_name'"
# Only when the query returns no row:
psql "$FUNDING_ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE \"$funding_database_name\""
psql "$FUNDING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f server/src/db/migrations/002_customer_roster.sql
psql "$FUNDING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f server/src/db/migrations/008_remote_authorization.sql
DATABASE_URL="$FUNDING_MIGRATION_DATABASE_URL" \
  go -C services/funding-collection run ./cmd/migrate \
  --database-name "$funding_database_name"
```

After migration, create the nonprivileged service login and grant its NOLOGIN
group membership. Keep its password and `FUNDING_RUNTIME_DATABASE_URL` only in
the private runtime credential file:

```sh
psql "$FUNDING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -v runtime_role="$funding_runtime_role" <<'SQL'
CREATE ROLE :"runtime_role" LOGIN NOSUPERUSER NOCREATEDB
  NOCREATEROLE NOREPLICATION NOBYPASSRLS;
GRANT servicing_runtime TO :"runtime_role";
\password :runtime_role
SQL
```

Compile the binaries before generating the fresh source anchor:

```sh
go -C services/underwriting-engine build ./...
go -C services/funding-collection build ./...
```

## Prepare a fresh run

Do this close to the eventual evaluation. The manifest is fresh for four
minutes; if it expires, prepare another run and preserve the old run folder.
Do not edit capture timestamps or reuse saved decision IDs.

```sh
funding_output="$funding_repo_root/.dev/funding-collection/"
funding_output="${funding_output}bundle-$funding_suffix"
go -C services/funding-collection run ./cmd/demo prepare \
  --as-of now --out "$funding_output" \
  --database-name "$funding_database_name" \
  --underwriting-database-name "$underwriting_database_name"
funding_manifest="$funding_output/manifest.json"
funding_run_id=$(jq -r '.run_id' "$funding_manifest")
funding_run_dir="$funding_output/runs/$funding_run_id"
psql "$FUNDING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f "$funding_run_dir/mapping-funding.sql"
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  "$funding_bin/funding-import" --kind facility \
  --file "$funding_run_dir/facility-abundant.json" \
  --database-name "$funding_database_name"
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  "$funding_bin/funding-import" --kind receivables \
  --file "$funding_run_dir/receivables.json" \
  --database-name "$funding_database_name"
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  "$funding_bin/funding-import" --kind authority \
  --file "$funding_run_dir/authority.json" \
  --database-name "$funding_database_name"
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  "$funding_bin/funding-import" --kind actors \
  --file "$funding_run_dir/actors.json" \
  --database-name "$funding_database_name"
```

Component preparation writes two profile input bundles:

| Profile | Expected saved policy outcome |
| --- | --- |
| `abundant` | `ELIGIBLE`, candidate for the later normal DEMO journey |
| `tight` | `REVIEW_REQUIRED`, operator approval required |

For `tight`, prove the approval guard, then show that scarce execution is
unsupported.

Both profiles preserve the synthetic $9,000 attempted payment, $2,000
observed balance, and $7,000 current need. The pinned public evaluator checks
the expected profile outcomes during preparation. The later underwriting
command creates the persisted assessment. It is not included or cloned here.

The primary manifest and all inputs are under the unique
`.dev/funding-collection/bundle-$funding_suffix` directory.
Names use safe lowercase letters, digits and underscores and must remain under
their service-specific prefixes. The generated SQL carries an exact
`current_database()` comparison to `funding_database_name`.
Use a new `--out` directory if an older private bundle schema is present;
keep previous run directories intact.
Profile manifests are:

```text
"$funding_run_dir/upstream/abundant/manifest.json"
"$funding_run_dir/upstream/tight/manifest.json"
```

Inspect nonsecret metadata and artifact names only. `runtime.env` and
`provider-state.json` have mode `0600`; do not `cat`, paste, mail or log their
contents. The generated environment has distinct synthetic role/HMAC keys and
the fixed DEMO provider values `demo-client-id` and `demo-api-key`. It has no
database URL. Supply separate task-owned database credentials at execution
time.

To bind a fresh owned database to the generated SQL and later service
readiness, pass both database names to `prepare`. `cmd/migrate` and every
`cmd/import` call require the same funding `--database-name`; they compare it
to `current_database()` before applying schema or importing facts. Runtime
readiness also returns the connected database name, and `cmd/demo run` rejects
a mismatch. Choose a new unique suffix for each isolated manual/test database.

## Apply generated mappings and import facts

Database creation, mapping-table DDL, Funding migrations and restricted-login
grants are completed before `prepare` as described above. After preparation,
apply only the generated mapping SQL to the exact database it names. Then
import with the restricted runtime URL. Do not put database URLs in generated
fixtures, output, tracked files or the root `.env`.

```sh
psql "$FUNDING_MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f "$funding_run_dir/mapping-funding.sql"
```

The import command checks both `current_database()` and migration/runtime-role
readiness. Import in this order:

```sh
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  go -C services/funding-collection run ./cmd/import \
  --kind facility --file "$funding_run_dir/facility-abundant.json" \
  --database-name "$funding_database_name"
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  go -C services/funding-collection run ./cmd/import \
  --kind receivables --file "$funding_run_dir/receivables.json" \
  --database-name "$funding_database_name"
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  go -C services/funding-collection run ./cmd/import \
  --kind authority --file "$funding_run_dir/authority.json" \
  --database-name "$funding_database_name"
DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL" SERVICING_MODE=DEMO \
  SERVICING_FACILITY_ID=facility_underwriting_demo \
  go -C services/funding-collection run ./cmd/import \
  --kind actors --file "$funding_run_dir/actors.json" \
  --database-name "$funding_database_name"
```

Import one facility configuration at a time. For the scarce negative path,
import `facility-tight.json`, start the services below, evaluate the matching
`tight` upstream profile, and run `demo run --profile tight`. The real funding
guard must return `SCARCE_CAPITAL_RANKING_UNSUPPORTED` after explicit operator
approval, with no bridge reservation or new provider transfer. To proceed with
the abundant journey, verify there is no bridge, reservation, outstanding
operation or hold from the tight attempt, then import `facility-abundant.json`.
Both facility imports retain the same exact funding database guard. Never use
an update, reset or cleanup command to force a successful state.

The component upstream manifests use the same run ID and anchor as the funding
bundle. Follow [Underwriting local](underwriting-local.md) to provision its
dedicated database, import the bundle's snapshots and mapping, and start its
actual API. For the negative pass, start the source fixture with the tight
profile manifest in an owned terminal:

```sh
go -C services/underwriting-engine run ./cmd/demo serve-shortfalls \
  --manifest "$funding_run_dir/upstream/tight/manifest.json"
```

Then run the actual evaluation CLI against the actual API:

```sh
tight_evaluation_output=$(
  go -C services/underwriting-engine run ./cmd/demo evaluate \
    --manifest "$funding_run_dir/upstream/tight/manifest.json" \
    --api-url http://127.0.0.1:8083
)
tight_evaluation_id=$(printf '%s' "$tight_evaluation_output" | \
  jq -r '.results[] | select(.scenario_id == "tight") | .evaluation_id')
```

For the abundant pass, stop only the shortfall fixture process you started,
then restart it with the abundant manifest and evaluate that profile. Never
replace another owner's process on port `8084`.

```sh
go -C services/underwriting-engine run ./cmd/demo serve-shortfalls \
  --manifest "$funding_run_dir/upstream/abundant/manifest.json"
abundant_evaluation_output=$(
  go -C services/underwriting-engine run ./cmd/demo evaluate \
    --manifest "$funding_run_dir/upstream/abundant/manifest.json" \
    --api-url http://127.0.0.1:8083
)
abundant_evaluation_id=$(printf '%s' "$abundant_evaluation_output" | \
  jq -r '.results[] | select(.scenario_id == "abundant") | .evaluation_id')
```

Use the actual evaluation ID and a real saved coordinator case ID when
creating an offer. Do not synthesize either ID or copy expected policy output
into a decision record. The upstream runbook owns its runtime credentials and
migration details; preserve its existing database and services.

## Run the authenticated funding journey

Task 12B's `run` command orchestrates actor/API requests and reads only. It does
not migrate, import, start or stop any process. Start the provider double,
Underwriting API/detail fixture, Funding API and the single serial Funding
worker using the commands below. Load the private runtime environment in each
owned process without printing it; set its restricted `DATABASE_URL` from the
task-owned credential file. Never source the root `.env`.

For the `tight` negative pass, import the tight facility configuration and
evaluate its actual saved assessment first. Then run:

```sh
go -C services/funding-collection run ./cmd/demo run \
  --manifest "$funding_manifest" --api-url http://127.0.0.1:8085 \
  --profile tight --evaluation-id "$tight_evaluation_id"
```

The command records customer acceptance and explicit operator approval via
authenticated actor routes, then submits the funding command. It requires the
saved command receipt to be `SCARCE_CAPITAL_RANKING_UNSUPPORTED`, verifies the
same-key replay, confirms the known-empty funding state remains unchanged, and
checks that provider transfer count did not increase.

After confirming the tight run created no reservation or operation, import
the abundant facility file and evaluate the abundant saved assessment. Run:

```sh
go -C services/funding-collection run ./cmd/demo run \
  --manifest "$funding_manifest" --api-url http://127.0.0.1:8085 \
  --profile abundant --evaluation-id "$abundant_evaluation_id"
```

If the customer case ID is available from the host, include
`--originating-case-id "$coordinator_case_id"`. The command validates the
saved evaluation against the prepared source IDs and terms, creates or replays
the bound offer, records the synthetic customer decision through the public
API, and records an operator approval only when the saved assessment requires
it. The command obtains principal, term and fee from the saved evaluation.

For the current component profile, the actual evaluation is $7,000 principal
for 30 days with a $190 fixed fee. The provider-double controls prepare a $100
first deposit and a $7,090 second deposit. The real worker and adapter settle
$100 contract fee, then $90 contract fee and $7,000 principal. For a different
saved evaluation matched to an owned prepared bundle, the runner sizes only
the second synthetic deposit from that assessment's remaining due; it does not
assume $7,000 or $190. Its output reports the source-input kind and keeps
`provider_acceptance_verified=false`.

The provider double drops the first collection-fee create response after
persisting its transfer. The serial worker recovers by reading the same saved
request ID. The runner checks the recorded drop, transfer identity/status,
settlement allocation order, zero debt, zero invoice backing, zero holds and
final provider balances. It does not infer success from a create response.
After each provider-control deposit settlement, the runner posts the normal
HMAC-signed `DEMO_DEPOSIT_SETTLED` wake envelope to the servicing inbox. The
worker retrieves canonical deposit and balance facts through the HTTP adapter;
the wake body does not assert settlement or write ledger facts.
The provider-state file persists across a provider-double restart; the
database and idempotency receipts persist across API/worker restarts. Restart
only a service process that you own, keep its configuration and database, and
let the existing worker recover its durable operation. Unknown movement stays
held for review; never clear an operation, ledger line or provider state.

## Lead-owned shared actual-profile handoff

The actual-HTTP profile is lead-owned. Do not refresh Underwriting data, start
or replace its listeners, or execute this handoff during the source checkpoint.
The lead retains one fresh known-empty bundle at:

```text
.dev/integration/20261114/runtime/underwriting/bundle/manifest.json
```

The lead prepares it with Underwriting helper commit
`465fab6bb9cca9219a64c89446545ca0035e85da` only after binaries, owned
Databases, profile and launcher are ready. Funding must use the exact manifest,
run ID and anchor. The existing lead-owned Underwriting API at
`127.0.0.1:8083` and detail source at `127.0.0.1:8084` remain running; Funding
must not refresh, start, replace or restart them.

Open a shell in MAIN before resolving `.dev`. Read only nonsecret names from the
retained manifests. The host's nonsecret runtime manifest records the selected
Funding target; its private `database.env` contains the restricted DSN. The
host obtains the coordinator case ID and the exact saved evaluation ID linked
to that case. Never create a separate evaluation and attach it to a case with a
different reference.

```sh
funding_repo_root=$(pwd -P)
test -f "$funding_repo_root/services/underwriting-engine/go.mod"
funding_runtime_dir="$funding_repo_root/.dev/integration/20261114/runtime/funding"
actual_upstream_manifest="$funding_repo_root/.dev/integration/20261114/runtime/underwriting/bundle/manifest.json"
underwriting_database_name=$(jq -r '.database_name' "$actual_upstream_manifest")
funding_database_name=$(jq -r '.funding_database_name' "$funding_runtime_dir/manifest.json")
funding_suffix="$(date -u +%Y%m%d%H%M%S)_$$"
funding_source="$funding_repo_root/.worktrees/funding-collection"
funding_bin="$funding_source/.dev/bin"
mkdir -p "$funding_bin"
```

Build Funding binaries from the assigned feature worktree, then execute them
with MAIN as the current directory. Before merge, `cmd/demo` locates MAIN by
its `.git` file/directory and public Underwriting module marker; it
bounds every manifest/artifact under MAIN's ignored `.dev` and rejects
symlinks. Build before the lead's fresh profile window:

```sh
go -C "$funding_source/services/funding-collection" build \
  -o "$funding_bin/funding-demo" ./cmd/demo
go -C "$funding_source/services/funding-collection" build \
  -o "$funding_bin/funding-api" ./cmd/api
go -C "$funding_source/services/funding-collection" build \
  -o "$funding_bin/funding-worker" ./cmd/worker
go -C "$funding_source/services/funding-collection" build \
  -o "$funding_bin/funding-migrate" ./cmd/migrate
go -C "$funding_source/services/funding-collection" build \
  -o "$funding_bin/funding-import" ./cmd/import
```

The Funding owner chooses and provisions the restricted database/login before
the source window. Bind preparation to the lead-retained manifest without
changing or copying its captures:

```sh
funding_output="$funding_runtime_dir/bundle-$funding_suffix"
"$funding_bin/funding-demo" prepare --as-of now --out "$funding_output" \
  --database-name "$funding_database_name" \
  --underwriting-database-name "$underwriting_database_name" \
  --underwriting-manifest "$actual_upstream_manifest"
funding_manifest="$funding_output/manifest.json"
funding_run_id=$(jq -r '.run_id' "$funding_manifest")
funding_run_dir="$funding_output/runs/$funding_run_id"
```

This bundle retains the Underwriting run ID, anchor and candidate customer,
account, card, shortfall and transaction identities. It creates only synthetic
Funding actors/authority and local provider-deposit facts. The owned database
schema and restricted role must already exist. The commands above apply its
guarded mapping and import only service-owned fixture facts. Source the per-run
`runtime.env`, matching upstream URLs/keys from private `upstreams.env`, and
restricted `DATABASE_URL` from private `database.env`. Never print environment
values or read/copy root `.env`.

In separate owned process terminals, source the private environment as shown
above and start:

```sh
funding_runtime_dir="$funding_repo_root/.dev/integration/20261114/runtime/funding"
set -a
source "$funding_run_dir/runtime.env"
source "$funding_runtime_dir/upstreams.env"
source "$funding_runtime_dir/database.env"
export DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL"
set +a
```

Then start the feature-built Funding processes:

```sh
"$funding_bin/funding-demo" serve-provider --manifest "$funding_manifest"
"$funding_bin/funding-api"
"$funding_bin/funding-worker"
```

The provider double binds `8091`, the API binds `8085`, and the worker has no
listener. The coordinator's `8090`/adapter `8092` and Underwriting's
`8083`/source `8084` remain with their owners. Run the host's saved
case/evaluation pair:

```sh
"$funding_bin/funding-demo" run --manifest "$funding_manifest" \
  --api-url http://127.0.0.1:8085 --profile abundant \
  --evaluation-id "$evaluation_id" --originating-case-id "$coordinator_case_id"
```

The runner rejects mismatched case/evaluation, source identities, policy, mode,
database target or expired evidence. Its output reports exact origin,
evaluation, offer, bridge and operation feed references returned by the APIs.
Provider acceptance remains false; no live consent or provider movement is
claimed.

## Standalone component-process matrix

For the separately labeled component fixture, run these `go -C` commands from
the assigned Funding worktree root. The pre-merge shared actual-profile path
above runs feature-built binaries from MAIN instead. These commands remain a
planned matrix; they were not started in this source checkpoint.
For Funding API, worker and `demo run`, source the per-run `runtime.env`, then
source `upstreams.env` and `database.env` from the private funding runtime
directory. `upstreams.env` supplies only the Underwriting/shortfall URLs and
service keys that match the upstream listeners; it overrides the generated
placeholder reader keys. `database.env` supplies the restricted
`FUNDING_RUNTIME_DATABASE_URL` as `DATABASE_URL`. Keep both private, do not
copy root `.env`, and never print the environment. In each process terminal:

```sh
funding_runtime_dir="$funding_repo_root/.dev/integration/20261114/runtime"
funding_runtime_dir="${funding_runtime_dir}/funding"
set -a
source "$funding_run_dir/runtime.env"
source "$funding_runtime_dir/upstreams.env"
source "$funding_runtime_dir/database.env"
export DATABASE_URL="$FUNDING_RUNTIME_DATABASE_URL"
set +a
```

### Provider double

```sh
go -C services/funding-collection run ./cmd/demo serve-provider \
  --manifest "$funding_manifest"
```

Binds `127.0.0.1:8091` with fixed synthetic credentials and persisted
provider facts only.

### Funding API

```sh
go -C services/funding-collection run ./cmd/api
```

Binds `127.0.0.1:8085` and uses bounded runtime SQL settings.

### Funding worker

```sh
go -C services/funding-collection run ./cmd/worker
```

Runs one serial worker. In DEMO, its real adapter calls the local double.

### Underwriting API and detail source

See [Underwriting local](underwriting-local.md) for the API at
`127.0.0.1:8083`. The `serve-shortfalls` command above binds the detail source
at `127.0.0.1:8084`.

The generated provider double handles adapter login, transfer create/list/read,
balance, payment and deposit reads, plus DEMO reversal facts. A private
control key can settle a provider deposit or reverse a mock transfer. These
controls persist mock provider state only. They never call the funding API or
write the application ledger. The first eligible collection-fee create can
persist the transfer and drop its response; later lookup uses the saved
request ID and survives process restart. Unknown or pending provider outcomes
remain holds; there is no success override or reset command.

The Dockerfile is built from the repository root so it can copy the funding
module and the pinned public underwriting module explicitly:

```sh
docker build -f services/funding-collection/Dockerfile \
  -t flowcard-funding-demo:local .
```

The verified joined journey used standalone binaries. Container image build
and container startup were not exercised by that proof; they remain separate
packaging checks.

The source-level 12B runner uses authenticated actor/API calls and the real
worker/adapter. The component profile specifies $7,000 principal, 30 days,
$190 selected fee, $100 then $7,090 eligible deposits, fee-first collection,
and final $7,000 principal collection. It checks zero debt, backing and holds
on closure, and that no balance beyond the saved debt is collected. For a
different saved evaluation it derives principal, term, fee and second deposit
amount from the saved assessment. TIGHT requires explicit operator approval
and then returns `SCARCE_CAPITAL_RANKING_UNSUPPORTED` before reservation or
provider create. These are source checks only; no runtime journey was executed.

## Readiness, recovery and integration handoff

`GET /health/live` is process liveness. `GET /health/ready` reports connected
database name, migration readiness, policy ID/hash/pricing binding, mode,
provider configuration, fixture-only status, supported regimes and controlled
blockers. The runner compares the connected database name and policy binding
to its manifest before any actor command. A ready local fixture never sets
`provider_acceptance_verified=true`. DEMO mock creates work with
`SERVICING_LIVE_DISPATCH_ENABLED=false`; SANDBOX creates remain disabled.

The worker is one serial process. After its restart, the database retains
operation leases/journal/commands/ledger and the provider double retains its
own mode-`0600` transfer/deposit facts. The run command polls the same saved
operation and request identity. Inspect manifest/run ID, permissions, source
binding, runtime mode, connected database name, migration checksums, open
holds and provider-state metadata. Keep the old run and database intact.
Unknown movement/settlement stays held for review; use only the versioned
reconcile/review routes. Do not update source snapshots, truncate rows, edit
evidence, reset state, or mark a provider action complete by hand. Existing
Node call paths remain under the separate Task 14 one-writer cutover gate.

For actual cross-service presentation, the lead creates private files under
`.dev/integration/20261114/runtime/funding/`, including a complete `runtime.env`
(mode `0600`) and a nonsecret `manifest.json`. Read only the necessary fields
from the private `funding/upstreams.env` at execution; never print or mail
secret values. Keep the source binding `flowcard-integration-20261114`, use
the selected actual Underwriting manifest refreshed within five minutes, and
retain identical IDs and anchor across services. The API ports owned by other
teams must be inspected and left in place. A real saved evaluation and
coordinator case ID are supplied by the host. Any returned origin/evaluation/
offer feed references are copied from actual responses, never predicted.

## See also

- [Funding service README](/services/funding-collection/README.md)
- [Funding design](/docs/designs/funding-and-collection.md)
- [Funding API contract](/docs/api/funding-and-collection-contract.md)
- [Implementation plan](/docs/plans/20261114-funding-and-collection.md)
- [Underwriting local runbook](/docs/demo/underwriting-local.md)
- [Shortfall local runbook](/docs/demo/shortfall-detection-local.md)
