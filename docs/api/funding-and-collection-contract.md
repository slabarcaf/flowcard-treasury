# Funding and collection contract

This document transcribes the reviewed funding and collection design into an
implementation contract for its Go service. Funding consumes the committed
public underwriting evaluator and authenticated context API; it does not
define a second financial policy.

[TOC]

**Sequencing update:** public policy commit
`d2aeb7ff1987fb7ae6ac4c47fc40e5ca8bae3766` supplies the typed evaluator and
immutable pricing accessor. Funding loads `Config` once and takes pricing,
policy ID, and policy hash from that same value. The local policy dependency
gate is resolved; final journey tests and provider dispatch remain separate
review gates.

## DEMO fixture imports

`cmd/migrate --database-name NAME` and
`cmd/import --database-name NAME --kind KIND --file ABSOLUTE_PATH` require a
safe owner-prefixed database name and verify it against `current_database()`
before DDL/import. Import accepts only configured DEMO mode over the restricted
runtime URL. Every typed version 1 file has `source_mode`, `source_id`,
`source_version`, `captured_at`, and nonempty `source_refs` (`system`,
`record_id`, `version`, `effective_at`).
Capture time may be at most five seconds ahead; older source capture is
rejected. Canonical typed JSON SHA-256 binds the producer key. Exact replay is
idempotent; changed content at that key conflicts. Import journal write and
owner update commit together. Request files cap at 2 MB and arrays at 10,000;
unknown/repeated keys and trailing JSON are rejected.

Facility files declare `known_empty_books:true`, USD, configured facility,
source A, total/reserve/other commitments and mapped customers. The importer
checks active existing roster/card mappings and zero servicing debt,
reservations and defaults; it creates only servicing-owned customer rows.
Receivable files carry customer/source/invoice/payer, eligible unpaid cents,
currency, optional due date and explicit verification/completeness/dispute
flags with source ref. Caller cannot set allocations or reservations. Source
updates preserve local encumbrances; an impairment records the factual new
amount and puts affected bridges on hold rather than clamping backing.
Authority files are synthetic DEMO facts for B-to-A USD collection, allowed
fee/principal kinds, limits, dates, disclosures and evidence refs. Incoming
usage counters must be zero. The latest authority version governs permission,
while consumed and reserved usage is accumulated from operations across every
version, so publishing a new version cannot reset prior debit usage. Actor files
bind DEMO customer/operator/system subjects to roles; they never create
acceptance or approval decisions. SYSTEM is only for automatic-approval audit
and is not an inbound user role.

Examples live in `services/funding-collection/testdata/`. Migration 002 adds
import provenance and narrow source-owned update columns. No public mapping
rows are written. SANDBOX import remains blocked pending verified source and
mandate handoffs.

## Authority and service boundaries

`services/funding-collection` owns offers, actor decisions, reservations,
bridges, debt, receivable encumbrances, collection attempts, provider
operations, settlement and its ledger. It is one HTTP API and one serial
worker using PostgreSQL, Go 1.27.3, pgx v5.12.0, and otherwise the standard
library. It does not depend on the Node runtime, a broker or workflow
framework.

Underwriting assesses only. An eligible result never authorizes a transfer.
The coordinator investigates and requests only allowed work. Funding verifies
acceptance, approval, terms, mappings, capacity and fresh evidence, then owns
the provider operation and settlement. Provider wallet balance supplements
but never defines facility capital. The funding account A sends to borrower
B; fee and principal collection are distinct connected-account transfers
from B to A. Provider account IDs and credentials remain server-side.

Read normalized verified non-ledger customer context over HTTP before taking
SQL locks. Under the common facility-first lock order, substitute the locked
funding-owned customer, facility and receivable facts into the shared pure
policy evaluator. Pin candidate, offer and evaluator policy ID/hash; any
mismatch blocks funding. Do not copy or recreate the evaluator.

### Upstream interfaces and source inventory

The public underwriting `policy` Go module exports normalized input/result
types, `Evaluate`, `LoadConfig`, `PolicyID`, `PolicyHash`, and the immutable
scalar `Config.Pricing()` accessor. Funding pins module commit
`d2aeb7ff1987fb7ae6ac4c47fc40e5ca8bae3766`, policy ID
`bridge-capital-go-v1`, and embedded SHA-256
`0a0cf42f72e101a458c260be8fbca315704d9a093264049acbf366f8cd4d38f0`.
Load pricing and policy identity from the same `policy.Config`; do not guess
thresholds or create a placeholder result.
The shortfall detail DTO intentionally has no `source_mode`; the client binds
its authenticated endpoint to configured `SERVICING_MODE`, validates that
installation scope, and cross-checks the saved evaluation mode and identity.
It never infers mode from a missing DTO field. The evaluation service contract
in `docs/api/underwriting-contract.md` is a source reference. The context
endpoint is `GET /api/underwriting/customers/{customer_id}/context`; it returns
normalized non-ledger facts, snapshot version, capture time, completeness,
source references and policy hash. Require fresh facts and verify scope.
A funding servicing state DTO must report mode, capture time, committed
ledger version, facility
configuration and commitments, customer debt/reservations/history, and
invoice unpaid/allocation/completeness/source revisions. It is a read model,
never authorization.

