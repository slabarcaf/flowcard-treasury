# Remaining Node-to-Go cutover

PARTIAL: duplicate Node financial source and startup writers were retired in
`4dd9dd1`, merged into develop at `8662dd1`. Financial routes now return
410. Node snapshot, underwriting-profile and AI-evidence pipelines were also
retired at `7d2bba4`, with verified closeout `569ced2`. Existing processes,
books and callbacks were not cut over.

Unused Node issuing helpers and the historical ledger HTTP handler were
retired at `35fd469`; `/api/ledger` now returns explicit 410. Historical records
and mapping administration remain preserved. See the
[cleanup verification](/docs/demo/node-issuing-ledger-retirement.md).

[TOC]

## Completed source slice

The ownership inventory, removed writers/routes, preserved migrations and
121 focused retained-function checks are recorded in
[Node funding retirement](/docs/demo/node-funding-retirement.md).
The delivered Go scope is recorded in the
[Funding runbook](/docs/demo/funding-and-collection-local.md).

The read-model retirement and retained boundaries are recorded in
[its verification note](/docs/demo/node-readmodel-retirement.md) and
[completed plan](/docs/plans/completed/20261115-node-readmodel-retirement.md).
Live Salesforce retrieval and automatic financial-fact publication remain
deferred; retiring the Node pipeline does not implement those capabilities.

## Remaining execution

### Selected first implementation: real login and Go browser boundary

Ramil selected real customer/operator login on November 15, 2026. Follow
[the gateway design](/docs/designs/browser-gateway.md). Implementation order:

1. **A, real auth foundation:** add `services/browser-gateway` with API,
   migration/user CLI, restricted user/session storage and the specified
   password/session/Origin behavior. Focused password, HTTP/session lifecycle
   and DB privilege tests use unique owned targets. No upstream money calls.
2. **B, operator onboarding:** add exact allowlisted Go onboarding/docs proxy
   routes and server-signed actor assertions. Replace the credential-only
   frontend login/session panel with username/password/current-user/logout.
   Route a dedicated local preview through Go; preserve the existing instance.
   Verify draft save, person/company PATCH, list/detail and role denial.
3. **C, backend reads:** display real Coordinator evaluations and Funding
   offers/bridges/collections with versions, cents and explicit missing data.
   Keep fixture screens explicitly separate; no financial fallback to mocks.
4. **D, customer-bound writes:** establish trusted account bindings, then wire
   create offer, customer acceptance, operator approval and fund/reconcile to
   their exact contracts. Persist retry keys and uncertain receipts. Verify
   cross-customer denial and exact terms/version binding before a local DEMO
   money journey. Real provider operations remain separately gated.

- [x] Review current Node retirement, frontend adapter and Go actor contracts.
- [x] Select real identity and implement A, with independent race/owned DB,
  build and vet verification and both bounded lead corrections.
- [x] Implement and verify B through actual Go onboarding handlers and owned
  PostgreSQL, preserving actor attribution and same-draft person IDs.
- [x] Implement C: operator-only Coordinator/Funding reads, exact cents and
  versions, no financial writes or silent fixture fallback. See the
  [financial reads plan](/docs/plans/completed/20261115-browser-financial-reads.md).
- [x] Complete D source/owned DEMO verification: immutable verified bindings,
  current actor constraints, distinct-user acceptance/approval, durable command
  recovery and real API/worker/adapter funding plus fee-first repayment. See
  [the Task D plan](/docs/plans/completed/20261115-browser-customer-writes.md).
  The persistent local instance and browser funding/repayment walkthrough are
  complete; see [the local guide](/docs/demo/local-go-browser.md).

A-C were merged at `6a499b6` and verified together with UI cleanup `9e3ec34`.
The integration checks and limits are recorded in
[the merge note](/docs/demo/browser-gateway-integration.md).
The A-C preview originally required the Go gateway flag. The local developer
entry now selects Go, while the running older instance and hosted build remain
preserved. Root environment and existing user records were not modified.
Live provider/model execution remains outside the local DEMO acceptance.

### Remaining onboarding webhook intake and forwarding

