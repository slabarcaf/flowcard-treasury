/**
 * The approval door: approve as asked, or at terms the officer chooses.
 *
 * Nothing is decided here. Changing the amount or the term re-prices through
 * `priceOffer` and re-runs `evaluate`, and the panel shows what the engine
 * says about the amended request before anything is committed.
 */
import { useState } from 'react';
import type { BridgeRequest, Customer, PlatformCapital } from '../../types.js';
import { evaluate } from '../../policy/eligibility.js';
import { buildOffer, isValidTerm } from '../../policy/pricing.js';
import { PRICING } from '../../policy/policy.config.js';
import { color, font, radius, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import { Badge, Button, Label, Row } from './primitives.js';

function NumberField({
  label,
  value,
  onChange,
  prefix,
  suffix,
}: {
  label: string;
  value: number;
  onChange: (next: number) => void;
  prefix?: string;
  suffix?: string;
}) {
  return (
    <div style={{ display: 'grid', gap: space.sm }}>
      <Label>{label}</Label>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: space.sm,
          background: color.surfaceSubtle,
          borderRadius: radius.lg,
          padding: `${space.md} ${space.lg}`,
        }}
      >
        {prefix !== undefined && <span style={{ color: color.textMuted }}>{prefix}</span>}
        <input
          type="number"
          value={value}
          onChange={(e) => onChange(Number(e.target.value))}
          style={{
            width: '100%',
            background: 'transparent',
            border: 'none',
            outline: 'none',
            color: color.text,
            fontFamily: font.sans,
            fontSize: font.size.lg,
            fontVariantNumeric: 'tabular-nums',
          }}
        />
        {suffix !== undefined && <span style={{ color: color.textMuted }}>{suffix}</span>}
      </div>
    </div>
  );
}

export function ApprovalPanel({
  request,
  customer,
  capital,
  today,
  onApprove,
  onDecline,
  onEscalate,
}: {
  request: BridgeRequest;
  customer: Customer;
  capital: PlatformCapital;
  today: string;
  onApprove: (principal: number, termDays: number) => void;
  onDecline: (reason: string) => void;
  onEscalate: () => void;
}) {
  const [principal, setPrincipal] = useState(request.principal);
  const [termDays, setTermDays] = useState(request.term_days);
  const [editing, setEditing] = useState(false);

  const changed = principal !== request.principal || termDays !== request.term_days;
  const termValid = isValidTerm(termDays);
  const principalValid = principal > 0;
  const usable = termValid && principalValid;

  // Re-run the engine against the amended request, so the officer sees what the
  // rules say about what they are actually about to approve.
  const amended: BridgeRequest = { ...request, principal, term_days: termDays };
  const decision = usable ? evaluate(amended, customer, capital, { today }) : null;
  const offer = usable ? buildOffer(amended, principal, termDays) : null;
  const failed = decision?.rules.filter((r) => !r.passed) ?? [];

  return (
    <div style={{ display: 'grid', gap: space.lg }}>
      {editing && (
        <div style={{ display: 'grid', gap: space.lg, background: color.bg, borderRadius: radius.xl, padding: space.xl }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: space.lg }}>
            <NumberField label="Amount" value={principal} onChange={setPrincipal} prefix="$" />
            <NumberField label="Term" value={termDays} onChange={setTermDays} suffix="days" />
          </div>

          <div style={{ display: 'flex', gap: space.sm, flexWrap: 'wrap' }}>
            {PRICING.presetTermDays.map((d) => (
              <button
                key={d}
                type="button"
                onClick={() => setTermDays(d)}
                style={{
                  background: d === termDays ? color.accentSubtle : color.surfaceSubtle,
                  color: d === termDays ? color.accent : color.textSecondary,
                  border: 'none',
                  borderRadius: radius.pill,
                  padding: `${space.xs} ${space.lg}`,
                  fontSize: font.size.sm,
                  fontFamily: font.sans,
                  cursor: 'pointer',
                }}
              >
                {d} days
              </button>
            ))}
          </div>

          {!termValid && (
            <div style={{ color: color.critical, fontSize: font.size.sm }}>
              Term must be a whole number of days between {PRICING.minCustomTermDays} and{' '}
              {PRICING.maxCustomTermDays}.
            </div>
          )}
          {!principalValid && (
            <div style={{ color: color.critical, fontSize: font.size.sm }}>
              Amount must be more than zero.
            </div>
          )}

          {offer !== null && decision !== null && (
            <div style={{ display: 'grid', gap: space.md }}>
              <div>
                <Row label="Fee at these terms" value={usd(offer.fee, { cents: true })} />
                <Row label="Repayable" value={usd(offer.principal + offer.fee, { cents: true })} />
                <Row label="Due" value={shortDate(offer.due_date)} />
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: space.md, flexWrap: 'wrap' }}>
                {decision.eligible ? (
                  <Badge tone="positive">Still passes every rule</Badge>
                ) : (
                  <Badge tone="critical">
                    {failed.length} {failed.length === 1 ? 'rule' : 'rules'} would fail
                  </Badge>
                )}
                {failed.length > 0 && (
                  <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>
                    {failed.map((r) => r.label).join(' · ')}
                  </span>
                )}
              </div>
            </div>
          )}

          {changed && (
            <div
              style={{
                background: color.warningSubtle,
                borderRadius: radius.lg,
                padding: space.lg,
                fontSize: font.size.sm,
                color: color.text,
                lineHeight: 1.5,
              }}
            >
              These are not the terms the customer asked for. Approving voids any acceptance they
              have already given, and a new offer at {usd(principal)} over {termDays} days goes
              back to them before anything moves (§5, §7).
            </div>
          )}
        </div>
      )}

      <div style={{ display: 'flex', gap: space.md, flexWrap: 'wrap' }}>
        <Button
          variant="primary"
          disabled={!usable || decision?.eligible !== true}
          onClick={() => onApprove(principal, termDays)}
        >
          {offer === null
            ? 'Approve'
            : `Approve ${usd(offer.principal)} for ${customer.name} — ${offer.term_days} days, fee ${usd(offer.fee, { cents: true })}`}
        </Button>
        <Button onClick={() => setEditing((e) => !e)}>
          {editing ? 'Keep the asked terms' : 'Approve at different terms'}
        </Button>
        <Button variant="danger" onClick={() => onDecline('Declined on review.')}>
          Decline
        </Button>
        <Button onClick={onEscalate}>Escalate</Button>
      </div>

      <div style={{ fontSize: font.size.sm, color: color.textMuted, lineHeight: 1.5 }}>
        {decision !== null && !decision.eligible
          ? 'Approval is closed at these terms: a person decides where the regime asks for one, not whether a rule applies. Change the amount or the term until every rule passes, or decline.'
          : 'Approval is bound to this customer, amount, fee and term. Change any of them and it is void, and a new offer has to be issued.'}
      </div>
    </div>
  );
}