Use the shortfall service's saved observation and read boundary, but canonical
payment/balance refresh and full-shortfall race semantics remain live
integration gates. Reuse the active roster/card mapping through a read-only
adapter. Mapping locks use the fixed privileged helper described below. Keep
source invoice truth upstream and imported receivable state with source
revision/completeness. Existing Node funding, reservation, ledger, collection
and Airwallex code is implementation evidence only; do not import its runtime
logic, old due-only collection, 5 bps/day fee, or mutable tables. Existing
migrations remain unchanged.

## Money, terms and debt

All money is checked integer USD cents. For integer term days 7 through 90:

```text
fee_cents = 5000 + round_half_up(principal_cents * 200 * term_days / 300000)
```

Round the percentage component once, with checked intermediates or standard
library big integers. A $7,000 30-day bridge has a $190 fee and $7,190 total;
$10,000 for 60 days has a $450 fee. The full selected fee remains due after
early repayment. Offers, acceptance and reservations book no debt. Confirmed
funding settlement books principal and contracted fee exactly once.

Customer line exposure is funded principal owed plus all unpaid fees,
including earned overdue fees, plus active unfunded reserved principal and
fee commitments. A converted reservation is not counted twice. A new bridge
adds principal plus its fixed fee. Facility cash capacity counts principal
commitments only. One ordinary open bridge per customer; funding is full or
zero. USD only.

Canonical payment identity is mode + server-verified connected account +
parent transaction ID. Keep authorization event separately. Different decline
attempts for one payment cannot produce separate bridges. Never accept
customer/account/amount/eligibility/consent/approval/settlement authority from
AI or command bodies.

### Canonical terms hash

`offer_revision` is immutable, begins at 1, and differs from the mutable
aggregate `version`. Acceptance or approval increments aggregate version
without changing an unchanged terms hash. Supersession creates a new offer ID,
revision and bindings.

Serialize a dedicated Go struct with exactly these fields in this order, with
no maps or `omitempty`:

```text
terms_version, source_mode, facility_id, funding_account_ref,
fee_beneficiary_ref, customer_id, borrower_account_ref, card_ref,
transaction_id, offer_id, offer_revision, principal_cents,
contract_fee_cents, term_days, offered_on, due_on, expires_at,
policy_id, policy_hash, disclosures_version, waterfall_version,
late_fee_convention, collection_authority_id,
collection_authority_version, mapping_fingerprint
```

Use `encoding/json.Marshal`; cents and revisions are integers. Expiry is
RFC3339 UTC at whole-second precision; dates are `YYYY-MM-DD`. IDs and version
strings are nonempty ASCII, except `collection_authority_version`, which is a
positive integer aligned with the authority record version. `policy_hash` and
`mapping_fingerprint` are 64-character lowercase hexadecimal SHA-256 strings;
`late_fee_convention` is `OPENING_PRINCIPAL_UTC_DAY`. No float or null
monetary value. Pin
`terms_version=1`, `waterfall_version=1`; hash the exact encoded bytes with
SHA-256 and lowercase hexadecimal. Do not hash presentation prose or request
JSON key order.

Decision records contain UUID, mode, offer ID/revision/hash, actor ID/type,
decision (`ACCEPT`, `APPROVE`, `AUTO_APPROVE`), reason code, server
`recorded_at`, authenticated subject, disclosures version and source refs.
Resolve actor and customer/role in the imported DEMO registry. Command JSON
never supplies actor identity or customer ownership. Auto approval cites a
fresh all-PASS authorization and policy hash, never agent explanation.

Collection authority import fields: ID, positive version, mode, customer ID,
source account B, destination A, USD, permitted kinds (`COLLECT_FEE`,
`COLLECT_PRINCIPAL`), maximum total debit cents including provider fees,
consumed/reserved debit, `valid_from`, `expires_at`, status (`ACTIVE`,
`REVOKED`), disclosures version, evidence refs and `captured_at`. A consumed
reservation becomes settled consumption once; release only on confirmed
no-debit cancellation/failure. Revocation does not erase consumption. Each
collection operation stores authority ID/version and exact scope. Missing or
unknown authority blocks. SANDBOX authority publication is gated.

### Offer and maturity guards

Create an immutable offer from a saved evaluation whose hard rules all PASS.
`REVIEW_REQUIRED` solely for `HUMAN_APPROVAL_REQUIRED` may create an offer
requiring an operator approval. Any hard FAIL or UNKNOWN blocks. Keep the
original evaluation, rule results, payment/customer identity, mode and policy
hash. Offer expiry is ten minutes after creation. No capital is promised
while waiting. `offered_on` is the server UTC date and `due_on` is
`offered_on + term_days`; settlement does not move maturity. Do not first-send
after `due_on`.

Reservation and first send also require current server UTC date to equal
`offered_on`. At midnight, supersede proven-unsent work, cancel its unsent
intent and release its reservations; issue new terms and actor bindings. Do
not shift the evaluator's today-plus-term invoice window. Already-dispatched
movement reconciles under original terms. A late confirmed funding settlement
is booked with original due date and held for review.

