/**
 * Portal state: which customer is signed in, what is on screen, and the terms
 * they have picked. Reads and writes go through the DataAdapter; every number
 * shown comes from the engine.
 */
import { useCallback, useMemo, useState } from 'react';
import type {
  Acceptance,
  Bill,
  Bridge,
  BridgeOffer,
  BridgePurpose,
  BridgeRequest,
  CardTransaction,
  Customer,
  IssuedCard,
} from '../types.js';
import type { ActivityItem } from './activity.js';
import type { DataAdapter } from '../data/adapter.js';
import { AS_OF } from '../data/customers.js';
import { withLiveBridges } from '../data/live.js';
import { useBridgeData, type BridgeData } from '../ui/useBridgeData.js';
import { usd } from '../ui/format.js';
import { applyRepayment } from '../policy/repayment.js';
import { criticalityForCategory } from '../policy/criticality.js';
import { cardAccess, type CardAccess } from '../policy/cardAccess.js';
import { computeTier } from '../policy/tiers.js';
import { round2 } from '../policy/util.js';

export type PortalScreen =
  | { screen: 'wallet' }
  | { screen: 'cashflow' }
  | { screen: 'bridges' }
  | { screen: 'bills' }
  | { screen: 'receivables' }
  | { screen: 'cards' }
  | { screen: 'spend' }
  | { screen: 'statements' }
  | { screen: 'activity' }
  | { screen: 'account' }
  | { screen: 'transaction'; transactionId: string }
  | { screen: 'decline'; transactionId: string }
  | { screen: 'accepted'; transactionId: string }
  | { screen: 'bridge'; bridgeId: string }
  | { screen: 'pastBridge'; pastBridgeId: string }
  | { screen: 'offer'; requestId: string };

/** Term offered when the customer has not picked one yet. */
export const DEFAULT_TERM_DAYS = 30;

export interface PlanInput {
  amount: number;
  termDays: number;
  /** Merchant category of what the money is for; sets criticality like a card payment. */
  category: string;
}

export interface Portal {
  today: string;
  data: BridgeData | null;
  /** The signed-in customer, with bridges disbursed or repaid this session folded in. */
  customer: Customer | null;
  selectCustomer: (customerId: string) => void;
  screen: PortalScreen;
  go: (screen: PortalScreen) => void;
  /** Chosen term per transaction. */
  termFor: (transactionId: string) => number;
  setTerm: (transactionId: string, termDays: number) => void;
  acceptanceFor: (offerId: string) => Acceptance | undefined;
  accept: (request: BridgeRequest, offer: BridgeOffer) => Promise<void>;
  retryPayment: (request: BridgeRequest, offer: BridgeOffer) => Promise<void>;

  // ---- spec §8 ----
  wallet: number;
  myBridges: Bridge[];
  access: CardAccess;
  /** Toast-style confirmation of the last action. */
  notice: string | null;
  clearNotice: () => void;
  repay: (bridge: Bridge, amount: number) => Promise<void>;
  /** Raise a planned bridge (from the forecast) or Flex on a bill, then open its offer. */
  raiseRequest: (purpose: Exclude<BridgePurpose, 'CARD_DECLINE'>, input: PlanInput, bill?: Bill) => Promise<void>;
  /** Accept a planned or Flex offer. Funds move now unless the decision needs a person. */
  acceptAndFund: (request: BridgeRequest, offer: BridgeOffer, needsApproval: boolean) => Promise<void>;
  payBill: (bill: Bill) => Promise<void>;
  saveCard: (card: IssuedCard, message: string) => Promise<void>;
  createVirtualCard: (vendor: string, cap: number) => Promise<void>;
  cancelCard: (card: IssuedCard) => Promise<void>;
  attachReceipt: (transaction: CardTransaction, fileName: string) => Promise<void>;
}

const CLOSED: Bridge['status'][] = ['REPAID', 'WITHDRAWN', 'DECLINED'];

