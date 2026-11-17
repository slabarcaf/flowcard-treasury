# Underwriting engine

Proposed hackathon design for a separate Go service that evaluates a detected
shortfall against a versioned customer picture and deterministic rules.
Implementation is not authorized by this document.

[TOC]

## Decisions and scope

- Service: `services/underwriting-engine`, separate from shortfall detection.
- One synchronous API process, PostgreSQL persistence, explicit migration and
  snapshot-import commands. No worker, broker, scheduler, or rules framework.
- V1 covers structured customer facts and hard rules only. AI extraction,
  unstructured-source connectors, UI, offers, human approval workflows,
  reservations, transfers, and repayment execution are later milestones.
- Decisions are assessments, not permission to disburse. Always return
  `requires_recheck: true` and `funding_authorized: false`.
- Use the current Bridge Capital product policy as the service baseline.
  `shared/policy/` implements an older policy family; its signal scoring,
  starter limits, and existing AI gate are not silently mixed into this one.
  Existing Node/frontend paths remain unchanged until a later cutover.
- Persist policy ID `bridge-capital-go-v1` and a hash of its checked-in config.
  All thresholds below are demo defaults, not validated credit policy.

## Inputs and ownership

### What the customer picture means

The customer picture is the structured input to one evaluation: standing,
cash-flow history, existing debt, expected repayment and the specific request.
A snapshot is a saved version of those input facts, with sources and capture
times. It lets an operator answer why a decision was made after the original
systems have changed. It is not a new financial source or an AI-generated
customer profile. The initial import CLI is a demo data-loading choice;
automated source collection remains an explicit integration task.

Source collection, input normalization and decision evaluation are distinct:

1. Source adapters or an operator obtain records from their owning systems.
2. The importer validates structured facts, linkage and declared completeness.
3. The engine reads those facts and applies the rules specified below.
4. The decision preserves exactly which versions and values it used.

The coordinator can request context and investigate missing information. It
does not gain authority to publish financial facts by producing an AI summary.
For the initial demo, an operator imports the explicitly labeled synthetic
customer/facility fixtures. No background source-sync platform is required.

Ramil selected prepared fixtures as the intended first demonstration path.
The demo runs real imports, rules, persistence and authenticated API calls over
synthetic financial records. Continuous ingestion for actual customers is a
separate integration milestone, not a prerequisite for implementing this demo.

### Source of each decision input

These are the intended source owners, not a claim that the Go connectors exist.

| Input | Intended authoritative source | Current design boundary |
| --- | --- | --- |
| Shortfall, payment/account/card IDs | Go shortfall service | Authenticated read client |
| Onboarding completion and platform identity | Company onboarding service and trusted roster mapping | Verified imported standing facts |
| Account active/frozen standing | Airwallex account state | Verified imported account facts |
| Relationship start | Verified customer relationship record | Source/date mapping required; do not use import time |
| Daily wallet balances and settled inflows | Airwallex balance/transaction records | History adapter still required |
| Previous debt, late/default events and reservations | Funding and Collection ledger | Funding contract in parallel design |
| Facility size, reserve and committed capital | Funding and Collection ledger/config | Versioned imported facility picture initially |
| Receivable due/unpaid amounts | Verified invoice/accounting records | Actual source mapping unresolved |
| Receivable allocations | Funding and Collection ledger | Live ownership belongs to funding |
| Structured CRM account/contract context | Salesforce account/contract records | Context or specifically verified fields |
| Support/call notes and document claims | Salesforce notes/documents via coordinator | Pending AI evidence, outside hard rules in v1 |
| Thresholds, percentages and prices | Checked-in versioned policy config | Owned by this service |

Salesforce is one contributor. It is not the source of wallet balances,
FlowCard loan balances or available facility capital. The retained Salesforce
snapshot includes account/contract/case/activity context but does not establish
the required expected-receivable due dates. That gap is already recorded in
the expected-deposit source backlog. Do not claim a Salesforce invoice or
payment-date field is mapped merely because a note mentions a payment.

### Existing source inventory for this implementation