PARTIAL: direct Go receivers exist for shortfalls and servicing. Onboarding
`account.*` and `rfi.*` events still enter Node at `/webhooks/airwallex`.
Node verifies the provider signature, persists delivery work and forwards a
service-signed request to Go onboarding at `/api/webhooks/airwallex`. The
existing internal Go endpoint verifies a service assertion. Direct provider-
authenticated Go intake is locally implemented/tested and merged into develop
at `3a94cbf` (source `f5848a6`). See its
[runbook](/docs/demo/onboarding-webhook-intake.md). Public callback routing and
the Node backlog are unchanged.

- [x] Add direct provider-authenticated intake to Go company-onboarding,
  retaining the existing protected internal delivery contract during cutover.
  Verify the original body and provider signature before accepting an event;
  do not expose the internal JWT endpoint as an unauthenticated callback.
- [x] Preserve durable receipt before acknowledgement, provider-event
  deduplication, controlled conflicting-event handling, restart recovery and
  existing account/RFI application updates. Reuse the Go onboarding inbox and
  processing rules where compatible.
- [x] Verify signed synthetic account/RFI events through actual local HTTP and
  owned PostgreSQL, including duplicates, invalid signatures and restart
  recovery. Keep shortfall and servicing event ownership separate.
- [ ] Before a separately authorized hosted switch, verify provider
  subscriptions and infrastructure routing, then drain or safely transfer
  outstanding Node forwarding work. Preserve event IDs and delivery evidence.
- [ ] Remove the Node onboarding receiver/forwarder and exclusive tests only
  after replacement acceptance and callback cutover. Retain historical
  migrations and delivery records.

Source retirement and an audit-first Go receipt transfer are now prepared in
`feat/retire-node-webhooks`. They remain a candidate until actual subscription
delivery and backlog acceptance. See
[the retirement runbook](/docs/demo/node-webhook-retirement.md).

### Browser default and mapping administration

The local developer entry now uses the verified Go browser workspace. The
retained legacy entry is `npm run dev:legacy`. See the
[local browser guide](/docs/demo/local-go-browser.md) for provisioning and
walkthrough evidence. Existing legacy processes and hosted builds remain.

- [x] Provision the gateway schema, restricted runtime connection and invited
  operator accounts through the existing privileged tools. Configure exact
  trusted origins and the actual Go onboarding/Coordinator/Funding endpoints.
- [x] Switch the normal frontend entry/config and API proxy target to Go only
  after login/restore/logout, Applications and financial reads pass in that
  intended local runtime. Resolve public docs routing explicitly. Retain the
  old instance until the replacement works.
- [x] Complete and verify Task D before making the full customer financial
  portal the default: trusted bindings, exact acceptance/approval, persisted
  uncertain receipts and distinct customer/operator authorization.
- [ ] Replace the retained roster CLI with a trusted Go administrative CLI
  before deleting it. Preserve `enroll`, `deactivate`, `map-account`,
  `bind-card`, `unbind-card` and `list`, their stable-ID/active-mapping guards
  and actor/reason audit history. A new microservice is unnecessary.

Go demo preparation writes synthetic mapping fixtures only. Gateway user-to-
servicing bindings verify existing actors; neither is a general replacement
for roster administration. Customer login is not enrollment. Node webhook
intake and hosted traffic cutover remain separate from a local browser switch.

### Remaining migration and ownership work

- [ ] Reconcile opening balances and unresolved operations before retiring a
  running old financial writer or switching provider callbacks. Go currently
  supports known-empty DEMO opening books, not nonempty legacy cutover.
  Preserve operation identities and settlement provenance.
- [x] Complete customer-bound Go offer/actor commands in Task D. The old
  `BridgeApiClient` and historical ledger HTTP handler have been removed;
  operator reads use Go. No legacy request is translated into customer consent.
- [ ] Replace remaining onboarding/session/roster/tooling responsibilities
  before deleting the whole Node service, its Compose/Docker process or shared
  dependencies. Funding verification alone does not complete that rewrite.

- [ ] Specify and implement actual card retry and the customer-facing journey.
  Go Funding currently funds and collects; its delivery does not retry a card.
- [ ] Verify provider callback ownership before switching traffic. Node
  financial callbacks return 410; real Go shortfall extraction is still held.

## Boundaries

No nonempty-book migration, process shutdown, callback switch or entire Node
removal was performed by source retirement. Preserve existing operation IDs,
financial records and historical migrations. Deployment and live provider
acceptance need their own verified scope. This document authorizes none.
