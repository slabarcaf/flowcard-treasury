import { useState } from 'react';
import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onUpdate: (patch: Partial<OnboardingData>) => void;
  onNext: () => void;
  onBack: () => void;
}

export function CompanyNameStep({ data, onUpdate, onNext, onBack }: Props) {
  const [touched, setTouched] = useState(false);

  const canProceed = data.companyName.trim() !== '';

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (canProceed) onNext();
  }

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2" style={{ color: '#25252D' }}>What's your company called?</h2>
      <p className="text-sm mb-8" style={{ color: '#55555E' }}>
        This doesn't have to be your legal name — you'll add that later.
      </p>

      <div>
        <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Company name</label>
        <input
          type="text"
          value={data.companyName}
          onChange={(e) => onUpdate({ companyName: e.target.value })}
          placeholder="Acme Inc."
          className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
          style={{
            backgroundColor: '#F6F5FA',
            border: `1px solid ${touched && !data.companyName.trim() ? '#E34850' : '#E2E1E9'}`,
            color: '#25252D',
          }}
          onFocus={(e) => { e.currentTarget.style.borderColor = touched && !data.companyName.trim() ? '#E34850' : '#5266EB'; }}
          onBlur={(e) => { e.currentTarget.style.borderColor = touched && !data.companyName.trim() ? '#E34850' : '#E2E1E9'; }}
        />
        {touched && !data.companyName.trim() && (
          <p className="text-xs mt-1" style={{ color: '#E34850' }}>Company name is required</p>
        )}
      </div>

      <div className="mt-8 flex justify-center sm:justify-start">
        <button
          type="submit"
          className="w-full sm:w-auto px-8 py-3 text-sm font-semibold text-white rounded-full transition-colors cursor-pointer"
          style={{ backgroundColor: '#5266EB' }}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = '#4355D0'; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = '#5266EB'; }}
          onMouseDown={(e) => { e.currentTarget.style.backgroundColor = '#3848B8'; }}
          onMouseUp={(e) => { e.currentTarget.style.backgroundColor = '#4355D0'; }}
        >
          Next →
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
