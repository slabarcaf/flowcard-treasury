# Go onboarding webhook intake

Execute directly under the [intake design](/docs/designs/onboarding-webhook-intake.md).
Base: develop `7ac6b92`. No delegated executor, dependency additions or live
calls.

[TOC]

- [x] Inspect current signature contract, inbox, processor and recovery leases.
- [x] Add opt-in provider configuration and separately authenticated public
  route.
- [x] Add raw receipt/conflict storage in new migration 007; preserve 001–006.
- [x] Preserve internal JWT delivery and fence conflicting unfinished work.
- [x] Verify signature/body/config cases and owned real HTTP/PostgreSQL
  recovery.
- [x] Run scoped race/build/vet/format checks and record exact cleanup/evidence.
- [x] Document hosted cutover and Node backlog-drain gates without executing
  them.

Local implementation is complete. Exact counts, earlier failed attempts and
limits are recorded in the [runbook](/docs/demo/onboarding-webhook-intake.md).
Local integration is complete: source `f5848a6`, develop merge `3a94cbf`.
Post-merge checks passed all 54 top-level test functions and 73 subtests,
with zero skipped scenarios, both owned cleanup checks and build/vet/spec
verification. Hosted acceptance remains separate in the Node cutover plan.

No deployment, root env edit or existing-service restart was performed.
Do not run shared-DB truncating tests.
