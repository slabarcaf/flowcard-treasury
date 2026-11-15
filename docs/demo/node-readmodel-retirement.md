# Node snapshot and evidence retirement

Source retirement implemented on November 15, 2026 in
`chore/retire-node-readmodels`, based on develop `8662dd1`. The change is
uncommitted and unmerged pending integration. Existing processes and stored
records were not changed.

[TOC]

## Removed

- Node Salesforce collection, snapshot scheduling and snapshot publication.
- Legacy Airwallex report collection and underwriting-profile publication.
- Node AI extraction, calibration, evidence review and its CLI command.
- Exclusive implementation tests, obsolete configuration fields and example
  environment settings.

The compatibility snapshot endpoint returns 410
`legacy_customer_snapshot_retired` before database access. The legacy browser
client surfaces that error without switching to synthetic data. The compiled
`evidence-review` command exits 1 before configuration or database access.

## Preserved

- Five synthetic Salesforce fixture JSON files, moved byte-for-byte to
  `src/services/mock/salesforce/fixtures/`, with their fixture reader and types.
- Customer/account/card mapping administration and provider reconciliation.
- Onboarding session/API/docs proxy and durable account/RFI forwarding.
- Historical ledger reads, database migrations and guarded seed tooling.
- Existing processes, root `.env`, databases and deployment configuration.

Go underwriting owns snapshot imports and hard-rule evaluations. Go coordinator
owns selected-source investigations and pending evidence. Live Salesforce
retrieval and automatic financial-fact validation/publication remain deferred.
This retirement does not add either capability.

## Verification

- Focused baseline: 56 tests in five files passed after allowing loopback test
  listeners. The first sandbox run failed because local listeners were denied.
- Final retained boundaries: 167 tests in 20 files passed. One existing
  shared-PostgreSQL forwarding concurrency test was deliberately excluded;
  no database-backed acceptance claim is made by this retirement.
- Frontend production build and Node server build passed.
- Scoped Oxlint exited successfully with warnings in unchanged provider-error
  parsing. No lint errors were reported.
- The five moved fixture hashes match their original develop versions. All
  historical migration and seed files have no diff.
- The server source tree decreased from 103 files/about 632 KiB to 49
  files/about 296 KiB. No references to removed source modules remain in
  executable source, scripts or retained tests.
- The server build removed obsolete compiled worker/review modules. A direct
  compiled evidence CLI invocation returned the explicit retirement error.

Local logs: `/tmp/flowcard-readmodel-final-tests.log`,
`/tmp/flowcard-readmodel-server-build.log`,
`/tmp/flowcard-readmodel-frontend-build.log` and
`/tmp/flowcard-readmodel-retired-cli.log`.

No commit, merge, provider/model request, process restart, database mutation,
remote publication or deployment was performed.
