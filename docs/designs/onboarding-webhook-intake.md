# Direct Go onboarding webhook intake

Add provider-authenticated receipt to the existing Go onboarding service.
Reuse its inbox, processor and lease fencing. Node forwarding and hosted
callback routes stay in place until a separately verified cutover.

[TOC]

## Receipt boundary

`POST /webhooks/airwallex` verifies HMAC-SHA256 over the exact timestamp header
followed by the original body, matching the tested Node contract and
[Airwallex delivery headers](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events).
Require one signature and timestamp header. Accept at most 10 MiB streamed,
with a 15-second transport read deadline on this route only.
After signature verification, reject invalid/nonpositive millisecond timestamps
and timestamps over five minutes ahead. Accept old timestamps as Ramil selected;
durable event identity handles replay. This tolerance is application policy.

Opt in with paired `ONBOARDING_WEBHOOK_SECRET` and
`ONBOARDING_WEBHOOK_ENVIRONMENT` (`sandbox` or `production`). They do not reuse
the internal JWT secret or silently enable from Node's environment. Unconfigured
intake returns 503. Environment comes from the server; supplied mismatches fail.
The configured environment must match the worker/provider deployment.

Verify before parsing. Require an object with body-owned `id` and `name`,
reject duplicate JSON keys, and accept only `account.*`/`rfi.*`. No header can
supply a missing event ID. Existing internal JWT delivery endpoints remain
protected with their current contract.

## Durability and recovery

Commit receipt before 200. Store exact raw bytes alongside existing JSONB.
Compare duplicate payloads semantically, including metadata, under a row lock.
Ignore insignificant JSON formatting; preserve original receipt and terminal
results. A changed payload for an existing event creates immutable conflict
evidence and returns 409. Quarantine unfinished original work and clear its
lease atomically, fencing in-flight workers. Preserve already processed results.
For compatible old Node deliveries, an absent environment remains acceptable
only on an otherwise identical event; it does not overwrite stored scope.

Existing polling and expired-lease reclamation recover accepted work after a
restart. RFI handling remains conservative: unverified correlation stays
quarantined. No fabricated question, account state or provider acceptance.

## Verification and cutover

Use signature/body unit cases plus real HTTP, restricted PostgreSQL receipt,
concurrent duplicates/conflicts, restart and stale-worker fencing. Create only
unique marker-owned databases/logins via a private admin-URL file. Never run
existing truncating tests against shared/manual data.

Keep Node intake, its pending deliveries, migrations and deployed routing until
normal migration, subscription/routing and backlog-drain prechecks are accepted.
No infrastructure, provider subscription, live event or root `.env` change.

## Retirement candidate

The isolated retirement change adds a callback-only launch of the existing Go
API and a privileged, audit-first legacy receipt transfer CLI. Public callback
mode exposes the signed `/webhooks/airwallex/onboarding` route and liveness;
normal business and internal JWT routes are absent from that mode. Legacy
receipt transfer uses mandatory expected database names, an exact audited
source hash, read-only Node access and the normal Go durable inbox writer.
Original bytes, event IDs and historical Node rows remain preserved.

See [the cutover runbook](/docs/demo/node-webhook-retirement.md). Actual provider
delivery and backlog acceptance still gate runtime retirement. The candidate
does not switch a hosted callback or remove historical migrations/data.
