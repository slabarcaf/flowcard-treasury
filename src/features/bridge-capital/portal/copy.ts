/**
 * Customer-facing words for states the engine decides. The portal shows status,
 * amount due and the one restriction in force; the ladder, the collections log
 * and internal actions stay in the back office (spec §8.4).
 */
import type { BridgeOutcome } from '../types.js';
import type { CardAccess } from '../policy/cardAccess.js';
import type { ToneName } from './tokens.js';
import { usd } from '../ui/format.js';

export function restrictionSentence(access: CardAccess): string | null {
  if (access.state === 'FROZEN') return 'Your cards are paused until your bridge is repaid.';
  if (access.state === 'RESTRICTED') {
    const cap = access.cap === null ? '' : `, up to ${usd(access.cap)}`;
    return `Your cards only work for essential business spend (suppliers, payroll, inventory)${cap} until your bridge is repaid.`;
  }
  return null;
}

export const OUTCOME_COPY: Record<BridgeOutcome, { label: string; tone: ToneName }> = {
  ON_TIME: { label: 'Repaid on time', tone: 'positive' },
  LATE: { label: 'Repaid late', tone: 'warning' },
  DEFAULTED: { label: 'Not repaid', tone: 'critical' },
  OPEN: { label: 'Running', tone: 'accent' },
};

/** Merchant categories a customer can plan a bridge for, critical first. */
export const PURPOSES: Array<{ category: string; label: string }> = [
  { category: 'suppliers', label: 'Suppliers' },
  { category: 'payroll', label: 'Payroll' },
  { category: 'inventory', label: 'Inventory' },
  { category: 'utilities', label: 'Utilities' },
  { category: 'logistics', label: 'Logistics' },
  { category: 'professional_services', label: 'Services' },
  { category: 'saas', label: 'Software' },
  { category: 'travel', label: 'Travel' },
];

export function categoryLabel(category: string): string {
  return (
    PURPOSES.find((p) => p.category === category)?.label ??
    category.charAt(0).toUpperCase() + category.slice(1).replace(/_/g, ' ')
  );
}
