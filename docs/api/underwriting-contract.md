# Underwriting contract

Pinned implementation contract for the Go underwriting service. Read alongside
[the rule design](/docs/designs/underwriting-engine.md) and
[the task plan](/docs/plans/completed/20261114-underwriting-engine.md). The
service is implemented against this field and API baseline.

[TOC]

## Conventions and startup

- JSON field names use snake_case; enums use UPPER_SNAKE_CASE. Dates are UTC
  `YYYY-MM-DD`; instants are RFC3339 with timezone, normalized to UTC.
- Money is integer cents, stored in signed bigint. Use checked arithmetic.
  Fractions, negative amounts where disallowed, overflow, NaN and strings
  pretending to be money are invalid. Flags can be null only as stated below.
- Snapshot/schema version is 1. Unknown JSON fields, repeated object keys,
  trailing JSON values and malformed values are rejected on writes/imports.
- Body limit: evaluation POST 16,384 bytes; each snapshot file 2,000,000 bytes.
  At most 10,000 records in any snapshot array. Oversize request is 413.
- Runtime variables: `DATABASE_URL`, `UNDERWRITING_MODE` (DEMO or SANDBOX),
  `UNDERWRITING_HTTP_ADDR` (default `127.0.0.1:8083`),
  `UNDERWRITING_SERVICE_SECRET` (>=32 bytes), `UNDERWRITING_FACILITY_ID`,
  `SHORTFALL_BASE_URL`, and `SHORTFALL_SERVICE_SECRET` (>=32 bytes).
  Shortfall source URL is bound to mode at configuration, not by client JSON.
- Require HTTPS for non-loopback dependency URLs. Disable redirects. Client
  timeout five seconds, response limit 4,000,000 bytes. Overall evaluation
  deadline ten seconds, DB operations five seconds within that deadline.
- Use existing module Go/pgx versions without upgrades. Migration credentials
  are supplied explicitly only to `cmd/migrate`; runtime never runs DDL.

## Customer input file

The CLI accepts one JSON object. Common metadata fields are all required:

| Field | Type and meaning |
| --- | --- |
| schema_version | Integer 1 |
| source_mode | DEMO or SANDBOX, matching configured mode |
| source_id | Nonempty stable producer identifier |
| source_version | Nonempty producer version/run ID |
| captured_at | UTC capture instant |
| source_refs | Array of {system,record_id,version,effective_at}; at least one |
| customer_id, account_id, card_id | Nonempty mapped identifiers |
| coverage | Object with the eight required flags below |
| standing | Object or null when standing coverage is false |
| balance_history | Array of {date,balance_cents}; dates unique |
| inflow_history | Array of {id,date,amount_cents,kind,payer_id} |
| bridges | Array of bridge records below |
| reservations | Array of reservation records below |
| receivables | Array of invoice records below |
| classifications | Array of {transaction_id,merchant_category,source_ref} |

`coverage` has booleans `standing`, `balance_history`, `inflow_history`,
`bridge_history`, `lifetime_defaults`, `reservations`, `receivables`, and
`classification`. Arrays are always present, including when empty; coverage
distinguishes known-empty from incomplete. Explicit false is a valid incomplete
input, not a reason for an importer to invent facts. Required fields inside
a declared complete group must be valid, or publication fails.

`standing` fields: `relationship_started_on` date, `onboarding_complete`
boolean, `active` boolean, `suspended` boolean, `provider_account_status`
(ACTIVE/FROZEN/CLOSED/UNKNOWN). Nullable unknown standing values require false
standing coverage. `UNKNOWN` account status cannot satisfy active standing.

Balance cents may be signed to preserve actual history; other monetary values
are nonnegative unless a field explicitly states otherwise.
Balances and inflows carry `window_start` and `window_end` dates in a required
`history_window` object. Latest 90 completed UTC dates must be represented for
the balance requirement. A longer supplied window is allowed, with one balance
per date. Complete inflow coverage means all records in that interval were
considered, not that each date has a deposit. Allowed inflow kinds are
EXTERNAL_SETTLED, OWN_TRANSFER, REFUND, BRIDGE_FUNDING, REVERSAL and UNKNOWN;
only EXTERNAL_SETTLED counts. UNKNOWN makes inflow coverage unusable.

