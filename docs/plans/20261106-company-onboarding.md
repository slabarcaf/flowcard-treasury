# Company onboarding implementation plan

Build the separate Go onboarding service described in the agreed design.
This plan is unexecuted; deferred revision policy blocks finalizing submit.

[TOC]

## Context and constraints

Use [the design](/docs/designs/company-onboarding.md) as the domain contract.
Implement from a worktree based on develop under .worktrees/. Preserve user
changes, the existing backend and roster. No deployment, platform writes,
commits or pushes are authorized by this document alone.

Use Planning Exec when execution is authorized. Repository instructions require
Agterm coding sessions instead of spawned subagents. Apply Go style when writing
Go, keep checks fixture-based, and add tests with each behavioral task.

## Progress tracking

Mark tasks complete immediately after their checks pass. Record blockers and
scope changes without silently resolving decisions parked in the backlog.

## Implementation steps

### Task 1: Record the platform contract and unresolved gates

**Files:**

- Create/modify: `docs/designs/company-onboarding.md`
- Create/modify: `services/company-onboarding/internal/platform/airwallex/testdata/`

- [ ] Use Docs MCP to pin US business create/update/submit, consent and file
      contracts; record sources and API version (MCP unavailable in environment;
      fell back to public official documentation with provisional versioning).
- [ ] Resolve role and document ambiguities; document supported RFI and webhook
      payloads (role and country-document matrix remain unresolved external gates).
- [x] Document uncertain-create recovery; keep manual hold when no safe lookup
      exists (lookup unverified; manual hold enforced).
- [x] Record that reviewed revisions remain undecided and block final submit
      contract until resolved.
- [x] Add sanitized contract fixtures and fixture-shape checks; never invent
      missing platform behavior (tracked in testdata/PROVENANCE.md).
- [x] Run relevant checks and resolve failures before proceeding (native go test
      file execution passed).

### Task 2: Establish the isolated service

**Files:**

- Create/modify: `services/company-onboarding/go.mod`
- Create/modify: `services/company-onboarding/cmd/api/main.go`
- Create/modify: `services/company-onboarding/cmd/worker/main.go`
- Create/modify: `services/company-onboarding/internal/config/config.go`

- [x] Create an independent module under services/company-onboarding with
      separate API and worker commands.
- [x] Use standard HTTP routing and separate domain, application and adapter
      packages.
- [x] Define only necessary configuration; use sourced .env, brief comments, and
      ask before uncertain new settings.
- [x] Add startup/configuration tests and run module checks.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 3: Implement Company rules

**Files:**

- Create/modify: `services/company-onboarding/internal/domain/company.go`
- Create/modify: `services/company-onboarding/internal/domain/company_test.go`

- [x] Model Company, embedded People, identification entities and value objects
      as agreed.
- [x] Implement draft edits, audited identifier corrections and archive/deletion
      eligibility.
- [x] Allow duplicate legal identities; preserve free-text role and products
      max500.
- [x] Test aggregate invariants and archive/open-application coordination
      requirements.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 4: Implement application and account state machines

**Files:**

- Create/modify: `services/company-onboarding/internal/domain/application.go`
- Create/modify: `services/company-onboarding/internal/domain/platform_account.go`
- Create/modify: `services/company-onboarding/internal/domain/application_test.go`

- [x] Model application, Submission, Document and Information Request children
      plus independent Platform Account.
- [x] Implement frozen copies, consent data, text answers and all approved
      scenario transitions.
- [x] Preserve completed histories and distinguish submission refusal from
      verification rejection.
- [x] Test success, RFI, rejection, cancellation and uncertainty scenarios.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 5: Persist aggregates and durable operations

**Files:**

- Create/modify: `services/company-onboarding/migrations/`
- Create/modify: `services/company-onboarding/internal/postgres/`
- Create/modify: `services/company-onboarding/internal/postgres/repository_test.go`

- [x] Create numbered migrations scoped to dedicated onboarding schema/role and
      migration ownership.
- [x] Enforce one open application and one Platform Account per
      Company/platform; do not deduplicate companies.
- [x] Persist immutable snapshots and idempotency results atomically with queued
      operations.
- [x] Use transaction coordination for archive versus application creation, and
      leased job claims.
- [x] Test constraints, concurrent commands, rollback and restart behavior using
      isolated PostgreSQL tests.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 6: Expose resource and validation contracts

**Files:**

