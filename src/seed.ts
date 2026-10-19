import type { FacilityConfig, CustomerProfile, Signals } from './policy/types.js';

// ---------------------------------------------------------------------------
// Facility config
// ---------------------------------------------------------------------------
export const FACILITY_CONFIG: FacilityConfig = {
  facilitySize: 50_000,
  reserveFloorPct: 0.05,
  maxPerCustomerPct: 0.20,
  feeRatePer30Days: 0.015,
  minHistoryDays: 90,
};

// ---------------------------------------------------------------------------
// Customer profiles
// ---------------------------------------------------------------------------

const bloomSignals: Signals = {
  inflowReliability: 'green',
  inflowConcentration: 'green', // unknown before CRM note
  cashBuffer: 'yellow',
  trackRecord: 'green',
};

const bloomSignalsAfterCrm: Signals = {
  inflowReliability: 'yellow',
  inflowConcentration: 'yellow',
  cashBuffer: 'yellow',
  trackRecord: 'green',
};

const atlasSignals: Signals = {
  inflowReliability: 'green',
  inflowConcentration: 'green',
  cashBuffer: 'green',
  trackRecord: 'green',
};

const novaSignals: Signals = {
  inflowReliability: 'green',
  inflowConcentration: 'yellow',
  cashBuffer: 'yellow',
  trackRecord: 'green',
};

export const CUSTOMERS: Record<string, CustomerProfile> = {
  bloom: {
    id: 'acct_bloom001',
    name: 'Bloom & Co.',
    monthlyInflow: 40_000,
    signals: bloomSignals,
    historyDays: 730, // 2 years
    noGo: {
      excludedIndustry: false,
      fraudFlag: false,
      accountFrozen: false,
      pastWriteOff: false,
      monoClient: false,
    },
  },
  atlas: {
    id: 'acct_atlas001',
    name: 'Atlas SaaS',
    monthlyInflow: 20_000,
    signals: atlasSignals,
    historyDays: 365,
    noGo: {
      excludedIndustry: false,
      fraudFlag: false,
      accountFrozen: false,
      pastWriteOff: false,
      monoClient: false,
    },
  },
  nova: {
    id: 'acct_nova001',
    name: 'Nova Agency',
    monthlyInflow: 35_000,
    signals: novaSignals,
    historyDays: 180,
    noGo: {
      excludedIndustry: false,
      fraudFlag: false,
      accountFrozen: false,
      pastWriteOff: false,
      monoClient: false,
    },
  },
  pixel: {
    id: 'acct_pixel001',
    name: 'Pixel Studio',
    monthlyInflow: 0,
    signals: {
      inflowReliability: 'green',
      inflowConcentration: 'green',
      cashBuffer: 'green',
      trackRecord: 'yellow',
    },
    historyDays: 41,
    noGo: {
      excludedIndustry: false,
      fraudFlag: false,
      accountFrozen: false,
      pastWriteOff: false,
      monoClient: false,
    },
  },
};

/** Bloom's signals after the Salesforce CRM note is discovered. */
export const BLOOM_SIGNALS_AFTER_CRM = bloomSignalsAfterCrm;

// ---------------------------------------------------------------------------
// Declined transactions (seeded)
// ---------------------------------------------------------------------------
export interface DeclinedTransaction {
  customerId: string;
  customerName: string;
  transactionId: string;
  paymentDescription: string;
  paymentAmount: number;
  walletBalance: number;
  shortfall: number;
  failureReason: string;
  detectedAt: string; // HH:MM display time
  merchantCategory: string;
}

