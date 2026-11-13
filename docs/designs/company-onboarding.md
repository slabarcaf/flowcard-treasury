# Company onboarding service

Agreed design from Ramil's November 2026 brainstorm and scenario review.
This document describes planned behavior, not implemented capabilities.

[TOC]

## Scope and boundaries

A separate Go service manages US business connected-account onboarding for
Airwallex. Sandbox and production use isolated deployments, credentials and
data. Share PostgreSQL with the existing service, using a dedicated schema,
role and migrations. Keep the current Node backend and bridge logic in place.
Node authenticates operators and forwards signed, short-lived actor assertions
to private Go endpoints. Preserve the existing configured operator identity;
do not imply individual user attribution the current login cannot establish.

Use Platform to mean Airwallex. Platform Account means a Company's connected
account, distinct from FlowCard's own Airwallex platform account. Activation
makes a Company available for explicit enrollment in the existing roster;
never automatically enroll or migrate roster ownership.

## Company aggregate

One Company represents one registered legal entity. Allow duplicates without
detection or warnings for now. Fields:

- `company_id`, `status` (`ACTIVE`, `ARCHIVED`), `revision`, audit timestamps.
- Legal name, trading name, entity type, registration country/state.
- Registration identifiers: type, value, issuing country.
- Business profile: description, industry, website, email, phone, registered
  address, optional operating address, operating countries, and `products`.
- `products` is goods/services text, maximum 500 characters. Map it to
  Airwallex `description_of_goods_or_services`. Company industry remains our
  classification; select Airwallex's category code on the application.

Addresses are value objects with line 1/2, city, state, postal code, country.
Absent operating address does not silently imply registered address.

Person is a Company-scoped child entity: `person_id`, first/middle/last names,
date of birth, nationality, email, phone, free-text `role`, identifications.
Do not include residential address or ownership on Company Person.
Identifications are child entities with stable `identification_id`, type,
issuing country, number, issued date and expiry date. One Person record
supports multiple platform roles through application-specific information.
Cross-company person matching is outside scope.

Allow incomplete drafts; validate supplied values. Legal identifier correction
is free before submission and audited with a reason afterward. A different
legal entity requires a new Company. Archive only when no applications are open.
Permanently delete only drafts that have never entered platform onboarding;
preserve submitted records and existing bridge servicing.

## Onboarding Application aggregate

Fields: `application_id`, embedded `company`, `platform`, `status`, documents,
submissions, information requests, creator, timestamps, revision and closed
time.
The embedded Company includes its internal ID and revision. While DRAFT, reads
reflect current Company details. Immediately before initial submission, persist
a deep copy in that same field; do not introduce a separate snapshot field.

`platform` contains `name: AIRWALLEX`, internal `platform_account_id`,
`decision` (null, `APPROVED`, `REJECTED`) and `onboarding_details`.
Reason codes remain on the platform. One open application per Company/platform.
Cancelled/rejected applications remain history; reapplication creates a new one.

Application statuses: `DRAFT`, `SUBMISSION_PENDING`, `UNDER_REVIEW`,
`INFORMATION_REQUIRED`, `COMPLETED`, `REJECTED`, `CANCELLED`.
Cancellation is permitted only before submission. Completion requires confirmed
Platform Account activation. Later suspension never changes COMPLETED history.
Mid-review suspension only rejects on authoritative terminal refusal; otherwise
keep the application open and flag it for operator review.

### Platform onboarding details

Store Airwallex industry category code, intended Airwallex products, estimated
monthly revenue (decimal string amount and currency), primary-contact Person
reference, and per-person platform roles and residential addresses.
Map Person free-text role as job title, not as a regulated platform role.
Identification-specific additions required by the platform belong here.

Store `customer_agreements` with `agreed_to_data_usage`,
`agreed_to_terms_and_conditions`, and `terms_and_conditions` containing
`agreed_at`, optional `device_data` (IP and user agent), and agreement type.
No accepting Person ID is stored. Device data represents the real acceptance
occasion; omit when unknown. Never fabricate acceptance or default consent true.
Biometrics and other conditional agreements require platform verification.

### Submission child entity

