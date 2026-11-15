# Node read-model retirement

Ramil authorized retirement of the overlapping Node CRM snapshot,
underwriting-profile and AI-evidence pipelines on November 15, 2026.

[TOC]

## Ownership

Go underwriting owns normalized snapshot imports and hard-rule decisions.
Go coordinator owns selected-source investigations and attributed pending
evidence. Neither delivery establishes a live Salesforce connector or automatic
verification/publication of extracted financial facts.

Remove Node snapshot gathering/scheduling/publication, legacy underwriting
report/profile publication and evidence extraction/review. Remove their
exclusive tests and CLI entry. Return 410 for the retired snapshot endpoint
before database work, with no forwarding or fixture fallback.

Keep customer/account/card mapping administration and reconciliation,
onboarding session/proxy/webhook delivery, historical ledger reads, schema
migrations and guarded seed tooling. Do not change stored records or schemas.

## Frontend fixtures

Move the existing five synthetic Salesforce snapshots, their types and pure
fixture reader under `src/services/mock/salesforce/`. Preserve their bytes and
customer identities. Keep fixture clients explicitly local; the legacy HTTP
client reports retirement and never silently switches to fixture data.

## Runtime and verification

Remove background startup wiring and obsolete configuration fields. Existing
running services, root `.env`, databases and deployed settings remain unchanged.
Unused old environment values cannot reactivate deleted workers.

Verify frontend/server builds, retained fixture behavior, explicit retirement
without DB access, rejected legacy CLI commands, mapping reconciliation,
onboarding/session/webhook boundaries and graceful shutdown. Retain SQL safety
checks, and verify no historical migration or seed file changed.
