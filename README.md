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
| `npm test`        | Policy + Kit 5 engine tests (Vitest, 64 tests)|
| `npm run build`   | Type-check + production build into `dist/`    |
| `npm run lint`    | Lint with oxlint                              |
| `npm run preview` | Serve the production build locally            |

### Update the live demo

The demo is on Vercel (account `slabarcaf`, project `flowcard-treasury`), which is linked to
this GitHub repo: pushing a branch creates a preview deploy, and merges to `main` should update
production. If https://flowcard-treasury.vercel.app doesn't show a merged change after a minute,
deploy it by hand from this folder:

```bash
vercel deploy --prod
```

## The demo (Kit 5)

- **Facility:** $50,000 with a 20% reserve floor → $10,000 floor, $40,000 lendable.
  $25,000 is already lent out, so $15,000 is deployable (*Less*).
- **Sequencing:** each decline is decided **the moment it arrives**, in arrival order,
  against the capital left at that moment.
- **Scenarios:** four, picked from the **Scenario** dropdown in the header.
- **Inputs:** every number is editable in the **Scenario inputs** drawer (slider icon).

It's scripted: no backend, no API keys, nothing leaves the browser. The rules come from the
*Kit 5 Bridge Capital: Source of Truth* doc. This build covers the floor and sequencing
changes; the doc's other changes are not built yet.

GD's original version (5% floor, rows decided on click) was removed from the UI on
2026-10-21. It is still in the git history at commit `b7e1c91`.

### Scenarios

| Scenario | What it shows |
| --- | --- |
| **Original** | GD's four customers on our facility. Bloom arrives with an orange **"Agent found something in CRM."** box, is re-evaluated against the Salesforce note and declined at Tier C. Atlas is auto-funded, Nova goes to a credit officer (deployable $10,000 → $4,000), Pixel is ineligible. Nova repays on day 9 ($27 fee). |
| **Kit 5 target** | The doc's target demo: Original plus **Juniper Labs**, the same $6,000 request as Nova, arriving later at 9:16. Only $4,000 is left above the floor, so Juniper gets a firm no. |
| **First come, first served** | Two identical Tier A requests ($8,000 each). The first is funded; the second would breach the floor and is refused. After the first is repaid, the second customer's retried payment is funded. |
| **Tight capital → human** | Two identical Tier A requests ($3,000 each) with $6,500 deployable. The first auto-funds and drops capital into *Critical*, where Tier A needs a person, so the second goes to a credit officer. |

While a scenario plays, declines arrive about every 1.5 seconds. It stops when it needs you:
**Re-evaluate** (Salesforce note), **Continue** after an auto-approval (a green callout shows
what was funded), **Approve Bridge** (credit officer) or **Simulate Collection** (a repayment
on a later day).

On screen:
- **Top left:** the demo date, large, with *Funding day* (day 0) or *Collection day · +N days*.
- **Summary bar:** the current step, plus a *Funded:* line listing every funded client with
  its amount and collect-by or repaid date.
- **Activity panel (right):** collapse it with the chevron to give the queue the full width;
  the choice is remembered in the browser. On narrow screens the queue also scrolls sideways.

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

## How the code is organized

```
src/
  App.tsx              Renders the Kit 5 dashboard
  main.tsx             React entry point
  index.css            Tailwind import + custom animations
  kit5/
    scenarios.ts       The four scenarios + loadScenarios() — where backend data will plug in
    types.ts           Scenario input shape (backend-ready JSON) and engine state
    engine.ts          Sequential engine: one pure function per step (arrive, decide, approve, repay)
    useKit5Dashboard.ts  Wires the engine to React; plays arrivals on a timer
    Kit5Dashboard.tsx  Page: header (date, scenario picker, buttons), summary, capital bar, queue, feed
    SettingsPanel.tsx  The hidden "Scenario inputs" drawer
  seed.ts              Customer profiles, Salesforce note and AI text reused by the scenarios
  state/types.ts       Dashboard state shape the components render
  components/          UI blocks (Sidebar, CapitalBar, BridgeQueue, ActivityFeed, …)
  policy/              Underwriting rules as pure, tested functions (tiers, credit line, bands, fees)
  services/            Typed interfaces + mocks for Airwallex, CRM and the AI agent — NOT wired
                       into the UI yet (see docs/EDITING.md)
tests/policy/          Unit tests for policy/
tests/kit5/            Plays every scenario end to end and checks the outcomes
```

Data flows one way: **`kit5/scenarios.ts` → `kit5/engine.ts` (uses `policy/`) → `useKit5Dashboard` → components.**

## Editing

See **[docs/EDITING.md](docs/EDITING.md)** for recipes: changing numbers and scenarios,
restyling, changing policy rules, and where to plug in the backend.

## Tech

React 19, TypeScript, Vite, Tailwind CSS v4, Vitest, oxlint.
