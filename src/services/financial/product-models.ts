import type { SourceMode } from './client.js';
export interface ProductSource {
  source_mode: SourceMode;
  source_binding_id: string;
  facility_id: string;
  as_of: string;
}
export interface Portfolio extends ProductSource {
  facility_version: number;
  facility_total_cents: number;
  reserve_bps: number;
  reserve_floor_cents: number | null;
  funded_principal_cents: number;
  reserved_principal_cents: number;
  other_commitments_cents: number;
  available_capital_cents: number | null;
  current_regime: string | null;
  principal_owed_cents: number;
  unpaid_fee_cents: number;
  fees_collected_cents: number | null;
  history_complete: boolean;
  lifecycle_counts: Record<string, number>;
  held_bridge_count: number;
  policy_id: string;
  policy_hash: string;
}
export interface ProductCustomer {
  customer_id: string;
  display_name: string | null;
  mapping_active: boolean;
  accounting_available: boolean;
  principal_owed_cents: number | null;
  unpaid_fee_cents: number | null;
  reserved_principal_cents: number | null;
  reserved_fee_cents: number | null;
  history_complete: boolean | null;
  active_hold: boolean | null;
  version: number | null;
  captured_at: string | null;
  active_bridge_id: string | null;
}
export interface CustomerPage extends ProductSource {
  items: ProductCustomer[];
  next_cursor: string | null;
}
export interface CustomerDetail extends ProductSource {
  customer: ProductCustomer;
}
export interface ProductBridge {
  id: string;
  customer_id: string;
  offer_id: string;
  originating_case_id: string | null;
  evaluation_id: string;
  version: number;
  lifecycle: string;
  created_at: string;
  funded_at: string | null;
  due_on: string;
  principal_owed_cents: number;
  contract_fee_owed_cents: number;
  late_fee_owed_cents: number;
  principal_original_cents: number;
  contract_fee_original_cents: number;
  term_days: number;
  hold_reasons: string[];
}
export interface BridgePage extends ProductSource {
  items: ProductBridge[];
  next_cursor: string | null;
}

export interface WalletBalance extends ProductSource {
  customer_id: string;
  currency: 'USD';
  available_balance_cents: number;
  requested_at: string;
  observed_at: string;
  provider_mode: string;
}
