# AWS sandbox demo deployment

Deploy FlowCard's frontend, Node gateway, Go API and Go worker to AWS as an
always-on development demo. This plan records agreed choices and pending
implementation work. Infrastructure creation and live platform operations
require subsequent execution authorization.

[TOC]

## Agreed scope

- Personal AWS account currently accessed with root login. AWS CLI installed
  locally and unconfigured. Configure a dedicated human deployment identity
  with temporary credentials and a named CLI profile.
- Region is Oregon, `us-west-2`.
- Managed ECS Fargate services for Node gateway, Go API and Go worker.
- Frontend and gateway share an AWS-generated CloudFront HTTPS hostname.
- Fresh RDS PostgreSQL database with synthetic seed data and separate runtime
  roles. Airwallex uses sandbox accounts and endpoints.
- Private S3 stores frontend files and onboarding documents in distinct buckets.
- Terraform defines infrastructure, with versioned encrypted S3 state and
  locking.
- GitHub Actions deploys after approval. Authenticate to AWS through GitHub OIDC
  with scoped roles and environment/branch restrictions.
- Always-on, low-traffic demo. Target approximately $100–150 monthly before
  taxes/credits. Earlier component ranges sum to roughly $106–160, so this is
  provisional. Confirm Oregon line items before provisioning and choose an
  approved budget alert threshold during implementation.

## Infrastructure location

Keep deployment-owned Terraform, scripts, environment definitions,
configuration templates, runbooks and estimates under `infra/`.
Implementation planning documents belong in `docs/plans/`.

```text
infra/
  bootstrap/
  environments/demo/
  modules/
  scripts/
  docs/
```

GitHub requires workflow entry files under `.github/workflows/`. Keep that entry
thin and call checked-in scripts from `infra/scripts/`. Service source and its
Dockerfile remain in their existing application paths. Application runtime
wiring changes belong beside the existing application entrypoints.

## Proposed architecture

CloudFront serves static frontend files from private S3 with origin access
control. Route `/api/*` and `/docs/api*` to the private Node ALB origin using
CloudFront VPC origins. Preserve route specificity and trailing-slash behavior.
Never apply SPA fallback rewrites to APIs, webhooks, docs assets or specs.

Run Node and Go tasks in private subnets, with a single managed NAT gateway for
sandbox outbound platform calls and image/service access. Use an S3 gateway
endpoint where appropriate. One task per service and Single-AZ RDS are proposed
initial demo sizes, subject to cost review. This is not a high-availability SLA.
Use ECS private service discovery for Node-to-Go access without a second ALB.

Node authenticates operators and verifies external Airwallex signatures. Go's
private API trusts scoped short-lived service assertions. Worker has no public
listener. Security groups restrict database and internal-service access.

Keep frontend and gateway on the same HTTPS origin. Disable CloudFront caching
for APIs, login, webhook delivery and authenticated responses. Forward necessary
cookies, query strings, methods, Origin and authentication/idempotency headers.
Allow POST webhooks without browser Origin requirements on the signature-checked
webhook route. Preserve raw bytes end to end for signature verification.

Use separate ECS task roles for AWS access, an execution role for image/secrets
startup, and a separately authorized migration task. Use Secrets Manager for
runtime credentials and S3/KMS-scoped task permissions. Never persist cloud
access keys in application config or Terraform state.

## Existing application dependencies

Current Go entrypoints use deny-all authentication, unavailable storage and an
unconfigured Airwallex client. Node service URL and signing-secret fields are
constructor dependencies. Infrastructure deployment must include deliberate
runtime wiring for these dependencies.

S3 currently uses a static-credentials constructor. Extend it to accept AWS SDK
credential providers and use ECS task-role credentials in AWS. Keep existing
constructor/test behavior compatible. Do not deploy static IAM keys.

The onboarding plan retains unresolved US platform contract requirements and
submit revision policy. Resolve them explicitly before claiming operational
sandbox onboarding. HTTP submit is currently blocked. RFI response acceptance
also requires verified confirmation behavior. A healthy container or readable
Swagger page alone does not establish a working onboarding flow.

Keep necessary environment variable names documented in a reviewed configuration
matrix. Ask Ramil before introducing uncertain configuration controls. Local
variables belong in ignored `.env` with brief comments. Compose uses `env_file`.
ECS references managed secrets and task configuration rather than embedding a
local `.env` file in images. Terraform owns secret references, not secret
values.

