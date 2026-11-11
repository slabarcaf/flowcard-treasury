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
  Bill,
  Bridge,
  BridgeOffer,
  BridgeRequest,
  CardTransaction,
  ChargeResponse,
  CollectionAction,
  Customer,
  Decision,
  Deposit,
  IssuedCard,
  MonthlyCashFlow,
  PlatformCapital,
  Receipt,
  TimelineEvent,
  TransferResponse,
} from '../types.js';

export interface DataAdapter {
  // Customers and capital
  listCustomers(): Promise<Customer[]>;
  getCustomer(customerId: string): Promise<Customer | null>;
  getCapital(): Promise<PlatformCapital>;
  setOutstanding(outstanding: number): Promise<PlatformCapital>;

  // Card transactions and requests
  /** Every card transaction, or one customer's. */
  listTransactions(customerId?: string): Promise<CardTransaction[]>;
  listDeclinedTransactions(): Promise<CardTransaction[]>;
  /** Settled deposits, or one customer's. Derived from paid invoices. */
  listDeposits(customerId?: string): Promise<Deposit[]>;
  listRequests(): Promise<BridgeRequest[]>;
  /** A request raised from the portal: a planned bridge or Flex on a bill (spec §8). */
  saveRequest(request: BridgeRequest): Promise<BridgeRequest>;

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

  // ---- Spec §8 proposals. Each names the backend route it will need. ----

  /** backend: GET balances/current per connected account (server already calls it; needs a route). */
  getWalletBalance(customerId: string): Promise<number>;
  /** Take money from the wallet, e.g. the customer repaying early. backend: POST charges (exists server-side). */
  chargeWallet(customerId: string, amount: number, reference: string): Promise<ChargeResponse>;
  /** backend: per-customer in/out route over the transaction report and deposits (missing). */
  getCashFlow(customerId: string, months?: number): Promise<MonthlyCashFlow[]>;

  /** backend: supplier bills (missing; needs payouts and beneficiaries). */
  listBills(customerId?: string): Promise<Bill[]>;
  saveBill(bill: Bill): Promise<Bill>;

  /** backend: Airwallex Issuing cards and controls (missing). */
  listCards(customerId?: string): Promise<IssuedCard[]>;
  saveCard(card: IssuedCard): Promise<IssuedCard>;
  removeCard(cardId: string): Promise<void>;

  /** backend: file storage for receipts (missing; could reuse the onboarding S3 store). */
  listReceipts(): Promise<Receipt[]>;
  attachReceipt(receipt: Receipt): Promise<Receipt>;

  /** backend: collections action endpoints (missing; collections service exists server-side). */
  listCollectionActions(customerId?: string): Promise<CollectionAction[]>;
  saveCollectionAction(action: Omit<CollectionAction, 'id'>): Promise<CollectionAction>;
}
