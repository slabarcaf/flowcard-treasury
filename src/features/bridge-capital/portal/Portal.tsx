/**
 * Customer portal shell: a desktop sidebar, a customer picker for demos, and
 * the current screen. Navigation is state, not a router. Below 860px the
 * sidebar becomes a scrolling row above the content.
 */
import type { DataAdapter } from '../data/adapter.js';
import type { PortalScreen } from './usePortal.js';
import { color, font, glow, radius, space } from './tokens.js';
import { GlowBackground } from '../ui/Surface.js';
import { usePortal } from './usePortal.js';
import { PORTAL_CSS } from './components/styles.js';
import { PageHeader } from './components/layout.js';
import { HomeScreen } from './screens/HomeScreen.js';
import { CashFlowScreen } from './screens/CashFlowScreen.js';
import { BridgesScreen } from './screens/BridgesScreen.js';
import { BillsScreen } from './screens/BillsScreen.js';
import { ReceivablesScreen } from './screens/ReceivablesScreen.js';
import { CardsScreen } from './screens/CardsScreen.js';
import { SpendScreen } from './screens/SpendScreen.js';
import { StatementsScreen } from './screens/StatementsScreen.js';
import { ActivityScreen } from './screens/ActivityScreen.js';
import { AccountScreen } from './screens/AccountScreen.js';
import { TransactionScreen } from './screens/TransactionScreen.js';
import { DeclineScreen } from './screens/DeclineScreen.js';
import { AcceptedScreen } from './screens/AcceptedScreen.js';
import { ActiveBridgeScreen } from './screens/ActiveBridgeScreen.js';
import { PastBridgeScreen } from './screens/PastBridgeScreen.js';
import { OfferScreen } from './screens/OfferScreen.js';
import { openBridges } from './usePortal.js';
import { applyDelinquency } from '../policy/delinquency.js';

type NavId =
  | 'wallet'
  | 'cashflow'
  | 'bridges'
  | 'bills'
  | 'receivables'
  | 'cards'
  | 'spend'
  | 'statements'
  | 'activity'
  | 'account';

const NAV: Array<{ id: NavId; label: string }> = [
  { id: 'wallet', label: 'Home' },
  { id: 'cashflow', label: 'Cash flow' },
  { id: 'bridges', label: 'Bridges' },
  { id: 'bills', label: 'Bills' },
  { id: 'receivables', label: 'Receivables' },
  { id: 'cards', label: 'Cards' },
  { id: 'spend', label: 'Spend' },
  { id: 'statements', label: 'Statements' },
  { id: 'activity', label: 'Activity' },
  { id: 'account', label: 'Account' },
];

/** Which sidebar item a screen sits under. */
const NAV_FOR: Record<PortalScreen['screen'], NavId> = {
  wallet: 'wallet',
  cashflow: 'cashflow',
  bridges: 'bridges',
  bills: 'bills',
  receivables: 'receivables',
  cards: 'cards',
  spend: 'spend',
  statements: 'statements',
  activity: 'activity',
  account: 'account',
  transaction: 'activity',
  decline: 'wallet',
  accepted: 'wallet',
  bridge: 'bridges',
  pastBridge: 'bridges',
  offer: 'bridges',
};

/** Screens from the original phone layout, kept in a reading-width column. */
const NARROW: PortalScreen['screen'][] = ['activity', 'account', 'transaction', 'decline', 'accepted'];

