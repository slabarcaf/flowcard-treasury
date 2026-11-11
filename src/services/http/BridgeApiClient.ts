import type { Kit5AttemptStatusResponse, Kit5FixtureCheckpointLifecycleRequest, Kit5FixtureCheckpointRecoveryResponse, Kit5FixtureCheckpointRequest, Kit5FixtureCheckpointResponse, Kit5NextCustomerRequest, Kit5NextCustomerResponse, Kit5RepaymentDepositResponse, LedgerProjectionResponse } from '../types.js';

export class BridgeApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'BridgeApiError';
    this.status = status;
  }
}

function isProviderTransaction(value: unknown): boolean {
  if (typeof value !== 'object' || value === null) return false;
  const transaction = value as Record<string, unknown>;
  return (transaction.processResult === 'APPROVED' || transaction.processResult === 'DECLINED') &&
    (typeof transaction.failureReason === 'string' || transaction.failureReason === null) &&
    typeof transaction.cardTransactionEventId === 'string' &&
    typeof transaction.cardTransactionId === 'string' &&
    typeof transaction.cardTransactionLifecycleId === 'string';
}

function isSafeProviderError(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 420 &&
    /^Airwallex rejected the sandbox request \(HTTP 4\d{2}(?:; phase=(?:authentication|issuing_simulation|provider_request))?(?:; code=[A-Za-z0-9_.-]{1,80})?(?:; source=[A-Za-z0-9_.\[\]/-]{1,120})?(?:; message=[A-Za-z0-9_ .,:()'\/\[\]-]{1,160})?\)$/.test(value);
}

function isFixtureCheckpointResponse(value: unknown): value is Kit5FixtureCheckpointResponse {
  if (typeof value !== 'object' || value === null) return false;
  const result = value as Record<string, unknown>;
  if (result.status === 'rejected') return typeof result.reason === 'string';
  if (!['completed', 'in_progress', 'ambiguous', 'failed'].includes(String(result.status)) ||
      (result.scenarioId !== undefined && typeof result.scenarioId !== 'string') ||
      (result.customerId !== undefined && typeof result.customerId !== 'string') ||
      (result.actionId !== undefined && typeof result.actionId !== 'string') ||
      typeof result.attemptId !== 'string' ||
      (typeof result.lifecycleId !== 'string' && result.lifecycleId !== null)) return false;
  if (result.providerError !== undefined && (result.status !== 'failed' || !isSafeProviderError(result.providerError))) return false;
  const amount = result.attemptedAmount;
  if (amount !== null && (typeof amount !== 'object' || amount === null ||
      !Number.isSafeInteger((amount as Record<string, unknown>).amountCents) ||
      typeof (amount as Record<string, unknown>).currency !== 'string')) return false;
  if (result.status === 'completed') return isProviderTransaction(result.result);
  return result.result === undefined || isProviderTransaction(result.result);
}

/** Browser-safe, read-only client for the server-owned bridge ledger. */
export class BridgeApiClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(baseUrl = '', fetchImpl?: typeof fetch) {
    this.baseUrl = baseUrl;
    this.fetchImpl = fetchImpl ?? globalThis.fetch.bind(globalThis);
  }

  async getLedgerProjection(): Promise<LedgerProjectionResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/ledger`, {
      method: 'GET',
      headers: { accept: 'application/json' },
      credentials: 'omit',
    });
    if (!response.ok) throw new Error(`Bridge ledger request failed (${response.status})`);
    return await response.json() as LedgerProjectionResponse;
  }

  async nextCustomer(input: Kit5NextCustomerRequest): Promise<Kit5NextCustomerResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/next-customer`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
    const body = await response.json() as unknown;
    if (!response.ok && typeof body === 'object' && body !== null && 'status' in body
      && body.status === 'rejected' && 'reason' in body && typeof body.reason === 'string') {
      return body as Kit5NextCustomerResponse;
    }
    if (!response.ok) {
      const message = typeof body === 'object' && body !== null && 'error' in body
        && typeof body.error === 'string'
        ? body.error
        : `Kit 5 provider action failed (${response.status})`;
      throw new Error(message);
    }
    return body as Kit5NextCustomerResponse;
  }

  /** Create or reconcile the allowlisted provider-only checkpoint using stable run-scoped identity. */
  async fixtureCheckpoint(input: Kit5FixtureCheckpointRequest): Promise<Kit5FixtureCheckpointResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/fixture-checkpoint`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
    let body: unknown;
    try {
      body = await response.json() as unknown;
    } catch {
      throw new BridgeApiError(`Kit 5 fixture checkpoint failed (${response.status})`, response.status);
    }
    if (!isFixtureCheckpointResponse(body)) {
      const message = typeof body === 'object' && body !== null && 'error' in body && typeof body.error === 'string'
        ? body.error
        : `Kit 5 fixture checkpoint failed (${response.status})`;
      throw new BridgeApiError(message, response.status);
    }
    // The provider-only route uses 502 for a persisted terminal failure and
    // 409 for a definitive rejection. Preserve both response bodies so the
    // dashboard can show the saved attempt and distinguish it from a network
    // failure that still needs reconciliation.
    if (!response.ok && body.status !== 'failed' && body.status !== 'rejected') {
      throw new BridgeApiError(`Kit 5 fixture checkpoint failed (${response.status})`, response.status);
    }
    return body;
  }

  /** Recover the server-owned checkpoint identity after session storage is lost. */
  async recoverFixtureCheckpoint(): Promise<Kit5FixtureCheckpointRecoveryResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/fixture-checkpoint/recover`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({}),
    });
    let body: unknown;
    try {
      body = await response.json() as unknown;
    } catch {
      throw new BridgeApiError(`Kit 5 fixture checkpoint recovery failed (${response.status})`, response.status);
    }
    const recoveredRecord = typeof body === 'object' && body !== null ? body as Record<string, unknown> : null;
    const validRecovery = recoveredRecord !== null && (recoveredRecord.status === 'not_found' ||
      (isFixtureCheckpointResponse(body) && recoveredRecord.status !== 'rejected' &&
        typeof recoveredRecord.scenarioId === 'string' && typeof recoveredRecord.customerId === 'string' &&
        typeof recoveredRecord.actionId === 'string'));
    if (!validRecovery) {
      throw new BridgeApiError(`Kit 5 fixture checkpoint recovery failed (${response.status})`, response.status);
    }
    if (!response.ok) throw new BridgeApiError(`Kit 5 fixture checkpoint recovery failed (${response.status})`, response.status);
    return body as Kit5FixtureCheckpointRecoveryResponse;
  }

  /** Verify a lifecycle found by an operator, without repeating the provider create. */
  async reconcileFixtureCheckpointLifecycle(input: Kit5FixtureCheckpointLifecycleRequest): Promise<Kit5FixtureCheckpointResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/fixture-checkpoint/reconcile`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
    let body: unknown;
    try {
      body = await response.json() as unknown;
    } catch {
      throw new BridgeApiError(`Kit 5 fixture checkpoint reconciliation failed (${response.status})`, response.status);
    }
    if (!isFixtureCheckpointResponse(body)) {
      const message = typeof body === 'object' && body !== null && 'error' in body && typeof body.error === 'string'
        ? body.error
        : `Kit 5 fixture checkpoint reconciliation failed (${response.status})`;
      throw new BridgeApiError(message, response.status);
    }
    if (!response.ok && body.status !== 'failed' && body.status !== 'rejected') {
      throw new BridgeApiError(`Kit 5 fixture checkpoint reconciliation failed (${response.status})`, response.status);
    }
    return body;
  }

  async createOperatorSession(token: string): Promise<{ approvalAvailable: boolean }> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/session`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ token }),
    });
    if (!response.ok) throw new Error(`Kit 5 operator login failed (${response.status})`);
    const body = await response.json() as { approvalAvailable?: unknown };
    return { approvalAvailable: body.approvalAvailable === true };
  }

  async refreshAttempt(input: Kit5NextCustomerRequest): Promise<Kit5AttemptStatusResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/attempt-status`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
    const body = await response.json() as unknown;
    if (!response.ok) throw new BridgeApiError(`Kit 5 status refresh failed (${response.status})`, response.status);
    return body as Kit5AttemptStatusResponse;
  }

  async approveHumanReview(input: Kit5NextCustomerRequest & { reason: string }): Promise<{ status: 'approved' | 'pending' | 'blocked'; bridgeId?: string }> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/approve`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
    const body = await response.json() as unknown;
    if (!response.ok) throw new BridgeApiError(`Kit 5 approval failed (${response.status})`, response.status);
    return body as { status: 'approved' | 'pending' | 'blocked'; bridgeId?: string };
  }

  async createOrRefreshNovaRepaymentDeposit(input: Kit5NextCustomerRequest): Promise<Kit5RepaymentDepositResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/api/kit5/repayment-deposit`, {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
    const body = await response.json() as unknown;
    if (!response.ok) {
      const message = typeof body === 'object' && body !== null && 'error' in body && typeof body.error === 'string'
        ? body.error
        : `Nova repayment deposit failed (${response.status})`;
      throw new BridgeApiError(message, response.status);
    }
    return body as Kit5RepaymentDepositResponse;
  }
}
