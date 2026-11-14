export type AppScreen = 'landing' | 'login' | 'onboarding' | 'dashboard' | 'portal';

const SCREEN_BY_HASH: Record<string, AppScreen> = {
  '#bridge-capital': 'dashboard',
  '#login': 'login',
  '#onboarding': 'onboarding',
  '#portal': 'portal',
};

export function screenFromHash(hash: string): AppScreen {
  return SCREEN_BY_HASH[hash] ?? 'landing';
}
