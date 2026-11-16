export type SourceMode = 'DEMO' | 'SANDBOX';
/** Linkage from another saved read, checked locally and never sent as authority. */
export interface ReadScope { mode?: SourceMode; customerID?: string; evaluationID?: string; originatingCaseID?: string }
export interface CaseItem {
  id: string; kind: string; status: string; next_stage: string;
  customer_id: string | null; assessment_id: string | null; decision_status: string | null;
  bridge_id: string | null; bridge_version: number | null; updated_at: string;
}
export interface CasePage { items: CaseItem[]; next_cursor: string | null }
export interface Rule { rule_id: string; status: string; reason_code: string; actual: unknown; threshold: unknown }
export interface Evaluation {
  id: string; customer_id: string; account_ref: string; shortfall_id: string; source_mode: SourceMode;
  status: string; tier?: string | null; evaluated_at: string; policy_id: string; policy_hash: string; term_days: number;
  customer_snapshot_id: string | null; customer_snapshot_version: number | null;
  facility_snapshot_id: string | null; facility_snapshot_version: number | null;
  funding_authorized: false; requires_recheck: true; approval_required: boolean;
  principal_cents: number | null; estimated_fee_cents: number | null; total_due_cents: number | null;
  customer_limit_cents: number | null; existing_exposure_cents: number | null;
  post_funding_exposure_cents: number | null; available_capital_cents: number | null;
  rules: Rule[]; reason_codes: string[];
}
export interface CaseDetail {
  case_id: string; mode: SourceMode; kind: string; status: string; stage: string;
  customer_id: string | null; account_ref: string | null; detection_id: string | null;
  bridge_id: string | null; bridge_version: number | null; refresh_needed: boolean;
  assessment?: Evaluation; next_actions: string[]; updated_at: string;
  investigation?: { summary: string; verified: boolean; current: boolean; explanation_mode: string; limitation?: string };
}
export interface Change {
  sequence: number; resource_type: 'OFFER' | 'BRIDGE' | 'OPERATION'; resource_id: string;
  resource_version: number; change_type: string; recorded_at: string; customer_id: string;
  originating_case_id: string | null; evaluation_id: string | null;
  offer_id: string | null; bridge_id: string | null; bridge_version: number | null;
}
export interface Changes { mode: SourceMode; items: Change[]; next_after_seq: number; has_more: boolean }
export interface Offer {
  source_mode?: SourceMode; customer_id?: string;
  waterfall_version?: number;
  id: string; version: number; offer_revision: number; terms_hash: string; state: string;
  principal_cents: number; contract_fee_cents: number; total_due_cents: number; term_days: number;
  offered_on: string; due_on: string; expires_at: string; disclosures_version: string;
  late_fee_convention: string; consent_label: string; originating_case_id: string | null; evaluation_id: string | null;
}
export interface Operation {
  id: string; version: number; kind: string; state: string; reserved_amount_cents: number | null;
  last_verified_settlement_reference: string | null; retry_count: number; next_check: string | null; hold_reason: string | null;
}
export interface Bridge {
  id: string; mode: SourceMode; source_binding_id: string; customer_id: string; account_ref: string;
  version: number; as_of: string; currency: 'USD'; lifecycle: string; delinquency: string;
  originating_case_id: string | null; evaluation_id: string | null; offer_id: string | null; terms_hash: string | null;
  principal_owed_cents: number | null; contract_fee_owed_cents: number | null; late_fee_owed_cents: number | null;
  due_on: string | null; funded_at?: string | null; days_past_due: number | null; debt_as_of: string | null;
  last_provider_observed_at?: string | null; next_eligible_retry_at?: string | null;
  last_attempt_id?: string | null; last_attempt_state?: string | null;
  accrual_current: boolean | null; accrual_current_through: string | null;
  acceptance_status: string | null; approval_status: string | null; collection_authority_status: string | null;
  hold_reasons: string[]; allowed_next_actions: string[]; operations: Operation[];
}
export interface Allocation { allocation_index: number; bucket: string; amount_cents: number }
export interface Collection {
  id: string; operation_id: string; deposit_id: string; state: string; created_at: string;
  settled_at: string | null; reserved_debit_cents: number; settled_debit_cents: number; provider_fee_cents: number;
  planned_allocations: Allocation[]; allocations: Allocation[];
}
export interface Collections { bridge_version: number; items: Collection[]; next_cursor: string | null }

