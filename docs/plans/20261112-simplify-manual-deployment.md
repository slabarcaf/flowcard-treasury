# Simplify manual AWS deployment

Make the single-demo deployment runnable from GitHub Actions without manual
inputs. A maintainer selects `develop` and clicks **Run workflow**; validation,
build, deployment, and health checks then run in sequence.

This is an implementation plan. Creating it does not execute a release or
authorize application-data changes.

[TOC]

## Agreed behavior

- Remove all seven dispatch inputs. Manual dispatch authorizes the deployment.
- Hardcode `infra/environments/demo/foundation-outputs.json` as the deployment
  configuration path. Keep non-secret infrastructure IDs in this tracked file.
- Remove the manually supplied foundation checksum and historical foundation
  approval-evidence requirements. Configuration comes from the pinned commit.
- Keep credentials in AWS Secrets Manager and GitHub authentication through
  OIDC. Keep the existing repository variables; introduce no new secrets or
  environment-variable controls.
- Identify each release automatically by commit SHA and workflow run identity.
- Remove previous-release inputs and coupled rollback from the normal workflow.
- Continue publishing the frontend at its existing S3 paths. Upload assets
  first, publish `index.html` last, and invalidate mutable CloudFront paths.
- Leave older frontend assets in place. Add no deletion, retention scheduler,
  release-directory switching, or frontend rollback system.
- Keep automatic checksums for artifacts transferred between workflow jobs and
  immutable container digests. These require no manual input.

Every successful manual run will deploy. The removed approval checkbox will no
longer provide a validation-only mode. Existing local test/build commands remain
available; a separate CI-only workflow is outside this change.

## Current implementation

Task 1 pinned the captured foundation configuration and added pre-OIDC target
and resource validation. Task 2 now uses manual dispatch without input fields,
and removes foundation approval evidence and prior-release bookkeeping from
the normal workflow. It preserves the develop and repository restrictions,
artifact checksums, migrations, service health checks, and fixed frontend
publication paths. Task 3 now records commit/run/attempt identity, derives
safe run-specific artifact and image tags, and deploys by exact image digests.
The provenance check requires build and deploy artifacts from the same attempt;
failed-jobs-only retries must use **Re-run all jobs**. Task 4 was independently
accepted offline. Its latest strict full-suite run remains blocked by the
unavailable local PostgreSQL test database and one resulting onboarding E2E
failure; this does not count as a green full-suite result.

`infra/scripts/deploy-demo.sh` consumes infrastructure references, registers
task definitions, runs migrations, and updates services. The frontend script
already uploads assets before HTML and retains older objects. The finalizer
marks a release successful only after backend and frontend verification.

Existing runtime and platform acceptance limitations are documented in
`docs/infra/`. This change must preserve the application submission and RFI
gates; successful deployment does not establish provider acceptance.

## Configuration and release records

The tracked foundation JSON will contain only deployment references needed by
the scripts: account and region, ECS cluster, private subnets, migration
security group, ECR repositories, frontend bucket, CloudFront distribution,
and base service/migration task definitions. Reuse existing Terraform output
names and the current script schema where possible.

Remove the historical `approval_evidence` object and its workflow gates rather
than populating artificial approver names, PR links, dates, or status flags.
Existing approvals of runtime settings remain documented in the runbooks.
Actual configuration validation and required runtime wiring remain enforced.

The existing repository variables remain the source for deployment role,
account, region, frontend bucket, and distribution settings consumed by the
workflow. Validate matching values in the JSON to prevent conflicting targets.
Never put the JSON in `.env`, upload `.env` to GitHub, or commit secret values.
Infrastructure changes must update the corresponding tracked references.

Release metadata records the full commit SHA, run ID and attempt, deployed
image digests, task-definition revisions, frontend checksum, and phase result.
Use `<commit-sha>/<run-id>-<run-attempt>` as a logical release identity. Name
retained GitHub artifacts accordingly and pass the exact names between jobs.
This identity does not create per-release S3 website folders or replace image
digests and artifact-integrity checks.

## Development approach

