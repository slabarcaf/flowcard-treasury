---
goal: Implement Airwallex bridge funding and collection through a durable ledger
version: 1.0
date_created: 2026-10-21
last_updated: 2026-10-21
owner: Ramil
status: Planned
tags: [feature, architecture, airwallex, bridge-ledger]
---

# 20261021 Airwallex bridge ledger implementation plan

![Status: Planned](https://img.shields.io/badge/status-Planned-blue)

This plan adds a server-side Airwallex integration and durable bridge ledger to
the FlowCard Treasury project. It covers funding, transfer status, repayment
charges, webhook handling, reconciliation, and the current Kit 5 integration.

[TOC]

## Overview

Build a small TypeScript API with PostgreSQL-backed ledger, outbox, and
webhook inbox. The browser calls this API; only the server holds Airwallex
credentials and issues provider requests.

This plan is limited to Airwallex and bridge-ledger behavior. Salesforce
snapshots, AI evidence extraction, and changes to underwriting policy rules
remain separate work.

## Context (from discovery)

- The repository is a React 19, TypeScript, and Vite frontend with no backend.
- `src/services/` contains Airwallex, CRM, and agent interfaces with mocks;
  the UI does not call those clients yet.
- Kit 5 currently runs a scripted sequential engine and includes the target
  facility, seeded exposure, and demo scenarios.
- The latest authority is the [Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md).
- The [README](/README.md) documents current Vite scripts and the frontend-only
  live demo.

## 1. Requirements & Constraints

- **REQ-001**: Facility config and the internal bridge ledger determine
  deployable capital. The live platform wallet is an execution check, not the
  facility limit.
- **REQ-002**: V1 uses a $50,000 facility, a 20% reserve floor, and $25,000 of
  explicitly synthetic opening exposure, leaving $15,000 deployable.
- **REQ-003**: Append immutable ledger events and derive current bridge and
  facility views from them.
- **REQ-004**: Store the facility reservation and outbound command in one
  database transaction. Process provider calls from the transactional outbox.
- **REQ-005**: Verify Airwallex webhook signatures against the raw body, store
  each event once by provider event ID, then acknowledge it promptly.
- **REQ-006**: Treat webhook delivery as at-least-once and unordered. Fetch the
  provider object when needed; reconcile unresolved operations by `request_id`.
- **REQ-007**: Serialize USD facility reservations. `PENDING` and
  `SUSPENDED` transfers retain the reservation; `SETTLED` converts it to
  principal; `FAILED` releases it only after the provider confirms return.
- **REQ-008**: Use stable operation IDs in kebab case: `bridge-<UUID>`,
  `fee-<UUID>`, and `principal-<UUID>`. Persist each ID before the API call,
  reuse it for retries, and keep it under Airwallex's 50-character limit.
- **REQ-009**: Collection attempts are triggered by the agreed repayment term
  and capped at the triggering settled deposit. Do not use pre-existing wallet
  funds to cover a short deposit.
- **REQ-010**: Collect fees before principal. A partial deposit can pay part of
  the fee; submit principal only after the fee charge settles. Reduce a balance
  only when its charge settles.
- **REQ-011**: Store all four repayment choices: 15 days, 30 days, a fixed date,
  or as soon as money arrives. The demo exercises the last choice.
- **CON-001**: V1 supports one open bridge per customer and USD only.
- **CON-002**: Operating buffer is zero for demo and production for now; review
  it before live use.
- **CON-003**: Assess the fee at the collection trigger and keep that amount
  fixed if principal settlement crosses a day boundary.
- **CON-004**: Do not invent a 30-day backstop for the ASAP term or implement
  the late-payment ladder until those policies are decided.
- **CON-005**: Multi-bridge repayment allocation and cross-day fee adjustments
  are deferred in [the backlog](/docs/backlog/).
- **CON-006**: The existing policy engine remains the source of approval.
  The backend accepts a trusted approved command; this plan does not change
  underwriting rules or add CRM/AI behavior.

## Development Approach

Use a regular code-then-test sequence, with focused tests added in the same
task as each behavior. No tests are run while authoring this plan. The selected
backend default is a same-repository TypeScript service with PostgreSQL; the
deployment host remains an implementation assumption to verify.

## Solution Overview

A server API owns bridge commands and customer-visible read models. A shared
PostgreSQL database stores the append-only ledger, facility position, provider
operations, transactional outbox, and webhook inbox. A single USD allocator
locks the facility position, recomputes available capital, and records the
reservation and outbox command atomically.

Workers submit CA transfers and Charges after the local transaction commits.
Webhook workers append provider status observations and update projections.
A reconciler checks unresolved operations; an unexplained mismatch pauses new
funding until it is resolved. The existing mock scenario path remains available
for deterministic demo runs.

## Technical Details

### Ledger records and exposure

Persist a versioned facility config, a bridge case, immutable ledger events,
provider-operation mappings, and derived bridge and facility projections. Mark
the $25,000 opening exposure as `demo_seed`, not as a real Airwallex transfer.

Calculate deployable capital as lendable facility minus opening exposure,
settled principal, and active reservations. Fees are tracked separately and
do not change principal exposure. The service rejects a second open bridge for
a customer in V1.

### Outbox and provider operation IDs

Create the bridge reservation, provider-operation row, and outbox command in
one transaction. Workers call Airwallex outside the transaction. Transient or
ambiguous retries reuse the saved `request_id`; a new ID represents a new
operation after the previous operation is terminal and reconciled.

### Webhook inbox and reconciliation

Verify the HMAC using the raw body and the subscription secret before parsing.
Persist verified events under a unique Airwallex event ID, then return `200`.
Process events asynchronously and fetch the current provider object when event
order is uncertain. Periodically reconcile unresolved operations by request ID.
Any unexplained provider-to-ledger mismatch blocks new bridge reservations.

### Collection attempts

Create one collection attempt per settled deposit and bridge. Compute the fee
at the collection trigger using the 0.05% daily rate and seven-day minimum.
The fee charge is capped by the triggering deposit; any remainder can pay
principal only after the fee charge settles. V1 fixes the assessment amount
at the trigger and records any unpaid balance as still due.

Store the selected term on the bridge. ASAP collection waits for a settled
deposit; 15-day, 30-day, and fixed-date terms trigger at their due time. The
ASAP 30-day backstop remains undecided and is not implemented.

## What Goes Where

- `server/`: Node.js TypeScript API, Postgres adapter, ledger services,
  Airwallex gateway, outbox and inbox workers, and reconciliation.
- `src/services/`: typed frontend API client plus the existing mock clients.
- `src/kit5/`: call the API in live mode while preserving deterministic
  scenario mode.
- `tests/server/`: unit and database-backed tests for ledger and API flows.
- `tests/kit5/`: client-mode and scenario integration coverage.

## 2. Implementation Steps

### Implementation Phase 1: Backend and database foundation

#### Task 1 (TASK-001): Add the server runtime

**Files:**

- Create: `server/tsconfig.json`
- Create: `server/src/main.ts`
- Create: `server/src/config.ts`
- Create: `server/src/http/server.ts`
- Modify: `package.json`
- Create: `tests/server/config.test.ts`

- [ ] Create a Node.js TypeScript service entrypoint and health endpoint.
- [ ] Read database and Airwallex secrets only from server environment.
- [ ] Add local start and build scripts without changing the Vite client build.
- [ ] Test missing config, valid config, and health-route behavior.
- [ ] Run the focused server tests before Task 2.

#### Task 2 (TASK-002): Create the durable ledger schema

**Files:**

- Create: `server/src/db/pool.ts`
- Create: `server/src/db/migrations/001_bridge_ledger.sql`
- Create: `server/src/db/repositories.ts`
- Create: `tests/server/schema.test.ts`

- [ ] Add tables for facility config, bridge cases, immutable ledger events,
  provider operations, outbox commands, webhook inbox, and read projections.
- [ ] Add unique constraints for provider request IDs and webhook event IDs.
- [ ] Store the synthetic opening exposure with explicit provenance.
- [ ] Test schema constraints, uniqueness, and projection initialization.
- [ ] Run the focused database tests before Task 3.

### Implementation Phase 2: Ledger, reservations, and provider operations

#### Task 3 (TASK-003): Implement the append-only ledger and allocator

**Files:**

- Create: `server/src/ledger/events.ts`
- Create: `server/src/ledger/projector.ts`
- Create: `server/src/ledger/reservations.ts`
- Create: `tests/server/ledger.test.ts`
- Create: `tests/server/reservations.test.ts`

- [ ] Apply ledger events to rebuild bridge and facility projections.
- [ ] Lock the USD facility position while checking and reserving capital.
- [ ] Create the reservation and outbox command in the same transaction.
- [ ] Convert a reservation to principal only on transfer `SETTLED`.
- [ ] Release it only after terminal `FAILED` and confirmed return.
- [ ] Test the target $50K, 20% floor, $25K opening, $15K deployable figures.
- [ ] Test concurrent reservations cannot exceed deployable capital.
- [ ] Run focused ledger tests before Task 4.

#### Task 4 (TASK-004): Add Airwallex operations and stable request IDs

**Files:**

- Create: `server/src/airwallex/client.ts`
- Create: `server/src/airwallex/operations.ts`
- Create: `server/src/outbox/dispatcher.ts`
- Create: `tests/server/airwallex.test.ts`
- Create: `tests/server/outbox.test.ts`

- [ ] Implement server-side transfer, charge, balance, deposit, and sandbox
  card-retry operations behind a typed gateway.
- [ ] Generate and persist `bridge-<UUID>`, `fee-<UUID>`, and
  `principal-<UUID>` before sending each operation.
- [ ] Reuse the same request ID after transient errors or ambiguous responses.
- [ ] Look up by request ID before retrying; never create a second operation
  for the same outbox command.
- [ ] Test status normalization, duplicate responses, timeouts, and errors.
- [ ] Run focused Airwallex adapter tests before Task 5.

### Implementation Phase 3: Webhooks and reconciliation

#### Task 5 (TASK-005): Add the webhook inbox and status processor

**Files:**

- Create: `server/src/http/routes/airwallex-webhook.ts`
- Create: `server/src/webhooks/verify.ts`
- Create: `server/src/webhooks/inbox.ts`
- Create: `server/src/webhooks/process.ts`
- Create: `tests/server/webhooks.test.ts`

- [ ] Verify signatures against the raw body before parsing the payload.
- [ ] Persist each verified event once by event ID, then return `200`.
- [ ] Subscribe to transfer, charge, deposit-settled, and card-decline events.
- [ ] Fetch current provider status for out-of-order notifications.
- [ ] Append provider observations and update projections idempotently.
- [ ] Test invalid signatures, duplicate events, and out-of-order statuses.
- [ ] Run focused webhook tests before Task 6.

#### Task 6 (TASK-006): Add reconciliation and funding holds

**Files:**

- Create: `server/src/reconciliation/service.ts`
- Create: `server/src/reconciliation/worker.ts`
- Create: `tests/server/reconciliation.test.ts`

- [ ] Scan unresolved provider operations and query by request ID.
- [ ] Append recovered provider observations without rewriting ledger history.
- [ ] Place a facility hold on unexplained mismatches.
- [ ] Require a reconciled state before accepting new bridge reservations.
- [ ] Test missed events, ambiguous responses, and hold release conditions.
- [ ] Run focused reconciliation tests before Task 7.

### Implementation Phase 4: Collection and frontend integration

#### Task 7 (TASK-007): Implement deposit-triggered collections

**Files:**

- Create: `server/src/collections/fee.ts`
- Create: `server/src/collections/service.ts`
- Create: `server/src/collections/scheduler.ts`
- Create: `tests/server/collections.test.ts`

- [ ] Store the selected repayment term on each bridge.
- [ ] Create one attempt per settled deposit and bridge.
- [ ] Cap each attempt at the triggering deposit and current available balance.
- [ ] Assess the fee at the trigger, use a seven-day minimum, and keep the
  amount fixed if principal settles on a later day.
- [ ] Charge fee first; submit principal only after the fee charge settles.
- [ ] Carry unpaid fee or principal forward without reducing ledger balances.
- [ ] Test partial deposits, duplicate deposit events, and all four term types.
- [ ] Run focused collection tests before Task 8.

#### Task 8 (TASK-008): Connect Kit 5 through the server API

**Files:**

- Modify: `src/services/types.ts`
- Modify: `src/services/index.ts`
- Create: `src/services/http/BridgeApiClient.ts`
- Modify: `src/kit5/useKit5Dashboard.ts`
- Modify: `tests/kit5/engine.test.ts`
- Create: `tests/kit5/bridge-api.test.ts`

- [ ] Add an API client for approved bridge commands and ledger projections.
- [ ] Keep the deterministic mock scenario mode for the demo.
- [ ] Keep credentials and provider calls off the browser.
- [ ] Test API and mock modes against the same bridge status contract.
- [ ] Run Kit 5 tests before Task 9.

### Implementation Phase 5: Acceptance and documentation

#### Task 9 (TASK-009): Verify the accepted ledger scenarios

**Files:**

- Modify: `tests/server/acceptance.test.ts`
- Modify: `tests/kit5/engine.test.ts`

- [ ] Verify Atlas $5K and Nova $6K move deployable capital from $15K to $4K.
- [ ] Verify Juniper is declined when only $4K remains above the floor.
- [ ] Verify an $8K deposit collects Nova's $27 fee, then $6K principal.
- [ ] Verify pending status holds reservations and settled status releases them.
- [ ] Verify reconciliation mismatches stop new reservations.
- [ ] Run the full test suite, build, and lint during implementation.

#### Task 10 (TASK-010): Update project documentation

**Files:**

- Modify: `README.md`
- Modify: `docs/EDITING.md`

- [ ] Document local server setup and required environment variable names.
- [ ] Document mock and live API modes and webhook configuration prerequisites.
- [ ] Document the ledger, fee, and repayment behavior accepted for V1.
- [ ] Run the Markdown checker on changed documentation.

## 3. Alternatives

- **ALT-001**: Mutable bridge rows only. Rejected because retries,
  out-of-order webhooks, and reconciliation require an immutable history.
- **ALT-002**: Use the live platform wallet as the facility limit. Rejected
  because the Airwallex balance is not the configured bridge facility.
- **ALT-003**: Separate integration service in V1. Deferred to keep the first
  server boundary small; retain an API boundary that can move later.
- **ALT-004**: Direct Airwallex calls from the browser. Rejected because it
  would expose credentials and bypass the durable outbox and ledger.

## 4. Dependencies

- **DEP-001**: PostgreSQL database and server runtime. PostgreSQL is the
  plan default; the hosting provider remains to be confirmed.
- **DEP-002**: Airwallex sandbox credentials, connected accounts, transfer,
  charge, deposit, and issuing API permissions.
- **DEP-003**: Public HTTPS webhook URL and a managed webhook secret.
- **DEP-004**: A trusted `ApprovedBridgeCommand` from the policy module.
  Policy-rule changes and Salesforce/AI evidence are outside this plan.

## 5. Files

**Existing files to modify:**

- `package.json`
- `src/services/types.ts`
- `src/services/index.ts`
- `src/kit5/useKit5Dashboard.ts`
- `tests/kit5/engine.test.ts`
- `README.md`
- `docs/EDITING.md`

**New files:**

- `server/tsconfig.json` and `server/src/` service modules.
- `server/src/db/migrations/001_bridge_ledger.sql`.
- `src/services/http/BridgeApiClient.ts`.
- `tests/server/` unit and integration test modules.
- `tests/kit5/bridge-api.test.ts`.

## Testing Strategy

Use Vitest for unit and integration tests. Use a test PostgreSQL database
for transactional reservation and outbox/inbox tests; use a fake Airwallex
gateway for deterministic tests. Sandbox operations require explicit
credentials and a configured webhook subscription.

## 6. Testing

- **TEST-001**: Event projection and exposure arithmetic, including the
  synthetic $25K opening entry and target capital sequence.
- **TEST-002**: Concurrent facility reservations with two requests competing
  for the same deployable capital.
- **TEST-003**: Stable operation IDs, duplicate request recovery, and
  ambiguous create responses.
- **TEST-004**: HMAC verification, unique webhook event IDs, duplicate
  delivery, and out-of-order provider statuses.
- **TEST-005**: Deposit cap, fee-first partial collection, and charge failure.
- **TEST-006**: Funding hold on an unexplained provider-to-ledger mismatch.
- **TEST-007**: Existing scenario tests remain valid in mock mode.

## Progress Tracking

- Mark each task complete only after its implementation and focused tests
  pass.
- Keep this plan updated if runtime, database, or collection scope changes.
- Do not start the Salesforce/AI evidence module under this plan.

## 7. Risks & Assumptions

- **ASSUMPTION-001**: Use a TypeScript server in this repository and
  PostgreSQL. Confirm the deployment host before implementing server routes.
- **ASSUMPTION-002**: The policy module provides trusted approved commands;
  this plan does not make the browser authoritative for approval.
- **ASSUMPTION-003**: An ASAP repayment has no 30-day hard backstop until
  product policy resolves that question.
- **RISK-001**: Zero operating buffer is approved for now, including
  production; revisit before live use.
- **RISK-002**: Fee cross-day adjustment and multi-bridge allocation are
  deferred; this plan must not silently implement either behavior.
- **RISK-003**: Airwallex permissions and webhook setup may differ by
  sandbox account; verify required capabilities before provider smoke tests.

## 8. Related Specifications / Further Reading

- [Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md)
- [Airwallex data inventory](/docs/requirements/Airwallex%20Hackathon_req_6.md)
- [Transfer module](/docs/requirements/Airwallex%20Hackathon_req_7.md)
- [Current README](/README.md)
- [Editing guide](/docs/EDITING.md)
- [Webhooks overview](https://www.airwallex.com/docs/developer-tools/webhooks/webhooks-overview)
- [Connected Account Transfers API](https://www.airwallex.com/docs/api/scale/connected_account_transfers)
- [Charges API](https://www.airwallex.com/docs/api/scale/charges/create)

## Post-Completion

Before live or sandbox deployment, provision the PostgreSQL database, select
a hosting runtime for the server, configure Airwallex server credentials and
webhook secrets, register the webhook endpoint, and run an authorized sandbox
smoke flow. The later Salesforce and AI evidence module remains a separate
brainstorm and implementation plan.
