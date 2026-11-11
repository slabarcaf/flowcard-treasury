# Generated OpenAPI documentation for company onboarding

Add generated OpenAPI specifications and read-only, offline Swagger UI for the
implemented onboarding API. This is an unexecuted implementation plan based on
Ramil's approved documentation scope.

[TOC]

## Agreed outcome

- `/docs/api` serves Swagger UI, with operator and internal-webhook selectors.
- `/docs/api/openapi.json` serves the operator specification.
- `/docs/api/webhooks.json` serves the internal webhook specification.
- Documentation routes are readable without authentication. Business operations
  and webhook ingestion retain their existing, separate authentication gates.
- Disable Swagger UI request execution for every method. Do not add an auth
  bypass, accept service credentials in query strings, or embed credentials.
- Bundle pinned Swagger UI assets locally, including fonts/icons if used. No
  CDN,
  remote configuration, external validator, telemetry or external fonts.
- Generated specification files live in repository `docs/api/`; plans remain in
  `docs/plans/`. Describe existing implemented behavior, including limitations.
- Generate schemas from existing Go transport DTOs. Describe operations,
  response
  variants and examples in Go rather than maintaining parallel handwritten YAML.
- Serve the same documentation through Node's gateway and directly from Go.

## Context and constraints

Go routes and request/response models currently live in
`services/company-onboarding/internal/http/handlers.go` and `dto.go`.
Multipart documents use `documents.go`; internal webhook payloads live in
`internal/webhooks/dto.go`. Node's ingress routing lives in
`server/src/http/server.ts` and `routes/onboarding-proxy.ts`.

The API uses native go-playground validation codes. Some validation rules are
custom or cross-field/domain rules; reflection alone cannot infer them all.
Use transport models, JSON tags, actual decoder behavior and validator rules as
schema evidence. Preserve nullable, omitted, required, date and array semantics.
Do not expose private aggregate persistence fields as writable API properties.

The service defaults to deny-all authentication, unavailable object storage,
and incomplete platform rules. Submit currently returns
`SUBMISSION_CONTRACT_BLOCKED`. Document these facts without enabling operations
or resolving any deferred onboarding decisions. State provisional requirements
explicitly; do not document desired behavior as implemented behavior.

Implementation starts in a new `.worktrees/` worktree based on `develop`.
Preserve the existing user `.gitignore` edit. Use Planning Exec and Agterm for
implementation/review per repository instructions; no spawned subagents.
Apply Go style and Markdown style when applicable. No deployment, live platform
calls, secret changes, commits, merges or pushes are authorized by this plan.

## Solution overview

Use a small Go operation registry binding method/path metadata to actual request
and response types. Generate the two specs deterministically from that registry
and DTO reflection using a maintained OpenAPI-capable library selected in Task
1.
Keep the current HTTP implementation and validation framework.

Prefer OpenAPI 3.1 if the selected library and bundled Swagger UI support it
reliably. Pin the chosen dialect and dependencies after a focused compatibility
check; avoid a framework migration just to produce documentation.

Keep one schema/operation source. The Go build cannot embed files above its
module root, so generate both a module-local embedded copy and the repository
`docs/api/` copies in one command. A check command must reject missing or stale
copies. Do not manually edit either generated copy.

Serve the operator spec using relative server URLs so gateway access targets
Node and direct service access targets Go. Describe the operator session at the
gateway and service JWT at Go accurately, including their distinct deployment
boundaries. The internal spec describes Node-to-Go webhook delivery with its
specific JWT audience; it does not imply browsers submit Airwallex webhooks.

## Progress tracking

All tasks remain unchecked until implemented and verified. Record unresolved
schema/contract questions rather than silently changing API behavior. Keep this
plan separate from the onboarding implementation plan and its open gates.

## Implementation steps

### Task 1: Inventory the actual HTTP contracts and choose generation tooling

**Files:**

- Create: `services/company-onboarding/internal/apidocs/contracts_test.go`
- Modify: `services/company-onboarding/go.mod` and `go.sum`, if needed

- [ ] Inventory every registered operator route, method, request DTO, response
      shape, status, query/path/header parameter and multipart/binary operation.
- [ ] Inventory both internal webhook route aliases and their authentication,
      request bounds, durable acknowledgement, and error behavior.
- [ ] Evaluate a maintained schema-generation library against actual DTOs and
      validator tags using a minimal representative fixture. Record the chosen
      library/version and OpenAPI dialect; retain existing handlers/validation.
- [ ] Identify any DTO/handler disagreement as an explicit issue. Document
      actual
      behavior; do not sneak behavioral fixes into the documentation change.
- [ ] Add focused checks for nested arrays, optional fields, date-only strings,
      nullability and custom validation constraints; run them successfully.

### Task 2: Generate separate operator and webhook specifications

**Files:**

- Create: `services/company-onboarding/internal/apidocs/operations.go`
- Create: `services/company-onboarding/internal/apidocs/generate.go`
- Create: `services/company-onboarding/internal/apidocs/generate_test.go`
- Create: `services/company-onboarding/cmd/openapi/main.go`
- Generate: `docs/api/openapi.json` and `docs/api/webhooks.json`
- Generate: `services/company-onboarding/internal/apidocs/specs/*.json`

- [x] Implement stable operation IDs, reusable schemas, descriptions, tags,
      examples, parameters, content types and response metadata in Go.
