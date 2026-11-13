# Onboarding API integration

Merge the onboarding frontend into develop, align company intake with the
existing API, and connect the existing verify/submit lifecycle to the UI.
The design in `docs/designs/company-onboarding.md` governs this work.
Authentication changes are outside this milestone. Live provider acceptance
requires the documented external contract gates to be resolved.

## Agreed scope

- Preserve develop routing, dashboard selection, infrastructure, and deployment.
- Audit contracts against `docs/api/openapi.json` and implemented
  DTOs/validation.
- Collect supported company creation fields, including nested people,
  identifications, registration identifiers, business profile, and address.
  Keep optional API fields optional. Flag unsupported or inconsistent fields.
- Save company/application data and retrieve the same records in Treasury
  Customers. Display actual persisted application and Submission statuses.
- Assume data does not change after operator review. Concurrent-edit policy
  and additional review-lock mechanisms are deferred.
- Use existing authentication for verification; do not introduce bypasses.
- Keep tests focused, at most two or three new cases per critical feature.
  Run the combined verification only after implementation is complete.

## Starting point

The isolated branch `integrate/onboarding-api-fields` starts at remote feature
commit `4f4b7e2`. The trial merge of develop `7c83cb8` found one conflict,
`src/App.tsx`; it was aborted, leaving the integration worktree clean.
Refresh refs and recheck divergence before execution.

The current `/submit` handler always returns `SUBMISSION_CONTRACT_BLOCKED`.
It represents provider submission. A submission worker, webhook processor,
and account reconciler exist. The repository already exposes the atomic
`InitiateSubmission` transaction used to freeze data and queue work.
The default readiness gate also reports unverified provider rules. Removing
the submit placeholder alone will not establish submission readiness.

Use the designed lifecycle DRAFT -> SUBMISSION_PENDING -> UNDER_REVIEW ->
COMPLETED, with the existing INFORMATION_REQUIRED/REJECTED/CANCELLED branches.
Do not add an internal-review API, PENDING_REVIEW status, or related migration.
The draft acknowledgement must say Draft saved until submission is accepted.

## Task group A: Merge and authoritative API contract

### Task A1: Merge develop and resolve routing

- [x] Refresh remote refs and confirm both worktrees are clean.
- [x] Merge `origin/develop` into the integration branch.
- [x] In `src/app-routing.ts`, retain the `AppScreen` union and
  `legacyVersionFromStorage`. Add `'#login': 'login'` and
  `'#onboarding': 'onboarding'` to `SCREEN_BY_HASH`. Keep `#bridge-capital`
  mapped to dashboard, `#portal` to portal, `#kit5` to kit5, and the default
  landing fallback. Do not introduce another hash parser in App.
- [x] In `src/App.tsx`, resolve the conflict using develop's imports,
  `screenFromHash` initialization, remembered demo version, and version
  toggle. Keep both GD and Kit5 branches under the kit5 screen. Keep existing
  BackOffice/Portal navigation and LoginPage callbacks. Keep the feature's
  OnboardingWizard completion callback returning to landing.
- [x] Keep `vite.config.ts`: React/Tailwind plugins, `.dev/dist` output,
  existing allowed hosts, and the `/api` proxy to `127.0.0.1:8000` with
  `changeOrigin`. Merge in the feature's gateway-refused 503 response and
  validated local Origin fallback. Extend test exclusion to
  `**/.worktrees/**` while retaining `**/worktrees/**` and default excludes.
  Vite remains the build system and local gateway proxy; deleting this
  configuration would change those behaviors.
- [x] Preserve develop's `server/tsconfig.json` output `.dev/server`,
  `.gitignore`, `.github/workflows/` deployment behavior, and `docs/infra/`.
  This task changes routing and combines configuration; it adds no deployment
  workflow inputs or infrastructure resources.

Completion: conflict-free merge, one routing helper, and preserved build and
deployment paths. Final runtime checks are in task D1.

### Task A2: Correct OpenAPI before changing forms

Depends on A1. This is the first feature-development task, so all later form
work uses corrected documentation. No form changes precede this task.

- [ ] Edit `services/company-onboarding/internal/apidocs/generate.go`,
  `populateRequiredFields`: for a slice/array property, recurse into the
  property's item schema with its element type. Currently the function uses
  the array schema itself, so required members of array items are omitted.
  Keep direct struct/pointer recursion and the existing required-field rules.
- [ ] Retain requiredness from `internal/http/dto.go`: registration item
  requires type/value/issuing_country; person requires first_name/last_name/
  role/date_of_birth; identification requires type/number/issuing_country.
  Optional parent arrays remain optional. Do not change DTO requirements.
