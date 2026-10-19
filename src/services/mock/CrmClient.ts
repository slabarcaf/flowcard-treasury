import type { CrmClient, CrmNote } from '../types.js';
import { BLOOM_CRM_NOTE } from '../../seed.js';

/**
 * Mock CRM client that returns seeded Salesforce notes.
 */
export const mockCrmClient: CrmClient = {
  async getLatestNotes(accountId) {
    if (accountId === 'acct_bloom001') {
      return [BLOOM_CRM_NOTE];
    }
    return [];
  },
};
