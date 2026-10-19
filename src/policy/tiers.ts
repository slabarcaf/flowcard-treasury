import type { Signals, SignalRating, Tier } from './types.js';

/**
 * Returns true if any no-go rule applies.
 * Mono-client (>70% concentration) is checked via the noGo flag, not signals.
 */
export function hasNoGo(noGo: {
  excludedIndustry: boolean;
  fraudFlag: boolean;
  accountFrozen: boolean;
  pastWriteOff: boolean;
  monoClient: boolean;
}): boolean {
  return Object.values(noGo).some(Boolean);
}

/**
 * Assign a tier from the four signal ratings.
 *
 * Rules (Underwriting Rules v001):
 * - Red on inflow reliability → no line
 * - All green or 1 yellow → A
 * - 2 yellows → B
 * - 3+ yellows or 1 red (not on reliability) → C
 */
export function assignTier(signals: Signals): Tier {
  if (signals.inflowReliability === 'red') return 'none';

  const ratings: SignalRating[] = [
    signals.inflowReliability,
    signals.inflowConcentration,
    signals.cashBuffer,
    signals.trackRecord,
  ];

  const yellows = ratings.filter((r) => r === 'yellow').length;
  const reds = ratings.filter((r) => r === 'red').length;

  // One red on a signal other than reliability → C
  if (reds >= 1) return 'C';
  if (yellows >= 3) return 'C';
  if (yellows === 2) return 'B';
  return 'A';
}
