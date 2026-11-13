import { getDemoSalesforceSnapshot } from '../../../server/src/salesforce/fixture-source.js';
import type { SalesforceSnapshotClient, SalesforceSnapshotReadModel } from '../types.js';

const DEMO_CAPTURED_AT = '2026-10-21T00:00:00.000Z';

/** Deterministic, local-only Salesforce snapshot source for demo mode. */
export class SalesforceSnapshotFixtureClient implements SalesforceSnapshotClient {
  async getSnapshot(customerId: string): Promise<SalesforceSnapshotReadModel> {
    const data = getDemoSalesforceSnapshot(customerId);
    if (!data) {
      return {
        customerId,
        active: false,
        freshness: 'missing',
        ageSeconds: null,
        snapshot: null,
        latestAttempt: null,
        validationErrors: [],
      };
    }

    return {
      customerId,
      active: true,
      freshness: 'fresh',
      ageSeconds: 0,
      snapshot: {
        snapshotId: `demo-${customerId}`,
        version: 1,
        capturedAt: DEMO_CAPTURED_AT,
        publishedAt: DEMO_CAPTURED_AT,
        sourceMode: 'demo',
        schemaVersion: data.schemaVersion,
        mappingVersion: data.mappingVersion,
        data: structuredClone(data),
      },
      latestAttempt: { status: 'succeeded', finishedAt: DEMO_CAPTURED_AT },
      validationErrors: [],
    };
  }
}
