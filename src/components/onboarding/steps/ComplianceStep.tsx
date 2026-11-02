import { useState } from 'react';
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

export function ComplianceStep({ data, onUpdate, onNext, onBack }: Props) {
  const [touched, setTouched] = useState(false);

  const hasAnyChecked = data.complianceFlags.length > 0;

  function toggle(id: string) {
    const flags = data.complianceFlags.includes(id)
      ? data.complianceFlags.filter((f) => f !== id)
      : [...data.complianceFlags, id];
    onUpdate({ complianceFlags: flags });
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setTouched(true);
    // If none are checked, user is eligible → proceed
    if (!hasAnyChecked) onNext();
  }

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2" style={{ color: '#25252D' }}>Compliance check</h2>
      <p className="text-sm mb-8" style={{ color: '#55555E' }}>
        Do any of the following apply to your company? Check all that apply.
      </p>

      <div className="space-y-3">
        {EXCLUSION_ITEMS.map((item) => {
          const checked = data.complianceFlags.includes(item.id);
          return (
            <label
              key={item.id}
              className="flex items-start gap-3 cursor-pointer py-2"
            >
              <input
                type="checkbox"
                checked={checked}
                onChange={() => toggle(item.id)}
                className="mt-0.5 w-4 h-4 rounded cursor-pointer"
                style={{ accentColor: '#5266EB' }}
              />
              <span className="text-sm leading-relaxed" style={{ color: '#55555E' }}>{item.text}</span>
            </label>
          );
        })}
      </div>

      {/* Warning if any exclusion is checked */}
      {hasAnyChecked && (
        <div
          className="mt-4 p-3 rounded-lg text-sm"
          style={{ backgroundColor: '#FEF2F2', border: '1px solid #FECACA', color: '#E34850' }}
        >
          Based on your selections, your company may not be eligible for a FlowCard account.
        </div>
      )}

      <div className="mt-8 flex justify-center sm:justify-start">
        <button
          type="submit"
          disabled={hasAnyChecked}
          className="w-full sm:w-auto px-8 py-3 text-sm font-semibold text-white rounded-full transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          style={{ backgroundColor: '#5266EB' }}
          onMouseEnter={(e) => { if (!hasAnyChecked) e.currentTarget.style.backgroundColor = '#4355D0'; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = '#5266EB'; }}
          onMouseDown={(e) => { if (!hasAnyChecked) e.currentTarget.style.backgroundColor = '#3848B8'; }}
          onMouseUp={(e) => { if (!hasAnyChecked) e.currentTarget.style.backgroundColor = '#4355D0'; }}
        >
          None of the above →
        </button>
      </div>
      <div className="mt-3 text-center sm:text-left">
        <button
          type="button"
          onClick={onBack}
          className="text-sm font-medium transition-colors cursor-pointer"
          style={{ color: '#8E8E99' }}
          onMouseEnter={(e) => { e.currentTarget.style.color = '#55555E'; }}
          onMouseLeave={(e) => { e.currentTarget.style.color = '#8E8E99'; }}
        >
          Back
        </button>
      </div>
    </form>
  );
}
