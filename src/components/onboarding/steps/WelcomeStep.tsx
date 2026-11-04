import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onComplete: () => void;
}

export function WelcomeStep({ data, onComplete }: Props) {
  return (
    <div className="animate-step-in text-center">
      <div
        className="mx-auto mb-6 w-16 h-16 rounded-2xl flex items-center justify-center"
        style={{ background: 'linear-gradient(135deg, #5266EB, #7C3AED)' }}
      >
        <svg className="w-8 h-8 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
      </div>

      <h2 className="text-2xl font-bold mb-2" style={{ color: '#25252D' }}>
        Welcome to FlowCard{data.firstName ? `, ${data.firstName}` : ''}!
      </h2>
      <p className="text-sm mb-2" style={{ color: '#55555E' }}>
        Your application for <span className="font-medium" style={{ color: '#25252D' }}>{data.companyName || 'your company'}</span> has been submitted.
      </p>
      <p className="text-sm mb-10" style={{ color: '#8E8E99' }}>
        You're all set to explore the FlowCard Treasury dashboard.
      </p>

      <button
        onClick={onComplete}
        className="w-full sm:w-auto px-8 py-3 text-sm font-semibold text-white rounded-full transition-colors cursor-pointer"
        style={{ backgroundColor: '#5266EB' }}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = '#4355D0'; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = '#5266EB'; }}
        onMouseDown={(e) => { e.currentTarget.style.backgroundColor = '#3848B8'; }}
        onMouseUp={(e) => { e.currentTarget.style.backgroundColor = '#4355D0'; }}
      >
        Go to Dashboard
      </button>
    </div>
  );
}