Any changed bound term, customer/payment, mapping or disclosure version
supersedes the offer. Old decisions remain audit records but cannot authorize
the replacement. Recheck policy at funding; policy/regime changes require
fresh evaluation and approval when newly required, with acceptance still
bound to unchanged terms. Approval cannot waive a hard rule. Fixture-only
actions use the local Go demo actor and are visibly synthetic.

### Full funding reservation command

`POST /api/offers/{id}/fund` accepts only
`{"expected_version": n}` plus an `Idempotency-Key`. A funding coordinator or
authenticated operator may issue it. The body never supplies an amount,
account, payment, approval, or policy result. An identical committed replay
returns HTTP 200 with the original response; a changed body under the same key
returns HTTP 409. Transient upstream or storage failures return HTTP 503
without a command receipt. Deterministic guard and policy refusals return HTTP
422 and retain their command receipt and, after evaluation, an immutable
`funding_check` containing the typed input/result and provider observations.

A successful reservation returns HTTP 202:

```json
{
  "bridge_id": "uuid",
  "reservation_id": "uuid",
  "operation_id": "uuid",
  "version": 1,
  "status": "RESERVED",
  "read_url": "/api/bridges/uuid"
}
```

The transaction stores a never-sent reservation, full invoice allocation,
zero-debt bridge, immutable FUND operation identity, balanced memo ledger
entry, command receipt and change-feed rows together. HTTP 202 means only
`RESERVED`; it never asserts provider submission or settlement. Reservation
expiry is the earlier of two minutes or offer expiry. Unknown or dispatched
movement is never released by this reservation path.

The fresh borrower balance is cross-checked against the current attempted
payment using `max(attempted_amount_cents - available_balance_cents, 0)`. A
changed or zero current need never becomes a smaller reservation. The source A
check subtracts every other unresolved local debit and its known provider fee
from the fresh provider balance before checking the full candidate plus its
known fee. Unknown fees fail closed. Only the explicit DEMO `MOCK_HTTP`
zero-fee contract supplies a known zero; SANDBOX remains gated without a
reviewed fee contract.

### Debt, accrual and lifecycle

DPD is `max(0, current_UTC_date - due_on)` for nonzero settled debt. Due date
is DPD 0; Watch 1–7, Delinquent 8–30, Serious 31–60, Default 61+. Record first
default date once at `due_on + 61 days`, including worker catch-up; repayment
does not erase it. Publish actual default date; eligibility interpretation
belongs to the evaluator.

Daily overdue accrual is on actual unpaid principal. Assess at the start of
each overdue UTC day from 00:00 on `due_on + 1 day`. Opening basis reflects
confirmed settlements effective strictly before that midnight; a payment at
midnight belongs to the new day. Full opening-day charge applies on payoff
day. This timing detail remains a proposed, disclosed demo convention, not
separately approved live policy. Bind
`OPENING_PRINCIPAL_UTC_DAY` into terms and disclosures. Per bridge/date record
opening principal, contributing settlement references, rational numerator
and assessed delta. Add `opening_principal_cents * 200` to cumulative
numerator, denominator 300000, round cumulative assessed cents half-up once,
and book the difference from prior assessed. Track `last_accrued_on`,
cumulative assessed and cumulative paid separately; payment never erases
earned fees. A unique bridge/day effect prevents duplicate accrual. Catch up
before every collection planning, first-send and closure, in date order, at
most 90 dates/pass. Pending catch-up blocks new debit, closure and funding.
Use immutable effective-time principal settlement and reversal history,
including funding settlement; funding first settled after midnight contributes
zero to that day's opening basis. Late/backdated evidence that changes a
frozen basis raises correction hold and compensation review, never mutates old
fees or closes automatically. Overdue fees are unbacked debt.
Bridge/read DTOs expose `accrual_current`, `accrual_current_through` and
recorded debt as-of time. Stale balances are explicitly marked; if accrual is
not current the only allowed action is `WAIT_FOR_ACCRUAL`, and no final payoff
claim is made. GET does not write or catch up.

Lifecycle is `RESERVED`, `FUNDING_PENDING`, `ACTIVE`, `COLLECTING`, `REPAID`,
`VOID_UNFUNDED`, `REOPENED_HELD`. Offer waiting states belong to offers.
`ever_funded` becomes irreversibly true only on confirmed recipient funding
settlement. Void requires never funded, zero debt, no active reservation.
Normal customer-slot uniqueness covers RESERVED/FUNDING_PENDING/ACTIVE/
COLLECTING. REOPENED_HELD is outside the slot, remains in exposure, and blocks
new funding. Payment uniqueness is `(mode, canonical_account_id,
transaction_id)` where the account is the server verified provider scope;
apply where `ever_funded OR lifecycle <> VOID_UNFUNDED`. Never clear
`ever_funded` or delete to permit retry. A contradictory settlement after
void is held with evidence, not reused.

Proven-unsent guard failure/expiry, or authoritative no-movement/returned
funding with no recipient settlement ever, atomically cancels the operation,
sets zero-debt bridge to `VOID_UNFUNDED`, releases facility/customer/invoice
commitments, terminates the consumed offer, increments bridge version and
emits feed change. Retain operation/evidence history; add no repayment history.
A fresh offer with fresh acceptance/approval and a new operation identity may
retry the never-funded payment. `ever_funded` remains permanent after any
confirmed recipient funding.

