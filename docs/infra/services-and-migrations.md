# ECS Fargate Services, Migrations, and Synthetic Seed Architecture

Run repository commands from the repository root.

This document specifies the Amazon ECS Fargate cluster architecture, container
definitions, one-off database migration tasks, least-privilege DBA role
provisioning, synthetic fixture seeding, and deployment automation for
FlowCard's demonstration deployment.

[TOC]

## Service architecture and compute specifications

FlowCard operates three independent containerized workloads on AWS ECS Fargate
within private subnets across two Availability Zones in `us-west-2`:

1. **Node Gateway Service (`flowcard-demo-node-gateway`)**:
   - Container Port: `3000` (HTTP).
   - Ingress: Private internal Application Load Balancer (`module.alb`) routing
     `/api/*`, `/docs/api*`, and `/webhooks/airwallex`.
   - Compute Allocation: 0.25 vCPU (256 CPU units), 0.5 GB RAM (512 MiB).
   - CPU Architecture: Linux `X86_64` (compatible with local Docker builds
     targeting `--platform linux/amd64`).
   - Non-root User: `node` (UID 1000).
   - Graceful Shutdown: `stopTimeout = 30` seconds. Ceases accepting connections
     synchronously via `server.close()`, drains in-flight requests, and drains
     database pool connections within a 10-second bounded deadline.
   - Container Health Check:
     `node -e "fetch('http://127.0.0.1:3000/health').then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))"`
     (interval: 15s, timeout: 5s, retries: 3, startPeriod: 10s).
   - Service Discovery: Accessible via ALB internal DNS; resolves Go API via
     Cloud Map DNS (`http://onboarding-api.flowcard.internal:8080`).

2. **Go Onboarding API Service (`flowcard-demo-go-api`)**:
   - Container Port: `8080` (HTTP).
   - Ingress: Inbound TCP 8080 restricted strictly to Node Gateway security
     group. No direct load balancer ingress.
   - Compute Allocation: 0.25 vCPU, 0.5 GB RAM.
   - CPU Architecture: Linux `X86_64`.
   - Non-root User: `app` (UID/GID 10001).
   - Temporary Storage: Dedicated world-writable `/tmp` (`1777`) for multipart
     document upload streaming.
   - Container Health Check:
     `wget -qO- http://127.0.0.1:8080/healthz || exit 1`
     (interval: 15s, timeout: 5s, retries: 3, startPeriod: 5s).
   - Service Discovery: Registered with AWS Cloud Map private DNS namespace
     `flowcard.internal` as `onboarding-api` (SRV/A records, TTL: 10s).

3. **Go Onboarding Background Worker Service (`flowcard-demo-go-worker`)**:
   - Ingress: None (`portMappings = []`).
   - Workload: Asynchronous queue processing (PostgreSQL outbox polling and
     webhook inbox processing with lease fencing).
   - Compute Allocation: 0.25 vCPU, 0.5 GB RAM.
   - CPU Architecture: Linux `X86_64`.
   - Non-root User: `app` (UID/GID 10001).
   - Outbound Connectivity: Outbound HTTPS 443 via managed NAT Gateway for
     Airwallex sandbox API communication.

## Initial infrastructure creation sequencing and pre-registration plan

To prevent broken container deployments before container images exist in ECR or
secrets are populated, the deployment follows a deterministic 4-stage lifecycle:

1. **Stage 1 (Bootstrap Foundation Apply)**:
   - Run Terraform with `enable_services = false`.
   - Provisions VPC, subnets, NAT, security groups, RDS PostgreSQL, KMS, S3
     buckets, CloudWatch log groups, ECR repositories, Secrets Manager
     containers, IAM execution/task roles, and internal ALB.
   - ECS task definitions and services are not provisioned during this stage
     because container images do not yet exist in ECR.

2. **Stage 2 (Container Image Build & Publication)**:
   - The 4 required container images (Node Gateway, Go API, Go Worker, and
     Migration runner) are built with `--platform linux/amd64`.
   - Pushed to their respective ECR repositories.
   - Immutable SHA-256 digests (`sha256:...`) are captured for each image.
     No mutable tags (e.g. `:latest`) are permitted.

3. **Stage 3 (Pre-Registration & Empty Service Provisioning)**:
   - Run Terraform with `enable_services = true`, `service_desired_count = 0`,
     and the captured image digests (`node_image_digest`, `go_api_image_digest`,
     `go_worker_image_digest`, `migration_image_digest`).
   - Provisions the initial ECS task definition families
     (`flowcard-demo-node-gateway`, `flowcard-demo-go-api`,
     `flowcard-demo-go-worker`, `flowcard-demo-node-migration`,
     `flowcard-demo-go-migration`, `flowcard-demo-synthetic-seed`).
   - Provisions the ECS services with `desired_count = 0` attached to the ALB
     target group and Cloud Map DNS without triggering early container launch.

