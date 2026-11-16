# Original demo UI connected to Go

Status: LOCAL DEMO IMPLEMENTED and verified in `feat/demo-ui-go`; local
integration and archive bookkeeping follow. Actual Airwallex sandbox acceptance
is a subsequent goal.
Prepared against local `develop`
`c54f9f0`, November 15, 2026.

Design authority: [UI/API gap review](/docs/designs/demo-ui-api-gap-review.md).
Reuse the original Bridge Capital interface visible on port 5173. Keep
`BrowserGatewayApp` as a distinct operator developer interface. This plan
covers local DEMO acceptance; deployment and live provider execution remain
separate.

[TOC]

## Completion criteria

The original back-office and portal designs run against the same Go services
and real sessions as the verified developer interface. Distinct customer and
operator accounts complete offer creation, exact acceptance, approval,
funding, fee-first repayment and zero-debt closure through the original UI.
Customer bridges are discoverable after reload, without pasting UUIDs.

Preserve the existing navigation order, typography, colors, spacing, tables,
wallet cards and onboarding components. Add states and fields needed for API
truthfulness. A new minimalist financial dashboard does not satisfy this plan.

The implementation agent addressed by Ramil owns execution, tests and handoff.
Do not spawn agents or add review layers without a new explicit instruction.
This planning request does not start implementation, provision data or change
any existing listener.

## Decisions fixed for this implementation

| Concern | Decision |
| --- | --- |
| Product entry | Keep landing, styled login, `#bridge-capital`, `#portal` and `#onboarding`; support hash changes, Back/Forward and direct reloads. |
| Developer entry | Add `#developer` under the same real session; OPERATOR only. The product Admin navigation opens this developer area. Provide a return-to-product action. |
| Authentication | Reuse real Go username/password sessions and verified immutable servicing bindings. Remove the product customer picker and visual role switching. |
| Financial terms | Use server-created 30-day offers. Render exact saved principal, fee, due date, hash, revision and expiry. No browser repricing or custom-term command. |
| Authority | CUSTOMER accepts; OPERATOR creates offers, approves and requests funding. Funding service retains all economic guards and settlement authority. |
| Data layer | Screen-specific typed queries and existing financial commands. Do not implement the legacy financial `DataAdapter` over HTTP. |
| Unsupported capabilities | Card retry, live bank/card activity, current wallet balance, live CRM, card restrictions and message delivery stay explicit unavailable/deferred states. |
| Rules | Read-only saved rule results, policy ID/hash and evaluated-at values. No new full policy catalogue or policy-edit API in this slice. |
| Sources | Actual Go API/worker/ledger with synthetic shortfall/provider/agent fixtures. No successful card purchase inferred from a transfer. |
| Infrastructure | Existing PostgreSQL and service binaries; no new service, broker, cache, framework, dependency or hosted operation. |
| Preview and cutover | Preview the preserved product on 5176 first. Keep current 5173 reference and 5175 developer instance intact until the replacement is accepted. |

## Scope and preservation

- New worktree: `.worktrees/demo-ui-go`, branch `feat/demo-ui-go`, from current
  local `develop`, not another feature branch. Record the actual execution
  base; it may advance beyond this planning snapshot.
- Read `AGENTS.md`, the gap review, browser-gateway design and current service
  contracts first. Bring these exact approved plan/design inputs into the
  worktree if still untracked; record hashes and stage only owned documents.
- Preserve root `.env`, Test Inc. records, all existing manual financial books,
  the consent backlog, safety-script edits and other uncommitted plan audits.
- Preserve schema history. Read-only additions should need no migration. If a
  measured query needs an index, use a new numbered owner migration. Do not
  rewrite existing migrations or add a duplicate customer/offer registry.
- Keep fixture policy and `MockAdapter` available to their focused tests or an
  explicitly isolated fixture tool. They must be unreachable from connected
  product reads/actions and never become an error fallback.
- During development keep the old listeners untouched. For the final owned Go
  runtime, stop/restart only the known demo supervisor using its documented
  control, retaining its data. Never kill an unknown port occupant.
- No push, cloud deployment, callback switch, real financial/provider/model
  call, dependency installation or Node-process retirement is included.

