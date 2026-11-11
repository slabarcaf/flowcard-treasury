# **Kit 5 Demo Flow v2**

Oct 19, 2026 · @Yidi

## **Core story**

On a Monday payroll morning, three FlowCard customers hit insufficient-funds declines within ten minutes. They need \$21,000 of bridge capital; the platform can deploy \$15,000 above its reserve floor. The agent proposes an allocation, a Salesforce note changes one customer's risk before any money moves, and the revised plan funds a different customer with a human sign-off. The loop closes when the deposit lands and the platform collects principal and fee.

The demo makes one point: **the model reads evidence and proposes; deterministic policy decides what money moves.** Every beat shows which side did what.

| Time | Act | What the audience sees |
| :---- | :---- | :---- |
| 0:00–0:15 | Hook | The problem: more shortfalls than bridge capital |
| 0:15–0:45 | 1\. Declines arrive | Three real sandbox declines; \$21k need vs \$15k deployable |
| 0:45–1:20 | 2\. Initial plan | Tiers, credit lines and doors; Bloom and Atlas funded, Nova queued at \$0 |
| 1:20–2:05 | 3\. New evidence | Salesforce note on Bloom; AI extracts signals; policy rejects AI's partial bridge; revised plan |
| 2:05–2:45 | 4\. Approve and fund | Credit officer approves Nova; real transfers settle; Nova pays again |
| 2:45–3:15 | 5\. Collect | Deposit lands; fee and principal collected as two charges; capital returns |
| 3:15–3:25 | Close | One summary card |

## **Demo setup**

Numbers follow the Underwriting Rules v001 so tiers, credit lines and doors all show on screen. The facility lives in app config because the sandbox platform wallet holds about 10M per currency and would never bind.

### **Bridge facility (app config)**

| Item | Value |
| :---- | :---- |
| Bridge facility | \$50,000 |
| Reserve floor (5%) | \$2,500 |
| Lendable (facility − floor) | \$47,500 |
| Outstanding bridges to other customers (seeded ledger) | \$32,500 |
| Deployable now | **\$15,000** |
| Capital level (15,000 ÷ 47,500) | 32%, band **Less** |
| Doors in band Less | Tier A Auto · Tier B Human · Tier C Closed |

### **Capital bar (on screen the whole video)**

One horizontal bar pinned to the top of the dashboard replaces the facility table on screen; the table above is for the build team. It has three segments (lent out, deployable, floor) and shows the current band beside it.

| Moment | Lent out | Deployable | Floor | Band |
| :---- | :---- | :---- | :---- | :---- |
| Start (Hook to Act 3\) | \$32,500 | \$15,000 | \$2,500 | Less |
| Atlas funded (Act 4\) | \$37,500 | \$10,000 | \$2,500 | Less |
| Nova funded (Act 4\) | \$43,500 | \$4,000 | \$2,500 | Critical |
| Nova repays (Act 5\) | \$37,500 | \$10,000 | \$2,500 | Less |

Values come from app config plus the bridge ledger and refresh each time a transfer or charge settles. The floor is a fixed hatched block the bar never eats into.

### **Customers (four connected accounts)**

| Customer | Monthly inflow | Tier at start | Credit line | Attempted payment | Wallet at decline | Shortfall |
| :---- | :---- | :---- | :---- | :---- | :---- | :---- |
| Bloom & Co. | \$40,000 | A (one yellow: cash buffer) | \$10,000 (30%, capped) | \$12,000 payroll | \$2,000 | \$10,000 |
| Atlas SaaS | \$20,000 | A (all green) | \$6,000 | \$6,500 supplier | \$1,500 | \$5,000 |
| Nova Agency | \$35,000 | B (two yellows) | \$7,000 (20%) | \$7,000 contractor | \$1,000 | \$6,000 |
| Pixel Studio | n/a | No line: 41 days of history | \$0 | \$2,500 | \$500 | \$2,000 (ineligible) |

Eligible shortfalls total \$21,000 against \$15,000 deployable.

### **Build prerequisites**

