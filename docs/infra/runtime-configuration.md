# Runtime Configuration

This document inventories the runtime configuration, secrets, service-to-service
authentication contracts, and external platform dependencies for FlowCard's AWS
demonstration deployment. It establishes concrete mappings to AWS Secrets
Manager and environment variables, records deferred application gates, and
identifies open decisions requiring human authorization.

[TOC]

## Configuration principles

The demonstration deployment adheres to strict operational and security
boundaries:

- **No credential exposure in Git**: No credentials, secrets, or API keys are
  stored in source code, tracked files, Docker images, or Terraform state.
- **Fail-closed defaults**: Unconfigured services fail closed immediately,
  returning safe `503 Service Unavailable` or `401 Unauthorized` without
  leaking internal stack traces, database schema, or unauthenticated execution.
- **Dedicated role separation**: Compute services run under dedicated
  least-privilege ECS task roles. Node gateway has zero AWS IAM privileges (no
  AWS SDK credentials required), while maintaining PostgreSQL connectivity
  for ledger operations, webhook receipt, and durable forwarding; Go API has
  scoped S3/KMS write and read access; Go worker reads S3/KMS using an
  independent task role with no write or queue privileges.
- **Secrets Manager integration**: Sensitive runtime credentials are provisioned
  directly into AWS Secrets Manager and referenced by ECS container definition
  secrets blocks at task startup. Non-sensitive configuration is supplied via
  container environment variables. The deployment manages 6 total secrets:
  1. `rds!db-...`: RDS-managed master password secret generated automatically
     by Secrets Manager (`manage_master_user_password = true`). This is a
     bootstrap administrative credential reserved for initial DBA setup and
     is never granted to runtime tasks or migration tasks.
  2. `flowcard/db/migrate`: privileged migration database credentials (DDL),
     resolved strictly by the migration task execution role.
  3. `flowcard/db/app`: runtime application database credentials (DML),
     resolved by Node Gateway, Go API, and Go Worker execution roles.
     Per-service `node_url` and `onboarding_url` separation in this JSON
     container is an **explicit required design**: Node Gateway connects via
     `node_url` (scoped to Node ledger and webhook inbox tables; Node cannot
     use the onboarding-only DB role), while Go API and Worker connect via
     `onboarding_url` (scoped strictly to company onboarding tables). While
     execution roles access the shared container at the IAM level, actual ECS
     container definitions inject only the service-specific JSON key into
     `DATABASE_URL`, never exposing the full secret container.
  4. `flowcard/airwallex`: Airwallex sandbox credentials & webhook secret.
  5. `flowcard/operator`: Kit 5 operator session login credential.
  6. `flowcard/service-auth`: Node-to-Go service JWT signing secret.
- **No unapproved environment variables**: New configuration controls are
  introduced only where strictly required for production container operation,
  distinguishing existing codebase environment variables from proposed new
  runtime settings.

## Service configuration matrix

Ramil approved the four onboarding configuration names, and the standard Node
and Go loaders now read them. The demo rollout inputs enable services and the
onboarding integration. The November 12 operator checkpoint reports that
runtime values were provisioned and corrected images deployed; this offline
change did not independently inspect AWS. Submission revision and platform
contract gates remain unresolved.

The matrix below inventories configuration parameters across the Node Gateway,
Go Onboarding API, Go Onboarding Worker, and Database Migration tasks. It
distinguishes between existing variables and new proposals requiring user
authorization:

