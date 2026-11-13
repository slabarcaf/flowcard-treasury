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

export function CreateAccountStep({ data, onUpdate, onNext }: Props) {
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

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2 text-white">Enter email address</h2>
      <p className="text-sm mb-8" style={{ color: '#8B95A5' }}>You'll need this email address to access your business account</p>

      <div className="space-y-4">
        <div>
          <input
            type="email"
            value={data.email}
            onChange={(e) => onUpdate({ email: e.target.value })}
            placeholder="Enter your email"
            className="revolut-input w-full"
            style={{
              borderColor: touched && !emailValid ? '#E34850' : undefined,
            }}
          />
          {touched && !emailValid && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>Please enter a valid email address</p>
          )}
        </div>

        <div className="relative">
          <input
            type={showPassword ? 'text' : 'password'}
            value={data.password}
            onChange={(e) => onUpdate({ password: e.target.value })}
            placeholder="Create a password"
            className="revolut-input w-full pr-11"
            style={{
              borderColor: touched && !passwordLongEnough ? '#E34850' : undefined,
            }}
          />
          <button
            type="button"
            onClick={() => setShowPassword(!showPassword)}
            className="absolute right-3 top-1/2 -translate-y-1/2 cursor-pointer"
            style={{ color: '#5A6577' }}
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

        {/* Password strength indicator */}
        <div className="flex items-center gap-1.5">
          {passwordLongEnough ? (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#22A06B" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="20 6 9 17 4 12" />
            </svg>
          ) : (
            <span className="w-[14px] h-[14px] inline-flex items-center justify-center">
              <span className="block w-1.5 h-1.5 rounded-full" style={{ backgroundColor: '#5A6577' }} />
            </span>
          )}
          <span className="text-xs" style={{ color: passwordLongEnough ? '#22A06B' : '#5A6577' }}>
            Minimum 8 characters
          </span>
        </div>

        {touched && !passwordLongEnough && (
          <p className="text-xs" style={{ color: '#E34850' }}>Password must be at least 8 characters</p>
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
