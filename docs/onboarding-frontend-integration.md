# Onboarding frontend integration

For the consolidated handoff, see [Ramil onboarding/dashboard handoff](ramil-onboarding-dashboard-handoff.md).

Based on team `develop` commit `439d84d`; delivered on feature branch `feature/onboarding-api-fields`.

## Intake form rules

The Company step follows `POST /api/companies`: only `legal_name` is required
for company creation. Business profile, registered address, registrations and
people are optional; fields become required only within an enabled section or
an added row. The People step applies the current person/identification DTO
requirements only to rows the customer adds. Application details are optional
under `CreateApplicationRequest`; saved-data `/verify` readiness remains a
separate provider gate.

| Page | Required | Optional |
| --- | --- | --- |
| Company | Legal name, 2–200 characters | Trading name, company type, US incorporation country/state, business profile, registered address, registration identifiers, people |
| Enabled business profile | Valid HTTP(S) website and description | Registered address |
| Enabled registered address | Street, city, US ZIP, country | State, address line 2 |
| Added registration/person/identification | Registration type/value/country; person name/job title/past birth date; identification type/number/country | All registration, person and identification collections; identification issue/expiry dates |
| Application | Client-supplied company ID and AIRWALLEX platform | Sandbox environment, industry code, products, revenue amount/currency, primary contact, per-person roles and residential address |
| Enabled residential address | Street, city, postal code, ISO country | State, address line 2 |
| Review | Confirm earlier pages and choose file classifications for selected files | Supporting files until country/person-specific file rules are verified |

Application selections are API codes, not display labels. Industry and product
codes accept additional user-entered values because the request schema has no
enum. Per-person application details are sent only when a relationship code or
residential address is supplied. A residential address requires street, city,
postal code and ISO country; state and line 2 are optional.

Country/state/currency/identifier/document/relationship/product values are
selected rather than entered as internal codes. US EIN and tax IDs receive
format validation; US addresses use ZIP or ZIP+4. Actual names, addresses,
website URLs, amounts, job titles and descriptions remain user input.

## Industry dictionary: frontend snapshot

No backend endpoint is added or changed. The real Airwallex sandbox export is
bundled as `industry-categories.json`, with source/time metadata: 13 top-level
categories and 169 codes. The form works without a running backend for this
selection. Category/question labels are shown to users; selected codes are sent
to Ramil's existing `industry_category_code` field.

The optional development script `scripts/fetch-onboarding-industries.mjs` refreshes
this snapshot using ignored local `.env` credentials. No credential/token is
included in the browser assets, snapshot or repository.

Source: https://www.airwallex.com/docs/api/supporting_services/reference_data/industry_categories

## Persistence and status

Save draft creates the Company and DRAFT application on first save. Later edits
PATCH the known records using refreshed revisions; successful stages and
uploaded-file keys stay in memory so a definite failure does not repeat POSTs.
Selected documents are optional for draft saving and use the latest application
revision. After an ambiguous write result, automatic retries pause for
reconciliation. No personal information is persisted in browser storage or
logged.

Draft edits use refreshed revisions and PATCH the existing records. Empty
values and arrays clear fields or collections where the edit DTO supports it.
The current API cannot remove a saved business profile or registered address;
those removal controls stay disabled after save and the limitation is shown.

A refused connection to the local Node gateway is different: Vite explicitly
returns `503 ONBOARDING_GATEWAY_UNREACHABLE` only on `ECONNREFUSED`, before the
request reaches the backend. The client explains that it was not submitted and
allows retry while retaining the form. It does not apply this guarantee to a
network timeout or disconnect after a write.

