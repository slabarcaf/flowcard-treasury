# Airwallex Sandbox Demo Prerequisites

This note records local fixture mappings and the presence of local server
configuration for Kit 5. It does not contain Airwallex-issued identifiers or
evidence of provider behavior. See the execution plan for final sandbox
acceptance gates.

[TOC]

## Demo modes and provider mappings

- **Fixture** uses deterministic local data and makes no Airwallex calls.
- **Fixture + Airwallex checkpoint** pauses the Kit 5 target before Bloom's
  9:02, $9,000 supplier event. The authenticated browser calls
  `POST /api/kit5/fixture-checkpoint`; the server resolves the trusted
  scenario amount and Bloom's active connected-account and issuing-card
  mapping, then sends one `POST /api/v1/simulation/issuing/create` attempt.
  The five customer IDs remain ordered in the scenario, but only Bloom's
  provider mapping is needed. Other customer outcomes stay in fixtures.
  Confirm the checkpoint only for exact `DECLINED` plus
  `INSUFFICIENT_FUNDS`. The presenter must choose **Continue fixture
  sequence**; a different definite response may proceed only through the
  clearly labeled **Continue fixture-only** action. In-progress, ambiguous,
  or unresolved results stay paused and reconcile by replaying the same
  provider-only request with the same run/action identity when a lifecycle ID
  was saved. If Airwallex returned no lifecycle ID, the operator can find the
  ID in Airwallex and submit it to the authenticated reconciliation route,
  which verifies the result under Bloom's mapped account without another
  create. A missing or invalid lifecycle keeps the attempt unresolved. Checkpoint
  submission is blocked unless the run identity is safely stored. Edited Bloom
  amount, wallet, or scheduled event terms disable checkpoint mode. This mode
  makes no bridge transfer, card retry, fee charge, or repayment deposit.
  **Reset** restarts visual fixture state and does not create another provider
  attempt. After choosing a continuation, **Start new Airwallex checkpoint
  run** begins a fresh provider run.
- **Sandbox live** is the separate per-customer full provider path. It requires
  active connected-account and card mappings for all five Kit 5 customers and
  retains the existing funding, transfer, retry, and Nova deposit/collection
  behavior where applicable.

Credentials and connected-account/card IDs remain server-side. The
authenticated dashboard displays provider event, transaction, and lifecycle
IDs as receipt data. Fixture aliases below are documentation aliases, not
provider IDs. No provider mappings, provider success, or webhook acceptance
are verified by this note.

## Fixture customer mapping

These aliases are fictional fixture keys for local implementation and test
data. They must never be sent as Airwallex IDs. Local implementation tests use
a mocked Airwallex gateway and do not create sandbox customers or cards.

| Customer | FlowCard key | Connected account alias | Cardholder alias | Card alias | USD global account alias |
| --- | --- | --- | --- | --- | --- |
| Bloom & Co. | `bloom` / `acct_bloom001` | `awx-demo-ca-bloom` | `awx-demo-holder-bloom` | `awx-demo-card-bloom` | `awx-demo-ga-bloom` |
| Atlas SaaS | `atlas` / `acct_atlas001` | `awx-demo-ca-atlas` | `awx-demo-holder-atlas` | `awx-demo-card-atlas` | `awx-demo-ga-atlas` |
| Nova Agency | `nova` / `acct_nova001` | `awx-demo-ca-nova` | `awx-demo-holder-nova` | `awx-demo-card-nova` | `awx-demo-ga-nova` |
| Pixel Studio | `pixel` / `acct_pixel001` | `awx-demo-ca-pixel` | `awx-demo-holder-pixel` | `awx-demo-card-pixel` | `awx-demo-ga-pixel` |
| Juniper Labs | `juniper` / `acct_juniper001` | `awx-demo-ca-juniper` | `awx-demo-holder-juniper` | `awx-demo-card-juniper` | `awx-demo-ga-juniper` |

## Opening balances and expected card attempts

The target USD balances are the available balances after preflight seeding, not
guaranteed gross deposit amounts. Read each connected account after seeding and
reconcile any deposit fee or other balance change.

| Customer | Target available USD | First card attempt | Shortfall | Policy outcome |
| --- | ---: | ---: | ---: | --- |
| Bloom & Co. | $2,000 | $9,000 supplier | $7,000 | Block while evidence is under review; after resolution, refuse at Tier C's $4,000 line. |
| Atlas SaaS | $1,500 | $6,500 supplier | $5,000 | Auto bridge after a verified insufficient-funds decline. |
| Nova Agency | $1,000 | $7,000 contractor | $6,000 | Human approval; the later $8,000 settled deposit triggers collection. |
| Pixel Studio | $500 | $2,500 advertising | $2,000 | Refuse at the 90-day history gate. |
| Juniper Labs | $1,000 | $7,000 contractor | $6,000 | Refuse because only $4,000 remains above the facility floor. |

