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

Verify exact US legal structures, regulated roles, identification and document
requirements against the selected Airwallex API version. Country-document
conditionality and role descriptions retrieved during research were ambiguous.
Confirm webhook catalog/payloads and uncertain-create reconciliation paths.
Consent payload shape was checked through Docs MCP; collection evidence and
platform enablement still need deployment validation. These checks gate live
acceptance, not claims of already verified behavior.

## See also

- [Implementation plan](/docs/plans/20261106-company-onboarding.md)
- [Revision policy](/docs/backlog/onboarding-verification-submission-revisions.md)
- [Application copying](/docs/backlog/copy-onboarding-application.md)
- [Post-activation updates](/docs/backlog/connected-account-post-activation-updates.md)
- [Airwallex Native API](https://www.airwallex.com/docs/connected-accounts/onboarding/kyc-and-onboarding/native-api)
- [Airwallex US requirements](https://www.airwallex.com/docs/connected-accounts/get-started/kyc-and-onboarding/native-api/business-kyc-requirements/united-states-%28us%29)
