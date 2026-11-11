import { describe, expect, it } from 'vitest';
import { mapDemoEvidenceFixture, mockAgentClient, selectDemoEvidenceScenario } from '../../src/services/mock/AgentClient.js';

describe('demo evidence fixtures', () => {
  it('selects a named scenario from a synthetic note reference and defaults deterministically', () => {
    expect(selectDemoEvidenceScenario({ author: 'Demo', timestamp: '', text: '', sourceRecordId: 'demo:unmapped' })).toBe('unmapped');
    expect(selectDemoEvidenceScenario({ author: 'Demo', timestamp: '', text: '' })).toBe('multi_signal');
  });

  it('maps evidence fields, provenance, confidence, alternatives, and blocked states', async () => {
    const result = await mockAgentClient.extractSignals({ author: 'Demo', timestamp: '', text: '', sourceRecordId: 'demo:conflicts' });
    expect(result.status).toBe('review_required');
    expect(result.signals[0]).toMatchObject({
      signal: 'expected_inflow_delay',
      evidence: '“payment will arrive two weeks late”',
      sourceReference: 'Case 500-ATLAS-08',
      counterparty: 'Northstar Supply',
      confidenceStatus: 'review',
      alternatives: ['“payment is due as scheduled” (Call 00T-ATLAS-31)'],
    });
  });

  it('contains all six planned deterministic scenarios', () => {
    const scenarios = ['no_signal', 'unmapped', 'conflicts', 'multi_signal', 'correction', 'failure'] as const;
    const results = scenarios.map(mapDemoEvidenceFixture);
    expect(results.map((result) => result.status)).toEqual(['no_signal', 'unmapped', 'review_required', 'success', 'success', 'failed']);
    expect(results[0].signals).toEqual([]);
    expect(results[1].unmapped).toHaveLength(1);
    expect(results[3].signals).toHaveLength(2);
    expect(results[4].signals[0].alternatives[0]).toContain('Original model value');
    expect(results[5].message).toContain('no stale result is reused');
  });
});
