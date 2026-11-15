# Funding and collection

Reviewed design, November 14, 2026. Task 12A adds a preparation bundle, a
loopback provider double, runtime readiness checks and packaging. The separate
Go service owns bound offers, guarded funding and deposit-driven collection.
This remains a staged local fixture implementation. It authorizes no live
provider operation or deployment.

[TOC]

## Scope and recommended arrangement

Create `services/funding-collection`: one HTTP API, one serial worker and the
existing PostgreSQL installation. Use standard library and the repository's
approved pgx version. No Node runtime dependency, broker, workflow framework,
event-sourcing framework or additional infrastructure.

Keep offers, acceptance, approval, reservations, bridges and servicing in this
service. These records protect one transition into a durable money obligation.
Splitting offers into another service adds a distributed authorization boundary
without helping this demo. Putting them in the coordinator would duplicate
authoritative financial state and trust agent progress as authorization.

The coordinator investigates both shortfalls and collection progress, chooses
allowed workflow requests, and explains saved evidence. Underwriting evaluates
hard rules. Funding and Collection alone authorizes its provider operations,
owns debt accounting and reconciles settlement. An agent cannot construct a
transfer, assert customer acceptance, impersonate an operator, change terms,
choose accounts, waive rules or mark money settled.

Minimal useful demo: synthetic shortfall and verified fixture facts, candidate
underwriting assessment, fixed offer, explicit fixture customer acceptance,
recorded automatic approval, atomic reservation, fixture transfer pending
then settled, two partial settled deposits, fee-first collection, closure and
coordinator-visible updates. Include an uncertain operation and recovery in
fixtures. Fixture actors and consent are visibly synthetic, never live consent.
The funded end-to-end fixture uses ABUNDANT capital. A separate TIGHT offer
proves missing/recorded operator approval and then returns
SCARCE_CAPITAL_RANKING_UNSUPPORTED without reservation or provider create.
It does not demonstrate approved scarce-regime disbursement.

## Evidence, authority and discrepancies

Inspected main checkout: `develop`, HEAD `6a9a83a`. The untracked underwriting
draft is owned by the lead and remains an evolving proposal. Source authority
is current requirements, policy design and frontend policy configuration.
Existing Node paths provide implementation evidence, not current pricing.

| Source | Finding and treatment |
| --- | --- |
| `docs/requirements/README.md` | Acceptance precedes funds; full-or-zero, one open bridge, USD, fee-first deposits. |
| `docs/designs/bridge-capital-policy-spec.md` | Fixed selected-term fee; binding approval; late ladder and overdue rate. |
| `src/features/bridge-capital/policy/pricing.ts` | Due date starts at request/offer date; full fee remains on early repayment. |
| `src/features/bridge-capital/policy/collection.ts` | Overdue fee before contracted fee, then principal. |
| `src/features/bridge-capital/policy/delinquency.ts` | Recalculates overdue fee using remaining principal; partial repayment can reduce accrued debt. Needs policy decision below. |
| `policy.config.ts` in that directory | Default starts at 61 DPD despite ambiguous “60+” spec prose. Use 61. |
| `server/src/collections/service.ts` and `fee.ts` | Legacy timed-term collection waits until due; fee uses 5 bps/day, seven-day minimum. Do not port those behaviors. |
| `server/src/ledger/reservations.ts` | Facility locking and durable intent are useful patterns; old policy inputs are incompatible. |
| `server/src/ledger/projector.ts` | Failure retains reservation until return confirmed; preserve the conservative principle. |
| `server/src/db/migrations/001_bridge_ledger.sql` | Useful operation/inbox/ledger constraints; do not rewrite or share mutable tables. |
| `server/src/airwallex/client.ts` | CA transfer and Charges candidates exist; paths and permissive parsing do not prove live contracts. |
| Shortfall design and local runbook | Fixture path exists; runtime live reference extraction remains unsupported. |
| Underwriting draft | Assessments always require recheck and never authorize funding. |

Additional proposed clarifications, requiring review: exact-cent half-up
rounding, ten-minute offer expiry and UTC date boundaries. Ramil confirmed
that customer line exposure includes funded principal, all unpaid fees
(including earned overdue fees), and active unfunded principal/fee
reservations. Converted reservations are not counted again. A new bridge
adds principal plus its full fixed fee. Facility cash capacity counts principal
commitments; fee receivables are not spendable facility capital.

## Ownership and integration prerequisites

Funding and Collection is the only writer of its facility commitments,
principal/fee balances, reservations, repayment allocations and receivable
encumbrances. Underwriting customer/facility snapshots are read models, not
reservable capital. Provider wallet balances prove spendable provider funds,
not facility size. Facility fee income does not automatically enlarge capital.

Use the existing active roster and card mappings through a narrow read-only
Go adapter, as shortfall detection does. Do not create another mapping registry.
Retain mapping revision/fingerprint and server-owned account/card references
on offers and operations. Reject inactive, missing, duplicate, conflicting or
changed mappings. No account IDs or monetary inputs arrive from the browser or
coordinator. USD only; each installation and row belongs to DEMO or SANDBOX.
Never mix modes, credentials, provider endpoints, fixtures or accounting books.

Proposed integration boundaries, not implemented by these design documents:

- Export a pure Go policy evaluator from the underwriting module as `policy`
  with versioned normalized input types. Its HTTP service and this service use
  that same evaluator. It accepts funding-owned locked ledger/receivable facts;
  it performs no HTTP, SQL, time lookup or provider commands. Do not copy the
  rule implementation or import an `internal` package illegally.
  Pin evaluator policy ID/hash to the candidate assessment and bound offer.
  A mismatch blocks funding and requires reevaluation, with refreshed terms,
  acceptance and approval where their binding changes. Never silently apply
  a newer policy to an older authorized offer.
- Use proposed `GET /api/underwriting/customers/{customer_id}/context` for
  immutable normalized non-ledger facts, current snapshot version, capture
  time, completeness, source references and policy hash. Require the draft's
  24-hour hard-fact freshness. The funding service obtains these facts outside
  its transactions; stale/unknown facts block. This is an explicit proposed
  API aligned with the lead's evolving draft, not an endpoint available today.
- Maintain receivables in this service through a restricted import command,
  with unique source invoice identity, verified unpaid amount/date, source
  revision and completeness. Funding owns encumbrances; the upstream source
  owns invoice truth. Imports lock the same invoice rows as reservations and
  cannot reduce available backing silently. A changed/disputed backing invoice
  places affected bridges in review and blocks new use.
- Expose funding-owned exposure, history, receivables and facility read models
  for future underwriting snapshot publication. Until an adapter exists,
  fixture/import snapshots must identify their ledger version and remain
  assessments. No Node profile adapter is assumed complete.

The minimal demo can use fixture implementations of these boundaries. Real
funding stays disabled until they are implemented and contract-reviewed.
Shortfall canonical payment/balance refresh is also a missing live contract.
Do not refresh a historic detection by editing its saved evidence.

## Offers, acceptance and approval

Create an immutable offer version from a saved evaluation with complete hard
rules PASS. `REVIEW_REQUIRED` solely for `HUMAN_APPROVAL_REQUIRED` can produce
an offer requiring operator approval. Any hard FAIL or UNKNOWN blocks it.
Never rename an evaluation into an approval. Save original evaluation and all
rule results, customer and payment identity, source mode and policy hash.

Canonical payment identity is mode +connected account +parent transaction ID;
save the authorization event separately. Distinct decline attempts for the
same payment must not create separate bridge funding.

All bindings include offer ID/immutable revision, terms hash/version, customer,
canonical payment, account mapping revision, USD principal, contracted fee,
term days,
due date, repayment waterfall, collection-authority reference and disclosures
version. Save exact terms presented, actor identity, authenticated actor type,
decision reason, timestamp, source evidence and originating case reference.
Customer acceptance records contain the presented terms hash and explicit
acceptance reference. Approval records contain operator identity and reason,
or a distinct deterministic automatic-approval record with fresh policy result.
The agent has neither acceptance nor human-approval credentials.

