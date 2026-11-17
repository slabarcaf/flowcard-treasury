# Go coordinator implementation plan

Execution record for the [Go coordinator design](/docs/designs/go-coordinator.md).
Ramil authorized implementation in the isolated `feat/go-coordinator`
worktree. Parent Rootie 3 accepted Groups A through E after static review and
accepted Group F and released Group G. The three focused G scenarios now cover
distinct older late-visible discovery, repository-reconnect dirty-work recovery
and an account/customer-overlap context pin/restore round trip. The automated
wrapper uses synthetic defaults and needs only the local test-admin handoff.
The actual underwriting HTTP continuation used the lead-refreshed retained
run in a separate task-owned DB/profile. Three actual evaluations and fixture
investigations completed before the five-minute cutoff. The scheduler made a
successful shortfall list poll after that cutoff; no new case or evaluation
was added, and both owned listeners have stopped. This freshness deviation is
recorded, not a clean continuous-freshness pass. A later lead-owned joined
DEMO run was independently accepted for source-emitted monitoring, exact
origin links, closure and restart recovery; its provider double recorded
`provider_acceptance_verified:false`. Funding/browser/live acceptance remains
pending, and no model call was made. The Go module files have no new
dependencies.

[TOC]

## Scope and execution discipline

Build `services/go-coordinator` as a separate Go service: one serial scheduler,
PostgreSQL state, narrow AI tools, attributed evidence, saved deterministic
underwriting, bounded collection monitoring and sourced case reads. Preserve
all existing Node behavior and
user data. No funding, UI, CRM mutation or generic agent platform.

Baseline inspected: main checkout `develop` at `6a9a83a`. The underwriting
HTTP implementation is committed at `e608a4556f83dde4c45e51d389d7d24da32835bd`
and merged to lead `develop`; the actual underwriting API and joined local DEMO
slice were exercised in G. This does not verify live decline extraction,
provider acceptance, or live financial integration. This plan follows the
repository's granular task-group pattern.

Follow the repository's Planning Exec group discipline in the same authorized
Agterm session and worktree, and obtain independent parent review before each
release. No subagents. Preserve unrelated work; never copy root `.env`. Runtime
profiles and source artifacts remain under task-owned `.dev` paths.

No new dependencies without approval. Use standard Go HTTP/JSON/testing and
the already approved pgx version. New schema changes use new numbered SQL
migrations, executed explicitly before runtime. Final tests occur only in
group G, with at most three focused test functions and a few representative
scenarios per critical behavior.

## A. Freeze shared contracts and installation choices

- [x] Review source list/detail DTO, descending cursor/filter behavior and
  internal assertion validation in `services/shortfall-detection`. Record
  source binding identity/mode; do not invent a DTO mode or change issuer.
- [x] Agree with underwriting owner on POST/GET response fields needed for
  linkage: saved ID, shortfall, term, snapshot references, policy hash, rules,
  status and immutable funding/recheck flags. Verify idempotency caller scope
  permits coordinator retry with the same subject/key after assertion renewal.
- [x] Record implemented GET `/api/underwriting/customers/{customer_id}/context`
  DTO, dedicated internal auth, version/mode/linkage, bounded normalized inputs
  and freshness/completeness flags. Missing snapshot 404 differs from outage
  503. Stale context stays readable and visibly stale. No underwriting SELECT
  grants or snapshot write API.
- [x] Align the owner-accepted funding change feed and
  bridge/collections/operation reads: commit-ordered per-mode sequence,
  retained replay, aggregate versions,
  safe scope aliases, as-of time, debt buckets and allowed next-action codes.
  Pin `{mode,items,next_after_seq,has_more}`, item sequence/resource/version
  fields, `obligation_id=bridge_id`, nullable provider observations and the six
  recommendation codes in the design. Collection cursor version change is
  409 with one bounded refetch. Guard evidence statuses grant no debit
  authority.
  Keep review/reconcile command routes outside the first tool registry.
- [x] Pin the owner-agreed feed routing: typed bridge ID
  and parent version, exact originating references and atomic parent bridge
  version advancement for recommendation-relevant operation/authority changes.
  The funding draft records the same invariant. Freeze its wire shape in the
  double before dependent wiring; do not infer associations. This agreement
  does not establish a running endpoint.
