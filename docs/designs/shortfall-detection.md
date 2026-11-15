# Shortfall detection service

Finalized hackathon design, scoped for a short-lived demo installation.
Implementation is specified in
[the completed execution plan](/docs/plans/completed/20261113-shortfall-detection.md).
This document describes intended behavior, not implemented capabilities.

[TOC]

## Scope

Create `services/shortfall-detection`. The service records confirmed card
payment shortfalls and makes them available for later underwriting. It does
not evaluate credit eligibility, create offers, approve funding, transfer
money, retry cards, or collect repayments.

V1 supports USD wallet-funded purchase authorizations only. Other currencies,
FX, incremental authorizations, verification, clearing, reversals, refunds,
credit-funded cards, and card-control declines do not create actionable
shortfalls. Preserve the received event and record why it was excluded or
could not be resolved.

## Provider contract and reuse

Port the tested Node raw-body signature verifier and receiver behavior:

- HMAC-SHA256 of x-timestamp followed by the original body, hex signature,
  constant-time comparison, and no JSON reserialization before verification.
- Check only timestamps ahead of server UTC time. Parse x-timestamp as Unix
  milliseconds without changing the original header string used for HMAC.
  Verify HMAC first, then reject only when the delivery timestamp exceeds
  server time by more than 300,000 milliseconds (five minutes). Exactly five
  minutes ahead is valid. Older timestamps have no age cutoff and are not
  rejected merely because they precede server time.
  Missing, malformed, overflowing, or excessively future-dated timestamps
  return 401 invalid_signature with no inbox write. Use an injectable clock
  for boundary checks. This rule applies to x-timestamp, not event.created_at.
  Because old signed deliveries remain acceptable, durable event-ID
  deduplication is required; timestamp checking provides no replay-age limit.
- Request body limit: 10 MB (10,000,000 bytes), inclusive. Return 413 when
  the actual streamed body exceeds this limit; do not trust Content-Length
  alone. Enforce before unbounded buffering, including chunked requests.
- Return 401 for invalid signature, 400 for invalid envelope,
  500 when durable inbox insertion fails, and 200 after successful insertion.
- Duplicate deliveries receive success after the existing durable row is
  identified. Worker processing does not block webhook acknowledgement.

Use a dedicated externally routed path `/webhooks/airwallex/shortfalls` that
terminates at this Go service. Node retains its other webhook responsibilities.
At cutover, the Go service owns decline detection. Do not forward the same
decline into a second Node detector or retain scenario-triggered automatic
funding as an implicit side effect of this migration.

Use the existing Node subscription/version and working webhook JSON as the
Go compatibility baseline. The Node event list includes
`issuing.card_transaction.declined`; do not require a webhook-version upgrade
or a new event subscription for the first port. The receiver's generic
JSON acceptance is not proof that it already extracts decline facts.
The provider also documents `issuing.card_transaction_event.failed`; support
it only through a separately verified parser if needed. Both notification
families must converge on one canonical attempt. The webhook name alone does
not prove INSUFFICIENT_FUNDS.

Retrieve authoritative transaction-event facts when missing from the delivery.
Do not reuse the issuing simulation response as the webhook schema. The
reference exposes `GET /api/v1/issuing/card_transaction_events/{id}` with
account-scoped reads. The guide also names `/issuing/transaction_events`;
resolve this documentation/version discrepancy against the pinned issuing
API contract before implementing the provider client.

Required normalized facts: account ID, card ID, transaction ID, transaction
event ID, lifecycle ID when available, event type/subtype, purchase category,
process result, failure reason, transaction/billing amounts and currencies,
and provider event time. Unknown mandatory facts remain unresolved. Record
field provenance; envelope IDs and transaction-event IDs are different fields.

### Delivery envelope versus transaction facts

The public issuing webhook page lists modern lifecycle events, but its shown
payload examples explicitly cover legacy `issuing.transaction.failed`,
`issuing.transaction.succeeded`, and reissue notifications. Their envelope
shows `id`, `name`, `account_id`, `created_at`, `source_id`, `version`, and
`data`. Treat these examples as evidence for that version/family only.
Do not declare `source_id` or `data.id` to be an authorization-event ID across
all event names, or alias legacy `transaction_id` to a modern event ID.

Receiver requirements:

