# Airwallex sandbox funding implementation plan

Connect the real-login Go browser flow to Airwallex sandbox, then prove funding
and deposit-driven repayment through the real API and worker. Implementation
and the sandbox browser acceptance are complete. The runbook and private report
record setup repairs, verification evidence and retained test funds.

[TOC]

## Goal and scope

One isolated USD journey must show a genuine insufficient-funds card decline,
a saved shortfall and assessment, exact offer terms, customer acceptance,
independent operator approval, settled funding, and deposit-driven fee and
principal collection. Provider retrieval must corroborate every settlement.

Use the existing Go services and browser clients. Keep the 30-day product,
existing policy, integer cents, immutable operation intents, reservations and
ledger reducers. Real Airwallex sandbox events replace provider doubles for
the opt-in acceptance run; routine tests continue to use fixtures.

Production, AWS deployment, public signup, automatic card retry, new credit
policy, historical backfills, shared-data migration, Node reactivation and
automatic refunds remain outside scope. A successful funding checkpoint does
not establish repayment acceptance until the collection checkpoint passes.

## Decisions and assumptions

- Ramil authorized sandbox accounts to be created from scratch and mapped by
  the implementer. Use synthetic businesses and a unique run marker.
- Preserve the agreed topology: platform P authenticates; funding connected
  account A lends to borrower connected account B. Both accounts belong to P.
  A also receives fees and principal. The platform wallet is not substituted
  for A. Global Accounts are bank receiving instruments owned by an account.
- Use one new servicing customer, one facility and distinct invited CUSTOMER
  and OPERATOR users. Record browser-user, service-actor, customer, account,
  card and Global Account mappings explicitly. Names never establish identity.
- Root `.env` contains API and webhook credentials according to Ramil. Their
  presence, sandbox scope and applicability remain execution-time checks.
  Read them only inside a bounded server-side process; never copy the file
  into a worktree, print values or place them in command arguments or reports.
- Account activation simulations are sandbox evidence. They do not establish
  production KYB, customer agreements or a legal collection mandate. Never
  fabricate human agreement records. Use the existing explicit sandbox
  onboarding behavior and document any provider-required terms separately.
- Create bounded sandbox test balances sufficient for the chosen journey.
  Prefer the existing $9,000 attempt, $2,000 borrower cash and $7,000 shortfall
  only if verified card limits and fees permit it. Save final amounts before
  execution; never weaken policy to fit an example or assume provider fees
  are zero. Use actual supplied offer fees rather than hardcoding $190.
- No new dependencies. Use current service module versions, standard-library
  HTTP/crypto and existing PostgreSQL tooling.

## Approach and alternatives

Extend the existing Go provider adapter and constrained browser delegation.
Keep separate explicit DEMO and SANDBOX profiles, with production rejected.
Preserve policy and settlement ownership in funding-collection.

A CLI-only provider proof is useful for contract discovery but leaves browser
identity and acceptance untested. Reviving Node execution would duplicate
financial ownership. Neither substitutes for the selected Go browser journey.

## Current source and verified evidence

Planning inspected feature branch `feat/browser-gateway-auth` at `c06dad6`,
including Tasks A through D. Local `develop` currently points to `c54f9f0`.
Implementation must start from then-current `develop` in a fresh worktree,
verify that the D delivery is included and reconcile this draft with that
baseline. Do not implement on this old feature branch or edit other worktrees.

Relevant design authorities:

- [Browser gateway](/docs/designs/browser-gateway.md).
- [Funding and collection](/docs/designs/funding-and-collection.md), especially
  account direction, first-send checks, settlement and live readiness gates.
- [Shortfall detection](/docs/designs/shortfall-detection.md).
- [Existing provider prerequisites](/docs/demo/airwallex-sandbox-demo-prerequisites.md)
  and [demo acceptance](/docs/infra/demo-acceptance.md). Their historical
  checkpoints do not establish acceptance for this Go slice.

Observed implementation gaps:

- Funding adapter authentication, scoped transfers, balance retrieval and
  request-ID lookup exist; sandbox creates remain disabled. Payment/deposit
  observations, fee contracts and reversal normalization are fixture-only.
- Shortfall has issuing event/balance read clients, but its fixture manifest
  explicitly marks the live parser unsupported and subscription unverified.
- Gateway config, binding CLI, command handlers, actor verification and
  offer/customer checks require DEMO. Funding actor keys are also DEMO-only.
