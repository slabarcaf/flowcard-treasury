# Funding and collection implementation plan

Updated November 15, 2026. The separate Go service in
[the design](/docs/designs/funding-and-collection.md) is implemented and
component- and joined-runtime-tested. This document records the completed
local fixture delivery and the separately deferred Node/live cutover.

[TOC]

## Independent review status

November 14, 2026: independent review accepted the bounded known-empty DEMO
implementation plan. Subsequent source reviews accepted Tasks 1–12 and the
Task 13 component candidate. The public evaluator and pricing accessor are
integrated from `d2aeb7ff1987fb7ae6ac4c47fc40e5ca8bae3766`.

November 15, 2026: component implementation `9a687ca`, scope correction
`e8d68a2`, Ramil-authorized test cleanup `1d82beb`, and standalone provider
balance fix `ae90c96` are integrated into local `develop` at `edbcebf`.
All seven Funding tests passed on the integrated tree; build and vet passed.
The corrected joined Underwriting/Coordinator/Funding journey subsequently
passed on `5cea5c6`, with independent financial and Coordinator read/feed
audits. Tasks 1–13 are complete for the local fixture delivery. See the
[verification report](/docs/demo/funding-and-collection-verification.md).

The first actual joined run created a linked offer and acceptance, then saved
422 `PROVIDER_READ_HELD` before funding. The standalone provider double had
encoded a numeric balance as a JSON string. The adapter correctly held it;
`ae90c96` fixes the double's wire encoding. Its regression fails before the
fix and passes afterward. Payment reads already use fresh observation times;
no timestamp or freshness bypass was needed. The failed book and receipts
are preserved; the corrected journey uses new owned books and a fresh window.

The next actual run reached the funding guard and saved
`SYSTEM_ACTOR_UNAVAILABLE` before money movement. Commit `004494b` adds the
missing synthetic SYSTEM audit actor to prepared imports. Commit `5d3c06b`
adds the invoice's exact source reference to the built-in customer provenance
list, fixing `RECEIVABLE_COVERAGE_UNKNOWN` during component preparation.
Both preparation modes now pass their focused regression. These follow-ups
retain all real authority, policy and freshness guards.

Corrected run `da3bc19b-085d-4ff3-80c4-33b570b4896a` linked the actual saved
Coordinator case/evaluation to the accepted offer and settled bridge. It
funded 700000 cents and collected 19000 cents in fees followed by 700000 cents
in principal from the two deposits, recovering the dropped collection reply
with four provider creates total. Funding ended REPAID with zero debt, holds
and backing. Coordinator closed its linked collection case at bridge version
28 and consumed the feed through sequence 58, including recovery changes.

Evidence available in this execution environment:

- `/tmp/funding-component-verification.md`: original component proof.
- `/tmp/funding-provider-read-before.json`: standalone regression reproduction.
- `/tmp/funding-provider-read-after.json`: seven-test suite after correction.
- `/tmp/funding-joined-provider-fix-report.txt`: source paths, exact SHAs and
  commands/results.
- `/tmp/funding-joint-final-focused-tests.json`: fourteen top-level test
  functions passed after the preparation and remaining guard corrections.
- `/tmp/funding-system-actor-before.json` and
  `/tmp/funding-system-actor-after.json`: system-actor regression reproduction
  and corrected result.
- `/tmp/funding-prepare-both-profiles.json`: both preparation modes pass.
- Main checkout `.dev/integration/20261114/funding-corrected-merged-tests.json`:
  independent integrated seven-test PASS, with zero skips.

The verification report records the actual joined result, independent
read-only audits and their retained evidence paths. Source checkboxes below
mean implemented and source-accepted; demonstration and verification boxes are
backed by their stated runtime or test evidence. Task 14 source retirement is
now released by Ramil; live acceptance and financial data cutover remain
separate scope. Ramil removed the numeric test cap: prefer isolated focused
tests and concise journeys, preserve assertions, and reuse existing fixtures
and dependencies.

## Scope, dependencies and execution boundary

Deliver a local fixture demonstration of bound offers, acceptance/approval,
atomic capacity reservation, uncertain funding recovery, settled debt,
deposit-driven partial collection, delinquency and coordinator progress.
Ramil approved daily overdue accrual on actual unpaid principal and customer
line exposure including principal, all unpaid fees and unfunded active
principal/fee reservations, without double-counting converted reservations.
Facility cash capacity counts principal commitments only. Ten-minute offer
expiry and UTC offer-date due-date binding remain proposed clarifications.
Ramil selected a funding connected account A and borrower connected account B:
CA transfers fund A-to-B and collect B-to-A. Recommend A also receive fees;
no platform Charges path or separate fee sweep in this demo.

One API, one serial worker, existing PostgreSQL, standard library and already
approved pgx. No Node service dependency or new dependency approval inferred.
Include a concrete Go Airwallex HTTP adapter, tested against local HTTP doubles
and disabled for real dispatch until live gates clear.
Provider doubles are explicitly synthetic DEMO fixtures. Live funds, model
calls, AWS, public auth, card controls, notifications, automatic refunds and
frontend integration are outside this delivery.

Implementation and follow-up edits use the assigned `feat/funding-collection`
worktree. The lead integrated accepted commits into local `develop` and owns
the shared runtime launches and source refresh. Preserve user files, existing
runtime processes and database volumes. Do not copy or read root `.env`.
Never mark tasks complete for planned code.

