# Shortfall detection implementation plan

Finalized plan for the hackathon demo. Build a small Go service that receives
Airwallex declines, records current USD shortfalls, and exposes observations
for later underwriting. Local implementation and G verification are complete,
and the lead accepted the fixture implementation after independent review.
Live provider-contract acceptance remains unmet. Ramil subsequently authorized
local commit/merge into develop and shortfall worktree cleanup. This completed
execution plan is archived separately from the original planning input.

[TOC]

## Scope and completion

- Follow [the design](/docs/designs/shortfall-detection.md).
- Create `services/shortfall-detection` from local `develop` in an isolated
  worktree. Preserve current local services, data, and unrelated worktrees.
- Run separate API and worker commands, sharing one PostgreSQL schema.
- Support the existing tested Node subscription first, initial wallet-funded
  USD purchase authorizations declined for `INSUFFICIENT_FUNDS` only.
- Calculate current need, not the historical balance at decline.
- Exclude underwriting, offers, transfers, payment retry, repayment, UI work,
  automatic resolution, an event publisher, and new cloud infrastructure.
- Finish with a reproducible local signed-webhook-to-read-API demonstration.
  Fixture success and live provider compatibility are separate evidence.
- No commit, push, subscription change, deployment, or Node cutover is implied
  by this planning request.

## A. Establish the reusable provider contract

- [x] A1. Inspect the existing receiver, signature verifier, issuing client,
  balance client, mapping queries, and retained Node webhook fixtures.
  Starting sources are `server/src/http/routes/airwallex-webhook.ts`,
  `server/src/webhooks/verify.ts`, `server/src/webhooks/process.ts`,
  `server/src/airwallex/client.ts`, `server/src/airwallex/issuing.ts`,
  `server/src/policy/kit5-funding.ts`, and `tests/server/webhooks.test.ts`.
- [x] A2. Add `docs/api/shortfall-provider-contract.md` and sanitized fixtures
  under `services/shortfall-detection/internal/provider/testdata/`.
  Record event name, subscription version, exact JSON paths, account scope,
  canonical authorization ID resolution, retrieval URL, pagination, amount
  sign/units, USD cash selection, and required read permissions.
  Distinguish a recorded payload from a synthetic test fixture.
- [x] A3. Reuse the working subscription without a version upgrade. Do not
  invent `data.id` semantics or assume webhook ID equals transaction-event ID.
  Resolve the documented retrieval-path discrepancy before using that client.
  Missing evidence holds that parser as unsupported; fixture development can
  continue, but live readiness cannot pass. Use connected documentation when
  available, otherwise official Airwallex references linked in the design.

Acceptance: one explicit mapping from a supported delivery to canonical
authorization facts. No live payment simulation is required for this task.
If the repository has no recorded decline payload, report that evidence gap
precisely rather than calling generic JSON acceptance a tested decline parser.

Task A inspection/artifacts reviewed and approved by the lead. See
[the provider contract](/docs/api/shortfall-provider-contract.md).
No recorded modern decline or actual subscription version was found.
Live parser acceptance remains unmet. Synthetic fixtures use an external mock
resolver input, with no invented provider reference paths. No subscription
was changed. B through G are complete and reviewed for the local fixture scope.

## B. Create service and persistence

- [x] B1. Add `go.mod`, `cmd/api`, `cmd/worker`, `cmd/migrate`, and
  `cmd/replay`.
  Use existing Go/PostgreSQL libraries and repository conventions; reuse
  approved dependencies without introducing new third-party dependencies.
  Organize code into `internal/config`, `http`, `provider`, `domain`,
  `postgres`, and `worker`. Avoid a plugin framework or generic job system.
- [x] B2. Add `internal/postgres/migrations/001_shortfall.sql` and explicit
  migration execution. Runtime processes never execute DDL.
  Use the existing database with a dedicated `shortfall` schema/runtime role.
  Grant narrow SELECT access to existing mapping tables.
- [x] B3. Create these tables and constraints:
  `webhook_inbox` stores source/environment/event identity, protected bytes,
  SHA-256, event name, processing state, failed-attempt count, next attempt,
  lease token/expiry, sanitized reason, and timestamps.
  `inbox_conflicts` preserves differing bytes/hash for the same inbox identity.
  `detections` stores normalized references, classification, amounts, provider
  and observation times, and outcome/reason. `inbox_detections` links evidence.
  Unique inbox key: environment/source/webhook ID. Unique detection key:
  environment/account/canonical authorization-event ID. Amount columns use
  integer cents; absent amounts remain NULL. Add queue and cursor indexes.

