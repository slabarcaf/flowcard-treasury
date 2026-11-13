[https\://airwallexdev.com/guide](https://airwallexdev.com/guide)

Platform Spend Controller \- Kit 5: Issue cards to connected accounts and ration bridge funding when customer deposits arrive late.

* Painpoint
* Solution
* User flow

### Yidi’s Idea: AI-Powered Loyalty & Liquidity Protection

### Painpoint

* SMBs face temporary cash flow gaps that can cause critical payments to fail.
* Traditional loyalty programs reward spending but don't address customers' liquidity needs.
* Fintech platforms have limited capital and struggle to decide **which customers to support, and how much to advance.**

### Solution

* **Loyalty-based benefits:** Reward frequent Airwallex users with higher potential bridge funding limits, lower fees, or priority service.
* **AI-powered allocation:** Dynamically allocate limited capital based on three factors:
  * **Customer Value:** How valuable is this customer to the platform?
  * **Business Urgency:** How critical is the payment to the customer's business?
  * **Repayment Risk:** How likely is the customer to repay?
* **Premium option:** Offer enhanced liquidity protection and monitoring as a paid service, subject to eligibility.

Why It Could Be Interesting

1. Creates a powerful growth flywheel: More Airwallex Usage → Better Loyalty Benefits → AI-Powered Liquidity Allocation → Fewer Failed Payments → Higher Engagement & Retention.
2. Introduces an interesting AI decision-making challenge: Should we prioritize our most valuable customers or those who need the funding most urgently?

### User Flow (demo flow)

1. **Earn Loyalty Benefits:** Customers earn loyalty points through Airwallex usage, unlocking higher potential funding limits and lower fees.
2. **Detect Liquidity Gaps:** The agent identifies three customers from different loyalty tiers who need bridge funding, but the platform only has \$5,000 available.
3. **Evaluate & Allocate:** The agent evaluates customer loyalty, payment urgency, and repayment risk to decide who receives funding and how much.
4. **React to Changes:** A Premium customer's expected deposit is suddenly delayed by seven days. The agent reassesses the risk and reallocates funds that have not yet been disbursed.
5. **Execute Funding:** The platform advances approved funds to selected customers, preventing critical payments from failing.
6. **Recover & Learn:** The platform collects repayment when incoming funds arrive and updates future funding eligibility based on repayment behavior.


Santiago’s idea:

**One line:** An agent that rations a platform's scarce bridge capital across SMB customers when their deposits arrive late. It bridges through card limits instead of cash, scores risk from each customer's history, and opens or closes access by customer tier depending on how close the platform is to its reserve floor.

## Painpoint

* SMBs on platform-issued corporate cards depend on incoming deposits to fund spend. When a deposit is late, critical payments (payroll, key suppliers, tax) fail. The business takes the hit and the platform loses the customer.
* Platforms have limited capital to bridge these gaps. Today the decision is manual, ad hoc, and blind to the platform's own liquidity: they either bridge everyone until the money runs out, or no one.
* Cash bridges are irreversible and unconditional. Once money lands in the customer's wallet, the platform has no control over its use and no way to adjust exposure.

## Solution

### 1\. History-based risk scoring

Each customer gets a score built from five layers, all fed by data the platform already has:

| Layer | What it measures | Source |
| :---- | :---- | :---- |
| Deposit reliability | % of deposits on time, average days late, variance, reversals | Deposit and Global Account events |
| Repayment history | Past bridges repaid on time, days to repay, defaults | Platform bridge ledger, charge history |
| Relationship value | Tenure, card and wallet volume, fee revenue | Card transactions, balances |
| Current exposure | Outstanding bridges vs. average monthly inflow | Platform ledger |
| Payer quality | Track record of whoever owes the late deposit, concentration on that payer | Deposit history by sender |

The score maps customers into four tiers (A to D). The tier sets the maximum bridge size, the fee, the instrument, and the approval path.

### 2\. Payment criticality

\[In case it is traceable\]

Every request is tagged by what it funds: **critical** (payroll, tax, key supplier) or **discretionary** (ads, software, travel), using payee type and merchant category.

### 3\. Reserve floor bands

The platform sets a hard reserve floor that is never touched. The capital above it is divided into bands, and access narrows as capital is used:

| Band (bridge capital remaining) | Tier A | Tier B | Tier C | Tier D |
| :---- | :---- | :---- | :---- | :---- |
| Green (\>60%) | Auto | Auto | Critical only, restricted card | No |
| Amber (30 to 60%) | Auto | Critical only | Critical only, human approval | No |
| Red (\<30%) | Critical only | Human approval | No | No |
| At hard floor | No new bridges. Existing commitments are honored. |  |  |  |

### 4\. Card limits as the bridge instrument

Instead of sending cash, the agent temporarily raises the customer's card limit, restricted to specific merchants or categories and with an end date. Platform money only leaves when the customer actually spends, every dollar is tied to its purpose, and nothing ever needs to be clawed back. \[THIS GIVE US CONTROL ON WHAT IS IT BEING USED FOR\]

### 5\. Adjust terms, don't cut

When a customer's risk rises mid-bridge, the agent never reduces a commitment. Its levers are:

* Extend the term to match the new deposit date
* Restrict unused headroom to critical merchants only
* Reprice the fee for the extra days
* Escalate to a human approver
* Re-tier the customer for future requests

Reallocation only uses capital that has not yet been committed.

### 6\. Recovery

When the late deposit lands, the agent collects the amount used plus a fee that reflects risk and days outstanding, and updates the customer's score.

## Why it's interesting

1. **The decision is about portfolio liquidity, not only individual scoring.** The same customer can get a yes on Monday and be queued on Friday because the platform's position changed. That is how real credit desks work, and a richer AI problem than approve or deny.
2. **Enforceable policy, not just a recommendation.** Tiers translate directly into card controls (stacked limits, merchant and category allowlists, end dates). The policy lives in the payment rails.
3. **Protects the customer while protecting the platform.** Never cutting commitments and prioritizing critical payments means support doesn't disappear when the customer needs it most.
4. **A learning loop.** Every bridge, repaid on time, late, or not at all, sharpens the next decision.

## User flow (demo)

1. **Setup:** Platform wallet \$50k, hard floor \$30k, so \$20k of bridge capital (Green). Four customers on cards, each with history.
2. **Detect gaps:** Three customers have overdue deposits and upcoming payments.
   * A: 2-year customer, always repays, payroll due
   * B: mid-history customer, supplier payment due
   * C: new customer, irregular deposits, ad spend
3. **Score and allocate:**
   * A: Tier A, critical. Automatic limit raise for payroll.
   * B: Tier B, critical. Limit raise restricted to the supplier category.
   * C: Tier C, discretionary. Declined.
4. Each decision comes with a short written rationale.
5. **Spend:** A and B pay through their cards. Capital is drawn only as they spend, and the band moves to Amber.
6. **Shock:** B's payer delays the deposit by 7 days. The agent keeps B's commitment, extends the term, restricts B's unused headroom to critical merchants, reprices the fee, and re-tiers B to C for new requests.
7. **Band changes the answer:** A new request from D (Tier B, critical) would normally be automatic, but in Amber it goes to human approval. The approver sees the score, the platform position, and the liquidity impact.
8. **Recover:** A's deposit lands. The agent collects the amount used plus the fee, capital returns to the pool, the band goes back to Green, and A's score improves.

Decision making

* Quantative: equation
* Qualitative: agent read support tickets

Use cases that agents are a better fit for than typical automation/deterministic:

* Case1: Agent gather all info \> suggest bridge amount for customer A
* Case2: agent decide we can give customer A \$50 and customer B \$150, instead of just choosing one customer
* Case3: repayment

demo flow:

1) User sign up for the service
2) Airwallex detect the bridge need for customer A,B,C
3) Agent gather all info (support tickets, txn history etc)
4) Agent give bridge plan (\$30 to A, \$20 to B, \$50 to C and why)
   1) Input:
   2) Logic:
   3) Output:
5) Asking customer if they are willing to receive the fund, or we give them reverse option in x days

Pending questions:

* What are the drivers for Airwallex? (Why do they want to do the bridges)?
  * Interest rate?
  * Extra fees?
  * Customer experience?
  * Customer survival?
* Are the bridges for a general use for the company or a virtual card for a person in particular?
* On average how much % of deposits/transactions go through Airwallex?

Questions for team

1. Data simulation
2.

# Flow Description

This note describes how the Platform Spend Controller handles a customer payment shortfall. It captures the decisions agreed for the user-flow diagram and expands

## Actors

The **platform operator** sets the reserve floor, customer tiers, and risk guardrails. The operator approves the allocation policy once.

The **Bridge Allocation Agent** uses customer deposit and repayment history, operator-set tier, and other risk signals to allocate bridge capital each day. It executes an eligible transfer automatically within the approved guardrails.

Each **small-business customer** has a connected account and a separate wallet. Ordinary card spending uses that customer's own wallet. The platform's wallet is used only for an approved bridge.

## Daily client-cap allocation

At the start of each day, the agent reads the platform balance, outstanding bridge commitments, and customer histories. It calculates deployable capital as the platform balance above the reserve floor and existing commitments: `max(0, platform balance - reserve floor - commitments)`.

The agent allocates that pool through a priority waterfall. It ranks repayment reliability and deposit predictability first, the operator-set customer tier second, and other risk signals third. It assigns daily caps in that order, with the total caps no greater than deployable capital. The agent can update caps daily within the policy approved by the operator.

A client without sufficient real deposit and repayment history receives no bridge budget. For the demo, one established client with real history is eligible; new clients remain ineligible until they build enough history.

## Card shortfall and bridge decision

During the day, the established client attempts a card purchase that exceeds the wallet balance. The first card simulation is declined for insufficient funds and records the shortfall. The agent checks the client's remaining daily cap and confirms that transferring the shortfall will leave the platform above its reserve floor.

If both checks pass, the agent initiates a platform-to-customer transfer for the shortfall. It assigns a unique request ID and tracks the transfer until it settles. If the API response is ambiguous or non-success, the system checks the transfer list for that request ID before retrying the same transfer. After settlement, the agent retries the card simulation for the same purchase; the customer wallet now covers it.

If the client has no approved budget, has used the cap, or the transfer would breach the reserve floor, the payment stays declined. The agent logs the reason and does not retry the allocation.

## Bridge recovery and monthly fee

When the customer's next deposit arrives, the platform attempts to collect the bridge principal from that customer's account. If the charge cannot collect, the balance remains outstanding and the system tries again after the next deposit. It does not use another customer's funds.

Only a client that received a bridge is charged the monthly platform fee. The fee is collected on the monthly billing date as a separate charge from bridge principal.

## Demo scope and sandbox behavior

The demo uses three connected accounts: one established client with real history and two new clients that are not eligible for bridge funding. This shows the history gate and one shortfall-to-bridge-to-success path. With only one eligible client, it does not demonstrate competition between multiple eligible clients in the priority waterfall; that would require at least two clients with qualifying real histories and competing shortfalls.

