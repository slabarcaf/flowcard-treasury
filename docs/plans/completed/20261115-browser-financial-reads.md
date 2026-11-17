# Go browser financial reads

Task C adds operator-only reads from existing Coordinator and Funding APIs.
Ramil selected this slice directly. Preserve A/B, existing services and source
changes. No financial command, customer binding or provider/model call belongs
in C.

[TOC]

## Agreed implementation

Use a separate typed read client and Financial records screen in the opt-in Go
app. Keep legacy fixture screens excluded. Do not implement the legacy
DataAdapter write contract or recalculate policy, exposure, fees or debt.

The gateway validates optional paired origins/secrets for each service and
uses exact GET routes only. Coordinator assertions use `flowcard-go`, audience
`flowcard-coordinator-service`, subject `coordinator-reader`. Funding assertions
use `flowcard-go`, audience `flowcard-funding-collection`, subject
`coordinator-reader`, signed with its existing coordinator-role key. The
subject is fixed server-side, and no browser financial write is routed.
Current active OPERATOR sessions are required; customers remain unlinked.

Coordinator list/detail supplies saved evaluations and source/snapshot versions.
Funding's committed change feed discovers offer/bridge/operation IDs; there is
no invented offer-list API. Read the owner's exact detail and collection page
contracts. Preserve opaque cursors and sequence/version distinctions. Verify
collection pages against the displayed bridge version. Show missing, stale,
unavailable and malformed data distinctly. Reject unsafe JavaScript integers;
render integer cents exactly and use supplied totals rather than summing debt.

## Implementation checklist

- [x] Review current designs, exact read routes, DTOs and signing identities.
- [x] Add optional paired configuration and exact authenticated GET routes.
- [x] Add typed financial reads, precision/linkage/version validation and UI.
- [x] Verify auth/role/read-only boundaries, real Go handlers and UI behavior.
- [x] Run focused Go/frontend checks, record exact evidence and owned cleanup:
  15 top-level Go tests plus 18 HTTP subtests with race detection, 41 frontend
  tests, build/vet/TypeScript/lint and both Vite builds. Nine database/login
  pairs from the final suite and three from the isolated C browser walkthrough
  were removed after ownership checks. No test scenario was skipped.

Browser verification covered actual saved assessment/terms/bridge/collections,
explicit source-outage handling, logout clearing and customer direct-hash/API
denial. It used production read handlers and explicit synthetic source rows,
not a scheduler/underwriting execution or financial transition acceptance.
Evidence and complete path inventory:
`.dev/browser-auth/task-c-report.md`.

Local integration is complete: source commit `249024d`, develop merge
`6a499b6`. The combined verification and its limits are recorded in
[the integration note](/docs/demo/browser-gateway-integration.md).

## Acceptance boundaries

Use fixture-backed local HTTP and uniquely marker-owned PostgreSQL targets.
Read-only production handlers may run in bounded test harnesses; no scheduler,
worker, provider or model is started. Label synthetic source data and process
versus handler evidence. Preserve existing shared roles and all manual data.
No new dependencies, root env copy, push, deploy or real provider/model call.
Task D remains separate.
