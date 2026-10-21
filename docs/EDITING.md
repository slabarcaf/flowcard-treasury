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

## Connecting real APIs (not done yet)

`src/services/` already defines typed interfaces for **Airwallex** (card spend, balance,
transfers, charges, deposits), a **CRM** (latest account notes) and the **AI agent**
(signal extraction, allocation proposal, rationale), with mock implementations under
`src/services/mock/`. Nothing in the UI calls them today — the state machine reads
`seed.ts` directly. Wiring them in means making the `machine.ts` steps async and fetching
through these clients instead of reading seed constants. Any real API key must stay on a
server, never in this frontend.

## Before you commit

```bash
npm run lint && npm test && npm run build
```

All three should pass cleanly.
