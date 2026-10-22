import type { LedgerProjectionResponse } from '../types.js';

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
}