## Capacity, reservations and database boundary

Funding owns its principal/fee balances, facility commitments, reservations,
repayment allocations and receivable encumbrances. Facility available is
`max(0, total - ceil(total * reserve_bps / 10000) - principal_owed -
active_reserved_principal - other_verified_commitments)`. The reserve floor
rounds up; missing commitments are never assumed to be zero. Reserve principal
only at facility level, and principal plus full fee against customer line and
eligible invoice coverage. Deterministically allocate invoices by due date
then source ID. For a $20,000 customer line, $12,810 existing exposure leaves
exactly $7,190 for a $7,000/30-day bridge.

Under facility-first locks, lock customer, bridge, invoices, mapping, operation
and feed as applicable. Recheck authoritative facts and fingerprint
locked facts. No external I/O while SQL locks are held. TIGHT/CRITICAL returns
`SCARCE_CAPITAL_RANKING_UNSUPPORTED` before reservation, even with human
approval. Underwriting may assess those regimes normally. Only ABUNDANT/NORMAL
dispatch is supported in first slice.

Use a separate `servicing` schema and restricted runtime role. Migration
`services/funding-collection/internal/postgres/migrations/001_initial.sql`
is new; only privileged migration command runs DDL. Runtime API/worker never
run DDL. Persist mode-scoped facilities/customers, receivables/allocations,
offers/actor decisions, authorities/actors, bridges, accrual days,
reservations, operations, deposits/attempts, webhook inbox/provider
observations, balanced ledger transactions/lines, idempotent commands and
committed changes. Constraints enforce checked positive amounts, USD,
valid states, immutable terms, one open obligation/customer, canonical
payment, request identity, settlement and allocation effect uniqueness.
Update balances atomically with ledger postings. Reservations are balanced
memo commitments, not debt. Late fees count toward exposure but do not consume
invoice capacity.

The helper is `servicing.lock_verified_mapping(customer_id, account_id,
card_id)`, called through transaction-bound `LockVerifiedMapping`. Fixed SQL
locks the exact roster row then card binding `FOR SHARE`, validating unique
active linkage. It returns the canonical tuple, not credentials. Fingerprint
field order is `customer_id`, `roster_account_id`, `roster_active`, `card_id`,
`card_account_id`, `card_active`; Go `json.Marshal` then SHA-256 lowercase
hex. Compare to bound terms/operation evidence. Source A is immutable for a
facility ID; change requires a new facility/offer binding. No dynamic SQL or
runtime mapping UPDATE.

The helper is fixed-SQL `SECURITY DEFINER`, fully
qualifies roster then card rows, uses safe `search_path`, and is owned by a
privileged non-runtime role. Revoke PUBLIC and grant runtime only `EXECUTE`.
Direct foreign grants remain SELECT-only. Follow facility-first lock order
and include locked mapping facts in the transaction fingerprint.

Before reservation and dispatch require payment/balance observation at most
30 seconds old and no more than five seconds in the future; require context at
most 24 hours old. A reservation lasts at most two minutes before first
dispatch, bounded by offer expiry. If dispatch started or certainty is absent,
retain commitments pending reconciliation.

Worker polls once/second, claims one bounded job with 60-second lease and
unique fencing token; HTTP timeout is ten seconds. Every claim/finish CAS
checks expected state, matching token and DB-clock unexpired lease. An
expired dispatch becomes UNKNOWN and lookup-only. Use ten-second HTTP outside
transactions. Retry reads/transport at 5, 30, 120, 300, 900 seconds, then
HELD. Pending settlement polls each minute up to 30 attempts, then HELD.
Permission/identity/schema mismatch holds immediately. Same facility-first
lock order applies; never lock operation first.

## Provider transfer and collection contract

Funding CA transfer scope is A, destination B. Separate fee and principal CA
returns use scope B, destination A; A is the recommended fee beneficiary.
This is direct connected-account transfer flow, no Charges-to-platform
baseline. Authenticate with `POST /api/v1/authentication/login`, server-side
`x-client-id` and `x-api-key`; cache bearer until `expires_at` and refresh
before expiry. Platform context is explicit; scoped keys may require
`x-login-as`. Disable redirects. Sandbox host is fixed to
`https://api.sandbox.airwallex.com`; DEMO uses loopback fixture HTTP only.
Create is `POST /api/v1/connected_account_transfers/create`, with platform
bearer and `x-on-behalf-of: A`, destination B for funding; B/A for collection.
Body fields are `request_id`, `destination`, `amount`, `currency`, `reason`,
`reference`; there is no caller-controlled source. Use exact two-decimal amount
strings, request ID <=50 chars, reference <=140 chars, server reason mapping.
Balance is `GET /api/v1/balances/current`, scoped via `x-on-behalf-of` A or B.
Known object retrieval is `GET /api/v1/connected_account_transfers/{id}`;
lookup is `GET /api/v1/connected_account_transfers?request_id=...`, retaining
original source scope. Lookup traverses bounded pages and requires exactly one
match on identity, amount, currency and account. Empty, multiple, contradictory
or eventual-consistency results do not prove no movement. Keep immutable amount/
payload hash and fixed source scope. UNKNOWN permits lookup only. Never blindly
recreate, guess success or claim exactly-once provider execution. Match the
authoritative retrieved source,
destination, operation/object/request identity, amount, currency, effective
settlement time and applicable fees. Missing fields require authoritative
retrieval; unknown status is held. Mismatched or contradictory terminal
results go to reconciliation.