## Read contracts to add

All routes in this section are proposed new routes, not current capabilities.
Document final DTOs in `docs/api/demo-ui-read-models.md` during Task A before
writing dependent frontend code. Keep these names unless actual source reveals
an endpoint collision; record any necessary change in that contract and plan.

### Owner and browser route mapping

| Browser gateway route | Funding owner route | Required principal |
| --- | --- | --- |
| `GET /api/operator/portfolio` | `GET /api/servicing/portfolio` | Active verified OPERATOR actor, configured mode/facility/installation. |
| `GET /api/operator/customers?cursor=...` | `GET /api/servicing/customers?cursor=...` | Same OPERATOR binding. |
| `GET /api/operator/customers/{id}` | `GET /api/servicing/customers/{id}` | Same OPERATOR binding; configured book only. |
| `GET /api/operator/customers/{id}/bridges?cursor=...` | `GET /api/bridges?customer_id={id}&cursor=...` | Same OPERATOR binding. |
| `GET /api/customer/bridges?cursor=...` | `GET /api/bridges?cursor=...` | Active verified CUSTOMER; Funding infers customer from its registry. |

Use existing bound actor assertions and distinct customer/operator signing
keys. Do not grant these routes to an ordinary Coordinator reader, use an
underwriting-reader secret in the browser, or widen existing reader roles.
The gateway has no financial database access. It forwards only explicit routes
and validates upstream shape, mode, identity and monetary units.

### Common read semantics

- Use GET only, `Cache-Control: no-store`, existing request/response limits and
  controlled storage/unavailable errors. Retain production readiness checks.
- Include `source_mode`, `source_binding_id`, `as_of` and applicable record
  versions. No tokens, raw provider/card IDs, actor secrets or source documents.
- Money is integer USD cents; nullable means unknown. All arrays encode as
  arrays, including empty results. Reject unsafe JavaScript integer values.
- Lists use 25 rows and an opaque canonical cursor. Sort customers by stable
  customer ID; sort bridges by `(created_at DESC, id DESC)`. Bind cursors to
  mode, facility, installation, route and exact customer scope. Validate the
  shape and scope on every page; reject malformed or foreign cursors.
- CUSTOMER requests cannot select a customer, mode or facility in query/JSON.
  Reject supplied scope selectors rather than treating them as authority.
  Existing known-ID offer/bridge/operation reads retain owner scope checks.
- Use read-only repeatable-read transactions for each aggregate/list snapshot.
  No HTTP/provider call within a DB transaction and no financial write on read.
- Snapshot pagination is a live view, not an export guarantee. Detail/version
  reads remain the authority when a row changes between pages.

### Portfolio DTO

Return one configured facility with total cents, reserve BPS, reserve cents,
funded principal, reserved principal, other commitments, available capital,
version and current capital regime. Include lifecycle counts, hold count,
current principal/fee debt, and `fees_collected_cents` with explicit coverage.

Reuse authoritative facility columns and checked arithmetic. For current
capital description, add a small public pure wrapper in the existing
underwriting `policy` package around its existing `facilityAvailable` and
`deriveRegime` helpers. Proposed signature:

```go
func DescribeCapital(input FacilitySnapshot, config Config) CapitalDescription
```

`CapitalDescription` has `Known bool`, `ReserveFloorCents *int64`,
`AvailableCapitalCents *int64` and `Regime *CapitalRegime`. Invalid or unknown
input returns `Known=false` with nil values. It performs no I/O or eligibility,
approval or funding decision. Expose it through the existing Funding evaluator
wrapper using its already loaded validated config.

Use the existing Funding normalization of other commitments into policy
facility inputs; never subtract them twice. Reuse ceiling reserve rounding,
checked arithmetic and zero-floor behavior. Do not change policy JSON,
thresholds, ID/hash or the results of `Evaluate`. Update dependency provenance
through the repository's existing local module procedure, without inventing a
commit hash before the source exists.