Source review, focused tests and joined-runtime verification are complete for
the local fixture service. Task 13 closes this first delivery. Task 14 remains
a separately authorized later migration phase. Use focused behavior and
failure scenarios rather than exhaustive permutations. New source follows Go
style and explicit typed boundaries; fixture results never imply live
acceptance.

## Context and integration contracts

Reviewed current requirements, Bridge Capital policy/config/pricing/collection,
shortfall design and fixture runbook, evolving underwriting draft, legacy Node
funding/reservations/ledger/provider/collection code, migration 001, repayment
deposit migration/runbook references and demo acceptance matrix.

Reuse patterns, never import Node runtime logic or copy legacy policy. Preserve
$50 +2% per month for the selected 7–90 days, full fee on early payment,
fee-first deposits immediately after settlement, one open bridge, full-or-zero,
USD and ledger-owned facility capacity. Do not preserve legacy due-only
collection or 5-bps/day fee. Funding settlement precedes card retry; no retry
implementation in this plan.

The following contracts are implemented for the local DEMO boundary; their
shared-runtime linkage was proved in Task 13. SANDBOX authority and
real provider extraction remain live acceptance requirements:

- Public pure Go underwriting `policy` package usable outside its module's
  `internal` tree, stable normalized facts and policy hash.
- Authenticated latest verified non-ledger funding facts, independent of
  assessment-time exposure. Existing evaluation is never a funding command.
- Trusted fresh canonical payment/balance refresh and mapping revision.
- Funding-owned receivable import/exposure read model and source completeness.
- Coordinator servicing change feed, expected-version actions and case links.
- Customer delegation and actual collection authority for SANDBOX.

Upstream files belong to their respective owners. The public policy and
local upstream contracts are integrated; synthetic provider facts remain
explicitly identified. This plan does not authorize changing underwriting,
coordinator or shortfall files in this session.

## Proposed file map

All service paths below are relative to `services/funding-collection/`.
Concrete names may be refined during reviewed implementation without creating
a generic financial framework.

| Area | Files |
| --- | --- |
| Module and commands | `go.mod`, `go.sum`, `cmd/api/main.go`, `cmd/worker/main.go`, `cmd/migrate/main.go`, `cmd/import/main.go`, `cmd/demo-actor/main.go`, `cmd/demo/main.go` |
| Domain and policy boundary | `internal/domain/{money,terms,bridge,operation,collection,delinquency}.go`, `internal/policy/evaluator.go` |
| Upstream clients | `internal/upstream/{underwriting,shortfall,mapping}.go` |
| Persistence | `internal/postgres/{migrator,imports,mapping,offers,reservations,operations,settlements,collections,accrual,changes}.go` |
| Migration | `internal/postgres/migrations/001_initial.sql` |
| Work and provider | `internal/worker/{worker,dispatch,reconcile,collections,delinquency}.go`, `internal/provider/{provider,airwallex,auth,transfers,fixture,unsupported}.go` |
| HTTP/configuration | `internal/http/{server,auth,offers,bridges,operations,changes,webhook,readiness}.go`, `internal/config/config.go` |
| Offline proof | `internal/domain/servicing_test.go`, `internal/http/contract_test.go`, `internal/acceptance/journey_test.go`, `internal/testutil/{database,provider,upstream}.go`, `testdata/` |
| Local packaging | `Dockerfile`, `README.md`, `scripts/demo-request.go` |
| Proposed shared docs | `docs/api/funding-and-collection-contract.md`, `docs/demo/funding-and-collection-local.md` at repository root |

No edits to existing SQL migrations. A future opt-in Compose profile may need
root `compose.yaml`; defer that file until explicitly included in execution
scope. Independent loopback binaries suffice for the first demo.

## Implementation tasks

### Task 1: Transcribe the pinned contract and verify dependencies

**Files:** create `docs/api/funding-and-collection-contract.md`; update only
the reviewed sections of the new design and this plan.

- [x] Transcribe the design's pinned terms/actor/authority schemas, canonical
  struct hash, config/auth/error tables, maturity/accrual rules and endpoint
  DTOs into the API contract. Do not invent unresolved financial behavior.
- [x] Verify shared evaluator inputs, customer-facts response and servicing
  state DTO with source completeness, revision and policy hash. Mark upstream
  contracts absent until their owners implement them; add fixture substitutes.
- [x] Specify exact offer, actor decision, collection-authority and terms hash
  schemas. Include mapping changes and disclosures in invalidation rules.
- [x] Define each endpoint's request/response/errors, role, idempotency scope,
  expected-version rules, no-store headers and sanitized evidence references.
- [x] Agree ascending transactional change sequence and both shortfall and
  collection discovery with coordinator owner. Save case/evaluation links.
- [x] Pin Ramil's selected A-to-B/B-to-A account layout in facility and actor
  bindings. Funding uses scope A/destination B; separate fee/principal returns
  use scope B/destination A. Verify both authorizations and never substitute a
  platform receipt for a credit to A.
- [x] Integrate the reviewed public policy module and actual exported
  signature at the owner-approved fast-forward. Pin the public dependency and
  policy ID/hash; do not copy policy rules.
- [x] Record explicit first-slice bounds: known-empty DEMO financial/history
  books; ABUNDANT/NORMAL dispatch only; TIGHT/CRITICAL returns unsupported
  ranking before reservation. Nonempty legacy import is later cutover work.

