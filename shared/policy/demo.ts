import { checkBridge } from './bridge.js';
import { computeCreditLine } from './creditLine.js';
import { assignTier, hasNoGo } from './tiers.js';
import type {
  CustomerProfile,
  DemoPolicyGateResult,
  DemoPolicyReadiness,
  FacilityConfig,
  PolicyReasonCode,
  Tier,
  Band,
} from './types.js';

const REASON_TEXT: Readonly<Record<PolicyReasonCode, string>> = {
  NO_GO_APPLIED: 'A configured no-go rule applies.',
  RED_INFLOW_RELIABILITY: 'Inflow reliability is red, so no credit line is available.',
  MULTIPLE_RED_SIGNALS: 'Two or more red ratings mean no credit line is available.',
  ONE_RED_SIGNAL: 'One red rating limits the customer to Tier C.',
  THREE_OR_MORE_YELLOW_SIGNALS: 'Three or more yellow ratings limit the customer to Tier C.',
  TWO_YELLOW_SIGNALS: 'Two yellow ratings assign Tier B.',
  TIER_A: 'Ratings assign Tier A.',
  STARTER_LINE: 'A starter line applies.',
  STARTER_PROMOTED: 'The repayment history qualifies for the promoted line.',
  NO_CREDIT_LINE: 'No credit line is available.',
  DOOR_CLOSED: 'The capital door is closed for this tier.',
  SHORTFALL_EXCEEDS_REMAINING_LINE: 'The request exceeds the remaining customer line.',
  CREDIT_LINE_TOO_SMALL: 'The full shortfall exceeds the customer credit line.',
  INSUFFICIENT_DEPLOYABLE_CAPITAL: 'Deployable facility capital is insufficient for the full shortfall.',
  SNAPSHOT_MISSING: 'The required Salesforce snapshot is missing.',
  SNAPSHOT_STALE: 'The Salesforce snapshot is stale.',
  SNAPSHOT_INVALID: 'The Salesforce snapshot is invalid.',
  EVIDENCE_REVIEW_REQUIRED: 'Evidence requires human review before policy can proceed.',
  EVIDENCE_UNMAPPED: 'Evidence contains a signal without an approved mapping.',
  EVIDENCE_FAILED: 'Evidence extraction failed; no prior result is reused.',
  CREDIT_REVIEW_WATCH: 'Salesforce credit review is on watch; human approval is required.',
  CREDIT_REVIEW_BLOCKED: 'Salesforce credit review is hold or review required; new decisions are blocked.',
  CREDIT_REVIEW_UNKNOWN: 'Salesforce credit review status is unknown; new decisions are blocked.',
  PRIOR_DEFAULT: 'A verified prior default blocks a new bridge.',
  PRIOR_DEFAULT_UNKNOWN: 'Prior default status is unknown; new decisions are blocked.',
  REPAYMENT_HISTORY_UNKNOWN: 'Trusted on-time repayment history is missing or invalid; no credit line is available.',
};

export function policyReasonText(code: PolicyReasonCode): string {
  return REASON_TEXT[code];
}

/** Apply the synthetic snapshot and evidence gates shared by both demo engines. */
export function evaluateDemoPolicyGate(readiness: DemoPolicyReadiness): DemoPolicyGateResult {
  const reasonCodes: PolicyReasonCode[] = [];
  if (readiness.snapshot === 'missing') reasonCodes.push('SNAPSHOT_MISSING');
  if (readiness.snapshot === 'stale') reasonCodes.push('SNAPSHOT_STALE');
  if (readiness.snapshot === 'invalid') reasonCodes.push('SNAPSHOT_INVALID');
  if (readiness.evidence === 'review_required') reasonCodes.push('EVIDENCE_REVIEW_REQUIRED');
  if (readiness.evidence === 'unmapped') reasonCodes.push('EVIDENCE_UNMAPPED');
  if (readiness.evidence === 'failed') reasonCodes.push('EVIDENCE_FAILED');
  if (readiness.creditReview === 'watch') reasonCodes.push('CREDIT_REVIEW_WATCH');
  if (readiness.creditReview === 'hold' || readiness.creditReview === 'review_required') reasonCodes.push('CREDIT_REVIEW_BLOCKED');
  if (readiness.creditReview === 'unknown') reasonCodes.push('CREDIT_REVIEW_UNKNOWN');
  if (readiness.priorDefault === 'yes') reasonCodes.push('PRIOR_DEFAULT');
  if (readiness.priorDefault === 'unknown') reasonCodes.push('PRIOR_DEFAULT_UNKNOWN');

  const isHardBlocked = reasonCodes.some((code) => code !== 'CREDIT_REVIEW_WATCH');
  return {
    allowed: !isHardBlocked,
    humanApprovalRequired: reasonCodes.includes('CREDIT_REVIEW_WATCH'),
    ineligible: reasonCodes.includes('PRIOR_DEFAULT') || reasonCodes.includes('NO_GO_APPLIED'),
    reasonCodes,
    reasons: reasonCodes.map(policyReasonText),
  };
}