DEMO selects `MOCK_HTTP`; synthetic creates work while
`SERVICING_LIVE_DISPATCH_ENABLED=false` and cannot reach a real host. SANDBOX
selects `AIRWALLEX_SANDBOX`; creates remain blocked until named live gates and
separate explicit execution authorization clear. The mock uses real adapter
HTTP shapes; fixture controls may change provider state or drop receipts, not
write application settlements. MOCK_HTTP payment and deposit reads use only
synthetic `GET /__fixture/payments/{parent_transaction_id}` and
`GET /__fixture/deposits/{deposit_id}` paths. Their envelopes carry mode,
customer/account/payment or deposit identity, exact USD cents, status and UTC
observation/settlement times. These are test-double contracts, never Airwallex
endpoints. Airwallex current-cash balance parsing follows the shortfall
adapter's cash-array shape and exact decimal-cents parser. A real transfer
source echo, provider fee semantics and settlement event envelope remain live
parser gates; absent fee stays unknown and blocks accounting assumptions.
The synthetic payment shortfall is required and nonnegative, so zero is a
valid fresh observation after a deposit; missing/null values remain invalid.
Its attempted amount and available balance are both explicit nonnegative
facts, and the adapter verifies `max(attempted - available, 0)` against the
shortfall before returning the typed observation.
An observed SETTLED or FAILED transfer requests authoritative final
verification. That recovery class never authorizes ledger booking by itself.

Operation kinds are FUND, COLLECT_FEE and COLLECT_PRINCIPAL. Persist operation
ID, mode, accounts, amount, USD, terms hash, request identity, payload hash,
bridge and trigger refs before send. Funding uses `bridge-<uuid>`, fee uses
`fee-<uuid>`, principal uses `principal-<uuid>` for the same operation. These
identities do not imply provider deduplication. States are QUEUED, DISPATCHING,
PENDING, UNKNOWN, HELD, SETTLED, FAILED_CONFIRMED, CANCELLED_UNSENT. DISPATCHING
crash becomes UNKNOWN; only authoritative matching settlement reaches SETTLED.
UNKNOWN is lookup-only; no resend absent verified provider replay guarantees.

Collection triggers only on eligible settled external deposit after funding.
Retrieve canonical settled credit and available USD balance in trusted B
scope. Exclude pending, funding, internal transfers, refund/reversal and
unclassified credits. Validate customer mapping, identity, net amount, source
and settlement time. Pre-funding deposits reduce need and are not swept.
Require active B-to-A collection authority, mandate, product, permissions,
purpose and limits. Funding authority on A does not imply collection authority
on B. Zero operating buffer in V1. Serialize unresolved collection per
customer/account. Every send is capped by remaining eligible deposit net,
fresh available USD, collectible debt, authority limit. Provider fee semantics
must be known and included. For known source-debited fee `f`, cap the transfer
at `max(0,min(deposit_remaining-f,available_USD-f,collectible_debt,
authority_remaining-f))`. Reserve transfer plus `f` against deposit, authority
and provider liquidity, but transfer alone against debt. Book `f` as provider
expense, never financing debt. Fixture fee is zero. Unknown or destination-
deducted fees block the first slice. Provider expense is not customer debt.

Waterfall is overdue fee, contracted fee, principal. Persist immutable
`late_fee_cents` and `contract_fee_cents` split summing to COLLECT_FEE amount;
COLLECT_PRINCIPAL stores its exact principal allocation. Fee and principal are
separate operations; wait for fee settlement before principal and refresh
balance. Partial fee repayment does not advance to principal. Requests do not
change debt/capacity. Confirmed settlement reduces assigned bucket once and
releases invoice backing for principal/contract fee reductions in original
due-date/source-ID order. Late fees have no invoice backing. Close only at
zero debt, encumbrance and pending debit, with accrual finalized. Excess
debit is recorded as refund payable, applies only debt owed, and holds for
review; refund execution is gated. Reversal restores debt and backing only
where free, otherwise records backing deficit and holds. Repaid reversal
becomes REOPENED_HELD. Never reallocate used backing or erase settlement.

For funding dispatch recheck, project out only its own NEVER_SENT customer's
principal/fee reservation, facility reserved principal, invoice encumbrances,
RESERVED bridge placeholder and payment duplicate, then evaluate the full
candidate once. Retain all other and settled commitments, including other
facility commitments mapped once into shared reserved principal. Do not mutate
stored reservations. For collection first-send, fetch fresh facts outside SQL,
then lock facility, customer,
bridge, invoices, deposit, mapping, operation and feed. Catch up accrual and
recheck mapping/authority/deposit/balance/debt/waterfall. Project out only the
same queued proven-never-sent operation's own deposit debit, mandate, assigned
debt bucket and local liquidity reservations; exclude it from unresolved
debit check. Keep every other hold, settled consumption and DISPATCHING/
PENDING/UNKNOWN reservation. Never inflate observed provider balance. If
unchanged immutable intent no longer fits, cancel unsent and release exact
holds, never resize. Replan only on changed evidence tuple, at most three per
deposit/day, then hold. Unknown send keeps identity for lookup only.

