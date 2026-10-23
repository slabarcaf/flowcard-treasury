# Airwallex Fixture Sandbox Checkpoint Implementation Plan

This plan adds one real Airwallex Sandbox issuing attempt inside the existing
Kit 5 fixture sequence. The provider attempt is limited to Bloom's $9,000
payment; the deterministic fixture engine supplies the remaining scenario.

[TOC]

## Overview

Add an opt-in **Fixture + Airwallex checkpoint** mode for the **Kit 5 target**
scenario. When the fixture timeline reaches Bloom's 9:02 supplier payment, it
pauses before processing that event and offers one server-mediated Airwallex
Sandbox attempt. If Airwallex returns `DECLINED` with
`INSUFFICIENT_FUNDS`, the presenter can continue through the existing fixture
engine. The provider result and fixture decision remain separately identified
in the dashboard.

The checkpoint creates only an issuing simulation. It does not dispatch a
bridge transfer, retry the card, collect a repayment, or change the policy and
evidence fixtures. Existing pure Fixture and full Sandbox live modes retain
their current behavior.

## Context

- `src/kit5/engine.ts` is a pure deterministic state machine. The timer in
  `src/kit5/useKit5Dashboard.ts` advances it with `step()`.
- `Kit5Dashboard` currently exposes Fixture and Sandbox live modes.
- Sandbox live calls `POST /api/kit5/next-customer`. The server persists the
  attempt and may continue through `bridgeFunding`, settlement, and card retry.
- `processNextCustomerAction` already supports an omitted funding boundary and
  persists provider lifecycle IDs for reconciliation.
- `createKit5ScenarioResolver` resolves scenario customers from active roster
  and card bindings. The Bloom checkpoint is the first card event in Kit 5
  target, so it fits the server's ordered-attempt guard.
- Bloom's current fixture evidence is `review_required`. After the live
  checkpoint, the existing fixture policy blocks Bloom pending review while
  the other customers continue.

## Decisions

- Add a third explicit mode named **Fixture + Airwallex checkpoint**. Keep
  pure Fixture free of provider calls.
- Configure one checkpoint: Kit 5 target's
  `txn_bloom_supplier_001`, Bloom's $9,000 supplier attempt against the
  scenario's $2,000 opening balance.
- Resolve account, card, amount, merchant, and currency on the server from
  trusted scenario and roster data. Never accept provider IDs or credentials
  from the browser.
- Call the Airwallex simulation endpoint through the existing server gateway
  and durable card-attempt repository. Do not pass a bridge-funding boundary
  to the checkpoint handler.
- Confirm the checkpoint only for an exact provider result of
  `DECLINED` / `INSUFFICIENT_FUNDS`. For another definite result, pause and
  show the provider's actual response. Offer an explicit, visibly labeled
  fixture-only continuation for presentation recovery. Ambiguous attempts
  must be reconciled before another provider create.
- Keep the fixture policy, evidence gate, capital, repayment, and later
  customers unchanged. Do not represent fixture funding as a provider
  transfer.

## Development Approach

- Use a regular code-first workflow and add or update tests within each task.
- Keep tasks small and sequential for `$planning:exec`.
- Keep provider access server-side and use fake gateways in automated tests.
- Run focused tests after each task, then the full test suite, lint, and build.
- Do not make live Airwallex calls during automated tests.

## Testing Strategy

- Test that pure Fixture mode advances as it does today and makes no provider
  calls.
- Test that checkpoint mode pauses exactly before Bloom's configured event.
- Test that a confirmed provider insufficient-funds result unlocks the same
  fixture event without creating a duplicate queue item.
- Test that a different provider result remains visible and cannot be labeled
  as a provider-confirmed insufficient-funds decline.
- Test server operator authentication, checkpoint allowlisting, account/card
  resolution, idempotency, lifecycle reconciliation, and the absence of
  bridge funding or card retry.
- Use fake `IssuingSimulationGateway` implementations for all automated
  provider tests.
- Final repository checks: `npm test`, `npm run lint`, `npm run build`, and
  `git diff --check`.

## Solution Overview

1. The presenter selects **Fixture + Airwallex checkpoint** and **Kit 5
   target**.
2. Fixture events advance normally until Bloom's configured 9:02 event is
   next. The scheduler pauses before calling `engine.step()` for that event.
3. The dashboard offers **Run Airwallex Sandbox attempt**. The operator is
   authenticated through the existing Kit 5 session flow.
4. A new provider-only route validates the scenario checkpoint and resolves
   Bloom's active connected account and card from the server-side roster. It
   creates and persists the issuing attempt without invoking bridge funding.
5. On a normalized `DECLINED` / `INSUFFICIENT_FUNDS` response, the dashboard
   shows the provider result and IDs. The presenter continues, and the pure
   fixture engine processes its existing Bloom event and later events.
