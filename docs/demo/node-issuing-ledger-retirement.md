# Node issuing and ledger API retirement

Implemented locally on November 15, 2026 in
`chore/retire-node-issuing-ledger`, based on develop `40dc865`. Source commit
`7df8e0c` was merged into local develop at `35fd469`. No running service or
stored record was changed.

[TOC]

## Source cleanup

- Removed unused issuing simulation/lifecycle methods, normalization and
  checkpoint error formatting, their types/version constant and exclusive
  tests. Go card retry remains an open feature.
- Removed the historical ledger projection query/serialization handler.
  `/api/ledger` now returns 410 `legacy_ledger_retired` before database access,
  including when storage is unconfigured. No old ledger is translated into a
  Go financial book.
- Preserved connected-account listing/authentication and provider diagnostics
  for retained reconciliation tooling. Account listing uses GET; authentication
  remains its only POST.
- Preserved roster commands, historical migrations, seed tooling, records,
  current UI, Go services and the active customer-writes worktree.

## Browser default and roster CLI

The local Go operator workspace is ready for a deliberate default switch once
the actual gateway, restricted database, invited users, trusted origins and
upstreams are configured and verified in that runtime. Public docs routing
must be handled explicitly. This cleanup does not change the default or start
services.

The full customer financial portal should follow Task D acceptance: trusted
bindings, real customer/operator permissions, exact saved terms and durable
uncertain-command recovery. Node webhook intake is a separate remaining owner.

The Node roster CLI remains the administrative writer for enrollment,
deactivation, account remapping and card bindings. Go DEMO setup is synthetic
fixture preparation, and gateway bindings verify existing actors. A small Go
administrative CLI with the same guards/audit behavior should replace these
commands before the Node CLI is deleted. No new microservice is needed.

These prerequisites are tracked in the
[Node cutover plan](/docs/plans/20261115-node-go-cutover.md#browser-default-and-mapping-administration).

## Verification

- Baseline: 42 tests in five files and Node build passed.
- Retained scope: 240 tests in 28 files passed. One existing shared-database
  forwarding concurrency test was deliberately excluded.
- Node and frontend production builds passed. The frontend production asset
  names/sizes remain the same as the merged pre-cleanup build.
- Account pagination/validation tests pass, including an assertion that the
  complete call sequence is login POST followed by two account-list GETs.
- Ledger retirement tests cover GET, query parameters, POST and absent storage;
  no database query or connection is attempted.
- No obsolete issuing/ledger implementation references remain in executable
  source or retained tests. Historical DB files, mapping implementation and Go
  services have no diff.
- Scoped Oxlint completed with pre-existing warnings in unchanged provider
  error-sanitization expressions. Diff and new-document checks passed.
- Post-merge verification also passed 240 tests in 28 files, Node build and
  both frontend modes. The same shared-database test was excluded. Pre-existing
  main checkout changes remain byte-for-byte unchanged.

Evidence: `/tmp/flowcard-node-issuing-retained-tests.log`,
`/tmp/flowcard-node-issuing-build.log`,
`/tmp/flowcard-node-issuing-frontend-build.log` and
`/tmp/flowcard-node-issuing-lint.log`. Post-merge results are in
`/tmp/flowcard-node-issuing-merged-tests.log` and the corresponding
`/tmp/flowcard-node-issuing-merged-*-build.log` files.

## Scripts audit

The repository's `scripts/` directory contains two tracked files, about 8 KiB
total, with no ignored or untracked debris:

| Script | Current use | Decision |
| --- | --- | --- |
| `build-server.mjs` | `package.json` server build and retained Node tooling; cleans stale compiled modules | Keep until Node tooling is retired |
| `fetch-onboarding-industries.mjs` | Documented refresh of bundled onboarding industry categories and source metadata, used by the active dropdown | Keep; no Go replacement was found |

The reference-fetch script was read but never executed. It authenticates to
the sandbox and reads reference data; this audit made no provider request.

No database mutation, provider/model call, credential change, runtime restart,
push or deployment was performed.
