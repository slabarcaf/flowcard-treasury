# FlowCard Treasury

A clickable demo of **bridge financing underwriting**: when a customer's card payment
is declined for insufficient funds, FlowCard decides — using fixed policy rules plus an
AI agent reading CRM notes — whether to front the shortfall from its lending facility.

> **Credit.** This project was originally built by
> [@yidi-li-ydl](https://github.com/yidi-li-ydl) in
> [yidi-li-ydl/flowcard-treasury](https://github.com/yidi-li-ydl/flowcard-treasury).
> This repository is a copy made to keep iterating on the frontend. The original commit
> history is preserved, so the first commit is hers.

## Run it

Needs Node 20.19+ or 22.12+.

```bash
npm install
npm run dev        # http://localhost:5173
```

| Command           | What it does                                  |
| ----------------- | --------------------------------------------- |
| `npm run dev`     | Dev server with hot reload                    |
| `npm test`        | Policy unit tests (Vitest, 53 tests)          |
| `npm run build`   | Type-check + production build into `dist/`    |
| `npm run lint`    | Lint with oxlint                              |
| `npm run preview` | Serve the production build locally            |

## The demo, click by click

The whole demo is scripted — no backend, no API keys, nothing leaves the browser.

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
  App.tsx              Page shell: sidebar, header buttons, layout
  main.tsx             React entry point
  index.css            Tailwind import + custom animations
  seed.ts              ALL demo data: facility size, customers, declines, CRM note, AI text
  state/
    machine.ts         Demo state machine — every step of the story is a pure function here
    types.ts           Shape of the dashboard state
  hooks/
    useDashboard.ts    Wires the state machine to React (scan, select, approve, collect, reset)
  components/          One file per UI block (CapitalBar, BridgeQueue, ActivityFeed, …)
  policy/              Underwriting rules as pure, tested functions (tiers, credit line, bands, fees)
  services/            Typed interfaces + mocks for Airwallex, CRM and the AI agent — NOT wired
                       into the UI yet (see docs/EDITING.md)
tests/policy/          Unit tests for policy/
```

Data flows one way: **`seed.ts` → `state/machine.ts` (uses `policy/`) → `useDashboard` → components.**

## Editing

See **[docs/EDITING.md](docs/EDITING.md)** for recipes: changing copy and numbers, adding a
customer, restyling, changing policy rules, and where to plug in real APIs.

## Tech

React 19, TypeScript, Vite, Tailwind CSS v4, Vitest, oxlint.
