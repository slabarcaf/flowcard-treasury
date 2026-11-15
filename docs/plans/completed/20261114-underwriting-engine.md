# Underwriting engine implementation plan

Implement the separate Go hard-rule service described in
[the design](/docs/designs/underwriting-engine.md). The accepted design and
contract copies are being executed in the dedicated worktree; live integration,
deployment, and publication are outside this plan.

[TOC]

## Scope and execution

Create `services/underwriting-engine` in a fresh worktree from local develop.
Preserve existing local instances, manual data, other worktrees, consent
backlog, and untracked plans. Do not copy root `.env`. No dependency additions,
Node/frontend policy cutover, provider/model calls, UI, reservations, funding,
or AWS changes. Use the current Bridge Capital policy with the explicit
refinements in the new design. Verification runs at the end.

Ramil selected a prepared synthetic fixture demonstration as the first
deliverable. Build a working interactive local service around those fixtures;
continuous ingestion for actual customers remains a separate milestone.

## Implementation handoff contract

Read the [pinned DTO/API/demo contract](/docs/api/underwriting-contract.md)
before editing code. Its field names, error codes, command arguments and
examples are part of this plan. Ramil confirmed total-debt capacity: include
principal, unpaid fees and active reservation principal/fees, then the new
principal and fee. Keep facility cash capacity based on principal commitments.

No implementer policy/schema choices are deferred to a later group. If a pinned
contract cannot be implemented as written, report the concrete discrepancy at
that group's review. Routine naming/helper organization may follow Go style.
The prepared DEMO source is the intended working implementation in this phase.
Unavailable live connectors must not be represented as completed live feeds.

Required file placement, relative to `services/underwriting-engine/`:

| Files | Responsibility |
| --- | --- |
| `cmd/api/main.go`, `internal/config/config.go` | Runtime config, API start/shutdown |
| `cmd/migrate/main.go`, `internal/postgres/migrator.go` | Explicit numbered migration |
| `cmd/import-snapshot/main.go`, `internal/snapshots/import.go` | Typed validation/publication |
| `policy/types.go`, `policy/features.go`, `policy/money.go` | Public normalized input and derived values |
| `policy/evaluate.go`, `policy/config.go`, `policy/policy.v1.json` | Pure rules and pinned defaults |
| `internal/domain/contracts.go` | Snapshot/API wire DTOs and errors |
| `internal/postgres/snapshots.go`, `evaluations.go` | Append-only inputs, reads, saved-key replay |
| `internal/shortfall/client.go` | Authenticated bounded dependency read |
| `internal/evaluation/service.go` | Orchestration without HTTP inside DB locks |
| `internal/http/auth.go`, `server.go`, `cursor.go` | Routes, auth, validation, allowlisted reads |
| `cmd/demo/main.go`, `demo/fixtures/` | Prepare, serve-shortfalls, evaluate commands |

Public entry point: `policy.Evaluate(input, config)` returns a typed result.
Pass the evaluation instant and all financial facts in `input`; no clock,
HTTP/SQL or environment access inside the pure package. Config loads embedded
JSON once; its hash comes from the exact embedded bytes. Formatting a changed
policy file changes its hash and creates an explicitly different policy build.

Execution order A–F is sequential. Pause after A–E for independent lead review,
then run final verification in F. Keep source uncommitted until final review
and further instruction. The future funding implementation imports public
policy types rather than copying rules or importing another module's internals.

## A. Pin the contracts and policy comparison

- [x] Read AGENTS and the design, current product policy/requirements,
  `src/features/bridge-capital/policy/`, `shared/policy/`,
  `server/src/underwriting/`, existing mapping migrations and shortfall HTTP
  auth/read DTOs. Record current branch/base and independent inputs.
- [x] Verify the supplied `docs/api/underwriting-contract.md` against this
  design and existing shortfall DTO/auth. The field, error, freshness, scope,
  import and idempotency requirements are implementable as written. Record the
  existing input-source inventory and distinguish preserved synthetic DEMO
  snapshots from live connector delivery. No fake live mappings; keep missing
  receivable-date integration explicit.
- [x] Include a comparison table: product baseline versus older server policy;
  resolve to the new design, preserving older paths. Record Ramil's confirmed
  principal-plus-unpaid-fees-plus-reservations line exposure, near-zero history,
  amount-based receivable coverage, unknown categories and default event dates.
  Update the requirements note to say this is the proposed service path, not a
  completed UI cutover.
