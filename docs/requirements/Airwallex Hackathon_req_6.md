# Bridge Capital Underwriting: Airwallex Data Inventory

Oct 18, 2026 · Charley

## Bottom line

Airwallex holds enough per-customer data to set a bridge limit without an outside bureau, but the MCP connector cannot reach any of it per customer. Every per-customer read must go over REST with the header `x-on-behalf-of: <connected account id>`.

Five signals carry most of the decision; everything else in this doc modifies them:

1. **Deposit reliability**: `GET /api/v1/deposits` gives cadence, size and settled vs rejected/reversed. Lateness needs the expected date from the CRM.  
2. **Shortfall history**: declined card transactions with `failure_reason = INSUFFICIENT_FUNDS`. This is the direct label for the problem being solved.  
3. **Balance trajectory**: `GET /api/v1/balances/history` gives daily minimum balance, burn rate and days near zero.  
4. **Repayment record**: `GET /api/v1/connected_account_transfers?destination=` (bridges sent) and `GET /api/v1/charges?source=` (repayments and fees collected).  
5. **Standing gates**: `GET /api/v1/accounts/{id}` status plus `GET /api/v1/rfis`. Suspended, dormant or an open RFI means a limit of zero.

## Sandbox reconnaissance

The team sandbox was queried live on 2026-10-18 through the Airwallex Developer connector. It holds the platform account only; no connected-account data was visible through that path.

| Object | Observed | Underwriting relevance |
| ----- | ----- | ----- |
| Platform account | `acct_b07vyeDXPACSJGZR0HKv1Q`, "Sandbox Business", US, EIN on file, ACTIVE since 2026-10-04 | Same field set every connected account returns |
| Wallet balances | About 10,000,000 each in USD, EUR, GBP, CNY; zero elsewhere; nothing pending or reserved | Unrealistic. Bridge budget and reserve floor must be app-level parameters or rationing never binds |
| Global Account | 1 USD account (Community Federal Savings Bank); ACH, Fedwire, SWIFT inbound; direct debit enabled | Inbound rails available per customer |
| Deposits | 1 settled bank transfer, \$500, payer "Kit5 Test Payer", settled 1 second after creation | Simulated deposits settle instantly; lateness is not observable natively |
| Cardholders | 3 (2 DELEGATE, 1 INDIVIDUAL), all READY | Headcount proxy |
| Cards | 2 active Visa. Card ending 0134: virtual, program CREDIT / GOOD\_FUNDS\_CREDIT, \$300 per-transaction and \$2,000 monthly limit, \$1,800 remaining, MCCs 5411, 5812, 5814, 5734 allowed | Card limits cap the largest single bridge request |
| Card transactions | 5 total: 2 cleared (\$50, \$150); 3 declined (LIMIT\_EXCEEDED \$150, MERCHANT\_CATEGORY\_NOT\_ALLOWED \$20 at MCC 7995, CARD\_INACTIVE \$10) | Decline reasons are machine-readable; no INSUFFICIENT\_FUNDS decline exists yet |
| Balance history | Each card spend posts as AUTHORISATION\_HOLD, then AUTHORISATION\_RELEASE, then CAPTURE | Net spend must be computed from captures, not holds |
| Transfers, payment intents, payment disputes, direct debits, linked accounts, FX conversions, billing invoices, spend bills | Empty | Endpoints respond; data must be seeded |
| Beneficiaries | 7 sandbox defaults (CN, DE, GB, US bank accounts) | Not customer data |

## Integration constraints

The context fetcher must be built on REST, not MCP. MCP authenticates as the platform and cannot send `x-on-behalf-of`, so its tools return platform-account data only.

* **Auth**: `POST /api/v1/authentication/login` with client ID and API key returns a Bearer token. Sandbox base URL is `https://api.sandbox.airwallex.com`.  
* **Per-account reads** (header `x-on-behalf-of: acct_...`): balances, balance history, deposits, global accounts, linked accounts, issuing cards and transactions, transfers, RFIs, capabilities, funding limits.  
* **Platform-scoped reads with a filter**: accounts list, `connected_account_transfers?destination=`, `charges?source=`, payment acceptance `?connected_account_id=`, platform reports (all accounts in one file).  
* **Window caps per call**: balance history 7 days; transfers 30 days; global account transactions 31 days; platform reports 366 days. Card transactions, payment intents and disputes are retained 2 years.  
* **Platform report download URL** expires 1 minute after it is issued. Fetch it immediately on `platform_report.completed`.  
* **Enablement required from Airwallex**: Linked Account APIs and native RFI calls on behalf of connected accounts. Expect permission errors in sandbox until enabled.  
* **API version 2026-07-17** removed `lifecycle_id` from issuing transactions and authorizations; use `card_transaction_data`. `GET /api/v1/issuing/transactions` is deprecated in favor of `card_transactions`, `card_transaction_events` and `lifecycles`.  
* **Latency budget**: remote authorization gives 2.5 seconds per card decision. The real-time path reads cached features plus `GET /api/v1/balances/current`; it never calls history endpoints live.

