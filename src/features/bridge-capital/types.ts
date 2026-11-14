/**
 * Domain types for Bridge Capital, following the policy spec in
 * docs/designs/bridge-capital-policy-spec.md.
 *
 * Airwallex-shaped transfer and charge types are reused from src/services/types.ts
 * rather than redefined here.
 */
import type {
  TransferResponse,
  ChargeResponse,
  TransferStatus,
  ChargeStatus,
  CardDeclineReason,
} from '../../services/types.js';

export type { TransferResponse, ChargeResponse, TransferStatus, ChargeStatus };

// ---------------------------------------------------------------------------
// Customer (spec §2)
// ---------------------------------------------------------------------------

/** Tier from spec §3. NONE means not eligible for a bridge. */
export type Tier = 'A' | 'B' | 'C' | 'NONE';

/** Outcome of a past bridge (spec §2). */
export type BridgeOutcome = 'ON_TIME' | 'LATE' | 'DEFAULTED' | 'OPEN';

/** One end-of-day balance. The generator produces 180 of these per customer. */
export interface DailyBalance {
  date: string;
  balance: number;
}

/** One invoice. The generator produces 24 months of these per customer. */
export interface Invoice {
  id: string;
  payer_name: string;
  amount: number;
  issued_date: string;
  due_date: string;
  /** null while unpaid. Deposits are derived from paid invoices. */
  paid_date: string | null;
}

/** An inflow we expect before a bridge term ends (spec §2, used by rule 7). */
export interface ExpectedReceivable {
  payer_name: string;
  amount: number;
  due_date: string;
  payer_on_time_rate: number;
  payer_avg_days_late: number;
  relationship_months: number;
}

/**
 * A bridge on the customer's record (spec §2 bridge_history).
 *
 * `transaction_id` and `outstanding_balance` are not named in the spec but are
 * required by eligibility rules 6 (no duplicate bridge for the same transaction)
 * and 3 (existing outstanding counts against the limit).
 */
export interface PastBridge {
  id: string;
  transaction_id: string;
  amount: number;
  term_days: number;
  opened_at: string;
  closed_at: string | null;
  days_outstanding: number;
  outcome: BridgeOutcome;
  /** Principal + fee still owed. 0 once closed. */
  outstanding_balance: number;
  /** Date the bridge was/is due, used to tell whether an open bridge is past due. */
  due_date: string;
}

/** Derived balance statistics (spec §2). */
export interface BalanceStats {
  avg_balance_30d: number;
  avg_balance_90d: number;
  avg_balance_180d: number;
  /** 30d against 180d. */
  balance_trend: 'UP' | 'FLAT' | 'DOWN';
  /** 10th percentile of the last 90 daily balances. */
  low_point_balance_90d: number;
  /** Days in the last 90 below 10% of avg_balance_90d. */
  days_near_zero_90d: number;
}

/** Derived inflow statistics (spec §2). */
export interface InflowStats {
  /** Average monthly deposits over the last 3 months. */
  monthly_inflows: number;
  /** Share of inflows from the largest payer, 0–1. */
  top_payer_concentration: number;
}

/** Derived bridge-history statistics (spec §2). */
export interface BridgeStats {
  bridges_count: number;
  /** Closed bridges only. */
  avg_days_outstanding: number;
  on_time_count: number;
  late_count: number;
  open_bridge: boolean;
  open_bridge_past_due: boolean;
}

/** CRM context. Spec §2: shown to ops, never read by the policy engine. */
export interface CrmContext {
  onboarding_date: string;
  contract_status: string;
  support_notes: Array<{ author: string; timestamp: string; text: string }>;
}

export interface Customer {
  id: string;
  name: string;
  tenure_months: number;
  /** 180 days of end-of-day balances, oldest first. */
  balance_history: DailyBalance[];
  /** 24 months of invoices, oldest first. */
  invoices: Invoice[];
  expected_receivables: ExpectedReceivable[];
  bridge_history: PastBridge[];
  balance_stats: BalanceStats;
  inflow_stats: InflowStats;
  bridge_stats: BridgeStats;
  /** Context for ops only. Never an input to tiering or eligibility. */
  crm: CrmContext;
}

// ---------------------------------------------------------------------------
// Card transactions (spec §4 spend criticality)
// ---------------------------------------------------------------------------

export type CardTransactionStatus = 'APPROVED' | 'CLEARING' | 'SETTLED' | 'DECLINED';

/** Re-exported so the feature has one vocabulary for decline reasons. */
export type DeclineReason = CardDeclineReason;

/** Spec §4: critical (suppliers, payroll, inventory), standard, discretionary. */
export type SpendCriticality = 'CRITICAL' | 'STANDARD' | 'DISCRETIONARY';

