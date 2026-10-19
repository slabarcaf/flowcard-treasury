import { useEffect, useRef, useState, useCallback } from 'react';
import type { FeedEvent } from '../state/types.js';

interface ToastItem {
  event: FeedEvent;
  exiting: boolean;
}

const DOT_COLOR: Record<FeedEvent['type'], string> = {
  decline: 'bg-red-500',
  notification: 'bg-sky-500',
  transfer: 'bg-emerald-500',
  charge: 'bg-violet-500',
  payment: 'bg-emerald-500',
  deposit: 'bg-teal-500',
  info: 'bg-gray-400',
};

const TEXT_COLOR: Record<FeedEvent['type'], string> = {
  decline: 'text-red-700',
  notification: 'text-sky-700',
  transfer: 'text-emerald-700',
  charge: 'text-violet-700',
  payment: 'text-emerald-700',
  deposit: 'text-teal-700',
  info: 'text-gray-600',
};

const BORDER_COLOR: Record<FeedEvent['type'], string> = {
  decline: 'border-red-200',
  notification: 'border-sky-200',
  transfer: 'border-emerald-200',
  charge: 'border-violet-200',
  payment: 'border-emerald-200',
  deposit: 'border-teal-200',
  info: 'border-gray-200',
};

interface Props {
  events: FeedEvent[];
}

export function FeedToast({ events }: Props) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const prevLenRef = useRef(events.length);
  const timersRef = useRef<ReturnType<typeof setTimeout>[]>([]);

  const scheduleRemoval = useCallback((eventId: string) => {
    // Start exit animation after 3.5s
    const t1 = setTimeout(() => {
      setToasts((prev) =>
        prev.map((t) => (t.event.id === eventId ? { ...t, exiting: true } : t)),
      );
    }, 3500);

    // Remove from DOM after exit animation (300ms)
    const t2 = setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.event.id !== eventId));
    }, 3800);

    timersRef.current.push(t1, t2);
  }, []);

  useEffect(() => {
    const prevLen = prevLenRef.current;
    const curLen = events.length;
    prevLenRef.current = curLen;

    if (curLen <= prevLen) {
      // Reset happened — clear all toasts and timers
      if (curLen < prevLen) {
        for (const t of timersRef.current) clearTimeout(t);
        timersRef.current = [];
        setToasts([]);
      }
      return;
    }

    const newEvents = events.slice(prevLen);
    const newToasts = newEvents.map((event) => ({ event, exiting: false }));
    setToasts((prev) => [...prev, ...newToasts]);

    for (const ev of newEvents) {
      scheduleRemoval(ev.id);
    }
  }, [events, scheduleRemoval]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      for (const t of timersRef.current) clearTimeout(t);
    };
  }, []);

  if (toasts.length === 0) return null;

  return (
    <div className="fixed top-4 right-6 z-50 flex flex-col gap-2 pointer-events-none" style={{ maxWidth: 360 }}>
      {toasts.map((toast) => (
        <div
          key={toast.event.id}
          className={`pointer-events-auto bg-white border ${BORDER_COLOR[toast.event.type]} rounded-lg shadow-lg px-4 py-3 flex items-start gap-3 ${
            toast.exiting ? 'animate-toast-out' : 'animate-toast-in'
          }`}
        >
          <div className={`w-2 h-2 rounded-full mt-1.5 shrink-0 ${DOT_COLOR[toast.event.type]}`} />
          <div className="min-w-0">
            <div className="text-[10px] font-mono text-gray-400">{toast.event.time}</div>
            <div className={`text-xs leading-relaxed ${TEXT_COLOR[toast.event.type]}`}>
              {toast.event.text}
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}
