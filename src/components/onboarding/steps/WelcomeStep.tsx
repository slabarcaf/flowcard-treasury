import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onComplete: () => void;
}

export function WelcomeStep({ data, onComplete }: Props) {
  return (
    <div className="animate-step-in text-center pt-16">
      {/* Success icon */}
      <div
        className="mx-auto mb-6 w-16 h-16 rounded-full flex items-center justify-center"
        style={{ backgroundColor: 'rgba(34,160,107,0.15)' }}
      >
        <svg className="w-8 h-8" fill="none" viewBox="0 0 24 24" stroke="#22A06B" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
      </div>

      <h2 className="text-2xl font-bold mb-3 text-white">
        Welcome to FlowCard{data.firstName ? `, ${data.firstName}` : ''}!
      </h2>
      <p className="text-sm mb-2" style={{ color: '#8B95A5' }}>
        Your application for <span className="font-medium text-white">{data.companyName || 'your company'}</span> has been submitted.
      </p>
      <p className="text-sm mb-12" style={{ color: '#5A6577' }}>
        You're all set to explore the FlowCard Treasury dashboard.
      </p>

      <button onClick={onComplete} className="revolut-btn-primary w-full max-w-xs mx-auto">
        Go to Dashboard
      </button>
    </div>
  );
}
