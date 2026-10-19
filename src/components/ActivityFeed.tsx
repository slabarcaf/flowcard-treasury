import type { FeedEvent } from '../state/types.js';

interface Props {
  events: FeedEvent[];
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

export function ActivityFeed({ events }: Props) {
  return (
    <div className="w-80 shrink-0 border-l border-gray-200 bg-white">
      <div className="px-5 py-4 border-b border-gray-200">
        <h3 className="text-sm font-semibold text-gray-900">
          Activity
        </h3>
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