- [x] Keep server-private raw `account_id` separate from
  `account_ref=customer:<customer_id>`. Validate the trusted customer/source/
  mode/raw account relation and exact origin/evaluation references. Never infer
  raw provider ownership by removing the alias prefix.
- [x] Require a positive operation `version` in operation detail and every
  `bridge.operations` entry. Keep it distinct from the bridge parent version;
  no version guess or new command tool.
- [x] Pin both existing source-binding settings to the owner-approved
  `flowcard-integration-20261114` DEMO identity. Preserve feed checkpoint state
  across a snapshot refresh; a reset book needs a new identity or fresh scoped
  consumer DB.
- [x] Record the approved optional `originating_case_id` field on the existing
  funding offer request as attributed provenance only. Funding owns publisher
  behavior; the exact source-emitted refs were verified in the joined G run.
- [x] Confirm funding read auth uses issuer `flowcard-go`, audience
  `flowcard-funding-collection`, subject `coordinator-reader`, 20-second TTL;
  `SERVICING_SERVICE_SECRET` equals the existing funding coordinator-reader
  secret. The coordinator receives read-only authority.
- [x] Confirm pending-evidence review/export/link contract. Snapshot publication
  remains underwriting's local import CLI. Identify who validates source
  authenticity/financial meaning; absence must produce review, not promotion.
- [x] Review 30-day initial term, fixed budgets, manual waiting disposition
  and excluded automatic reevaluation with Ramil. No need to settle funding
  or live CRM implementation to build the fixture installation.
- [ ] Select/pin a tool-capable Claude model for the proposed Anthropic
  adapter, document data handling and spend approval gates. Keep routine
  execution fixture-only and use no live call to choose a model.

Group A complete: dependency DTO/auth and pending-evidence ownership are
recorded, the 30-day term is approved, and unavailable live capabilities are
labeled. Model selection and live model/data/spend approval remain deferred to
a separate checkpoint. Parent review released Group B. The
coordinator consumes underwriting HTTP and saved results. It does not import
or call the underwriting policy package.

## B. Service skeleton and durable state

Files under `services/go-coordinator`:
`go.mod`, `cmd/api/main.go`, `cmd/migrate/main.go`,
`cmd/replay/main.go`, `cmd/evidence/main.go`,
`internal/config/config.go`, `internal/domain/`,
`internal/postgres/migrations/001_coordinator.sql`,
`internal/postgres/{repository,cases,discovery,runs,evidence,assessments}.go`.

- [x] Reuse approved Go/pgx versions; no shared framework. Define case status,
  source binding, run/revision, typed source/evidence references and controlled
  failure codes. Unknown facts stay nullable, money stays integer cents.
- [x] Add tables/indexes from the design. Case kind SHORTFALL or COLLECTION,
  unique binding/detection or binding/bridge, collection observation history,
  servicing feed checkpoint and investigated-version/trigger deduplication.
  Unique binding/detection and
  case/revision assessment; immutable exact body/key once dispatched. Keep
  model/tool call counts durable, including across crash/replay.
- [x] Add explicit privileged migration command and restricted runtime grants.
  Allow writes only within coordinator schema; all underwriting and servicing
  context arrives through authenticated APIs. Runtime performs no DDL.
- [x] Implement page+cursor transaction, token-fenced case claims/disposition,
  append-only step records and evidence/link audits. No HTTP in transactions.
  Keep secrets out of config fingerprints, schema fixtures and errors.
- [x] Refuse incompatible binding/config reuse. Keep persisted run call counts
  across process restarts and replay.

Done for the bounded persistence interfaces: uniqueness constraints prevent
duplicate cases/revisions, exact assessment requests are pinned before POST,
page checkpoints and case stubs commit together, and leases fence workflow
writes. At the B checkpoint the migration command had not yet been run; G
later verified migrations twice on unique owned databases. Parent then
reviewed and released C; API, replay, and evidence commands were implemented
in later groups.

## C. Discovery and deterministic scheduler

Files: `cmd/api/main.go`, `internal/shortfall/client.go`,
`internal/discovery/poller.go`, `internal/servicing/client.go`,
`internal/workflow/{scheduler,stages}.go`, the bounded repository additions
under `internal/postgres/`, and forward migration `003_scheduler_retries.sql`.