Fields: submission ID, type (`INITIAL_APPLICATION`, `INFORMATION_RESPONSE`),
status (`PENDING`, `ACCEPTED`, `FAILED`, `UNRESOLVED`), immutable Company and
onboarding-details copies, document IDs, optional information-request ID,
answers, submitting actor, creation time and acceptance time.
Transport retries reuse the same Submission and operation identities.
ACCEPTED means platform acceptance for processing, not final approval.

Definitive invalid-data refusal produces FAILED Submission and REJECTED
application; platform decision remains null. Final verification refusal leaves
the original Submission ACCEPTED, closes the application REJECTED and sets
platform decision REJECTED. Transient infrastructure failure is not rejection.

### Document child entity

Application-owned immutable ID, type, optional Person/identification references,
filename, MIME type, size, internal storage key, uploading actor and time.
Replacement creates a new record; old submissions retain original references.
Store encrypted private object bytes outside PostgreSQL; authenticated streaming
access only. Initially accept PDF/JPEG/PNG up to 10 MiB as a local policy.
Platform-specific document constraints may be stricter. Validate actual content
format; format checks do not establish authenticity. Keep Airwallex upload IDs
in integration records, separate from immutable local document metadata.

### Information Request child entity

Fields: internal ID, platform reference, status, questions, received/resolved
times. Questions contain ID, text and optional Person reference. Answers are
text only and live in linked Submissions; no response-type field is needed.
Statuses: `OPEN`, `RESPONSE_PENDING`, `RESPONDED`, `RESOLVED`.

Keep application INFORMATION_REQUIRED while operator action or unresolved
response delivery remains. Accepted response marks the request RESPONDED;
platform confirmation alone marks RESOLVED. Multiple requests can coexist.
Documentary RFIs need manual platform console/hosted handling until automated
support is designed. Email ingestion is unverified, not established absent.

## Platform Account aggregate

Separate aggregate with internal platform-account ID, Company ID, platform name,
external account ID, observed status, last successful sync time, timestamps and
revision. At most one account per Company/platform/environment. It survives
applications and is not owned by an application ID. A new application must not
blindly create a second account. Reapplication eligibility against an existing
account requires confirmed platform behavior.

## HTTP contract

Resource reads and create/edit operations coexist with POST action endpoints.
Company list/fetch/create/edit, archive and eligible draft deletion; application
list/fetch/create/draft edit, verify, submit and cancel; document upload/read;
information-response commands; Submission and Platform Account reads.
Use cursor pagination and Company/platform/status filters. Final route names
beyond the agreed endpoints are implementation-contract work.

`POST /api/onboarding-applications/{id}/verify` checks saved Company/application
and documents locally without mutations or platform calls. Return 200 with
`code: VALIDATION_PASSED` and application/Company revisions, or 422 with
`code: VALIDATION_FAILED` and all detected field errors. Each error contains
`field`, native validator `code`, `param`, `message`. Use JSON field names and
array indexes, such as `people[0].date_of_birth`. Never echo sensitive values.
Use go-playground/validator with one shared formatter. Domain state failures
use domain codes rather than pretending to be native field validation rules.

`POST /api/onboarding-applications/{id}/submit` requires Idempotency-Key.
Return 202 `{code: OK, application_id, submission_id}` after atomically saving
frozen data, application state, job and idempotency result. Include a Location
for fetching the Submission. Entity status comes from resource reads.
Same key/request replays the original response before state validation.
Different body with reused key: 409 IDEMPOTENCY_CONFLICT. Pending/unresolved
operation: 409 SUBMISSION_IN_PROGRESS. Missing key: 400.
Revision fields in submit requests remain provisional, explicitly deferred.

Other errors: malformed input 400, unauthenticated 401, forbidden 403, missing
resource 404, incompatible state 409. Validation errors are 422.

## Execution and recovery

Worker sequence: create connected account, save external ID/link, upload files,
update account information, submit for activation. Persist separate durable
steps. Use PostgreSQL jobs with expiring leases and short claiming transactions;
commit before network calls. Database locking does not provide remote
exactly-once execution. Ambiguous platform calls require reconciliation or
manual
hold before replay, particularly account creation. Do not invent lookup filters.

Node verifies and durably stores webhooks, then reliably forwards onboarding
events to Go's own deduplicated inbox. No cross-schema writes. Preserve history
and prevent out-of-order state regression through documented ordering or fresh
authoritative reads. Periodic reads recover missed events. Do not assume event
names or platform statuses are identical to application statuses.

