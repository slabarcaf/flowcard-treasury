interface Props {
  visible: boolean;
}

const ITEMS = [
  { customer: 'Atlas SaaS', result: '$5,000 auto-bridged', cls: 'text-emerald-600' },
  { customer: 'Nova Agency', result: '$6,000 approved, repaid in 9 days', cls: 'text-emerald-600' },
  { customer: 'Bloom & Co.', result: 'Declined on new evidence, reassess on deposit', cls: 'text-amber-600' },
  { customer: 'Pixel Studio', result: 'Ineligible (41 days history)', cls: 'text-gray-400' },
];

export function SummaryCard({ visible }: Props) {
  if (!visible) return null;

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-5 animate-panel-in">
      <h4 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-4">
        Summary
      </h4>
      <div className="space-y-3">
        {ITEMS.map((item, i) => (
          <div
            key={item.customer}
            className="flex items-baseline justify-between"
            style={{ animation: `fadeIn 0.3s ease-out ${i * 0.15}s both` }}
          >
            <span className="text-sm text-gray-800 font-medium">
              {item.customer}
            </span>
            <span className={`text-xs ${item.cls}`}>{item.result}</span>
          </div>
        ))}
      </div>
      <div className="mt-4 pt-3 border-t border-gray-200 flex gap-4 text-xs text-gray-400">
        <span>Reserve floor never breached</span>
        <span>Every decision logged with inputs and the rule that made it</span>
      </div>
    </div>
  );
}