- [x] Add authenticated bounded list/detail client, no credential redirects,
  five-second deadlines, 256 KiB response cap, exact DTO/scope checks.
- [x] Scan `outcome=SHORTFALL_DETECTED` from the head through every returned
  opaque cursor. Commit stubs and next cursor together. Resume after crash;
  invalid cursor restarts at head. Persist source `created_at` on each case
  stub. Never stop at known rows/high watermark.
- [x] Persist diagnostic maximum, tail completion, page success and scan age.
  Tail starts a new full scan after five seconds. Each scheduler poll handles
  one page, then offers a case step; the opaque cursor resumes the full scan.
- [x] Define invalid page versus held-row behavior. Preserve cursor on failure,
  degrade after 60 seconds without success, retry with capped poll delays.
  Ensure continuous discovery under case backlog and case progress under scan
  backlog. No false fixed-latency or exactly-once claim.
- [x] Poll owner-accepted servicing changes ascending from persisted
  sequence; commit
  page updates/checkpoint together. Validate mode, monotonic sequence and
  retained-feed coverage. Deduplicate repeated delivery and never apply an
  older bridge version over a newer one. Persist related shortfall case links.
- [x] Persist `refresh_needed` and target parent version in the same feed-page
  transaction. Clear only after a matching/newer authoritative refresh commits;
  retain dirty work when a newer change arrives. No checkpoint/model limit can
  discard unfinished deterministic refresh work.
- [x] Add collection MONITORING/ACTION_REQUIRED/CLOSED states. Read current
  bridge/collection state before explanation, coalesce intermediate updates,
  reopen only from a newer authoritative state. Model outage/budget exhaustion
  cannot stop deterministic monitoring or invent closure.
- [x] Pin collection run bridge version/allowed actions. Re-read before finish
  publication; fence against latest locally observed version as well as lease.
  Supersede stale runs instead of publishing their recommendations as current.
  New progress invalidates earlier current recommendations without requiring
  a model call or resetting investigation limits.
- [x] Alternate due feeds and case steps fairly. Persist independent feed age
  and checkpoint; stale observations remain visible as stale. Cap collection
  investigations at three per bridge, five minutes apart, on distinct allowed
  trigger/version transitions. Other updates use a deterministic summary.
- [x] Implement states/stage transitions, 45-second work plus five-second
  persistence allowance, 60-second leases and one model call per step,
  three transient failures with 5/20-second jittered delays and Retry-After.
  Do not sleep holding a lease. Shutdown cancels work and fences disposition.
The initial C checkpoint covered source clients, feed fairness, freshness,
checkpointing and deterministic refresh. Collection caps, publication fences,
and operator replay were completed in later C/D/E implementation groups and
included in the accepted G checks.

- [x] Replay only allowed held/interrupted stages with same revision/key and
  remaining call allowance. No counter reset or automatic new evaluation.

Group C final status: recurring full rescans cover late commits; source
clients, durable checkpoints, dirty refresh, fair serial scheduling, bounded
collection reads, retries, lease-fenced refresh disposition, readiness age,
run-version supersession and operator replay are implemented. The accepted
G tests verify representative discovery, dirty-work recovery, and stale
publication fences. The actual joined run verifies source-owned collection
closure and restart recovery; collection-agent narrative coverage remains
separately deferred below.

## D. Context, sources, evidence and bounded AI loop

Files: `internal/context/client.go`, `internal/sources/{manifest,reader}.go`,
`internal/evidence/{service,review}.go`,
`internal/agent/{agent,tools,prompt,anthropic,fixture,limits}.go`,
`internal/agent/testdata/` and `internal/sources/testdata/`.

- [x] Implement A's customer-context HTTP reader, retaining version,
  capture time, completeness and provenance. Missing snapshot is missing
  context; DB failure is retry. Exclude facility and unrelated customer data
  from prompts. No trust inferred from model content.
- [x] Implement selected local document/exported-CRM manifest with trusted
  customer/account/mode linkage, opaque handles, hashes, locators and explicit
  export/source versions. Bound source counts/bytes and mark truncation.
  No arbitrary paths/URLs or live CRM claim.
