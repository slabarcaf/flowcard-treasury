/**
 * The four demo customers, their declined transactions, and the facility.
 *
 * The facility is sized so that bridging every request at once would breach the
 * reserve floor: $31,000 of shortfalls against $12,000 available above the floor.
 */
import type {
  Bridge,
  BridgeRequest,
  CardTransaction,
  PlatformCapital,
  TimelineEvent,
} from '../types.js';
import { criticalityForCategory } from '../policy/criticality.js';
import { addDays } from '../policy/util.js';
import type { CustomerBlueprint } from './generator.js';

/** The day all windows are measured back from. */
export const AS_OF = '2026-11-03';

/**
 * $60,000 facility, 20% floor, $36,000 already lent.
 *   floor            $12,000
 *   lendable         $48,000
 *   available        $12,000  → 20% of the facility → Tight regime (§6)
 */
export const DEMO_CAPITAL: PlatformCapital = {
  facility_size: 60_000,
  reserve_floor_pct: 0.20,
  outstanding: 36_000,
};

// ---------------------------------------------------------------------------
// 1. Tier A — long tenure, stable balances, every bridge repaid on time.
// ---------------------------------------------------------------------------
const northwind: CustomerBlueprint = {
  id: 'acct_northwind',
  name: 'Northwind Supply',
  seed: 101,
  tenure_months: 30,
  monthly_inflow_target: 80_000,
  balance_level: 0.35,
  near_zero_days_in_90: 1,
  balance_noise: 0.12,
  trend: 'UP',
  payers: [
    { name: 'Dorsey Grocers', share: 0.45, on_time_rate: 0.97, avg_days_late: 1, relationship_months: 28 },
    { name: 'Kestrel Markets', share: 0.35, on_time_rate: 0.95, avg_days_late: 2, relationship_months: 22 },
    { name: 'Tamarind Retail', share: 0.20, on_time_rate: 0.92, avg_days_late: 3, relationship_months: 14 },
  ],
  bridges: [
    { amount: 6_000, term_days: 30, outcome: 'ON_TIME', closed_days_ago: 320, days_outstanding: 24 },
    { amount: 9_000, term_days: 30, outcome: 'ON_TIME', closed_days_ago: 190, days_outstanding: 27 },
    { amount: 5_500, term_days: 15, outcome: 'ON_TIME', closed_days_ago: 70, days_outstanding: 12 },
    {
      amount: 5_500,
      term_days: 30,
      outcome: 'OPEN',
      days_outstanding: 18,
      outstanding_balance: 3_000,
      due_in_days: 12,
      transaction_id: 'txn_northwind_prior',
    },
  ],
  receivables: [
    { payer_name: 'Dorsey Grocers', amount: 34_000, due_in_days: 11 },
    { payer_name: 'Kestrel Markets', amount: 26_000, due_in_days: 19 },
  ],
  crm: {
    onboarding_date: '2024-05-02',
    contract_status: 'Active — annual, renewed',
    support_notes: [
      { author: 'M. Okafor', timestamp: '2026-10-12', text: 'Asked about raising their card limit ahead of the holiday season.' },
    ],
  },
};

// ---------------------------------------------------------------------------
// 2. Tier C, the hard case — biggest and most critical shortfall (payroll),
//    volatile balance, one late bridge, a slow payer.
//
//    Why C and not B: tier B allows at most one late bridge but no defaults at
//    all, and this customer defaulted 500 days ago. That is outside tier C's
//    12-month default window, so they are eligible, but only at C — where the
//    10% limit is far below the payroll shortfall.
// ---------------------------------------------------------------------------
const harbor: CustomerBlueprint = {
  id: 'acct_harbor',
  name: 'Harbor Foods',
  seed: 202,
  tenure_months: 20,
  monthly_inflow_target: 95_000,
  balance_level: 0.22,
  near_zero_days_in_90: 18,
  balance_noise: 0.45,
  trend: 'DOWN',
  payers: [
    { name: 'Calder Distribution', share: 0.62, on_time_rate: 0.55, avg_days_late: 14, relationship_months: 19 },
    { name: 'Pinewood Catering', share: 0.24, on_time_rate: 0.88, avg_days_late: 4, relationship_months: 12 },
    { name: 'Rowan Cafes', share: 0.14, on_time_rate: 0.90, avg_days_late: 3, relationship_months: 8 },
  ],
  bridges: [
    { amount: 12_000, term_days: 60, outcome: 'DEFAULTED', closed_days_ago: 500, days_outstanding: 122 },
    { amount: 7_500, term_days: 30, outcome: 'ON_TIME', closed_days_ago: 240, days_outstanding: 28 },
    { amount: 9_000, term_days: 30, outcome: 'LATE', closed_days_ago: 60, days_outstanding: 44 },
    {
      amount: 9_000,
      term_days: 30,
      outcome: 'OPEN',
      days_outstanding: 40,
      outstanding_balance: 9_230,
      due_in_days: -10,
      transaction_id: 'txn_harbor_prior',
    },
  ],
  receivables: [
    { payer_name: 'Calder Distribution', amount: 58_000, due_in_days: 21 },
    { payer_name: 'Pinewood Catering', amount: 22_000, due_in_days: 27 },
  ],
  crm: {
    onboarding_date: '2025-03-14',
    contract_status: 'Active — monthly',
    support_notes: [
      { author: 'R. Villanueva', timestamp: '2026-10-28', text: 'Calder is paying two weeks behind again; CFO says payroll is the priority this month.' },
    ],
  },
};

