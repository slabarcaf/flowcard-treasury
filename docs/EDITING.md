# Editing the FlowCard Treasury frontend

A practical guide to changing the demo. Run `npm run dev` first; every save hot-reloads
in the browser.

## Where things live on screen

| On screen                                   | File                                         |
| ------------------------------------------- | -------------------------------------------- |
| Sidebar, logo, nav items, header buttons    | `src/App.tsx`                                |
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

## Common changes

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
- sidebar background `#1a1b2e` and primary button `#e04e2f` in `src/App.tsx`
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
button in App.tsx / BridgeQueue.tsx
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
