export type AppScreen = 'landing' | 'login' | 'onboarding' | 'dashboard' | 'portal' | 'kit5';
export type LegacyDemoVersion = 'kit5' | 'gd';

const SCREEN_BY_HASH: Record<string, AppScreen> = {
  '#bridge-capital': 'dashboard',
  '#portal': 'portal',
  '#kit5': 'kit5',
};

export function screenFromHash(hash: string): AppScreen {
  return SCREEN_BY_HASH[hash] ?? 'landing';
}

export function legacyVersionFromStorage(value: string | null): LegacyDemoVersion {
  return value === 'gd' ? 'gd' : 'kit5';
}