## Database isolation and role privileges

The service operates in a dedicated, isolated `onboarding` schema within PostgreSQL:
- Schema migrations are owned exclusively by this service using numbered SQL files and a session-level PostgreSQL advisory lock (`pg_advisory_lock(847291)`).
- Runtime access uses a dedicated `onboarding_service` role (`NOLOGIN` grant role) with DML permissions granted only on `onboarding.*` tables.
- **Shared deployment limits and PUBLIC privileges:** In PostgreSQL, revoking permissions on a dedicated schema from `PUBLIC` does not alter permissions inherited by `onboarding_service` from `PUBLIC` at the cluster or database level (e.g. default privileges on the database or existing `public` schema objects). Global boundaries in shared clusters must be governed at the database level (`REVOKE CONNECT ON DATABASE ... FROM PUBLIC`). The service strictly limits its operations to the `onboarding` schema and makes zero writes to existing application or ledger tables.

## Validated scenarios

- Success: DRAFT -> SUBMISSION_PENDING -> UNDER_REVIEW -> COMPLETED; initial
  Submission PENDING -> ACCEPTED; confirmed ACTIVE sets decision APPROVED.
- Invalid data: definitive platform refusal closes application REJECTED with
  FAILED Submission and null decision. New application requires review again.
- Verification rejection: ACCEPTED Submission remains; application and decision
  become REJECTED. Completed applications never regress.
- RFI: create OPEN request and INFORMATION_REQUIRED application. Response
  freezes
  new Company/onboarding copies; request RESPONSE_PENDING -> RESPONDED when
  accepted. Return UNDER_REVIEW when operator action is no longer required;
  RESOLVED requires platform confirmation. Original snapshots remain unchanged.
- Uncertainty: Submission UNRESOLVED blocks another send. Initial application
  remains SUBMISSION_PENDING; an RFI response keeps INFORMATION_REQUIRED.
  Reconcile confirmed outcome; otherwise hold manually. Client replay returns
  original command response and IDs.

## Deferred decisions and acceptance gates

See the backlog for copying applications, post-activation updates and changes
between verification and submission. Do not silently decide these in code.

### Platform contract specifications (Task 1 resolution)

- **API version & endpoints:** Target version is provisional until verified in
  Airwallex sandbox. Existing FlowCard integration uses `2026-08-21` for issuing
  simulations (`AIRWALLEX_ISSUING_API_VERSION`); do not pin an unsupported date
  without cited version-specific evidence. All calls target base path `/api/v1/`.
  - Account creation: `POST /api/v1/accounts/create` returns account ID with status `CREATED`.
  - File upload: `POST /api/v1/files/upload` on `files.airwallex.com` (max 20 MB provider limit, local policy 10 MiB) returns `file_id`.
  - Submission: `POST /api/v1/accounts/{id}/submit` transitions status to `SUBMITTED`.
- **Consent payload contract:** Verified structure under `customer_agreements`:
  `agreed_to_terms_and_conditions` (bool), `agreed_to_data_usage` (bool), and
  `terms_and_conditions` containing `agreed_at` (ISO timestamp), optional `device_data` (`ip_address`, `user_agent`), and `service_agreement_type: "FULL"`.
  No accepting Person ID exists in the platform schema and is intentionally omitted locally.
- **Webhook event catalog:**
  - `account.submitted`: Account details received for onboarding review.
  - `account.active`: Account active and verified; marks application `COMPLETED`.
  - `account.action_required`: Further action required on the account (such as missing information, terms acceptance, or RFI); not an automatic 1:1 mapping to RFI.
  - `account.suspended`: Verification hold or suspension. A `COMPLETED` application is never rewritten. Mid-review suspension closes as `REJECTED` only on explicit terminal refusal; otherwise remains open with an operator alert.
  - `account.dormant` & `account.closed`: Documented in Airwallex docs as available from version `2026-08-21`; retained as provisional/unresolved until target version is confirmed.
  - Webhook payloads use camelCase envelope keys (`accountId`, `name`, `data.accountDetails`)
    matching the Get account by ID reference structure. This represents documented
    reference structure; inner `accountDetails` and status payload remain provisional
    until verified against recorded webhook deliveries.

### Unresolved external gates