6. If the result differs, the provider result is shown accurately and the
   checkpoint stays unconfirmed. The presenter can stop or explicitly
   continue as fixture-only.

## Technical Details

### Checkpoint identity and ordering

Store the checkpoint marker as scenario metadata, separate from
`ScenarioEvent`, so `engine.ts` remains deterministic and unaware of provider
side effects. Use a run UUID to form the same run-scoped scenario identity
accepted by `createKit5ScenarioResolver`. Derive a stable action ID from that
run identity and the configured request ID. Retries within a run reuse the
same action ID; a new provider attempt requires a new run.

The server must validate the scenario and checkpoint request against its own
allowlist. Bloom is first in the Kit 5 target attempt order. Preserve the full
ordered customer list for the durable attempt guard, while resolving real
provider bindings only as needed for the configured checkpoint.

### Provider-only operation

Add `POST /api/kit5/fixture-checkpoint` beside the existing Kit 5 routes. It
uses the existing operator authentication, Airwallex gateway, and
`kit5_card_attempts` lifecycle persistence. Reuse the provider-attempt logic
from `processNextCustomerAction`, but pass no `Kit5BridgeFundingBoundary`.
The route must never create a transfer, charge, repayment deposit, or card
retry. Extend the existing attempt-status reconciliation path for ambiguous
responses; never blindly repeat an uncertain provider create.

The browser submits only the run-scoped scenario, checkpoint customer, and
action identity. The server takes the attempt amount and provider IDs from
trusted configuration and roster bindings, sends `x-on-behalf-of`, and keeps
Airwallex credentials in the server environment.

### Result provenance and fixture continuation

Keep a provider checkpoint receipt separate from the fixture engine state.
Show the actual Airwallex process result, failure reason, lifecycle ID, event
ID, and transaction ID. Then show the fixture policy result with a clear
fixture label. The checkpoint does not overwrite Salesforce evidence,
profile, credit line, capital, or repayment data.

On an unexpected but definite provider result, offer a separate
**Continue fixture-only** action and mark subsequent outcomes as fixture data.
On an ambiguous result, retain the pause until lifecycle/status reconciliation
resolves the saved attempt. Resetting the visual fixture sequence must not
create another provider attempt; a new provider run gets a new run UUID.

## What Goes Where

- **Implementation Steps** contain code, tests, and documentation work in
  this repository.
- **Post-Completion** contains operator setup and manual checks against the
  Airwallex Sandbox, which are not automated by the plan executor.

## Implementation Steps

### Task 1: Configure the scenario checkpoint and fixture pause

**Files:**
- Modify: `src/kit5/types.ts`
- Modify: `src/kit5/scenarios.ts`
- Modify: `src/kit5/useKit5Dashboard.ts`
- Create: `tests/kit5/fixture-checkpoint.test.ts`
- Modify: `tests/kit5/dashboard-live-state.test.ts`

- [x] Add optional, explicit provider-checkpoint metadata to the scenario
      model and mark Bloom's Kit 5 target event as the sole checkpoint.
- [x] Add a `fixture-checkpoint` mode while leaving pure Fixture and Sandbox
      live mode behavior intact.
- [x] Pause automatic fixture advancement before the marked event until its
      provider result is confirmed or the presenter explicitly chooses
      fixture-only continuation.
- [x] Keep `engine.step()` pure and ensure the verified checkpoint resumes
      the existing event once, without adding a duplicate queue entry.
- [x] Add tests for checkpoint selection, pause, success, fallback, and
      unchanged pure Fixture behavior.
- [x] Run the focused Kit 5 tests before starting Task 2.

### Task 2: Add the authenticated provider-only checkpoint route

**Files:**
- Modify: `server/src/demo/kit5-scenario-resolver.ts`
- Modify: `server/src/http/routes/kit5-demo.ts`
- Modify: `server/src/http/server.ts`
- Modify: `server/src/main.ts`
- Create: `tests/server/kit5-fixture-checkpoint.test.ts`
- Modify: `tests/server/kit5-scenario-resolver.test.ts`
- Modify: `tests/server/kit5-operator-auth.test.ts`

- [x] Add a server-owned allowlist for the Kit 5 target Bloom checkpoint and
      reject other scenarios, customers, or request IDs.
- [x] Resolve Bloom's account, card, amount, and merchant from the active
      roster, card binding, and scenario configuration.
- [x] Add `POST /api/kit5/fixture-checkpoint` behind the existing operator
      authorization and invoke the durable issuing-attempt path without a
      bridge-funding boundary.
- [x] Return the normalized provider result and saved attempt identifiers;
      preserve in-progress and ambiguous states for reconciliation.
- [x] Test authentication, allowlist failures, stale/missing mappings,
      idempotent replays, and that bridge funding and card retry are never
      invoked.
- [x] Run the focused server tests before starting Task 3.

### Task 3: Add browser checkpoint requests and recovery state