- [x] Pin sanitized DEMO fixture identities and expected traces for Tier B
  eligible, capacity failure, and missing-data review. Add the minimal fixture
  manifest plus matching shortfall and facility templates. Use $7,000
  principal/30 days/$190 fee/$7,190 coverage examples. Task E adds the complete
  relative-date history and runnable preparation path.
  Specify presenter-ready names `eligible`, `capacity_exceeded`, and
  `missing_receivable_dates`, with expected rule traces. Task E prepares their
  complete runnable data pack; these are deliverables, not only unit fixtures.

Done when implementers can identify every rule input, owner, time window and
failure state without inventing a source. Report for lead review before B.

## B. Module, schema and trusted snapshot import

- [x] Add `go.mod`/`go.sum` using current approved Go/pgx versions. Add
  `cmd/migrate`, `cmd/import-snapshot`, and explicit import configuration.
  Reuse patterns from shortfall, not cross-module internal-package imports.
- [x] Add `cmd/api` with runtime configuration and server startup in D,
  alongside the authenticated HTTP routes and evaluation orchestration.
- [x] Add `internal/postgres/migrations/001_underwriting.sql`: immutable
  customer/facility snapshot tables and immutable evaluations with normalized
  input copies, source versions, policy hash, result, canonical request hash,
  mode/caller/key. Add uniqueness, positivity/null constraints and indexes.
  Separate privileged migration command from restricted runtime access.
- [x] Implement import schemas in `internal/snapshots`: validate all IDs,
  date/amount/state consistency, duplicate rows, coverage declarations,
  source mode and source references. Missing groups must be explicit, not
  silently empty. Validate active roster/account linkage through SELECT only.
- [x] Import CLI takes `--kind customer|facility --file PATH`. It atomically
  publishes a monotonically increasing version; identical explicit source
  version/hash is idempotent, changed content conflicts. Reject stale captures
  replacing newer ones. Log safe IDs/status only; no raw snapshots/credentials.
- [x] Provide checked-in schema examples with required/nullable fields,
  including complete empty histories, lifetime-default completeness, active
  reservations, unpaid receivables and allocated portions.

Done when invalid import cannot become current, earlier versions remain
readable, and runtime cannot mutate foreign-service data. Source acceptance
here; migration/permissions execution is in final F. Lead reviews before C.

## C. Pure features, money and hard-rule evaluator

- [x] Add public `policy/policy.v1.json` embedded config and typed loader:
  policy ID/hash, history/tier thresholds, 24-hour/five-minute ages, five-second
  future tolerance, term 7–90, prices, regime boundaries and category mapping.
  `LoadConfig` rejects invalid config; D wires it to API startup. No per-request
  override or extra policy.
- [x] Implement public `policy` normalized input/result types and money/date
  helpers, with service-only DTOs in `internal/domain`. UTC dates,
  month-end-clamped anniversaries, complete trailing 90-day windows, external
  settled inflows/three, near-zero days, late/default windows, principal owed
  plus unpaid fees and unduplicated principal/fee reservations. Add proposed
  principal plus fee for post-funding capacity. Use overflow-safe int64 cents.
- [x] Implement tiers in design order. Known recent defaults block; unknown
  tier-defining facts produce review. Keep missing distinct from false/zero.
- [x] Implement every design rule as PASS/FAIL/UNKNOWN with operands,
  thresholds and source references. Full-or-zero request only. Preserve all
  evaluable rule results; known FAIL dominates UNKNOWN, otherwise unknown or
  human-approval gate yields REVIEW_REQUIRED, otherwise ELIGIBLE.
  Implement the exact predicates in the design catalogue, not rule-name-only
  placeholders. Preserve the worked $19,190/$20,000 capacity PASS and
  $21,190/$20,000 FAIL, plus missing due-date UNKNOWN, in final verification.
- [x] Implement fee estimate, invoice free-amount coverage and facility math.
  Match $7,000/30-day/$190 fee; account for invoices already allocated, exact
  date boundaries and principal/fee distinction. No inference from notes.
- [x] Return tier/headroom/amounts only when known, always
  `funding_authorized:false` and `requires_recheck:true`. Preserve the policy
  config hash and evaluation UTC instant for audit/reproduction.
- [x] Keep the public evaluator independent of HTTP/DB/provider/agent packages.
  Document funding recheck: fetch verified non-ledger context before its
  transaction, replace exposure/facility/receivable values with locked funding
  state, run the same version/hash, reserve atomically. No duplicate tier/rule
  engine or HTTP under funding locks. Funding implementation stays separate.

Done when evaluation is deterministic given normalized inputs, policy and time.
No DB/network calls in the pure evaluator. Lead reviews before D.

## D. Shortfall client, saved evaluation and authenticated API

