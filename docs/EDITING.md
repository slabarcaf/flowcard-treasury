# Editing the FlowCard Treasury frontend

There are two versions behind the toggle at the top right. **Kit 5** is the default; **GD
original** is Yidi's build, kept as it was. Most of this guide below the Kit 5 section is about
GD's files.

A practical guide to changing the demo. Run `npm run dev` first; every save hot-reloads
in the browser.

## Where things live on screen

| On screen                                   | File                                         |
| ------------------------------------------- | -------------------------------------------- |
| Sidebar, logo, nav items                    | `src/components/Sidebar.tsx`                 |
| GD header buttons and layout                | `src/gd/GdDashboard.tsx`                     |
| "Bridge Facility" bar (lent / deployable / floor) | `src/components/CapitalBar.tsx`        |
| Declines table and expanded customer panel  | `src/components/BridgeQueue.tsx`, `CustomerDetail.tsx` |
| Underwriting flowchart (pre-check → per-request) | `src/components/UnderwritingPipeline.tsx` |
| Policy checklist                            | `src/components/PolicyChecks.tsx`            |
| Salesforce note + AI-extracted signals (Bloom) | `src/components/SalesforceNote.tsx`, `SignalExtraction.tsx` |
| Human approval card (Nova)                  | `src/components/ApprovalCard.tsx`            |
| Repayment waterfall                         | `src/components/CollectionWaterfall.tsx`     |
| Right-hand Activity column / pop-up toasts  | `src/components/ActivityFeed.tsx`, `FeedToast.tsx` |

The sidebar items other than **Dashboard** are visual only — they don't route anywhere yet.
`src/components/SummaryCard.tsx` (an end-of-demo outcome list with hard-coded text) exists
but is not rendered anywhere.

## Kit 5 version (default)

Everything Kit 5 lives in `src/kit5/`. It reuses GD's components, so a visual change to the
queue, capital bar or feed shows up in both versions.

| To change… | Edit |
| --- | --- |
| Facility numbers, customers, requests, times, repayments | `src/kit5/scenarios.ts` |
| Scenario names and one-line summaries | `SCENARIOS` in `src/kit5/scenarios.ts` |
| How a request is decided, feed wording, approval card terms | `src/kit5/engine.ts` (`evaluate` and `decide`) |
| Speed of arrivals | `ARRIVAL_MS` in `src/kit5/useKit5Dashboard.ts` |
| Default collect-by window (30 days) | `DEFAULT_TERM_DAYS` in `src/kit5/engine.ts`, or `termDays` on a decline event |
| Auto-approval callout | `Kit5Dashboard.tsx` (the pause itself is `pause.kind === 'ack'` in `engine.ts`) |
| Header, scenario picker, status line | `src/kit5/Kit5Dashboard.tsx` |
| Fields in the inputs drawer | `src/kit5/SettingsPanel.tsx` |
| Toggle labels or default version | `src/components/VersionToggle.tsx`, `src/App.tsx` |

### Add a scenario
Add an entry to `SCENARIOS` in `src/kit5/scenarios.ts` with a `customers` list and an `events`
list. Each `decline` event needs a unique `requestId`. A `repayment` event names the
`requestId` of the bridge it repays. Events can be listed in any order; the engine sorts
them by `day`, then `time`. Then add a test in `tests/kit5/engine.test.ts` that plays it and
checks the outcome.

### Connect the backend
`loadScenarios()` in `src/kit5/scenarios.ts` is the single place scenario data enters.
Replace its body with a call to the backend that returns `Scenario[]` in the shape defined in
`src/kit5/types.ts` (plain JSON: facility, already lent out, customers, events). The inputs
drawer edits a copy of that same object, so it keeps working.

### How the sequence runs
`useKit5Dashboard` calls `step()` on a timer. Each step takes the next event in arrival
order. A decline is decided immediately against the capital left at that moment. The timer
stops while `state.pause` is set (Salesforce re-evaluation, a credit officer, or an
auto-approval waiting for **Continue**), and before a
repayment, which the presenter triggers with **Simulate Collection**.

## GD original version: common changes

### Change wording, numbers, or the demo story
Almost all of it is in **`src/seed.ts`**:
- `FACILITY_CONFIG` — facility size, reserve floor %, per-customer cap, fee rate, minimum history days
- `CUSTOMERS` — each customer's monthly inflow, signal ratings, days of history, no-go flags
- `DECLINED_TRANSACTIONS` — the four declined payments (amount, wallet balance, shortfall, time)
- `SEEDED_OUTSTANDING` — how much is already lent out at the start ($32,500)
- `BLOOM_CRM_NOTE`, `AI_EXTRACTED_SIGNALS`, `AGENT_RATIONALES` — the "AI" text shown in the UI

Activity-feed messages and step labels are written in **`src/state/machine.ts`** (search for
the text you see on screen). The date in the header is `demoClock` in `initialState()`.

### Change the look
Styling is Tailwind utility classes written directly on the elements (`className="…"`), so
change the classes in the relevant component. Brand colors are hard-coded hex values:
- sidebar background `#1a1b2e` in `src/components/Sidebar.tsx`; primary button `#e04e2f` in
  `src/gd/GdDashboard.tsx` and `src/kit5/Kit5Dashboard.tsx`
- custom animations (slide-in, fade) in `src/index.css`

Tailwind v4 is loaded with `@import "tailwindcss";` — there is no `tailwind.config.js`.

### Add a customer to the story
1. Add a profile to `CUSTOMERS` and a decline to `DECLINED_TRANSACTIONS` in `src/seed.ts`
   (use a new `acct_…` id in both).
