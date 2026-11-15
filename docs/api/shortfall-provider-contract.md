# Shortfall provider contract

Task A evidence inventory for the shortfall service, inspected on 2026-11-14
at base `dab4c74d1f92c535b6cd23d3278215d212e5b989`. This separates reusable
Node behavior, current public API documentation, and synthetic test inputs.
No live provider compatibility has been demonstrated.

[TOC]

## Compatibility status

The configured Node event list includes `issuing.card_transaction.declined`.
Its actual subscription version and a recorded decline delivery are absent
from the inspected repository evidence. `server/src/config.ts` pins issuing
requests to `2026-08-21`; this does not establish the webhook version.
Keep the existing subscription unchanged. There is currently no verified
modern decline delivery-to-canonical-event parser to port.

Both `issuing.card_transaction.declined` and
`issuing.card_transaction_event.failed` remain unsupported for live fact
extraction until version-matched delivery evidence exists. Preserve their
verified bytes in the inbox and hold with `UNSUPPORTED_WEBHOOK_VERSION` for
unverified versions, or an unresolved reference reason when a verified
version lacks a usable reference. Do not fall back to legacy JSON paths.

Task A's acceptance criterion of a verified supported-delivery mapping
cannot pass with the retained evidence. This is a live-readiness gap;
fixture-based persistence, calculation, worker, and read-API work can proceed.

## Reusable Node behavior and explicit changes

Historical Node source inspected before retirement (the financial worker and
policy mapping resolver were later removed; see the
[retirement inventory](/docs/demo/node-funding-retirement.md)):

- `server/src/http/routes/airwallex-webhook.ts`
- `server/src/webhooks/verify.ts`, `inbox.ts`, and `process.ts`
- `server/src/airwallex/client.ts` and `issuing.ts`
- `server/src/policy/kit5-funding.ts`
- `tests/server/webhooks.test.ts`, `airwallex.test.ts`, and `issuing.test.ts`
- Mapping migrations `002_customer_roster.sql` and
  `008_remote_authorization.sql`

Node verifies HMAC-SHA256 over the original timestamp string concatenated
with original body bytes, decodes the hex signature, and compares in constant
time. It validates nonempty string envelope `id` and `name`, inserts before
acknowledgement, acknowledges duplicate IDs, and processes asynchronously.
Its retained webhook tests use synthetic transfers and deposits. They do not
establish issuing decline JSON paths or canonical event resolution.

The Go design deliberately strengthens the receiver contract:

- Body limit changes from Node's 1,048,576 to 10,000,000 inclusive bytes.
- Reject repeated timestamp/signature headers and require an object envelope.
- After HMAC verification, validate Unix-millisecond timestamp range and the
  five-minute future bound; Node currently checks digits without this bound.
- Inbox uniqueness includes configured environment/source. Changed payload
  hashes require durable conflict evidence; Node only ignores duplicate IDs.

Return 401 `invalid_signature`, 400 `invalid_event`, 413
`payload_too_large`, 500 `inbox_unavailable`, or 200 `{"received":true}`.
Environment, source, and secret identity come from server configuration.
Old signed timestamps remain valid, requiring durable deduplication.

The Node processor handles transfers, charges, and deposits. Generic object
extraction in it is not a decline parser. The issuing simulation normalizer
accepts simulation-specific aliases, including `id`; these aliases must not
be applied to webhooks. No Node funding path is invoked by this contract.

## Envelope and reference provenance

Only `$.id` and `$.name` have a reusable receiver-level requirement. Preserve
all other fields, including `$.version`, without assigning unverified fact
semantics. The public issuing webhook examples show `$.account_id`,
`$.source_id`, `$.created_at`, and `$.data` for legacy
`issuing.transaction.*` events with an example `2019-09-09` version.
Those examples do not establish the modern decline reference path.

