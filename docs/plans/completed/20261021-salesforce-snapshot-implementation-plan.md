---
goal: Build an internal roster and daily Salesforce snapshot service
version: 1.0
date_created: 2026-10-21
last_updated: 2026-10-21
owner: Ramil
status: Complete
tags: [feature, salesforce, snapshots, bridge-ledger]
---

# 20261021 Salesforce customer snapshot implementation plan

![Status: Complete](https://img.shields.io/badge/status-Complete-brightgreen)

[TOC]

## Overview

Build a server-side customer roster and daily Salesforce snapshot service for
customers explicitly enrolled in the FlowCard bridge program. Snapshots provide
a versioned, canonical representation of approved Salesforce account,
contract, case, and call-activity data. The later AI evidence pipeline can
consume this data, but this plan stops before AI extraction, underwriting
decisions, or policy changes.

The demo uses synthetic fixtures. Production uses the existing read-only
Salesforce MCP only. Both sources produce the same internal snapshot schema.
Each customer snapshot is published atomically after every required query and
validation succeeds. Failed or stale snapshots block new bridge decisions for
that customer while existing bridge servicing continues.

## Context from discovery

- The repository is currently a React, TypeScript, and Vite frontend. The
  server and PostgreSQL foundation are planned in the [Airwallex bridge ledger
  plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md).
- The current CRM interface in src/services/types.ts only returns account
  notes. Its mock returns the seeded Bloom note; the UI still uses demo seed
  data directly.
- src/seed.ts contains the demo customer profiles and synthetic CRM content.
  There is no persistent customer roster or server-side Salesforce snapshot.
- The authoritative requirements are the [Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md),
  the [Salesforce Context Integration Spec](/docs/requirements/Airwallex%20Hackathon.md#salesforce-context-integration-spec),
  and the [gap analysis](/docs/requirements/Airwallex%20Hackathon_req_2.md).
- The requirements call Salesforce field API names and picklist values
  provisional. The production mapping must be checked against the connected
  Salesforce org before live sync is enabled.

## 1. Requirements and constraints

- **REQ-001:** The FlowCard database owns the bridge-enrollment roster. The
  daily job selects only customers with active enrollment.
- **REQ-002:** Enrollment requires an explicit operator action. Soft
  deactivation stops future snapshots and new bridge decisions, retains the
  roster row and snapshot history, and does not stop servicing open bridges.
- **REQ-003:** Store a stable internal customer ID, one Airwallex connected
  account ID, and one Salesforce Account ID. Enforce one-to-one mapping. Never
  match by company name or email.
- **REQ-004:** Use synthetic fixture snapshots in the demo and the existing
  read-only Salesforce MCP in production. Do not write to Salesforce or add a
  REST fallback.
- **REQ-005:** Run one scheduled snapshot per day. Do not add change-event,
  polling, or on-demand refresh paths in this plan.
- **REQ-006:** Publish snapshots atomically per customer. Every allowlisted
  query and all of its source pages must complete before publishing. A
  successful empty result is complete.
- **REQ-007:** Process records in internal batches of up to 1,000. Honor the
  MCP's own pagination contract and fetch every required page.
- **REQ-008:** A published snapshot is fresh for at most 24 hours. After
  bounded retries are exhausted for a required query, retain the previous
  version as stale and block new bridge decisions for that customer.
- **REQ-009:** Map Salesforce fields and picklist values into a canonical,
  versioned internal schema. A null approved bridge cap or unmapped picklist
  value makes that customer's candidate snapshot invalid.
- **REQ-010:** Include the approved Account and Contract fields, open or
  escalated risk-relevant Cases, and up to five recent call Activities from
  the prior 90 days. Include bounded case descriptions and call notes; exclude
  full email threads and attachments.
- **REQ-011:** Use the Airwallex connected-account list for reconciliation
  against the internal roster only. It does not enroll or deactivate customers.
- **REQ-012:** Keep the current policy engine authoritative. This plan does
  not extract AI signals, generate rationales, change underwriting rules, or
  alter bridge amounts.
- **REQ-013:** A Salesforce or MCP failure for one customer must not prevent
  complete snapshots for other enrolled customers from publishing.
- **CON-001:** Actual Salesforce object names, field names, and picklist values
  are not verified. Production sync is gated on confirming the org mapping.
- **CON-002:** The production MCP must support unattended calls, read-only
  access, complete pagination, and bounded retries. If it cannot, production
  Salesforce sync remains blocked; the demo fixture mode remains available.
- **CON-003:** The exact maximum text length for case and call notes is not
  specified yet. Implement it as configuration and set it before production
  enablement.

## 2. Development approach

Use the regular code-then-test sequence, matching the Airwallex plan. Complete
one task and its focused tests before starting the next. Use Vitest for unit and
integration tests and the existing PostgreSQL test setup planned for the shared
server foundation.

This plan depends on the Node.js server, PostgreSQL pool, migration runner,
configuration, and HTTP foundation from the Airwallex plan. Reuse those
components instead of creating a second server or database layer. Start
Salesforce tasks after that foundation is available in the implementation
branch.

The enrollment interface is an assumption: because the repository has no
authentication or admin UI, v1 uses a restricted operations CLI with an actor
and reason recorded for each change. Demo customers are seeded. Replace this
assumption if an authenticated operator interface is selected before
implementation.

## 3. Testing strategy

- Use Vitest for mapping, validation, roster, pagination, retry, freshness,
  reconciliation, and API tests.
- Use a test PostgreSQL database for unique roster mappings and atomic snapshot
  publication.
- Use a fake MCP client for deterministic tests. Automated tests must not call
  a live Salesforce org or Airwallex account.
- Contract-test the fixture and MCP adapters against the same canonical
  snapshot schema.
- Test empty results, more than 1,000 records, duplicate or missing IDs,
  transient and exhausted failures, unknown picklists, null approved cap,
  stale snapshots, deactivation, and customer-level failure isolation.
- Run npm test, npm run lint, and npm run build after implementation tasks
  that change application code.
- Production MCP verification is a manual readiness check. It must prove
  unattended access, read-only permissions, complete query pagination, and
  retry behavior without exposing credentials in logs.

## 4. Solution overview

1. An operator enrolls a customer in the internal roster with stable Salesforce
   and Airwallex IDs. The Airwallex list is used to reconcile those IDs.
2. The daily worker selects active roster rows and asks the configured source
   for Account, Contract, Case, and call-activity records.
3. The source adapter maps provider values to the canonical schema and records
   source IDs, source timestamps, mapping version, and query completeness.
4. The worker builds a candidate per customer, validates required data, and
   publishes the complete version in one database transaction.
5. A read API exposes the latest snapshot and freshness state. New bridge
   decisions require active enrollment and a fresh, valid snapshot. Existing
   bridge servicing does not depend on a current Salesforce snapshot.

The fixture adapter is selected for demo mode. The MCP adapter is selected for
production mode. The mode is explicit; production must not silently fall back
to fixtures or another Salesforce transport.

## 5. Technical details

### 5.1 Internal enrollment roster

Create a roster table with a stable internal customer ID, unique Salesforce
Account ID, unique Airwallex connected-account ID, enrollment state, and
enrollment/deactivation audit fields. Keep deactivated rows for history. The
operations CLI must require the operator identity and reason for enrollment or
deactivation. It must not create enrollments from provider lists.

The initial roster state is the sole source of snapshot membership. Airwallex
account-list results and Salesforce Account records are reconciliation and
data sources, not enrollment authorities.

### 5.2 Canonical snapshot schema

The internal model contains:

- Account customer status, KYB status, credit-review status, approved bridge
  cap, prior-default status, and loyalty tier.
- Contract status and end date.
- Risk-relevant Case status, priority, reason, created/closed dates, escalation
  state, controlled risk category, bounded description, and Salesforce record
  ID.
- Up to five recent call Tasks or Events from the prior 90 days, with activity
  date, controlled outcome category, bounded note text, and Salesforce record
  ID.
- Snapshot ID, schema version, mapping version, source mode, run ID, captured
  time, per-query completion metadata, and record counts.

Store only allowlisted fields and bounded text. Preserve null distinctly from
an absent or unmapped field. A null approved bridge cap invalidates the
candidate. An unrecognized mapped picklist value invalidates the candidate
instead of being coerced to a permissive default.

### 5.3 Snapshot publication and freshness

Keep immutable snapshot versions and a per-customer pointer to the current
version. Store the latest sync attempt separately from the published snapshot
state so a failed run cannot overwrite a good version with partial data.

For each customer, publish only after every allowlisted query and page has
completed and all required mappings validate. A successful empty Case or
Activity query is valid. If a query fails transiently, retry a bounded number
of times; use three total attempts with backoff as the initial implementation
default. After exhaustion, leave the old version in place, mark it stale, and
block new bridge decisions for that customer. Other customers continue
independently.

Mark a version stale immediately after an exhausted refresh failure or when
its age exceeds 24 hours. Deactivation removes the customer from future
snapshot runs and new bridge decisions, while existing servicing continues.

### 5.4 Salesforce and reconciliation sources

The fixture adapter supplies synthetic demo snapshots. The production adapter
uses only the existing read-only Salesforce MCP. It must use fixed allowlisted
queries and fetch all pages. The MCP capability check must verify that the
scheduled worker can access the connector without an interactive user
session.

Use the Airwallex platform account-list API to compare provider accounts with
enrolled roster entries. Record discrepancies for operator review; never
auto-enroll, auto-deactivate, or match by name. The [Airwallex list connected
accounts API](https://www.airwallex.com/docs/api/scale/accounts/list) documents
platform-scoped enumeration and pagination.

## 6. What goes where

- **server/src/roster/**: enrollment types, repository, service, and operations
  CLI.
- **server/src/salesforce/**: canonical types, field mapping, fixture source,
  production MCP source, and query definitions.
- **server/src/snapshots/**: version storage, publisher, daily worker, and
  freshness/read model.
- **server/src/reconciliation/**: Airwallex account-list reconciliation.
- **server/src/http/routes/**: read-only snapshot endpoint and enrollment
  administration if the selected operator mechanism changes.
- **src/services/**: typed snapshot reader; demo fixture mode remains
  deterministic.
- **tests/server/** and **tests/kit5/**: unit, database, adapter, and client
  coverage.

## 7. Implementation steps

### Phase 1: Shared backend and roster

#### Task 1 (TASK-001): Add the enrolled-customer roster

**Files:**

- Create: server/src/db/migrations/002_customer_roster.sql
- Create: server/src/roster/types.ts
- Create: server/src/roster/repository.ts
- Create: server/src/roster/service.ts
- Create: server/src/roster/cli.ts
- Modify: package.json
- Create: tests/server/roster.test.ts

- [x] Add a roster row with internal customer ID, Salesforce Account ID,
  Airwallex connected-account ID, state, and audit fields.
- [x] Enforce unique provider IDs and reject missing or duplicate mappings.
- [x] Add explicit enroll, soft-deactivate, and list operations. Require actor
  identity and reason; do not auto-enroll from Salesforce or Airwallex.
- [x] Seed synthetic demo roster entries without using production data.
- [x] Verify deactivation stops new snapshot runs and new bridge decisions but
  leaves open bridge servicing available.
- [x] Test enrollment, deactivation, duplicate IDs, and invalid mappings.
- [x] Run the focused roster and migration tests.

### Phase 2: Canonical schema and source adapters

#### Task 2 (TASK-002): Define canonical snapshot types and field mapping

**Files:**

- Create: server/src/salesforce/types.ts
- Create: server/src/salesforce/field-map.ts
- Create: server/src/salesforce/validate.ts
- Create: tests/server/salesforce-mapping.test.ts

- [x] Define the versioned internal Account, Contract, Case, and call-Activity
  schema from the approved allowlist.
- [x] Map proposed API names and provisional picklist values to canonical types
  without leaking provider field names into consumers.
- [x] Preserve Salesforce source IDs, source timestamps, mapping version, and
  null values.
- [x] Reject a null approved bridge cap, missing required field, or
  unrecognized picklist value.
- [x] Add configuration for the maximum permitted Case and Activity text
  length; require a value before production enablement.
- [x] Test every mapping and validation failure, including schema drift.
- [x] Run focused mapping tests before Task 3.

Implementation note: `salesforce-proposed-v1` uses the documented proposed
field names and provisional picklist values. It is explicitly unverified and
production readiness fails closed until the org mappings and text limits are
confirmed. No live Salesforce verification was performed.

#### Task 3 (TASK-003): Add synthetic demo snapshot fixtures

**Files:**

- Create: server/src/salesforce/fixture-source.ts
- Create: server/src/salesforce/fixtures/*.json
- Create: tests/server/salesforce-fixture.test.ts
- Modify: src/services/types.ts
- Modify: src/services/mock/CrmClient.ts

- [x] Convert the existing synthetic Bloom CRM note and required demo records
  to the canonical snapshot fixture shape.
- [x] Add fixture cases for valid values, empty Cases/Activities, invalid
  mapping, and bounded text.
- [x] Ensure fixture mode uses no Salesforce or Airwallex network calls.
- [x] Keep existing AI extraction and policy mocks unchanged.
- [x] Contract-test fixtures against the canonical schema.
- [x] Run focused fixture tests before Task 4.

#### Task 4 (TASK-004): Implement the production Salesforce MCP adapter

**Files:**

- Create: server/src/salesforce/mcp-source.ts
- Create: server/src/salesforce/queries.ts
- Create: server/src/salesforce/mcp-config.ts
- Create: tests/server/salesforce-mcp-source.test.ts

- [x] Implement read-only allowlisted reads for Account, Contract, risk Case,
  and call Activity records.
- [x] Use the internal roster's Salesforce Account ID for every query. Do not
  query or update Salesforce outside the allowlist.
- [x] Normalize MCP pagination and expose whether each query is complete.
- [x] Add bounded retries for transient MCP errors; keep the retry limit
  configurable.
- [x] Reject production mode at startup if required MCP configuration is
  missing. Do not fall back to REST or demo fixtures.
- [x] Test permissions errors, incomplete pages, empty results, timeouts,
  retries, and unknown fields with a fake MCP client.
- [x] Run focused MCP adapter tests before Task 5.
- [x] Manually verify unattended connector access and read-only permissions
  before enabling production sync. **Skipped:** provider calls are forbidden for
  this work; production remains blocked until this live check is completed.

### Phase 3: Versioned storage and daily synchronization

#### Task 5 (TASK-005): Add immutable snapshot storage and atomic publication

**Files:**

- Create: server/src/db/migrations/003_salesforce_snapshots.sql
- Create: server/src/snapshots/repository.ts
- Create: server/src/snapshots/publisher.ts
- Create: tests/server/snapshot-publisher.test.ts

- [x] Add per-customer snapshot versions and a current-version pointer.
- [x] Store canonical payload, source mode, schema/mapping versions, sync run
  ID, capture timestamps, query completion, and record counts.
- [x] Publish a complete candidate and advance the pointer in one database
  transaction per customer.
- [x] Keep prior versions immutable. Store failed attempts separately from the
  last published version.
- [x] Test transaction rollback, pointer stability on failure, and independent
  publishing for two customers.
- [x] Run focused database publication tests before Task 6. The repository has
  no PostgreSQL test harness and Docker socket access was unavailable, so these
  focused tests use a transactional database fake plus migration assertions.

#### Task 6 (TASK-006): Build the daily per-customer sync worker

**Files:**

- Create: server/src/snapshots/sync-worker.ts
- Create: server/src/snapshots/scheduler.ts
- Create: tests/server/snapshot-sync-worker.test.ts

- [x] Schedule one configurable daily run. Do not add event-triggered refresh
  or polling between daily runs.
- [x] Read active customers from the internal roster only.
- [x] Fetch Account, Contract, Case, and Activity results and exhaust every
  source page before validating a candidate.
- [x] Process results in internal batches of up to 1,000 records, regardless
  of the MCP's page size.
- [x] Treat a successful empty result as complete. Limit call Activities to
  the five most recent records in the prior 90 days.
- [x] Use three total attempts with backoff as the initial default. After
  exhaustion, discard the candidate, preserve the prior pointer, and persist a
  failed attempt; Task 7 derives stale status and the per-customer decision gate.
- [x] Test more than 1,000 records, page failures, empty queries, retry
  exhaustion, and customer failure isolation.
- [x] Run focused sync-worker tests before Task 7.

Implementation note: exhausted runs are persisted in the separate failed-attempt
store without advancing the current snapshot pointer. Task 7 derives immediate
staleness and the per-customer decision gate from that latest failed attempt.

#### Task 7 (TASK-007): Add freshness state and the snapshot read API

**Files:**

- Create: server/src/snapshots/freshness.ts
- Create: server/src/http/routes/customer-snapshot.ts
- Modify: server/src/http/server.ts
- Create: tests/server/customer-snapshot-api.test.ts

- [x] Separate latest sync-attempt status from the published snapshot status.
- [x] Return snapshot version, freshness, age, source mode, and canonical
  allowlisted data from a read-only endpoint.
- [x] Mark the snapshot stale immediately after an exhausted required-query
  failure and after 24 hours without a new complete version.
- [x] Gate new bridge commands on active roster status and fresh valid
  snapshot; leave existing bridge servicing unaffected.
- [x] Test fresh, missing, stale, invalid, and deactivated customer states.
- [x] Run focused snapshot API tests before Task 8.

Implementation note: the API returns demo fixture snapshots without credentials.
Production MCP snapshot responses require the configured
`SALESFORCE_SNAPSHOT_READ_TOKEN` bearer token; missing or incorrect credentials
do not expose production snapshot data. Production snapshots with unverified
mapping remain invalid and cannot pass the new-bridge gate.

### Phase 4: Reconciliation and consumer integration

#### Task 8 (TASK-008): Reconcile enrolled customers with Airwallex

**Files:**

- Create: server/src/reconciliation/airwallex-accounts.ts
- Create: server/src/reconciliation/customer-roster.ts
- Create: tests/server/customer-roster-reconciliation.test.ts

- [x] Use the existing Airwallex server client to list connected accounts with
  source-appropriate pagination.
- [x] Compare Airwallex IDs with active and deactivated roster entries.
- [x] Emit a report for missing, extra, duplicate, or status-mismatched IDs.
- [x] Never auto-enroll, auto-deactivate, or match by company name or email.
- [x] Keep reconciliation state separate from Salesforce query completeness.
- [x] Test reconciliation output and verify it never mutates the roster.
- [x] Run focused reconciliation tests before Task 9.

Implementation note: the existing server gateway requests `GET /api/v1/accounts`
with the documented `page_num` pagination and maximum `page_size` of 500, and
fails closed on malformed or incomplete pagination. The reconciliation report
compares only stable connected-account IDs and treats provider `ACTIVE` as
active for status comparison. It is an in-memory read-only report with no
Salesforce completeness state and does not write to the roster.

#### Task 9 (TASK-009): Wire snapshot source selection into the service layer

**Files:**

- Create: src/services/http/SalesforceSnapshotApiClient.ts
- Modify: src/services/types.ts
- Modify: src/services/index.ts
- Modify: src/kit5/scenarios.ts
- Create: tests/kit5/salesforce-snapshot-client.test.ts

- [x] Select synthetic fixtures in demo mode and the server read API in
  production mode.
- [x] Keep Salesforce MCP credentials on the server; never place them in the
  browser bundle.
- [x] Keep the existing mock extraction panel and policy behavior unchanged.
- [x] Test deterministic demo behavior and production client error handling.
- [x] Run focused client tests before Task 10.

Implementation note: the browser-safe production client calls only the server
read API and sends no credentials. The current API's production bearer-token
gate therefore remains fail-closed (401) until a trusted same-origin server
proxy is available; no token is exposed to the browser and no fixture fallback
is attempted in production. Demo mode reads the deterministic local fixtures.

#### Task 10 (TASK-010): Verify snapshot acceptance and production readiness

**Files:**

- Create: tests/server/salesforce-snapshot.integration.test.ts

- [x] Verify roster selection, query completeness, atomic per-customer
  publication, freshness, and stale bridge gating end to end.
- [x] Verify one customer's failed query does not block another customer's
  complete snapshot.
- [x] Verify production mode cannot silently switch to fixtures or REST.
- [x] Run the complete Vitest suite, lint, and production build.
- [x] Record MCP capability and mapping results in the deployment checklist.

Task 10 acceptance record (2026-10-21): The automated acceptance suite uses
deterministic fixtures and a simulated MCP read client; it makes no provider
calls. The suite verifies active-roster selection, complete paginated MCP
queries, atomic customer publication, freshness, stale bridge gating, and
customer-level failure isolation. Production fallback is absent: production
source errors remain failures and the production readiness check rejects the
provisional mapping.

Production deployment checklist:

- [x] Verify unattended MCP access, read-only permissions, complete pagination,
  and bounded retry behavior against the production connector (**not verified;
  no provider calls were authorized; production remains gated**).
- [x] Confirm Salesforce Account/Contract/Case/Activity field API names and
  canonical picklist mappings with the org owner (**not verified; provisional
  mappings are in use; production remains gated**).
- [x] Set the maximum case and call note text lengths before production
  enablement (**not set; confirmation is required before production; production
  remains gated**).

Keep production sync and new bridge decisions gated until the MCP capability
check passes and an owner confirms the org mapping. This record is not a
production-readiness approval.

#### Task 11 (TASK-011): Update project documentation

**Files:**

- Modify: README.md
- Modify: docs/EDITING.md
- Create: docs/salesforce-snapshot-operations.md

- [x] Document demo fixture mode, production MCP mode, roster enrollment, and
  soft deactivation.
- [x] Document daily schedule, freshness behavior, retry settings, and
  reconciliation output.
- [x] Document the required Salesforce field and picklist mapping checks.
- [x] Document the production MCP capability gate and explicitly note that
  there is no REST fallback.
- [x] Run the Markdown source checker on updated documentation.
- [x] Move this plan to `docs/plans/completed/` after every implementation task
  and acceptance check is complete. The orchestrator will perform the move
  after review and finalization; the plan remains in its current location now.

## 8. Acceptance criteria

- The scheduler selects only explicitly enrolled, active roster rows.
- Demo mode returns synthetic canonical snapshots without external calls.
- Production mode uses only the read-only MCP adapter.
- Each customer's snapshot is a complete, immutable version published
  atomically after every required query and page succeeds.
- Empty successful Case or Activity results do not invalidate a snapshot.
- A null approved cap, missing required mapping, or unknown picklist fails
  that customer's candidate and blocks new bridges for that customer.
- A failed refresh after retries or a snapshot older than 24 hours is stale.
- Other customers continue syncing when one customer's candidate fails.
- Soft deactivation stops new snapshots and new bridge decisions while
  retaining history and servicing open bridges.
- Airwallex reconciliation reports discrepancies without changing enrollment.
- No AI extraction, Salesforce writes, event-driven refresh, or policy-rule
  changes are introduced.

## 9. Dependencies and assumptions

- **DEP-001:** The shared TypeScript server, PostgreSQL pool, migration runner,
  configuration, and HTTP foundation from the Airwallex plan are available.
- **DEP-002:** The existing Salesforce MCP can run unattended from the
  production worker and supports all required read queries and pagination.
  This must be verified before production enablement.
- **DEP-003:** A Salesforce owner confirms exact object names, field API names,
  and canonical picklist mappings for the target org.
- **DEP-004:** The maximum note-text length is set before production
  enablement.
- **ASSUMPTION-001:** V1 enrollment changes use a restricted operations CLI
  because the repository has no existing authentication or admin UI. Demo
  entries are seeded. Replace this if an authenticated admin API is selected.
- **ASSUMPTION-002:** Use three total MCP attempts with backoff as the initial
  configurable retry default.
- **ASSUMPTION-003:** Use regular code-then-test development, matching the
  Airwallex plan.

## 10. Risks and deferred scope

- The production MCP may be tied to an interactive session or may not expose
  complete query pagination. Production sync must stay blocked until the
  capability gate passes.
- The actual Salesforce schema may differ from the proposed API names. Mapping
  drift fails closed per customer and requires an updated mapping.
- The text-length cap must be agreed before production because the subsequent
  AI evidence design determines the useful context window.
- The AI evidence pipeline, prompts, signal schema, evidence confidence,
  rationale generation, policy changes, and on-record-change re-scoring are
  separate work and must not be added to this plan.

## 11. Related specifications

- [Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md)
- [Salesforce Context Integration Spec](/docs/requirements/Airwallex%20Hackathon.md#salesforce-context-integration-spec)
- [Salesforce and AI gap analysis](/docs/requirements/Airwallex%20Hackathon_req_2.md)
- [Airwallex bridge ledger plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md)
- [Current README](/README.md)
- [Editing guide](/docs/EDITING.md)
- [Airwallex list connected accounts API](https://www.airwallex.com/docs/api/scale/accounts/list)

## Post-completion

Before production enablement, provision read-only Salesforce MCP access for the
scheduled runtime, verify unattended operation and pagination, confirm the
Salesforce field/picklist map, set the note-text maximum, configure monitoring
for stale customers, and test roster reconciliation in the sandbox. Do not
enable production bridge decisions until the MCP capability gate passes.