// ---------------------------------------------------------------------------
// 3. Tier B — discretionary shortfall. Loses when capital is tight.
// ---------------------------------------------------------------------------
const lumen: CustomerBlueprint = {
  id: 'acct_lumen',
  name: 'Lumen Studio',
  seed: 303,
  tenure_months: 9,
  monthly_inflow_target: 45_000,
  balance_level: 0.28,
  near_zero_days_in_90: 4,
  balance_noise: 0.2,
  trend: 'FLAT',
  payers: [
    { name: 'Ardent Media', share: 0.5, on_time_rate: 0.93, avg_days_late: 2, relationship_months: 9 },
    { name: 'Juniper Brands', share: 0.3, on_time_rate: 0.9, avg_days_late: 4, relationship_months: 7 },
    { name: 'Selby Partners', share: 0.2, on_time_rate: 0.86, avg_days_late: 6, relationship_months: 5 },
  ],
  bridges: [
    { amount: 4_000, term_days: 30, outcome: 'ON_TIME', closed_days_ago: 120, days_outstanding: 26 },
  ],
  receivables: [
    { payer_name: 'Ardent Media', amount: 21_000, due_in_days: 14 },
  ],
  crm: {
    onboarding_date: '2026-02-09',
    contract_status: 'Active — monthly',
    support_notes: [
      { author: 'M. Okafor', timestamp: '2026-09-30', text: 'Team offsite booked for December; asked whether travel can go on the card.' },
    ],
  },
};

// ---------------------------------------------------------------------------
// 4. No history — two months on the platform. Must be declined.
// ---------------------------------------------------------------------------
const vertex: CustomerBlueprint = {
  id: 'acct_vertex',
  name: 'Vertex Labs',
  seed: 404,
  tenure_months: 2,
  monthly_inflow_target: 30_000,
  balance_level: 0.3,
  near_zero_days_in_90: 3,
  balance_noise: 0.25,
  trend: 'UP',
  payers: [
    { name: 'Halcyon Health', share: 0.7, on_time_rate: 0.95, avg_days_late: 2, relationship_months: 2 },
    { name: 'Stride Clinics', share: 0.3, on_time_rate: 0.9, avg_days_late: 3, relationship_months: 2 },
  ],
  bridges: [],
  receivables: [{ payer_name: 'Halcyon Health', amount: 18_000, due_in_days: 16 }],
  crm: {
    onboarding_date: '2026-09-05',
    contract_status: 'Active — monthly, in onboarding',
    support_notes: [
      { author: 'R. Villanueva', timestamp: '2026-10-20', text: 'Finished KYB. Asked when bridges become available.' },
    ],
  },
};

export const DEMO_BLUEPRINTS: CustomerBlueprint[] = [northwind, harbor, lumen, vertex];

// ---------------------------------------------------------------------------
// The declined transactions, one per customer
// ---------------------------------------------------------------------------

interface DeclineSpec {
  customer_id: string;
  merchant_name: string;
  merchant_category: string;
  amount: number;
  wallet_balance: number;
  term_days: number;
}