- [x] Define three shortfall tools (read source, submit pending evidence,
  finish investigation) with host-side scope and input
  validation. No tool for funding, snapshot mutation, generic SQL or browsing.
  Persist tool arguments/results within caps and count denied calls.
- [x] Add collection progress/operation reads and local recommendation recording
  only for COLLECTION runs. Bind handles to case bridge/customer; validate
  recommendation against current source-owned allowed action/version. No
  underwriting tool or funding/review/reconcile command in that registry.
- [x] Bound collection attempt history to two 25-row pages, persist cursor and
  version between substeps, and label truncation/incompleteness. One version
  restart is allowed; subsequent change defers with backoff. Never infer absent
  attempts or authority from a truncated history; debt uses bridge aggregates.
- [x] Record PENDING_REVIEW claims with exact excerpt validation and stable
  local deduplication. Implement review export and snapshot link CLI with
  actor/time/source checks. Linking cannot publish or financially validate.
- [x] Implement the explicit agent loop: typed task/case packet, versioned
  system prompt, case-specific tool registry, persisted call/result history,
  typed finish-tool findings/gaps/next steps, citation validation and host-owned
  stage completion. Include shortfall and collection examples in the runbook.
- [x] Implement a direct Claude Messages HTTP adapter with client tool JSON
  schemas and `disable_parallel_tool_use:true`. Match `tool_result` to
  `tool_use_id`, replay protocol blocks in order and reject plain `end_turn`,
  and accept only a validated finish tool as completion. Force that tool on
  the final turn; reject truncated/refused/incomplete responses. No server
  tools or thinking transcript. Keep Anthropic key server-side.
  No SDK, model-quality claims or live request in routine verification.
- [x] Pin ordinary auto tool choice, one corrective plain-text turn within
  existing limits and forced named finish on the final reserved call. Reject
  multiple/malformed tool blocks before execution. Resolve any saved tool use
  with its matching saved/recovered result before the next model request.
- [x] Add deterministic fixture agent using identical tool dispatch, budgets
  and schema validation. Include useful evidence selection and injected source
  instruction behavior; label every run FIXTURE or live-provider mode.
- [x] Persist call counts before dispatch: twelve model calls/twelve tool calls
  per run, one model call per lease step, 25-second model timeout and 1,500
  output tokens normally (3,000 for forced finish). Retain one call for finish.
  Reserve request space for finish instructions/history. Enforce source/
  request byte limits and record reported usage. No token reservation ledger
  or installation quota. No hidden retries or counter reset after restart.
- [x] Separate system authority from source text, redact irrelevant sensitive
  data, retain protected source hash/excerpt references and sanitize logs.
  Extracted claims remain unverified regardless of confidence/citations.
- [x] Derive timestamp/model/source metadata in Go; validate exact retained
  redacted source sections/locators. Label model prose as AI commentary and
  pending claims separately from saved financial result/next actions.

Done for the bounded fixture path: the agent chooses selected evidence, reports
gaps, and submits attributed claims while the host rejects out-of-scope
authority. Live model and CRM verification flags remain false without separate
authorized evidence. Parent reviewed the D path and released E.

## E. Assessment, safe explanation and internal reads

Files: `internal/underwriting/client.go`,
`internal/workflow/{assessment,explanation}.go`,
`internal/http/{server,auth,cases,health}.go`.

- [x] Persist `{shortfall_id,term_days:30}` and stable caller-scoped key before
  POST. Retry same body/key after ambiguous success; read known decision IDs.
  Validate returned decision linkage/flags. Hold 409/422/schema mismatches;
  outages never become negative credit decisions.
- [x] Underwriting request runs even when investigation finds incomplete facts,
  provided the shortfall contract is valid. Missing data yields engine review;
  no invented source values or coordinator policy branch.
- [x] Save the assessment before model work. Seed investigation with saved
  normalized inputs/rule reasons and source manifest. Fetch customer context
  only when needed; an enrichment/model outage uses a visible fallback without
  blocking the saved assessment. No agent underwriting-request tool.
- [x] Preserve immutable machine result separately from agent narrative. Render
  amounts/status/policy/rules deterministically; validate every narrative
  source and rule citation. Invalid/budget-limited post-assessment explanation
  uses labeled RULE_TEMPLATE and retains the saved decision.