Bridge record fields: `id`, `transaction_id`, `original_principal_cents`,
`principal_owed_cents`, `fees_owed_cents`, `status` (OPEN/REPAID/DEFAULTED),
`opened_on`, `due_on`, nullable `closed_on` and `defaulted_on`, and nullable
`repayment_outcome` (ON_TIME/LATE). All monetary fields are nonnegative;
original principal is positive. REPAID has zero debt, a close date and outcome;
DEFAULTED has a default date and can retain debt. OPEN has no close/default
date. Closed dates/outcomes determine late-closure history; default date
determines default windows. Default exclusion does not depend on close date.

Reservation fields: `id`, `transaction_id`, `principal_cents`, `fee_cents`,
`status` (ACTIVE/CONVERTED/RELEASED), nullable `bridge_id`. ACTIVE contributes
principal plus fee commitment; CONVERTED contributes zero because its linked
bridge already contributes debt. A converted reference must resolve to that
bridge, never an unrelated customer/payment. RELEASED contributes zero.

Invoice fields: `invoice_id`, `payer_id`, `currency` (USD),
`eligible_unpaid_cents`, `allocated_cents`, nullable `due_on`,
`due_date_verified`, `allocation_complete`, and `source_ref`.
Eligible unpaid is already net of paid/cancelled/disputed amounts. Allocated
must be between zero and eligible unpaid. Free amount is their difference.
Missing/unverified dates or allocations make coverage UNKNOWN, including the
demo missing-date case. Invoice identity is unique within the snapshot.

No future economic events can be reported as already settled/defaulted/repaid.
Receivable due dates can be future. Reconcile bridge/reservation references
before claiming coverage complete. Do not interpret Salesforce note text as
one of these validated financial fields.

## Facility input file

Use the common metadata above, replacing customer/account/card fields with
`facility_id`. Data fields: `currency: USD`, `facility_total_cents` (>0),
`reserve_floor_bps` (0..10000), `funded_principal_cents`,
`reserved_principal_cents`, `ledger_version`, `ledger_complete` boolean and
`source_refs`. Principal totals are nonnegative. Ledger completeness false
remains publishable but cannot prove available capital. Fee receivables never
increase available cash. This source represents the selected configured
facility, not an arbitrary most-recent row across all facilities.

## Import and persistence behavior

Commands from the service directory:

```bash
go run ./cmd/migrate
go run ./cmd/import-snapshot --kind customer --file /path/customer.json
go run ./cmd/import-snapshot --kind facility --file /path/facility.json
```

Import validates mode, active customer/card/roster linkage and metadata before
insert. Scope customer versions by mode/customer; facility by mode/facility.
Assign monotonically increasing version and UUID server-side. Lock that scope
within the publication transaction. Unique producer key is
(mode,kind,owner_id,source_id,source_version). Same key and canonical hash
returns
the existing ID/version; changed content conflicts. A new producer version with
older capture time is rejected. Same capture time is allowed with a new version.

Use typed canonical JSON for SHA-256, preserving array order. Reordered arrays
are changed content, not a request to merge/deduplicate. Snapshot CLI reports
safe JSON {snapshot_id,version,replayed}, exits 0 on success, nonzero on invalid
input/storage failure, and never prints raw input/DSNs. Old rows are immutable.

`customer_snapshots` and `facility_snapshots` retain metadata, source key/hash
and validated JSON. `evaluations` stores UUID, mode/caller/key/request hash,
request, shortfall copy, snapshot IDs/versions or null for missing sources,
copied input facts, evaluated_at, created_at, policy ID/hash and result JSON.
Enforce
unique (mode,caller,idempotency_key). No separate idempotency table is needed.
Database state is not shared with or written into shortfall/funding schemas.

