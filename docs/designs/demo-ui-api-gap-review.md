# Demo UI and Go API gap review

Review complete against local `develop` commit `c54f9f0`, November 15, 2026.
Implementation has not started. This document records recommendations for the
original Bridge Capital interface, previously served on port 5173.

Ramil's direction: preserve that interface's design and navigation, connect it
to the tested Go services, and keep `BrowserGatewayApp` as an operator-only
administrative/developer interface.

[TOC]

## Recommendation

Keep the existing shells, visual tokens, cards, tables, charts and onboarding
components. Replace their fixture state and financial behavior with typed Go
reads and commands. Introduce small, authorized read projections where the
screens lack a source. Keep missing wallet/card capabilities visible as
unavailable or outside the first demo.

The existing `DataAdapter` grants the browser responsibilities such as
`saveDecision`, `saveBridge`, `setOutstanding` and `disburse`. Implementing that
interface over HTTP would preserve the wrong ownership model. Use screen
query hooks and the existing financial command client instead.

The core action APIs are implemented. Most work concerns presentation, state,
identity and discovery. API expansion should concentrate on reads; avoid new
financial writers, policy engines, customer registries or infrastructure.

## Component coverage

| Component or screen | Current behavior | Required change before Go wiring |
| --- | --- | --- |
| `App`, `LoginPage`, `BackOffice`, `Portal` | The global Go flag replaces the whole app. Legacy login uses the Node token endpoint. Portal has a customer picker and both shells can switch roles visually. | Separate API selection from presentation selection. Use real login, restored session, role-specific entry and logout. Gate the developer interface with the existing OPERATOR role. |
| `useBridgeData`, `useBackOffice`, `usePortal` | Load a complete mock bank dataset and mutate local maps, approvals and outcomes. Missing adapters silently instantiate `MockAdapter`. | Replace fixture behavior with independent typed reads and explicit commands. Preserve durable IDs, bindings, versions and recovery. Surface source errors without mock fallback. |
| `OverviewScreen`, `CapitalGauge` | Rank requests and calculate capital, fees and delinquency in TypeScript. | Render Coordinator case/evaluation status and a Funding-owned portfolio summary. Preserve the layout, remove claims that the UI ranking authorizes or reserves funding. |
| `CustomersScreen` | Lists generated customers and computes tiers, limits and exposure. | Add a bounded operator customer projection with display metadata, saved assessment fields and current servicing exposure. Keep onboarding company identity separate from financial customer identity. |
| `RequestDetailScreen`, `ApprovalPanel`, `RuleList` | Evaluate locally, build/reprice offers, show boolean rules and synthetic CRM/history. Approve, decline and escalate update local state. | Read saved assessment, render PASS/FAIL/UNKNOWN, create an offer from its evaluation and case IDs, and approve its exact hash/version. Disable unsupported decline/escalation and term amendment actions. |
| `ApplicationsScreen`, `ApplicationDetailScreen`, onboarding wizard | Already use the onboarding API, revisions, readiness and submissions. | Reuse these components inside the preserved shell with Go session context. Handle existing unsupported internal rejection/information-request actions and local upload/provider limitations explicitly. |
| `DeclineScreen`, `TermPicker` | Customer computes eligibility, fees and arbitrary term variants in the browser. | Show a server-created offer and fixed saved terms. Use the server-selected 30-day flow initially. The gateway has no customer term-change/reevaluation command. |
| `AcceptedScreen` | Customer triggers fixture disbursement through a button promising card retry. | Show accepted/waiting/queued/settled states. Funding stays an operator command in the current API. Actual card retry remains outside this demo. |
| `WalletHome`, `AccountScreen`, `TransactionScreen`, `ActivityScreen` | Use generated balances, bank/card history, derived line information and fabricated card last-four digits. | Add only sourced fields. Current wallet balance, complete card/bank activity and card metadata have no gateway contract. Display historical observations with their timestamp and meaning, or omit unavailable sections. |
| `BridgesScreen`, `BridgeDetailScreen`, `ActiveBridgeScreen`, `Timeline` | Read all local bridges and simulate late status/accrual and timeline events. | Add customer bridge discovery. Read current debt, lifecycle, delinquency, holds, operations and collection allocations from Funding, with original terms from the linked offer. |
| `RulesScreen`, `AdminScreen` | Render and edit mutable TypeScript configuration. | Show saved rule evidence and policy identity read-only. A full current policy catalogue needs a narrow server metadata contract. Keep fixture policy controls in the explicitly isolated developer area. |
| Existing Go clients and action components | Already implement authenticated reads, constrained DEMO writes and command recovery. | Reuse their behavior and validation, then restyle their presentation to match the original interface. |

## Blocking contract mismatches

### 1. Session and role ownership

`src/App.tsx:16` uses one flag to choose between the entire developer app and
`LegacyApp`. `LoginPage.tsx:15` calls `/api/kit5/session`, while Go uses
`/api/session/login`, `/me` and `/logout` with username/password sessions.
`Portal.tsx:67` exposes a customer selector. `usePortal.ts:54` defaults to the
first generated customer. These cannot select authenticated customer scope.