- [ ] Regenerate via `go run ./cmd/openapi` from
  `services/company-onboarding`. Commit generated `docs/api/openapi.json`
  and `services/company-onboarding/internal/apidocs/specs/openapi.json`.
  If the generator rewrites webhook specs, retain only necessary generated
  differences; do not hand-edit generated schemas.
- [ ] Record the remaining provider-readiness gaps in
  `docs/onboarding-frontend-integration.md`: products/services description and
  person contact fields have no write DTO; record the current submit/readiness
  blockers and the C1 changes that will replace the submit placeholder.
  Correct the stale claim that the environment loader cannot configure the
  onboarding upstream, since develop now supports that configuration.

Completion: generated nested required fields match DTOs; known provider gaps
are explicitly documented. Spec checks and test execution wait for D1.

## Task group B: Company and application forms

### Task B1: Align company and people inputs

The feature already renders the basic company and people fields. Its current
`stepIssues` requires a US company, website, profile, address, EIN, people, and
identifications even though company creation requires only `legal_name`.
Apply the following changes in `CompanyApplicationStep.tsx`,
`PeopleApplicationStep.tsx`, `ApplicationFields.tsx`, `formRules.ts`, and
`contracts.ts`. The table describes company creation. Submission readiness
comes from the saved-data `/verify` contract, not draft-create requiredness.

| API field | Exact form behavior and validation |
|---|---|
| `legal_name` | Keep Legal company name, required, 2–200 characters after trimming. |
| `trading_name` | Keep Trading / display name, optional, maximum 200 characters. Omit when blank. |
| `entity_type` | Make Company type optional. Preserve existing choices and add Other with a text input, maximum 50 characters. The API accepts a string, not a four-value enum. |
| `registration_country` | Keep the current US intake scope, default US and explain that scope. Do not imply the backend supports only US. |
| `registration_state` | Keep optional State of incorporation and its US choices, maximum 100 characters. |
| `business_profile.website` | Move into an optional Business profile section. When enabled, require a valid URL, maximum 500 characters. |
| `business_profile.description` | In that section, require Business description, maximum 2,000 characters. |
| `business_profile.registered_address` | Add an optional Registered address section. When enabled, require street, city, ZIP, and country; state and line 2 remain optional under the create DTO. |
| Address members | Street/line 2 maximum 200, city/state maximum 100, postal code maximum 20. Keep US ZIP formatting for this US form. Country is US. |
| `registration_identifiers[]` | Replace the first-entry-only tax section with optional repeatable registrations. Add Add registration and Remove controls; preserve all entries. |
| Registration members | Each added row requires type (maximum 50), value (maximum 100), and issuing country. Default the first added row to EIN/US; provide Other registration type text and country selection. Apply nine-digit normalization only to US EIN values. |
| `people[]` | Make the collection optional for company creation. Start empty and provide Add person. Permit removing the last person. |
| Person members | Each added person requires first name, last name, job title (each maximum 100), and a valid past birth date. Keep role as free text. Generate stable client IDs, never expose an ID input. |
| `people[].identifications[]` | Make identifications optional. Add Add identification and Remove controls per person; retain all entries and stable IDs. |
| Identification members | For each added row require type (maximum 50), number (maximum 100), and issuing country. Preserve known choices and allow Other type text. SSN/ITIN retain US nine-digit validation. |
| `issued_on`, `expires_on` | Keep optional date inputs; omit empty strings. Validate real calendar dates and enforce the existing domain rule that expiry follows issue date when both are present. |

- [ ] Optional profile/address sections omit the entire nested object when
  disabled. Enabled, incomplete sections show field errors and cannot save.
- [ ] Remove provider-readiness requirements from company-create validation.
  Keep collection row requirements conditional on the row being present.
- [ ] Replace `newCompany()`'s populated empty objects/rows with legal name,
  the US default, and empty optional collections. Add helpers for new rows.
- [ ] Add a payload serializer that trims text, omits blank optional members,
  and preserves every entered collection item and stable ID. It must not
  send display labels or application-only fields in `CreateCompanyRequest`.
- [ ] On removal of a person/identification, remove its application references
  and pending document associations. Clear primary contact if that person was
  selected. Do not leave dangling `person_id` or `identification_id` values.

Completion: the field table is implemented, and every entered supported
company value has an explicit path into the company request.

### Task B2: Align the application form with CreateApplicationRequest

