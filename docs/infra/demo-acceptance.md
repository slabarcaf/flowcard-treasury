# FlowCard Demo Acceptance

This matrix records acceptance for the FlowCard AWS demonstration. The November
12, 2026 manual rollout checkpoint is operator-reported historical evidence.
The simplified GitHub workflow has not had its first live run; that check and
the remaining acceptance gates are still open.

[TOC]

## Status terms

- `VERIFIED_LIVE`: Supported by operator-provided live results, with the scope
  stated in the rollout checkpoint.
- `VERIFIED_OFFLINE`: Checked against source, configuration, or local fixtures.
  It does not prove that a live resource or integration works.
- `PENDING_LIVE`: Requires an authorized live check after prerequisites clear.
- `BLOCKED_APPROVAL`: Must wait for the listed decision or evidence.
- `BLOCKED_ENVIRONMENT`: A required local or CI dependency is unavailable.

## Readiness gates

### Historical manual rollout checkpoint, November 12, 2026

The frontend and Swagger UI load at
`https://d2dogzq0d9gxdl.cloudfront.net`. All three ECS services reached one running
task; the Node ALB target reported healthy. Node and Go migrations and the Node
synthetic seed task exited with code zero after the TLS and role-grant fixes.

Operator-provided HTTP results verify session creation (`200`), company creation
(`201`), reads (`200`), update (`200`), and deletion (`204`). The temporary
company was deleted. An unauthenticated company request returned `401`.

These checks record the earlier manual rollout only. They do not verify the
simplified GitHub workflow. Airwallex submission, RFI,
webhooks, document storage permissions, restore, rollback, and alert delivery
remain unverified. Existing cost alerts are user-reported; Ramil declined a new
budget resource. The first run of the simplified GitHub workflow remains
pending.

### Original offline and extended acceptance gates

Focused frontend/workflow/deployment checks and builds pass. Earlier evidence
recorded 939 strict-baseline tests with the 23 documented failures. The latest
Task 4 comparator ran 940 tests and reported those 23 plus one onboarding E2E
failure because PostgreSQL at `127.0.0.1:54329` was unavailable. The baseline
needs a rerun with that local test database available. Offline results do not
constitute live demo acceptance.

| Gate | Status | Required before acceptance |
| --- | --- | --- |
| Simplified manual workflow | `PENDING_LIVE` | Dispatch from `develop` with no form inputs; verify OIDC, release identity, deployment phases, and final manifest. |
| Task 8 deployment and security implementation | `VERIFIED_OFFLINE` | Focused workflow, IAM, release-script, and offline-safety checks. |
| AWS identity, account, MFA, and routine operator access | `BLOCKED_APPROVAL` | Owner establishes authorized access. Operator verifies STS account and region before any plan or resource action. |
| Repository deployment configuration | `PENDING_LIVE` | Verify existing Actions variables against the tracked foundation file and confirm the immutable `develop` OIDC subject before the first workflow run. |
| Foundation outputs and infrastructure | `BLOCKED_APPROVAL` | Separate infrastructure changes still require an authorized, reviewed plan and provisioning approval. The workflow reads the real tracked `infra/environments/demo/foundation-outputs.json`; it has no manual digest or approval-evidence input. |
| Budget amount and alert email | `BLOCKED_APPROVAL` | Ramil approves an amount and recipient. The current `$150/month` proposal is not approval; `enable_budget` remains false by default. |
| Runtime integration settings | `VERIFIED_OFFLINE` | Ramil approved the four names; standard loaders and focused checks pass. The November 12 operator checkpoint reports values provisioned and services deployed, but this task did not independently inspect AWS. |
| Submit and platform contract | `BLOCKED_APPROVAL` | Approve submission revision policy and verify US roles, fields, documents, API version, and platform outcomes. |
| Live CloudFront, ALB, RDS, S3, KMS, and observability | `PENDING_LIVE` | Provision and run the checks below under separate live authorization. |

## Acceptance matrix

