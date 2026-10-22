---
goal: Build auditable underwriting profiles and deterministic bridge decisions
version: 1.0
date_created: 2026-10-22
last_updated: 2026-10-22
owner: Ramil
status: Planned
tags: [feature, policy, underwriting, bridge-ledger]
---

# 20261022 Deterministic underwriting policy implementation plan

![Status: Planned](https://img.shields.io/badge/status-Planned-blue)

Build the versioned customer risk inputs, daily credit-line precheck, and
request-time policy decision that connect Salesforce snapshots, qualified AI
signals, and the bridge ledger. The server remains the only authority for
funding; the demo uses fixtures, and production stays gated on verified data
sources and mappings.

[TOC]

## Context from discovery

- The current application is a React, TypeScript, and Vite demo. Its pure
  underwriting functions are in `src/policy/`; they currently accept four
  pre-rated signals and do not consume snapshots or evidence readiness.
- The [Airwallex bridge ledger plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md)
  adds the TypeScript server, PostgreSQL ledger, facility projection,
  transactional outbox, and atomic reservation path. Its current boundary
  expects a trusted approved command.
- The [Salesforce snapshot plan](/docs/plans/20261021-salesforce-snapshot-implementation-plan.md)
  owns explicit enrollment and complete, atomic, daily snapshots. A missing,
  invalid, or stale snapshot blocks new decisions for that customer.
- The [AI evidence pipeline plan](/docs/plans/20261022-ai-evidence-pipeline-implementation-plan.md)
  maps only qualified signals into the existing `Signals` input. It blocks
  customers with unresolved review, unmapped evidence, failed extraction, or
  unqualified signal types.
- The Airwallex server worktree already has the ledger, roster, Salesforce,
  and snapshot modules. The AI evidence plan adds the versioned evidence
  records and reviewer CLI. This plan depends on those upstream changes being
  integrated before execution.
- The source-of-truth policy requires two stages: a daily precheck computes a
  line, then each arriving request is checked against that line and current
  facility capacity. The current `assignTier` implementation does not yet
  apply the source-of-truth rule that two or more red signals mean no line.

## Decision log

| Topic | Selected recommendation | Reason |
| --- | --- | --- |
| Decision authority | Deterministic server policy | Auditable yes, no, and amount |
| Computation | Daily precheck plus request-time check | Balances freshness and latency |
| Risk history | Versioned daily profile per customer | Avoids historical reads on a live request |
| Incomplete inputs | Block only the affected customer | Fail closed without stopping a batch |
| Evidence | Use only qualified, source-mapped signals | Keeps model output outside policy |
| Request ordering | Sequential arrival; doors enforce tier priority | Matches the bridge flow |
| Partial funding | Full shortfall or zero | A smaller bridge does not clear the payment |
| Model allocation | Exclude model-suggested splits in V1 | Keep amount and priority deterministic |
| Human door | Record review; reserve nothing until approval | Approval must use current data and exact terms |
| Positive evidence | Cannot raise a line without human approval | Prevents unsupported automatic upgrades |
| Capital | Keep the 20% facility floor and zero extra operating buffer | These are separate settings in the ledger plan |
| Hysteresis | Defer band reopening hysteresis | It needs extra state and is marked for later |

## Requirements and constraints

- **REQ-001 — Server authority:** The server computes the policy result. A
  browser-provided amount or approval flag is never sufficient to reserve
  facility capacity or enqueue a transfer.
- **REQ-002 — Two stages:** Run a daily customer precheck after its required
  Salesforce, Airwallex profile, and AI evidence inputs are complete. Recheck
  each request against current customer exposure and facility state.
- **REQ-003 — Explicit roster:** Evaluate only actively enrolled customers
  from the internal roster. Provider account lists are reconciliation inputs,
  not enrollment sources.
- **REQ-004 — Versioned inputs:** Every decision references the customer
  roster state, profile version, Salesforce snapshot ID, evidence run and
  catalog versions, policy version, and facility ledger position used.
- **REQ-005 — Freshness and completeness:** A profile and Salesforce snapshot
  must be complete and no older than 24 hours. Publish each customer version
  atomically and process customers independently in batches of at most 1,000
  records.
- **REQ-006 — Customer-level fail-closed:** Missing or unmapped required data,
  stale versions, failed queries, unqualified signals, `review_required`,
  `unmapped`, and `failed` evidence block new decisions for that customer.
  Other customers continue. A valid `no_signal` result is not a failure.
- **REQ-007 — Salesforce gates:** Require active customer status, an active
  contract that has not expired, verified KYB, and a non-null approved bridge
  cap. `watch` requires human review. `hold` and
  `review_required` block. Unknown values fail closed. A mapped null contract
  end date means no scheduled expiry; a missing or unmapped value is invalid.
- **REQ-008 — Airwallex standing:** Require an active connected account and no
  open KYC or transaction RFI. An unknown account or RFI state blocks.
- **REQ-009 — No-go rules:** No line for excluded industry, fraud flag,
  frozen or closed account, verified prior write-off/default, or payer
  concentration above 70%. Unknown required no-go facts block instead of
  defaulting to safe. An active delinquency blocks until resolved.
- **REQ-010 — Signal and tier rules:** Use the four typed ratings and the
  October 20 source-of-truth rules. Red inflow reliability or two or more red
  ratings means no line. One red on another signal or three or more yellows
  means Tier C; two yellows means Tier B; all green or one yellow means Tier A.
- **REQ-011 — Rating thresholds:** Inflow reliability is green at 90% or more,
  yellow from 75% to below 90%, and red below 75%. Concentration is green at
  40% or less, yellow above 40% through 70%, and a no-go above 70%. Cash
  buffer uses the source proposal: 5 or fewer low-balance days is green, 6 to
  20 is yellow, and more than 20 is red. A prior on-time repayment record is
  green, never borrowed is yellow, and a prior late repayment is red.
- **REQ-012 — Credit line:** The line is the minimum of average monthly
  inflows times the tier multiplier, the configured per-customer facility
  cap, and the Salesforce approved bridge cap. Multipliers are 30%, 20%, and
  10% for Tiers A, B, and C. A starter borrower uses 10% when the other three
  ratings are green and 5% otherwise; promote after three fully repaid,
  on-time bridges. The facility cap defaults to 20%; an operator may set it
  to 10% for a mature portfolio. Do not infer maturity from an undocumented
  threshold.
- **REQ-013 — Evidence effect:** Only qualified and explicitly mapped
  evidence can affect a rating. Evidence may worsen a rating. Positive
  evidence does not raise a rating or line without a recorded human approval.
  Salesforce text is never sent to policy as instructions or interpreted by
  free-form model rationale.
- **REQ-014 — Request checks:** Require a traceable, supported card spend;
  reject cash, quasi-cash, and unknown spend categories. Require a positive
  USD shortfall, no open bridge for the customer, no balance more than two
  days past due, enough remaining customer line, and enough deployable
  facility capacity.
- **REQ-015 — Full amount:** Fund the full shortfall or zero. Never shrink a
  request to fit a credit line or the facility.
- **REQ-016 — Capital doors:** Use the current band table: above 70% is A lot;
  40% to 70% is Normal; 10% to below 40% is Less; above 0% to below 10% is
  Critical; 0% is At floor. A lot serves A/B/C automatically; Normal serves
  A/B automatically and C by human review; Less serves A automatically, B by
  human review, and closes C; Critical sends A to human review and closes B/C;
  At floor closes all tiers.
- **REQ-017 — Order:** Process requests in arrival order. Do not reorder,
  queue for future capital, or make model-generated portfolio splits.
  Sequential capital checks and the door table provide tier priority.
- **REQ-018 — Human approval:** A human-review result is not an approval and
  does not reserve capacity. Require an identified operator and reason.
  Re-evaluate all gates and exact terms in the reservation transaction before
  funding.
- **REQ-019 — Ledger boundary:** The internal ledger and its configured 20%
  reserve floor determine facility capacity. The operating buffer remains
  zero as specified by the ledger plan; it is not the reserve floor. Policy
  never calls Airwallex transfers or charges directly.
- **REQ-020 — Audit:** Keep immutable policy evaluations with stable reason
  codes and version references. Do not duplicate raw Salesforce notes or
  unnecessary Airwallex PII in policy audit rows.
- **CON-001 — Upstream plans:** Execute after the Airwallex ledger, Salesforce
  snapshot, and AI evidence pipeline work is integrated.
- **CON-002 — Provider safety:** Use fixtures and fake providers in automated
  tests. Do not call live Airwallex, Salesforce, or model endpoints during
  implementation or verification. Do not deploy or push.
- **CON-003 — Expected inflow dates:** The current Salesforce snapshot schema
  does not include expected customer-deposit dates. Do not guess lateness from
  settlement time. Until a trusted expected-date source is mapped, that
  customer profile is incomplete and production decisions stay blocked.
- **CON-004 — Controlled mappings:** The industry exclusion list, fraud
  source, prior-default semantics, and signal-to-rating mappings must be
  versioned and explicit. Unknown values fail closed. The Typesafe.ai Jev
  signal-definition backlog item remains a production dependency.
- **CON-005 — Scope boundaries:** Do not change Salesforce synchronization,
  AI extraction or calibration, bridge collection fees, repayment allocation,
  or the number of open bridges. Multiple open bridges and cross-day fee
  changes remain deferred.

## Development approach

Use test-first development for the decision tables and input gates. Keep
underwriting calculations pure and dependency-free. Place the canonical
calculations in `shared/policy/`; keep `src/policy/` as a compatibility
facade for the two demo engines. Server orchestration loads trusted inputs,
records evaluations, and asks the ledger to reserve capacity. The browser can
display decisions but cannot create an approved command.

Use Airwallex whole-book reports for supported nightly historical features,
filtering results against the explicit roster. Use per-account REST reads with
`x-on-behalf-of` for fields not available in reports. Respect endpoint window
limits; the request-time path uses cached profile features and, when required,
only a current-balance read. Do not read historical endpoints on the
authorization path.

## Testing strategy

- Use Vitest unit tests for rating thresholds, tier mapping, starter behavior,
  caps, capital bands, doors, reason codes, and every hard gate.
- Use fake Airwallex sources and fixture profiles to test pagination, report
  completion, seven-day history windows, 90-day feature calculations, missing
  expected dates, malformed rows, retries, freshness, and atomic publication.
- Use PostgreSQL tests for immutable profile versions and policy evaluations,
  transaction rollback, concurrent requests, one-open-bridge enforcement,
  and facility reservations.
- Test customer isolation: one incomplete profile or evidence review blocks
  that customer without stopping other customers in a batch.
- Test that human-review, blocked, and declined outcomes create no transfer
  outbox command. Auto approval must revalidate policy and reserve within the
  ledger transaction.
- Test both demo engines against the same policy fixtures so they cannot
  drift from the shared rules.
- Run `npm test`, `npm run lint`, and `npm run build` after implementation
  changes. Never use live provider or model calls in automated tests.

## Solution overview

1. The daily job reads the active internal roster and builds one complete,
   versioned Airwallex underwriting profile per customer. It uses supported
   whole-book reports for bulk history and per-account REST reads for account
   standing and other missing fields.
2. The existing Salesforce worker publishes its atomic daily snapshot. The
   AI runner consumes that snapshot and supplies only qualified signals and
   the customer-level evidence gate.
3. The policy runner joins the three versioned inputs. It applies hard gates,
   calculates ratings, tier, and credit line, and stores a precheck result
   with stable reason codes. A failed customer does not stop the batch.
4. For each arriving request, the server checks cached profile freshness,
   current customer exposure, spend category, remaining line, capital band,
   and door. The request is processed sequentially with no model reordering.
5. Auto-approved requests enter the existing ledger reservation and
   transaction-outbox path. Human review reserves nothing; approval reruns
   the same checks under the facility lock before it can enqueue a transfer.

## Technical details

### Canonical Airwallex underwriting profile

Use an allowlisted, versioned profile containing the internal customer ID,
profile ID, 90-day window, capture time, source mode, query completion,
feature-definition version, and provenance IDs. Store derived values in USD
cents or documented ratios and keep unknown values distinct from zero.

The profile exposes average monthly settled inflows, expected-date reliability,
top-payer concentration, low-balance days, history days, eligible-spend
classification, connected-account standing, open RFI state, and repayment
history from the internal bridge ledger. Calculate monthly inflows from the
trailing 90-day settled cash-credit window divided by three. Calculate cash
buffer days from daily minimum balances below 10% of monthly spend. Exclude
authorization holds from spend; use captured transactions. Use the internal
ledger, not provider charge labels, as the authority for bridge repayment
history.

The available Salesforce snapshot does not contain expected deposit dates.
The profile builder must return an incomplete result rather than substitute
deposit settlement time, model inference, or a guessed date. Similarly,
industry, fraud, and prior-default inputs must have explicit mappings before
they can pass a production gate.

### Daily precheck and input gates

Apply gates in this order: active enrollment; profile and Salesforce
freshness/completeness; AI evidence readiness; connected-account standing;
Salesforce account, contract, KYB, credit-review, cap, and prior-default
states; 90-day history and no-go rules; then signal ratings, tier, and line.
Persist the first and all applicable stable reason codes so operators can see
every blocker. Keep a valid no-signal result distinct from extraction failure.

For a credit-review status of `watch`, retain the calculated line but require
human approval for each request. `hold` or `review_required` blocks the
customer. `approved` permits the normal deterministic path. Loyalty tier is
context only and does not raise a line or reorder sequential requests.

### Request-time decision and capital

Read historical risk features from the latest complete profile. A live request
may read only current balance data needed to validate the shortfall; history
calls are outside the latency budget. If the current-balance call times out or
returns invalid data, do not fund the request.

Use the ledger projection for facility amount, reserve floor, reservations,
open bridges, and outstanding exposure. Evaluate the door against the
pre-reservation band and then atomically recheck capacity under the same
facility lock used for reservation. The facility ledger keeps its 20% reserve
floor and zero separate operating buffer. Do not implement the deferred
five-point band-reopening hysteresis.

### Decision records and operator review

Use outcomes `ready`, `ineligible`, and `blocked` for daily prechecks;
use `auto_approved`, `human_review`, `declined`, and `blocked` for
requests. Data-quality and unresolved-review outcomes are `blocked`, not
business declines. A business rule failure is `ineligible` or `declined`
with stable reason codes.

Store policy version, input version IDs, profile window, evaluated amount,
computed line, tier, outcome, reason codes, and the ledger sequence used.
Keep records append-only. Human approval records actor, time, reason, and
approved terms. Approval reruns the policy against current data and creates no
provider operation unless all checks still pass.

## Alternatives considered

- **Recommended — cached profiles with deterministic policy:** Computes
  history-dependent features daily and makes a small request-time evaluation.
  It fits the 2.5-second authorization budget and preserves a complete audit
  trail.
- **Historical provider reads on every request:** Rejected because balance
  history has short endpoint windows, provider calls add unpredictable
  latency, and the data inventory sets a 2.5-second remote-authorization
  budget.
- **Model-ranked portfolio allocation:** Rejected for V1 because the policy
  must own every amount and outcome, and requests are handled in arrival order.
- **Browser-authoritative policy:** Rejected because the browser is
  unauthenticated and cannot safely authorize facility reservations or
  transfers.

## What goes where

- `shared/policy/`: canonical, pure ratings, tier, line, band, door, and
  request-check calculations.
- `server/src/underwriting/`: versioned Airwallex profile input, adapters,
  feature builder, publication, freshness, and daily runner.
- `server/src/policy/`: gates, precheck, request evaluation, audit
  repository, operator approval, and policy version configuration.
- `server/src/ledger/`: atomic facility reservation and transactional outbox;
  policy must use this boundary and must not call Airwallex operations itself.
- `src/policy/`, `src/kit5/`, and `src/state/`: demo compatibility
  facade and fixture scenarios using the same pure calculations.
- `tests/policy/`, `tests/server/underwriting/`,
  `tests/server/policy/`, and `tests/kit5/`: unit, database, and
  end-to-end fixture coverage.

## Implementation steps

### Task 1: Create one shared deterministic policy core

**Files:**

- Create: `shared/policy/types.ts`
- Create: `shared/policy/tiers.ts`
- Create: `shared/policy/creditLine.ts`
- Create: `shared/policy/facility.ts`
- Create: `shared/policy/bridge.ts`
- Modify: `src/policy/index.ts`
- Modify: `server/tsconfig.json`
- Modify: `tsconfig.app.json`
- Modify: `tests/policy/policy.test.ts`

- [x] Move the pure underwriting functions into `shared/policy/` and leave
  `src/policy/index.ts` as a compatibility re-export.
- [x] Add a versioned policy identifier and typed stable reason codes.
- [x] Apply the October 20 tier rule: reliability red or at least two red
  signals means no line; one other red means Tier C.
- [x] Implement starter multipliers and the three-on-time-repayment promotion
  rule using ledger-derived repayment history.
- [x] Add boundary tests for rating thresholds, overlapping red/yellow cases,
  starter transitions, all caps, and capital-band boundaries.
- [x] Configure the server TypeScript build to compile the shared policy
  sources without creating a second implementation.
- [x] Run focused policy tests before Task 2.

### Task 2: Add versioned Airwallex underwriting profiles

**Files:**

- Create: `server/src/db/migrations/005_underwriting_profiles.sql`
- Create: `server/src/underwriting/types.ts`
- Create: `server/src/underwriting/repository.ts`
- Create: `server/src/underwriting/publisher.ts`
- Create: `tests/server/underwriting/repository.test.ts`
- Create: `tests/server/underwriting/publisher.test.ts`

- [x] Define the allowlisted profile schema, source versions, 90-day window,
  per-query completion, capture time, source mode, and feature version.
- [x] Store immutable profile versions and a current pointer per customer.
- [x] Publish each customer profile atomically; retain the previous version and
  mark the current attempt failed when any required query or validation fails.
- [x] Use active roster customers only and process records in batches of at
  most 1,000.
- [x] Enforce a maximum profile age of 24 hours for new decisions.
- [x] Test atomic publication, completeness, freshness, failure isolation,
  unknown-versus-zero values, and source version retention.
- [x] Run focused profile persistence tests before Task 3.

### Task 3: Build daily Airwallex profile sources and features

**Files:**

- Modify: `server/src/airwallex/client.ts`
- Create: `server/src/underwriting/airwallex-source.ts`
- Create: `server/src/underwriting/feature-builder.ts`
- Create: `server/src/underwriting/report-worker.ts`
- Create: `server/src/underwriting/runner.ts`
- Create: `server/src/underwriting/fixtures/*.json`
- Create: `tests/server/underwriting/feature-builder.test.ts`
- Create: `tests/server/underwriting/airwallex-source.test.ts`
- Create: `tests/server/underwriting/runner.test.ts`

- [x] Use platform reports for supported whole-book history and filter
  resulting records against the explicit roster.
- [x] Use per-account REST with `x-on-behalf-of` for connected-account
  standing and fields not provided by reports; do not use Airwallex MCP for
  per-customer reads.
- [x] Fetch expiring report URLs immediately after completion and avoid
  logging credentials, report URLs, or unnecessary personal data.
- [x] Respect the seven-day balance-history window and provider pagination;
  compute the profile from a complete 90-day window only.
- [x] Compute monthly settled inflows, payer concentration, low-balance days,
  spend eligibility, account standing, and ledger-based repayment features
  from source-allowlisted records.
- [x] Leave inflow reliability incomplete when trusted expected deposit dates
  are unavailable; do not infer them from settlement timestamps or note text.
- [x] Add fixture and fake-provider tests for window boundaries, pagination,
  duplicates, report errors, malformed data, and bounded retries.
- [x] Run focused feature-source tests before Task 4.

### Task 4: Join policy inputs and enforce customer-level gates

**Files:**

- Create: `server/src/policy/input.ts`
- Create: `server/src/policy/gates.ts`
- Modify: `server/src/ai-evidence/policy-input.ts`
- Create: `tests/server/policy/input.test.ts`
- Create: `tests/server/policy/gates.test.ts`

- [x] Join only active roster state, a complete fresh profile, the latest
  complete Salesforce snapshot, and the AI pipeline's qualified policy input.
- [x] Enforce account active, contract active and unexpired, verified KYB,
  approved cap, approved credit review, Airwallex active status, and no open
  KYC or transaction RFI.
- [x] Route credit-review `watch` to human approval; block
  `hold`, `review_required`, unknown, unmapped, and missing states.
- [x] Block the customer for inactive enrollment, stale or incomplete inputs,
  unresolved evidence, failed extraction, or an unqualified signal type.
- [x] Apply explicit no-go mappings for industry, fraud, prior default, and
  concentration; fail closed until each required source mapping is verified.
- [x] Preserve a valid `no_signal` outcome and continue processing other
  customers when one customer's inputs fail.
- [x] Test every input state and confirm no raw CRM text reaches the policy
  calculation.
- [x] Run focused gate tests before Task 5.

### Task 5: Compute and persist daily precheck decisions

**Files:**

- Create: `server/src/db/migrations/006_policy_evaluations.sql`
- Create: `server/src/policy/precheck.ts`
- Create: `server/src/policy/repository.ts`
- Create: `server/src/policy/runner.ts`
- Create: `tests/server/policy/precheck.test.ts`
- Create: `tests/server/policy/repository.test.ts`
- Create: `tests/server/policy/runner.test.ts`

- [x] Add append-only daily evaluation records keyed idempotently by customer,
  profile, Salesforce snapshot, evidence run, and policy version.
- [x] Apply deterministic ratings, tiers, no-go checks, starter rules, and
  credit-line caps from the shared policy core.
- [x] Store input version IDs, computed ratings and line, outcome, stable
  reason codes, and evaluation time without duplicating raw source text.
- [x] Run once each day after that customer's required snapshots and evidence
  result are complete; do not calculate against partial inputs.
- [x] Continue the batch when a single customer's evaluation is blocked or
  fails.
- [x] Test idempotency, immutable history, policy-version changes, customer
  isolation, and source-version references.
- [x] Run focused precheck tests before Task 6.

### Task 6: Evaluate each bridge request deterministically

**Files:**

- Create: `server/src/policy/request.ts`
- Create: `server/src/policy/decision-service.ts`
- Create: `tests/server/policy/request.test.ts`
- Create: `tests/server/policy/decision-service.test.ts`

- [x] Validate request amount, USD currency, traceable spend category,
  customer precheck freshness, and unresolved risk gates.
- [x] Reject a second open bridge, an exposure above the current line, a
  balance more than two days late, or insufficient deployable facility.
- [x] Apply the current capital band and tier door before reservation.
- [x] Return `auto_approved`, `human_review`, `declined`, or
  `blocked` with stable codes and a deterministic display rationale.
- [x] Make no partial allocations and do not reorder requests or create a
  future-capital queue.
- [x] Use cached profile features at request time; allow only the required
  current-balance read and fail closed on timeout or invalid response.
- [x] Test each gate, door boundary, full-or-zero amount, and request retry.
- [x] Run focused request tests before Task 7.

### Task 7: Make policy evaluation and ledger reservation atomic

**Files:**

- Modify: `server/src/ledger/reservations.ts`
- Modify: `server/src/policy/decision-service.ts`
- Modify: `server/src/policy/repository.ts`
- Create: `tests/server/policy/reservation.test.ts`

- [x] Recheck snapshot/evidence readiness, open-bridge uniqueness, door,
  credit-line exposure, and facility capacity under the ledger transaction.
- [x] Store the policy evaluation and facility reservation in the same
  transaction before creating the existing outbox command.
- [x] Enqueue a transfer only for an auto-approved decision or a currently
  revalidated human approval.
- [x] Create no reservation or outbox command for blocked, declined, or
  pending human-review outcomes.
- [x] Make concurrent requests serialize against the facility lock and return
  deterministic outcomes without exceeding deployable capital.
- [x] Test rollback, idempotent retries, concurrent requests, and human
  approval after input versions change.
- [x] Run focused transaction tests before Task 8.

### Task 8: Add policy APIs and operator approval

**Files:**

- Create: `server/src/http/routes/bridge-decision.ts`
- Create: `server/src/policy/approval-cli.ts`
- Modify: `server/src/http/server.ts`
- Modify: `server/src/main.ts`
- Modify: `package.json`
- Create: `server/src/db/migrations/008_remote_authorization.sql`
- Modify: `server/src/config.ts`
- Modify: `server/src/roster/repository.ts`
- Modify: `server/src/roster/cli.ts`
- Modify: `server/src/policy/approval-cli.ts`
- Create: `server/src/policy/remote-authorization.ts`
- Modify: `server/src/ledger/reservations.ts`
- Modify: `server/src/snapshots/freshness.ts`
- Modify: `server/src/underwriting/repository.ts`
- Create: `server/src/http/routes/remote-authorization.ts`
- Create: `tests/server/policy/bridge-decision-route.test.ts`
- Create: `tests/server/policy/bridge-decision-auth.test.ts`
- Create: `tests/server/policy/approval-cli.test.ts`
- Modify: `tests/server/schema.test.ts`
- Create: `tests/server/policy/remote-authorization-binding.test.ts`
- Create: `tests/server/policy/remote-authorization-route.test.ts`

The v2 remote authorization ingress persists only allowlisted transaction IDs,
account/card/customer binding, amount, currency, transaction kind, category,
and merchant category code. Card bindings are operator-recorded and audited;
unmapped cards and spend categories fail closed. This is a Task 8 boundary
addition because the documented provider request is the trusted request source.

- [x] Add a server endpoint that computes a decision from trusted stored inputs;
  reject client-supplied approval, tier, line, or reason-code fields.
- [x] Return the evaluation ID, outcome, amount, line, reason codes, and
  freshness/version metadata without exposing provider secrets or raw notes.
- [x] Add operator-only list, inspect, and approve actions requiring actor and
  reason, matching the roster and evidence-review CLI patterns.
- [x] Re-evaluate exact terms and current inputs during human approval.
- [x] Test invalid requests, unauthorized direct approvals, response
  redaction, and successful/failed operator actions.
- [x] Run focused API and CLI tests before Task 9.

### Task 9: Keep demo and live consumers aligned

**Files:**

- Modify: `src/kit5/engine.ts`
- Modify: `src/state/machine.ts`
- Modify: `src/services/types.ts`
- Modify: `src/services/mock/AgentClient.ts`
- Modify: `src/components/PolicyChecks.tsx`
- Modify: `src/components/UnderwritingPipeline.tsx`
- Modify: `src/components/ApprovalCard.tsx`
- Modify: `tests/kit5/engine.test.ts`

- [x] Run both demo engines through the shared pure policy functions and
  fixture-backed snapshot/evidence gates.
- [x] Display blocked, ineligible, human-review, and declined outcomes with
  deterministic reason codes and useful text.
- [x] Keep demo fixture mode deterministic and prevent browser state from
  authorizing a live bridge.
- [x] Show the exact line, request amount, door, and review requirement before
  presenting a human approval action.
- [x] Add regression scenarios for stale snapshots, review-required evidence,
  valid no-signal, watch status, prior default, and capital exhaustion.
- [x] Test both demo versions against the same decision fixtures.
- [x] Run focused UI and scenario tests before Task 10.

### Task 10: Verify end-to-end acceptance and document the policy boundary

**Files:**

- Modify: `README.md`
- Modify: `docs/EDITING.md`
- Create: `tests/server/policy/acceptance.test.ts`
- Modify: this implementation plan

- [x] Test an eligible customer from roster through profile, Salesforce
  snapshot, qualified evidence, precheck, request decision, and ledger
  reservation using fixtures and a test database.
- [x] Test a blocked customer at each missing, stale, unqualified, review, and
  no-go gate while confirming other customers continue.
- [x] Verify the decision log references every input version and rule version.
- [x] Verify no model or browser path can set ratings, tiers, lines, amounts,
  or transfer commands.
- [x] Run `npm test`, `npm run lint`, and `npm run build`; do not contact
  live providers or deploy.
- [x] Document which sources own risk data, policy decisions, and bridge
  execution, including the external gates for production use.
- [x] Mark implementation tasks complete and move this plan to
  `docs/plans/completed/` when implementation is complete. The exec workflow
  moves it after reviews.

## Post-completion gates

Production decisioning remains disabled until all of these gates are satisfied:

- Airwallex permissions, report contents, pagination, and account-level REST
  access are verified for the production environment.
- A trusted expected-deposit-date source is added to the canonical profile;
  settlement timestamps are not an acceptable substitute for due dates.
- Industry exclusion, fraud, prior-default, and signal-to-rating mappings are
  approved and versioned.
- Jev's signal definitions are settled, and each signal type meets the
  qualification and audit conditions in the AI evidence plan.
- A production authentication and authorization design protects policy review
  and bridge approval commands.

## Risks and assumptions

- The current snapshot contracts do not yet supply every underwriting fact.
  Fixture flows can be completed, but production requests remain blocked until
  the required source mappings exist.
- Cash-buffer thresholds are proposed in the source requirements. Use them as
  versioned V1 defaults for fixtures and require empirical validation before
  production enablement.
- The 10% mature-portfolio cap is operator-configured. No undocumented
  maturity threshold is introduced.
- The source requirements contain older reserve-floor values. Follow the
  current Airwallex ledger plan's 20% facility floor; keep the separately
  approved operating buffer at zero.
- One open bridge per customer, USD-only operation, trigger-deposit collection
  caps, fixed collection-trigger fees, and deferred multi-bridge allocation
  remain owned by the Airwallex bridge ledger plan.

## See also

- [Airwallex bridge ledger implementation plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md)
- [Salesforce customer snapshot implementation plan](/docs/plans/20261021-salesforce-snapshot-implementation-plan.md)
- [AI evidence pipeline implementation plan](/docs/plans/20261022-ai-evidence-pipeline-implementation-plan.md)
- [Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md)
- [Airwallex underwriting data inventory](/docs/requirements/Airwallex%20Hackathon_req_6.md)
- [Bridge Capital Underwriting Rules v001](/docs/requirements/Airwallex%20Hackathon_req_8.md)
- [Typesafe.ai Jev signal-definition backlog item](/docs/backlog/work-with-typesafe-jev-on-signal-definitions.md)
