/**
 * Demo data for the customer-portal features proposed in spec §8: supplier
 * bills and issued cards. Vendors and categories come from each customer's
 * blueprint merchants, so bills and cards line up with their card spend.
 */
import type { Bill, IssuedCard } from '../types.js';
import { addDays } from '../policy/util.js';
import { AS_OF } from './customers.js';

function bill(
  customer: string,
  n: number,
  vendor: string,
  merchant_category: string,
  amount: number,
  dueInDays: number,
): Bill {
  return {
    id: `bill_${customer}_${n}`,
    customer_id: `acct_${customer}`,
    vendor,
    merchant_category,
    amount,
    due_date: addDays(AS_OF, dueInDays),
    status: 'OPEN',
    paid_at: null,
    bridge_id: null,
  };
}

export const DEMO_BILLS: Bill[] = [
  bill('northwind', 1, 'Rowe Packaging', 'suppliers', 7_400, 4),
  bill('northwind', 2, 'Harlow Freight', 'logistics', 2_360, 11),
  bill('northwind', 3, 'Citywide Power', 'utilities', 690, 18),
  bill('harbor', 1, 'Gulf Seafood Co.', 'suppliers', 4_350, 3),
  bill('harbor', 2, 'Verity Produce', 'inventory', 3_200, 9),
  bill('harbor', 3, 'Coldline Storage', 'logistics', 2_300, 15),
  bill('lumen', 1, 'Orbit Print Co.', 'suppliers', 2_150, 6),
  bill('lumen', 2, 'Hallam & Reed', 'professional_services', 1_600, 12),
  bill('lumen', 3, 'Adobe Creative Cloud', 'saas', 390, 20),
  bill('vertex', 1, 'Calder Lab Supply', 'inventory', 3_800, 7),
];

function card(
  customer: string,
  n: number,
  kind: IssuedCard['kind'],
  holder: string,
  role: string,
  last4: string,
  monthly_cap: number,
  spent_this_month: number,
  blocked_categories: string[] = [],
): IssuedCard {
  return {
    id: `card_${customer}_${n}`,
    customer_id: `acct_${customer}`,
    kind,
    holder,
    role,
    last4,
    monthly_cap,
    spent_this_month,
    blocked_categories,
    frozen: false,
  };
}

export const DEMO_CARDS: IssuedCard[] = [
  card('northwind', 1, 'PHYSICAL', 'Dana Whitfield', 'Founder', '4417', 30_000, 18_420),
  card('northwind', 2, 'PHYSICAL', 'Marco Ibáñez', 'Operations lead', '9032', 12_000, 5_110, ['travel', 'entertainment']),
  card('northwind', 3, 'VIRTUAL', 'Netsuite', 'Vendor card', '2281', 600, 480),
  card('harbor', 1, 'PHYSICAL', 'Rosa Medina', 'Owner', '6120', 25_000, 21_940),
  card('harbor', 2, 'PHYSICAL', 'Tom Achebe', 'Kitchen manager', '3388', 8_000, 6_870, ['travel']),
  card('harbor', 3, 'VIRTUAL', 'Toast POS', 'Vendor card', '5509', 400, 310),
  card('lumen', 1, 'PHYSICAL', 'Priya Natarajan', 'Founder', '1184', 15_000, 9_420),
  card('lumen', 2, 'VIRTUAL', 'Figma', 'Vendor card', '6603', 300, 180),
  card('lumen', 3, 'VIRTUAL', 'Adobe Creative Cloud', 'Vendor card', '7741', 500, 390),
  card('vertex', 1, 'PHYSICAL', 'Sam Okafor', 'Founder', '8851', 6_000, 2_310),
  card('vertex', 2, 'VIRTUAL', 'Benchling', 'Vendor card', '3307', 1_000, 860),
];

/** Merchant categories a customer can block on a card, in the order the UI lists them. */
export const BLOCKABLE_CATEGORIES = ['travel', 'entertainment', 'saas', 'professional_services', 'utilities'];