An offer expires ten minutes after creation; no capital is promised while
waiting. Pin `offered_on` to server UTC date and
`due_on = offered_on +term_days` to match current frontend semantics. Do not
move the due date at settlement. Block a first send after the due date. If an
already-dispatched transfer settles late, book its actual movement and debt,
retain the original due date and hold for reconciliation/review. Never conceal
settled funds because their arrival violated an expected timing bound.
Reservation and first send additionally require server UTC date =offered_on.
Crossing midnight supersedes a proven-unsent offer, cancels its unsent intent
and releases reservations. Issue new terms and actor bindings; do not shift
the evaluator's today-plus-term invoice window beyond the accepted maturity.
Already-dispatched movement always reconciles under its original terms.

Change any bound term, customer/payment, mapping or disclosure version and
supersede the offer. Prior acceptance and approval remain audit records but
cannot authorize its replacement. Recheck policy at funding time. If a regime
change newly requires a person, request approval on unchanged terms; a hard
failure cannot be approved away. Same terms can use a new fresh check, recorded
alongside the original assessment. An approval never bypasses acceptance.

Customer authentication and commercial-financing/collection consent are open
live gates. For the fixture demo only, a local Go demo-actor CLI submits
explicit synthetic customer/operator actions. It cannot mint live consent.
No new public customer identity service is part of this installation.

## Terms, debt and delinquency

Store money as checked integer USD cents. For integer term 7–90 days:
`fee_cents = 5000 + round_half_up(principal_cents *200 *term_days /300000)`.
Round the percentage component once, use checked intermediates or standard
library big integers. $7,000 for 30 days owes $190 fee and $7,190 total;
$10,000 for 60 days owes $450 fee. Full chosen fee survives early repayment.

No debt is booked on offer/acceptance/reservation. On confirmed funding
settlement, atomically book full principal and contracted fee, funded timestamp,
due date, ledger postings and converted reservation. Keep principal owed,
contract fee owed and overdue fee owed separately, plus immutable original
terms, allocations, paid totals, reversal references and closure history.

DPD is `max(0, current_UTC_date -due_on)` for nonzero settled debt. Worker
checks once per minute and catches up on restart. Watch is 1–7, Delinquent
8–30, Serious 31–60, Default 61+. Due day is DPD 0. Record first default date
once, effective on `due_on +61 days` even after worker downtime; repayment
does not erase default history. Underwriting's proposed
365-day exclusion differs from spec's calendar 12 months; publish the actual
default date and leave eligibility to the reviewed policy evaluator.

Ramil approved daily accrual on actual unpaid principal in this session.
Pin the demo timing convention: assess at the start of each overdue UTC day,
beginning at 00:00 on due_on +1 day. The day's basis is unpaid principal after
all confirmed settlements effective strictly before that midnight. A payment
at midnight belongs to the new day. The full opening-day charge applies even
if payoff is later that day; no second service fee or compounding.
This is the proposed implementation convention refining the approved daily
accrual method; Ramil has not separately approved its full closing-day detail.
Accepted disclosures must explicitly state opening-principal daily assessment
and the full charge on payoff day. Bind
`late_fee_convention: OPENING_PRINCIPAL_UTC_DAY` into the immutable terms.

Persist one immutable `accrual_days` row per bridge/UTC date with opening
principal, contributing settlement references, cumulative rational numerator
and assessed delta. Numerator adds `opening_principal_cents *200` per day;
denominator is 300000. Cumulative assessed cents are half-up rounded once,
and today's delta is cumulative assessed minus prior assessed. Track
`last_accrued_on`, cumulative assessed cents and cumulative paid cents
separately; payment never reduces earned historical accrual. Unique bridge/day
effect key prevents double assessment. Use checked big-integer intermediates.

Before collection planning, first send and closure,
catch up due dates in order. Reconstruct each opening basis from immutable
effective-time principal settlement/reversal lines, never today's residual
principal. Worker downtime does not change the answer. Limit each pass to
90 dates; if more remain, mark ACCRUAL_CATCHUP_PENDING and block new debit,
closure and funding until subsequent passes finish. Repaid bridges stop
accruing; zero principal creates no future fee even if old fees remain owed.
Late-arriving/backdated settlements changing a frozen basis require
ACCRUAL_CORRECTION_REQUIRED and compensating review, never mutation of old
fees. No automatic correction or closure while that hold remains.
Include funding settlement in that history: principal first disbursed after
the day's midnight contributes zero to that day's opening basis. A later
receipt proving it was outstanding earlier holds the frozen basis for review.
Reads expose `accrual_current`, `accrual_current_through` and recorded debt
as-of time. If catch-up is pending, return recorded balances with explicit
staleness and only WAIT_FOR_ACCRUAL as next action. GET performs no write;
the normal worker catches up. Never present those balances as a final payoff.

Example: due November 30, principal $7,000 and contracted fee already paid.
On December 1 at 00:00 assess $4.67. At noon settle $4.67 fee, then $3,500
principal. December 2 opening basis is $3,500: cumulative fee is $7.00, so
assess $2.33. Settle $2.33 fee and $3,500 principal that day and close, without
waiting until December 3 or charging another day. Catch-up from the same
effective settlement history produces those identical amounts.
This approves the demo accounting method. Live collection still requires the
reviewed terms/disclosure and debit authority covering those fees. Missing
authority blocks fee collection and final closure, preserving the debt.

Expose reminder/escalation work items and required card-control actions.
Actual email/SMS, card restrictions/freezes, tier mutation, outside collections
referral, writeoffs and legal enforcement are deferred. Show them as required
or pending, never completed. Any live demo claiming these protections must
first implement/verify them or restrict its scope to cases that do not need
them. New funding blocks on overdue debt and unresolved servicing holds.

## Atomic authorization and reservation

Facility configuration is versioned and explicitly imported. First delivery
supports only explicitly known-empty DEMO debt/reservation/default-history
books, verified against the fixture context, with a dedicated facility. It
does not import nonempty legacy books. That importer/reconciliation is a later
cutover deliverable; unknown legacy exposure blocks SANDBOX. Do not run Node
and Go writers against the same facility/customers: first installation uses a
separate fixture facility; live cutover must establish one writer and import
opening exposure before dispatch is enabled. No migration or cutover occurs
in this planning task.

Facility available is `max(0, total -ceil(total *reserve_bps /10000)
-principal_owed -active_reserved_principal -other_verified_commitments)`.
Never infer missing commitments as zero. Use current pre-request regime.
Reserve principal at facility level, principal +full fee against the customer
line, and principal +full fee against eligible invoice coverage. For a $20,000
customer line, $12,810 existing debt/reservations leaves exactly $7,190 for a
$7,000/30-day bridge; $12,811 leaves insufficient room. This illustrates line
arithmetic only; any existing open bridge separately blocks a second one.
Deterministically allocate invoices by due date,
then source ID. One open bridge/reservation per customer and one funded or
unresolved funding per canonical payment, including after repayment.

Funding command and dispatch use this sequence:

1. Replay an identical committed command before dependency reads. Fetch fresh
   mapped payment/balance facts and latest compatible non-ledger policy facts,
   outside a transaction. Require payment/balance observation within 30 seconds
   and at most five seconds in the future at reservation/dispatch. Recompute
   current shortfall. A changed principal requires new terms, full-or-zero.
2. Begin transaction; lock facility row, customer row, offer/bridge and invoice
   rows in sorted ID order. All ledger/import/settlement writers follow this
   order. Lock/verify current mapping rows through the restricted function
   specified below, not row-locking SELECT using runtime's read-only grants.
   Check versions, active holds, fact freshness and fixed bindings again.
3. Evaluate shared pure hard rules using fetched verified non-ledger facts and
   current locked facility/exposure/invoice facts. Missing inputs stop funding.
   Require acceptance, current collection authority and applicable approval.
   Persist the new authorization evidence, not merely an ELIGIBLE label.
4. Atomically reserve all capacity, mark offer consumed, create bridge and one
   immutable provider operation identity/payload hash, append balanced ledger
   audit entries and a servicing change. Commit before provider I/O. If any
   check/write fails, no reservation or operation exists.