For fees, sum owner-verified settled CONTRACT_FEE/LATE_FEE allocations net of
verified reversals/refunds, under the configured facility. Label this value
"Fees collected", not earned revenue or facility cash. Do not subtract
provider expenses from this specific metric or infer it from original fees
minus unpaid fees. Reconcile the read against existing ledger effects. An
incomplete opening book gives a null total and explicit coverage, not zero.

The portfolio contract uses `facility_id`, `facility_version`,
`facility_total_cents`, `reserve_bps`, `reserve_floor_cents`,
`funded_principal_cents`, `reserved_principal_cents`,
`other_commitments_cents`, `available_capital_cents`, `current_regime`,
`principal_owed_cents`, `unpaid_fee_cents`, `fees_collected_cents`,
`history_complete`, `lifecycle_counts`, `held_bridge_count`, `policy_id` and
`policy_hash`, plus the common source/time fields. Derived/coverage-dependent
values are nullable. Lifecycle counts count actual local records at `as_of`.
No snapshot timestamp from a separate service is presented as this read time.

### Customer and bridge DTOs

Customer directory/detail returns stable `customer_id`, active mapping state,
nullable display label, accounting availability, current principal/unpaid fee
and reserved principal/fee cents, history coverage, holds, version/as-of and
nullable active bridge ID. No customer row is enrolled by a GET.

Build the directory from actual mappings and servicing records in the
configured installation. A mapped customer without accounting has
`accounting_available=false` and null financial values. A Coordinator-only
customer can appear in the request queue without pretending to have a funded
servicing record. Do not require all three assessment fixtures to be funding
customers.

For friendly names, use a tiny checked-in DEMO display-label map keyed by exact
known customer IDs. Apply it only to matching DEMO records; unknown labels
fall back to their stable ID. Labels never bind accounts or grant authority.
There is no name/email join with onboarding `comp_*` records.

Bridge lists return saved IDs/linkages, lifecycle, version, funded/created/due
times, debt buckets and hold reasons. Include original principal/fee/term
from the exact linked immutable offer. A customer must discover both open and
repaid bridges after a fresh login, including multiple historical attempts;
do not assume one bridge per offer across VOID_UNFUNDED history.

Use existing detail and collection endpoints for operation certainty,
authority, debt/accrual times and version-bound allocations. Show original
terms from the linked offer and current debt from Funding, never from a
reconstructed local bridge.

Tier/limit information may come from the latest saved Coordinator assessment
for that customer, with evaluation ID/time and snapshot versions. If no such
assessment is loaded, show unavailable. Do not recalculate a standing line or
join an unrelated case. Rich historical charts/CRM are deferred when the
current safe API does not provide them.

## Existing commands to reuse unchanged

| User action | Existing route and payload |
| --- | --- |
| Operator creates offer | `POST /api/offers`, `{evaluation_id, originating_case_id}` |
| Customer accepts exact terms | `POST /api/offers/{id}/acceptances`, `{terms_hash, expected_version}` |
| Operator approves exact terms | `POST /api/offers/{id}/approvals`, `{terms_hash, expected_version, reason_code}` |
| Operator requests funding | `POST /api/offers/{id}/fund`, `{expected_version}` |
| Operator requests collection review | `POST /api/bridges/{id}/collection-reviews`, `{expected_version, reason_code}` |
| Operator requests reconciliation | `POST /api/operations/{id}/reconcile`, `{expected_version, reason_code}` |

Reuse `FinancialCommandClient`, gateway durable intent/receipts and the same
saved key/payload for unknown-outcome recovery. Create keys once per explicit
attempt. Do not translate fixture `saveBridge`, `saveOffer`, `setOutstanding`,
`disburse`, `chargeDeposit` or hardcoded actor/timestamp values into commands.

## Implementation tasks

### A. Contract and baseline

Files: the new API contract, this plan, routing/session definitions and
existing service/frontend tests. No implementation changes in this task.

- [x] Record execution base, branch, worktree and protected-file hashes.
- [x] Read current source to verify route collisions, permission helpers,
  installed module pinning and current table/ledger semantics.
- [x] Write the new API contract with field names/types/nullability, cursor
  scope, auth matrix, 400/401/403/404/409/503 behavior and fixture labels.
- [x] Record reusable UI components, retained Node/fixture boundaries and the
  list of components whose hooks/props will change. No redesign is proposed.