const DECLINES: DeclineSpec[] = [
  // Above the $5,000 Tight threshold, so a person has to approve it.
  {
    customer_id: 'acct_northwind',
    merchant_name: 'Rowe Packaging',
    merchant_category: 'suppliers',
    amount: 7_600,
    wallet_balance: 600,
    term_days: 30,
  },
  // Under the threshold, so it clears automatically.
  {
    customer_id: 'acct_northwind',
    merchant_name: 'Calloway Inventory',
    merchant_category: 'inventory',
    amount: 3_300,
    wallet_balance: 300,
    term_days: 30,
  },
  {
    customer_id: 'acct_harbor',
    merchant_name: 'Payroll run — November',
    merchant_category: 'payroll',
    amount: 18_400,
    wallet_balance: 400,
    term_days: 30,
  },
  {
    customer_id: 'acct_lumen',
    merchant_name: 'Meridian Travel',
    merchant_category: 'travel',
    amount: 6_800,
    wallet_balance: 300,
    term_days: 30,
  },
  {
    customer_id: 'acct_vertex',
    merchant_name: 'Northstar Analytics',
    merchant_category: 'saas',
    amount: 2_700,
    wallet_balance: 200,
    term_days: 30,
  },
];

function transactionFor(spec: DeclineSpec, index: number): CardTransaction {
  return {
    id: `txn_${spec.customer_id.replace('acct_', '')}_${index + 1}`,
    customer_id: spec.customer_id,
    card_id: `card_${spec.customer_id.replace('acct_', '')}`,
    amount: spec.amount,
    currency: 'USD',
    merchant_name: spec.merchant_name,
    merchant_category: spec.merchant_category,
    criticality: criticalityForCategory(spec.merchant_category),
    status: 'DECLINED',
    decline_reason: 'INSUFFICIENT_FUNDS',
    wallet_balance: spec.wallet_balance,
    created_at: `${AS_OF}T09:0${index}:00Z`,
  };
}

export const DEMO_TRANSACTIONS: CardTransaction[] = DECLINES.map(transactionFor);

/** One bridge request per declined transaction, for the shortfall. */
export const DEMO_REQUESTS: BridgeRequest[] = DECLINES.map((spec, i) => {
  const transaction = DEMO_TRANSACTIONS[i];
  return {
    id: `req_${spec.customer_id.replace('acct_', '')}_${i + 1}`,
    customer_id: spec.customer_id,
    transaction,
    principal: spec.amount - spec.wallet_balance,
    term_days: spec.term_days,
    requested_at: AS_OF,
  };
});

// ---------------------------------------------------------------------------
// Bridges already on the books, so the back office has live stages to show.
// Each one matches an OPEN or closed entry in its customer's bridge_history.
// Fees are what priceOffer gives for the principal and term.
// ---------------------------------------------------------------------------

export const DEMO_BRIDGES: Bridge[] = [
  {
    id: 'bridge_northwind_1',
    customer_id: 'acct_northwind',
    transaction_id: 'txn_northwind_prior',
    principal: 5_500,
    fee: 160,
    term_days: 30,
    status: 'COLLECTING',
    disbursed_at: addDays(AS_OF, -18),
    due_date: addDays(AS_OF, 12),
    principal_outstanding: 3_000,
    fee_outstanding: 0,
    accrued_fee: 0,
  },
  {
    id: 'bridge_harbor_1',
    customer_id: 'acct_harbor',
    transaction_id: 'txn_harbor_prior',
    principal: 9_000,
    fee: 230,
    term_days: 30,
    status: 'DELINQUENT',
    disbursed_at: addDays(AS_OF, -40),
    due_date: addDays(AS_OF, -10),
    principal_outstanding: 9_000,
    fee_outstanding: 230,
    accrued_fee: 0,
  },
  {
    id: 'bridge_lumen_1',
    customer_id: 'acct_lumen',
    transaction_id: 'txn_lumen_past_1',
    principal: 4_000,
    fee: 130,
    term_days: 30,
    status: 'REPAID',
    disbursed_at: addDays(AS_OF, -146),
    due_date: addDays(AS_OF, -116),
    principal_outstanding: 0,
    fee_outstanding: 0,
    accrued_fee: 0,
  },
];

function event(
  bridge_id: string,
  customer_id: string,
  daysAgo: number,
  time: string,
  type: TimelineEvent['type'],
  text: string,
): TimelineEvent {
  return {
    id: `${bridge_id}_${type}_${daysAgo}_${time.replace(':', '')}`,
    at: `${addDays(AS_OF, -daysAgo)}T${time}:00Z`,
    customer_id,
    bridge_id,
    type,
    text,
  };
}

