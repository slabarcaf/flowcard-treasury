import { SalesforceSnapshotApiClient } from './http/SalesforceSnapshotApiClient.js';
import { SalesforceSnapshotFixtureClient } from './mock/SalesforceSnapshotFixtureClient.js';
import type { SalesforceSnapshotClient } from './types.js';

export type SalesforceSnapshotClientMode = 'demo' | 'production';

/** Select a source explicitly; production never falls back to local fixtures. */
export function createSalesforceSnapshotClient(
  mode: SalesforceSnapshotClientMode = import.meta.env.PROD ? 'production' : 'demo',
  options: { baseUrl?: string; fetchImpl?: typeof fetch } = {},
): SalesforceSnapshotClient {
  if (mode === 'production') {
    const baseUrl = options.baseUrl ?? import.meta.env.VITE_SALESFORCE_SNAPSHOT_API_URL ?? '';
    return new SalesforceSnapshotApiClient(baseUrl, options.fetchImpl);
  }
  return new SalesforceSnapshotFixtureClient();
}
