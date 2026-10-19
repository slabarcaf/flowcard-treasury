import { AI_EXTRACTED_SIGNALS } from '../seed.js';

interface Props {
  visible: boolean;
}

export function SignalExtraction({ visible }: Props) {
  if (!visible) return null;

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-4 animate-panel-in">
      <h4 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
        AI Signal Extraction
      </h4>
      <table className="w-full text-sm">
        <thead>
          <tr className="text-gray-400 text-xs uppercase">
            <th className="text-left pb-2 font-medium">Signal</th>
            <th className="text-left pb-2 font-medium">Value</th>
            <th className="text-left pb-2 font-medium">Evidence</th>
          </tr>
        </thead>
        <tbody>
          {AI_EXTRACTED_SIGNALS.map((s, i) => (
            <tr
              key={s.signal}
              className="border-t border-gray-100"
              style={{ animation: `fadeIn 0.3s ease-out ${i * 0.15}s both` }}
            >
              <td className="py-2 pr-3 font-mono text-amber-600 text-xs">
                {s.signal}
              </td>
              <td className="py-2 pr-3 font-mono text-gray-900">{s.value}</td>
              <td className="py-2 text-gray-500 italic text-xs">{s.evidence}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
