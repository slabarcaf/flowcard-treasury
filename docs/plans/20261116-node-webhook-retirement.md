# Retire Node onboarding webhook handling

Replace the remaining Node provider receiver and forwarding worker with the
existing direct Go onboarding receiver. Preserve historical Node delivery
records and transfer outstanding work through normal Go durable receipt.

[TOC]

## Scope and current evidence

- Work starts from develop `ae9eaf6` in `feat/retire-node-webhooks`.
- Go direct HMAC intake and restricted inbox/recovery acceptance already exist.
- The configured sandbox subscription is an issuing/financial subscription,
  version `2026-08-21`, with no `account.*` or `rfi.*` events. Preserve it.
  Native webhook updates change only URL; event subscriptions are immutable.
- The current ngrok endpoint forwards to localhost:1234. Do not redirect the
  shared subscription to an onboarding-only receiver.
- Root `.env` points to localhost:5432 and its read-only database audit failed.
  A retained local profile names the manual instance on 54329, but automatic
  approval review rejected use of those credentials as outside the approved
  scope. No manual queue was read or changed through that profile.
- Focused baseline: 5 Node test files, 40 tests passed using fixtures.
- Required marketplace upgrade succeeded. No dependency installation is needed.

## Implementation

- [x] Remove Node provider callback intake, verifier, inbox writer and automatic
  forwarding worker; return explicit 410 at the retired route without false
  acknowledgement or a private redirect.
- [x] Remove unused Airwallex configuration from the compatibility API while
  retaining existing onboarding/session/docs and roster tooling.
- [x] Add a bounded audit-first Go legacy receipt transfer CLI. Verify both
  expected database names before writes, read the legacy database only, require
  an exact audited source digest to apply, and use the normal Go inbox writer.
  Keep original raw bodies, event IDs and Node delivery records. Conflicts and
  missing bodies block completion. Never forge a provider signature.
- [x] Test actual owned PostgreSQL transfer/restart/idempotency/conflicts,
  default read-only behavior, changed-source and wrong-target refusal. Preserve
  existing direct HMAC, deduplication and worker fencing acceptance.
- [x] Run focused/full applicable tests, build, vet, lint, formatting and docs
  checks. Retain historical migrations and evidence.

## Operational cutover

- [ ] Resolve authorized access to the actual legacy queue and target runtime.
  Prepare exact onboarding-only subscription payload, Go configuration,
  callback route and rollback evidence before any external change.
- [ ] Verify direct Go intake with actual signed sandbox delivery through the
  intended public callback and confirm durable receipt/restart behavior.
- [ ] Quiesce old intake, audit and transfer all outstanding canonical receipts,
  then verify no untransferred event remains. Retain unresolved conflict data.
- [ ] Switch the intended runtime to the checked retired Node source only after
  callback/backlog acceptance. Keep unrelated manual services and financial
  provider subscriptions untouched.

Provider or runtime writes remain unapplied until their concrete payload and
credential scope are reviewable and authorized. This source branch does not
claim a hosted deployment or a completed live queue drain.

## Prepared cutover surface

The Go API now has explicit `-webhook-only` mode exposing only the signed
onboarding route, legacy-route 410 and liveness. It hides business, docs and
internal JWT routes. The exact unsent sandbox subscription payload and local
cutover/rollback proposal are ready in the private evidence directory. See
[the runbook](/docs/demo/node-webhook-retirement.md). Live cutover remains
pending the credential-scope approval described above.
