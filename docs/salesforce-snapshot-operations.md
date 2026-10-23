# Salesforce Snapshot Operations

This guide describes how to operate the enrolled-customer snapshot service
alongside the FlowCard bridge ledger. Demo mode uses synthetic records;
production sync is gated until the Salesforce org mapping and MCP capabilities
have been verified.

[TOC]

## Source modes

The source mode must be selected explicitly. Demo mode reads deterministic,
synthetic fixture snapshots from `server/src/salesforce/fixtures/`. It makes no
Salesforce calls and is suitable for local demonstrations and tests.

Production mode is designed to read through the existing read-only Salesforce
MCP connector. It uses fixed, allowlisted queries and follows the MCP
pagination contract until each query is complete. There is no Salesforce REST
fallback, write operation, or silent fallback to demo fixtures. Production MCP
access has not been verified against the connected org.

## Customer roster

The PostgreSQL roster is the authority for snapshot membership. Enroll a
customer only after an operator confirms the stable internal customer ID,
Salesforce Account ID, and Airwallex connected-account ID. The CLI also records
the operator identity and reason for the action. IDs must be unique; the
service does not match by company name or email.

Apply migrations `001_bridge_ledger.sql`, `002_customer_roster.sql`, and
`003_salesforce_snapshots.sql` in numeric order before using the service. Set
`DATABASE_URL` and invoke the CLI from the repository root:

```bash
npm run roster:cli -- enroll \
  --customer-id customer-123 \
  --salesforce-account-id 001000000000001AAA \
  --airwallex-account-id acct_example \
  --actor operator@example.com \
  --reason "Approved for bridge program"

npm run roster:cli -- list

npm run roster:cli -- deactivate \
  --customer-id customer-123 \
  --actor operator@example.com \
  --reason "Customer left bridge program"
```

Soft deactivation retains the roster row and snapshot history. It removes the
customer from future snapshot runs and new bridge decisions; servicing open
bridges continues. The CLI requires an actor and reason for enrollment and
deactivation. Restrict access to the CLI and its database credentials to
authorized operators.

## Daily sync, freshness, and retries

The daily scheduler uses UTC and defaults to `02:00`. Its clock can be set with
`SALESFORCE_SNAPSHOT_DAILY_RUN_UTC` using `HH:mm`. It schedules one run per day
and avoids overlapping runs in the scheduler process. Set
`SALESFORCE_SNAPSHOT_MODE=demo` to run a fixture sync once at startup and then
use that daily schedule. Production scheduling remains disabled because the
server currently has no trusted host MCP runtime binding; production mode logs
this limitation and never falls back to fixtures or REST.

When run, the worker selects only active roster entries. It syncs customers
independently and publishes each complete, validated snapshot atomically. A
successful empty Case or Activity result is valid. A failed customer does not
prevent other customers from publishing.

A snapshot is fresh through 24 hours of age. After the limit, or after a
required query exhausts its retries, the read model reports it as stale and
blocks new bridge decisions for that customer. The previous published version
is retained; a partial candidate never replaces it. Existing bridge servicing
continues. Missing, invalid, and deactivated customers are distinct read-model
states.

MCP reads retry transient failures up to three total attempts by default, with
exponential delays starting at 100 ms. Configure `SALESFORCE_MCP_MAX_ATTEMPTS`
and `SALESFORCE_MCP_RETRY_DELAY_MS` on the server when needed; the code caps
these at 10 attempts and 60,000 ms. Permission, malformed response, pagination,
and other permanent failures are not retried. A query must finish all pages
before publication.

## Airwallex roster reconciliation

Reconciliation compares stable Airwallex connected-account IDs from the
provider account list against all roster rows. Its report groups discrepancies
as:

- **Missing:** a roster account ID was absent from the provider list.
- **Extra:** a provider account ID has no roster row.
- **Duplicates:** an account ID appears more than once in either source.
- **Status mismatches:** the roster active/deactivated state differs from the
  provider's `ACTIVE` state.

This is report-only. It does not enroll or deactivate customers and does not
match on names. The reconciliation service is implemented, but no
operator-facing command or scheduled customer-roster reconciliation output is
wired into the server entrypoint. Reconciliation is not a production operating
procedure until an authorized invocation and report destination are provided.

## Production readiness gate

Production remains disabled until an owner confirms the connected Salesforce
org mapping and the MCP capability check. The current `salesforce-proposed-v1`
mapping is provisional; `server/src/salesforce/field-map.ts` contains proposed
object fields and picklist mappings. Verify each field API name, field type,
nullability, relationship filter, and each picklist value against the org. In
particular, verify the Account approved bridge cap and required customer,
KYB, and credit-review statuses; Contract status and end date; Case status,
priority, escalation, risk category, and date fields; and call Task/Activity
date, outcome, and note fields. Confirm that every source value has an explicit
canonical mapping. Unknown picklists and a missing approved cap must continue
to fail validation rather than map to a permissive default.

The maximum lengths for Case descriptions and call notes are unresolved. Set
positive integer values in `SALESFORCE_MAX_CASE_TEXT_LENGTH` and
`SALESFORCE_MAX_ACTIVITY_TEXT_LENGTH` only after the data owner confirms the
limits. Both are required for production readiness. Do not infer values from
the demo fixtures.

The production MCP must support unattended scheduled access without an
interactive user session, read-only permissions, complete pagination, and
bounded retry behavior. Confirm those capabilities before setting
`SALESFORCE_MCP_CAPABILITIES_VERIFIED=true`. Configure the host-provided
connector identifiers with `SALESFORCE_MCP_SERVER_NAME` and
`SALESFORCE_MCP_QUERY_TOOL_NAME`. No live MCP readiness verification has been
performed. Production sync and new bridge decisions must remain gated until
all readiness checks are evidenced and approved.

## Browser access to production snapshots

The server protects production MCP snapshot responses with the server-side
`SALESFORCE_SNAPSHOT_READ_TOKEN`. The browser API client deliberately sends no
bearer token or cookies. Do not put the token in a `VITE_` variable or bundle
it into frontend assets. Until a trusted same-origin server proxy injects the
server-held token, browser reads of production snapshots must remain
fail-closed. Demo fixture reads remain local and deterministic.

## Scope

Snapshots provide versioned Salesforce context only. This workflow does not
extract AI evidence, generate rationales, change underwriting policy, or alter
bridge amounts. Existing bridge servicing remains owned by the bridge ledger.

## See also

- [Project README](/README.md)
- [Editing guide](/docs/EDITING.md)
- [Salesforce snapshot implementation plan](/docs/plans/20261021-salesforce-snapshot-implementation-plan.md)