Acceptance: no candidate decision can be mistaken for approval, source of
capacity or money command. Unresolved live contracts remain named blockers.

### Task 2: Scaffold Go commands and fail-closed configuration

**Files:** module files, `cmd/{api,worker,migrate}/main.go`,
`internal/config/config.go`, `internal/provider/{provider,unsupported}.go`.

- [x] Pin Go 1.27.3 and pgx v5.12.0 without upgrades. The policy module
  replacement uses the reviewed sibling public underwriting module.
- [x] Implement exact configuration names, default loopback address 8085,
  distinct role keys, mode/endpoint validation, timeout and source binding.
  API startup binds the configured loopback listener and fails on conflicts.
- [x] Reject mode/endpoint/key mismatches; require provider execution disabled
  unless explicit accepted contract and mode prerequisites exist.
- [x] Define minimal typed provider methods for fresh payment/balance facts,
  deposit retrieval, create and lookup, returning certainty and evidence.
- [x] Supply typed controlled errors for unsupported provider capabilities.
  Concrete parser/replay controls and the HTTP adapter are implemented in
  Task 8. API/worker commands are wired; real provider creates stay disabled.

Acceptance: API/worker cannot silently select a live provider or claim live
readiness. The migration command is separate from runtime startup.

### Task 3: Add servicing schema, ledger and atomic change ordering

**Files:** `internal/postgres/migrations/001_initial.sql`,
`internal/postgres/{migrator,mapping,accrual,changes}.go`, domain record types.

- [x] Create mode-scoped facility/customer/invoice state, offers/decisions,
  bridges/reservations, operations, deposits/attempts, inbox/observations,
  commands/changes and balanced ledger transaction/line tables.
- [x] Add actor/authority records, accrual basis/cursor/assessed/paid totals,
  backing deficit and separate immutable offer revision versus mutable version.
- [x] Add immutable terms/evidence/ledger protections, checked nonnegative USD
  amounts, one open bridge per customer, canonical-payment and request-identity
  uniqueness, deposit identity and settlement-effect uniqueness.
- [x] Implement the pinned bridge enums/partial unique predicates: normal
  customer slot excludes VOID_UNFUNDED/REPAID/REOPENED_HELD; canonical payment
  blocks every ever_funded bridge forever and all nonvoid attempts. Void is
  zero debt/never funded, never a successful repayment history record.
- [x] Specify indexes for leased work, due dates, customer reads and ascending
  changes. Use a locked transactional per-mode counter, not a sequence whose
  allocation can precede another transaction's commit.
- [x] Update balances and balanced postings together; retain effective and
  recorded times, actual provider fees, source IDs and exact actor references.
- [x] Define privileged migration grants and restricted runtime SELECT on
  existing mapping tables. Runtime can mutate only servicing schema.
- [x] Create the exact fixed-SQL SECURITY DEFINER mapping-lock function from
  the design: privileged non-runtime owner, safe search_path, schema-qualified
  roster-then-card FOR SHARE, PUBLIC revoke and runtime EXECUTE-only. Keep
  direct runtime mapping access SELECT-only; no foreign writes or dynamic SQL.

Acceptance: source review demonstrates one SQL boundary for reservations,
settlements, allocation and corresponding coordinator changes; no runtime DDL.

Task 3 schema, migration and ledger/feed primitives are source-accepted.
Worker leases and webhook inbox processing were completed in Tasks 8 and 9.
The component tests executed migrations against uniquely owned databases;
joined-runtime acceptance remains in Task 13.

### Task 4: Implement controlled fixture/facility/receivable imports

**Files:** `cmd/import/main.go`, `internal/postgres/imports.go`,
`internal/upstream/mapping.go`, `testdata/{facility,receivables,actors}.json`.

- [x] Support `--kind facility|receivables|authority|actors --file PATH` with
  typed versioned records, mode, source references and completeness. Authority
  fixture imports are DEMO-only and clearly synthetic.
- [x] Require explicit known-empty debt/reservation/default-history DEMO
  declaration matching context. Reject nonempty opening debt; its importer is
  outside this delivery. Missing history is never a known-empty declaration.
- [x] Validate source invoice identity, unpaid/net portions, verified due date,
  dispute/cancel flags, captured time and allocation completeness.
- [x] Lock facility/customer/invoices in common order; reject duplicate version
  with changed content, stale publication and changes that silently erase
  backing/commitments. Hold affected obligations for contradictory updates.
- [x] Resolve roster/card mappings via read-only adapter and record revision
  fingerprints. Never enroll a customer or update a provider mapping here.

Acceptance: imports cannot bypass holds or turn synthetic dates/consent into
verified sandbox evidence. A separate fixture facility avoids legacy writers.

### Task 5: Implement pure money, terms and policy adaptation

**Files:** `internal/domain/{money,terms,delinquency}.go`,
`internal/underwriting/evaluator.go`, `internal/upstream/underwriting.go`.

- [x] Implement checked cents and single half-up price rounding, fixed
  selected-term fee, UTC dates, 7–90-day term and immutable terms hashing.
- [x] Implement bounded read-only GET clients for saved evaluation and
  normalized customer context using the copied HTTP contract; preserve raw
  normalized data without turning it into funding authority.
