/** One request: what triggered it, who is asking, every rule, and the offer. */
import type { Customer } from '../../types.js';
import { evaluate } from '../../policy/eligibility.js';
import { buildOffer } from '../../policy/pricing.js';
import { outstandingBalance } from '../../policy/tiers.js';
import { REGIME_LABELS } from '../../policy/policy.config.js';
import {
  CRITICALITY_TONE,
  REGIME_TONE,
  TIER_TONE,
  color,
  font,
  radius,
  space,
} from '../tokens.js';
import { usd, pct, shortDate, timeOf, titleCase } from '../../ui/format.js';
import type { BackOffice } from '../useBackOffice.js';
import {
  Badge,
  Breadcrumb,
  Button,
  Card,
  CardHeader,
  Empty,
  Label,
  Row,
  Stat,
  Table,
  Td,
  Th,
} from '../components/primitives.js';
import { BalanceChart } from '../components/BalanceChart.js';
import { RuleList } from '../components/RuleList.js';

function LimitBar({ customer, limit }: { customer: Customer; limit: number }) {
  const used = outstandingBalance(customer);
  const usedPct = limit === 0 ? 0 : Math.min(1, used / limit);
  return (
    <div style={{ display: 'grid', gap: space.md }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
        <Label>Limit used</Label>
        <span style={{ fontSize: font.size.sm, color: color.textMuted, fontVariantNumeric: 'tabular-nums' }}>
          {usd(used)} of {usd(limit)}
        </span>
      </div>
      <div
        style={{
          height: '8px',
          borderRadius: radius.pill,
          background: color.surfaceSubtle,
          overflow: 'hidden',
        }}
      >
        <div style={{ width: `${usedPct * 100}%`, height: '100%', background: color.accent }} />
      </div>
      <div style={{ fontSize: font.size.sm, color: color.textMuted }}>
        {usd(Math.max(0, limit - used))} of limit still available
      </div>
    </div>
  );
}

export function RequestDetailScreen({ office, requestId }: { office: BackOffice; requestId: string }) {
  const { data, today, go, outcomes, approve, decline, escalate } = office;
  if (data === null) return <Empty>Loading…</Empty>;

  const request = data.requests.find((r) => r.id === requestId);
  const customer = request === undefined ? undefined : data.customers.find((c) => c.id === request.customer_id);
  if (request === undefined || customer === undefined) {
    return <Empty>Request not found</Empty>;
  }

  const decision = evaluate(request, customer, data.capital, { today });
  const offer = buildOffer(request);
  const txn = request.transaction;
  const outcome = outcomes[request.id];
  const showActions = decision.needs_human_approval && outcome === undefined;
  const b = customer.balance_stats;

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <div style={{ display: 'grid', gap: space.lg }}>
        <Breadcrumb
          items={[
            { label: 'Overview', onClick: () => go({ screen: 'overview' }) },
            { label: customer.name },
          ]}
        />
        <div style={{ display: 'flex', alignItems: 'center', gap: space.lg, flexWrap: 'wrap' }}>
          <h1
            style={{
              margin: 0,
              fontSize: font.size['2xl'],
              fontWeight: font.weight.semibold,
              letterSpacing: '-0.02em',
              color: color.text,
            }}
          >
            {customer.name}
          </h1>
          <Badge tone={TIER_TONE[decision.tier]}>
            {decision.tier === 'NONE' ? 'No tier' : `Tier ${decision.tier}`}
          </Badge>
          <Badge tone={REGIME_TONE[decision.regime]}>{REGIME_LABELS[decision.regime]}</Badge>
          {outcome !== undefined && (
            <Badge tone={outcome === 'APPROVED' ? 'positive' : outcome === 'DECLINED' ? 'critical' : 'warning'}>
              {titleCase(outcome)}
            </Badge>
          )}
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1.25fr) minmax(0, 1fr)', gap: space.xl, alignItems: 'start' }}>
        {/* Left: decline, rules, offer */}
        <div style={{ display: 'grid', gap: space.xl }}>
          <Card>
            <CardHeader
              title="Triggering decline"
              meta={`${shortDate(txn.created_at)} · ${timeOf(txn.created_at)}`}
            />
            <div style={{ display: 'flex', gap: space['3xl'], flexWrap: 'wrap', marginBottom: space.xl }}>
              <Stat label="Payment" value={usd(txn.amount)} sub={txn.merchant_name} />
              <Stat label="Wallet balance" value={usd(txn.wallet_balance)} />
              <Stat label="Shortfall" value={usd(request.principal)} tone="critical" />
            </div>
            <div>
              <Row label="Decline reason" value={<span style={{ fontFamily: font.mono, fontSize: font.size.sm }}>{txn.decline_reason}</span>} />
              <Row label="Merchant category" value={titleCase(txn.merchant_category)} />
              <Row
                label="Spend criticality"
                value={<Badge tone={CRITICALITY_TONE[txn.criticality]}>{titleCase(txn.criticality)}</Badge>}
              />
              <Row label="Card" value={<span style={{ fontFamily: font.mono, fontSize: font.size.sm }}>{txn.card_id}</span>} />
            </div>
          </Card>

          <Card>
            <CardHeader
              title="Eligibility"
              meta={
                decision.eligible ? (
                  <Badge tone="positive">All rules pass</Badge>
                ) : (
                  <Badge tone="critical">
                    {decision.rules.filter((r) => !r.passed).length} of {decision.rules.length} failed
                  </Badge>
                )
              }
            />
            <RuleList rules={decision.rules} />
          </Card>

          <Card>
            <CardHeader title="Offer" meta={decision.needs_human_approval ? <Badge tone="warning">Approval required</Badge> : <Badge tone="positive">Within auto-approve</Badge>} />
            <div style={{ display: 'flex', gap: space['3xl'], flexWrap: 'wrap', marginBottom: space.xl }}>
              <Stat label="Amount" value={usd(offer.principal)} />
              <Stat label="Term" value={`${offer.term_days} days`} sub={`due ${shortDate(offer.due_date)}`} />
              <Stat label="Fee" value={usd(offer.fee, { cents: true })} sub="$50 service + 2%/month, full term" />
              <Stat label="Repayable" value={usd(offer.principal + offer.fee, { cents: true })} />
            </div>

            {showActions ? (
              <div style={{ display: 'grid', gap: space.lg }}>
                <div style={{ display: 'flex', gap: space.md, flexWrap: 'wrap' }}>
                  <Button variant="primary" onClick={() => void approve(request, offer)}>
                    Approve {usd(offer.principal)} for {customer.name} — {offer.term_days} days, fee{' '}
                    {usd(offer.fee, { cents: true })}
                  </Button>
                  <Button variant="danger" onClick={() => void decline(request, decision.reasons[0] ?? 'Declined on review.')}>
                    Decline
                  </Button>
                  <Button onClick={() => void escalate(request)}>Escalate</Button>
                </div>
                <div style={{ fontSize: font.size.sm, color: color.textMuted, lineHeight: 1.5 }}>
                  Approval is bound to this customer, amount, fee and term. Change any of them and it
                  is void, and a new offer has to be issued.
                </div>
              </div>
            ) : (
              <div style={{ fontSize: font.size.md, color: color.textSecondary }}>
                {outcome !== undefined
                  ? `A credit officer has already ${outcome.toLowerCase()} this request.`
                  : decision.eligible
                    ? `No approval needed in ${REGIME_LABELS[decision.regime]} at this amount.`
                    : 'Nothing to approve: the request fails eligibility.'}
              </div>
            )}
          </Card>
        </div>

        {/* Right: customer snapshot */}
        <div style={{ display: 'grid', gap: space.xl }}>
          <Card>
            <CardHeader title="Customer snapshot" meta={`${customer.tenure_months} months on platform`} />
            <div style={{ display: 'grid', gap: space.xl }}>
              <LimitBar customer={customer} limit={decision.limit} />
              <div>
                <Row label="Monthly inflows" value={usd(customer.inflow_stats.monthly_inflows)} />
                <Row label="Top payer concentration" value={pct(customer.inflow_stats.top_payer_concentration)} />
                <Row label="Avg balance 30d" value={usd(b.avg_balance_30d)} />
                <Row label="Avg balance 90d" value={usd(b.avg_balance_90d)} />
                <Row label="Avg balance 180d" value={usd(b.avg_balance_180d)} />
                <Row label="Trend (30d vs 180d)" value={titleCase(b.balance_trend)} />
                <Row label="Low point (90d, 10th pct)" value={usd(b.low_point_balance_90d)} />
                <Row
                  label="Days near zero (90d)"
                  value={
                    <span style={{ color: b.days_near_zero_90d > 5 ? color.critical : color.text }}>
                      {b.days_near_zero_90d}
                    </span>
                  }
                />
              </div>
              <BalanceChart history={customer.balance_history} stats={b} />
            </div>
          </Card>

          <Card padded={false}>
            <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
              <CardHeader
                title="Bridge history"
                meta={`${customer.bridge_stats.on_time_count} on time · ${customer.bridge_stats.late_count} late`}
              />
            </div>
            {customer.bridge_history.length === 0 ? (
              <Empty>No bridges yet</Empty>
            ) : (
              <Table>
                <thead>
                  <tr>
                    <Th>Opened</Th>
                    <Th align="right">Amount</Th>
                    <Th align="right">Days out</Th>
                    <Th>Outcome</Th>
                  </tr>
                </thead>
                <tbody>
                  {[...customer.bridge_history]
                    .sort((x, y) => y.opened_at.localeCompare(x.opened_at))
                    .map((br) => (
                      <tr key={br.id}>
                        <Td muted>{shortDate(br.opened_at)}</Td>
                        <Td align="right">{usd(br.amount)}</Td>
                        <Td align="right" muted>{br.days_outstanding}</Td>
                        <Td>
                          <Badge
                            tone={
                              br.outcome === 'ON_TIME'
                                ? 'positive'
                                : br.outcome === 'LATE'
                                  ? 'warning'
                                  : br.outcome === 'DEFAULTED'
                                    ? 'critical'
                                    : 'accent'
                            }
                          >
                            {titleCase(br.outcome)}
                          </Badge>
                        </Td>
                      </tr>
                    ))}
                </tbody>
              </Table>
            )}
          </Card>

          <Card padded={false}>
            <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
              <CardHeader title="Payer track record" meta="Expected before the term ends" />
            </div>
            {customer.expected_receivables.length === 0 ? (
              <Empty>No expected receivables</Empty>
            ) : (
              <Table>
                <thead>
                  <tr>
                    <Th>Payer</Th>
                    <Th align="right">Amount</Th>
                    <Th align="right">Due</Th>
                    <Th align="right">On time</Th>
                  </tr>
                </thead>
                <tbody>
                  {customer.expected_receivables.map((r) => (
                    <tr key={`${r.payer_name}-${r.due_date}`}>
                      <Td>
                        <div style={{ display: 'grid' }}>
                          <span>{r.payer_name}</span>
                          <span style={{ fontSize: font.size.sm, color: color.textMuted }}>
                            {r.relationship_months} months · avg {r.payer_avg_days_late}d late
                          </span>
                        </div>
                      </Td>
                      <Td align="right">{usd(r.amount)}</Td>
                      <Td align="right" muted>{shortDate(r.due_date)}</Td>
                      <Td align="right">
                        <span style={{ color: r.payer_on_time_rate < 0.7 ? color.critical : color.text }}>
                          {pct(r.payer_on_time_rate, 0)}
                        </span>
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            )}
          </Card>

          <Card>
            <CardHeader title="CRM notes" meta="Context only — not used in scoring" />
            <div style={{ display: 'grid', gap: space.lg }}>
              <div>
                <Row label="Onboarded" value={shortDate(customer.crm.onboarding_date)} />
                <Row label="Contract" value={customer.crm.contract_status} />
              </div>
              {customer.crm.support_notes.map((note) => (
                <div
                  key={note.timestamp}
                  style={{
                    background: color.surfaceSubtle,
                    borderRadius: radius.md,
                    padding: space.lg,
                    display: 'grid',
                    gap: space.sm,
                  }}
                >
                  <div style={{ fontSize: font.size.xs, color: color.textMuted }}>
                    {note.author} · {shortDate(note.timestamp)}
                  </div>
                  <div style={{ fontSize: font.size.md, color: color.text, lineHeight: 1.5 }}>
                    {note.text}
                  </div>
                </div>
              ))}
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