5. Worker claims that queued operation using a fenced lease. Repeat fresh
   payment/balance, mapping, consent and policy guards before first dispatch;
   under the same locks validate the reservation against current policy,
   using the exact self-exclusion projection below. Mark
   DISPATCHING and commit; call provider outside the transaction. No policy
   change after this point can pretend the request was never sent.

Reserve for at most two minutes before first dispatch, bounded by offer
expiry. Only an operation proven never sent can expire/release automatically.
If dispatch has started or certainty is absent, retain commitments indefinitely
pending reconciliation. A changed guard before send cancels the unsent intent
and releases all capacity atomically. No silent smaller amount is dispatched.
That cancellation also makes its zero-debt bridge VOID_UNFUNDED, releases
all memo/invoice/customer commitments, terminates the consumed offer and emits
its incremented bridge version atomically. Apply this only to proven-unsent
work or authoritative no-movement/returned funding with no recipient funding
ever settled. Retain operation/evidence history. Never mark it REPAID or add
repayment history. A fresh offer with fresh acceptance/approval and a new
operation identity may retry that never-funded payment.
External customer spending can race a balance read; the 30-second guard is a
freshness bound, not an atomic provider lock. Strict live full-shortfall funding
requires a verified provider hold/atomic mechanism or an explicitly reviewed
bounded-staleness contract. Until then live funding remains gated.

For dispatch recheck of this NEVER_SENT reservation, build an in-memory policy
projection excluding only its own customer principal/fee commitment, facility
reserved principal, invoice encumbrances, RESERVED bridge placeholder and
payment duplicate. Then evaluate the same full principal +fee candidate once.
Keep all other commitments and all settled debt, even for this payment. This
also restores the correct pre-request regime and free-invoice coverage.
Do not mutate stored reservations to run the projection. Map verified other
facility commitments into evaluator `reserved_principal_cents` in addition to
its active funding reservations, once. Unknown or negative reconciliation
fails closed. Persist input ledger version and actual evaluated regime.

First slice dispatch supports ABUNDANT and NORMAL only. TIGHT and CRITICAL
require ranked allocation, but current source DTOs lack verified payer-quality
inputs and no reservation-ranking contract exists. Return deterministic
422/SCARCE_CAPITAL_RANKING_UNSUPPORTED before reservation (or cancel a proven
unsent reservation if the regime changes). Human approval cannot bypass this
gate. Assessments/offers may still describe the human requirement; they do
not promise execution. Scarce ranking is explicitly deferred, not silently
first-come funding or an agent-invented score.

### Restricted mapping lock boundary

The privileged migration creates `servicing.lock_verified_mapping` with typed
customer/account/card arguments and fixed SQL only. It locks the exact roster
row, then exact card binding, both FOR SHARE, and validates unique active
customer/account/card linkage. Return the canonical tuple and its fingerprint
inputs, not credentials. The caller computes the pinned Go SHA-256 encoding.
Locks persist to caller transaction commit. All funding/collection first-send
transactions call it after facility/customer/bridge/invoice locks, before
operation/feed locks. Missing/duplicate/mismatched rows produce controlled
mapping failure, never a guessed match.

Use SECURITY DEFINER, `SET search_path = pg_catalog`, fully qualified tables,
no dynamic SQL, owner =privileged migration owner that runtime cannot assume.
Revoke PUBLIC execution; grant only EXECUTE to servicing runtime. Keep its
direct foreign-table privileges SELECT-only. The function owner must have
SELECT plus required row-lock privileges on the two mapping tables. PostgreSQL
requires UPDATE privilege for locking SELECT; do not grant runtime that
privilege or use an advisory lock without all writers participating.
See [PostgreSQL SELECT privileges][pg-select]. No new mapping registry or
foreign-table writes are introduced. Source account A remains facility-bound;
its active provider relationship/authorization is independently verified.
`internal/postgres/mapping.go` exposes a transaction-bound LockVerifiedMapping
adapter called with server-resolved expected customer/account/card references.
Canonical fingerprint struct field order is customer_id, roster_account_id,
roster_active, card_id, card_account_id, card_active, encoded with Go
json.Marshal and SHA-256 lowercase hex. Validate the locked tuple and compare
that computed fingerprint with bound terms/operation evidence; merely passing
an account argument to SQL does not make it trusted. Source A binding is
immutable for a facility ID; changing A requires a new facility/offer binding.
These locks end at dispatch-intent commit. External mapping/permission changes
during the subsequent HTTP request remain a live race: enabling that path
requires a reviewed mapping-change freeze or provider enforcement contract.
Do not claim end-to-end atomicity across PostgreSQL and provider I/O.

[pg-select]: https://www.postgresql.org/docs/current/sql-select.html

## Airwallex integration and actual funds flow

Documentation checked November 14, 2026 through the official Airwallex docs
connector. These are documented API capabilities, not verified permissions or
successful movements in our installation. Implement the concrete Go HTTP
adapter below; keep dispatch disabled until account-specific gates clear.

### Account layout and direction

Ramil selected direct connected-account transfers for this demo. Bind a
designated funding connected account A to the facility and the borrower B to
its trusted customer mapping. This changes the existing requirements' platform
funding topology; those source files remain unchanged pending lead integration.
Recommended fee beneficiary is A as well, keeping servicing on the same pair.

| Business movement | Source | Destination | Airwallex mechanism |
| --- | --- | --- | --- |
| Bridge funding | Funding connected account A | Borrower connected account B | CA transfer, scope A, destination B |
| Fee collection | Borrower connected account B | Funding connected account A | CA transfer, scope B, destination A |
| Principal collection | Borrower connected account B | Funding connected account A | Separate CA transfer, scope B, destination A |

For example, a $7,000 approved bridge creates a $7,000 transfer to the
borrower. Later eligible deposits support separate $190 fee and $7,000
principal repayment transfers, potentially across several partial payments.
This uses
internal account balances on Airwallex, with no external bank beneficiary
required for these movements. Verify both accounts' active relationship,
KYC/ACTIVE state and direction-specific authority. See the official
[transfer reference][ca-api].

Airwallex also documents direct transfers from one connected account to
another on the same platform. Use the same transfer endpoint, set
`x-on-behalf-of` to the authorized source connected account, and set body
`destination` to the recipient connected account. The platform bearer token
authenticates the call; the header selects the source account context. It does
not itself establish customer permission. See
[connected-account transfer guidance][ca-guide].

Pin A as the source for disbursement and destination for repayment in immutable
facility configuration and each operation. Borrower consent must authorize the
reverse B-to-A sweep; A-to-B funding authority does not imply that permission.
Never fund from another unrelated customer's balance. A's fee receipts remain
separate fee income in our ledger and do not automatically expand its facility.

Charges debit a connected account and credit the platform. They are documented
for comparison in the [Charges reference][charge-api], but are outside the
selected A-to-B/B-to-A adapter. A later fee sweep to the platform requires an
explicit additional design; never relabel a platform receipt as cash in A.

[ca-api]: https://www.airwallex.com/docs/api/scale/connected_account_transfers/create
[charge-api]: https://www.airwallex.com/docs/api/scale/charges/create
[ca-guide]: https://www.airwallex.com/docs/connected-accounts/move-funds/connected-account-(ca)-transfers

### Authentication, payload and source ownership

The Go adapter authenticates server-side with
`POST /api/v1/authentication/login`, using `x-client-id` and `x-api-key`.
Cache the returned bearer token in memory until its `expires_at`, refreshing
before expiry. Configure platform account context explicitly; a multi-account
scoped key may require `x-login-as` at login. Never confuse organization ID,
platform account ID and connected account ID. Documentation:
[API access token][auth-api] and [scoped API keys][key-guide].

Use `https://api.sandbox.airwallex.com` for the planned sandbox adapter;
DEMO uses injected fixture HTTP with synthetic credentials only. Production
execution remains outside this plan. No arbitrary caller-supplied base URL or
provider account. Disable automatic redirect following for credentialed calls.

All selected movements use the platform bearer token and CA transfer body
fields `request_id`, `destination`, `amount`, `currency`, `reason`, `reference`.
Funding sets `x-on-behalf-of: A`, `destination: B`; collection sets
`x-on-behalf-of: B`, `destination: A`. There is no caller-controlled `source`
field. Persist source scope/destination with the payload, and use that same
scope for lookup. Refuse A=B, remapped accounts and an absent source header.

