/** Every bridge this customer has had, running first. */
import { applyDelinquency } from '../../policy/delinquency.js';
import { color, font, space, stageToneFor } from '../tokens.js';
import { usd, shortDate, titleCase } from '../../ui/format.js';
import type { Bridge, PastBridge } from '../../types.js';
import type { Portal } from '../usePortal.js';
import { Card, ListRow, Pill, SectionTitle } from '../components/ui.js';

const OUTCOME_LABEL: Record<PastBridge['outcome'], string> = {
  ON_TIME: 'Repaid on time',
  LATE: 'Repaid late',
  DEFAULTED: 'Went to collections',
  OPEN: 'Running',
};

const OUTCOME_TONE: Record<PastBridge['outcome'], 'positive' | 'warning' | 'critical' | 'neutral'> = {
  ON_TIME: 'positive',
  LATE: 'warning',
  DEFAULTED: 'critical',
  OPEN: 'neutral',
};

function outstandingOf(bridge: Bridge, today: string): number {
  const stage = applyDelinquency(bridge, today);
  return bridge.principal_outstanding + bridge.fee_outstanding + stage.accrued_fee;
}

export function BridgesScreen({ portal }: { portal: Portal }) {
  const { data, customer, today, go } = portal;
  if (data === null || customer === null) return null;

  // Running bridges come from the live records; the closed ones come from the
  // customer's own history, which reaches further back than this demo's ledger.
  const running = data.bridges
    .filter((b) => b.customer_id === customer.id && b.status !== 'REPAID')
    .sort((a, b) => (b.disbursed_at ?? '').localeCompare(a.disbursed_at ?? ''));

  const closed = customer.bridge_history
    .filter((b) => b.closed_at !== null)
    .sort((a, b) => (b.closed_at ?? '').localeCompare(a.closed_at ?? ''));

  const count = running.length + closed.length;
  const covered = running.reduce((s, b) => s + b.principal, 0)
    + closed.reduce((s, b) => s + b.amount, 0);

  if (count === 0) {
    return (
      <Card>
        <div style={{ display: 'grid', gap: space.sm }}>
          <span style={{ fontSize: font.size.lg, fontWeight: font.weight.medium }}>
            No bridges yet
          </span>
          <span style={{ fontSize: font.size.md, color: color.textSecondary, lineHeight: 1.6 }}>
            If a card payment is ever short, we can cover it here and collect it back from your
            next deposits.
          </span>
        </div>
      </Card>
    );
  }

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      {running.map((bridge) => {
        const stage = applyDelinquency(bridge, today);
        const pastDue = stage.days_past_due > 0;
        return (
          <button
            key={bridge.id}
            type="button"
            onClick={() => go({ screen: 'bridge', bridgeId: bridge.id })}
            style={{
              textAlign: 'left',
              width: '100%',
              background: pastDue ? color.criticalSubtle : color.surface,
              border: 'none',
              borderRadius: '16px',
              padding: space['2xl'],
              display: 'grid',
              gap: space.md,
              cursor: 'pointer',
              fontFamily: font.sans,
            }}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: space.lg }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
                {usd(bridge.principal)} over {bridge.term_days} days
              </span>
              <Pill tone={stageToneFor(stage.status)}>{titleCase(stage.status)}</Pill>
            </div>
            <span
              style={{
                fontSize: font.size['2xl'],
                fontWeight: font.weight.semibold,
                color: color.text,
                fontVariantNumeric: 'tabular-nums',
              }}
            >
              {usd(outstandingOf(bridge, today), { cents: true })}
            </span>
            <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
              {pastDue
                ? `${stage.days_past_due} days past due`
                : `Still to collect · due ${shortDate(bridge.due_date)}`}
            </span>
          </button>
        );
      })}

      {closed.length > 0 && (
        <Card>
          <div style={{ display: 'grid', gap: space.md }}>
            <SectionTitle>Past bridges</SectionTitle>
            <div style={{ display: 'grid' }}>
              {closed.map((bridge, i) => (
                <ListRow
                  key={bridge.id}
                  first={i === 0}
                  title={`${usd(bridge.amount)} over ${bridge.term_days} days`}
                  subtitle={`${OUTCOME_LABEL[bridge.outcome]} · ${shortDate(bridge.closed_at ?? bridge.opened_at)}`}
                  amount={`${bridge.days_outstanding} days`}
                  tone={OUTCOME_TONE[bridge.outcome]}
                />
              ))}
            </div>
          </div>
        </Card>
      )}

      <Card>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: space.lg, fontSize: font.size.md }}>
          <span style={{ color: color.textSecondary }}>
            {count} {count === 1 ? 'bridge' : 'bridges'} all time
          </span>
          <span style={{ color: color.text }}>{usd(covered)} covered</span>
        </div>
      </Card>
    </div>
  );
}
