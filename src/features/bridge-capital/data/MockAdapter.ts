/**
 * In-memory DataAdapter, seeded with the four demo customers.
 *
 * Money movement goes through the existing mock Airwallex client in
 * src/services, so transfers and charges carry real Airwallex-shaped responses.
 */
import { mockAirwallexClient } from '../../../services/mock/AirwallexClient.js';
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
import type { DataAdapter } from './adapter.js';
import { generateCustomers } from './generator.js';
import { monthlyCashFlow } from './cashflow.js';
import { DEMO_BILLS, DEMO_CARDS } from './extras.js';
import { round2 } from '../policy/util.js';
import {
  AS_OF,
  DEMO_BLUEPRINTS,
  DEMO_BRIDGES,
  DEMO_CAPITAL,
  DEMO_DEPOSITS,
  DEMO_REQUESTS,
  DEMO_TIMELINE,
  DEMO_TRANSACTIONS,
} from './customers.js';

export interface MockAdapterSeed {
  customers?: Customer[];
  capital?: PlatformCapital;
  transactions?: CardTransaction[];
  deposits?: Deposit[];
  requests?: BridgeRequest[];
  bridges?: Bridge[];
  timeline?: TimelineEvent[];
  bills?: Bill[];
  cards?: IssuedCard[];
}

/** The demo data set, built from the blueprints. */
export function demoSeed(): Required<MockAdapterSeed> {
  return {
    customers: generateCustomers(DEMO_BLUEPRINTS, { as_of: AS_OF }),
    capital: { ...DEMO_CAPITAL },
    transactions: DEMO_TRANSACTIONS.map((t) => ({ ...t })),
    deposits: DEMO_DEPOSITS.map((d) => ({ ...d })),
    requests: DEMO_REQUESTS.map((r) => ({ ...r })),
    bridges: DEMO_BRIDGES.map((b) => ({ ...b })),
    timeline: DEMO_TIMELINE.map((e) => ({ ...e })),
    bills: DEMO_BILLS.map((b) => ({ ...b })),
    cards: DEMO_CARDS.map((c) => ({ ...c, blocked_categories: [...c.blocked_categories] })),
  };
}

export class MockAdapter implements DataAdapter {
  private customers: Customer[];
  private capital: PlatformCapital;
  private transactions: CardTransaction[];
  private deposits: Deposit[];
  private requests: BridgeRequest[];
  private decisions: Decision[] = [];
  private offers = new Map<string, BridgeOffer>();
  private acceptances = new Map<string, Acceptance>();
  private approvals = new Map<string, Approval>();
  private bridges = new Map<string, Bridge>();
  private timeline: TimelineEvent[] = [];
  private bills: Bill[];
  private cards: IssuedCard[];
  private receipts: Receipt[] = [];
  private collectionActions: CollectionAction[] = [];
  /** Money moved in or out of each wallet since the last end-of-day balance. */
  private walletMoves = new Map<string, number>();

  constructor(seed: MockAdapterSeed = {}) {
    const base = demoSeed();
    this.customers = seed.customers ?? base.customers;
    this.capital = seed.capital ?? base.capital;
    this.transactions = seed.transactions ?? base.transactions;
    this.deposits = seed.deposits ?? base.deposits;
    this.requests = seed.requests ?? base.requests;
    for (const b of seed.bridges ?? base.bridges) this.bridges.set(b.id, b);
    this.timeline = [...(seed.timeline ?? base.timeline)];
    this.bills = seed.bills ?? base.bills;
    this.cards = seed.cards ?? base.cards;
  }

  private moveWallet(customerId: string, amount: number): void {
    this.walletMoves.set(customerId, round2((this.walletMoves.get(customerId) ?? 0) + amount));
  }

  async listCustomers(): Promise<Customer[]> {
    return this.customers;
  }

  async getCustomer(customerId: string): Promise<Customer | null> {
    return this.customers.find((c) => c.id === customerId) ?? null;
  }

  async getCapital(): Promise<PlatformCapital> {
    return { ...this.capital };
  }

  async setOutstanding(outstanding: number): Promise<PlatformCapital> {
    this.capital = { ...this.capital, outstanding };
    return { ...this.capital };
  }

  async listTransactions(customerId?: string): Promise<CardTransaction[]> {
    return customerId === undefined
      ? this.transactions
      : this.transactions.filter((t) => t.customer_id === customerId);
  }

  async listDeclinedTransactions(): Promise<CardTransaction[]> {
    return this.transactions.filter((t) => t.status === 'DECLINED');
  }

  async listDeposits(customerId?: string): Promise<Deposit[]> {
    return customerId === undefined
      ? this.deposits
      : this.deposits.filter((d) => d.customer_id === customerId);
  }

  async listRequests(): Promise<BridgeRequest[]> {
    return this.requests;
  }

  async saveDecision(decision: Decision): Promise<Decision> {
    this.decisions = [...this.decisions.filter((d) => d.request_id !== decision.request_id), decision];
    return decision;
  }

  async listDecisions(): Promise<Decision[]> {
    return this.decisions;
  }

  async getDecisionForTransaction(transactionId: string): Promise<Decision | null> {
    return this.decisions.find((d) => d.transaction_id === transactionId) ?? null;
  }

  async saveOffer(offer: BridgeOffer): Promise<BridgeOffer> {
    this.offers.set(offer.id, offer);
    return offer;
  }