Convert integer cents to an exact two-decimal string at the adapter boundary:
700000 cents becomes `"7000.00"`, never a floating-point calculation.
`request_id` is the saved operation identity (maximum 50 characters), and
`reference` is a bounded bridge/collection reference (maximum 140).
Use a reviewed server-side reason mapping: principal collection candidate
`loan_credit_repayment`, fee candidate `professional_business_services`.
The disbursement reason must describe lending truthfully and be confirmed
with Airwallex before live use; an allowed enum alone does not approve a use.
No browser/model controls headers, purpose, source, destination or amount.

Required scoped permissions include Embedded Finance Transfers Read/Write
for the selected account relationships, plus pinned balance/deposit reads.
Support for an API or header does not prove the installed key, platform
relationship or customer mandate permits the operation.

Before funding dispatch, read `GET /api/v1/balances/current` in the selected
funding source context as well as the borrower's context. Require source USD
available funds to cover the transfer and known provider fees; keep unresolved
local source debits reserved to avoid simultaneous use. This is an additional
provider-liquidity check, not a replacement for facility ledger authorization.
Borrower reads use `x-on-behalf-of: B`; funding-source reads use
`x-on-behalf-of: A`. Pin actual balance fields/fee treatment before enabling
dispatch, using the existing shortfall balance contract as reusable evidence.

[auth-api]: https://www.airwallex.com/docs/api/authentication/api_access_token/login
[key-guide]: https://www.airwallex.com/docs/developer-tools/api/manage-api-keys

### Adapter endpoints, receipts and reconciliation

| Adapter operation | Provider endpoint |
| --- | --- |
| Create funding transfer | POST `/api/v1/connected_account_transfers/create` |
| Retrieve known transfer | GET `/api/v1/connected_account_transfers/{id}` |
| Find transfer by saved request | GET `/api/v1/connected_account_transfers?request_id=...` |
| Create fee/principal repayment | POST `/api/v1/connected_account_transfers/create`, scope B |
| Retrieve repayment | GET `/api/v1/connected_account_transfers/{id}`, scope B |
| Find repayment by saved request | GET `/api/v1/connected_account_transfers?request_id=...`, scope B |

List lookups retain the operation's original account scope, traverse bounded
pagination and require exactly one matching identity/amount/currency/account.
Multiple or contradictory results hold the operation. Never treat the first
list item as a match without validation. The official
[transfer list][ca-list] documents request-ID filters; an empty result still
does not prove a timed-out create was rejected. The same request identity under
another account scope is not a valid reconciliation match.

Store provider ID, raw status, request ID, amount/currency, account linkage,
provider fee and timestamps as a receipt/observation. NEW/PENDING/SUSPENDED
remain unresolved. SETTLED permits the guarded accounting transition below.
Airwallex explains that a successful create can debit the source before
transaction monitoring releases credit to the recipient. Preserve that
in-transit movement; never release reserved capacity merely because the
recipient has not yet received it. See
[funds movement and flight time][funds-guide].

Use the documented transfer event family
`connected_account_transfer.new|pending|settled|suspended|failed` as provider
contract evidence. Pin actual signed transfer/deposit event envelopes and
account-scoping fields before implementing their live parsers. Reconcile by
GET even if a webhook is missed; a webhook is a wakeup, not a balance update
by itself. Request-ID fields and lookup filters are documented, but the
searched references do not establish replay retention/duplicate-create
guarantees. Keep automatic uncertain-create resends disabled until verified.

[ca-list]: https://www.airwallex.com/docs/api/scale/connected_account_transfers/list
[funds-guide]: https://www.airwallex.com/docs/connected-accounts/move-funds

## Provider operation and settlement semantics

Operation kinds are FUND, COLLECT_FEE and COLLECT_PRINCIPAL. Save immutable
operation ID, mode, accounts, amount, USD, terms hash, request identity,
payload hash, bridge and trigger references before any send. Reuse
`bridge-<uuid>`, `fee-<uuid>` and `principal-<uuid>` for the same operation.
Do not infer a provider idempotency guarantee from that naming convention.

| Operation state | Meaning and allowed next step |
| --- | --- |
| QUEUED | Durable, never dispatched; may cancel under locks. |
| DISPATCHING | Send might occur; crash becomes UNKNOWN, not QUEUED. |
| PENDING | Provider object exists; await authoritative settlement. |
| UNKNOWN | Timeout, malformed response, lost receipt or uncertain failure; lookup only. |
| HELD | Permission, identity mismatch, conflict or retry budget exhausted; inspect/reconcile. |
| SETTLED | Authoritative matching final result applied once. |
| FAILED_CONFIRMED | Definitive no movement, or separately confirmed return; release applicable holds. |
| CANCELLED_UNSENT | Proven never sent, guard failed or expired. |

A create response/HTTP 200 is a receipt, not proof of settlement. A verified
settled result must match mode, source/destination accounts, request/object
identity, amount, currency, effective settlement time and applicable fees.
Missing fields require authoritative retrieval. Unknown status remains held.
Webhook events trigger retrieval; never let an older pending event regress
settled state. Duplicate event IDs and different events for one settlement
cannot apply debt twice. Conflicting terminal results enter reconciliation.

After uncertainty, lookup stored identity/object first. Empty lookup, 404 or
eventual consistency alone cannot prove no movement. Resend the SAME payload
and identity only when the verified provider contract guarantees safe replay
within its deduplication window. If lookup/idempotency is unverified, do not
resend. Operator reconciliation requests only repeat verified retrieval; they
cannot mark success or replace identities. There is no exactly-once external
execution claim. Local settlement posting is deduplicated transactionally.

FUND settlement converts reserved principal to outstanding principal in one
transaction, with no temporary double count or release gap. Book contracted
fee once. Pending/failure-without-return keeps facility/customer/invoice holds.
Only proven no movement or confirmed return releases funding reservations.
Settlement reversal after funding needs compensating ledger entries and a
hold; do not erase customer debt based on an unverified failure notification.

Card retry is a later coordinator/issuing boundary, available only after
confirmed funding settlement. This service neither retries cards nor assumes
the payment subsequently succeeded.

## Deposit-driven collection and accounting

Signed deposit events are durable wakeups. Retrieve canonical settled credit
and available USD balance in its trusted connected-account scope. Verify
customer mapping, deposit identity, amount/net fees, credit type, source and
effective settlement time. Pending deposits, bridge funding, internal transfers,
refunds/reversals and unclassified credits do not trigger automatic collection.
The triggering credit must settle after bridge funding. A pre-funding deposit
reduces current need instead of financing and immediately sweeping itself.

Collection authority must permit CA transfers from borrower B to funding
connected account A, with enabled product,
permissions, customer mandate/contract reference, allowed purpose and limits.
Disbursement authority on A does not establish sweep authority on B. Missing or
revoked authority yields `COLLECTION_AUTHORITY_BLOCKED`. Retain the debt and
coordinator work item. Do not invent customer consent from an offer acceptance
unless the reviewed accepted disclosure explicitly contains that authority.

V1 zero operating buffer follows current requirements. Lock customer, bridge
and deposit budget under the common facility-first order. Serialize one
unresolved collection operation per customer/account. On every dispatch cap:

`min(remaining eligible deposit net amount, freshly available USD,
remaining collectible debt, authority remaining limit)`.

Subtract settled collections AND reserved/unknown debit amounts from each
deposit budget and debt allocation. Customer serialization prevents two
deposits using the same observed balance concurrently. External spending can
still race; provider insufficient funds is a failed/held collection, never a
negative ledger balance or automatic overdraft. Provider fees must be known
and included in authority/budget limits or live collection is blocked.
For known source-debited provider fee `f`, cap transfer principal/fee repayment
at `max(0,min(deposit_remaining-f, available_USD-f, collectible_debt,
authority_remaining-f))`. Reserve transfer amount +f against deposit/authority
and provider liquidity, but transfer amount alone against debt. Book f as
provider expense, never as customer financing debt. Fixture provider fee is
explicitly zero. Unknown or destination-deducted fee semantics block the first
slice; borrower must receive full funded principal, not a fee-netted shortfall.