After Save draft succeeds, the form stays editable and shows the backend
application status and ID. DRAFT remains DRAFT; no internal-review transition
is created. Verify and submit first saves the current draft, calls `/verify`,
and calls `/submit` only after `VALIDATION_PASSED`. The server requires
`Idempotency-Key`, checks readiness under the submission transaction, and
returns a 202 receipt when queued. The UI keeps the receipt separate from
application status and provider approval; uncertain retries reuse the same key
and request revision. It does not call Airwallex synchronously. Current
provider-data and consent gaps still prevent this form from passing readiness.
No funding rule or customer-to-admin navigation is introduced.

## Remaining backend contracts

The agreed target remains: customer supplies data into a saved draft, FlowCard
verifies it, and the existing application lifecycle records provider submission
and outcomes. Any operator review happens without adding an application status
or internal-review API.

This is not yet fully supported by the existing backend:

- Customer-scoped identity, ownership, intake submission, status reads and resume
  are missing. Existing gateway writes require an internal operator session.
- Provider readiness still requires product/service description and person
  email and phone, but the current write DTOs do not accept those fields. The
  frontend must not collect and silently discard them or send unsupported JSON
  keys. These write DTOs remain unchanged in this integration.
- Correct terms/data-use text and agreement type must be supplied before explicit
  acceptance can be recorded. Current flags remain false; no consent/timestamp is
  fabricated, and no consent acceptance flow is implemented.
- The existing application status model remains authoritative. This integration
  adds no internal-review endpoint, `PENDING_REVIEW` status, or review migration.
- `/submit` requires `Idempotency-Key` and atomically queues the frozen
  submission, idempotency response, and worker job after saved-data readiness
  passes. The default `/verify` and submit readiness gate still fails closed
  while the provider contract, US regulated-role rules, document requirements,
  and required consent remain unverified.
- The Node environment loader supports `ONBOARDING_SERVICE_URL` and
  `ONBOARDING_SERVICE_SECRET` together. A configured, running backend is still
  needed to exercise the proxy.
- Treasury Customers now reads live companies and their applications through the
  existing GET APIs. Mock customers remain separately labeled Demo portfolio.
  Saving a real company does not invent a credit tier, line or funding permission.

## Validation and local preview

```sh
npm install
npm run build
npm run server:build
npm test -- tests/onboarding
npm run dev -- --host 127.0.0.1 --port 5174
```

Vite's existing `/api` target remains `127.0.0.1:8000`. This gateway was not running
when the user saw the original empty 502/unknown-result error. The new refused
connection response has been verified at the actual local proxy with a synthetic
company name; no backend write occurred.

Frontend build, server build and focused tests pass. Changed source lint is
clean. The broader repository suite was already not green locally: Go E2E tests
need an installed Go toolchain; existing Kit 5 lifecycle tests also fail. Full
repository lint includes bundled third-party Swagger assets and fails there.
Computer-use inspection reached the local preview, but browser click QA was
interrupted by the host returning `noWindowsAvailable`; do not claim full browser
or live-database acceptance from these checks.

## Scope confirmed by Yidi

Only frontend code, the API adapter, verified reference data, frontend development
proxy configuration, focused tests and integration documentation may be included
in a future push. No files under `server/` or `services/` are modified. Backend
field, authorization, persistence and review-state gaps are handoff items for Ramil,
not frontend emulation or local changes to his service. Yidi authorized a push to the feature branch after acceptance checks on November 11; no merge to develop or main is authorized.

## Local connectivity acceptance

Ramil's backend files are unchanged. The local PostgreSQL cluster and test launch
configuration live outside this repository, under
`/Users/teki/.local/share/flowcard-runtime`. The launcher invokes the existing
`cmd/testserver` (real handlers/repository and service JWT authentication), and
constructs the unchanged Node gateway using its existing dependency injection.
It does not start the unfinished production entrypoint or bypass authentication.
The original six migrations were applied to the isolated `flowcard_local` DB.

Verified through the frontend `OnboardingApiClient` and `SaveApplication`:
unauthorized reads return 401; operator login creates a session; company and
application POSTs persist real records; list/detail GETs return the same records;
SQL confirms persistence; the same-origin Vite GET proxy works with the backend's
existing exact-origin check. Tests used synthetic company data, not live KYB.