## HTTP, authentication and idempotency

Routes bind to role scopes. All responses are no-store. Bind loopback/private
by default. HS256 assertions use issuer `flowcard-go`, audience
`flowcard-funding-collection`, registry-bound subjects, jti, integer iat/exp,
maximum TTL 30 seconds, five-second skew. Validate algorithm, exactly one
Authorization header, signature and role/actor binding. Customer subjects are
rejected in SANDBOX pending delegation review. Separate keys, each at least
32 bytes, for coordinator, operator, DEMO synthetic customer, reader and
upstream services. Accept `coordinator-reader` only as read-only under the
coordinator key; `funding-coordinator` may request workflow operations under
that key. `underwriting-reader` is read-only under the reader key. Other DEMO
subjects must resolve to an active imported CUSTOMER or OPERATOR actor after
signature verification; SYSTEM is never an inbound role. SANDBOX customer
subjects remain rejected pending delegation review. Outbound underwriting/
shortfall use their existing exact issuer/audience/subject labels, including
compatibility issuer `flowcard-node-proxy`; no Node runtime dependency follows.
Offer creation remains blocked while the reviewed shared policy package/hash
binding is pending. No saved evaluation authorizes a transfer.

Only typed route fields are accepted. Command body max 16,384 bytes; import
files max 2,000,000 bytes and 10,000 entries/array. Reject unknown/repeated
JSON keys, trailing JSON, noninteger money and overflow. Idempotency key is
1–128 ASCII `[A-Za-z0-9._:-]`. Fingerprint typed fixed-order request struct.
Scope by authenticated principal + mode + HTTP method + concrete resource path
(including resource ID) + key and canonical body hash/version. Changed body is
409 before side effects. Same successful committed body returns HTTP 200 with
identical body/IDs, before source or version checks; original 201/202 remains
audit metadata. Deterministic guard rejection saves and replays its original
422/error. Retriable 503 is never committed.

| Status | Error code | Meaning |
| --- | --- | --- |
| 400 | `INVALID_REQUEST`, `INVALID_QUERY` | Malformed body/query |
| 401/403 | `UNAUTHORIZED`, `FORBIDDEN` | Assertion failure/scope denied |
| 404 | `RESOURCE_NOT_FOUND` | Missing scoped resource |
| 409 | `VERSION_CONFLICT`, `IDEMPOTENCY_CONFLICT` | Stale version/body mismatch |
| 413 | `PAYLOAD_TOO_LARGE` | Bounded request exceeded |
| 503 | `STORAGE_UNAVAILABLE`, `UPSTREAM_UNAVAILABLE` | Transient dependency failure |
| 422 | `TERMS_SUPERSEDED`, `OFFER_EXPIRED`, `ACCEPTANCE_REQUIRED`, `APPROVAL_REQUIRED`, `COLLECTION_AUTHORITY_BLOCKED`, `INPUT_STALE`, `HARD_RULE_FAILED`, `SCARCE_CAPITAL_RANKING_UNSUPPORTED`, `MAPPING_CONFLICT` | Deterministic guard blocked |

Envelope is `{error:{code,message}}` with safe fixed message. New offer/actor
creation returns 201 with resource ID/version/read URL. New queued command
returns 202 `{command_id,resource_id,resource_version,status:QUEUED}`. Replay
is 200. A funding receipt references bridge/reservation/operation IDs and
says RESERVED, never SETTLED. Reconcile's expected version is operation
version; collection review's is bridge version. Neither route accepts
amount/account. A 503 is not a committed result.

| Route | Role | Request |
| --- | --- | --- |
| `POST /api/offers` | Coordinator/operator | `{evaluation_id}` |
| `GET /api/offers/{id}` | Scoped reader | None |
| `POST /api/offers/{id}/acceptances` | Customer/demo actor | `{terms_hash,expected_version}` |
| `POST /api/offers/{id}/approvals` | Operator | `{terms_hash,expected_version,reason_code}` |
| `POST /api/offers/{id}/fund` | Coordinator/operator | `{expected_version}` |
| `GET /api/bridges/{id}` | Scoped reader | None |
| `GET /api/bridges/{id}/collections` | Scoped reader | Cursor only; fixed 25/page |
| `POST /api/bridges/{id}/collection-reviews` | Coordinator/operator | `{expected_version,reason_code}` |
| `POST /api/operations/{id}/reconcile` | Coordinator/operator | `{expected_version,reason_code}` |
| `GET /api/operations/{id}` | Scoped reader | None |
| `GET /api/servicing/changes?after_seq=N` | Coordinator | Ascending committed page |
| `GET /api/servicing/state?customer_id=...` | Underwriting reader | Current versioned read model |