- [x] For collection cases render current source-owned balances, due/DPD,
  attempt state and settlement references separately from agent recommendations.
  Keep pending/unknown movement distinct from settlement. Record pending
  consent/reconciliation tasks without claiming an action was executed.
- [x] Display service-calculated customer exposure/line/headroom and source
  version only. Respect unpaid fees and unfunded principal/fee commitments;
  no coordinator monetary aggregation or converted-reservation double count.
  Agree a missing exposure field with the owner or display unavailable.
- [x] Atomically link result/explanation and set COMPLETED with controlled
  next actions derived from saved rule reasons. Human approval stays
  REVIEW_REQUIRED, never resolved by the agent. No offer/reservation or
  new funding state.
- [x] Expose authenticated no-store list/detail with 25-row scoped descending
  cursor, dedicated reader audience/subject/secret, safe reference allowlist
  and fixed errors. Bind on unused loopback port, default 8084; the accepted
  manual combined profile overrides this to 8090.
- [x] Include safe ordered tool-step summaries in case detail and add
  `cmd/watch --case ID` for a visible terminal demonstration. No raw source
  documents, secrets or thinking in the trace.
- [x] Add liveness and truthful selected-mode readiness, worker/discovery age,
  source/agent modes and separate live verification flags. Do not turn live
  readiness on from configuration or fixture success. Preserve shortfall's
  unsupported-live distinction without breaking an explicit fixture source.

Group E implementation and its focused source corrections were accepted by
parent static review. Offline build, vet, format and whitespace checks passed.
No database or behavior tests, migrations, or upstream calls were run; those
remain in Group G. The group adds no live publisher or model acceptance.

Account identity is now separated in the coordinator: private raw shortfall and
underwriting `account_id` is retained in server-only case state, while
`account_ref` is derived as `customer:<customer_id>` and used for funding,
source manifests, model packets and case API projection. Forward migration 008
backfills this split and holds legacy rows whose scope cannot be proven. A
linked servicing event is accepted only with matching case, evaluation, offer,
customer, alias, source mode and stored raw-account evidence. The optional
`originating_case_id` on the existing funding offer request is owner-approved
provenance, and the DEMO source binding is fixed at
`flowcard-integration-20261114`. Funding publisher/runtime consistency was
demonstrated by the exact local joined-G source event. Actual provider
acceptance remains false; no linking metadata is inferred from a free-form
alias.
Shortfall and collection case IDs use `domain.StableCaseID`; the F helper calls
that same implementation to prepare manifests for new cases. Existing stored
case IDs are not rewritten.

E review corrections also separate immutable agent outcomes from durable case
publication, persist private run context separately from the model packet, and
pin source transaction/card linkage before assessment POST. Forward migration
009 adds private run scope, publication and saved source linkage. Reviewed
001–007 baselines remain untouched. Collection detail is source-version fenced;
follow-up runs require a new actionable transition, not routine monitoring
updates. Worker claims and replay are binding/mode scoped. Group G still owns
behavior, SQL and HTTP proof.

## F. Runnable local packaging and operator documentation

Files: `services/go-coordinator/Dockerfile`,
`services/go-coordinator/.dockerignore`, root opt-in
`compose.coordinator.yaml`, service `.env.example` and `.compose.env.example`,
`cmd/fixture-shortfalls`, `cmd/prepare-sources`, `internal/fixturebundle`,
`internal/agent/fixture.go`, and `docs/demo/go-coordinator-local.md`.

- [x] Package API, migration, watch, replay, evidence, shortfall-adapter and
  source-preparation binaries. Compose is opt-in, uses host networking for the
  approved loopback profile, creates no database/volume, and keeps migration
  separate from runtime. `.dockerignore` excludes `.dev`, env/key/secret files,
  and retains tracked `*.example` templates. No root `.env` copy or dependency
  change.
- [x] Provide exact variable, path, command and port matrices. Keep API,
  source-reader and migration profiles separate and mode `0600`; the main
  upstream handoff remains partial and lead-owned. Blank database/source
  targets fail closed. The runbook documents a unique task-owned logical DB on
  existing PostgreSQL and separate migration/runtime URLs for G; no DB was
  created in F.