- Require an object envelope with nonempty string `id` and `name`.
- Preserve optional envelope fields and unknown fields in the protected raw
  payload. Missing account/transaction facts are worker-resolution failures,
  not invented values or a reason to lose a verified delivery.
- Determine environment and subscription/secret identity from server routing
  and configuration, not a client-supplied environment field.
- Preserve `version` as evidence. The parser is selected by a verified
  subscription version and event family. An unsupported version is held as
  UNRESOLVED with reason UNSUPPORTED_WEBHOOK_VERSION, never parsed through
  a best-effort legacy fallback.

The repository's issuing client currently uses `2026-08-21`. Use this as the
candidate for webhook/retrieval compatibility verification, not proof of the
subscription's actual version. Pin the tested version explicitly in the
event contract before live enablement. Never infer it from the date today.

### Canonical authorization-event resolution

The current Card Transaction Events reference documents these retrieval
fields. This table describes the GET response, not assumed webhook JSON paths.

| GET response field | Normalized meaning |
|---|---|
| `id` | Canonical transaction-event ID identifying one network event |
| `card_transaction_id` | Parent card transaction |
| `lifecycle_id` | Correlation across related payment activity |
| `card_id` | Card to match against trusted account/customer mapping |
| `type`, `subtype`, `transaction_category` | Event classification |
| `process_result`, `failure_reason` | Actual outcome and decline reason |
| `transaction_amount`, `transaction_currency` | Attempted transaction value |
| `billing_amount`, `billing_currency` | Value in the card's billing currency |
| `created_at`, `transacted_at`, `updated_at` | Distinct provider timestamps |

Worker procedure:

1. Select the version-specific parser by verified subscription and event name.
   For `issuing.card_transaction_event.failed`, extract only the documented
   transaction-event reference; exact envelope/data path remains pending
   a version-matched sample. Do not use envelope `id` as that reference.
2. Resolve the connected-account scope through trusted mapping evidence.
   Retrieve the referenced event through the documented event-by-ID endpoint
   with `x-on-behalf-of` for that account. Verify the returned ID and card
   ownership.
3. If `issuing.card_transaction.declined` supplies only a transaction ID,
   list events filtered by `card_transaction_id` under the same account.
   Follow all `page_after` cursors using the documented `page` parameter;
   do not select only the first response page or guess from a lifecycle total.
4. Require an unambiguous event with type AUTHORIZATION, subtype AUTHORIZATION,
   category PURCHASE, process_result DECLINED and failure_reason
   INSUFFICIENT_FUNDS. A known different reason/classification is ignored;
   missing facts or multiple plausible events are UNRESOLVED. Do not choose
   the latest candidate merely because its timestamp is greatest.
5. Persist the returned event `id` as canonical identity, retaining parent
   transaction/lifecycle IDs separately. Resolve both notification types to
   this same identity before creating a detection or reading the balance.

The lookup API documents `issuing.transaction:read` and account scoping.
Validate actual key permissions and selected API-version support separately;
public documentation does not establish our account's access.

### Duplicate and conflict rules

- Inbox identity: environment + configured webhook source + envelope `id`.
  Same identity and payload hash reuses the existing item and returns 200.
  A changed hash preserves the original plus conflict evidence, acknowledges
  only after durable conflict recording, and holds processing for review.
- Detection identity: environment + connected-account ID + canonical event
  `id`. Enforce this with a database uniqueness constraint.
- Different webhook IDs/types resolving to the same canonical event attach
  evidence to one detection, without another balance-triggered calculation
  merely because a duplicate arrived.
- Distinct canonical authorization events remain distinct attempts even under
  the same transaction/lifecycle. Never deduplicate by lifecycle alone, network
  reference, merchant, amount, or approximate timestamp.
- Retry an unresolved lookup using its original references. Even after identity
  resolution, unresolved work stays on the inbox until its observation is final.
  Reuse a completed canonical detection rather than calculating another one.
- Redelivery of a completed detection returns its stored observation; an
  intentional later balance recalculation is a separate versioned operation,
  not an implicit effect of webhook duplication.

Examples: webhook W1 delivered twice produces one inbox item; W1 and W2 both
resolving to authorization E1 produce one detection; authorization E2 in E1's
lifecycle produces its own detection. An event with only an unverified source
reference remains UNRESOLVED and produces no actionable shortfall.

## Mapping ownership