- Create/modify: `services/company-onboarding/internal/http/`
- Create/modify: `services/company-onboarding/internal/validation/`
- Create/modify: `services/company-onboarding/internal/http/handlers_test.go`

- [x] Implement Company/application CRUD scope, cursor lists, account/submission
      reads and action endpoints.
- [x] Use go-playground validator native rule codes and JSON names; do not
      expose supplied sensitive values.
- [x] Implement saved-data POST verify with 200 VALIDATION_PASSED or 422
      exhaustive field errors.
- [ ] Finalize submit request only after explicit resolution of deferred
      revision policy; do not choose a default.
- [x] Test error envelopes, draft permissiveness, submission completeness and
      pagination.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 7: Add private document storage

**Files:**

- Create/modify: `services/company-onboarding/internal/storage/`
- Create/modify: `services/company-onboarding/internal/http/documents.go`
- Create/modify: `services/company-onboarding/internal/storage/storage_test.go`

- [x] Implement encrypted private object storage and authenticated streamed
      upload/read access.
- [x] Enforce local PDF/JPEG/PNG 10MiB policy and verified document-specific
      constraints.
- [x] Store immutable local metadata and separately tracked platform upload
      references.
- [x] Provide fixture test storage and audit access without leaking identity
      data.
- [x] Test boundaries, access denial, invalid formats, replacement and
      historical references.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 8: Run recoverable platform submissions

**Files:**

- Create/modify: `services/company-onboarding/internal/platform/airwallex/`
- Create/modify: `services/company-onboarding/internal/worker/`
- Create/modify: `services/company-onboarding/internal/worker/submission_test.go`

- [x] Implement verified token authentication and platform adapter contracts
      with timeouts and sanitized errors.
- [x] Execute durable create, upload, update and submit steps; never hold DB
      transactions during HTTP calls.
- [x] Require idempotency key and replay original accepted command response
      before state checks.
- [x] Persist external IDs immediately; use safe documented reconciliation or
      manual hold after ambiguity.
- [x] Test crashes at each boundary, retries, duplicate commands, outages and
      refusal classification.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 9: Integrate authentication and webhook delivery

**Files:**

- Create/modify: `services/company-onboarding/internal/http/auth.go`
- Create/modify: `server/src/http/server.ts`
- Create/modify: `server/src/http/kit5-operator-auth.ts`
- Create/modify: `server/src/http/routes/airwallex-webhook.ts`
- Create/modify: `server/src/db/migrations/`
- Create/modify: `services/company-onboarding/internal/webhooks/`

- [x] Add Node proxy and signed short-lived operator assertions; strip untrusted
      identity headers and verify fixed algorithm/issuer/audience/expiry.
- [x] Revisit cookie path and allowed HTTP methods so existing session reaches
      onboarding routes safely.
- [x] Persist reliable webhook-forwarding work on Node side and deduplicate into
      Go-owned inbox.
- [x] Apply verified events without regressions and reconcile missed
      observations periodically.
- [x] Add boundary tests for session scope, forged assertions, forwarding
      outages, duplicate/stale events and unknown accounts.
- [x] Run relevant checks and resolve failures before proceeding.

### Task 10: Verify acceptance and operating documentation

**Files:**

- Create/modify: `README.md`
- Create/modify: `compose.yaml`
- Create/modify: `services/company-onboarding/Dockerfile`
- Create/modify: `docs/designs/company-onboarding.md`

- [x] Package separate API and worker commands; retain current backend, schema
      and roster ownership.
- [x] Reference .env through env_file; never copy credentials or enumerate env
      mappings in Compose.
- [x] Run module tests, race checks and static checks plus affected Node
      tests/build/lint.
- [x] Exercise all four approved scenarios using mocked platform and real
      isolated PostgreSQL/storage.
- [x] Document migrations, manual holds, webhook forwarding recovery and
      external acceptance gates.
- [x] Run relevant checks and resolve failures before proceeding.

## Post-completion

Live sandbox acceptance requires explicit authorization, verified platform
enablement and mappings, webhook subscriptions and actual US onboarding
requirements. Production additionally requires isolated credentials/data and
verified storage/access controls. Do not claim live acceptance from mocks.

A rejected platform account may be ineligible for another onboarding attempt;
confirm platform rules before enabling new sends against it. Documentary RFI
manual handling must use a verified supported channel. Post-activation updates
and copying remain deferred. No automatic bridge enrollment is introduced.
