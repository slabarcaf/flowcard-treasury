import type { AirwallexGateway } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { dispatchOne } from "./dispatcher.js";

/** Periodically dispatches durable outbox commands after the service starts. */
export class OutboxWorker {
  private timer: ReturnType<typeof setInterval> | undefined;
  private running = false;

  constructor(
    private readonly pool: DatabasePool,
    private readonly gateway: AirwallexGateway,
    private readonly intervalMs = 1_000,
  ) {}

  start(): void {
    if (this.timer) return;
    this.timer = setInterval(() => {
      void this.runOnce().catch((error: unknown) => console.error("Outbox worker failed", error));
    }, this.intervalMs);
    this.timer.unref?.();
    void this.runOnce().catch((error: unknown) => console.error("Outbox worker failed", error));
  }

  stop(): void {
    if (!this.timer) return;
    clearInterval(this.timer);
    this.timer = undefined;
  }

  /** Dispatches at most one queued command; safe for deterministic tests. */
  async runOnce(): Promise<boolean> {
    if (this.running) return false;
    this.running = true;
    try {
      return await dispatchOne(this.pool, this.gateway);
    } finally {
      this.running = false;
    }
  }
}
