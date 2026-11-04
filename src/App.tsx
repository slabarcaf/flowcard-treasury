import { useState } from 'react';
import { Kit5Dashboard } from './kit5/Kit5Dashboard.js';
import { OnboardingWizard } from './components/onboarding/OnboardingWizard.js';
import { BackOffice } from './features/bridge-capital/backoffice/index.js';
import { Portal } from './features/bridge-capital/portal/index.js';

/** Views the app can show. No router: a switch on state (see CLAUDE.md). */
type AppView = 'kit5' | 'bridge-capital' | 'portal';

const HASH_VIEWS: Record<string, AppView> = {
  '#bridge-capital': 'bridge-capital',
  '#portal': 'portal',
};

/** #bridge-capital and #portal open those views straight away, for demos. */
function initialView(): AppView {
  return HASH_VIEWS[window.location.hash] ?? 'kit5';
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

  if (view === 'portal') {
    return <Portal onExit={() => setView('kit5')} />;
  }

  return (
    <>
      <Kit5Dashboard />
      <div className="fixed bottom-4 right-4 z-50 flex gap-2">
        <button
          type="button"
          onClick={() => setView('portal')}
          className="px-4 py-2 text-sm font-medium bg-white text-[#2a2520] border border-[#dbd2c6] rounded-lg shadow-lg cursor-pointer hover:bg-[#faf7f4] transition-colors"
        >
          Customer portal →
        </button>
        <button
          type="button"
          onClick={() => setView('bridge-capital')}
          className="px-4 py-2 text-sm font-medium bg-[#16161a] text-white rounded-lg shadow-lg cursor-pointer hover:bg-[#2a2a31] transition-colors"
        >
          Bridge Capital back office →
        </button>
      </div>
    </>
  );
}

export default App;
