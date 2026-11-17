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
import { evaluate } from '../policy/eligibility.js';
import { isAcceptanceValid, isApprovalValid } from '../policy/binding.js';
import { buildOffer, priceOffer } from '../policy/pricing.js';
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
  private disbursements = new Map<string, { intent: string; transfer: Promise<TransferResponse> }>();
  private bridges = new Map<string, Bridge>();
  private timeline: TimelineEvent[] = [];
  private lastActionTime = Number.NEGATIVE_INFINITY;

  constructor(seed: MockAdapterSeed = {}) {
    const base = demoSeed();
    this.customers = seed.customers ?? base.customers;
    this.capital = seed.capital ?? base.capital;
    this.transactions = seed.transactions ?? base.transactions;
    this.deposits = seed.deposits ?? base.deposits;
    this.requests = seed.requests ?? base.requests;
    for (const b of seed.bridges ?? base.bridges) this.bridges.set(b.id, b);
    this.timeline = [...(seed.timeline ?? base.timeline)];
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
    const prior = this.offers.get(offer.id);
    if (prior !== undefined && (
      prior.customer_id !== offer.customer_id || prior.transaction_id !== offer.transaction_id ||
      prior.principal !== offer.principal || prior.fee !== offer.fee ||
      prior.term_days !== offer.term_days || prior.due_date !== offer.due_date
    )) {
      this.acceptances.delete(offer.id);
      this.approvals.delete(offer.id);
    }
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
    // These are fixture presentation guards. Real money remains server-owned.
    const key = JSON.stringify([bridge.customer_id, bridge.transaction_id]);
    const intent = JSON.stringify([bridge.id, bridge.principal, bridge.fee, bridge.term_days, bridge.due_date]);
    const prior = this.disbursements.get(key);
    if (prior !== undefined) {
      if (prior.intent !== intent) throw new Error('This payment was already funded on different terms.');
      return prior.transfer;
    }
    const request = this.requests.find((item) => item.transaction.id === bridge.transaction_id && item.customer_id === bridge.customer_id);
    const customer = this.customers.find((item) => item.id === bridge.customer_id);
    if (request === undefined || customer === undefined || bridge.principal !== request.principal || bridge.principal <= 0) {
      throw new Error('A bridge must cover the complete requested shortfall.');
    }
    const offer = this.offers.get(buildOffer(request).id);
    if (offer === undefined || offer.customer_id !== bridge.customer_id || offer.transaction_id !== bridge.transaction_id ||
      offer.principal !== bridge.principal || offer.fee !== bridge.fee || offer.term_days !== bridge.term_days ||
      offer.due_date !== bridge.due_date || priceOffer(bridge.principal, bridge.term_days).fee !== bridge.fee) {
      throw new Error('The offered terms changed. Review and accept the current offer.');
    }
    const acceptance = this.acceptances.get(offer.id);
    if (acceptance === undefined || !isAcceptanceValid(acceptance, offer)) {
      throw new Error('Customer acceptance is required on these exact terms.');
    }
    const decision = evaluate({ ...request, term_days: bridge.term_days }, customer, this.capital, { today: AS_OF });
    if (!decision.eligible) throw new Error('This payment is no longer eligible for a bridge.');
    const approval = this.approvals.get(offer.id);
    if (decision.needs_human_approval && (approval === undefined || !isApprovalValid(approval, offer))) {
      throw new Error('Credit officer approval is required on these exact terms.');
    }
    const fundedBridge = { ...bridge };
    const transfer = mockAirwallexClient.createTransfer({
      destination: bridge.customer_id, amount: bridge.principal, currency: 'USD',
      reason: 'Bridge Capital disbursement', reference: bridge.id,
      requestId: `fixture_${bridge.id}`,
    }).then(async (result) => {
      if (result.status !== 'SETTLED') throw new Error('The transfer has not settled.');
      this.capital = { ...this.capital, outstanding: this.capital.outstanding + fundedBridge.principal };
      this.bridges.set(fundedBridge.id, fundedBridge);
      await this.appendTimeline({
        at: `${AS_OF}T09:22:00Z`, customer_id: fundedBridge.customer_id,
        bridge_id: fundedBridge.id, type: 'DISBURSEMENT',
        text: `${fundedBridge.principal.toLocaleString('en-US', { style: 'currency', currency: 'USD' })} added to your wallet.`,
      });
      await this.appendTimeline({
        at: `${AS_OF}T09:23:00Z`, customer_id: fundedBridge.customer_id,
        bridge_id: fundedBridge.id, type: 'NOTICE',
        text: `Card payment to ${request.transaction.merchant_name} retried and approved.`,
      });
      return result;
    });
    this.disbursements.set(key, { intent, transfer });
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
    const proposed = Date.parse(event.at);
    if (!Number.isFinite(proposed)) throw new Error('Invalid fixture event timestamp.');
    // Role views reuse story timestamps; preserve the order of new user actions.
    this.lastActionTime = Math.max(proposed, this.lastActionTime + 1);
    const stored: TimelineEvent = { ...event, at: new Date(this.lastActionTime).toISOString(), id: `evt_${this.timeline.length + 1}` };
    this.timeline.push(stored);
    return stored;
  }

  async listTimeline(customerId?: string): Promise<TimelineEvent[]> {
    return customerId === undefined
      ? this.timeline
      : this.timeline.filter((e) => e.customer_id === customerId);
  }
}
