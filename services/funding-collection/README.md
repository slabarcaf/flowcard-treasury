# Funding and collection service

This Go service owns authenticated offers, customer/operator decisions,
funding reservations, servicing ledger facts, and provider operation records.
The reservation path uses the committed public underwriting evaluator and
commits a full-funding intent before any provider submission.

## Configuration

`internal/config.Load` requires `DATABASE_URL`, `SERVICING_MODE`,
`SERVICING_FACILITY_ID`, `SERVICING_SOURCE_BINDING_ID`, separate role keys,
provider mode, and server-side provider credentials. The API and worker derive
policy identity and pricing from the same loaded public policy config, not
environment selectors. HTTP defaults to
`127.0.0.1:8085` and is loopback-only. DEMO requires `MOCK_HTTP` and a loopback
HTTP provider URL. SANDBOX requires `AIRWALLEX_SANDBOX` and fixes the provider
host to `https://api.sandbox.airwallex.com`. Dependency URLs must use HTTPS
except for loopback development endpoints. Request timeout is ten seconds.

DEMO provider credentials are fixed to `demo-client-id` and `demo-api-key`.
If `AIRWALLEX_CLIENT_ID` or `AIRWALLEX_API_KEY` is set to another value in
DEMO, startup fails instead of forwarding inherited credentials to the local
mock. SANDBOX retains server-side credentials, but transfer creation stays
disabled even when `SERVICING_LIVE_DISPATCH_ENABLED=true`.

`AIRWALLEX_WEBHOOK_SECRET` is an optional private webhook signing key. The
servicing webhook route remains unavailable until a generated secret of at
least 32 bytes is configured. Do not place it in a tracked file or root `.env`.

The signed route is `/webhooks/airwallex/servicing`. Its DEMO-only fixture
envelopes use `DEMO_TRANSFER_UPDATED` with
`data.request_id`, or `DEMO_DEPOSIT_SETTLED` with
`data.customer_id`, `data.card_id`, and `data.deposit_id`. Event fields are
wake references; transfer reconciliation loads saved operation scope and
retrieves canonical provider state. Deposit reconciliation resolves the
active account from the roster/card mapping and retrieves the canonical
deposit. Unsupported SANDBOX event extraction is stored for review and never
creates a financial fact by itself.

The coordinator, operator, reader, and DEMO customer keys must be distinct and
at least 32 bytes. Configured upstream service keys follow the same length and
separation checks. Keys and database URLs are excluded from JSON serialization
and are never printed by the commands.

`SERVICING_LIVE_DISPATCH_ENABLED` defaults false. The worker is a separate
binary. DEMO creates use only the fixed synthetic `demo-client-id` and
`demo-api-key` credentials with the loopback mock endpoint; arbitrary inherited
provider credentials are rejected. SANDBOX transfer creation remains disabled
even when the flag is true. The policy JSON hash is pinned in
`internal/underwriting/README.md`. Do not add root `.env` values for this
service.

Funding reservations require fresh payment and A/B balance reads, current
underwriting context, a full hard-rule PASS, exact accepted terms, current
authority, and the local locked ledger projection. An invalid runtime policy
binding or unknown provider fee contract fails closed. DEMO MOCK_HTTP defines
an explicit zero funding-transfer-fee contract. It is not a live provider fee
estimate.

## Deposit-driven collection

The serial worker plans only from a fresh canonical settled external USD
deposit GET and a fresh borrower balance GET. It verifies the current card and
roster mapping, the B-to-A `CONNECTED_ACCOUNT_TRANSFERS` authority, explicit
mandate reference, permitted operation kinds and purpose codes, and available
limits. Fee collection uses a separate explicit
`MOCK_HTTP_ZERO_COLLECTION_FEE_V1` B-to-A quote. Unknown fee or mandate facts
place the customer and existing active bridge on review hold.