Edit `src/components/onboarding/steps/UsageApplicationStep.tsx`,
`src/services/onboarding/contracts.ts`, `formRules.ts`, and `options.ts`.
All fields in the table are under `onboarding_details` unless stated otherwise.
OpenAPI describes transport fields, not a downloadable page layout.

| Request field | Exact application form change |
|---|---|
| `company_id` | Use the ID returned by company creation. Never expose an editable ID or substitute a demo company. |
| `platform_name` | Keep AIRWALLEX as the integration target. Show a read-only platform label; no platform selector. |
| `environment` | Keep sandbox as configured by the existing client. No customer input or production switch. |
| `industry_category_code` | Make Industry optional. Keep the bundled industry snapshot as selectable suggestions and add Other code entry because the request schema accepts a string. Remove the undocumented ICCV3-only regex. Send the selected or entered code, never its label. |
| `intended_products[]` | Make the checkbox group optional. Keep current product suggestions and allow adding another code, since the API has no enum. Preserve all selected codes. |
| `estimated_monthly_revenue.amount` | Make optional and preserve a decimal string. Remove the mandatory/nonnegative rule from API-create validation. Use a decimal input mode so transport formatting does not round large values. Omit blank amount. |
| `estimated_monthly_revenue.currency` | Make optional; preserve the existing currency selector. Omit blank currency. Keep the enclosing revenue object when sending details. |
| `primary_contact_person_id` | Make optional, include No primary contact selected, and offer only saved/intake people. If there are no people, show a link to People; do not block draft creation. |
| `people[].person_id` | Use the associated stable person ID. A person gets an application details entry only when a role or address is supplied. Remove entries for deleted people. |
| `people[].platform_roles[]` | Make optional. Retain relationship suggestions, permit additional role codes, and preserve every selected value. API schema contains no enum. |
| `people[].residential_address` | Make an optional per-person address section. When enabled require line_1, city, postal_code, ISO country; line_2/state remain optional under the DTO. Apply DTO length limits from B1. |
| `customer_agreements` | Send explicit false flags and omit terms metadata. Do not render acceptance controls without approved agreement text/type. False is a saved value, not recorded consent. |

- [ ] In `formRules.ts`, replace step 2's provider-readiness checks with the
  above optionality and nested address checks. Keep referential checks for
  supplied person IDs. Do not use `/verify` to block draft creation.
- [ ] In `contracts.ts`, change the amount/currency contract to match optional
  string members. Keep required wire containers in the serializer rather
  than marking their child controls as required.
- [ ] Add `src/services/onboarding/payloads.ts` with pure company/application
  serializers. Omit blank optional values, retain explicit agreement booleans,
  and emit `estimated_monthly_revenue: {}` when its values are blank.
- [ ] In `OnboardingWizard.tsx`, replace unconditional registration value
  normalization with the serializer. Normalize only recognized US EIN and
  SSN/ITIN values; preserve other identifiers. Keep IDs consistent across
  company people, application details, and pending document links.
- [ ] In `OnboardingApiClient.ts`, keep existing create endpoints and
  `company_id`, AIRWALLEX, sandbox assignment. Send the serialized details;
  do not add unsupported goods/services or person contact fields.

Completion: an API-valid application can be created without optional industry,
usage, revenue, or person details. Provider readiness remains a separate step.

### Task B3: Complete the review screen and save orchestration

- [ ] Expand `ReviewApplicationStep.tsx` to show company type, incorporation
  country/state, business description, every registration, every person and
  their job title/birth date/identifications, and all application fields.
  Provide an Edit link to each corresponding wizard step. Mask identification
  numbers by default with an explicit reveal control in the current session.
- [ ] Show industry code and label when known, every intended product, revenue
  amount/currency, primary contact name, each person's platform roles and
  residential address, and agreement flags as Not accepted. Show Not provided
  for omitted optional data. Resolve unknown codes to their raw values rather
  than hiding them. Never print undefined or invent default saved values.
- [ ] Keep selected documents optional for draft saving. Show file name,
  classification, and linked person/identification. A selected upload must
  succeed or be explicitly removed before final submission.
- [ ] Add Save draft and Verify and submit actions on the final step.
  Save draft persists data/uploads without calling submit. Verify and submit
  saves first, calls `/verify`, then `/submit` only after verification passes.
  Disable both during requests. Show field errors or contract blockers
  without claiming submission succeeded.
- [ ] In `SaveApplication.ts`, execute company POST, application POST,
  selected document uploads, and then return the saved DRAFT. Refresh the
  application revision before uploads and final submission. Keep the known
  IDs between stages and reuse completed stages after definite failures.