Bloom's $9,000 attempt is below Airwallex's $9,999.99 maximum for one issuing
simulation. Keep the connected-account balance at $2,000 and set the card's
per-transaction limit high enough to allow $9,000. The $7,000 shortfall remains
above Bloom's revised $4,000 line.

## Card and funding configuration

- Use one active USD wallet-funded card and one `READY` cardholder for each
  connected account. Confirm Airwallex enables this card program for the
  platform and region.
- Set each card's per-transaction limit at or above its first attempt and its
  monthly limit to at least $100,000. Allow the merchant category used by each
  scenario.
- Set the account home currency to USD. Keep other available currency balances
  at zero and do not attach cards to a funded Platform Liquidity Program or
  another fallback source that could cover the purchase.
- Use fresh connected accounts for repeatable runs, or read current balances
  before seeding. Do not replay opening-balance deposits without reconciliation.

## Preflight funding through sandbox deposits

Use `POST /api/v1/simulation/deposit/create` to seed connected accounts before
the presenter sequence. The USD currency comes from each USD
`global_account_id`. Send the request on behalf of the customer with
`x-on-behalf-of` and set `status: SETTLED`. Deposit only enough to reach the
target available balance above, then verify it with
`GET /api/v1/balances/current` using the same `x-on-behalf-of` account.

Nova's later $8,000 deposit is a separate sandbox event. Create it only after
the bridge settles, then begin fee-first collection after Airwallex confirms
the deposit as `SETTLED`.

## Failure simulation options

Airwallex's `transaction_failure_reason` input does not accept
`INSUFFICIENT_FUNDS`. Do not send that value or translate a different provider
reason into it.

1. **Use natural account state for the final sandbox run.** Create the issuing
   attempt with the actual card and balance, omit `transaction_failure_reason`,
   and require Airwallex to return `INSUFFICIENT_FUNDS`. This proves the
   intended provider decline and can enter bridge policy.
2. **Use `TRANSACTION_BLOCKED` for a forced API smoke test.** Show its actual
   provider reason and stop before bridge evaluation. This checks the API and
   dashboard decline handling without standing in for a shortfall.
3. **Use the local fixture for the video and deterministic tests.** The
   fixture may carry `INSUFFICIENT_FUNDS`, but label it as fixture data and
   make no Airwallex call. It does not satisfy sandbox-live acceptance.

Recommended sequence: use fixture mode for the video, `TRANSACTION_BLOCKED`
only for a provider smoke check, and a naturally balance-driven decline for
the final sandbox run. If the account setup does not produce the expected
reason, stop that live scenario and ask Airwallex to confirm the supported
sandbox funding configuration.

## Local server configuration

The ignored root `.env` in the main checkout was previously checked for the
presence of these seven variable names. Their values were not copied, printed,
or logged:
or logged:

- `AIRWALLEX_API_KEY`
- `AIRWALLEX_CLIENT_ID`
- `AIRWALLEX_ORG_ID`
- `AIRWALLEX_ACCOUNT_ID`
- `AIRWALLEX_WEBHOOK_EVENTS_FROM`
- `AIRWALLEX_WEBHOOK_ID`
- `AIRWALLEX_WEBHOOK_SECRET`

Variable presence does not verify authentication, key scopes, product
enablement, customer mappings, webhook delivery, or provider success. Do not
copy credentials into this worktree, tests, browser code, or source control.
Node does not load `.env` automatically. From the repository root, export the
ignored `.env` into the server process. From a worktree, source the main
checkout's `.env` by absolute path instead of copying it:

```bash
set -a
source .env
set +a
PORT=8000 npm run server:start
```

The authenticated operator approval route also requires server-only
`KIT5_OPERATOR_ID`. Evidence review requires server-only
`AI_EVIDENCE_REVIEWER_ID`; this must match the operator used with the
`ai-evidence:review` command. Never expose these identities or credentials to
frontend JavaScript.

### Customer, card, and Global Account mappings