Acceptance: migrations run twice safely, runtime cannot mutate mapping tables,
and database constraints prevent duplicate inbox identities and detections.
Use a disposable logical test database, never reset the manual demo database.

Task B source reviewed and approved; local runtime acceptance passed in G.
The privileged migration creates a restricted NOLOGIN runtime role and grants
SELECT on existing public mapping tables without mapping DML. Runtime login
credentials/membership are operator provisioning. B created unavailable
entrypoints; E implemented worker/replay and F completed API wiring.
Runtime verification is recorded in G. Existing onboarding Go and pgx
versions are retained. A remains inspection/artifacts complete;
live parser acceptance is still unmet. The fixture resolver is confined to the
local test harness and will not bypass mapping or classification.

## C. Implement direct webhook receipt

- [x] C1. Add POST `/webhooks/airwallex/shortfalls`, preserving original bytes.
  Limit actual streamed input to 10,000,000 bytes inclusive, including chunked
  requests. Require one timestamp and one signature header, rejecting
  duplicates.
  Port HMAC-SHA256(timestamp string + raw body), hex decoding, constant-time
  comparison, and signature-first verification.
- [x] C2. Parse timestamp as Unix milliseconds. Reject missing, malformed,
  overflowing values or values more than 300,000 ms ahead of the server clock.
  Accept exactly five minutes ahead and all older valid signed timestamps.
  Require an object envelope with nonempty string `id` and `name`.
  Select source/environment from server configuration, never request data.
- [x] C3. Persist before acknowledgement. Return 200 `{"received":true}` for
  new and identical duplicate deliveries, 401 `invalid_signature`,
  400 `invalid_event`, 413 `payload_too_large`, or 500 `inbox_unavailable`.
  Store changed-hash conflicts durably before returning 200, hold the original
  unprocessed item, and never overwrite a completed detection. If completion
  raced with conflict receipt, retain both audit facts for review.
  Store valid unrelated events and mark them ignored during processing.

Acceptance: signature verification uses raw bytes; acknowledgement never waits
for a provider call. No credentials or raw payloads appear in logs.

Task C source reviewed and approved. Local functional acceptance passed in G.
Receipt stores all valid envelopes without provider access. A READ COMMITTED
transaction identifies the durable original under row lock, records changed
bytes, and atomically clears leases while holding uncompleted conflicts.
Already committed results remain PROCESSED with conflict evidence attached.
Runtime uses the B column grants. No migration or permission changes were
needed. F completed network listener/config wiring; no additional runtime
placeholder or fixture bypass was introduced.

## D. Resolve facts and calculate the observation

- [x] D1. Implement the single verified version/event-family adapter from A.
  Read authoritative facts when missing. Follow pagination when resolving an
  authorization from a parent transaction. Require one unambiguous canonical
  event; never select by latest timestamp, amount, or merchant alone.
  Keep webhook, event, transaction, and lifecycle IDs distinct.
- [x] D2. Join active `issuing_card_bindings` to active `customer_roster` by
  customer ID. Require binding account, roster connected account, and verified
  provider scope to agree. Missing/inactive mapping is `MAPPING_UNAVAILABLE`;
  ambiguous/conflicting mapping is `MAPPING_CONFLICT`. Do not auto-enroll.
  Obtain the account scope from verified webhook/mapping evidence before any
  account-scoped GET; never search arbitrary provider accounts.
- [x] D3. Require initial PURCHASE AUTHORIZATION, declined specifically for
  `INSUFFICIENT_FUNDS`, with USD transaction and billing currency and verified
  wallet funding. Known unsupported cases are ignored; missing facts are held.
  Normalize the provider's documented outgoing-amount sign once in the adapter.
  Do not apply an unconditional absolute value to refunds or unknown categories.
- [x] D4. Port `GET /api/v1/balances/current?account_type=cash` with trusted
  `x-on-behalf-of`. Require exactly one applicable USD cash available balance.
  Use exact decimal decoding for numeric or documented string values, validate
  cent precision/range, and require positive attempt/nonnegative balance.
  Compute `max(attempted_amount_cents - available_balance_cents, 0)`.
  Never subtract reserved funds again or turn missing/error balances into zero.
- [x] D5. Persist final `SHORTFALL_DETECTED`, `NO_CURRENT_SHORTFALL`, or
  `IGNORED` observations with reasons and evidence times. Keep unresolved work
  on HELD/RETRY_WAIT inbox records, never an immutable observation.
  For eligible resolved results,
  store request/observation UTC times and all three amount fields.
  If verified existing lookup data shows this payment is no longer actionable,
  ignore it before the balance request. Ambiguous outcome ordering is held.
  No automatic later reconciliation or extra event subscriptions in v1.