| Facts | Existing implementation evidence | Underwriting service handling |
| --- | --- | --- |
| Shortfall amount, customer/account/card, outcome and balance observation | Shortfall detail DTO and authenticated `GET /api/shortfalls/{id}`; transaction ID is nullable and environment is not returned | Fetch by configured server URL and verify all linkage/amount fields; require transaction ID for the payment identity. Bind mode to configuration. |
| Active customer/account/card mapping | `public.customer_roster`, `public.issuing_card_bindings`; shortfall mapping reader performs read-only linkage checks | Importer performs SELECT-only active mapping validation. It never enrolls customers or edits mapping tables. |
| Onboarding and account standing | Existing roster identity and onboarding/provider systems; no complete verified standing snapshot is supplied by this service | DEMO facts are explicitly synthetic. SANDBOX requires verified imported references; no live adapter is delivered here. |
| Historical balances and settled inflows | Airwallex records are referenced by existing shortfall/provider paths, but no underwriting history adapter or complete 90-day history API exists | Import structured, versioned history. DEMO supplies synthetic records; future connector work must establish coverage and inflow kinds. |
| Funded exposure, unpaid fees, reservations, defaults and facility commitments | Funding/collection ownership is being designed separately; no live underwriting-readable ledger contract exists in this service | Import a versioned facility/customer snapshot. SANDBOX completeness and provenance must be verified; DEMO records are synthetic. Customer exposure includes principal, all unpaid fees and active principal-plus-fee reservations, without counting converted reservations twice. Facility cash uses principal commitments only. |
| Eligible receivables, due dates and allocations | The current Salesforce snapshot omits expected-receivable dates; source ownership and completeness remain open in the expected-deposit backlog | Require explicit verified dates, unpaid amounts and allocation completeness. Unknown due-date evidence yields UNKNOWN/REVIEW_REQUIRED. DEMO fixtures supply labeled synthetic values. |
| Merchant category and classification provenance | Shortfall details identify the payment but do not establish the checked-in underwriting classification | Require a verified classification reference when a gate depends on it. Unknown categories remain UNKNOWN; no default-to-standard inference. |
| CRM notes and document claims | Salesforce snapshots retain contextual case/activity data, not validated numeric receivable facts | Excluded from hard-rule inputs. No extraction or AI call in this service. |

These boundaries describe existing evidence and required adapters, not completed
live data delivery. In particular, the locally imported snapshot is a preserved
set of declared inputs; it is not a source-sync system or proof that a live
customer's financial facts have been verified.

### Policy comparison and resolution

The Go service follows the current Bridge Capital product baseline in
`docs/designs/bridge-capital-policy-spec.md` and
`src/features/bridge-capital/policy/`. The older `shared/policy/` family and
older server underwriting paths remain separate and are not migrated here.

| Topic | Current product baseline | Older server/shared behavior | Go service resolution |
| --- | --- | --- | --- |
| Tier evidence | Tenure, late/default history and near-zero balance days | `shared/policy/` tiers from four signal ratings; starter limits until three on-time repayments | Use the current product's deterministic tier gates. No signal score or starter multiplier. |
| History | At least three months and 90 days; tiers use relationship and balance history | Older server profile/evidence gates do not provide this exact structured trailing-window contract | Require three calendar months and 90 distinct complete trailing UTC dates. Use server UTC date, not the last row date. |
| Customer line | Tier percentage of monthly inflows against outstanding plus new bridge | Older server can apply starter/customer/facility caps; legacy paths also use different policy rules | Tier line only, with total debt exposure including funded principal owed, all unpaid fees, active reservation principal and fees, then requested principal and estimated fee. Converted reservations are not duplicated. |
| Receivables | Expected receivable due within chosen term | Older policy may gate on evidence/profile signals; current Salesforce snapshot has no mapped expected due date | Require verified eligible unpaid amount net of paid/cancelled/disputed portions, subtract known allocations once, and cover principal plus fee. Missing date or allocation evidence is UNKNOWN. |
| Spend category | Category maps to critical, standard or discretionary; product baseline defaults unknown categories to STANDARD | Legacy signal/no-go classification is distinct | Require verified category when needed; unknown classification is UNKNOWN, not STANDARD. |
| Capital | Product regime thresholds and reserve floor | Shared policy uses different deployable-capital bands, floor percentages and tier doors | Apply the design's explicit ABUNDANT/NORMAL/TIGHT/CRITICAL/AT_FLOOR thresholds to facility principal commitments. Fees do not increase available cash. |
| Default and delinquency | Current product excludes a default for 12 months; Tier B says no defaults | Shared policy maps prior-default/evidence signals; server history formats differ | Use recorded `defaulted_on`, the design's explicit 365-day general gate and lifetime no-default requirement for Tier B. Missing lifetime coverage is UNKNOWN. |
| Outcome and authority | Offer acceptance and funding are later steps | Older server policy paths may include AI/evidence gates or operate in authorization flows | Return an assessment only; always `funding_authorized: false` and `requires_recheck: true`. No AI gate or funding action. |

