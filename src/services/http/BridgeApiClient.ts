import type { Kit5AttemptStatusResponse, Kit5NextCustomerRequest, Kit5NextCustomerResponse, Kit5RepaymentDepositResponse, LedgerProjectionResponse } from '../types.js';

export class BridgeApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'BridgeApiError';
    this.status = status;
  }
}

/** Browser-safe, read-only client for the server-owned bridge ledger. */
export class BridgeApiClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(baseUrl = '', fetchImpl: typeof fetch = fetch) {
    this.baseUrl = baseUrl;
    this.fetchImpl = fetchImpl;
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
