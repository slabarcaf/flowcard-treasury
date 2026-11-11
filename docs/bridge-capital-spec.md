# Bridge Capital Spec v1

## 1. Product
A corporate-card platform (FlowCard) built on Airwallex issues cards to SMB customers, each a connected account with its own wallet. When a card payment is declined for insufficient funds, we offer a bridge: we fund the shortfall from platform capital and collect it back from the customer's next deposits, with a fee. Two views: a back office for our credit/ops team and a customer portal.

## 2. Customer data
Balance history (from end-of-day balances):
- avg_balance_30d, avg_balance_90d, avg_balance_180d (trend = 30d vs 180d)
- low_point_balance_90d: 10th percentile of daily balances (on its worst 10% of days, the customer had less than this)
- days_near_zero_90d: days with balance below 10% of avg_balance_90d

Inflows:
- monthly_inflows: average monthly deposits over the last 3 months
- expected_receivables: payer_name, amount, due_date, payer_on_time_rate, payer_avg_days_late, relationship_months
- top_payer_concentration: share of inflows from the largest payer

Relationship:
- tenure_months
- bridge_history: each past bridge with amount, term_days, opened_at, closed_at, days_outstanding, outcome (ON_TIME | LATE | DEFAULTED | OPEN)
- derived: bridges_count, avg_days_outstanding, on_time_count and late_count (closed bridges only), open_bridge (bool), open_bridge_past_due (bool)

CRM (context, shown to ops, not used in scoring): onboarding date, contract status, recent support notes.

## 3. Tiers and limits (thresholds are placeholders)
- No history: tenure < 3 months or < 90 days of balance data. Not eligible.
- Tier A: tenure >= 12 months, no late or defaulted bridges in 180 days, days_near_zero_90d <= 5. Limit: 30% of monthly_inflows.
- Tier B: tenure >= 6 months, at most 1 late bridge in 180 days, no defaults. Limit: 20% of monthly_inflows.
- Tier C: everyone else eligible, no default in 12 months. Limit: 10% of monthly_inflows.
- Limit applies to total outstanding bridge balance (existing + new).
- Tiers recompute daily and after every bridge closes.

## 4. Eligibility rules (each returns rule_id, label, passed, detail)
1. Has history
2. No bridge currently past due
3. Outstanding + new bridge <= customer limit
4. Spend criticality allowed in current capital regime
5. Bridge does not breach the platform reserve floor
6. No duplicate bridge for the same transaction
7. An expected receivable is due before the term ends

Spend criticality: critical (suppliers, payroll, inventory), standard, discretionary (travel, entertainment, SaaS), mapped from merchant category.

## 5. Pricing, terms, repayment
- Fee = $50 service fee + principal x 2% per month, prorated daily over the chosen term: 50 + principal x 0.02 x term_days / 30.
  Example: $10,000 for 60 days = 50 + 400 = $450.
- Fee is charged for the full chosen term even if repaid early.
- Terms: customer chooses 15, 30, 60 or 90 days, or a custom 7 to 90 days.
- Customer must accept the offer (amount, fee, term) before funds move. If any of those change before disbursement, the acceptance is void and a new offer is issued.
- Collection: principal + fee auto-collected from incoming deposits as they land until fully repaid.
- While bridged: tier C cards restricted to critical spend, capped at the bridged amount. Restriction lifts on full repayment.

Late repayment (days past due date, outstanding keeps accruing at 2% per month):
- 1-7 DPD, Watch: daily reminders, collect from any deposit, no new bridges.
- 8-30 DPD, Delinquent: card restricted to critical spend (all tiers), tier downgraded one level, ops notified.
- 31-60 DPD, Serious: card frozen, escalated to a human in collections, limit set to 0.
- 60+ DPD, Default: recorded as DEFAULTED, referred to collections. Ineligible for 12 months.
- After full repayment, restrictions lift and tier recomputes from history.

## 6. Platform capital regimes
Measured on capital available above the reserve floor, as % of the facility.
- Abundant > 60%: any eligible customer, auto-approve.
- Normal 30-60%: tier B or better, auto-approve within limit.
- Tight 10-30%: critical spend only, requests ranked, bridges above $5,000 need human approval.
- Critical < 10%: tier A and critical spend only, every bridge needs approval.
- At floor: no new bridges, collect only.

Ranking when capital is scarce: tier, then spend criticality, then receivable quality (payer on-time rate, relationship length).

## 7. Approvals
Human approvals are bound to customer, amount, fee and term. Any change invalidates the approval.

## 8. v1.1 proposals (PLACEHOLDER, pending approval)
Taken from the clickable prototype. Built behind the DataAdapter on demo data; every number lives in `policy.config.ts` and shows as a placeholder on the Rules screen.

### 8.1 Cash forecast (customer portal)
- Horizon 60 days. Start from today's wallet balance.
- Each expected receivable lands on due_date + the payer's average days late.
- Spend is the average daily settled card spend over the last 90 days.
- Low line = 10% of avg_balance_90d (the §2 near-zero definition). The first day below it is shown, with an option to plan a bridge.

### 8.2 Early warnings (back office only)
- 30-day average balance more than 25% below the 180-day average.
- More than 5 days near zero in the last 90 (the tier A cap).
- A payer with an expected receivable pays on time less than 70% of the time.
- The forecast crosses the low line within 30 days.
- The largest payer is more than 60% of inflows.

### 8.3 Planned bridges and Flex on bills
- A customer can plan a bridge from the forecast, or pay a supplier bill with Flex over 30, 60 or 90 days.
- Both are bridge requests with a stated purpose; the bill's (or chosen) merchant category sets criticality, and all §4 rules, §5 pricing and acceptance binding, §6 regimes and §7 approvals apply unchanged.
- "You can bridge up to $X" = tier limit − outstanding, capped by capital above the floor; 0 with no history, a past-due bridge, or at the floor.

### 8.4 Customer-facing late bridge
- The portal shows only status, amount due, a Repay action and one sentence on the current card restriction. The ladder, collections log and internal actions stay in the back office.
