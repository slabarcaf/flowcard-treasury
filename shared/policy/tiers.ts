import type {
  PolicyReasonCode,
  Signals,
  SignalRating,
  Tier,
  TierAssignment,
} from './types.js';

/** Returns true if any explicitly supplied no-go rule applies. */
export function hasNoGo(noGo: {
  excludedIndustry: boolean;
  fraudFlag: boolean;
  accountFrozen: boolean;
  pastWriteOff: boolean;
  monoClient: boolean;
}): boolean {
  return Object.values(noGo).some(Boolean);
}

/** Assign a tier and stable reason codes from the four signal ratings. */
export function evaluateTier(signals: Signals): TierAssignment {
  if (signals.inflowReliability === 'red') {
    return { tier: 'none', reasonCodes: ['RED_INFLOW_RELIABILITY'] };
  }

  const ratings: SignalRating[] = [
    signals.inflowReliability,
    signals.inflowConcentration,
    signals.cashBuffer,
    signals.trackRecord,
  ];
  const reds = ratings.filter((rating) => rating === 'red').length;
  if (reds >= 2) {
    return { tier: 'none', reasonCodes: ['MULTIPLE_RED_SIGNALS'] };
  }
  if (reds === 1) {
    return { tier: 'C', reasonCodes: ['ONE_RED_SIGNAL'] };
  }

  const yellows = ratings.filter((rating) => rating === 'yellow').length;
  if (yellows >= 3) {
    return { tier: 'C', reasonCodes: ['THREE_OR_MORE_YELLOW_SIGNALS'] };
  }
  if (yellows === 2) {
    return { tier: 'B', reasonCodes: ['TWO_YELLOW_SIGNALS'] };
  }
  return { tier: 'A', reasonCodes: ['TIER_A'] };
}

/** Compatibility helper returning only the assigned tier. */
export function assignTier(signals: Signals): Tier {
  return evaluateTier(signals).tier;
}

export type { PolicyReasonCode };