1. **Exact US legal structures and regulated roles:** Detailed entity type enums and FinCEN control/ownership role mappings remain gated on unavailable platform documentation evidence (e.g. via Airwallex Docs MCP or API schema inspection). Free-text roles are preserved locally without fabricated mappings.
2. **Country-specific document requirement matrix:** Exact mandatory document sets per legal structure remain gated on unavailable platform documentation evidence. Local binary magic-byte sniffing validates PDF/JPEG/PNG container format, but cannot establish document authenticity.
3. **Documentary RFI channels:** Email support is unverified (neither established absent nor confirmed). Follow-up document RFIs require manual handling in the Airwallex Console or Hosted UI. No automated document RFI API is assumed.
4. **Uncertain-create recovery:** Lookup not verified in this investigation; manual hold until verified. Any timeout or network partition during `accounts/create` places the operation into a manual hold; blind creation replay is strictly prohibited.
5. **Deferred submit revisions:** Policy for changes between operator verification and submission remains deferred in `docs/backlog/onboarding-verification-submission-revisions.md` and blocks the final submit request revision contract. Test fixtures in `services/company-onboarding/internal/platform/airwallex/testdata/` use synthetic placeholders with heuristic checks and provenance tracked in `testdata/PROVENANCE.md`.

## Packaging, Containerization, and Runtime Architecture (Task 10)

### Multi-Stage Non-Root Docker Image

The Go onboarding subsystem is packaged in `services/company-onboarding/Dockerfile` using a reproducible multi-stage build:
1. **Builder Stage (`golang:1.27.3-alpine3.24`)**:
   - Compiles statically linked, stripped binaries with `CGO_ENABLED=0` and `-ldflags="-s -w"` for `/bin/api` (`cmd/api`), `/bin/worker` (`cmd/worker`), and `/bin/migrate` (`cmd/migrate`).
   - Uses layer caching for `go.mod` and `go.sum` dependencies.
2. **Runtime Stage (`alpine:3.21`)**:
   - Installs runtime SSL CA certificates (`apk add --no-cache ca-certificates`) as root.
   - Creates an unprivileged non-root user and group `app:app` (UID/GID 10001) without shell access (`/sbin/nologin`).
   - Copies compiled binaries `/bin/api`, `/bin/worker`, and `/bin/migrate` with ownership `10001:10001`.
   - Runs strictly as `USER 10001:10001`.
   - Defaults to `CMD ["/bin/api"]` without `ENTRYPOINT`, allowing Compose commands (`/bin/api -port 8080` and `/bin/worker`) to cleanly specify process execution.

### Docker Compose Architecture (`compose.yaml`)

Both services are integrated into `compose.yaml`:
- **`onboarding-api`**:
  - Service container running `command: ["/bin/api", "-port", "8080"]`.
  - Maps port `8080:8080`.
  - Uses `env_file: .env` (zero per-variable mappings; adheres to repository environment rules).
  - Depends on `database` service.
- **`onboarding-worker`**:
  - Service container running `command: ["/bin/worker"]`.
  - Uses `env_file: .env`.
  - Depends on `database` service.

### Shared Environment & PORT Collision Resolution

The shared root `.env` configures `PORT=3000` for the Node.js application server. To prevent port collision between Node and Go without inventing new environment variables or modifying the shared `.env`, the Go service introduces command-line flag `-port`:
- If `-port` flag is provided, it takes precedence.
- If omitted, the service falls back to `PORT` environment variable, or default `8080`.
- In Compose, `command: ["/bin/api", "-port", "8080"]` explicitly binds to port 8080.

### Honest Runtime Wiring, Least-Privilege & Fail-Closed Safeguards