- Gateway migration 002 constrains both bindings and customer offers to DEMO.
  Supporting SANDBOX requires a forward migration, preserving immutability.
- Signed servicing receipt exists. Live event normalization, subscription
  version and routing ownership still need evidence.

Earlier read-only MCP checks confirmed an ACTIVE sandbox business account,
one ACTIVE USD Global Account and working card-list access (no cards returned
for the inspected month). They did not verify platform enablement, the root
API key, connected accounts, transfer permission or webhook delivery.

The required marketplace upgrade was attempted during planning. It failed
because GitHub DNS resolution was unavailable. Installed skills and the
Airwallex docs MCP remained available; no dependency upgrade is required.

## Provider contract and activation rules

Use only `https://api.sandbox.airwallex.com`, bounded requests, fixed paths and
no credentialed redirects. Authenticate with client ID/API key at
`POST /api/v1/authentication/login`. Verify platform login context and any
required `x-login-as` support without confusing account and organization IDs.
MCP OAuth success cannot establish runtime API-key permissions.

CA funding uses `POST /api/v1/connected_account_transfers/create`,
`x-on-behalf-of: A`, destination B. Collections use source B and destination A.
Read the object by ID and search by the persisted `request_id` in its original
source scope. Embedded Finance Transfers Read/Write permissions, ACTIVE
relationships and direction-specific authority must be verified.

Keep exact two-decimal amounts, stable request IDs and server-owned purpose
codes. Pin permitted lending/repayment/fee purposes and actual fee semantics.
The API enum alone does not establish that a lending purpose is permitted.
An empty lookup or a timeout never authorizes another create with a new key.
Keep uncertain creates lookup-only until replay guarantees are established.

Pin issuing API/subscription versions from actual configuration and samples.
The existing `2026-08-21` candidate is not proof. Resolve a declined card
transaction to its canonical authorization event; require PURCHASE,
AUTHORIZATION, DECLINED, INSUFFICIENT_FUNDS, USD and verified wallet funding.
The simulator's explicit failure-reason list does not include
INSUFFICIENT_FUNDS. Test a natural insufficient-balance authorization; a
TRANSACTION_BLOCKED simulation cannot satisfy this gate.

Deposit documentation exposes `/api/v1/deposits/{id}` and Global Account
transactions as distinct retrieval surfaces. The Global Account simulation
uses `/api/v1/simulation/deposit/create` and may emit Global Account events.
Pin the observed event family, canonical identifier and matching GET before
choosing the live parser. Do not assume every simulated Global Account credit
is retrievable through the generic deposit-by-ID surface. Missing gross/net,
fee, account, status or external-credit provenance holds collection.

Webhook routes retain raw-body HMAC-SHA256 over timestamp plus body, existing
timestamp policy, bounded durable receipt and event deduplication. Validate
the actual notification URL, per-URL secret, subscription/version and event
ownership. Existing root settings do not establish that a local Go route is
reachable. No dual Node/Go consumer may service this customer's events.

Readiness must distinguish local runtime readiness, verified read contracts,
funding permission and collection permission. Require installation-bound
evidence tied to mode, account mappings, API/subscription versions and fee
contracts; invalidate it on drift. Funding and collection switches default
off independently. Unknown fees or unresolved evidence keep dispatch off.

## Implementation steps

All eight source tasks and the native sandbox browser acceptance are complete.
Task 2 bindings required Task 5 migrations/delegation before local book setup.
Validation reused the existing joined fixture journey, native wire-contract
tests and one actual sandbox browser lifecycle. The private run report records
actual outcomes, setup repairs, retained funds and cleanup evidence.

Ramil authorized autonomous execution while away, with no further questions.
The private run decision log records selections, actual evidence and limits.

### Task 1: Add a bounded sandbox preflight and ownership manifest

**Files:** Create `services/funding-collection/cmd/sandbox/main.go` and focused
command tests; create `/docs/demo/airwallex-go-sandbox-runbook.md`.

- [x] Verify fresh-develop baseline and service contracts before edits.
- [x] Add a read-only preflight that loads approved root settings in-process,
  authenticates at the fixed sandbox host and reports safe capability results.
  Do not source arbitrary shell content, echo secrets or dump provider bodies.
- [x] Separate read-only inspection from explicit setup subcommands; acceptance
  actions remain Task 8.
  Require expected sandbox environment, platform and unique run marker for
  mutation; persist a private resumable manifest before each external intent.
