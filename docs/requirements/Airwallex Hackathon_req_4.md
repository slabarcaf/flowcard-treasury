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

