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

/**
 * Capital band describing how much of the facility is still free.
 * Thresholds (% of lendable): a_lot >70, normal 40–70, less 10–40, critical <10, at_floor 0.
 */
export type Band = 'a_lot' | 'normal' | 'less' | 'critical' | 'at_floor';

/** Whether a tier can get a bridge at the current capital band. */
export type Door = 'auto' | 'human' | 'closed';

/** Facility-level configuration. */
export interface FacilityConfig {
  /** Total bridge facility size in dollars. */
  facilitySize: number;
  /** Reserve floor as a fraction (e.g. 0.05 for 5%). */
  reserveFloorPct: number;
  /** Max single-customer exposure as a fraction of facility (e.g. 0.20). */
  maxPerCustomerPct: number;
  /** Daily fee rate per 30-day period (e.g. 0.015 for 1.5%). */
  feeRatePer30Days: number;
  /** Minimum history days required for eligibility. */
  minHistoryDays: number;
}

/** A customer's profile for underwriting. */
export interface CustomerProfile {
  id: string;
  name: string;
  monthlyInflow: number;
  signals: Signals;
  historyDays: number;
  /** No-go flags — any true means no line regardless of signals. */
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
}
