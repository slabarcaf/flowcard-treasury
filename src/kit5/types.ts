import type { CustomerProfile, FacilityConfig, Signals } from '../policy/types.js';
import type { DashboardState } from '../state/types.js';

// ---------------------------------------------------------------------------
// Scenario inputs — the shape the backend will eventually send.
// Everything here is plain JSON so it can come straight from an API.
// ---------------------------------------------------------------------------

export interface ScenarioCustomer extends CustomerProfile {
  /** Short key that events use to refer to this customer. */
  key: string;
  /** One-line AI rationale shown when the bridge is funded or sent for approval. */
  rationale?: string;
  /**
   * Signals after the Salesforce note is read. Only Bloom has one; its presence
   * triggers the CRM re-evaluation step when the customer's decline arrives.
   */
  crmSignalsAfter?: Signals;
}

/** A card payment declined with INSUFFICIENT_FUNDS. */
export interface DeclineEvent {
  type: 'decline';
  /** Card transaction id; also the bridge request id. Unique per event. */
  requestId: string;
  customerKey: string;
  /** Days after the scenario start date (0 = demo Monday). */
  day: number;
  /** Arrival time, "H:MM". Events are processed in (day, time) order. */
  time: string;
  paymentDescription: string;
  paymentAmount: number;
  walletBalance: number;
  /** Days after funding by which we collect. Defaults to DEFAULT_TERM_DAYS. */
  termDays?: number;
}

/** A customer deposit that repays an outstanding bridge (fee first, then principal). */
export interface RepaymentEvent {
  type: 'repayment';
  /** requestId of the bridge being repaid. */
  requestId: string;
  day: number;
  time: string;
  depositAmount: number;
}

export type ScenarioEvent = DeclineEvent | RepaymentEvent;

export interface ScenarioInputs {
  facility: FacilityConfig;
  /** Seeded ledger: already lent to other customers at the start. */
  outstanding: number;
  /** ISO date of day 0. */
  startDate: string;
  customers: ScenarioCustomer[];
  events: ScenarioEvent[];
}

export interface Scenario {
  id: string;
  name: string;
  summary: string;
  inputs: ScenarioInputs;
}

// ---------------------------------------------------------------------------
// Engine state — extends the GD dashboard state so the same components render it
// ---------------------------------------------------------------------------

/**
 * What the sequence is waiting on before the next request can be decided:
 * the Salesforce re-evaluation, a credit officer, or the presenter
 * acknowledging an auto-approval.
 */
export type Pause = { kind: 'crm' | 'human' | 'ack'; requestId: string } | null;

export interface OpenBridge {
  customerName: string;
  amount: number;
  day: number;
  dueDay: number;
}

export interface Kit5State extends DashboardState {
  inputs: ScenarioInputs;
  /** Events sorted into arrival order. */
  events: ScenarioEvent[];
  /** Index of the next event to process. */
  cursor: number;
  pause: Pause;
  day: number;
  /** Bridges funded and not yet repaid, keyed by requestId. */
  bridges: Record<string, OpenBridge>;
}