- [x] Read the exact retained underwriting run manifest and its
  `shortfalls_file`. The `8092` adapter preserves record IDs, timestamps and
  source linkage, validates the run anchor/provenance, and serves only the
  existing list/detail DTO. No evaluation or funding endpoint is fabricated.
- [x] Prepare source manifests using the shared `domain.StableCaseID` function.
  The helper refuses a nonempty root, preserves prior IDs/artifacts, and adds
  only a labeled synthetic invoice source for the missing-receivable review.
- [x] Document Bloom-A eligible and Bloom-B missing-date journeys in
  evaluation-first order. Fixture mode uses the same durable agent tools; any
  submitted evidence remains `PENDING_REVIEW`, and later facts do not silently
  re-key or rewrite the saved decision.
- [x] Distinguish committed underwriting HTTP from the synthetic `8092`
  source adapter and the funding-owned API/provider double. Group G later
  demonstrated the Funding-owned source-emitted linked event in one joined
  DEMO run. The provider double is not real provider acceptance; no fake
  funding API or event is added.
- [x] Document the source-owned G collection walk: two partial settlements,
  uncertain operation recovery, missing authority and source-confirmed
  closure as source-owned states. The accepted local run confirms the source
  feed/closure slice, not collection-agent narration for each intermediate
  state. The coordinator only reads and recommends.
- [x] Keep routine profile `FIXTURE` and omit Anthropic keys. Document the
  existing Anthropic config variables as gated by separate model/data/spend
  approval; no model call or live-model claim is made in F.
- [x] Document fresh-run handling, exact manifest IDs/anchor, `watch`, replay,
  pending-evidence review, safe task-owned shutdown and live prerequisites.
  No shared reset or automatic reevaluation is provided.

F source and runbook artifacts are prepared for parent review. Offline build,
vet, format and whitespace checks pass. No database, migration, behavior test,
HTTP, model/provider operation or commit was run at the F checkpoint. Parent
accepted F and released G afterward.

## G. Final verification and handoff

Add no more than these three focused standard-library test functions. Use a
few representative scenarios per critical behavior, not exhaustive matrices;
request approval if meaningful coverage truly requires a fourth function.

- [x] `TestDiscoveryRecovery`: one small fixture scan/checkpoint-restart
  scenario. Assert a distinct older late-visible detection is saved once while
  the high-water mark stays put; reconnect the repository between servicing
  checkpoint and refresh claim; verify dirty work remains until detail commits.
  The same scenario preserves `VOID_UNFUNDED` and `REOPENED_HELD` source labels
  and rejects an unknown lifecycle code.
- [x] `TestAgentAuthority`: one useful source-to-pending-evidence scenario,
  with a representative wrong-scope call denied and call-limit failure using
  the saved-result fallback. Pin and restore selected customer context with
  overlapping raw account/customer IDs, and assert semantic packet equality,
  retained verified aliases, and absent private account/card/transaction paths.
- [x] `TestCoordinatorRecovery`: one isolated database recovery scenario:
  remote assessment save/local crash reuses its key/ID; publication rejects an
  expired lease and an older collection base version. Keep setup synthetic and
  assertions limited to these recovery invariants, without a permutation tree.
- [x] At the end run service build, vet, formatting and focused tests with
  independent routine checks concurrently. Validate optional Compose without
  printing resolved secrets. No live model, CRM or Airwallex calls.
- [x] Verify migrations twice/restricted grants as routine isolated setup
  checks. No shared container, volume or manual underwriting database was
  changed.
- [x] Record the accepted source-owned collection monitoring/closure/restart
  slice separately from the three automated scenarios. It covers 58 contiguous
  feed rows, exact origin/evaluation/offer refs, REPAID bridge v28, CLOSED case
  v28, four settled operations and matching restart state.
- [ ] A collection-agent explanation for held/uncertain intermediate states
  remains unobserved: the actual collection case had `runs:[]`,
  `recommendations:[]`, and `investigation:null`. Do not infer this coverage
  from Funding-side transitions or add a synthetic money run to manufacture it.