| Area | Current status | Acceptance procedure | Evidence to retain |
| --- | --- | --- | --- |
| Shared HTTPS hostname and static frontend | `PENDING_LIVE` | Check the CloudFront endpoint, certificate, SPA navigation, and asset behavior. | UTC time, release commit, frontend checksum, endpoint hostname, response status and content type. |
| Operator login and secure session | `PENDING_LIVE` | Log in through the shared hostname; verify the HTTP-only, Secure session cookie and authenticated request. Use synthetic operator credentials. | Status codes and cookie attribute names only. Never retain cookie values or operator tokens. |
| API errors and route separation | `PENDING_LIVE` | Confirm API failures return structured JSON and never SPA HTML. | Route, status, content type, and sanitized error code. |
| Public API documentation | `PENDING_LIVE` | Load `/docs/api`, both OpenAPI specifications, and bundled Swagger assets through the HTTPS hostname. | Paths, status, content type, and spec version. A readable page alone does not establish onboarding acceptance. |
| Company CRUD and verification through Node-to-Go auth | `PENDING_LIVE` | Use a synthetic company and verify requests traverse the authenticated service boundary. | Synthetic IDs, route, status, validation codes, and release digest. No personal or real company data. |
| Submission | `BLOCKED_APPROVAL` | Current `UnverifiedPlatformContractGate` fails closed. `POST /api/onboarding-applications/{id}/submit` currently returns `422 SUBMISSION_CONTRACT_BLOCKED`. Keep this expected failure until Ramil approves a revision policy and the platform contract is verified. | Sanitized status and error code only. Do not retry or bypass the gate. |
| Successful sandbox onboarding | `BLOCKED_APPROVAL` | Do not run until submit policy, US platform requirements, credentials, and account identity are approved. Then use a designated synthetic sandbox case and record provider evidence. | Redacted provider outcome, correlation IDs, platform account ID only if approved for retention. No credentials or full payloads. |
| Definitive refusal | `BLOCKED_APPROVAL` | After platform and policy gates clear, verify a definitive refusal does not create a false connected account or completion state. | Sanitized refusal code, application state, and correlation ID. |
| Text-only RFI | `BLOCKED_APPROVAL` | Documentary RFI automation is unsupported. Text RFI acceptance requires verified confirmation behavior and webhook evidence after the platform contract is resolved. | RFI type, sanitized state transition, webhook correlation ID. No RFI message content. |
| Uncertain provider result and reconciliation | `BLOCKED_APPROVAL` | Verify uncertain outcomes are held for reconciliation and never blindly replayed. | Sanitized hold state, correlation ID, and reconciliation outcome. |
| Initial and RFI snapshots | `BLOCKED_APPROVAL` | After the submission and RFI gates clear, compare each immutable application snapshot with its approved source state across initial and RFI submission. | Synthetic IDs, snapshot versions, checksums, timestamps. No document or company contents. |
| Webhook durability and missed-event recovery | `BLOCKED_APPROVAL` | Requires verified webhook configuration and provider contract. After approval, test durable receipt, retry/recovery, and restart behavior without duplicate connected accounts. | Event IDs, delivery attempt, state transition, and sanitized logs. No signature or raw payload. |
| Database role separation and migrations | `PENDING_LIVE` | Verify runtime roles cannot perform DDL or access the master secret; migration tasks use separate credentials and report successful migration versions. | Role grants, migration IDs, task-definition digests, and exit statuses. Never capture passwords or DSNs. |
| S3 document encryption and access | `PENDING_LIVE` | Verify public access blocks, TLS-only policy, KMS encryption, API write permissions, worker read permissions, and unauthorized access denial. Use synthetic documents only. | Bucket/key configuration, test object ID, encryption metadata, and sanitized access result. |
| Backups and restore | `PENDING_LIVE` | Confirm retention and complete an authorized restore rehearsal into an isolated test target. Never overwrite or clean production/demo data as part of the rehearsal. | Snapshot ID, restore target identifier, timestamps, and validation result. No connection strings. |
| Logs, alarms, and retention | `PENDING_LIVE` | Verify required log groups, retention, service health alarms, and alert delivery to an approved recipient. | Alarm names, retention values, test notification time, recipient classification. Do not include log payloads. |
| Budget alert | `BLOCKED_APPROVAL` | Wait for approved amount and notification email, then verify configured alerts. | Approved threshold and redacted recipient; no budget resource is enabled before approval. |
| Separately authorized manual recovery | `PENDING_LIVE` | Recovery is outside normal release dispatch. Review a successful release record and matching frontend artifact, then pass explicit targets. The current manifest does not match the legacy `--manifest` schema. Verify both phases or report partial state. Do not downgrade migrations or delete persisted data. | Prior manifest status, logical artifact identity, checksums, task-definition ARNs, invalidation ID, phase outcomes. |
| URL, safe reset, rotation, and teardown | `BLOCKED_APPROVAL` | Record access instructions after a real release. No safe application-data reset command is currently verified. Teardown or deletion requires separate explicit authorization and a reviewed retention plan. | Approved run ID, release checksums, resource inventory, backup/state retention decision, and rotation record. |

## Release metadata acceptance

The workflow records one release identity from the commit SHA, run ID, and run
attempt. Artifact names and ECR tags include that attempt. A failed-jobs-only
retry that reuses previous build artifacts fails provenance; choose **Re-run
all jobs** to rebuild and deploy from the same attempt.

The workflow has two deployment phases in its local release metadata:

1. `deploy-demo.sh` records `backend_verified_frontend_pending` only after
   service health checks pass.
2. `deploy-frontend.sh` verifies the built frontend checksum, uploads hashed
   assets and other public files at their existing S3 paths, publishes
   `index.html` last, retains older objects, creates a mutable-path
   invalidation, and waits for CloudFront invalidation completion.
3. `finalize-release.sh` records `deployed_and_verified` only after the
   frontend step succeeds and account, region, commit, and checksum match.

If the frontend upload or invalidation fails, leave the manifest pending. A
pending manifest is not a successful prior release or rollback target. Never
mark a release successful based only on healthy containers or a submitted
invalidation request.

## Evidence handling

For each acceptance row, record its state, operator, UTC time, workflow run and
attempt, commit SHA, relevant image digests, frontend checksum, sanitized
result, and blocker or approval reference. Keep evidence in an access-controlled
location. Do not store tokens, cookies, signatures, passwords, DSNs, raw company
or personal data, documents, or webhook bodies in reports, logs, or artifacts.

## See also

- [`demo-operations.md`](demo-operations.md): operational procedure and current
  readiness gates.
- [`public-routing-acceptance.md`](public-routing-acceptance.md): detailed
  shared-host routing checks.
- [`runtime-configuration.md`](runtime-configuration.md): pending settings and
  platform contract gates.
- [`services-and-migrations.md`](services-and-migrations.md): service and
  database role details.