### DEMO identity and expected outcomes

The fixture pack uses the existing synthetic roster/card mapping pairs below.
They are independent scenario identities and are not evidence for real
customers. A fixture run uses fresh shortfall/payment UUIDs and idempotency keys
under one run ID, while retaining these stable mapped identities.

| Scenario | Customer ID | Account ID | Card ID | Request facts | Expected outcome |
| --- | --- | --- | --- | --- | --- |
| `eligible` | `acct_bloom001` | `acct_bloom001` | `card_synthetic_bloom001` | $7,000, 30 days, Tier B line $20,000, $12,000 prior exposure, $8,000 free receivables | ELIGIBLE; $190 fee, $7,190 total; all ten rules PASS. |
| `capacity_exceeded` | `acct_atlas001` | `acct_atlas001` | `card_synthetic_atlas001` | Same facts except $14,000 prior exposure | INELIGIBLE; post-request total exposure $21,190 and `CUSTOMER_CAPACITY` FAIL. |
| `missing_receivable_dates` | `acct_nova001` | `acct_nova001` | `card_synthetic_nova001` | Same facts as eligible except invoice due date is null and unverified | REVIEW_REQUIRED; `RECEIVABLE_COVERAGE` UNKNOWN. |

All three use a synthetic $1,000,000 facility with a 20% reserve, $80,000
funded principal and $20,000 reserved principal. This leaves $700,000 above
the reserve floor and an ABUNDANT regime. Customer history is nine calendar
months, three complete $100,000 monthly-equivalent inflow months, 90 complete
balances with no near-zero days, active standing, complete lifetime-default
coverage and no late/default records. This proves Tier B, not Tier A. Existing
exposure records must reconcile to $12,000/$14,000 per scenario; the facility
snapshot's other-customer totals are explicitly synthetic. The missing-date
scenario changes only receivable date verification, and the capacity scenario
changes only prior customer exposure.

For each imported field, record its source ID/version, effective date and
capture time. An importer can validate shape and consistency; verified source
meaning remains a prerequisite for SANDBOX facts. DEMO fixtures are synthetic.

### Evaluation request

The evaluation request supplies `shortfall_id` and `term_days` (integer 7–90),
not customer identity, amounts, account IDs, tier, or capital figures. The
caller chooses a candidate term; this service assesses it and computes a fee
estimate. Offer creation and customer acceptance happen afterward.

Fetch the shortfall through its authenticated internal GET, verifying identity,
customer/account linkage, USD, positive SHORTFALL_DETECTED outcome, amount
consistency, and balance observation time. Never trust caller-provided facts.
Use a server-owned shortfall base URL bound to the configured source mode.
The current shortfall read DTO does not expose environment/mode; do not invent
such a response field. Verify its configured endpoint identity, use compatible
snapshots, and retain that source binding on the decision.

Define payment identity as mode +account ID +parent transaction ID, retaining
the canonical authorization event separately. Reject a missing parent reference
as invalid shortfall input. Funding/reservation duplicate checks use payment
identity, so separate declined attempts cannot independently fund one payment.

For the demo, require the balance observation to be at most five minutes old
and no more than five seconds in the future. An old observation returns
REVIEW_REQUIRED/SHORTFALL_STALE; the underwriting service does not recalculate
or mutate it. Preserve the request's exact original evidence. Funding must
eventually fetch fresh payment/balance state regardless of this age check.

Customer and facility facts arrive as validated, server-imported snapshots.
Use a local `cmd/import-snapshot` CLI with restricted database access, not a
public upload API. It is a concrete runnable source for the demo. Source mode
is DEMO or SANDBOX; deployments cannot mix modes within a decision. Existing
published Node underwriting profiles lack several required fields and must
not be treated as complete snapshots without an explicit future adapter.

