# Applications navigation

Add Applications immediately after Overview in the Treasury sidebar. Show
onboarding applications there and remove the onboarding panel from Customers.
Implementation Tasks A-D and bounded verification are complete in the isolated
worktree. Full acceptance verification remains deferred per Ramil's request.

## Scope and behavior

Sidebar order: Overview, Applications, Customers, Rules, Admin.
Applications contains live onboarding records across companies and statuses,
including DRAFT. Each row represents one application. Customers retains the
existing demo portfolio, tiers, limits, and bridge navigation. Display 25
records
per page using cursor-based Previous/Next navigation. Clicking an application
opens a dedicated detail page inside Treasury, replacing the list content.

Use the existing onboarding API and current session authentication. Preserve
the application lifecycle in `docs/designs/company-onboarding.md`, including
live Company data for drafts and frozen Company data for submitted records.
This change adds no backend endpoints, provider behavior, or customer
enrollment.

## Task A: Add the Applications navigation destination

Files:

- `src/features/bridge-capital/backoffice/useBackOffice.ts`
- `src/features/bridge-capital/backoffice/BackOffice.tsx`
- New `src/features/bridge-capital/backoffice/screens/ApplicationsScreen.tsx`

Changes:

- [x] In `useBackOffice.ts`, export `ApplicationsListState` with
  `cursor?: string` and `previousCursors: Array<string | undefined>`.
  First-page state has no cursor and an empty history.
- [x] Add two `View` variants: `{screen: 'applications'; listState:
  ApplicationsListState}` and `{screen: 'application'; applicationId: string;
  listState: ApplicationsListState}`. The detail view carries the list
  position it should return to. Keep existing variants unchanged.
- [x] Insert Applications after Overview in `NAV`. Add
  `applications: 'Applications'` to `SCREEN_TITLE` and render
  `ApplicationsScreen` when selected, using the existing active-item styling.
  Sidebar selection opens first-page state. Add `application: 'Application'`
  to `SCREEN_TITLE`; highlight Applications for both new screen variants.
- [x] Import/render the dedicated `ApplicationDetailScreen` from Task D.
  Pass application ID, list state, and `office.go`. Keep the existing shell
  header, sidebar, and other screen branches unchanged.
- [x] Make `ApplicationsScreen` render the onboarding applications component
  independently of `office.data` and demo portfolio loading. Keep existing
  in-view navigation; no new top-level hash route or router dependency.

Done when selecting Applications opens its own titled screen while Overview,
Customers, Rules, Admin, request details, and bridge details retain their
behavior.

## Task B: List applications directly across companies

Files:

- `src/services/onboarding/OnboardingApiClient.ts`
- In `src/features/bridge-capital/backoffice/components/`, rename
  `OnboardingCustomers.tsx` to `OnboardingApplications.tsx`, including its
  exported component name.

Changes:

- [x] Make the existing `listApplications(companyId, cursor)` company argument
  optional. With no company, request
  `GET /api/onboarding-applications?limit=25`; append cursor for subsequent
  pages. Keep `company_id` filtering when explicitly supplied by a caller.
  OpenAPI already supports omitting this filter; no schema changes are needed.
- [x] Set `limit` to 25 for both filtered and unfiltered calls. Use
  `URLSearchParams` to encode the optional company ID and opaque cursor.
  Call `listApplications(undefined, listState.cursor)` from the list page.
- [x] Return the typed `ApplicationPage` from the existing request helper.
  Remove this method's current client checks for array/record shape, matching
  company IDs, and cursor type. Display the backend results directly; add no
  response-validation helpers. Preserve HTTP/network errors, credentials,
  and validation behavior of unrelated API methods.
- [x] Replace the component's company-first list and company-expansion flow
  with application-list loading. Remove `listCompanies`, company-list cursor,
  `openCompany`, and the nested per-company application list from this view.
- [x] Table columns: Company (legal name), Application ID, Status, Created,
  and Details. Use the existing status formatter and date formatter. Show
  company ID as fallback if embedded legal name is unavailable. Do not fetch
  every company merely to populate the table.