* Create each connected account with an EIN in business\_identifiers before submit; without it on-behalf-of transfers fail and the account cannot be edited.  
* Issue each card with a per-transaction limit of at least \$12,000 plus a MONTHLY limit, so the decline reason is INSUFFICIENT\_FUNDS, not LIMIT\_EXCEEDED.  
* Fund each wallet to the "wallet at decline" amount via Global Account \+ deposit simulation.  
* Seed 90 days of history and the Salesforce records (Ramil's module) per customer.  
* Tag every money movement: request\_id prefixes bridge-, repay-, fee-.

## **Script**

### **Hook (0:00–0:15)**

**On screen:** Opening disclosure line for 3 seconds, then the FlowCard Treasury dashboard, empty queue. Capital bar at the top: \$15,000 deployable, \$2,500 floor, band Less.

**Narration:** "FlowCard issues corporate cards to small businesses on Airwallex. When a customer's deposit runs late, their card payments fail, and we can bridge the gap from our own capital. But we always have less capital than customers need. This is the agent that decides who gets it."

### **Act 1: Declines arrive (0:15–0:45)**

**On screen:**

1. 9:02, 9:05, 9:11: three INSUFFICIENT\_FUNDS declines land in the queue, each showing its sandbox transaction id and failure\_reason.  
2. A fourth decline, Pixel Studio, lands and is greyed out: "No line: 41 days of history (90 required)."  
3. Queue summary:

| Customer | Payment | Shortfall | Detected |
| :---- | :---- | :---- | :---- |
| Bloom & Co. | \$12,000 payroll | \$10,000 | 9:02 |
| Atlas SaaS | \$6,500 supplier | \$5,000 | 9:05 |
| Nova Agency | \$7,000 contractor | \$6,000 | 9:11 |

Banner: "Eligible shortfalls \$21,000 · Deployable \$15,000".

**Narration:** "It's Monday, payroll morning. Three customers hit insufficient-funds declines within ten minutes, and a fourth is too new to qualify. Together they need twenty-one thousand dollars. We can lend fifteen."

### **Act 2: Initial plan (0:45–1:20)**

**On screen:** The agent's allocation table. Each row shows tier, credit line, door and a one-line rationale.

| Customer | Tier | Door (band Less) | Allocation | Rationale (AI) |
| :---- | :---- | :---- | :---- | :---- |
| Bloom | A | Auto | \$10,000 | 2-year customer, deposits 94% on time, repaid 4 of 4 bridges |
| Atlas | A | Auto | \$5,000 | Diversified payers, never borrowed beyond line |
| Nova | B | Human | \$0 | Tier A served first; no capital left |

Status on all rows: "Planned, not executed. Pre-execution checks pending."

**Narration:** "Credit lines are precomputed daily from each customer's history, so the decision takes seconds. Capital is tight, so policy serves the strongest tier first. Bloom and Atlas are fully covered. Nova gets nothing yet. A partial bridge wouldn't help anyone: a card payment either clears in full or fails."

### **Act 3: New evidence (1:20–2:05)**

**On screen:**

1. Before execution, the agent pulls the latest CRM snapshot. A Salesforce case note on Bloom, logged at 8:40 by the account manager, appears in full.

   *"Spoke with Bloom's CFO. Their largest client, about 65% of revenue, is disputing last month's invoice. Payment expected next week at the earliest."*

2. AI extraction panel, each signal tied to the words it came from.

| Signal | Value | Evidence |
| :---- | :---- | :---- |
| payer\_dispute | true | "disputing last month's invoice" |
| payer\_concentration | 65% | "about 65% of revenue" |
| expected\_inflow\_delay | ≥ 5 days | "next week at the earliest" |

3. The rules re-rate Bloom: concentration turns yellow, inflow reliability turns yellow. Three yellows means Tier C. Credit line drops to \$4,000. Door for Tier C in band Less: Closed.  
4. AI proposes a revised split: Atlas \$5,000, Nova \$6,000, Bloom \$4,000.  
5. The policy check shows a red line on Bloom:  
   * ✗ Tier C door is Closed in band Less  
   * ✗ \$4,000 does not cover the \$10,000 shortfall, so the payment would still fail  
6. Final plan:

| Customer | Before | After | Path |
| :---- | :---- | :---- | :---- |
| Bloom | \$10,000 | \$0 | Declined; reassess when deposit settles |
| Atlas | \$5,000 | \$5,000 | Auto |
| Nova | \$0 | \$6,000 | Human approval |
| Unallocated | \$0 | \$4,000 | Held |

**Narration:** "Before any money moves, the agent re-reads the CRM. A note from Bloom's account manager says their biggest client is disputing an invoice. The model turns that note into signals, and the rules re-rate Bloom to Tier C. The model still wants to give Bloom four thousand. The rules say no: Tier C is closed at this capital level, and four thousand wouldn't clear a ten-thousand-dollar payroll. Nova moves up, but as Tier B it needs a person to sign off."

### **Act 4: Approve and fund (2:05–2:45)**

**On screen:**

1. Atlas row runs automatically: transfer created, then SETTLED. The capital bar drops to \$10,000 deployable.  
2. Credit officer approval card for Nova. The approval binds to exactly these terms:

| Field | Value |
| :---- | :---- |
| Amount | \$6,000 USD |
| To | Nova Agency (acct\_…) |
| Covers | Declined card transaction … |
| Term | Up to 30 days, auto-collected from next deposit |
| Fee | 1.5% per 30 days on daily outstanding |
| Evidence | Tier B: concentration and cash buffer yellow |
| Capital impact | Deployable \$10,000 → \$4,000, band Less → Critical, floor intact |

3. Presenter clicks Approve. Pre-execution checks tick through: case still open and unfunded; balance re-read, still short \$6,000; within line; under concentration cap; above floor; no existing transfer with this request\_id.  
4. POST /connected\_account\_transfers/create, request\_id: bridge-\<txn id\>. Status NEW, then SETTLED. Nova wallet re-read: \$7,000. The capital bar drops to \$4,000 deployable and the band turns Critical.  
5. Treasury activity feed: "Notification sent to Nova Agency: your \$6,000 bridge has arrived; you can pay again now." "Notification sent to Bloom & Co.: today's \$10,000 payment couldn't be bridged; we'll reassess when your deposit settles."  
6. A few seconds later, the feed shows: "Nova Agency initiated a new \$7,000 card payment: CLEARING", with the new transaction id. The original stays declined. The video never leaves the treasury view.

**Narration:** "Atlas is Tier A, so it funds automatically. Nova needs a credit officer, who approves these exact terms, not a blank cheque. The rules re-check everything one last time, then the transfer settles. We notify Nova, and seconds later their new payment clears. Bloom gets a clear answer instead of silence."

### **Act 5: Collect (2:45–3:15)**

**On screen:**

1. Demo clock advances 9 days.  
2. Nova deposit of \$8,000 via deposit simulation, SETTLED.  
3. Collection waterfall. Collectible \= min(deposit, available) \= \$8,000, enough for fee plus principal.

| Charge | Amount | request\_id | Status |
| :---- | :---- | :---- | :---- |
| Fee: \$6,000 × 1.5% × 9 ÷ 30 | \$27.00 | fee-\<acct\>-\<date\> | SETTLED |
| Principal | \$6,000.00 | repay-\<deposit id\> | SETTLED |

4. The capital bar refills: deployable \$4,000 → \$10,000, band Critical → Less. Nova track record: "1 of 3 on-time repayments toward full Tier B line."

**Narration:** "Nine days later Nova's deposit lands. The platform collects the fee first, then the principal, as two separate charges. Nova paid twenty-seven dollars for nine days of capital, and the six thousand is back in the pool for the next customer."

### **Close (3:15–3:25)**

**On screen:** Summary card: Atlas \$5,000 auto-bridged · Nova \$6,000 approved, repaid in 9 days · Bloom declined on new evidence, reassess on deposit · Pixel ineligible · reserve floor never breached · every decision logged with inputs and the rule that made it.

**Narration:** "The model reads the evidence. The rules decide what money moves. And every dollar comes back with a reason attached."