The entry points `cmd/api/main.go`, `cmd/worker/main.go`, and `cmd/migrate/main.go` implement genuine production wiring adhering to least-privilege boundaries:
- **Separation of Privileged DDL**: In accordance with least-privilege database role boundaries, runtime services (`cmd/api` and `cmd/worker`) do **NOT** execute schema migrations or DDL. Schema migrations are applied exclusively via the dedicated `cmd/migrate` tool using an administrative connection.
- **Schema Readiness Verification**: On startup, both `cmd/api` and `cmd/worker` invoke `postgres.VerifySchemaReadiness` to verify connectivity and assert that all 6 required migrations are present in `onboarding.schema_migrations` and required tables exist. If the database is unreachable or schema is missing, startup fails immediately.
- **Listener Leak Prevention**: In `cmd/api`, configuration and router building occur *before* opening the TCP socket listener. If database or configuration validation fails, no network listener is leaked.
- **Credential Sanitization**: Connection strings, passwords, and DSNs are scrubbed from error messages across both entrypoints via `postgres.SanitizeDBError`. Production `cmd/api` contains no secret-holding command line flags or unapproved environment variables.
- **Fail-Closed API Fallback**: When the database is unconfigured (`DATABASE_URL` empty), `cmd/api` serves public health checks (`/healthz`, `/livez`) with HTTP 200 while private endpoints return safe HTTP 503 `DATABASE_UNAVAILABLE` without panicking.
- **Authentication**: Wires `DenyAllAuthenticator` (fails closed, default deny) unless injected.
- **Object Storage**: Wires `storage.NewUnavailableStore` (returns 503 on file operations) when unconfigured.
- **Platform Client**: Wires `airwallex.NewUnconfiguredClient` (fails closed on remote dispatch calls) when unconfigured.
- **Platform Verification Gate**: Mounts `validation.UnverifiedPlatformContractGate` which blocks submissions until live integration gates are formally approved.
- **Background Engines**: `cmd/worker` runs three concurrent loops with graceful context cancellation:
  - Submission Job Worker: leases and executes pending submission dispatches (2s ticker).
  - Webhook Inbox Processor: leases and processes received external webhooks (3s ticker).
  - Periodic Account Reconciler: autoruns polling reconciliations for pending accounts (30s ticker).

### Acceptance Verification Suite

The subsystem is validated through two distinct test suites:
1. **Cross-Language Ingress & CRUD Verification (`tests/server/onboarding-e2e-acceptance.test.ts`)**:
   - Scope: Validates Node proxy ingress, operator cookie authentication, service-to-service JWT assertion signing, route mapping, and resource CRUD against live Go HTTP and Node proxy instances. It does *not* claim to test the complete provider underwriting lifecycle.
2. **Core Acceptance Scenarios Suite (`services/company-onboarding/internal/acceptance/scenarios_acceptance_test.go`)**:
   - Executes against isolated PostgreSQL (port 54329) using advisory package locking (`pg_advisory_lock(88888888)`).
   - Validates all 5 core scenarios:
     1. Success lifecycle: Application `DRAFT` -> `SUBMISSION_PENDING` -> `UNDER_REVIEW` -> Webhook `ACTIVE` -> `COMPLETED` with approved decision.
     2. Definitive platform refusal: 4xx refusal -> Application `REJECTED`, Submission `FAILED`, null decision.
     3. Crash ambiguity hold: 5xx upstream outage -> Step state marked `UNRESOLVED`, application held in `SUBMISSION_PENDING`, subsequent worker pass makes zero duplicate create calls.
     4. RFI lifecycle & dual snapshot preservation: Application in `UNDER_REVIEW` receives RFI inquiry (`INFORMATION_REQUIRED`). Operator updates trading name and submits RFI response. Worker executes with explicit test-only gate `AllowRFIResponseAcceptance: true` (production default false) calling `RespondRFI` (and never generic `SubmitAccount`). Step marked `CONFIRMED`, RFI marked `RESPONDED`, application returned to `UNDER_REVIEW`. Validates that initial submission snapshot is preserved untouched and distinct from the RFI response submission snapshot. Account activation completes application.
     5. Lost webhook recovery: Platform marks account active but webhook dropped -> Periodic reconciler queries platform and recovers application to `COMPLETED`.

## See also

- [Implementation plan](/docs/plans/20261106-company-onboarding.md)
- [Revision policy](/docs/backlog/onboarding-verification-submission-revisions.md)
- [Application copying](/docs/backlog/copy-onboarding-application.md)
- [Post-activation updates](/docs/backlog/connected-account-post-activation-updates.md)
- [Airwallex Native API](https://www.airwallex.com/docs/connected-accounts/onboarding/kyc-and-onboarding/native-api)
- [Airwallex US requirements](https://www.airwallex.com/docs/connected-accounts/get-started/kyc-and-onboarding/native-api/business-kyc-requirements/united-states-%28us%29)
