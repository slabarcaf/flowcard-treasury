# Onboarding frontend integration

For the consolidated handoff, see [Ramil onboarding/dashboard handoff](ramil-onboarding-dashboard-handoff.md).

Based on team `develop` commit `439d84d`; delivered on feature branch `feature/onboarding-api-fields`.

## Intake form rules

The UI now collects a complete application, rather than exposing the minimum
requirements for creating a company draft. Required company fields follow the
server's `VerifyOnboardingReadiness` plus the current US account mapper:

| Page | Required | Optional |
| --- | --- | --- |
| Company | Legal name, company type, US incorporation country, website, business description, US registered address including state/ZIP, EIN | Trading name, incorporation state, address line 2 |
| People | At least one person; name, job title, past birth date, identification type/number/issuing country | Identification issue/expiry dates under the current request contract |
| Application | Industry selection, intended use, monthly revenue and currency, primary contact, relationships and residential address for each person | Address line 2; non-US state/province where not required by the current local contract |
| Review | Confirm earlier pages and choose file classifications for selected files | Supporting files until country/person-specific file rules are verified |

The application page also gathers industry, usage, revenue, regulated roles and
residential addresses needed by the documented US account flow. Those are more
complete than the minimal create-application DTO. Do not describe them as
mandatory to *create a draft*; they are required by this complete-intake UI.

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

The write sequence is company creation, application creation, then optional
multipart uploads. Files use the latest expected application revision. Completed
company/application IDs and uploaded-file keys are retained in memory so a later
definite failure does not repeat successful steps. After an ambiguous write
result, automatic retry stops because no verified client idempotency contract
exists. No personal information is persisted in browser storage or logged.

A refused connection to the local Node gateway is different: Vite explicitly
returns `503 ONBOARDING_GATEWAY_UNREACHABLE` only on `ECONNREFUSED`, before the
request reaches the backend. The client explains that it was not submitted and
allows retry while retaining the form. It does not apply this guarantee to a
network timeout or disconnect after a write.

The result page reflects the backend application status. After the confirmed one-shot save succeeds, the customer acknowledgement says
Pending review and explains that information is saved with FlowCard, not yet sent
to Airwallex. The backend DRAFT status is unchanged and is shown explicitly in the
treasury detail; no new persisted internal-review transition is claimed. No customer-side `/submit` call, funding rule or customer-to-admin navigation is introduced. The internal review menu calls the existing blocked submit endpoint only after explicit confirmation.

## Remaining backend contracts

The agreed target remains: customer supplies all data, FlowCard receives it for
internal review, then submits to Airwallex, requests information or rejects.

This is not yet fully supported by the existing backend:

- Customer-scoped identity, ownership, intake submission, status reads and resume
  are missing. Existing gateway writes require an internal operator session.
- Product/service description, person email and phone are required by readiness
  checks but not accepted by the write DTOs. Do not collect and silently discard
  them or send unsupported JSON keys. These writes remain unchanged pending
  agreement on who will add the fields.
- Correct terms/data-use text and agreement type must be supplied before explicit
  acceptance can be recorded. Current flags remain false; no consent/timestamp is
  fabricated, and no consent acceptance flow is implemented.
- Internal review, request-information and intake rejection actions/states need
  contracts. Cancellation is not a replacement for rejection.
- `/submit` returns `SUBMISSION_CONTRACT_BLOCKED`; `/verify` retains the unverified
  external platform gate. US regulated-role and document requirement policies
  need confirmation before claiming an application is ready for Airwallex.
- Node onboarding upstream configuration is constructor-injected and not populated
  by the existing environment loader. A configured running backend is needed.
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
  stored industry. Submission returned SUBMISSION_CONTRACT_BLOCKED and did not
  alter revision or DRAFT status.
- Treasury now includes registration identifiers, primary contact, birth dates
  and masked identification summaries. Full personal identification numbers are
  not displayed in the review summary.
- Review actions use an accessible disclosure menu and native modal dialogs.
  Submit calls the existing revision-aware endpoint and refreshes from GET only
  after an accepted response. Missing information-request/rejection integrations
  produce explicit feedback and retain input; no status is simulated. Cancellation
  is deliberately not used as a replacement for internal rejection.
- Backend DRAFT is presented as Pending Review in the internal review header;
  this is a frontend label, not a persisted internal review transition.
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
confirmed the recovered page reaches Pending review. Focused tests now total 25
passing, including storage-failure classification and no-duplicate retry coverage.
Yidi subsequently reported no apparent issue in the manual UI review; this was not an automated browser acceptance run.
