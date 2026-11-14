import { useState } from 'react';

import { LandingPage } from './components/onboarding/LandingPage.js';
import { LoginPage } from './components/onboarding/LoginPage.js';
import { OnboardingWizard } from './components/onboarding/OnboardingWizard.js';
import { BackOffice } from './features/bridge-capital/backoffice/index.js';
import { Portal } from './features/bridge-capital/portal/index.js';
import { screenFromHash, type AppScreen } from './app-routing.js';

type Screen = AppScreen;

function App() {
  const [screen, setScreen] = useState<Screen>(() => screenFromHash(window.location.hash));

  if (screen === 'landing') {
    return <LandingPage onGetStarted={() => setScreen('onboarding')} onLogin={() => setScreen('login')} />;
  }
  if (screen === 'login') {
    return <LoginPage onLogin={() => setScreen('dashboard')} onBack={() => setScreen('landing')} />;
  }
  if (screen === 'onboarding') {
    return <OnboardingWizard onComplete={() => setScreen('dashboard')} />;
  }
  if (screen === 'portal') {
    return <Portal onExit={() => setScreen('dashboard')} />;
  }
  return <BackOffice onExit={() => setScreen('portal')} exitLabel="Customer portal →" />;
}

export default App;
