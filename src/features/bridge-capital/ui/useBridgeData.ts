/**
 * Loads everything both surfaces need, through a DataAdapter and nothing else.
 */
import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  Bridge,
  BridgeRequest,
  CardTransaction,
  Customer,
  Deposit,
  PlatformCapital,
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
    const [customers, capital, transactions, deposits, requests, bridges, timeline] = await Promise.all([
      client.listCustomers(),
      client.getCapital(),
      client.listTransactions(),
      client.listDeposits(),
      client.listRequests(),
      client.listBridges(),
      client.listTimeline(),
    ]);
    setData({ customers, capital, transactions, deposits, requests, bridges, timeline });
  }, [client]);

  useEffect(() => {
    // Loading through the adapter is exactly the external system an effect is
    // for; reload is async, so no state is set during this render.
    // oxlint-disable-next-line react/set-state-in-effect
    void reload();
  }, [reload]);

  return { client, data, reload };
}
