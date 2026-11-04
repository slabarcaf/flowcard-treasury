import type { AgentClient, AgentSignalExtraction, AgentAllocationProposal, CrmNote } from '../types.js';
import { AI_EXTRACTED_SIGNALS, AGENT_RATIONALES, AI_PROPOSED_PLAN } from '../../seed.js';

/**
 * Mock Agent client that returns the exact text from the demo doc.
 */
export const mockAgentClient: AgentClient = {
  async extractSignals(_note: CrmNote): Promise<AgentSignalExtraction[]> {
    return AI_EXTRACTED_SIGNALS;
  },

  async proposeAllocation(_params): Promise<AgentAllocationProposal[]> {
    // The AI's proposed plan after CRM evidence (the one policy partially rejects)
    return [
      {
        customerId: 'acct_atlas001',
        customerName: 'Atlas SaaS',
        amount: AI_PROPOSED_PLAN.atlas,
        rationale: AGENT_RATIONALES.atlas,
      },
      {
        customerId: 'acct_nova001',
        customerName: 'Nova Agency',
        amount: AI_PROPOSED_PLAN.nova,
        rationale: AGENT_RATIONALES.nova_revised,
      },
      {
        customerId: 'acct_bloom001',
        customerName: 'Bloom & Co.',
        amount: AI_PROPOSED_PLAN.bloom,
        rationale: AGENT_RATIONALES.bloom_initial,
      },
    ];
  },

  async generateRationale(customerId: string, _context: string): Promise<string> {
    const map: Record<string, string> = {
      acct_bloom001: AGENT_RATIONALES.bloom_initial,
      acct_atlas001: AGENT_RATIONALES.atlas,
      acct_nova001: AGENT_RATIONALES.nova_initial,
    };
    return map[customerId] ?? 'No rationale available';
  },
};