## A. Eligibility and account standing

These fields decide whether a customer gets any limit at all, and supply the OFAC screening inputs.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Account status and tenure | `GET /api/v1/accounts/{id}` | `status`, `created_at`, `suspend_details`, `reactivate_details`, `next_action`, `requirements` | Gate: only ACTIVE is eligible. Tenure \= today minus `created_at` |
| Business identity | `GET /api/v1/accounts/{id}` | `business_details`: name, registration address, identifiers (EIN), URLs, operating countries | OFAC screening on the entity; geography risk; web presence |
| Owners and controllers | `GET /api/v1/accounts/{id}` | `business_person_details`: nationality, roles (BENEFICIAL\_OWNER, CONTROLLING\_PERSON, AUTHORISED\_PERSON) | OFAC screening on each owner; PII depth depends on `view_type` |
| Legal entity grouping | `GET /api/v1/accounts/{id}` | `legal_entity_id` | Sum exposure across several accounts of one entity |
| CRM join key | `GET /api/v1/accounts/{id}` | `identifier`, `metadata` | Store the Salesforce ID here at account creation |
| Onboarding device | `GET /api/v1/accounts/{id}` | `terms_and_conditions.device_data`: IP address, user agent, `agreed_at` | Fraud check: onboarding IP geography vs business address |
| Open compliance requests | `GET /api/v1/rfis` | `status`, `type` (KYC, TRANSACTION, CARDHOLDER, PAYMENT\_ENABLEMENT), questions, `created_at` | Gate: open KYC or TRANSACTION RFI means zero. RFI count and age measure compliance friction |
| Capability status | `GET /api/v1/account_capabilities/{id}` | `status` (ENABLED, DISABLED, PENDING), `details.reason_codes`, `details.mcc` | Disabled reason codes expose Airwallex's own risk view; assigned MCC gives industry |
| Airwallex funding limit | `GET /api/v1/account_capabilities/funding_limits` | `limit`, `available`, `requested_limit`, `status` | Airwallex's risk-assigned direct-debit limit: a free external prior on size |
| Whole-book KYC/KYB | `POST /api/v1/platform_reports/create` type `ACCOUNT_REPORT` | KYC and KYB fields for every connected account | Nightly batch source for all customers at once |

Webhooks: `account.active`, `account.action_required`, `account.suspended`, `account.dormant`, `account.closed`, `account.offboarding.pending`, `account.offboarding.completed`, `rfi.action_required`.

## B. Liquidity and balance behavior

Balance history is the richest single source: one ledger line per balance change, with the running balance attached.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Current balance | `GET /api/v1/balances/current` | Per currency: `available_amount`, `pending_amount`, `reserved_amount`, `total_amount`, `prepayment_amount`; `account_type` (cash, yield, credit) | Live shortfall \= transaction amount minus available. Pending inflows shorten the bridge |
| Balance history | `GET /api/v1/balances/history` (7-day windows) | `posted_at`, `amount`, `balance`, `source_type`, `transaction_type`, `fee` | Daily minimum, mean and closing balance; volatility; days at or near zero; net burn; inflow/outflow ratio |
| Settlement calendar | `GET /api/v1/financial_transactions` | `transaction_type`, `source_type`, `status` (PENDING, SETTLED), `estimated_settled_at`, `net`, `fee` | When pending money lands, which sets bridge tenor |
| Whole-book balances | Platform report `BALANCE_REPORT` | `account_id`, currency, available, reserved, total, `last_update` | All customers in one nightly call |
| Whole-book ledger | Platform report `TRANSACTION_RECON_REPORT` (up to 366 days) | Every financial transaction across all connected accounts | Backfill 12 months of history at onboarding |
| Statements | Financial reports API, account statement report | Minimum and maximum balance per currency over a period | Pre-computed balance extremes |
| Multi-currency mix | `GET /api/v1/balances/current` | Non-zero currencies | Currency mismatch between spend and deposits adds FX risk |

