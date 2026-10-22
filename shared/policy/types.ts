/** Version of the deterministic underwriting rules implemented in this core. */
export const POLICY_VERSION = 'underwriting-2026-10-20-v1' as const;

/** Rating for each of the four underwriting signals. */
export type SignalRating = 'green' | 'yellow' | 'red';

/** The four signals that determine a customer's tier. */
export interface Signals {
  inflowReliability: SignalRating;
  inflowConcentration: SignalRating;
  cashBuffer: SignalRating;
  trackRecord: SignalRating;
}

/** Customer tier derived from signal ratings. 'none' means no credit line. */
export type Tier = 'A' | 'B' | 'C' | 'none';

/** Stable machine-readable reasons emitted by policy calculations. */
export type PolicyReasonCode =
  | 'NO_GO_APPLIED'
  | 'RED_INFLOW_RELIABILITY'
  | 'MULTIPLE_RED_SIGNALS'
  | 'ONE_RED_SIGNAL'
  | 'THREE_OR_MORE_YELLOW_SIGNALS'
  | 'TWO_YELLOW_SIGNALS'
  | 'TIER_A'
  | 'STARTER_LINE'
  | 'STARTER_PROMOTED'
  | 'NO_CREDIT_LINE'
  | 'DOOR_CLOSED'
  | 'SHORTFALL_EXCEEDS_REMAINING_LINE'
  | 'CREDIT_LINE_TOO_SMALL'
  | 'INSUFFICIENT_DEPLOYABLE_CAPITAL';

/** Outcome from mapping ratings to a tier, with stable audit reasons. */
export interface TierAssignment {
  tier: Tier;
  reasonCodes: PolicyReasonCode[];
}

/** Repayment facts derived from the internal bridge ledger. */
export interface RepaymentHistory {
  /** Number of bridges fully repaid on time, as determined by the ledger. */
  fullyRepaidOnTimeBridges: number;
}

/** Capital band describing how much of the facility is still free. */
export type Band = 'a_lot' | 'normal' | 'less' | 'critical' | 'at_floor';

/** Whether a tier can get a bridge at the current capital band. */
export type Door = 'auto' | 'human' | 'closed';

/** Facility-level configuration. Amounts are in dollars for the demo core. */
export interface FacilityConfig {
  facilitySize: number;
  reserveFloorPct: number;
  maxPerCustomerPct: number;
  feeRatePer30Days: number;
  minHistoryDays: number;
  /** Optional cap from an approved customer-specific facility decision. */
  approvedBridgeCap?: number;
}

/** A customer's profile for underwriting. */
export interface CustomerProfile {
  id: string;
  name: string;
  monthlyInflow: number;
  signals: Signals;
  historyDays: number;
  noGo: {
    excludedIndustry: boolean;
    fraudFlag: boolean;
    accountFrozen: boolean;
    pastWriteOff: boolean;
    monoClient: boolean;
  };
}

/** Result of a bridge eligibility check. */
export interface BridgeDecision {
  approved: boolean;
  door: Door;
  allocation: number;
  reasons: string[];
  reasonCodes: PolicyReasonCode[];
}
