/**
 * A demo control panel: the engine's thresholds, as inputs.
 *
 * Every field writes straight into the policy config the engine reads, so the
 * next decision uses the new number. It is deliberately not an ops screen —
 * there is no audit trail and nothing is saved.
 */
import { useState } from 'react';
import { color, font, radius, space } from '../tokens.js';
import { usd, pct } from '../../ui/format.js';
import {
  CONFIG_FIELDS,
  CONFIG_GROUPS,
  isModified,
  modifiedCount,
  resetPolicyConfig,
  specDefault,
  type ConfigField,
} from '../adminConfig.js';
import type { BackOffice } from '../useBackOffice.js';
import { Badge, Button, Card, CardHeader, Label } from '../components/primitives.js';

function display(field: ConfigField, value: number): string {
  switch (field.kind) {
    case 'money':
      return usd(value);
    case 'percent':
      return pct(value, value < 0.1 ? 1 : 0);
    case 'days':
      return `${value} days`;
    case 'months':
      return `${value} months`;
  }
}

/** Inputs are in the unit an officer would say out loud: percents as percents. */
function toInput(field: ConfigField): number {
  return field.kind === 'percent' ? Number((field.get() * 100).toFixed(2)) : field.get();
}

function fromInput(field: ConfigField, raw: number): number {
  return field.kind === 'percent' ? raw / 100 : raw;
}

function Field({ field, onChange }: { field: ConfigField; onChange: () => void }) {
  const modified = isModified(field);
  return (
    <div
      style={{
        display: 'grid',
        gap: space.sm,
        padding: `${space.lg} 0`,
        boxShadow: `inset 0 -1px 0 ${color.border}`,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: space.md, flexWrap: 'wrap' }}>
        <Label>{field.label}</Label>
        {modified && <Badge tone="warning">Changed from spec</Badge>}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: space.lg }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: space.sm,
            background: color.surfaceSubtle,
            borderRadius: radius.lg,
            padding: `${space.sm} ${space.lg}`,
            width: '140px',
          }}
        >
          {field.kind === 'money' && <span style={{ color: color.textMuted }}>$</span>}
          <input
            type="number"
            step={field.kind === 'percent' ? 0.5 : 1}
            value={toInput(field)}
            onChange={(e) => {
              field.set(fromInput(field, Number(e.target.value)));
              onChange();
            }}
            style={{
              width: '100%',
              background: 'transparent',
              border: 'none',
              outline: 'none',
              color: color.text,
              fontFamily: font.sans,
              fontSize: font.size.md,
              fontVariantNumeric: 'tabular-nums',
            }}
          />
          {field.kind === 'percent' && <span style={{ color: color.textMuted }}>%</span>}
        </div>
        <span style={{ fontSize: font.size.sm, color: color.textMuted, lineHeight: 1.4 }}>
          {field.hint}
          {modified && ` · spec says ${display(field, specDefault(field.id))}`}
        </span>
      </div>
    </div>
  );
}

export function AdminScreen({ office }: { office: BackOffice }) {
  // The config lives outside React, so the screen keeps its own tick to
  // re-render after a write.
  const [, setTick] = useState(0);
  const bump = () => setTick((t) => t + 1);
  const changed = modifiedCount();

  return (
    <div style={{ display: 'grid', gap: space.xl, maxWidth: '860px' }}>
      <Card>
        <div style={{ display: 'grid', gap: space.lg }}>
          <div style={{ fontSize: font.size.md, color: color.textSecondary, lineHeight: 1.6 }}>
            A demo control panel, not an ops screen. These write straight into the thresholds the
            engine reads, so the next decision uses them — but nothing is saved, nothing is
            audited, and a reload puts the spec values back. The Rules screen shows whatever is set
            here.
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: space.lg, flexWrap: 'wrap' }}>
            <Button onClick={() => { resetPolicyConfig(); bump(); }} disabled={changed === 0}>
              Reset to the spec
            </Button>
            <span style={{ fontSize: font.size.sm, color: changed > 0 ? color.warning : color.textMuted }}>
              {changed === 0
                ? 'Everything is at its spec value.'
                : `${changed} ${changed === 1 ? 'threshold differs' : 'thresholds differ'} from the spec.`}
            </span>
            <Button variant="primary" onClick={() => office.go({ screen: 'overview' })}>
              See the queue re-decide
            </Button>
          </div>
        </div>
      </Card>

      {CONFIG_GROUPS.map((group) => (
        <Card key={group}>
          <CardHeader title={group} />
          <div style={{ display: 'grid' }}>
            {CONFIG_FIELDS.filter((f) => f.group === group).map((field) => (
              <Field key={field.id} field={field} onChange={bump} />
            ))}
          </div>
        </Card>
      ))}
    </div>
  );
}
