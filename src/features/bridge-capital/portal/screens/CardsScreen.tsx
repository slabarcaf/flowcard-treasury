/** Team cards and one virtual card per vendor, each with a cap, blocked categories and freeze. */
import { useState } from 'react';
import type { IssuedCard } from '../../types.js';
import { color, font, radius, space } from '../tokens.js';
import { usd } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { BLOCKABLE_CATEGORIES } from '../../data/extras.js';
import { categoryLabel, restrictionSentence } from '../copy.js';
import { Pill } from '../components/ui.js';
import { Banner, Chips, PageHeader, Panel, SmallButton } from '../components/layout.js';
import { inputStyle } from '../components/styles.js';

function CardFace({ card, lock }: { card: IssuedCard; lock: string | null }) {
  const virtual = card.kind === 'VIRTUAL';
  return (
    <div
      style={{
        position: 'relative',
        aspectRatio: '1.586',
        maxWidth: '100%',
        borderRadius: radius.xl,
        padding: space.xl,
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'space-between',
        overflow: 'hidden',
        background: virtual ? 'linear-gradient(135deg, #2A3A8F, #141B3D)' : 'linear-gradient(135deg, #2B3443, #11161D)',
        filter: card.frozen ? 'grayscale(0.9)' : 'none',
        opacity: card.frozen ? 0.7 : 1,
      }}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: space.md }}>
        <span style={{ display: 'grid' }}>
          <span style={{ fontWeight: font.weight.semibold }}>{card.holder}</span>
          <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>{card.role}</span>
        </span>
        <Pill>{virtual ? 'Virtual' : 'Physical'}</Pill>
      </div>
      <span style={{ letterSpacing: '0.08em', fontVariantNumeric: 'tabular-nums', color: color.textSecondary }}>•••• {card.last4}</span>
      {(card.frozen || lock !== null) && (
        <span style={{ position: 'absolute', left: space.md, right: space.md, bottom: space.md, background: 'rgba(5, 9, 15, 0.85)', borderRadius: radius.lg, padding: `${space.sm} ${space.lg}`, fontSize: font.size.sm, color: color.warning }}>
          {card.frozen ? 'Frozen by you' : lock}
        </span>
      )}
    </div>
  );
}

export function CardsScreen({ portal }: { portal: Portal }) {
  const { data, customer, access, saveCard, createVirtualCard, cancelCard } = portal;
  const [editing, setEditing] = useState<string | null>(null);
  const [cap, setCap] = useState(0);
  const [blocked, setBlocked] = useState<string[]>([]);
  const [newVendor, setNewVendor] = useState('');
  const [newCap, setNewCap] = useState(500);
  const [adding, setAdding] = useState(false);
  if (data === null || customer === null) return null;

  const cards = data.cards.filter((c) => c.customer_id === customer.id);
  const restriction = restrictionSentence(access);
  const lock = access.state === 'FROZEN' ? 'Paused: overdue bridge' : access.state === 'RESTRICTED' ? 'Essential spend only' : null;

  const startEdit = (c: IssuedCard) => {
    setEditing(editing === c.id ? null : c.id);
    setCap(c.monthly_cap);
    setBlocked([...c.blocked_categories]);
  };

  const renderCard = (c: IssuedCard) => {
    const used = c.monthly_cap === 0 ? 0 : Math.min(1, c.spent_this_month / c.monthly_cap);
    return (
      <div key={c.id} style={{ display: 'grid', gap: space.lg, alignContent: 'start' }}>
        <CardFace card={c} lock={lock} />
        <span style={{ height: '8px', background: color.surfaceSubtle, borderRadius: radius.pill, overflow: 'hidden' }}>
          <span style={{ display: 'block', height: '100%', width: `${used * 100}%`, background: used > 0.9 ? color.warning : color.accent }} />
        </span>
        <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>
          {usd(c.spent_this_month)} of {usd(c.monthly_cap)} this month
          {c.blocked_categories.length > 0 && ` · Blocked: ${c.blocked_categories.map(categoryLabel).join(', ')}`}
        </span>
        <div style={{ display: 'flex', gap: space.md, flexWrap: 'wrap' }}>
          <SmallButton onClick={() => startEdit(c)}>Controls</SmallButton>
          <SmallButton onClick={() => void saveCard({ ...c, frozen: !c.frozen }, c.frozen ? `Card •••• ${c.last4} is active again.` : `Card •••• ${c.last4} frozen.`)}>
            {c.frozen ? 'Unfreeze' : 'Freeze'}
          </SmallButton>
          {c.kind === 'VIRTUAL' && <SmallButton variant="danger" onClick={() => void cancelCard(c)}>Cancel</SmallButton>}
        </div>
        {editing === c.id && (
          <div style={{ display: 'grid', gap: space.lg, background: color.surfaceSubtle, borderRadius: radius.lg, padding: space.xl }}>
            <label style={{ display: 'grid', gap: space.sm }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Monthly limit (USD)</span>
              <input type="number" min={0} step={100} value={cap} onChange={(e) => setCap(Math.max(0, Number(e.target.value) || 0))} style={{ ...inputStyle, background: color.surface }} />
            </label>
            <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Block these merchant types</span>
            <Chips
              options={BLOCKABLE_CATEGORIES}
              value={blocked}
              onChange={(cat) => setBlocked(blocked.includes(cat) ? blocked.filter((x) => x !== cat) : [...blocked, cat])}
              label={categoryLabel}
            />
            <div>
              <SmallButton
                variant="primary"
                onClick={() => {
                  void saveCard({ ...c, monthly_cap: cap, blocked_categories: blocked }, 'Card controls saved.');
                  setEditing(null);
                }}
              >
                Save
              </SmallButton>
            </div>
          </div>
        )}
      </div>
    );
  };

  return (
    <>
      <PageHeader
        title="Cards"
        sub="Team cards, and one virtual card per vendor with its own limit."
        action={<SmallButton variant="primary" onClick={() => setAdding(!adding)}>New vendor card</SmallButton>}
      />
      {restriction !== null && <Banner title={access.state === 'FROZEN' ? 'Cards paused' : 'Cards limited'} tone="warning">{restriction}</Banner>}
      {adding && (
        <Panel title="New vendor card">
          <div className="bcp-g2">
            <label style={{ display: 'grid', gap: space.sm }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Vendor</span>
              <input type="text" value={newVendor} placeholder="e.g. Google Workspace" onChange={(e) => setNewVendor(e.target.value)} style={inputStyle} />
            </label>
            <label style={{ display: 'grid', gap: space.sm }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Monthly limit (USD)</span>
              <input type="number" min={0} step={50} value={newCap} onChange={(e) => setNewCap(Math.max(0, Number(e.target.value) || 0))} style={inputStyle} />
            </label>
          </div>
          <div style={{ marginTop: space.xl }}>
            <SmallButton
              variant="primary"
              disabled={newVendor.trim() === ''}
              onClick={() => {
                void createVirtualCard(newVendor.trim(), newCap);
                setNewVendor('');
                setAdding(false);
              }}
            >
              Create card
            </SmallButton>
          </div>
        </Panel>
      )}
      <Panel title="Team cards">
        <div className="bcp-cards">{cards.filter((c) => c.kind === 'PHYSICAL').map(renderCard)}</div>
      </Panel>
      <Panel title="Vendor cards" meta={cards.filter((c) => c.kind === 'VIRTUAL').length}>
        <div className="bcp-cards">{cards.filter((c) => c.kind === 'VIRTUAL').map(renderCard)}</div>
      </Panel>
    </>
  );
}