To try it locally:

1. Open `http://127.0.0.1:5174/#login` in another tab so the current form is retained.
2. Use the local-only credential in
   `/Users/teki/.local/share/flowcard-runtime/operator-credential.txt` (not an
   Airwallex key). The cookie is HttpOnly; no credential goes into browser storage.
3. Return to the onboarding form and submit without supporting files (the existing
   testserver object store remains unavailable, so uploads return an explicit 503).
4. Open `http://127.0.0.1:5174/#bridge-capital`, choose Customers, and refresh the
   Onboarding applications panel. The synthetic Local Integration record is
   already present from the acceptance run.

This is an authorized internal local integration test. Public customer-scoped
submission/authentication is still a backend handoff item; the operator session
is never auto-issued to anonymous customers. No Pending review page is shown
if a write or upload fails.


## Latest acceptance and handoff

- Complete synthetic intake exercised through the frontend API client and save
  operation against the unchanged Node gateway, Go HTTP service and PostgreSQL.
  62 leaf values matched after fresh GET reads, including two people, identification
  dates, different registered/residential addresses, primary contact, two products,
  industry ICCV3_0001XX and USD revenue 12345.67. SQL independently confirmed the
  stored industry. At the time, submission returned SUBMISSION_CONTRACT_BLOCKED
  and did not alter revision or DRAFT status; C1 has since replaced that
  unconditional placeholder with the durable idempotent queue.
- Treasury now includes registration identifiers, primary contact, birth dates
  and masked identification summaries. Full personal identification numbers are
  not displayed in the review summary.
- Review actions use an accessible disclosure menu and native modal dialogs.
  Verify and submit verifies saved data before sending the revision and stable
  idempotency key. A 202 receipt is shown as queued processing, not provider
  approval. Missing information-request/rejection integrations produce explicit
  feedback and retain input; no status is simulated. Cancellation is not used
  as a replacement for internal rejection.
- Backend DRAFT is shown as Draft saved. No internal review status or transition
  is persisted.
- Browser automation host is unavailable in this session. Automated API/database
  acceptance is complete; browser interaction acceptance must be checked manually.
- Changed-file lint and focused onboarding tests pass. Full lint is blocked by
  existing bundled Swagger assets. Existing Kit 5 fixture/lifecycle tests fail in
  unchanged server code; these failures are outside this frontend integration.
- The Vite proxy is local-development configuration only. Production hosting must
  forward /api to the configured backend and preserve supported auth/origin rules.
  No deployed backend connection, public customer auth, document storage, or
  successful Airwallex submission is claimed by this change.

### Repository-wide check result

The unrestricted full-suite run reported 747 passing and 23 failing tests, with
19 skipped. Three unchanged Kit 5 test files account for assertion failures;
two Go suites initially could not find Go on PATH. Re-running those two with the
installed toolchain yielded 12 passing tests and one failure: the existing
fixture acceptance company POST returned 500. The separate configured local
Node/Go/PostgreSQL 62-field acceptance described above passed. These results do
not constitute a fully green repository suite. No backend test or source was
changed to suppress failures.

### Attachment failure recovery acceptance

The manual browser run saved a company/application but failed at unavailable
file storage. The frontend now distinguishes the explicit STORAGE_UNAVAILABLE
pre-write failure from ambiguous network/server outcomes. A read-only recovery
button fetches the known saved application and, on explicit user choice, abandons
pending local uploads without deleting stored files or repeating POSTs. Yidi
confirmed the then-current recovered page reached Pending review. That was a
frontend label at the time; current UI reports Draft saved from the persisted
status. Focused tests then totaled 25, including storage-failure classification
and no-duplicate retry coverage.
Yidi subsequently reported no apparent issue in the manual UI review; this was not an automated browser acceptance run.
