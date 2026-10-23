# Airwallex Sandbox Demo Prerequisites

This document maps the Kit 5 customers to fictional sandbox fixture aliases and
records the account state needed for the card decline sequence. The `awx-demo-*`
values are invented aliases, not Airwallex-issued IDs, and must be replaced
before any sandbox API call.

[TOC]

## Customer and card mapping

| Customer | FlowCard key | Connected account alias | Cardholder alias | Card alias | USD global account alias |
| --- | --- | --- | --- | --- | --- |
| Bloom & Co. | `bloom` / `acct_bloom001` | `awx-demo-ca-bloom` | `awx-demo-holder-bloom` | `awx-demo-card-bloom` | `awx-demo-ga-bloom` |
| Atlas SaaS | `atlas` / `acct_atlas001` | `awx-demo-ca-atlas` | `awx-demo-holder-atlas` | `awx-demo-card-atlas` | `awx-demo-ga-atlas` |
| Nova Agency | `nova` / `acct_nova001` | `awx-demo-ca-nova` | `awx-demo-holder-nova` | `awx-demo-card-nova` | `awx-demo-ga-nova` |
| Pixel Studio | `pixel` / `acct_pixel001` | `awx-demo-ca-pixel` | `awx-demo-holder-pixel` | `awx-demo-card-pixel` | `awx-demo-ga-pixel` |
| Juniper Labs | `juniper` / `acct_juniper001` | `awx-demo-ca-juniper` | `awx-demo-holder-juniper` | `awx-demo-card-juniper` | `awx-demo-ga-juniper` |

These values are fixture keys only. Replace them with real connected account
IDs, cardholder IDs, card UUIDs, and Global Account IDs obtained from the
sandbox before enabling sandbox-live mode. Resolve customer-specific data with
REST and `x-on-behalf-of`; the Developer MCP inventory is platform scoped.

## Opening balances and expected card attempts

The balances below describe the target USD available balance after preflight
seeding, not a guaranteed gross deposit amount. Read each connected account's
balance after seeding and reconcile any deposit fee or other balance change.

| Customer | Target available USD | First card attempt | Expected shortfall | Policy outcome |
| --- | ---: | ---: | ---: | --- |
| Bloom & Co. | $2,000 | $9,000 supplier | $7,000 | Refuse: Tier C is closed and the line is $4,000. |
| Atlas SaaS | $1,500 | $6,500 supplier | $5,000 | Auto bridge after a verified insufficient-funds decline. |
| Nova Agency | $1,000 | $7,000 contractor | $6,000 | Human approval; the later $8,000 settled deposit triggers collection. |
| Pixel Studio | $500 | $2,500 advertising | $2,000 | Refuse at the 90-day history gate. |
| Juniper Labs | $1,000 | $7,000 contractor | $6,000 | Refuse because only $4,000 remains above the facility floor. |

Bloom's $9,000 amount is below Airwallex's $9,999.99 maximum for one issuing
simulation. Keep its connected-account balance at $2,000 and configure the
card's own per-transaction limit to allow $9,000. Its $7,000 shortfall remains
above the revised $4,000 credit line, so Bloom still receives no bridge.

## Card and funding configuration

- Use one active USD wallet-funded card and one `READY` cardholder for each
  connected account. Confirm Airwallex enables this card program for the
  platform and region.
- Set the card's per-transaction limit at or above its first attempt and its
  monthly limit to at least $100,000. Allow the merchant category used by each
  scenario.
- Set the account home currency to USD. Keep other available currency balances
  at zero and do not attach the cards to a funded Platform Liquidity Program
  or another fallback source that could cover the purchase.
- Use fresh connected accounts for a repeatable run, or read current balances
  before seeding. Do not replay opening-balance deposits without reconciliation.

## Preflight funding through sandbox deposits

Use `POST /api/v1/simulation/deposit/create` to seed the connected accounts
before the presenter sequence. The USD currency comes from each USD
`global_account_id`. Send the request on behalf of the customer with
`x-on-behalf-of` and use `status: SETTLED`. Set the deposit amount to reach the
target available balance shown above, then verify the result with
`GET /api/v1/balances/current` using the same `x-on-behalf-of` account.

Nova's later $8,000 deposit is a separate sandbox event. Create it only after
the bridge has settled, and begin fee-first collection only after the deposit
is confirmed as `SETTLED`.

## Failure simulation options

Airwallex's `transaction_failure_reason` input does not accept
`INSUFFICIENT_FUNDS`. Do not send that value or translate a different provider
reason into it.

1. **Use natural account state for the final sandbox run.** Create the issuing
   attempt with the actual card and balance, omit `transaction_failure_reason`,
   and require Airwallex to return `INSUFFICIENT_FUNDS`. This is the only option
   that proves the intended provider decline and can enter the bridge policy.

2. **Use `TRANSACTION_BLOCKED` for a forced API smoke test.** This is an
   accepted simulation input. Show its actual provider reason and stop before
   bridge evaluation; it checks the API and dashboard decline handling without
   standing in for a shortfall.

3. **Use the local fixture for the video and deterministic tests.** The fixture
   may carry `INSUFFICIENT_FUNDS`, but label it as fixture data and make no
   Airwallex call. It does not satisfy sandbox-live acceptance.

Recommended sequence: use fixture mode for the video, `TRANSACTION_BLOCKED`
only for a provider smoke check, and a naturally balance-driven decline for the
final sandbox run. If the account setup does not produce the expected reason,
stop that live scenario and ask Airwallex to confirm the supported sandbox
funding configuration.