- [x] Include all API-returned statuses. Do not label DRAFT as submitted or
  Pending review. Do not hide terminal applications or create default filters.
- [x] Component props are `listState`, `onPageChange(nextState)`, and
  `onOpenApplication(applicationId)`. Keep local state for the current page's
  `applications`, `nextCursor`, `busy`, `error`, and sign-in controls.
  `ApplicationsScreen` converts callbacks into the View variants from Task A.
- [x] Load on mount/cursor change. Replace rows with each response page rather
  than append. Keep backend order and display unknown codes through existing
  formatter fallbacks. Fetch no Company merely to fill list labels.
- [x] Add Previous, Page N, and Next beneath the table. N equals cursor-history
  length plus one; no total pages/count is available from the API.
  Disable Previous on page 1, Next when `next_cursor` is absent/empty, and both
  while loading. On Next, append current cursor (including undefined for the
  first page) to history and navigate to next_cursor. On Previous, pop history
  and navigate to that cursor. Copy arrays; never parse cursors or use offsets.
- [x] Each row uses the existing ClickableTr pattern to open details, with a
  keyboard-accessible View application button calling the same callback.
  Prevent button propagation from navigating twice. Pass the current cursor
  history into the detail View. Remove inline expanded-detail rendering.
- [x] Preserve initial loading, error/retry, existing sign-in handling,
  explicit empty state (No applications yet), refresh on window focus, and
  refresh on `flowcard:onboarding-saved`. Event/focus/manual refresh resets
  to page 1; pagination and returning from detail preserve list position.
  A company with no application does not produce an applications row.
- [x] Render No applications yet on an empty first page and No applications
  on this page on later pages, keeping Previous available. Clear previous-page
  rows when a new page fails rather than mislabel stale rows. Use the existing
  request-generation guard; ignore late responses and remove listeners on
  unmount. This protects UI state without validating backend record contents.

Done when applications from multiple companies appear in one paginated list
without first opening a company.

## Task C: Extract detail content and remove the Customers panel

Files:

- `src/features/bridge-capital/backoffice/components/OnboardingApplications.tsx`
- `src/features/bridge-capital/backoffice/screens/CustomersScreen.tsx`

Changes:

- [x] Move existing CompanySummary, PersonSummary, ApplicationDetails,
  DocumentsAndRequests, and SubmissionHistory helpers from the old component
  into new `components/OnboardingApplicationDetails.tsx` under the same
  back-office directory. Match the actual existing symbols when extracting;
  retain full field display, last-four ID masking, labels and formatting.
- [x] Extract existing date/status display helpers into
  `components/onboarding-format.ts` if shared by list/detail. Update imports;
  do not duplicate formatting or rewrite the detail presentation.
- [x] Remove detail-fetching, selected company/application, nested application
  list, and history state from the renamed list component. Task D owns them
  on the dedicated page. Keep ApplicationReviewActions as an existing reusable
  component, preserving its exact retry/receipt/confirmation behavior.
- [x] In `CustomersScreen.tsx`, remove the OnboardingCustomers import and
  mounted panel. Remove the extra layout wrapper only if unnecessary after
  the move. Keep the Demo portfolio heading and existing customer table,
  calculations, empty/loading state, and bridge-detail click behavior.
- [x] Remove any remaining imports/references to the old component name.
  Mount the live applications view only on Applications, so Customers no
  longer triggers onboarding API reads or displays their auth errors.

Done when details are extracted for reuse and no onboarding panel or
onboarding request is triggered by opening Customers.

## Task D: Add a dedicated application detail page

New files under `src/features/bridge-capital/backoffice/`:

- `screens/ApplicationDetailScreen.tsx`
- `components/OnboardingApplicationDetails.tsx` (extracted in Task C)

- [x] ApplicationDetailScreen receives `applicationId`, `listState`, and `go`.
  Render Back to applications above the header and extracted detail component.
  Back calls `go({screen: 'applications', listState})`. This replaces the list
  screen inside the shell; do not use a modal, drawer or expanded table row.
