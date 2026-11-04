import { useOnboarding } from '../../hooks/useOnboarding.js';
import { AuroraBackground } from './AuroraBackground.js';
import { GetStartedStep } from './steps/GetStartedStep.js';
import { CreateAccountStep } from './steps/CreateAccountStep.js';
import { CompanyNameStep } from './steps/CompanyNameStep.js';
import { ComplianceStep } from './steps/ComplianceStep.js';
import { CompanyInfoStep } from './steps/CompanyInfoStep.js';
import { WelcomeStep } from './steps/WelcomeStep.js';

interface Props {
  onComplete: () => void;
}

export function OnboardingWizard({ onComplete }: Props) {
  const { state, updateData, nextStep, prevStep } = useOnboarding();
  const { currentStep, data } = state;

  // Phase A: steps 1-2 and 6 (lavender bg + card)
  // Phase B: steps 3-5 (white bg, no card, progress bar)
  const isPhaseA = currentStep <= 2 || currentStep === 6;
  const isWelcome = currentStep === 6;
  const showProgressBar = currentStep >= 3 && currentStep <= 5;

  // Progress for steps 3-5 only: 33% / 66% / 100%
  const progressPct = showProgressBar ? ((currentStep - 2) / 3) * 100 : 0;

  function renderStep() {
    switch (currentStep) {
      case 1:
        return <GetStartedStep data={data} onUpdate={updateData} onNext={nextStep} />;
      case 2:
        return <CreateAccountStep data={data} onUpdate={updateData} onNext={nextStep} onBack={prevStep} />;
      case 3:
        return <CompanyNameStep data={data} onUpdate={updateData} onNext={nextStep} onBack={prevStep} />;
      case 4:
        return <ComplianceStep data={data} onUpdate={updateData} onNext={nextStep} onBack={prevStep} />;
      case 5:
        return <CompanyInfoStep data={data} onUpdate={updateData} onNext={nextStep} onBack={prevStep} />;
      case 6:
        return <WelcomeStep data={data} onComplete={onComplete} />;
      default:
        return null;
    }
  }

  return (
    <div
      className="min-h-screen flex flex-col relative"
      style={{ backgroundColor: isPhaseA ? '#EEEDF5' : '#ffffff' }}
    >
      <AuroraBackground />

      {/* Header */}
      <header
        className="px-6 py-3 flex items-center justify-between shrink-0 relative z-10"
        style={{
          backgroundColor: isPhaseA ? 'transparent' : '#ffffff',
          borderBottom: isPhaseA ? 'none' : '1px solid #E2E1E9',
        }}
      >
        <div className="flex items-center gap-3">
          <div
            className="w-8 h-8 rounded-lg flex items-center justify-center"
            style={{ background: 'linear-gradient(135deg, #5266EB, #7C3AED)' }}
          >
            <span className="text-white text-xs font-bold">FC</span>
          </div>
          {!isPhaseA && (
            <span className="text-sm font-semibold" style={{ color: '#25252D' }}>FlowCard</span>
          )}
        </div>
        {currentStep <= 2 && !isWelcome && (
          <button
            type="button"
            className="text-sm font-medium transition-colors hover:underline cursor-pointer"
            style={{ color: '#5266EB' }}
          >
            Log in
          </button>
        )}
        {currentStep >= 3 && currentStep <= 5 && data.firstName && (
          <span className="text-sm" style={{ color: '#55555E' }}>
            {data.firstName} {data.lastName}
          </span>
        )}
      </header>

      {/* Progress bar — Phase B only */}
      {showProgressBar && (
        <div className="h-[3px] relative z-10" style={{ backgroundColor: '#E2E1E9' }}>
          <div
            className="h-full transition-all duration-500 ease-out"
            style={{ width: `${progressPct}%`, backgroundColor: '#5266EB' }}
          />
        </div>
      )}

      {/* Content */}
      <main className="flex-1 flex items-start justify-center px-4 pt-12 pb-12 relative z-10">
        {isPhaseA ? (
          /* Phase A: card wrapper */
          <div
            className="w-full max-w-md bg-white rounded-xl shadow-sm p-8"
            style={{ border: '1px solid #E2E1E9' }}
          >
            {renderStep()}
          </div>
        ) : (
          /* Phase B: no card, centered column */
          <div className="w-full max-w-xl">
            {renderStep()}
          </div>
        )}
      </main>
    </div>
  );
}
