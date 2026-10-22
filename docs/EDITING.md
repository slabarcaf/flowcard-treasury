# Editing the FlowCard Treasury frontend

A practical guide to changing the demo. Run `npm run dev` first; every save hot-reloads
in the browser.

## Where things live on screen

| On screen                                   | File                                         |
| ------------------------------------------- | -------------------------------------------- |
| Sidebar (logo + Dashboard)                  | `src/components/Sidebar.tsx`                 |
| Header (date, scenario picker, buttons), summary bar, auto-approval callout | `src/kit5/Kit5Dashboard.tsx` |
| "Bridge Facility" bar (lent / deployable / floor) | `src/components/CapitalBar.tsx`        |
| Declines table and expanded customer panel  | `src/components/BridgeQueue.tsx`, `CustomerDetail.tsx` |
| Underwriting flowchart (pre-check → per-request) | `src/components/UnderwritingPipeline.tsx` |
| Policy checklist                            | `src/components/PolicyChecks.tsx`            |
| Salesforce note + AI-extracted signals (Bloom) | `src/components/SalesforceNote.tsx`, `SignalExtraction.tsx` |
| Credit officer approval card                | `src/components/ApprovalCard.tsx`            |
| Repayment waterfall                         | `src/components/CollectionWaterfall.tsx`     |
| Right-hand Activity panel (collapsible) / pop-up toasts | `src/components/ActivityFeed.tsx`, `FeedToast.tsx` |
| "Scenario inputs" drawer                    | `src/kit5/SettingsPanel.tsx`                 |

`src/components/SummaryCard.tsx` (an end-of-demo outcome list with hard-coded text) exists
but is not rendered anywhere.

## Common changes

| To change… | Edit |
| --- | --- |
| Facility numbers, customers, requests, times, repayments | `src/kit5/scenarios.ts` |
| Scenario names and one-line summaries | `SCENARIOS` in `src/kit5/scenarios.ts` |
| How a request is decided, feed wording, approval card terms | `src/kit5/engine.ts` (`evaluate` and `decide`) |
| Bloom's orange "Agent found something in CRM." box | `decide()` in `src/kit5/engine.ts` (a pipeline step with `status: 'flag'`; its colors are `STATUS_STYLES.flag` in `UnderwritingPipeline.tsx`) |
| Speed of arrivals | `ARRIVAL_MS` in `src/kit5/useKit5Dashboard.ts` |
| Default collect-by window (30 days) | `DEFAULT_TERM_DAYS` in `src/kit5/engine.ts`, or `termDays` on a decline event |
| Auto-approval callout | `Kit5Dashboard.tsx` (the pause itself is `pause.kind === 'ack'` in `engine.ts`) |
| Profiles, Salesforce note text, AI rationales | `src/seed.ts` (`CUSTOMERS`, `BLOOM_CRM_NOTE`, `AI_EXTRACTED_SIGNALS`, `AGENT_RATIONALES`) |

### Add a scenario
Add an entry to `SCENARIOS` in `src/kit5/scenarios.ts` with a `customers` list and an `events`
list. Each `decline` event needs a unique `requestId`. A `repayment` event names the
`requestId` of the bridge it repays. Events can be listed in any order; the engine sorts
them by `day`, then `time`. Then add a test in `tests/kit5/engine.test.ts` that plays it and
checks the outcome.

### Change the look
Styling is Tailwind utility classes written directly on the elements (`className="…"`), so
change the classes in the relevant component. Brand colors are hard-coded hex values:
sidebar background `#1a1b2e` in `src/components/Sidebar.tsx`, primary button `#e04e2f` in
`src/kit5/Kit5Dashboard.tsx`. Custom animations (slide-in, fade) are in `src/index.css`.

Tailwind v4 is loaded with `@import "tailwindcss";` — there is no `tailwind.config.js`.

### Change the underwriting rules
Rules are pure functions in `src/policy/` and covered by tests in `tests/policy/policy.test.ts`:
- `tiers.ts` — signals → Tier A/B/C/none
- `creditLine.ts` — tier multipliers (30% / 20% / 10% of monthly inflow), per-customer cap
- `facility.ts` — capital bands and the **door table** (auto / human / closed per band × tier)
- `bridge.ts` — the per-request approval checks
- `fees.ts` — 1.5% per 30 days

After changing a rule, run `npm test` and update the tests that encode the old rule.
The scenarios assume the current rules, so changing them can change which customers get funded.

## How a click becomes a screen update

```
button in Kit5Dashboard.tsx / BridgeQueue.tsx
  → action in src/kit5/useKit5Dashboard.ts  (scan, approve, continue, collect, reset, …)
  → pure function in src/kit5/engine.ts     (takes old state, returns new state)
  → React re-renders components from the new state (src/kit5/types.ts → src/state/types.ts)
```

`useKit5Dashboard` calls `step()` on a timer. Each step takes the next event in arrival
order. A decline is decided immediately against the capital left at that moment. The timer
stops while `state.pause` is set (Salesforce re-evaluation, a credit officer, or an
auto-approval waiting for **Continue**), and before a repayment, which the presenter
triggers with **Simulate Collection**.

To add a new interaction: write a function in `engine.ts`, expose it from
`useKit5Dashboard.ts`, and call it from a component.

## Connecting the backend

`loadScenarios()` in `src/kit5/scenarios.ts` is the single place scenario data enters.
Replace its body with a call to the backend that returns `Scenario[]` in the shape defined in
`src/kit5/types.ts` (plain JSON: facility, already lent out, customers, events). The inputs
drawer edits a copy of that same object, so it keeps working.

`src/services/` already defines typed interfaces for **Airwallex** (card spend, balance,
transfers, charges, deposits), a **CRM** (latest account notes) and the **AI agent**
(signal extraction, allocation proposal, rationale), with mock implementations under
`src/services/mock/`. Nothing in the UI calls them today. Any real API key must stay on a
server, never in this frontend.

## Before you commit

```bash
npm run lint && npm test && npm run build
```

All three should pass cleanly.
