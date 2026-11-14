# FlowCard Bridge Capital Requirements

This is the consolidated product and integration requirements document for
FlowCard Bridge Capital. It replaces the overlapping Airwallex Hackathon
requirement drafts that were previously stored in this directory.

[TOC]

## Scope and authority

- The current user-facing product is the Bridge Capital back office and
  customer portal.
- The Kit 5 and GD dashboards, their presenter scripts, and their demo-only
  screen behavior are retired. Some Kit 5 server APIs remain for authenticated
  API clients; they are compatibility surfaces, not current product UI.
- Current Bridge Capital policy is summarized here and specified in the
  [Bridge Capital policy design](/docs/designs/bridge-capital-policy-spec.md).
  Runtime thresholds live in `src/features/bridge-capital/policy/`.
- Numeric policy values in this file are demo defaults, not approval for
  production credit policy.
- Older drafts disagreed on facility floors, fee formulas, request ordering,
  and the role of AI. This document uses the current Bridge Capital policy for
  the product and keeps the retained server API behavior separate.

## Product goal and user flow

FlowCard issues corporate cards to small businesses using Airwallex connected
accounts. When a customer lacks enough available wallet balance for a card
payment, the platform can bridge the shortfall from its configured facility,
then collect principal and fees from later customer deposits.

The product has two primary user surfaces:

- **Bridge Capital back office:** gives credit and operations staff the request
  queue, customer and bridge details, policy reasons, rules, and operator
  controls.
- **Customer portal:** lets a customer review bridge offers, account activity,
  repayment status, and balances.

The customer must accept an offer with fixed principal, fee, and term before
funds move. An approval is bound to those same terms; changing any term
requires a new offer or approval. The current demo uses deterministic seeded
data and does not create real accounts or authenticate customers.

## Decision boundaries

- Deterministic policy owns eligibility, tiers, customer limits, capital
  regimes, approval requirements, and final bridge amounts.
- The facility configuration and ledger determine available capital. Airwallex
  balances confirm customer wallet state and provider movement; the platform
  wallet balance is not the facility's source of truth.
- The AI may extract typed evidence and explain a decision. It cannot set a
  tier, limit, approval, transfer amount, or provider command.
- Salesforce is read-only. Free-text notes are untrusted data and cannot
  directly change a policy outcome.
- Clients must not provide trusted Airwallex account IDs, card IDs, policy
  decisions, or transfer commands.

## Customer data and evidence

The underwriting profile may include:

- Account standing, tenure, contracts, verified onboarding status, and
  operator-approved caps.
- Current balances, balance history, deposit history, expected receivables,
  card declines, spend categories, and repayment history.
- Concentration and repayment reliability measures derived from verified
  records.
- Salesforce customer context and selected, bounded support or call notes.

Use a stable one-to-one Salesforce Account to Airwallex connected-account
mapping. Never join records by company name or email. Quarantine missing or
duplicate mappings rather than guessing.

For server-side Salesforce snapshots, publish a version per customer only after
required fields and mappings validate. Hard fields are fresh for at most
24 hours. A missing, stale, unknown, or invalid hard field blocks new funding
for that customer while existing bridge servicing continues. One customer's
sync failure must not block other customers.

When the server-side evidence pipeline is used, each signal must carry its
source record, timestamps, attribution, bounded evidence, and review status.
Ambiguous, conflicting, or unsupported text becomes `review_required`; it does
not automatically change a limit. Unqualified signals remain review-only.
Current Bridge Capital demo CRM context is informational and is not scored.
The local demo uses deterministic fixture evidence; a live model provider is
not wired into production decisioning.

Expected-deposit dates must come from a trusted source. Do not infer due dates
from settlement timestamps or model output. The source, customer/payer matching
key, date semantics, and completeness rules remain open; see the
[expected-deposit source backlog](/docs/backlog/expected-deposit-date-source.md).

## Underwriting and capital policy

The current demo policy has an eligibility stage and a request-time decision.
A customer needs at least three months of tenure and 90 days of balance history.
The tier determines a share of average monthly inflows that may be outstanding:

- **Tier A:** at least 12 months of tenure, no late or defaulted bridge in the
  past 180 days, and no more than five near-zero-balance days in the last
  90 days. Limit: 30% of monthly inflows.
- **Tier B:** at least six months of tenure, no more than one late bridge in
  the past 180 days, and no defaults. Limit: 20% of monthly inflows.
- **Tier C:** other eligible customers with no default in the past 12 months.
  Limit: 10% of monthly inflows.
- Customers without sufficient history or with a disqualifying recent default
  have no line. Existing outstanding principal counts against the line.

Each request must pass the configured checks for history, past-due bridges,
customer limit, spend criticality, reserve floor, duplicate transactions, and
an expected receivable due within the chosen term. The capital-regime tier gate
also applies. A bridge is full-or-zero; do not partially fund a shortfall.

