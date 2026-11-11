import { useState } from 'react';

import { LandingPage } from './components/onboarding/LandingPage.js';
import { LoginPage } from './components/onboarding/LoginPage.js';
import { OnboardingWizard } from './components/onboarding/OnboardingWizard.js';
import { BackOffice } from './features/bridge-capital/backoffice/index.js';
import { Portal } from './features/bridge-capital/portal/index.js';
import { Kit5Dashboard } from './kit5/Kit5Dashboard.js';

type Screen = 'landing' | 'login' | 'onboarding' | 'dashboard' | 'portal' | 'kit5';

function initialScreen(): Screen {
  switch (window.location.hash) {
    case '#bridge-capital': return 'dashboard';
    case '#portal': return 'portal';
    case '#kit5': return 'kit5';
    default: return 'landing';
  }
}

function App() {
  const [screen, setScreen] = useState<Screen>(initialScreen);

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
  if (screen === 'kit5') {
    return <Kit5Dashboard versionToggle={null} />;
  }
  return <BackOffice onExit={() => setScreen('portal')} exitLabel="Customer portal →" />;
}

export default App;