## Implementation tasks

### Task 1: Establish AWS and GitHub access

**Files**

- Create `docs/infra/account-access.md`.

- [ ] Verify account access, root MFA, existing organization state and billing
      access. Plan IAM Identity Center organization setup explicitly if needed.
- [ ] Create/assign dedicated deployment identity and temporary session access.
      Configure a named local CLI profile and default region `us-west-2`.
- [ ] Verify caller account/role with STS without printing credentials.
- [ ] Check repository visibility and GitHub plan eligibility for required
      environment reviewers. If unavailable, obtain agreement on a manual
      workflow-dispatch approval mechanism before promising a reviewer gate.
- [ ] Document least-privilege bootstrap and deployment permissions, plus a
      tested access verification command. Do not use root for routine releases.

### Task 2: Finalize cost, runtime contracts and configuration

**Files**

- Create `docs/infra/demo-costs.md`.
- Create `docs/infra/runtime-configuration.md`.
- Reference existing onboarding backlog and implementation plan.

- [ ] Estimate 730-hour Fargate CPU/memory, ALB capacity, NAT/data/IPv4,
      RDS/storage,
      backups, CloudFront, S3, ECR, logs, secrets and KMS in Oregon.
- [ ] Confirm initial task/database sizes and budget alert amount with Ramil.
- [ ] Inventory every runtime secret/config dependency and its existing source.
      Propose only necessary new settings and get required user decisions.
- [ ] Resolve deferred submit policy and verify selected Airwallex API version,
      US fields/documents, RFI acceptance and webhook configuration contracts.
- [ ] Test the agreed settings and sandbox contract using fixtures. Record
      external checks still pending rather than marking them passed.

### Task 3: Bootstrap Terraform state and foundational AWS resources

**Files**

- Create `infra/bootstrap/`, `infra/environments/demo/` and focused modules.

- [ ] Pin Terraform/provider versions and commit provider lock files.
- [ ] Bootstrap versioned encrypted private state bucket, native S3 state
      locking
      and scoped state access. Keep state and secret payloads out of Git.
- [ ] Define consistent names/tags, ECR repositories, GitHub OIDC
      provider/roles,
      budgets and bounded log retention.
- [x] Separate bootstrap state lifecycle from demo application teardown.
- [ ] Run format, validation and reviewed Terraform plans. Verify account/region
      and expected cost before any authorized apply.

### Task 4: Define networking and managed data services

**Files**

- Create modules under `infra/modules/` and demo environment configuration.

- [x] Define VPC/subnets/routes, single demo NAT, security groups, private ALB
      and ECS service discovery. Check CloudFront VPC-origin prerequisites.
- [x] Define private Single-AZ PostgreSQL with encryption, backups and explicit
      snapshot/deletion policy. Use a supported PostgreSQL version compatible
      with existing migrations. Restrict inbound access to task roles/groups.
- [x] Define separate private frontend/document buckets, encrypted documents,
      public-access blocks and least-privilege ECS task policies.
- [x] Define Secrets Manager containers/references without embedding values in
      Terraform files or state. Provision secret values through secure steps.
- [ ] Verify Terraform plans and IAM/network isolation with focused checks.

### Task 5: Wire application runtime dependencies

**Files**

- Modify existing Node and Go configuration/entrypoints and S3 adapter as
  needed.
- Document decisions in `docs/infra/runtime-configuration.md`.

- [x] Wire Node upstream/private Go URL, service signing key and forwarder.
      Establish correct CloudFront allowed browser origin and operator identity.
- [ ] Wire Go JWT verification, S3 bucket/KMS task-role credentials, Airwallex
      sandbox clients and required platform rule validation.
- [x] Keep sandbox versus production endpoints explicit and reject mismatches.
- [ ] Implement approved submit request behavior and verified RFI confirmation.
      Avoid introducing a configuration bypass for unresolved platform rules.
- [x] Test actual entrypoints and cross-language identity/origin/body handling,
      preserving crash recovery, immutable submissions and service boundaries.

### Task 6: Define services, migrations and synthetic seeds

**Files**

- Create ECS modules and release/seed scripts under `infra/`.
- Reuse application Dockerfiles and migration binaries.