Reuse existing active `customer_roster` and `issuing_card_bindings` mappings.
Do not create another mutable mapping registry or use company names/emails.
The adapter must resolve exactly one active customer/account/card combination
and reject mismatches, missing mappings, or ambiguity.

For the initial shared-database deployment, use a narrow read-only mapping
adapter with SELECT grants on the existing mapping tables. The service writes
only its own schema. Keep SQL behind a Go interface so a future mapping API
can replace this dependency without changing detection logic. Account/card
IDs supplied by browser clients are never trusted mapping authority.

Ramil accepted this mapping behavior: join the active card binding's
`customer_id` to an active roster entry, and require its `airwallex_account_id`
to equal the roster's `airwallex_connected_account_id` and the verified
provider account scope. Missing/inactive mappings produce MAPPING_UNAVAILABLE;
conflicting mappings produce MAPPING_CONFLICT. Database availability failures
retry. Do not auto-enroll onboarding companies or create mappings here.

## Calculation

The amount means money needed now to cover this declined payment, not a
reconstruction of the historical wallet balance when the decline happened.

1. Confirm an initial purchase authorization was declined specifically for
   INSUFFICIENT_FUNDS using verified provider facts.
2. Confirm transaction and billing currency are USD and the mapped card's
   funding configuration fits V1's USD wallet assumptions.
3. Read the mapped account's current available USD cash balance using the
   provider's account scope. Missing balance is not zero.
4. Decode decimal amounts without float rounding; convert validated USD
   amounts to integer cents. Reject excess precision, overflow, malformed,
   negative balance, or nonpositive attempted amount as unresolved evidence.
5. Compute `max(attempted_amount_cents - available_balance_cents, 0)`.
6. Persist source facts, response observation time, and the result.

Positive result: SHORTFALL_DETECTED. Zero: NO_CURRENT_SHORTFALL. Missing or
ambiguous evidence: UNRESOLVED workflow condition with a reason and retry
metadata on the inbox, not an immutable detection. Other confirmed decline
reasons are ignored with a reason; they are not balance shortfalls.
Transient provider errors retry with bounded backoff. Invalid/missing mappings
remain held until corrected, without repeatedly querying an unrelated account.

A detection is a timestamped observation, not a reservation or promise to fund.
Later underwriting/funding must recheck current state. Delayed or out-of-order
notifications cannot overwrite a newer provider outcome with an older decline.
Check for a later successful/reversed transaction outcome before recording a
detection when the verified lookup exposes that outcome;
unresolved ordering is held. Automatic later reconciliation is deferred.

### Balance lookup contract

Reuse the existing Node request:
`GET /api/v1/balances/current?account_type=cash`, using the resolved
connected-account ID in `x-on-behalf-of` and server-side provider credentials.
Read the USD cash entry's `available_amount`. Do not use `total_amount`,
`pending_amount`, `reserved_amount`, yield, or credit balances. Do not subtract
reserved funds again from an already available balance.

The provider documents account type/currency/available amount in each entry.
Require exactly one applicable USD cash balance under the versioned adapter;
duplicates, absent USD balance, or invalid amounts remain UNRESOLVED. Preserve
any verified version-specific missing-account_type compatibility explicitly;
do not guess from whichever entry appears first.

Persist `attempted_amount_cents`, `available_balance_cents`, `shortfall_cents`,
`currency: USD`, `balance_requested_at`, `balance_observed_at`, and the
canonical event reference. The two balance times are service UTC times around
the request; do not label them provider-reported balance timestamps.
Use exact decimal decoding/conversion and validate both amounts before
subtraction. A provider timeout is not an available balance of zero.

Acceptance examples for an eligible declined USD purchase:

| Attempt | Available cash | Outcome | Shortfall |
|---|---|---|---|
| 100.00 | 30.00 | SHORTFALL_DETECTED | 7,000 cents |
| 100.00 | 100.00 | NO_CURRENT_SHORTFALL | 0 cents |
| 100.00 | 120.00 | NO_CURRENT_SHORTFALL | 0 cents |
| 100.00 | Explicit 0.00 | SHORTFALL_DETECTED | 10,000 cents |
| 100.00 | Missing or request failed | UNRESOLVED (inbox) | Absent, not zero |

These results describe the balance at processing time, even if a deposit
arrived after the original decline. They do not authorize funding.

