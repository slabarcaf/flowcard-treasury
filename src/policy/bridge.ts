import type { Band, BridgeDecision, FacilityConfig, Tier } from './types.js';
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

/**
 * Decide whether a bridge request can be approved.
 *
 * Checks (per Underwriting Rules v001 "Per request"):
 * 1. History / eligibility (tier !== 'none')
 * 2. Line check: shortfall fits within remaining line
 * 3. No partial bridge: if credit line < shortfall, payment still fails
 * 4. Facility check: door is not closed, and deployable >= shortfall
 */
export function checkBridge(params: BridgeCheckParams): BridgeDecision {
  const {
    shortfall,
    creditLine,
    outstanding,
    tier,
    band,
    deployableCapital,
  } = params;

  const door = getDoor(band, tier);
  const reasons: string[] = [];

  // No tier means ineligible
  if (tier === 'none') {
    reasons.push('Customer has no credit line (ineligible)');
    return { approved: false, door: 'closed', allocation: 0, reasons };
  }

  // Door closed at this capital level
  if (door === 'closed') {
    reasons.push(`Tier ${tier} door is Closed in band ${band.replace('_', ' ')}`);
  }

  // Line check: remaining line
  const remainingLine = creditLine - outstanding;
  if (shortfall > remainingLine) {
    reasons.push(
      `Shortfall $${shortfall.toLocaleString()} exceeds remaining line $${remainingLine.toLocaleString()}`,
    );
  }

  // No partial bridge: even if we could fund up to the credit line,
  // it won't clear the payment if the line < shortfall
  if (creditLine < shortfall) {
    reasons.push(
      `$${creditLine.toLocaleString()} does not cover the $${shortfall.toLocaleString()} shortfall, so the payment would still fail`,
    );
  }

  // Facility check: enough deployable capital
  if (deployableCapital < shortfall) {
    reasons.push(
      `Deployable capital $${deployableCapital.toLocaleString()} insufficient for $${shortfall.toLocaleString()} shortfall`,
    );
  }

  if (reasons.length > 0) {
    return { approved: false, door, allocation: 0, reasons };
  }

  return { approved: true, door, allocation: shortfall, reasons: [] };
}
