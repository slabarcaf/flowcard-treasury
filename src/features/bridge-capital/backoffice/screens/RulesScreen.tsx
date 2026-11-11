/**
 * The rules, as the engine holds them.
 *
 * Everything here is rendered from policy.config.ts, so it cannot drift from
 * what the engine applies. Nothing on this screen decides anything.
 */
import type { Regime, Tier } from '../../types.js';
import { currentRegime } from '../../policy/regime.js';
import { priceOffer } from '../../policy/pricing.js';
import {
  CRITICALITY_BY_CATEGORY,
  DELINQUENCY,
  HISTORY,
  PRICING,
  REGIME_LABELS,
  REGIME_RULES,
  REGIME_THRESHOLDS,
  RULE_CATALOGUE,
  TIERS,
  TIER_LIMIT_PCT,
  WHILE_BRIDGED,
} from '../../policy/policy.config.js';
import { CRITICALITY_TONE, TIER_TONE, color, font, radius, space } from '../tokens.js';
import { usd, pct, titleCase } from '../../ui/format.js';
import type { BackOffice } from '../useBackOffice.js';
import {
  Badge,
  Card,
  CardHeader,
  Empty,
  Label,
  Row,
  Table,
  Td,
  Th,
} from '../components/primitives.js';

const EXAMPLE = { principal: 10_000, termDays: 60 };

function Source({ children }: { children: string }) {
  return (
    <span style={{ fontSize: font.size.sm, color: color.textMuted, fontFamily: font.mono }}>
      {children}
    </span>
  );
}

