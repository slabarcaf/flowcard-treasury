/**
 * Customer portal shell: one phone-width column, a customer picker for demos,
 * and the current screen. Navigation is state, not a router.
 */
import type { DataAdapter } from '../data/adapter.js';
import type { PortalScreen } from './usePortal.js';
import { color, font, glow, layout, radius, space } from './tokens.js';
import { GlowBackground } from '../ui/Surface.js';
import { usePortal } from './usePortal.js';
import { TabBar, TAB_BAR_HEIGHT, type TabId } from './components/TabBar.js';
import { WalletHome } from './screens/WalletHome.js';
import { ActivityScreen } from './screens/ActivityScreen.js';
import { BridgesScreen } from './screens/BridgesScreen.js';
import { AccountScreen } from './screens/AccountScreen.js';
import { TransactionScreen } from './screens/TransactionScreen.js';
import { DeclineScreen } from './screens/DeclineScreen.js';
import { AcceptedScreen } from './screens/AcceptedScreen.js';
import { ActiveBridgeScreen } from './screens/ActiveBridgeScreen.js';

/** Which tab a screen belongs to. The task screens keep the bar hidden. */
const TAB_FOR_SCREEN: Partial<Record<PortalScreen['screen'], TabId>> = {
  wallet: 'wallet',
  activity: 'activity',
  bridges: 'bridges',
  bridge: 'bridges',
  account: 'account',
};

export function Portal({ adapter, onExit }: { adapter?: DataAdapter; onExit?: () => void }) {
  const portal = usePortal(adapter);
  const { data, customer, screen, selectCustomer, go } = portal;
  const tab = TAB_FOR_SCREEN[screen.screen];

  return (
    <GlowBackground
      canvas={color.bg}
      glow={glow}
      style={{
        color: color.text,
        fontFamily: font.sans,
        fontSize: font.size.lg,
      }}
    >
      <header
        style={{
          boxShadow: `inset 0 -1px 0 ${color.border}`,
          padding: `${space.xl} ${layout.gutter}`,
        }}
      >
        <div
          style={{
            maxWidth: layout.maxWidth,
            margin: '0 auto',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: space.lg,
          }}
        >
          <span style={{ fontSize: font.size.lg, fontWeight: font.weight.semibold }}>FlowCard</span>

          <div style={{ display: 'flex', alignItems: 'center', gap: space.md }}>
            {data !== null && customer !== null && (
              <select
                value={customer.id}
                onChange={(e) => selectCustomer(e.target.value)}
                aria-label="Signed in as"
                style={{
                  background: color.surfaceSubtle,
                  border: 'none',
                  borderRadius: radius.pill,
                  padding: `${space.md} ${space.lg}`,
                  fontSize: font.size.md,
                  fontFamily: font.sans,
                  color: color.text,
                  cursor: 'pointer',
                  maxWidth: '180px',
                }}
              >
                {data.customers.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            )}
            {onExit !== undefined && (
              <button
                type="button"
                onClick={onExit}
                style={{
                  background: 'none',
                  border: 'none',
                  padding: space.sm,
                  color: color.textMuted,
                  fontSize: font.size.md,
                  fontFamily: font.sans,
                  cursor: 'pointer',
                }}
              >
                Exit
              </button>
            )}
          </div>
        </div>
      </header>

      <main
        style={{
          maxWidth: layout.maxWidth,
          margin: '0 auto',
          padding: `${space['3xl']} ${layout.gutter} ${space['4xl']}`,
          paddingBottom: tab === undefined ? space['4xl'] : `calc(${TAB_BAR_HEIGHT} + ${space['3xl']})`,
        }}
      >
        {data === null || customer === null ? (
          <div style={{ color: color.textMuted, textAlign: 'center', padding: space['4xl'] }}>Loading…</div>
        ) : (
          <>
            {screen.screen === 'wallet' && <WalletHome portal={portal} />}
            {screen.screen === 'activity' && <ActivityScreen portal={portal} />}
            {screen.screen === 'bridges' && <BridgesScreen portal={portal} />}
            {screen.screen === 'account' && <AccountScreen portal={portal} />}
            {screen.screen === 'transaction' && (
              <TransactionScreen portal={portal} transactionId={screen.transactionId} />
            )}
            {screen.screen === 'decline' && (
              <DeclineScreen portal={portal} transactionId={screen.transactionId} />
            )}
            {screen.screen === 'accepted' && (
              <AcceptedScreen portal={portal} transactionId={screen.transactionId} />
            )}
            {screen.screen === 'bridge' && (
              <ActiveBridgeScreen portal={portal} bridgeId={screen.bridgeId} />
            )}
          </>
        )}
      </main>

      {tab !== undefined && (
        <TabBar active={tab} onSelect={(next) => go({ screen: next })} />
      )}
    </GlowBackground>
  );
}
