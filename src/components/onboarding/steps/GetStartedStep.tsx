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
      <h2 className="text-2xl font-bold mb-2" style={{ color: '#25252D' }}>Get started</h2>
      <p className="text-sm mb-8" style={{ color: '#55555E' }}>Tell us about yourself to begin.</p>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>First name</label>
          <input
            type="text"
            value={data.firstName}
            onChange={(e) => onUpdate({ firstName: e.target.value })}
            placeholder="Jane"
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={{
              backgroundColor: '#F6F5FA',
              border: `1px solid ${touched && !data.firstName.trim() ? '#E34850' : '#E2E1E9'}`,
              color: '#25252D',
            }}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && !data.firstName.trim() ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && !data.firstName.trim() ? '#E34850' : '#E2E1E9'; }}
          />
          {touched && !data.firstName.trim() && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>First name is required</p>
          )}
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Last name</label>
          <input
            type="text"
            value={data.lastName}
            onChange={(e) => onUpdate({ lastName: e.target.value })}
            placeholder="Doe"
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={{
              backgroundColor: '#F6F5FA',
              border: `1px solid ${touched && !data.lastName.trim() ? '#E34850' : '#E2E1E9'}`,
              color: '#25252D',
            }}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && !data.lastName.trim() ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && !data.lastName.trim() ? '#E34850' : '#E2E1E9'; }}
          />
          {touched && !data.lastName.trim() && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>Last name is required</p>
          )}
        </div>
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
          Start Application
        </button>
      </div>
    </form>
  );
}