- [x] Capture baseline screenshots of the original operator and customer
  layouts using an isolated browser. Do not modify manual data to do so.
- [x] Run only relevant baseline frontend and service checks, recording actual
  failures/skips separately. Do not run shared truncating DB tests.

Acceptance: concrete read contracts match the reviewed source and all later
screens have an explicit field source or explicit unavailable state.

### B. Funding-owned read projections

Files: `services/underwriting-engine/policy/capital.go` (new wrapper), Funding
evaluator wrapper, and owner HTTP `server.go`, `auth.go`, `portfolio.go`,
`customers.go` and `bridge_list.go` (new read files where absent). Add focused
tests in those existing packages.

- [x] Add the pure capital-description wrapper and prove parity with existing
  policy helpers for normal, floor, overcommitted, fractional-reserve and
  invalid/overflow cases. Verify policy JSON/hash and `Evaluate` unchanged.
- [x] Implement portfolio reads with facility scope, coherent snapshot,
  current commitments, lifecycle/hold counts and exact fee coverage semantics.
- [x] Implement customer list/detail and bridge list, using active caller
  identity, exact mode/facility and explicit nulls for absent accounting.
- [x] Infer customer scope for CUSTOMER. Permit OPERATOR customer filtering
  only within the configured book. Refuse broad reader or foreign cursors.
- [x] Update both ServeMux registration and `fundingRouteMethods`; cover
  malformed/trailing paths, unsupported methods and strict query keys.
- [x] Keep old APIs compatible. Confirm no runtime grant expansion or DDL is
  required; add a forward index migration only if justified by an actual query.
- [x] Test reads against real owner tables: no bridge, reserved, funded,
  partial collection, repaid, VOID_UNFUNDED/retry, verified reversal, incomplete
  history, and a concurrent version change. Reuse existing fixtures/helpers;
  do not add another exhaustive financial journey.

Acceptance: reads are authorized, exact and read-only; existing funding,
collection, reversal and policy tests still pass.

### C. Gateway routes and typed browser clients

Files: gateway HTTP/config/servicing client packages; new explicit
`internal/httpapi/product_reads.go`; `src/services/financial/product-reads.ts`
and `product-models.ts`; existing typed clients and focused frontend tests.

- [x] Register only the proposed exact routes and methods; use the verified
  actor binding/signing key for each new upstream read.
- [x] Map operator/customer paths explicitly. Reject user-supplied identity,
  scope and authorization headers. Never query another service's DB directly.
- [x] Add bounded typed decoders for all new read DTOs, nullable money, arrays,
  mode, customer linkage, versions and cursors. Extend existing types for
  already returned `tier` and `funded_at` fields needed by the original UI.
- [x] Keep collection pages matched to the displayed bridge version. A 409
  requires a fresh bridge and restarted collection pagination.
- [x] Add the DEMO display-label map with no private/source credentials and
  no enrollment, mapping or financial meaning.
- [x] Add customer/role/foreign-ID and cursor denial tests through the actual
  gateway and owner handler. Test 503 storage/upstream errors separately
  from 401, preserving a valid session on source failure.

Acceptance: the browser can discover its own bridges after relogin, operator
pages can read portfolio/customers, and every foreign-customer read is denied.

### D. Product root, real login and reusable view models

Files: `src/App.tsx`, `src/app-routing.ts`, onboarding session/login components,
new product session/query hooks, existing back-office/portal shells and hooks.

- [x] Separate the API-selection flag from the choice of visual surface.
  With Go selected, original shells are the product. `#developer` mounts the
  retained developer workspace only after OPERATOR authorization.
- [x] Reuse the original login design with username/password, me/restore,
  logout, expired-session and service-unavailable states. Do not use the old
  Node token login for the connected product.
- [x] Handle initial hash, hash changes, browser navigation and direct reload.
  Guard every protected entry before mounting data-loading components.
- [x] Route OPERATOR to the back office; route CUSTOMER to their own portal.
  Direct access to another role's hash gets a clear denial or their own home.
  Remove the customer picker and role-switch links from the connected product.
