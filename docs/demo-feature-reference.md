# FlowCard — feature reference for the demo

Written for whoever builds the hackathon walkthrough. It describes what each feature **is**,
what it **reads**, and what is **real versus mocked**. It is not a shot list — pick the order
and the story yourself.

Branch: `feature/bridge-capital` · as-of date inside the demo: **3 November 2026**
Verified against the running app on 15 November 2026.

---

## 1. The product in one paragraph

FlowCard is a corporate-card platform built on Airwallex. Each SMB customer is a connected
account with its own wallet. When a card payment is declined for insufficient funds, FlowCard
offers a **bridge**: it funds the shortfall from platform capital and collects it back from the
customer's next deposits, with a fee. Two surfaces: a **back office** for the credit and ops
team, and a **customer portal**. Everything that decides anything lives in one pure policy
engine, written against `docs/bridge-capital-spec.md`.

The honest one-line pitch: *the underwriting decision is the product*. The screens exist to
show a decision being made and to explain it to both sides.

---

## 2. What is real and what is not — read this before scripting anything

| Thing | State |
|---|---|
| Policy engine (tiers, limits, 7 rules, pricing, regimes, DPD ladder) | **Real.** Pure functions, 83 passing tests. |
| Bridge Capital portal + back office | **Real UI, mock data.** Every number is computed by the engine from a seeded generator. |
| Demo customer data | **Generated, deterministic.** 180 days of balances, 24 months of invoices, ~12 months of card spend per customer. |
| Money movement | **Mocked Airwallex client.** Transfers and charges return Airwallex-shaped responses; no money moves. |
| Onboarding wizard | **Real UI, local state only.** It calls no API; `useOnboarding` holds the form in React state. |
| Backend (`server/`) | **Exists and is substantial** — Postgres, ledger, Airwallex, Salesforce, underwriting, onboarding, webhooks. **Bridge Capital does not call it.** |
| Kit 5 dashboard (the older demo) | **Real UI, and the only front end wired to the API** (`VITE_BRIDGE_API_URL`). |
| CRM / Salesforce | Contract defined server-side, **field mapping unverified**, not wired to Bridge Capital. See §7. |

Front-end tests: **83 Bridge Capital + 87 Kit 5, all passing.** Build is clean.
Backend tests: **23 failing** in `tests/server/` as of today — Kit 5 Airwallex fixture
checkpoint cases, plus 2 that fail only because a Go test server will not start locally. None of
them touch the demo surfaces. Worth knowing before anyone claims "all green".

**Do not claim on camera:** that money moves, that the portal is reading a live ledger, that CRM
changes a credit line today, or that onboarding creates an account anywhere.

---

## 3. How to reach each surface

No router — `src/App.tsx` switches on state, with URL hashes as shortcuts.

- `/` — onboarding wizard, then the Kit 5 dashboard with two floating entry buttons
- `/#bridge-capital` — back office, skipping onboarding
- `/#portal` — customer portal, skipping onboarding

The hash is read **on mount only**: changing it in the address bar needs a reload.

---

## 4. Surface A — onboarding wizard

Six steps, in `src/components/onboarding/`. Animated aurora background, light theme (it predates
the dark restyle of the other two surfaces).

1. **Get started** — "Let's get started!"
2. **Create account** — "Enter email address"; first name, last name, email, password
3. **Company name** — "What's your company called?"
4. **Compliance check** — six exclusion checkboxes (excluded industry, gambling, crypto exchange,
   fraud/AML, OFAC sanctions, not a registered entity). Checking any one shows a red ineligibility
   warning and disables the continue button. The button reads "None of the above →".
5. **Company info** — "Tell us about your business"; legal name, country, phone, website, EIN,
   industry, company type
6. **Welcome** — hands off to the dashboard

The compliance step is the one with real behaviour worth showing: it is a hard gate, and it is the
only place the wizard says no.

---

## 5. Surface B — customer portal

Desktop layout with a left sidebar (it was phone-first until 10 November; it is now a sidebar app).
A customer picker in the header switches between the four demo businesses. Ten destinations:

**Home** — wallet balance; money in and out over the last 30 days; **banners for each declined
payment** with a "See options" button; and the pre-approved figure: *"You can bridge up to
$12,000 — Tier A · $3,000 in use of $24,096"*, with **Plan a bridge**.
The pre-approved number is the smaller of (limit − outstanding) and (capital available above the
reserve floor). It is computed with the same inputs as eligibility rules 1, 2, 3 and 5, so the
figure shown can never promise more than a request would actually get. It reads $0 with no
history, with a past-due bridge, or at the floor.

