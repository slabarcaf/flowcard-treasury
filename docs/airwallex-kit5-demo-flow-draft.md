# Airwallex Kit 5 Demo Flow Draft

This draft describes the deterministic fixture demo, its single real
Airwallex checkpoint, and the separate full Sandbox live path. It records the
provider checks still required for acceptance.

[TOC]

## Run modes and current status

Kit 5 offers three modes:

- **Fixture** plays deterministic local data and makes no Airwallex calls.
  Fixture customer aliases are fictional and must remain separate from
  provider-issued IDs.
- **Fixture + Airwallex checkpoint** pauses the Kit 5 target scenario before
  Bloom's 9:02, $9,000 supplier event. The browser calls the authenticated
  `POST /api/kit5/fixture-checkpoint` route. The server resolves the trusted
  scenario amount and Bloom's active connected-account and issuing-card
  mapping, then sends one `POST /api/v1/simulation/issuing/create` attempt.
  Show the provider receipt separately from fixture policy state. Only an exact
  `DECLINED` result with `INSUFFICIENT_FUNDS` confirms the checkpoint. The
  presenter must click **Continue fixture sequence** to resume the fixture
  event. A different definite result is shown accurately and allows only the
  clearly labeled **Continue fixture-only** action. In-progress, ambiguous, or
  unresolved attempts stay paused; reconcile by replaying the same
  provider-only request with the same run/action identity before any new
  provider attempt.
- **Sandbox live** remains the separate per-customer full provider path. Each
  **Next customer** action creates one simulated card transaction for that
  customer's mapped card, then the live policy/funding flow may transfer a
  bridge and retry the card. Its Nova deposit and collection behavior remains
  part of this full path where applicable.

The checkpoint performs no bridge transfer, card retry, fee charge, or
repayment deposit. After presenter continuation, Bloom's evidence-review block
and all later customer outcomes come from fixture data. The regular **Reset**
restarts visual fixture state; it does not make a new Airwallex attempt. Other
customers remain in the five-customer ordered scenario, but the checkpoint
resolves provider mappings only for Bloom. Full Sandbox live requires mappings
for all five Kit 5 customers.

The checkpoint route uses the existing operator session and keeps credentials
and provider IDs server-side. Local implementation tests use fixture aliases
and a mocked Airwallex gateway; they do not establish provider success,
verified mappings, webhook acceptance, or end-to-end live readiness. No
customer or card is created by this demo.

## Kit 5 target story

The facility is $50,000, the protected floor is $10,000, and $25,000 is already
outstanding. Opening deployable capital is $15,000, in the Less band. These
numbers and the customer outcomes are defined by the
[Kit 5 source of truth](/docs/requirements/Airwallex%20Hackathon_req_1.md).

Bloom's supplier attempt is $9,000 against an opening USD balance of $2,000,
leaving a $7,000 shortfall in both the original and Kit 5 target scenarios. The
revised evidence moves Bloom to Tier C with a $4,000 line. The request is
refused because it exceeds that line and the Tier C door is closed. A partial
bridge would not clear the payment.

Atlas has a $5,000 shortfall and is approved automatically in the Less band.
Nova has a $6,000 shortfall and needs credit-officer approval. Pixel has a
$2,000 shortfall but only 41 days of history against the 90-day minimum.
Juniper arrives after Nova with the same $6,000 shortfall; only $4,000 remains
above the floor, so policy refuses it. Nova's Day +9 repayment is triggered by
an $8,000 settled deposit and collects the $27 fee before the $6,000 principal.

## Fixture plus Airwallex checkpoint presenter sequence

1. **Set the scene.** Open **Kit 5 target**, select **Fixture + Airwallex
   checkpoint**, authenticate the operator, and show the facility, floor,
   outstanding exposure, and $15,000 opening deployable amount.
2. **Pause at Bloom.** Let the fixture clock reach Bloom's 9:02 event. It pauses
   before the fixture engine processes that event. Show the $9,000 attempt,
   $2,000 opening balance, and $7,000 shortfall.
3. **Run the provider attempt.** Click **Run Airwallex Sandbox attempt**. The
   authenticated server route uses Bloom's active connected-account/card
   mapping and the trusted scenario amount. Show the Airwallex result,
   failure reason, and provider IDs in the provider receipt section, separate
   from the fixture policy panel.
4. **Confirm and continue.** Continue the fixture sequence only when the
   provider result is exactly `DECLINED` / `INSUFFICIENT_FUNDS`. The presenter
   explicitly clicks **Continue fixture sequence**; the fixture then processes
   Bloom using its existing evidence-review gate, which blocks Bloom pending
   review.
