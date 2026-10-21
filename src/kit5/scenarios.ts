import type { FacilityConfig, Signals } from '../policy/types.js';
import { CUSTOMERS, BLOOM_SIGNALS_AFTER_CRM, AGENT_RATIONALES } from '../seed.js';
import type { Scenario, ScenarioCustomer, ScenarioInputs } from './types.js';

// ---------------------------------------------------------------------------
// Our actual facility: $50,000 with a 20% reserve floor → $40,000 lendable.
// ---------------------------------------------------------------------------
export const KIT5_FACILITY: FacilityConfig = {
  facilitySize: 50_000,
  reserveFloorPct: 0.20,
  maxPerCustomerPct: 0.20,
  feeRatePer30Days: 0.015,
  minHistoryDays: 90,
};

const START_DATE = '2026-10-19';

const NO_GO = {
  excludedIndustry: false,
  fraudFlag: false,
  accountFrozen: false,
  pastWriteOff: false,
  monoClient: false,
};

const ALL_GREEN: Signals = {
  inflowReliability: 'green',
  inflowConcentration: 'green',
  cashBuffer: 'green',
  trackRecord: 'green',
};

// GD's four customers, unchanged. Bloom keeps the Salesforce re-rating.
const BLOOM: ScenarioCustomer = {
  ...CUSTOMERS.bloom,
  key: 'bloom',
  rationale: AGENT_RATIONALES.bloom_initial,
  crmSignalsAfter: BLOOM_SIGNALS_AFTER_CRM,
};
const ATLAS: ScenarioCustomer = { ...CUSTOMERS.atlas, key: 'atlas', rationale: AGENT_RATIONALES.atlas };
const NOVA: ScenarioCustomer = { ...CUSTOMERS.nova, key: 'nova', rationale: AGENT_RATIONALES.nova_revised };
const PIXEL: ScenarioCustomer = { ...CUSTOMERS.pixel, key: 'pixel' };

const original: ScenarioInputs = {
  facility: KIT5_FACILITY,
  outstanding: 25_000,
  startDate: START_DATE,
  customers: [BLOOM, ATLAS, NOVA, PIXEL],
  events: [
    { type: 'decline', requestId: 'txn_bloom_payroll_001', customerKey: 'bloom', day: 0, time: '9:02', paymentDescription: '$12,000 payroll', paymentAmount: 12_000, walletBalance: 2_000 },
    { type: 'decline', requestId: 'txn_atlas_supplier_001', customerKey: 'atlas', day: 0, time: '9:05', paymentDescription: '$6,500 supplier', paymentAmount: 6_500, walletBalance: 1_500 },
    { type: 'decline', requestId: 'txn_nova_contractor_001', customerKey: 'nova', day: 0, time: '9:11', paymentDescription: '$7,000 contractor', paymentAmount: 7_000, walletBalance: 1_000 },
    { type: 'decline', requestId: 'txn_pixel_ads_001', customerKey: 'pixel', day: 0, time: '9:14', paymentDescription: '$2,500 ad spend', paymentAmount: 2_500, walletBalance: 500 },
    { type: 'repayment', requestId: 'txn_nova_contractor_001', day: 9, time: '10:01', depositAmount: 8_000 },
  ],
};

// Kit 5 target demo: GD's cast plus Juniper, the same request as Nova arriving later.
const kit5Target: ScenarioInputs = {
  ...original,
  customers: [
    ...original.customers,
    {
      id: 'acct_juniper001',
      key: 'juniper',
      name: 'Juniper Labs',
      monthlyInflow: 30_000,
      signals: { ...CUSTOMERS.nova.signals },
      historyDays: 240,
      noGo: NO_GO,
      rationale: 'Same profile shape as Nova: two yellows on concentration and cash buffer',
    },
  ],
  events: [
    { type: 'decline', requestId: 'txn_bloom_supplier_001', customerKey: 'bloom', day: 0, time: '9:02', paymentDescription: '$12,000 supplier', paymentAmount: 12_000, walletBalance: 2_000 },
    { type: 'decline', requestId: 'txn_atlas_supplier_001', customerKey: 'atlas', day: 0, time: '9:05', paymentDescription: '$6,500 supplier', paymentAmount: 6_500, walletBalance: 1_500 },
    { type: 'decline', requestId: 'txn_nova_contractor_001', customerKey: 'nova', day: 0, time: '9:11', paymentDescription: '$7,000 contractor', paymentAmount: 7_000, walletBalance: 1_000 },
    { type: 'decline', requestId: 'txn_pixel_ads_001', customerKey: 'pixel', day: 0, time: '9:13', paymentDescription: '$2,500 ad spend', paymentAmount: 2_500, walletBalance: 500 },
    { type: 'decline', requestId: 'txn_juniper_contractor_001', customerKey: 'juniper', day: 0, time: '9:16', paymentDescription: '$7,000 contractor', paymentAmount: 7_000, walletBalance: 1_000 },
    { type: 'repayment', requestId: 'txn_nova_contractor_001', day: 9, time: '10:01', depositAmount: 8_000 },
  ],
};