- [x] Define independent Node/API/worker services with immutable image digests,
      health checks, private networking, task roles and graceful shutdown.
- [x] Build images for the explicit ECS CPU architecture. Do not deploy an
      Apple Silicon image to an incompatible task architecture.
- [x] Define one-off Node and Go migration tasks with privileged DB access.
      Runtime services use separate least-privilege credentials.
- [x] Provide idempotent synthetic seed data. Distinguish fictional aliases from
      actual sandbox connected-account identifiers.
- [x] Test deployment health and seed repeatability against isolated test data.

### Task 7: Configure HTTPS frontend and gateway delivery

**Files**

- Create CloudFront/S3/ALB configuration under `infra/modules/`.

- [ ] Serve frontend through private S3 and AWS-generated CloudFront
      certificate.
- [ ] Configure private ALB origin and explicit API/docs behaviors with required
      methods, body limits, headers/cookies and no authenticated-response cache.
- [ ] Keep browser Origin consistent, preserve Secure session cookies and serve
      API docs at `/docs/api` with their local assets and both specifications.
- [ ] Test browser login, SPA navigation, API errors, multipart uploads and raw
      signature-checked webhook delivery through the actual HTTPS endpoint.
- [ ] Verify workers/database/Go API are private and origin access is
      restricted.

### Task 8: Implement approved GitHub Actions deployment

**Files**

- Create thin `.github/workflows/aws-demo.yml`.
- Create release scripts and runbook under `infra/scripts/` and `docs/infra/`.

- [ ] Run source tests, generated OpenAPI freshness and build checks before
      release. Address or explicitly account for existing test-baseline failures
      without blanket ignoring future failures.
- [ ] Authenticate GitHub to AWS using OIDC constrained to repository,
      environment and approved branch. Configure the supported approval gate.
- [ ] Build/publish images with commit tags and deploy immutable digests.
      Show Terraform plan and image/frontend release metadata for approval.
- [ ] Run migrations once before compatible service rollout, wait for healthy
      services, publish frontend and invalidate only necessary cached paths.
- [ ] Serialize releases. Roll back app images/frontend together when needed.
      Do not promise automatic database rollback after irreversible migrations.
- [ ] Test approval enforcement and a failed deployment/recovery path.

### Task 9: Perform sandbox acceptance and operational handoff

**Files**

- Create `docs/infra/demo-operations.md` and `docs/infra/demo-acceptance.md`.

- [ ] Verify shared-hostname frontend, operator login, Swagger docs and actual
      company CRUD/verify/submit through Node-to-Go authenticated requests.
- [ ] Exercise successful sandbox onboarding, definitive refusal, text RFI and
      uncertain result hold/reconciliation. Record actual platform evidence.
- [ ] Verify initial/RFI snapshots, durable webhook delivery, missed-event
      recovery and restart behavior without duplicate connected accounts.
- [ ] Verify alarms/log retention, cost alerts, least-privilege runtime database
      access, backup restore steps and document encryption/access.
- [ ] Record URL, release digests, access instructions, safe reset, secret
      rotation and teardown commands with resource-retention consequences.

## Post-completion and authorization

This plan creates no AWS resources. Bootstrap account changes, provisioned
infrastructure, spending, deployment and sandbox account mutations must follow
explicit authorization during execution. The AWS CLI being installed does not
establish account identity or permissions.

Infrastructure is ready for demo use only after runtime integrations and the
sandbox acceptance checks succeed. Keep any remaining application gates visible
in their canonical plan/backlog until actually resolved.

## Sources

- [ECS services](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs_services.html)
- [CloudFront private VPC origins](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-vpc-origins.html)
- [CloudFront default HTTPS certificate](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/DownloadDistValuesGeneral.html)
- [IAM human access guidance](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_root-user.html)
- [CLI SSO setup](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sso.html)
- [GitHub deployment environments](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments)
- [Terraform S3 backend and locking](https://developer.hashicorp.com/terraform/language/backend/s3)
- [Fargate pricing](https://aws.amazon.com/fargate/pricing/)
- [RDS PostgreSQL pricing](https://aws.amazon.com/rds/postgresql/pricing/)
- [ALB pricing](https://aws.amazon.com/elasticloadbalancing/pricing/)
- [NAT pricing](https://docs.aws.amazon.com/vpc/latest/userguide/nat-gateway-pricing.html)