- [x] Use one lead-refreshed retained underwriting manifest for the `8092`
  adapter, source preparation, and actual underwriting HTTP evaluation. The
  exact run was `978759d7-8f21-4cdc-9880-631f669a2200` with anchor
  `2026-11-15T09:58:16.789858Z`. Three saved evaluations and fixture
  investigations completed before the five-minute cutoff. The scheduler made
  a successful shortfall list poll at `10:04:22.722Z`, after the approximate
  `10:03:16.790Z` cutoff; it returned the same three cases and created no new
  evaluation. Both owned listeners were stopped. Record this deviation, not a
  clean continuous-freshness pass. Keep all artifacts; do not edit timestamps.
- [x] Actual local Funding Task11B publisher/event proof was accepted for the
  joined DEMO slice. The run linked the exact shortfall case, saved evaluation,
  offer, bridge and collection case through the source feed. The local provider
  double reported four create effects and `provider_acceptance_verified:false`;
  no real provider acceptance or funds movement is claimed.
- [x] Record exact joined run `da3bc19b-085d-4ff3-80c4-33b570b4896a`, anchor
  `2026-11-15T19:22:03.586696Z`, shortfall case
  `14227972-3985-87f7-b846-9da5dfd99860`, saved evaluation
  `d521b3f0-4916-4418-81dd-2fadf1322f0a`, offer
  `3c832fab-2218-41b5-b3a8-dfb477cc1c09`, bridge
  `913cd3df-ff6e-49d4-8640-c708ac4da638`, and collection case
  `91511fc3-c895-8a3b-bba8-c881cef988e4`. Artifact paths are listed below.
- [x] Preserve the distinct earlier failed/422 books and receipts under main
  `runtime/funding/retired-books/flowcard_funding_demo_integrated_1aa196a5/`
  and `.../flowcard_funding_demo_integrated_f01ac791/`. The final joined run
  does not overwrite or relabel those historical outcomes.
- [x] Record the focused test case/evaluation/evidence IDs, migration and grant
  results, actual API health response, and synthetic HTTP-double boundaries in
  `/tmp/rootie6-group-g-checkpoint.txt`. Keep credentials out of evidence.
- [x] Record initial watch capture errors in the final report. Two supplied
  invocations used mistyped case UUIDs; a temporary summary parser expected a
  nested response shape although `cmd/watch` returns the case DTO at top level.
  The source watcher was correct, exact IDs came from `prepare.log`, and the
  summary parser was corrected. Initial empty outputs were removed before this
  history was recorded; current valid before/after restart captures are in the
  task-owned ignored `manual-http` directory.
- [x] Rootie3 independently reviewed the source and final joined result
  artifacts. The demonstrated linked monitoring/closure/restart slice is
  accepted; the unobserved collection-agent narrative and actual provider
  acceptance remain explicit limitations.
- [x] Rootie3 accepted the documentation closeout and released the scoped local
  docs commit after the final provider-key/table/path corrections below. The
  lead alone merges `develop`; no push, deploy, subscription change, private
  artifact archive or preserved-database removal is included.

### Accepted joined runtime artifact paths

The exact main-runtime artifacts for the accepted joined run are:

```text
.dev/integration/20261114/runtime/funding/selected-run.json
.dev/integration/20261114/runtime/funding/joint-funding-result.json
.dev/integration/20261114/runtime/funding/bridge-final.json
.dev/integration/20261114/runtime/funding/verified-allocations.json
.dev/integration/20261114/runtime/coordinator/selected-profile.json
.dev/integration/20261114/runtime/coordinator-task/manual-http/r6g_lead_mwi7rj10_eca601/capture/joined-feed-proof.json
.dev/integration/20261114/runtime/coordinator-task/manual-http/r6g_lead_mwi7rj10_eca601/capture/restart-proof.json
.dev/integration/20261114/runtime/coordinator-task/manual-http/r6g_lead_mwi7rj10_eca601/capture/after-restart-91511fc3-c895-8a3b-bba8-c881cef988e4.json
```

This archive records a durable coordinator, bounded fixture agent, and the
accepted local joined DEMO source/collection slice. The coordinator has no
funding command. The Funding-owned provider double did not establish real
provider acceptance or movement of real funds. Live model/CRM/decline readiness,
Task 14, browser acceptance, and the unobserved collection-agent explanation
require separate evidence and authorization.