5. **Handle other provider results accurately.** For a different definite
   result, show the returned status and reason and offer only **Continue
   fixture-only**. Keep in-progress, ambiguous, or unresolved results paused.
   Use the reconcile control to replay the same provider-only request with the
   same run/action identity before any new provider attempt.
6. **Finish with fixture outcomes.** Atlas, Nova, Pixel, Juniper, and Bloom's
   policy outcome remain scripted fixture data after the checkpoint. No bridge
   transfer, card retry, fee charge, or repayment deposit is created by this
   checkpoint. **Reset** returns the visual fixture sequence to its start and
   does not issue another Airwallex attempt.

## Full Sandbox live presenter sequence

Use this separate path for the full per-customer provider flow. It requires
verified active connected-account and card mappings for all five Kit 5
customers.

1. **Start the sequence.** Open **Kit 5 target**, select **Sandbox live**,
   authenticate the operator, and verify the live-action controls are enabled.
2. **Record Bloom.** Submit Bloom's $9,000 attempt and show the actual provider
   result. Continue through policy and the existing evidence-review gate.
3. **Fund Atlas.** Record the $5,000 sandbox card attempt. Show the provider
   result, then the policy decision and separate bridge transfer if approved.
4. **Pause for Nova.** Record the $6,000 attempt and wait for credit-officer
   approval before any bridge funding.
5. **Show Pixel's history gate.** Record the $2,000 attempt and explain why
   policy gives Pixel no line.
6. **Refuse Juniper at the floor.** Record the $6,000 attempt and show that
   only $4,000 remains above the protected floor.
7. **Collect Nova's repayment.** Where the configured Nova deposit flow is
   enabled, use a settled $8,000 deposit to collect the $27 fee first and then
   principal, capped at that deposit.
8. **Close on the treasury view.** Summarize the customer outcomes and show
   provider records alongside FlowCard records after the applicable Sandbox
   live acceptance gates pass.

## Customer mapping requirements

For the Fixture + Airwallex checkpoint, verify one active Bloom roster entry
and a matching active issuing-card binding. The binding's connected-account
ID must match Bloom's roster mapping. The five customer IDs remain in the
ordered Kit 5 scenario, but only Bloom's provider mapping is resolved by this
checkpoint; the other customer outcomes are fixtures.

Full Sandbox live requires existing provider records for Bloom, Atlas, Nova,
Pixel, and Juniper. Do not invent IDs or create real customers, accounts,
cardholders, cards, or global accounts as part of this work. Record and verify
these fields for each customer:

- Stable internal customer key and Airwallex connected-account ID.
- Connected-account status and account-scoped access through
  `x-on-behalf-of`.
- Cardholder ID, active USD issuing card ID, card status, and applicable spend
  controls, merchant category, and per-transaction limit.
- USD global-account ID for opening-balance deposits. Nova also needs the
  global account used for the Day +9 $8,000 deposit.
- Scenario opening USD balance and transaction amount, verified against the
  connected account's actual funding state.

For the checkpoint, Bloom's natural $9,000 attempt must return provider
`DECLINED` / `INSUFFICIENT_FUNDS` with a $2,000 opening USD balance. Do not pass
`INSUFFICIENT_FUNDS` as a forced failure value or translate another decline
reason into it. The Issuing simulation API documents a $9,999.99 maximum per
transaction; the mapped card's own limit must also allow $9,000. These provider
conditions and mappings have not been verified by this document.

## Sandbox API contract

Use the sandbox base URL `https://api.sandbox.airwallex.com` and direct
server-side REST calls. The browser receives no credentials. Obtain a bearer
token with `POST /api/v1/authentication/login`, sending `x-client-id` and
`x-api-key`; reuse the token until it expires.

| Operation | Endpoint | Required behavior |
| --- | --- | --- |
| Create a card attempt | `POST /api/v1/simulation/issuing/create` | Send the mapped `card_id`, scenario amount, USD currency, merchant data, `x-on-behalf-of: <connected_account_id>`, and `x-api-version: 2026-08-21`. Store the lifecycle ID before the call. |
| Retrieve a card lifecycle | `GET /api/v1/issuing/lifecycles/{id}` | Reconcile an ambiguous create response using the saved lifecycle ID before any authorized retry. |
| Retrieve card transactions/events | `GET /api/v1/issuing/card_transactions` and current issuing event retrieval | Reconcile provider records with FlowCard. |
| Transfer an approved bridge | `POST /api/v1/connected_account_transfers/create` | Use the durable bridge ledger and stable request ID. Count funding only after provider settlement. |
| Simulate Nova's deposit | `POST /api/v1/simulation/deposit/create` | Sandbox only. Use Nova's USD `global_account_id`, $8,000, and `x-on-behalf-of` where applicable. The global account determines the destination. |
| Collect fee and principal | `POST /api/v1/charges/create` | Use separate, stable ledger outbox operations, fee first and principal second, capped at the triggering settled deposit. |