- Execute in a new `.worktrees/` checkout based on `develop`, using the
  repository's agterm implementation and independent lead-review workflow.
- Use the existing test framework and fixtures. Add at most three new focused
  test cases across the whole change; update existing assertions as needed.
- Use fixtures and AWS CLI stubs for routine tests. Add no dependencies or test
  infrastructure. Do not call live providers during implementation tests.
- Complete each task's focused checks before advancing. Keep this plan current
  if implementation reveals a necessary scope change.
- Keep generated builds, manifests, and reports under ignored `.dev/`.

## Implementation steps

### Task 1: Define the fixed infrastructure configuration

**Files:**

- Modify `.github/workflows/aws-demo.yml`.
- Modify `infra/environments/demo/foundation-outputs.example.json`.
- Modify `infra/scripts/verify-offline-safety.py`.
- Modify `tests/server/github-actions-workflow.test.ts`.
- Create `infra/environments/demo/foundation-outputs.json` after the one-time
  read-only configuration capture described under Post-completion.

- [x] Simplify the example to deployment references, removing approval and
      reviewer evidence. Preserve the existing resource-field structure.
- [x] Define required field, account, region, and resource-identifier checks,
      including consistency with existing repository variables.
- [x] Reject missing configuration and example/placeholder identifiers before
      cloud mutation. Keep failure messages free of secret values.
- [x] Update existing foundation-fixture assertions. Use one new parameterized
      case only if needed for valid, missing, and mismatched configuration.
- [x] Run affected workflow tests. Capture the real configuration only through
      authorized read-only AWS queries, never with guessed or sample IDs.

### Task 2: Make manual dispatch deploy without inputs

**Files:**

- Modify `.github/workflows/aws-demo.yml`.
- Modify `infra/scripts/deploy-demo.sh` where workflow-facing requirements need
  adjustment.
- Modify `tests/server/github-actions-workflow.test.ts` and
  `tests/server/deploy-scripts.test.ts`.
- Modify `infra/scripts/verify-offline-safety.py`.
- Update this plan's current state and Task 2 checklist.

- [x] Remove all dispatch inputs, input expressions, and the checkbox job
      condition. Keep manual dispatch, exact repository/`develop` restrictions,
      release serialization, and deploy-job-only OIDC permission.
- [x] Load the hardcoded configuration from the pinned checkout. Remove the
      external foundation checksum, ancestor-bound approval bookkeeping, and
      configuration-approval flags that depended on the old evidence object.
- [x] Keep real account/region checks, scoped role/resource validation, runtime
      requirements, and artifact-integrity checks before deployment writes.
- [x] Remove previous-release input parsing, manifest merging, workflow
      arguments, and automatic coupled-rollback promises. Keep useful failure
      state for diagnosis and stop on errors; never report partial deployment
      as successful. Existing standalone manual recovery tools can remain.
- [x] Remove obsolete gate/proposal text from workflow summaries and metadata.
      Preserve migration ordering, private service wiring, and health waits.
- [x] Update existing workflow, script, and safety-check expectations. Use at
      most one new stubbed execution case for successful no-input deployment
      and pre-mutation rejection of invalid targets.
- [x] Run affected workflow/deploy tests and all offline safety checks.

### Task 3: Record release identity automatically

**Files:**

- Modify `.github/workflows/aws-demo.yml`.
- Modify `infra/scripts/deploy-demo.sh` and
  `infra/scripts/finalize-release.sh` as required for metadata compatibility.
- Modify `tests/server/github-actions-workflow.test.ts` and
  `tests/server/deploy-scripts.test.ts`.
- Modify `infra/scripts/verify-offline-safety.py`.
- Update this plan's current state and Task 3 checklist.

- [x] Derive the release identity from commit SHA, run ID, and run attempt.
      Include it in summaries, manifests, and retained artifact names.
- [x] Keep build/download artifact names consistent across jobs. Preserve
      narrowly scoped hidden-file uploads and checksum verification.
- [x] Record container digests and deployed revisions automatically. Mark
      success only after service checks and frontend invalidation finish.
