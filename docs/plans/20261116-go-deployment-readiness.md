# Go deployment readiness and publication

Prepare the completed Go implementation for an explicitly dispatched AWS demo
release, integrate finished work, preserve private artifacts while removing
merged worktrees, and publish local develop to GitHub. Ramil authorized this
sequence after Rootie 8's final merge. This task dispatches no deployment.

[TOC]

## Baseline and boundaries

- Rootie 8's final verified merge is `73e9b75`, with retirement source
  `3fb6e83`.
- The legacy AWS workflow builds Node and only company-onboarding Go. It does
  not deploy shortfall, underwriting, servicing, coordinator or real Go login.
- Work directly in `.worktrees/go-deployment-readiness`, created from develop.
  Do not spawn agents, copy root `.env`, modify running books or dispatch AWS.
- Preserve the 20 pre-existing main changes, consent backlog, private database
  URLs, credentials, signed evidence, native sandbox records and runtime data.
- Source readiness, remotely passing checks and actual deployment are distinct.
  Record missing first-deployment configuration rather than inventing values.

## Deployment design

Use one private Fargate runtime task containing separate Go containers. The
public browser gateway retains the existing gateway service/port deployment
slot. Internal APIs communicate over localhost, preserving their existing
HTTPS requirements for non-loopback service URLs. Each container receives its
own restricted database URL and only the service secrets it needs. Migration
credentials are available only in a separate one-off task.

A common Go image builds all six committed modules and their migration/worker
commands, including the local public policy replacement used by servicing.
Image aliases reuse the existing scoped ECR repositories. Deploy only immutable
digests and preserve exact previous task definitions and desired counts.

Opt-in foundation additions provide distinct ALB target groups for native Go
webhooks. CloudFront forwards the native webhook subpaths without compression;
browser traffic continues through the real-login gateway. Existing callback
registrations are not changed by this source work. Legacy callback takeover
requires its own reviewed secret configuration and remains explicit.

New release ordering: validate/build sealed artifacts, verify configuration,
quiesce old writers, run forward migrations, verify restricted users, update
the Go runtime, verify service/container/target health, publish the Go-enabled
frontend, and finalize the exact-source release. Failures retain recovery
metadata and do not automatically restore an old financial writer.

Financial dispatch defaults off. Workers and native execution contracts require
explicit installation configuration. Initial empty read services must report
missing business data honestly, without synthesizing a provider account or
customer capital. Existing accepted sandbox profiles are never uploaded as
implicit production configuration.

## Tasks

- [x] Verify Rootie 8's final merge and source-preservation handoff.
- [x] Create the isolated worktree and inspect the existing infrastructure.
- [x] Define a versioned deployment/secret contract and common runtime image.
- [x] Add bounded migration/bootstrap and health tooling with focused tests.
- [x] Prepare opt-in foundation routing and prevent Terraform undoing releases.
- [x] Add ordinary unprivileged CI for all six modules and the frontend.
- [x] Add a manual Go release workflow with sealed artifacts and scoped OIDC.
- [x] Verify rollout ordering, failure handling and secret isolation offline.
- [x] Build and smoke-test the common image against owned disposable data.
- [x] Integrate completed Go deployment source into develop; preserve prior drafts.
- [x] Archive private worktree artifacts and remove only completed worktrees.
- [ ] Check unpublished history for secrets and push develop without force.
- [ ] Inspect the actual remote SHA/checks and report operational prerequisites.

## Acceptance

All APIs and optional workers are represented by real image commands and
explicit runtime configuration. CI exercises their actual tests with isolated
PostgreSQL, and has no AWS deployment credentials. Manual deployment rejects a
wrong repository/ref/account, unsealed artifact, invalid digest, unprepared
foundation, missing configuration, failed migration or unhealthy runtime.
Container health is separate from provider/financial readiness.

Local evidence, secret values and unreviewed drafts never enter Git or image
contexts. Worktree cleanup requires ancestry/content verification, checked
archives and safe live-process ownership. The final remote develop SHA must
equal the locally tested integrated SHA, with no force push or deployment.

## Integration and cleanup evidence

Deployment source commit `964be07` was merged at `a119a37`. Local checks
passed: 351 safe product/release tests, 60 servicing tests, 22 gateway race
tests, infrastructure validation and the exact committed common-image smoke.
Five completed worktrees were archived with byte-verified inventories and
removed with their ancestor branches. Private archives remain under ignored
`.dev/worktree-archives/20261116-publication/`. No database was deleted.
The original review UI on port 18073 and onboarding API on port 8080 remain
available from the main checkout. The obsolete mock preview was retired.

The 20 pre-existing main changes remain outside this publication. Automatic
approval review rejected including prior documentation and the unfinished
shortfall draft; those files are preserved without staging. No AWS workflow
was dispatched. The new foundation output variable, installation secrets,
DBA prerequisites and explicit callback cutover remain deployment gates.