| Fact | Modern delivery path | Authoritative retrieval path |
| --- | --- | --- |
| Webhook identity | `$.id` | Not a transaction-event identity |
| Event family | `$.name` | Select verified version/family adapter |
| Subscription version | `$.version`, preserve evidence | Verify against server subscription |
| Connected account | Pending version evidence | Trusted mapping plus verified scope |
| Canonical event | Pending version evidence | `$.id` on event GET, `$.items[*].id` on list |
| Parent transaction | Pending version evidence | `$.card_transaction_id` |
| Lifecycle | Pending version evidence | `$.lifecycle_id`, optional |
| Card | Pending version evidence | `$.card_id` |
| Classification | Pending version evidence | `$.type`, `$.subtype`, `$.transaction_category` |
| Outcome | Pending version evidence | `$.process_result`, `$.failure_reason` |
| Attempt amount | Pending version evidence | `$.transaction_amount`, `$.transaction_currency` |
| Billing amount | Pending version evidence | `$.billing_amount`, `$.billing_currency` |
| Provider time | Pending version evidence | `$.created_at`, `$.transacted_at`, `$.updated_at` |

The event GET paths above become `$.items[*].FIELD` for list responses.
Never substitute webhook `id`, legacy `transaction_id`, or lifecycle ID for
the canonical authorization event ID.

## Retrieval and authentication

Use the API reference's `/api/v1/issuing/card_transaction_events` resource.
The lifecycle guide's `/api/v1/issuing/transaction_events` spelling conflicts
with the API reference. Resolve the implementation choice in favor of the
dedicated API reference; do not implement fallback requests to both paths.
Availability under `2026-08-21` still requires version-matched verification.

- Event by verified reference:
  `GET /api/v1/issuing/card_transaction_events/{id}`. The API reference's
  response field schema is a single event object. The connected documentation
  search returned a contradictory paginated example under this endpoint;
  prefer the endpoint schema, retaining this discrepancy as an evidence gap.
- Parent transaction resolution:
  `GET /api/v1/issuing/card_transaction_events?card_transaction_id=ID`.
  Read `items`; follow every nonempty `page_after` as the next request's
  `page`, retaining the original filter. Maximum documented `page_size`: 100.
- Resolve exactly one eligible initial authorization. Multiple plausible
  events are unresolved. Inspect existing lookup evidence for newer terminal
  outcomes before balance access; ambiguous ordering is held.
- Obtain a bearer token through `POST /api/v1/authentication/login`, with
  server-side `x-client-id` and `x-api-key`, matching Node. Use
  `Authorization: Bearer TOKEN` for reads and `x-on-behalf-of` with the trusted
  connected-account scope. Never enumerate arbitrary accounts to find a match.
- Issuing permission: Issuing > Authorizations and Transactions > Read,
  or OAuth `issuing.transaction:read`. Actual key access is unverified.

## Mapping and wallet evidence

Join active `issuing_card_bindings.customer_id` to active
`customer_roster.customer_id`. Require binding `airwallex_account_id`, roster
`airwallex_connected_account_id`, and verified delivery/retrieval scope to
agree. Missing/inactive mappings are `MAPPING_UNAVAILABLE`; disagreement or
ambiguity is `MAPPING_CONFLICT`. Database failures retry. Grant SELECT only.

The existing mapping schema contains no wallet/credit funding configuration.
An active card binding alone does not prove V1 USD wallet funding. Recorded
card-program/configuration evidence is an additional live-readiness gap.
Synthetic fixture funding evidence belongs to the test harness only.

## Amount and balance contract

For an initial PURCHASE AUTHORIZATION declined for INSUFFICIENT_FUNDS,
require both transaction and billing currencies USD. The API fields describe
currency-unit decimal amounts. The modern reference example uses positive
purchase amounts; legacy webhook examples use negative outgoing amounts.
The inspected documentation does not pin the sign for this subscription's
declined modern events. Never apply an unconditional absolute value. Keep
live sign normalization unsupported until that version's evidence is pinned.
Synthetic event fixtures explicitly use positive USD units.

Reuse Node's request:
`GET /api/v1/balances/current?account_type=cash`, account scoped through
`x-on-behalf-of`. Response is an array. Require exactly one entry with
`currency == "USD"` and `account_type == "cash"`; use `available_amount`.
Node's first-match selection and missing-account_type fallback are not
carried forward without version-specific evidence. Retained balance tests
verify explicit cash entries, not that fallback.

Balance permission: Core Resources > Balances > Read, or `balance:read`.
Decode JSON numeric decimal tokens exactly, require whole cents in int64
range, a positive attempt and nonnegative balance. String amount support is
not established by these references. Missing/duplicate USD cash entries,
invalid amounts, and timeouts are unresolved, never zero. Do not subtract
reserved funds from an already available amount. Compute
`max(attempted_cents - available_cents, 0)`. Service UTC times around the
request are `balance_requested_at` and `balance_observed_at`.

