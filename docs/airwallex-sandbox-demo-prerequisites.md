# Airwallex Sandbox Demo Prerequisites

This note records local fixture mappings and the presence of local server
configuration for Kit 5. It does not contain Airwallex-issued identifiers or
evidence of provider behavior. See the execution plan for final sandbox
acceptance gates.

[TOC]

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

## Fixture amounts

The local Bloom scenario uses the values in the Kit 5 source of truth:

| Customer | Opening USD balance | Card attempt | Shortfall |
| --- | ---: | ---: | ---: |
| Bloom & Co. | $2,000 | $9,000 supplier | $7,000 |

The original and Kit 5 target scenarios both use these values. The fixture's
`INSUFFICIENT_FUNDS` reason is synthetic and is not proof of a natural provider
decline.

## Local server configuration

The ignored root `.env` in the main checkout was previously checked for the
presence of these seven variable names. Their values were not copied, printed,
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

## Final sandbox acceptance

Real customer and account IDs, card configuration, natural provider
`INSUFFICIENT_FUNDS` behavior, provider transaction retrieval, and delivery
through the existing ngrok webhook to a running backend are release gates after
implementation. Do not invent provider IDs or claim these checks have passed.
The execution plan records the full acceptance criteria.
