# Customer-bound browser commands

COMPLETED locally: Task D is merged into develop. The local Go browser
walkthrough and retained profile are documented in
[the local browser guide](/docs/demo/local-go-browser.md). Live provider and
hosted traffic cutover remain outside this completed slice.

Task D connects real invited users to existing servicing actors, then tests
explicit customer/operator actions on uniquely owned DEMO targets. No live
provider enablement, shared-data change, persistent provisioning or deployment.

[TOC]

## Binding and authority

An administrative CLI verifies an active invited user's immutable role against
the actual Funding actor self-read, then stores an immutable source/mode/actor/
customer binding in the gateway schema. No name/company/provider-ID inference.
Runtime reads bindings only. A customer remains unlinked until that check.

Funding's existing actor importer permits reassignment. Add optional signed
binding restrictions to its existing service assertion, checked against the
current registry and installation. These only narrow the authenticated
subject's authority; they never supply a role/customer identity. Preserve
existing CLI callers and per-role keys. Gateway requests always carry the
verified restrictions. A revoked, changed or disabled binding fails closed.

Commands are explicitly DEMO-only and loopback, with distinct existing
operator/customer keys. Verify actual source mode/fixture-only installation.
SANDBOX delegation/provider execution stays gated. Runtime gains no access to
Funding tables. No credentials or trusted actor/customer IDs come from browser
JSON or public VITE variables.

## Commands and recovery

Wire only the existing create-offer, accept, approve, fund, collection-review
and reconcile contracts. Require exact saved hash/version and explicit user
action; distinct users hold customer/operator roles. The owning service keeps
policy, funds, terms and ledger authority. Create-offer provenance is checked
against the saved Coordinator case/evaluation.

Persist canonical request, user/binding, idempotency key and source before
sending. Append definitive receipts; retain unknown outcomes. Recovery lists
only that user's commands and retries the original payload/key manually.
Never infer settled funds from queued/reserved receipts. Retain a verified
customer offer index from successful create receipts; customer detail reads
use the actual customer actor, never the broad reader credential.

## Checklist

- [x] Review actor reassignment, command/terms and recovery contracts.
- [x] Add constrained actor self-read/delegation and preserve old callers.
- [x] Add forward migration, binding CLI and restricted runtime grants.
- [x] Wire role-scoped commands, customer reads and durable recovery.
- [x] Add explicit customer acceptance/operator UI with no mock fallback.
- [x] Run owned API/worker DEMO journey and denial/recovery tests.
- [x] Complete build/vet/race/frontend checks, cleanup and final report:
  18 top-level Go tests plus 18 HTTP subtests with race detection, existing
  Funding contract test and 45 frontend tests pass. Both services build/vet;
  TypeScript, scoped lint and both Vite builds pass.

The normal worker funds $7,000, charges the $190 contract fee, allocates $100
then $7,090 mock deposits fee-first and reaches zero-debt REPAID. No servicing
debt/settlement row was injected. Gateway restart and lost receipt recover the
same persisted command/key without a duplicate offer. Browser consent/recovery,
distinct operator approval and cross-customer privacy are verified. The first
browser funding request correctly rejected stale mock-provider evidence. A
subsequent owned walkthrough explicitly refreshed only the provider double's
payment observation, then verified the browser RESERVED receipt followed by
an ACTIVE bridge with $7,000 principal, $190 fee and a SETTLED operation.
No production freshness or policy guard was changed or bypassed. All temporary
browser/service/database resources were removed.

Exact inventory/evidence: `.dev/browser-auth/task-d-report.md`.

## Verification limits

Use actual gateway sessions, Funding API, durable operations, real worker and
Go adapter with a local provider double only. Fixture controls alter mock
provider facts, never servicing debt or settlement rows. Test cross-customer,
role/hash/version, binding drift, logout/disable and uncertain receipt recovery.
Keep provider mode DEMO and report fixture execution separately from live
acceptance. No new dependencies or team-lead reporting.
