# Salesforce snapshot retirement

The Node Salesforce scheduler, snapshot publisher and evidence pipeline were
retired in develop `7d2bba4`. Their old operating instructions are preserved
as a [historical record](/docs/archive/node-legacy/salesforce-snapshot-operations.md).

- Use [customer mapping administration](/docs/demo/customer-mappings.md) for
  the retained roster/account/card tooling.
- Use [Go underwriting](/docs/demo/underwriting-local.md) for normalized
  snapshot imports and saved evaluations.
- Use [Go coordinator](/docs/demo/go-coordinator-local.md) for attributed
  pending evidence and source investigation.

Live Salesforce retrieval and automated verification/publication of financial
facts remain deferred. The retired Node snapshot API returns 410.