**Cash flow** — a 60-day forecast. Starts from today's wallet balance, adds each expected
receivable on the day that payer usually pays (due date + their average lateness), and subtracts
average daily card spend over the last 90 days. A **low line** at 10% of the 90-day average
balance; the first day the forecast crosses it is called out, with the option to plan a bridge for
that gap. §8.1 — placeholder thresholds.

**Bridges** — every bridge, running and closed. A running one shows status, amount due, a
**Repay** action, and one sentence about the current card restriction. Closed ones show the
outcome and what they cost. Planned bridges start here too.

**Bills** — supplier bills with due dates, and **Flex**: pay a bill with a bridge over 30, 60 or
90 days. A Flex request is an ordinary bridge request with a stated purpose — the bill's merchant
category sets its criticality, and all the §4 rules, §5 pricing and acceptance binding, §6 regimes
and §7 approvals apply unchanged. Nothing about Flex bypasses the engine.

**Receivables** — expected inflows with each payer's on-time rate, average days late, and
relationship length. This is the evidence behind eligibility rule 7.

**Cards** — issued team and virtual cards: holder, role, last four, monthly cap, spent this month,
and blocked merchant categories the customer can toggle (travel, entertainment, SaaS, professional
services, utilities).

**Spend** — card spend by category, with receipt attachment.

**Statements** — monthly statements.

**Activity** — the full money ledger, a month at a time: a month switcher across the whole
history, money in / out / net, a filter (All / Card / Deposits / Bridges), rows grouped by day.
Card spend, deposits, bridge disbursements and bridge repayments merged into one stream. A bridge
repayment takes its amount from the ledger, not from timeline prose, so it cannot drift.

**Account — "Your bridge line"** — the customer-facing read of the engine. The limit, how much is
in use, how much is left, and **why it is that number** in plain language: time with FlowCard,
monthly deposits, repayment record, balance stability — each marked good or not. Then what would
move it. **Tier letters are deliberately never shown to customers**; the tier is an internal grade.

### The customer-facing late bridge (§8.4)

When a bridge is past due, the portal shows **only**: status, amount due, Repay, and one sentence
about the card restriction in force. The DPD ladder, the collections log and every internal action
stay in the back office. This is a deliberate product decision, not an omission — worth saying out
loud if the demo shows a delinquent customer.

---

## 6. Surface C — back office

Dark, dense, sidebar navigation. The header always carries the **current capital regime** badge
and the as-of date.

**Overview** — the capital picture and the decision queue.
- Facility gauge: $60,000 facility, $12,000 reserve floor (20%), $36,000 deployed, $12,000
  available = 20% of the facility → **Tight** regime
- Outstanding, fees earned ($290 collected on tracked bridges), bridges by stage
- **Outstanding by tier** and **days past due** buckets (current / 1–7 / 8–30 / 31–60 / 60+)
- **Book quality** — loss rate across all bridges on record
- **Request queue** — the five declined payments, ranked by tier, then spend criticality, then
  receivable quality, each showing shortfall, headroom and outcome (Auto-approve / Needs approval /
  Declined)

**Customers** — the four demo businesses with tier and limit; click through to Customer 360.

**Customer 360** — everything ops needs about one customer on one page: tier and limit with the
percentage that produced it, outstanding, early warnings, cash-flow and forecast charts, card
access state, bridge history, payer track record, CRM notes, and the timeline.

**Collections** — past-due bridges, oldest first. For the selected bridge: principal outstanding,
fee outstanding, late fee accrued at 2%/month, total amount due, the date it was due, and the card
state. Then two distinct blocks, which is the part worth pointing at:
- **Required actions** — what the DPD stage *prescribes*, straight from the ladder. Harbor at 10
  days past due shows: send daily reminders, collect from any incoming deposit, block new bridges,
  restrict card to critical spend, downgrade tier one level, notify ops. Ones already applied are
  shown as done.
- **Actions** — what this officer can do now: Send reminder, Restrict cards, Freeze cards, Escalate
  to collector, open Customer 360, and record a promise to pay with a date. Every one is logged to
  the timeline, and the History panel below shows the bridge end to end.

Card access is the **strictest** of the bridge rules and whatever ops has done.

