/**
 * All back-office state: what is on screen, and the data behind it.
 *
 * Every read and write goes through a DataAdapter, so the screens never touch
 * a data source directly. The policy engine is pure and is called from the
 * screens themselves.
 */
import { useCallback, useMemo, useState } from 'react';
import type { Bridge, BridgeOffer, BridgeRequest, CollectionActionType, Customer } from '../types.js';
import type { DataAdapter } from '../data/adapter.js';
import { AS_OF } from '../data/customers.js';
import { withLiveBridges } from '../data/live.js';
import { useBridgeData, type BridgeData } from '../ui/useBridgeData.js';
import { buildOffer } from '../policy/pricing.js';

export type View =
  | { screen: 'overview' }
  | { screen: 'customers' }
  | { screen: 'rules' }
  | { screen: 'admin' }
  | { screen: 'request'; requestId: string }
  | { screen: 'bridge'; bridgeId: string }
  | { screen: 'customer'; customerId: string }
  | { screen: 'collections'; customerId?: string }
  | { screen: 'alerts' };

export type RequestOutcome = 'APPROVED' | 'DECLINED' | 'ESCALATED';

export type BackOfficeData = BridgeData;

export interface BackOffice {
  today: string;
  data: BackOfficeData | null;
  view: View;
  go: (view: View) => void;
  /** Requests a person has already acted on in this session. */
  outcomes: Record<string, RequestOutcome>;
  approve: (request: BridgeRequest, offer: BridgeOffer) => Promise<void>;
  decline: (request: BridgeRequest, reason: string) => Promise<void>;
  escalate: (request: BridgeRequest) => Promise<void>;
  customerById: (id: string) => Customer | undefined;
  bridgesFor: (customerId: string) => Bridge[];
  /** Customers with bridges disbursed or repaid this session folded in (data/live.ts). */
  liveCustomers: Customer[];
  /** Record what ops did about a past-due bridge (spec §5 ladder actions, taken by a person). */
  act: (bridge: Bridge, type: CollectionActionType, promisedDate?: string) => Promise<void>;
  notice: string | null;
}

const ACTION_TEXT: Record<CollectionActionType, string> = {
  REMIND: 'Reminder sent by email and SMS.',
  RESTRICT_CARDS: 'Cards restricted to critical spend by ops.',
  FREEZE_CARDS: 'Cards frozen by ops.',
  ESCALATE: 'Escalated to a collector.',
  PROMISE_TO_PAY: 'Promise to pay recorded.',
};

export function useBackOffice(adapter?: DataAdapter): BackOffice {
  const { client, data, reload } = useBridgeData(adapter);
  const [view, setView] = useState<View>({ screen: 'overview' });
  const [outcomes, setOutcomes] = useState<Record<string, RequestOutcome>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const liveCustomers = useMemo(
    () => (data === null ? [] : data.customers.map((c) => withLiveBridges(c, data.bridges, AS_OF))),
    [data],
  );

  const approve = useCallback(
    async (request: BridgeRequest, offer: BridgeOffer) => {
      await client.saveOffer(offer);
      await client.saveApproval({
        offer_id: offer.id,
        customer_id: offer.customer_id,
        principal: offer.principal,
        fee: offer.fee,
        term_days: offer.term_days,
        approved_by: 'credit.officer',
        approved_at: AS_OF,
      });

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

      // Flex on a bill (spec §8.3): the funds pay the supplier straight away.
      if (request.purpose === 'BILL' && request.bill_id !== undefined) {
        const bill = (await client.listBills()).find((b) => b.id === request.bill_id);
        if (bill !== undefined) {
          await client.chargeWallet(bill.customer_id, bill.amount, bill.id);
          await client.saveBill({ ...bill, status: 'FLEXED', paid_at: AS_OF, bridge_id: bridge.id });
        }
      }

      await client.appendTimeline({
        at: `${AS_OF}T09:30:00Z`,
        customer_id: request.customer_id,
        bridge_id: bridge.id,
        type: 'APPROVAL',
        text: `Credit officer approved ${offer.principal} over ${offer.term_days} days, fee ${offer.fee}.`,
      });
      await client.appendTimeline({
        at: `${AS_OF}T09:31:00Z`,
        customer_id: request.customer_id,
        bridge_id: bridge.id,
        type: 'DISBURSEMENT',
        text: 'Transfer to connected account SETTLED.',
      });

      setOutcomes((prev) => ({ ...prev, [request.id]: 'APPROVED' }));
      await reload();
    },
    [client, reload],
  );

  const decline = useCallback(
    async (request: BridgeRequest, reason: string) => {
      await client.appendTimeline({
        at: `${AS_OF}T09:30:00Z`,
        customer_id: request.customer_id,
        type: 'NOTICE',
        text: `Credit officer declined the bridge request. ${reason}`,
      });
      setOutcomes((prev) => ({ ...prev, [request.id]: 'DECLINED' }));
      await reload();
    },
    [client, reload],
  );

  const escalate = useCallback(
    async (request: BridgeRequest) => {
      await client.appendTimeline({
        at: `${AS_OF}T09:30:00Z`,
        customer_id: request.customer_id,
        type: 'NOTICE',
        text: 'Escalated for a second opinion before any capital moves.',
      });
      setOutcomes((prev) => ({ ...prev, [request.id]: 'ESCALATED' }));
      await reload();
    },
    [client, reload],
  );

  const act = useCallback(
    async (bridge: Bridge, type: CollectionActionType, promisedDate?: string) => {
      await client.saveCollectionAction({
        customer_id: bridge.customer_id,
        bridge_id: bridge.id,
        type,
        at: `${AS_OF}T11:00:00Z`,
        by: 'credit.officer',
        ...(promisedDate !== undefined ? { promised_date: promisedDate } : {}),
      });
      const text = type === 'PROMISE_TO_PAY' && promisedDate !== undefined ? `Promise to pay recorded for ${promisedDate}.` : ACTION_TEXT[type];
      await client.appendTimeline({
        at: `${AS_OF}T11:00:00Z`,
        customer_id: bridge.customer_id,
        bridge_id: bridge.id,
        type: type === 'REMIND' || type === 'PROMISE_TO_PAY' ? 'NOTICE' : 'STATUS_CHANGE',
        text,
      });
      setNotice(text);
      await reload();
    },
    [client, reload],
  );

  const customerById = useCallback(
    (id: string) => data?.customers.find((c) => c.id === id),
    [data],
  );

  const bridgesFor = useCallback(
    (customerId: string) => (data?.bridges ?? []).filter((b) => b.customer_id === customerId),
    [data],
  );

  return {
    today: AS_OF,
    data,
    view,
    go: (next: View) => {
      setView(next);
      setNotice(null);
    },
    outcomes,
    approve,
    decline,
    escalate,
    customerById,
    bridgesFor,
    liveCustomers,
    act,
    notice,
  };
}

export { buildOffer };
