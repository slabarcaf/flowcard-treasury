/**
 * Customer portal shell: one phone-width column, a customer picker for demos,
 * and the current screen. Navigation is state, not a router.
 */
import type { DataAdapter } from '../data/adapter.js';
import { color, font, layout, radius, space } from './tokens.js';
import { usePortal } from './usePortal.js';
import { WalletHome } from './screens/WalletHome.js';
import { DeclineScreen } from './screens/DeclineScreen.js';
import { AcceptedScreen } from './screens/AcceptedScreen.js';
import { ActiveBridgeScreen } from './screens/ActiveBridgeScreen.js';

export function Portal({ adapter, onExit }: { adapter?: DataAdapter; onExit?: () => void }) {
  const portal = usePortal(adapter);
  const { data, customer, screen, selectCustomer } = portal;

  return (
    <div
      style={{
        minHeight: '100vh',
        background: color.bg,
        color: color.text,
        fontFamily: font.sans,
        fontSize: font.size.lg,
      }}
    >
      <header
        style={{
          background: color.surface,
          borderBottom: `1px solid ${color.border}`,
          padding: `${space.lg} ${layout.gutter}`,
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
                  border: `1px solid ${color.border}`,
                  borderRadius: radius.pill,
                  padding: `${space.sm} ${space.lg}`,
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
        }}
      >
        {data === null || customer === null ? (
          <div style={{ color: color.textMuted, textAlign: 'center', padding: space['4xl'] }}>Loading…</div>
        ) : (
          <>
            {screen.screen === 'wallet' && <WalletHome portal={portal} />}
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
    </div>
  );
}