**Early warnings** — customers whose numbers are slipping *before* they ask for anything. Five
signals (§8.2, placeholder thresholds):
- 30-day average balance more than **25%** below the 180-day average
- more than **5** days near zero in the last 90 (the tier A cap)
- a payer with an expected receivable who pays on time less than **70%** of the time
- the forecast crosses the low line within **30** days
- the largest payer is more than **60%** of inflows

The trigger table is on the screen itself, marked *Placeholder · spec §8.2*. Harbor Foods raises
four at once — 18 days near zero in the last 90; Calder Distribution pays on time 55% of the time,
14 days late on average; the forecast drops below the low line on 15 November (marked High); 62% of
inflows from one payer. Vertex Labs raises one: 70% concentration. This is the screen that makes
the "we see it before they ask" claim concrete.

**Rules** — the whole policy, rendered from `policy.config.ts` itself so it cannot drift from what
the engine applies: tiers and limits, the regime matrix with the live regime marked **"Now"**, the
seven eligibility rules, the pricing formula with a worked example priced by the engine, spend
criticality mappings, and the DPD ladder. v1.1 numbers are listed separately and marked
placeholder. This screen is the answer to "where do I check what Tight actually means".

**Admin** — a demo control panel: the engine's thresholds as inputs. Change one and the next
decision uses it. Every changed field says what the spec said. Nothing is persisted or audited, and
a reload restores the spec values. The strongest beat here: raise the Tight approval threshold from
$5,000 to $10,000 and Northwind's $7,000 request flips from "Needs approval" to "Auto-approve" —
and funding it drops capital into **Critical**, so the $3,000 request behind it now needs a person
instead.

**Request detail** — one declined payment end to end: the triggering decline, all eight rules with
pass/fail and the engine's own reasoning, the 180-day balance chart, the customer snapshot, and the
offer. Where a person is required, the **approval door**: approve as asked, or change the amount
and term — which re-prices and re-runs the whole evaluation live, reports which rules would now
fail, and states that the customer's acceptance is void and a new offer goes back to them.
Approval stays **disabled while any rule fails**: a person decides where the regime asks for one,
not whether a rule applies.

---

## 7. CRM — what exists, and what it would connect to

This is the question the team raised. The answer differs by surface.

### Today, in Bridge Capital: CRM is display-only

`CrmContext` carries an onboarding date, contract status and support notes. It appears in three
places, all read-only: the Request detail ("CRM notes — context only, not used in scoring"),
Customer 360, and one line on the customer's Account screen. **No policy function reads it.**
`policy/tiers.ts` touches `crm.onboarding_date` once, purely as a fallback calendar date for a
customer with no balance history — a date, not a signal. The spec states the rule explicitly
(§2: *"CRM (context, shown to ops, not used in scoring)"*), and `tiers.ts` carries a comment
saying so.

**So: no feature in Bridge Capital recomputes a bridge line when CRM changes.**

### In Kit 5: CRM does re-rate the line

Kit 5 is the older dashboard and the only place a CRM change moves a credit decision. An agent
reads a Salesforce note on Bloom & Co.; two signals drop from green to yellow; the tier goes
**A → C**, the line goes **$10,000 → $4,000**, and a request that would have cleared automatically
is declined. It is in `src/kit5/engine.ts` (`revealCrm` → `reEvaluateCrm`) and is covered by tests.
If the demo needs a "CRM changes the decision" moment, this is the only one that actually exists.

### Server-side: the contract is defined but not verified or wired

`server/src/salesforce/` defines a customer snapshot read from Salesforce. The account fields are
exactly the ones a credit line would have to react to:

| Field | Why a line would move |
|---|---|
| `Approved_Bridge_Cap__c` | An explicit cap set by credit — should override or bound the computed limit |
| `Credit_Review_Status__c` | A review in progress should freeze new bridges |
| `Prior_Default_Status__c` | Feeds the §3 default lookback that drops a customer to no tier |
| `Customer_Status__c` | A suspended or churned customer should have no line |
| `KYB_Status__c` | Failed or expired KYB should close the line |
| `Loyalty_Tier__c` | A commercial override on tier |

Plus contract status and end date, and cases and call activity with risk category and escalation
flags.

`server/src/ledger/reservations.ts` already refuses to reserve capital when a customer's snapshot
is not **fresh** — so staleness is treated as a hard stop server-side. Snapshot freshness has
states: fresh, missing, stale, invalid, deactivated.