4. **Stage 4 (Executable Deployment Automation via `deploy-demo.sh`)**:
   - The approved GitHub release workflow invokes `deploy-demo.sh` with OIDC;
     an authorized local operator may use `--profile`. Both require explicit
     confirmation, the account and region, and immutable image digests.
   - Verifies STS caller account and validates outputs (ECR URLs, subnets,
     cluster ARN).
   - Registers new task definition revisions using the exact digests.
   - Executes Node migrations task (`flowcard-demo-node-migration`) and verifies
     exit code 0 before proceeding. A failure aborts deployment and stops
     service updates immediately.
   - Executes Go migrations task (`flowcard-demo-go-migration`) and verifies
     exit code 0.
   - Optionally executes synthetic seed task (`flowcard-demo-synthetic-seed`)
     and verifies exit code 0.
   - Sequentially updates ECS services with the newly registered task definition
     ARNs and scales `desired_count` to 1.
   - Awaits service stability (`aws ecs wait services-stable`) and verifies
     running counts and active task definitions.

The four approved environment names (`ONBOARDING_SERVICE_URL`,
`ONBOARDING_SERVICE_SECRET`, `S3_BUCKET_DOCUMENTS`, `S3_KMS_KEY_ID`) are read
by the standard loaders. Demo rollout inputs set
`enable_proposed_onboarding_integration = true`. The November 12 operator
checkpoint reports values provisioned and services deployed; this document
does not independently verify AWS state. Runtime secrets stay outside Git and
Terraform state.

## Database migrations and role separation

In accordance with least-privilege security boundaries, runtime application
services do **NOT** execute DDL migrations. Migrations are executed as
isolated, one-off ECS Fargate tasks before runtime services start.

### Distinct migration task definitions

1. **Node Migration Task (`flowcard-demo-node-migration`)**:
   - Task Family: `flowcard-demo-node-migration`.
   - Container Image: `flowcard-node-gateway@${var.node_image_digest}`.
   - Command: `["node", "dist/server/src/db/migrate.js"]`.
   - Secret Injection: Standard `DATABASE_URL` resolved from Secrets Manager
     `flowcard/db/migrate` using JSON key `node_url`:
     `${module.secrets.secret_arns["flowcard/db/migrate"]}:node_url::`.
   - Behavior: Applies migrations `001_bridge_ledger.sql` through
     `021_onboarding_webhook_forwarding.sql` in numerical order, recording
     completion in `schema_migrations`. Fully idempotent.

2. **Go Migration Task (`flowcard-demo-go-migration`)**:
   - Task Family: `flowcard-demo-go-migration`.
   - Container Image: `flowcard-migration@${var.migration_image_digest}`.
   - Command: `["/bin/migrate"]`.
   - Secret Injection: Standard `DATABASE_URL` resolved from Secrets Manager
     `flowcard/db/migrate` using JSON key `onboarding_url`:
     `${module.secrets.secret_arns["flowcard/db/migrate"]}:onboarding_url::`.
   - Behavior: Applies onboarding migrations `001` through `006` in the
     `onboarding` schema. Fully idempotent.

### Secure DBA role provisioning instructions

Administrative database roles must be provisioned out-of-band by an authorized
DBA using the RDS master credential. No passwords may appear in CLI arguments,
shell history, Terraform HCL, or Terraform state files:

Create roles without putting passwords in SQL text, then use psql's interactive
password prompt for each role:

```sql
CREATE ROLE flowcard_migrate_node WITH LOGIN;
CREATE ROLE flowcard_migrate_onboarding WITH LOGIN;
CREATE ROLE flowcard_app_node WITH LOGIN;
CREATE ROLE flowcard_app_onboarding WITH LOGIN;

-- Schema permissions for migration roles (DDL):
GRANT ALL PRIVILEGES ON SCHEMA public TO flowcard_migrate_node;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO flowcard_migrate_node;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO flowcard_migrate_node;

CREATE SCHEMA IF NOT EXISTS onboarding AUTHORIZATION flowcard_migrate_onboarding;
GRANT ALL PRIVILEGES ON SCHEMA onboarding TO flowcard_migrate_onboarding;

-- Least-privilege runtime application permissions (DML only):
GRANT USAGE ON SCHEMA public TO flowcard_app_node;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO flowcard_app_node;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO flowcard_app_node;

GRANT USAGE ON SCHEMA onboarding TO flowcard_app_onboarding;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA onboarding TO flowcard_app_onboarding;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA onboarding TO flowcard_app_onboarding;
```

At the interactive `psql` prompt, run `\password <role-name>` for each role.
The password is entered at the prompt, not in the command text. After role
provisioning, create the following JSON documents with an owner-approved secure
editor or credential manager. Keep each file mode `0600`, outside the
repository, and remove it after the Secrets Manager update:

- Migration JSON keys: `node_url`, `onboarding_url`.
- Runtime JSON keys: `node_url`, `onboarding_url`.
- Values are PostgreSQL DSNs using the role-specific credentials. Do not print
  the documents or include password-bearing DSNs in shell arguments or logs.

Pass only the protected file paths to AWS CLI:

```bash
aws secretsmanager put-secret-value \
  --secret-id "flowcard/db/migrate" \
  --secret-string file:///secure/path/flowcard-db-migrate.json

aws secretsmanager put-secret-value \
  --secret-id "flowcard/db/app" \
  --secret-string file:///secure/path/flowcard-db-app.json
```