- [x] Record required account/issuing/balance/deposit/transfer permissions and
  the webhook reachability check. Unsupported products are named blockers.
- [x] Test wrong host/environment, missing permissions, redaction and resumable
  setup intents against local HTTP doubles. No provider calls in normal tests.

### Task 2: Provision and verify the isolated account pair

**Files:** Create `services/funding-collection/cmd/sandbox/setup.go` and tests;
modify its private manifest handling; reuse existing onboarding adapters and
service import CLIs where their contracts fit.

- [x] Implement marker-owned creation/resumption of A and B, their sandbox
  onboarding/status transitions, USD receiving instruments and B's test card.
  Retrieve real returned IDs; never send fixture aliases or guessed IDs.
- [x] Use synthetic account/cardholder data, server-side keys and card IDs.
  No PAN/CVV retrieval is required. Verify ownership and ACTIVE capability.
- [x] Persist exact mappings in each owning service via supported tooling.
  Seed complete isolated facility/exposure/receivable state, never shared books.
  Create distinct browser users through the privileged CLI with bounded stdin.
- [x] Verify separate A-to-B funding and B-to-A collection authority and
  permitted purposes. Mark unavailable capabilities blocked without retries
  that create duplicate accounts or invented acceptance records.
- [x] Test interrupted setup/restart, mismatched marker/platform, account
  reassignment and cross-customer denial using owned fixtures and databases.

### Task 3: Implement verified sandbox shortfall and payment refresh

**Files:** Modify `services/shortfall-detection/internal/provider/resolver.go`,
`reads.go`, owning webhook/config code and focused tests; modify
`services/funding-collection/internal/provider/airwallex.go` and provider tests.
Update `/docs/api/shortfall-provider-contract.md`.

- [x] Capture protected version-matched sandbox samples in the opt-in run and
  derive sanitized fixtures with explicit provenance for routine tests.
- [x] Implement the verified webhook reference parser, canonical event lookup,
  card/account matching and fresh available-balance observation. Reuse current
  clients and contracts; do not import another service's internal packages.
- [x] Replace Funding's fixture-only payment refresh with authoritative scoped
  issuing reads plus current mapping/shortfall validation and balance refresh.
- [x] Retain unresolved outcomes for ambiguous events, unsupported funding
  models/versions and non-shortfall declines. No manual actionable shortfall
  insertion may replace the provider-driven acceptance path.
- [x] Test valid decline, wrong scope/reason/version, ambiguous references,
  duplicate events and changed final payment/balance facts against HTTP doubles.

### Task 4: Normalize sandbox transfers, deposits and fees

**Files:** Modify `services/funding-collection/internal/provider/airwallex.go`,
`provider.go`, owning HTTP webhook/worker files and focused provider/acceptance
tests. Add a new numbered servicing migration only if evidence storage needs it.

- [x] Pin transfer create/retrieve/list payloads, settlement and failure fields,
  exact cents, original scope, fees and account directions to verified samples.
- [x] Implement the selected deposit retrieval/event contract, external-credit
  provenance and gross/net/provider-fee treatment. Funding A-to-B credits are
  never eligible customer repayment deposits.
- [x] Add independently enabled funding/collection fee contracts. Unknown fees
  block first-send; receipts must match the saved amount/account/fee contract.
- [x] Implement verified transfer/deposit webhook wakeups and missed-event
  retrieval. Reconcile by scoped GET before ledger reducers. Unsupported
  reversal/refund evidence must hold the affected bridge visibly, preserving
  debt and reservations until a separately verified recovery is available.
- [x] Test exact money/scoping, absent fee evidence, internal-credit exclusion,
  pending/failure settlement, duplicate/missed wakeups and unknown-create
  lookup recovery. Keep DEMO fixture behavior independently runnable.

### Task 5: Add constrained sandbox browser delegation

**Files:** Modify gateway `internal/config/config.go`,
`internal/cli/bindings.go`,
`internal/servicing/client.go`, `internal/httpapi/commands.go`; add the next
numbered gateway migration after rechecking develop. Modify Funding
`internal/http/auth.go`, `actor.go` and focused tests.

- [x] Add explicit SANDBOX configuration and actor capabilities. Preserve
  DEMO's loopback mock checks; reject production and implicit mode fallback.
