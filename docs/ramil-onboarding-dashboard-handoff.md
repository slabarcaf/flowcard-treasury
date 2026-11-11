# FlowCard onboarding and treasury integration — handoff to Ramil

Updated: November 11, 2026 · Branch: `feature/onboarding-api-fields`
Backend reference: team `develop` at `439d84d`.

## Current outcome

The implemented local path is:

Customer completes intake → company and application are saved through your API →
customer sees **Pending review** → treasury staff can read the saved application.

This works against a locally running copy of your unchanged Node gateway, Go
onboarding service and PostgreSQL. It has **not** been validated against a deployed
backend or through a successful Airwallex submission. No backend source or schema
was changed. Delivery branch: `feature/onboarding-api-fields` in `yidi-li-ydl/flowcard-treasury`.
This delivery does not merge into team `develop` or `main`.

The target flow agreed with Yidi is:

1. Customers provide their information and wait for FlowCard review.
2. FlowCard submits to Airwallex, requests more information, or rejects internally.
3. After submission, show Airwallex's review status. Funding must remain blocked
   server-side until KYB approval and all other required eligibility checks pass.

## API integration inventory

Paths below are existing routes, not proposed new endpoints.

| Existing endpoint | Frontend use | Current integration status |
| --- | --- | --- |
| `POST /api/kit5/session` | Team/local operator sign-in | Verified locally; explicitly entered credential creates an authenticated session. Not customer authentication. |
| `POST /api/companies` | Save company, address, identifiers and people | Verified with real local persistence. |
| `POST /api/onboarding-applications` | Save platform onboarding details for the company | Verified; returns a `DRAFT` application. |
| `GET /api/companies` | Treasury onboarding company list | Verified locally; supports cursor pagination. |
| `GET /api/companies/{id}` | Treasury company details | Verified locally. |
| `GET /api/onboarding-applications?company_id={id}` | Applications belonging to the selected company | Verified locally; supports cursor pagination. |
| `GET /api/onboarding-applications/{id}` | Details, status refresh and read-only failure recovery | Verified locally. |
| `POST /api/onboarding-applications/{id}/documents` | Multipart supporting files, classification, person/identification links, expected revision | Client implemented; local storage returns `503 STORAGE_UNAVAILABLE`. Successful upload is **not** verified. |
| `POST /api/onboarding-applications/{id}/submit` | Explicit internal confirmation, sending `expected_revision` | Connected; backend explicitly returns `422 SUBMISSION_CONTRACT_BLOCKED`. No successful submission or status transition. |
| `POST /api/onboarding-applications/{id}/verify` | API client helper for readiness checks | Helper exists; not invoked as a final readiness gate by the current UI. |
| `PATCH /api/companies/{id}` and `PATCH /api/onboarding-applications/{id}` | Revision-aware API helpers | Implemented helpers; no customer edit/resubmit journey is connected to them. |

Information-request and internal-rejection endpoints are not present in the
inspected route set. Existing application cancellation is **not** used to emulate
internal rejection. Document download/list and submission history are not wired
into this frontend review panel.

## Onboarding: what is collected

The form uses the current strict DTOs. Some fields optional for creating a draft
are required by our complete-intake UI; this does not prove Airwallex readiness.

| Area | Collected and sent | Optional in this UI |
| --- | --- | --- |
| Company | Legal name, entity type, US incorporation country, website, description, registered address including state/ZIP, EIN | Trading name, incorporation state, address line 2 |
| People | At least one person; first/last name, job title, date of birth, identification type/number/issuing country; stable person/identification IDs | Identification issue/expiry dates |
| Application | Industry code, intended products, monthly revenue amount/currency, primary contact, each person's platform roles and residential address | Address line 2; non-US province/state where not required by current form rules |
| Documents | Selected file, type and applicable person/identification association | Files currently optional pending confirmed document rules; backend storage still required for actual upload |

Registration currently supports US companies. Other country selections on the
form concern person identification and residential addresses; they do not imply
support for onboarding businesses incorporated in those countries.

The industry list is a real sandbox reference export: 13 categories, 169 unique
codes, bundled in the frontend. Codes are sent as `industry_category_code`.
No new industry route is needed for this version. Source/time metadata and a
server-side-credential development refresh script are included.

### Field/consent gaps to resolve before claiming KYB readiness

- Readiness/provider checks need data not accepted by current write DTOs, including
  business products/services description and person email/phone. Confirm the exact
  requirements and extend the contracts as appropriate; the frontend does not send
  unsupported keys or silently discard collected values.
- Confirm legal terms/data-use text, agreement type and how acceptance evidence is
  recorded. Current agreement flags are **false**; no acceptance or timestamp is
  fabricated. There is no completed consent acceptance flow.
- Confirm country/entity/person-role-specific document requirements. Complete
  intake in this UI is not equivalent to an approved/ready Airwallex application.

## Dashboard: what is and is not live

The **Onboarding applications** panel on Treasury → Customers uses real API data.
It shows company information, registration identifiers, people, birth dates,
masked identification summaries, primary contact, residential addresses, industry
name/code, intended products, revenue and attached-file metadata.

The separate **Demo portfolio** remains demo data. This work does not integrate
all treasury balances, ledger metrics, cards, underwriting, bridge history or
funding actions. Creating an onboarding record does not create a verified customer
credit line or make a customer eligible for funding.

