# Legacy UI and repository cleanup

Implemented locally on November 15, 2026 in `chore/legacy-ui-cleanup`, based
on develop `569ced2`. Source commit `3877772` was merged into local develop
at `9e3ec34`. The Go gateway delivery was then merged at `6a499b6`.

[TOC]

## Source removed

- The old presenter state machine, two hooks and thirteen dashboard components.
- Six unused onboarding steps, including the old client-only account form.
- Legacy Bridge and Salesforce HTTP clients, their barrel/factory and obsolete
  Kit 5, ledger, CRM and agent client contracts.
- The old presenter seed, shared TypeScript policy and its exclusive tests.
- Old CRM/evidence mock clients and their exclusive fixtures/tests. These had
  no current app callers; the earlier fixture move is historical in the
  [Node retirement record](/docs/demo/node-readmodel-retirement.md).
- Obsolete Node repayment/snapshot environment defaults and old TypeScript
  build includes.

The current Bridge Capital UI, policy, generator, `MockAdapter`, mock Airwallex
client, onboarding API code and every Go service are unchanged. Historical
Node schema and seed tooling are preserved. The retired snapshot route still
returns 410 before database access.

## Documentation and plan status

Both previous Node operations guides are preserved byte-for-byte under
`docs/archive/node-legacy/`, with an archive index explaining their status.
Their former entry paths now give current Go/service prerequisites and point
to the retained mapping administration guide. Requirements distinguish the
current post-decline flow from retired financial and remote-authorization APIs.

The main checkout's existing plan index and Node cutover summary were updated
separately, preserving audit-input backups under `.dev/cleanup-20261115/`.
The canonical inventory has 24 plans: 20 archived and four open scopes. This
does not include unpublished task plans in other active worktrees.

## Local cache cleanup

The main checkout's `.dev/applications-local/go-cache` contained Go build
artifacts with the standard cache marker and no open process files. Running
`go clean -cache` against that exact path reduced it from about 405 MiB to
8 KiB. The 24 MiB testserver, private configuration, scripts, logs, retained
archives and integration evidence were preserved. No process was stopped.

## Verification

- Baseline: 160 tests in twelve files passed; frontend build passed.
- Pre-merge retained scope: 235 tests in 26 files passed. One existing shared-DB
  forwarding concurrency test was deliberately excluded, not claimed passing.
- Frontend and Node server production builds passed.
- Current UI, onboarding, Go and retained Node source: 448 file hashes match
  their pre-cleanup versions.
- Archived original guides match their previous Git versions byte-for-byte.
- New document links and all main plan-index links resolve. The plan count
  matches the actual main checkout inventory.
- OpenTofu format check, scoped Oxlint and diff checks passed. No Terraform
  initialization, plan, apply or cloud request was performed.
- Combined develop verification passed 251 TypeScript tests in 29 files,
  gateway race tests (15 top-level tests and 18 HTTP subtests), Go build/vet,
  Node build and both frontend modes. One shared-DB Node test was excluded;
  no gateway scenario was skipped.

Logs are retained at `/tmp/flowcard-cleanup-retained-tests.log`,
`/tmp/flowcard-cleanup-frontend-build.log`,
`/tmp/flowcard-cleanup-server-build.log` and
`/tmp/flowcard-cleanup-tofu-format.log`. The worktree's ignored `.dev/cleanup/`
contains the execution checklist and preserved-source hash inventory.

No credential, existing-database, provider, model or deployed-service change,
runtime restart or remote publication was performed. Gateway verification
used owned disposable databases and cleaned them after testing.
