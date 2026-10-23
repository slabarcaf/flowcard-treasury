import { useState } from 'react';
import type { DemoEvidenceScenario } from '../services/types.js';
import { mapDemoEvidenceFixture, mockAgentClient } from '../services/mock/AgentClient.js';

interface Props {
  visible: boolean;
}

const SCENARIOS: Array<{ id: DemoEvidenceScenario; label: string }> = [
  { id: 'no_signal', label: 'No qualifying signal' },
  { id: 'unmapped', label: 'Unmapped evidence' },
  { id: 'conflicts', label: 'Conflicting claims' },
  { id: 'multi_signal', label: 'Multiple signals' },
  { id: 'correction', label: 'Reviewer correction' },
  { id: 'failure', label: 'Extraction failure' },
];

export function SignalExtraction({ visible }: Props) {
  const [scenario, setScenario] = useState<DemoEvidenceScenario>('multi_signal');
  const [result, setResult] = useState(() => mapDemoEvidenceFixture('multi_signal'));
  if (!visible) return null;

  const selectScenario = (selected: DemoEvidenceScenario) => {
    setScenario(selected);
    void mockAgentClient.extractSignals({ author: 'Synthetic demo fixture', timestamp: '', text: '', sourceRecordId: `demo:${selected}` })
      .then(setResult);
  };
  const blocked = ['review_required', 'unmapped', 'failed'].includes(result.status);

  return (
    <section className="bg-white border border-gray-200 rounded-lg p-4 animate-panel-in" aria-label="AI evidence extraction demo">
      <div className="flex items-center justify-between gap-3 mb-3">
        <h4 className="text-xs font-semibold text-gray-500 uppercase tracking-wider">Synthetic evidence replay</h4>
        <label className="text-xs text-gray-500">
          Scenario{' '}
          <select aria-label="Evidence scenario" value={scenario} onChange={(event) => selectScenario(event.target.value as DemoEvidenceScenario)} className="border rounded px-2 py-1 text-gray-800">
            {SCENARIOS.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
          </select>
        </label>
      </div>
      <p className={`text-sm mb-3 ${blocked ? 'text-rose-700' : 'text-gray-700'}`} role="status">
        {blocked ? 'BLOCKED' : result.status === 'no_signal' ? 'NO SIGNAL' : 'REVIEW ONLY'} · {result.message}
      </p>
      {result.signals.length > 0 && (
        <div className="space-y-3">
          {result.signals.map((signal, index) => (
            <article key={`${signal.signal}-${signal.sourceReference}-${index}`} className="border-t border-gray-100 pt-3 text-sm">
              <div className="flex flex-wrap gap-x-3 gap-y-1">
                <span className="font-mono text-amber-700">{signal.signal}</span>
                <span className="font-medium text-gray-900">{signal.value}</span>
                <span className="text-xs text-amber-700">Confidence: {signal.confidenceStatus}</span>
              </div>
              <blockquote className="mt-1 text-gray-700">{signal.evidence}</blockquote>
              <p className="mt-1 text-xs text-gray-500">Source: {signal.sourceReference} · Counterparty: {signal.counterparty ?? 'not stated'}</p>
              {signal.alternatives.length > 0 && <p className="mt-1 text-xs text-gray-600">Alternatives: {signal.alternatives.join('; ')}</p>}
            </article>
          ))}
        </div>
      )}
      {result.unmapped.map((item) => (
        <article key={`${item.type}-${item.sourceReference}`} className="border-t border-rose-100 pt-3 text-sm">
          <p className="font-mono text-rose-700">Unmapped: {item.type} · {item.value}</p>
          <blockquote className="mt-1 text-gray-700">{item.evidence}</blockquote>
          <p className="mt-1 text-xs text-gray-500">Source: {item.sourceReference} · Counterparty: not stated · Confidence: review</p>
        </article>
      ))}
    </section>
  );
}
