/** Your bridge line: how much, why, and what moves it. */
import { computeTier, customerLimit, outstandingBalance } from '../../policy/tiers.js';
import { WHILE_BRIDGED } from '../../policy/policy.config.js';
import { color, font, radius, space } from '../tokens.js';
import { usd, titleCase } from '../../ui/format.js';
import { lineExplanation, lineNextStep } from '../guidance.js';
import type { Portal } from '../usePortal.js';
import { Amount, Callout, Card, Row, SectionTitle } from '../components/ui.js';

function UsageBar({ used, limit }: { used: number; limit: number }) {
  const share = limit <= 0 ? 0 : Math.min(1, used / limit);
  return (
    <div
      style={{
        height: '10px',
        borderRadius: radius.pill,
        background: color.surfaceSunken,
        overflow: 'hidden',
      }}
    >
      <div
        style={{
          width: `${share * 100}%`,
          height: '100%',
          borderRadius: radius.pill,
          background: share >= 1 ? color.warning : color.accent,
        }}
      />
    </div>
  );
}

function Point({ label, detail, good }: { label: string; detail: string; good: boolean }) {
  return (
    <div style={{ display: 'flex', gap: space.lg, padding: `${space.lg} 0`, boxShadow: `inset 0 -1px 0 ${color.border}` }}>
      <span
        aria-hidden="true"
        style={{
          flexShrink: 0,
          width: '8px',
          height: '8px',
          marginTop: '7px',
          borderRadius: '50%',
          background: good ? color.positive : color.warning,
        }}
      />
      <span style={{ display: 'grid', gap: '2px' }}>
        <span style={{ fontSize: font.size.lg, color: color.text, fontWeight: font.weight.medium }}>
          {label}
        </span>
        <span style={{ fontSize: font.size.md, color: color.textSecondary, lineHeight: 1.55 }}>
          {detail}
        </span>
      </span>
    </div>
  );
}

export function AccountScreen({ portal }: { portal: Portal }) {
  const { data, customer, today } = portal;
  if (data === null || customer === null) return null;

  const tier = computeTier(customer);
  const limit = customerLimit(customer, tier);
  const used = outstandingBalance(customer);
  const left = Math.max(0, limit - used);
  const points = lineExplanation(customer);
  const nextStep = lineNextStep(customer);

  const openBridge = data.bridges.find(
    (b) => b.customer_id === customer.id && b.status !== 'REPAID',
  );
  const restricted = openBridge !== undefined && WHILE_BRIDGED.restrictedTiers.includes(tier);

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <Card>
        <div style={{ display: 'grid', gap: space.xl }}>
          <Amount label="You can bridge up to" value={usd(limit)} />
          {limit > 0 && (
            <>
              <UsageBar used={used} limit={limit} />
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: font.size.md }}>
                <span style={{ color: color.textSecondary }}>{usd(used)} in use</span>
                <span style={{ color: color.text }}>{usd(left)} available</span>
              </div>
            </>
          )}
        </div>
      </Card>

      {tier === 'NONE' && (
        <Callout tone="warning" title="Not available yet">
          Bridges are not open on this account today. The points below are what we look at.
        </Callout>
      )}

      {restricted && (
        <Callout tone="warning" title="While a bridge is running">
          Your card is limited to{' '}
          {WHILE_BRIDGED.allowedCriticality.map((c) => titleCase(c)).join(' and ').toLowerCase()}{' '}
          spend — suppliers, payroll and inventory — until the bridge is repaid in full. The limit
          lifts automatically on repayment.
        </Callout>
      )}

      <Card>
        <div style={{ display: 'grid', gap: space.md }}>
          <SectionTitle>Why it is this number</SectionTitle>
          <div style={{ display: 'grid' }}>
            {points.map((p) => (
              <Point key={p.label} {...p} />
            ))}
          </div>
        </div>
      </Card>

      {nextStep !== null && (
        <Callout tone="neutral" title="What moves it">
          {nextStep}
        </Callout>
      )}

      <Card>
        <div style={{ display: 'grid', gap: space.md }}>
          <SectionTitle>Account</SectionTitle>
          <div style={{ display: 'grid' }}>
            <Row label="Business" value={customer.name} />
            <Row label="With FlowCard since" value={customer.crm.onboarding_date.slice(0, 4)} />
            <Row label="Bridges to date" value={`${customer.bridge_stats.bridges_count}`} />
            <Row label="As of" value={today} />
          </div>
        </div>
      </Card>
    </div>
  );
}