| Parameter | Type / Target | Status | In-Repo Source | AWS Deployment Target | Purpose |
|---|---|---|---|---|---|
| `NODE_ENV` | Env / Node | Existing | `server/src/config.ts` | ECS Task Definition | Node environment (`production`) |
| `PORT` | Env / Node | Existing | `server/src/config.ts` | ECS Task Definition | Node HTTP port (`3000`) |
| `DATABASE_URL` | Secret / Node | Existing | `server/src/config.ts` | Secrets Manager (`flowcard/db/app`) | Node runtime PostgreSQL connection (DML) |
| `AIRWALLEX_CLIENT_ID` | Secret / Node | Existing | `server/src/config.ts` | Secrets Manager (`flowcard/airwallex`) | Airwallex sandbox client identifier |
| `AIRWALLEX_API_KEY` | Secret / Node | Existing | `server/src/config.ts` | Secrets Manager (`flowcard/airwallex`) | Airwallex sandbox API credential |
| `AIRWALLEX_WEBHOOK_SECRET` | Secret / Node | Existing | `server/src/config.ts` | Secrets Manager (`flowcard/airwallex`) | Airwallex webhook HMAC verification secret |
| `KIT5_OPERATOR_TOKEN` | Secret / Node | Existing | `server/src/config.ts` | Secrets Manager (`flowcard/operator`) | Kit 5 operator session login credential |
| `KIT5_OPERATOR_ID` | Env / Node | Existing | `server/src/config.ts` | ECS Task Definition | Operator identity string for session binding |
| `KIT5_ALLOWED_ORIGINS` | Env / Node | Existing | `server/src/config.ts` | ECS Task Definition | CloudFront HTTPS origin URL for CORS |
| `REMOTE_AUTH_REPAYMENT_TERM` | Env / Node | Existing | `server/src/config.ts` | ECS Task Definition | Repayment authorization term (`asap`) |
| `SALESFORCE_SNAPSHOT_MODE` | Env / Node | Existing | `server/src/config.ts` | ECS Task Definition | Snapshot provider mode (`demo`) |
| `ONBOARDING_SERVICE_URL` | Env / Node | **Approved** | `server/src/config.ts` | ECS Task Definition | Upstream Go API endpoint (Service Discovery) |
| `ONBOARDING_SERVICE_SECRET` | Secret / Node & Go | **Approved** | Node and Go configuration loaders | Secrets Manager (`flowcard/service-auth`) | Shared HMAC secret for service-to-service JWTs |
| `PORT` | Env / Go API | Existing | `services/config.go` | ECS Task Definition | Go API HTTP port (`8080`) |
| `DATABASE_URL` | Secret / Go API & Worker | Existing | `services/config.go` | Secrets Manager (`flowcard/db/app`) | Go runtime PostgreSQL connection (DML) |
| `DATABASE_URL` (Migration) | Secret / Migration Task | Existing | `cmd/migrate/main.go` | Secrets Manager (`flowcard/db/migrate`) | Privileged PostgreSQL connection (DDL) injected into standard `DATABASE_URL` |
| `AIRWALLEX_BASE_URL` | Env / Go Worker | Existing | `services/config.go` | ECS Task Definition | Airwallex sandbox base URL |
| `AIRWALLEX_CLIENT_ID` | Secret / Go Worker | Existing | `services/config.go` | Secrets Manager (`flowcard/airwallex`) | Go worker Airwallex sandbox client ID |
| `AIRWALLEX_API_KEY` | Secret / Go Worker | Existing | `services/config.go` | Secrets Manager (`flowcard/airwallex`) | Go worker Airwallex sandbox API key |
| `AWS_REGION` | Env / Go API & Worker | Existing | `services/config.go` | ECS Task Definition | AWS Region (`us-west-2`) for SDK clients |
| `S3_BUCKET_DOCUMENTS` | Env / Go API & Worker | **Approved** | Go configuration loader | ECS Task Definition | Private onboarding documents S3 bucket name |
| `S3_KMS_KEY_ID` | Env / Go API & Worker | **Approved** | Go configuration loader | ECS Task Definition | KMS Key ARN/ID for SSE-KMS document encryption |

> [!NOTE]
> Legacy Kit 5 demo settings (`KIT5_NOVA_GLOBAL_ACCOUNT_ID`,
> `KIT5_NOVA_GLOBAL_ACCOUNT_CONNECTED_ACCOUNT_ID`, and
> `KIT5_NOVA_GLOBAL_ACCOUNT_CURRENCY`) remain optional in `server/src/config.ts`
> and are not required for Company Onboarding API operations.

## Service-to-service authentication contract

FlowCard uses a secure reverse-proxy architecture where the Node Gateway fronts
the private Go Onboarding API:

1. **Client to Node Gateway**:
   - Web browser communicates over HTTPS with CloudFront.
   - CloudFront routes `/api/*`, `/docs/api*`, and `/webhooks/airwallex` to the
     private Node ALB origin using explicit uncached behaviors, never
     applying Single Page Application (SPA) fallback rewrites to API, docs, or
     webhook endpoints.
   - Operator requests authenticate exclusively via the `kit5_operator_session`
     HTTP-only cookie established after logging in with `KIT5_OPERATOR_TOKEN`
     via `POST /api/kit5/session`. Raw bearer token bypass on general operator
     routes is not permitted by the gateway.
   - Public documentation routes (`/docs/api`, `/docs/api/`, `/docs/api/*`) are
     unauthenticated; Node strips client cookies and authorization headers
     before upstream forwarding.
