# Airwallex Real Sandbox Demo Execution Plan

This plan adds a real Airwallex sandbox card-transaction path to the Kit 5
customer sequence and makes provider results visible in FlowCard. It uses the
latest demo flow while relying on the existing bridge ledger plan for durable
funding, collections, webhooks, and reconciliation.

[TOC]

## Overview

The final submission must use Airwallex APIs. In sandbox-live mode, each
**Next customer** action creates a sandbox issuing simulation through a real
Airwallex API call and reflects its provider result and IDs in FlowCard. This
is a sandbox record, not a production authorization or movement of funds. The
deterministic fixture mode remains available for tomorrow's video.

Each **Next customer** action creates a simulated card attempt for that
customer. The existing policy engine consumes the actual provider result. Only
an approved bridge proceeds to a separate connected-account transfer; rejected
and ineligible customers do not receive transfers.

## Decisions applied

-   Each customer action creates a sandbox card attempt. The bridge transfer is
    a separate operation after policy approval.
-   The video may use deterministic fixtures. The final submission uses
    sandbox-live mode.
-   Bloom's supplier payment is $9,000. With a $2,000 USD balance, the
    shortfall is $7,000; Bloom remains refused by the Tier C door and its
    $4,000 line. The payment is within the issuing simulation maximum.
-   The Kit 5 policy outcomes and capital sequence remain canonical. Provider
    results must be displayed as Airwallex returns them.

## Context and boundaries

-   `docs/requirements/Airwallex Hackathon_req_1.md` is the current Kit 5 source
    of truth. It supersedes older demo numbers where they conflict.
-   The current dashboard uses scripted scenario events. It already pauses for
    CRM re-evaluation, auto-approval, and human approval.
-   The existing
    [bridge ledger plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md)
    owns the server API, reservations, transfer and charge outbox, webhook
    inbox, reconciliation, and deterministic fixture mode.
-   This plan adds the real issuing-transaction trigger and dashboard
    reflection.
    It does not change underwriting rules, Salesforce mapping, AI signal
    definitions, bridge collection policy, or the existing API operation IDs.
-   The $50,000 facility, 20% floor, $25,000 opening exposure, $15,000 opening
    deployable amount, sequential arrival order, and full-or-nothing bridge
    decision remain fixed.

## Airwallex documentation findings