- [x] Include all implemented operator routes: Companies, applications, verify,
      blocked submit, document operations, submissions and Platform Account
      reads.
      Include health endpoints as operational documentation if in the inventory.
- [x] Document internal webhook aliases in the separate specification. Document
      server-side JWT authentication and durable receipt, not platform signature
      verification that occurs at Node ingress.
- [x] Model multipart upload, binary download, cursors, revision parameters,
      validation error arrays and distinct domain error codes accurately.
- [x] Explicitly describe blocked submission, unverified platform rules and
      unavailable runtime dependencies. Use fictional examples without secrets.
- [x] Generate all copies deterministically; implement a non-mutating check mode
      for stale/missing generated output and validate references/spec structure.
- [x] Test deterministic generation, valid references, separate security schemes
      and representative request/response conformance.

### Task 3: Serve bundled read-only Swagger UI from Go

**Files:**

- Create: `services/company-onboarding/internal/apidocs/handler.go`
- Create: `services/company-onboarding/internal/apidocs/handler_test.go`
- Create: `services/company-onboarding/internal/apidocs/assets/`
- Modify: `services/company-onboarding/internal/http/handlers.go`

- [x] Bundle a pinned Swagger UI distribution with license notices and both spec
      selectors. Disable all request execution and authorization persistence.
- [x] Register exact documentation routes outside API auth and repository gates.
      Redirect `/docs/api` to `/docs/api/` for relative asset loading if needed.
- [x] Serve specs and assets with correct content types, bounded responses and
      suitable cache/security headers. Do not use a directory listing or broad
      filesystem handler for repository `docs/`.
- [x] Ensure docs work with no database, no auth keys and no platform clients;
      keep business endpoints deny-by-default.
- [x] Test documentation access without login, JSON content/reference validity,
      read-only UI configuration, asset traversal denial and unchanged API auth.

### Task 4: Expose the same documentation through Node

**Files:**

- Modify: `server/src/http/server.ts`
- Modify: `server/src/http/routes/onboarding-proxy.ts`
- Create: `tests/server/onboarding-api-docs.test.ts`

- [x] Add an explicit unauthenticated allowlist for `/docs/api` and its approved
      specification/UI asset paths. Forward only read methods to Go.
- [x] Preserve route suffixes, redirects and relative asset/spec URLs at both
      service and gateway origins. Do not expose plans or arbitrary docs files.
- [x] Keep operational proxy authentication and header stripping unchanged;
      documentation access must not permit API or webhook invocation.
- [x] Use constructor-injected upstream configuration as before. If
      unconfigured,
      return a clear unavailable response without inventing environment
      variables.
- [x] Test gateway/direct URL parity, no cookies required, path encoding and
      traversal rejection, unavailable upstream, and protected-route separation.

### Task 5: Add drift prevention and offline acceptance checks

**Files:**

- Modify: `services/company-onboarding/internal/apidocs/contracts_test.go`
- Create: `tests/server/onboarding-api-docs-e2e.test.ts`
- Modify: existing CI configuration, only if one exists and is applicable

- [x] Compare registered route methods/paths with operation inventory. Prefer
      shared registration metadata or an independent route inventory check so
      adding a handler without documentation fails verification.
- [x] Validate actual example responses/errors against generated schemas using
      fixture handlers; cover multipart upload metadata and binary responses.
- [x] Check generated files are current using the documented check command.
      Ensure deliberate metadata edits require deterministic regeneration.
- [x] Run actual Node-to-Go documentation acceptance with business defaults
      still
      denied; reuse the existing isolated test harness and database guards.
- [x] Verify UI rendering and selector behavior with network requests restricted
      to the local service. Confirm no external assets or Try-it-out controls.
      Use a properly authorized Agterm/browser review workflow if required.
- [x] Run relevant Go tests/race/vet and affected Node tests/server build.
      Record
      the existing 23 Node baseline failures separately from any new regression.

### Task 6: Document review and maintenance commands

**Files:**

- Modify: `README.md`
- Create: `docs/api/README.md`
- Modify: this plan's progress checkboxes during authorized implementation

- [x] Document how to start the service and review `/docs/api` directly or
      through
      the configured Node gateway. State when gateway configuration is absent.
- [x] Document generation/check commands, generated artifact locations,
      dependency
      versions, operation metadata ownership and adding a new route/schema.
- [x] Explain read-only documentation, the two audience-specific specs and the
      unchanged runtime authentication/platform/storage limitations.
- [x] Verify links and Markdown source formatting; rerun spec freshness/coverage
      checks and summarize implementation limitations accurately.

## Acceptance criteria

- A reviewer can browse both specifications at `/docs/api` without login or DB.
- Swagger UI renders entirely from bundled assets with request execution
  disabled.
- Specs match current handlers, DTOs, validation errors and blocked operations.
- Operator and internal-webhook security descriptions remain distinct.
- Node and Go documentation routes work; no broad repository docs exposure
  occurs.
- Generation is deterministic and route/schema drift checks fail meaningfully.
- Existing business behavior and all previously deferred decisions are
  preserved.

## Post-completion

No public publication, live Airwallex acceptance or production authentication
configuration is required to review this documentation. Any later interactive
Swagger mode or externally hosted documentation is a separate scope decision.

Generated documentation does not resolve onboarding platform-contract gates or
submit revision policy. Keep those items open in their existing plan/backlog.
