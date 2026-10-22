import type { CrmClient } from '../types.js';
import { getDemoSalesforceSnapshot } from '../../../server/src/salesforce/fixture-source.js';

/**
 * Mock CRM client that returns seeded Salesforce notes.
 */
export const mockCrmClient: CrmClient = {
  async getLatestNotes(accountId) {
    const snapshot = getDemoSalesforceSnapshot(accountId);
    return (snapshot?.callActivities ?? [])
      .filter((activity) => activity.note.value !== null)
      .map((activity) => ({
        author: 'Salesforce call activity',
        timestamp: activity.activityAt.value ?? activity.sourceUpdatedAt ?? '',
        text: activity.note.value ?? '',
        sourceRecordId: activity.recordId,
      }));
  },
};
