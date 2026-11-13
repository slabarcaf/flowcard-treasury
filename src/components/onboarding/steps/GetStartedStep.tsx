import { useState } from 'react';
import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onUpdate: (patch: Partial<OnboardingData>) => void;
  onNext: () => void;
}

export function GetStartedStep({ data, onUpdate, onNext }: Props) {
  const [touched, setTouched] = useState(false);

  const canProceed = data.firstName.trim() !== '' && data.lastName.trim() !== '';

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (canProceed) onNext();
  }

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2 text-white">Let's get started!</h2>
      <p className="text-sm mb-8" style={{ color: '#8B95A5' }}>Tell us about yourself to begin.</p>

      <div className="space-y-4">
        <div className="relative">
          <input
            type="text"
            value={data.firstName}
            onChange={(e) => onUpdate({ firstName: e.target.value })}
            placeholder="First name"
            className="revolut-input w-full"
            style={{
              borderColor: touched && !data.firstName.trim() ? '#E34850' : undefined,
            }}
          />
          {touched && !data.firstName.trim() && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>First name is required</p>
          )}
        </div>

        <div className="relative">
          <input
            type="text"
            value={data.lastName}
            onChange={(e) => onUpdate({ lastName: e.target.value })}
            placeholder="Last name"
            className="revolut-input w-full"
            style={{
              borderColor: touched && !data.lastName.trim() ? '#E34850' : undefined,
            }}
          />
          {touched && !data.lastName.trim() && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>Last name is required</p>
          )}
        </div>
      </div>

      <div className="mt-auto pt-10">
        <p className="text-center text-xs mb-4" style={{ color: '#5A6577' }}>
          By proceeding, you agree to our <span className="revolut-link">Terms of Service</span> and that you have read and understood our <span className="revolut-link">Privacy Policy</span>
        </p>
        <button type="submit" className="revolut-btn-primary w-full">
          Continue
        </button>
      </div>
    </form>
  );
}
