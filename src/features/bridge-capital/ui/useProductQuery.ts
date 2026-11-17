import { useEffect, useRef, useState } from 'react';
export interface ProductQuery<T> {
  data: T | null;
  busy: boolean;
  error: string;
  refresh: () => void;
}
/** One visible source at a time. Polling performs reads, never commands. */
export function useProductQuery<T>(key: string, load: () => Promise<T>, poll = 0): ProductQuery<T> {
  const loader = useRef(load);
  useEffect(() => {
    loader.current = load;
  }, [load]);
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<{ key: string; data: T | null; busy: boolean; error: string }>(
    { key, data: null, busy: true, error: '' }
  );
  useEffect(() => {
    let active = true,
      running = false;
    let failures = 0;
    let retryAt = 0;
    async function read() {
      if (!active || running) return;
      running = true;
      setState((old) => ({ key, data: old.key === key ? old.data : null, busy: true, error: '' }));
      try {
        const data = await loader.current();
        failures = 0;
        retryAt = 0;
        if (active) setState({ key, data, busy: false, error: '' });
      } catch (error) {
        failures++;
        retryAt = Date.now() + Math.min(30000, (poll || 1000) * 2 ** Math.min(failures, 4));
        if (active)
          setState((old) => ({
            key,
            data: old.key === key ? old.data : null,
            busy: false,
            error: error instanceof Error ? error.message : 'Source is unavailable.',
          }));
      } finally {
        running = false;
      }
    }
    void read();
    const tick = () => {
      if (document.visibilityState !== 'hidden' && Date.now() >= retryAt) void read();
    };
    const timer = poll ? setInterval(tick, poll) : null;
    document.addEventListener('visibilitychange', tick);
    return () => {
      active = false;
      if (timer) clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
    };
  }, [key, poll, revision]);
  return {
    data: state.key === key ? state.data : null,
    busy: state.key !== key || state.busy,
    error: state.key === key ? state.error : '',
    refresh: () => setRevision((x) => x + 1),
  };
}