Allocate authorized overdue fees first, then contracted fee,
then principal. Fees and principal are separate provider operations. Wait for
fee SETTLED before principal, then refresh balance and recalculate remaining
deposit budget. Partial fee repayment does not advance to principal. No debt
or facility availability changes merely because a transfer was requested.

Each canonical deposit has one durable budget for its customer/mode, even if
delivered under several event IDs. Attempts record immutable amount/authority
observations and allocations. Definitive no-debit failure releases only its
reserved amount; uncertainty retains it. A later verified deposit or bounded
review can reconsider remaining old budget, always with a fresh available
balance and unexpired authority. A new attempt after definitive failure has
a distinct recorded operation; retrying an uncertain attempt keeps its identity.
No uncontrolled loop debits the same unchanged insufficient balance.

SETTLED collection posts matched provider cash receipt, reduces the assigned
fee/principal and releases debit holds once. Principal repayment replenishes
facility available capital; fees go to fee income, not new facility capital.
Invoice backing covers original principal plus contracted fee. Every settled
reduction of either covered bucket releases exactly that amount of invoice
encumbrance in original due-date/source-ID allocation order, in the same
transaction. Overdue fees are explicitly unbacked servicing debt: they count
against customer line exposure but never consume invented invoice capacity.
Their repayment releases no invoice allocation. At REPAID assert zero active
invoice encumbrance, zero debt and zero pending debit reservations.

A reversal restores debt and attempts to restore its original released
backing only to the extent still free. Record remaining `backing_deficit_cents`
and hold new funding/automatic closure; never overallocate an invoice now used
by another obligation. Invoice impairment likewise records a backing deficit.
If a repaid bridge reverses after another opens, represent old debt as
REOPENED_HELD outside the normal customer funding slot, include it in exposure
and block every new reservation. Exceptional debt cannot be hidden to satisfy
the ordinary one-open-bridge rule. Payment
history preserves provider deposit/transfer IDs, operation ID, event and lookup
references, settlement/recorded times, gross/net/provider fees and ledger IDs.

Close REPAID only when all debt buckets are zero, no unresolved operation,
reversal or refund exists, and overdue-fee accounting is finalized. Leave
excess deposit funds with the customer. Never clamp an oversized provider debit
silently: record actual movement, apply only owed debt and put excess in a
customer refund-payable liability, hold servicing and queue operator review.
Refund execution and repayment-transfer/deposit reversal automation are live
gates. Record confirmed reversals with compensating postings, restore affected
debt/exposure, reopen servicing and hold new funding. If capacity is then below
the floor, block new funding; never hide the deficit or edit old settlements.

### Collection first-send transaction

Planning creates one immutable operation with `late_fee_cents` and
`contract_fee_cents` summing to COLLECT_FEE amount, or a principal allocation
equal to COLLECT_PRINCIPAL amount. Record deposit budget reservation and exact
mandate version/limit consumption. Never settle a fee against a newly computed
split instead of the saved one.

Before first send, fetch fresh balance/provider facts outside SQL, then lock
facility, customer, bridge, allocated invoices, deposit, mapping through the
restricted function, operation and feed in that order. Catch accrual up to the
current day before debt/fee checks. Check current mapping/mandate/version,
authority limits/expiry, unresolved holds, deposit remainder, available USD,
owed buckets and stored waterfall/split. COLLECT_PRINCIPAL requires all fee
buckets zero and no unresolved fee operation. Accrual at midnight can therefore
invalidate an unsent principal intent.

For that first-send comparison, project out ONLY this QUEUED/proven-never-sent
operation's deposit debit reservation (amount plus known provider fee), mandate
reservation, assigned debt-bucket reservation and local liquidity hold. Exclude
its operation ID from the one-unresolved-debit check. Compare the unchanged
immutable amount and split once against the projected capacity; do not mutate
counters to check. Keep all other operation holds, settled consumption and
DISPATCHING/PENDING/UNKNOWN reservations. Never increase the actual observed
provider balance: only our own not-yet-sent local hold is excluded. Thus a
$100 fee intent on a $100 deposit can send once rather than rejecting itself.

If the immutable amount/split/binding no longer fits, CANCELLED_UNSENT and
release its exact debit/deposit/mandate reservations, atomically emitting the
new parent version. Do not resize it. A fresh planner may create a distinct
operation once per changed evidence tuple (deposit, balance observation,
mandate version, bridge debt version), at most three replans per deposit/day;
then hold for review. A new verified deposit permits a new bounded plan.
DISPATCHING/PENDING/UNKNOWN instead keeps its identity and reservations for
lookup only. Mark DISPATCHING with token/unexpired lease CAS, commit, then I/O.

Revoked/missing authority, mapping conflict, correction/backing/refund/reversal
holds and uncertain debit block new collection. A funding-only hold such as
scarce ranking or insufficient facility capital does not stop authorized
repayment. All holds preserve actual settlement reconciliation, including a
movement confirmed after revocation. Clearing a hold never creates a provider
success or resets an unknown operation identity.

## Durable schema and worker

Use separate `servicing` schema and restricted runtime role. New migration
`services/funding-collection/internal/postgres/migrations/001_initial.sql`;
never modify Node or onboarding migrations. Explicit migration command uses
a privileged connection; API/worker never run DDL.

- `facility` and `customers`: mode-scoped configuration, aggregate principal,
  reservations, default/history markers, active hold and version counters.
- `receivables` and `receivable_allocations`: source invoice/version,
  completeness, due/unpaid amounts and reserved/assigned obligation amounts.
- `offers` and `actor_decisions`: immutable bound terms, evaluation references,
  expiry, acceptance/approval/authority evidence and supersession links.
- `authorities` and `actors`: versioned direction-specific mandate facts and
  actor-to-role/customer bindings. Synthetic imports are DEMO-only.
- `bridges`: immutable original terms plus transactional debt counters, due
  date, lifecycle, delinquency, funded/closed timestamps, accrual cursor,
  cumulative assessed/paid late fees, backing deficit and version.
- `accrual_days`: immutable bridge/date opening principal and settlement basis,
  rational numerator, assessed delta and unique bridge/date ledger effect.
- `reservations`: one per bridge, facility principal commitment,
  customer principal/fee commitment, invoice coverage and
  NEVER_SENT/DISPATCHED/CONVERTED/RELEASED state.
- `operations`: immutable request facts plus worker state, lease token/expiry,
  retry count, next check, provider object and sanitized error code. This is
  the transactional work queue, avoiding a second generic outbox framework.
- `deposits` and `collection_attempts`: canonical deposit budget, verified net
  credit facts, debit reservations and operation/allocation references.
- `webhook_inbox` and `provider_observations`: original verified body protected
  in DB, event-ID uniqueness and immutable normalized retrieval evidence.
- `ledger_transactions` and `ledger_lines`: balanced USD postings per
  transaction, unique settlement/operation effect key, debit/credit accounts,
  effective/recorded times, bridge/source IDs and provenance. Balances update
  in the same transaction. No replay engine is required for routine reads.
- `commands` and `changes`: caller-scoped idempotency receipts and committed
  coordinator progress records, including blocked/failed actions.

Ledger postings distinguish facility cash/control, bridge principal receivable,
contract/late fee receivable and income, collected cash/control, commitment
memo accounts and customer refund payable. Funding: debit principal receivable,
credit facility cash/control; fee: debit fee receivable, credit fee income.
Collection: debit cash/control and credit relevant receivable. Reservations
use balanced memo commitments, never settled debt. Provider cash reconciliation
keeps actual provider fees distinct from customer financing fees. No claim of
full statutory accounting or general-purpose accounting platform.

Database constraints enforce positive checked amounts, USD, valid states,
one open obligation/customer, canonical payment uniqueness, immutable terms,
unique mode/request identity and unique settlement/allocation effect keys.
Do not rely on an application-only “already processed” query. Store explicit
state version on every externally visible aggregate.

