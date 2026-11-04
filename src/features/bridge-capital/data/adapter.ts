/**
 * Where Bridge Capital reads and writes its data.
 *
 * The policy engine is pure; everything with state or a side effect goes
 * through this interface, so a real Airwallex-backed adapter can replace the
 * in-memory one without touching the rules.
 */
import type {
  Acceptance,
  Approval,
  Bridge,
  BridgeOffer,
  BridgeRequest,
  CardTransaction,
  ChargeResponse,
  Customer,
  Decision,
  Deposit,
  PlatformCapital,
  TimelineEvent,
  TransferResponse,
} from '../types.js';

export interface DataAdapter {
  // Customers and capital
  listCustomers(): Promise<Customer[]>;
  getCustomer(customerId: string): Promise<Customer | null>;
  getCapital(): Promise<PlatformCapital>;
  setOutstanding(outstanding: number): Promise<PlatformCapital>;

  // Declines and requests
  listDeclinedTransactions(): Promise<CardTransaction[]>;
  listRequests(): Promise<BridgeRequest[]>;

  // Decisions
  saveDecision(decision: Decision): Promise<Decision>;
  listDecisions(): Promise<Decision[]>;
  /** The decision already taken for a transaction, if any (spec §4 rule 6). */
  getDecisionForTransaction(transactionId: string): Promise<Decision | null>;

  // Offers, acceptance, approval
  saveOffer(offer: BridgeOffer): Promise<BridgeOffer>;
  getOffer(offerId: string): Promise<BridgeOffer | null>;
  saveAcceptance(acceptance: Acceptance): Promise<Acceptance>;
  getAcceptance(offerId: string): Promise<Acceptance | null>;
  saveApproval(approval: Approval): Promise<Approval>;
  getApproval(offerId: string): Promise<Approval | null>;

  // Bridges
  saveBridge(bridge: Bridge): Promise<Bridge>;
  getBridge(bridgeId: string): Promise<Bridge | null>;
  listBridges(customerId?: string): Promise<Bridge[]>;

  // Money movement, mirroring Airwallex connected-account transfers and charges
  disburse(bridge: Bridge): Promise<TransferResponse>;
  chargeDeposit(bridge: Bridge, deposit: Deposit): Promise<ChargeResponse>;

  // Timeline
  appendTimeline(event: Omit<TimelineEvent, 'id'>): Promise<TimelineEvent>;
  listTimeline(customerId?: string): Promise<TimelineEvent[]>;
}
