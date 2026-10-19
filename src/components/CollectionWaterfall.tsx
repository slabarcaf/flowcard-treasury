import type { CollectionRow } from '../state/types.js';

interface Props {
  visible: boolean;
  rows: CollectionRow[];
}

function fmt(n: number): string {
  if (n >= 1000) return '$' + n.toLocaleString('en-US', { minimumFractionDigits: 2 });
  return '$' + n.toFixed(2);
}

export function CollectionWaterfall({ visible, rows }: Props) {
  if (!visible || rows.length === 0) return null;

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-4 animate-panel-in">
      <h4 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
        Collection Waterfall
      </h4>
      <table className="w-full text-sm">
        <thead>
          <tr className="text-gray-400 text-xs uppercase">
            <th className="text-left pb-2 font-medium">Charge</th>
            <th className="text-right pb-2 font-medium">Amount</th>
            <th className="text-left pb-2 pl-4 font-medium">request_id</th>
            <th className="text-center pb-2 font-medium">Status</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr
              key={r.requestId}
              className="border-t border-gray-100"
              style={{ animation: `fadeIn 0.3s ease-out ${i * 0.2}s both` }}
            >
              <td className="py-2.5 text-gray-700">{r.label}</td>
              <td className="py-2.5 text-right font-mono text-gray-900 font-medium">
                {fmt(r.amount)}
              </td>
              <td className="py-2.5 pl-4 font-mono text-gray-400 text-xs">
                {r.requestId}
              </td>
              <td className="py-2.5 text-center text-emerald-600 text-xs font-medium">
                {r.status}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
