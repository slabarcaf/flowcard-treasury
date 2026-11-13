import { useState } from 'react';

import { LandingPage } from './components/onboarding/LandingPage.js';
import { LoginPage } from './components/onboarding/LoginPage.js';
import { OnboardingWizard } from './components/onboarding/OnboardingWizard.js';
import { VersionToggle, type DemoVersion } from './components/VersionToggle.js';
import { BackOffice } from './features/bridge-capital/backoffice/index.js';
import { Portal } from './features/bridge-capital/portal/index.js';
import { GdDashboard } from './gd/GdDashboard.js';
import { Kit5Dashboard } from './kit5/Kit5Dashboard.js';
import { legacyVersionFromStorage, screenFromHash, type AppScreen } from './app-routing.js';

type Screen = AppScreen;

const STORAGE_KEY = 'flowcard.demoVersion';

function storedVersion(): DemoVersion {
  try {
    return legacyVersionFromStorage(localStorage.getItem(STORAGE_KEY));
  } catch {
    return 'kit5';
  }
}

function App() {
  const [screen, setScreen] = useState<Screen>(() => screenFromHash(window.location.hash));
  const [version, setVersion] = useState<DemoVersion>(storedVersion);

  const changeVersion = (nextVersion: DemoVersion) => {
    setVersion(nextVersion);
    try {
      localStorage.setItem(STORAGE_KEY, nextVersion);
    } catch {
      // Storage unavailable: the choice just will not be remembered.
    }
  };

  const versionToggle = <VersionToggle value={version} onChange={changeVersion} />;

  if (screen === 'landing') {
    return <LandingPage onGetStarted={() => setScreen('onboarding')} onLogin={() => setScreen('login')} />;
  }
  if (screen === 'login') {
    return <LoginPage onLogin={() => setScreen('dashboard')} onBack={() => setScreen('landing')} />;
  }
  if (screen === 'onboarding') {
    return <OnboardingWizard onComplete={() => setScreen('landing')} />;
  }
  if (screen === 'portal') {
    return <Portal onExit={() => setScreen('dashboard')} />;
  }
  if (screen === 'kit5') {
    return version === 'gd'
      ? <GdDashboard versionToggle={versionToggle} />
      : <Kit5Dashboard versionToggle={versionToggle} />;
  }
  return <BackOffice onExit={() => setScreen('portal')} exitLabel="Customer portal →" />;
}

export default App;