- [x] Consume the public underwriting evaluator, version/hash and normalized
  non-ledger facts. Supply locked local capacity/invoice facts at authorization;
  do not trust snapshot capacity and do not duplicate the credit rules. Public
  policy/pricing commit `d2aeb7ff1987fb7ae6ac4c47fc40e5ca8bae3766` is pinned in
  the funding underwriting adapter.
- [x] Require evaluator policy ID/hash to match the candidate and offer;
  mismatch blocks funding until reevaluation and any changed terms/actor
  bindings are refreshed. Never upgrade policy silently during dispatch.
- [x] Keep FAIL/UNKNOWN distinct; only all hard rules PASS can proceed.
  HUMAN_APPROVAL_REQUIRED is a workflow requirement, never a rule override.
- [x] Implement 1/8/31/61 DPD stage boundaries and persistent first default
  date. Do not clear history when a bridge closes.
- [x] Implement start-of-day assessment before collections, immutable day
  basis, rational cumulative numerator, assessed/paid counters and cursor in
  `internal/postgres/accrual.go`. Reconstruct downtime from effective-time
  principal history; at most 90 dates/pass, otherwise WAIT_FOR_ACCRUAL.
- [x] Pin the full opening-day fee at same-day payoff, zero further fee after
  principal payoff, and held correction for late evidence. Match the design's
  December 1/2 $4.67/$2.33 example, never mutate prior earned charges.
- [x] Bind the explicit OPENING_PRINCIPAL_UTC_DAY convention/disclosure to
  terms. Principal first funded after midnight has zero opening basis for
  that day. Do not label payoff-day timing as separately approved by Ramil.

Acceptance: $7,000/30 days has $190 contracted fee; early repayment preserves
it; partial repayment cannot delete prior overdue accrual.

### Task 6: Persist offers, customer acceptance and operator approval

**Files:** `internal/postgres/offers.go`,
`internal/http/{offers,auth}.go`, `cmd/demo-actor/main.go`.

- [x] Create offer solely from saved compatible evaluation and referenced
  facts. Derive all amounts/customer/payment/provider scope server-side.
- [x] Persist terms/disclosure/mapping binding, original evaluation, UTC
  offered/due dates, ten-minute expiry and authoritative version.
- [x] Accept exact terms hash only for authenticated customer or DEMO-only
  fixture actor. Operator approval requires separate scope and reason.
- [x] Store immutable customer acceptance/operator approval decisions with
  actor identity, subject, reason, terms hash and source refs; preserve audit
  evidence. AUTO_APPROVE remains exclusively in the later fresh funding guard.
- [x] Implement role-bound assertions and caller-scoped idempotent command
  receipts for customer acceptance and operator approval, including blocked
  outcomes. Agent cannot accept or human-approve.
- [x] Create offers only from the published shared policy package/hash and
  pinned pricing accessor; the API never fabricates all-PASS.

Acceptance: changed principal, fee, term, payment, customer, mapping or terms
version requires new bindings. No queueing funds from an evaluation alone.

### Task 7: Atomically recheck and reserve full funding

**Files:** `internal/http/fund.go`, `internal/postgres/reservations.go`,
`internal/postgres/cancel_funding.go`,
`internal/postgres/migrations/006_funding_checks.sql`,
`internal/underwriting/projection.go`, and `internal/upstream/shortfall.go`.

- [x] Fetch verified current payment/balance/non-ledger facts before locks;
  validate mode, scope, 30-second current-observation age and source freshness.
- [x] Lock facility, customer, offer/bridge, then sorted invoice rows; invoke
  restricted mapping-lock function. Check bindings, authority, approvals,
  expiry, same offered_on UTC date and current holds.
- [x] Re-run shared hard rules on the first-reservation locked facility,
  customer and invoice state. Retain all existing debt/commitments and map
  external facility commitments into reserved principal exactly once.
  Prevent duplicate payment and a second open customer bridge.
- [x] Provide a typed pure projection for one proven NEVER_SENT operation:
  remove only its customer principal/fee, facility principal, exact invoice
  allocations, placeholder and payment duplicate. Retain every other hold.
  `ValidateSourceLiquidity` separately excludes only that operation's local
  debit while checking all other source holds.
- [x] Task 8 calls both projections in the actual first-send guard and
  verifies the locked placeholder and allocation identities.
- [x] Refuse TIGHT/CRITICAL with SCARCE_CAPITAL_RANKING_UNSUPPORTED; do not
  implement an invented score or arrival-order scarce allocation. Reserve
  facility principal, customer principal-plus-fee and full principal-plus-fee
  invoice coverage, with no partial success. Consume offer and create immutable
  funding operation atomically.
- [x] Post memo commitments, command result and change together. Expire only
  proven-unsent work within two minutes/offer expiry; unknown send keeps holds.
- [x] In the same proven-unsent/no-movement cancellation transaction set
  VOID_UNFUNDED, release all commitments, terminate offer and emit bridge
  version. Allow a fresh guarded offer/operation for that never-funded payment;
  preserve funded-payment blocking and operation audit permanently.

Acceptance: a simultaneous capacity race can produce only one fitting
reservation. A failed check cannot leave an orphan operation or allocation.

### Task 8: Implement Airwallex adapter and guarded dispatch/reconciliation

**Files:** `internal/postgres/operations.go`,
`internal/worker/{worker,dispatch,reconcile}.go`,
`internal/domain/operation.go`,
`internal/provider/{airwallex,auth,transfers,fixture}.go`.

