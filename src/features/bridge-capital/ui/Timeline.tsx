/**
 * A bridge's life, decline to closing. Shared by the back office and the portal.
 *
 * Status colours come from the shared tone palette; the surrounding neutrals
 * are passed in, so the same component sits correctly on either surface.
 */
import type { TimelineEvent, TimelineEventType } from '../types.js';
import { font, radius, space } from './scale.js';
import { tone, type ToneName } from './tone.js';
import { shortDate, timeOf, titleCase } from './format.js';

const EVENT_TONE: Record<TimelineEventType, ToneName> = {
  DECLINE: 'critical',
  OFFER: 'accent',
  ACCEPTANCE: 'accent',
  APPROVAL: 'accent',
  DISBURSEMENT: 'positive',
  DEPOSIT: 'positive',
  COLLECTION: 'positive',
  STATUS_CHANGE: 'warning',
  NOTICE: 'warning',
};

export interface TimelineColors {
  text: string;
  textMuted: string;
  border: string;
}

export function Timeline({
  events,
  colors,
  emptyLabel = 'No events yet',
}: {
  events: TimelineEvent[];
  colors: TimelineColors;
  emptyLabel?: string;
}) {
  if (events.length === 0) {
    return (
      <div style={{ padding: space['3xl'], textAlign: 'center', color: colors.textMuted, fontSize: font.size.md }}>
        {emptyLabel}
      </div>
    );
  }

  return (
    <div style={{ display: 'grid' }}>
      {events.map((event, i) => {
        const t = tone[EVENT_TONE[event.type]];
        const isLast = i === events.length - 1;
        return (
          <div key={event.id} style={{ display: 'flex', gap: space.lg }}>
            {/* Rail */}
            <div style={{ display: 'grid', justifyItems: 'center', width: '16px', flexShrink: 0 }}>
              <span
                style={{
                  width: '9px',
                  height: '9px',
                  borderRadius: radius.pill,
                  background: t.fg,
                  marginTop: '5px',
                }}
              />
              {!isLast && <span style={{ width: '1px', flex: 1, background: colors.border }} />}
            </div>

            <div style={{ display: 'grid', gap: space.xs, paddingBottom: isLast ? 0 : space.xl, minWidth: 0 }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: space.md, flexWrap: 'wrap' }}>
                <span
                  style={{
                    fontSize: font.size['2xs'],
                    fontWeight: font.weight.semibold,
                    letterSpacing: '0.06em',
                    textTransform: 'uppercase',
                    color: t.fg,
                  }}
                >
                  {titleCase(event.type)}
                </span>
                <span
                  style={{
                    fontSize: font.size.xs,
                    color: colors.textMuted,
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {shortDate(event.at)}
                  {timeOf(event.at) !== '' ? ` · ${timeOf(event.at)}` : ''}
                </span>
              </div>
              <span style={{ fontSize: font.size.md, color: colors.text, lineHeight: 1.5 }}>
                {event.text}
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}
