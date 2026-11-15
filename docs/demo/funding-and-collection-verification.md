# Funding and collection verification

Verified November 15, 2026. The local DEMO delivery passed its focused tests
and the actual joined Underwriting, Coordinator and Funding process journey.
Provider facts and actors were synthetic; this verifies the local integration.

[TOC]

## Verified source

The joined run used local `develop` commit
`5cea5c62dc56dd52a9bab0e7cbf9191eb72b2f45`. Its Funding service and public
underwriting policy trees match the tested implementation worktree exactly.

The runtime investigation produced three bounded fixes:

- `ae90c96`: encode the standalone double's available balance as a numeric
  JSON amount, preserving the adapter's strict parser and freshness guards.
- `004494b`: import exactly one synthetic SYSTEM audit actor so guarded
  automatic approval has an explicit registry identity to record.
- `5d3c06b`: include the built-in invoice's exact source reference in customer
  provenance so the real public policy can verify coverage.

Commit `c190084` adds the remaining isolated guard verification. No policy
rules, money guards or assertion requirements were weakened. Both earlier
failed books and their immutable 422 receipts were retained.

## Focused verification

All 14 top-level test functions passed, including both preparation-mode
subtests. No test function was skipped. The suite executed actual migrations
against uniquely named, marker-owned local databases with restricted runtime
roles and cleaned only its own disposable resources.

The tests cover terms, fee-inclusive capacity, actor authorization, command
replay, signed durable inbox receipts, concurrent funding, unsent cancellation,
all five self-reservation projection groups, changed collection capacity,
ambiguous lookup without resend, UTC rollover, duplicate funding settlement,
fee-first repayment, reversals, persisted daily accrual and frozen-basis holds.
The API, worker and HTTP adapter run against local provider doubles.

Commands from `services/funding-collection`, with cached dependencies and a
privately supplied `FUNDING_TEST_ADMIN_URL_FILE`:

```sh
GOPROXY=off GOSUMDB=off go test -json -count=1 -p 1 ./...
GOPROXY=off GOSUMDB=off go build ./...
GOPROXY=off GOSUMDB=off go vet ./...
gofmt -l .
git diff --check
```

Tests, build, vet, formatting and diff checks passed. The optional installed
`golangci-lint` was built with an older Go version and could not run. No new
test framework, dependency or infrastructure was added.

Local execution output is retained at
`/tmp/funding-joint-final-focused-tests.json`.

## Actual joined journey

The lead launched the real service processes against new owned books and
one fresh, known-empty Underwriting input window. Coordinator discovery and
Underwriting produced the actual saved case and evaluation; normal Funding
commands used that exact pair. The first failed runs were not replayed as new
successes or overwritten.

| Binding | Verified identity |
| --- | --- |
| Run | `da3bc19b-085d-4ff3-80c4-33b570b4896a` |
| Underwriting anchor | `2026-11-15T19:22:03.586696Z` |
| Original Coordinator case | `14227972-3985-87f7-b846-9da5dfd99860` |
| Saved evaluation | `d521b3f0-4916-4418-81dd-2fadf1322f0a` |
| Accepted offer | `3c832fab-2218-41b5-b3a8-dfb477cc1c09` |
| Funded bridge | `913cd3df-ff6e-49d4-8640-c708ac4da638` |
| Coordinator collection case | `91511fc3-c895-8a3b-bba8-c881cef988e4` |

The actual worker and standalone local provider executed this sequence:

| Movement | USD cents | Result |
| --- | ---: | --- |
| Funding | 700000 | Settled |
| First external deposit | 10000 | Verified through provider reads |
| First fee collection | 10000 | Settled after dropped-reply recovery |
| Second external deposit | 709000 | Verified through provider reads |
| Remaining fee collection | 9000 | Settled |
| Principal collection | 700000 | Settled |

Exactly four provider creates produced the funding and three collection
operations. Recovery retained original request identities. The final bridge
was `REPAID`, with principal paid 700000 cents, contracted fee paid 19000 cents,
zero principal/contract/late debt, zero active invoice backing, no debit
reservations and no holds. The final provider balances were 100019000 cents
for the funding account and 900000 cents for the borrower. Card retry is a
separate feature; the borrower retains the funded payment balance.

An independent audit used the exact owned Funding runtime login in a
`READ ONLY` transaction, verified its database marker and checked all of the
above financial records. All 13 audit assertions passed: exact links and
terms, repayment, original allocations, released commitments, four distinct
settled operations, balanced ledger transactions, one funding settlement
effect and retained uncertain-recovery journal transitions.

The actual Coordinator reader API returned the linked collection case as
`CLOSED`, bridge version 28, `refresh_needed=false`, and collection state
`REPAID/current=true/source_stale=false` with zero debt and no holds. A second
independent read-only audit confirmed its checkpoint consumed all 58 servicing
changes, including 16 recovery-related updates, and its saved observation
matched the final bridge version. All seven Coordinator audit assertions
passed. Intermediate changes remain in the feed; current case reads present
the final authoritative state.

After the shortfall input window expired, Coordinator liveness remained 200.
Readiness correctly returned 503 with `SOURCE_FEEDS_UNAVAILABLE`,
`shortfall_stale=true` and `workflow_ready=false`, while servicing monitoring
remained enabled and fresh. Historical reads did not authorize new decisions.

## Retained evidence

Under the main checkout's ignored
`.dev/integration/20261114/runtime/funding/`:

- `selected-run.json`: owned book, source commit and input-window binding.
- `joint-case-evaluation.json`: actual source case and saved evaluation pair.
- `joint-funding-result.json`: runner result and exact final amounts.
- `retired-books/`: preserved failed attempts and receipts.

Independent captures in this execution environment:

- `/tmp/funding-joined-independent-audit.json`: financial assertions.
- `/tmp/funding-coordinator-joined-closure.json`: authenticated case read.
- `/tmp/funding-coordinator-feed-audit.json`: checkpoint and recovery proof.
- `/tmp/funding-system-actor-before.json`: missing-actor reproduction.
- `/tmp/funding-prepare-both-profiles.json`: corrected preparation modes.

These local artifacts contain no authorization to operate a live provider.
Credentials remain in private runtime files and are omitted from this report.

## Delivery boundary

Tasks 1–13 of the
[implementation plan](/docs/plans/20261114-funding-and-collection.md) are
complete for the local fixture delivery. The
[Node source retirement](/docs/demo/node-funding-retirement.md) removes the
duplicate financial implementations. Full Node/data cutover and the listed
live-provider, consent, UI and deployment acceptance gates remain separate
work. Provider acceptance remains explicitly unverified. The joined
proof used standalone binaries; container image build and startup were not
exercised.
