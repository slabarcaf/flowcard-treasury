import type { AirwallexGateway } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { reconcileProviderOperations } from "./service.js";

/** Periodically recovers missed provider events and maintains funding holds. */
export class ReconciliationWorker {
  private timer: ReturnType<typeof setInterval> | undefined;
  private running = false;

  constructor(
    private readonly pool: DatabasePool,
    private readonly gateway: AirwallexGateway,
    private readonly intervalMs = 30_000,
  ) {}

  start(): void {
    if (this.timer) return;
    this.timer = setInterval(() => { void this.runOnce(); }, this.intervalMs);
    this.timer.unref?.();
    void this.runOnce().catch((error: unknown) => console.error("Reconciliation worker failed", error));
  }

  stop(): void {
    if (!this.timer) return;
    clearInterval(this.timer);
    this.timer = undefined;
  }

  async runOnce(): Promise<boolean> {
    if (this.running) return false;
    this.running = true;
    try {
      await reconcileProviderOperations(this.pool, this.gateway);
      return true;
    } finally {
      this.running = false;
    }
  }
}
