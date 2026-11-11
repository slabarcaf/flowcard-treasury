# **Kit 5 Bridge Capital: Source of Truth**

Oct 20, 2026 · @Santiago

## **How to use this tab**

This tab is the single source of truth for Kit 5\. When another tab disagrees, this tab wins; the other tab is outdated.

* **Decided:** agreed by the team or by the area owner. Build against it.  
* **Proposed:** owner's recommendation. Build against it unless someone objects before Thursday.  
* **Open:** needs a team decision. Listed in the Open section with a recommendation.

Sections: Decision log (what and when), Logic (the rules), Flow (the steps), Target demo (what the video shows), Changes vs current build, Open, Small defaults. Precedence: Santiago's Decisions and Open Gaps (Oct 19\) and the Oct 20 call override Underwriting Rules v001, the Transfer Module and Demo Flow v2 where they conflict.

## **Decision log**

22 items are decided and 3 are proposed pending team OK. Newest source first in each row.

| \# | Topic | Decision | Status | Source |
| :---- | :---- | :---- | :---- | :---- |
| 1 | Trigger | Reactive: a card payment declines, the customer is notified, we bridge the shortfall, the payment is retried. Pre-authorization (remote auth) is the production path, named once in the pitch. | Decided | DG Oct 19 |
| 2 | Rules vs AI | Rules decide every yes, no and amount. AI reads outside text (Salesforce notes, support tickets), turns it into signals with quoted evidence and writes the rationale. AI may propose a split; the rules can overrule it. Only the rules engine calls the transfer module. | Decided | DG Oct 19, Santiago Oct 20 |
| 3 | Processing | Sequential: each request is decided the moment it arrives, because a card payment can't wait for a batch. Tier priority is enforced by the doors, not by reordering requests. | Proposed | Santiago Oct 20 |
| 4 | Full vs partial | Fund the full shortfall or nothing. A partial bridge doesn't clear the payment, so it doesn't help the customer. | Proposed | UR v001, Santiago Oct 20 |
| 5 | Credit line | Set daily for every customer. Smallest of: monthly inflows × tier multiplier; 20% of the facility per customer (10% once mature); any cap the account manager sets in Salesforce. Lines can add up to more than our capital, so a line is not a promise. | Decided | DG Oct 19, UR v001 |
| 6 | Multipliers | A 30%, B 20%, C 10%. Starter (never borrowed): 10%, or 5% if signals are weaker. Starter moves to full tier after 2 to 3 on-time repayments. | Decided | UR v001 |
| 7 | Tiers | A: all green or one yellow. B: two yellows. C: three or more yellows, or one red. No line: red on inflow reliability, or two or more reds. | Decided | DG Oct 19 |
| 8 | History gate | At least 90 days of activity (ours, their bank's or our CRM's). Less means no line. | Decided | UR v001 |
| 9 | No-go rules | Excluded industry, fraud flag, account frozen or closed, past write-off with us, one client above 70% of income. Any one means no line. | Decided | UR v001 |
| 10 | Reserve floor | 20% of the facility is never lent, and the demo must reach it. | Decided | DG Oct 19 |
| 11 | Facility source | Facility, floor and capital level live in app config plus our bridge ledger. The live platform wallet (about \$10M) is only a sanity check. | Decided | DF, DI |
| 12 | Doors | Capital level sets which tiers are Auto, Human or Closed (table in Logic). | Decided | UR v001 |
| 13 | Fee | 1.5% per 30 days, prorated daily (0.05% per day), one rate for every tier, charged per bridge on the amount used. 7-day minimum, so a next-day repayment still pays 7 days. This is our answer to the hackathon's "monthly fee". | Decided | Oct 20 call |
| 14 | Charges | Fee and principal are collected as two separate charges, fee first. | Decided | TM, Oct 20 call |
| 15 | Repayment term | Customer picks in the bridge message: 15 days, 30 days, a set date, or as soon as money arrives. No pick means as soon as money arrives. Collection timing reopened Oct 20 (Open 1). | Decided, under review | DG Oct 19 |
| 16 | Collection authority | We collect automatically only the agreed amount and fee, on the agreed terms. Anything else needs a person. | Decided | DG Oct 19 |
| 17 | CRM | Real Salesforce through Ramil's MCP, read-only. Daily snapshot, plus a re-score when a record changes. | Decided | Oct 20 call |
| 18 | New information | A Salesforce note (e.g. a client dispute) is read by the AI, becomes a signal, and the rules re-rate the customer's tier and line. Downgrades apply automatically. | Decided | Santiago Oct 20 |
| 19 | Upgrades from notes | A positive note never raises a line by itself; an upgrade needs a person's OK. | Proposed | DG Oct 19, CRM spec |
| 20 | Criticality | Payroll vs other spend is out of the demo. Later phase. | Decided | DG Oct 19 |
| 21 | Data | Current state is live in the sandbox; history (90 days of balances, invoices, past bridges) is synthetic and disclosed. | Decided | Discovery Oct 12, DF |
| 22 | Scan button | "Scan Declines" is demo-only. Production runs on decline webhooks or a scheduled job. | Decided | Oct 20 call |
| 23 | Platform | FlowCard, a corporate-card issuer on Airwallex. We are the platform, not Airwallex. | Decided | Oct 16 call |
| 24 | Currency | USD only in the demo. No FX paths. | Decided | Review \#34 |
| 25 | Bloom payment amount | $9,000 supplier purchase; $2,000 opening balance; $7,000 shortfall. The Tier C / $4,000-line rejection remains unchanged. | Decided | Ramil, Oct 22 |