Pin bridge lifecycle values to RESERVED, FUNDING_PENDING, ACTIVE, COLLECTING,
REPAID, VOID_UNFUNDED and REOPENED_HELD; pre-bridge OFFERED/actor-wait states
belong to offers. `ever_funded` starts false and becomes irreversibly true
only on confirmed recipient funding settlement. VOID_UNFUNDED requires false
ever_funded, zero debt and no active reservation. The ordinary customer slot
unique index is `(mode,customer_id)` for RESERVED/FUNDING_PENDING/ACTIVE/
COLLECTING only. REOPENED_HELD is an exceptional debt/hold outside that slot,
counts all debt in exposure and blocks all further funding.

Canonical payment blocking uniqueness is
`(mode,canonical_account_id,transaction_id)`; canonical_account_id is the
server-stored verified provider account scope, never the public alias or a
caller-supplied field. The index applies
where `ever_funded OR lifecycle <> VOID_UNFUNDED`. This permits several
audited never-funded void attempts and one current attempt, but any payment
that ever funded remains blocked after repayment or reversal. Never clear
ever_funded or delete a row to permit a retry. Contradictory settlement after
an authoritative void is a terminal-conflict hold with preserved provider
evidence, not automatic reuse or a fabricated repayment transition.

Worker polls once per second, claims one bounded job with a 60-second lease
and unique fencing token, HTTP timeout ten seconds. No I/O while SQL locks are
held. Every claim/finish state transition requires expected state, matching
lease token AND `lease_expires_at > clock_timestamp()` checked by PostgreSQL.
Claim/finish transactions use the same facility-first lock order as settlement,
never operation-first then facility. An expired DISPATCHING lease
becomes UNKNOWN and reconciliation-only. Read/transport retries use 5, 30,
120, 300 and 900 seconds, then HELD. Pending settlement polls once per minute
for at most 30 attempts before HELD. Authorization/permission/schema mismatch
holds immediately. Database unavailability cannot acknowledge webhook receipt
or fabricate operation success. One worker restart resumes saved work.

## Narrow HTTP contracts and coordinator progress

All routes below are proposals. Reuse the shortfall HS256 validation pattern,
with dedicated audience `flowcard-funding-collection`, issuer allowlist for Go
callers, separate per-role signing keys and at most 30-second assertions.
Do not preserve `flowcard-node-proxy` as a new dependency. Map authenticated
subjects to explicit scopes; actor claims must match the verified principal.
No browser token, supplied actor ID, model output or onboarding cookie grants
approval. Live customer delegation requires a separately verified identity
boundary. Default deployment binds loopback/private networking.

Commands require opaque 1–128 safe ASCII `Idempotency-Key`, scoped by
authenticated principal +mode +route +key, canonical body hash and version.
Identical committed replay returns original receipt before rechecking sources;
different body returns 409. A receipt references current read URLs and never
claims that a queued command settled. Return 201 for new records, 202 for new
queued work, 200 for replay, 400 malformed, 401/403 auth, 404 unknown, 409
version/binding conflict, 422 deterministic blocked and 503 dependency/DB
unavailable. Persist idempotent blocked outcomes; a new review uses a new key.
Responses are no-store; coordinator DTOs expose internal aliases and evidence
references, not provider account/card IDs or protected source bodies.

| Route | Role and request |
| --- | --- |
| POST `/api/offers` | Coordinator/operator: `{evaluation_id}`. Terms derived server-side. |
| GET `/api/offers/{id}` | Scoped reader: exact offered terms and binding state. |
| POST `/api/offers/{id}/acceptances` | Customer/demo actor: `{terms_hash, expected_version}`. |
| POST `/api/offers/{id}/approvals` | Operator only: `{terms_hash, expected_version, reason_code}`. |
| POST `/api/offers/{id}/fund` | Coordinator/operator: `{expected_version}`. Recheck/reserve only. |
| GET `/api/bridges/{id}` | Scoped reader: terms, balances, lifecycle, holds and authoritative version. |
| GET `/api/bridges/{id}/collections` | Scoped reader: deposits, attempts, allocations and reconciliation progress. |
| POST `/api/bridges/{id}/collection-reviews` | Coordinator/operator: `{expected_version, reason_code}`; schedule checks only. |
| POST `/api/operations/{id}/reconcile` | Coordinator/operator: `{expected_version, reason_code}` schedules lookup only. |
| GET `/api/operations/{id}` | Scoped operation summary and held/retry state. |
| GET `/api/servicing/changes?after_seq=N` | Coordinator: ascending committed changes, 25 per page. |
| GET `/api/servicing/state?customer_id=...` | Underwriting reader: current ledger/history/invoice facts and version. |

`GET /api/servicing/state` returns facility ID/config version/total/reserve,
principal/reservations/other commitments; customer bridge IDs, debt buckets,
funded/due/closed/default dates and outcomes; invoice unpaid/allocation facts,
completeness and source revisions. It is a read model with mode, captured time
and committed ledger version, never a funding authorization.

Changes carry mode, sequence, resource ID/version, originating case/evaluation
references, customer, status/reason, debt summary, operation progress, evidence
references and deterministic allowed next actions. Events cover offer waiting
on acceptance/approval, funding pending/held/settled, partial collection,
authority blocked, due/overdue/default, reconciliation needed and closure.
Use one transactional per-mode counter row, locked before writing a change,
so sequence allocation and commit order agree. A plain bigserial high watermark
can skip late commits. Rollback also rolls back the counter increment. This
serializes the brief final change-write portion of transactions per mode, an
acceptable throughput tradeoff for this demo. Acquire it after financial locks
in the common order and perform no external I/O while holding it.
Keep all changes for the short-lived installation,
page using the last returned sequence, and allow replay from zero.

Bridge reads include `id`, `mode`, `version`, `as_of`, `customer_id`,
`originating_case_id`, `evaluation_id`, `offer_id`, `terms_hash`,
`principal_owed_cents`, `contract_fee_owed_cents`, `late_fee_owed_cents`,
`due_on`, `funded_at`, `lifecycle`, `delinquency`, `days_past_due`,
`hold_reasons`, `operations` and `allowed_next_actions`. Collection reads
include that same bridge version and bounded attempt/allocation rows plus
cursor. Operation summaries include internal ID, kind/state, reserved amount,
last verified settlement reference, retry count, next check and hold reason.
Unknown provider facts remain null. A collection page cursor binds bridge
version; a changed version returns 409 so the caller restarts its bounded read.

For coordinator monitoring, `obligation_id` is the stable bridge ID in V1,
not a new collection object per deposit. Include `source_binding_id` (a
nonsecret configured installation identity), internal `account_ref`,
`currency: USD`, `last_attempt_id`, `last_attempt_state`,
`last_provider_observed_at`, `next_eligible_retry_at` and separate
`acceptance_status`, `approval_status`, `collection_authority_status` fields.
These statuses are VALID/MISSING/EXPIRED/REVOKED/NOT_REQUIRED/UNKNOWN where
applicable, with controlled reasons. They describe stored guard evidence,
never a reusable debit authorization. Account references are internal aliases,
not provider account IDs. `as_of` is server response time; provider observation
time remains separate and nullable. `due_on` is a UTC date; DPD begins on the
next UTC date, not at an invented provider timestamp.

Initial allowed-next-action codes are WAIT_FOR_SETTLEMENT,
REQUEST_OPERATOR_REVIEW, VERIFY_COLLECTION_AUTHORITY, RECONCILE_OPERATION,
WAIT_FOR_ELIGIBLE_DEPOSIT and MONITOR_REPAYMENT. They are recommendations for
the coordinator's first read-only slice, not execution permission. No new
descending collection list is needed: replay the retained ascending change
feed from zero to discover obligations, then read current bridge/collections
detail by ID. Persist feed binding/mode/sequence and deduplicate resource
versions; no recurring full servicing scan is required under this contract.
WAIT_FOR_ACCRUAL additionally represents unfinished deterministic catch-up.

