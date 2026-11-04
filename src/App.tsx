import { useState } from 'react';
import { Kit5Dashboard } from './kit5/Kit5Dashboard.js';
import { LandingPage } from './components/onboarding/LandingPage.js';
import { LoginPage } from './components/onboarding/LoginPage.js';
import { OnboardingWizard } from './components/onboarding/OnboardingWizard.js';

function App() {
  const [screen, setScreen] = useState<'landing' | 'login' | 'onboarding' | 'dashboard'>('landing');

  if (screen === 'landing') {
    return (
      <LandingPage
        onGetStarted={() => setScreen('onboarding')}
        onLogin={() => setScreen('login')}
      />
    );
  }

  if (screen === 'login') {
    return (
      <LoginPage
        onLogin={() => setScreen('dashboard')}
        onBack={() => setScreen('landing')}
      />
    );
  }

  if (screen === 'onboarding') {
    return <OnboardingWizard onComplete={() => setScreen('dashboard')} />;
  }

  return <Kit5Dashboard />;
}

export default App;