## Evaluation HTTP and responses

Authenticate with the existing service-assertion shape: HS256, issuer
`flowcard-node-proxy` (retained compatibility label; no Node runtime needed),
audience `flowcard-underwriting-service`, subject `underwriting-evaluator`,
nonempty jti, integer iat/exp, maximum TTL 30 seconds and five-second skew.
Use the dedicated underwriting secret. Duplicate Authorization values and
malformed/expired assertions give 401; valid wrong audience/subject gives 403.

POST `/api/underwriting/evaluations`:

```json
{"shortfall_id":"00000000-0000-4000-8000-000000000001","term_days":30}
```

Require one `Idempotency-Key` header of 1–128 characters from
`[A-Za-z0-9._:-]`. Validate UUID and integer term 7..90. Fingerprint only typed
{shortfall_id,term_days} in fixed field order. Caller is verified JWT subject;
mode comes from server config. Look up completed key before dependency calls.
First committed result is 201; exact replay 200 with same saved body; changed
request on same scoped key 409/IDEMPOTENCY_CONFLICT. At concurrency race,
INSERT ON CONFLICT waits, then a fresh READ COMMITTED SELECT compares hashes
and returns winner or 409. Do not retry with another generated key.

Response fields:

- `id`, `shortfall_id`, `customer_id`, `account_id`, `transaction_id`,
  `source_mode`, `evaluated_at`, `created_at`, `policy_id`, `policy_hash`.
- Nullable `customer_snapshot_id`, `customer_snapshot_version`,
  `facility_snapshot_id`, `facility_snapshot_version`.
- `status`: ELIGIBLE/INELIGIBLE/REVIEW_REQUIRED; `approval_required` boolean,
  `funding_authorized:false`, `requires_recheck:true`.
- Nullable computed `tier`, `customer_limit_cents`, `existing_exposure_cents`,
  `post_funding_exposure_cents`, `available_capital_cents`.
- `principal_cents`, `term_days`, `estimated_fee_cents`, `total_due_cents`.
  For unusably stale amount evidence, dependent monetary results are null.
- `rules`: ten catalogue entries in design order, each with `rule_id`,
  `status`, `reason_code`, `actual` object, `threshold` object, `source_refs`.
  PASS code is RULE_ID_PASSED, FAIL RULE_ID_FAILED, UNKNOWN RULE_ID_UNKNOWN.
  `reason_codes` aggregates non-PASS codes, adding HUMAN_APPROVAL_REQUIRED
  and/or SHORTFALL_STALE when applicable. Explanations use controlled templates.

All response fields are present; null means unknown. A decision with a complete
known hard failure can still be INELIGIBLE while unrelated rules are UNKNOWN.
Human approval is computed from regime/request when known; REVIEW_REQUIRED
does not itself imply an approval task can bypass a missing-data rule.

Errors are `{"error":{"code":"CODE","message":"safe fixed text"}}`:

| Status | Codes and meaning |
| --- | --- |
| 400 | INVALID_REQUEST, INVALID_QUERY |
| 401/403 | UNAUTHORIZED, FORBIDDEN |
| 404 | SHORTFALL_NOT_FOUND, EVALUATION_NOT_FOUND, CONTEXT_NOT_FOUND |
| 409 | IDEMPOTENCY_CONFLICT |
| 413 | PAYLOAD_TOO_LARGE |
| 422 | SHORTFALL_NOT_ELIGIBLE, SHORTFALL_LINKAGE_INVALID |
| 503 | SHORTFALL_UNAVAILABLE, SHORTFALL_RESPONSE_INVALID, STORAGE_UNAVAILABLE |

Shortfall timeout/unavailable/malformed 2xx is 503 without saving a decision;
unknown source ID is 404. Wrong outcome/currency/amount identity is 422.
Known valid but stale shortfall is a saved review. Missing/stale snapshots
also save a review, using explicit UNKNOWN rules. No silent fallback from a
missing current snapshot to an older version. No persistence on API errors.