No Airwallex/Salesforce ingestion or historical-data acquisition is implemented
here. A sandbox snapshot must contain verified source references; a manually
authored synthetic snapshot is DEMO. Local fixtures demonstrate policy behavior
without claiming verified live customer readiness.

## Customer snapshot contract

Every snapshot has immutable UUID, customer/account IDs, source mode, captured
UTC time, source references/versions, explicit completeness per data group,
and `schema_version: 1`. Require valid linkage to existing active roster and
card/account mapping. Customer snapshot age limit: 24 hours inclusive.
Future capture time beyond five seconds is invalid. Missing is never zero.

| Group | Required facts |
| --- | --- |
| Standing | relationship_started_on, onboarding_complete, active, suspended |
| Balances | USD end-of-day date/cents, coverage start/end, completeness |
| Inflows | settled USD inflow ID/date/cents, kind, payer ID, coverage |
| Bridge history | bridge/payment IDs, principal and fee owed, state, dates |
| Reservations | reservation ID, payment ID, principal and fee commitment, active state |
| Receivables | invoice/payer IDs, USD amount unpaid, due date, allocations |
| Classification | canonical payment ID, verified merchant category/source |

Import validates types, nonnegative amounts, unique IDs/dates, UTC date forms,
consistent bridge states/dates and sums, no duplicate reservation/bridge
exposure, and completeness claims. Malformed imports fail without publishing
a new current snapshot. A valid snapshot may explicitly contain an incomplete
group; evaluation then reports missing evidence rather than false eligibility.

Snapshot publication is atomic and uses monotonically increasing versions per
customer. Reject older capture times becoming current, or repeated versions
with different content. Keep previous snapshots for decision reproducibility.

### Feature definitions

- Evaluate against server UTC date, never the date of the last available row.
- History floor: relationship at least three calendar months, plus complete
  end-of-day balance observations for the last 90 completed UTC dates.
  Compare calendar-month anniversaries with month-end clamping.
- Average monthly inflows: qualifying settled external USD credits across
  those 90 dates, summed and divided by three, rounding down to cents. Exclude
  own-account transfers, reversals/refunds, and bridge funding. Unknown inflow
  kinds make the group incomplete; do not guess that every deposit is revenue.
- Near-zero days: count balances strictly below 10% of that 90-day average
  daily balance. Use exact comparison without floating-point rounding.
  An all-zero history has 90 near-zero days, avoiding an artificial Tier A.
- Track late closures in the last 180 days and default events in the last
  365 days. A default uses its recorded defaulted_on date even if never closed.
  B's no-default-ever requirement needs complete lifetime default evidence.
- Ramil confirmed total-debt line exposure: funded principal owed plus all
  unpaid fees (including earned overdue fees), plus unfunded active reserved
  principal and fee commitments. Do not count a reservation again after it
  becomes funded. The proposed bridge adds principal plus its estimated fee.
  Receivable coverage also includes the new principal and fee.
- Payer concentration and balance trends may be displayed as context, but do
  not add an unapproved rule or AI score in v1.

## Policy rules and result semantics

Each rule yields `rule_id`, `status` (PASS/FAIL/UNKNOWN), controlled reason,
actual value, threshold, and snapshot/source references. Evaluate all rules
that have their required inputs; dependent rules are UNKNOWN when inputs are
missing. Never substitute a zero tier, balance, or limit for unknown data.

The public Go evaluator executes the following rule catalogue. Conditions use
the evaluation's fixed UTC instant and the calculation definitions below.
Each known false substantive condition is FAIL; missing prerequisites are
UNKNOWN. INPUT_FRESH is a data gate: unavailable/stale evidence is UNKNOWN.