Reuse `BrowserSessionClient` and `BrowserAccess`, preserve the original login
styling, and bind the portal to `/api/servicing/session`. A hash or a customer
picker must not change the authenticated role/customer. Retain the developer
surface under a distinct route, using the same role checks. These routes are
recommendations, not implemented changes.

### 2. Browser policy and server policy have different contracts

`RequestDetailScreen.tsx:75`, `DeclineScreen.tsx` and `OverviewScreen.tsx:40`
call browser policy. `CustomersScreen.tsx:53` derives tiers from generated
history. The real assessment contains controlled reasons and
PASS/FAIL/UNKNOWN rules, policy hash, snapshot versions and nullable values.
`RuleList.tsx:5` supports only a boolean. Portal guidance uses old `R1_*` rule
identifiers rather than the Go evaluator's ten rule IDs.

Render saved rule results and distinguish REVIEW_REQUIRED from INELIGIBLE.
Keep assessment values labeled as evaluated-at facts. Funding must recheck
current exposure/capital. Use its current ledger for debt and reservations.
An eligible assessment and an approval both remain distinct from settlement.
Do not carry TypeScript scarce-capital ranking into the live command path;
Funding currently refuses unsupported TIGHT/CRITICAL execution.

### 3. Acceptance, approval and funding are separate commands

`usePortal.ts:75` creates its own offer, then saves acceptance without the
server terms hash/version. `useBackOffice.ts:62` supplies a hardcoded approval
actor. Those methods do not correspond to current Go authority.

Reuse `FinancialCommandClient`: create from saved evaluation/case, customer
acceptance with hash/version, operator approval with hash/version/reason, then
operator funding with expected version. The gateway explicitly reserves
acceptance for CUSTOMER and other financial commands for OPERATOR
(`internal/httpapi/commands.go:282`). It supplies actual actor identities.

A customer cannot call the current funding command. Preserve the accepted
screen visually, but show waiting for processing and then the saved bridge.
An existing backend automatic-approval path does not turn customer credentials
into operator funding authority.

### 4. Card retry is not implemented

`AcceptedScreen.tsx:125` promises a payment retry. `usePortal.ts:108` creates a
local `bridge_*` ID and marks it DISBURSED before calling the mock adapter.
`MockAdapter` later appends a notice saying the card payment was approved.
The real Go flow settles a transfer and services the bridge; it has no card
retry command in the gateway.

Replace this action/copy with the actual acceptance and funding progress.
Display a card retry result only if a separate provider-backed capability is
implemented and verified. Do not synthesize a successful card purchase after
funding.

### 5. Customer bridge discovery is missing

`GET /api/customer/offers` returns up to 50 IDs from successful gateway create
receipts. The customer can read a known bridge UUID, but cannot use the
operator-wide change feed or Coordinator case list. The offer detail has no
bridge ID. `CustomerFinancialWorkspace.tsx` consequently asks the user to paste
a bridge UUID. The polished portal needs discovery after reload and on another
device, without sharing operator recovery history.

Add a customer-scoped, paginated bridge/financing read in the owning service,
with offer linkage and original terms. Authenticate it through the gateway's
existing verified customer binding. Keep backend scope checks on every read.
A read projection is sufficient; no new financial state machine is needed.
Customer decline explanations also need a sanitized customer-owned view if
shown before an offer exists; broad Coordinator access stays operator-only.

### 6. Portfolio and customer reads need a narrow browser projection

The gateway currently exposes cases, changes and known offer/bridge/operation
reads. It has no customer directory, portfolio aggregate, customer context or
policy catalogue routes. Offer/bridge IDs can be discovered by replaying the
operator change feed, with pagination and deduplication, but that feed alone
does not supply every original dashboard field.

Funding already exposes `/api/servicing/state?customer_id=...` to its dedicated
UNDERWRITING_READER identity. It returns current facility/customer accounting,
receivables and bridge history in a read snapshot. Underwriting separately has
`/api/underwriting/customers/{customer_id}/context` with captured, normalized
history and coverage. These are existing internal contracts, not browser
routes. Do not forward their credentials or raw account/card fields.

Add bounded operator read projections using appropriate server-side identity:
current facility/portfolio summary and customer directory/detail. Source fees
collected from settled accounting/allocations, including reversals, rather
than assuming original fee minus outstanding fee equals revenue. Mark history
coverage and assessment freshness. Display metadata must use explicit stable
ID mappings; a saved `comp_*` onboarding draft does not identify an activated
servicing customer.

### 7. Money, IDs and states cannot be cast into legacy types

The legacy `usd` formatter accepts dollars; its `cents` option controls decimal
places, not monetary units (`ui/format.ts:3`). Go returns integer cents.
Passing `700000` directly would display $700,000 instead of $7,000. Reuse the
integer-cent formatter in `src/services/financial/client.ts:205`, retain safe
integer validation and keep unknown amounts nullable.