GET detail returns the same saved result with 200, without reevaluation.
List returns {items,next_cursor}; 25 results and null when exhausted, ordered
by (created_at DESC,id DESC). `customer_id` is optional; when absent, list the
configured mode's evaluations across customers. When supplied, it must be
nonempty and scopes the page to that customer. Allow only `customer_id` and
`cursor`, rejecting duplicate or unknown query keys. Cursor encodes typed JSON
fields mode, customer_id, created_at and id as base64url, validated and bound
to the exact mode and customer filter, including an empty filter. It is never
executable SQL.

GET customer context returns {schema_version,source_mode,customer_id,
snapshot_id,snapshot_version,captured_at,policy_id,policy_hash,coverage,fresh,
data,source_refs}; data is normalized snapshot fields defined above, with no
raw documents/notes/secrets. Missing context 404, outage 503. Stale context
remains readable with fresh=false; it cannot satisfy a fresh evaluation rule.
All routes return no-store. Health/readiness follow the design.

## Demo preparation and executable proof

Pin demo fixture relationships: three distinct mapped customers/accounts/cards;
tenure nine calendar months, no late/default history, lifetime-default coverage
known, 90 daily balances with no near-zero days, qualifying settled inflows
totaling $300,000 (monthly $100,000), active standing, no existing bridge for
the fixture request payment. These facts prove Tier B rather than Tier A.

Existing total funded/reserved exposure is $12,000 for eligible/missing-date
cases and $14,000 for capacity failure. Its component records must be internally
consistent, with original principal/fees/payments permitting remaining amounts.
New principal $7,000 plus fee $190 produces $19,190/$21,190 against $20,000.
Receivables have $8,000 free in-term amount with complete allocations, except
the missing-date case has due_on=null and due_date_verified=false.

One configured facility can serve all three cases: total $1,000,000, reserve
20%, funded principal $80,000, reserved principal $20,000, leaving $700,000
above floor (ABUNDANT). Its books must include the imported customer exposures
and consistent explicitly synthetic other-customer totals. Fee amounts are
excluded only from facility cash depletion, not customer total-debt limits.

`cmd/demo` has three explicit subcommands:

```bash
go run ./cmd/demo prepare --scenario all --as-of now --out .dev/demo
go run ./cmd/demo serve-shortfalls --manifest .dev/demo/manifest.json
go run ./cmd/demo evaluate --manifest .dev/demo/manifest.json --api-url http://127.0.0.1:8083
```

Prepare also accepts optional `--database-name`, defaulting to
`flowcard_underwriting_demo`. Names must be the default or use the
`flowcard_underwriting_demo_<lowercase-alphanumeric-or-underscore-suffix>`
namespace and fit PostgreSQL's identifier limit. The manifest captures the
validated name, and generated mapping SQL requires that exact current database.
Prepare renders relative dates and creates fresh run IDs/source versions/keys.
It writes customer files, facility file, synthetic mapping setup SQL, signed
shortfall-response fixture inputs, expected traces and manifest; it does not
execute migrations/reset data. Import and mapping commands in the runbook
target only a dedicated task-owned demo database. Serve-shortfalls is a
loopback-only authenticated read mock, never inserted into normal runtime
rules. Evaluate uses normal internal auth/config, POST and saved GET for each
case, printing result ID/status/reasons without secrets. Same-run evaluation
reuses manifest keys. New preparation produces a fresh output run and retains
prior artifacts. No stale timestamp bypass or direct result insertion.

Read commands resolve artifacts relative to the validated manifest bundle root.
The bundle may be mounted or copied to a runtime directory that does not
contain this repository; root and retained-run manifests must agree, and
artifact paths cannot traverse or follow symlinks outside the bundle.

Preparation supports a pinned RFC3339 as-of instant for tests, defaulting to
current UTC in presentations. Use one clock anchor for all three scenarios.
Rerender an expired prepared run, rather than rewriting source capture time
on an old decision or extending the five-minute source freshness limit.
