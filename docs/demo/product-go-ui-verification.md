# Original product UI verification

Local UI implementation is verified against actual Go services and a local
provider double. Ramil subsequently requested the complete Airwallex sandbox
browser flow. That extension is active and is not claimed complete by this
local report.

[TOC]

## Source and ownership

- Worktree: `.worktrees/demo-ui-go`, branch `feat/demo-ui-go`.
- Capital-description dependency: `f51ff163997b5755331093f3b8793693169d64f6`.
- Core product/read/API source: `6e29268`.
- Scoped current-wallet follow-up: `c7594da`.
- Local product preview: <http://127.0.0.1:5176/#bridge-capital>.
- Developer route: <http://127.0.0.1:5176/#developer>, OPERATOR only.
- No production or hosted deployment was performed.

The original sidebar, visual tokens, tables, cards, forms and phone-width
portal were reused. The product uses screen reads and exact saved commands;
fixture policy and adapter behavior stay behind the explicit legacy entry.
No service-signing key or provider credential is exposed to the browser.

## Actual local browser proof

Final source run: `2f5cd129-cd41-4d00-80b3-22d2d4e29f32`.
Source anchor: `2026-11-16T03:23:22.68323Z`.
Retained book: main `.dev/product-go-demo/runs/f45121af/`.

| Record | Saved identity |
| --- | --- |
| Shortfall case | `e67ec9d7-e5a7-8efc-877f-956536bb49f1` |
| Evaluation | `ab4422d3-b928-4768-826b-aede5b18a6c2` |
| Offer | `5b9f8ccb-a18c-41bd-bb78-caef48e1dca5` |
| Bridge | `d5603718-9859-4de2-b016-0118ec1ae790` |
| Funding operation | `4cd116d9-0055-4149-b8db-7e0f79b5d633` |

The operator created an offer from the saved eligible case. A distinct real
customer session reviewed and accepted exact terms in the original portal.
The operator then approved the terms and requested funding through the
original back office. The queued receipt was followed by actual Go worker,
HTTP adapter and provider-double settlement.

Principal was 700,000 cents, contract fee 19,000 cents and term 30 days. Two
provider-double deposits of 10,000 and 709,000 cents produced three settled
collections: fee 10,000, fee 9,000, principal 700,000. The final bridge is
REPAID version 28 with zero debt and four settled operations.

The prepared provider double dropped the first collection-fee reply. Normal
lookup recovered its original identity. Provider creates remained exactly
four. A read-only ledger audit found a zero debit/credit difference and exact
case/evaluation/offer/bridge linkage. All 13 audit assertions passed.

The Coordinator consumed the real servicing feed and closed its linked case.
A closed-case null-action encoding defect was corrected in the Go owner;
strict frontend validation remains enabled. Root and collection action arrays
now encode as arrays. Initial failure captures remain under `/tmp`.

## Other browser checks

- Customer bridge discovery works after fresh login and full reload, without
  manually pasted UUIDs. Repaid bridges remain in history.
- The original portal at 390px has document/body width 390px, with no horizontal
  overflow. Desktop screenshots preserve the original back-office hierarchy.
- CUSTOMER access to operator portfolio and Coordinator APIs returned 403.
  A customer-supplied wallet/bridge scope selector returned 400.
- CUSTOMER access to `#developer` displays the operator-only denial.
- The deliberately unlinked customer has no financial records and receives
  403 for another user's bridge.
- Applications list/detail and synthetic draft save work in the original
  operator shell. The retained first run saved
  `app_7bdb07065652601aeb2f737f9cdc38a5`, DRAFT, for Product UI Demo Ltd.
- Real re-login preserved an unsaved company field. The form was not saved
  during this preservation check.
- Admin opens the retained developer interface and returns to the product.
- Initial funding completed within the source window. After source expiry,
  servicing/history reads continue; source readiness remains honest.

The browser did not deliberately lose a gateway command reply in this run.
The actual gateway-owned test journey and existing typed command tests verify
persisted UNKNOWN intent and same-key recovery. Intermediate provider UNKNOWN
state was recovered, but no claim of a separately captured browser UNKNOWN
recommendation is made.

## Automated verification

- Core source: 119 frontend tests in 14 files passed; TypeScript and scoped
  Oxlint passed; product, Go/developer and preserved legacy builds passed.
- Gateway: 18 top-level tests plus 18 HTTP subtests passed with race detection,
  no skipped cases, using owned PostgreSQL databases/logins and HTTP fixtures.
- Funding: all 15 top-level functions passed, including new scope/pagination,
  exact settled/reversed fee reads and VOID_UNFUNDED/retry history assertions.
- Shared policy tests and the new capital-description parity cases passed.
  Policy JSON, thresholds and ID/hash were unchanged.
- Coordinator: existing three focused authority/discovery/recovery functions
  passed through the guarded DB wrapper. The new closed-collection array
  regression passed separately. Owned test resources were removed.
- A current-wallet follow-up passed the real guarded gateway money journey:
  200,000 cents before funding and 900,000 cents after servicing, with operator
  and foreign-selector denial. Its frontend suite passed 120 tests. The
  original portal also displayed a retrieved 200,000-cent balance through
  the new Go customer-only route.

Two existing test assertions compared the host clock strictly against the
PostgreSQL clock. The observed reversal mismatch was about 14ms. Assertions
now allow the service's existing five-second skew; production time checks are
unchanged. Original failed logs remain retained. An initial fee expectation
was also corrected to preserve unknown totals for incomplete opening history.

## Retained evidence

Private files and logs are ignored, not committed with credentials:

- Main `.dev/product-go-demo/runs/f45121af/final-audit.json` contains exact
  source/operation/collection/portfolio proofs and the read-only ledger audit.
- Main `.dev/product-go-demo/runs/f45121af/verified-links.json` records links.
- `/tmp/product-final-command-proof.txt` records actual gateway command
  results.
- `/tmp/product-final-case-arrays-proof.txt` records actual case response
  shapes.
- `/tmp/product-ui-gateway-release.jsonl` and
  `/tmp/product-ui-funding-release.jsonl` contain final core suites.
- `/tmp/product-ui-wallet-journey.jsonl` records the wallet follow-up.
- `/tmp/product-ui-frontend-release.log` and
  `/tmp/product-ui-wallet-frontend.log` record frontend checks.
- `/tmp/product-ui-coordinator-regression.log` records the guarded regression.
- Screenshots are copied to the implementation worktree's ignored
  `.dev/implementation/screens/` with a hash inventory.

## Airwallex extension status

Selected develop configuration was verified as sandbox. Authentication
returned 201, platform identity and all six platform GET probes returned 200.
Read access does not establish transfer authority, fee or settlement contracts.
The configured documentation MCP is reachable over its real HTTP JSON-RPC
transport; official auth, transfer, issuing and deposit contracts were read.
No account identifiers or credentials were sent to documentation searches.

The active sandbox owner worktree contains owned account/card/receiving-account
setup and signed decline capture. Its WIP, subscriptions, unresolved intents
and provider objects remain preserved. This implementation has not duplicated
that provider setup or executed an external financial transfer.

The full browser-to-Airwallex acceptance and server SANDBOX delegation remain
open. Synthetic policy history, provider sandbox facts, real invited-user
identity and production acceptance must remain distinguishable. Card retry,
production funds, live CRM/model calls and cloud deployment remain deferred.