export class FinancialReadError extends Error {
  readonly status: number;
  constructor(status: number) {
    super(status === 401 ? 'Your session has expired. Sign in again to read these records.'
      : status === 403 ? 'This account cannot read operator financial records.'
      : status === 404 ? 'This record is missing from the connected service.'
      : status === 409 ? 'The bridge version changed. Refresh the bridge and restart collection history.'
      : status === 502 ? 'The service returned an invalid or unsupported record. No financial values are displayed.'
      : 'The financial source is unavailable. Refresh when the service is available.');
    this.status = status;
  }
}

type ObjectValue = Record<string, unknown>;
const object = (value: unknown): ObjectValue => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new FinancialReadError(502);
  return value as ObjectValue;
};
const text = (value: unknown): value is string => typeof value === 'string' && value.length > 0;
const nullableText = (value: unknown) => value === null || text(value);
const integer = (value: unknown, minimum = 0): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum;
const mode = (value: unknown) => value === 'DEMO' || value === 'SANDBOX';
const texts = (value: unknown) => Array.isArray(value) && value.every(item => typeof item === 'string');
const nullableVersion = (value: unknown, minimum = 1) => value === null || integer(value, minimum);
function requireRecord(valid: boolean): void { if (!valid) throw new FinancialReadError(502); }
function rows(value: unknown, validate: (row: ObjectValue) => void): void {
  requireRecord(Array.isArray(value));
  for (const row of value as unknown[]) validate(object(row));
}
function safeNumbers(value: unknown, key = '', depth = 0): void {
  requireRecord(depth < 40);
  if (key.endsWith('_cents')) requireRecord(value === null || (typeof value === 'number' && Number.isSafeInteger(value)));
  if (typeof value === 'number') requireRecord(Number.isFinite(value) && (!Number.isInteger(value) || Number.isSafeInteger(value)));
  else if (Array.isArray(value)) for (const item of value) safeNumbers(item, '', depth + 1);
  else if (value && typeof value === 'object') for (const [name, item] of Object.entries(value)) safeNumbers(item, name, depth + 1);
}
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
function resourcePath(prefix: string, id: string): string {
  if (!uuid.test(id)) throw new FinancialReadError(404);
  return `${prefix}/${id}`;
}
function operation(row: ObjectValue): void {
  requireRecord(text(row.id) && integer(row.version, 1) && text(row.kind) && text(row.state) && integer(row.retry_count));
}