The sandbox flow is orchestrated through card simulation and transfer calls; the available guide does not establish a production-time card authorization hook. Customer-scoped card creation and card simulation use REST with `x-on-behalf-of`. See the [Airwallex builder guide](https://airwallexdev.com/guide) and the API details in

| Part | flowchart |
| ----- | ----- |
| **Input** | Platform balance, minimum reserve, existing bridge commitments, customer deposit and repayment history, customer tiers, and risk limits |
| **Logic** | Check whether there is enough history → Rank by repayment reliability and deposit predictability → Then consider customer tier → Allocate daily caps in priority order |
| **Output** | Each customer’s daily allocation and an explanation of the allocation; customers with insufficient history receive no allowance |

**Customer payment has a shortfall → Check the customer’s eligibility, remaining daily allowance, and the platform’s available funds → Advance only the shortfall → Retry the payment after the transfer settles → Recover the principal when the customer’s deposit arrives → Collect the monthly fee separately.**

[https\://excalidraw.com/](https://excalidraw.com/)



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

# Bridge Capital Underwriting: Airwallex Data Inventory

Oct 18, 2026 · Charley

## Bottom line

Airwallex holds enough per-customer data to set a bridge limit without an outside bureau, but the MCP connector cannot reach any of it per customer. Every per-customer read must go over REST with the header `x-on-behalf-of: <connected account id>`.

Five signals carry most of the decision; everything else in this doc modifies them:

1. **Deposit reliability**: `GET /api/v1/deposits` gives cadence, size and settled vs rejected/reversed. Lateness needs the expected date from the CRM.
2. **Shortfall history**: declined card transactions with `failure_reason = INSUFFICIENT_FUNDS`. This is the direct label for the problem being solved.
3. **Balance trajectory**: `GET /api/v1/balances/history` gives daily minimum balance, burn rate and days near zero.
4. **Repayment record**: `GET /api/v1/connected_account_transfers?destination=` (bridges sent) and `GET /api/v1/charges?source=` (repayments and fees collected).
5. **Standing gates**: `GET /api/v1/accounts/{id}` status plus `GET /api/v1/rfis`. Suspended, dormant or an open RFI means a limit of zero.

## Sandbox reconnaissance

The team sandbox was queried live on 2026-10-18 through the Airwallex Developer connector. It holds the platform account only; no connected-account data was visible through that path.

| Object | Observed | Underwriting relevance |
| ----- | ----- | ----- |
| Platform account | `acct_b07vyeDXPACSJGZR0HKv1Q`, "Sandbox Business", US, EIN on file, ACTIVE since 2026-10-04 | Same field set every connected account returns |
| Wallet balances | About 10,000,000 each in USD, EUR, GBP, CNY; zero elsewhere; nothing pending or reserved | Unrealistic. Bridge budget and reserve floor must be app-level parameters or rationing never binds |
| Global Account | 1 USD account (Community Federal Savings Bank); ACH, Fedwire, SWIFT inbound; direct debit enabled | Inbound rails available per customer |
| Deposits | 1 settled bank transfer, \$500, payer "Kit5 Test Payer", settled 1 second after creation | Simulated deposits settle instantly; lateness is not observable natively |
| Cardholders | 3 (2 DELEGATE, 1 INDIVIDUAL), all READY | Headcount proxy |
| Cards | 2 active Visa. Card ending 0134: virtual, program CREDIT / GOOD\_FUNDS\_CREDIT, \$300 per-transaction and \$2,000 monthly limit, \$1,800 remaining, MCCs 5411, 5812, 5814, 5734 allowed | Card limits cap the largest single bridge request |
| Card transactions | 5 total: 2 cleared (\$50, \$150); 3 declined (LIMIT\_EXCEEDED \$150, MERCHANT\_CATEGORY\_NOT\_ALLOWED \$20 at MCC 7995, CARD\_INACTIVE \$10) | Decline reasons are machine-readable; no INSUFFICIENT\_FUNDS decline exists yet |
| Balance history | Each card spend posts as AUTHORISATION\_HOLD, then AUTHORISATION\_RELEASE, then CAPTURE | Net spend must be computed from captures, not holds |
| Transfers, payment intents, payment disputes, direct debits, linked accounts, FX conversions, billing invoices, spend bills | Empty | Endpoints respond; data must be seeded |
| Beneficiaries | 7 sandbox defaults (CN, DE, GB, US bank accounts) | Not customer data |

## Integration constraints

The context fetcher must be built on REST, not MCP. MCP authenticates as the platform and cannot send `x-on-behalf-of`, so its tools return platform-account data only.

* **Auth**: `POST /api/v1/authentication/login` with client ID and API key returns a Bearer token. Sandbox base URL is `https://api.sandbox.airwallex.com`.
* **Per-account reads** (header `x-on-behalf-of: acct_...`): balances, balance history, deposits, global accounts, linked accounts, issuing cards and transactions, transfers, RFIs, capabilities, funding limits.
* **Platform-scoped reads with a filter**: accounts list, `connected_account_transfers?destination=`, `charges?source=`, payment acceptance `?connected_account_id=`, platform reports (all accounts in one file).
* **Window caps per call**: balance history 7 days; transfers 30 days; global account transactions 31 days; platform reports 366 days. Card transactions, payment intents and disputes are retained 2 years.
* **Platform report download URL** expires 1 minute after it is issued. Fetch it immediately on `platform_report.completed`.
* **Enablement required from Airwallex**: Linked Account APIs and native RFI calls on behalf of connected accounts. Expect permission errors in sandbox until enabled.
* **API version 2026-07-17** removed `lifecycle_id` from issuing transactions and authorizations; use `card_transaction_data`. `GET /api/v1/issuing/transactions` is deprecated in favor of `card_transactions`, `card_transaction_events` and `lifecycles`.
* **Latency budget**: remote authorization gives 2.5 seconds per card decision. The real-time path reads cached features plus `GET /api/v1/balances/current`; it never calls history endpoints live.

## A. Eligibility and account standing

These fields decide whether a customer gets any limit at all, and supply the OFAC screening inputs.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Account status and tenure | `GET /api/v1/accounts/{id}` | `status`, `created_at`, `suspend_details`, `reactivate_details`, `next_action`, `requirements` | Gate: only ACTIVE is eligible. Tenure \= today minus `created_at` |
| Business identity | `GET /api/v1/accounts/{id}` | `business_details`: name, registration address, identifiers (EIN), URLs, operating countries | OFAC screening on the entity; geography risk; web presence |
| Owners and controllers | `GET /api/v1/accounts/{id}` | `business_person_details`: nationality, roles (BENEFICIAL\_OWNER, CONTROLLING\_PERSON, AUTHORISED\_PERSON) | OFAC screening on each owner; PII depth depends on `view_type` |
| Legal entity grouping | `GET /api/v1/accounts/{id}` | `legal_entity_id` | Sum exposure across several accounts of one entity |
| CRM join key | `GET /api/v1/accounts/{id}` | `identifier`, `metadata` | Store the Salesforce ID here at account creation |
| Onboarding device | `GET /api/v1/accounts/{id}` | `terms_and_conditions.device_data`: IP address, user agent, `agreed_at` | Fraud check: onboarding IP geography vs business address |
| Open compliance requests | `GET /api/v1/rfis` | `status`, `type` (KYC, TRANSACTION, CARDHOLDER, PAYMENT\_ENABLEMENT), questions, `created_at` | Gate: open KYC or TRANSACTION RFI means zero. RFI count and age measure compliance friction |
| Capability status | `GET /api/v1/account_capabilities/{id}` | `status` (ENABLED, DISABLED, PENDING), `details.reason_codes`, `details.mcc` | Disabled reason codes expose Airwallex's own risk view; assigned MCC gives industry |
| Airwallex funding limit | `GET /api/v1/account_capabilities/funding_limits` | `limit`, `available`, `requested_limit`, `status` | Airwallex's risk-assigned direct-debit limit: a free external prior on size |
| Whole-book KYC/KYB | `POST /api/v1/platform_reports/create` type `ACCOUNT_REPORT` | KYC and KYB fields for every connected account | Nightly batch source for all customers at once |

Webhooks: `account.active`, `account.action_required`, `account.suspended`, `account.dormant`, `account.closed`, `account.offboarding.pending`, `account.offboarding.completed`, `rfi.action_required`.

## B. Liquidity and balance behavior

Balance history is the richest single source: one ledger line per balance change, with the running balance attached.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Current balance | `GET /api/v1/balances/current` | Per currency: `available_amount`, `pending_amount`, `reserved_amount`, `total_amount`, `prepayment_amount`; `account_type` (cash, yield, credit) | Live shortfall \= transaction amount minus available. Pending inflows shorten the bridge |
| Balance history | `GET /api/v1/balances/history` (7-day windows) | `posted_at`, `amount`, `balance`, `source_type`, `transaction_type`, `fee` | Daily minimum, mean and closing balance; volatility; days at or near zero; net burn; inflow/outflow ratio |
| Settlement calendar | `GET /api/v1/financial_transactions` | `transaction_type`, `source_type`, `status` (PENDING, SETTLED), `estimated_settled_at`, `net`, `fee` | When pending money lands, which sets bridge tenor |
| Whole-book balances | Platform report `BALANCE_REPORT` | `account_id`, currency, available, reserved, total, `last_update` | All customers in one nightly call |
| Whole-book ledger | Platform report `TRANSACTION_RECON_REPORT` (up to 366 days) | Every financial transaction across all connected accounts | Backfill 12 months of history at onboarding |
| Statements | Financial reports API, account statement report | Minimum and maximum balance per currency over a period | Pre-computed balance extremes |
| Multi-currency mix | `GET /api/v1/balances/current` | Non-zero currencies | Currency mismatch between spend and deposits adds FX risk |

Webhooks: `balance.ga.top_up`, `balance.va.top_up`, `balance.adjustment`.

## C. Funding and deposit reliability

This section measures the exact behavior the product bridges: whether deposits arrive, how late, and whether they stick.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Deposits | `GET /api/v1/deposits`, `GET /api/v1/deposits/{id}` | `amount`, `currency`, `status` (PENDING, SETTLED, REJECTED, REVERSED), `type` (BANK\_TRANSFER, DIRECT\_DEBIT), `created_at`, `settled_at`, `payer.name`, `reference` | Cadence, median size, gap variability, on-time rate and days-late distribution against CRM expected dates, rejected and reversed counts |
| Inbound bank credits | `GET /api/v1/global_accounts/{id}/transactions` (31-day windows) | `amount`, `payer_name`, `payer_country`, `status`, `type` (CREDIT, DEBIT), `create_time` | Payer concentration (one payer is fragile); cross-border share |
| Collection rails | `GET /api/v1/global_accounts` | Currencies, clearing systems, `status` | Number of ways money can come in |
| Linked external bank | `GET /api/v1/linked_accounts`, `GET /api/v1/linked_accounts/{id}` | `status`, `capabilities.balance_check`, `capabilities.direct_debit_deposit`, `failure_details` | A verified outside bank account is a repayment fallback |
| External bank balance | `GET /api/v1/linked_accounts/{id}/balances` (Plaid or TrueLayer links only) | `available_amount`, `currency` | Strongest capacity signal: money exists elsewhere, so the gap is timing, not solvency |
| Direct debit mandate | `GET /api/v1/linked_accounts/{id}/mandate` | Mandate status | Platform can pull repayment automatically; supports a higher limit |
| NSF on external bank | Deposits of type DIRECT\_DEBIT; transfer `funding.failure_details` | `code` (insufficient\_funds), `iso_code` (AM04), ACH return R01 | Hard negative: the outside account was empty |
| Third-party pulls | `GET /api/v1/direct_debits` | `status` (created, in review, pending, rejected, returned, settled), amounts | Recurring obligations drawn from the wallet; returns signal distress |

Webhooks: `deposit.pending`, `deposit.settled`, `deposit.rejected`, `deposit.reversed`, `direct_debit.returned`, `direct_debit.rejected`, `linked_account.failed`, `linked_account.suspended`.

## D. Card spend behavior

Card data sizes the need: how much the customer spends, when, on what, and how often the wallet has already run dry.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Card transactions | `GET /api/v1/issuing/card_transactions` | `status`, `failure_reason`, `billing_amounts.*`, `merchant.category_code`, `merchant.country`, merchant brand IDs, `fund_direction`, `digital_wallet_token`, `created_at` | Monthly spend, volatility, day-of-month peaks, MCC mix, merchant concentration, recurring merchants, decline rate by reason |
| Shortfall label | Same endpoint, `status=DECLINED`, `failure_reason=INSUFFICIENT_FUNDS` | Count, amount, date | Historical shortfall frequency and size; the p90 shortfall is a natural base size |
| Transaction events | `GET /api/v1/issuing/card_transaction_events` | `subtype` (incremental auth, partial reversal), `process_result`, `transaction_category` (PURCHASE, CASH\_DISBURSEMENT, QUASI\_CASH, BILL\_PAYMENT), `pos_entry_mode`, `conversion_details`, `risk_details`, `remote_auth` | Cash and quasi-cash use is a red flag; `risk_factors` and failed 3DS indicate fraud |
| Lifecycles | `GET /api/v1/issuing/lifecycles` | Aggregated `billing_amounts` per payment | Cheap spend totals without summing events |
| Pending holds | `GET /api/v1/issuing/authorizations` | Open authorizations | Committed spend not yet captured |
| Cards and controls | `GET /api/v1/issuing/cards`, `GET /api/v1/issuing/cards/{id}` | `card_status`, `program.type` and `sub_type`, `form_factor`, `authorization_controls` (allowed MCCs, countries, `transaction_limits`), `auto_close_date` | Card limit caps the per-transaction bridge; number of active cards |
| Limit utilization | `GET /api/v1/issuing/cards/{id}/limits` | `amount`, `remaining` per interval | How close the customer runs to its own card limits |
| Cardholders | `GET /api/v1/issuing/cardholders` | Count, `status`, `type` | Headcount proxy; DISABLED cardholders |
| Card disputes | `GET /api/v1/issuing/transaction_disputes` | `status`, outcome | Spend that may reverse; abuse pattern |
| Whole-book spend | Platform report `ISSUING_TRANSACTION_REPORT` | Every issuing transaction across accounts | Nightly batch source |

Webhooks: `issuing.card_transaction.authorized`, `.cleared`, `.declined`, `.reversed`; `issuing.transaction.failed`; `issuing.card.low_remaining_transaction_limit`; `issuing.card.blocked`, `.lost`, `.stolen`; `issuing.transaction_dispute.*`.

In production the "should we fund" call happens inside remote authorization. Its payload already carries `card_id`, amount, merchant, MCC, `billing_order` and 3DS data, with 2.5 seconds to answer.

## E. Revenue and receivables

These show the customer's repayment source, but they populate only if the customer also collects payments or invoices through Airwallex. For a spend-only customer they are empty and CRM or accounting data substitutes.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Card acquiring revenue | `GET /api/v1/pa/payment_intents?connected_account_id=` | `amount`, `currency`, `status`, `created_at` | Sales run-rate: the primary repayment source |
| Settlement timing | `GET /api/v1/financial_transactions` (types PAYMENT, PAYMENT\_RESERVE\_HOLD, PAYMENT\_RESERVE\_RELEASE); settlements list | `estimated_settled_at`, `settled_at`, reserve amounts | When receivables become cash; an Airwallex reserve hold is a risk flag on the merchant |
| Refunds | `GET /api/v1/pa/refunds?connected_account_id=` | Amount, `status` | Refund ratio \= revenue quality |
| Chargebacks | `GET /api/v1/pa/payment_disputes?connected_account_id=` | `stage`, `status`, `reason.type` (e.g. FRAUDULENT), `amount`, `due_at` | Chargeback rate; lost disputes debit the wallet (open team concern) |
| Invoices issued | `GET /api/v1/billing/invoices` | `status`, `payment_status` (UNPAID, PAID), amounts, dates | Outstanding receivables \= expected inflows; late-paying customers of the customer |
| Invoice collection | `GET /api/v1/billing/billing_transactions?invoice_id=` | `status`, `succeeded_at` | Collection success rate |
| Subscriptions | Billing subscriptions list | `status` (active, unpaid, cancelled) | Recurring revenue stability |

Webhooks: `payment_intent.succeeded`, `payment_attempt.settled`, `refund.settled`, `invoice.paid`, `invoice.payment_failed`, `subscription.unpaid`.

## F. Obligations and outflows

Scheduled outflows compete with bridge repayment for the same incoming deposit.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Payouts | `GET /api/v1/transfers` | `transfer_amount`, `status`, `transfer_date`, `beneficiary`, `funding.status`, `funding.failure_details`, `failure.code`, `reason` | Upcoming outflows; a payout that failed funding for insufficient funds signals distress |
| Payees | `GET /api/v1/beneficiaries` | Count, `bank_country_code`, `entity_type` | Payee geography for sanctions exposure; payroll-like patterns |
| Payables (AP) | `GET /api/v1/spend/bills` | `status` (AWAITING\_PAYMENT, SCHEDULED, PAID), `billing_amount`, `due_date`, `vendor_id` | Bills due before the expected deposit reduce repayment capacity |
| Vendors | Spend vendors list | Vendor count and concentration | Dependence on a few suppliers |
| FX conversions | FX conversions list | `status` (SCHEDULED, OVERDUE, SETTLED), currency pair, amounts | An OVERDUE conversion signals distress; spend currency vs deposit currency mismatch |
| Third-party pulls | `GET /api/v1/direct_debits` | See section C | Recurring obligations |
| Whole-book payouts | Platform report `PAYOUT_REPORT` | Every payout across accounts, including `failure_reason` | Nightly batch source |

Webhooks: `transfer.new`, `transfer.pending`, `transfer.settled`, `transfer.suspended`, `transfer.failed`.

## G. Bridge history and platform exposure

The platform's own ledger with each customer can be rebuilt entirely from Airwallex, provided every transfer and charge is tagged consistently.

| Signal | Endpoint | Key fields | Use in the limit |
| ----- | ----- | ----- | ----- |
| Bridges sent | `GET /api/v1/connected_account_transfers?destination=acct_...` | `amount`, `status` (NEW, PENDING, SETTLED, SUSPENDED, FAILED), `created_at`, `reason`, `reference`, `request_id`, `failure_reasons` | Count and size of past bridges. Failure reasons such as WATCHLIST\_RESTRICTION\_BENEFICIARY or OTHER\_HIGH\_RISK\_BENEFICIARY are Airwallex compliance blocks |
| Idempotency check | `GET /api/v1/connected_account_transfers?request_id=` | Match on `request_id` | Required before any retry after a non-success response |
| Repayments and fees | `GET /api/v1/charges?source=acct_...` | `amount`, `status` (NEW, PENDING, SETTLED, SUSPENDED, FAILED), `created_at`, `reason` | Days from bridge to repayment; a FAILED repayment charge is a default event |
| Outstanding exposure | Derived | Settled bridges minus settled repayment charges | Available headroom \= assigned limit minus outstanding |
| Fee revenue | Charges with fee reason; platform report `APPLICATION_FEE_REPORT` | Amounts, status | Customer profitability, which feeds rationing priority |

Tagging convention: prefix `request_id` and set `reason` as `bridge-`, `repay-` or `fee-` so these three flows separate without a local database.

Webhooks: `connected_account_transfer.new`, `.pending`, `.settled`, `.suspended`, `.failed`; `charge.new`, `.pending`, `.settled`, `.suspended`, `.failed`.

## Internal client profile (CRM)

The expected funding schedule is the one CRM field the model cannot run without: Airwallex records when a deposit arrived, never when it was due.

| Field | Why it matters |
| ----- | ----- |
| Contracted funding schedule (expected deposit date, amount, frequency) | Defines "late"; lateness and days-late are uncomputable without it |
| Customer since, contract start, renewal date | Tenure; churn risk rises near renewal |
| Plan tier, fees and ARR paid to the platform | Customer value; sets expected-loss tolerance and rationing priority |
| Industry (NAICS), business model, seasonality | Expected spend volatility; seasonal shortfalls are lower risk |
| Annual revenue, employee count, funding stage, last raise date, cash runway | Capacity to repay |
| External business credit (D\&B PAYDEX, Experian Intelliscore), guarantor score | Independent default prior |
| Accounting integration (QuickBooks, Xero, NetSuite): AR aging, AP aging, P\&L | Receivables and payables that never touch Airwallex |
| External bank data via Plaid, if not linked in Airwallex | Balance held elsewhere |
| Collections history: past-due platform invoices, write-offs | Payment behavior toward the platform itself |
| Personal guarantee, UCC liens, existing debt or merchant cash advances | Seniority of the platform's claim |
| Sanctions and adverse-media screening result with timestamp | Rule 3: OFAC SDN check on entity and owners, logged for audit |
| Corporate family: parent, subsidiaries, related accounts | Group-level exposure cap |
| Support escalations, NPS, account-owner notes, manual risk flags | Soft signals of distress or disputes |
| Manual limit overrides with reason and approver | Transparency: every non-model limit is explained |
| Profile-change events (address, owner, bank details) | Re-score trigger; owner or bank changes are fraud markers |

## Refresh model

Limits are computed in the background, by a nightly batch plus event-driven re-scores, so the live decision touches only the cached limit and one balance call.

Architecture: three sources (nightly batch of platform reports and per-account REST history; Airwallex webhooks; CRM sync from Salesforce) feed a client feature store that records value, endpoint and timestamp per feature. Limit assignment (gates, base, modifiers, portfolio reserve floor) reads the store. The live funding decision reads the cached limit and features plus `GET /api/v1/balances/current` and answers within 2.5 seconds.

Webhooks bypass the 24-hour cycle for the events below; everything else waits for the nightly batch.

| Event | Effect on the limit |
| ----- | ----- |
| `account.suspended`, `account.dormant`, `account.action_required`, `rfi.action_required` | Set to zero immediately |
| `charge.failed` (repayment) | Freeze; record a default event |
| `deposit.rejected`, `deposit.reversed`, `direct_debit.returned` | Cut and re-score |
| `connected_account_transfer.failed`, `connected_account_transfer.suspended` | Query by `request_id` before any retry; flag the customer |
| `deposit.settled`, `balance.ga.top_up` | Recompute outstanding exposure; trigger the repayment charge |
| `issuing.card_transaction.declined` with INSUFFICIENT\_FUNDS | Append to shortfall history |
| `issuing.transaction_dispute.*`, payment dispute events | Re-score |
| CRM profile update | Re-score |

## How signals map to the limit

Signals fall into four roles; the Decision Policy owner sets the weights and thresholds.

1. **Gates** (any failure sets the limit to zero): account ACTIVE; no open KYC or TRANSACTION RFI; entity and every owner clear on OFAC; no FAILED repayment charge outstanding; no deposit reversed within the policy window.
2. **Base size**: the smallest of the customer's typical shortfall, a share of verified monthly inflow, and the card's per-transaction limit.
3. **Modifiers**: raise for on-time deposits, long tenure, a linked external balance, a direct-debit mandate and clean repayments. Cut for late deposits, payer concentration, risky MCCs, prior declines for fraud, chargebacks and card disputes.
4. **Portfolio constraint**: limits are checked against the platform reserve floor and a per-customer concentration cap; when capital is scarce, rank requests by expected loss per dollar and customer value.

A starting form for the base size, with s \= shortfall amounts from INSUFFICIENT\_FUNDS declines and I \= trailing 90-day settled deposits:

**L\_base \= min( P90(s), α × I / 3, C\_txn ),  L \= L\_base × Π m\_i**

α is the policy share of monthly inflow, C\_txn the card's per-transaction limit, and each m\_i a modifier. Transparency (rule 1\) requires storing every feature value with its source endpoint and fetch timestamp beside the limit it produced.

## Sandbox gaps and test-data plan

The sandbox cannot produce history on its own, so the demo needs seeded customers with deliberately different profiles.

| Gap | Consequence | Workaround |
| ----- | ----- | ----- |
| Simulated deposits settle instantly and cannot be backdated | No native lateness and only days of history | Put expected dates in the CRM mock; send `POST /api/v1/simulation/deposit/create` after them. Seed 90 days of synthetic history in the profile mock and use live Airwallex for current state |
| No INSUFFICIENT\_FUNDS declines exist | The shortfall label is empty | Simulate spend above wallet balance but within the card limit, with `x-on-behalf-of` on `POST /api/v1/simulation/issuing/create` |
| Platform wallet holds about 10M per major currency | The reserve floor never binds | Set the bridge budget and reserve floor in app config, well below total customer need |
| Linked-account balances need Plaid or TrueLayer and Airwallex enablement | External balance signal unavailable | Mark as production-only; mock the value in the CRM profile |
| RFI calls on behalf of connected accounts need enablement | Compliance gate untestable live | Read `account.status` live; mock RFI state |
| Acquiring, Billing and Spend data are empty | Section E and AP signals absent | Treat as optional; the model must run without them |

Three archetype customers force rationing: one reliable payer with early deposits, one chronically late payer who always repays, and one new, volatile account with a chargeback and a reversed deposit.

## Sources

* Live sandbox queries through the Airwallex Developer connector, 2026-10-18: account details, balances, balance history, global accounts, deposits, cardholders, cards, card limits, card transactions, transfers, payment intents, disputes, direct debits, linked accounts, FX conversions, beneficiaries, billing invoices, spend bills
* [Retrieve a connected account](https://www.airwallex.com/docs/api/scale/accounts/retrieve.md)
* [List RFIs](https://www.airwallex.com/docs/api/risk/request_for_information_rfi/list.md)
* [Retrieve an account capability](https://www.airwallex.com/docs/api/account_capability/account_capability/retrieve.md)
* [Get funding limits](https://www.airwallex.com/docs/api/account_capability/account_capability/funding_limits.md)
* [Linked account balances](https://www.airwallex.com/docs/api/core_resources/linked_accounts/balances.md)
* [List financial transactions](https://www.airwallex.com/docs/api/finance/financial_transactions/list.md)
* [Card transactions](https://www.airwallex.com/docs/api/issuing/card_transactions.md)
* [Card transaction events](https://www.airwallex.com/docs/api/issuing/card_transaction_events.md)
* [List connected account transfers](https://www.airwallex.com/docs/api/scale/connected_account_transfers/list.md)
* [List charges](https://www.airwallex.com/docs/api/scale/charges/list.md)
* [Create a platform report](https://www.airwallex.com/docs/api/scale/platform_reports/create.md)
* [List payment disputes](https://www.airwallex.com/docs/api/payments/payment_disputes/list.md)
* [List billing invoices](https://www.airwallex.com/docs/api/billing/invoices/list.md)
* [List spend bills](https://www.airwallex.com/docs/api/spend/bills/list.md)
* [Account webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/account.md)
* [Deposit webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/deposits.md)
* [Issuing webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/issuing.md)

# Transfer module: bridge funding and fee collection

Oct 18, 2026 · Charley

Airwallex cards hold no balance of their own: they draw from the connected account's wallet. Funding the card therefore means a connected account transfer from the platform wallet, and every repayment or fee is a Charge in the reverse direction.

## How money moves

1. **Bridge advance**: platform wallet → customer wallet via `connected_account_transfers`.
2. **Spend draws the wallet**: the card payment uses one currency wallet.
3. **Deposit settles**: the customer's late deposit lands in their wallet (Global Account or direct debit).
4. **Fee, then principal**: customer wallet → platform wallet via `charges`, capped at the deposit.

![][image1]

Only steps 1 and 4 are platform-initiated money movements; steps 2 and 3 happen on the customer's side and are observed through webhooks.

## Part 1: Funding the customer's card

One write call moves the money: `POST /api/v1/connected_account_transfers/create`. The other nine steps make sure the transfer is the right size, actually lands, and actually lets the payment through.

| \# | Purpose | Endpoint | Header | Check or payload |
| ----- | ----- | ----- | ----- | ----- |
| 1 | Customer can receive funds | `GET /api/v1/accounts/{id}` | none | `status` \= ACTIVE. Transfers to a non-operational account fail with `validation_failed` |
| 2 | Card can use the money | `GET /api/v1/issuing/cards/{card_id}`, `GET /api/v1/issuing/cards/{card_id}/limits` | `x-on-behalf-of` | `card_status` ACTIVE, MCC allowed, amount within remaining per-transaction and monthly limits. If not, the payment declines for a reason a bridge cannot fix |
| 3 | Size the shortfall | `GET /api/v1/balances/current` | `x-on-behalf-of` | Shortfall \= amount minus `available_amount` in the transaction currency. A card payment draws from one currency wallet only, so balances in other currencies do not add up |
| 4 | Stay inside the assigned limit | `GET /api/v1/connected_account_transfers?destination=`, `GET /api/v1/charges?source=` | none | Shortfall ≤ assigned limit minus outstanding principal |
| 5 | Respect the reserve floor | `GET /api/v1/balances/current` | none (platform) | Platform `available_amount` minus shortfall ≥ reserve floor |
| 6 | Get the right currency (only if missing) | `POST /api/v1/fx/conversions/create` | none (platform) | Buy the shortfall amount in the transaction currency |
| 7 | Move the money | `POST /api/v1/connected_account_transfers/create` | none | `amount` \= shortfall, `currency`, `destination` \= `acct_...`, `reason` \= `business_expenses`, `reference` \= "Bridge advance, card ending 0134", `request_id` \= `bridge-<card transaction id>` |
| 8 | Confirm it landed | Create response, `connected_account_transfer.settled` webhook, or `GET /api/v1/connected_account_transfers?request_id=` | none | SETTLED \= funded. PENDING \= held by transaction monitoring; do not approve. FAILED \= funds returned; log `failure_reasons` |
| 9 | Re-check and approve | `GET /api/v1/balances/current` | `x-on-behalf-of` | `available_amount` ≥ payment amount, then the deterministic engine approves |
| 10 | Run the payment (sandbox) | `POST /api/v1/simulation/issuing/create` | `x-on-behalf-of` | Simulated card spend against the funded wallet |

Record in the ledger: principal, currency, transfer `id`, `request_id`, SETTLED timestamp (fee accrual starts here), card transaction id, and the agent's rationale.

## Funding rules and failure handling

* **Bridge the shortfall, not the payment.** The customer's own balance covers the rest; this keeps platform exposure minimal.
* **Idempotency.** `request_id` is deterministic per card transaction. On a timeout, 5xx or any non-success, query `GET /api/v1/connected_account_transfers?request_id=` before retrying. A `request_id_duplicate` error returns `details.source_id`: the transfer already exists, so treat it as created.
* **`insufficient_fund` on the transfer** means the platform wallet is empty in that currency. Decline and alert treasury; never fall back to another currency silently.
* **Funds are fungible once credited.** Bridged money is ordinary wallet balance: other card payments, payouts and conversions can spend it. Fund immediately before the payment, then re-check (step 9). An outbound payout soon after a bridge is a misuse flag.
* **PENDING transfers are debited from the platform but not yet credited.** Count them as outstanding exposure; do not approve the payment until SETTLED.
* **Production timing.** A live card authorization does not wait for a transfer. Airwallex's Platform Liquidity Program funds authorizations just in time from a platform pool (`POST /api/v1/platform_liquidity_programs/{id}/program_spending_accounts/create`, `.../deposit`) and needs Account Executive setup. The hackathon flow is unaffected: the deterministic engine fronts the simulated payment.

## Part 2: Fee design

One rate, one minimum, one due date. Every bridge accrues **0.05% per day** on the principal still outstanding, every advance pays **at least 7 days** of that fee, and everything owed is due at the **next settled deposit or day 30**, whichever comes first.

### The rate

Quote it to customers as **0.05% per day: \$0.50 per \$1,000 per day** (1.5% per 30 days, about 18% annualized). The fee accrues daily from the moment the bridge transfer settles until the repayment charge settles, on the principal still outstanding. A partial repayment lowers the fee from the day it settles. There is no fee on unpaid fees and no fee on an undrawn limit.

**Formula:** Fee per advance \= max(r\_d × Σ P\_d, 7 × r\_d × P\_0), with r\_d \= 0.05% per day, P\_d the principal outstanding at the end of day d, and P\_0 the amount advanced.

Both inputs come from Airwallex timestamps (transfer SETTLED, charge SETTLED), so every fee is reproducible from Airwallex records alone (rule 1).

### Guardrail 1: 7-day minimum fee

Every advance pays at least 7 days of accrual, which is 0.35% of the amount advanced, even if it is repaid sooner. A default loses the whole principal however short the bridge was; the minimum keeps very short bridges from being priced below that risk. It is charged once per advance and never refunded.

### Guardrail 2: 30-day maximum term

Repayment is due at the next settled deposit or 30 days after the bridge transfer settles, whichever comes first. If an advance is not fully repaid by day 30, the customer's limit is frozen, the customer is marked delinquent, and the platform pulls by direct debit if a mandate exists. The fee keeps accruing at the same 0.05% per day: no penalty rate and no late fee.

### Guardrail 3: Fee collected with principal from the next deposit

There is no monthly statement. When the next deposit settles, the waterfall collects the accrued fee first, then principal, as two separate charges. Whatever that deposit cannot cover carries to the following deposit.

### Examples: \$5,000 advance

| Repaid after | Accrued at 0.05%/day | 7-day minimum | Fee charged |
| :---- | :---- | :---- | :---- |
| 3 days | \$7.50 | \$17.50 | \$17.50 |
| 7 days | \$17.50 | \$17.50 | \$17.50 |
| 10 days | \$25.00 | \$17.50 | \$25.00 |
| 30 days | \$75.00 | \$17.50 | \$75.00 |
| \$3,000 repaid on day 10, the rest on day 15 | (5,000 × 10 \+ 2,000 × 5\) × 0.05% \= \$30.00 | \$17.50 | \$30.00 |

Quote shown to the customer at the moment of the advance: "Bridge of \$5000. Fee: 0.05% per day (\$2.50 per day), minimum \$17.50. Due from your next deposit, no later than Nov 18."

### Parameters

| Parameter | Value | Note |
| :---- | :---- | :---- |
| Daily rate | 0.05% (1.5% per 30 days) | One rate for every customer; review only if cost of funds moves materially |
| Minimum fee | 7 days of accrual on the amount advanced (0.35%) | Charged once per advance; not refunded on early repayment |
| Maximum term | 30 days from transfer SETTLED | Hard due date |
| Collection | Next settled deposit: fee first, then principal | Replaces the month-end statement |
| After day 30 | Same daily rate; limit frozen; delinquent | No penalty rate, no late fee |
| Undrawn limit | No fee | Access without use costs nothing |

Charging for advances likely makes this commercial financing. California requires cost disclosures, including an estimated APR, for commercial financing up to \$500,000, and rate limits can apply to lenders without a California Financing Law license. The 7-day minimum raises the effective APR on very short advances: a 3-day bridge works out to about 43% annualized. Confirm with counsel before production; this is not legal advice.

## Collection endpoints

Collection is two Charges per deposit, fee and principal kept separate. Airwallex's own docs recommend this split for loan repayments because it reconciles cleanly.

| Purpose | Endpoint | Header | Notes |
| ----- | ----- | ----- | ----- |
| Detect the deposit | Webhooks `deposit.settled`, `balance.ga.top_up` | n/a | `account_id` in the payload identifies the customer |
| Confirm the deposit | `GET /api/v1/deposits/{id}` | `x-on-behalf-of` | `status` SETTLED, `amount`, `currency`, `type` (BANK\_TRANSFER or DIRECT\_DEBIT) |
| Collectible balance | `GET /api/v1/balances/current` | `x-on-behalf-of` | Use `available_amount`; it excludes funds reserved for pending card authorizations |
| Rebuild what is owed | `GET /api/v1/connected_account_transfers?destination=`, `GET /api/v1/charges?source=` | none | Principal out, principal repaid, fees paid |
| Match currencies | `POST /api/v1/fx/conversions/create` | `x-on-behalf-of` | Charges move one currency only; convert inside the customer wallet first if the deposit currency differs |
| Collect the fee | `POST /api/v1/charges/create` | none | `source` \= `acct_...`, `amount` \= fee due, `reason` \= `professional_business_services`, `reference` \= "Bridge access fee, Oct 2026", `request_id` \= `fee-<acct>-<YYYYMM>` |
| Collect principal | `POST /api/v1/charges/create` | none | `reason` \= `loan_credit_repayment`, `request_id` \= `repay-<deposit id>` |
| Confirm collection | Webhooks `charge.settled`, `charge.failed`, `charge.suspended`; `GET /api/v1/charges?request_id=` | none | Check by `request_id` before any retry |
| Fallback pull | `GET /api/v1/account_capabilities/funding_limits`, `POST /api/v1/deposits/create` (DIRECT\_DEBIT) | `x-on-behalf-of` | Pulls from the customer's linked bank into their wallet when no deposit arrives; needs a linked account and an active mandate |

Charges require the source account to be ACTIVE. A charge larger than the wallet balance is refused with `insufficient_fund` and nothing moves.

## Deposit-sufficiency rules

Each settled deposit is split by a fixed waterfall: fee due first, then principal, oldest bridge first. Fees go first because they are what the next deposit is contracted to cover; principal follows so the remainder still frees bridge capital for other customers.

Definitions, all in the bridge currency:

* **D**: amount of the settled deposit
* **A**: customer `available_amount` after the deposit
* **F**: fee due from closed statements, unpaid
* **P**: principal outstanding
* **m**: operating floor left with the customer (hackathon: 0; production: about 7 days of median card spend)
* **C**: collectible \= max(0, min(D, A − m)). Collection never takes more than the new deposit, and never touches funds reserved for pending card payments.

| Condition | Action | Customer state |
| ----- | ----- | ----- |
| C ≥ F \+ P | Charge F, then charge P | Current; full limit headroom restored |
| F ≤ C \< F \+ P | Charge F, then charge C − F against principal | Current on fees; headroom \= limit − remaining P |
| 0 \< C \< F | Charge C against the fee; carry F − C | Delinquent; new bridges blocked |
| C \= 0 | No charge; retry on the next deposit or in 24 hours | Delinquent once a fee is past due |
| Deposit currency ≠ bridge currency | Convert inside the customer wallet, then apply the rows above | Unchanged |
| Deposit still PENDING | Wait for `deposit.settled` | Unchanged |
| No settled deposit by expected date \+ 5 days | Pull by direct debit if a mandate exists; otherwise limit \= 0 and manual collections | Past due |
| `deposit.reversed` after collection | Limit \= 0; re-score; amounts already collected stand | Under review |
| Charge refused `insufficient_fund` | Re-read balance, recompute C once, retry with a suffixed `request_id` | Unchanged |
| Charge PENDING or SUSPENDED | Do not re-charge; wait for `charge.settled` or `charge.failed` | Unchanged |
| Charge FAILED (repayment) | Record a default event; freeze the limit | Default review |

## Runbook

On every `deposit.settled` for a customer with anything owed:

1. `GET /api/v1/deposits/{id}` with `x-on-behalf-of`; continue only if SETTLED.
2. Lock the customer: no new bridge while collection runs.
3. Rebuild P and F from transfers, charges and closed statements. Both zero: unlock and stop.
4. `GET /api/v1/balances/current` with `x-on-behalf-of`; compute C.
5. Convert currency inside the customer wallet if needed.
6. `POST /api/v1/charges/create` for the fee (`fee-<acct>-<YYYYMM>`); confirm SETTLED.
7. `POST /api/v1/charges/create` for principal (`repay-<deposit id>`); confirm SETTLED.
8. Update the ledger, limit headroom and customer state; send the customer a breakdown of deposit, fee, principal and balance left.
9. Unlock.

At month end: compute F per customer with the formula above, issue the statement, and mark it due for the next deposit.

## Sources

* [Create a connected account transfer](https://www.airwallex.com/docs/api/scale/connected_account_transfers/create.md)
* [Create a charge](https://www.airwallex.com/docs/api/scale/charges/create.md)
* [Move funds between accounts: transfers and charges](https://www.airwallex.com/docs/connected-accounts/move-funds/charges.md)
* [Wallets and funds flow](https://www.airwallex.com/docs/connected-accounts/about/wallets-and-funds-flow.md)
* [Issuing funding sources and Platform Liquidity Program](https://www.airwallex.com/docs/issuing/get-started/fund-your-issuing-balance.md)
* [Create a conversion](https://www.airwallex.com/docs/api/transactional_fx/conversion/create.md)
* [Create a deposit via direct debit](https://www.airwallex.com/docs/api/core_resources/deposits/create.md)
* [Deposit webhooks](https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/deposits.md)

# **Bridge Capital Underwriting Rules v001**

Oct 17, 2026 · @Santiago

We are a corporate-card platform on Airwallex. When a customer's wallet can't cover a payment, we lend from our own bridge facility and collect it back from their wallet. Every decision runs in two stages: a daily precheck that sets each customer's credit line, and a fast check on each request against that line and the facility.

## **Key terms**

| Term | Meaning |
| :---- | :---- |
| Bridge facility | Our own money set aside for bridges, shared by all customers. \$50k in the demo; millions in production |
| Reserve floor | The slice of the facility we never lend: 5% |
| Capital level | Free capital as a share of what's lendable (facility minus floor) |
| Credit line | The most one customer can have bridged at once. Set daily, from the customer's profile only |
| Bridge | Money we move into a customer's wallet to cover a payment, repaid by a due date |
| Tier | A, B or C: how strong the customer is, from four signals |
| Door | Whether a tier can get a bridge right now, given the capital level |

## **Facility rules**

The bridge facility is the pot of our own money that funds every bridge. These rules protect it. They're percentages, so they work the same at \$50k or \$5M.

| Rule | Value | Demo (\$50k) |
| :---- | :---- | :---- |
| Reserve floor | 5% | \$2.5k |
| Max per customer, early | 20% | \$10k |
| Max per customer, mature | 10% | \$5k |

•      **Reserve floor:** a small buffer we never lend, so the facility never hits zero.
•      **Max per customer:** no single customer takes too big a share, so one default can't sink the facility. 20% while the portfolio is young, 10% once it's mature.

**Capital level** tells us how much of the facility is still free: free capital ÷ lendable capital (facility minus floor). Example: \$47.5k lendable, \$28.5k lent, \$19k free \= 40%.

| Capital level | Free capital |
| :---- | :---- |
| A lot | above 70% |
| Normal | 40 to 70% |
| Less | 10 to 40% |
| Critical | below 10% |
| At floor | 0%: no new bridges |

The lower the level, the fewer customers we serve (see Per request). A level only reopens 5 points above where it closed, so doors don't flip open and shut with every bridge.

## **Daily precheck**

Every customer gets a credit line before they ever ask, refreshed daily. When they need a bridge, the hard work is already done. Five steps:

1\.     Enough data?
2\.     No-go rules
3\.     Rate four signals
4\.     Assign a tier
5\.     Set the credit line

**Step 1: Enough data?**

We need at least 90 days of the customer's activity on our platform, or equivalent history from their bank or our CRM. Without it we can't read their signals, so there's no line yet.

**Step 2: No-go rules**

Deal breakers. If any applies, the customer gets no line, however good the rest looks.

| Rule | Why |
| :---- | :---- |
| Excluded industry | Outside our risk appetite |
| Fraud flag | Basic hygiene |
| Account frozen or closed | Basic hygiene |
| Past write-off with us | They already cost us money once |
| Mono client: one client is over 70% of their income | If that one client pays late, so does our customer |

**Step 3: Four signals**

Each signal answers one question about whether the customer can repay us. We rate each green (strong), yellow (watch) or red (serious weakness).

•      **Inflow reliability.** Do the customer's own clients pay them on time? That money is what repays us, so this matters most. Green: 90%+ of invoices paid on time. Yellow: 75 to 90%. Red: below 75%.
•      **Inflow concentration.** How much depends on their biggest client? Green: top client is 40% or less of income. Yellow: 40 to 70%. Above 70% is a no-go.
•      **Cash buffer.** Does their wallet often run close to zero? A thin buffer leaves no room when something slips. Green: rarely. Yellow: sometimes. Red: often.
•      **Track record.** Have they repaid us before? Green: always on time. Yellow: never borrowed. Red: paid late before.

**Step 4: Tier**

The four ratings combine into a tier. Strong points don't cancel out weak ones: if their clients don't pay reliably, nothing else makes up for it.

| Tier | Rule | What it means |
| :---- | :---- | :---- |
| A | All green, or one yellow | Strong customer, no real weaknesses |
| B | Two yellows | Good customer, minor weaknesses |
| C | Three or more yellows, or one red (not on reliability) | One serious weakness, but their clients still pay |
| No line | Red on inflow reliability | Their clients don't pay reliably, so neither would they |

**Step 5: Credit line**

The credit line is the most a customer can have bridged at once. It's a share of what flows into their wallet each month, because that's what repays us.

Credit line \= average monthly inflows × multiplier, capped at the max per customer.

| Tier | Multiplier | Roughly equals |
| :---- | :---- | :---- |
| A | 30% | 9 days of inflows |
| B | 20% | 6 days of inflows |
| C | 10% | 3 days of inflows |
| Starter | 10% (5% if signals are weaker) | Never borrowed from us |

**Starter** means a new borrower starts small whatever their tier: "we believe in you, let's start here." After 2 to 3 bridges repaid on time, they move to their full tier.

Example, \$40k monthly inflows: Tier A \= \$10k (capped), Tier B \= \$8k, Tier C \= \$4k.

## **Per request**

When a customer needs a bridge, three quick checks run. All pass: we cover the full amount. Any fails: no bridge.

1\.     **Spend check.** We only fund payments we can trace. No cash withdrawals, because we can't see where cash goes.
2\.     **Line check.** The request fits in what's left of their line (line minus what they already owe), and nothing they owe is more than 2 days late. Example: \$100k line, \$20k owed, so up to \$80k passes.
3\.     **Facility check.** We have the capital right now. See the doors below.

**Doors**

The less free capital we have, the fewer customers we serve, strongest first.

•      **Auto:** approved instantly by the rules.
•      **Human:** a credit officer approves.
•      **Closed:** no bridge at this capital level.

| Capital level | Tier A | Tier B | Tier C |
| :---- | :---- | :---- | :---- |
| A lot | Auto | Auto | Auto |
| Normal | Auto | Auto | Human |
| Less | Auto | Human | Closed |
| Critical | Human | Closed | Closed |
| At floor | Closed | Closed | Closed |

When capital is short, we serve by tier, then first come, first served. We never shrink a customer's line because we're short: the best customers get their full amount, the rest get a clear no.

A request can wait up to 6 hours, only if expected repayments will free the capital in time. The customer always gets a yes, a firm no, or a time for the decision. A no is final for that request.

## **Collections**

On the due date we collect straight from the customer's wallet. They're only late if the wallet doesn't hold enough.

1\.     **Due date** (for example, 30 days after the bridge): we pull the full amount from their wallet.
2\.     **Wallet short:** we take what's there; the rest is past due.
3\.     **Every new deposit** goes to the past-due amount first, until it's cleared.

**When a customer is late**

| Days late | What happens | Who acts |
| :---- | :---- | :---- |
| 1 to 2 | Grace: line still usable | Reminders only |
| 3 to 15 | Line frozen; cards work on their own money | Agent or human follows up |
| 16+ | Cards blocked until repaid | Human, collections |

Paid in full at any stage: the line reopens and the next daily precheck re-rates them.

**Customer communications**

Every step has a message, so the customer is never surprised.

| When | What we tell the customer |
| :---- | :---- |
| 3 days before due | “Your \$X bridge is due on \[date\]. We'll collect it from your wallet automatically.” |
| Due date, collected | “We collected \$X. Your bridge is repaid and your line is available again.” |
| Due date, wallet short | “We collected \$Y of \$X. Please add \$Z within 2 days; we'll collect it automatically.” |
| Day 3 | “Your \$Z is 3 days late, so your credit line is paused. Your cards still work with your own funds. Our team will contact you.” |
| Day 16 | “Your cards are blocked until the \$Z is repaid. Contact us to agree a plan.” |
| Repaid after being late | “Thanks, you're all paid up. Your cards and credit line are active again.” |

## **Approval layers**

| Layer | When | Who decides |
| :---- | :---- | :---- |
| Auto | Passes all checks and the door says Auto | The rules |
| Human review | The door says Human | A credit officer, approving these exact terms |
| Outside the rules | Anything the rules don't cover | Declined, or escalated to a human |

Every decision is logged with its inputs and the rule that decided it, so any yes or no can be explained.

## **Final schema: the credit decision**

**![credit decision · daily precheck and per-request checks][image2]**

credit decision · daily precheck and per-request checks

The top lane runs every day for every customer and produces a credit line. The bottom lane runs on each request and only checks the request against that line and the facility.

## **Final schema: collections**

**![collections · due date to repaid or blocked][image3]**

collections · due date to repaid or blocked

A full repayment at any stage returns the customer to Repaid, and the next daily precheck re-rates them.

## **Data mapping**

Where each input comes from, checked against the Airwallex API docs. "Verify" means it still needs a live test in our sandbox (Charley).

| Input | Source | Status |
| :---- | :---- | :---- |
| Account status, onboarding date (tenure) | Airwallex connected accounts | Seen in dashboard; verify via API |
| Daily balances (cash buffer) | Airwallex balance history | Endpoint exists; verify on a connected account |
| Deposits by payer (concentration) | Airwallex deposits, payer name | Verify payer names are reliable |
| Invoice due dates (reliability) | Customer's invoicing or accounting system | Not in Airwallex; synthetic for demo |
| Card spend and merchant category (spend check) | Airwallex card transactions | Tested Oct 12 |
| Bridges, repayments, write-offs (track record) | Our ledger, plus Airwallex transfers and charges | Ours to build |
| Industry, fraud flags, notes | CRM (Salesforce) | Synthetic for demo |
| Facility size, floor | Our config | Ours |

Two things to know:

•      **Airwallex shows when money arrived, not when it was due.** On-time payment needs invoice due dates, which live in the customer's invoicing system, not Airwallex.
•      **Reading a connected account's balances may need an extra security token** (SCA). Charley to confirm in sandbox.

## **Open for next pass**

•      ☐ Agent boundary: what the agent reads and proposes, and what stays in rules
•      ☐ Pricing: minimum rate \= our opportunity cost, plus a risk premium by tier
•      ☐ Collections detail: messages, agent role from day 3, the human handoff
•      ☐ Charley: can our cards withdraw ATM cash at all? Can we block payouts from a customer's wallet while a bridge is outstanding?
•      ☐ Charley: can we pull from a customer's wallet on the due date (charges)?

# Salesforce Context Integration Spec

## Purpose and scope

The Salesforce integration enriches the agent's view of each small-business customer with verified profile, contract, credit-review, and customer-interaction context.

Salesforce is a read-only input to the spend-control system.

The module covers:

- A daily read-only Salesforce snapshot. Salesforce is selected by the most frequent CRM toll, but this adapter can be connected to any other CRM systems if they provide MCP support.
- A context-preparation agent that converts approved fields and bounded notes into typed signals for deterministic approve policy.
- A deterministic policy layer that uses those signals with Airwallex data to set eligibility and caps.
- Freshness, failure, audit, override, and acceptance requirements.

The MVP does not define lending thresholds, transfer execution, or external credit-provider integrations. These parts are covered in other parts of the document this is just one section about the CRM integration.

## Decisions from the brainstorm

- Use the team's existing Salesforce MCP connector if its access and query capabilities are confirmed in the hackathon org.
- Use a daily snapshot. Do not query Salesforce during the money-movement decision.
- For the demo, map one Salesforce Account to one Airwallex connected account with a stable unique ID.
- Grant Salesforce read-only access. Do not write decisions back to Salesforce.
- Read approved Account and Contract fields, all open or escalated risk-relevant Cases, and up to five recent call activities from the previous 90 days.
- Include only bounded Case descriptions and call notes. Exclude full email threads and attachments.
- Keep Airwallex as the source for balances, transactions, payment activity, invoices, and receivable data, subject to confirmation in Charley's field list.
- The agent structures CRM context; deterministic policy rules set eligibility and caps.
- Permit explicit, source-linked negative interaction signals to trigger a bounded temporary reduction or hold. Positive unstructured notes cannot raise a cap.
- Re-evaluate temporary actions at each daily policy run. Keep them only while the evidence remains current and relevant.
- Stale or missing hard eligibility fields block new bridges. Existing bridge servicing continues.

## System boundaries

The integration has four components:

1. **Salesforce adapter:** Pulls the allowlisted CRM records and builds a versioned customer snapshot. It validates mapping, required fields, freshness, and record completeness.
2. **Context-preparation agent:** Reads the snapshot and bounded interaction notes. It extracts typed signals with attribution, confidence, evidence, and source references. It does not set a cap.
3. **Deterministic policy layer:** Combines validated CRM signals with Airwallex transaction and repayment data. It applies Santiago's eligibility, tier, cap, and allocation rules.
4. **Airwallex transfer module:** Executes only after policy approves an amount and current balance and reserve checks pass.

The adapter must use fixed, allowlisted reads. The agent must not create arbitrary Salesforce queries, transfer money, or write to the CRM. Salesforce note text is untrusted customer data; instructions found inside notes are never system instructions.

## Salesforce account mapping

The MVP assumes one Salesforce org owned by the platform operator, with one Account per small-business customer. Add a unique external-ID field such as `Airwallex_Connected_Account_Id__c` to Account and populate it during customer onboarding.

Before publishing a snapshot, the adapter must verify that the external ID maps to exactly one Account and exactly one connected account. Missing or duplicate mappings quarantine that customer's snapshot. Never match by company name or email.

If customers later need multiple Airwallex connected accounts, replace the one-to-one field with a child link object that stores the Salesforce Account ID, connected-account ID, relationship status, and effective dates. This is outside the demo scope.

## CRM fields and records

The API names below are proposed. Fields ending in `__c` are custom fields. Confirm actual names, picklist values, permissions, and object availability against the hackathon Salesforce org before implementation.

| CRM field or record | Decision use |
| :---- | :---- |
| Account.Customer\_Status\_\_c | Hard gate for active, paused, closed, or under-review customers. |
| Contract.Status | Require an active commercial contract. |
| Contract.EndDate | Bound eligibility by contract expiry where the policy requires it. |
| Account.KYB\_Status\_\_c | Hard gate for verified, pending, or failed onboarding status. |
| Account.Credit\_Review\_Status\_\_c | Apply approved, watch, hold, or review-required policy states. |
| Account.Approved\_Bridge\_Cap\_\_c | Human-approved ceiling; policy cannot exceed it. |
| Account.Prior\_Default\_Status\_\_c | Use verified default or delinquency status to reduce or pause eligibility. |
| Account.Loyalty\_Tier\_\_c | Relationship context and, at most, an allocation tie-breaker; never an independent cap increase. |
| Linked Case records | Use status, priority, reason, creation date, closure date, escalation state, and a controlled financial-risk category. |
| Linked Task or Event call activities | Use activity date, controlled call-outcome category, and bounded summary or note text. |

Salesforce Case records can be linked to Accounts and include standard fields such as status, priority, reason, created date, closed date, and escalation state. Task and Event records are the CRM activity sources for calls. Object and field access varies by org and edition, so the adapter must inspect the connected org schema before relying on any field.

The call and Case selection rules are:

- Include every open or escalated Case categorized as relevant to financial distress, repayment, business continuity, account identity, or security, regardless of age.
- Include up to five recent call activities from the previous 90 days.
- Include a bounded description or call-summary excerpt only for selected records.
- Exclude email threads, attachments, unrelated service notes, and unrestricted CRM history.
- Carry each source record ID, record type, event date, update date, category, and attribution into the snapshot.

## Daily snapshot contract

Publish one snapshot per customer. A snapshot contains:

- `schema_version`
- `snapshot_id`
- `snapshot_at`
- `sync_status`: `complete`, `partial`, or `stale`
- Salesforce Account ID and Airwallex connected-account ID
- Field values with source record ID and `updated_at`
- Selected interaction records with type, ID, date, category, attribution, and bounded note text
- Missing-field and partial-data indicators

Example interaction entry:

```json
{
  "record_type": "Case",
  "record_id": "500000000000001",
  "observed_at": "2026-10-17T15:30:00Z",
  "category": "financial_distress_reported",
  "attribution": "customer_reported",
  "note_excerpt": "Customer said the expected payment is delayed.",
  "source_updated_at": "2026-10-17T15:45:00Z"
}
```

The snapshot preserves source facts and provenance. It does not contain an agent-created cap or an untraceable risk score.

## Context-preparation output

The agent converts the snapshot into a `CRMPolicyContext` containing normalized structured values and zero or more typed signals. Each signal must include:

- Signal code and polarity.
- Source record type and ID.
- Observation date and source update date.
- Attribution, such as `customer_reported`, `staff_observed`, or `document_verified`.
- A bounded evidence excerpt.
- Confidence band and an explicitness indicator.
- Whether the item is ready for policy use or requires review.

The initial signal vocabulary is:

| Signal code | Initial handling |
| :---- | :---- |
| `financial_distress_reported` | Eligible for a policy-defined temporary reduction or hold when an explicit source statement supports it. |
| `repayment_commitment_missed` | Eligible for a policy-defined temporary reduction or hold when directly evidenced. |
| `business_continuity_risk` | Eligible for policy-defined review, reduction, or hold when directly evidenced. |
| `account_or_identity_concern` | Hold until a verified structured status clears the concern. |
| `billing_or_service_dispute` | Route to review unless corroborated by an approved source or structured status. |
| `no_material_risk_signal` | No adverse interaction signal was found in the selected records. |
| `review_required` | Use for ambiguous, contradictory, stale, or unsupported interpretations. |

The agent must cite a specific passage for free-text signals. Its confidence band alone is insufficient for a policy action. A vague impression such as "the customer sounded worried" becomes `review_required`. A direct statement about an expected payment delay may become `financial_distress_reported`. Only deterministic rules map approved codes to cap or hold outcomes.

## Policy and cap behavior

The deterministic policy layer consumes:

- Fresh, structured Salesforce fields.
- Agent-prepared CRM signals and their evidence references.
- Airwallex balances, transactions, deposits, and repayment history.
- The current platform reserve floor and outstanding bridge commitments.

The policy applies hard eligibility gates, the human-approved customer cap, customer tiers, and platform-level daily capacity. It performs a current Airwallex balance and reserve-floor check before any transfer.

Negative unstructured evidence may trigger a bounded temporary reduction or hold under a rule defined by Santiago. The agent does not determine the amount. Numeric thresholds and the precise mapping from each signal code to an action remain in Santiago's policy specification.

Unstructured CRM notes cannot increase a cap. Increases require verified structured evidence and must follow the approved policy. Billing or service disputes do not change a cap by themselves; policy may act only when the dispute is corroborated as a lending-relevant risk.

## Freshness and temporary actions

Hard eligibility and limit fields are fresh only when their source update time is no more than 24 hours old. These fields include customer status, contract status, KYB status, credit-review status, and approved bridge cap.

If a hard field is missing, unknown, or stale:

- Mark the customer's snapshot `stale` or `blocked` with a field-specific reason.
- Do not initiate a new bridge for that customer.
- Preserve the last good snapshot for review.
- Continue repayment collection and servicing for existing bridges.

If optional interaction data is unavailable, publish the validated structured fields with `sync_status: partial`; the agent must not infer that missing interaction data means there is no risk signal. Stale soft context must be ignored or explicitly routed for review.

A temporary reduction or hold caused by an interaction signal is reconsidered at each daily policy run. Renew it only while its evidence remains current and relevant. For a Case signal, the Case must remain open or escalated. For a call-only signal, the activity must remain within the 90-day window and must not be superseded or contradicted. Expire the action when the Case is resolved or closed, the call activity ages out of the window, or verified structured data clears the signal. Identity or compliance holds remain until the verified structured status clears them.

## Sync and failure handling

The scheduled job runs before the daily cap recalculation. It publishes snapshots atomically per customer:

1. Read the unique Account mapping.
2. Fetch allowlisted Account and Contract fields.
3. Fetch all open or escalated risk-relevant Cases and up to five recent calls from the 90-day window.
4. Normalize values and validate required hard fields and source timestamps.
5. Publish a new per-customer snapshot only after required fields and mapping pass validation.
6. Pass the versioned snapshot to the context-preparation agent.

Keep the last successful snapshot when a sync fails. Retry transient timeouts, rate limits, and temporary connector failures with bounded exponential backoff. Do not publish a partial replacement for hard fields. If only optional interaction reads fail, publish the structured snapshot as partial. Missing or duplicate mappings, permission failures, unknown hard-field values, and schema changes quarantine the affected customer and surface an operator-visible error.

Unknown picklist values remain `unknown`; never default them to an allowed state. A single customer's mapping or data error must not stop snapshots for other customers.

## Read access and audit

Use a dedicated Salesforce integration user with read-only permissions for the approved Account, Contract, Case, Task, and Event fields. Apply the narrowest record and field access supported by the org. Store connector credentials outside snapshots and logs.

The spend-control system stores the audit record; Salesforce remains read-only. For each policy decision, record:

- Decision ID, customer IDs, and snapshot ID.
- Snapshot time, sync status, stale fields, and schema version.
- Policy version and applicable tier.
- Signal codes, evidence references, and agent rationale.
- Old and new cap, hold state, and final policy action.
- Airwallex balance and reserve checks used by the decision.
- Human reviewer, override reason, and override expiry when applicable.

Overrides expire and return for review. They cannot bypass hard eligibility gates, the approved ceiling, the current Airwallex balance, or the platform reserve floor.

## Acceptance scenarios

1. **Fresh ordinary profile:** A unique mapping and current hard fields produce a complete snapshot with no adverse signal.
2. **Stale hard field:** A required field is missing or older than 24 hours; new funding is held with a field-specific reason.
3. **Explicit financial concern:** A selected call note contains a direct, source-linked statement; the agent emits a typed signal and policy applies its configured temporary action.
4. **Ambiguous or irrelevant note:** A general service complaint or vague inference routes to review and produces no automatic cap change.
5. **Signal resolution:** An open, relevant Case permits policy to reconsider a temporary action. A resolved Case or aged-out interaction expires the action.
6. **Mapping error:** A missing or duplicate account mapping quarantines only that customer's snapshot.
7. **Optional interaction failure:** The validated hard fields publish with a partial status, and the policy sees that interaction context is incomplete.
8. **Connector outage:** The last good snapshot remains inspectable; stale hard fields block new funding, while existing repayment servicing continues.

## Future extension

The snapshot contract can accept additional structured context providers later. Each provider must supply provenance, observation time, retrieval time, freshness, and source references. Provider-specific scoring and provider integrations are outside this MVP.

Salesforce Change Data Capture or Pub/Sub API could later refresh the cached context between daily runs. Keep daily reconciliation as a recovery path; event delivery is asynchronous and should not become the sole record of current CRM state.

## Open implementation items

- **Ramil:** Confirm the Salesforce MCP connector is available in the hackathon org, verify read-only permissions, and map the proposed field names to the org schema.
- **Santiago:** Define customer tiers, lending thresholds, numeric cap reductions, and signal-code-to-policy mappings.
- **Charley:** Confirm the Airwallex fields and endpoints for balances, transactions, deposits, repayment history, invoices, and receivables. Keep those financial facts in the Airwallex feed.
- **Yidi:** Select the demo screen states that show snapshot freshness, evidence, policy outcome, and hold or review reasons.
- **Team:** Confirm one-to-one account mapping and the trial-org test data.

## See also

- \[\[projects/airwallex-hack/airwallex-hack\]\]
- \[\[projects/airwallex-hack/work/kit-05-idea-draft\]\]
- \[\[projects/airwallex-hack/work/kit-05-user-flow-description\]\]
- [Salesforce Case field mapping](https://developer.salesforce.com/docs/data/data-cloud-dmo-mapping/guide/c360dm-case_dmo_mappings.html)
- [Salesforce Tasks and Events data model](https://developer.salesforce.com/docs/platform/data-models/guide/tasks-events.html)
- [Salesforce Change Data Capture guidance](https://developer.salesforce.com/docs/platform/change-data-capture/guide/cdc-when-to-use.html)
- [Salesforce Pub/Sub API guide](https://developer.salesforce.com/docs/platform/pub-sub-api/guide/intro.html)

## Comments

```
// Schema: { "<id>": { anchor:{exact,prefix,suffix,pos?}, status:open|resolved, thread:[{author,ts,text}], suggestion?:{replacement,author,ts,result?} } }
// Anchor = quote from the prose. To locate: search for "exact", disambiguate via prefix/suffix.
{
  "6e57": {
    "anchor": {
      "exact": "A deterministic policy layer that uses those signals with Airwallex data to\n  set eligibility and caps.",
      "pos": 488,
      "prefix": "c approve policy.\n- ",
      "suffix": "\n- Freshness, failur"
    },
    "status": "open",
    "thread": [
      {
        "author": "Ramil Aleshkin",
        "ts": "2026-10-19T00:15:21.702Z",
        "text": "this is out of the scope. this module covers only data source, not policy itself"
      }
    ]
  },
  "6bf8": {
    "anchor": {
      "exact": "Account.KYB_Status__c",
      "pos": 0
    },
    "status": "open",
    "thread": [
      {
        "author": "Ramil Aleshkin",
        "ts": "2026-10-19T01:18:13.457Z",
        "text": "think about this again, and brainstorm"
      },
      {
        "author": "Ramil Aleshkin",
        "ts": "2026-10-19T01:18:30.818Z",
        "text": "mb onboarding status will work better here"
      }
    ]
  }
}
```

Gaps
**Bridge Capital (Kit 5): Decisions and Open Gaps**

Santiago Labarca · Oct 19, 2026

**Decided**

| Topic | Decision |
| :---- | :---- |
| **How it starts** | A card payment is declined, the customer gets a message, we lend the missing amount, and the payment is tried again. |
| **Credit limits** | Each customer gets a credit limit, updated every day. All limits together can add up to more than the money we have, so a limit is not a promise. |
| **Sharing scarce money** | At each request we check how much of our money is left. The less we have, the fewer tiers get automatic approval. Some requests need a person to approve, and some get a no. |
| **Rules vs AI** | Fixed rules make every decision. AI reads outside information (CRM notes, support tickets) and can trigger a change, which the rules then apply. AI never decides an amount. |
| **Size of a limit** | The smallest of three: a share of the customer’s monthly incoming money, based on tier (smaller for first-time borrowers); 20% of our fund per customer (10% later on); any limit the account manager sets in Salesforce. |
| **Tiers** | A: all green, or one yellow. B: two yellows. C: three or more yellows, or one red. No limit: red on “do their clients pay on time”, or two or more reds. |
| **Reserve floor** | 20% of our fund is never lent. The demo must reach it. |
| **Repayment** | In the message, the customer picks: 15 days, 30 days, a specific date, or as soon as money arrives in their wallet. If they don’t pick, we collect as soon as money arrives. |
| **Fee** | Charged per bridge, only on the amount used: amount × monthly rate × (days ÷ 30). |
| **Collecting** | We take money back automatically only for the agreed amount and fee, on the agreed date. Anything else needs a person. |
| **Payroll vs other spend** | Not in the demo. Later phase. |

**Open**

| \# | Open item | What we need to decide | Owner |
| :---- | :---- | :---- | :---- |
| 1 | Fee rates | The monthly rate for each tier and for first-time borrowers, and a minimum fee per bridge. | Santiago |
| 2 | Final due date | Does every bridge need a final due date? Option A: always a set date. Option B: no date, we collect whenever money arrives. Option C: both, we collect whenever money arrives, with a final date as the deadline. Also: the latest date a customer can pick. | Santiago |
| 3 | Paying late | Does the fee keep growing after the agreed date? The late steps (2 days of grace, limit paused on day 3, cards blocked on day 16\) count from that date. | Santiago |
| 4 | How AI changes a tier | Which sources AI reads (account manager notes, support tickets, others?). What it looks for. What happens when it finds something: move the customer down a tier, cut the limit to zero, or ask a person to review. Can it also move a customer up, or only down? Starting proposal: down automatically, up only with a person’s OK. | Santiago, Ramil |
| 5 | Demo numbers | Pick our fund size, money already lent, and the demo requests so we run low on screen. Requests come one at a time: the first bridge uses money, so the next request gets a stricter answer. | Santiago, Yidi |
| 6 | New information in the demo | Add one moment where new information changes a decision. Proposal: the account manager notes in Salesforce that the customer’s main client will pay two weeks late. AI spots it and quotes the note, the rules move the customer down one tier, and its next request needs a person’s OK instead of being automatic. Needs team OK. | Team |
| 7 | Demo companies | Pick one set of three companies and write their history so we get the results we want. Needed before we create them in the sandbox. | Team |
| 8 | Conflicting answers in the Google Doc | The tabs give different answers on the same topics (how it starts, how money is shared, who sets limits, tiers, fee, collecting, demo companies, floor). For each topic, confirm which answer stands and delete the others. Most are settled by the decisions above. | Santiago, Yidi |

# **Kit 5 Bridge Capital: Pre-Build Consistency Review**

Reviewed Oct 19, 2026, against these sources:

> * **PS**: Problem statement and team customer flow (project context)
> * **AD**: Architecture diagram
> * **DF**: Kit 5 Demo Flow v2 (Yidi, Oct 19\)
> * **UR**: Underwriting Rules v001 (Santiago, Oct 17\)
> * **DI**: Airwallex Data Inventory (Charley, Oct 18\)
> * **TM**: Transfer Module (Charley, Oct 18\)
> * **DG**: Decisions and Open Gaps (Santiago, Oct 19\)

Totals: 9 P0, 9 P1, 11 P2, 8 P3.

## **Issue table**

| ID | Pri | Type | Journey step | Sources (exact quotes) | Issue | Impact if unresolved | Owner | Options | Recommended resolution   |
| :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- |
| 1 | P0 | Requirement | Sandbox setup | UR Data mapping: "Reading a connected account's balances may need an extra security token (SCA). Charley to confirm." · UR Open: "Charley: can we pull from a customer's wallet on the due date (charges)?" · DI: "It holds the platform account only" · DI recon: transfers "Empty" | None of the money-moving calls has been run live in the sandbox: connected account create/activate, x-on-behalf-of balance reads, connected\_account\_transfers/create, charges/create. Charges were not part of the recon at all. | If any one of these fails, the coding agent builds against an API path that does not work, and Acts 4–5 cannot be recorded. | Charley | (a) A spike test before the build. (b) The coding agent discovers the failures during the build. | (a). Run one end-to-end script today: create and activate an account with an EIN → Global Account → deposit simulation → cardholder and card (on-behalf-of) → simulated INSUFFICIENT\_FUNDS decline → transfer → balance re-read → charge. Give the coding agent the working request/response pairs. |
| 2 | P0 | Conflict | Capital level | DG Decided: "Reserve floor: 20% of our fund is never lent." · UR: "Reserve floor … 5%" · DF: "Reserve floor (5%) \$2,500" | The floor percentage differs between docs. | At 20% the floor is \$10k and lendable is \$40k, so deployable is \$7.5k, not \$15k. Every capital-bar value, band and allocation in DF breaks. | Santiago, Yidi | (a) 5%: DF works unchanged. (b) 20%: seeded outstanding becomes \$25,000. Then Nova funded leaves \$4k ÷ \$40k \= exactly 10%, which sits on the Less/Critical boundary, so the band edge rule must also be fixed. | (a) 5%. All demo arithmetic already depends on it. |
| 3 | P0 | Number | Daily precheck → Act 4/5 | DF Act 4: "Evidence: Tier B: concentration and cash buffer yellow"; DF customers: "Nova … \$7,000 (20%)" · DF Act 5: "Nova track record: '1 of 3 on-time repayments toward full Tier B line.'" · UR: "Track record … Yellow: never borrowed"; "Starter 10% … Never borrowed from us … After 2 to 3 bridges repaid on time, they move to their full tier." | Act 5 implies Nova is a Starter (never borrowed). Under UR, a Starter has track record yellow. That makes three yellows, so Tier C with a \$3,500 line. DF shows Tier B with a \$7,000 line. | A coding agent applying UR computes Nova as Tier C (Closed in band Less) with a line below the \$6,000 shortfall. The Act 3 re-plan and the Act 4 approval cannot happen. | Santiago, Yidi | (a) Nova has repaid before (track record green): Tier B and \$7k hold; delete the "1 of 3" line. (b) Nova is a Starter: the story breaks. | (a). Seed Nova with at least one prior on-time repayment. Replace the Act 5 caption with "Track record: N of N on time". |
| 4 | P0 | Conflict | Bridge request → policy decision | UR Doors: "Auto: approved instantly by the rules." · DG Open 5: "Requests come one at a time: the first bridge uses money, so the next request gets a stricter answer." · DF Act 2: "Status on all rows: 'Planned, not executed. Pre-execution checks pending.'" | UR and DG describe sequential per-request decisions with instant Auto execution. DF batches three requests into one plan and holds even the Auto rows until a CRM re-read. | If built per-request, Bloom (9:02, Tier A, Auto) is funded at 9:02 and Atlas at 9:05. Capital reaches the floor, Act 3 never happens, and Nova is Closed. | Santiago, Yidi | (a) Batch: requests queue, and a plan runs on a presenter trigger or a time window. (b) Sequential per-request, with DF rewritten. (c) Sequential, plus a mandatory pre-execution CRM refresh before each Auto transfer. | (a) for the demo. State the rule explicitly: "Requests received within the window are allocated together by tier, then FCFS. No transfer executes until the pre-execution checks, including a CRM re-read, pass." Add it to UR so policy and script match. |
| 5 | P0 | Gap | Act 3 AI read and proposal | DF Act 3: "AI proposes a revised split: Atlas \$5,000, Nova \$6,000, Bloom \$4,000." · DF: "AI extraction panel, each signal tied to the words it came from." | The script requires specific model outputs: three named signals with exact values, and a \$4,000 partial proposal for Bloom. No doc says whether these come from a live LLM call or from fixtures. | A live model may skip the partial proposal or extract different values. Then the "policy rejects AI" beat, which is the demo's core point, may not appear on the recording. | Team | (a) Live LLM, retrying until the output matches. (b) Fixture outputs replayed. (c) Live LLM with a "recording mode" toggle that replays a captured good run. | (c). Live extraction, with a captured fixture used for the recording. Disclose this in the opening disclosure line. |
| 6 | P0 | Conflict | Capital level / facility check | TM step 5: "Respect the reserve floor: GET /api/v1/balances/current … Platform available\_amount minus shortfall ≥ reserve floor" · DF: "The facility lives in app config because the sandbox platform wallet holds about 10M per currency" · DI: "Bridge budget and reserve floor must be app-level parameters" | TM checks the floor against the live platform wallet (about \$10M). DF and DI compute it from the app-config facility minus the ledger. | Following TM literally, the floor never binds and the capital bar shows the wrong numbers. | Charley | (a) App config \+ ledger is authoritative. Use the live wallet only as an insufficient\_fund sanity check. (b) Live wallet. | (a). Amend TM step 5 to: "Deployable \= facility − floor − outstanding (ledger) ≥ shortfall". |
| 7 | P0 | Conflict | Fee collection (Act 5\) | TM Guardrail 3: "There is no monthly statement." · TM: "F: fee due from closed statements, unpaid" · TM Runbook: "At month end: compute F per customer … issue the statement, and mark it due for the next deposit." · DF Act 5: "Fee: \$6,000 × 1.5% × 9 ÷ 30 … \$27.00" | TM contradicts itself. Its waterfall collects only fees from *closed statements*, but no statement closes inside the 9-day window. DF collects the fee accrued so far at the deposit. | Built to TM's runbook, F \= 0 on day 9, so no fee charge is made and Act 5 shows only principal. | Charley | (a) F \= fee accrued per bridge up to the collection time, minimum 7 days, collected at the next deposit. (b) Month-end statements. | (a). Delete the statement references from TM. Define F as "accrued fee on each open bridge as of collection time, floored at the 7-day minimum." |
| 8 | P0 | Gap | Act 1 timestamps · Act 5 fee | DF Act 5: "Demo clock advances 9 days." · DF Act 1: "9:02, 9:05, 9:11" · TM: "Both inputs come from Airwallex timestamps (transfer SETTLED, charge SETTLED), so every fee is reproducible from Airwallex records alone" · DI: "Simulated deposits settle instantly and cannot be backdated" | DF uses a virtual clock. TM computes fees from real Airwallex timestamps. A recording session spans minutes, not 9 days. | The fee from real timestamps is the 7-day minimum (\$6,000 × 0.35% \= \$21.00), not \$27.00, so the formula on screen does not match the charge. "Late" deposits and the 9:02 times also cannot come from the API. | Yidi, Charley | (a) One app-level demo clock drives displayed times, accrual, due dates and lateness; Airwallex timestamps are stored alongside. (b) Real timestamps only. | (a). Add to TM: "In the demo, accrual days come from the demo clock." Accept that TM rule 1 (reproducible from Airwallex alone) does not hold in the demo. |
| 9 | P0 | Gap | Daily precheck inputs · Act 3 CRM read | AD: "Integration with internal client's CRM (Salesforce) / Ramil" · DF: "Seed 90 days of history and the Salesforce records (Ramil's module) per customer." · UR Data mapping: "Industry, fraud flags, notes · CRM (Salesforce) · Synthetic for demo" | There is no CRM doc. The record schema, the Airwallex join key, whether Salesforce is a real dev org or a mock, and how a note change reaches the engine (event vs re-read) are all undefined. | The coding agent invents the CRM schema and integration, and Act 3 depends entirely on it. | Ramil | (a) A real Salesforce dev org via API. (b) A local mock shaped like Salesforce (JSON \+ fake event). | (b), unless Ramil already has (a) working. Minimum fields: account\_id join, expected deposit schedule, AM notes with timestamp, AM limit override, industry, fraud flag, write-off flag, invoices on-time %, top-client revenue share. |
| 10 | P1 | Conflict | Agent boundary (whole flow) | PS: "The agent returns yes or no … If yes, the agent instructs the transfer module" · AD: AI-decision-engine → "YES / NO", "defines limit per client", "fund balance client A" → transfer-module · DG Decided: "Fixed rules make every decision … AI never decides an amount." · DF: "the model reads evidence and proposes; deterministic policy decides"; "AI proposes a revised split" · UR Open: "☐ Agent boundary" | Three different roles for the AI. In PS and AD it decides, sets limits and triggers transfers. In DG it only triggers re-rates. In DF it proposes allocations with amounts, and the rules can override them. | The architecture the coding agent builds, and the demo's central claim, depend on which version it follows. AD and PS literally contradict the voiceover. | Santiago, Ramil | (a) DG: AI extracts signals and writes rationale only. (b) DF: AI also proposes allocations, which rules validate. (c) PS/AD: AI decides. | (b), with explicit limits: the AI outputs signals, rationale and a proposed allocation. Rules compute tiers, lines, doors and final amounts. Only the rules engine calls the transfer module. Update AD and PS to match. |
| 11 | P1 | Conflict | Payment attempt → decline | PS: "The deterministic engine checks the wallet balance … If funds are insufficient, the engine asks the agent" · AD: decision-engine "if balance \>= trx.amount" before approval · DI: "In production the 'should we fund' call happens inside remote authorization … 2.5 seconds" · DG: "A card payment is declined, the customer gets a message, we lend the missing amount, and the payment is tried again." · DF: "The original stays declined." | PS, AD and DI intercept before the decline (pre-authorization). DG and DF bridge after the decline and retry. | Different engine design and a different customer experience. The voiceover ("their card payments fail") contradicts the diagram. | Team | (a) Post-decline retry in the demo, with pre-authorization narrated as production. (b) Simulate pre-authorization in-app. | (a). Airwallex simulation produces real declines. Relabel AD's decision-engine as "post-decline bridge engine" and mention remote auth / Platform Liquidity Program in one line as the production path. |
| 12 | P1 | Conflict | Repayment terms · collection timing | DG Decided: "In the message, the customer picks: 15 days, 30 days, a specific date, or as soon as money arrives" · DG Decided: "We take money back automatically only for the agreed amount and fee, on the agreed date." · UR: "Due date (for example, 30 days after the bridge): we pull the full amount" · TM: "due at the next settled deposit or day 30, whichever comes first" · DF: "Up to 30 days, auto-collected from next deposit" | Three repayment models: the customer picks a term; collect on a fixed due date; or collect at the next deposit, capped at day 30\. | It changes the approval card, the collection trigger (date vs deposit event), whether a customer-facing choice UI exists, and Act 5\. | Santiago | (a) TM/DF: next deposit or day 30\. (b) DG: the customer picks. (c) UR: a fixed due date. | (a) for the demo. It needs no customer UI and matches Act 5\. Move the customer choice to the roadmap and record it in DG as the decision for Open 2\. |
| 13 | P1 | Requirement | Fee collection | PS: "bridge one shortfall and collect a monthly fee" · DG: "Charged per bridge" · TM: "There is no monthly statement" | The hackathon asks for a monthly fee. Every team doc charges a per-bridge fee prorated by days. | Judges checking the stated flow may see the requirement as unmet. | Team | (a) Keep the per-bridge fee and frame it as a "1.5% monthly rate, prorated" in the UI and voiceover. (b) Add a separate monthly program/access fee charge. (c) Collect accrued fees once a month. | (a). The narration already says "1.5% per 30 days". If the team reads the requirement literally, (b) is one extra charge. |
| 14 | P1 | Conflict | Sandbox setup (customers) | PS: "create and activate three connected accounts" · DG Open 7: "Pick one set of three companies" · DI: "Three archetype customers … one reliable payer … one chronically late payer … one new, volatile account with a chargeback and a reversed deposit" · DF: "Customers (four connected accounts)"; "Outstanding bridges to other customers (seeded ledger) \$32,500" | Three vs four companies, and DI's archetypes differ from DF's cast. The \$32,500 belongs to "other customers" who are neither real accounts nor described anywhere. | The coding agent cannot know which accounts to create or whether the lent-out balance must exist as real transfers. DI says exposure is "rebuilt entirely from Airwallex", which a ledger-only seed breaks. | Team | (a) Four DF accounts; the \$32.5k is ledger-only rows labelled "existing book". (b) Also create real accounts and real transfers for the \$32.5k. (c) Cut to three accounts. | (a). Four satisfies "three". Drop DI's archetypes from the build input and disclose the seeded book in the opening line. |
| 15 | P1 | Gap | Act 3 new evidence | DG Open 4: "What happens when it finds something: move the customer down a tier … Can it also move a customer up" · DG Open 6: "customer's main client will pay two weeks late … rules move the customer down one tier … next request needs a person's OK" · DF: note says "disputing last month's invoice. Payment expected next week at the earliest"; "Three yellows means Tier C … Closed" · UR reliability: "Green: 90%+ of invoices paid on time. Yellow: 75 to 90%" | No rule maps the extracted signals (payer\_dispute, payer\_concentration, expected\_inflow\_delay) to signal ratings. UR rates reliability as a percentage, which a single dispute does not move. DF drops Bloom from Tier A to C, while DG proposed one tier down to Human. | The coding agent invents the mapping. A different mapping gives a different tier and door, and Act 3's outcome changes. | Santiago, Ramil | (a) An explicit override table: dispute by a client with at least X% of revenue → reliability yellow; a stated concentration replaces the stored value. (b) DG proposal: a flat one-tier drop. | (a). Write the table into UR. Confirm DF's two-tier drop (and the Bloom declined outcome) over DG's one-tier proposal. |
| 16 | P1 | Conflict | Seed data · Act 3 | DF: Bloom "A (one yellow: cash buffer)", so concentration is green (UR: "top client is 40% or less") · DF note: "Their largest client, about 65% of revenue" · DF note: "logged at 8:40" · AD: "events about updates" · DI: "CRM profile update · Re-score" | Bloom's starting data must show its top client at ≤40%, but the note says 65%. The note is also timestamped 8:40, before the 9:02 decline. Under AD and DI, the event-driven re-score would already have re-rated Bloom before the plan. | The story is internally inconsistent, and an event-driven build skips the Act 3 reveal. | Yidi, Ramil | (a) Seed a stale CRM revenue split (top client 38%) that the note updates. Move the note time after the plan, e.g. 9:14. (b) Keep 8:40 and remove event-driven re-scoring from the demo. | (a). It keeps the event architecture honest and makes the reveal causal. |
| 17 | P1 | Conflict | Daily precheck model | UR: "Tier … from four signals"; "Credit line \= average monthly inflows × multiplier, capped" · DI: "Five signals carry most of the decision"; "L\_base \= min( P90(s), α × I / 3, C\_txn ), L \= L\_base × Π m\_i"; "rank requests by expected loss per dollar and customer value" · UR: "we serve by tier, then first come, first served" | Two incompatible underwriting models: different signals, line formulas and rationing rules. | The coding agent may implement DI's formula, which does not produce DF's lines (\$10k / \$6k / \$7k). | Santiago | (a) UR governs policy; DI is the data-source map only. (b) Merge the two. | (a). Add a line to DI: "Signal-to-limit mapping is superseded by Underwriting Rules v001." |
| 18 | P1 | Conflict | Capital level / floor | DG Decided: "The demo must reach it." · DF Close: "reserve floor never breached"; lowest point is "\$4,000 deployable … Critical" · PS: "Set a reserve floor that can constrain the advance. If it never binds, the agent never has to ration capital." | The demo never reaches the floor. The floor binds only implicitly in Act 2: \$15k is fully allocated, so Nova gets \$0. | It is unclear whether the DG decision is met. Judges may ask whether the floor ever constrained anything. | Santiago, Yidi | (a) Treat "binds" as "deployable above the floor is exhausted" (Act 2\) and say so on screen. (b) Change the numbers so a request is refused at the floor. | (a). Add a floor callout to Act 2 ("Nova: \$0, the floor blocks further lending") and a matching voiceover line. |
| 19 | P2 | Gap | Seed data | DG Open 7: "write their history so we get the results we want" · UR cash buffer: "Green: rarely. Yellow: sometimes. Red: often." · DF gives only the resulting tiers | No per-customer raw inputs exist (on-time %, top-client %, low-balance days, prior bridges, inflow), and "rarely/sometimes/often" has no threshold. | The coding agent fabricates the inputs. The rationale text ("94% on time, repaid 4 of 4") must match them. | Yidi, Santiago | (a) The team writes a fixture table. (b) The agent generates fixtures to fixed targets. | (b), with thresholds fixed by the team. Default cash buffer: days in the last 90 with balance \<10% of monthly spend: ≤5 rarely, 6–20 sometimes, \>20 often. |
| 20 | P2 | Conflict | Fee rate | DG Open 1: "The monthly rate for each tier and for first-time borrowers, and a minimum fee per bridge." · UR Open: "risk premium by tier" · TM: "One rate for every customer"; "at least 7 days" · DF: "1.5% per 30 days" | Tiered rates vs a single rate. The minimum fee is set in TM but still open in DG. | Only Nova pays a fee on screen, so the demo survives if the Tier B rate is 1.5%. | Santiago | (a) A single 0.05%/day rate with a 7-day minimum. (b) Tiered rates. | (a) for the demo. Close DG Open 1 with it. |
| 21 | P2 | Conflict | Payment attempt | DG Decided: "Payroll vs other spend: Not in the demo." · DF: "Monday payroll morning"; "Bloom & Co. … \$12,000 payroll" · DI recon: card allows only "MCCs 5411, 5812, 5814, 5734" · UR: "We only fund payments we can trace" | The demo's framing is payroll, which DG excludes, and payroll on a corporate card is implausible. Each simulated payment's MCC is not specified. | Judges may question it. A simulation at an MCC outside the allowed list declines for MERCHANT\_CATEGORY\_NOT\_ALLOWED. | Yidi | (a) Keep "Monday morning", but make Bloom's payment a contractor or supplier payment. (b) Keep payroll. | (a). Specify merchant name and MCC per payment, and allow those MCCs on each new card. |
| 22 | P2 | Gap | Customer communication | DG: "the customer gets a message"; repayment picked "In the message" · DF: "The video never leaves the treasury view"; activity-feed notifications only · UR: "Every step has a message" | No customer-facing surface is defined. DG implies one with a choice. | The coding agent may build a customer UI that DF never shows. | Yidi | (a) Treasury view only, with notifications as feed lines. (b) Add a customer panel. | (a). |
| 23 | P2 | Conflict | Fee idempotency | TM: "request\_id \= fee-\<acct\>-\<YYYYMM\>" · DF: "fee-\<acct\>-\<date\>" · TM: "A request\_id\_duplicate error … treat it as created" | A monthly fee key collides when one customer has two bridges collected in the same month, and the second fee is then silently skipped. | Not triggered in the demo, but it is a latent bug that is wrong by design. | Charley | (a) fee-\<transfer id\>. (b) fee-\<acct\>-\<date\>. | (a). One fee per bridge maps cleanly to one key. |
| 24 | P2 | Gap | Daily precheck no-go rules | AD sticky: "Client shouldn't be in the OFAC list of sanctioned individuals" · DI gates: "entity and every owner clear on OFAC" · UR no-go list: no OFAC entry | OFAC is in the architecture and DI but missing from the policy. | The coding agent may or may not build and show a sanctions gate. | Santiago | (a) Add it to the UR no-go list; mock it as "clear" in the precheck panel. (b) Defer it. | (a). It costs one row and supports the transparency claim. |
| 25 | P2 | Conflict | Tier assignment | DG: "No limit: red on 'do their clients pay on time', or two or more reds." · UR: "C: Three or more yellows, or one red (not on reliability)"; "No line: Red on inflow reliability" | DG adds "two or more reds → no limit". UR does not have it. | No demo customer has a red, but the coding agent must pick one rule set. | Santiago | (a) Adopt DG's rule into UR. (b) Keep UR. | (a). |
| 26 | P2 | Gap | Credit line size | DG: "any limit the account manager sets in Salesforce" (the third term of the min) · UR line formula has no AM override | The AM override is missing from UR and from the CRM fields. | The coding agent may omit or invent it. | Santiago, Ramil | (a) Add the field; set it to null for all demo customers. (b) Drop it. | (a). |
| 27 | P2 | Gap | Door check | UR: "A level only reopens 5 points above where it closed" · DF approval card: "band Less → Critical" | It is not defined whether the door is evaluated at the capital level before or after the request's own bridge, or what "reopens" means (band vs door). | Evaluating after the bridge puts Nova in Critical, where Tier B is Closed, and Act 4 breaks. | Santiago | (a) Evaluate at the pre-bridge level; apply hysteresis per door. (b) Evaluate post-bridge. | (a). |
| 28 | P2 | Gap | Retry after funding | DF Act 4: "Nova Agency initiated a new \$7,000 card payment: CLEARING" · TM step 10: "POST /api/v1/simulation/issuing/create" · DF shows no retry for Atlas | Who triggers the retry (the app automatically, or the presenter) is undefined. Atlas is funded, but its payment is never retried on screen. | The coding agent guesses. Atlas's bridge looks pointless. | Yidi | (a) The app auto-simulates a retry for every funded bridge, and the feed shows both. (b) Nova only. | (a). |
| 29 | P2 | Conflict | Reliability signal definition | UR: "Do the customer's own clients pay them on time? … invoices paid on time" · DI: "Deposit reliability … Lateness needs the expected date from the CRM" · DF rationale: "deposits 94% on time" | Two different concepts share one name: the customer's clients paying the customer vs the customer funding its wallet on schedule. PS's problem is the second. | The coding agent mixes data sources, and the rationale text contradicts the signal definition. | Santiago | (a) Keep UR's definition and change the DF rationale to "invoices 94% paid on time". (b) Switch to deposit-on-schedule. | (a) for consistency with UR thresholds. Optionally show deposit timeliness as context. |
| 30 | P3 | Terminology | All | credit line / credit limit / limit / assigned limit / cap-per-client · bridge facility / fund / our money / bridge budget / our-budget-management · customer / client / connected account · deterministic engine / decision-engine / policy / rules · agent / model / AI-decision-engine | Synonyms used across docs. | Inconsistent UI labels and code naming. | Agent default | Pick one term per concept. | Credit line, Bridge facility, Customer, Policy engine, AI agent. |
| 31 | P3 | Terminology | Collections | UR/DF: "Reserve floor" (platform) · TM: "m: operating floor left with the customer" | One word for two concepts. | A variable-name collision. | Agent default | Rename. | Rename TM's m to "customer operating buffer". |
| 32 | P3 | Terminology | Branding | DF: "FlowCard Treasury dashboard" · AD: "RAMP" | The platform's name differs. | Mixed labels on screen. | Agent default | — | FlowCard. |
| 33 | P3 | Gap | Capital bands | UR: "Normal 40 to 70% · Less 10 to 40%"; example "\$19k free \= 40%" | Boundary values belong to two bands. | An edge case only. | Agent default | — | Lower bound inclusive: 40% \= Normal, 10% \= Less. |
| 34 | P3 | Gap | Currency | TM steps 6 and "Match currencies" (FX) | No doc states that the demo is USD-only. | The coding agent may build unnecessary FX paths. | Agent default | — | USD only; skip the FX steps. |
| 35 | P3 | Gap | Card setup | DF: "per-transaction limit of at least \$12,000 plus a MONTHLY limit" (no value) · PS: "POST /issuing/cardholders/create" (not in DF prerequisites) | The monthly limit value and the cardholder step are missing. | The coding agent fills them in. | Agent default | — | Monthly limit \$100,000; one cardholder per account. |
| 36 | P3 | Gap | Transfer payload | TM step 7: reference "Bridge advance, card ending 0134" | The reference is hard-coded to the existing platform card. | A cosmetic error. | Agent default | — | Template: "Bridge advance, card ending {last4}". |
| 37 | P3 | Conflict | Act 2 status | DF: "Nova queued at \$0" · UR: "The customer always gets a yes, a firm no, or a time for the decision." | "Queued" carries no decision time. | Minor policy inconsistency on screen. | Agent default | — | Show "Pending: decision by {time}". |

## **Reconciliation with Decisions and Open Gaps (DG)**

### **Decided**

| Item | Status | Detail   |
| :---- | :---- | :---- |
| How it starts | **Contradicted** by PS, AD and DI | Those three intercept before authorization; DF and TM match DG. See \#11. |
| Credit limits | Consistent | UR, DI and DF all refresh daily. DI adds event-driven re-scores, which is the source of \#16. |
| Sharing scarce money | Consistent | Matches the UR doors and DF. Sequential vs batch execution is unresolved (\#4). |
| Rules vs AI | **Contradicted** by DF, AD and PS | DF has the AI propose amounts, including a \$4k partial; AD and PS have the AI decide and set limits. See \#10. |
| Size of a limit | Partly contradicted | UR omits the AM Salesforce cap (\#26); DI uses a different formula (\#17). |
| Tiers | Partly contradicted | UR lacks "two or more reds → no limit" (\#25). |
| Reserve floor | **Contradicted** by UR and DF | 20% vs 5% (\#2). "Demo must reach it" vs "never breached" (\#18). |
| Repayment | **Contradicted** by TM, DF and UR | The customer does not pick in any of them (\#12). |
| Fee | Consistent in formula | TM adds a 7-day minimum and a single rate (\#20). |
| Collecting | **Contradicted** by TM and DF | They collect at the next deposit, not "on the agreed date". TM also auto-collects partial amounts, where DG says "anything else needs a person" (Deferred). |
| Payroll vs other spend | Contradicted in framing by DF | The DF story is "payroll morning" (\#21). |

### **Open**

| \# | Status | Detail   |
| :---- | :---- | :---- |
| 1 Fee rates | Resolved elsewhere, pending confirmation | TM sets a single 0.05%/day rate with a 7-day minimum; DF uses it. Confirm (\#20). |
| 2 Final due date | Resolved elsewhere, pending confirmation | TM chooses Option C ("next settled deposit or day 30"); DF matches. The "latest date a customer can pick" becomes moot if the customer choice is dropped (\#12). |
| 3 Paying late | Still open, and contradicted | UR: freeze on day 3, block on day 16 after the due date. TM: freeze at day 30, same rate, no penalty. Not on screen, so Deferred. |
| 4 How AI changes a tier | Still open | DF implies an answer (AI extracts signals; rules re-rate; the tier can drop by two) but no mapping exists (\#15). |
| 5 Demo numbers | Resolved by DF | Valid only under a 5% floor (\#2) and with Nova's track record fixed (\#3). |
| 6 New information in the demo | Resolved differently by DF | The note content (dispute vs "two weeks late"), the size of the tier drop (two tiers vs one) and the outcome (declined vs Human) all differ from the DG proposal. Needs team OK (\#15, \#16). |
| 7 Demo companies | Partly resolved by DF | Cast chosen, but four, not three. Histories not written (\#14, \#19). DI's archetypes are superseded. |
| 8 Conflicting answers in the Google Doc | Still open | The conflicts persist across these exported docs: how it starts, money sharing, limits, tiers, fee, collecting, companies, floor. |

### **New issues not in DG**

\#1, \#3, \#4, \#5, \#6, \#7, \#8, \#9, \#13, \#17, \#19, \#21–\#24, \#27–\#37.

## **Decisions needed**

**P0**

> 1. Charley: has the end-to-end sandbox spike (create/activate account, on-behalf-of balance read, transfer, charge, INSUFFICIENT\_FUNDS decline) passed? Who runs it, and by when? (\#1)
> 2. Reserve floor: 5% or 20%? (\#2)
> 3. Nova: did it repay a prior bridge on time (Tier B, \$7k line, delete "1 of 3"), yes or no? (\#3)
> 4. Execution model: batch the queued requests into one plan, held until pre-execution checks pass, or decide each request sequentially? (\#4)
> 5. AI outputs in the recording: live, fixture replay, or live with a recording-mode fixture? (\#5)
> 6. Facility check: app config \+ ledger as the source of truth, with the platform wallet ignored for the floor? Yes/no. (\#6)
> 7. Fee basis: accrued per bridge at collection time with a 7-day minimum, and monthly statements removed? Yes/no. (\#7)
> 8. Demo clock: one virtual clock drives displayed times, fee accrual and due dates? Yes/no. (\#8)
> 9. CRM: real Salesforce dev org or mock? Ramil to deliver the record schema. (\#9)

**P1**

> 10. AI role: signals and rationale only, or also proposed allocations that the rules validate? (\#10)
> 11. Demo interception point: post-decline retry, or simulated pre-authorization? (\#11)
> 12. Repayment model for the demo: next deposit or day 30, customer-picked term, or fixed due date? (\#12)
> 13. "Monthly fee" requirement: frame the per-bridge fee as a monthly rate, or add a separate monthly charge? (\#13)
> 14. Customers: four accounts as in DF, with the \$32.5k book as ledger-only rows? Yes/no. (\#14)
> 15. Evidence mapping: approve an override table (dispute by a client with ≥X% of revenue → reliability yellow; stated concentration replaces the stored value). What is X? Confirm the A→C drop and the "declined" outcome over DG's one-tier drop. (\#15)
> 16. Bloom seed: stale CRM top-client share of 38%, updated to 65% by a note moved to 9:14? Yes/no. (\#16)
> 17. UR v001 is the sole underwriting model, and DI's formula and ranking are superseded? Yes/no. (\#17)
> 18. Floor-binding claim: is the Act 2 "deployable exhausted" moment enough, or must a request be refused at the floor? (\#18)

**P2**

> 19. Cash-buffer thresholds: accept ≤5 / 6–20 / \>20 low-balance days in 90? (\#19)
> 20. Single fee rate of 0.05%/day with a 7-day minimum for all tiers? Yes/no. (\#20)
> 21. Replace Bloom's "payroll" with a contractor or supplier payment, and specify the MCC per payment? Yes/no. (\#21)
> 22. Treasury view only, with no customer-facing screen? Yes/no. (\#22)
> 23. Fee request\_id \= fee-\<transfer id\>? Yes/no. (\#23)
> 24. Add OFAC to the UR no-go list, mocked as clear? Yes/no. (\#24)
> 25. Adopt "two or more reds → no line" into UR? Yes/no. (\#25)
> 26. Keep the AM Salesforce limit override as a field, null in the demo? Yes/no. (\#26)
> 27. Evaluate doors at the pre-bridge capital level? Yes/no. (\#27)
> 28. Auto-retry and show the payment for every funded bridge (Atlas and Nova)? Yes/no. (\#28)
> 29. Reliability \= customer's clients paying invoices on time (UR), and change the DF rationale wording? Yes/no. (\#29)

## **Deferred (production only)**

> * The late-payment ladder: UR day 3 / day 16 vs TM day 30 freeze; fee accrual after the due date.
> * Partial-collection automation (TM waterfall rows) vs DG "anything else needs a person".
> * Remote authorization within the 2.5 s budget; Platform Liquidity Program just-in-time funding.
> * California commercial-financing disclosures and APR on the 7-day minimum.
> * Chargebacks and settlement cycles (AD concerns sticky).
> * ATM / cash-withdrawal blocking; blocking payouts while a bridge is outstanding.
> * RFI gates, linked accounts, Plaid/TrueLayer balances, direct-debit fallback pull.
> * The mature-portfolio 10% per-customer cap.
> * Platform-report nightly batch and 366-day backfill.
> * deposit.reversed after collection; default and write-off handling.
> * Expected-loss-per-dollar rationing; group-level (legal entity) exposure caps.
> * Multi-currency and FX conversion paths.

1. **Scope and component boundary conflict.** The open comment says the module should cover the data source, not policy. Yet the spec includes the deterministic policy layer and cap behavior. Based on your clarified design, define the boundary as: Salesforce adapter pulls; agent structures context; policy consumes that structured input and sets the cap. Keep policy rules and thresholds in the policy spec.

2. **Salesforce data model and field meaning are unsettled.** The spec labels API names as proposed, and an open comment questions `KYB_Status__c`. Confirm the actual objects, fields, picklist values, data owners, and meanings. In particular, decide whether the relevant field is onboarding status, who maintains it, and what each value allows the policy to do.

3. **The connector and sync job are not yet buildable from the spec.** “Use the existing Salesforce MCP connector if confirmed” leaves the core dependency open. Confirm its read methods and permissions, then specify the account and related-record queries, pagination, daily run trigger, retries, deletion handling, and how a refresh avoids publishing mixed versions of a customer snapshot. Also confirm whether the demo has one Salesforce org for all customers, as the spec assumes.

4. **The agent’s signal extraction needs a stricter contract.** The code list is useful, but “explicit,” “corroborated,” attribution, and confidence are not defined precisely enough to implement consistently. Specify the output schema and examples for direct, ambiguous, conflicting, and irrelevant notes. Include rules for duplicate records and evidence that has since been superseded.

5. **Some runtime states and expected outcomes conflict or remain vague.** The snapshot enum lists `complete`, `partial`, and `stale`, but later the spec also uses `blocked`. Define sync state separately from funding-decision state, then map missing, stale, or partial data to expected outcomes. Likewise, “bounded temporary reduction” and override expiry need concrete policy inputs from Santiago. The acceptance scenarios should state expected snapshot, signal, and policy result for each case.

# **Kit 5 Bridge Capital: Source of Truth**

Oct 20, 2026 · @Santiago

## **How to use this tab**

This tab is the single source of truth for Kit 5\. When another tab disagrees, this tab wins; the other tab is outdated.

* **Decided:** agreed by the team or by the area owner. Build against it.
* **Proposed:** owner's recommendation. Build against it unless someone objects before Thursday.
* **Open:** needs a team decision. Listed in the Open section with a recommendation.

Sections: Decision log (what and when), Logic (the rules), Flow (the steps), Target demo (what the video shows), Changes vs current build, Open, Small defaults. Precedence: Santiago's Decisions and Open Gaps (Oct 19\) and the Oct 20 call override Underwriting Rules v001, the Transfer Module and Demo Flow v2 where they conflict.

## **Decision log**

21 items are decided and 3 are proposed pending team OK. Newest source first in each row.

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
| Bloom & Co. | \$40,000 | A last night (one yellow: cash buffer) | \$10,000 (30% capped at 20% of facility) | \$12,000 supplier | \$2,000 | \$10,000 |
| Atlas SaaS | \$20,000 | A (all green) | \$6,000 | \$6,500 supplier | \$1,500 | \$5,000 |
| Nova Agency | \$35,000 | B (yellows: concentration, cash buffer; repaid before, track record green) | \$7,000 | \$7,000 contractor | \$1,000 | \$6,000 |
| Pixel Studio | n/a | No line: 41 of 90 days | \$0 | \$2,500 | \$500 | \$2,000 |
| Juniper Labs (new) | \$30,000 | B (same profile shape as Nova) | \$6,000 | \$7,000 contractor | \$1,000 | \$6,000 |

Bloom's seeded CRM shows its top client at 38% of revenue (stale), so it starts as Tier A.

### **Beats**

| Time | Event | Rule that decides | Deployable after |
| :---- | :---- | :---- | :---- |
| 8:40 | Salesforce note on Bloom: largest client (about 65% of revenue) disputes last month's invoice. AI extracts three signals with quotes. Rules re-rate: concentration and reliability turn yellow, three yellows, Tier C, line \$4,000. | Mapping table, tier rules | \$15,000 |
| 9:02 | Bloom declines, shortfall \$10,000. AI proposes a \$4,000 partial bridge. Rules say no: shortfall above line, Tier C door Closed in Less, and a partial bridge wouldn't clear the payment. | Line check, door, full-or-nothing | \$15,000 |
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
| 11 | seed.ts DECLINED\_TRANSACTIONS | Bloom: "\$12,000 payroll", category Payroll | "\$12,000 supplier", category Supplier | Decision log \#20 |
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
| Cards | Per-transaction limit at least \$12,000 and monthly limit \$100,000, so declines read INSUFFICIENT\_FUNDS; allowed MCCs set per payment |
| Accounts | Each connected account created with an EIN before submit; one cardholder each |
| Demo clock | One app clock drives displayed times, fee days and due dates; Airwallex timestamps stored alongside |
| Words | Credit line, bridge facility, customer, policy engine, AI agent. Customer's own minimum balance is "operating buffer", never "floor" |

[image1]: <data:image/png;base64,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>

[image2]: <data:image/png;base64,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>

[image3]: <data:image/png;base64,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>
