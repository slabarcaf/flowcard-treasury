import type { Band, BridgeDecision, FacilityConfig, PolicyReasonCode, Tier } from './types.js';
import { getDoor } from './facility.js';

export interface BridgeCheckParams {
  shortfall: number;
  creditLine: number;
  outstanding: number;
  tier: Tier;
  band: Band;
  deployableCapital: number;
  facilityConfig: FacilityConfig;
}

/** Deterministically check a full-or-zero bridge request. */
export function checkBridge(params: BridgeCheckParams): BridgeDecision {
  const { shortfall, creditLine, outstanding, tier, band, deployableCapital } = params;
  const door = getDoor(band, tier);
  const reasons: string[] = [];
  const reasonCodes: PolicyReasonCode[] = [];

  if (tier === 'none') {
    return {
      approved: false,
      door: 'closed',
      allocation: 0,
      reasons: ['Customer has no credit line (ineligible)'],
      reasonCodes: ['NO_CREDIT_LINE'],
    };
  }
  if (door === 'closed') {
    reasons.push(`Tier ${tier} door is Closed in band ${band.replace('_', ' ')}`);
    reasonCodes.push('DOOR_CLOSED');
  }
  const remainingLine = creditLine - outstanding;
  if (shortfall > remainingLine) {
    reasons.push(
      `Shortfall $${shortfall.toLocaleString()} exceeds remaining line $${remainingLine.toLocaleString()}`,
    );
    reasonCodes.push('SHORTFALL_EXCEEDS_REMAINING_LINE');
  }
  if (creditLine < shortfall) {
    reasons.push(
      `$${creditLine.toLocaleString()} does not cover the $${shortfall.toLocaleString()} shortfall, so the payment would still fail`,
    );
    reasonCodes.push('CREDIT_LINE_TOO_SMALL');
  }
  if (deployableCapital < shortfall) {
    reasons.push(
      `Deployable capital $${deployableCapital.toLocaleString()} insufficient for $${shortfall.toLocaleString()} shortfall`,
    );
    reasonCodes.push('INSUFFICIENT_DEPLOYABLE_CAPITAL');
  }
  if (reasons.length > 0) {
    return { approved: false, door, allocation: 0, reasons, reasonCodes };
  }
  return { approved: true, door, allocation: shortfall, reasons, reasonCodes };
}
