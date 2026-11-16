# Demo product read contracts

Implemented locally in `feat/demo-ui-go`; deployment is separate.
These read-only contracts supply the preserved Bridge Capital interface.

[TOC]

## Routes and authority

| Gateway GET | Funding GET | Principal |
| --- | --- | --- |
| `/api/operator/portfolio` | `/api/servicing/portfolio` | Verified OPERATOR |
| `/api/operator/customers?cursor=...` | `/api/servicing/customers?cursor=...` | Verified OPERATOR |
| `/api/operator/customers/{id}` | `/api/servicing/customers/{id}` | Verified OPERATOR |
| `/api/operator/customers/{id}/bridges?cursor=...` | `/api/bridges?customer_id={id}&cursor=...` | Verified OPERATOR |
| `/api/customer/bridges?cursor=...` | `/api/bridges?cursor=...` | Verified CUSTOMER; owner infers scope |
| `/api/customer/wallet` | `/api/servicing/wallet` | Verified CUSTOMER; owner infers active wallet |

Gateway session and immutable binding are checked before a signed owner read.
Funding resolves the current active actor and role. Broad Coordinator and
underwriting readers cannot use these actor endpoints. A CUSTOMER cannot pass
customer, mode or facility selectors. Known-ID reads retain owner scope checks.
No financial writer is invoked by a read. Portfolio/history reads use the
owner database; the wallet read uses the existing read-only provider boundary.

## Common fields

Responses contain `source_mode`, `source_binding_id`, `facility_id` and `as_of`.
Money uses integer USD cents. Unknown fields are null, never fabricated zero.
Arrays always encode as arrays. Reads use no-store and bounded response sizes.
The gateway verifies installation, timestamps and safe integer values.

Lists return `items` and `next_cursor` (null at end), 25 rows maximum.
Customer sort is ascending stable ID; bridge sort is descending created time
and ID. Canonical base64url cursors bind mode, facility, installation, route
and exact customer scope. They encode a live keyset position, not a frozen
export. Customer/history detail versions remain authoritative.

Malformed query/cursor returns 400; missing session/assertion 401; wrong role
403; missing owner record 404; source/storage unavailable 503. Source auth
failures at the gateway become 503 without clearing the browser session.
Existing bridge/collection version conflicts remain 409.

## Portfolio

Fields: `facility_version`, `facility_total_cents`, `reserve_bps`,
`reserve_floor_cents`, `funded_principal_cents`, `reserved_principal_cents`,
`other_commitments_cents`, `available_capital_cents`, `current_regime`,
`principal_owed_cents`, `unpaid_fee_cents`, `fees_collected_cents`,
`history_complete`, `lifecycle_counts`, `held_bridge_count`, `policy_id`,
`policy_hash`, plus common fields.

Capital uses the shared pure policy description and ceiling reserve rounding.
Other commitments enter the same normalization as the Funding evaluator once.
The result describes current accounting and does not authorize funding.

Fees collected are credits minus debits to contract/late fee receivables on
verified COLLECTION_SETTLEMENT/COLLECTION_REVERSAL ledger transactions for
this facility. Provider costs, original fee recognition and principal are
excluded. Incomplete customer opening history makes this total null.
All portfolio queries share one read-only repeatable-read transaction.

## Customers

Each row contains `customer_id`, nullable `display_name`, `mapping_active`,
`accounting_available`, nullable `principal_owed_cents`, `unpaid_fee_cents`,
`reserved_principal_cents`, `reserved_fee_cents`, `history_complete`,
`active_hold`, `version`, `captured_at` and `active_bridge_id`.
A mapped customer without accounting retains null amounts. Detail wraps the
same shape in `customer`, with the common source fields.

Synthetic Bloom/Atlas/Nova labels are keyed by exact DEMO IDs for display.
They do not bind a servicing account to an onboarding company or user.
Other customers have no invented label. Raw provider/card identifiers and
financial signing credentials are never returned.

## Bridge discovery

Each row contains `id`, `customer_id`, `offer_id`, nullable
`originating_case_id`, `evaluation_id`, `version`, `lifecycle`, `created_at`,
nullable `funded_at`, `due_on`, principal/contract-fee/late-fee owed cents,
`principal_original_cents`, `contract_fee_original_cents`, `term_days` and
`hold_reasons`.

Discovery includes repaid and unfunded historical attempts. Original terms
come from the linked offer; current debt comes from the owner ledger. Existing
bridge/operation/collection detail routes supply certainty and allocations.
No customer needs an operator receipt or a manually pasted bridge UUID.

## Current wallet balance

The wallet response includes common source fields, `customer_id`, USD
`currency`, `available_balance_cents`, `requested_at`, `observed_at` and
`provider_mode`. The owner resolves an active customer/account mapping, reads
the current provider balance outside DB locks, validates currency/freshness and
rechecks the mapping before returning. It never returns raw provider IDs,
credentials or evidence bodies. Unknown/unavailable balance is 503, not zero.

`observed_at` records the server receipt of the provider response, not a bank
statement posting time. Available cash is informational and remains separate
from the customer credit line, facility capital and bridge debt. No account
selector is accepted from the customer and no financial command is enabled
by exposing this read.
