/** Delinquency stage and the actions the ladder requires (spec §5). */
import type { Bridge } from '../../types.js';
import { applyDelinquency, type DelinquencyAction } from '../../policy/delinquency.js';
import { color, font, radius, space, stageToneFor, tone } from '../tokens.js';
import { usd, titleCase } from '../../ui/format.js';
import { Badge, Label, Row } from './primitives.js';

const ACTION_COPY: Record<DelinquencyAction, string> = {
  SEND_DAILY_REMINDER: 'Send daily reminders',
  COLLECT_FROM_ANY_DEPOSIT: 'Collect from any incoming deposit',
  BLOCK_NEW_BRIDGES: 'Block new bridges',
  RESTRICT_CARD_TO_CRITICAL: 'Restrict card to critical spend',
  DOWNGRADE_TIER: 'Downgrade tier one level',
  NOTIFY_OPS: 'Notify ops',
  FREEZE_CARD: 'Freeze card',
  ESCALATE_TO_COLLECTIONS: 'Escalate to a human in collections',
  SET_LIMIT_ZERO: 'Set limit to zero',
  RECORD_DEFAULT: 'Record as defaulted',
  REFER_TO_COLLECTIONS: 'Refer to collections',
};

export function DelinquencyPanel({ bridge, today }: { bridge: Bridge; today: string }) {
  const result = applyDelinquency(bridge, today);
  const onTrack = result.days_past_due === 0;

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: space.lg, flexWrap: 'wrap' }}>
        <Badge tone={stageToneFor(result.status)}>{titleCase(result.status)}</Badge>
        {!onTrack && (
          <span
            style={{
              fontSize: font.size.md,
              color: color.critical,
              fontWeight: font.weight.medium,
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {result.days_past_due} days past due
          </span>
        )}
      </div>

      {onTrack ? (
        <div style={{ fontSize: font.size.md, color: color.textSecondary }}>
          Inside its term. No action required.
        </div>
      ) : (
        <>
          <div>
            <Row label="Principal outstanding" value={usd(bridge.principal_outstanding)} />
            <Row label="Fee outstanding" value={usd(bridge.fee_outstanding, { cents: true })} />
            <Row label="Accrued while late" value={usd(result.accrued_fee, { cents: true })} />
            {result.ineligible_until !== undefined && (
              <Row label="Ineligible until" value={result.ineligible_until} />
            )}
          </div>

          <div style={{ display: 'grid', gap: space.md }}>
            <Label>Required actions</Label>
            <div style={{ display: 'grid', gap: space.sm }}>
              {result.actions.map((action) => (
                <div
                  key={action}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: space.md,
                    background: tone[stageToneFor(result.status)].bg,
                    borderRadius: radius.lg,
                    padding: `${space.md} ${space.lg}`,
                    fontSize: font.size.md,
                    color: color.text,
                  }}
                >
                  <span style={{ color: tone[stageToneFor(result.status)].fg }}>→</span>
                  {ACTION_COPY[action]}
                </div>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