Collection review and operation reconciliation require `expected_version`, a
controlled `reason_code`, and an `Idempotency-Key`. Review targets the bridge
version and queues bounded accrual/delinquency checks; it does not authorize a
debit. Reconciliation targets the operation version and only queues a lookup
for a `HELD` operation. New queued requests return 202; exact successful replay
returns 200. Deterministic 422 refusals are saved for replay, while transient
503 failures have no receipt. Changed requests under the same key return 409.
Forbidden reader writes return JSON 403 before readiness checks. Unknown paths
and unsupported methods return fixed JSON 404 and 405 errors.

The existing worker runs a bounded serial catch-up on startup and once per
minute. Each scan refreshes up to 25 funded bridges for overdue accrual,
delinquency stage, and first-default state, then publishes the corresponding
bridge versions and change-feed events. Customer reminders, card actions, and
notifications remain pending boundaries.

`GET /api/servicing/state?customer_id=...` requires exactly one bounded
customer ID and is available only to the underwriting reader. It returns that
customer's debt, reservations, history completeness and invoices, plus the
configured facility's configuration version, reserve, principal commitments
and other commitments. Funded history includes funded, due, closed and first
default dates; `VOID_UNFUNDED` is excluded. Lists are complete up to 1,000
rows each. An oversized snapshot is refused instead of truncated. The response
includes mode, capture time and committed ledger version. It cannot authorize
funding.

### Read DTOs, cursors and change feed

Bridge DTO has `id`, `mode`, `version`, `as_of`, `customer_id`,
`originating_case_id`, `evaluation_id`, `offer_id`, `terms_hash`,
`principal_owed_cents`, `contract_fee_owed_cents`, `late_fee_owed_cents`,
`due_on`, `funded_at`, `lifecycle`, `delinquency`, `days_past_due`,
`hold_reasons`, `operations`, `allowed_next_actions`. Collections share the
bridge version and include bounded attempt/allocation history and cursor.
Operation DTO includes internal ID, kind/state, reserved amount, last verified
settlement reference, retry count, next check and hold reason. Unknown values
stay null. Never return raw provider account IDs, request bodies or
credentials.

Collection attempts order `(created_at DESC,id DESC)`, fixed 25/page. The
cursor is `base64url(typed JSON).base64url(HMAC-SHA256 signature)`. Its body
contains `mode`, `bridge_id`, `bridge_version`, `created_at`, and `id`. It is
the only collection-list query parameter. Unknown, duplicate, malformed, or
scope-mismatched values return `400 INVALID_QUERY`; a valid cursor with a
changed bridge version returns `409 VERSION_CONFLICT`.

Each collection item contains `id` (attempt ID), `operation_id`, `deposit_id`,
`state`, `created_at`, `deposit_settled_at`, nullable `settled_at`,
`reserved_debit_cents`, `settled_debit_cents`, `provider_fee_cents`,
`planned_allocations`, and `allocations`. Each allocation contains
`allocation_index`, `bucket`, and `amount_cents`. Planned allocations are the
immutable queued split. Actual allocations are debt/refund credits from the
original verified `COLLECTION_SETTLEMENT` ledger entry, or an empty array
before settlement. `settled_at` is that entry's effective time; the deposit's
settlement time is reported separately. Reversals do not rewrite original
settlement history.

Bridge operation summaries include every unresolved money operation and every
FUND operation, then the most recent completed summaries, up to 25 total.
Unresolved operations alone above that bound are refused. The selected
summaries are returned in `created_at,id` ascending order. A settlement
reference is present only for the exact verified FUNDING_SETTLEMENT or
COLLECTION_SETTLEMENT ledger effect. Full collection history uses the cursor
above. Bridge `version` increments atomically whenever operation, authority,
debt, settlement or hold changes affect allowed actions. All expected-version
actions target the stated aggregate.

Per-mode transactional counter serializes the final feed write through
transaction commit, after financial locks. This gives sequence order equal to
commit order; a plain bigserial may skip late commits. Rollback rolls back the
counter. Retain all rows; replay from zero. GET changes takes `after_seq`,
returns ascending 25/page `{mode,items,next_after_seq,has_more}` and leaves
input cursor unchanged on no items. Reject unknown query keys/mode or malformed
cursor. Item includes `sequence`, `resource_type` (`OFFER`, `BRIDGE`,
`OPERATION`), resource ID/version, change type, recorded time and sanitized
summary: customer/internal account alias, nullable case/evaluation/offer refs,
bridge ID/version when created, status/reason, debt, operation progress,
evidence refs and deterministic actions. Bridge version is mandatory whenever
bridge exists, even for OPERATION resource. OFFER before bridge routes to its
exact case and creates no collection obligation. Future retention gaps must
error explicitly and require complete state reconciliation.

Stable obligation ID is bridge ID. Include `source_binding_id`, internal
`account_ref`, USD, last attempt/state, provider observation time, next retry,
and separate acceptance/approval/authority statuses (`VALID`, `MISSING`,
`EXPIRED`, `REVOKED`, `NOT_REQUIRED`, `UNKNOWN`). These describe stored
evidence, not reusable debit authority. `as_of` is server response time;
provider observation time remains separate. DPD begins next UTC date.
Allowed actions are recommendations: `WAIT_FOR_SETTLEMENT`,
`REQUEST_OPERATOR_REVIEW`, `VERIFY_COLLECTION_AUTHORITY`,
`RECONCILE_OPERATION`, `WAIT_FOR_ELIGIBLE_DEPOSIT`, `MONITOR_REPAYMENT`,
`WAIT_FOR_ACCRUAL`. They grant no execution permission.

