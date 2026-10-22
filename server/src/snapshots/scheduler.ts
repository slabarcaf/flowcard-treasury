import type { SnapshotSyncWorker } from "./sync-worker.js";

export interface DailySnapshotSchedule {
  runAtUtc: string;
  now?: () => Date;
  setTimer?: typeof setTimeout;
  clearTimer?: typeof clearTimeout;
  onError?: (error: unknown) => void;
}

const MAX_TIMER_DELAY_MS = 2_147_483_647;

/** Parse a daily UTC clock time; defaults to 02:00 UTC. */
export function snapshotDailyRunTime(env: NodeJS.ProcessEnv = process.env): string {
  const value = env.SALESFORCE_SNAPSHOT_DAILY_RUN_UTC?.trim() || "02:00";
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(value)) {
    throw new Error("SALESFORCE_SNAPSHOT_DAILY_RUN_UTC must use HH:mm in UTC");
  }
  return value;
}

/** Schedule exactly one run per UTC day, recalculating after each completion to avoid overlap. */
export function scheduleDailySnapshotSync(worker: Pick<SnapshotSyncWorker, "run">, options: DailySnapshotSchedule): { stop: () => void } {
  const [hour, minute] = parseClockTime(options.runAtUtc);
  const now = options.now ?? (() => new Date());
  const setTimer = options.setTimer ?? setTimeout;
  const clearTimer = options.clearTimer ?? clearTimeout;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;

  const scheduleNext = (): void => {
    if (stopped) return;
    const current = now();
    const next = new Date(current);
    next.setUTCHours(hour, minute, 0, 0);
    if (next.getTime() <= current.getTime()) next.setUTCDate(next.getUTCDate() + 1);
    const delay = Math.min(next.getTime() - current.getTime(), MAX_TIMER_DELAY_MS);
    timer = setTimer(() => {
      void worker.run().catch((error: unknown) => options.onError?.(error)).finally(scheduleNext);
    }, delay);
  };

  scheduleNext();
  return {
    stop() {
      stopped = true;
      if (timer !== undefined) clearTimer(timer);
    },
  };
}

function parseClockTime(value: string): [number, number] {
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(value)) throw new Error("Daily snapshot run time must use HH:mm in UTC");
  const [hour, minute] = value.split(":").map(Number);
  return [hour!, minute!];
}
