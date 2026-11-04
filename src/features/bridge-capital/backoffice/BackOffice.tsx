/**
 * Back-office shell: sidebar, header, and the current screen.
 *
 * Navigation is in-view state, not a router.
 */
import type { DataAdapter } from '../data/adapter.js';
import { currentRegime } from '../policy/regime.js';
import { REGIME_LABELS } from '../policy/policy.config.js';
import { REGIME_TONE, color, font, radius, space } from './tokens.js';
import { shortDate } from '../ui/format.js';
import { useBackOffice, type View } from './useBackOffice.js';
import { Badge } from './components/primitives.js';
import { OverviewScreen } from './screens/OverviewScreen.js';
import { RequestDetailScreen } from './screens/RequestDetailScreen.js';
import { BridgeDetailScreen } from './screens/BridgeDetailScreen.js';
import { CustomersScreen } from './screens/CustomersScreen.js';

const NAV: Array<{ label: string; view: View }> = [
  { label: 'Overview', view: { screen: 'overview' } },
  { label: 'Customers', view: { screen: 'customers' } },
];

const SCREEN_TITLE: Record<View['screen'], string> = {
  overview: 'Bridge Capital',
  customers: 'Customers',
  request: 'Request',
  bridge: 'Bridge',
};

export function BackOffice({
  adapter,
  onExit,
}: {
  adapter?: DataAdapter;
  onExit?: () => void;
}) {
  const office = useBackOffice(adapter);
  const { view, go, data } = office;
  const regime = data === null ? null : currentRegime(data.capital);

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        background: color.bg,
        color: color.text,
        fontFamily: font.sans,
        fontSize: font.size.md,
      }}
    >
      {/* Sidebar */}
      <aside
        style={{
          width: '216px',
          flexShrink: 0,
          background: color.sidebar,
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        <div style={{ padding: `${space['2xl']} ${space.xl}`, display: 'grid', gap: space.xs }}>
          <div style={{ color: color.sidebarTextActive, fontSize: font.size.md, fontWeight: font.weight.semibold }}>
            FlowCard
          </div>
          <div style={{ color: color.sidebarText, fontSize: font.size.xs }}>Bridge Capital · Back office</div>
        </div>

        <nav style={{ padding: `0 ${space.md}`, display: 'grid', gap: '2px', alignContent: 'start', flex: 1 }}>
          {NAV.map((item) => {
            const active = item.view.screen === view.screen;
            return (
              <button
                key={item.label}
                type="button"
                onClick={() => go(item.view)}
                style={{
                  width: '100%',
                  textAlign: 'left',
                  background: active ? color.sidebarActiveBg : 'transparent',
                  color: active ? color.sidebarTextActive : color.sidebarText,
                  border: 'none',
                  borderRadius: radius.md,
                  padding: `${space.md} ${space.lg}`,
                  fontSize: font.size.md,
                  fontFamily: font.sans,
                  fontWeight: active ? font.weight.medium : font.weight.regular,
                  cursor: 'pointer',
                }}
              >
                {item.label}
              </button>
            );
          })}
        </nav>

        {onExit !== undefined && (
          <div style={{ padding: space.md, borderTop: '1px solid rgba(255,255,255,0.08)' }}>
            <button
              type="button"
              onClick={onExit}
              style={{
                width: '100%',
                textAlign: 'left',
                background: 'transparent',
                color: color.sidebarText,
                border: 'none',
                borderRadius: radius.md,
                padding: `${space.md} ${space.lg}`,
                fontSize: font.size.sm,
                fontFamily: font.sans,
                cursor: 'pointer',
              }}
            >
              ← Back to Kit 5 dashboard
            </button>
          </div>
        )}
      </aside>

      {/* Main */}
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        <header
          style={{
            background: color.surface,
            borderBottom: `1px solid ${color.border}`,
            padding: `${space.lg} ${space['3xl']}`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: space.lg,
          }}
        >
          <h1 style={{ margin: 0, fontSize: font.size.lg, fontWeight: font.weight.semibold, letterSpacing: '-0.01em' }}>
            {SCREEN_TITLE[view.screen]}
          </h1>
          <div style={{ display: 'flex', alignItems: 'center', gap: space.lg }}>
            {regime !== null && <Badge tone={REGIME_TONE[regime]}>{REGIME_LABELS[regime]}</Badge>}
            <span style={{ fontSize: font.size.sm, color: color.textMuted }}>
              as of {shortDate(office.today)}
            </span>
          </div>
        </header>

        <main style={{ flex: 1, minWidth: 0, padding: space['3xl'], overflowY: 'auto' }}>
          {view.screen === 'overview' && <OverviewScreen office={office} />}
          {view.screen === 'customers' && <CustomersScreen office={office} />}
          {view.screen === 'request' && (
            <RequestDetailScreen office={office} requestId={view.requestId} />
          )}
          {view.screen === 'bridge' && <BridgeDetailScreen office={office} bridgeId={view.bridgeId} />}
        </main>
      </div>
    </div>
  );
}