**Two caveats to state plainly:** `SALESFORCE_MAPPING_VERIFIED = false` — the field names are
proposed from a spec, not verified against a real org, and production sync refuses to run until
they are. And the Bridge Capital front end never calls any of it.

### What "connecting CRM" would actually mean

If someone asks what the work is: a snapshot arrives for a customer → the tier and limit are
recomputed → the derived figures move with them (the pre-approved number, the queue outcome, card
access) → and for anything already offered but not yet disbursed, §7 binding applies: **a changed
limit voids an outstanding approval and a new offer has to be issued.** The engine already has that
machinery (`policy/binding.ts`); nothing reads CRM into it yet.

---

## 8. The decision engine

All of it is pure functions in `src/features/bridge-capital/policy/`, with every threshold in
`policy.config.ts`. Nothing is hard-coded in a component.

### Tiers and limits (§3)

| Tier | Condition | Limit |
|---|---|---|
| A | 12+ months, no late or defaulted bridge in 180 days, ≤5 days near zero in 90 | 30% of monthly inflows |
| B | 6+ months, ≤1 late bridge in 180 days, never defaulted | 20% |
| C | Everyone else eligible: no default in the last 365 days | 10% |
| No tier | Under 3 months, or under 90 days of balance data, or a default inside 365 days | Not eligible |

The limit applies to **total outstanding** bridge balance, existing plus new.

### The eligibility rules (§4)

Each returns an id, a label, pass/fail and a detail line:

1. **Has history** — 3+ months tenure and 90+ days of balance data
2. **No bridge currently past due**
3. **Outstanding + new bridge within limit**
4. **Spend criticality allowed in the current capital regime**
5. **Bridge does not breach the platform reserve floor** — and bridges are never partial
6. **No duplicate bridge for the same transaction**
7. **An expected receivable is due before the term ends**
8. **Tier allowed in the current capital regime** — *not one of the spec's seven.* §6 gates by
   tier as well as criticality, so the engine states that as its own rule rather than hiding it.

Spend criticality: **critical** = suppliers, payroll, inventory · **standard** = utilities,
logistics, professional services · **discretionary** = travel, entertainment, SaaS. Anything
unmapped falls back to standard.

### Pricing (§5)

```
fee = $50 + principal × 2% per month × term_days / 30
```

$10,000 over 60 days = **$450**. Charged for the full chosen term even if repaid early. Terms:
15, 30, 60, 90, or custom 7–90 days. The customer must accept amount, fee and term before funds
move; if any of the three changes, the acceptance is void and a new offer is issued.

### Capital regimes (§6)

Measured on capital available above the reserve floor, as a share of the facility.

| Regime | Band | Tiers | Spend | Approval |
|---|---|---|---|---|
| Abundant | above 60% | any eligible | all | automatic |
| Normal | 30–60% | B or better | all | automatic |
| **Tight** | **10–30%** | any eligible | **critical only** | automatic up to $5,000, then a person · **ranked** |
| Critical | under 10% | A only | critical only | every bridge needs a person · ranked |
| At floor | at or below | — | none | no new bridges, collect only |

**The demo starts in Tight.** When capital is ranked, requests compete on tier, then criticality,
then receivable quality.

### Late repayment (§5)

| Past due | Stage | What happens |
|---|---|---|
| 1–7 days | Watch | daily reminders, collect from any deposit, no new bridges |
| 8–30 | Delinquent | card restricted to critical spend, **tier downgraded one level**, ops notified |
| 31–60 | Serious | card frozen, escalated to collections, limit set to zero |
| 61+ | Default | recorded as defaulted, referred to collections, **ineligible for 12 months** |

Outstanding keeps accruing at 2% a month while late. Collection order is late fee, then fee, then
principal. After full repayment, restrictions lift and the tier recomputes from history.

### Approvals (§7)

Bound to customer, amount, fee and term. Any change invalidates the approval.

---

## 9. The demo customers

Four businesses, chosen so each one is a different answer. All figures as of 3 November 2026.

**Northwind Supply** — `acct_northwind` · **Tier A** · limit **$24,096** (30% of $80,319 monthly
inflows) · 30 months tenure · three bridges repaid on time, one running with $3,000 outstanding.
The healthy customer. Two declined payments:
- **Rowe Packaging $7,600**, wallet held $600 → **$7,000 shortfall**, critical. Above the $5,000
  Tight threshold, so **a person must approve**. This is the approval-door story.