- [x] Implement server-side token exchange with login headers, explicit account
  context and in-memory expires-at cache. Use sandbox host allowlist, bounded
  response bodies, context timeouts and no credentialed redirects.
- [x] Implement CA transfer create with saved request ID, mapped destination,
  exact decimal USD amount, reviewed reason and bridge reference. Bind source
  to configured funding connected account A via `x-on-behalf-of`; destination
  is borrower B. No agent selects either account. Reject missing source scope.
- [x] Implement separate fee/principal repayment CA transfers with scope B and
  destination A, reviewed purpose and borrower mandate. Persist direction and
  refuse swapped/self/remapped accounts. No Charges adapter is required.
- [x] Read source and borrower USD available balances in their correct account
  contexts before dispatch. Source must cover transfer plus known provider
  fees, alongside ledger/reservation checks; unknown fee/balance semantics
  block dispatch. Verify mapped ACTIVE/KYC prerequisites.
- [x] Implement retrieve-by-ID and request-ID filtered list lookups for both
  directions, original account scope, bounded pagination and strict
  identity/amount/currency/status matching. Preserve fee and receipt timestamps.

- [x] Implement one serial worker, one-second polling, 60-second fenced
  leases, ten-second provider timeout, bounded retry schedules and hold codes.
- [x] Require expected state +token +unexpired DB-clock lease on each finish
  CAS. Claim/finish obey facility-first locks; expiry never applies a stale
  worker's observation or starts a second unknown create.
- [x] Before first send repeat fresh financial/mapping/authority guards;
  mark DISPATCHING and commit before I/O. Refuse changed full shortfall.
- [x] Store same operation/request identity and payload across retry; restart
  from DISPATCHING enters UNKNOWN and lookup-only recovery.
- [x] Match complete retrieved provider identity/accounts/amount/USD/status;
  absent lookup cannot authorize resend. Keep live replay unsupported until
  provider deduplication contract/retention is verified.
- [x] Reconcile PENDING/UNKNOWN with bounded reads; permission/schema/mismatch
  holds immediately. Cancellation/release requires never-sent or verified
  no-movement/returned evidence. Manual requests schedule lookup only.

Acceptance: timeout/crash can never create a fresh provider operation identity
for the same funding. No success is guessed when provider certainty is absent.

### Task 9: Receive events and apply confirmed settlements

**Files:** `internal/http/webhook.go`,
`internal/postgres/settlements.go`, `internal/domain/bridge.go`.

- [x] Port exact shortfall raw-body signature/body-bound/timestamp rules to
  the dedicated servicing route with durable mode/event deduplication.
- [x] Normalize deposit/transfer wakeups behind verified parser
  interfaces; fixtures inject explicit contracts, SANDBOX remains unsupported.
- [x] Retrieve authoritative state outside transactions, retain protected
  provider observations and reject mismatched or incomplete settlement facts.
- [x] Lock/apply one unique settlement effect: funding converts reservation
  to principal and books contracted fee atomically; duplicate/out-of-order
  events cannot regress state or book a second obligation.
- [x] Preserve pending/unknown commitments; hold terminal conflicts and record
  verified return/reversal with compensating postings, source and audit refs.

Acceptance: HTTP success or a webhook name alone cannot book debt. Provider
event duplication and settlement duplication are separate deduplication keys.

### Task 10: Budget deposits and settle partial collections

**Files:** `internal/domain/collection.go`,
`internal/postgres/collections.go`, `internal/worker/collections.go`.

- [x] Verify canonical net settled USD credit after funding and fresh available
  balance. Exclude pending/internal/bridge/refund/unclassified credits.
- [x] Require reviewed account-specific collection authority, purpose and
  limits. Missing mandate/product/fee semantics leaves a visible blocked item.
- [x] Serialize one unresolved debit per customer/account; reserve against
  remaining deposit budget, debt and mandate limit. Unknown operations consume
  budget until reconciled, preventing retry-driven overcollection.
- [x] Queue fee before principal as separate operations; only settled fee
  completion permits principal, after refreshed available-balance check.
  Store immutable late/contract fee split. Follow the design's collection
  first-send lock/guard procedure, re-accruing before debt/waterfall checks.
- [x] For first-send checks project out only this proven-unsent operation's
  deposit/mandate/debt/local-liquidity holds and its ID from unresolved checks.
  Keep all other and unknown commitments; never add to actual provider balance.
- [x] Cancel only NEVER_SENT operations when a changed cap/split/authority
  invalidates intent; release exact holds and allow bounded fresh replanning
  under the design's three-per-deposit/day limit. Never resize UNKNOWN.
- [x] Release invoice backing on every settled original fee/principal debt
  reduction in original allocation order; late fees are unbacked servicing
  debt. Closure asserts zero allocations, debt and debit reservations.
- [x] Restore reversal backing only if still free; record deficit/hold rather
  than overallocate. Reopened old debt after another bridge opens remains
  visible as REOPENED_HELD and counts against exposure.
- [x] Record actual cash, debt allocation and principal capacity replenishment;
  fee income does not enlarge facility. Oversized debit creates refund payable
  and hold; confirmed reversals restore debt and block new funding.
- [x] Close only zero finalized debt with no uncertain/refund/reversal work.
  Retry only with fresh evidence and bounded attempt policy; never repeatedly
  charge unchanged insufficient balance or reset deposit consumed totals.