-   The
    [issuing simulation API](https://www.airwallex.com/docs/issuing/transactions/simulate-transactions-on-issued-cards)
    creates sandbox-only card transactions. The guide describes retrieving
    the created transaction through issuing transaction APIs.
-   The guide documents a $9,999.99 maximum for a single simulated transaction.
    Bloom's revised $9,000 supplier payment is below this maximum. Configure
    the mapped card's own per-transaction limit to allow $9,000 as well.
-   The current versioned API reference allows forced failure inputs
    `CARDHOLDER_VERIFICATION_FAILED`, `CVV_INVALID`, `INVALID_EXPIRY_DATE`,
    `PIN_INVALID`, `PIN_RETRIES_EXCEEDED`, and `TRANSACTION_BLOCKED`, but not
    `INSUFFICIENT_FUNDS`. The demo must produce insufficient funds from
    sandbox account/card state or another documented provider behavior.
-   The
    [Connected Account Transfers API](https://www.airwallex.com/docs/api/scale/connected_account_transfers)
    moves funds from the platform account to an active connected account. The
    existing ledger plan handles funding status and idempotency.
-   The
    [sandbox guide](https://www.airwallex.com/docs/developer-tools/sandbox-environment)
    requires sandbox API credentials and a bearer token for API calls.

## Failure simulation options

The issuing simulation request does not accept `INSUFFICIENT_FUNDS` as a
forced `transaction_failure_reason`. Keep the provider's returned
`failure_reason` authoritative.

1. **Recommended for the final sandbox run: produce insufficient funds from
   account state.** Set Bloom's wallet-backed USD card to a $9,000 purchase and
   seed Bloom's connected-account USD balance to $2,000. Omit
   `transaction_failure_reason`. Keep the home currency USD, avoid another
   currency wallet or Platform Liquidity Program that could cover the payment,
   and confirm Airwallex returns `INSUFFICIENT_FUNDS`. For an approved Atlas
   or Nova bridge, the settled transfer should add the shortfall before the
   separate retry; Bloom remains policy-rejected and receives no transfer.
   Verify this with sandbox API calls before the acceptance run.

2. **Use `TRANSACTION_BLOCKED` for a deterministic API smoke test.** This is a
   supported forced-failure input and confirms that FlowCard records and
   displays a provider decline. Keep the returned reason visible and prevent
   bridge evaluation for this result; it does not demonstrate an
   insufficient-funds bridge.

3. **Use the local `INSUFFICIENT_FUNDS` fixture for the video and automated
   demo flow.** Label it as fixture data and make no Airwallex call. This keeps
   the presentation deterministic while the natural sandbox decline is being
   verified; it does not satisfy sandbox-live acceptance.

Do not send the unsupported forced value `INSUFFICIENT_FUNDS`, translate
`TRANSACTION_BLOCKED` into it, or let another decline reason trigger the
bridge path.

## Readiness review (2026-10-22)

- Reviewed against the fetched `origin/main` at `12e03b3`. The latest main
  includes the Bloom CRM before/after result callout and pauses for CRM
  re-evaluation, auto-approval, and human approval. This plan preserves that
  presenter flow and adds the live Airwallex path around it.
- Ramil reports that the sandbox API settings and webhook are already set up.
  I verified that the current `main` checkout has a root `.env` containing
  all seven supplied variable names without reading their values. Git ignores
  this file, so it is local configuration rather than a committed branch file
  and will not appear in a fresh worktree or deployment automatically.
- The existing webhook is reported to use an ngrok tunnel forwarding to
  `localhost:8000`. Keep the Airwallex notification URL on the public HTTPS
  ngrok host; the Airwallex service cannot deliver directly to `localhost`.
  Verify that the registered path reaches the planned
  `POST /webhooks/airwallex` handler before acceptance.
- The Kit 5 source of truth, the Airwallex data inventory, the transfer module
  requirements, and the bridge ledger plan support the selected flow: one card
  attempt per customer action; only an approved bridge creates a separate
  transfer; collections use the settled deposit that triggered them.
- Bloom's $9,000 amount is within the documented simulation maximum. The plan
  remains blocked on two sandbox checks: proving a natural
  `INSUFFICIENT_FUNDS` decline and verifying the five customer/account/card
  mappings. Do not change the provider result in FlowCard to hide either gap.
- A fresh read-only Developer MCP check found two active cards, three ready
  cardholders, and three recent declines (`CARD_INACTIVE`,
  `MERCHANT_CATEGORY_NOT_ALLOWED`, and `LIMIT_EXCEEDED`). None establishes a
  natural insufficient-funds decline. These MCP results are platform scoped;
  they do not confirm the five customers' connected-account cards, balances,
  or global accounts. Verify those through backend REST with
  `x-on-behalf-of`.

## Airwallex sandbox setup

### Existing sandbox credentials and webhook

The root `.env` in the current checkout contains these variable names:
`AIRWALLEX_API_KEY`, `AIRWALLEX_CLIENT_ID`, `AIRWALLEX_ORG_ID`,
`AIRWALLEX_ACCOUNT_ID`, `AIRWALLEX_WEBHOOK_EVENTS_FROM`,
`AIRWALLEX_WEBHOOK_ID`, and `AIRWALLEX_WEBHOOK_SECRET`. Do not copy their
values into this plan, logs, frontend variables, or source control.

1. **Verify the existing sandbox key.** Reuse the configured key if it has the
   required account-level permissions, scoped to the platform account that
   owns the connected accounts. Request additional scopes or issue another
   dedicated key only if this key lacks them. The Developer MCP OAuth
   connection is for operator discovery; it is not the backend credential.

   - **Simulations: Write** (`simulation:write`) for card and deposit
     simulations.
   - **Cards: Read** for cards, cardholders, and issuing transactions
     (`issuing.card:read`, `issuing.cardholder:read`,
     `issuing.transaction:read`).
   - **Business Accounts: Read** for balances, deposits, and global accounts
     (`balance:read`, `global_account:read`, and Deposits Read in the
     scoped-key resource picker).
   - **Connected Accounts:** Account Read, Transfers Read and Write, and
     Charges Read and Write. These are API resources in the scoped-key picker;
     grant no unrelated resources.

   Keep `AIRWALLEX_ORG_ID` and `AIRWALLEX_ACCOUNT_ID` as platform identifiers.
   Customer-scoped requests still use each mapped connected account in
   `x-on-behalf-of`; do not substitute the platform account ID. Confirm the
   meaning and expected format of `AIRWALLEX_WEBHOOK_EVENTS_FROM` in the
   backend configuration before using it as a filter or cursor.

2. **Confirm sandbox product enablement.** Ask the Airwallex account manager
   or support to confirm Connected Accounts / Embedded Finance, connected
   account transfers, Charges, Issuing, and the simulation APIs are enabled
   for this sandbox organization. Standard sandbox access may not include
   every Connected Account capability. Confirm the account can use
   account-scoped `x-on-behalf-of` calls before implementation smoke tests.
   Confirm the Issuing configuration uses USD as the home currency, permits
   the intended card program, and allows enough cards and transaction limits
   for the five-customer sequence.

3. **Prepare the five customer mappings.** Map Bloom, Atlas, Nova, Pixel, and
   Juniper to their sandbox connected-account IDs, active USD issuing cards,
   ready cardholders, and USD global-account IDs for the opening-balance
   deposits. Nova's global account also receives the Day +9 deposit. Provision
   missing accounts/cards before the demo; do not create them as a side effect
   of a customer click. Confirm each connected account is active and can be
   accessed with `x-on-behalf-of`; check card status, USD currency, spend
   controls, merchant category, per-transaction limit, and connected-account
   balance for each scenario.

4. **Verify the natural insufficient-funds decline.** Use the recommended
   account-state setup in **Failure simulation options** and confirm that the
   $9,000 Bloom request returns `INSUFFICIENT_FUNDS`. The amount is within the
   API maximum; ensure the card's own limit also allows $9,000. Do not set
   `transaction_failure_reason` to `INSUFFICIENT_FUNDS`; that forced input is
   unsupported.

5. **Verify the existing webhook and tunnel.** Reuse
   `AIRWALLEX_WEBHOOK_ID` and `AIRWALLEX_WEBHOOK_SECRET`; do not create a
   duplicate webhook. In **Settings > Developer > Webhooks**, verify that the
   existing notification URL is the current public HTTPS ngrok URL with the
   `/webhooks/airwallex` path, and that ngrok forwards it to
   `http://localhost:8000/webhooks/airwallex`. Confirm the subscription has:

   - Transfers: `connected_account_transfer.new`,
     `connected_account_transfer.pending`,
     `connected_account_transfer.settled`,
     `connected_account_transfer.suspended`, and
     `connected_account_transfer.failed`.
   - Charges: `charge.new`, `charge.pending`, `charge.settled`,
     `charge.suspended`, and `charge.failed`.
   - Deposits: `deposit.pending`, `deposit.settled`, `deposit.rejected`, and
     `deposit.reversed`.
   - Issuing: `issuing.transaction.succeeded` and
     `issuing.transaction.failed`.

   Send a test notification and confirm the local endpoint verifies the
   signature and acknowledges it with HTTP 200. If the ngrok public host
   changes, update the existing Airwallex notification URL and refresh
   `AIRWALLEX_WEBHOOK_SECRET` if Airwallex rotates it. Keep the tunnel and
   backend running during the demo. If inbound IP allowlists are enabled, use
   Airwallex's current sandbox source IP list.

### Backend runtime contract

Use direct server-side REST calls to `https://api.sandbox.airwallex.com`.
For operator preflight, the existing Airwallex Developer MCP provides
`get_account_balances`, `list_cards`, `list_cardholders`, and
`list_issuing_transactions`. These calls are platform scoped and cannot
substitute for connected-account REST requests with `x-on-behalf-of`; the
application runtime uses the REST endpoints below, not MCP.

| Purpose | Endpoint | Required behavior |
| --- | --- | --- |
| Authenticate | `POST /api/v1/authentication/login` | Send `x-client-id` and `x-api-key` from server secrets; use the returned bearer token. |
| Create the customer card attempt | `POST /api/v1/simulation/issuing/create` | Send the mapped `card_id`, actual USD amount, merchant data, `x-on-behalf-of: <connected_account_id>`, and `x-api-version: 2026-08-21`. Persist a UUID `card_transaction_lifecycle_id` before the call. |
| Reconcile an ambiguous card attempt | `GET /api/v1/issuing/lifecycles/{card_transaction_lifecycle_id}` | Query the saved lifecycle before considering a retry. The lifecycle ID is a correlation identifier, not a documented idempotency key; never blindly repeat the create call. |
| Transfer approved bridge funds | `POST /api/v1/connected_account_transfers/create` | Use the existing ledger outbox and a stable `bridge-<UUID>` `request_id`; count funding only at `SETTLED`. This is a platform-originated transfer, not an on-behalf-of customer call. |
| Simulate the Day +9 customer deposit | `POST /api/v1/simulation/deposit/create` | Send `amount: 8000`, Nova's USD `global_account_id`, `status: SETTLED`, and `x-on-behalf-of: <Nova_connected_account_id>`; confirm settlement before collection. The global account determines currency. |
| Collect fee, then principal | `POST /api/v1/charges/create` | Use the bridge ledger's fee-first outbox operations, `x-on-behalf-of: <connected_account_id>`, and stable `fee-<UUID>` / `principal-<UUID>` request IDs. |
| Inspect a customer deposit or card transaction | `GET /api/v1/deposits`, `GET /api/v1/issuing/card_transactions`, or lifecycle/event retrieval endpoints | Use the customer `x-on-behalf-of` context and reconcile the provider object before updating the dashboard. |

For issuing simulation, normalize `process_result` (`APPROVED` or `DECLINED`),
`failure_reason`, and all three identifiers:
`card_transaction_event_id`, `card_transaction_id`, and
`card_transaction_lifecycle_id`. The simulation supports at most $9,999.99 per
transaction and only documented currencies. Its forced-failure input does not
include `INSUFFICIENT_FUNDS`; obtain that outcome from verified sandbox
account/card state. Do not relabel another failure reason.

Read these server-side settings from the local root `.env` for the local demo:
`AIRWALLEX_API_KEY`, `AIRWALLEX_CLIENT_ID`, `AIRWALLEX_ORG_ID`,
`AIRWALLEX_ACCOUNT_ID`, `AIRWALLEX_WEBHOOK_EVENTS_FROM`,
`AIRWALLEX_WEBHOOK_ID`, and `AIRWALLEX_WEBHOOK_SECRET`. Use
`https://api.sandbox.airwallex.com` as `AIRWALLEX_BASE_URL` and
`2026-08-21` for the issuing simulation API version unless the endpoint
contract specifies otherwise. Keep API keys and webhook secrets in server
environment variables; configure a separate secret store for any deployed
backend. The root `.env` is ignored by Git and does not populate a new worktree
or deployment. Never expose values through Vite/client variables, fixtures,
source control, or logs.

Airwallex signs webhook requests with HMAC-SHA256 over the raw body prefixed
by the `x-timestamp` header, using the notification URL's secret. Verify the
signature with a constant-time comparison and validate the timestamp before
parsing; persist each verified event once by provider event ID; acknowledge
with HTTP 200 promptly; then process asynchronously. Treat delivery as
duplicate-prone and unordered and fetch the provider object when needed.

## Solution outline

1.  The browser sends a single **Next customer** command to the backend with the
    scenario ID, customer key, and a stable local action ID.
2.  The backend verifies sequence order and that the action has not already
    created a provider transaction.
3.  The backend calls Airwallex's sandbox issuing simulation API with the
    mapped card and transaction data. It stores `process_result`, failure
    reason, all three provider transaction identifiers, and raw response
    provenance.
4.  The dashboard renders the provider result and transaction identifiers.
    The app's ledger and Airwallex's sandbox transaction record must agree.
5.  The deterministic policy evaluates the actual card result. Only a
    confirmed insufficient-funds decline can proceed through the bridge
    decision path. Other failures stop and display the provider reason. A
    sandbox-live acceptance run is blocked until that natural decline has been
    demonstrated for the mapped account and card.
6.  Auto and human approvals use the existing bridge ledger command. Funding is
    visible only after the transfer settles. The card retry runs only after
    settlement and creates its own provider transaction record. Nova's Day +9
    deposit is created through the sandbox deposit simulation; the ledger then
    collects fee and principal through separate Charges.
7.  Fixture mode continues to play the scripted scenario without provider calls.

## Implementation steps

### Task 1: Verify existing sandbox configuration and customer mappings

**Files:**

-   Modify: `docs/airwallex-sandbox-demo-prerequisites.md`

- [ ] Map each Kit 5 scenario customer to an active sandbox connected account
      and an issuing card without exposing API credentials to the browser.
- [ ] Load the supplied server variables from root `.env`; verify the API key
      has the required scopes without printing or logging any values.
- [ ] Confirm `AIRWALLEX_ACCOUNT_ID` and `AIRWALLEX_ORG_ID` are platform
      identifiers and customer calls use each mapped connected account in
      `x-on-behalf-of`.
- [ ] Confirm `AIRWALLEX_WEBHOOK_EVENTS_FROM` has a documented format and
      meaning before using it as a filter or cursor.
- [ ] Verify the existing `AIRWALLEX_WEBHOOK_ID` subscription has the required
      event set and the webhook secret matches the registered notification URL.
- [ ] Confirm the active public HTTPS ngrok URL forwards the webhook path to
      `http://localhost:8000/webhooks/airwallex`; send a test delivery and verify
      signature validation, inbox persistence, and HTTP 200 acknowledgement.
- [ ] Keep the local server and ngrok tunnel running for the demo. If the public
      ngrok URL changes, update the existing Airwallex webhook configuration
      and refresh the secret if Airwallex rotates it.
- [ ] Verify five distinct customer/account/card mappings through REST with
      `x-on-behalf-of`; confirm cards are active, cardholders are ready, and
      account/card controls allow the intended USD merchants and amounts.
- [ ] Verify natural `INSUFFICIENT_FUNDS` behavior from sandbox account/card
      state. Do not force or relabel this failure reason.
- [ ] Verify all transaction amounts are at or below the documented $9,999.99
      API maximum. Bloom's amount is now $9,000; confirm the card's own limit
      also allows it.
- [ ] Confirm the sandbox supports the connected-account transfer, Charges,
      issuing, deposit simulation, and account-scoped `x-on-behalf-of` APIs.
- [ ] Create or map a USD global account for each customer to seed the opening
      balances. Reuse Nova's account for the separate Day +9 deposit.
- [ ] Verify the transaction can be retrieved from Airwallex sandbox records
      after creation.
- [ ] Record the fixture mappings, actual sandbox IDs, measured opening
      balances, verified decline setup, and card limits.

### Task 2: Add a typed issuing-simulation operation

**Files:**

-   Modify: `server/src/airwallex/client.ts`
-   Create: `server/src/airwallex/issuing.ts`
-   Modify: `server/src/config.ts`
-   Create: `tests/server/issuing.test.ts`

- [ ] Add a server-only operation for
      `POST /api/v1/simulation/issuing/create`.
- [ ] Validate card, amount, currency, merchant, and scenario inputs before
      calling Airwallex.
- [ ] Send `x-on-behalf-of` and `x-api-version: 2026-08-21`; normalize
      `process_result`, `failure_reason`, and the event, transaction, and
      lifecycle IDs into a typed result.
- [ ] Persist a UUID `card_transaction_lifecycle_id` before create; on an
      ambiguous response, query the lifecycle before any operator-authorized
      retry. Do not treat this ID as a provider idempotency key.
- [ ] Keep credentials in server environment configuration.
- [ ] Cover provider success, rejection, timeout, malformed response, and
      unexpected failure reason.

### Task 3: Persist and deduplicate each next-customer action

**Files:**

-   Create: `server/src/db/migrations/002_kit5_card_attempts.sql`
-   Modify: `server/src/db/repositories.ts`
-   Create: `server/src/demo/next-customer.ts`
-   Create: `server/src/http/routes/kit5-demo.ts`
-   Create: `tests/server/next-customer.test.ts`

- [ ] Add a provider-transaction record tied to scenario, customer, and local
      action ID.
- [ ] Enforce one provider-create operation for each accepted action ID.
- [ ] Persist the action before calling Airwallex so a repeated click cannot
      create a second transaction.
- [ ] Store the lifecycle ID before the provider call and query the lifecycle
      on an ambiguous timeout; never blindly resubmit the create request.
- [ ] Reject out-of-order or duplicate customer actions with the existing
      scenario state.

### Task 4: Wire sandbox-live mode into the Kit 5 dashboard

**Files:**

-   Modify: `src/services/types.ts`
-   Modify: `src/services/http/BridgeApiClient.ts`
-   Modify: `src/kit5/useKit5Dashboard.ts`
-   Modify: `src/kit5/Kit5Dashboard.tsx`
-   Create: `tests/kit5/live-transaction.test.ts`

- [ ] Add a **Next customer** action for sandbox-live mode.
- [ ] Disable duplicate submission while the current provider action is
      pending or unresolved.
- [ ] Render the actual transaction ID, status, amount, and failure reason in
      the dashboard activity view. Keep the provider's event, transaction,
      and lifecycle identifiers available for verification.
- [ ] Continue fixture mode without Airwallex calls.
- [ ] Show provider errors and stop the affected decision instead of falling
      back to a fabricated result.

### Task 5: Gate bridge funding and card retry on provider outcome

**Files:**

-   Modify: `server/src/demo/next-customer.ts`
-   Create: `tests/server/live-demo-funding.test.ts`

- [ ] Allow bridge evaluation only after a provider-confirmed
      `INSUFFICIENT_FUNDS` result.
- [ ] Keep the existing policy as the sole source of approval and amount.
- [ ] Route approved commands through the existing bridge API and ledger
      reservation path.
- [ ] Wait for transfer `SETTLED` before marking the bridge funded or
      creating the separate card retry through the issuing simulation API.
- [ ] In the repayment rehearsal, create the documented $8,000 sandbox deposit
      simulation for Nova, wait for `deposit.settled`, and verify the ledger
      sends separate fee and principal Charges capped at that deposit.
- [ ] Confirm policy-rejected and ineligible customers create no bridge
      transfer.

### Task 6: Verify the complete Kit 5 live sequence

**Files:**

-   Create: `tests/server/kit5-sandbox-acceptance.test.ts`
-   Modify: `tests/kit5/engine.test.ts`

- [ ] Verify each of the five **Next customer** actions creates one sandbox
      card attempt through the simulation API, with the provider result and
      identifiers visible in FlowCard. Verify approved bridges create a
      separate retry transaction after settlement.
- [ ] Verify Bloom's CRM note is visible before its decline decision and that
      the result is refused at Tier C.
- [ ] Verify Atlas and approved Nova transfers use the existing ledger path.
- [ ] Verify Nova's simulated settled deposit is reflected in the webhook
      inbox and triggers fee-first then principal collection.
- [ ] Verify Pixel fails the 90-day history gate and Juniper is refused at the
      $4,000 remaining-above-floor boundary.
- [ ] Verify no live result is fabricated when Airwallex returns an unexpected
      status or failure reason.
- [ ] Compare dashboard amounts, `process_result`, failure reasons, and all
      provider IDs with Airwallex sandbox records; record the $9,000 Bloom
      amount and successful limit check.
- [ ] Verify duplicate and out-of-order webhook delivery, raw-body signature
      validation, prompt HTTP 200 acknowledgement, and provider reconciliation.
- [ ] Verify a provider test event reaches the local backend through the
      registered public ngrok URL and appears in the webhook inbox.

### Task 7: Document video and final-submission run modes

**Files:**

-   Modify: `README.md`
-   Modify: `docs/EDITING.md`
-   Modify: `docs/airwallex-kit5-demo-flow-draft.md`

- [ ] Document fixture mode for the video and sandbox-live mode for the final
      submission.
- [ ] Document required sandbox account/card mappings and server environment
      variable names without storing secret values.
- [ ] Document scoped API key permissions, Connected Account enablement,
      webhook event subscriptions, secret handling, exact REST endpoints, and
      sandbox-live acceptance gates.
- [ ] Document the next-customer transaction behavior and the transaction
      amount constraint.
- [ ] Document that the Airwallex AgentOS production connector is not used by
      the sandbox demo.

## Acceptance criteria

-   The final submission runs in sandbox-live mode and makes one Airwallex
    sandbox issuing simulation API call for every next-customer action.
-   Every click produces one durable FlowCard record that matches the
    corresponding Airwallex sandbox transaction.
-   Duplicate clicks cannot create duplicate provider transactions.
-   Only provider-confirmed insufficient-funds results enter the bridge policy
    path.
-   Bloom's provider transaction amount is $9,000, below the $9,999.99
    simulation maximum, and within the mapped card's own limit. A fabricated
    insufficient-funds result never satisfies acceptance.
-   Five customer/account/card mappings are verified, and the sandbox
    Connected Accounts, Issuing, simulation, deposit, Charges, and webhook
    capabilities are enabled.
-   The existing bridge ledger controls reservations, transfers, collections,
    webhooks, and reconciliation.
-   The Kit 5 target outcomes and capital arithmetic remain as specified, with
    Bloom's revised $7,000 shortfall and no change to the other customers.
-   The deterministic fixture mode remains usable for the video.
-   No production Airwallex endpoint or credential is used.

## Dependencies and post-completion actions

-   Supplied: the root `.env` in the current main checkout has the seven listed
    Airwallex variable names. Git ignores that local file; new worktrees and
    deployed backends need their own secure environment configuration.
-   Manual verification: confirm the existing scoped key has the permissions
    in **Airwallex sandbox setup**. Create another key only if the existing one
    lacks required scopes.
-   Manual: have Airwallex confirm Connected Accounts / Embedded Finance,
    Issuing, Charges, transfer, deposit simulation, and `x-on-behalf-of`
    enablement for this sandbox account.
-   Manual: prepare five customer mappings, active cards, ready cardholders,
    and required global accounts; verify natural insufficient-funds behavior
    before the live acceptance run.
-   Supplied: an Airwallex webhook is configured through ngrok to reach
    `localhost:8000`. Verify the public URL, route path, existing event
    subscription, webhook ID, and secret before the live acceptance run.
-   Implementation: finish the existing Airwallex bridge ledger before wiring
    live transfer, webhook, and collection operations into this flow.
-   Codex MCP connections are operator tooling only. They do not provide
    backend credentials or replace REST calls with `x-on-behalf-of`.
-   Rehearse the final submission in sandbox-live mode and verify provider
    records in Airwallex.

## See also

*   [Kit 5 demo flow draft](/docs/airwallex-kit5-demo-flow-draft.md)
*   [Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md)
*   [Airwallex bridge ledger plan](/docs/plans/20261021-airwallex-bridge-ledger-implementation-plan.md)
*   [Sandbox demo mappings and failure options](/docs/airwallex-sandbox-demo-prerequisites.md)
*   [Airwallex sandbox MCP documentation](https://www.airwallex.com/docs/developer-tools/ai/developer-mcp)
*   [Airwallex issuing transaction simulation guide](https://www.airwallex.com/docs/issuing/transactions/simulate-transactions-on-issued-cards)
*   [Scoped API keys](https://www.airwallex.com/docs/developer-tools/api/manage-api-keys)
*   [API key permission catalog](https://www.airwallex.com/docs/developer-tools/api/api-key-scopes)
*   [Obtain an API access token](https://www.airwallex.com/docs/api/authentication/api_access_token/login)
*   [Sandbox issuing simulation API](https://www.airwallex.com/docs/api/simulation/issuing)
*   [Sandbox deposit simulation](https://www.airwallex.com/docs/api/simulation/deposits/create)
*   [Supported issuing card programs and funding](https://www.airwallex.com/docs/issuing/supported-card-programs)
*   [Issuing transaction failure reasons](https://www.airwallex.com/docs/issuing/transactions/transaction-failure-reasons)
*   [Airwallex webhook setup and signature verification](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events)
*   [Connected account transfer webhook events](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/connected-account-transfers)
*   [Charge webhook events](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/charges)
*   [Deposit webhook events](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/deposits)
*   [Issuing webhook events](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/issuing)