- [x] Use a forward migration to permit SANDBOX bindings/customer offers while
  preserving immutable identity, role matching, uniqueness and restricted
  runtime grants. Never rewrite migration 002 or relabel existing records.
- [x] Enable separate customer/operator sandbox assertions only for verified
  registry bindings, installation and source mode. Check current actor and
  customer mapping on every request; client identity never supplies authority.
- [x] Preserve exact origin/session rules, saved offer hashes/versions, separate
  acceptance and approval, durable browser intent keys and unknown receipts.
- [x] Test sandbox authorization plus wrong mode/installation/customer/key,
  disabled users, role crossover, mapping drift and outage behavior using real
  local HTTP and owned restricted PostgreSQL. Run migration twice and verify
  admin readiness refusal and runtime DDL/user-write denial.

### Task 6: Present sandbox source and operation state in the browser

**Files:** Modify `src/services/financial/client.ts`, `commands.ts`,
`src/features/bridge-capital/backoffice/screens/FinancialActions.tsx`,
`FinancialRecordsScreen.tsx` and
`src/features/bridge-capital/portal/screens/CustomerFinancialWorkspace.tsx`;
update focused `tests/financial/` tests.

- [x] Display server-supplied SANDBOX source and independent execution blockers.
  Keep backend cents, versions, consent terms and allowed actions authoritative.
- [x] Preserve distinct customer/operator sessions, immutable attempted offer
  snapshots, explicit acceptance and idempotent manual recovery for unknowns.
- [x] Show funding settlement separately from card payment status. No automatic
  card retry or inference that a funded card transaction succeeded.
- [x] Show partial/complete collection, provider pending and reconciliation
  holds from current backend reads. Never silently fall back to DEMO data.
- [x] Test mode rendering, unavailable source, stale version, independent actors
  and pending/unknown recovery with existing typed fixtures and test framework.

### Task 7: Verify the joined runtime with owned local resources

**Files:** Extend gateway `internal/acceptance/money_test.go`, Funding
`internal/acceptance/` and existing typed test helpers where needed.

- [x] Run the existing joined browser-command/API/worker/adapter fixture
      journey,
  verify native sandbox wire contracts independently, and exercise the joined
  native lifecycle against Airwallex sandbox. This bounded validation split
  avoids a second acceptance framework.
- [x] Add focused restart/unknown-receipt and duplicate webhook tests; assert
  exact durable operation counts, scoped provider calls and ledger balances.
- [x] Run affected Go tests with race detection, build, vet and gofmt, plus
  frontend tests/build/lint and diff checks. Broad checks follow affected
  packages and unresolved risks, not a fabricated fixed test count.
- [x] DB tests use the portable admin URL-file input and marker-owned disposable
  PostgreSQL targets. Missing DB access is explicitly unverified acceptance.
  Never run DB setup from package init or interrupt shared runtimes.

### Task 8: Add safe sandbox acceptance tooling and evidence collection

**Files:** Create `services/funding-collection/cmd/sandbox/accept.go` and tests;
complete `/docs/demo/airwallex-go-sandbox-runbook.md` and update service
READMEs.

- [x] Add bounded acceptance actions using the owned manifest, explicit amounts
  and expected accounts. Keep external setup/simulation separate from worker
  settlement logic. Use official sandbox APIs when MCP lacks the capability.
- [x] Add a read-only reconciliation report joining canonical decline,
  detection, assessment, offer, actors, operation, provider object and ledger
  evidence. Redact secrets, signatures, raw personal data and sensitive bodies.
- [x] Add a dry-run inventory and safe cleanup command for exact marker-owned
  local resources. Never delete shared roles/volumes or erase financial audit
  evidence. Retain sandbox objects with unresolved funds or uncertain intents.
- [x] Test mismatched manifests, interrupted acceptance, duplicate invocation,
  cleanup ownership checks and redaction with doubles and owned databases.
- [x] Document actual checked commands, limitations and live checkpoint results.
  Mark this plan complete only after the acceptance below is evidenced.

## External sandbox acceptance

These are opt-in external checkpoints after the implementation tasks. They
are not ordinary automated tests. Current status: not passed. Read-only
preflight passed; account creation was
refused for missing customer agreements. No financial transfer was sent.

1. Verify root credentials against sandbox, A/B relationships, both directions,
   product permissions, fees/purposes and isolated opening state. Record the
   provider versions and reachable signed webhook routes. Configure additional
   subscriptions only for owned resources where existing routing is suitable.
   An unavailable public route is a concrete blocker, not a reason to deploy
   infrastructure or change shared subscriptions without a separate decision.