export interface CardTransaction {
  id: string;
  customer_id: string;
  card_id: string;
  amount: number;
  currency: string;
  merchant_name: string;
  /** Merchant category code or name; mapped to criticality by the policy config. */
  merchant_category: string;
  criticality: SpendCriticality;
  status: CardTransactionStatus;
  decline_reason?: DeclineReason;
  /** Wallet balance at the moment of the attempt; the shortfall is amount - this. */
  wallet_balance: number;
  created_at: string;
}

// ---------------------------------------------------------------------------
// Platform capital (spec §6)
// ---------------------------------------------------------------------------

export type Regime = 'ABUNDANT' | 'NORMAL' | 'TIGHT' | 'CRITICAL' | 'AT_FLOOR';

export interface PlatformCapital {
  facility_size: number;
  /** Reserve floor as a fraction of the facility, e.g. 0.20. */
  reserve_floor_pct: number;
  /** Principal currently lent out and not yet repaid. */
  outstanding: number;
}

// ---------------------------------------------------------------------------
// Offers, decisions, bridges (spec §4, §5, §7)
// ---------------------------------------------------------------------------

export type BridgeStatus =
  | 'OFFERED'
  | 'ACCEPTED'
  | 'NEEDS_APPROVAL'
  | 'DISBURSED'
  | 'COLLECTING'
  | 'REPAID'
  | 'WATCH'
  | 'DELINQUENT'
  | 'SERIOUS'
  | 'DEFAULTED'
  | 'WITHDRAWN'
  | 'DECLINED';

/** A request for a bridge, raised by a declined card transaction. */
export interface BridgeRequest {
  id: string;
  customer_id: string;
  transaction: CardTransaction;
  /** Shortfall being asked for. */
  principal: number;
  term_days: number;
  requested_at: string;
}

/** Priced terms presented to the customer (spec §5). */
export interface BridgeOffer {
  id: string;
  customer_id: string;
  transaction_id: string;
  principal: number;
  term_days: number;
  fee: number;
  offered_at: string;
  due_date: string;
}

/** Customer acceptance, bound to amount, fee and term (spec §5). */
export interface Acceptance {
  offer_id: string;
  principal: number;
  fee: number;
  term_days: number;
  accepted_at: string;
}

/** Human approval, bound to customer, amount, fee and term (spec §7). */
export interface Approval {
  offer_id: string;
  customer_id: string;
  principal: number;
  fee: number;
  term_days: number;
  approved_by: string;
  approved_at: string;
}

/** One eligibility rule result (spec §4). */
export interface RuleResult {
  rule_id: string;
  label: string;
  passed: boolean;
  detail: string;
}

export interface Decision {
  request_id: string;
  /** The declined transaction this decision belongs to; the key for rule 6. */
  transaction_id: string;
  customer_id: string;
  tier: Tier;
  /** The customer's limit at this tier. */
  limit: number;
  /** Outstanding bridge balance counted against the limit. */
  outstanding: number;
  regime: Regime;
  rules: RuleResult[];
  /** Details of the rules that failed, in rule order. */
  reasons: string[];
  eligible: boolean;
  requested: number;
  /** Most we could bridge right now: limit headroom capped by capital above the floor. */
  max_bridgeable: number;
  needs_human_approval: boolean;
  /** Set when this request already has a bridge (rule 6); the prior decision stands. */
  duplicate_of?: string;
}

export interface Bridge {
  id: string;
  customer_id: string;
  transaction_id: string;
  principal: number;
  fee: number;
  term_days: number;
  status: BridgeStatus;
  disbursed_at: string | null;
  due_date: string;
  principal_outstanding: number;
  fee_outstanding: number;
  /** Fee accrued past the due date at the monthly rate (spec §5). */
  accrued_fee: number;
}

/** A deposit landing in the customer's wallet, used to collect (spec §5). */
export interface Deposit {
  id: string;
  customer_id: string;
  amount: number;
  payer_name: string;
  settled_at: string;
}

// ---------------------------------------------------------------------------
// Timeline
// ---------------------------------------------------------------------------

export type TimelineEventType =
  | 'DECLINE'
  | 'OFFER'
  | 'ACCEPTANCE'
  | 'APPROVAL'
  | 'DISBURSEMENT'
  | 'DEPOSIT'
  | 'COLLECTION'
  | 'STATUS_CHANGE'
  | 'NOTICE';

export interface TimelineEvent {
  id: string;
  at: string;
  customer_id: string;
  bridge_id?: string;
  type: TimelineEventType;
  text: string;
}