// Two identical Tier A customers; only arrival order differs.
function twin(key: string, id: string, name: string, monthlyInflow: number): ScenarioCustomer {
  return {
    id,
    key,
    name,
    monthlyInflow,
    signals: { ...ALL_GREEN },
    historyDays: 400,
    noGo: NO_GO,
    rationale: 'All four signals green, repaid every past bridge on time',
  };
}

const firstCome: ScenarioInputs = {
  facility: KIT5_FACILITY,
  outstanding: 25_000,
  startDate: START_DATE,
  customers: [
    twin('cedar', 'acct_cedar001', 'Cedar Supply', 30_000),
    twin('maple', 'acct_maple001', 'Maple Supply', 30_000),
  ],
  events: [
    { type: 'decline', requestId: 'txn_cedar_supplier_001', customerKey: 'cedar', day: 0, time: '9:00', paymentDescription: '$9,000 supplier', paymentAmount: 9_000, walletBalance: 1_000 },
    { type: 'decline', requestId: 'txn_maple_supplier_001', customerKey: 'maple', day: 0, time: '9:04', paymentDescription: '$9,000 supplier', paymentAmount: 9_000, walletBalance: 1_000 },
    { type: 'repayment', requestId: 'txn_cedar_supplier_001', day: 6, time: '9:00', depositAmount: 10_000 },
    { type: 'decline', requestId: 'txn_maple_supplier_002', customerKey: 'maple', day: 6, time: '9:30', paymentDescription: '$9,000 supplier (retried)', paymentAmount: 9_000, walletBalance: 1_000 },
  ],
};

const tightEscalates: ScenarioInputs = {
  facility: KIT5_FACILITY,
  outstanding: 33_500,
  startDate: START_DATE,
  customers: [
    twin('orchid', 'acct_orchid001', 'Orchid Studio', 20_000),
    twin('willow', 'acct_willow001', 'Willow Studio', 20_000),
  ],
  events: [
    { type: 'decline', requestId: 'txn_orchid_contractor_001', customerKey: 'orchid', day: 0, time: '9:00', paymentDescription: '$3,500 contractor', paymentAmount: 3_500, walletBalance: 500 },
    { type: 'decline', requestId: 'txn_willow_contractor_001', customerKey: 'willow', day: 0, time: '9:03', paymentDescription: '$3,500 contractor', paymentAmount: 3_500, walletBalance: 500 },
  ],
};

export const SCENARIOS: Scenario[] = [
  {
    id: 'original',
    name: 'Original',
    summary: "GD's four customers on our facility ($50K, 20% floor), decided one at a time as they arrive.",
    inputs: original,
  },
  {
    id: 'kit5-target',
    name: 'Kit 5 target',
    summary: 'Five declines, $27K of shortfalls against $15K deployable. Juniper sends the same request as Nova, arrives later and is refused at the floor.',
    inputs: kit5Target,
  },
  {
    id: 'first-come',
    name: 'First come, first served',
    summary: 'Two identical Tier A requests. The first is funded; the second would breach the floor and is refused until the first is repaid.',
    inputs: firstCome,
  },
  {
    id: 'tight-escalates',
    name: 'Tight capital → human',
    summary: 'Two identical Tier A requests. The first drops capital into Critical, so the second goes to a credit officer instead of auto-funding.',
    inputs: tightEscalates,
  },
];

/**
 * Where scenario inputs come from. Today: the local data above.
 * Later: replace the body with a fetch to the backend that returns Scenario[].
 */
export function loadScenarios(): Scenario[] {
  return structuredClone(SCENARIOS);
}