2. **Node Gateway to Go Onboarding API**:
   - The Go API runs in a private ECS subnet with no public ingress.
   - For operator business routes, Node generates a short-lived service JWT
     assertion adhering strictly to `server/src/http/onboarding-proxy-auth.ts`:
     - Issuer (`iss`): `flowcard-node-proxy` (`DEFAULT_ISSUER`)
     - Audience (`aud`): `flowcard-onboarding-service`
       (`DEFAULT_OPERATOR_AUDIENCE`)
     - Subject (`sub`): operator session identity (`kit5OperatorId` or
       authenticated actor)
     - Expiration: maximum 30 seconds (`DEFAULT_MAX_TTL_SECONDS = 30`)
     - Signature: HMAC-SHA256 using `ONBOARDING_SERVICE_SECRET`.
   - For webhook forwarding, Node uses audience `flowcard-onboarding-webhooks`
     (`DEFAULT_WEBHOOK_AUDIENCE`).
   - For documentation routes, Node proxies raw HTTP streams without minting or
     sending service assertions. The Go documentation handler operates outside
     authentication and database checks.
3. **Runtime Upstream Dependency Wiring**:
   - In `server/src/config.ts`, `parseOnboardingProxyConfig` provides strict
     offline validation for upstream URL, protocol, credential stripping, and
     shared secret byte length ($\ge 32$ bytes).
   - The four environment names are approved and standard loaders validate
     them through the existing parsers. When values are absent, the proxy and
     document storage fail closed. Runtime values are managed outside this
     repository; the reported live checkpoint was not independently inspected
     in this offline task.
   - In Go, `storage.ResolveRuntimeCredentials` resolves AWS credentials from
     standard ECS task container endpoints
     (`AWS_CONTAINER_CREDENTIALS_RELATIVE_URI`) or static environment
     variables. If credentials are missing, runtime factories fail closed with
     `ErrStorageUnconfigured`.
   - Node process lifecycle implements bounded, idempotent graceful shutdown
     in `server/src/lifecycle.ts`, closing the HTTP server, clearing interval
     timers, stopping workers and forwarders, and draining the database pool
     with a 10-second forced termination guard.

## Inbound webhook routes and contracts

Inbound webhooks follow distinct routing and signature verification paths:

1. **Airwallex Ledger Webhooks**:
   - Received by Node Gateway at `POST /webhooks/airwallex`.
   - Verified using HMAC-SHA256 signature against `AIRWALLEX_WEBHOOK_SECRET`.
   - Enqueued into the PostgreSQL outbox ledger table for asynchronous
     processing by `WebhookInboxWorker`.
2. **Onboarding Platform Webhooks**:
   - Received by Go Onboarding API at `POST /api/webhooks/airwallex` and
     `POST /api/v1/webhooks/airwallex`.
   - Protected by `whAuth` requiring a service JWT with audience
     `flowcard-onboarding-webhooks`.
   - Forwarded by Node Gateway when configured, or delivered directly within
     the private network.

## Object storage and KMS encryption contracts

Onboarding documents (business registration certificates, proof of identity,
articles of incorporation) are stored in a dedicated private S3 bucket:

1. **Bucket Configuration**:
   - Encryption: Mandatory server-side encryption with customer-managed KMS key
     (`aws:kms`).
   - Access: Block All Public Access enabled. S3 bucket policy enforces HTTPS
     in transit strictly using the `aws:SecureTransport` boolean condition
     (denying all plaintext HTTP requests; note that this enforces HTTPS
     transport, while TLS 1.2+ minimum negotiation is enforced by AWS S3
     service endpoints).
   - Versioning: S3 bucket versioning is permanently enabled. All document
     revisions are preserved indefinitely for underwriting audit and regulatory
     compliance.
   - Lifecycle Rules: Configured strictly to clean up expired object delete
     markers (`expired_object_delete_marker = true`). This removes tombstone
     metadata when all historical versions of an object have been archived; it
     does not expire, delete, or prune noncurrent document versions.
2. **Task Role & Container Credential Resolution (Task 5)**:
   - Go S3 storage adapter
     (`services/company-onboarding/internal/storage/s3.go`) supports ECS task
     role credentials via `ResolveRuntimeCredentials`.
   - Resolves credentials via standard ECS relative URI
     (`AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` with fixed link-local host
     `169.254.170.2`), full URI security restrictions (enforces HTTPS or plain
     HTTP restricted strictly to loopback/link-local, no userinfo), token file
     authorization (`AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`), and static
     environment fallback (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
     `AWS_SESSION_TOKEN`).
   - Redirects are strictly refused to prevent credential exfiltration. If
     credentials are missing or token file is unreadable, factories fail closed
     with `ErrStorageUnconfigured` without leaking endpoints or tokens.

