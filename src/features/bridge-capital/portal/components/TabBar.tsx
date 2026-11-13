/** The portal's four destinations, as a fixed bottom bar. */
import type { ReactNode } from 'react';
import { color, font, layout, space } from '../tokens.js';

export type TabId = 'wallet' | 'activity' | 'bridges' | 'account';

function Icon({ path }: { path: ReactNode }) {
  return (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      {path}
    </svg>
  );
}

const ICONS: Record<TabId, ReactNode> = {
  wallet: (
    <>
      <rect x="3" y="6" width="18" height="13" rx="3" stroke="currentColor" strokeWidth="1.6" />
      <path d="M3 10h18" stroke="currentColor" strokeWidth="1.6" />
      <circle cx="17" cy="14.5" r="1.2" fill="currentColor" />
    </>
  ),
  activity: (
    <>
      <path d="M4 15.5 9 10l3.5 3.5L20 6" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M15 6h5v5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </>
  ),
  bridges: (
    <>
      <path d="M3 15c3.5 0 4-7 9-7s5.5 7 9 7" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      <path d="M3 19V9M21 19V9M12 19v-6" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </>
  ),
  account: (
    <>
      <circle cx="12" cy="8.5" r="3.5" stroke="currentColor" strokeWidth="1.6" />
      <path d="M4.5 19.5c1.2-3.4 4-5 7.5-5s6.3 1.6 7.5 5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </>
  ),
};

const TABS: Array<{ id: TabId; label: string }> = [
  { id: 'wallet', label: 'Home' },
  { id: 'activity', label: 'Activity' },
  { id: 'bridges', label: 'Bridges' },
  { id: 'account', label: 'Account' },
];

/** Height the main column leaves clear so the bar never covers content. */
export const TAB_BAR_HEIGHT = '76px';

export function TabBar({ active, onSelect }: { active: TabId; onSelect: (tab: TabId) => void }) {
  return (
    <nav
      style={{
        position: 'fixed',
        left: 0,
        right: 0,
        bottom: 0,
        background: color.bg,
        boxShadow: `inset 0 1px 0 ${color.border}`,
        padding: `${space.md} ${layout.gutter} ${space.xl}`,
      }}
    >
      <div
        style={{
          maxWidth: layout.maxWidth,
          margin: '0 auto',
          display: 'grid',
          gridTemplateColumns: `repeat(${TABS.length}, 1fr)`,
        }}
      >
        {TABS.map((tab) => {
          const selected = tab.id === active;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => onSelect(tab.id)}
              aria-current={selected ? 'page' : undefined}
              style={{
                display: 'grid',
                justifyItems: 'center',
                gap: space.xs,
                background: 'none',
                border: 'none',
                padding: space.sm,
                cursor: 'pointer',
                fontFamily: font.sans,
                fontSize: font.size.sm,
                color: selected ? color.accent : color.textMuted,
              }}
            >
              <Icon path={ICONS[tab.id]} />
              {tab.label}
            </button>
          );
        })}
      </div>
    </nav>
  );
}