## **Logic**

Two stages: a daily precheck sets every customer's credit line from their own profile; each request is then checked, on arrival, against that line and the facility.

### **Facility and capital level**

* Lendable \= facility − reserve floor (20%).  
* Deployable \= lendable − outstanding bridges (from our ledger).  
* Capital level \= deployable ÷ lendable. Lower bound of each band is inclusive (40% is Normal, 10% is Less).  
* A band only reopens 5 points above where it closed, so doors don't flip with every bridge.

| Capital level | Free capital | Tier A | Tier B | Tier C |
| :---- | :---- | :---- | :---- | :---- |
| A lot | above 70% | Auto | Auto | Auto |
| Normal | 40 to 70% | Auto | Auto | Human |
| Less | 10 to 40% | Auto | Human | Closed |
| Critical | below 10% | Human | Closed | Closed |
| At floor | 0% | Closed | Closed | Closed |

### **Daily precheck (every customer, every day)**

1. Enough data: 90 days of history, else no line.  
2. No-go rules (Decision log \#9). Any one means no line.  
3. Rate four signals green, yellow or red:  
   * Inflow reliability (the customer's own clients pay them on time): green 90%+ of invoices on time, yellow 75 to 90%, red below 75%.  
   * Inflow concentration (top client's share of income): green 40% or less, yellow 40 to 70%, above 70% is a no-go.  
   * Cash buffer (days in the last 90 with balance below 10% of monthly spend): green 5 or fewer, yellow 6 to 20, red more than 20\. Proposed.  
   * Track record: green always repaid on time, yellow never borrowed, red paid late before.  
4. Tier from the ratings (Decision log \#7). Strong signals never cancel a red on reliability.  
5. Credit line \= smallest of the three terms in Decision log \#5.

### **Per request (sequential, on arrival)**

All four checks pass: we bridge the full shortfall. Any one fails: firm no for that request, with the reason.

1. Spend check: traceable card spend only. No cash or quasi-cash.  
2. Line check: shortfall ≤ line − amount already owed, and nothing owed is more than 2 days late.  
3. Facility check: shortfall ≤ deployable. A bridge never takes the facility below the floor. We never fund part of a request to fit what's left.  
4. Door check: the customer's tier door at the capital level before this bridge. Auto funds now; Human goes to a credit officer who approves these exact terms; Closed is a no.

Tier priority in a sequential flow comes from the doors: as capital shrinks, lower tiers close first. Two identical requests can get different answers: the first one funded uses capital, so the second may meet a stricter door or fail the facility check. Every request gets a yes or a firm no at the moment it arrives; the 6-hour wait in UR v001 is dropped (Proposed).

### **AI boundary**

| AI does | Rules do |
| :---- | :---- |
| Reads Salesforce notes and support tickets | Compute signals, tiers, lines, capital level and doors |
| Extracts typed signals, each tied to the words it came from | Decide yes or no and the amount |
| Writes the one-line rationale per decision | Call the transfer and charge modules |
| May propose a split across customers | Overrule any proposal that breaks a rule (e.g. a partial bridge, a closed door) |

Salesforce note text is untrusted data; instructions inside a note are ignored. A vague impression ("sounded worried") goes to human review and changes nothing automatically.

### **New information to tier (Proposed mapping)**

| Signal from a note | Effect on ratings |
| :---- | :---- |
| A client with 40% or more of revenue disputes or delays payment | Inflow reliability becomes yellow |
| A stated revenue share for a client | Replaces the stored concentration value; above 70% is a no-go |
| Stated expected inflow delay | Logged and shown; no rating change by itself |
| Positive note | No change; an upgrade needs a person |

The re-rated profile flows through the normal tier and line rules. The AI never sets the new tier or line.

### **Fee**

Accrues daily from the moment the bridge transfer settles until the repayment charge settles, on principal still outstanding. Minimum 7 days of accrual per bridge, never refunded. No fee on an unused line.

\\text{Fee} \= \\max\\left(0.0005 \\times \\sum\_d P\_d,\\; 7 \\times 0.0005 \\times P\_0\\right)  
P\_d is principal outstanding at the end of day d; P\_0 is the amount bridged. Example: \$6,000 repaid on day 9 pays \$27.00; repaid on day 3 pays the \$21.00 minimum.

### **Collections**

* Collect from the customer's wallet per the agreed term (Decision log \#15): fee charge first, then principal charge. Timing rule is Open 1\.  
* Wallet short at collection: take what's there, the rest is past due, and a person handles anything beyond the agreed terms.  
* Late ladder, counted from the due date: days 1 to 2 grace with reminders; days 3 to 15 line frozen, cards still work on the customer's own money; day 16+ cards blocked until repaid, human collections. Out of the demo.  
* Paid in full at any stage: the line reopens and the next daily precheck re-rates the customer.

## **Flow**

One request, end to end. Steps 1 to 3 run in the background; steps 4 to 12 run per request, in arrival order.

**Background**

1. Nightly: sync Airwallex data and the Salesforce snapshot, run the daily precheck, store each customer's tier and line with the inputs and the rule behind it.  
2. On a Salesforce record change: the AI reads the new note, extracts signals with quoted evidence, and the rules re-rate that customer's tier and line immediately.  
3. On any transfer or charge settling: recompute outstanding, deployable and capital level.

**Per request**

4. A card payment declines with INSUFFICIENT\_FUNDS. The decline lands in the queue with its sandbox transaction id.  
5. Shortfall \= payment − customer's available balance in that currency.  
6. Run the four per-request checks against the current line and the capital level before this bridge.  
7. Outcome:  
   * No: log the failed check, notify the customer with the reason. Final for that request.  
   * Auto: go to step 8\.  
   * Human: a credit officer sees amount, customer, payment covered, term, fee, evidence and capital impact, and approves those exact terms. Any change to them means a new approval.  
8. Pre-execution re-check: request still open and unfunded, balance still short by the same amount, line and facility still pass, no existing transfer with this request\_id.  
9. Transfer the shortfall from the platform wallet to the customer's wallet (connected\_account\_transfers, request\_id bridge-\<card transaction id\>). Wait for SETTLED. On any ambiguous response, look up the request\_id before retrying.  
10. Notify the customer (amount, fee, term choice) and retry the card payment.  
11. Collection per the agreed term: fee charge (fee-\<transfer id\>), then principal charge (repay-\<deposit id\>). Capital returns to the pool.  
12. Update the ledger and track record; the next precheck re-rates the customer.

## **Target demo**

Five declines arrive one at a time on a Monday morning; \$27,000 of eligible shortfalls meet \$15,000 of deployable capital. New Salesforce evidence turns one yes into a no, a credit officer approves one, and the last identical request is refused at the floor.

### **Facility (app config)**

| Item | Value |
| :---- | :---- |
| Bridge facility | \$50,000 |
| Reserve floor (20%) | \$10,000 |
| Lendable | \$40,000 |
| Outstanding to other customers (seeded ledger, disclosed) | \$25,000 |
| Deployable at start | \$15,000 (37.5%, Less) |

### **Cast**

| Customer | Monthly inflow | Tier | Credit line | Payment | Wallet | Shortfall |
| :---- | :---- | :---- | :---- | :---- | :---- | :---- |
| Bloom & Co. | \$40,000 | A last night (one yellow: cash buffer) | \$10,000 (30% capped at 20% of facility) | \$9,000 supplier | \$2,000 | \$7,000 |
| Atlas SaaS | \$20,000 | A (all green) | \$6,000 | \$6,500 supplier | \$1,500 | \$5,000 |
| Nova Agency | \$35,000 | B (yellows: concentration, cash buffer; repaid before, track record green) | \$7,000 | \$7,000 contractor | \$1,000 | \$6,000 |
| Pixel Studio | n/a | No line: 41 of 90 days | \$0 | \$2,500 | \$500 | \$2,000 |
| Juniper Labs (new) | \$30,000 | B (same profile shape as Nova) | \$6,000 | \$7,000 contractor | \$1,000 | \$6,000 |

Bloom's seeded CRM shows its top client at 38% of revenue (stale), so it starts as Tier A.

### **Beats**

| Time | Event | Rule that decides | Deployable after |
| :---- | :---- | :---- | :---- |
| 8:40 | Salesforce note on Bloom: largest client (about 65% of revenue) disputes last month's invoice. AI extracts three signals with quotes. Rules re-rate: concentration and reliability turn yellow, three yellows, Tier C, line \$4,000. | Mapping table, tier rules | \$15,000 |
| 9:02 | Bloom declines, shortfall \$7,000. AI proposes a \$4,000 partial bridge. Rules say no: shortfall above line, Tier C door Closed in Less, and a partial bridge wouldn't clear the payment. | Line check, door, full-or-nothing | \$15,000 |
| 9:05 | Atlas declines, shortfall \$5,000. Tier A, Less, Auto. Transfer settles, payment retried and clears. | Door Auto | \$10,000 (25%, Less) |
| 9:11 | Nova declines, shortfall \$6,000. Tier B, Less, Human. Credit officer approves the bound terms; transfer settles; payment clears. Nova picks "as soon as money arrives". | Door Human | \$4,000 (10%, Less) |
| 9:13 | Pixel declines. No line: 41 days of history. | History gate | \$4,000 |
| 9:16 | Juniper declines with the same \$6,000 shortfall as Nova. Line passes, but \$6,000 exceeds the \$4,000 above the floor. Firm no; no partial bridge. | Facility check (floor binds) | \$4,000 |
| Day \+9 | Nova's \$8,000 deposit settles. Fee \$27.00 (\$6,000 × 0.05% × 9), then principal \$6,000, as two charges. | Collections | \$10,000 (25%, Less) |
| Close | Summary: Atlas auto, Nova approved and repaid, Bloom declined on new evidence, Pixel ineligible, Juniper refused at the floor, floor never breached, every decision logged with its inputs and rule. |  |  |

The capital bar (lent out, deployable, hatched floor, band) stays pinned at the top of the treasury view the whole video. The video never leaves the treasury view; customer messages appear as feed lines.

Why this shape: Nova and Juniper are the same request from the same kind of customer. The only difference is arrival order, which is what a sequential, instant model means for a scarce facility.

## **Changes vs current build (v001 yidi)**

Reviewed against the code in flowcard-treasury on Oct 20\. 16 changes take the build to the target demo; most sit in src/seed.ts and src/state/machine.ts. Already aligned: tier multipliers (30/20/10%), 20% per-customer cap, door table, band boundaries (40% is Normal, 10% is Less), full-or-nothing, 90-day gate, no-go list, Nova's green track record, and the customer accept step before funding.

| \# | Where | Current code | Target | Why |
| :---- | :---- | :---- | :---- | :---- |
| 1 | seed.ts FACILITY\_CONFIG, SEEDED\_OUTSTANDING | Floor 0.05, lent out \$32,500 | Floor 0.20, lent out \$25,000 (deployable stays \$15,000) | Decision log \#10 |
| 2 | machine.ts processCustomer | Each customer is decided when the presenter clicks its row, in any order | Decide declines automatically in detectedAt order | Decision log \#3 |
| 3 | machine.ts Bloom special case, revealBloomCrm, reEvaluateBloom | Bloom passes as Tier A, sits in "Evaluating", then the CRM note is "found" at 9:13 and re-rates it | CRM note event at 8:40, before the scan; Bloom's 9:02 decline is decided on post-note signals; show last night's A / \$10,000 next to C / \$4,000 | Sequential model; new info still changes the decision |
| 4 | seed.ts AI\_PROPOSED\_PLAN | Pooled split across Atlas, Nova and Bloom | One AI suggestion on Bloom's request (\$4,000 partial), rejected on screen | Decision log \#2, \#4 |
| 5 | seed.ts CUSTOMERS, DECLINED\_TRANSACTIONS, AGENT\_RATIONALES | Four customers | Add Juniper Labs: \$30,000 inflow, Nova's signal shape, \$7,000 payment, \$1,000 wallet, 9:16 | Floor must bind |
| 6 | policy/tiers.ts assignTier | Any red gives Tier C | Two or more reds give no line | Decision log \#7 |
| 7 | policy/fees.ts bridgeFee | No minimum | Fee uses max(days, 7\) | Decision log \#13 |
| 8 | machine.ts per-request steps | "Spend check" tests shortfall ≤ line, which duplicates the line check; line check ignores outstanding and lateness; policy/bridge.ts checkBridge is never called | Spend check \= traceable spend, no cash; line check \= line − outstanding, nothing owed more than 2 days late; call checkBridge | Logic, per request |
| 9 | seed.ts NOVA\_APPROVAL\_TERMS | "band Less → Critical"; term "Up to 30 days, auto-collected from next deposit" | "Deployable \$10,000 → \$4,000, band stays Less (10%)"; term \= customer's pick, default as soon as money arrives | Band rule, Decision log \#15 |
| 10 | seed.ts NOVA\_COLLECTION | feeRequestId fee-acct\_nova001-202610 | fee-\<transfer id\> | Small defaults |
| 11 | seed.ts DECLINED\_TRANSACTIONS | Bloom: "\$12,000 payroll", category Payroll | "\$9,000 supplier", category Supplier | Decision log \#20, \#25 |
| 12 | seed.ts AGENT\_RATIONALES | bloom\_initial says "deposits 94% on time"; nova\_initial says "Tier A served first; no capital left" (pooled-era text) | "invoices 94% paid on time"; drop nova\_initial | Reliability \= clients paying invoices |
| 13 | machine.ts feed | No retry shown after funding | Feed line per funded bridge: payment retried, CLEARING | Small defaults |
| 14 | machine.ts feed times | Hard-coded 9:13 to 9:16 | Times follow arrival order and the demo clock | Small defaults |
| 15 | policy/creditLine.ts | No Starter line, no account manager cap | Add both; AM cap empty for every demo customer. Not on screen | Decision log \#5, \#6 |
| 16 | policy/facility.ts | No 5-point reopen rule | Add later; the demo never crosses back over a band edge | Logic, facility |

Outside the frontend: src/services defines Airwallex, CRM and AI clients with mocks, but nothing calls them yet. Wiring them is Ramil's integration work.

## **Open for team decision**

Seven items, to close at the Oct 21 5 PM PT call. Each has a recommendation so the build can start on it now.

| \# | Question | Options | Recommendation | Owner |
| :---- | :---- | :---- | :---- | :---- |
| 1 | Collection timing | (a) Collect as soon as the wallet holds enough. (b) Collect on the term the customer picked. | (b), with "as soon as money arrives" as the default. It respects the agreed terms (\#16); the customer pays per day, so picking a longer term is their trade-off. Demo shows Nova on the default. | Santiago |
| 2 | Sequential or pooled | (a) Each request decided on arrival. (b) Requests pooled for a window, then allocated together. | (a). A card payment can't wait 10 to 20 minutes. | Team |
| 3 | Full or partial funding | (a) Full shortfall or nothing. (b) Split capital across more customers. | (a). Half a shortfall still fails the payment. | Team |
| 4 | Tier priority under sequential | (a) Doors only. (b) Also hold back a slice of capital that only Tier A can use. | (a) for the demo; (b) as a production lever. | Santiago |
| 5 | AI in the recording | (a) Live LLM. (b) Captured good run replayed for the video, disclosed. | Live extraction while building; record from a captured run so the rejected-split beat always appears. | Team |
| 6 | Note-to-rating threshold | Client share that makes a dispute turn reliability yellow | 40%, same line as concentration green | Santiago, Ramil |
| 7 | Fifth customer | Add Juniper Labs as a fifth connected account so the floor visibly binds | Yes; one extra account, card and seeded history | Ramil |

## **Small defaults**

Housekeeping choices the build applies unless someone objects. None changes the story.

| Topic | Default |
| :---- | :---- |
| Sanctions | OFAC check on entity and owners added to the no-go list; shown as "clear" (mocked) in the precheck panel |
| Account manager cap | Salesforce field exists, empty for every demo customer |
| Retry | Every funded bridge retries its payment automatically and shows the result in the feed (Atlas and Nova) |
| Screens | Treasury view only; customer messages appear as feed lines |
| Idempotency keys | bridge-\<card transaction id\>, fee-\<transfer id\>, repay-\<deposit id\> |
| Cards | Per-transaction limit at least \$9,000 and monthly limit \$100,000; seed each wallet below its attempt amount and verify the provider returns INSUFFICIENT\_FUNDS; allowed MCCs set per payment |
| Accounts | Each connected account created with an EIN before submit; one cardholder each |
| Demo clock | One app clock drives displayed times, fee days and due dates; Airwallex timestamps stored alongside |
| Words | Credit line, bridge facility, customer, policy engine, AI agent. Customer's own minimum balance is "operating buffer", never "floor" |
| Operating buffer | 0 for demo and production for now; revisit before live use |

This overrides the Transfer Module's earlier proposal of about seven days of
median card spend for production. Zero is chosen for development simplicity.