  async getOffer(offerId: string): Promise<BridgeOffer | null> {
    return this.offers.get(offerId) ?? null;
  }

  async saveAcceptance(acceptance: Acceptance): Promise<Acceptance> {
    this.acceptances.set(acceptance.offer_id, acceptance);
    return acceptance;
  }

  async getAcceptance(offerId: string): Promise<Acceptance | null> {
    return this.acceptances.get(offerId) ?? null;
  }

  async saveApproval(approval: Approval): Promise<Approval> {
    this.approvals.set(approval.offer_id, approval);
    return approval;
  }

  async getApproval(offerId: string): Promise<Approval | null> {
    return this.approvals.get(offerId) ?? null;
  }

  async saveBridge(bridge: Bridge): Promise<Bridge> {
    this.bridges.set(bridge.id, bridge);
    return bridge;
  }

  async getBridge(bridgeId: string): Promise<Bridge | null> {
    return this.bridges.get(bridgeId) ?? null;
  }

  async listBridges(customerId?: string): Promise<Bridge[]> {
    const all = [...this.bridges.values()];
    return customerId === undefined ? all : all.filter((b) => b.customer_id === customerId);
  }

  async disburse(bridge: Bridge): Promise<TransferResponse> {
    const transfer = await mockAirwallexClient.createTransfer({
      destination: bridge.customer_id,
      amount: bridge.principal,
      currency: 'USD',
      reason: 'Bridge Capital disbursement',
      reference: bridge.id,
      requestId: crypto.randomUUID(),
    });
    this.capital = {
      ...this.capital,
      outstanding: this.capital.outstanding + bridge.principal,
    };
    this.moveWallet(bridge.customer_id, bridge.principal);
    return transfer;
  }

  async chargeDeposit(bridge: Bridge, deposit: Deposit): Promise<ChargeResponse> {
    return mockAirwallexClient.createCharge({
      source: bridge.customer_id,
      amount: deposit.amount,
      currency: 'USD',
      reason: 'Bridge Capital collection',
      reference: bridge.id,
      requestId: crypto.randomUUID(),
    });
  }

  async appendTimeline(event: Omit<TimelineEvent, 'id'>): Promise<TimelineEvent> {
    const stored: TimelineEvent = { ...event, id: `evt_${this.timeline.length + 1}` };
    this.timeline.push(stored);
    return stored;
  }

  async listTimeline(customerId?: string): Promise<TimelineEvent[]> {
    return customerId === undefined
      ? this.timeline
      : this.timeline.filter((e) => e.customer_id === customerId);
  }

  async saveRequest(request: BridgeRequest): Promise<BridgeRequest> {
    this.requests = [...this.requests.filter((r) => r.id !== request.id), request];
    return request;
  }

  async getWalletBalance(customerId: string): Promise<number> {
    const customer = this.customers.find((c) => c.id === customerId);
    const lastClose = customer?.balance_history.at(-1)?.balance ?? 0;
    return round2(lastClose + (this.walletMoves.get(customerId) ?? 0));
  }

  async chargeWallet(customerId: string, amount: number, reference: string): Promise<ChargeResponse> {
    const charge = await mockAirwallexClient.createCharge({
      source: customerId,
      amount,
      currency: 'USD',
      reason: 'FlowCard wallet payment',
      reference,
      requestId: crypto.randomUUID(),
    });
    this.moveWallet(customerId, -amount);
    return charge;
  }

  async getCashFlow(customerId: string, months = 12): Promise<MonthlyCashFlow[]> {
    const customer = this.customers.find((c) => c.id === customerId);
    const asOf = customer?.balance_history.at(-1)?.date ?? new Date().toISOString().slice(0, 10);
    return monthlyCashFlow(customerId, this.deposits, this.transactions, asOf, months);
  }

  async listBills(customerId?: string): Promise<Bill[]> {
    return customerId === undefined ? this.bills : this.bills.filter((b) => b.customer_id === customerId);
  }

  async saveBill(bill: Bill): Promise<Bill> {
    this.bills = this.bills.map((b) => (b.id === bill.id ? bill : b));
    return bill;
  }

  async listCards(customerId?: string): Promise<IssuedCard[]> {
    return customerId === undefined ? this.cards : this.cards.filter((c) => c.customer_id === customerId);
  }

  async saveCard(card: IssuedCard): Promise<IssuedCard> {
    const exists = this.cards.some((c) => c.id === card.id);
    this.cards = exists ? this.cards.map((c) => (c.id === card.id ? card : c)) : [...this.cards, card];
    return card;
  }

  async removeCard(cardId: string): Promise<void> {
    this.cards = this.cards.filter((c) => c.id !== cardId);
  }

  async listReceipts(): Promise<Receipt[]> {
    return this.receipts;
  }

  async attachReceipt(receipt: Receipt): Promise<Receipt> {
    this.receipts = [...this.receipts.filter((r) => r.transaction_id !== receipt.transaction_id), receipt];
    return receipt;
  }

  async listCollectionActions(customerId?: string): Promise<CollectionAction[]> {
    return customerId === undefined
      ? this.collectionActions
      : this.collectionActions.filter((a) => a.customer_id === customerId);
  }

  async saveCollectionAction(action: Omit<CollectionAction, 'id'>): Promise<CollectionAction> {
    const stored: CollectionAction = { ...action, id: `col_${this.collectionActions.length + 1}` };
    this.collectionActions.push(stored);
    return stored;
  }
}