2. Create a natural insufficient-funds test authorization on B's card. Prove
   canonical provider reason/amount/card/account, durable webhook receipt and
   Go detection. If sandbox cannot produce this evidence, report the exact
   missing capability; a seeded shortfall cannot pass this journey.
3. Use the existing Coordinator/underwriting path and complete source snapshot
   to produce the saved assessment. No model is required to grant authority;
   preserve deterministic hard policy and saved source provenance.
4. In the browser, log in as the operator, create the saved offer, switch to
   the distinct customer to accept exact terms, then approve/fund as operator.
   Observe queued/pending followed by provider-confirmed SETTLED. Verify one
   A-to-B transfer, correct principal/fee obligations and current reservations.
5. Restart only the owned worker/API and refresh both browser views. Verify
   durable state and absence of duplicate provider creates or lost intents.
6. Simulate an eligible external deposit into B's owned USD Global Account.
   Prove signed delivery, authoritative GET and eligible net amount. Observe
   separate fee/principal B-to-A operations in the agreed allocation order,
   exact debt reductions and final REPAID when fully covered. Unknown provider
   outcomes or fee/reversal contracts hold the bridge instead of passing.
7. Save a redacted evidence report, exact counts/amounts, commands and resource
   inventory. Stop temporary local processes and clean only proven-owned local
   resources. Record retained sandbox objects and outstanding balances. No
   broad sandbox reset, funds sweep, commit, merge, push or deployment is part
   of this plan-writing request.

## Review and execution boundaries

The earlier designs intentionally restrict provider writes and browser
delegation to DEMO. This plan proposes a separate SANDBOX extension; changing
an environment variable alone cannot implement it. Update the owning designs
alongside reviewed code, preserving production deferrals and existing gates.

Ramil supplied the sandbox setup direction and authorized reasonable synthetic
defaults. Unknown provider capabilities, legal/customer agreements, fee
contracts and actual ownership remain facts to verify, never assumptions to
turn into successful checks. Ask only about a concrete blocker that cannot be
resolved by owned sandbox setup or official documentation.

Ramil approved implementation and the described sandbox walkthrough after plan
review. Work runs directly in this agent's session on
`feat/airwallex-sandbox-funding`, based on `develop` at `c54f9f0`. Production,
persistent runtime setup, commits, merges, pushes and deployments remain outside
this authorization. Do not contact the former team lead or create another
executor.

## See also

- [Connected account creation and activation][account-simulation].
- [CA transfer create contract][transfer-create].
- [CA transfer lookup][transfer-list].
- [API key scopes][key-scopes].
- [Issuing sandbox simulations][issuing-simulation].
- [Global Account deposit simulations][deposit-simulation].
- [Deposit retrieval][deposit-read].
- [Signed webhook delivery][webhooks].

[account-simulation]:
https://www.airwallex.com/docs/developer-tools/sandbox-environment/connected-accounts/simulate-connected-account-status-transition
[transfer-create]:
https://www.airwallex.com/docs/api/scale/connected_account_transfers/create
[transfer-list]:
https://www.airwallex.com/docs/api/scale/connected_account_transfers/list
[key-scopes]: https://www.airwallex.com/docs/developer-tools/api/api-key-scopes
[issuing-simulation]:
https://www.airwallex.com/docs/issuing/transactions/simulate-transactions-on-issued-cards
[deposit-simulation]:
https://www.airwallex.com/docs/developer-tools/sandbox-environment/deposits
[deposit-read]:
https://www.airwallex.com/docs/api/core_resources/deposits/retrieve
[webhooks]:
https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events

## Completion evidence

The real sandbox browser run funded $7,000 and collected the $190 fee and
$7,000 principal, all confirmed by fresh scoped provider GETs. The browser and
owned ledger showed REPAID, zero debt and released reservations after restart.
The run retained an expired offer, one cancelled-unsent fee intent and audited
setup repairs. A distinct recovery income deposit was needed, so the unused
original $7,190 credit is included in final retained test balances.

See [the completed runbook](/docs/demo/airwallex-go-sandbox-runbook.md) for exact
boundaries, checks, commands, cleanup and limitations. Provider reversal/refund
semantics remain conservatively held. No production operation or card retry
was performed.