Webhooks: `balance.ga.top_up`, `balance.va.top_up`, `balance.adjustment`.

## C. Funding and deposit reliability

This section measures the exact behavior the product bridges: whether deposits arrive, how late, and whether they stick.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Deposits | `GET /api/v1/deposits`, `GET /api/v1/deposits/{id}` | `amount`, `currency`, `status` (PENDING, SETTLED, REJECTED, REVERSED), `type` (BANK\_TRANSFER, DIRECT\_DEBIT), `created_at`, `settled_at`, `payer.name`, `reference` | Cadence, median size, gap variability, on-time rate and days-late distribution against CRM expected dates, rejected and reversed counts |
| Inbound bank credits | `GET /api/v1/global_accounts/{id}/transactions` (31-day windows) | `amount`, `payer_name`, `payer_country`, `status`, `type` (CREDIT, DEBIT), `create_time` | Payer concentration (one payer is fragile); cross-border share |
| Collection rails | `GET /api/v1/global_accounts` | Currencies, clearing systems, `status` | Number of ways money can come in |
| Linked external bank | `GET /api/v1/linked_accounts`, `GET /api/v1/linked_accounts/{id}` | `status`, `capabilities.balance_check`, `capabilities.direct_debit_deposit`, `failure_details` | A verified outside bank account is a repayment fallback |
| External bank balance | `GET /api/v1/linked_accounts/{id}/balances` (Plaid or TrueLayer links only) | `available_amount`, `currency` | Strongest capacity signal: money exists elsewhere, so the gap is timing, not solvency |
| Direct debit mandate | `GET /api/v1/linked_accounts/{id}/mandate` | Mandate status | Platform can pull repayment automatically; supports a higher limit |
| NSF on external bank | Deposits of type DIRECT\_DEBIT; transfer `funding.failure_details` | `code` (insufficient\_funds), `iso_code` (AM04), ACH return R01 | Hard negative: the outside account was empty |
| Third-party pulls | `GET /api/v1/direct_debits` | `status` (created, in review, pending, rejected, returned, settled), amounts | Recurring obligations drawn from the wallet; returns signal distress |

Webhooks: `deposit.pending`, `deposit.settled`, `deposit.rejected`, `deposit.reversed`, `direct_debit.returned`, `direct_debit.rejected`, `linked_account.failed`, `linked_account.suspended`.

## D. Card spend behavior

Card data sizes the need: how much the customer spends, when, on what, and how often the wallet has already run dry.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Card transactions | `GET /api/v1/issuing/card_transactions` | `status`, `failure_reason`, `billing_amounts.*`, `merchant.category_code`, `merchant.country`, merchant brand IDs, `fund_direction`, `digital_wallet_token`, `created_at` | Monthly spend, volatility, day-of-month peaks, MCC mix, merchant concentration, recurring merchants, decline rate by reason |
| Shortfall label | Same endpoint, `status=DECLINED`, `failure_reason=INSUFFICIENT_FUNDS` | Count, amount, date | Historical shortfall frequency and size; the p90 shortfall is a natural base size |
| Transaction events | `GET /api/v1/issuing/card_transaction_events` | `subtype` (incremental auth, partial reversal), `process_result`, `transaction_category` (PURCHASE, CASH\_DISBURSEMENT, QUASI\_CASH, BILL\_PAYMENT), `pos_entry_mode`, `conversion_details`, `risk_details`, `remote_auth` | Cash and quasi-cash use is a red flag; `risk_factors` and failed 3DS indicate fraud |
| Lifecycles | `GET /api/v1/issuing/lifecycles` | Aggregated `billing_amounts` per payment | Cheap spend totals without summing events |
| Pending holds | `GET /api/v1/issuing/authorizations` | Open authorizations | Committed spend not yet captured |
| Cards and controls | `GET /api/v1/issuing/cards`, `GET /api/v1/issuing/cards/{id}` | `card_status`, `program.type` and `sub_type`, `form_factor`, `authorization_controls` (allowed MCCs, countries, `transaction_limits`), `auto_close_date` | Card limit caps the per-transaction bridge; number of active cards |
| Limit utilization | `GET /api/v1/issuing/cards/{id}/limits` | `amount`, `remaining` per interval | How close the customer runs to its own card limits |
| Cardholders | `GET /api/v1/issuing/cardholders` | Count, `status`, `type` | Headcount proxy; DISABLED cardholders |
| Card disputes | `GET /api/v1/issuing/transaction_disputes` | `status`, outcome | Spend that may reverse; abuse pattern |
| Whole-book spend | Platform report `ISSUING_TRANSACTION_REPORT` | Every issuing transaction across accounts | Nightly batch source |

