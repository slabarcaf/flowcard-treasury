import { describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { FinancialReadClient, cents } from '../../src/services/financial/client.js';
import type { Bridge, CaseDetail, Changes, Offer, ReadScope } from '../../src/services/financial/client.js';
import { FinancialSnapshot } from '../../src/features/bridge-capital/backoffice/screens/FinancialRecordsScreen.js';

const caseID = '11111111-1111-4111-8111-111111111111';
const offerID = '22222222-2222-4222-8222-222222222222';
const bridgeID = '33333333-3333-4333-8333-333333333333';
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const offer: Offer = { id: offerID, version: 8, offer_revision: 1, terms_hash: 'saved-hash', state: 'TERMINATED', principal_cents: 700000, contract_fee_cents: 19000, total_due_cents: 719000, term_days: 30, offered_on: '2026-11-15', due_on: '2026-12-15', expires_at: '2026-11-15T21:00:00Z', disclosures_version: 'v1', late_fee_convention: 'saved-convention', consent_label: 'SYNTHETIC_DEMO_DECISION', originating_case_id: caseID, evaluation_id: 'evaluation-saved' };
const caseRecord: CaseDetail = {
  case_id: caseID, mode: 'DEMO', kind: 'SHORTFALL', status: 'COMPLETED', stage: 'COMPLETE', customer_id: 'customer-a', account_ref: 'customer:customer-a', detection_id: 'decline-a', bridge_id: null, bridge_version: null, refresh_needed: false, updated_at: '2026-11-15T20:00:00Z', next_actions: ['PROVIDE_VALIDATED_EVIDENCE'],
  assessment: { id: 'evaluation-saved', customer_id: 'customer-a', account_ref: 'customer:customer-a', shortfall_id: 'decline-a', source_mode: 'DEMO', status: 'REVIEW_REQUIRED', evaluated_at: '2026-11-15T20:00:00Z', policy_id: 'policy-a', policy_hash: 'saved-policy-hash', term_days: 30, customer_snapshot_id: null, customer_snapshot_version: null, facility_snapshot_id: 'snapshot-a', facility_snapshot_version: 9, funding_authorized: false, requires_recheck: true, approval_required: false, principal_cents: 700000, estimated_fee_cents: 19000, total_due_cents: 719000, customer_limit_cents: null, existing_exposure_cents: null, post_funding_exposure_cents: null, available_capital_cents: null, rules: [{ rule_id: 'MISSING_FACT', status: 'UNKNOWN', reason_code: 'MISSING_FACT_UNKNOWN', actual: null, threshold: { amount_cents: 100 } }], reason_codes: ['MISSING_FACT_UNKNOWN'] },
};
const bridge: Bridge = {
  id: bridgeID, mode: 'DEMO', source_binding_id: 'saved-binding', customer_id: 'customer-a', account_ref: 'customer:customer-a', version: 12, as_of: '2026-11-15T20:00:00Z', currency: 'USD', lifecycle: 'ACTIVE', delinquency: 'CURRENT', originating_case_id: caseID, evaluation_id: 'evaluation-saved', offer_id: offerID, terms_hash: 'saved-hash', principal_owed_cents: null, contract_fee_owed_cents: null, late_fee_owed_cents: null, due_on: '2026-12-15', days_past_due: 0, debt_as_of: null, accrual_current: false, accrual_current_through: null, acceptance_status: 'VALID', approval_status: 'NOT_REQUIRED', collection_authority_status: 'MISSING', hold_reasons: [], allowed_next_actions: ['WAIT_FOR_ACCRUAL'], operations: [],
};
const changes: Changes = { mode: 'DEMO', items: [{ sequence: 4, resource_type: 'OFFER', resource_id: offerID, resource_version: 8, change_type: 'OFFER_TERMINATED', recorded_at: '2026-11-15T20:00:00Z', customer_id: 'customer-a', originating_case_id: caseID, evaluation_id: 'evaluation-saved', offer_id: offerID, bridge_id: null, bridge_version: null }], next_after_seq: 4, has_more: true };

describe('read-only Go financial contracts', () => {
  it('reads saved cases and immutable offer terms using cookies and GET only', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(response({ items: [], next_cursor: 'opaque+/=' })).mockResolvedValueOnce(response(caseRecord)).mockResolvedValueOnce(response(offer));
    const api = new FinancialReadClient(fetcher);
    expect((await api.cases()).next_cursor).toBe('opaque+/=');
    expect((await api.case(caseID)).assessment?.total_due_cents).toBe(719000);
    expect(await api.offer(offerID)).toMatchObject({ version: 8, offer_revision: 1, terms_hash: 'saved-hash' });
    for (const [, init] of fetcher.mock.calls) {
      expect(init?.method).toBe('GET'); expect(init?.credentials).toBe('include'); expect(init?.cache).toBe('no-store');
      expect(init?.body).toBeUndefined(); expect(init?.headers).not.toHaveProperty('Authorization');
    }
  });
  it('uses returned sequences and opaque cursors without turning resource versions into bridge versions', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(response(changes)).mockResolvedValueOnce(response({ items: [], next_cursor: null }));
    const api = new FinancialReadClient(fetcher);
    expect((await api.changes(3)).items[0]).toMatchObject({ resource_version: 8, bridge_version: null });
    await api.cases('opaque+/=');
    expect(fetcher.mock.calls[0][0]).toBe('/api/servicing/changes?after_seq=3');
    expect(fetcher.mock.calls[1][0]).toBe('/api/coordinator/cases?cursor=opaque%2B%2F%3D');
  });
  it('rejects unsafe integers, fractional cents and incorrectly typed cents before rendering', async () => {
    for (const principal_cents of [Number.MAX_SAFE_INTEGER + 1, 10.25, '700000']) {
      await expect(new FinancialReadClient(vi.fn<typeof fetch>().mockResolvedValue(response({ ...offer, principal_cents }))).offer(offerID)).rejects.toMatchObject({ status: 502 });
    }
    expect(cents(Number.MAX_SAFE_INTEGER)).toBe('$90,071,992,547,409.91');
    expect(cents(-101)).toBe('-$1.01');
  });
  it('preserves missing facts, rejects mismatched scope and never treats saved eligibility as funding authority', async () => {
    for (const patch of [{ customer_id: 'other-customer' }, { source_mode: 'SANDBOX' }, { funding_authorized: true }, { shortfall_id: 'other-decline' }]) {
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ ...caseRecord, assessment: { ...caseRecord.assessment, ...patch } }));
      await expect(new FinancialReadClient(fetcher).case(caseID)).rejects.toMatchObject({ status: 502 });
    }
    const valid = await new FinancialReadClient(vi.fn<typeof fetch>().mockResolvedValue(response(caseRecord))).case(caseID);
    expect(valid.assessment?.available_capital_cents).toBeNull();
    expect(cents(valid.assessment?.available_capital_cents)).toBe('Unavailable');
    const scopes: ReadScope[] = [{ mode: 'SANDBOX' }, { customerID: 'other-customer' }, { evaluationID: 'other-evaluation' }, { originatingCaseID: bridgeID }];
    for (const scope of scopes) await expect(new FinancialReadClient(vi.fn<typeof fetch>().mockResolvedValue(response(caseRecord))).case(caseID, undefined, scope)).rejects.toMatchObject({ status: 502 });
    await expect(new FinancialReadClient(vi.fn<typeof fetch>().mockResolvedValue(response(bridge))).bridge(bridgeID, undefined, { mode: 'SANDBOX' })).rejects.toMatchObject({ status: 502 });
    await expect(new FinancialReadClient(vi.fn<typeof fetch>().mockResolvedValue(response(offer))).offer(offerID, undefined, { evaluationID: 'other-evaluation' })).rejects.toMatchObject({ status: 502 });
  });
  it('refuses mixed collection/bridge versions and preserves source cursor on matching pages', async () => {
    const cursor = 'signed.opaque+/=';
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(response(bridge)).mockResolvedValueOnce(response({ bridge_version: 13, items: [], next_cursor: null })).mockResolvedValueOnce(response({ bridge_version: 12, items: [{ id: 'attempt-a', operation_id: offerID, deposit_id: 'deposit-a', state: 'SETTLED', created_at: '2026-11-15T20:00:00Z', settled_at: null, reserved_debit_cents: 1200, settled_debit_cents: 1000, provider_fee_cents: 200, planned_allocations: [], allocations: [{ allocation_index: 0, bucket: 'CONTRACT_FEE', amount_cents: 1000 }] }], next_cursor: cursor }));
    const api = new FinancialReadClient(fetcher);
    const saved = await api.bridge(bridgeID, undefined, { mode: 'DEMO', customerID: 'customer-a', evaluationID: 'evaluation-saved', originatingCaseID: caseID });
    await expect(api.collections(bridgeID, saved.version)).rejects.toMatchObject({ status: 409 });
    const page = await api.collections(bridgeID, saved.version, cursor);
    expect(page.next_cursor).toBe(cursor);
    expect(page.items[0]).toMatchObject({ settled_debit_cents: 1000, provider_fee_cents: 200, allocations: [{ allocation_index: 0, bucket: 'CONTRACT_FEE', amount_cents: 1000 }] });
    expect(fetcher.mock.calls[2][0]).toBe(`/api/bridges/${bridgeID}/collections?cursor=signed.opaque%2B%2F%3D`);
  });
  it('keeps unavailable, missing, unauthorized and stale reads distinct without retry or fixture fallback', async () => {
    for (const status of [401, 403, 404, 409, 503]) {
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({}, status));
      await expect(new FinancialReadClient(fetcher).offer(offerID)).rejects.toMatchObject({ status });
      expect(fetcher).toHaveBeenCalledTimes(1);
    }
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(new Error('private endpoint'));
    await expect(new FinancialReadClient(fetcher).offer(offerID)).rejects.toMatchObject({ status: 503 });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it('renders saved money, snapshots and rule flags without fabricating missing amounts or action controls', () => {
    const html = renderToStaticMarkup(<FinancialSnapshot record={{ kind: 'case', value: caseRecord }} nextCollections={() => {}} busy={false} />);
    expect(html).toContain('DEMO'); expect(html).toContain('synthetic source book'); expect(html).toContain('$7,190.00');
    expect(html).toContain('snapshot-a'); expect(html).toContain('UNKNOWN'); expect(html).toContain('Unavailable');
    expect(html).not.toContain('$0.00'); expect(html).not.toContain('<button');
    const stale = renderToStaticMarkup(<FinancialSnapshot record={{ kind: 'bridge', value: bridge, collections: { bridge_version: 12, items: [], next_cursor: null } }} nextCollections={() => {}} busy={false} />);
    expect(stale).toContain('WAIT_FOR_ACCRUAL'); expect(stale).toContain('bridge version 12'); expect(stale).not.toContain('$0.00');
    expect(stale).toContain('Response as of'); expect(stale).toContain('Last provider observation');
    const terms = renderToStaticMarkup(<FinancialSnapshot record={{ kind: 'offer', value: { ...offer, total_due_cents: 719123 } }} nextCollections={() => {}} busy={false} />);
    expect(terms).toContain('$7,191.23'); // Uses supplied total, never principal + fee.
    expect(terms).toContain('Immutable offer revision'); expect(terms).toContain('Aggregate version');
  });
});
