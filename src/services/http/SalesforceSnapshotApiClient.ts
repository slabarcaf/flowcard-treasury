import type { SalesforceSnapshotClient, SalesforceSnapshotReadModel } from '../types.js';

/** Browser-safe, read-only client for the server-owned Salesforce snapshot API. */
export class SalesforceSnapshotApiClient implements SalesforceSnapshotClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(baseUrl = '', fetchImpl: typeof fetch = fetch) {
    this.baseUrl = baseUrl;
    this.fetchImpl = fetchImpl;
  }

  async getSnapshot(customerId: string): Promise<SalesforceSnapshotReadModel> {
    const response = await this.fetchImpl(
      `${this.baseUrl}/api/customers/${encodeURIComponent(customerId)}/snapshot`,
      {
        method: 'GET',
        headers: { accept: 'application/json' },
        credentials: 'omit',
      },
    );
    if (!response.ok) throw new Error(`Salesforce snapshot request failed (${response.status})`);
    return await response.json() as SalesforceSnapshotReadModel;
  }
}