Webhooks: `issuing.card_transaction.authorized`, `.cleared`, `.declined`, `.reversed`; `issuing.transaction.failed`; `issuing.card.low_remaining_transaction_limit`; `issuing.card.blocked`, `.lost`, `.stolen`; `issuing.transaction_dispute.*`.

In production the "should we fund" call happens inside remote authorization. Its payload already carries `card_id`, amount, merchant, MCC, `billing_order` and 3DS data, with 2.5 seconds to answer.

## E. Revenue and receivables

These show the customer's repayment source, but they populate only if the customer also collects payments or invoices through Airwallex. For a spend-only customer they are empty and CRM or accounting data substitutes.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Card acquiring revenue | `GET /api/v1/pa/payment_intents?connected_account_id=` | `amount`, `currency`, `status`, `created_at` | Sales run-rate: the primary repayment source |
| Settlement timing | `GET /api/v1/financial_transactions` (types PAYMENT, PAYMENT\_RESERVE\_HOLD, PAYMENT\_RESERVE\_RELEASE); settlements list | `estimated_settled_at`, `settled_at`, reserve amounts | When receivables become cash; an Airwallex reserve hold is a risk flag on the merchant |
| Refunds | `GET /api/v1/pa/refunds?connected_account_id=` | Amount, `status` | Refund ratio \= revenue quality |
| Chargebacks | `GET /api/v1/pa/payment_disputes?connected_account_id=` | `stage`, `status`, `reason.type` (e.g. FRAUDULENT), `amount`, `due_at` | Chargeback rate; lost disputes debit the wallet (open team concern) |
| Invoices issued | `GET /api/v1/billing/invoices` | `status`, `payment_status` (UNPAID, PAID), amounts, dates | Outstanding receivables \= expected inflows; late-paying customers of the customer |
| Invoice collection | `GET /api/v1/billing/billing_transactions?invoice_id=` | `status`, `succeeded_at` | Collection success rate |
| Subscriptions | Billing subscriptions list | `status` (active, unpaid, cancelled) | Recurring revenue stability |

Webhooks: `payment_intent.succeeded`, `payment_attempt.settled`, `refund.settled`, `invoice.paid`, `invoice.payment_failed`, `subscription.unpaid`.

## F. Obligations and outflows

Scheduled outflows compete with bridge repayment for the same incoming deposit.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Payouts | `GET /api/v1/transfers` | `transfer_amount`, `status`, `transfer_date`, `beneficiary`, `funding.status`, `funding.failure_details`, `failure.code`, `reason` | Upcoming outflows; a payout that failed funding for insufficient funds signals distress |
| Payees | `GET /api/v1/beneficiaries` | Count, `bank_country_code`, `entity_type` | Payee geography for sanctions exposure; payroll-like patterns |
| Payables (AP) | `GET /api/v1/spend/bills` | `status` (AWAITING\_PAYMENT, SCHEDULED, PAID), `billing_amount`, `due_date`, `vendor_id` | Bills due before the expected deposit reduce repayment capacity |
| Vendors | Spend vendors list | Vendor count and concentration | Dependence on a few suppliers |
| FX conversions | FX conversions list | `status` (SCHEDULED, OVERDUE, SETTLED), currency pair, amounts | An OVERDUE conversion signals distress; spend currency vs deposit currency mismatch |
| Third-party pulls | `GET /api/v1/direct_debits` | See section C | Recurring obligations |
| Whole-book payouts | Platform report `PAYOUT_REPORT` | Every payout across accounts, including `failure_reason` | Nightly batch source |

Webhooks: `transfer.new`, `transfer.pending`, `transfer.settled`, `transfer.suspended`, `transfer.failed`.

## G. Bridge history and platform exposure