- [ ] In `useCompanyApplication.ts` and `OnboardingWizard.tsx`, track saved
  draft and accepted submission separately. A failed verification or submit
  keeps the draft reference and form. Existence of `saved.application` alone
  must not hide editable data. If the user edits after draft saving, PATCH
  the existing company/application before verifying, using current revisions.
  Do not repeat POST creation or silently verify stale saved inputs.
- [ ] Retain false agreement flags until actual acceptance can be collected.
  Provider data and consent gaps are handled as explicit C1 prerequisites;
  do not duplicate the contract findings table already covered by A2.

## Task group C: Existing submission lifecycle and Treasury retrieval

### Task C1: Wire the existing submit endpoint to durable processing

Design references: HTTP contract and execution/recovery sections of
`docs/designs/company-onboarding.md`. This task uses existing statuses,
repository transaction, outbox, and worker. No new review resource is needed.

- [ ] In `services/company-onboarding/internal/http/handlers.go`, replace
  `handleSubmitApplication`'s unconditional 422 with strict request parsing,
  required Idempotency-Key validation, authenticated actor extraction, and
  dispatch to the existing repository transaction. Keep the current route.
- [ ] Keep `{expected_revision}` in `SubmitApplicationRequest`. Under Ramil's
  assumption, use the saved application revision and current company data;
  add no reviewed-company-revision field or new approval-lock mechanism.
  Preserve the existing application CAS check and immutable snapshot behavior.
- [ ] Reuse `internal/postgres/repository.go: InitiateSubmission` with scope
  bound to actor/application/action, a deterministic request fingerprint,
  generated Submission ID, topic `onboarding.submission.v1`, and payload
  `{application_id, submission_id}`. Reuse `worker.SubmissionJobPayload`'s
  shape without importing worker lifecycle into HTTP handling.
- [ ] Return 202 `{code: "OK", application_id, submission_id}` and Location
  pointing to the existing application submissions collection. Save that
  response in the same atomic idempotency transaction. No unregistered
  submission-detail URL or new endpoint is introduced.
- [ ] Ensure an existing completed idempotency entry is replayed before
  lifecycle/readiness validation, even if application status has advanced.
  Reuse existing reservation/transaction primitives; do not implement an
  unlocked read-then-create shortcut. Changed request with the same key is
  409 IDEMPOTENCY_CONFLICT; in-progress/unresolved work is 409
  SUBMISSION_IN_PROGRESS; missing key is 400. Missing resource is 404.
- [ ] For new commands, run the same saved-data readiness validation as
  `/verify` before queueing. Map validation to 422 and state/revision conflict
  to 409. Do not call Airwallex synchronously from the HTTP handler.
- [ ] Preserve `InitiateSubmission`'s Company-first/Application-second lock
  ordering, frozen Company/onboarding/doc references, submitted-history flag,
  and atomic outbox/idempotency writes. Extend it only if needed to evaluate
  the readiness check on the same locked data. Do not rebuild the transaction.
- [ ] In `internal/apidocs/operations.go` and `internal/http/dto.go`, describe
  the working submit contract: idempotency header, 202 response, Location,
  400/409/422 errors. Regenerate specs using A2's command. Update the existing
  blocked-contract assertions in `internal/apidocs/contracts_test.go`.
- [ ] In `server/src/http/routes/onboarding-proxy.ts`, preserve forwarding of
  Idempotency-Key and Location. Add forwarding only if absent; retain existing
  cookies, actor assertion, and authorization checks.

Prerequisites for successful real submission, not resolved by the data-change
assumption:

1. `internal/validation/validator.go` defaults to
   `UnverifiedPlatformContractGate`, which always rejects readiness. A verified
   US legal-structure/role/document implementation must replace that default
   before claiming live submission works. Do not use a permissive stub.
2. Goods/services description and person email/phone are required by readiness
   but absent from write DTOs. Exposing them requires an explicit API extension
   to `BusinessProfileDTO`, `PersonDTO`, their create/edit converters in
   `handlers.go`, generated schemas, and the company form. That extension is
   flagged for Ramil, not silently added to this merge scope.
3. Real agreement acceptance requires supplied terms/data-use text and agreement
   type. The design records FULL, but actual acceptance must be explicit;
   never fabricate consent, acceptance time, IP, or user agent.

If these prerequisites remain unresolved, implement the UI/command wiring and
surface specific 422 errors. Report submission acceptance as blocked. A working
draft journey or mocked test does not complete the live-submit milestone.

### Task C2: Connect verify/submit in the frontend