- [x] Detail component owns `application`, `submissions`, `busy`, `detailError`,
  `historyError` and a request-generation ref. On ID change, clear the previous
  record/history and call `api.getApplication(applicationId)`, followed by
  `api.listSubmissions(applicationId)`. Reuse existing client methods without
  adding new validation requirements or response schemas.
- [x] While loading show Loading application. A failed application read shows
  its actionable HTTP error plus Retry and Back. A history-only failure leaves
  the application visible with Retry history. Do not label failed history
  retrieval No submissions recorded; reserve that for a successful empty list.
- [x] Display Company from the fetched application's embedded data. For DRAFT
  without embedded Company, load `getCompany(company_id)` as the existing
  fallback. For submitted/RFI records, show unavailable historical details
  if their snapshot is absent; never replace it with today's live Company.
- [x] Render in order: application ID/status/revision/timestamps; existing
  ApplicationReviewActions; Company/People; platform/onboarding fields and
  agreements; documents/information requests; Submission history.
  Reuse Task C's extracted renderers and their omitted/unknown-value behavior.
- [x] Wire ApplicationReviewActions `onUpdated` to replace the detail record
  and reload submissions. Keep the action component mounted for the same ID
  so unresolved idempotency keys/receipts survive updates. Do not alter which
  actions the backend supports or infer approval from queue acceptance.
- [x] Refresh application reloads that ID in place and history. Preserve
  unresolved action state; ignore responses after ID changes/unmounting.
- [x] Returning to Applications reloads the saved cursor page, showing status
  updates without losing Previous history. Clicking the sidebar Applications
  entry starts at page 1. Browser reload uses existing shell reset behavior;
  browser Back/deep links are outside scope, no router is introduced.

Done when clicking a list row opens a distinct page containing all existing
application details/actions/history, with Back returning to the originating
list page.

## Verification performed and remaining scope

Bounded verification for Tasks A-D was requested and performed:

- `tsc --noEmit -p tsconfig.app.json --pretty false` passed.
- `npm run build` passed.
- Scoped Oxlint over the changed frontend/API files passed with no findings.
- `vitest run tests/onboarding/api.test.ts` passed all 17 tests. One existing
  API test was updated; no test case was added.
- `TestWorker_DistinctApplications_SameCompany_CreationCoordinated` passed
  once against a disposable logical database. An ephemeral overlay allowed
  only that disposable DB name through the test helper's safety guards. The DB
  was dropped afterward; the shared manual DB was unchanged. Comments were
  clarified after the run and were not rerun.
- `gofmt -s -l` and `git diff --check` passed for the selected fixture change.
- Markdown source checks passed for this completed plan.
- A dedicated headless browser used synthetic network routes, not the real API
  or DB. It rendered a 25-row first page, paged forward, opened a detail, backed
  to page two, and returned to page one. It also exercised history failure and
  retry, same-ID refresh preserving the unresolved submission attempt and its
  idempotency key, and Customers without an extra onboarding-list request.
- The real local preview loaded the Applications screen, but its unfiltered
  list remains blocked by a malformed synthetic REJECTED row with NULL
  `closed_at`. Its ID matches a test-fixture marker, but no reliable historical
  close time exists. The row was not modified or deleted; real-data detail and
  pagination were therefore unavailable.

Full-suite, broad backend, and final acceptance verification remain deferred.
No new test task, framework, dependency, filter, production fallback, or
fabricated close timestamp was added.

## Execution boundary

The implementation is isolated in `.worktrees/applications-navigation`, based
on local `develop` at `83bd34c`. The main checkout's untracked active plan,
consent backlog, and shortfall files remain preserved. This completed plan
copy moves to `docs/plans/completed/` in the reviewed UI commit. The selected
test-only fixture correction remains in its separate worktree and commit.

The lead reviewed both intended commit file lists and authorized local
integration. No push or deployment is authorized. Preserve the SQL alternative
worktree, shared/manual test data, and all local services.