The platform's own ledger with each customer can be rebuilt entirely from Airwallex, provided every transfer and charge is tagged consistently.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Bridges sent | `GET /api/v1/connected_account_transfers?destination=acct_...` | `amount`, `status` (NEW, PENDING, SETTLED, SUSPENDED, FAILED), `created_at`, `reason`, `reference`, `request_id`, `failure_reasons` | Count and size of past bridges. Failure reasons such as WATCHLIST\_RESTRICTION\_BENEFICIARY or OTHER\_HIGH\_RISK\_BENEFICIARY are Airwallex compliance blocks |
| Idempotency check | `GET /api/v1/connected_account_transfers?request_id=` | Match on `request_id` | Required before any retry after a non-success response |
| Repayments and fees | `GET /api/v1/charges?source=acct_...` | `amount`, `status` (NEW, PENDING, SETTLED, SUSPENDED, FAILED), `created_at`, `reason` | Days from bridge to repayment; a FAILED repayment charge is a default event |
| Outstanding exposure | Derived | Settled bridges minus settled repayment charges | Available headroom \= assigned limit minus outstanding |
| Fee revenue | Charges with fee reason; platform report `APPLICATION_FEE_REPORT` | Amounts, status | Customer profitability, which feeds rationing priority |

Tagging convention: prefix `request_id` and set `reason` as `bridge-`, `repay-` or `fee-` so these three flows separate without a local database.

Webhooks: `connected_account_transfer.new`, `.pending`, `.settled`, `.suspended`, `.failed`; `charge.new`, `.pending`, `.settled`, `.suspended`, `.failed`.

## Internal client profile (CRM)

The expected funding schedule is the one CRM field the model cannot run without: Airwallex records when a deposit arrived, never when it was due.

| Field | Why it matters |
| ----- | ----- |
| Contracted funding schedule (expected deposit date, amount, frequency) | Defines "late"; lateness and days-late are uncomputable without it |
| Customer since, contract start, renewal date | Tenure; churn risk rises near renewal |
| Plan tier, fees and ARR paid to the platform | Customer value; sets expected-loss tolerance and rationing priority |
| Industry (NAICS), business model, seasonality | Expected spend volatility; seasonal shortfalls are lower risk |
| Annual revenue, employee count, funding stage, last raise date, cash runway | Capacity to repay |
| External business credit (D\&B PAYDEX, Experian Intelliscore), guarantor score | Independent default prior |
| Accounting integration (QuickBooks, Xero, NetSuite): AR aging, AP aging, P\&L | Receivables and payables that never touch Airwallex |
| External bank data via Plaid, if not linked in Airwallex | Balance held elsewhere |
| Collections history: past-due platform invoices, write-offs | Payment behavior toward the platform itself |
| Personal guarantee, UCC liens, existing debt or merchant cash advances | Seniority of the platform's claim |
| Sanctions and adverse-media screening result with timestamp | Rule 3: OFAC SDN check on entity and owners, logged for audit |
| Corporate family: parent, subsidiaries, related accounts | Group-level exposure cap |
| Support escalations, NPS, account-owner notes, manual risk flags | Soft signals of distress or disputes |
| Manual limit overrides with reason and approver | Transparency: every non-model limit is explained |
| Profile-change events (address, owner, bank details) | Re-score trigger; owner or bank changes are fraud markers |

## Refresh model

Limits are computed in the background, by a nightly batch plus event-driven re-scores, so the live decision touches only the cached limit and one balance call.

Architecture: three sources (nightly batch of platform reports and per-account REST history; Airwallex webhooks; CRM sync from Salesforce) feed a client feature store that records value, endpoint and timestamp per feature. Limit assignment (gates, base, modifiers, portfolio reserve floor) reads the store. The live funding decision reads the cached limit and features plus `GET /api/v1/balances/current` and answers within 2.5 seconds.

Webhooks bypass the 24-hour cycle for the events below; everything else waits for the nightly batch.

| Event | Effect on the limit |
| ----- | ----- |
| `account.suspended`, `account.dormant`, `account.action_required`, `rfi.action_required` | Set to zero immediately |
| `charge.failed` (repayment) | Freeze; record a default event |
| `deposit.rejected`, `deposit.reversed`, `direct_debit.returned` | Cut and re-score |
| `connected_account_transfer.failed`, `connected_account_transfer.suspended` | Query by `request_id` before any retry; flag the customer |
| `deposit.settled`, `balance.ga.top_up` | Recompute outstanding exposure; trigger the repayment charge |
| `issuing.card_transaction.declined` with INSUFFICIENT\_FUNDS | Append to shortfall history |
| `issuing.transaction_dispute.*`, payment dispute events | Re-score |
| CRM profile update | Re-score |

## How signals map to the limit

Signals fall into four roles; the Decision Policy owner sets the weights and thresholds.

