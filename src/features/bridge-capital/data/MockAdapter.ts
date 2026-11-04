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
import type { DataAdapter } from './adapter.js';
import { generateCustomers } from './generator.js';
import {
  AS_OF,
  DEMO_BLUEPRINTS,
  DEMO_CAPITAL,
  DEMO_REQUESTS,
  DEMO_TRANSACTIONS,
} from './customers.js';

export interface MockAdapterSeed {
  customers?: Customer[];
  capital?: PlatformCapital;
  transactions?: CardTransaction[];
  requests?: BridgeRequest[];
}

/** The demo data set, built from the blueprints. */
export function demoSeed(): Required<MockAdapterSeed> {
  return {
    customers: generateCustomers(DEMO_BLUEPRINTS, { as_of: AS_OF }),
    capital: { ...DEMO_CAPITAL },
    transactions: DEMO_TRANSACTIONS.map((t) => ({ ...t })),
    requests: DEMO_REQUESTS.map((r) => ({ ...r })),
  };
}

export class MockAdapter implements DataAdapter {
  private customers: Customer[];
  private capital: PlatformCapital;
  private transactions: CardTransaction[];
  private requests: BridgeRequest[];
  private decisions: Decision[] = [];
  private offers = new Map<string, BridgeOffer>();
  private acceptances = new Map<string, Acceptance>();
  private approvals = new Map<string, Approval>();
  private bridges = new Map<string, Bridge>();
  private timeline: TimelineEvent[] = [];

  constructor(seed: MockAdapterSeed = {}) {
    const base = demoSeed();
    this.customers = seed.customers ?? base.customers;
    this.capital = seed.capital ?? base.capital;
    this.transactions = seed.transactions ?? base.transactions;
    this.requests = seed.requests ?? base.requests;
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

  async listDeclinedTransactions(): Promise<CardTransaction[]> {
    return this.transactions.filter((t) => t.status === 'DECLINED');
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
}