Acceptance: a $7,190 obligation and two $100/$7,090 net settled deposits can
collect $100 fee, then $90 fee and $7,000 principal, with customer excess left
untouched. A timeout reserves the debit until resolved.

### Task 11: Publish servicing reads and coordinator work queues

**Files:** `internal/http/{bridges,operations,changes}.go`,
`internal/worker/delinquency.go`, `internal/postgres/changes.go`.

- [x] Implement bridge/collection/operation reads and exposure/history/invoice
  state, with source mode/version and bounded sanitized evidence references.
- [x] Include stable obligation/bridge ID, source binding, internal account
  alias, debt/USD/due date, last attempt/outcome, authority/actor guard
  statuses, provider observation time and next eligible retry. Keep flags
  distinct
  from movement authority and expose controlled recommendation codes.
- [x] Publish ascending transactional changes for offer decisions, funding,
  partial collection, holds, due/overdue/default and closure, 25 per page.
- [x] Type resources as OFFER/BRIDGE/OPERATION, include exact parent bridge
  ID/version whenever created, customer/account aliases and nullable case/
  evaluation/offer refs. Increment parent bridge version for every operation,
  authority, settlement, debt or hold change affecting allowed actions, in the
  same transaction as its feed row. Never reuse operation version as bridge
  version; coordinator checkpoints refresh targets and fences stale output.
- [x] Validate cursor mode and numeric range; replay from zero remains valid
  for the retained installation lifetime. Never skip uncommitted lower changes.
- [x] Add version-guarded collection-review and operation-reconcile commands;
  use exact expected-version target and receipt from the pinned design.
  Neither route accepts amounts/accounts. Implement pinned history ordering
  and bridge-version cursor, plus accrual-current/as-of staleness fields.
- [x] Scan once per minute, catch up overdue fees/stages after restart, record
  default once and publish reminder/escalation/card-control requirements as
  pending work. Never mark an external notification/control as executed.

Acceptance: coordinator can discover and explain collection progress without
holding debt state or selecting its own monetary commands.

### Task 12: Package an honest local fixture demonstration

**Files:** `cmd/demo/{main,manifest,prepare,external_prepare}.go`,
`cmd/demo/{serve_provider,run}.go`,
`cmd/{api,worker,migrate,import,demo-actor}/main.go`,
`internal/config/config.go`, `internal/postgres/runtime_pool.go`,
`internal/http/server.go`, `Dockerfile`, `README.md`, API/design/plan docs and
`docs/demo/funding-and-collection-local.md`.

- [x] Provide API/worker/import/migrate/demo-actor commands with private
  loopback settings and no root `.env` access. Document unused-port selection.
- [x] Bundle synthetic offer/evaluation/mapping/payment/deposit/authority
  fixtures plus fixture upstream HTTP/provider adapters. No provider IDs.
- [x] Implement pinned `cmd/demo prepare|serve-provider|run` commands and
  manifest with current relative dates, known-empty scope and synthetic actors.
  The real Go worker/HTTP adapter must produce the demonstrated lifecycle,
  not injected final ledger results. Refuse stale runs and nonloopback mocks.
- [x] Demonstrate component actor steps, reservation, confirmed funding,
  partial fee/principal collection, dropped-receipt recovery and same-identity
  reconciliation through the real API/worker/HTTP adapter.
- [x] Complete the standalone joined-process demonstration with actual saved
  Underwriting evaluation and Coordinator case, partial collections, recovery
  and linked Coordinator readback. Retain pending/held observations if seen.
- [x] Expose liveness separately from DB/migration/policy/mode readiness;
  disclose FIXTURE_ONLY and funding/collection gates even when runtime is ready.
- [x] Expose supported regimes ABUNDANT/NORMAL and unsupported scarce
  execution. Demonstrate human-approval guarding in a separate TIGHT offer,
  followed by unsupported ranking and zero provider creates. The HTTP
  contract test proves the refusal and identical saved-422 replay.
- [x] Document metadata-only operation inspection, restart, bounded review,
  unknown-state escalation and one-writer cutover requirements. No database
  reset/cleanup recipe or success override command.

Acceptance: presenter can show the full deterministic lifecycle and coordinator
read models without credentials, a model, Node service or live provider calls.

#### Historical Task 12A source checkpoint

Source-only preparation, provider-double, runtime-budget and packaging work is
implemented in the assigned `feat/funding-collection` worktree. The fresh
bundle command and local provider double are available; `/health/live` and
`/health/ready` are separate; Docker packaging copies the public underwriting
module explicitly. The staged runbook records guarded database/import
commands and synthetic provenance. The `cmd/demo run` journey, lifecycle
proof and presenter walkthrough remain Task 12B. No tests, database, app,
provider or network operations are part of this checkpoint.

#### Historical Task 12B source checkpoint

