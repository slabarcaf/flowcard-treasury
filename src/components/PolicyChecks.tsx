import type { PolicyCheck } from '../state/types.js';

interface Props {
  visible: boolean;
  checks: PolicyCheck[];
}

export function PolicyChecks({ visible, checks }: Props) {
  if (!visible || checks.length === 0) return null;

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-4 animate-panel-in">
      <h4 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
        Policy Checks
      </h4>
      <ul className="space-y-1.5">
        {checks.map((c, i) => (
          <li
            key={i}
            className="flex items-start gap-2 text-sm"
            style={{ animation: `fadeIn 0.25s ease-out ${i * 0.12}s both` }}
          >
            <span
              className={`mt-0.5 text-base leading-none ${
                c.passed ? 'text-emerald-500' : 'text-red-500'
              }`}
            >
              {c.passed ? '\u2713' : '\u2717'}
            </span>
            <span className={c.passed ? 'text-gray-700' : 'text-red-600'}>
              {c.label}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