function TierTable() {
  const rows: Array<{ tier: Tier; condition: string }> = [
    {
      tier: 'A',
      condition: `${TIERS.A.minTenureMonths}+ months, no late or defaulted bridge in ${TIERS.A.lateLookbackDays} days, at most ${TIERS.A.maxDaysNearZero90d} days near zero in 90`,
    },
    {
      tier: 'B',
      condition: `${TIERS.B.minTenureMonths}+ months, at most ${TIERS.B.maxLateInLookback} late bridge in ${TIERS.B.lateLookbackDays} days, never defaulted`,
    },
    {
      tier: 'C',
      condition: `Everyone else eligible: no default in the last ${TIERS.C.defaultLookbackDays} days`,
    },
    {
      tier: 'NONE',
      condition: `Under ${HISTORY.minTenureMonths} months, or under ${HISTORY.minBalanceDays} days of balance data, or a default inside ${TIERS.C.defaultLookbackDays} days`,
    },
  ];

  return (
    <Table>
      <thead>
        <tr>
          <Th>Tier</Th>
          <Th>Condition</Th>
          <Th align="right">Limit</Th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.tier}>
            <Td>
              <Badge tone={TIER_TONE[r.tier]}>{r.tier === 'NONE' ? 'No tier' : `Tier ${r.tier}`}</Badge>
            </Td>
            <Td muted>{r.condition}</Td>
            <Td align="right">
              {TIER_LIMIT_PCT[r.tier] === 0
                ? 'Not eligible'
                : `${pct(TIER_LIMIT_PCT[r.tier], 0)} of monthly inflows`}
            </Td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

function RegimeMatrix({ current }: { current: Regime | null }) {
  const order: Regime[] = ['ABUNDANT', 'NORMAL', 'TIGHT', 'CRITICAL', 'AT_FLOOR'];
  const band: Record<Regime, string> = {
    ABUNDANT: `Above ${pct(REGIME_THRESHOLDS.abundantAbove, 0)}`,
    NORMAL: `${pct(REGIME_THRESHOLDS.normalAbove, 0)} – ${pct(REGIME_THRESHOLDS.abundantAbove, 0)}`,
    TIGHT: `${pct(REGIME_THRESHOLDS.tightAbove, 0)} – ${pct(REGIME_THRESHOLDS.normalAbove, 0)}`,
    CRITICAL: `Under ${pct(REGIME_THRESHOLDS.tightAbove, 0)}`,
    AT_FLOOR: 'At or below the floor',
  };

  return (
    <Table>
      <thead>
        <tr>
          <Th>Regime</Th>
          <Th>Capital above floor</Th>
          <Th>Tiers</Th>
          <Th>Spend</Th>
          <Th>Approval</Th>
        </tr>
      </thead>
      <tbody>
        {order.map((regime) => {
          const rule = REGIME_RULES[regime];
          const live = regime === current;
          return (
            <tr
              key={regime}
              style={live ? { background: color.accentSubtle } : undefined}
            >
              <Td>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: space.md }}>
                  {REGIME_LABELS[regime]}
                  {live && <Badge tone="accent">Now</Badge>}
                </span>
              </Td>
              <Td muted>{band[regime]}</Td>
              <Td muted>{rule.minTier === null ? 'Any eligible tier' : `${rule.minTier} or better`}</Td>
              <Td muted>
                {rule.allowedCriticality.length === 0
                  ? 'None — collect only'
                  : rule.allowedCriticality.map((c) => titleCase(c)).join(', ')}
              </Td>
              <Td muted>
                {rule.approval === 'CLOSED'
                  ? 'No new bridges'
                  : rule.approval === 'HUMAN'
                    ? 'Every bridge needs a person'
                    : rule.humanApprovalAbove === null
                      ? 'Automatic'
                      : `Automatic up to ${usd(rule.humanApprovalAbove)}, then a person`}
                {rule.rankRequests && ' · ranked'}
              </Td>
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
}

function DelinquencyLadder() {
  const steps = [
    { band: `${DELINQUENCY.watch.from}–${DELINQUENCY.watch.to} days`, name: 'Watch', what: 'Daily reminders, collect from any deposit, no new bridges.' },
    { band: `${DELINQUENCY.delinquent.from}–${DELINQUENCY.delinquent.to} days`, name: 'Delinquent', what: 'Card restricted to critical spend, tier downgraded one level, ops notified.' },
    { band: `${DELINQUENCY.serious.from}–${DELINQUENCY.serious.to} days`, name: 'Serious', what: 'Card frozen, escalated to collections, limit set to zero.' },
    { band: `${DELINQUENCY.defaulted.from}+ days`, name: 'Default', what: `Recorded as defaulted and referred to collections. Ineligible for ${DELINQUENCY.defaultIneligibleMonths} months.` },
  ];

  return (
    <Table>
      <thead>
        <tr>
          <Th>Past due</Th>
          <Th>Stage</Th>
          <Th>What happens</Th>
        </tr>
      </thead>
      <tbody>
        {steps.map((s) => (
          <tr key={s.name}>
            <Td>{s.band}</Td>
            <Td>{s.name}</Td>
            <Td muted>{s.what}</Td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

export function RulesScreen({ office }: { office: BackOffice }) {
  const { data } = office;
  if (data === null) return <Empty>Loading…</Empty>;

  const regime = currentRegime(data.capital);
  const example = priceOffer(EXAMPLE.principal, EXAMPLE.termDays);

  const byCriticality = new Map<string, string[]>();
  for (const [category, criticality] of Object.entries(CRITICALITY_BY_CATEGORY)) {
    byCriticality.set(criticality, [...(byCriticality.get(criticality) ?? []), category]);
  }

  return (
    <div style={{ display: 'grid', gap: space.xl, maxWidth: '1100px' }}>
      <Card>
        <div style={{ fontSize: font.size.md, color: color.textSecondary, lineHeight: 1.6 }}>
          Everything on this page is read straight out of the engine's configuration. Change a
          threshold there and this page changes with it — there is no second copy of the rules.
        </div>
      </Card>

      <Card padded={false}>
        <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
          <CardHeader title="Tiers and limits" meta={<Source>§3</Source>} />
        </div>
        <TierTable />
      </Card>

      <Card padded={false}>
        <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
          <CardHeader
            title="Capital regimes"
            meta={<Badge tone="accent">{REGIME_LABELS[regime]} right now</Badge>}
          />
        </div>
        <RegimeMatrix current={regime} />
        <div style={{ padding: space['2xl'], fontSize: font.size.sm, color: color.textMuted, lineHeight: 1.5 }}>
          Measured on capital available above the reserve floor, as a share of the facility. When
          capital is ranked, requests compete on tier, then spend criticality, then receivable
          quality.
        </div>
      </Card>

      <Card padded={false}>
        <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
          <CardHeader title="Eligibility rules" meta={<Source>§4</Source>} />
        </div>
        <Table>
          <thead>
            <tr>
              <Th>#</Th>
              <Th>Rule</Th>
              <Th>What it checks</Th>
              <Th align="right">Spec</Th>
            </tr>
          </thead>
          <tbody>
            {RULE_CATALOGUE.map((rule, i) => (
              <tr key={rule.id} id={`rule-${rule.id}`}>
                <Td muted>{i + 1}</Td>
                <Td>
                  <div style={{ display: 'grid' }}>
                    <span>{rule.label}</span>
                    <span style={{ fontSize: font.size['2xs'], fontFamily: font.mono, color: color.textMuted }}>
                      {rule.id}
                    </span>
                  </div>
                </Td>
                <Td muted>{rule.checks}</Td>
                <Td align="right" muted>{rule.source}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </Card>

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', gap: space.xl, alignItems: 'start' }}>
        <Card>
          <CardHeader title="Pricing" meta={<Source>§5</Source>} />
          <div style={{ display: 'grid', gap: space.xl }}>
            <div
              style={{
                background: color.surfaceSubtle,
                borderRadius: radius.lg,
                padding: space.lg,
                fontFamily: font.mono,
                fontSize: font.size.sm,
                color: color.text,
              }}
            >
              fee = {PRICING.serviceFee} + principal × {PRICING.monthlyRate} × term_days /{' '}
              {PRICING.daysPerMonth}
            </div>
            <div>
              <Row label="Service fee" value={usd(PRICING.serviceFee)} />
              <Row label="Rate" value={`${pct(PRICING.monthlyRate, 0)} a month, prorated daily`} />
              <Row label="Preset terms" value={PRICING.presetTermDays.map((d) => `${d}d`).join(' · ')} />
              <Row
                label="Custom term"
                value={`${PRICING.minCustomTermDays}–${PRICING.maxCustomTermDays} days`}
              />
            </div>
            <div style={{ display: 'grid', gap: space.sm }}>
              <Label>Worked example</Label>
              <div style={{ fontSize: font.size.md, color: color.textSecondary, lineHeight: 1.6 }}>
                {usd(EXAMPLE.principal)} over {EXAMPLE.termDays} days costs{' '}
                <span style={{ color: color.text }}>{usd(example.fee, { cents: true })}</span> —{' '}
                {usd(PRICING.serviceFee)} service plus{' '}
                {usd(example.fee - PRICING.serviceFee, { cents: true })} interest. The fee is
                charged for the full term even if it is repaid early.
              </div>
            </div>
          </div>
        </Card>

        <Card>
          <CardHeader title="Spend criticality" meta={<Source>§4</Source>} />
          <div style={{ display: 'grid', gap: space.xl }}>
            {['CRITICAL', 'STANDARD', 'DISCRETIONARY'].map((c) => (
              <div key={c} style={{ display: 'grid', gap: space.sm }}>
                <Badge tone={CRITICALITY_TONE[c as keyof typeof CRITICALITY_TONE]}>
                  {titleCase(c)}
                </Badge>
                <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
                  {(byCriticality.get(c) ?? []).map((cat) => titleCase(cat)).join(', ')}
                </span>
              </div>
            ))}
            <div style={{ fontSize: font.size.sm, color: color.textMuted, lineHeight: 1.5 }}>
              An unmapped merchant category falls back to Standard. While a bridge is running,
              tier {WHILE_BRIDGED.restrictedTiers.join(', ')} cards are held to{' '}
              {WHILE_BRIDGED.allowedCriticality.map((x) => titleCase(x)).join(', ').toLowerCase()}{' '}
              spend until it is repaid in full.
            </div>
          </div>
        </Card>
      </div>

      <Card padded={false}>
        <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
          <CardHeader title="Late repayment" meta={<Source>§5</Source>} />
        </div>
        <DelinquencyLadder />
        <div style={{ padding: space['2xl'], fontSize: font.size.sm, color: color.textMuted, lineHeight: 1.5 }}>
          Outstanding keeps accruing at {pct(PRICING.monthlyRate, 0)} a month while late. After full
          repayment, restrictions lift and the tier recomputes from history.
        </div>
      </Card>
    </div>
  );
}