## Persistence and processing

Use the existing PostgreSQL instance with a separate `shortfall` schema,
numbered migrations, a dedicated runtime role, and no runtime DDL. Sandbox and
production deployments/credentials/data remain isolated.

Tables:

- `webhook_inbox`: environment/source scope, provider webhook ID/type,
  protected raw payload, payload hash, received time, processing state,
  attempts, next attempt, last sanitized error, lease token/expiry.
- `detections`: internal detection ID; environment/account/card/customer;
  canonical transaction-event, transaction and lifecycle references; provider
  facts; attempt/balance/shortfall cents; currency; observation times; final
  outcome, reason and result version. Keep links to supporting inbox events.

Use a small `inbox_detections` link table for multiple deliveries describing
one detection. Conflicting bytes are stored separately in `inbox_conflicts`.
No outbox or event publisher is needed until a consumer exists.

Deduplicate inbox by environment/source/webhook ID. Identical IDs with changed
payload hashes are recorded as conflicts and never overwrite verified bytes.
Deduplicate detections by environment/account/canonical authorization-event ID,
not lifecycle alone. Different webhook types describing that attempt attach
to the existing detection. If the canonical identity is unavailable, retain
the inbox item unresolved; do not guess a key from amount/merchant/time.

Lead-selected v1 simplification during group E: unresolved evidence, including
resolved identity with missing facts/balance, remains only on HELD or RETRY_WAIT
inbox rows. Do not insert an immutable UNRESOLVED detection that would prevent
recovery after correction. Final detections are SHORTFALL_DETECTED,
NO_CURRENT_SHORTFALL, or confirmed IGNORED outcomes. An ignore without canonical
identity is recorded only on the processed inbox. The enum/schema may retain
UNRESOLVED for compatibility, but runtime does not insert or complete it and
the observation read API does not expose unresolved workflow rows.

Protected original bytes remain on the inbox. Retries repeat authoritative
reads from those original references; partial lookup facts are not persisted
as an immutable observation. Replay after correction can resolve and calculate
unfinished work, while completed observations remain unchanged.

Workers claim jobs in short PostgreSQL transactions with expiring leases,
commit before provider calls, and fence final writes using the lease token.
Expired workers cannot overwrite a replacement worker's result. Commit the
detection result, evidence link, and inbox completion atomically. A restart
resumes
pending work. No new message broker is required.

### Worker processing and recovery defaults

Run one worker process with one in-flight job. Poll every second. Each job
has a 45-second overall deadline, a 60-second inbox lease, and 10-second
provider-request timeouts. Within the job deadline, reserve the last five
seconds for durable disposition after at most 40 seconds of resolution work.
Do not hold a transaction during network calls.
No lease heartbeat, parallel processing, or detection-level lease is needed.

Claim eligible jobs with `FOR UPDATE SKIP LOCKED` and a fresh lease token.
At commit, require that token and an unexpired lease. Expired work is reclaimed
on the next poll. Stop on shutdown and let unfinished leases expire.
A unique canonical detection key prevents duplicate persisted results even if
a worker is accidentally duplicated; the first committed observation wins.
Completed duplicates attach evidence without another calculation. Concurrent
work may repeat read-only provider calls, but cannot create two detections.

Retry network failures, timeouts, 429, 5xx, and lookup 404 up to eight failed
attempts. Delays are 5, 10, 20, 40, 80, 160, and 300 seconds with 20 percent
jitter. Honor a valid `Retry-After` as a minimum. Store the next attempt time.
An authentication refresh is allowed once within an attempt. Repeated 401,
403, invalid facts, or missing/conflicting mapping holds the item with its
specific reason. Exhausted transient retries use HELD and controlled
`last_reason: RETRIES_EXHAUSTED:<original-sanitized-code>` so the original cause
is retained without raw provider/DB error text.

States are `RECEIVED`, `PROCESSING`, `RETRY_WAIT`, `PROCESSED`, and `HELD`.
Track failed attempts separately from claims; worker crashes do not silently
consume provider-failure retries. All errors are sanitized and stored.
Database failure never counts as successful completion. A crash before commit
can repeat GETs; after commit the stored result is reused.

