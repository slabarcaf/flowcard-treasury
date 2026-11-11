/**
 * Loads everything both surfaces need, through a DataAdapter and nothing else.
 */
import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  Bill,
  Bridge,
  BridgeRequest,
  CardTransaction,
  CollectionAction,
  Customer,
  Deposit,
  IssuedCard,
  MonthlyCashFlow,
  PlatformCapital,
  Receipt,
  TimelineEvent,
} from '../types.js';
import type { DataAdapter } from '../data/adapter.js';
import { MockAdapter } from '../data/MockAdapter.js';

export interface BridgeData {
  customers: Customer[];
  capital: PlatformCapital;
  transactions: CardTransaction[];
  deposits: Deposit[];
  requests: BridgeRequest[];
  bridges: Bridge[];
  timeline: TimelineEvent[];
  bills: Bill[];
  cards: IssuedCard[];
  receipts: Receipt[];
  collectionActions: CollectionAction[];
  /** Current wallet balance, by customer id. */
  wallets: Record<string, number>;
  /** Monthly in/out/net, by customer id. */
  cashFlows: Record<string, MonthlyCashFlow[]>;
}

export interface BridgeDataHandle {
  client: DataAdapter;
  data: BridgeData | null;
  reload: () => Promise<void>;
}

export function useBridgeData(adapter?: DataAdapter): BridgeDataHandle {
  const client = useMemo<DataAdapter>(() => adapter ?? new MockAdapter(), [adapter]);
  const [data, setData] = useState<BridgeData | null>(null);

  const reload = useCallback(async () => {
    const [customers, capital, transactions, deposits, requests, bridges, timeline, bills, cards, receipts, collectionActions] =
      await Promise.all([
        client.listCustomers(),
        client.getCapital(),
        client.listTransactions(),
        client.listDeposits(),
        client.listRequests(),
        client.listBridges(),
        client.listTimeline(),
        client.listBills(),
        client.listCards(),
        client.listReceipts(),
        client.listCollectionActions(),
      ]);
    const perCustomer = await Promise.all(
      customers.map(async (c) => [c.id, await client.getWalletBalance(c.id), await client.getCashFlow(c.id)] as const),
    );
    const wallets = Object.fromEntries(perCustomer.map(([id, balance]) => [id, balance]));
    const cashFlows = Object.fromEntries(perCustomer.map(([id, , flows]) => [id, flows]));
    setData({
      customers,
      capital,
      transactions,
      deposits,
      requests,
      bridges,
      timeline,
      bills,
      cards,
      receipts,
      collectionActions,
      wallets,
      cashFlows,
    });
  }, [client]);

  useEffect(() => {
    // Loading through the adapter is exactly the external system an effect is
    // for; reload is async, so no state is set during this render.
    // oxlint-disable-next-line react/set-state-in-effect
    void reload();
  }, [reload]);

  return { client, data, reload };
}
