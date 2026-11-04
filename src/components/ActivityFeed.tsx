import type { FeedEvent } from '../state/types.js';

interface Props {
  events: FeedEvent[];
  /** Collapsed to a narrow rail so the queue gets the full width. */
  collapsed?: boolean;
  onToggle?: () => void;
}

const TYPE_COLORS: Record<FeedEvent['type'], string> = {
  decline: 'text-red-600',
  notification: 'text-sky-600',
  transfer: 'text-emerald-600',
  charge: 'text-violet-600',
  payment: 'text-emerald-600',
  deposit: 'text-teal-600',
  info: 'text-gray-500',
};

const TYPE_DOTS: Record<FeedEvent['type'], string> = {
  decline: 'bg-red-500',
  notification: 'bg-sky-500',
  transfer: 'bg-emerald-500',
  charge: 'bg-violet-500',
  payment: 'bg-emerald-500',
  deposit: 'bg-teal-500',
  info: 'bg-gray-400',
};

function Chevron({ direction }: { direction: 'left' | 'right' }) {
  return (
    <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d={direction === 'left' ? 'M15.75 19.5L8.25 12l7.5-7.5' : 'M8.25 4.5l7.5 7.5-7.5 7.5'} />
    </svg>
  );
}

export function ActivityFeed({ events, collapsed = false, onToggle }: Props) {
  if (collapsed) {
    return (
      <button
        onClick={onToggle}
        aria-label="Show activity"
        title="Show activity"
        className="w-10 shrink-0 border-l border-gray-200 bg-white hover:bg-gray-50 flex flex-col items-center gap-3 pt-4 text-gray-500 hover:text-gray-800 cursor-pointer"
      >
        <Chevron direction="left" />
        <span className="text-xs font-semibold [writing-mode:vertical-rl] rotate-180">Activity</span>
        {events.length > 0 && (
          <span className="text-[10px] font-mono bg-gray-100 rounded-full px-1.5 py-0.5">{events.length}</span>
        )}
      </button>
    );
  }

  return (
    <div className="w-80 shrink-0 border-l border-gray-200 bg-white">
      <div className="px-5 py-4 border-b border-gray-200 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-gray-900">
          Activity
        </h3>
        {onToggle && (
          <button
            onClick={onToggle}
            aria-label="Hide activity"
            title="Hide activity"
            className="text-gray-400 hover:text-gray-700 cursor-pointer"
          >
            <Chevron direction="right" />
          </button>
        )}
      </div>
      <div className="overflow-y-auto max-h-[calc(100vh-130px)] px-5 py-3 space-y-0.5">
        {events.length === 0 && (
          <p className="text-sm text-gray-400 py-8 text-center">No activity yet</p>
        )}
        {events.map((ev, i) => (
          <div
            key={ev.id}
            className="flex gap-3 py-2.5 border-b border-gray-100 last:border-0"
            style={{
              animation: i === 0 ? 'fadeSlideDown 0.3s ease-out' : undefined,
            }}
          >
            <div className="flex flex-col items-center pt-1.5">
              <div className={`w-1.5 h-1.5 rounded-full ${TYPE_DOTS[ev.type]}`} />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-[10px] font-mono text-gray-400 mb-0.5">
                {ev.time}
              </div>
              <div className={`text-xs leading-relaxed ${TYPE_COLORS[ev.type]}`}>
                {ev.text}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