1. **Gates** (any failure sets the limit to zero): account ACTIVE; no open KYC or TRANSACTION RFI; entity and every owner clear on OFAC; no FAILED repayment charge outstanding; no deposit reversed within the policy window.  
2. **Base size**: the smallest of the customer's typical shortfall, a share of verified monthly inflow, and the card's per-transaction limit.  
3. **Modifiers**: raise for on-time deposits, long tenure, a linked external balance, a direct-debit mandate and clean repayments. Cut for late deposits, payer concentration, risky MCCs, prior declines for fraud, chargebacks and card disputes.  
4. **Portfolio constraint**: limits are checked against the platform reserve floor and a per-customer concentration cap; when capital is scarce, rank requests by expected loss per dollar and customer value.

A starting form for the base size, with s \= shortfall amounts from INSUFFICIENT\_FUNDS declines and I \= trailing 90-day settled deposits:

**L\_base \= min( P90(s), α × I / 3, C\_txn ),  L \= L\_base × Π m\_i**

α is the policy share of monthly inflow, C\_txn the card's per-transaction limit, and each m\_i a modifier. Transparency (rule 1\) requires storing every feature value with its source endpoint and fetch timestamp beside the limit it produced.

## Sandbox gaps and test-data plan

The sandbox cannot produce history on its own, so the demo needs seeded customers with deliberately different profiles.

| Gap | Consequence | Workaround |
| ----- | ----- | ----- |
| Simulated deposits settle instantly and cannot be backdated | No native lateness and only days of history | Put expected dates in the CRM mock; send `POST /api/v1/simulation/deposit/create` after them. Seed 90 days of synthetic history in the profile mock and use live Airwallex for current state |
| No INSUFFICIENT\_FUNDS declines exist | The shortfall label is empty | Simulate spend above wallet balance but within the card limit, with `x-on-behalf-of` on `POST /api/v1/simulation/issuing/create` |
| Platform wallet holds about 10M per major currency | The reserve floor never binds | Set the bridge budget and reserve floor in app config, well below total customer need |
| Linked-account balances need Plaid or TrueLayer and Airwallex enablement | External balance signal unavailable | Mark as production-only; mock the value in the CRM profile |
| RFI calls on behalf of connected accounts need enablement | Compliance gate untestable live | Read `account.status` live; mock RFI state |
| Acquiring, Billing and Spend data are empty | Section E and AP signals absent | Treat as optional; the model must run without them |

Three archetype customers force rationing: one reliable payer with early deposits, one chronically late payer who always repays, and one new, volatile account with a chargeback and a reversed deposit.

## Sources

* Live sandbox queries through the Airwallex Developer connector, 2026-10-18: account details, balances, balance history, global accounts, deposits, cardholders, cards, card limits, card transactions, transfers, payment intents, disputes, direct debits, linked accounts, FX conversions, beneficiaries, billing invoices, spend bills  
* [Retrieve a connected account](https://www.airwallex.com/docs/api/scale/accounts/retrieve.md)  
* [List RFIs](https://www.airwallex.com/docs/api/risk/request_for_information_rfi/list.md)  
* [Retrieve an account capability](https://www.airwallex.com/docs/api/account_capability/account_capability/retrieve.md)  
* [Get funding limits](https://www.airwallex.com/docs/api/account_capability/account_capability/funding_limits.md)  
* [Linked account balances](https://www.airwallex.com/docs/api/core_resources/linked_accounts/balances.md)  
* [List financial transactions](https://www.airwallex.com/docs/api/finance/financial_transactions/list.md)  
* [Card transactions](https://www.airwallex.com/docs/api/issuing/card_transactions.md)  
* [Card transaction events](https://www.airwallex.com/docs/api/issuing/card_transaction_events.md)  
* [List connected account transfers](https://www.airwallex.com/docs/api/scale/connected_account_transfers/list.md)  
* [List charges](https://www.airwallex.com/docs/api/scale/charges/list.md)  
* [Create a platform report](https://www.airwallex.com/docs/api/scale/platform_reports/create.md)  
* [List payment disputes](https://www.airwallex.com/docs/api/payments/payment_disputes/list.md)  
* [List billing invoices](https://www.airwallex.com/docs/api/billing/invoices/list.md)  
* [List spend bills](https://www.airwallex.com/docs/api/spend/bills/list.md)  
* [Account webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/account.md)  
* [Deposit webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/deposits.md)  
* [Issuing webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/issuing.md)

