# FlowCard Demo Operations

Run repository commands from the repository root. This is the operational
handoff for the FlowCard AWS demonstration environment.
It consolidates the release workflow and prerequisites for future authorized
operations. The manual rollout checkpoint records operator-provided evidence.

[TOC]

## Status terms

- `VERIFIED_OFFLINE`: Source, configuration, or fixture-based checks passed.
  This does not establish that a cloud resource exists or works.
- `PENDING_LIVE`: A future authorized check must inspect or exercise a live
  resource.
- `BLOCKED_APPROVAL`: A named user or owner decision is required before the
  operation can proceed.
- `BLOCKED_ENVIRONMENT`: A required local or CI dependency is unavailable.

Do not change a live status based on Terraform syntax, a healthy container, or
an available API documentation page alone.

## Current readiness

### Manual rollout checkpoint, November 12, 2026

Ramil completed the rollout in account `968198283143`, region `us-west-2`,
using profile `agentic-banking-hackathon`. PostgreSQL `17.11`, both migrations,
the Node synthetic seed, and all three ECS services started successfully.
The ALB accepts HTTP from the CloudFront service-managed security group, and the
private frontend bucket grants CloudFront access to the exact distribution ARN.

The frontend and Swagger UI load at
`https://d2dogzq0d9gxdl.cloudfront.net`. Ramil reported successful operator login
and company create/read/update/delete responses; the test company was deleted.
An unauthenticated company request returned `401`. These results are operator
evidence, not independent AWS inspection by the assistant.

The four runtime settings were approved, secrets populated, and corrected images
deployed. Existing cost alerts are user-reported; no new budget was requested.
Airwallex submission and RFI decisions, provider/webhook acceptance, restore and
rollback rehearsals, alert delivery, and protected GitHub deployment remain
pending. This checkpoint is historical operator evidence from the earlier
manual rollout. It does not verify the simplified GitHub workflow, whose first
live run remains pending. The table below distinguishes that report from later
local implementation checks.

| Area | Status | Evidence or remaining work |
| --- | --- | --- |
| Deployment workflow implementation | `VERIFIED_OFFLINE` | Tasks 3 and 4 were accepted offline. Focused workflow/deploy tests pass 91; offline safety passes 138; builds, lint, formatting, and OpenTofu validation pass. The first live workflow run remains pending. |
| Frontend application build and routing fixtures | `VERIFIED_OFFLINE` | `npm run build` passes; route fixtures preserve `#bridge-capital`, `#portal`, and `#kit5`, including the legacy GD/Kit 5 preference on `#kit5`. |
| Full Node test baseline | `VERIFIED_OFFLINE` | Latest strict comparison: 940 tests, 911 passed, 23 documented failures, 6 skipped, zero unexpected failures; comparator exit 0. An earlier run lacked local PostgreSQL and had one extra onboarding E2E failure; the isolated database rerun resolved it. Raw Vitest still exits 1 for the 23 known failures. |
| Synthetic seed facility safety | `VERIFIED_OFFLINE` | PostgreSQL coverage verifies another active facility is preserved, fresh default creation is idempotent, and original facility rows are restored by fixture rollback. |
| Go service checks | `VERIFIED_OFFLINE` | `go vet`, OpenAPI freshness, and `go test -race ./...` passed using the local `flowcard_onboarding_test` database and advisory lock `88888888`. |
| AWS account identity, MFA, and named temporary profile | `BLOCKED_APPROVAL` | Root account access was the last recorded state. Identity Center, MFA, account, and STS caller identity need authorized owner setup and live verification. |
| Simplified workflow OIDC execution | `PENDING_LIVE` | Run the reviewed workflow on `develop`, then verify OIDC authentication and the release record. This documentation work does not perform a live run. |
| Foundation resources and cost review | `BLOCKED_APPROVAL` | Requires an authorized account, a reviewed Terraform plan, a cost decision, and explicit provisioning approval. |
| Four onboarding settings | `VERIFIED_OFFLINE` | Ramil approved all four names; standard loaders and focused tests are updated. The November 12 operator checkpoint reports values provisioned and services deployed, but this work did not independently inspect AWS. |
| Platform submit and RFI contract | `BLOCKED_APPROVAL` | Submission revision policy, US requirements, and RFI confirmation remain unresolved. The API keeps its unverified platform gate active. |
| Live endpoint, database, storage, webhook, backup, and alarm checks | `PENDING_LIVE` | Run the Task 9 acceptance matrix only after the related approval gates clear. |

## Decisions and prerequisites

Resolve each blocked item before planning or applying the related resources.

