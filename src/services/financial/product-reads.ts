import { FinancialReadError } from './client.js';
import type {
  Portfolio,
  WalletBalance,
  CustomerPage,
  CustomerDetail,
  ProductCustomer,
  BridgePage,
} from './product-models.js';
type Obj = Record<string, unknown>;
function record(value: unknown): Obj {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new FinancialReadError(502);
  return value as Obj;
}
function need(value: boolean): void {
  if (!value) throw new FinancialReadError(502);
}
const str = (v: unknown): v is string => typeof v === 'string' && v.length > 0;
const nullableString = (v: unknown) => v === null || str(v);
const money = (v: unknown) => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const nullableMoney = (v: unknown) => v === null || money(v);
function source(v: Obj) {
  need(
    (v.source_mode === 'DEMO' || v.source_mode === 'SANDBOX') &&
      str(v.source_binding_id) &&
      str(v.facility_id) &&
      str(v.as_of) &&
      Number.isFinite(Date.parse(v.as_of))
  );
}
function customer(value: unknown): ProductCustomer {
  const v = record(value);
  need(
    str(v.customer_id) &&
      nullableString(v.display_name) &&
      typeof v.mapping_active === 'boolean' &&
      typeof v.accounting_available === 'boolean' &&
      nullableString(v.active_bridge_id) &&
      nullableString(v.captured_at)
  );
  for (const k of [
    'principal_owed_cents',
    'unpaid_fee_cents',
    'reserved_principal_cents',
    'reserved_fee_cents',
    'version',
  ])
    need(nullableMoney(v[k]));
  for (const k of ['history_complete', 'active_hold'])
    need(v[k] === null || typeof v[k] === 'boolean');
  if (!v.accounting_available)
    need(
      [
        'principal_owed_cents',
        'unpaid_fee_cents',
        'reserved_principal_cents',
        'reserved_fee_cents',
      ].every((k) => v[k] === null)
    );
  return v as unknown as ProductCustomer;
}
function cursor(v: Obj) {
  need(v.next_cursor === null || str(v.next_cursor));
  need(Array.isArray(v.items) && v.items.length <= 25);
}
export class ProductReadClient {
  private readonly fetchImpl: typeof fetch;
  constructor(fetchImpl: typeof fetch = globalThis.fetch.bind(globalThis)) {
    this.fetchImpl = fetchImpl;
  }
  private async read(path: string): Promise<Obj> {
    let r: Response;
    try {
      r = await this.fetchImpl(path, {
        credentials: 'include',
        cache: 'no-store',
        signal: AbortSignal.timeout(15000),
        headers: { accept: 'application/json' },
      });
    } catch {
      throw new FinancialReadError(503);
    }
    if (!r.ok) throw new FinancialReadError(r.status);
    try {
      const raw = await r.text();
      need(raw.length <= 2 * 1024 * 1024);
      const v = record(JSON.parse(raw));
      source(v);
      return v;
    } catch {
      throw new FinancialReadError(502);
    }
  }
  async wallet(): Promise<WalletBalance> {
    const v = await this.read('/api/customer/wallet');
    need(
      str(v.customer_id) &&
        v.currency === 'USD' &&
        money(v.available_balance_cents) &&
        str(v.provider_mode) &&
        str(v.requested_at) &&
        str(v.observed_at) &&
        Number.isFinite(Date.parse(v.observed_at))
    );
    return v as unknown as WalletBalance;
  }
  async portfolio(): Promise<Portfolio> {
    const v = await this.read('/api/operator/portfolio');
    for (const k of [
      'facility_version',
      'facility_total_cents',
      'reserve_bps',
      'funded_principal_cents',
      'reserved_principal_cents',
      'other_commitments_cents',
      'principal_owed_cents',
      'unpaid_fee_cents',
      'held_bridge_count',
    ])
      need(money(v[k]));
    for (const k of ['reserve_floor_cents', 'available_capital_cents', 'fees_collected_cents'])
      need(nullableMoney(v[k]));
    need(
      nullableString(v.current_regime) &&
        typeof v.history_complete === 'boolean' &&
        str(v.policy_id) &&
        str(v.policy_hash)
    );
    for (const n of Object.values(record(v.lifecycle_counts))) need(money(n));
    return v as unknown as Portfolio;
  }
  async customers(after?: string): Promise<CustomerPage> {
    const v = await this.read(
      '/api/operator/customers' + (after ? '?' + new URLSearchParams({ cursor: after }) : '')
    );
    cursor(v);
    for (const row of v.items as unknown[]) customer(row);
    return v as unknown as CustomerPage;
  }
  async customer(id: string): Promise<CustomerDetail> {
    const v = await this.read('/api/operator/customers/' + encodeURIComponent(id));
    need(customer(v.customer).customer_id === id);
    return v as unknown as CustomerDetail;
  }
  async bridges(customerID?: string, after?: string): Promise<BridgePage> {
    const path = customerID
      ? '/api/operator/customers/' + encodeURIComponent(customerID) + '/bridges'
      : '/api/customer/bridges';
    const v = await this.read(path + (after ? '?' + new URLSearchParams({ cursor: after }) : ''));
    cursor(v);
    for (const value of v.items as unknown[]) {
      const b = record(value);
      for (const k of [
        'id',
        'customer_id',
        'offer_id',
        'evaluation_id',
        'lifecycle',
        'created_at',
        'due_on',
      ])
        need(str(b[k]));
      for (const k of [
        'version',
        'principal_owed_cents',
        'contract_fee_owed_cents',
        'late_fee_owed_cents',
        'principal_original_cents',
        'contract_fee_original_cents',
        'term_days',
      ])
        need(money(b[k]));
      need(
        nullableString(b.originating_case_id) &&
          nullableString(b.funded_at) &&
          Array.isArray(b.hold_reasons) &&
          b.hold_reasons.every(str)
      );
      if (customerID) need(b.customer_id === customerID);
    }
    return v as unknown as BridgePage;
  }
}
