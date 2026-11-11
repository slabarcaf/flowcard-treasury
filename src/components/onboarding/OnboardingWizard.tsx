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
    <div className="min-h-screen flex flex-col relative" style={{ backgroundColor: '#0D1421' }}>
      <AuroraBackground />

      {/* Content */}
      <main className="flex-1 flex flex-col items-center justify-between px-4 pt-8 pb-8 relative z-10 min-h-screen">
        <div className="w-full max-w-[540px]">
          {/* Back button */}
          {currentStep > 1 && currentStep < 6 && (
            <button
              type="button"
              onClick={prevStep}
              className="mb-6 text-white/70 hover:text-white transition-colors cursor-pointer"
              aria-label="Go back"
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M19 12H5" />
                <polyline points="12 19 5 12 12 5" />
              </svg>
            </button>
          )}

          {renderStep()}
        </div>
      </main>
    </div>
  );
}
