import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onUpdate: (patch: Partial<OnboardingData>) => void;
  onNext: () => void;
  onBack: () => void;
}

const EXCLUSION_ITEMS = [
  {
    id: 'excluded_industry',
    text: 'My company operates in an excluded industry (e.g., weapons, tobacco, adult entertainment)',
  },
  {
    id: 'gambling',
    text: 'My company is involved in gambling, lotteries, or fantasy sports',
  },
  {
    id: 'crypto_exchange',
    text: 'My company is a cryptocurrency exchange or money transmitter',
  },
  {
    id: 'fraud',
    text: 'My company has been involved in fraud, money laundering, or terrorism financing',
  },
  {
    id: 'sanctions',
    text: 'My company is subject to OFAC sanctions or located in a sanctioned jurisdiction',
  },
  {
    id: 'not_legal_entity',
    text: 'My company is not a legally registered entity in good standing',
  },
];

export function ComplianceStep({ data, onUpdate, onNext }: Props) {

  const hasAnyChecked = data.complianceFlags.length > 0;

  function toggle(id: string) {
    const flags = data.complianceFlags.includes(id)
      ? data.complianceFlags.filter((f) => f !== id)
      : [...data.complianceFlags, id];
    onUpdate({ complianceFlags: flags });
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!hasAnyChecked) onNext();
  }

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2 text-white">Compliance check</h2>
      <p className="text-sm mb-8" style={{ color: '#8B95A5' }}>
        Do any of the following apply to your company? Check all that apply.
      </p>

      <div className="space-y-2">
        {EXCLUSION_ITEMS.map((item) => {
          const checked = data.complianceFlags.includes(item.id);
          return (
            <label
              key={item.id}
              className="flex items-center gap-3 cursor-pointer rounded-xl px-4 transition-colors"
              style={{
                backgroundColor: checked ? 'rgba(227,72,80,0.1)' : '#1A2332',
                border: `1px solid ${checked ? 'rgba(227,72,80,0.3)' : '#2A3545'}`,
                minHeight: '56px',
              }}
            >
              <input
                type="checkbox"
                checked={checked}
                onChange={() => toggle(item.id)}
                className="shrink-0 w-4 h-4 rounded cursor-pointer accent-blue-500"
              />
              <span className="text-sm leading-snug py-3" style={{ color: '#C5CDD8' }}>{item.text}</span>
            </label>
          );
        })}
      </div>

      {hasAnyChecked && (
        <div
          className="mt-4 p-3 rounded-xl text-sm"
          style={{ backgroundColor: 'rgba(227,72,80,0.1)', border: '1px solid rgba(227,72,80,0.3)', color: '#E34850' }}
        >
          Based on your selections, your company may not be eligible for a FlowCard account.
        </div>
      )}

      <div className="mt-auto pt-10">
        <button
          type="submit"
          disabled={hasAnyChecked}
          className="revolut-btn-primary w-full disabled:opacity-40 disabled:cursor-not-allowed"
        >
          None of the above — Continue
        </button>
      </div>
    </form>
  );
}
