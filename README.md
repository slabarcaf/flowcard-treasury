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
| `npm run build`   | Type-check + production build into `dist/`    |
| `npm run lint`    | Lint with oxlint                              |
| `npm run preview` | Serve the production build locally            |

The local demo Compose service sets `SALESFORCE_SNAPSHOT_MODE=demo`, which
publishes synthetic snapshots at startup and refreshes them daily. Production
Salesforce snapshots remain disabled until a trusted host MCP binding exists.

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
behavior remains the deterministic local mock. The Kit 5 sandbox-live route is
fail-closed until real customer mappings and policy/ledger composition are
verified. Local implementation tests use fixture aliases and a mocked Airwallex
gateway; they do not make provider calls. No browser funding command is
available until the policy module can submit a trusted server-side approval.

### Start the server locally

Use Node 20.19+ or 22.12+ and PostgreSQL. From the repository root, create a
local database, then apply the schema and demo facility seed:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/001_bridge_ledger.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/002_customer_roster.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/003_salesforce_snapshots.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/004_ai_evidence.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/005_kit5_card_attempts.sql
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
| `KIT5_OPERATOR_TOKEN` | For Kit 5 actions | Server-only operator credential, at least 32 characters. |
| `KIT5_ALLOWED_ORIGINS` | For Kit 5 actions | Comma-separated exact browser origins allowed to authenticate. |

Keep the credentials on the server. `PORT` defaults to `3000`. The existing
ngrok tunnel forwards to port `8000`, so use this port for the webhook-capable
local server:

```bash
PORT=8000 npm run server:start
```

Point the local Kit 5 frontend at that server:

```bash
VITE_BRIDGE_API_URL=http://localhost:8000 npm run dev
```

Set `KIT5_OPERATOR_TOKEN` and `KIT5_ALLOWED_ORIGINS` in the server process.
Include the exact frontend origin in the allowlist. Enter the operator token
in the dashboard at runtime. The server issues a short-lived `HttpOnly`,
`Secure`, `SameSite=None` session cookie. The token is never embedded in the
frontend bundle or stored by the browser client. Missing authentication
configuration disables Kit 5 transaction actions. The Airwallex webhook route
continues to use raw-body signature verification and does not require a browser
session.

This script builds the server and runs it on `PORT`. It does not apply database
migrations; apply all five SQL migrations first. Leave
`VITE_BRIDGE_API_URL` unset to use the deterministic Kit 5 mock without a
ledger projection.

### Run the ledger demo with Docker

Docker Compose starts PostgreSQL and the ledger API. PostgreSQL applies
migrations 001 through 003 and synthetic opening exposure when it initializes
its data volume. Apply migrations 004 and 005 manually using the instructions
above for the current application schema.
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
PostgreSQL volume preserves demo data when containers stop. Migrations 001
through 003 run automatically only when PostgreSQL initializes an empty
volume. Compose does not apply migrations 004 and 005.

### Webhook setup

Before accepting provider events, verify the existing public HTTPS ngrok URL
ending in `/webhooks/airwallex` forwards to
`http://localhost:8000/webhooks/airwallex`. Subscribe to connected account
transfer events (`connected_account_transfer.new`, `.pending`, `.settled`,
`.suspended`, `.failed`), charge events (`charge.new`, `.pending`, `.settled`,
`.suspended`, `.failed`), deposit events (`deposit.pending`, `.settled`,
`.rejected`, `.reversed`), and issuing events
(`issuing.transaction.succeeded`, `.failed`). The receiver verifies HMAC-SHA256
over the raw body using `x-timestamp` and the notification URL's secret,
deduplicates by event ID, and promptly acknowledges verified requests with HTTP
200. Airwallex may deliver events out of order. Keep secrets server-side and use
a sandbox subscription.

The final submission uses sandbox-live mode. Each **Next customer** action
creates one issuing simulation for the mapped card, using the scenario amount
resolved by the server. The video can use deterministic fixture mode. Bloom's
$9,000 attempt is below the $9,999.99 simulation limit, and the mapped card
must also allow that amount. The current route remains fail-closed while these
implementation prerequisites are open:

- Connect the active Kit 5 policy adapter as the approval and amount source.
- Compose policy approval with durable bridge reservation, transfer settlement,
  and the separate card retry.
- The typed server-side Nova Global Account deposit-simulation operation and
  mocked tests exist. Compose it with webhook settlement and fee-first then
  principal collection before treating Nova's path as production-ready.

After those prerequisites are implemented, external sandbox acceptance checks
are real customer mappings, verified scoped API permissions and product
enablement, Bloom's natural provider `INSUFFICIENT_FUNDS` result, configured
webhook delivery with signature validation and HTTP 200 acknowledgement, and
provider record retrieval and reconciliation. See the
[Kit 5 demo flow and sandbox gates](/docs/airwallex-kit5-demo-flow-draft.md).

The sandbox demo uses direct server-side REST calls, not the Airwallex AgentOS
production connector. The seven Airwallex environment variable names are
`AIRWALLEX_API_KEY`, `AIRWALLEX_CLIENT_ID`, `AIRWALLEX_ORG_ID`,
`AIRWALLEX_ACCOUNT_ID`, `AIRWALLEX_WEBHOOK_EVENTS_FROM`,
`AIRWALLEX_WEBHOOK_ID`, and `AIRWALLEX_WEBHOOK_SECRET`. The ignored root `.env`
exists only in the main checkout. Never copy it into a worktree, deployment,
browser variable, fixture, source control, or log.

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
that customer. The latest extraction's gate result is persisted, and bridge
reservation checks it inside the reservation transaction. A reviewer
disposition releases a `review_required` or `unmapped` result; failed
extractions and unqualified signals stay blocked until a later run resolves
the gate. Other customers continue. A valid signal reaches deterministic
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
[AI evidence implementation plan](/docs/plans/completed/20261022-ai-evidence-pipeline-implementation-plan.md)
for the contracts, validation rules, test coverage, and remaining production
gates.

### Kit 5 scenarios

| Scenario | What it shows |
| --- | --- |
| **Original** | GD's four customers on our facility. Bloom's $9,000 supplier attempt, $2,000 opening USD balance, and $7,000 shortfall are declined at Tier C. Atlas is auto-funded, Nova goes to a credit officer, Pixel is ineligible, and Nova repays on day 9 ($27 fee). |
| **Kit 5 target** | Original plus **Juniper Labs**, whose $6,000 request arrives at 9:16. Bloom keeps the same $9,000 attempt, $2,000 opening USD balance, and $7,000 shortfall. Only $4,000 remains above the floor, so Juniper gets a firm no. |
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