Acceptance: USD 9,000 attempted and 2,000 available produces 700,000 cents;
equal/excess balance produces zero; missing balance produces no amount.
Another notification for the same canonical attempt reuses the stored result.

Task D source reviewed and approved; local functional acceptance passed in G.
D1 uses the approved unsupported-live/reference-injection boundary from A,
not a verified live parser. Mapping and documented-shape provider HTTP reads
are implemented independently. Funding/sign evidence uses a separate trusted
dependency, with no runtime bypass. D5 provides observation persistence and
canonical lookup helpers; E wired them into fenced completion and checks
for stored results before balance calculation. No persistence function runs
outside its caller's transaction. Differing USD billing/transaction values
are conservatively held. Test functions, builds, DB execution, and the final
local journey are recorded in G. Live parser acceptance is still unmet.

## E. Process durably with bounded retries

- [x] E1. One worker, one in-flight job, one-second polling. Claim with
  `FOR UPDATE SKIP LOCKED`, issue a 60-second lease, then commit. Use a
  45-second job deadline and ten-second HTTP timeouts. No network in DB
  transactions, heartbeat, broker, or second detection-lease mechanism.
- [x] E2. Before calculation, check for an existing canonical detection.
  Commit result, evidence link, and inbox completion atomically, requiring
  the current unexpired inbox token. A unique-key race attaches the losing
  delivery to the first committed result. It must not overwrite that result.
- [x] E3. Retry network/timeouts, 429, 5xx, and lookup 404 up to eight failed
  attempts, using 5/10/20/40/80/160/300-second delays with 20 percent jitter.
  Honor valid `Retry-After` minimums. Refresh auth once per attempt if needed;
  repeated 401/403 holds as `PROVIDER_AUTHORIZATION_FAILED`.
  Exhaustion holds with `last_reason` equal to
  `RETRIES_EXHAUSTED:<original-sanitized-code>`. Persist next-attempt time.
  Recover expired leases after crash. Count failures separately from claims.
- [x] E4. Add `cmd/replay --inbox-id ID --reason TEXT` for HELD items only.
  Use the restricted DB connection. Preserve payload/identity, append replay
  reason/time to an audit field, reset failed-attempt count, and enqueue again.
  Refuse conflicted, completed, or currently leased items. Replaying never
  bypasses mapping/classification checks or changes an existing observation.

Acceptance: restart resumes work; stale tokens cannot complete; duplicate
delivery cannot produce a second observation or recalculate a completed one.

Task E source reviewed and approved; local functional acceptance passed in G.
F included
the requested exhaustion-cause preservation correction, using existing storage.
Lead-selected deviation: unresolved evidence/errors stay on HELD/RETRY_WAIT
inbox rows, including when canonical identity is known. No immutable UNRESOLVED
observation is inserted or reused as a completed result. Replay after correction
repeats resolution/calculation; finalized detections remain immutable. Original
protected payloads remain durable, without a separate partial-lookup snapshot.

Worker reserves five seconds of its 45-second deadline for DB disposition,
after at most 40 seconds of resolution. All completion/link writes use a locked
current token and a final database-time expiry check. DB failure never completes
work; if a retry state cannot be persisted, lease expiry enables recovery.
Worker/replay use the restricted runtime DB connection. Runtime explicitly uses
UnsupportedReferences with no harness evidence dependency. Provider/config
wiring and authenticated reads are completed in F; verification is recorded
in G.

## F. Expose results and run locally

- [x] F1. Add authenticated internal GET `/api/shortfalls` and
  `/api/shortfalls/{id}` using the existing service-assertion convention with
  a shortfall-specific audience. No browser-facing auth or gateway work.
  Return 401/403 for failed authentication/authorization, 404 for unknown ID,
  and 400 for malformed filters/cursor. Responses use `Cache-Control: no-store`.
- [x] F2. List 25 records ordered by `(created_at DESC, id DESC)`, using an
  opaque validated cursor and optional `customer_id`/`outcome` filters.
  Return `items` and nullable `next_cursor`. Each record exposes `id`,
  `customer_id`, `outcome`, `reason`, `currency`, amount fields, canonical
  references, `created_at`, balance observation times, and
  `requires_recheck: true`. Use null for unavailable fields, never fake zeros.
  Exclude raw bodies, secrets, PAN, and unnecessary personal data.
  Expose final observations only; unresolved HELD/RETRY_WAIT work is inbox-only.