export function Portal({ adapter, onExit }: { adapter?: DataAdapter; onExit?: () => void }) {
  const portal = usePortal(adapter);
  const { data, customer, screen, selectCustomer, go, today, myBridges, notice, clearNotice } = portal;
  const active = NAV_FOR[screen.screen];
  const overdue = openBridges(myBridges).some((b) => applyDelinquency(b, today).days_past_due > 0);

  return (
    <GlowBackground canvas={color.bg} glow={glow} style={{ color: color.text, fontFamily: font.sans, fontSize: font.size.lg }}>
      <style>{PORTAL_CSS}</style>
      <header style={{ boxShadow: `inset 0 -1px 0 ${color.border}`, padding: `${space.xl} max(16px, env(safe-area-inset-right, 0px)) ${space.xl} max(16px, env(safe-area-inset-left, 0px))` }}>
        <div style={{ maxWidth: '1320px', margin: '0 auto', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg, flexWrap: 'wrap' }}>
          <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: space.md }}>
            <span style={{ fontSize: font.size.xl, fontWeight: font.weight.semibold, letterSpacing: '-0.02em' }}>FlowCard</span>
            <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Business</span>
          </span>
          <div style={{ display: 'flex', alignItems: 'center', gap: space.md }}>
            {data !== null && customer !== null && (
              <label style={{ display: 'inline-flex', alignItems: 'center', gap: space.md, fontSize: font.size.sm, color: color.textMuted }}>
                Viewing as
                <select
                  value={customer.id}
                  onChange={(e) => selectCustomer(e.target.value)}
                  style={{
                    background: color.surfaceSubtle,
                    border: 'none',
                    borderRadius: radius.pill,
                    padding: `${space.md} ${space.lg}`,
                    fontSize: font.size.md,
                    fontFamily: font.sans,
                    color: color.text,
                    cursor: 'pointer',
                  }}
                >
                  {data.customers.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {onExit !== undefined && (
              <button
                type="button"
                onClick={onExit}
                style={{ background: 'none', border: 'none', padding: space.sm, color: color.textMuted, fontSize: font.size.md, fontFamily: font.sans, cursor: 'pointer' }}
              >
                Exit
              </button>
            )}
          </div>
        </div>
      </header>

      <div className="bcp-shell">
        <nav className="bcp-nav" aria-label="Customer portal">
          {customer !== null && (
            <span className="bcp-nav-label" style={{ fontSize: font.size.xs, letterSpacing: '0.08em', textTransform: 'uppercase', color: color.textMuted, padding: `0 ${space.lg} ${space.md}` }}>
              {customer.name}
            </span>
          )}
          {NAV.map((item) => {
            const on = item.id === active;
            return (
              <button
                key={item.id}
                type="button"
                aria-current={on ? 'page' : undefined}
                onClick={() => go({ screen: item.id })}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: space.md,
                  textAlign: 'left',
                  background: on ? color.surfaceHover : 'transparent',
                  color: on ? color.text : color.textSecondary,
                  border: 'none',
                  borderRadius: radius.lg,
                  padding: `${space.lg} ${space.lg}`,
                  fontSize: font.size.lg,
                  fontFamily: font.sans,
                  fontWeight: on ? font.weight.medium : font.weight.regular,
                  cursor: 'pointer',
                }}
              >
                {item.label}
                {item.id === 'bridges' && overdue && (
                  <span aria-label="overdue" style={{ width: '7px', height: '7px', borderRadius: radius.pill, background: color.critical, marginLeft: 'auto' }} />
                )}
              </button>
            );
          })}
        </nav>

        <main style={{ minWidth: 0, display: 'grid', gap: space['2xl'], alignContent: 'start' }}>
          {notice !== null && (
            <div
              role="status"
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                gap: space.lg,
                background: color.positiveSubtle,
                color: color.positive,
                borderRadius: radius.xl,
                padding: `${space.lg} ${space['2xl']}`,
                fontSize: font.size.md,
                fontWeight: font.weight.medium,
              }}
            >
              {notice}
              <button type="button" onClick={clearNotice} aria-label="Dismiss" style={{ background: 'none', border: 'none', color: color.positive, cursor: 'pointer', fontSize: font.size.lg }}>
                ×
              </button>
            </div>
          )}
          {data === null || customer === null ? (
            <div style={{ color: color.textMuted, textAlign: 'center', padding: space['4xl'] }}>Loading…</div>
          ) : (
            <div className={NARROW.includes(screen.screen) ? 'bcp-narrow' : undefined} style={{ display: 'grid', gap: space['2xl'] }}>
              {screen.screen === 'wallet' && <HomeScreen portal={portal} />}
              {screen.screen === 'cashflow' && <CashFlowScreen portal={portal} />}
              {screen.screen === 'bridges' && <BridgesScreen portal={portal} />}
              {screen.screen === 'bills' && <BillsScreen portal={portal} />}
              {screen.screen === 'receivables' && <ReceivablesScreen portal={portal} />}
              {screen.screen === 'cards' && <CardsScreen portal={portal} />}
              {screen.screen === 'spend' && <SpendScreen portal={portal} />}
              {screen.screen === 'statements' && <StatementsScreen portal={portal} />}
              {screen.screen === 'activity' && (
                <>
                  <PageHeader title="Activity" sub="Everything that moved money in your wallet." />
                  <ActivityScreen portal={portal} />
                </>
              )}
              {screen.screen === 'account' && (
                <>
                  <PageHeader title="Account" sub="Your bridge limit and what sets it." />
                  <AccountScreen portal={portal} />
                </>
              )}
              {screen.screen === 'transaction' && <TransactionScreen portal={portal} transactionId={screen.transactionId} />}
              {screen.screen === 'decline' && <DeclineScreen portal={portal} transactionId={screen.transactionId} />}
              {screen.screen === 'accepted' && <AcceptedScreen portal={portal} transactionId={screen.transactionId} />}
              {screen.screen === 'bridge' && <ActiveBridgeScreen portal={portal} bridgeId={screen.bridgeId} />}
              {screen.screen === 'pastBridge' && <PastBridgeScreen portal={portal} pastBridgeId={screen.pastBridgeId} />}
              {screen.screen === 'offer' && <OfferScreen portal={portal} requestId={screen.requestId} />}
            </div>
          )}
        </main>
      </div>
    </GlowBackground>
  );
}