Replace generated `offer_*`/`bridge_*` identifiers with saved UUIDs and explicit
case/evaluation/offer/bridge relationships. Backend bridge lifecycle,
delinquency and operation certainty are separate fields. Add RESERVED,
FUNDING_PENDING, ACTIVE, VOID_UNFUNDED and REOPENED_HELD presentation alongside
operation PENDING/UNKNOWN/HELD. Preserve server UTC dates and timestamps;
remove the fixed fixture `AS_OF` from API-connected paths.

### 8. Async outcomes need durable UX

Legacy outcomes are React state. A funding HTTP 202 is a queued reservation;
settlement follows asynchronously. Collections can change while a detail page
is open. `useBridgeData.ts:45` loads everything with one `Promise.all`, and its
initial effect has no error state for a rejected read.

Use independent screen queries with retry/error/empty/unauthorized states,
bounded polling while work is pending, fresh version reads before a command,
and collection pagination bound to the displayed bridge version. Reuse durable
command history and original-key recovery. Never automatically issue a new
money command because a response was lost. Retain the user's entered values
when authentication or a source becomes unavailable.

## Capabilities to limit explicitly for this demo

- Wallet balance history is not a current provider balance. The old wallet uses
  the last history row, defaulting to zero. Show a labeled historical value or
  unavailable state until a scoped current-balance source exists.
- Complete bank/card activity, merchant details, card last-four digits, payer
  reliability and live CRM notes are not supplied by the current gateway.
  `TransactionScreen.tsx:11` fabricates last-four digits from a card ID. Omit
  missing facts; do not backfill them from unrelated generated customers.
- Coordinator investigation commentary can be reused with source/verification
  labels. Pending evidence does not establish verified financial facts.
- Late accrual and debt come from Funding. Copy promising card freezing,
  notifications or sent reminders must reflect verified execution, not just
  frontend delinquency rules.
- The current gateway flow does not amend an offer's term or price. Use its
  saved 30-day offer initially; custom terms require a separate evaluated,
  versioned offer flow and renewed bindings.
- Operator decline/escalation commands for financial cases are not exposed.
  Internal onboarding rejection and outgoing information requests are already
  explicitly unsupported in `ApplicationReviewActions.tsx:82`.
- Real user login does not imply live provider execution. Keep synthetic
  source/provider/agent labels, mode and source freshness visible.
- Local sources expire after five minutes. A fresh-book preparation command
  belongs to developer setup, while ordinary UI refresh only rereads records.
  Never refresh timestamps or reset an active financial book from a UI reload.
- Public onboarding documents remain at the separate Go docs route for now.
  Existing upload/storage and provider-worker availability need truthful UI
  states. Customer self-service onboarding is outside the current
  operator-only gateway routes.

## Smallest implementation sequence

1. Separate presentation selection from backend selection. Restore the
   original shells, add real session/role guards, and preserve the developer
   interface as a distinct operator surface. Keep both current listeners and
   their data unchanged until replacement acceptance.
2. Pin screen DTOs: cents, nullable fields, saved IDs, tri-state rules,
   lifecycle, source mode, freshness and versions. Add the customer bridge
   discovery and operator portfolio/customer projections. Record proposed
   routes before implementation; the review does not claim they exist.
3. Wire operator Overview, request detail, Applications and bridge detail to
   real Go reads. Keep unavailable context explicit. Reuse the existing
   command client for create/approve/fund/review/reconcile.
4. Wire the customer portal's offers, exact acceptance, active bridge and
   repayment history using customer-scoped reads. Preserve the existing visual
   cards and navigation, replace the customer picker with the actual session.
5. Make Rules read-only against saved policy identity/evidence. Expose a full
   versioned policy catalogue only if needed for the demo. Keep editable
   TypeScript policy controls isolated from the connected financial flow.
6. Verify the original interface against a single fresh owned book: login,
   list/detail, offer creation, distinct customer acceptance, operator
   approval/funding, pending-to-settled transition, fee-first repayment and
   zero-debt closure. Include reload/logout, foreign-customer denial, changed
   version, UNKNOWN recovery and stale-source refusal. Capture the preserved
   UI and keep the developer console available for diagnosis.

## Review evidence and limits

This is a source/contract review. No application source, listener, database,
credential, runtime profile or existing plan was changed. No tests or browser
journey were rerun for this review. The earlier local browser proof exercised
`BrowserGatewayApp`; it does not establish acceptance of the original UI.
The only new file is this report.

Primary source groups inspected: `src/App.tsx`, `app-routing.ts`, onboarding
session/forms and Applications components, Bridge Capital shells/hooks/screens,
`data/adapter.ts`, `MockAdapter.ts`, policy/display types, financial/session
clients, gateway HTTP/auth/command routes, Coordinator public case DTOs,
underwriting context/evaluation DTOs, and Funding offer/bridge/servicing reads.
Existing designs and requirements were checked; some requirement wording still
predates the delivered real-session and Go service slices. Running code is the
endpoint authority for this review.