The review UI is implemented: compact action menu with icons/descriptions, explicit
confirmation dialogs, required request/rejection text, destructive rejection
styling, failure feedback and input preservation. Submit calls the real endpoint.
The other two actions explain their missing integrations; no API call, delivery,
internal decision or success status is simulated.

## Status semantics — important

| Current display | Actual persisted meaning |
| --- | --- |
| Customer `Pending review` / internal header `Pending Review` for a draft | Successful save acknowledgement; backend is still `DRAFT`. Not a persisted customer-submit/internal-review event. |
| Raw application status in treasury detail | Returned by the backend, not overridden by browser state. |
| Airwallex review status | Successful submission/status synchronization is not yet verified. |
| `Funding unavailable` | Informational UI. It is not proof of backend enforcement. |

Please define how internal intake/review state is persisted separately from
Airwallex state, including the authoritative approval/capability fields. We must
not equate company `ACTIVE`, a saved draft, or a successful HTTP call with KYB
approval. No new transition rules have been invented in the frontend.

## Backend work requested

| Priority | Needed from Ramil | Done when |
| --- | --- | --- |
| P0 | Customer-scoped authentication/authorization and ownership checks | A customer can submit/read only their application without an operator credential. Team review data stays restricted. |
| P0 | Customer submission / internal review contract | Backend records receipt for internal review distinctly from draft creation and Airwallex submission. |
| P0 | Finish and enable the existing submit contract | Reviewed revisions, readiness, consent and provider submission are handled under an agreed contract; frontend can read the resulting authoritative state. |
| P0 | Deployed backend and working gateway configuration | Share base URL, frontend origins, session/cookie requirements and how Node is configured to reach Go. Secrets are provisioned separately, never via Git. |
| P1 | Information-request API | Save message, actor/time and internal status; expose to the right customer; define response/edit/resubmit behavior and notification delivery. |
| P1 | Internal rejection API | Save required reason and actor/time; close application using agreed transitions and enforce subsequent restrictions. |
| P1 | Document storage configuration | Valid uploads persist and can be read by authorized users; failed uploads remain explicit. |
| P1 | Required-field and consent contracts | All readiness-required data is writable and real agreement acceptance is supported. |
| P1 | Provider status synchronization and funding enforcement | Airwallex updates reach authoritative reads, and funding is rejected server-side until eligibility requirements are met. |

For each action, please specify path/method, request/response, allowed source
states, `expected_revision` behavior, retry/idempotency rules and error codes.
The frontend can then wire the remaining controls without guessing transitions.
Request-info/reject reasons, customer responses and decisions must be persisted;
browser-only state is not a substitute.

## Auth and deployment details

- Operator session auth exists and works locally. Public customer access does not.
  The team sign-in page is an operator-credential interface, not a completed staff
  identity provider or customer email/password system.
- Frontend API requests are relative `/api/...` with credentials included. Local
  Vite proxies to `127.0.0.1:8000`. Production hosting needs its own supported
  routing/auth configuration; Vite development proxy settings do not deploy it.
- Local acceptance uses your existing Go `cmd/testserver` and constructor-configured
  Node gateway. The existing environment loader does not populate the onboarding
  upstream settings. This is not proof production startup is configured.
- The local database, test records, credentials and launch tools are outside the
  tracked change. They will not appear in another developer's environment after
  pulling the branch. Provision equivalent server configuration separately.

## Validation and remaining limits

Passed: frontend/server builds, changed-file lint, 25 focused onboarding tests,
and a real Node → Go → PostgreSQL save/read reconciliation of **62 fields**.
That included two people, distinct addresses, IDs/dates, primary contact, multiple
products, industry and decimal revenue. Blocked submission left revision/status
unchanged. No backend files were changed.

Yidi manually confirmed the saved-record recovery reaches Pending review after an
attachment-storage failure, and reported no apparent issue in the UI review.
Browser automation was unavailable; do not describe this as a fully automated
browser acceptance run. Successful attachments, deployed connectivity, customer
auth and end-to-end Airwallex KYB/funding remain unverified.

The full repository suite is not green: an unrestricted run reported 747 passed,
23 failed and 19 skipped, including unchanged Kit 5 assertions and unavailable Go
on PATH. Re-running the two Go suites with the installed toolchain produced 12
passes and one company-create 500 in the existing fixture suite. The separately
configured 62-field local persistence acceptance passed. Full lint also encounters
bundled third-party Swagger assets. These are recorded, not suppressed.

## Code map

- `src/components/onboarding/`: intake, review, Pending review, team/local sign-in.
- `src/services/onboarding/`: wire contracts, API client, save orchestration,
  validation/options and industry reference export.
- `src/hooks/useCompanyApplication.ts`: save/refresh state, ambiguous-result guard,
  explicit read-only saved-record recovery without pending uploads.
- `src/features/bridge-capital/backoffice/components/OnboardingCustomers.tsx`:
  live list/detail view.
- `ApplicationReviewActions.tsx` and `application-review.css`: menu and modals.
- `CustomersScreen.tsx`: real onboarding panel separated from demo portfolio.
- `vite.config.ts`: local proxy failure handling and same-origin GET forwarding.
- `src/App.tsx`: onboarding/login routes and repair of inherited malformed merge.
- `tests/onboarding/`: API, save/retry, form and reference-data coverage.

Suggested next step: agree on customer auth and internal review contracts first,
then provide a configured backend for joint deployed-environment acceptance.
