/**
 * Portal state: which customer is signed in, what is on screen, and the terms
 * they have picked. Reads and writes go through the DataAdapter; every number
 * shown comes from the engine.
 */
import { useCallback, useState } from 'react';
import type { Acceptance, Bridge, BridgeOffer, BridgeRequest, Customer } from '../types.js';
import type { ActivityItem } from './activity.js';
import type { DataAdapter } from '../data/adapter.js';
import { AS_OF } from '../data/customers.js';
import { useBridgeData, type BridgeData } from '../ui/useBridgeData.js';
import { usd } from '../ui/format.js';

export type PortalScreen =
  | { screen: 'wallet' }
  | { screen: 'activity' }
  | { screen: 'bridges' }
  | { screen: 'account' }
  | { screen: 'transaction'; transactionId: string }
  | { screen: 'decline'; transactionId: string }
  | { screen: 'accepted'; transactionId: string }
  | { screen: 'bridge'; bridgeId: string };

/** Term offered when the customer has not picked one yet. */
export const DEFAULT_TERM_DAYS = 30;

export interface Portal {
  today: string;
  data: BridgeData | null;
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
  reload: () => Promise<void>;
  retrying: boolean;
  actionError: string | null;
}

export function usePortal(adapter?: DataAdapter): Portal {
  const { client, data, reload } = useBridgeData(adapter);
  const [customerId, setCustomerId] = useState<string | null>(null);
  const [screen, setScreen] = useState<PortalScreen>({ screen: 'wallet' });
  const [terms, setTerms] = useState<Record<string, number>>({});
  const [retrying, setRetrying] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const customer =
    data === null ? null : (data.customers.find((c) => c.id === customerId) ?? data.customers[0] ?? null);

  const selectCustomer = useCallback((id: string) => {
    setCustomerId(id);
    setScreen({ screen: 'wallet' });
  }, []);

  const termFor = useCallback(
    (transactionId: string) => terms[transactionId] ??
      Object.values(data?.offers ?? {}).find((offer) => offer.transaction_id === transactionId)?.term_days ?? DEFAULT_TERM_DAYS,
    [terms, data],
  );

  const setTerm = useCallback((transactionId: string, termDays: number) => {
    setTerms((prev) => ({ ...prev, [transactionId]: termDays }));
  }, []);

  const acceptanceFor = useCallback((offerId: string) => data?.acceptances[offerId], [data]);

  const accept = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer) => {
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
      await reload();
      setScreen({ screen: 'accepted', transactionId: request.transaction.id });
    },
    [client, reload],
  );

  const retryPayment = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer) => {
      setRetrying(true);
      setActionError(null);
      try {
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
        const existing = await client.getBridge(bridge.id);
        if (existing !== null) {
          if (existing.customer_id !== bridge.customer_id || existing.transaction_id !== bridge.transaction_id ||
            existing.principal !== bridge.principal || existing.fee !== bridge.fee || existing.term_days !== bridge.term_days ||
            existing.due_date !== bridge.due_date) {
            throw new Error('This payment was already funded on different terms.');
          }
          await reload();
          setScreen({ screen: 'bridge', bridgeId: existing.id });
          return;
        }
        const transfer = await client.disburse(bridge);
        if (transfer.status !== 'SETTLED') throw new Error('The transfer has not settled.');
        if (await client.getBridge(bridge.id) === null) {
          throw new Error('The settled bridge record is unavailable. Check its status before retrying.');
        }
        await reload();
        setScreen({ screen: 'bridge', bridgeId: bridge.id });
      } catch (error) {
        setActionError(error instanceof Error ? error.message : 'The fixture payment could not be retried.');
      } finally {
        setRetrying(false);
      }
    },
    [client, reload],
  );

  return {
    today: AS_OF,
    data,
    customer,
    selectCustomer,
    screen,
    go: setScreen,
    termFor,
    setTerm,
    acceptanceFor,
    accept,
    retryPayment,
    reload,
    retrying,
    actionError,
  };
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