/** Decline through to closing, for each bridge on the books. */
export const DEMO_TIMELINE: TimelineEvent[] = [
  // Northwind: funded, retried, partly collected, still inside its term.
  event('bridge_northwind_1', 'acct_northwind', 18, '08:41', 'DECLINE', 'Card declined for $5,500 at Rowe Packaging — INSUFFICIENT_FUNDS, wallet held $410.'),
  event('bridge_northwind_1', 'acct_northwind', 18, '08:42', 'OFFER', 'Offered $5,500 over 30 days, fee $160 (service $50 + 2%/month prorated).'),
  event('bridge_northwind_1', 'acct_northwind', 18, '08:49', 'ACCEPTANCE', 'Customer accepted the offer at $5,500 / 30 days / $160.'),
  event('bridge_northwind_1', 'acct_northwind', 18, '08:50', 'DISBURSEMENT', 'Transfer to connected account SETTLED — $5,500.'),
  event('bridge_northwind_1', 'acct_northwind', 18, '08:53', 'NOTICE', 'Card payment retried and approved.'),
  event('bridge_northwind_1', 'acct_northwind', 6, '11:12', 'DEPOSIT', 'Deposit $2,660 received from Dorsey Grocers.'),
  event('bridge_northwind_1', 'acct_northwind', 6, '11:12', 'COLLECTION', 'Collected $160 fee, then $2,500 principal. $3,000 principal remaining.'),

  // Harbor: funded, then missed its due date and walked up the ladder.
  event('bridge_harbor_1', 'acct_harbor', 40, '09:20', 'DECLINE', 'Card declined for $9,000 at Calder Distribution — INSUFFICIENT_FUNDS.'),
  event('bridge_harbor_1', 'acct_harbor', 40, '09:21', 'OFFER', 'Offered $9,000 over 30 days, fee $230.'),
  event('bridge_harbor_1', 'acct_harbor', 40, '09:38', 'ACCEPTANCE', 'Customer accepted the offer at $9,000 / 30 days / $230.'),
  event('bridge_harbor_1', 'acct_harbor', 40, '09:39', 'DISBURSEMENT', 'Transfer to connected account SETTLED — $9,000.'),
  event('bridge_harbor_1', 'acct_harbor', 40, '09:44', 'NOTICE', 'Card payment retried and approved.'),
  event('bridge_harbor_1', 'acct_harbor', 10, '00:05', 'NOTICE', 'Due date passed with nothing collected; no qualifying deposit landed.'),
  event('bridge_harbor_1', 'acct_harbor', 9, '08:00', 'STATUS_CHANGE', 'Moved to Watch at 1 day past due. Daily reminders started, new bridges blocked.'),
  event('bridge_harbor_1', 'acct_harbor', 2, '08:00', 'STATUS_CHANGE', 'Moved to Delinquent at 8 days past due. Card restricted to critical spend, tier downgraded one level, ops notified.'),

  // Lumen: funded and closed on time.
  event('bridge_lumen_1', 'acct_lumen', 146, '14:02', 'DECLINE', 'Card declined for $4,000 at Ardent Media — INSUFFICIENT_FUNDS.'),
  event('bridge_lumen_1', 'acct_lumen', 146, '14:03', 'OFFER', 'Offered $4,000 over 30 days, fee $130.'),
  event('bridge_lumen_1', 'acct_lumen', 146, '14:15', 'ACCEPTANCE', 'Customer accepted the offer at $4,000 / 30 days / $130.'),
  event('bridge_lumen_1', 'acct_lumen', 146, '14:16', 'DISBURSEMENT', 'Transfer to connected account SETTLED — $4,000.'),
  event('bridge_lumen_1', 'acct_lumen', 124, '10:30', 'DEPOSIT', 'Deposit $21,000 received from Ardent Media.'),
  event('bridge_lumen_1', 'acct_lumen', 124, '10:30', 'COLLECTION', 'Collected $130 fee and $4,000 principal in full.'),
  event('bridge_lumen_1', 'acct_lumen', 124, '10:31', 'STATUS_CHANGE', 'Repaid on time, 22 days outstanding. Restrictions lifted, tier recomputed.'),
];
