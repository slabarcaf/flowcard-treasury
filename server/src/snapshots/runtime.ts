import { scheduleDailySnapshotSync, type DailySnapshotSchedule } from "./scheduler.js";
import type { SnapshotSyncWorker } from "./sync-worker.js";

/** Run a demo fixture sync at startup and continue on the configured daily schedule. */
export function startDemoSnapshotSync(
  worker: { run(): Promise<unknown> } | Pick<SnapshotSyncWorker, "run">,
  options: DailySnapshotSchedule,
): { stop: () => void } {
  void worker.run().catch((error: unknown) => options.onError?.(error));
  return scheduleDailySnapshotSync(worker, options);
}