## Readiness, configuration and fixture bounds

| Variable | Contract |
| --- | --- |
| `DATABASE_URL` | Restricted servicing runtime connection |
| `SERVICING_MODE` | `DEMO` or `SANDBOX` |
| `SERVICING_HTTP_ADDR` | Default `127.0.0.1:8085`; fail if occupied |
| `SERVICING_FACILITY_ID` | Single configured USD facility/A binding |
| `SERVICING_SOURCE_BINDING_ID` | Stable nonsecret installation/feed identity |
| `SERVICING_COORDINATOR_SECRET` | Coordinator assertion key, >=32 bytes |
| `SERVICING_OPERATOR_SECRET` | Separate operator assertion key |
| `SERVICING_CUSTOMER_SECRET` | DEMO-only synthetic customer assertion key |
| `SERVICING_READER_SECRET` | Separate underwriting read-model key |
| `UNDERWRITING_BASE_URL`, `UNDERWRITING_SERVICE_SECRET` | Reviewed context/evaluation boundary |
| `SHORTFALL_BASE_URL`, `SHORTFALL_SERVICE_SECRET` | Saved shortfall read boundary |
| `SERVICING_PROVIDER_MODE` | DEMO `MOCK_HTTP`; SANDBOX `AIRWALLEX_SANDBOX` |
| `SERVICING_PROVIDER_BASE_URL` | Loopback-only mock URL; sandbox host fixed |
| `SERVICING_LIVE_DISPATCH_ENABLED` | Default false; never enables SANDBOX create |
| `AIRWALLEX_CLIENT_ID`, `AIRWALLEX_API_KEY` | Server-only in SANDBOX; DEMO uses fixed synthetic values |
| `AIRWALLEX_WEBHOOK_SECRET` | Optional private signing key; absent disables receipt |

Do not add root `.env` variables. DEMO+MOCK_HTTP fixture create works when
local prerequisites pass, even with live dispatch false, and never accesses a
real host. DEMO accepts only the fixed synthetic mock credentials. SANDBOX
creates remain blocked even when the live flag is true, though reads and
reconciliation can remain available. Readiness distinguishes `FIXTURE_ONLY`
from provider acceptance and reports `mode`, `runtime_ready`,
funding/collection enabled flags, blockers,
supported regimes `[ABUNDANT,NORMAL]` and `scarce_execution_supported:false`.
Liveness is separate from readiness. A valid config never means live
acceptance verified. Readiness also reports the connected `database_name`;
the prepared runner refuses a mismatch. Expected funding/underwriting database
names use safe owner prefixes and are recorded in the nonsecret manifest.

Pinned keys follow the policy runbook: `go.mod` reuses Go 1.27.3 and pgx
v5.12.0 with local public-policy replacement `../underwriting-engine`, no root
go.work or Node build. The public module and pricing accessor are pinned in the
private funding underwriting adapter. Other live provider and settlement
contracts remain independent acceptance gates.

First slice requires known-empty reconciled DEMO debt/history books and
ABUNDANT/NORMAL funding. TIGHT/CRITICAL returns
`SCARCE_CAPITAL_RANKING_UNSUPPORTED` before reservation. Task 12A implements
`cmd/demo prepare --as-of now --out PATH --database-name NAME
--underwriting-database-name NAME` and `cmd/demo serve-provider --manifest
PATH`. `prepare` can take `--underwriting-manifest PATH` to bind a fresh owned
known-empty upstream bundle. Task 12B adds
`cmd/demo run --manifest PATH --api-url URL`, which uses authenticated actor
API routes and the existing worker/HTTP adapter without DDL or direct ledger
insertion. An optional evaluation ID and host-supplied coordinator case ID bind
actual saved records. Preparation accepts safe task-owned database names and
can bind to an owner-prepared known-empty Underwriting manifest, preserving
its run ID, anchor and customer/payment identities without editing captured
inputs or copying decisions. No reset, stale-time bypass or real credentials.
The provider double models login, transfer/list/retrieve, balance, payment and
deposit shapes; its controls change mock provider facts only. Live provider
access is never fallback.

Named live gates include canonical fresh payment/balance facts and race
semantics; reviewed policy/customer-context/opening state; customer identity,
disclosures/mandate and operator permissions; CA schemas, scopes, permissions,
idempotency, retrieval, settlement, fees, return/reversal/refund; webhook
ownership/subscriptions/retrieval; and card safeguards. No provider
simulations, live funds, models, AWS, UI, public auth, notifications, card
control/refund execution, or deployment are in this task.

## See also

- [Funding and collection design](/docs/designs/funding-and-collection.md)
- [Funding and collection implementation plan](/docs/plans/20261114-funding-and-collection.md)
- [Underwriting design](/docs/designs/underwriting-engine.md)
- [Underwriting contract](/docs/api/underwriting-contract.md)
- [Coordinator design](/docs/designs/go-coordinator.md)
- [Shortfall design](/docs/designs/shortfall-detection.md)
- [Shortfall local runbook](/docs/demo/shortfall-detection-local.md)