`cmd/demo run` now uses the actual saved Underwriting evaluation and the normal
authenticated funding API routes. It records fixture customer acceptance,
conditional operator approval, funding command receipt/replay, worker-settled
funding, synthetic deposit control and fee-first collection readback. It
asserts the TIGHT scarce-capital 422 has no new provider transfer, then checks
the abundant profile's same-request timeout recovery, settlement allocations,
zero debt/backing/holds and final provider balances. The runner rejects stale
inputs and mismatched policy, mode, database name or source identities. It
binds generated SQL, migration/import invocations, API readiness and the
runner to an explicit owner-prefixed database name. The actual-profile
path reads a fresh owner-prepared known-empty Underwriting manifest without
changing its source facts; the host supplies the saved assessment ID and
matching coordinator case ID. This checkpoint originally covered source
only. Task 12 source review and Task 13 component verification subsequently
passed; the corrected standalone joined journey has now passed as recorded
in the verification report.

### Task 13: Final focused verification and review report

**Files:** `internal/domain/servicing_test.go`,
`internal/http/contract_test.go`, `internal/acceptance/*_test.go`,
`cmd/demo/serve_provider_test.go`, `internal/testutil/`, final runbook/plan.

- [x] Add focused terms/guards, HTTP contract and end-to-end smoke coverage.
  Ramil subsequently authorized independent cancellation, uncertain-send
  recovery and reversal/accrual scenarios plus typed shared setup. The
  standalone provider correction adds its own focused handler regression.
- [x] Check selected-term pricing/hash binding, fee-inclusive full-or-zero
  customer capacity, scarce-capital refusal and cumulative daily accrual.
  Verify persisted UTC-day basis, numerator, assessed debt/cursor and replay
  after verified principal reversal in the collection scenario.
- [x] Check actor role refusals, same-body versus changed-body command replay,
  stale decision versions, signed durable raw webhook receipts, event-body
  conflict and ascending scoped servicing changes in the HTTP contract test.
- [x] Exercise concurrent fitting reservations, dropped funding receipt,
  worker restart and original-identity GET recovery without another create.
- [x] Verify duplicate deposit wake delivery, two partial fee-first deposits,
  exact original/reversal debt allocations, zero-debt/zero-backing closure,
  reopened held debt, and unsupported webhook held handling.
- [x] Verify proven-unsent cancellation releases capacity without funded
  history, permits fresh accepted terms/reservation, and an ever-funded
  payment remains blocked after repayment.
- [x] Exercise actual adapter login, source account header, exact funding
  amount/destination/request identity, fee/principal transfer settlement and
  recovery against a local provider double.
- [x] Keep `TestServicingJourney` on the real API -> committed operation ->
  worker -> HTTP adapter -> local double -> verified settlement/read path.
  Fixture controls change provider facts only; no application settlements are
  injected, and DEMO creates work with live dispatch disabled.
- [x] Guard disposable databases to explicit loopback, uniquely named and
  marked owned targets and restricted runtime logins. Never reset a shared
  database, table or volume. Actual migrations ran in the component suite.
- [x] Reproduce the standalone provider balance encoding hold before the
  source fix and pass the regression afterward. Check borrower/facility
  balances, fresh payment reads, known-zero changed need and unchanged event
  facts against the actual command-serving handlers.
- [x] Run the original seven integrated service tests and the expanded
  fourteen-function suite with `-p 1`, build, vet, formatting and diff checks.
  No critical skips. Lead independently reran the original integrated suite.
  Optional `golangci-lint` could not run with its older Go build; no new lint
  dependency was installed.
- [x] Close the original uncovered checks with isolated fixtures:
  `TestDispatchOfferRejectsNextUTCDay` and
  `TestMidnightCancellationReleasesAcceptedFunding` prove rollover denial and
  persisted cancellation; `TestNeverSentProjectionPreservesOtherCommitments`
  checks all five projected groups and preserves unrelated holds;
  `TestCollectionChangedCapCancelsOnlyUnsentIntent` verifies cancellation
  without resizing, booking debt or touching settled funding;
  `TestAmbiguousLookupPreservesFundingHolds` proves repeated ambiguous reads
  retain commitments without resend. `TestFundingRecovery` now proves one
  funding ledger effect after a duplicate terminal wake.
  `TestBackdatedReversalHoldsFrozenAccrual` proves a late principal reversal
  holds without rewriting frozen zero-principal accrual days.
- [x] Test prepared system-actor presence and invoice provenance using both
  external and built-in materialization, preserving real shared-policy checks.
- [x] Complete the corrected shared Underwriting/Coordinator/Funding process
  journey on fresh uniquely owned books, preserving the first failed 422.
  Capture exact case/evaluation/offer/bridge links, $7000 funding, $100 then
  $7090 deposits, $190 fee-first repayment, zero debt/backing/holds and actual
  Coordinator feed/detail progress through closure and uncertain recovery.
- [x] Review joined evidence and final contract/design/runbook consistency,
  record actual remaining limitations and close this local fixture delivery.
  The plan retains Task 14 and live gates; no live/provider acceptance is
  inferred from local evidence.

Commands used from the service directory, with cached dependencies and a
privately supplied `FUNDING_TEST_ADMIN_URL_FILE` for isolated databases:

```sh
go test -json -p 1 ./... -count=1
go build ./...
go vet ./...
gofmt -l .
```

The original seven tests are `TestTermsAndGuards`, `TestServicingContract`,
`TestUnsentFundingCancellation`, `TestFundingRecovery`,
`TestCollectionReversalAndAccrual`, `TestServicingJourney` and
`TestStandaloneProviderReads`. The additional focused tests are named above.
Each acceptance scenario can run independently.
There is no fixed test-count cap. Keep new coverage tied to a concrete risk
and reuse typed fixtures; new dependencies/infrastructure need approval.

