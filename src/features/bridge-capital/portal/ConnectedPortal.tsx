import { useState } from 'react';
import type { BrowserSession } from '../../../services/browser-session/client.js';
import type { Offer, SourceMode } from '../../../services/financial/client.js';
import { cents } from '../../../services/financial/client.js';
import { ProductReadClient } from '../../../services/financial/product-reads.js';
import { useProductQuery } from '../ui/useProductQuery.js';
import { BridgeCard, OfferCard, Recovery, ReadNotice, SourceNote } from '../ui/ConnectedFinance.js';
import { financeRead, financeCommand, human, stateTone } from '../ui/finance.js';
import type { ServicingSession } from '../../../services/financial/commands.js';
import { GlowBackground } from '../ui/Surface.js';
import { Card, Heading, Amount, Button, Pill, Row, BackLink } from './components/ui.js';
import { TabBar } from './components/TabBar.js';
import type { TabId } from './components/TabBar.js';
import { color, font, glow, layout, space } from './tokens.js';
const product = new ProductReadClient();
type PortalView = { screen: TabId | 'offer' | 'bridge'; id?: string };
async function loadCustomerOffers(customerID: string, expectedMode: SourceMode): Promise<Offer[]> {
  const ids = await financeCommand.offers();
  const offers: Offer[] = [];
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(4, ids.length) }, async () => {
      while (next < ids.length) {
        const index = next++;
        const offer = await financeRead.offer(ids[index]);
        if (offer.source_mode !== expectedMode || offer.customer_id !== customerID)
          throw new Error('Offer does not match the signed-in customer.');
        offers[index] = offer;
      }
    })
  );
  return offers;
}
export function ConnectedPortal({
  session,
  logout,
  relogin,
}: {
  session: BrowserSession;
  logout: () => void;
  relogin: () => void;
}) {
  const binding = useProductQuery('customer-binding', () => financeCommand.session());
  return (
    <GlowBackground
      canvas={color.bg}
      glow={glow}
      style={{ color: color.text, fontFamily: font.sans, fontSize: font.size.lg }}
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
            justifyContent: 'space-between',
            gap: 12,
            alignItems: 'center',
            flexWrap: 'wrap',
          }}
        >
          <span style={{ fontWeight: font.weight.semibold }}>FlowCard</span>
          <div
            style={{
              fontSize: 12,
              display: 'flex',
              gap: 12,
              alignItems: 'center',
              flexWrap: 'wrap',
            }}
          >
            <span>{session.user.username}</span>
            <button
              type="button"
              onClick={relogin}
              style={{ background: 'none', border: 0, color: color.textMuted, cursor: 'pointer' }}
            >
              Sign in again
            </button>
            <button
              type="button"
              onClick={logout}
              style={{ background: 'none', border: 0, color: color.textMuted, cursor: 'pointer' }}
            >
              Sign out
            </button>
          </div>
        </div>
      </header>
      <main
        style={{
          maxWidth: layout.maxWidth,
          margin: '0 auto',
          padding: `${space['3xl']} ${layout.gutter} 120px`,
        }}
      >
        {(binding.busy || binding.error) && <ReadNotice {...binding} />}
        {binding.data?.linked && binding.data.customer_id ? (
          <CustomerFinancing
            key={session.user.id + binding.data.customer_id}
            session={session}
            binding={binding.data}
          />
        ) : (
          !binding.busy && (
            <Card>
              <Heading>Account not linked</Heading>
              <p>
                Your login is active. A verified servicing account is required before offers or
                bridge records can be displayed.
              </p>
            </Card>
          )
        )}
      </main>
    </GlowBackground>
  );
}
function CustomerFinancing({
  session,
  binding,
}: {
  session: BrowserSession;
  binding: ServicingSession;
}) {
  const [view, setView] = useState<PortalView>({ screen: 'wallet' });
  const [cursor, setCursor] = useState<string>();
  const bridges = useProductQuery(
    'own-bridges:' + cursor,
    async () => {
      const page = await product.bridges(undefined, cursor);
      if (
        page.source_mode !== binding.mode ||
        page.items.some((x) => x.customer_id !== binding.customer_id)
      )
        throw new Error('Bridge records do not match your verified customer.');
      return page;
    },
    3000
  );
  const offers = useProductQuery(
    'own-offers',
    () => loadCustomerOffers(binding.customer_id!, binding.mode!),
    5000
  );
  const openBridge = (id: string) => setView({ screen: 'bridge', id });
  const refresh = () => {
    offers.refresh();
    bridges.refresh();
  };
  const active =
    bridges.data?.items.filter((x) => !['REPAID', 'VOID_UNFUNDED'].includes(x.lifecycle)) ?? [];
  const selected = bridges.data?.items.find((x) => x.offer_id === view.id);
  const tab: TabId =
    view.screen === 'offer' ? 'wallet' : view.screen === 'bridge' ? 'bridges' : view.screen;
  return (
    <>
      <div style={{ display: 'grid', gap: 24 }}>
        {view.screen === 'wallet' && (
          <>
            <Heading sub="Offers and financing linked to your verified account">
              Your wallet
            </Heading>
            <WalletCard binding={binding} />
            {active.map((b) => (
              <Card key={b.id}>
                <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                  <span>Your bridge</span>
                  <Pill tone={stateTone(b.lifecycle)}>{human(b.lifecycle)}</Pill>
                </div>
                <div style={{ margin: '20px 0' }}>
                  <Amount label="Principal still owed" value={cents(b.principal_owed_cents)} />
                </div>
                <Row label="Contract fee owed" value={cents(b.contract_fee_owed_cents)} />
                <Row label="Due" value={b.due_on} />
                <Button variant="secondary" onClick={() => openBridge(b.id)}>
                  View your bridge
                </Button>
              </Card>
            ))}
            <ReadNotice
              busy={offers.busy || bridges.busy}
              error={offers.error || bridges.error}
              refresh={refresh}
            />
            <h2 style={{ fontSize: 20 }}>Bridge offers</h2>
            {offers.data?.length === 0 && (
              <Card>
                <p>
                  No saved offers yet. The team will review eligible shortfalls and publish exact
                  terms here.
                </p>
              </Card>
            )}
            {offers.data?.map((offer) => (
              <Card key={offer.id}>
                <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                  <span>{cents(offer.principal_cents)} shortfall cover</span>
                  <Pill tone={stateTone(offer.state)}>{human(offer.state)}</Pill>
                </div>
                <Row label="Fixed fee" value={cents(offer.contract_fee_cents)} />
                <Row label="You repay" value={cents(offer.total_due_cents)} />
                <Row label="Due" value={offer.due_on} />
                <Button
                  variant="secondary"
                  onClick={() => setView({ screen: 'offer', id: offer.id })}
                >
                  Review offer
                </Button>
              </Card>
            ))}
          </>
        )}
        {view.screen === 'offer' && view.id && (
          <>
            <BackLink onClick={() => setView({ screen: 'wallet' })}>Wallet</BackLink>
            <Heading sub="Review the saved fee, repayment date and disclosures">
              Your bridge offer
            </Heading>
            <OfferCard
              key={view.id}
              id={view.id}
              session={session}
              openBridge={openBridge}
              changed={refresh}
            />
            {selected && <Button onClick={() => openBridge(selected.id)}>View your bridge</Button>}
            <Recovery changed={refresh} />
          </>
        )}
        {view.screen === 'bridges' && (
          <>
            <Heading sub="Saved financing and repayment status">Your bridges</Heading>
            <ReadNotice {...bridges} />
            {bridges.data?.items.length === 0 && <Card>No bridges yet.</Card>}
            {bridges.data?.items.map((b) => (
              <Card key={b.id}>
                <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                  <span>
                    {cents(b.principal_original_cents)} · {b.term_days} days
                  </span>
                  <Pill tone={stateTone(b.lifecycle)}>{human(b.lifecycle)}</Pill>
                </div>
                <Row label="Principal owed" value={cents(b.principal_owed_cents)} />
                <Row label="Fee owed" value={cents(b.contract_fee_owed_cents)} />
                <Row label="Late fee owed" value={cents(b.late_fee_owed_cents)} />
                <Row label="Due" value={b.due_on} />
                <Button variant="secondary" onClick={() => openBridge(b.id)}>
                  Read bridge
                </Button>
              </Card>
            ))}
            {bridges.data?.next_cursor && (
              <Button
                variant="secondary"
                onClick={() => setCursor(bridges.data?.next_cursor ?? undefined)}
              >
                Older bridges
              </Button>
            )}
            {cursor && (
              <Button variant="secondary" onClick={() => setCursor(undefined)}>
                Newest bridges
              </Button>
            )}
          </>
        )}
        {view.screen === 'bridge' && view.id && (
          <>
            <BackLink onClick={() => setView({ screen: 'bridges' })}>Bridges</BackLink>
            <BridgeCard key={view.id} id={view.id} />
            <Recovery changed={refresh} />
          </>
        )}
        {view.screen === 'activity' && (
          <>
            <Heading sub="Confirmed servicing records for the loaded bridge page">Activity</Heading>
            <Card>
              <p>
                Complete bank and card activity is unavailable. Open a bridge to see its funding
                operations, settled deposit collections and exact allocations.
              </p>
            </Card>
            {bridges.data?.items.map((b) => (
              <Card key={b.id}>
                <Row label="Bridge" value={cents(b.principal_original_cents)} />
                <Row
                  label="Funding confirmed"
                  value={b.funded_at ? new Date(b.funded_at).toLocaleString() : 'Pending'}
                />
                <Row label="State" value={human(b.lifecycle)} />
                <Button variant="secondary" onClick={() => openBridge(b.id)}>
                  Read servicing activity
                </Button>
              </Card>
            ))}
          </>
        )}
        {view.screen === 'account' && (
          <>
            <Heading>Your account</Heading>
            <Card>
              <Row label="Signed in as" value={session.user.username} />
              <Row
                label="Source"
                value={
                  String(binding.mode) === 'SANDBOX'
                    ? 'Airwallex sandbox test funds and scenario facts'
                    : 'Synthetic DEMO installation'
                }
              />
              <Row label="Servicing customer" value={binding.customer_id} />
              <Row label="Account link" value="Verified by the service" />
              <Row label="Credit line" value="Available on individual assessed offers" />
              <p>
                Customer scope is fixed by your verified login binding. Financing is reviewed on
                current facts before funds move.
              </p>
            </Card>
            <Card>
              <h2 style={{ fontSize: 20 }}>Repayment</h2>
              <p>
                Verified settled deposits are collected against your bridge, fees first and then
                principal. Saved ledger records show what has actually settled.
              </p>
            </Card>
          </>
        )}
        <SourceNote mode={binding.mode} />
      </div>
      <TabBar active={tab} onSelect={(screen) => setView({ screen })} />
    </>
  );
}

function WalletCard({ binding }: { binding: ServicingSession }) {
  const wallet = useProductQuery(
    'wallet-balance',
    async () => {
      const value = await product.wallet();
      if (value.customer_id !== binding.customer_id || value.source_mode !== binding.mode) {
        throw new Error('Wallet does not match this verified customer.');
      }
      return value;
    },
    10000
  );
  return (
    <Card>
      <Amount
        label="Available wallet balance"
        value={cents(wallet.data?.available_balance_cents)}
      />
      <ReadNotice {...wallet} />
      {wallet.data && (
        <p style={{ fontSize: 12, color: color.textMuted }}>
          Retrieved {new Date(wallet.data.observed_at).toLocaleString()} ·{' '}
          {wallet.data.source_mode === 'DEMO' ? 'local provider double' : 'Airwallex sandbox'}
        </p>
      )}
      <p style={{ fontSize: 14, color: color.textMuted }}>
        Provider cash is separate from your credit limit and bridge debt. Funding does not retry a
        card purchase.
      </p>
    </Card>
  );
}