**Files:**
- Modify: `src/services/types.ts`
- Modify: `src/services/http/BridgeApiClient.ts`
- Modify: `src/kit5/useKit5Dashboard.ts`
- Modify: `tests/kit5/bridge-api.test.ts`
- Modify: `tests/kit5/dashboard-live-state.test.ts`

- [x] Add typed request and response shapes for the provider-only route.
- [x] Add the browser API method without exposing provider credentials or
      account/card identifiers.
- [x] Persist a run UUID and checkpoint attempt identity so retrying within a
      run reuses the same action and a restart creates a new run.
- [x] Reconcile an unresolved attempt through the existing status/lifecycle
      path before allowing a new provider create.
- [x] Test exact insufficient-funds gating, unexpected responses, request
      deduplication, and ambiguous recovery with mocked fetch responses.
- [x] Run the focused browser tests before starting Task 4.

### Task 4: Present the hybrid flow and provenance in Kit 5

**Files:**
- Modify: `src/kit5/Kit5Dashboard.tsx`
- Modify: `src/kit5/useKit5Dashboard.ts`
- Modify: `tests/kit5/dashboard-live-state.test.ts`

- [ ] Add the **Fixture + Airwallex checkpoint** mode option and retain the
      existing Fixture and Sandbox live options.
- [ ] At the Bloom event, show **Run Airwallex Sandbox attempt** and keep the
      scenario clock and fixture decision paused while the request runs.
- [ ] Display the provider result, failure reason, scenario-resolved amount,
      provider event/transaction/lifecycle IDs, and recovery state.
- [ ] On exact `DECLINED` / `INSUFFICIENT_FUNDS`, require presenter
      continuation and then resume the fixture event sequence.
- [ ] On a different result, show the actual provider result and expose only
      a clearly labeled fixture-only continuation; keep ambiguous attempts
      paused pending reconciliation.
- [ ] Test that the UI labels provider and fixture outcomes separately and
      that no bridge/transfer or retry actions appear in checkpoint mode.
- [ ] Run the focused Kit 5 tests before starting Task 5.

### Task 5: Verify cross-mode safety and regression behavior

**Files:**
- Modify: `tests/server/kit5-mocked-live-flow.test.ts`
- Modify: `tests/server/next-customer.test.ts`
- Modify: `tests/kit5/fixture-checkpoint.test.ts`
- Modify: `tests/kit5/dashboard-live-state.test.ts`

- [ ] Add an end-to-end fake-gateway scenario for one provider-confirmed
      Bloom decline followed by fixture-driven handling of the same event.
- [ ] Assert the checkpoint creates one issuing attempt and no bridge
      transfer, retry, fee charge, or repayment-deposit operation.
- [ ] Assert later fixture customers continue in arrival order and Bloom's
      existing evidence-review gate remains fail-closed.
- [ ] Assert pure Fixture mode remains provider-free and Sandbox live retains
      its existing full live behavior.
- [ ] Run the focused cross-mode tests before starting Task 6.

### Task 6: Verify acceptance criteria

**Files:**
- No production files; fix failures in the owning task before proceeding.

- [ ] Run `npm test` with no live provider calls.
- [ ] Run `npm run lint`.
- [ ] Run `npm run build`.
- [ ] Run `git diff --check` and confirm no credentials or provider IDs were
      added to browser code, fixtures, or committed documentation.
- [ ] Confirm the automated result distinguishes provider-confirmed
      insufficient funds from explicit fixture-only continuation.

### Task 7: Document the hybrid presenter flow

**Files:**
- Modify: `README.md`
- Modify: `docs/airwallex-kit5-demo-flow-draft.md`
- Modify: `docs/airwallex-sandbox-demo-prerequisites.md`

- [ ] Document the three modes and identify Bloom's 9:02 event as the single
      live checkpoint in Kit 5 target.
- [ ] Explain which parts come from Airwallex and which remain fixture data.
- [ ] Document the exact provider-result gate, explicit fixture-only
      continuation, and lifecycle-reconciliation behavior.
- [ ] State that the checkpoint performs no bridge transfer, card retry,
      charge, or repayment deposit.
- [ ] Run `git diff --check` after documentation updates.

## Post-Completion

The following manual checks require an operator and an Airwallex Sandbox. Do
not run them in automated tests or the plan executor without a separate
explicit request.

- Verify Bloom's active connected-account and issuing-card mappings.
- Verify the mapped sandbox balance and card controls can naturally produce
  `INSUFFICIENT_FUNDS` for the $9,000 attempt; do not force a provider reason.
- Run the checkpoint from the dashboard and confirm the attempt appears in
  Airwallex Sandbox with its returned IDs.
- Confirm no bridge transfer, card retry, charge, or deposit was created by
  the checkpoint.
- Confirm the fixture sequence resumes and displays the evidence-review
  block for Bloom while other customers continue.