Normalize the issuing result, failure reason, and provider event, transaction,
and lifecycle IDs. Airwallex does not return the attempted amount in the
simulation response; display the server-resolved scenario amount with that
limitation labeled. Do not retry an ambiguous create blindly: the lifecycle ID
is a correlation identifier, not a documented idempotency key.

Airwallex documents the [authentication endpoint](https://www.airwallex.com/docs/api/authentication/api_access_token),
[Issuing simulation](https://www.airwallex.com/docs/api/simulation/issuing/create),
[transaction limit and simulation guide](https://www.airwallex.com/docs/issuing/transactions/simulate-transactions-on-issued-cards),
[lifecycle retrieval](https://www.airwallex.com/docs/issuing/transactions/transaction-lifecycle/retrieve-lifecycle-and-transaction-data),
[deposit simulation](https://www.airwallex.com/docs/api/simulation/deposits/create),
[Connected Account Transfers](https://www.airwallex.com/docs/connected-accounts/move-funds/connected-account-%28ca%29-transfers),
and [Charges](https://www.airwallex.com/docs/global-treasury/revenue-and-fees/charges).

## API key scope and product gates

The final sandbox key must have only the required scoped resources. Its current
permissions are unverified. Confirm scopes for:

- Issuing transaction and cardholder reads, plus card reads and limit
  management.
- Connected Account reads, account capabilities, and account-scoped access.
- Global account, balance, and deposit reads.
- Connected Account Transfers read and write.
- Charges read and write.
- Simulations write for both Issuing and Deposits APIs.

Confirm Connected Accounts / Embedded Finance, issuing, simulations, transfers,
Charges, and deposit simulation are enabled for the sandbox organization.
Confirm `x-on-behalf-of` works for customer-scoped calls. Do not claim that the
currently configured key has any required scope until verified. See the
[Airwallex scoped API key guide](https://www.airwallex.com/docs/developer-tools/api/api-key-scopes).

The seven configured Airwallex environment variable names are
`AIRWALLEX_API_KEY`, `AIRWALLEX_CLIENT_ID`, `AIRWALLEX_ORG_ID`,
`AIRWALLEX_ACCOUNT_ID`, `AIRWALLEX_WEBHOOK_EVENTS_FROM`,
`AIRWALLEX_WEBHOOK_ID`, and `AIRWALLEX_WEBHOOK_SECRET`. The local root `.env` is
ignored and exists only in the main checkout. Do not copy it into a worktree or
deployment, and never log or display secret values. Keep API keys and webhook
secrets in server-side environment or a deployment secret store, never in a
`VITE_` variable, fixture, or source file.

## Webhook delivery and acceptance gates

Reuse the existing sandbox webhook. Verify its public HTTPS ngrok URL ends in
`/webhooks/airwallex` and forwards to
`http://localhost:8000/webhooks/airwallex`. The local server and ngrok delivery
have not been verified yet. The webhook signing secret is URL-specific.

Subscribe to:

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
  `issuing.transaction.failed`, plus the lifecycle event family used by the
  implementation.

Verify `x-timestamp` and the HMAC-SHA256 signature over the timestamp-prefixed
raw body with the URL-specific secret. Deduplicate by event ID, allow for
out-of-order delivery, and acknowledge promptly with HTTP 200. See Airwallex's
[webhook guide](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events)
and [Issuing event list](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/issuing).

The final real demo remains gated on all of the following:

- Verified real connected-account, cardholder, active card, and USD global
  account mappings for the five customers.
- Verified key scopes, product enablement, account-scoped API behavior, and
  `AIRWALLEX_WEBHOOK_EVENTS_FROM` semantics.
- A natural provider `INSUFFICIENT_FUNDS` result for Bloom's $9,000 attempt
  with a $2,000 opening USD balance.
- Production composition of the active Kit 5 policy with the durable bridge
  ledger, transfer settlement, separate card retry, Nova deposit operation,
  and fee-first collection.
- A live webhook through the existing ngrok tunnel, signature validation,
  inbox persistence, and HTTP 200 acknowledgement.
- Retrieval of Airwallex transaction records and reconciliation with FlowCard.

The local demo does not use the Airwallex AgentOS production connector. Use
direct server-side REST calls. No sandbox provider success is claimed until the
acceptance gates above pass.

## See also

- [Sandbox demo mappings and failure options](/docs/airwallex-sandbox-demo-prerequisites.md)
- [Airwallex bridge funding and collections requirements](/docs/requirements/Airwallex%20Hackathon_req_7.md)
- [Airwallex sandbox overview](https://www.airwallex.com/docs/developer-tools/sandbox-environment)