| Decision or prerequisite | Required evidence | Owner |
| --- | --- | --- |
| AWS routine access | Root MFA and account review; dedicated temporary human role; named CLI profile; STS identity check returns the authorized account | Account owner, then operator |
| Repository deployment configuration | Repository Actions variables match the documented account, region, role, bucket, and distribution; AWS OIDC trust matches the immutable `develop` subject | Repository and AWS owners |
| Tracked deployment references | `infra/environments/demo/foundation-outputs.json` contains actual non-secret resource references and matches the existing Actions variables. Update it when Terraform replaces a referenced resource; validate the diff and workflow checks. | Infrastructure operator and reviewer |
| Budget alert | Approve amount and notification email before setting `enable_budget = true` | Ramil |
| Runtime integration | Names approved and loader activation completed. The operator checkpoint reports provisioning and deployment; verify through approved release evidence before claiming independent live acceptance | Operator |
| Database sizing and engine | PostgreSQL `17.11` on `db.t4g.micro` was selected after the live Oregon availability check and applied by Ramil | Ramil and operator |
| Platform contract | Approve the submit revision policy and verify US company roles, fields, documents, API version, and RFI confirmation behavior | Ramil and platform owner |

The cost document's `$150/month` budget is a proposal, not approval. Its price
estimates require current review before provisioning. No budget notification
email is approved in the current configuration.

## Foundation setup sequence

Use [`account-access.md`](account-access.md) and
[`bootstrap-runbook.md`](bootstrap-runbook.md) for the detailed human identity
and Terraform backend procedures. Keep every plan and apply under the exact
authorized account and `us-west-2` region.

1. **Resolve account access.** Use only an owner-approved temporary human role
   and named profile. Before a plan or apply, run
   `aws sts get-caller-identity --profile <approved-profile>` and compare the
   account ID with the approved target. Do not print credentials.
2. **Review bootstrap changes.** Initialize and plan
   `infra/bootstrap/`; inspect the plan for the state bucket, KMS key, locking,
   retention, and account. Apply only the reviewed plan after separate
   authorization. Preserve the state bucket and KMS key during later teardown.
3. **Review the demo foundation.** Initialize `infra/environments/demo/` with
   its approved partial backend configuration. Review the complete plan,
   including NAT, ALB, RDS, CloudFront VPC origin, S3, KMS, Secrets Manager,
   IAM, and budget resources. Verify database minor-version availability and
   cost before any authorized apply.
4. **Maintain tracked deployment references.** The real, non-secret values are
   committed at `infra/environments/demo/foundation-outputs.json`; the workflow
   reads this fixed path from the pinned commit. Do not substitute the example
   file. There is no user-supplied checksum or foundation approval-evidence
   object. When Terraform replaces a referenced resource, capture the actual
   output, update the file, confirm it matches repository variables, and review
   the change before merging.
5. **Maintain repository variables and OIDC trust.** Keep the existing account,
   region, role ARN, frontend bucket, and CloudFront distribution variables in
   sync with the tracked file. The workflow remains restricted to the exact
   `develop` OIDC subject; this plan does not change the trust policy.

## Secrets, database roles, migrations, and seeds

Terraform defines secret containers and references; it does not supply secret
values. An authorized operator provisions values through a secure channel after
role and application approvals. Do not put secret values in command arguments,
shell history, `.env` copies, GitHub artifacts, logs, or Terraform state.

The intended roles remain separate:

- The RDS master credential is reserved for authorized DBA setup and is never
  injected into runtime tasks.
- Node and Go services use the approved application DML credentials.
- Migration tasks use the separate DDL credentials.
- Go API document writes use the document bucket and KMS permissions; worker
  access remains read-only.

Use [`services-and-migrations.md`](services-and-migrations.md) for role grants
and the two migration binaries. The release workflow runs Node migrations,
then Go migrations, then the idempotent synthetic seed, then service rollout.
Migration or seed failure stops rollout. A later application rollback never
downgrades database schemas or deletes RDS/S3 data.

Seed records use synthetic aliases only; they do not create live Airwallex
accounts. The facility seed preserves an existing active operator configuration
and does not deactivate or update existing rows.

### Secret rotation

The repository does not provide automated rotation. An authorized owner must
coordinate database role changes and the corresponding Secrets Manager values.
Use an approved secure editor or secret-management interface. Store JSON
payloads in a protected file only when required, with mode `0600`, and
remove it after use. Do not pass secret values in command arguments or print
them. After updating a secret, deploy replacement tasks through the approved
release workflow and verify the service before retiring any prior secret
version. Rotation procedures and rollback of prior secret versions remain
`PENDING_LIVE` until rehearsed.

### Original ECS task-definition bootstrap sequence

The original infrastructure notes describe creating empty ECS services before
image-backed rollout. The November 12 operator checkpoint reports all three
services started. Treat that sequence as historical planning context; check
current state before any later infrastructure plan or apply, and never repeat
an apply blindly.

## Release procedure and evidence

The checked-in workflow is manual (`workflow_dispatch`), restricted to
`slabarcaf/flowcard-treasury` on `develop`, serialized, and uses OIDC. The
simplified workflow has no input fields. Dispatching it starts validation and,
if all gates pass, deployment.

