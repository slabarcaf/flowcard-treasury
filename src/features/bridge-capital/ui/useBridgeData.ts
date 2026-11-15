/**
 * Loads everything both surfaces need, through a DataAdapter and nothing else.
 */
import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  Acceptance,
  Approval,
  Bridge,
  BridgeOffer,
  BridgeRequest,
  CardTransaction,
  Customer,
  Deposit,
  PlatformCapital,
  TimelineEvent,
} from '../types.js';
import type { DataAdapter } from '../data/adapter.js';
import { MockAdapter } from '../data/MockAdapter.js';
import { buildOffer } from '../policy/pricing.js';

export interface BridgeData {
  customers: Customer[];
  capital: PlatformCapital;
  transactions: CardTransaction[];
  deposits: Deposit[];
  requests: BridgeRequest[];
  bridges: Bridge[];
  timeline: TimelineEvent[];
  offers: Record<string, BridgeOffer>;
  acceptances: Record<string, Acceptance>;
  approvals: Record<string, Approval>;
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
    const bindings = await Promise.all(requests.map(async (request) => {
      const id = buildOffer(request).id;
      const [offer, acceptance, approval] = await Promise.all([
        client.getOffer(id), client.getAcceptance(id), client.getApproval(id),
      ]);
      return { offer, acceptance, approval };
    }));
    const offers: Record<string, BridgeOffer> = {};
    const acceptances: Record<string, Acceptance> = {};
    const approvals: Record<string, Approval> = {};
    for (const binding of bindings) {
      if (binding.offer !== null) offers[binding.offer.id] = binding.offer;
      if (binding.acceptance !== null) acceptances[binding.acceptance.offer_id] = binding.acceptance;
      if (binding.approval !== null) approvals[binding.approval.offer_id] = binding.approval;
    }
    setData({ customers, capital, transactions, deposits, requests, bridges, timeline, offers, acceptances, approvals });
  }, [client]);

  useEffect(() => {
    // Loading through the adapter is exactly the external system an effect is
    // for; reload is async, so no state is set during this render.
    // oxlint-disable-next-line react/set-state-in-effect
    void reload();
  }, [reload]);

  return { client, data, reload };
}