- [x] An unlinked customer gets an account-not-linked state. Public landing
  remains public; the current onboarding workflow requires operator login.
- [x] Replace the aggregate mock loader with per-screen loading/error/empty
  models. Clear prior-user records on session change and ignore/cancel stale
  in-flight responses. No persistent customer record cache across sessions.
- [x] Use integer-cent formatters throughout connected cards and tables.
  Preserve null/unknown and server dates; remove `AS_OF` and generated IDs.
- [x] Implement read polling every three seconds only while a relevant view is
  visible and work is pending. Stop on unmount/logout/hidden tab, use bounded
  backoff on failures and preserve displayed records with a stale warning.
  A refresh is always a read and never a financial retry.

Acceptance: role/session navigation survives reload without losing the old
visual layout, leaking records or falling back to fixture data.

### E. Operator screens

Files: back-office shell/hooks, Overview, Customers, RequestDetail,
BridgeDetail, Rules, Admin entry and shared visual components.

- [x] Populate Overview's gauge/metrics from the portfolio read. Render the
  Coordinator queue with saved statuses, stable pagination and declared sort.
  List rows lack full assessment amounts: fetch details only for the visible
  page, at most four concurrent requests for its 25 cases. Key transient cache
  entries by user, case ID and updated time; cancel discarded pages. A detail
  failure leaves its row visible with unavailable fields. Customer-filtered
  case reads use the existing owner `customer_id` query and bound cursor.
  Remove client policy ranking and any implied reservation/auto-funding claim.
- [x] Populate Customers from the new directory/current accounting and exact
  same-customer saved assessment. Label assessed values and their timestamp.
- [x] Bind request detail to a saved case/evaluation. Reuse RuleList styling
  with PASS/FAIL/UNKNOWN marks and mapped friendly reasons plus code/details.
- [x] Reuse existing cards for offer/terms and create/approve/fund controls.
  Show exact expiry/hash/version; disable new actions until the prior result
  is reconciled and current saved state is refreshed. Respect owner refusal.
- [x] Remove in-memory approval/outcome changes. Financial decline/escalate
  and custom-term controls stay disabled with an accurate explanation.
- [x] Populate bridge detail from actual bridge/offer/operations/collections.
  Lifecycle, delinquency, holds and certainty remain separate. Read accrual
  and debt from Funding; do not recompute late fees or assume messages sent.
- [x] Render Rules as saved policy evidence and ID/hash. Move the Admin entry
  to the developer workspace; no product control mutates TypeScript policy.
- [x] Keep original chart/context positions where useful, with sourced values
  or a compact unavailable state. Do not fill missing CRM/history with seeds.

Acceptance: the operator can review real cases, create and fund a saved offer,
then inspect actual settlement and collection history in the original design.

### F. Customer screens and onboarding reuse

Files: portal shell/hooks, WalletHome, Account, Decline, Accepted, Bridges,
ActiveBridge, Activity, Transaction, guidance and existing onboarding screens.

- [x] Show the verified session identity instead of the customer picker.
  Load actual own offers using the existing index and own bridges using the
  new paginated discovery route. Poll the visible offer list so operator-created
  offers appear without a full-page reload.
- [x] Adapt Decline/offer cards to exact server terms. Disable TermPicker and
  show the saved 30-day term. Never create/reprice an offer from the portal.
- [x] Require an explicit acceptance gesture and display principal, fee, total,
  due date, expiry and disclosures. Bind it to the exact hash/version. A stale
  version requires rereading and reviewing terms before a new acceptance.
- [x] Adapt AcceptedScreen to waiting for operator approval/processing,
  RESERVED/FUNDING_PENDING, ACTIVE, HELD/UNKNOWN and confirmed failure states.
  Remove the card-retry button and claims that the card purchase succeeded.
- [x] Keep wallet/account styling, showing bridge/offer/repayment summaries.
  Current wallet balance and full bank/card history remain unavailable.
  Any historical balance says when and what was observed, never current cash.
- [x] Build bridge activity/timeline from actual funding/collection facts and
  allocations. Describe planned versus settled amounts explicitly. Do not
  relabel servicing history as a complete bank statement or invent last-four
  digits, merchant metadata, notices, restrictions or customer default actions.