Held-item replay uses a local authenticated-to-database CLI, not an admin UI.
It resets only HELD items to RECEIVED, keeps payload/identity/history, and
refuses completed or conflicted items. No automatic balance recalculation.
Preserve previous reason/failure count in replay audit. Exhausted failures use
HELD; v1 has no separate FAILED state. Replay retries corrected unresolved work
without reopening a completed canonical observation.

### Observation lifecycle and demo boundaries

Store an immutable observation, not a live funding request. Do not add success
or reversal subscriptions, background reconciliation, resolution states, or
balance polling for this demo. A later payment attempt is another canonical
event. A deposit alone does not alter an existing observation.

When the existing transaction lookup exposes a verified later terminal outcome
for the same payment, record `IGNORED: PAYMENT_NO_LONGER_ACTIONABLE` before
reading the balance. Never correlate independent attempts by amount or merchant.
If ordering cannot be established, hold as `OUTCOME_UNRESOLVED`. Do not make a
separate provider lookup contract mandatory merely to implement reconciliation.
The read API exposes observation time and `requires_recheck: true`; later
underwriting must recheck payment state and available balance before action.

## Go boundaries and read API

- `WebhookReceiver`: HTTP limits, signature verification, envelope validation
  and durable ingestion.
- `DeclineResolver`: provider payload normalization, authoritative lookup and
  canonical attempt identity.
- `MappingResolver`: read-only trusted customer/account/card resolution.
- `BalanceReader`: account-scoped USD available balance and observation time.
- `Detector`: pure classification and integer-cent calculation.
- `Repository`: inbox claims and atomic detection/evidence persistence.

API and worker are separate commands sharing these internal packages, following
the existing Go service's packaging conventions. Health/readiness endpoints
must distinguish process liveness from unavailable database/configuration.
For the currently unsupported live parser, `/readyz` returns 503 with separate
`service_ready`, DB/configuration flags, and `live_detection_ready: false`.
`/healthz` remains 200 independently. Local fixture success cannot turn live
readiness on, and does not imply provider permissions or subscription support.

Provide authenticated internal `GET /api/shortfalls` (25 records, opaque
cursor; optional customer/outcome filters) and `GET /api/shortfalls/{id}`.
Responses expose safe normalized facts, amount semantics and evidence times,
not raw webhook bodies, credentials, or payment-card secrets. Reuse the existing
internal service assertion convention, with service-specific authorization.
Frontend exposure and underwriting subscription are separate integration tasks.

## Verification and outstanding evidence

Use existing Go tooling and isolated PostgreSQL tests. Keep two or three
focused cases per critical feature, with a final combined run after coding.
Core behaviors: raw signature/durable dedup; insufficient-funds calculation
and zero/unresolved alternatives; duplicate notifications and fenced retries.
Port Node fixtures only where they actually represent the webhook contract.

Task A of the execution plan must verify and record:

- Exact failed-event webhook envelope/data shape for the selected version,
  including how it identifies the canonical authorization event.
- Version-specific retrieval endpoint, fields and permissions; reconcile the
  transaction-events endpoint naming discrepancy noted above.
- Safe USD available-balance selection for the configured card funding model.
- A sanitized recorded decline delivery and its authoritative retrieval,
  plus duplicate behavior. Automatic later resolution is deferred.

Public reference documentation is evidence of documented shape, not proof that
our sandbox credentials, subscriptions, or card program support it. The scoped
implementation plan is approved for local work with review checkpoints. Live
provider mutations, deployment, and Node cutover remain separately authorized
operations.

## Sources

- [FlowCard requirements](/docs/requirements/README.md)
- Existing Node receiver: `server/src/http/routes/airwallex-webhook.ts`
- Existing signature verifier: `server/src/webhooks/verify.ts`
- Historical mapping resolver: `server/src/policy/kit5-funding.ts`, removed
  during [Node financial retirement](/docs/demo/node-funding-retirement.md).
- [Airwallex issuing webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/issuing)
- [Card transaction events API](https://www.airwallex.com/docs/api/issuing/card_transaction_events)
- [Retrieve lifecycle and transaction data](https://www.airwallex.com/docs/issuing/transactions/transaction-lifecycle/retrieve-lifecycle-and-transaction-data)
- [Transaction failure reasons](https://www.airwallex.com/docs/issuing/transactions/transaction-failure-reasons)
- [Current balances API](https://www.airwallex.com/docs/api/core_resources/balances/current)
