# Node funding retirement

Ramil authorized removal of the Node financial code replaced by Go on
November 15, 2026, after the successful joined Funding journey. This change
retires duplicate source and startup wiring. Existing processes, databases,
provider subscriptions and financial records were not changed.

[TOC]

## Removed ownership

| Removed Node responsibility | Replacement |
| --- | --- |
| Ledger event/projector writers, reservations and provider-operation queue | Funding `internal/postgres/reservations.go`, settlement/ledger primitives and guarded operations |
| Collection budgeting, fees and deposit settlement | Funding `internal/postgres/collection.go`, `collection_settlement.go` and `accrual.go` |
| Outbox dispatch, money reconciliation and financial inbox worker | Funding serial worker, fenced leases, durable journal and verified settlement path |
| Node policy prechecks, request decisions, automatic funding and human approval CLI | Shared Go underwriting policy plus bound Funding offers and actor decisions |
| Kit 5 funding/repayment orchestration and its attempt repository | Funding `cmd/demo` and authenticated offer workflow |
| Node transfer/charge create, balance/lookup and repayment-deposit simulation helpers | Funding HTTP adapter and local provider-double boundary |

The old demo coupled card retry to its financial reservation path, so that
orchestrator is also removed. Independent issuing parsing/client utilities
remain; Go card retry is still outside the completed funding scope.
Tests exclusive to removed implementations were deleted with their source.
Historical SQL migrations, seed SQL and schema/seed safety tests remain.

## HTTP and command behavior

- `/api/bridge-decisions`, `/api/remote-authorization` and legacy
  `/api/kit5/*` financial routes return 410 `legacy_funding_retired` before
  database or provider work. No old command is translated into Go consent.
- `/api/kit5/session` remains for the onboarding UI and its existing cookie
  contract. It reports `approvalAvailable=false`.
- `/webhooks/airwallex` still verifies signatures and durably forwards
  onboarding `account.*` and `rfi.*` events. Other event types return 410
  `legacy_servicing_retired` and do not enter the old financial inbox.
- `npm run policy:approval`, the Node PostgreSQL policy-acceptance runner and
  their implementation files are removed. A direct retired CLI command exits
  before loading configuration or opening a database.
- `npm run server:build` cleans only its documented `.dev/server` compiler
  output before compiling, preventing deleted modules from surviving there.

Use the [Go Funding runbook](/docs/demo/funding-and-collection-local.md) for
new local financial journeys. Its offer, acceptance and service-role contracts
are distinct from the retired Node routes.

## Why Node remains

| Retained responsibility | Current callers |
| --- | --- |
| Onboarding API/docs proxy, authenticated operator session and webhook forwarding | Onboarding frontend and Go company-onboarding service |
| Customer roster/card mappings and provider account reconciliation CLI | Shared roster readers, onboarding and trusted mapping consumers |
| CRM snapshots, evidence review and legacy provider profile publication | Existing snapshot API and read-model workflows |
| Read-only `/api/ledger` | Legacy browser client and historical Node projections |
| Database migration and guarded seed commands | Historical schema and existing onboarding/development tooling |
| Shared frontend policy calculations | Existing fixture/simulation UI, not Funding execution authority |

The legacy browser `BridgeApiClient` still represents the retired financial
contract. Its old execution calls receive 410, and its ledger read remains a
historical Node view. Browser integration with Go needs explicit actor and
consent wiring; it is not implemented by redirecting old requests.

## Remaining cutover work

- Inventory and reconcile any existing Node debt, reservations and unresolved
  provider operations before changing their running writer or callbacks.
  Preserve operation/request identities. Go's current known-empty DEMO import
  does not migrate a nonempty Node financial book.
- Move remaining browser callers to the Go offer/consent contract in a separate
  frontend integration change. Update provider callback routing only after
  the relevant contracts and execution scope are verified.
- Replace the retained onboarding/session/CRM/roster/tooling responsibilities
  before removing the entire Node process or its Compose/Docker entrypoint.

This source cleanup does not stop a running old Node deployment or perform a
financial data cutover. The existing Compose service stays because it still
serves the retained compatibility responsibilities.

## Verification

- `npm run server:build`: passed after removing stale compiler output.
- `npm run build`: frontend type-check and production bundle passed.
- Focused Vitest run: 121 tests in 14 files passed, including retired commands,
  signed event ownership, onboarding proxy/docs/session/forwarding, historical
  ledger reads, snapshots, roster reconciliation, provider reads and lifecycle.
- A synthetic stale output file was removed by the build. The compiled retired
  `policy-approval` command exited 1 before configuration/database work.
- No source imports of deleted modules or remaining Node funding/collection
  writers were found. Historical migration files have no diff.

No database-backed integration tests, live provider calls, deployment or
running-process changes were performed for this retirement slice.