| Rule ID | Exact condition for PASS | Missing-evidence behavior |
| --- | --- | --- |
| INPUT_FRESH | Customer age <=24h; facility/shortfall age <=5m; capture <=now+5s; required source groups and mode/linkage valid | UNKNOWN for affected groups/dependent rules |
| CUSTOMER_ACTIVE | onboarding_complete=true AND active=true AND suspended=false AND provider account active | UNKNOWN if required standing facts unverified |
| HISTORY_SUFFICIENT | relationship start <=three-month anniversary AND 90 distinct complete daily balances in trailing window | UNKNOWN for incomplete history; known insufficient tenure is FAIL |
| NO_OVERDUE_OR_DEFAULT | No positive principal/fee debt due before today AND no default event dated within [today-365d,today] | UNKNOWN if debt/default coverage incomplete |
| CUSTOMER_CAPACITY | principal_owed +unpaid_fees +active_reserved_principal_and_fees +requested_principal +estimated_fee <=floor(monthly_inflows*tier_bps/10000) | UNKNOWN if tier/inflows/exposure/request unusable |
| PAYMENT_NOT_BRIDGED | Zero active funding reservations/funded obligations for mode/account/parent-payment key | UNKNOWN if payment identity or ledger coverage missing |
| RECEIVABLE_COVERAGE | Sum of eligible free invoice cents >=requested principal +estimated fee | UNKNOWN if due dates/unpaid amounts/allocations unverified |
| CAPITAL_AVAILABLE | requested_principal <=max(0,facility_total-funded-reserved-ceil(facility_total*reserve_bps/10000)) | UNKNOWN if facility inputs incomplete/stale |
| SPEND_ALLOWED | Any spend in ABUNDANT/NORMAL; CRITICAL spend in TIGHT/CRITICAL; none at floor | UNKNOWN if needed merchant classification absent |
| REGIME_TIER_ALLOWED | Any eligible tier in ABUNDANT/TIGHT; A/B in NORMAL; A in CRITICAL; none at floor | UNKNOWN if required tier/regime cannot be computed |

Source completeness applies to the facts a rule uses, not to every optional
context field. Missing CRM notes cannot block these numerical rules. Known
closed capital gate can FAIL independently of missing merchant classification.

### Evaluation order and worked trace

1. Validate source scope, completeness and freshness per input group.
2. Compute history, inflows, repayment features, tier and line when possible.
3. Calculate requested-term fee, free receivable coverage and capital regime.
4. Evaluate every rule with known operands, marking missing dependencies
   UNKNOWN. Record all outcomes and their actual values, not only first failure.
5. If any rule FAILS, return INELIGIBLE. Otherwise if any rule is UNKNOWN or
   the regime requires human approval, return REVIEW_REQUIRED. Else ELIGIBLE.
6. Save inputs, rule trace, policy version/hash and result atomically.

Example with valid complete inputs: shortfall $7,000, 30 days, proven Tier B,
monthly inflows $100,000, total funded/reserved exposure $12,000,
free in-term receivables $8,000, and abundant available capital $100,000.
The line is $20,000; exposure after request is $19,190, so capacity passes.
Fee is $190, required coverage $7,190; $8,000 passes. With valid standing,
history, no overdue/default and no existing payment bridge, all rules PASS
and the result is ELIGIBLE. Funding authorization remains false.

Change existing total exposure to $14,000: exposure becomes $21,190, producing
FAIL/CUSTOMER_CAPACITY and INELIGIBLE. Change only invoice due-date evidence
to unavailable: RECEIVABLE_COVERAGE becomes UNKNOWN and the result is
REVIEW_REQUIRED. Neither case needs an AI score to reach its result.

Only usable, fresh evidence can produce a substantive PASS or FAIL; missing
or stale prerequisites produce UNKNOWN for dependent rules. A shortfall too
old to assess makes request-amount-dependent rules UNKNOWN.
Known hard-rule FAIL produces INELIGIBLE, even if another rule is UNKNOWN.
If there is no FAIL but an UNKNOWN exists, return REVIEW_REQUIRED. If all
rules PASS but policy requires human approval, return REVIEW_REQUIRED with
HUMAN_APPROVAL_REQUIRED. Otherwise return ELIGIBLE. Also return
`approval_required`, applicable tier/line/headroom, principal, term, fee/total
estimate, and all reasons. Null values remain null when evidence is unknown.
A data-quality review cannot be converted into eligibility by an override
endpoint in this phase.

### Tiers and customer capacity

First apply history and recent-default gates. Then evaluate A, B, C in order:

