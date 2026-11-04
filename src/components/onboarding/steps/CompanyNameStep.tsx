import { useState } from 'react';
import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onUpdate: (patch: Partial<OnboardingData>) => void;
  onNext: () => void;
  onBack: () => void;
}

export function CompanyNameStep({ data, onUpdate, onNext }: Props) {
  const [touched, setTouched] = useState(false);

  const canProceed = data.companyName.trim() !== '';

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (canProceed) onNext();
  }

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2 text-white">What's your company called?</h2>
      <p className="text-sm mb-8" style={{ color: '#8B95A5' }}>
        This doesn't have to be your legal name — you'll add that later.
      </p>

      <div>
        <input
          type="text"
          value={data.companyName}
          onChange={(e) => onUpdate({ companyName: e.target.value })}
          placeholder="Company name"
          className="revolut-input w-full"
          style={{
            borderColor: touched && !data.companyName.trim() ? '#E34850' : undefined,
          }}
        />
        {touched && !data.companyName.trim() && (
          <p className="text-xs mt-1" style={{ color: '#E34850' }}>Company name is required</p>
        )}
      </div>

      <div className="mt-auto pt-10">
        <button
          type="submit"
          disabled={!canProceed && touched}
          className="revolut-btn-primary w-full"
        >
          Continue
        </button>
      </div>
    </form>
  );
}