- [ ] In `OnboardingApiClient.ts`, retain `verify(id)` and replace
  `submitApplication`'s discarded response with a typed submission receipt.
  Accept a caller-owned Idempotency-Key and forward it through the common
  request helper. Parse the 202 IDs, then read the application separately.
- [ ] In `contracts.ts`, add the receipt type and Submission list response
  matching the existing GET submissions endpoint. Add `listSubmissions` to
  the client; preserve existing application status mappings.
- [ ] In `useCompanyApplication.ts`, save drafts through B3, verify saved
  data, and generate one key per submission attempt after verification passes.
  Keep that key/request in memory for transport retries. Changed inputs start
  a new command; transport uncertainty must not create a new key.
- [ ] Render `/verify` field paths beside the corresponding form inputs and
  an error summary with links to Company/People/Application/Documents steps.
  Render platform-contract errors as blockers with the saved draft reference.
- [ ] A 202 means Submission pending, not approval. Display returned IDs and
  retrieve status through application/submission reads. A timeout retains the
  command key; replay the same command to retrieve its durable result.
- [ ] Use the existing worker entrypoint `cmd/worker/main.go` and its durable
  processing in `internal/worker/submission.go`. Preserve reconciliation and
  webhook processing. Do not add another worker or bypass its preflight gates.

### Task C3: Display persisted records and handle failures

- [ ] Verify company/application linkage and Treasury list/detail payloads,
  including pagination and explicit separation from the demo portfolio.
- [ ] Extend `OnboardingCustomers.tsx` details to display the complete saved
  company fields and all registrations/people/identifications described above.
  Render DRAFT as Draft saved, SUBMISSION_PENDING as Submission pending,
  UNDER_REVIEW as Under review, and other existing statuses accurately.
  Add the existing Submission history list with IDs/status/timestamps so
  queue acceptance can be distinguished from provider acceptance.
- [ ] Preserve entered data and known saved IDs on partial failures. A retry
  must not repeat successful creation steps or fabricate completion.
- [ ] Surface ambiguous create/upload writes without blind retries. Submission
  command retries use the same Idempotency-Key and original request.
- [ ] Confirm document upload errors are explicit and do not discard records.
  Do not claim successful uploads until verified against configured storage.
- [ ] Inspect deployed request compatibility, including the Vite-only Origin
  fallback. Flag any required gateway change without expanding auth scope.

## Task group D: Final verification

### Task D1: Run focused checks and the complete journey

- [ ] After development is complete, run frontend/server builds, lint scoped
  to changes, and existing onboarding/routing suites.
- [ ] Company form: two focused cases, one populated payload roundtrip with
  multiple registrations/identifications, one minimal company that omits all
  disabled optional sections while rejecting incomplete enabled rows.
- [ ] Submission: three focused cases, eligible submission atomically creates
  a frozen Submission and one job with 202; same key/request replays the same
  IDs without another job; failed readiness leaves DRAFT with no queued job.
  Extend existing assertions for changed-key-body and stale-revision errors.
- [ ] Recovery/UI: two focused cases, failed application/upload retains saved
  company/application IDs; Treasury refresh shows those same records and
  the persisted application/Submission statuses. Reuse routing coverage.
- [ ] Reuse frameworks/fixtures, no new dependencies or test harness. The
  approved two-to-three-per-feature scope supersedes the three-per-task default
  for this planned work. Run these cases in the final verification stage.
- [ ] Run existing Go domain/repository/HTTP/worker and OpenAPI suites,
  `go run ./cmd/openapi -check`, plus relevant frontend tests. Use existing
  test infrastructure and compare broader failures to the documented baseline.
- [ ] Perform a real local browser/API journey with synthetic company data:
  create company/application, upload a file when storage is configured, save,
  verify, submit, refresh Treasury Customers, and inspect identical IDs/fields.
  Use existing provider fixtures in isolated automated tests. Do not label a
  fixture-driven UNDER_REVIEW result as real Airwallex acceptance.
- [ ] Separate final outcomes: draft/UI integration; durable 202 queue
  acceptance; worker/provider acceptance. If production readiness gates remain,
  demonstrate their explicit errors and mark the latter outcomes blocked.
- [ ] Review the final diff and report API mismatches, checks, and remaining
  limitations. No push/deploy or live Airwallex calls during implementation
  without explicit authorization for that separate acceptance run.

## Execution

Use a Codex executor in the same agterm workspace, communicating through the
mailbox. The executor writes implementation files; the lead independently
reviews changes. Split merge resolution, form alignment, and submission wiring
into reviewable changes. Report newly discovered contract decisions before
implementing assumptions.