- [x] Update every old `R1_*` guidance mapping to controlled Go rule IDs where
  available. Keep operator-only assessments private; a customer without an
  offer gets an honest no-offer state, not a broad Coordinator read.
- [x] Reuse Applications list/detail and wizard unchanged where compatible,
  inside the real OPERATOR session. Preserve revision checks, person IDs,
  pagination, same-key submission recovery and draft state during re-login.
- [x] Keep internal onboarding reject/information-request controls explicitly
  unsupported. Submission queueing is not provider approval. Upload/storage
  and provider-worker limits stay visible; no live submit is needed for proof.

Acceptance: the customer sees only their financing after reload, explicitly
accepts exact terms and observes settlement/repayment without manual IDs.
The operator can still use the existing onboarding pages and save a draft.

### G. Build, runtime preparation and visual checks

Files: package scripts/Vite configuration, owner runbooks, local ignored
runtime helpers and the final original-UI verification guide.

- [x] Provide a strict loopback product preview on 5176 and a separate product
  build command, without changing existing running 5173/5175 listeners.
  Proxy `/api` to the explicitly configured Go gateway, preserving Origin.
- [x] Configure exact 5176 origins only in the new owned gateway profile.
  Preserve strict Origin checks, host-only cookies and distinct actor keys.
  Do not broaden origins to wildcards or inherit provider credentials.
- [x] Build all affected service binaries and record source/module hashes
  before preparing the fresh five-minute book. Prepare launcher, browser
  actions and capture paths before consuming that window.
- [x] Use the existing local demo lifecycle as the starting point, not a new
  orchestration platform. When the actual proof needs fixed service ports,
  stop only its known owned supervisor, retain its books and prepare a fresh
  marked profile through normal owner migrations/imports/invitation/bindings.
  Do not change the old Node/manual 5173/8000/8001 processes or Test Inc. data.
- [x] Keep source timestamps, known-empty provenance and configured installation
  consistent across all services. Expired shortfall feeds can stop while
  saved reads and servicing continue with honest degraded readiness.
- [x] Compare baseline and connected screenshots at desktop and portal/mobile
  widths: original shell, colors, typography and component hierarchy remain.
  Check navigation, empty/error states, focus, labels and keyboard acceptance.

Acceptance: a reproducible preview serves the original design with actual Go
reads and commands, isolated credentials and all fixture/live limits visible.

### H. Final verification and handoff

- [x] Run targeted frontend contract and component tests for money, unknown
  rules, routing/session changes, scope, fixed terms, version conflicts,
  discovery, delayed settlement and recovery. Reuse Vitest and existing tools.
- [x] Run gateway `go test -race ./... -count=1` with its existing private admin
  URL-file convention and unique marker-owned DBs/logins. Preserve prior auth,
  onboarding, command and negative-authorization assertions.
- [x] Run Funding affected read/contract tests and existing financial regression
  suite using its private test-admin URL-file convention. Run the changed
  public policy tests; build/vet affected Go modules and run gofmt checks.
- [x] Run TypeScript, relevant lint, frontend tests and both product/developer
  builds. Report toolchain limitations and unrelated failures exactly.
- [x] Perform one actual original-UI browser journey on the fresh profile:
  operator login/restore, Applications list and synthetic draft save, eligible
  case/offer, customer relogin/acceptance, operator relogin/approval/funding,
  queued-to-settled transition and customer bridge discovery after reload.
- [x] Settle only the prepared provider-double deposits via normal controls and
  signed durable wakes. Verify $7,000 principal plus $190 fee, fee-first $100
  then $7,090 deposits, all four operations SETTLED and zero-debt REPAID.
  No financial SQL, provider result injection or browser clock changes.
- [x] Prove cross-customer/operator denial, logout clearing and altered
  hash/version refusal, plus original-key UNKNOWN recovery. Use focused
  isolated failures rather than repeating the whole money flow for each case.
- [x] Prove stale sources block new decisions/actions as appropriate, while
  existing history and servicing remain available. Complete initial funding
  within the real source window; do not keep the whole API on a five-minute
  shutdown timer or claim a later historical read proves fresh input.