## Synthetic fixture boundary

Files under `services/shortfall-detection/internal/provider/testdata/` contain
synthetic, nonsecret data only. No file is a recorded provider delivery.
`manifest.json` links a minimal receiver envelope to a separate mock resolver
input and documented-shape event/balance responses. The reference is supplied
by a test double outside webhook JSON; this avoids inventing `data.id` or
`source_id` semantics. It is not an enabled production parser.

The explicit fixture chain is webhook `fixture-webhook-1`, mock trusted
account/card/parent reference, paginated lookup, canonical event
`00000000-0000-4000-8000-000000000003`, USD 9,000 attempt, USD 2,000 cash,
and expected 700,000-cent observation. Duplicate delivery reuses the same
webhook identity; a second notification resolving to this event must reuse
the stored detection without another balance calculation.

The lead approved reference extraction as an injected test/local dependency.
`provider.UnsupportedReferences` holds modern live declines. Reference
extraction supplies no amount or classification. A separate `EvidenceReader`
supplies synthetic card funding and amount-convention facts from the manifest;
neither dependency is selected through runtime headers, request parameters,
or environment bypass flags. Real mapping and provider reads remain required.

The D classifier conservatively holds differing USD transaction/billing
amounts as `BILLING_AMOUNT_INVALID` until those semantics are established.
It supports exact numeric tokens (including decimal exponents), rejecting
effective fractional precision beyond two places, even trailing zeroes.
The client reuses login tokens, applies ten-second request deadlines, and
shares one 401 refresh budget across all reads in a worker attempt. Provider
error bodies and credentials are never included in returned errors.

## Unresolved workflow and immutable results

The lead selected a v1 lifecycle simplification during E: unresolved evidence
and transient errors remain on HELD/RETRY_WAIT inbox records, even when the
canonical event is known. They are not immutable UNRESOLVED detections or list
API rows. Protected original payloads and sanitized reasons remain durable;
retries repeat authoritative reads using the original references. No separate
partial provider-response snapshot is introduced.

Only final positive/zero observations or confirmed canonical IGNORED outcomes
create detections. Unrelated/noncanonical ignores complete only their inbox
record. An existing final canonical observation is reused before any balance
request. Replay after correction can calculate unfinished work, and cannot
reopen conflicted, leased, linked, or completed inbox records. The existing
UNRESOLVED enum/schema allowance is not used to finalize runtime work.

Exhausted transient work is HELD with
`last_reason: RETRIES_EXHAUSTED:<original-sanitized-code>`. The suffix is the
controlled failure code, never raw provider/DB error text. Replay preserves
this exact prior reason in its audit. The planned G worker cases cover it.

The internal read API authorizes HS256 assertions with issuer
`flowcard-node-proxy`, audience `flowcard-shortfall-service`, and subject
`shortfall-reader`. It exposes only final observations in configured environment
scope. `/readyz` returns 503 while the live parser is unsupported, even when
local DB/configuration flags are ready. `/healthz` is independent liveness.

## Live enablement evidence still required

- Recorded sanitized decline delivery, actual subscription version/source,
  and duplicate delivery, preserving field paths and ID relationships.
- Matching authoritative event retrieval, account/card ownership, endpoint
  version support, and actual read-key permissions.
- Verified outgoing sign/units, USD wallet funding configuration, and balance
  shape (including any proposed missing-account_type compatibility).
- Verified canonical mapping for each enabled notification family. Keep the
  second family disabled until separately verified.
- Separate routing/cutover authorization for `/webhooks/airwallex/shortfalls`.
  Preserve Node's other webhook ownership and exclude automatic funding.

## Sources

Official references inspected using connected Airwallex docs and public API
pages on 2026-11-14. These are public documentation, not account evidence.

- [Issuing webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/issuing)
- [Retrieve a card transaction event](https://www.airwallex.com/docs/api/issuing/card_transaction_events/retrieve)
- [List card transaction events](https://www.airwallex.com/docs/api/issuing/card_transaction_events/list)
- [Lifecycle retrieval guide](https://www.airwallex.com/docs/issuing/transactions/transaction-lifecycle/retrieve-lifecycle-and-transaction-data)
- [Current balances](https://www.airwallex.com/docs/api/core_resources/balances/current)
- [Shortfall design](/docs/designs/shortfall-detection.md)