- A: tenure >=12 months, no late closures/defaults in 180 days, <=5 near-zero
  days in 90 days. Line =30% of average monthly settled inflows.
- B: tenure >=6 months, <=1 late closure in 180 days, no default ever.
  Line =20% of inflows.
- C: remaining eligible customers. Line =10% of inflows.

Use the highest tier proven by complete evidence. If missing evidence could
change the tier or decision, hold for review rather than assuming a lower tier.
Round line down to integer cents. No starter multipliers or additional
concentration caps in this version. Record these differences in a policy
comparison table against the old server policy before implementation.

### Term and receivable coverage

Preserve the demo price: fee =5000 cents + principal *200 basis points *
term_days / (10000 *30), rounding the percentage component half up once.
Total new obligation =principal + fee. Use checked integer arithmetic or
standard-library big integers for intermediate products; no float money.

Count only verified eligible unpaid USD amounts with due dates from today
through today +term_days, inclusive. The eligible unpaid amount is already net
of paid/cancelled/disputed portions; do not subtract those twice. For each
invoice compute free =max(0,eligible_unpaid_cents -allocated_cents). Require
explicit allocation completeness, including a known empty allocation set.
Sum free amounts once per unique invoice and require
coverage >=the new principal plus estimated fee. Do not double-subtract an
existing debt already represented by invoice allocations.

Known complete insufficient coverage is FAIL. Missing due-date source,
unverified dates, uncertain outstanding amounts or unknown allocations are
UNKNOWN. Never infer a due date from settlement time or a free-text promise.
No receivable haircut, payer prediction or AI inference in this phase.
The expected-deposit source backlog remains open until an actual source is
integrated. DEMO fixtures supply explicitly synthetic dates/allocations.

### Facility snapshot and approval policy

Facility snapshot: immutable ID/version/source mode/capture time, USD facility
total, reserve basis points, funded outstanding principal, reserved principal,
and source-ledger version/completeness. Age limit: five minutes inclusive.
All sums must agree with the snapshot's counted obligations. No provider wallet
balance substitutes for the facility ledger.

Available =max(0, facility total -funded -reserved -reserve floor). Round the
reserve floor up to cents. Regime ratio =available /facility total, using exact
integer comparisons. New request does not affect its own pre-decision regime.

| Regime | Available share | Gate and approval |
| --- | --- | --- |
| ABUNDANT | >60% | Any eligible tier/spend; automatic assessment |
| NORMAL | >30% through 60% | B or better; any spend |
| TIGHT | >10% through 30% | Critical spend; human if principal >$5,000 |
| CRITICAL | >0% through 10% | Tier A, critical spend, human required |
| AT_FLOOR | 0% | No new bridge |

Use a checked-in merchant-category mapping aligned with the current product.
Unknown category is UNKNOWN when classification is needed, rather than default
STANDARD. A known category never grants criticality from a browser label.
Ranking competing requests and reserving scarce funds are deferred. Several
assessments may be eligible against the same snapshot; no capital is promised.
Funding must later atomically reserve/recheck exposure, receivables and
facility.

### Funding recheck contract

The proposed Funding and Collection service owns offers, acceptance/approval,
live reservations and servicing ledger. A saved evaluation is a candidate
assessment. Funding must recheck current financial capacity before dispatch.

Expose the pure evaluator as public package `policy` in this Go module
(`services/underwriting-engine/policy`), with its normalized input/result types
and embedded versioned config. Both services call the same package. It has no
HTTP, database, credentials, agent or provider dependencies. Keep adapters and
snapshot persistence internal. Pin the same policy version/hash at offer and
funding recheck; a mismatch requires reevaluation and refreshed terms/approval
where applicable. This is one shared library, not a generic policy framework.

Funding fetches verified non-ledger inputs outside its database transaction,
retaining snapshot ID/version/mode/capture time. Inside its own transaction it
locks authoritative customer/facility/receivable state, substitutes those live
financial values into the normalized input, evaluates the shared rules and
atomically reserves only if permitted. No service HTTP calls occur under locks.
Stale, incomplete or conflicting non-ledger inputs block dispatch. Accepted
terms, required approvals, mapping and operation-idempotency checks remain
additional funding-owned guards. Shared rules never manufacture consent.
Current snapshot freshness still requires funding's later payment/balance
recheck; eligibility alone does not establish a payable provider obligation.

