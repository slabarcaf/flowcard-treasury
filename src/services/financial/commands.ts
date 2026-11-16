import type { Bridge, CaseDetail, Offer, Operation } from './client.js';

export interface ServicingSession { enabled: boolean; linked: boolean; mode?: 'DEMO'; role?: 'OPERATOR' | 'CUSTOMER'; customer_id?: string }
export interface SavedCommand { id: string; path: string; idempotency_key: string; payload: Record<string, unknown>; state: 'UNKNOWN' | 'RECEIVED' | 'REJECTED'; status: number | null; receipt: Record<string, unknown> | null; created_at: string }
export interface CommandReceipt { status: number; commandID: string; receipt: Record<string, unknown> }
export type ActionRecord = { kind: 'case'; value: CaseDetail } | { kind: 'offer'; value: Offer } | { kind: 'bridge'; value: Bridge } | { kind: 'operation'; value: Operation };

export class FinancialCommandError extends Error {
  readonly status: number;
  readonly commandID: string;
  constructor(status: number, commandID = '') {
    super(status === 401 ? 'Your session expired. Sign in again. Saved commands remain in recovery history.'
      : status === 403 ? 'Your verified servicing binding does not permit this action.'
      : status === 409 ? 'The request key or record version conflicts. Refresh saved state before a new action.'
      : status === 422 || status === 400 || status === 404 ? 'The service rejected this command. Review its saved receipt and current terms before a new action.'
      : 'The command outcome is unknown. Refresh recovery history and saved state. Retry only the same saved command and key.');
    this.status = status; this.commandID = commandID;
  }
}
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);

/** Only typed explicit actions. Durable keys/payloads/receipts live server-side. */
export class FinancialCommandClient {
  private readonly fetchImpl: typeof fetch;
  constructor(fetchImpl: typeof fetch = globalThis.fetch.bind(globalThis)) { this.fetchImpl = fetchImpl; }
  private async read(path: string): Promise<Record<string, unknown>> {
    let response: Response;
    try { response = await this.fetchImpl(path, { method: 'GET', credentials: 'include', cache: 'no-store', signal: AbortSignal.timeout(15_000), headers: { accept: 'application/json' } }); }
    catch { throw new FinancialCommandError(503); }
    if (!response.ok) throw new FinancialCommandError(response.status);
    let data: unknown; try { data = await response.json(); } catch { throw new FinancialCommandError(502); }
    if (!object(data)) throw new FinancialCommandError(502);
    return data;
  }
  async session(): Promise<ServicingSession> {
    const data = await this.read('/api/servicing/session');
    if (typeof data.enabled !== 'boolean' || typeof data.linked !== 'boolean' || data.linked && (data.mode !== 'DEMO' || !['OPERATOR', 'CUSTOMER'].includes(String(data.role)) || typeof data.customer_id !== 'string')) throw new FinancialCommandError(502);
    return data as unknown as ServicingSession;
  }
  async offers(): Promise<string[]> {
    const data = await this.read('/api/customer/offers');
    if (!Array.isArray(data.offer_ids) || !data.offer_ids.every(id => typeof id === 'string' && uuid.test(id))) throw new FinancialCommandError(502);
    return data.offer_ids;
  }
  async history(): Promise<SavedCommand[]> {
    const data = await this.read('/api/financial/commands');
    if (!Array.isArray(data.commands) || !data.commands.every(command => object(command) && typeof command.id === 'string' && uuid.test(command.id) && typeof command.path === 'string' && typeof command.idempotency_key === 'string' && object(command.payload) && ['UNKNOWN', 'RECEIVED', 'REJECTED'].includes(String(command.state)))) throw new FinancialCommandError(502);
    return data.commands as unknown as SavedCommand[];
  }
  private async send(path: string, payload: Record<string, unknown>, key: string): Promise<CommandReceipt> {
    // This whitelist also protects recovery responses from choosing any URL.
    if (!/^\/api\/(?:offers(?:\/[0-9a-f-]{36}\/(?:acceptances|approvals|fund))?|bridges\/[0-9a-f-]{36}\/collection-reviews|operations\/[0-9a-f-]{36}\/reconcile)$/.test(path)) throw new FinancialCommandError(400);
    let response: Response;
    try { response = await this.fetchImpl(path, { method: 'POST', credentials: 'include', cache: 'no-store', signal: AbortSignal.timeout(30_000), headers: { accept: 'application/json', 'content-type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify(payload) }); }
    catch { throw new FinancialCommandError(503); }
    const id = response.headers.get('X-FlowCard-Command-ID') ?? '';
    if (!response.ok) throw new FinancialCommandError(response.status, id);
    let body: unknown; try { body = await response.json(); } catch { throw new FinancialCommandError(502, id); }
    if (!object(body) || !uuid.test(id)) throw new FinancialCommandError(502, id);
    return { status: response.status, commandID: id, receipt: body };
  }
  async create(record: CaseDetail): Promise<CommandReceipt> {
    if (record.mode !== 'DEMO' || !record.assessment || !uuid.test(record.case_id)) throw new FinancialCommandError(400);
    return this.send('/api/offers', { evaluation_id: record.assessment.id, originating_case_id: record.case_id }, crypto.randomUUID());
  }
  private terms(offer: Offer): Record<string, unknown> {
    if (!uuid.test(offer.id) || !Number.isSafeInteger(offer.version) || offer.version < 1 || !/^[0-9a-f]{64}$/.test(offer.terms_hash)) throw new FinancialCommandError(400);
    return { terms_hash: offer.terms_hash, expected_version: offer.version };
  }
  async accept(offer: Offer): Promise<CommandReceipt> { return this.send(`/api/offers/${offer.id}/acceptances`, this.terms(offer), crypto.randomUUID()); }
  async approve(offer: Offer, reason: string): Promise<CommandReceipt> { return this.send(`/api/offers/${offer.id}/approvals`, { ...this.terms(offer), reason_code: reason }, crypto.randomUUID()); }
  async fund(offer: Offer): Promise<CommandReceipt> { this.terms(offer); return this.send(`/api/offers/${offer.id}/fund`, { expected_version: offer.version }, crypto.randomUUID()); }
  async review(bridge: Bridge, reason: string): Promise<CommandReceipt> { return this.send(`/api/bridges/${bridge.id}/collection-reviews`, { expected_version: bridge.version, reason_code: reason }, crypto.randomUUID()); }
  async reconcile(operation: Operation, reason: string): Promise<CommandReceipt> { return this.send(`/api/operations/${operation.id}/reconcile`, { expected_version: operation.version, reason_code: reason }, crypto.randomUUID()); }
  async retry(command: SavedCommand): Promise<CommandReceipt> { return this.send(command.path, command.payload, command.idempotency_key); }
}