- **Calloway Inventory $3,300**, wallet $300 → **$3,000**, critical → **clears automatically**.

The two sit side by side in the queue and differ only by amount. That contrast is the §6 regime
story in one screen.

**Harbor Foods** — `acct_harbor` · **Tier C** · limit **$9,521** (10% of $95,212) · 20 months ·
the hard case. Volatile balances, 18 days near zero in 90, a slow payer at 55% on-time, one late
bridge, and a **bridge 10 days past due → Delinquent**. Declined payment: **payroll $18,400**,
wallet $400 → **$18,000 shortfall** — the most sympathetic request in the demo, and it fails:
nearly twice the limit, on an account that is already past due.
*Why C and not B:* a default 500 days ago. That is outside tier C's 12-month window so they stay
eligible, but tier B's "no defaults" is unqualified. Good detail if anyone asks whether the tiers
are real.

**Lumen Studio** — `acct_lumen` · **Tier B** · limit **$9,051** (20% of $45,254) · 9 months · one
bridge repaid on time. Declined payment: **Meridian Travel $6,800**, wallet $300 → **$6,500**,
**discretionary**. Within limit, within capital — and declined anyway, because Tight covers
critical spend only. This is the cleanest demonstration that the regime, not the customer, closed
the door.

**Vertex Labs** — `acct_vertex` · **no tier** · limit **$0** · 2 months on the platform. Declined
payment: Northstar Analytics $2,700 → **$2,500**. Fails rule 1 on day one. Their Account screen
reads "Not available yet" and tells them what they need. The portal never shows them a tier letter.

### Why the whole set does not fit

Shortfalls total **$31,000** against **$12,000** of capital above the floor. That is deliberate:
the demo cannot fund everyone, so ranking and the reserve floor have to do real work.

---

## 10. Demo data, for anyone who asks "is this made up?"

Seeded and deterministic — the same blueprint always produces the same numbers. Per customer: 180
days of end-of-day balances, 24 months of invoices, and about 12 months of card spend drawn from a
per-customer merchant roster on realistic cadences (payroll monthly, suppliers weekly, SaaS
monthly, travel occasional). Roughly **368 card transactions and 152 deposits** across the four.

Deposits are derived from invoices that have been paid — so the inflows the engine scores and the
deposits the customer sees are the same records. Card spend feeds no statistic, which is why tiers
and limits do not move when spend changes. Merchant criticality is always derived from the
category by the same function the engine uses, so the portal and the engine can never disagree.

`npm run bridge:stats` prints every customer's computed statistics, tier and limit.

---

## 11. The backend, in one section

`server/` is a real Node service: Postgres, an Airwallex client, a ledger with capital
reservations, collections, webhooks, an outbox, a customer roster, reconciliation, an underwriting
pipeline, AI evidence packets, Salesforce snapshots, and onboarding. Routes include
`/api/ledger`, `/api/bridge-decisions`, `/api/onboarding`, `/api/submissions`,
`/api/platform-accounts`, `/api/companies`, `/api/customers/:id/snapshot` and a Kit 5 family
(`/api/kit5/session`, `/next-customer`, `/approve`, `/fixture-checkpoint`, …).

**The only front end that calls it is Kit 5**, via `VITE_BRIDGE_API_URL`. Bridge Capital reads
`MockAdapter` and nothing else; onboarding holds its form in React state and posts nowhere.

That is not a flaw to hide — it is a clean seam. Everything with state or a side effect in Bridge
Capital already goes through one `DataAdapter` interface, so a real adapter can replace the
in-memory one without touching a single rule. Worth saying on camera in exactly those terms.

---

## 12. Safe claims and unsafe ones

**Safe:**
- "Every number on screen is computed by the policy engine from the spec."
- "The rules page is rendered from the engine's own configuration, so it can't drift."
- "The decision logic is pure and tested — 83 tests on this feature."
- "The data layer is one interface, so a real backend drops in without touching the rules."
- "There's a working backend in the repo with a Salesforce contract defined for this."

**Unsafe:**
- "It's connected to Airwallex" — the client is mocked in this surface.
- "CRM updates the credit line" — only in Kit 5, and that path is a fixture.
- "Onboarding creates an account."
- "All tests pass" — 23 backend tests fail today.
- Anything implying money moves.