## Service flow and API

1. Authenticate internal caller and validate request/idempotency key.
2. Replay an already committed identical request before querying dependencies.
3. Fetch authoritative shortfall, then read latest compatible customer/facility
   snapshots in one short repeatable-read transaction. No HTTP runs inside it.
4. Validate snapshot linkage/freshness and compute features/rules in memory.
5. Store immutable decision plus copied normalized inputs and policy hash.
   An identical concurrency race returns the first committed decision.

Use existing shortfall-service HS256 assertion convention with dedicated
audience `flowcard-underwriting-service`. Subject `underwriting-evaluator`
can evaluate/read. No browser tokens or onboarding session cookie accepted.
Use a distinct configured signing secret. The shortfall client uses its
existing audience/subject with its server-side secret. No shared Go framework.

Wire fields, errors, snapshot publication and demo commands are pinned in
[the implementation contract](/docs/api/underwriting-contract.md). This draft
and plan use Ramil's confirmed principal-plus-fees total-debt limit convention.

- POST `/api/underwriting/evaluations`, body `{shortfall_id, term_days}`,
  required `Idempotency-Key` opaque 1–128 safe ASCII characters. First write
  returns 201, completed same-body replay 200; mismatched body 409.
- GET `/api/underwriting/evaluations/{id}` returns saved decision (404 unknown).
- GET `/api/underwriting/evaluations?customer_id=...&cursor=...` returns 25
  rows, opaque scoped descending (created_at,id) pagination.
- GET `/api/underwriting/customers/{customer_id}/context` provides the
  coordinator with a bounded read-only customer picture: current snapshot
  ID/version/mode/capture time, completeness/freshness, normalized rule inputs
  and verified source references. Exclude raw documents, free-text notes and
  credentials. Use the same internal authorization and no-store response.
  Unknown customer/snapshot is 404; DB failure is 503. Existing incomplete or
  stale context is returned with explicit flags for investigation, never
  represented as verified-current. This endpoint is proposed, not implemented.
  Include `schema_version`, snapshot ID/version and source mode so funding can
  bind verified non-ledger inputs to an exact snapshot. Its adapter must use
  only non-ledger customer/history/classification facts from this response;
  financial capacity/allocations come from funding's locked authoritative state.
- `/healthz` is process liveness; `/readyz` checks DB/migrations, policy and
  client configuration. It discloses source mode and does not claim external
  data/provider verification based solely on valid configuration.

Scope idempotency by authenticated caller +configured mode +key; fingerprint
the canonical request. Replay returns its original inputs/result even after
snapshot changes. A deliberate reevaluation uses a new key and new decision
linked by shortfall ID. Decisions do not themselves mark a payment bridged.
No automatic retries of POST with a changed key.

Missing/stale customer/facility data produces a persisted REVIEW_REQUIRED
with explicit missing inputs. An invalid/nonpositive/wrong-mode shortfall
returns 422 without a decision; unknown shortfall is 404. Dependency outages
or malformed dependency responses are 503 with no fabricated decision.
Malformed request/query is 400, auth 401/403, local persistence failure 503.
All responses are no-store and exclude raw documents/secrets.

## Persistence and packaging

Use existing PostgreSQL instance, separate `underwriting` schema and restricted
runtime role. Three tables: `customer_snapshots`, `facility_snapshots`, and
`evaluations`. Snapshots and evaluations are append-only. Unique customer
version, facility version, and caller/mode/idempotency key; selection indexes
on capture/version and read cursors. Import publication locks the relevant
customer/facility key in a short transaction to prevent out-of-order current
state. No mutable current table required.

Customer snapshot imports read existing mappings but never enroll customers.
No foreign-service table updates or shortfall schema dependency. Keep a small
`policy.v1.json` embedded in the service as the single threshold source for
this service; expose its version/hash on results. No hot reload/editor UI.

Commands: `cmd/api`, `cmd/migrate`, `cmd/import-snapshot`. Public package:
`policy`, owning pure normalized types and rules. Internal packages:
`domain` (service-specific DTOs), `snapshots`, `shortfall`, `postgres`, `http`,
and `config`.
Use standard library and already approved pgx versions. Local opt-in Compose
uses an unused loopback port (default 8083); no existing listener changes.

