import { afterEach, describe, expect, it, vi } from 'vitest';
import { MockAdapter } from '../../../src/features/bridge-capital/data/MockAdapter.js';
import { AS_OF } from '../../../src/features/bridge-capital/data/customers.js';
import { buildOffer } from '../../../src/features/bridge-capital/policy/pricing.js';
import { mockAirwallexClient } from '../../../src/services/mock/AirwallexClient.js';
import type { Bridge, BridgeOffer, BridgeRequest } from '../../../src/features/bridge-capital/types.js';

async function humanRequest(adapter: MockAdapter): Promise<BridgeRequest> {
  const request = (await adapter.listRequests()).find((item) => item.principal === 7_000);
  if (request === undefined) throw new Error('Missing human-approval demo request.');
  return request;
}

function bridgeFor(request: BridgeRequest, offer: BridgeOffer): Bridge {
  return {
    id: `bridge_${request.id}`, customer_id: request.customer_id,
    transaction_id: request.transaction.id, principal: offer.principal,
    fee: offer.fee, term_days: offer.term_days, status: 'DISBURSED',
    disbursed_at: AS_OF, due_date: offer.due_date,
    principal_outstanding: offer.principal, fee_outstanding: offer.fee, accrued_fee: 0,
  };
}

async function approve(adapter: MockAdapter, offer: BridgeOffer): Promise<void> {
  await adapter.saveApproval({
    offer_id: offer.id, customer_id: offer.customer_id,
    principal: offer.principal, fee: offer.fee, term_days: offer.term_days,
    approved_by: 'fixture.credit.officer', approved_at: AS_OF,
  });
}

async function accept(adapter: MockAdapter, offer: BridgeOffer): Promise<void> {
  await adapter.saveAcceptance({
    offer_id: offer.id, principal: offer.principal, fee: offer.fee,
    term_days: offer.term_days, accepted_at: AS_OF,
  });
}

afterEach(() => vi.restoreAllMocks());

describe('fixture funding consent and approval', () => {
  it('records approval without funding and refuses absent acceptance', async () => {
    const adapter = new MockAdapter();
    const request = await humanRequest(adapter);
    const offer = buildOffer(request);
    const transfer = vi.spyOn(mockAirwallexClient, 'createTransfer');
    const before = await adapter.getCapital();
    await adapter.saveOffer(offer);
    await approve(adapter, offer);
    await expect(adapter.disburse(bridgeFor(request, offer))).rejects.toThrow('acceptance');
    expect(transfer).not.toHaveBeenCalled();
    expect((await adapter.getCapital()).outstanding).toBe(before.outstanding);
    expect(await adapter.getBridge(`bridge_${request.id}`)).toBeNull();
  });

  it('requires human approval and funds matched terms once across concurrent retries', async () => {
    const adapter = new MockAdapter();
    const request = await humanRequest(adapter);
    const offer = buildOffer(request);
    const transfer = vi.spyOn(mockAirwallexClient, 'createTransfer');
    const before = await adapter.getCapital();
    await adapter.saveOffer(offer);
    await accept(adapter, offer);
    await expect(adapter.disburse(bridgeFor(request, offer))).rejects.toThrow('approval');
    expect(transfer).not.toHaveBeenCalled();
    await approve(adapter, offer);
    const [first, replay] = await Promise.all([
      adapter.disburse(bridgeFor(request, offer)), adapter.disburse(bridgeFor(request, offer)),
    ]);
    expect(first.id).toBe(replay.id);
    expect(transfer).toHaveBeenCalledTimes(1);
    expect((await adapter.getCapital()).outstanding).toBe(before.outstanding + offer.principal);
    expect((await adapter.getBridge(`bridge_${request.id}`))?.principal).toBe(offer.principal);
    const events = (await adapter.listTimeline()).filter((item) => item.bridge_id === `bridge_${request.id}`);
    expect(events.filter((item) => item.type === 'DISBURSEMENT')).toHaveLength(1);
  });

  it('invalidates consent and approval when terms change before funding', async () => {
    const adapter = new MockAdapter();
    const request = await humanRequest(adapter);
    const offer = buildOffer(request);
    const transfer = vi.spyOn(mockAirwallexClient, 'createTransfer');
    await adapter.saveOffer(offer);
    await accept(adapter, offer);
    await approve(adapter, offer);
    const changed = buildOffer(request, request.principal, 15);
    await adapter.saveOffer(changed);
    await expect(adapter.disburse(bridgeFor(request, changed))).rejects.toThrow('acceptance');
    await accept(adapter, changed);
    await expect(adapter.disburse(bridgeFor(request, changed))).rejects.toThrow('approval');
    expect(transfer).not.toHaveBeenCalled();
  });
});