2. Add a rationale for it under `AGENT_RATIONALES` (the key must match the `CUSTOMERS` key).
3. That's usually enough: `processCustomer()` in `src/state/machine.ts` runs every customer
   through the policy functions, so the outcome (funded / needs approval / declined /
   ineligible) follows from the numbers. Only Bloom has special-case code (`key === 'bloom'`)
   for the CRM-note reveal.

### Change the underwriting rules
Rules are pure functions in `src/policy/` and covered by tests in `tests/policy/policy.test.ts`:
- `tiers.ts` — signals → Tier A/B/C/none
- `creditLine.ts` — tier multipliers (30% / 20% / 10% of monthly inflow), per-customer cap
- `facility.ts` — capital bands and the **door table** (auto / human / closed per band × tier)
- `bridge.ts` — the per-request approval checks
- `fees.ts` — 1.5% per 30 days

After changing a rule, run `npm test` and update the tests that encode the old rule.
Note that the seeded story assumes the current rules, so changing them can change which
customers get funded.

## How a click becomes a screen update

```
button in gd/GdDashboard.tsx / BridgeQueue.tsx
  → action in src/hooks/useDashboard.ts   (scan, selectCustomer, approve, collect, reset)
  → pure function in src/state/machine.ts (takes old state, returns new state)
  → React re-renders components from the new DashboardState (src/state/types.ts)
```

To add a new interaction: write a function in `machine.ts`, expose it from
`useDashboard.ts`, and call it from a component.

## Airwallex bridge ledger

The server in `server/src/` owns the PostgreSQL bridge ledger, provider
operations, transactional outbox, and webhook inbox. Its browser API currently
exposes only the read-only `GET /api/ledger` projection. Kit 5 remains a
deterministic local mock; setting `VITE_BRIDGE_API_URL` to the server origin
displays the ledger projection beside that scenario. Leaving it unset keeps Kit
5 on mock data without a server projection.

The browser funding command remains unavailable until the policy module can
submit a trusted server-side approval. Do not treat a browser decision or the
Kit 5 UI as authority to fund a bridge. The server does not expose a funding
route for the browser.

### Run the local server

Use Node 20.19+ or 22.12+ and a PostgreSQL database. The repository contains one
SQL schema and seed migration; the server does not run migrations automatically.
Apply it from the repository root:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f server/src/db/migrations/001_bridge_ledger.sql
```

The server requires `DATABASE_URL`, `AIRWALLEX_CLIENT_ID`,
`AIRWALLEX_API_KEY`, and `AIRWALLEX_WEBHOOK_SECRET`. `PORT` is optional and
defaults to `3000`.
`AIRWALLEX_BASE_URL` is optional and defaults to
`https://api.sandbox.airwallex.com`. Set the credentials in the server process
environment, then run:

```bash
npm run server:start
```

The command builds the TypeScript server and starts it. It does not apply the
SQL migration. Keep Airwallex credentials on the server; the browser API client
makes only a GET request for the ledger projection.

### Run the read-only demo with Docker

From the repository root, run:

```bash
docker compose -p flowcard-ledger-demo up -d --build
```

Compose starts PostgreSQL and the ledger API, then applies the SQL seed when
PostgreSQL creates its empty data volume. The local Compose configuration uses
placeholder Airwallex values and a localhost-only base URL. It does not make
provider calls. Open `http://localhost:3000/api/ledger` to inspect the seeded
projection, or check `http://localhost:3000/health` for service health.

Use `docker compose -p flowcard-ledger-demo ps` to check service state and
`docker compose -p flowcard-ledger-demo logs -f ledger` to follow API logs.
Stop the services with `docker compose -p flowcard-ledger-demo down`. The named
database volume persists after stopping; the SQL seed only runs again if a new
empty volume is created.

### Webhook prerequisites

Register a reachable HTTPS endpoint ending in `/webhooks/airwallex` in the
Airwallex account. Configure the matching signing secret as
`AIRWALLEX_WEBHOOK_SECRET` and subscribe to connected account transfer status
events, charge status events, settled deposit events, and issuing card
transaction declines. The receiver verifies signatures against the raw request
body and stores each provider event ID once.
Use a sandbox webhook subscription and sandbox credentials for authorized
sandbox exercises.

### V1 ledger and repayment behavior

The seeded USD facility is $50,000 with a 20% reserve floor, $25,000 in
synthetic opening exposure, $15,000 deployable, and a zero operating buffer.
The opening exposure is not an Airwallex transfer. V1 supports one open bridge
per customer and USD only. The ledger is append-only; derived facility and
bridge balances are projections. A funding reservation, provider operation,
and outbox command are persisted together. Stable kebab-case request IDs are
saved before provider calls and reused for retries.

Pending and suspended transfers hold their reservation. A settled transfer
becomes principal. A failed transfer releases its reservation only after
Airwallex confirms the return. Repayment terms are 15 days, 30 days, a fixed
date, or as soon as money arrives. For dated terms, a bridge becomes eligible at
its due time and waits for the next settled deposit. Collection does not use
existing wallet funds. Each attempt is capped at the lesser of the triggering
settled deposit and the available USD balance.

The fee is assessed once at the collection trigger and remains fixed. Fees are
charged before principal. A partial deposit can pay part of the fee; principal
is queued only after the fee charge settles. Outstanding balances decrease only
after their charges settle.

## Before you commit

```bash
npm run lint && npm test && npm run build
```

All three should pass cleanly.