## Idempotent synthetic demonstration fixtures

Synthetic seed data is maintained under `infra/scripts/seed-synthetic-demo.sql`
and executed via `infra/scripts/seed-synthetic-demo.ts`:

- **Strict Fictional Boundaries**: All customer records (`acct_bloom001`,
  `acct_atlas001`, `acct_nova001`, `acct_pixel001`), CRM identifiers
  (`demo-sf-bloom001`), and card bindings (`card_synthetic_bloom001`) are
  strictly synthetic demonstration fixtures.
- **No Real Customer Data**: Zero production data, zero real customer PII, zero
  real business EINs, and zero real card numbers.
- **Unverified Account Link Notice**: Placeholder IDs distinguish synthetic demo
  scenarios from live Airwallex platform entities. No live platform account link
  is claimed until verified in Task 9.
- **Disposable Test Isolation**: Automated test suites targeting local databases
  enforce strict safety checks: port must be `54329`, database name must contain
  `test` (`flowcard_onboarding_test`), and execution acquires
  `pg_advisory_lock(88888888)` to ensure deterministic concurrency.
- **Repeatability**: All SQL insert statements use `ON CONFLICT DO NOTHING` or
  `ON CONFLICT DO UPDATE`. Executing the seed multiple times produces identical
  database counts without errors.
- **Facility Configuration Safety**: The seed never deactivates or updates an
  existing facility configuration. If another facility is active, the
  synthetic `usd-primary` insert is skipped by the partial unique-index
  conflict handler. A fresh configuration is inserted only when there is no
  conflicting row. PostgreSQL regression fixtures run in a transaction and
  roll back all temporary facility rows.

## Deployment automation and rollback runbooks

Scripts in `infra/scripts/` are executable and can mutate AWS resources when
run with live credentials and explicit confirmation. The following describes
their contract; it does not authorize or record a live run. See
[`demo-operations.md`](demo-operations.md) for the current rollout and metadata
finalization sequence.

1. **Deployment Execution (`infra/scripts/deploy-demo.sh`)**:
   - Requires explicit human confirmation flag (`--confirm-approval`).
   - Verifies caller AWS Account ID against authorized demo accounts.
   - Validates that image inputs are non-empty, 64-hex SHA-256 digests
     (`^sha256:[a-f0-9]{64}$`), strictly rejecting `:latest` or mutable tags.
   - Executes Node migrations task (`flowcard-demo-node-migration`) and verifies
     exit code 0 before proceeding.
   - Executes Go migrations task (`flowcard-demo-go-migration`) and verifies
     exit code 0 before proceeding.
   - Sequentially triggers rolling deployments for Go API, Go Worker, and Node
     Gateway services.
   - Awaits ECS service stability (`aws ecs wait services-stable`) and records
     `backend_verified_frontend_pending`; it does not mark the whole release
     successful.
   - `deploy-frontend.sh` uploads the verified build and waits for the exact
     CloudFront invalidation to complete. The workflow then runs
     `finalize-release.sh` to record whole-release success.

2. **Rollback Execution (`infra/scripts/rollback-demo.sh`)**:
   - Reverts ECS service task definitions to designated previous stable
     revisions.
   - Preserves all persisted database records in RDS and all document objects
     in S3.
   - Requires explicit human confirmation flag (`--confirm-approval`).
   - Note: Application rollbacks restore container revisions; irreversible
     schema DDL changes require separate manual DBA remediation.

The coupled coordinator uses finalized release metadata and a verified local
frontend build. The workflow currently does not download artifacts from a prior
run; see `demo-operations.md` for the retrieval limitation and required
checksum checks.

## Pending approval gates and open decisions

In accordance with deployment governance:

1. **4 Pending Proposed Environment Variables**:
   `ONBOARDING_SERVICE_URL`, `ONBOARDING_SERVICE_SECRET`,
   `S3_BUCKET_DOCUMENTS`, and `S3_KMS_KEY_ID` remain unapplied in standard
   loaders and ECS task definition environments until human approval is
   granted by Ramil.
2. **Deferred Application Policy Gates**:
   - `POST /api/onboarding-applications/{id}/submit` remains fail-closed with
     422 `SUBMISSION_CONTRACT_BLOCKED` (revisions policy deferred).
   - `UnverifiedPlatformContractGate` remains active.
   - Text-only RFI handling strictly enforced.
3. **Open Account & Budget Decisions**:
   - Monthly budget alert amount ($150.00).
   - RDS PostgreSQL 17.4 minor engine version availability check in `us-west-2`.
   - Initial container sizing (0.25 vCPU, 0.5 GB RAM) confirmed for demo.

## See also

- [AWS ECS Fargate Task Definition Parameters](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html)
- [AWS Cloud Map Service Discovery](https://docs.aws.amazon.com/cloud-map/latest/dg/what-is-cloud-map.html)
- [OpenTofu ECS Resource Documentation](https://search.opentofu.org/provider/hashicorp/aws/latest/docs/resources/ecs_service)
- [Airwallex API Reference](https://www.airwallex.com/docs/api)