export function usePortal(adapter?: DataAdapter): Portal {
  const { client, data, reload } = useBridgeData(adapter);
  const [customerId, setCustomerId] = useState<string | null>(null);
  const [screen, setScreen] = useState<PortalScreen>({ screen: 'wallet' });
  const [terms, setTerms] = useState<Record<string, number>>({});
  const [acceptances, setAcceptances] = useState<Record<string, Acceptance>>({});
  const [notice, setNotice] = useState<string | null>(null);

  const raw = data === null ? null : (data.customers.find((c) => c.id === customerId) ?? data.customers[0] ?? null);
  const customer = useMemo(
    () => (raw === null || data === null ? null : withLiveBridges(raw, data.bridges, AS_OF)),
    [raw, data],
  );
  const myBridges = useMemo(
    () => (data === null || customer === null ? [] : data.bridges.filter((b) => b.customer_id === customer.id)),
    [data, customer],
  );
  const access = useMemo<CardAccess>(
    () =>
      data === null || customer === null
        ? { state: 'NORMAL', source: 'NONE', cap: null }
        : cardAccess(
            computeTier(customer),
            myBridges,
            data.collectionActions.filter((a) => a.customer_id === customer.id),
            AS_OF,
          ),
    [data, customer, myBridges],
  );

  const selectCustomer = useCallback((id: string) => {
    setCustomerId(id);
    setScreen({ screen: 'wallet' });
    setNotice(null);
  }, []);

  const termFor = useCallback((transactionId: string) => terms[transactionId] ?? DEFAULT_TERM_DAYS, [terms]);

  const setTerm = useCallback((transactionId: string, termDays: number) => {
    setTerms((prev) => ({ ...prev, [transactionId]: termDays }));
  }, []);

  const acceptanceFor = useCallback((offerId: string) => acceptances[offerId], [acceptances]);

  const recordAcceptance = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer): Promise<Acceptance> => {
      await client.saveOffer(offer);
      const acceptance: Acceptance = {
        offer_id: offer.id,
        principal: offer.principal,
        fee: offer.fee,
        term_days: offer.term_days,
        accepted_at: AS_OF,
      };
      await client.saveAcceptance(acceptance);
      await client.appendTimeline({
        at: `${AS_OF}T09:20:00Z`,
        customer_id: request.customer_id,
        type: 'OFFER',
        text: `Offered ${usd(offer.principal)} over ${offer.term_days} days, fee ${usd(offer.fee, { cents: true })}.`,
      });
      await client.appendTimeline({
        at: `${AS_OF}T09:21:00Z`,
        customer_id: request.customer_id,
        type: 'ACCEPTANCE',
        text: `Accepted ${usd(offer.principal)} / ${offer.term_days} days / ${usd(offer.fee, { cents: true })}.`,
      });
      setAcceptances((prev) => ({ ...prev, [offer.id]: acceptance }));
      return acceptance;
    },
    [client],
  );

  const accept = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer) => {
      await recordAcceptance(request, offer);
      setScreen({ screen: 'accepted', transactionId: request.transaction.id });
      await reload();
    },
    [recordAcceptance, reload],
  );

  const fund = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer): Promise<Bridge> => {
      const bridge: Bridge = {
        id: `bridge_${request.id}`,
        customer_id: request.customer_id,
        transaction_id: request.transaction.id,
        principal: offer.principal,
        fee: offer.fee,
        term_days: offer.term_days,
        status: 'DISBURSED',
        disbursed_at: AS_OF,
        due_date: offer.due_date,
        principal_outstanding: offer.principal,
        fee_outstanding: offer.fee,
        accrued_fee: 0,
      };
      await client.saveBridge(bridge);
      await client.disburse(bridge);
      await client.appendTimeline({
        at: `${AS_OF}T09:22:00Z`,
        customer_id: request.customer_id,
        bridge_id: bridge.id,
        type: 'DISBURSEMENT',
        text: `Transfer to connected account SETTLED — ${usd(offer.principal)}.`,
      });
      return bridge;
    },
    [client],
  );

  const retryPayment = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer) => {
      const bridge = await fund(request, offer);
      await client.appendTimeline({
        at: `${AS_OF}T09:23:00Z`,
        customer_id: request.customer_id,
        bridge_id: bridge.id,
        type: 'NOTICE',
        text: `Card payment to ${request.transaction.merchant_name} retried and approved.`,
      });
      await reload();
      setScreen({ screen: 'bridge', bridgeId: bridge.id });
    },
    [client, fund, reload],
  );

  const repay = useCallback(
    async (bridge: Bridge, amount: number) => {
      if (amount <= 0) return;
      const result = applyRepayment(bridge, amount, AS_OF);
      await client.chargeWallet(bridge.customer_id, result.applied, bridge.id);
      await client.saveBridge(result.bridge);
      await client.appendTimeline({
        at: `${AS_OF}T10:00:00Z`,
        customer_id: bridge.customer_id,
        bridge_id: bridge.id,
        type: 'COLLECTION',
        text: `Customer repaid ${usd(result.applied, { cents: true })} from the wallet. ${
          result.fully_repaid ? 'Repaid in full; restrictions lifted, tier recomputed.' : `${usd(result.remaining, { cents: true })} left.`
        }`,
      });
      setNotice(
        result.fully_repaid
          ? 'Bridge repaid in full. Your cards work normally again.'
          : `Paid ${usd(result.applied, { cents: true })}. ${usd(result.remaining, { cents: true })} left, collected from your next deposits.`,
      );
      await reload();
      if (result.fully_repaid) setScreen({ screen: 'bridges' });
    },
    [client, reload],
  );

  const raiseRequest = useCallback(
    async (purpose: Exclude<BridgePurpose, 'CARD_DECLINE'>, input: PlanInput, bill?: Bill) => {
      if (customer === null) return;
      const n = Date.now().toString(36);
      const transaction: CardTransaction = {
        id: `txn_${purpose.toLowerCase()}_${n}`,
        customer_id: customer.id,
        card_id: 'none',
        amount: input.amount,
        currency: 'USD',
        merchant_name: bill?.vendor ?? 'Planned bridge',
        merchant_category: input.category,
        criticality: criticalityForCategory(input.category),
        status: 'APPROVED',
        wallet_balance: data?.wallets[customer.id] ?? 0,
        created_at: `${AS_OF}T09:00:00Z`,
      };
      const request: BridgeRequest = {
        id: `req_${purpose.toLowerCase()}_${n}`,
        customer_id: customer.id,
        transaction,
        principal: round2(input.amount),
        term_days: input.termDays,
        requested_at: AS_OF,
        purpose,
        ...(bill !== undefined ? { bill_id: bill.id } : {}),
      };
      await client.saveRequest(request);
      await reload();
      setScreen({ screen: 'offer', requestId: request.id });
    },
    [client, customer, data, reload],
  );

  const settleBill = useCallback(
    async (billId: string, bridgeId: string) => {
      const bill = (await client.listBills()).find((b) => b.id === billId);
      if (bill === undefined) return;
      await client.chargeWallet(bill.customer_id, bill.amount, bill.id);
      await client.saveBill({ ...bill, status: 'FLEXED', paid_at: AS_OF, bridge_id: bridgeId });
    },
    [client],
  );

  const acceptAndFund = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer, needsApproval: boolean) => {
      await recordAcceptance(request, offer);
      if (needsApproval) {
        await client.appendTimeline({
          at: `${AS_OF}T09:22:00Z`,
          customer_id: request.customer_id,
          type: 'NOTICE',
          text: `Sent for human approval at ${usd(offer.principal)} / ${offer.term_days} days / ${usd(offer.fee, { cents: true })}.`,
        });
        setNotice('Accepted. Our credit team reviews this one before the money moves; you will hear back today.');
        await reload();
        setScreen({ screen: request.purpose === 'BILL' ? 'bills' : 'bridges' });
        return;
      }
      const bridge = await fund(request, offer);
      if (request.purpose === 'BILL' && request.bill_id !== undefined) {
        await settleBill(request.bill_id, bridge.id);
        setNotice(`${request.transaction.merchant_name} is paid. You repay us by the date on your bridge.`);
      } else {
        setNotice(`${usd(offer.principal)} is in your wallet.`);
      }
      await reload();
      setScreen({ screen: 'bridge', bridgeId: bridge.id });
    },
    [client, fund, recordAcceptance, reload, settleBill],
  );

  const payBill = useCallback(
    async (bill: Bill) => {
      await client.chargeWallet(bill.customer_id, bill.amount, bill.id);
      await client.saveBill({ ...bill, status: 'PAID', paid_at: AS_OF });
      setNotice(`Paid ${bill.vendor} ${usd(bill.amount)}.`);
      await reload();
    },
    [client, reload],
  );

  const saveCard = useCallback(
    async (card: IssuedCard, message: string) => {
      await client.saveCard(card);
      setNotice(message);
      await reload();
    },
    [client, reload],
  );

  const createVirtualCard = useCallback(
    async (vendor: string, cap: number) => {
      if (customer === null) return;
      const last4 = String(1000 + Math.floor(Math.random() * 9000));
      await client.saveCard({
        id: `card_${customer.id}_${Date.now().toString(36)}`,
        customer_id: customer.id,
        kind: 'VIRTUAL',
        holder: vendor,
        role: 'Vendor card',
        last4,
        monthly_cap: cap,
        spent_this_month: 0,
        blocked_categories: [],
        frozen: false,
      });
      setNotice(`Virtual card for ${vendor} created.`);
      await reload();
    },
    [client, customer, reload],
  );

  const cancelCard = useCallback(
    async (card: IssuedCard) => {
      await client.removeCard(card.id);
      setNotice(`${card.holder} card cancelled.`);
      await reload();
    },
    [client, reload],
  );

  const attachReceipt = useCallback(
    async (transaction: CardTransaction, fileName: string) => {
      await client.attachReceipt({ transaction_id: transaction.id, file_name: fileName, attached_at: AS_OF });
      setNotice(`Receipt attached to ${transaction.merchant_name}.`);
      await reload();
    },
    [client, reload],
  );

  return {
    today: AS_OF,
    data,
    customer,
    selectCustomer,
    screen,
    go: (next) => {
      setScreen(next);
      setNotice(null);
    },
    termFor,
    setTerm,
    acceptanceFor,
    accept,
    retryPayment,
    wallet: customer === null || data === null ? 0 : (data.wallets[customer.id] ?? 0),
    myBridges,
    access,
    notice,
    clearNotice: () => setNotice(null),
    repay,
    raiseRequest,
    acceptAndFund,
    payBill,
    saveCard,
    createVirtualCard,
    cancelCard,
    attachReceipt,
  };
}

/** Open bridges for a customer: not repaid, withdrawn or declined. */
export function openBridges(bridges: Bridge[]): Bridge[] {
  return bridges.filter((b) => !CLOSED.includes(b.status));
}

/** Where an activity row leads: its payment, its bridge, or nowhere. */
export function openerFor(item: ActivityItem, go: Portal['go']): (() => void) | undefined {
  if (item.transaction !== undefined) {
    const transactionId = item.transaction.id;
    return () => go({ screen: 'transaction', transactionId });
  }
  const bridgeId = item.bridge_id;
  if (bridgeId !== undefined) return () => go({ screen: 'bridge', bridgeId });
  return undefined;
}
