import { useState } from 'react';
import { Kit5Dashboard } from './kit5/Kit5Dashboard.js';
import { OnboardingWizard } from './components/onboarding/OnboardingWizard.js';

function App() {
  const [onboardingComplete, setOnboardingComplete] = useState(false);

  if (!onboardingComplete) {
    return <OnboardingWizard onComplete={() => setOnboardingComplete(true)} />;
  }

  return <Kit5Dashboard />;
}

export default App;
