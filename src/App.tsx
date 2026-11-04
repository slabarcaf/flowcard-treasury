import { useState } from 'react';
import { Kit5Dashboard } from './kit5/Kit5Dashboard.js';
import { OnboardingWizard } from './components/onboarding/OnboardingWizard.js';
import { BackOffice } from './features/bridge-capital/backoffice/index.js';

/** Views the app can show. No router: a switch on state (see CLAUDE.md). */
type AppView = 'kit5' | 'bridge-capital';

/** #bridge-capital opens the back office straight away, for demos. */
function initialView(): AppView {
  return window.location.hash === '#bridge-capital' ? 'bridge-capital' : 'kit5';
}

function App() {
  const [view, setView] = useState<AppView>(initialView);
  const [onboardingComplete, setOnboardingComplete] = useState(() => initialView() !== 'kit5');

  if (!onboardingComplete) {
    return <OnboardingWizard onComplete={() => setOnboardingComplete(true)} />;
  }

  if (view === 'bridge-capital') {
    return <BackOffice onExit={() => setView('kit5')} />;
  }

  return (
    <>
      <Kit5Dashboard />
      <button
        type="button"
        onClick={() => setView('bridge-capital')}
        className="fixed bottom-4 right-4 z-50 px-4 py-2 text-sm font-medium bg-[#16161a] text-white rounded-lg shadow-lg cursor-pointer hover:bg-[#2a2a31] transition-colors"
      >
        Bridge Capital back office →
      </button>
    </>
  );
}

export default App;