## Examples and verification boundary

### Prepared interactive demo fixtures

Deliver a named fixture pack with three presenter-ready scenarios:

| Scenario | Financial setup | Expected result |
| --- | --- | --- |
| eligible | $7,000 request, 30 days, Tier B, $20,000 line, $12,000 exposure, $8,000 free in-term receivables, abundant capital | ELIGIBLE; $190 fee; all rules PASS |
| capacity_exceeded | Same facts except $14,000 exposure; request raises it to $21,190 including fee | INELIGIBLE; CUSTOMER_CAPACITY FAIL |
| missing_receivable_dates | Same eligible case except explicitly unverified invoice due dates | REVIEW_REQUIRED; RECEIVABLE_COVERAGE UNKNOWN |

Each case includes its synthetic customer/account/card mapping, shortfall HTTP
response, complete customer history/standing, facility picture, expected rule
trace and provenance labels. Capacity failure and missing-data review change
only their stated input from the eligible case. Distinct fixture identities
prevent one scenario's reservations/history from leaking into another.

Keep checked-in scenario templates deterministic, with relative dates. A demo
preparation command materializes a single run's UTC anchor, capture times,
90 completed balance dates, settled inflows, tenure and due dates. Historical
records preserve their intended age. The default anchor is now; tests may pin
it explicitly. Derive all three scenarios from the same anchor and policy hash.
This preserves freshness checks rather than disabling them for presentations.

The demo uses the actual snapshot importer, normal signed evaluation endpoint,
database and saved-decision GET. Only upstream financial records/shortfall HTTP
responses are synthetic. It requires no live Salesforce, Airwallex, AI calls,
or funding action. API inputs/results and the presenter guide identify DEMO.

Re-running a presentation prepares fresh versions and request keys under a
new run ID. Exact same-run retries reuse the original result. Never truncate
shared data or rewrite old decisions to reset a demo. Document a short command
sequence and expected output for each case, plus the retained artifact path.

### Worked policy examples

- Tier B, monthly inflows $100,000: $20,000 line. $15,000 principal already
  owed/reserved plus $7,000 requested fails CUSTOMER_CAPACITY.
- $7,000 requested for 30 days: fee $190, total $7,190. $7,000 free receivables
  fails coverage; $7,190 due within term passes that rule.
- A complete valid Tier B picture, zero prior exposure, $7,190 free coverage,
  and abundant facility yields ELIGIBLE for that request.
- Same facts in TIGHT with critical spend yields REVIEW_REQUIRED because
  principal is above $5,000. Missing invoice dates yields review, never
  approval.

At the end of implementation, use three focused test functions covering rules,
HTTP/snapshot/idempotency and an isolated database journey. Use two or three
representative scenarios per critical behavior, not exhaustive permutations.
Fixture shortfall HTTP is labeled as such; live Airwallex acceptance remains
separate from this engine. No UI or provider operations are needed for proof.

## AI extension boundary

Later AI evidence should be separate versioned, attributed observations linked
to a customer snapshot. Rules retain decision authority. This phase adds no
model calls, agent tools, generic signal registry or automatic note-based limit.
AI integration requires its own schema/rule mapping and focused design review.

The separate Go coordinator is the proposed application caller, including its
server-owned initial 30-day candidate term and persisted idempotency key.
It reads customer context through the explicit endpoint above; it receives no
direct SQL grants and cannot publish snapshots through an evaluation request.
Pending evidence and extraction claims belong to the coordinator. Its local
validation/export tooling can prepare candidate structured facts, but a human
verifies their meaning/source before publication through `import-snapshot`.
Schema validation alone establishes shape, not factual correctness. No new
snapshot/evidence write API is introduced by this integration boundary.

## See also

- [Completed implementation plan](/docs/plans/completed/20261114-underwriting-engine.md)
- [DTO/API/demo contract](/docs/api/underwriting-contract.md)
- [Current product policy](/docs/designs/bridge-capital-policy-spec.md)
- [Product requirements](/docs/requirements/README.md)
- [Shortfall design](/docs/designs/shortfall-detection.md)
- [Expected-deposit source gap](/docs/backlog/expected-deposit-date-source.md)
