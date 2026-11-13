# FlowCard Demo Operations

Run repository commands from the repository root. This is the operational handoff
for the FlowCard AWS demonstration environment.
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
pending. The historical offline readiness table below predates this checkpoint.

| Area | Status | Evidence or remaining work |
| --- | --- | --- |
| Task 8 release code and focused fixtures | `VERIFIED_OFFLINE` | Independent Task 8 review accepted 120 focused tests. The later CloudFront IAM correction passed 74 workflow tests, 27 CloudFront tests, and 138 offline-safety checks. |
| Frontend application build and routing fixtures | `VERIFIED_OFFLINE` | `npm run build` passes; route fixtures preserve `#bridge-capital`, `#portal`, and `#kit5`, including the legacy GD/Kit 5 preference on `#kit5`. |
| Full Node test baseline | `VERIFIED_OFFLINE` | The strict comparator ran 939 tests; all 23 failures matched the documented baseline, with zero unexpected regressions. |
| Synthetic seed facility safety | `VERIFIED_OFFLINE` | PostgreSQL coverage verifies another active facility is preserved, fresh default creation is idempotent, and original facility rows are restored by fixture rollback. |
| Go service checks | `VERIFIED_OFFLINE` | `go vet`, OpenAPI freshness, and `go test -race ./...` passed using the local `flowcard_onboarding_test` database and advisory lock `88888888`. |
| AWS account identity, MFA, and named temporary profile | `BLOCKED_APPROVAL` | Root account access was the last recorded state. Identity Center, MFA, account, and STS caller identity need authorized owner setup and live verification. |
| Repository Actions variables and OIDC trust | `PENDING_LIVE` | Parent operator must configure and read back repository variables, then apply and verify the trust-only AWS change. No deployment is authorized by this task. |
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
| Foundation outputs | Exact account and region, tracked non-example output artifact, full artifact SHA-256, ancestor-bound review source, reviewed output digest, usable PR reference | Authorized foundation operator and reviewer |
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

1. **Resolve account access.** Configure temporary human access and the planned
   `flowcard-demo` profile only after owner approval. Before a plan or apply,
   run `aws sts get-caller-identity --profile flowcard-demo` and compare the
   account ID with the approved target. This profile is not currently
   configured. Do not print credentials.
2. **Review bootstrap changes.** Initialize and plan
   `infra/bootstrap/`; inspect the plan for the state bucket, KMS key, locking,
   retention, and account. Apply only the reviewed plan after separate
   authorization. Preserve the state bucket and KMS key during later teardown.
3. **Review the demo foundation.** Initialize `infra/environments/demo/` with
   its approved partial backend configuration. Review the complete plan,
   including NAT, ALB, RDS, CloudFront VPC origin, S3, KMS, Secrets Manager,
   IAM, and budget resources. Verify database minor-version availability and
   cost before any authorized apply.
4. **Create the output evidence.** Put actual Terraform outputs at
   `infra/environments/demo/foundation-outputs.json`. Never use
   `foundation-outputs.example.json` as deployment input. Supply the exact
   file SHA-256 to the workflow. The artifact must identify a reviewed source
   ancestor, plan digest, canonical foundation-output digest, reviewer, and
   review PR. Do not fill evidence fields until an actual review has occurred.
5. **Configure repository deployment variables and OIDC trust.** Use the
   documented repository variables and exact immutable subject. The privileged
   job requires manual dispatch from `develop` and explicit confirmation.

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

### Initial ECS task-definition bootstrap

`deploy-demo.sh` registers revisions from existing ECS task-definition
families. If a family is absent, the script fails before migrations or service
updates. The first authorized foundation plan must not assume those families
exist. The documented pre-registration sequence is:

1. Review and apply the base foundation with services disabled.
2. Build and publish immutable Linux `amd64` images through the approved
   release process, retaining the exact four digests in release recovery
   metadata.
3. Review a separate Terraform plan with `enable_services = true`,
   `service_desired_count = 0`, and those exact image digests. Apply only that
   reviewed plan to create task-definition families and zero-count services.