- [x] Record source SHA, exact owned run and all linked resource IDs, process
  ownership, command/results, screenshots, allocations and limits in
  `docs/demo/product-go-ui-verification.md` and ignored private evidence.
- [x] Confirm protected-file hashes and existing manual data/services. Remove
  only task-owned disposable test resources. Keep a selected local review
  profile and credential path private; never paste credential values.
- [x] After acceptance, make the original Go-connected product the normal local
  entry. Prefer 5173 after the old listener has been deliberately handed off;
  if still occupied, retain 5176 and report that port handoff remains open.
  Never silently choose a new port or kill a user-owned process.
- [x] Keep the developer workspace at `#developer`; update entry/runbook links
  and legacy script descriptions truthfully. Do not maintain two independent
  financial implementations. Keep deploy/build compatibility explicit before
  any future hosted switch; this local acceptance deploys nothing.
- [ ] Update the plan index, mark only passed tasks, commit scoped changes when
  authorized, merge locally only when authorized, then archive this plan when
  its stated acceptance is complete. Preserve unresolved external gates.

## Test and execution guidance

Use existing frameworks and small behavior-focused tests. There is no fixed
function count. Avoid brittle snapshots of complete financial records, generic
framework tests, new dependencies and repeated full-suite runs without a new
failure/change. The end-to-end journey is concise; focused tests cover adverse
boundaries and actual reversals in portfolio reads.

Representative check commands (with the documented private URL-file inputs
set for DB suites, never values in the plan or logs):

```sh
npx tsc -b
npx vitest run --exclude 'tests/server/**'
go -C services/browser-gateway test -race ./... -count=1
go -C services/funding-collection test ./... -count=1
go -C services/underwriting-engine test ./policy -count=1
```

Use `BROWSER_GATEWAY_TEST_ADMIN_URL_FILE` and `FUNDING_TEST_ADMIN_URL_FILE`
for their existing guarded DB setups. Go build/vet and product/developer build
commands run on the final source. Legacy Node tests run only if that source
changes, with their own documented data isolation.

Run independent routine checks concurrently where safe. Do not parallelize
shared runtime startup, fresh-book preparation, money commands or DB cleanup.
Source-only checks can run before runtime setup; tests that need PostgreSQL
must explicitly use guarded owned targets and clean them up by exact marker.

Finish each dependency stage, verify its acceptance and update the checklist.
No automatic subagent delegation or arbitrary per-file approval pause is added.
If actual source contradicts a financial/identity contract, preserve the
refusal/evidence and resolve the smallest owning boundary before continuing.
Ask Ramil only for a consequential new product/authority decision.

## Deferred scope

Actual card retry, live Airwallex extraction/subscriptions and money, live AI
or Salesforce, complete wallet/card banking UI, custom offer terms, customer
self-service onboarding, internal rejection/information delivery, production
identity operations, nonempty book migration, full Node retirement and cloud
rollout remain in their separate plans/gates.

## Planning artifact status

Only this plan and its plan-index entry are created/updated by the planning
turn. The gap review is preserved. No implementation, test execution,
credential provisioning, listener change, commit, merge or deployment is
claimed by creating this document.

## Execution decisions and added scope

The original UI and all real-login/financial actions passed against the actual
Go API, worker, HTTP adapter and local provider double. A closed-collection
null-action encoding defect was corrected in its Go owner and verified by a
regression plus actual joined reads. Existing host/PostgreSQL timing tests
now use the existing five-second skew; production timing rules were unchanged.

Ramil then requested the complete Airwallex browser flow while away, authorized
selected develop `.env` credential use and autonomous decisions, and asked for
a decision log instead of questions. That extends the earlier local-only
provider scope. The local UI proof remains separate from provider acceptance.
The existing sandbox owner worktree is preserved and not duplicated.

A customer-only current wallet read was added through the existing Funding
provider boundary. Its ownership, role denial and actual pre/post-servicing
balances passed the guarded gateway journey; 120 frontend tests pass. No
financial authority or provider credential is exposed by that read. Full
Airwallex source integration remains tracked with the existing sandbox owner.
