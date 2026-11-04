/** Balance, recent card spend, and a banner for any bridge that is running. */
import type { Bridge, CardTransaction } from '../../types.js';
import { applyDelinquency } from '../../policy/delinquency.js';
import { color, font, radius, space, stageToneFor } from '../tokens.js';
import { usd, shortDate, titleCase } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { Amount, Card, ListRow, Pill, SectionTitle } from '../components/ui.js';

function BridgeBanner({ bridge, today, onOpen }: { bridge: Bridge; today: string; onOpen: () => void }) {
  const stage = applyDelinquency(bridge, today);
  const pastDue = stage.days_past_due > 0;
  const outstanding = bridge.principal_outstanding + bridge.fee_outstanding + stage.accrued_fee;

  return (
    <button
      type="button"
      onClick={onOpen}
      style={{
        width: '100%',
        textAlign: 'left',
        background: pastDue ? color.criticalSubtle : color.surface,
        border: 'none',
        borderRadius: radius.xl,
        padding: space['2xl'],
        display: 'grid',
        gap: space.md,
        cursor: 'pointer',
        fontFamily: font.sans,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg }}>
        <span style={{ fontSize: font.size.md, fontWeight: font.weight.semibold, color: color.text }}>
          Bridge running
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
        {usd(outstanding, { cents: true })}
      </span>
      <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
        {pastDue
          ? `${stage.days_past_due} days past due — tap to see what happens next`
          : `Due ${shortDate(bridge.due_date)} · collected from your deposits`}
      </span>
    </button>
  );
}

export function WalletHome({ portal }: { portal: Portal }) {
  const { data, customer, today, go } = portal;
  if (data === null || customer === null) return null;

  const balance = customer.balance_history.at(-1)?.balance ?? 0;
  const transactions = data.transactions
    .filter((t) => t.customer_id === customer.id)
    .sort((a, b) => b.created_at.localeCompare(a.created_at));
  const bridges = data.bridges.filter((b) => b.customer_id === customer.id && b.status !== 'REPAID');

  const requestFor = (txn: CardTransaction) =>
    data.requests.find((r) => r.transaction.id === txn.id);

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <Card>
        <Amount label="Wallet balance" value={usd(balance)} />
      </Card>

      {bridges.map((bridge) => (
        <BridgeBanner
          key={bridge.id}
          bridge={bridge}
          today={today}
          onOpen={() => go({ screen: 'bridge', bridgeId: bridge.id })}
        />
      ))}

      <Card>
        <div style={{ display: 'grid', gap: space.md }}>
          <SectionTitle>Recent card activity</SectionTitle>
          <div style={{ display: 'grid' }}>
            {transactions.map((txn, i) => {
              const declined = txn.status === 'DECLINED';
              const request = requestFor(txn);
              return (
                <ListRow
                  key={txn.id}
                  first={i === 0}
                  title={txn.merchant_name}
                  subtitle={
                    declined ? (
                      <span style={{ color: color.critical }}>
                        Declined · {shortDate(txn.created_at)}
                      </span>
                    ) : (
                      `${titleCase(txn.merchant_category)} · ${shortDate(txn.created_at)}`
                    )
                  }
                  amount={usd(txn.amount)}
                  tone={declined ? 'critical' : undefined}
                  onClick={
                    declined && request !== undefined
                      ? () => go({ screen: 'decline', transactionId: txn.id })
                      : undefined
                  }
                />
              );
            })}
          </div>
        </div>
      </Card>
    </div>
  );
}
