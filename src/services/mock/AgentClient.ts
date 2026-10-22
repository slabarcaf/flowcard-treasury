import type { AgentClient, AgentAllocationProposal, AgentEvidenceExtraction, CrmNote, DemoEvidenceScenario } from '../types.js';
import { AI_EVIDENCE_DEMO_FIXTURES, AGENT_RATIONALES, AI_PROPOSED_PLAN } from '../../seed.js';

const SCENARIOS = Object.keys(AI_EVIDENCE_DEMO_FIXTURES) as DemoEvidenceScenario[];

/** Select a stable, local-only fixture from the note source reference. */
export function selectDemoEvidenceScenario(note: CrmNote): DemoEvidenceScenario {
  const sourceReference = note.sourceRecordId ?? '';
  const selected = SCENARIOS.find((scenario) => sourceReference === `demo:${scenario}`);
  return selected ?? 'multi_signal';
}

export function mapDemoEvidenceFixture(scenario: DemoEvidenceScenario): AgentEvidenceExtraction {
  // The fixture catalog is declared `as const` to prevent accidental mutation.
  // Copy its nested values so callers receive the mutable service contract.
  const fixture = AI_EVIDENCE_DEMO_FIXTURES[scenario];
  return {
    scenario,
    status: fixture.status,
    message: fixture.message,
    signals: 'signals' in fixture ? fixture.signals.map((signal) => ({ ...signal, alternatives: [...signal.alternatives] })) : [],
    unmapped: 'unmapped' in fixture ? fixture.unmapped.map((item) => ({ ...item })) : [],
  } as AgentEvidenceExtraction;
}

/** Demo-only AgentClient. It never makes browser model or provider requests. */
export const mockAgentClient: AgentClient = {
  async extractSignals(note: CrmNote): Promise<AgentEvidenceExtraction> {
    return mapDemoEvidenceFixture(selectDemoEvidenceScenario(note));
  },

  async proposeAllocation(_params): Promise<AgentAllocationProposal[]> {
    return [
      { customerId: 'acct_atlas001', customerName: 'Atlas SaaS', amount: AI_PROPOSED_PLAN.atlas, rationale: AGENT_RATIONALES.atlas },
      { customerId: 'acct_nova001', customerName: 'Nova Agency', amount: AI_PROPOSED_PLAN.nova, rationale: AGENT_RATIONALES.nova_revised },
      { customerId: 'acct_bloom001', customerName: 'Bloom & Co.', amount: AI_PROPOSED_PLAN.bloom, rationale: AGENT_RATIONALES.bloom_initial },
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