#### Executed corrected joined journey

1. Confirm the single shared runtime owner has all corrected binaries,
   restricted DB profiles, source preparation and launch commands ready.
   Funding must include `ae90c96`; accepted unchanged components can reuse
   their verified binaries. Check listener ownership before starting anything.
2. Use fresh uniquely owned Funding and Coordinator books. Preserve the
   failed attempt's database, provider state, command receipts and consumed
   Coordinator checkpoints. Prepare imports/mappings with the existing guarded
   tooling; perform no shared reset or source rebinding.
3. Refresh the known-empty Underwriting source bundle once, only after all
   consumers are ready. Launch the prepared consumers together within the
   valid source window. Let the real Coordinator discover/evaluate the actual
   shortfall and supply its saved case/evaluation pair.
4. Run normal Funding demo commands with that exact pair: create offer,
   synthetic customer acceptance, guarded reservation and worker dispatch.
   Exercise the existing local double's dropped collection reply and recovery
   with the same operation/request identity. Publish the two deposits in
   sequence and let the worker apply settlement and repayment.
5. Capture sanitized command receipts, provider-request counts, bridge and
   collection reads, allocations, balances, Coordinator linked feed/checkpoint
   progress and final closure. Re-read historical results after source expiry
   with honest degraded readiness; expired facts cannot authorize decisions.
6. Compare evidence with the completion assertions above. Fix only observed
   defects, rerun affected checks, and start a new owned window if freshness
   expires before a required decision. Review the focused-assertion evidence
   and final joined captures before marking Task 13 done.

The lead executed this coordinated runtime sequence; independent read-only
Funding and Coordinator audits passed. Preserve retained runtime ownership
and source windows; do not launch duplicate consumers or rewrite old runs.
No live provider/model calls, deployment, Node cutover or financial-state
injection.

## Node source retirement and remaining cutover

### Task 14: Retire replaced Node funding and collection paths

Ramil authorized deleting the Go-replaced Node source on November 15, 2026.
The first retirement slice removes its financial startup paths and duplicate
implementations. Existing running processes, books and provider callbacks are
unchanged. See the [ownership inventory](/docs/demo/node-funding-retirement.md).

- [x] Inventory Node responsibilities and their callers. Record Go financial
  replacements and retain onboarding/session proxies, CRM/evidence/profile
  read models, roster tooling, historical ledger reads and migration/seed
  tooling that still have callers.
- [x] Remove Node financial reservation/ledger/projector writers, collections,
  outbox and money-reconciliation workers, financial webhook processing,
  legacy policy decisions/approval CLI and coupled Kit 5 orchestration.
  Remove exclusive tests and scripts with their source. Historical migrations
  and financial records are preserved.
- [x] Make old financial endpoints return explicit 410 retirement responses
  without database or provider execution. Retain onboarding session/forwarding
  and report Node approval unavailable. Do not forge Go consent or proxy the
  incompatible old command contract.
- [x] Update Node startup, command wiring and build cleanup, run the server and
  frontend builds and 121 focused retained-function/retirement tests, and
  document remaining Node dependencies. No database-backed test or live
  runtime operation was performed for source retirement.
- [ ] Reconcile opening balances and unresolved operations before retiring a
  running old financial writer or switching provider callbacks. Go currently
  supports known-empty DEMO opening books, not nonempty legacy cutover.
  Preserve operation identities and settlement provenance.
- [ ] Migrate legacy browser execution callers to the Go offer and actor
  contract. The old `BridgeApiClient` receives 410 for financial commands and
  retains only its historical ledger read; it is not a Go consent adapter.
- [ ] Replace remaining onboarding/session/CRM/roster/tooling responsibilities
  before deleting the whole Node service, its Compose/Docker process or shared
  dependencies. Funding verification alone does not complete that rewrite.

Acceptance: duplicate Node financial source and automatic writer startup are
removed. Full runtime/data and whole-service cutover remain explicitly open.
No database records, historical migrations or running instances were deleted.

## Post-completion live acceptance gates

These require separate authorization and provider evidence, outside the local
implementation checklist. A fixture report cannot mark them passed.

- Verify real shortfall extraction, trusted fresh payment/balance state and
  live full-shortfall race semantics; upstream currently fails closed.
- Verify real upstream policy/facts and coordinator source contracts,
  single-writer opening exposure/receivable reconciliation and trusted
  customer delegation.
- Pin CA transfer schemas, A/B account scopes, permissions, mandates,
  net amounts/fees, replay retention, lookup absence semantics, settlement,
  return, reversal and refund contracts. Verify actual debit authority.
- Verify signed webhook routing/subscriptions and missed-event retrieval,
  without dual Node/Go servicing of one customer or operation.
- Review accepted disclosure for chosen-term and daily overdue fees, customer
  rights and collection authority. Implement required card safeguards before
  claiming their policy behavior in any live demonstration.
- Obtain explicit scope for sandbox execution/acceptance, UI, public
  authentication, notification/card-control execution and deployment. Building
  the offline-tested HTTP adapter does not authorize any provider operation.

## See also

- [Funding and collection design](/docs/designs/funding-and-collection.md)
- [Underwriting proposal](/docs/designs/underwriting-engine.md)
- [Shortfall local runbook](/docs/demo/shortfall-detection-local.md)
- [Current requirements](/docs/requirements/README.md)
