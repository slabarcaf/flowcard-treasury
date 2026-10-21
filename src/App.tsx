import { useState } from 'react';
import { VersionToggle, type DemoVersion } from './components/VersionToggle.js';
import { GdDashboard } from './gd/GdDashboard.js';
import { Kit5Dashboard } from './kit5/Kit5Dashboard.js';

const STORAGE_KEY = 'flowcard.demoVersion';

function storedVersion(): DemoVersion {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'gd' ? 'gd' : 'kit5';
  } catch {
    return 'kit5';
  }
}

function App() {
  const [version, setVersion] = useState<DemoVersion>(storedVersion);

  const changeVersion = (v: DemoVersion) => {
    setVersion(v);
    try {
      localStorage.setItem(STORAGE_KEY, v);
    } catch {
      // Storage unavailable: the choice just won't be remembered.
    }
  };

  const toggle = <VersionToggle value={version} onChange={changeVersion} />;

  return version === 'gd' ? <GdDashboard versionToggle={toggle} /> : <Kit5Dashboard versionToggle={toggle} />;
}

export default App;