Capital regimes use available capital above the reserve floor as a share of the
facility:

- **Abundant, above 60%:** any eligible tier and spend category can be
  auto-approved.
- **Normal, above 30% through 60%:** Tier B or better can be auto-approved.
- **Tight, above 10% through 30%:** critical spend only; rank requests, and
  require a person for amounts above $5,000.
- **Critical, above 0% through 10%:** Tier A and critical spend only; every
  bridge needs human approval.
- **At the floor, 0%:** no new bridges.

When capital is scarce, rank by tier, spend criticality, then receivable
quality. Keep the current configured thresholds in sync with the policy design
and `policy.config.ts`; do not introduce a second set of hard-coded values.

## Offers, funding, and collection

For the current Bridge Capital product, the configured fee is a $50 service
fee plus 2% per month on principal, prorated over the selected term. Preset
terms are 15, 30, 60, or 90 days; custom terms are 7 to 90 days. The full
selected-term fee applies even if the customer repays early. There is no fee on
an unused limit. This is distinct from the legacy server-side Kit 5 API fee.

The funding and repayment flow must:

- Transfer approved principal from the platform to the customer's connected
  account and wait for a settled transfer before retrying a card payment.
- Use stable idempotency identities. Reconcile ambiguous provider responses by
  looking up the saved identity before retrying; never issue a blind duplicate.
- Collect from settled deposits, apply no more than the triggering deposit,
  and collect fees before principal as separate operations.
- Restrict Tier C spend to critical categories while bridged; remove the
  restriction after full repayment.
- Support one open bridge per customer and USD-only operation in V1.
- Apply the late ladder: days 1–7 Watch; 8–30 Delinquent; 31–60 Serious; and
  61+ Default. A default makes the customer ineligible for 12 months.

The retained server-side Kit 5 API uses separate compatibility settings: a
USD $50,000 facility, 20% reserve floor, 20% per-customer cap, and a legacy
1.5%-per-30-days fee with a seven-day minimum. These are not the source of
terms for current Bridge Capital offers. Keep this API behavior separate from
the current product policy.

## Integrations, security, and audit

- Keep Airwallex credentials and provider identifiers on the server. Per-
  connected-account operations use the verified account mapping and the
  provider's account-scoping mechanism.
- Keep Salesforce read-only and restrict it to allowlisted records and fields.
  Production connector permissions, field names, picklists, pagination, and
  text limits must be verified before production use. See the
  [Salesforce snapshot runbook](/docs/salesforce-snapshot-operations.md).
- Verify webhook signatures over the raw request, deduplicate by event ID, and
  reconcile out-of-order or missed provider events.
- Record the policy version, input/snapshot versions, signal evidence, rule
  results, approval identity, provider request IDs, and settlement results.
- Keep automated tests fixture-based. Sandbox evidence must not be described
  as production authorization or movement of real funds.
- This document specifies data capabilities, not a pinned endpoint catalog.
  Verify provider endpoint paths, API versions, field availability, and
  permissions against current provider documentation before integration work.

## Demo and production gates

The current local UI uses synthetic customers and in-memory adapters. Provider
operations are a separate server-side sandbox path. Before calling that path,
verify real sandbox account/card mappings, API permissions and product
enablement, a natural insufficient-funds result, signed webhook delivery, and
provider record retrieval and reconciliation. See the
[sandbox prerequisites](/docs/demo/airwallex-sandbox-demo-prerequisites.md) and
[demo acceptance matrix](/docs/infra/demo-acceptance.md).

Production remote authorization remains gated on its provider contract,
authentication, account mappings, trusted expected-deposit source, and verified
policy inputs. The decision path must meet the 2.5-second authorization budget
using cached profile features and a current-balance check, not live history
queries. Verify the contract and permissions before enabling it. Do not infer
production readiness from fixture tests or a local demo.

## Open requirements

- Define the expected-deposit-date source and matching rules; see the
  [backlog item](/docs/backlog/expected-deposit-date-source.md).
- Finalize signal vocabulary, attribution, conflict/supersession handling, and
  labeled examples; see the
  [signal-definition backlog](/docs/backlog/work-with-typesafe-jev-on-signal-definitions.md).
- Define how risk-related account and webhook events change policy; see the
  [risk-webhook backlog](/docs/backlog/airwallex-risk-webhook-policy-effects.md).
- Decide cross-day fee adjustment and multi-bridge repayment allocation
  before supporting those cases; see the respective backlog items for
  [fee adjustment](/docs/backlog/cross-day-fee-adjustment.md) and
  [multiple bridges](/docs/backlog/multiple-open-bridge-repayment-allocation.md).
- Obtain legal review of commercial-financing disclosures and state-specific
  requirements before production use.

## See also

- [Current Bridge Capital policy design](/docs/designs/bridge-capital-policy-spec.md)
- [Salesforce snapshot operations](/docs/salesforce-snapshot-operations.md)
- [API documentation](/docs/api/README.md)