- [x] Add `internal/shortfall/client.go` for authenticated internal GET by ID,
  with the existing shortfall audience/subject and configured server secret.
  Five-second limit, bounded response, blocked credential redirects and safe
  errors.
  Verify schema/IDs/customer-account linkage, USD/amount consistency and balance
  observation freshness. Bind the configured source URL to mode; the current
  shortfall DTO has no mode field. Do not invent one or accept caller overrides.
- [x] Add application service orchestration: completed-key lookup first,
  shortfall fetch, same-transaction snapshot selection/copy, pure evaluation,
  atomic immutable decision+key write. No HTTP calls in database transactions.
  Canonical uniqueness race returns its first committed identical result;
  changed request on same caller/mode/key returns 409.
- [x] Add internal HS256 auth with dedicated underwriting audience and
  evaluator subject, existing short TTL convention, fixed 401/403 behavior.
  No browser session integration. Add POST evaluation, GET detail, GET list
  with 25-record opaque cursor scoped to mode and the optional customer filter.
  Omitting `customer_id` lists all customers in the configured mode. No-store
  all responses.
- [x] Implement design's 400/404/409/422/503 outcomes. Missing valid snapshot
  data saves REVIEW_REQUIRED; dependency/storage outages do not save fake
  negative credit decisions. Replays bypass changed dependencies and retain
  original result. Reevaluation uses a new key and decision ID.
- [x] Validate list query/cursor and response allowlist. Exclude private raw
  source documents/secrets. Save rule input evidence for authenticated detail
  without exposing data unrelated to the decision.
- [x] Add authenticated GET
  `/api/underwriting/customers/{customer_id}/context` for the Go coordinator.
  Return bounded normalized rule inputs, current snapshot identity/version,
  mode, capture time, completeness/freshness and source references. Exclude
  raw documents/notes/secrets. Unknown context is 404, outage 503; return stale
  or incomplete context with explicit flags. No coordinator SQL grants or
  evidence/snapshot publication endpoint.
- [ ] Add API regression cases to the planned `TestUnderwritingContract` in F,
  including global/filtered pagination, cursor filter binding and constructor
  rejection of unsafe shortfall URLs.

Done when callers can evaluate by shortfall ID and proposed term, retrieve
the saved explanation, and safely repeat the exact request. Lead reviews.

## E. Prepare the demo fixture pack and local runtime

### E1. Presenter-ready data and repeatable preparation

- [x] Add scenario templates under `demo/fixtures/` in the Go service:
  manifest, customer history/standing, facility, shortfall response, mappings,
  and expected result/rule trace for each of the three named cases. Use
  synthetic identities and explicit complete/unknown fields, never real secrets.
- [x] Make `eligible` demonstrate $7,000 principal, 30 days, $190 fee,
  $20,000 line, $12,000 existing/reserved exposure and $8,000 free receivables.
  `capacity_exceeded` changes exposure to $14,000. The missing-date case
  explicitly marks due-date evidence unverified and keeps other facts equal.
  Ensure Tier B fixtures cannot accidentally qualify as Tier A.
- [x] Add a small Go `cmd/demo` helper using standard library and existing
  service packages to prepare relative-date templates for one run. Accept
  the prepare/serve-shortfalls/evaluate subcommands and exact flags pinned in
  the contract. Prepare uses `--scenario`, `--as-of` (default now) and `--out`.
  Write
  a manifest with run ID/anchor, scenario IDs, policy hash, artifact paths and
  expected results. All source capture/observation dates share that anchor;
  history covers the correct prior 90 dates. Do not relax freshness rules.
- [x] Supply fixture shortfall HTTP through a loopback-only demo helper mode,
  following the actual signed read contract and existing configured client.
  Import customer/facility records through the actual importer. Provision only
  known task-owned mappings in the dedicated demo DB, never shared manual data.
  Avoid runtime rule/result overrides or a special always-approved API path.
- [x] Document preparing a fresh run with new versions/keys, executing each
  scenario through the normal authenticated POST, reading its saved explanation,
  and retrying with the same key to show unchanged results. Keep prior runs;
  no truncate/reset. Clearly label the service output and guide as DEMO.

Done when all inputs needed by the demonstration are supplied by the fixture
pack and the presenter can repeat its three cases with current timestamps.
Functional execution is verified in F after the local setup below is complete.

### E2. Runtime setup and presentation guide

- [x] Wire API listener (default loopback 8083), graceful bounded shutdown,
  request time/body/header limits, liveness and truthful readiness. Readiness
  distinguishes local configuration from source/provider verification.
