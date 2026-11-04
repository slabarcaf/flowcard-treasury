/** A running bridge: what is left, when it is due, and what happens if it slips. */
import type { Bridge } from '../../types.js';
import { applyDelinquency, type DelinquencyAction } from '../../policy/delinquency.js';
import { DELINQUENCY } from '../../policy/policy.config.js';
import { color, font, radius, space, stageToneFor, timelineColors, tone } from '../tokens.js';
import { usd, shortDate, titleCase } from '../../ui/format.js';
import { Timeline } from '../../ui/Timeline.js';
import type { Portal } from '../usePortal.js';
import { Amount, BackLink, Callout, Card, Pill, Row, SectionTitle } from '../components/ui.js';

/** The ladder, in the customer's words. */
const LADDER: Array<{ status: Bridge['status']; days: string; what: string }> = [
  {
    status: 'WATCH',
    days: `${DELINQUENCY.watch.from}–${DELINQUENCY.watch.to} days late`,
    what: 'We send daily reminders and keep collecting from any deposit. No new bridges while this is open.',
  },
  {
    status: 'DELINQUENT',
    days: `${DELINQUENCY.delinquent.from}–${DELINQUENCY.delinquent.to} days late`,
    what: 'Your card is limited to essential spend and your tier drops one level. Our team is notified.',
  },
  {
    status: 'SERIOUS',
    days: `${DELINQUENCY.serious.from}–${DELINQUENCY.serious.to} days late`,
    what: 'Your card is frozen, your limit goes to zero, and a person from collections takes over.',
  },
  {
    status: 'DEFAULTED',
    days: `${DELINQUENCY.defaulted.from}+ days late`,
    what: `Recorded as a default and referred to collections. No bridges for ${DELINQUENCY.defaultIneligibleMonths} months.`,
  },
];

const ACTION_COPY: Record<DelinquencyAction, string> = {
  SEND_DAILY_REMINDER: 'We will remind you daily until it is cleared.',
  COLLECT_FROM_ANY_DEPOSIT: 'Any deposit that lands goes to this bridge first.',
  BLOCK_NEW_BRIDGES: 'No new bridges until this one is repaid.',
  RESTRICT_CARD_TO_CRITICAL: 'Your card is limited to essential spend.',
  DOWNGRADE_TIER: 'Your tier has dropped one level.',
  NOTIFY_OPS: 'Our team has been notified.',
  FREEZE_CARD: 'Your card is frozen.',
  ESCALATE_TO_COLLECTIONS: 'Someone from collections will be in touch.',
  SET_LIMIT_ZERO: 'Your bridge limit is set to zero.',
  RECORD_DEFAULT: 'This has been recorded as a default.',
  REFER_TO_COLLECTIONS: 'The balance has been referred to collections.',
};

export function ActiveBridgeScreen({ portal, bridgeId }: { portal: Portal; bridgeId: string }) {
  const { data, today, go } = portal;
  if (data === null) return null;

  const bridge = data.bridges.find((b) => b.id === bridgeId);
  if (bridge === undefined) return null;

  const stage = applyDelinquency(bridge, today);
  const pastDue = stage.days_past_due > 0;
  const outstanding = bridge.principal_outstanding + bridge.fee_outstanding + stage.accrued_fee;
  const collected = bridge.principal - bridge.principal_outstanding + (bridge.fee - bridge.fee_outstanding);
  const events = data.timeline
    .filter((e) => e.bridge_id === bridge.id)
    .sort((a, b) => a.at.localeCompare(b.at));
  const collections = events.filter((e) => e.type === 'COLLECTION');
  const currentIndex = LADDER.findIndex((l) => l.status === stage.status);

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <BackLink onClick={() => go({ screen: 'wallet' })}>Wallet</BackLink>

      <Card>
        <div style={{ display: 'grid', gap: space.xl }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg }}>
            <SectionTitle>Your bridge</SectionTitle>
            <Pill tone={stageToneFor(stage.status)}>{titleCase(stage.status)}</Pill>
          </div>
          <Amount
            label="Still to repay"
            value={usd(outstanding, { cents: true })}
            tone={pastDue ? 'critical' : undefined}
          />
          <div>
            <Row label="We covered" value={usd(bridge.principal)} />
            <Row label="Fee" value={usd(bridge.fee, { cents: true })} />
            {stage.accrued_fee > 0 && (
              <Row label="Added while late" value={usd(stage.accrued_fee, { cents: true })} />
            )}
            <Row label="Collected so far" value={usd(collected, { cents: true })} />
            <Row
              label={pastDue ? 'Was due' : 'Due'}
              value={
                <span style={{ color: pastDue ? color.critical : color.text }}>
                  {shortDate(bridge.due_date)}
                  {pastDue ? ` · ${stage.days_past_due} days ago` : ''}
                </span>
              }
            />
          </div>
        </div>
      </Card>

      {pastDue ? (
        <>
          <Callout title="What is happening now" tone="critical">
            <span style={{ display: 'grid', gap: space.sm, marginTop: space.xs }}>
              {stage.actions.map((action) => (
                <span key={action}>• {ACTION_COPY[action]}</span>
              ))}
            </span>
          </Callout>

          <Card>
            <div style={{ display: 'grid', gap: space.xl }}>
              <SectionTitle>What happens next</SectionTitle>
              <div style={{ display: 'grid', gap: space.md }}>
                {LADDER.map((step, i) => {
                  const isCurrent = i === currentIndex;
                  const isPast = currentIndex >= 0 && i < currentIndex;
                  return (
                    <div
                      key={step.status}
                      style={{
                        background: isCurrent ? tone[stageToneFor(step.status)].bg : color.surfaceSubtle,
                        border: `1px solid ${isCurrent ? tone[stageToneFor(step.status)].fg : 'transparent'}`,
                        borderRadius: radius.xl,
                        padding: space.xl,
                        display: 'grid',
                        gap: space.xs,
                        opacity: isPast ? 0.55 : 1,
                      }}
                    >
                      <span
                        style={{
                          fontSize: font.size.md,
                          fontWeight: font.weight.semibold,
                          color: isCurrent ? tone[stageToneFor(step.status)].fg : color.textSecondary,
                        }}
                      >
                        {titleCase(step.status)} · {step.days}
                        {isCurrent ? ' · you are here' : ''}
                      </span>
                      <span style={{ fontSize: font.size.md, lineHeight: 1.55, color: color.textSecondary }}>
                        {step.what}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          </Card>
        </>
      ) : (
        <Callout title="How you repay">
          We collect automatically from deposits as they land, fee first and then the amount we
          covered. Nothing for you to schedule.
        </Callout>
      )}

      <Card>
        <div style={{ display: 'grid', gap: space.xl }}>
          <SectionTitle>
            Collections so far{collections.length > 0 ? ` · ${collections.length}` : ''}
          </SectionTitle>
          <Timeline
            events={events}
            colors={timelineColors}
            emptyLabel="Nothing has happened yet"
          />
        </div>
      </Card>
    </div>
  );
}
