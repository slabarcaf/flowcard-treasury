import { useState } from 'react';
import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onUpdate: (patch: Partial<OnboardingData>) => void;
  onNext: () => void;
  onBack: () => void;
}

function validateEmail(email: string) {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email);
}

export function CreateAccountStep({ data, onUpdate, onNext, onBack }: Props) {
  const [touched, setTouched] = useState(false);
  const [showPassword, setShowPassword] = useState(false);

  const emailValid = validateEmail(data.email);
  const passwordLongEnough = data.password.length >= 8;
  const canProceed = emailValid && passwordLongEnough;

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (canProceed) onNext();
  }

  const inputStyle = (isError: boolean) => ({
    backgroundColor: '#F6F5FA',
    border: `1px solid ${isError ? '#E34850' : '#E2E1E9'}`,
    color: '#25252D',
  });

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2" style={{ color: '#25252D' }}>Create your account</h2>
      <p className="text-sm mb-8" style={{ color: '#55555E' }}>Use your work email to set up your FlowCard account.</p>

      <div className="space-y-5">
        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Work email</label>
          <input
            type="email"
            value={data.email}
            onChange={(e) => onUpdate({ email: e.target.value })}
            placeholder="jane@company.com"
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={inputStyle(touched && !emailValid)}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && !emailValid ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && !emailValid ? '#E34850' : '#E2E1E9'; }}
          />
          {touched && !emailValid && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>Please enter a valid email address</p>
          )}
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Password</label>
          <div className="relative">
            <input
              type={showPassword ? 'text' : 'password'}
              value={data.password}
              onChange={(e) => onUpdate({ password: e.target.value })}
              placeholder="At least 8 characters"
              className="w-full px-4 py-3 pr-11 rounded-lg text-sm outline-none transition-colors"
              style={inputStyle(touched && !passwordLongEnough)}
              onFocus={(e) => { e.currentTarget.style.borderColor = touched && !passwordLongEnough ? '#E34850' : '#5266EB'; }}
              onBlur={(e) => { e.currentTarget.style.borderColor = touched && !passwordLongEnough ? '#E34850' : '#E2E1E9'; }}
            />
            <button
              type="button"
              onClick={() => setShowPassword(!showPassword)}
              className="absolute right-3 top-1/2 -translate-y-1/2 cursor-pointer"
              style={{ color: '#8E8E99' }}
              tabIndex={-1}
            >
              {showPassword ? (
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
                  <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
                  <line x1="1" y1="1" x2="23" y2="23" />
                </svg>
              ) : (
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                  <circle cx="12" cy="12" r="3" />
                </svg>
              )}
            </button>
          </div>

          {/* Single-line Mercury-style validation */}
          <div className="flex items-center gap-1.5 mt-2">
            {passwordLongEnough ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#22A06B" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="20 6 9 17 4 12" />
              </svg>
            ) : (
              <span className="w-[14px] h-[14px] inline-flex items-center justify-center">
                <span className="block w-1.5 h-1.5 rounded-full" style={{ backgroundColor: '#8E8E99' }} />
              </span>
            )}
            <span
              className="text-xs"
              style={{ color: passwordLongEnough ? '#22A06B' : '#8E8E99' }}
            >
              Minimum 8 characters
            </span>
          </div>

          {touched && !passwordLongEnough && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>Password must be at least 8 characters</p>
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
          Continue
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