- [x] F3. Add `/healthz` and DB/config-aware `/readyz`. Log only safe IDs,
  state transitions, durations, and sanitized reasons. No metrics platform.
  Document required DB, provider base URL/credentials, webhook secret/source,
  version/environment, and internal-auth configuration in `.env.example`.
  Leave the user's root `.env` untouched.
- [x] F4. Add a local Dockerfile/Compose service definition or override for
  API and worker, following existing configuration conventions. Keep them
  opt-in so existing services retain their ports and behavior. Document migrate,
  startup, signed fixture delivery, inspection, replay, and shutdown commands
  in `docs/demo/shortfall-detection-local.md`. Supply only synthetic credentials
  in examples. No Terraform, AWS workflow, subscription, or live cutover
  changes.

Acceptance: a local signed fixture traverses receiver, database, worker, mocked
provider, and authenticated GET, without changing the manual demo database.

Task F source reviewed and approved. The API returns
final observations only, with HS256 service assertion authorization, validated
filter-bound opaque cursors, explicit null fields, and no-store responses.
Independent liveness stays available through DB/config outages. Readiness
always discloses the unsupported live parser with HTTP 503 and separate local
DB/config flags. No live-adapter bypass is introduced.

Standalone opt-in `compose.shortfall.yaml` uses a fresh task-owned `.env` file
and dedicated restricted database URL, with its own loopback port 8082; the
existing stack/listeners and root `.env` are unchanged. See
[the local runbook](/docs/demo/shortfall-detection-local.md) for bootstrap,
signed receipt, held/replay inspection and the disclosed injected G journey.
G has now completed the local verification below. No live provider operations
or existing-stack changes were performed.

## G. Final verification and handoff

Verification happens after A–F. Use existing tooling and at most three focused
new test functions, each covering related scenarios with table-driven subcases.
Do not expand the historical Node test suite or run unrelated truncating tests.

- [x] G1. Receiver integration test: raw signature and timestamp boundaries;
  10 MB/chunked limit; durable duplicate/conflict and storage failure responses.
- [x] G2. Detector/provider test: canonical resolution and mapping exclusions;
  exact USD calculation including the 9,000/2,000 example; zero versus missing
  balance and unsupported classifications. Use the A contract fixtures.
- [x] G3. DB-backed worker/read-API test: canonical dedup across webhook IDs;
  retry, expired/stale lease, restart and replay; authenticated pagination and
  persisted result. Use disposable database and mocked provider HTTP only.
  Cover corrected replay without UNRESOLVED detection poisoning, and exact
  `RETRIES_EXHAUSTED:<original-sanitized-code>` in this same focused function.
- [x] G4. Run service build, formatting, vet, and those focused tests together.
  Run the local signed-fixture journey and record exact commands/statuses,
  persisted IDs, amount, duplicate result, and held/error example in the
  handoff.
  Stop only task-owned temporary processes. Preserve the user's running stack.
- [x] G5. Review file inventory and confirm no automatic Node funding path is
  invoked. Report contract evidence gaps, live-provider checks not performed,
  and exact future routing/subscription cutover steps. Keep Node ownership
  unchanged until a separately authorized release.

G completed: three focused test functions, 79 subtest nodes / 77 leaf cases,
zero failures or skipped test cases. Service build, vet, gofmt, migration
twice/checksum drift, actual restricted permissions, full injected DB/HTTP
journey, ordinary runtime binary smoke, Compose interpolation and Docker build
passed. Optional golangci-lint could not load Go 1.27.3 with its Go 1.26-built
binary; no upgrades were made. All task-owned processes/databases/logins and
the initially absent test-created runtime role were cleaned with ownership and
dependency guards. Existing services/data/roles were preserved. Review image,
source, test logs and result IDs remain. Final independent review accepted the
local fixture implementation. Local integration is authorized separately from
the original planning scope. Live parser acceptance remains unmet.

The final lead review reported independent successful reruns of service
`go build ./...`, `go vet ./...`, and five selected receiver cases covering raw
signature verification, old signed timestamps, and future timestamp boundaries.
The lead also reviewed the G report, harness ownership/DB guards, receiver/
provider/worker scenarios, combined-log tail, runtime-smoke JSON, environment
diff, and final plan. This was a limited independent rerun, not a rerun of the
entire G suite. The exact receiver test selector was not supplied in the lead
message. Prior A–F source reviews stand. No code or tests were rerun for this
documentation-only bookkeeping.

Done means the local service works with the verified fixture contract, its
recovery behavior is demonstrated, and remaining live enablement prerequisites
are explicit. It does not mean underwriting or provider-funded payment recovery
has been implemented.