- [x] Preserve the existing frontend upload order and fixed S3 keys. Do not
      add old-object deletion or per-release website directories.
- [x] Update existing metadata, frontend-upload, and finalization assertions.
      Use at most one new case if necessary to cover distinct rerun identities
      and failures remaining unfinalized.
- [x] Run the affected tests and offline safety checks before continuing.

### Task 4: Verify the simplified release contract

**Files:**

- Review `.github/workflows/aws-demo.yml`, affected scripts, and existing tests.

- [x] Verify there are no manual inputs, obsolete `inputs.*` references,
      foundation-evidence prerequisites, or normal-flow rollback prerequisites.
- [x] Verify `develop` remains the permitted run ref and the immutable OIDC
      subject stays unchanged. No AWS permissions expansion is expected.
- [x] Run frontend/server builds, affected workflow/deployment/frontend tests,
      offline safety checks, infrastructure validation, and staged diff checks.
- [x] Use the existing strict Node baseline comparator if the full suite is
      run; report unexpected failures separately from its documented baseline.
      Keep fixture tests independent of live AWS and Airwallex.
- [x] Report the known vendored Swagger lint findings separately. If they
      block the first CI run, address them with a narrowly scoped generated-file
      exclusion; do not suppress application-source lint failures.
- [x] Independently review the final diff and confirm at most three new test
      cases were added. Add no further coverage without Ramil's agreement.

### Task 5: Update deployment instructions and acceptance status

**Files:**

- Modify `docs/infra/ci-cd-runbook.md`, `docs/infra/demo-operations.md`,
  `docs/infra/demo-acceptance.md`, and `docs/infra/github-oidc.md`.
- Modify `docs/infra/services-and-migrations.md`,
  `docs/infra/account-access.md`,
  `infra/environments/demo/foundation-plan-review.md`, and this plan where
  needed to remove active instructions for superseded gates.

- [x] Document the no-input release: select `develop`, run, monitor checks,
      and inspect the automatically generated release record.
- [x] Explain tracked deployment configuration, existing repository variables,
      AWS-managed secrets, and when infrastructure references need updating.
- [x] Remove active manual-field and automatic rollback instructions. Preserve
      historical evidence and describe partial-failure recovery accurately.
- [x] Document retained old frontend assets and the distinction between a
      healthy deployment and unresolved platform onboarding acceptance.
- [x] Check updated Markdown and local links. Leave this plan open until the
      outstanding local database baseline check and first live workflow are
      verified;
      move it to `docs/plans/completed/` only after that evidence exists.

## Post-completion and first live run

Task 1 completed the one-time read-only configuration capture from Terraform
outputs and AWS resource descriptions in account `968198283143`, region
`us-west-2`. The resulting non-secret references are tracked in
`infra/environments/demo/foundation-outputs.json`; no raw state or secret
payloads were exported. Update that file only when reviewed infrastructure
changes replace a referenced resource.

Publish the reviewed workflow changes to `develop` and the workflow entry on
default branch `main`. Keep the implementation on the intended run ref and do
not change the repository's default branch or OIDC trust as part of this plan.

The first manual workflow run uses `develop` and no input fields. It remains
outstanding. Before treating the full Node baseline as clear, rerun the strict
comparator with the required local PostgreSQL test database available; report
the 23 documented failures separately and do not claim a green suite while any
unexpected failure remains. On the first workflow run, observe tests and builds,
OIDC authentication, migrations, service rollout, frontend upload, and
invalidation. A failure must retain diagnostic metadata and identify the last
completed phase. The workflow does not automatically roll back the database or
claim recovery. Recording a commit SHA alone does not restore database state.

After a successful run, verify the shared HTTPS frontend, operator login,
company CRUD, Swagger, and service health. Record actual run URL, commit,
release identity, digests, and results. Submission/RFI/provider acceptance stays
subject to its existing separate application gates.

The tracked configuration capture is complete. Publishing the reviewed workflow
changes and running its first live workflow are subsequent execution steps.
This documentation task performs neither and does not establish deployment
acceptance.