Changes response is `{mode, items, next_after_seq, has_more}`; each item has
`sequence`, `resource_type`, `resource_id`, `resource_version`, `change_type`,
`recorded_at` and the sanitized summary described above. No items returns the
input sequence unchanged. Reject unknown query keys/mode or malformed cursors.
Enumerate `resource_type` as OFFER, BRIDGE or OPERATION. Always include
`customer_id`, internal `account_ref`, nullable exact `originating_case_id`,
`evaluation_id`, `offer_id`, and `bridge_id`/`bridge_version`. A bridge ID may
be null only before bridge creation; when present its version is mandatory.
Resource version belongs to the named resource, never implicitly to a bridge.
Every operation, authority, debt, settlement or hold mutation affecting bridge
state or allowed actions atomically increments the parent bridge version and
emits it, even if the event resource is OPERATION. This invalidates older
coordinator recommendations. OFFER changes with no bridge route to the exact
originating case; they cannot create a collection obligation.
Retaining all rows means no normal retention gap; if a future retention change
occurs, return an explicit gap error and require reconciliation from a complete
state read, never silently advance the checkpoint.

Coordinator persists its own checkpoint and case links; it deduplicates by
mode/sequence and reads current aggregate before an action. Expected-version
checks stop stale action selection. Explanations/reminder recommendations stay
in coordinator; balances, settlement and required actor decisions stay here.
Coordinator checkpoints the change and `refresh_needed`/target bridge version
together, then refreshes detail outside that transaction. It fences agent
publication against the current parent bridge version and cannot publish an
old allowed action after a newer operation/authority update.

Bridge lifecycle: OFFERED → AWAITING_ACCEPTANCE/APPROVAL → RESERVED →
FUNDING_PENDING → ACTIVE/COLLECTING → REPAID. Expired/superseded unsent offers
terminate without debt. HELD/RECONCILIATION_REQUIRED is an independent blocking
flag, preserving the underlying debt and operation state. Delinquency is a
separate derived stage, not a destructive replacement for collection progress.

Webhook endpoint is `/webhooks/airwallex/servicing`, externally routed only
after provider subscription/path ownership review. Port the shortfall raw-body
HMAC, 10 MB body bound, five-minute future timestamp rule and durable event
deduplication. Invalid signature is 401, invalid envelope 400, durable insert
failure 503, successful/duplicate durable receipt 200. No Node forwarding
dependency or dual Node/Go servicing of the same event/customer.

## Readiness, live gates and deferred work

`/healthz` proves process liveness. `/readyz` checks DB, migrations, policy
package/hash, source-mode consistency and required local interfaces. Return
explicit `mode`, `runtime_ready`, `funding_enabled`, `collection_enabled`,
`provider_acceptance` and blocker codes. A ready fixture service says
`FIXTURE_ONLY`. SANDBOX can serve reads/holds while provider mutations remain
disabled; a valid configuration alone cannot set provider acceptance VERIFIED.
Readiness also returns `supported_funding_regimes:[ABUNDANT,NORMAL]` and
`scarce_execution_supported:false`; no approval record enables ranked funding.

Live acceptance requires recorded verification of:

- Canonical shortfall extraction and fresh payment/balance refresh, including
  current amount and account/card mapping, already unsupported upstream.
- Shared Go hard-rule contract, funding-facts source and complete authoritative
  opening facility/exposure/receivable state with single-writer cutover.
- Customer authentication, exact financing disclosures and collection mandate,
  operator permissions and recorded terms/actor bindings.
- CA transfer creation/retrieval schema, account scoping, idempotency retention,
  absence/lookup consistency, settlement/return states, amount/fee semantics
  and a live full-shortfall concurrency contract.
- B-to-A CA-transfer collection authority, purpose codes,
  available-balance/net deposit semantics, provider fees, idempotency and
  settlement/reversal/refund contracts. Existing Node method names prove none
  of these prerequisites for the new service.
- Signed deposit/transfer webhook schemas, subscription version, exact
  matching IDs, routing ownership and missed-event retrieval.
- Disclosed overdue-fee accounting and any required card-control protections.

Build the documented Go authentication/CA-transfer/retrieval adapter and
verify its HTTP contract against local doubles. Unknown parser or replay
capabilities remain explicitly unsupported; real dispatch stays gated.
No provider simulations, test debits or live model calls are part of proof.
Deferred: UI, public auth, card retry/controls, notifications, automatic
refunds, multi-bridge allocation, production compliance, Salesforce ingestion,
full accounting reports and AWS deployment. Live gates remain named blockers.

## Verification boundary

At implementation end, add at most three focused Go test functions, with a
few representative cases per critical feature: money/terms/guards; HTTP/auth/
idempotency/progress; isolated PostgreSQL lifecycle/concurrency/recovery.
Exercise fee-first partial settlement and uncertain-send restart in the last
journey. Inject clock, fixture provider and upstream HTTP. No live-funds tests.
Report local fixture results separately from provider acceptance. The detailed
plan lists exact files and acceptance checks; no tests run in this design task.
The same final database journey must drive normal API commands through durable
operations, actual worker, actual Go Airwallex HTTP adapter, local HTTP double,
GET reconciliation and ledger/collection reads. A standalone adapter test plus
directly injected application settlements does not meet this acceptance.
Fixture controls may change the mock provider's state or lose a receipt;
they cannot write servicing debt or invoke its settlement reducer directly.

## Pinned delegation details

These decisions are part of the proposed handoff, not choices left to the
developer. A later API document transcribes them; it cannot redefine money
behavior without review.

### Canonical terms and actor records

`offer_revision` is immutable (first revision 1); `version` is the separate
mutable aggregate concurrency counter. Acceptance/approval increments
aggregate version without invalidating an unchanged terms hash. Supersession
creates a new offer ID/revision and new bindings.

Encode terms with a dedicated Go struct in this exact JSON field order:
`terms_version`, `source_mode`, `facility_id`, `funding_account_ref`,
`fee_beneficiary_ref`, `customer_id`, `borrower_account_ref`, `card_ref`,
`transaction_id`, `offer_id`, `offer_revision`, `principal_cents`,
`contract_fee_cents`, `term_days`, `offered_on`, `due_on`, `expires_at`,
`policy_id`, `policy_hash`, `disclosures_version`, `waterfall_version`,
`late_fee_convention`,
`collection_authority_id`, `collection_authority_version`,
`mapping_fingerprint`. Use `encoding/json.Marshal`, no maps/omitempty,
integer cents/revisions, RFC3339 UTC whole-second expiry and YYYY-MM-DD dates.
IDs/version strings are nonempty ASCII; no floats/null monetary values.
`terms_version=1`, `waterfall_version=1`. SHA-256 lowercase hex of those bytes
is `terms_hash`; do not hash rendered prose or request JSON key order.

Decision record fields: UUID, mode, offer ID/revision/hash, actor ID/type,
decision ACCEPT/APPROVE/AUTO_APPROVE, reason code, server recorded_at,
authentication subject, disclosures version and source references. Resolve
actor-to-customer/role in the imported DEMO registry; never accept actor ID or
customer ownership from command JSON. Auto approval cites fresh all-PASS
authorization and policy hash, never an agent explanation.

Authority import fields: ID, positive version, mode, customer ID, source
account reference B, destination reference A, USD, permitted kinds
COLLECT_FEE/COLLECT_PRINCIPAL, maximum total debit cents (including provider
fees), consumed/reserved debit, valid_from/expires_at, status ACTIVE/REVOKED,
disclosures version, evidence refs and captured_at. A consumed reservation
becomes settled consumption once; only confirmed no-debit cancellation/failure
releases it. Revocation does not erase prior consumption. Store both authority
ID/version and exact scope on every collection operation. Missing/unknown
authority is a blocker. SANDBOX authority publishing remains a live gate.

### Service configuration and authorization

Use Go 1.27.3 and pgx v5.12.0, matching existing services. Local public policy
module dependency uses repository path replacement to
`../underwriting-engine`; no root go.work or Node build step. Before handing
off source work, the lead must provide the reviewed public policy module and
pin its actual exported types/function signature. If absent, stop that task;
do not clone the evaluator or fabricate a final-result stub.

Proposed variables, documented in the new service README only during initial
delivery (no root `.env` changes in this plan):

