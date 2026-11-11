/**
 * What a customer's cards can do right now, from the bridge rules (spec §5) and
 * anything ops did in collections. The strictest source wins.
 *
 *   While bridged, tier C          → essential spend only, capped at the bridged amount
 *   Delinquent (8–30 DPD)          → essential spend only, all tiers
 *   Serious or defaulted (31+ DPD) → frozen
 *   Ops restricted / froze cards   → restricted / frozen, until that bridge is repaid
 */
import type { Bridge, CollectionAction, Tier } from '../types.js';
import { applyDelinquency } from './delinquency.js';
import { WHILE_BRIDGED } from './policy.config.js';
import { round2 } from './util.js';

export type CardAccessState = 'NORMAL' | 'RESTRICTED' | 'FROZEN';

export interface CardAccess {
  state: CardAccessState;
  /** Why, in ops language. */
  source: 'NONE' | 'TIER_C_WHILE_BRIDGED' | 'DELINQUENT' | 'SERIOUS' | 'OPS';
  /** Spend cap while restricted for tier C, or null. */
  cap: number | null;
}

const OPEN: Bridge['status'][] = ['DISBURSED', 'COLLECTING', 'WATCH', 'DELINQUENT', 'SERIOUS', 'DEFAULTED'];

export function cardAccess(
  tier: Tier,
  bridges: Bridge[],
  actions: CollectionAction[],
  today: string,
): CardAccess {
  const open = bridges.filter((b) => OPEN.includes(b.status));
  const openIds = new Set(open.map((b) => b.id));
  const live = actions.filter((a) => openIds.has(a.bridge_id));
  const ladder = open.map((b) => applyDelinquency(b, today));

  if (ladder.some((r) => r.actions.includes('FREEZE_CARD'))) return { state: 'FROZEN', source: 'SERIOUS', cap: null };
  if (live.some((a) => a.type === 'FREEZE_CARDS')) return { state: 'FROZEN', source: 'OPS', cap: null };
  if (ladder.some((r) => r.actions.includes('RESTRICT_CARD_TO_CRITICAL'))) {
    return { state: 'RESTRICTED', source: 'DELINQUENT', cap: null };
  }
  if (live.some((a) => a.type === 'RESTRICT_CARDS')) return { state: 'RESTRICTED', source: 'OPS', cap: null };
  if (open.length > 0 && WHILE_BRIDGED.restrictedTiers.includes(tier)) {
    return {
      state: 'RESTRICTED',
      source: 'TIER_C_WHILE_BRIDGED',
      cap: round2(open.reduce((s, b) => s + b.principal, 0)),
    };
  }
  return { state: 'NORMAL', source: 'NONE', cap: null };
}