One unresolved debit per customer/account holds its deposit budget, source
liquidity, and mandate limit until a fenced settlement or proven-unsent
cancellation. The immutable fee-first operation stores its exact late/contract
fee split or principal amount. Only a final matching transfer GET posts
collection debt, invoice backing release, provider fee, refund payable, and
ledger effects in one transaction. Pending or uncertain reads retain the holds.

DEMO `MOCK_HTTP` may return a normalized `REVERSED` transfer with `reversal_id`,
`reversed_amount`, `reversal_currency`, and `reversed_at`. The worker validates
that proof against the saved transfer and stores one compensating record per
reversal ID. It restores debt and invoice backing only up to verified available
capacity, records any backing deficit, and holds the customer for review. These
fields are a local fixture contract. SANDBOX deposit, return, and reversal
extraction remains unsupported and is held for review. The worker makes no
live provider creates.

## DEMO fixture imports

From the repository root, run an import with an absolute fixture path:

```sh
go -C services/funding-collection run ./cmd/import \
  --kind facility --file /absolute/path/to/fixture.json \
  --database-name flowcard_funding_demo_<owned-suffix>
```

Use `SERVICING_MODE=DEMO`, `SERVICING_FACILITY_ID`, and an explicit restricted
`DATABASE_URL`. The required database name uses the safe task-owned prefix and
must equal `current_database()`. Imports require migration checksums and
runtime privileges to pass `Repository.Ready`; they do not accept a migration
URL fallback. Never source `.env` for this command. Examples are in
`services/funding-collection/testdata/`.

Import `facility.json` first to bind the configured facility and verify active
existing mappings with a known-empty servicing history. Then import
`receivables.json`, `authority.json`, and `actors.json`. These are synthetic
DEMO records only. They create neither acceptance nor approval decisions and
do not establish SANDBOX consent. Exact producer replay is idempotent;
same-key changed content and older source capture are rejected. Authority
usage counters in files must be zero; later collection code derives consumed
and reserved usage from persisted operations across authority versions.
Collection authority imports include `product_code`, `mandate_reference`, and
`permitted_purposes`; a matching mandate source reference is required.

## Local DEMO preparation and run

From the repository root, run:

```sh
go -C services/funding-collection run ./cmd/demo prepare \
  --as-of now --out "$(pwd -P)/.dev/funding-collection" \
  --database-name flowcard_funding_demo_<owned-suffix> \
  --underwriting-database-name flowcard_underwriting_demo_<owned-suffix>
```

The command creates a new, fresh synthetic run under ignored `.dev` and prints
its run ID and manifest path. Database names must use the displayed owner
prefixes and contain only lowercase letters, digits and underscores. Generated
mapping SQL checks the exact funding database with `current_database()`.
Preparation does not connect to a database or start a service. Generated
`runtime.env` and `provider-state.json` files are private mode `0600`. The
manifest contains no provider credentials. Never copy the root `.env` into
the run.

The default component bundle contains actual-policy-checked `abundant` and
`tight` underwriting inputs. To bind preparation to an owner-prepared actual
Underwriting input manifest, add `--underwriting-manifest /absolute/path`.
The Funding bundle retains that manifest's run ID, anchor and customer/payment
identity without changing or copying its captured files. Both paths preserve
known-empty Funding opening books and generated `current_database()` guards.
New manifests use the current private fixture schema. If an older ignored
bundle is present, choose a fresh `--out` path and leave the old files intact.

Task 12B adds `demo run --manifest PATH --api-url URL`. It finds the unique
fresh saved assessment for the prepared customer/shortfall/term, or accepts
`--evaluation-id` and a host-supplied real `--originating-case-id`. It rejects
a mismatched database, mode, policy, identity or expired source. It submits
synthetic customer/operator decisions through authenticated API routes and
waits for the already-running serial worker and local HTTP adapter. It never
starts services, performs DDL, writes ledger outcomes or resets state. See the
[staged local runbook](/docs/demo/funding-and-collection-local.md) for
the command matrix. The provider double listens only on `127.0.0.1:8091`,
fails if that address is occupied, persists only its own provider facts, and
never writes funding-ledger success. Its one-time dropped collection-fee
reply is persisted so a later read can recover the same provider request. The
run command posts signed DEMO deposit wake events to the servicing inbox after
provider-double settlement; the worker retrieves canonical deposit and balance
evidence before planning collection.