4. Confirm the exact resources and plan state before a later authorized
   release. Do not repeat an apply blindly or substitute mutable tags.

This bootstrap sequence has not been run. Account setup, image publication,
Terraform planning, and apply remain separately authorized live operations.

## Release procedure and evidence

The checked-in workflow is manual (`workflow_dispatch`), restricted to
`slabarcaf/flowcard-treasury` on `develop`, serialized, and uses OIDC. A future
authorized operator should:

1. Ensure the workflow file is present on the repository's default `main`
   branch so GitHub offers it for manual dispatch. Then choose `develop` in
   **Use workflow from**, or run `gh workflow run aws-demo.yml --ref develop`.
2. Before dispatch, review the workflow commit, source tests,
   OpenAPI freshness, baseline comparator, offline-safety checks, Terraform
   validation, frontend checksum, container archive digests, and foundation
   artifact. ECR image digests are produced later in the privileged job.
3. Supply the tracked foundation artifact path and its mandatory SHA-256. The
   workflow rejects the example file, wrong account or region, missing review
   evidence, and mismatched digests before AWS role assumption.
4. Dispatch manually from `develop` and set the explicit confirmation input.
5. Let the deployment job publish immutable images, run migrations and seed,
   verify backend service health, upload frontend files, wait for the exact
   CloudFront invalidation, and finalize release metadata.
6. Treat the release as successful only when
   `.dev/.release-metadata/deployed-release.json` says
   `deployed_and_verified` and the frontend status is
   `published_and_invalidation_completed`. A frontend failure leaves the
   manifest at `backend_verified_frontend_pending`.

Capture the workflow run ID and attempt, source commit,
Node/API/worker/migration digests, frontend SHA-256, final manifest,
task-definition ARNs, CloudFront invalidation ID, and test results. Scrub
cookies, tokens, company data, personal data, and secret values from evidence.

### Prior frontend artifact limitation

The workflow records the frontend release identity as
`<GITHUB_RUN_ID>:<GITHUB_RUN_ATTEMPT>` in `target_release.frontend_artifact_id`;
this value is not the GitHub Actions artifact ID, despite the current dispatch
input name `previous_frontend_artifact_id`. `frontend-dist` is retained for
14 days;
release recovery metadata is retained for 90 days. The current workflow does
not download a frontend artifact from a prior workflow run. Its
`previous_release_manifest` and `previous_frontend_artifact` inputs are paths
inside the current runner, not cross-run artifact selectors.

For local recovery, an authorized operator must identify the successful run
and attempt, download its `release-recovery-metadata` and `frontend-dist`
artifacts using GitHub Actions tools, then compare the downloaded frontend
directory with the successful manifest checksum using
`infra/scripts/compute-frontend-hash.sh`. If the artifacts expired or the
identity/checksum do not match, stop. Cross-run artifact retrieval for a future
workflow rollback needs a separate reviewed implementation.

After separately authorized GitHub access, `gh run download <RUN_ID>` can
retrieve each named artifact into a local recovery directory. Verify the
manifest's `target_release.commit_sha`, release identity, and frontend checksum
against the selected workflow run and downloaded files. A rerun can reuse a run
ID, so also confirm `GITHUB_RUN_ATTEMPT`; do not assume the newest artifact is
the one recorded in the manifest.

For a separately authorized local rollback, provide the verified files and an
approved temporary profile to the coordinator:

```bash
infra/scripts/rollback-release.sh \
  --profile <approved-profile> \
  --confirm-approval \
  --account-id "<AUTHORIZED_ACCOUNT_ID>" \
  --region "us-west-2" \
  --manifest "<RECOVERY_DIR>/deployed-release.json" \
  --frontend-build-dir "<FRONTEND_DIR>"
```

## Rollback, monitoring, and teardown

For a future explicitly authorized rollback, use
`infra/scripts/rollback-release.sh` with a finalized manifest and the exact
matching frontend build directory. The script couples backend task-definition
reversion with frontend restoration and invalidation. Database migrations are
never automatically downgraded; preserve all persisted database and document
data. If either rollback phase fails, record the partial state and stop for
operator recovery. Do not claim "all restored."

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