export interface DemoPolicyDecision {
  status: 'blocked' | 'ineligible' | 'human_review' | 'declined' | 'approved';
  tier: Tier;
  creditLine: number;
  door: 'auto' | 'human' | 'closed';
  allocation: number;
  reasonCodes: PolicyReasonCode[];
  reasons: string[];
}

/** The shared deterministic calculation path used by both fixture demo engines. */
export function evaluateDemoPolicy(input: {
  profile: CustomerProfile;
  facility: FacilityConfig;
  readiness: DemoPolicyReadiness;
  shortfall: number;
  outstanding: number;
  band: Band;
  deployableCapital: number;
}): DemoPolicyDecision {
  const gate = evaluateDemoPolicyGate(input.readiness);
  if (!gate.allowed) {
    return {
      status: gate.ineligible ? 'ineligible' : 'blocked',
      tier: 'none', creditLine: 0, door: 'closed', allocation: 0,
      reasonCodes: gate.reasonCodes, reasons: gate.reasons,
    };
  }

  if (input.profile.historyDays < input.facility.minHistoryDays) {
    const reasonCodes: PolicyReasonCode[] = ['NO_CREDIT_LINE'];
    return {
      status: 'ineligible', tier: 'none', creditLine: 0, door: 'closed', allocation: 0,
      reasonCodes, reasons: [`${policyReasonText('NO_CREDIT_LINE')} ${input.profile.historyDays} days of history; ${input.facility.minHistoryDays} required.`],
    };
  }
  if (hasNoGo(input.profile.noGo)) {
    return {
      status: 'ineligible', tier: 'none', creditLine: 0, door: 'closed', allocation: 0,
      reasonCodes: ['NO_GO_APPLIED'], reasons: [policyReasonText('NO_GO_APPLIED')],
    };
  }

  const fullyRepaidOnTimeBridges = input.profile.fullyRepaidOnTimeBridges;
  if (typeof fullyRepaidOnTimeBridges !== 'number' || !Number.isSafeInteger(fullyRepaidOnTimeBridges) || fullyRepaidOnTimeBridges < 0) {
    return {
      status: 'blocked', tier: 'none', creditLine: 0, door: 'closed', allocation: 0,
      reasonCodes: ['REPAYMENT_HISTORY_UNKNOWN'], reasons: [policyReasonText('REPAYMENT_HISTORY_UNKNOWN')],
    };
  }

  const tier = assignTier(input.profile.signals);
  const creditLine = computeCreditLine(tier, input.profile.monthlyInflow, input.facility,
    input.profile.signals, { fullyRepaidOnTimeBridges });
  const bridge = checkBridge({
    shortfall: input.shortfall,
    creditLine,
    outstanding: input.outstanding,
    tier,
    band: input.band,
    deployableCapital: input.deployableCapital,
    facilityConfig: input.facility,
  });
  const codes = [...bridge.reasonCodes];
  const reasons = [...bridge.reasons];
  if (input.readiness.creditReview === 'watch') {
    codes.push('CREDIT_REVIEW_WATCH');
    reasons.push(policyReasonText('CREDIT_REVIEW_WATCH'));
  }
  if (codes.some((code) => code !== 'CREDIT_REVIEW_WATCH')) {
    return { status: 'declined', tier, creditLine, door: bridge.door, allocation: 0, reasonCodes: codes, reasons };
  }
  if (input.readiness.creditReview === 'watch' || bridge.door === 'human') {
    return { status: 'human_review', tier, creditLine, door: 'human', allocation: input.shortfall, reasonCodes: codes, reasons };
  }
  return { status: 'approved', tier, creditLine, door: bridge.door, allocation: bridge.allocation, reasonCodes: codes, reasons };
}
