# FlowCard Treasury

A clickable demo of **bridge financing underwriting**: when a customer's card payment
is declined for insufficient funds, FlowCard decides — using fixed policy rules plus an
AI agent reading CRM notes — whether to front the shortfall from its lending facility.

> **Credit.** This project was originally built by
> [@yidi-li-ydl](https://github.com/yidi-li-ydl) in
> [yidi-li-ydl/flowcard-treasury](https://github.com/yidi-li-ydl/flowcard-treasury).
> This repository is a copy made to keep iterating on the frontend. The original commit
> history is preserved, so the first commit is hers.

**Live demo:** https://flowcard-treasury.vercel.app (anyone with the link can open it; the code stays private).

## Run it

Needs Node 20.19+ or 22.12+.

```bash
npm install
npm run dev        # http://localhost:5173
```

| Command           | What it does                                  |
| ----------------- | --------------------------------------------- |
| `npm run dev`     | Dev server with hot reload                    |
| `npm test`        | Policy, ledger, and Salesforce snapshot tests (Vitest) |
| `npm run test:acceptance` | PostgreSQL policy acceptance suite; requires `POLICY_ACCEPTANCE_DATABASE_URL` |
| `npm run build`   | Type-check + production build into `dist/`    |
| `npm run lint`    | Lint with oxlint                              |
| `npm run preview` | Serve the production build locally            |

The local demo Compose service sets `SALESFORCE_SNAPSHOT_MODE=demo`, which
publishes synthetic snapshots at startup and refreshes them daily. Production
Salesforce snapshots remain disabled until a trusted host MCP binding exists.
Run `npm run test:acceptance` to execute the database-backed policy acceptance
suite. It requires a local PostgreSQL database whose name includes `test`.

### Update the live demo

The demo is deployed from this folder with the Vercel CLI (account `slabarcaf`). Pushing to
GitHub does **not** redeploy it. After merging changes:

```bash
vercel deploy --prod
```

## Two versions: Kit 5 and GD original

A toggle at the top right switches between two versions. The choice is remembered in the browser.

| | **Kit 5** (default) | **GD original** |
| --- | --- | --- |
| Reserve floor | 20% → $10,000 floor, $40,000 lendable | 5% → $2,500 floor |
| Already lent out | $25,000 (deployable $15,000, Less) | $32,500 (deployable $15,000, Less) |
| Sequencing | Each decline is decided **the moment it arrives**, in arrival order, against the capital left at that moment | All declines land at once; each is decided when you click its row |
| Scenarios | Four, picked from the **Scenario** dropdown | One |
| Inputs | Editable in the **Scenario inputs** panel (slider icon) | Fixed in `seed.ts` |

Both versions remain deterministic browser demos. Kit 5 can optionally display
a read-only bridge ledger projection; its scenario continues to run from local
mock data. The browser does not hold Airwallex credentials or issue Airwallex
requests. The rules for Kit 5 come from the *Kit 5 Bridge Capital:
Source of Truth* doc. This version covers the floor and sequencing changes only;
the doc's other changes are not built yet.

## Local Airwallex bridge ledger

The same-repository TypeScript server stores the USD bridge ledger in PostgreSQL
and processes Airwallex operations through an outbox. It exposes a read-only
`GET /api/ledger` projection and the `POST /webhooks/airwallex` receiver. Kit 5
may read the projection by setting `VITE_BRIDGE_API_URL`; its scenario
behavior remains the deterministic local mock. The browser cannot authorize a
bridge or create a transfer command.

### Start the server locally

Use Node 20.19+ or 22.12+ and PostgreSQL. From the repository root, create a
local database, then apply the schema and demo facility seed:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/001_bridge_ledger.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/002_customer_roster.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/003_salesforce_snapshots.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/004_ai_evidence.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/005_underwriting_profiles.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/006_policy_evaluations.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/007_policy_request_evaluations.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/008_remote_authorization.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/009_policy_request_ledger_sequence.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/010_remote_authorization_nonce_fingerprint.sql
```

Set these server environment variables before starting the service:

| Variable | Required | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | Yes | PostgreSQL connection string. |
| `PORT` | No | HTTP port; defaults to `3000`. |
| `AIRWALLEX_CLIENT_ID` | Yes | Server-side Airwallex client ID. |
| `AIRWALLEX_API_KEY` | Yes | Server-side Airwallex API key. |
| `AIRWALLEX_WEBHOOK_SECRET` | Yes | Secret used to verify webhook signatures. |
| `AIRWALLEX_BASE_URL` | No | API base URL; defaults to the Airwallex sandbox. |
| `AIRWALLEX_REMOTE_AUTHORIZATION_SECRET` | Required for remote authorization | Shared signature secret; without it, remote authorization ingress is rejected. |
| `POLICY_DECISION_READ_TOKEN` | Required for decision reads | Bearer token for the authenticated server-side decision endpoint. No browser proxy is configured. |
| `REMOTE_AUTH_REPAYMENT_TERM` | Required to reserve a bridge | Must be `15_days`, `30_days`, or `asap`; missing or invalid values fail closed. |
| `POLICY_APPROVAL_ENABLED` | Optional, defaults disabled | Set to `true` only for the operator approval CLI. Approval still rechecks current policy and exact request terms. |

Keep the credentials on the server. For example, after exporting the variables
in the local shell, start the API with:

```bash
npm run server:start
```

This script builds the server and runs it on `PORT`. It does not apply database
migrations; apply the SQL migration first. For the browser projection, set
`VITE_BRIDGE_API_URL` to the server origin when starting Vite, for example
`http://localhost:3000`. Leave it unset to use the deterministic Kit 5 mock
without a ledger projection.

### Run the ledger demo with Docker

Docker Compose starts PostgreSQL and the ledger API. PostgreSQL applies all
three schema migrations and synthetic opening exposure when it initializes its data volume.
The Compose file uses placeholder credentials and a localhost-only Airwallex
URL; it is configured for the read-only ledger demo, not provider operations.

```bash
docker compose -p flowcard-ledger-demo up -d --build
docker compose -p flowcard-ledger-demo ps
curl http://localhost:3000/health
curl http://localhost:3000/api/ledger
```

The ledger API is available at `http://localhost:3000`. Inspect service logs
with `docker compose -p flowcard-ledger-demo logs -f ledger`. Stop the
containers with `docker compose -p flowcard-ledger-demo down`. The named
PostgreSQL volume preserves demo data when containers stop. All three
migrations run automatically only when PostgreSQL initializes an empty volume;
apply later schema changes using the documented SQL migration process.

### Webhook setup

Before accepting provider events, register a reachable HTTPS URL ending in
`/webhooks/airwallex` in the Airwallex account and configure its signing secret
as `AIRWALLEX_WEBHOOK_SECRET`. Subscribe to connected account transfer status
events, charge status events, settled deposit events, and issuing card
transaction declines. The receiver verifies the signature over the raw request
body and stores each provider event ID once.
Use Airwallex sandbox credentials and a sandbox webhook subscription for any
authorized sandbox exercise.

### Ledger and collection behavior

The seeded USD facility is $50,000 with a 20% reserve floor, $25,000 of
synthetic opening exposure, $15,000 deployable, and a zero operating buffer.
The seed is not an Airwallex transfer. V1 allows one open bridge per customer
and uses USD only. Reservations and provider commands are recorded together;
stable kebab-case request IDs are saved before provider calls and reused for
retries. Pending or suspended transfers retain their reservation; settled
transfers become principal. A failed transfer releases its reservation only
after provider confirmation of return.

Repayment supports 15 days, 30 days, a fixed date, and as soon as money
arrives.
For dated terms, once due, the bridge waits for the next settled deposit; it
does not draw existing wallet funds. Each collection attempt is capped at the
triggering settled deposit. Fees are assessed and fixed at the collection
trigger, then collected before principal. A partial deposit may pay part of the
fee; principal is queued only after the fee charge
settles. Balances decrease only when their charges settle.

### Salesforce customer snapshots

The snapshot service keeps a server-owned roster and publishes versioned
snapshots for explicitly enrolled customers. Demo mode uses synthetic fixtures.
Production is designed to use the read-only Salesforce MCP only; there is no
Salesforce REST fallback. Production remains gated until the org field and
picklist mappings, text limits, and unattended MCP capabilities are confirmed.
Live MCP access has not been verified.

Operators use the restricted roster CLI to enroll or soft-deactivate customers.
Deactivation stops future snapshots and new bridge decisions while retaining
history and allowing open-bridge servicing. A snapshot is fresh for at most 24
hours. See the [Salesforce snapshot operations guide](/docs/salesforce-snapshot-operations.md)
for commands, scheduling status, retries, reconciliation, readiness checks, and
the browser proxy requirement.

The production snapshot endpoint requires a bearer token. The browser client
does not send that token, and the app has no trusted same-origin server proxy.
Production browser reads therefore remain fail-closed. Never place the token in
a `VITE_` variable or expose it to the browser.

### AI evidence extraction

The server processes evidence only from a completed daily Salesforce snapshot.
Demo mode uses deterministic synthetic fixtures and does not call a model.
Extraction results and review history are append-only in PostgreSQL. The
versioned result contains a status, customer and snapshot IDs, typed signals,
unmapped claims, and model metadata. Each signal retains its value,
counterparty, exact quotes and source references, raw and calibrated scores,
alternatives, and selection rationale. Metadata identifies the model, prompt,
catalog, output schema, packet builder, and calibration versions. The active
proposed catalog is `proposed-v1`; it is not approved for production use.

`no_signal` is a successful empty result. `success` contains validated signal
claims. `review_required` and `unmapped` block new decisions for that customer
until a reviewer resolves the result. `failed` and oversized packets also block
that customer. Other customers continue. A valid signal reaches deterministic
policy only after its signal type independently qualifies: the one-sided exact
95% precision lower bound must be at least 95% on eligible rolling 90-day
labels. Each type starts review-only and returns to review-only when evidence
is insufficient or misses the bound. Calibration-fit examples are separate
from held-out qualification and audit examples.

The fixture server defaults to `AI_EVIDENCE_MODE=fixture`. The production
configuration loader fails closed unless the provider and data-handling terms
are approved and server-side `AI_EVIDENCE_PROVIDER_API_KEY` and
`AI_EVIDENCE_MODEL_ID` are set. A live production provider is not wired into
the runner. Do not send Salesforce text to a provider before those gates and
the signal catalog, Salesforce fields, and values are confirmed. Model
credentials must remain server-side. The demo runner is enabled only alongside
`SALESFORCE_SNAPSHOT_MODE=demo`; it processes the published synthetic snapshot.

The operator review CLI requires `DATABASE_URL` and
`AI_EVIDENCE_REVIEW_ENABLED=true`. It supports `list`, `inspect`, `confirm`,
`correct`, and `clear` and records reviewer identity and disposition without
calling policy or provider write operations. For example:

```bash
AI_EVIDENCE_REVIEW_ENABLED=true npm run ai-evidence:review -- list
npm run ai-evidence:review -- inspect --extraction-id <uuid>
npm run ai-evidence:review -- confirm --extraction-id <uuid> --reviewer-id <operator>
npm run ai-evidence:review -- correct --extraction-id <uuid> \
  --reviewer-id <operator> --signal-json '<validated-signal-json>'
npm run ai-evidence:review -- clear --extraction-id <uuid> \
  --reviewer-id <operator> --reason <reason>
```

Apply migration `004_ai_evidence.sql` after the snapshot migrations before
running the CLI or evidence runner. See the
[AI evidence implementation plan](/docs/plans/20261022-ai-evidence-pipeline-implementation-plan.md)
for the contracts, validation rules, test coverage, and remaining production
gates.

### Deterministic underwriting policy

The server joins the explicit customer roster, versioned Airwallex underwriting
profile, complete Salesforce snapshot, and AI evidence result for each daily
precheck. Salesforce owns account, contract, KYB, credit-review, approved-cap,
and prior-default facts. The Airwallex profile owns derived cash-flow,
concentration, balance, account-standing, and internal-ledger repayment
features. AI extraction proposes typed claims from bounded Salesforce evidence;
it cannot assign ratings, tiers, credit lines, bridge amounts, or transfer
commands. A valid `no_signal` result can pass the evidence-readiness gate.

The deterministic server policy owns ratings, tier, line, request outcome, and
review requirements. The bridge ledger owns deployable capacity, reservations,
open-bridge exposure, and its transactional outbox. Only an auto-approved
request or a human approval that is rechecked against current inputs can enter
the reservation transaction. An authenticated server-side caller submits only
a stored `requestId` to the decision endpoint using
`POLICY_DECISION_READ_TOKEN`. No browser proxy is configured. The browser
cannot submit trusted request details or create a provider command. Daily and
request evaluations retain policy and source-version references for audit.

Operators can inspect and approve pending human-review decisions with the
server CLI. Approval rechecks current policy and exact request terms, and can
fail closed when inputs or capacity have changed:

```bash
POLICY_APPROVAL_ENABLED=true npm run policy:approval -- list
POLICY_APPROVAL_ENABLED=true npm run policy:approval -- inspect --decision-id 'DECISION_ID'
POLICY_APPROVAL_ENABLED=true npm run policy:approval -- approve --decision-id 'DECISION_ID' --actor 'OPERATOR' --reason 'REASON'
```

Production decisioning stays disabled until Airwallex permissions, reports,
pagination, and account reads are verified; a trusted expected-deposit-date
source is mapped; industry, fraud, prior-default, and signal mappings are
approved and versioned; Jev signal definitions are settled and qualified; and
production authentication and authorization protect review and approval
operations. Unknown, stale, incomplete, unmapped, or unresolved inputs block
the affected customer while other customers continue.

### Kit 5 scenarios

| Scenario | What it shows |
| --- | --- |
| **Original** | GD's four customers on our facility. Bloom is held for the Salesforce note and declined at Tier C, Atlas is auto-funded, Nova goes to a credit officer (deployable $10,000 → $4,000), Pixel is ineligible. Nova repays on day 9 ($27 fee). |
| **Kit 5 target** | The doc's target demo: Original plus **Juniper Labs**, the same $6,000 request as Nova, arriving later at 9:16. Only $4,000 is left above the floor, so Juniper gets a firm no. |
| **First come, first served** | Two identical Tier A requests ($8,000 each). The first is funded; the second would breach the floor and is refused. After the first is repaid, the second customer's retried payment is funded. |
| **Tight capital → human** | Two identical Tier A requests ($3,000 each) with $6,500 deployable. The first auto-funds and drops capital into *Critical*, where Tier A needs a person, so the second goes to a credit officer. |

While a scenario plays, declines arrive about every 1.5 seconds. It stops when it needs you:
**Re-evaluate** (Salesforce note), **Continue** after an auto-approval (a green callout shows
what was funded), **Approve Bridge** (credit officer) or **Simulate Collection** (a repayment
on a later day).

### Statuses and dates

| Status | Meaning |
| --- | --- |
| **Declined** | Firm no for this request, with the rule that failed. |
| **Ineligible** | No credit line at all, e.g. Pixel: 41 days of history against the 90-day minimum. |
| **Pending approval** | Waiting on a credit officer. |
| **Funded** | Bridge sent and **still owed**. Shows the funding date and the *collect by* date. |
| **Repaid** | Fee and principal collected. Shows the repayment date. |

*Collect by* = funding date + 30 days. The customer's default term is "as soon as money
arrives", so we collect as soon as a deposit lands; 30 days is the latest we wait before the
bridge is past due. After that the late ladder applies (days 1–2 grace, days 3–15 line
frozen, day 16+ cards blocked), which is outside the demo. The 30 days is a placeholder until
the team settles collection timing (Open item 1 in the source-of-truth doc); change it per
request with **Collect within (days)** in the inputs panel, or `DEFAULT_TERM_DAYS` in
`src/kit5/engine.ts`.

In the inputs panel, changing a request's time changes who arrives first. In *First come, first
served*, set Cedar's time to 9:10 and Maple gets funded instead.

### GD original, click by click

1. **Start state.** A $50,000 facility with $32,500 already lent out, $15,000 deployable,
   $2,500 reserve floor. Capital band: *Less*.
2. **Scan Declines** pulls in four declined payments:

   | Customer     | Payment            | Shortfall | What the story shows                                                                                     |
   | ------------ | ------------------ | --------- | -------------------------------------------------------------------------------------------------------- |
   | Bloom & Co.  | $12,000 payroll    | $10,000   | The AI agent finds a Salesforce note (largest client disputing an invoice), re-scores Bloom from Tier A to Tier C, and the bridge ends up declined. |
   | Atlas SaaS   | $6,500 supplier    | $5,000    | Clean Tier A customer — auto-approved and funded.                                                                   |
   | Nova Agency  | $7,000 contractor  | $6,000    | Tier B at a *Less* band → needs a human to approve.                                                      |
   | Pixel Studio | $2,500 ad spend    | $2,000    | Only 41 days on platform (< 90 required) → ineligible.                                                   |

3. **Click a row** to run that customer through the underwriting pipeline (daily
   pre-check → per-request checks) and see the policy checks and rationale.
4. **Approve** Nova → funding completes after a short delay.
5. **Simulate Collection** shows the repayment waterfall. **Reset** starts over.

## How the code is organized

```
src/
  App.tsx              Version toggle: renders the Kit 5 or the GD dashboard
  main.tsx             React entry point
  index.css            Tailwind import + custom animations
  kit5/                Kit 5 version
    scenarios.ts       The four scenarios + loadScenarios() — where backend data will plug in
    types.ts           Scenario input shape (backend-ready JSON) and engine state
    engine.ts          Sequential engine: one pure function per step (arrive, decide, approve, repay)
    useKit5Dashboard.ts  Wires the engine to React; plays arrivals on a timer
    Kit5Dashboard.tsx  Page: header (scenario picker, toggle, buttons), capital bar, queue, feed
    SettingsPanel.tsx  The hidden "Scenario inputs" drawer
  gd/
    GdDashboard.tsx    GD's original page
  seed.ts              GD's demo data (Kit 5 reuses its customers and Salesforce note)
  state/
    machine.ts         GD's state machine
    types.ts           Dashboard state shape, shared by both versions
  hooks/
    useDashboard.ts    Wires GD's state machine to React
  components/          Shared UI blocks (Sidebar, CapitalBar, BridgeQueue, ActivityFeed, …)
  policy/              Underwriting rules as pure, tested functions (tiers, credit line, bands, fees)
  services/            Typed interfaces + mocks for Airwallex, CRM and the AI agent — NOT wired
                       into the UI yet (see docs/EDITING.md)
tests/policy/          Unit tests for policy/
tests/kit5/            Plays every Kit 5 scenario end to end and checks the outcomes
```

Data flows one way in both versions, and both render the same components:

- Kit 5: **`kit5/scenarios.ts` → `kit5/engine.ts` (uses `policy/`) → `useKit5Dashboard` → components**
- GD: **`seed.ts` → `state/machine.ts` (uses `policy/`) → `useDashboard` → components**

## Editing

See **[docs/EDITING.md](docs/EDITING.md)** for recipes: changing copy and numbers, adding a
customer, restyling, changing policy rules, and where to plug in real APIs.

## Tech

React 19, TypeScript, Vite, Tailwind CSS v4, Vitest, oxlint.