/** Read-only client for existing Go contracts. No DataAdapter or write methods. */
export class FinancialReadClient {
  private readonly fetchImpl: typeof fetch;
  constructor(fetchImpl: typeof fetch = globalThis.fetch.bind(globalThis)) { this.fetchImpl = fetchImpl; }
  private async read(path: string, signal?: AbortSignal): Promise<ObjectValue> {
    let response: Response;
    try {
      response = await this.fetchImpl(path, { method: 'GET', credentials: 'include', cache: 'no-store', signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30_000)]) : AbortSignal.timeout(30_000), headers: { accept: 'application/json' } });
    } catch { throw new FinancialReadError(503); }
    if (!response.ok) throw new FinancialReadError(response.status);
    try {
      const raw = await response.text();
      requireRecord(raw.length <= 2 * 1024 * 1024);
      const value = object(JSON.parse(raw));
      safeNumbers(value);
      return value;
    } catch { throw new FinancialReadError(502); }
  }
  async cases(cursor?: string, signal?: AbortSignal): Promise<CasePage> {
    const value = await this.read(`/api/coordinator/cases${cursor ? `?${new URLSearchParams({ cursor })}` : ''}`, signal);
    rows(value.items, row => requireRecord(text(row.id) && text(row.kind) && text(row.status) && text(row.next_stage) && nullableText(row.customer_id) && nullableText(row.assessment_id) && nullableText(row.bridge_id) && nullableVersion(row.bridge_version, 0) && text(row.updated_at)));
    requireRecord(value.next_cursor === null || text(value.next_cursor));
    return value as unknown as CasePage;
  }
  async case(id: string, signal?: AbortSignal, scope?: ReadScope): Promise<CaseDetail> {
    const value = await this.read(resourcePath('/api/coordinator/cases', id), signal);
    requireRecord(value.case_id === id && mode(value.mode) && text(value.status) && text(value.kind) && text(value.stage) && texts(value.next_actions) && nullableText(value.customer_id) && nullableText(value.account_ref) && nullableText(value.detection_id) && nullableText(value.bridge_id) && nullableVersion(value.bridge_version, 0) && typeof value.refresh_needed === 'boolean' && text(value.updated_at));
    if (value.assessment !== undefined) {
      const assessment = object(value.assessment);
      requireRecord(text(assessment.id) && assessment.source_mode === value.mode && assessment.customer_id === value.customer_id && assessment.account_ref === value.account_ref && assessment.shortfall_id === value.detection_id && text(assessment.status) && integer(assessment.term_days, 1) && text(assessment.policy_id) && text(assessment.policy_hash) && text(assessment.evaluated_at) && assessment.funding_authorized === false && assessment.requires_recheck === true && typeof assessment.approval_required === 'boolean' && texts(assessment.reason_codes));
      rows(assessment.rules, rule => requireRecord(text(rule.rule_id) && text(rule.status) && typeof rule.reason_code === 'string'));
      for (const field of ['customer_snapshot_version', 'facility_snapshot_version']) requireRecord(nullableVersion(assessment[field]));
      for (const field of ['principal_cents','estimated_fee_cents','total_due_cents','customer_limit_cents','existing_exposure_cents','post_funding_exposure_cents','available_capital_cents']) requireRecord(assessment[field] === null || integer(assessment[field]));
    }
    if (value.investigation !== undefined) {
      const investigation = object(value.investigation);
      requireRecord(typeof investigation.summary === 'string' && typeof investigation.verified === 'boolean' && typeof investigation.current === 'boolean' && text(investigation.explanation_mode));
    }
    if (scope) requireRecord((scope.mode === undefined || value.mode === scope.mode) && (scope.customerID === undefined || value.customer_id === scope.customerID) && (scope.originatingCaseID === undefined || value.case_id === scope.originatingCaseID) && (scope.evaluationID === undefined || (value.assessment as ObjectValue | undefined)?.id === scope.evaluationID));
    return value as unknown as CaseDetail;
  }
  async changes(after = 0, signal?: AbortSignal): Promise<Changes> {
    requireRecord(integer(after));
    const value = await this.read(`/api/servicing/changes?${new URLSearchParams({ after_seq: String(after) })}`, signal);
    requireRecord(mode(value.mode) && integer(value.next_after_seq) && typeof value.has_more === 'boolean');
    let sequence = after;
    rows(value.items, row => {
      requireRecord(integer(row.sequence, sequence + 1) && ['OFFER','BRIDGE','OPERATION'].includes(String(row.resource_type)) && text(row.resource_id) && integer(row.resource_version, 1) && text(row.change_type) && text(row.recorded_at) && text(row.customer_id) && nullableText(row.bridge_id) && nullableVersion(row.bridge_version));
      for (const field of ['originating_case_id','evaluation_id','offer_id']) requireRecord(nullableText(row[field]));
      requireRecord((row.bridge_id === null) === (row.bridge_version === null));
      sequence = row.sequence as number;
    });
    requireRecord(value.next_after_seq === sequence && (!value.has_more || sequence > after));
    return value as unknown as Changes;
  }
  async offer(id: string, signal?: AbortSignal, scope?: ReadScope): Promise<Offer> {
    const value = await this.read(resourcePath('/api/offers', id), signal);
    requireRecord(value.id === id && integer(value.version, 1) && integer(value.offer_revision, 1) && text(value.terms_hash) && text(value.state) && integer(value.term_days, 1) && text(value.due_on) && text(value.offered_on) && text(value.expires_at) && text(value.consent_label));
    for (const field of ['principal_cents','contract_fee_cents','total_due_cents']) requireRecord(integer(value[field]));
    if (scope) requireRecord((scope.evaluationID === undefined || value.evaluation_id === scope.evaluationID) && (scope.originatingCaseID === undefined || value.originating_case_id === scope.originatingCaseID));
    return value as unknown as Offer;
  }
  async bridge(id: string, signal?: AbortSignal, scope?: ReadScope): Promise<Bridge> {
    const value = await this.read(resourcePath('/api/bridges', id), signal);
    requireRecord(value.id === id && mode(value.mode) && value.currency === 'USD' && text(value.customer_id) && value.account_ref === `customer:${value.customer_id}` && text(value.source_binding_id) && integer(value.version, 1) && text(value.as_of) && text(value.lifecycle) && text(value.delinquency) && texts(value.hold_reasons) && texts(value.allowed_next_actions));
    for (const field of ['principal_owed_cents','contract_fee_owed_cents','late_fee_owed_cents']) requireRecord(value[field] === null || integer(value[field]));
    requireRecord(value.accrual_current === null || typeof value.accrual_current === 'boolean');
    rows(value.operations, operation);
    if (scope) requireRecord((scope.mode === undefined || value.mode === scope.mode) && (scope.customerID === undefined || value.customer_id === scope.customerID) && (scope.evaluationID === undefined || value.evaluation_id === scope.evaluationID) && (scope.originatingCaseID === undefined || value.originating_case_id === scope.originatingCaseID));
    return value as unknown as Bridge;
  }
  async collections(id: string, expectedVersion: number, cursor?: string, signal?: AbortSignal): Promise<Collections> {
    requireRecord(integer(expectedVersion, 1));
    const value = await this.read(`${resourcePath('/api/bridges', id)}/collections${cursor ? `?${new URLSearchParams({ cursor })}` : ''}`, signal);
    requireRecord(integer(value.bridge_version, 1) && (value.next_cursor === null || text(value.next_cursor)));
    if (value.bridge_version !== expectedVersion) throw new FinancialReadError(409);
    rows(value.items, row => {
      requireRecord(text(row.id) && text(row.operation_id) && text(row.deposit_id) && text(row.state) && text(row.created_at));
      for (const field of ['reserved_debit_cents','settled_debit_cents','provider_fee_cents']) requireRecord(integer(row[field]));
      for (const field of ['planned_allocations','allocations']) rows(row[field], allocation => requireRecord(integer(allocation.allocation_index) && text(allocation.bucket) && integer(allocation.amount_cents)));
    });
    return value as unknown as Collections;
  }
  async operation(id: string, signal?: AbortSignal): Promise<Operation> {
    const value = await this.read(resourcePath('/api/operations', id), signal);
    requireRecord(value.id === id);
    operation(value);
    return value as unknown as Operation;
  }
}

/** Format safe integer cents without floating-point division or recomputation. */
export function cents(value: number | null | undefined): string {
  if (value === null || value === undefined) return 'Unavailable';
  if (!Number.isSafeInteger(value)) throw new FinancialReadError(502);
  const signed = BigInt(value), magnitude = signed < 0n ? -signed : signed;
  return `${signed < 0n ? '-' : ''}$${(magnitude / 100n).toLocaleString('en-US')}.${String(magnitude % 100n).padStart(2, '0')}`;
}