- [x] Add a service Dockerfile and standalone opt-in Compose definition.
  Reuse the existing PostgreSQL instance with a dedicated disposable demo DB
  and restricted login. Preserve occupied ports/volumes and existing services.
- [x] Add `.env.example` entries only for required DB, mode, HTTP/auth and
  shortfall base URL settings. Fresh ignored synthetic local config only,
  mode 0600; leave root `.env` unchanged.
- [x] Add `docs/demo/underwriting-local.md`: migrate, import the fixtures,
  start an explicit mock shortfall HTTP server or approved local source,
  make a signed evaluation request, retrieve/replay it, inspect rule reasons,
  shutdown. Label synthetic source evidence in API results and runbook.
  Include a short presenter script with exact preparation/evaluation/read
  commands and expected result/reason for each scenario; distinguish prepared
  synthetic data from future continuously refreshed customer sources.
- [x] Document later connector gaps for receivable dates/allocations, lifetime
  defaults, historical balances and live exposure/reservations. No fabricated
  published data, migrations of old Node policy, or automatic funding.

Done when the final verification journey has a reproducible local setup and
clear source-mode limitations. Lead reviews before final tests.

## F. Final verification and handoff

Add at most three focused test functions with two or three representative
scenarios for each critical behavior. Use standard Go tooling and current
fixtures, no new testing framework or exhaustive permutation matrix.

- [x] Policy test: eligibility/capacity and fee/receivable examples; exact
  tier/regime/time boundaries; missing versus known failure; exposure excludes
  duplicate reservations and all-zero balances cannot create Tier A. In the
  existing `TestPolicyDecisions`, add source-independent known-failure cases,
  a 300-day default gate case, and duplicate inflow/invoice plus conflicting
  classification evidence cases.
- [x] Contract test: shortfall validation/auth/errors/freshness; snapshot
  import completeness and mode; idempotency replay/mismatch; global and
  filtered read cursors; unfiltered mode scope; missing/null versus explicit
  zero shortfall values; direct URL safety checks.
- [x] Isolated PostgreSQL journey: actual migrations twice and restricted
  grants, actual snapshot CLI/import, real API/auth and mocked shortfall HTTP,
  201 ELIGIBLE, INELIGIBLE and REVIEW_REQUIRED examples; completed retry stays
  identical after new snapshot, changed-body key conflicts, concurrent same-key
  requests yield one decision. Verify an admin/privileged DB URL is refused by
  runtime readiness. No shared DB reset or live provider calls.
  Execute the prepared Task E fixture pack, including a fresh run anchor and
  same-run retry. Verify all three expected rule traces through the actual API,
  rather than adding a separate mock evaluator or a fourth test function.
- [x] Run service build, vet, formatting and focused tests together after
  implementation. Validate Compose without printing resolved secrets. Record
  exact commands, IDs, rule traces, fixture/source mode and all limitations.
- [ ] Independently review final source and results. Mark completed checks
  accurately. No merge, push, deploy or worktree cleanup without subsequent
  user instruction. Preserve user services/data/reports.

Done means the new Go engine can assess imported structured customer pictures
and shortfalls through the real local HTTP/persistence path. It does not mean
live history/receivable connectors, AI underwriting or capital reservations
have been delivered.

Use three root test functions: `TestPolicyDecisions`,
`TestUnderwritingContract`, and `TestUnderwritingJourney`. Tests use an injected
fixed clock. The journey creates a uniquely named database/login against the
existing isolated PostgreSQL test instance, uses actual Node mapping-table DDL,
and cleans up only its own database/login after task-owned connections close.
Never truncate shared tables or skip an unavailable required DB test as success.

Task F status: all three planned test functions and the full service build/vet/
formatting/Compose checks passed. The isolated PostgreSQL journey used the
existing port 54329 test instance, verified restricted permissions and actual
host/container API/demo flows, and removed only its marker-owned database/login.
The pre-existing PostgreSQL and Docker containers remained running. Independent
F source review by a separate actor was unavailable in the child session; prior
A-E source reviews stand, and no additional F source-review verdict is claimed.
Lead owns final integration.

Required final commands from the service directory:

```bash
go build ./...
go vet ./...
go test ./... -count=1 -v
gofmt -l .
```

Record optional tool availability separately from these required checks.
Demonstration acceptance additionally runs all three actual `cmd/demo`
subcommands, actual import, API process/auth, saved GET and same-key replay.
Run the migration twice in the disposable setup and verify restricted runtime
identity and denied foreign-table writes. Supply exact scenario decision
IDs/statuses/rule reasons and retained artifact paths.