`/health/live` confirms only that the HTTP process is alive. `/health/ready`
checks the funding database name against the prepared manifest, migrations,
pinned policy and pricing, provider boundary, and upstream configuration.
`provider_acceptance_verified` remains
false for the local double. DEMO mock creates can run when runtime
prerequisites pass even though `SERVICING_LIVE_DISPATCH_ENABLED=false`;
SANDBOX creates remain blocked.

## Focused tests

The service tests use the standard Go test runner and synthetic HTTP providers.
The acceptance scenarios each own a fresh disposable database:

- `TestUnsentFundingCancellation`: release backing after a proven unsent
  cancellation, then reserve fresh accepted terms for the same payment.
- `TestFundingRecovery`: competing reservations and recovery of an uncertain
  create by the restarted worker, using the original request identity.
- `TestCollectionReversalAndAccrual`: fee-first allocations, verified fee and
  principal reversals, persisted daily accrual and idempotent catch-up.
- `TestServicingJourney`: funding through repayment and the final public read
  models, using the real API, worker and HTTP adapter.

Typed setup and HTTP helpers live in `internal/acceptance/fixtures_test.go`,
worker-driving helpers in `worker_fixture_test.go`, and the shared two-deposit
repayment setup in `repayment_test.go`. Each scenario can run independently.
The terms/guards and HTTP contract tests cover pricing, policy refusals,
authentication, actor decisions and command replay.

Additional focused cases cover changed collection capacity, ambiguous lookup
without resend, midnight cancellation, immutable accrual correction and the
five-input self-reservation projection. Demo-command tests exercise standalone
provider reads and both preparation modes, including the system audit actor
and invoice provenance required by the real guards.

Run the pure terms/guards test without PostgreSQL, from the repository root:

```sh
GOPROXY=off GOSUMDB=off go -C services/funding-collection test ./internal/domain
```

For HTTP and acceptance tests, set `FUNDING_TEST_ADMIN_URL_FILE` to a private
file containing a loopback PostgreSQL maintenance URL. The existing database
helper creates and cleans only its uniquely named, marked databases and logins.
Run the suite serially, or select a single scenario with `-run`:

```sh
GOPROXY=off GOSUMDB=off go -C services/funding-collection test -p 1 ./... -count=1
GOPROXY=off GOSUMDB=off go -C services/funding-collection test ./internal/acceptance -run '^TestFundingRecovery$' -count=1
```

## Browser gateway DEMO delegation

The browser gateway can bind invited real users to existing DEMO actors after
an administrative verification. `GET /api/servicing/actor` authenticates the
current ACTOR with its existing per-role key and returns only its internal
actor/type/customer, mode/facility/source binding and fixture-only installation
flag. Broad reader/system subjects cannot use this self-read. Customer mapping
must exist and be active. No provider IDs or credentials are returned.

An optional signed `binding` assertion field narrows actor ID/type/customer and
mode/facility/source binding. Verification still derives authority from the
actual authenticated subject, key class and current active registry. A
restriction mismatch fails closed; constraints cannot grant authority. Existing
CLI callers without these additive restrictions remain compatible. Gateway
requests always carry the verified restrictions, so actor reassignment does
not silently rebind a browser user.

Offer create/read DTOs additionally expose actual `source_mode` and
`customer_id`, enabling verified gateway indexing against the owning source.
They introduce no client-selected customer authority or policy change. All
existing exact terms/hash/version/idempotency, live delegation, provider,
worker, financial guard and ledger contracts remain in force.