| Variable | Meaning |
| --- | --- |
| DATABASE_URL | Restricted servicing runtime connection |
| SERVICING_MODE | DEMO or SANDBOX |
| SERVICING_HTTP_ADDR | Default `127.0.0.1:8085`, fail if occupied |
| SERVICING_FACILITY_ID | Single configured USD facility/A binding |
| SERVICING_SOURCE_BINDING_ID | Stable nonsecret installation/feed identity |
| SERVICING_COORDINATOR_SECRET | Coordinator assertion key, at least 32 bytes |
| SERVICING_OPERATOR_SECRET | Separate operator assertion key |
| SERVICING_CUSTOMER_SECRET | DEMO-only synthetic customer assertion key |
| SERVICING_READER_SECRET | Separate underwriting read-model key |
| UNDERWRITING_BASE_URL, UNDERWRITING_SERVICE_SECRET | Reviewed context/evaluation HTTP boundary |
| SHORTFALL_BASE_URL, SHORTFALL_SERVICE_SECRET | Existing saved shortfall read boundary |
| SERVICING_PROVIDER_MODE | MOCK_HTTP in DEMO, AIRWALLEX_SANDBOX in SANDBOX |
| SERVICING_PROVIDER_BASE_URL | Loopback-only mock URL; real sandbox host is fixed |
| SERVICING_LIVE_DISPATCH_ENABLED | Default false; true alone cannot clear evidence gates |
| AIRWALLEX_CLIENT_ID, AIRWALLEX_API_KEY | Server-only; synthetic in mock mode |

DEMO +MOCK_HTTP permits fixture creates when local prerequisites pass, even
with SERVICING_LIVE_DISPATCH_ENABLED=false. It never permits real host access.
SANDBOX requires AIRWALLEX_SANDBOX and all named live gates plus explicit
enablement; otherwise create calls are blocked while read/reconcile can remain
available. Report fixture execution and live execution separately in readiness.
Do not require a live-enable flag merely to run the offline fixture journey.

Each signing key is at least 32 bytes and roles use different keys. Inbound
assertions use issuer `flowcard-go`, audience `flowcard-funding-collection`,
registry-bound subjects, jti, integer iat/exp, maximum TTL 30 seconds and five
seconds skew. Validate algorithm HS256, one Authorization header, signature
and role/actor binding. Outbound underwriting/shortfall clients retain those
services' exact existing issuer/audience/subject labels, including compatibility
issuer `flowcard-node-proxy`; that string introduces no Node runtime dependency.
Customer subjects are rejected entirely in SANDBOX until live delegation is
reviewed. Private role keys/DSNs never appear in output or tracked fixtures.

### Wire validation, history and errors

Command bodies allow only the fields in the route table, maximum 16,384 bytes;
import files maximum 2,000,000 bytes and 10,000 array entries. Reject unknown
or repeated JSON keys, trailing JSON, noninteger money and overflow. One
Idempotency-Key uses `[A-Za-z0-9._:-]`, 1–128 characters. Fingerprint typed
request struct with fixed field order; replay the same committed body/IDs
before version/source checks. Successful replay uses HTTP 200 (original
creation/queue status remains audit metadata); a saved deterministic rejection
replays its original 422/error. A retriable 503 has no committed result.

Errors are `{error:{code,message}}` with safe fixed text. Common codes:
INVALID_REQUEST/INVALID_QUERY (400), UNAUTHORIZED/FORBIDDEN (401/403),
RESOURCE_NOT_FOUND (404), VERSION_CONFLICT/IDEMPOTENCY_CONFLICT (409),
PAYLOAD_TOO_LARGE (413), STORAGE_UNAVAILABLE/UPSTREAM_UNAVAILABLE (503).
422 guard codes include TERMS_SUPERSEDED, OFFER_EXPIRED, ACCEPTANCE_REQUIRED,
APPROVAL_REQUIRED, COLLECTION_AUTHORITY_BLOCKED, INPUT_STALE,
HARD_RULE_FAILED, SCARCE_CAPITAL_RANKING_UNSUPPORTED and MAPPING_CONFLICT.
Commands rejected deterministically save their idempotent blocked receipt.
Reconcile expected_version targets the operation version; collection review
expected_version targets the bridge. New asynchronous review/reconcile returns
202 with `{command_id, resource_id, resource_version, status:QUEUED}`.
Funding success receipt references bridge/reservation/operation IDs and says
RESERVED, never SETTLED. Offer/actor creation returns 201 with resource ID,
version and read URL. Read DTOs follow the explicit fields above.

Collection attempt history sorts `(created_at DESC,id DESC)`, 25 per page.
Cursor is base64url typed JSON `{mode,bridge_id,bridge_version,created_at,id}`;
validate all fields/filter binding and reject changed version with 409.
Allocation rows inside each attempt sort by persisted allocation index.
Operations in bridge reads sort created_at then ID ascending. Unknown fields
stay null. No raw provider account IDs, bodies or credentials leave these reads.

### Executable fixture and opening-state limits

Add `cmd/demo prepare --as-of now --out .dev/demo`, `cmd/demo serve-provider
--manifest PATH`, and `cmd/demo run --manifest PATH --api-url URL`. Preparation
writes synthetic mapping SQL, known-empty facility/history declaration,
receivables, actor/authority import files, source-compatible shortfall/context
HTTP fixtures and a manifest with fresh IDs/UTC dates/keys. No source mutation,
database reset or direct ledger-result insertion. The mock server implements
real adapter login/transfer/list/retrieve/balance/deposit HTTP shapes; local
fixtures pin unavailable live event/reference contracts explicitly.

The runbook migrates/imports only a dedicated guarded demo database through
the separate migrate/import commands. `demo run` performs no DDL and uses
normal API actor actions and the real serial worker/HTTP adapter. Show
$7,000/30-day funding, $190 fee, $100 then $7,090 net deposits and partial
settlement with zero allocations/debt on closure. Use distinct identities for
timeout recovery and midnight/accrual checks. Prepare relative dates from one
anchor; expired data requires a new prepared run, no stale-time bypass.
The manifest never contains real credentials. Startup refuses nonloopback
mock targets; live provider access is not a fallback.

## Task 12A and 12B implementation boundary

`cmd/demo prepare --as-of now --out PATH` emits fresh synthetic upstream input
bundles, known-empty funding imports, guarded mapping SQL, provider-double
state, and private local runtime configuration. It does not fabricate a saved
decision: the actual underwriting API creates it from the checked input
profile. `cmd/demo serve-provider --manifest PATH` serves the adapter-shaped
provider double at `127.0.0.1:8091`; its controls alter mock provider facts
only. Preparation can bind to a fresh owner-prepared known-empty Underwriting
manifest, retaining its IDs and anchor without changing its captured files or
copying saved decisions. It validates safe, task-owned database-name prefixes
and writes matching mapping SQL.

Task 12B adds `cmd/demo run --manifest PATH --api-url URL`. The runner reads a
unique saved Underwriting assessment, validates the selected mode/database/
policy/source binding, then uses authenticated API actions for synthetic
customer acceptance and conditional operator approval. It submits the regular
fund command and waits for the already-running serial worker and actual local
HTTP adapter to settle and reconcile. The provider double controls only its
own deposit/transfer facts. The runner verifies fee-first allocations,
zero-debt closure, zero backing/holds, and borrower/funding balances. The
`tight` path records approval before the scarce-capital 422 and checks that no
provider transfer was created. It does not start services, run DDL or write
application ledger facts.

Task 12A also separates `/health/live` from `/health/ready`, bounds SQL
statement and lock waits and request contexts, and adds root-context Docker
packaging that copies the pinned public underwriting module explicitly.
Readiness reports its connected database name; migration/import commands and
the runner verify the expected owned target. Source changes alone do not
establish runtime or provider acceptance. See the
[staged local fixture runbook](/docs/demo/funding-and-collection-local.md) for
the command sequence and synthetic-data labels.

## See also

- [Implementation plan](/docs/plans/20261114-funding-and-collection.md)
- [Product requirements](/docs/requirements/README.md)
- [Bridge Capital policy](/docs/designs/bridge-capital-policy-spec.md)
- [Underwriting proposal](/docs/designs/underwriting-engine.md)
- [Coordinator proposal](/docs/designs/go-coordinator.md)
- [Shortfall design](/docs/designs/shortfall-detection.md)
- [Sandbox prerequisites](/docs/demo/airwallex-sandbox-demo-prerequisites.md)
- [Demo acceptance](/docs/infra/demo-acceptance.md)