For a checkpoint, the server resolves Bloom from one active `customer_roster`
row and one matching active `issuing_card_bindings` row. It does not resolve
provider mappings for the other four customers. Full Sandbox live resolves
each customer before its provider action and requires one active roster row
and matching active card binding per customer. In either mode, missing,
duplicate, inactive, malformed, or mismatched rows disable the applicable
provider action. The seeded roster's account values are synthetic
placeholders, not valid Airwallex IDs. Apply the audit migration:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/018_customer_roster_account_mapping_audit.sql
```

To enable the hybrid checkpoint, map Bloom's placeholder first. Full Sandbox
live requires provider-verified connected-account mappings for all five
customers; map the other aliases when preparing that mode. For each mapping,
first deactivate any active card binding for that customer, then run:

```bash
npm run roster:cli -- map-account \
  --customer-id acct_bloom001 \
  --airwallex-account-id "$VERIFIED_CONNECTED_ACCOUNT_ID" \
  --actor "$OPERATOR_ID" \
  --reason "Verified connected-account mapping"
```

The command requires an active enrolled customer and a non-empty provider ID
distinct from its internal alias. Existing account-ID uniqueness is enforced.
It updates the roster and records old/new IDs, actor, reason, and timestamp in
an immutable audit record within the same transaction. It refuses to run while
an active card binding exists. After mapping succeeds, bind the provider-issued
card with `npm run roster:cli -- bind-card`. Do not enroll duplicates or use a
fixture alias as a provider ID.

For the `kit5-target` scenario, the internal aliases are `acct_bloom001`,
`acct_atlas001`, `acct_nova001`, `acct_pixel001`, and `acct_juniper001`. They
identify local demo customers and are not provider account IDs. The hybrid
checkpoint requires a verified active mapping only for `acct_bloom001`; full
Sandbox live requires verified active mappings for all five aliases. Configure
aliases only against verified active roster entries and card bindings.

Nova's repayment target in the full Sandbox live flow remains disabled unless
the server process has all of these settings:

- `KIT5_NOVA_GLOBAL_ACCOUNT_ID`, a provider-verified USD Global Account ID.
- `KIT5_NOVA_GLOBAL_ACCOUNT_CONNECTED_ACCOUNT_ID`, which must match Nova's
  active roster connected-account ID.
- `KIT5_NOVA_GLOBAL_ACCOUNT_CURRENCY=USD`.

Verify from Airwallex that the Global Account belongs to Nova and is USD before
setting these values. Keep them in the server environment. The route does not
accept them from the browser and has no fallback to an inventory account.
Set `REMOTE_AUTH_REPAYMENT_TERM=asap` for this demo. The $8,000 repayment
deposit is triggered immediately after the bridge settles; a `15_days` or
`30_days` term correctly leaves that deposit unapplied until its due date.

## Hybrid checkpoint acceptance

Before the hybrid checkpoint is used in a demo, an operator must verify
Bloom's active connected-account and issuing-card mapping, the sandbox card's
ability to attempt $9,000, the mapped sandbox funding state, and the server's
operator authentication and Airwallex configuration. Run the attempt and
verify the returned provider status, reason, and IDs in Airwallex and FlowCard.
The expected confirmation is exact `DECLINED` / `INSUFFICIENT_FUNDS`; do not
force the reason. Confirm the checkpoint creates no transfer, retry, fee
charge, or repayment deposit. Confirm presenter continuation starts the
fixture sequence, Bloom remains blocked by the existing evidence-review gate,
and other customers use fixture outcomes. Reset the visual fixture sequence
and verify that it does not issue another Airwallex attempt.

These provider conditions, mappings, and success result have not been verified
by this note. Do not invent provider IDs or claim acceptance has passed.

## Full Sandbox live acceptance

Real customer and account IDs, card configuration, natural provider
`INSUFFICIENT_FUNDS` behavior, provider transaction retrieval, and delivery
through the existing ngrok webhook to a running backend are release gates after
implementation of the separate full Sandbox live path. It requires verified
active connected-account and card mappings for all five customers, plus the
policy, funding, transfer, retry, Nova deposit, collection, webhook, and
reconciliation gates in the flow draft. Do not invent provider IDs or claim
these checks have passed.
The execution plan records the full acceptance criteria.

The Kit 5 Nova deposit operation is implemented with migration
`017_kit5_repayment_deposits.sql`. Before using it, verify the operator session,
Nova connected-account mapping, and USD Global Account mapping on the server.
The route persists one fixed $8,000 USD operation against the approved bridge
before calling the sandbox simulation endpoint. It does not retry an ambiguous
create. The unique statement reference is for reconciliation, not a provider
idempotency key. A signed `deposit.settled` event must match the saved connected
account, Global Account, reference or deposit ID, and internal roster customer
before the existing fee-first collection path runs. The resolver is configured
in `main.ts`, but the provider-issued account and Global Account mappings are
not configured until an operator verifies and supplies them.