## Airwallex platform sandbox contract

Integration with Airwallex sandbox platform APIs is maintained across Node and
Go components:

1. **Node Airwallex Integration**:
   - Handles operator authentication, bridge ledger projections, and inbound
     webhook signature verification (`POST /webhooks/airwallex`).
   - Uses `AIRWALLEX_ISSUING_API_VERSION = "2026-08-21"` for issuing
     simulations.
2. **Go Onboarding Platform Client**:
   - Implements `PlatformClient` interface (`CreateAccount`, `UploadFile`,
     `UpdateAccount`, `SubmitAccount`, `GetAccount`, `GetRFI`, `RespondRFI`).
   - Uses `https://api.sandbox.airwallex.com` with API Key login token
     exchange.
   - In sandbox mode, calls are made through the VPC NAT Gateway with strict
     timeout limits and non-idempotent timeout detection
     (`ErrAmbiguousRemoteOutcome`).

## Deferred platform gates and unresolved contracts

The demo deployment must accurately reflect existing application realities
rather than presenting an idealized flow. The following application gates
remain unresolved and deferred:

1. **Onboarding Submission Revisions Gate**:
   - **Status**: Deferred by Ramil in
     `docs/backlog/onboarding-verification-submission-revisions.md`.
   - **Reality**: `POST /api/onboarding-applications/{id}/submit` currently
     fails closed with `422 Unprocessable Entity`
     (`SUBMISSION_CONTRACT_BLOCKED`).
   - **Requirement**: No deployment or infrastructure configuration can bypass
     this gate. Submission remains blocked until Ramil approves a formal
     revision conflict policy.
2. **Platform Rule Validation**:
   - **Status**: Production platform rules and US-specific fields/documents
     remain unverified against live Airwallex sandbox requirements.
   - **Reality**: Onboarding API currently injects
     `UnverifiedPlatformContractGate`, which marks platform checks as
     pending/unverified.
3. **Text RFI Scope and Confirmation**:
   - **Status**: Automated RFI responses in the onboarding domain model are
     strictly limited to **text inquiries** (`RFIQuestion` type `TEXT`).
     Documentary RFIs are not automated and require manual handling.
   - **Reality**: RFI resolution transitions require verified Airwallex sandbox
     webhook testing in Task 9.
4. **PostgreSQL 17 Minor Version Availability Gate**:
   - **Status**: Pending regional live verification.
   - **Reality**: Engine version is parameterized with default `17.4` and
     `postgres17` parameter group. Because specific minor engine version
     availability can vary across AWS regions and Availability Zones, live
     verification via `aws rds describe-db-engine-versions` is required before
     `terraform apply`.
   - **Requirement**: Verify `17.4` availability in `us-west-2` during live
     setup, or pin an available 17.x minor version supported across all
     selected AZs.

## Open configuration decisions for lead and user authorization

The following specific settings require human authorization before
provisioning:

1. **Budget Alert Amount**:
   - Proposed: **$150.00 / month**.
   - Requires Ramil's explicit confirmation before provisioning the budget alert
     resource in Task 3.
2. **Initial Task Sizing**:
   - Proposed: 0.25 vCPU, 0.5 GB RAM per container (Node Gateway, Go API, Go
     Worker).
   - Requires confirmation based on anticipated demonstration concurrency.
3. **Database Instance Class**:
   - Proposed: `db.t4g.micro` (PostgreSQL 17, Single-AZ, 20 GB gp3).
   - Requires confirmation for demo workload suitability.
4. **Secrets Provisioning Strategy**:
   - Proposed: Terraform creates empty Secrets Manager containers with
     lifecycle protection; operator provisions real sandbox credentials
     out-of-band via AWS CLI to prevent secret exposure in Terraform state.
5. **Runtime Environment Names**:
   - Ramil approved `ONBOARDING_SERVICE_URL`, `ONBOARDING_SERVICE_SECRET`,
     `S3_BUCKET_DOCUMENTS`, and `S3_KMS_KEY_ID`. Their standard loaders are
     active. Runtime values remain outside Git and Terraform state; the
     operator-reported rollout is not independent AWS verification.

## See also

- [AWS ECS Task Definition parameters and secrets](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html)
- [AWS Systems Manager and Secrets Manager integration with ECS](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/specifying-sensitive-data.html)
- [AWS SDK for Go v2 credential providers](https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/#specifying-credentials)
- [Airwallex API reference](https://www.airwallex.com/docs/api)