1. Open **Actions**, select **AWS Demo Deployment**, choose `develop` under
   **Use workflow from**, and click **Run workflow**. There is no confirmation
   checkbox, foundation checksum, or previous-release field.
2. Follow the run page. Validation and builds run before the privileged job.
   The deploy job verifies the pinned commit, artifact provenance and
   checksums, and `infra/environments/demo/foundation-outputs.json` against the
   existing repository variables before it assumes the OIDC role.
3. Review the build summary and generated manifest for the source commit,
   workflow run ID and attempt, logical release identity, artifact names, and
   frontend checksum. The deploy phase records image digests and task-definition
   revisions.
4. Monitor Node and Go migrations, the synthetic seed, ECS service health,
   frontend publication, and CloudFront invalidation.
5. Treat the release as successful only when
   `.dev/.release-metadata/deployed-release.json` says
   `deployed_and_verified` and the frontend status is
   `published_and_invalidation_completed`. A backend-only success or pending
   frontend state is a partial release.

The logical release identity is `<full-commit-sha>/<run-id>-<run-attempt>`.
Artifact names and ECR tags use a safe hyphenated form with the same attempt.
If a failed-jobs-only retry reuses earlier build outputs, provenance fails
before OIDC. Select **Re-run all jobs** to build and deploy from one matching
attempt.

The frontend is published at its existing S3 paths: hashed `/assets/` files,
other public files at their existing root-relative paths, then `index.html` at
the fixed root key. Old S3 objects are retained. The deploy script invalidates
mutable paths and waits for completion before finalizing the release record.

Capture the workflow run and attempt, source commit, image digests, frontend
SHA-256, final manifest, task-definition ARNs, invalidation ID, and test
results. Scrub cookies, tokens, company and personal data, and secret values.

### Artifact retention and separate manual recovery

The workflow retains frontend and container artifacts for 14 days, the build
manifest for 30 days, and recovery metadata for 90 days. It does not download
artifacts from an earlier run. A separately authorized recovery must select a
known successful run and attempt, download its frontend artifact, and verify
that its checksum matches the release metadata.

Do not pass a new workflow manifest directly to
`rollback-release.sh --manifest`.
That option reads legacy `.previous_release.*` fields, which current manifests
deliberately omit. For a current release, review its explicit target fields and
pass the values through the existing CLI options:

| Current manifest field or artifact | `rollback-release.sh` option |
| --- | --- |
| `.services.node_gateway.revision` | `--node-revision` |
| `.services.go_api.revision` | `--api-revision` |
| `.services.go_worker.revision` | `--worker-revision` |
| `.frontend.dist_checksum_sha256` | `--prior-frontend-checksum` |
| `.frontend.bucket_name` | `--bucket` |
| `.frontend.distribution_id` | `--distribution-id` |
| Verified frontend artifact from the same run and attempt | `--frontend-build-dir` |

Also provide the approved account, region, authentication profile, and the
script's explicit `--confirm-approval` flag. Extract and review these values
from the successful release selected as the recovery target. Stop if its
artifact is missing, expired, or has a checksum mismatch. Manual recovery
remains unverified until rehearsed.

A failed workflow does not automatically roll back. Preserve recovery metadata
and persistent database and document data. Identify the last completed phase
and report any partial state; a failed frontend phase can leave the backend and
static site at different revisions. Database migrations are never
automatically reversed. This recovery is separate from normal workflow
dispatch.

## Monitoring and teardown

Future live checks should inspect CloudWatch alarms and log retention, RDS
backup retention and restore procedure, database-role grants, document-bucket
encryption and access, CloudFront behavior, and budget notifications. These
checks have not been performed.

Setting ECS desired count to zero does not stop all charges. RDS, NAT gateway,
ALB, public IPv4, storage, secrets, KMS, and logs may continue to incur costs.
The demo is intended to remain always on, subject to a user-approved cost
decision. There is no automatic teardown authorization. Any destroy plan must
be separately reviewed; preserve state, backups, the database, and documents
unless Ramil explicitly approves a data-retention decision.

No safe application-data reset command has been verified. Use only a documented
synthetic reset after its behavior and data effects are explicitly reviewed.

## Related runbooks

- [`demo-acceptance.md`](demo-acceptance.md): acceptance matrix and evidence
  capture rules.
- [`account-access.md`](account-access.md): identity and account prerequisites.
- [`bootstrap-runbook.md`](bootstrap-runbook.md): Terraform state setup.
- [`runtime-configuration.md`](runtime-configuration.md): environment and
  secret approvals.
- [`services-and-migrations.md`](services-and-migrations.md): role separation,
  migrations, and service contracts.
- [`github-oidc.md`](github-oidc.md): OIDC claims and least-privilege IAM.
- [`demo-costs.md`](demo-costs.md): earlier provisional cost estimate.