export const DECLINED_TRANSACTIONS: DeclinedTransaction[] = [
  {
    customerId: 'acct_bloom001',
    customerName: 'Bloom & Co.',
    transactionId: 'txn_bloom_payroll_001',
    paymentDescription: '$12,000 payroll',
    paymentAmount: 12_000,
    walletBalance: 2_000,
    shortfall: 10_000,
    failureReason: 'INSUFFICIENT_FUNDS',
    detectedAt: '9:02',
    merchantCategory: 'Payroll',
  },
  {
    customerId: 'acct_atlas001',
    customerName: 'Atlas SaaS',
    transactionId: 'txn_atlas_supplier_001',
    paymentDescription: '$6,500 supplier',
    paymentAmount: 6_500,
    walletBalance: 1_500,
    shortfall: 5_000,
    failureReason: 'INSUFFICIENT_FUNDS',
    detectedAt: '9:05',
    merchantCategory: 'Supplier',
  },
  {
    customerId: 'acct_nova001',
    customerName: 'Nova Agency',
    transactionId: 'txn_nova_contractor_001',
    paymentDescription: '$7,000 contractor',
    paymentAmount: 7_000,
    walletBalance: 1_000,
    shortfall: 6_000,
    failureReason: 'INSUFFICIENT_FUNDS',
    detectedAt: '9:11',
    merchantCategory: 'Contractor',
  },
  {
    customerId: 'acct_pixel001',
    customerName: 'Pixel Studio',
    transactionId: 'txn_pixel_ads_001',
    paymentDescription: '$2,500 ad spend',
    paymentAmount: 2_500,
    walletBalance: 500,
    shortfall: 2_000,
    failureReason: 'INSUFFICIENT_FUNDS',
    detectedAt: '9:14',
    merchantCategory: 'Advertising',
  },
];

// ---------------------------------------------------------------------------
// Seeded ledger: $32,500 already lent to other customers
// ---------------------------------------------------------------------------
export const SEEDED_OUTSTANDING = 32_500;

// ---------------------------------------------------------------------------
// CRM data
// ---------------------------------------------------------------------------
export const BLOOM_CRM_NOTE = {
  author: 'Jamie Chen, Account Manager',
  timestamp: '8:40 AM',
  text: "Spoke with Bloom's CFO. Their largest client, about 65% of revenue, is disputing last month's invoice. Payment expected next week at the earliest.",
};

export const AI_EXTRACTED_SIGNALS = [
  {
    signal: 'payer_dispute',
    value: 'true',
    evidence: '"disputing last month\'s invoice"',
  },
  {
    signal: 'payer_concentration',
    value: '65%',
    evidence: '"about 65% of revenue"',
  },
  {
    signal: 'expected_inflow_delay',
    value: '≥ 5 days',
    evidence: '"next week at the earliest"',
  },
];

// ---------------------------------------------------------------------------
// Agent rationales (AI-generated text from the doc)
// ---------------------------------------------------------------------------
export const AGENT_RATIONALES = {
  bloom_initial:
    '2-year customer, deposits 94% on time, repaid 4 of 4 bridges',
  atlas:
    'Diversified payers, never borrowed beyond line',
  nova_initial:
    'Tier A served first; no capital left',
  nova_revised:
    'Strong contractor relationships, two yellow signals on concentration and cash buffer',
  bloom_declined:
    'Declined; reassess when deposit settles',
};

// ---------------------------------------------------------------------------
// AI proposed plan (the one policy rejects for Bloom)
// ---------------------------------------------------------------------------
export const AI_PROPOSED_PLAN = {
  atlas: 5_000,
  nova: 6_000,
  bloom: 4_000,
};

// ---------------------------------------------------------------------------
// Credit officer approval card fields
// ---------------------------------------------------------------------------
export const NOVA_APPROVAL_TERMS = {
  amount: 6_000,
  currency: 'USD',
  to: 'Nova Agency (acct_nova001)',
  covers: 'Declined card transaction txn_nova_contractor_001',
  term: 'Up to 30 days, auto-collected from next deposit',
  fee: '1.5% per 30 days on daily outstanding',
  evidence: 'Tier B: concentration and cash buffer yellow',
  capitalImpact: 'Deployable $10,000 → $4,000, band Less → Critical, floor intact',
};

// ---------------------------------------------------------------------------
// Collection data (Act 5)
// ---------------------------------------------------------------------------
export const NOVA_COLLECTION = {
  depositAmount: 8_000,
  daysOutstanding: 9,
  feeAmount: 27.0,
  principalAmount: 6_000,
  feeRequestId: 'fee-acct_nova001-202610',
  principalRequestId: 'repay-dep_nova_001',
};
