import type { AirwallexGateway } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { processWebhookEvent } from "./process.js";

/** Claims one pending inbox row, commits the claim, then processes outside the claim transaction. */
export class WebhookInboxWorker {
  private timer: ReturnType<typeof setInterval> | undefined;
  private activeRun: Promise<void> | undefined;

  constructor(
    private readonly pool: DatabasePool,
    private readonly gateway: AirwallexGateway,
    private readonly pollIntervalMs = 1_000,
  ) {}

  start(): void {
    if (this.timer) return;
    this.timer = setInterval(() => this.nudge(), this.pollIntervalMs);
    this.nudge();
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = undefined;
  }

  /** Safe to call on every delivery, including a duplicate event. */
  nudge(): void {
    if (!this.activeRun) {
      this.activeRun = this.drain().finally(() => { this.activeRun = undefined; });
    }
  }

  /** Exposed for deterministic tests and one-shot worker execution. */
  async processPendingOnce(): Promise<boolean> {
    const client = await this.pool.connect();
    let eventId: string | undefined;
    try {
      await client.query("BEGIN");
      const claim = await client.query<{ provider_event_id: string }>(
        `WITH candidate AS (
           SELECT provider_event_id
             FROM webhook_inbox
            WHERE processed_at IS NULL
              AND (processing_at IS NULL OR processing_at < now() - interval '1 minute')
            ORDER BY id
            LIMIT 1
            FOR UPDATE SKIP LOCKED
         )
         UPDATE webhook_inbox AS inbox
            SET processing_at = now()
           FROM candidate
          WHERE inbox.provider_event_id = candidate.provider_event_id
         RETURNING inbox.provider_event_id`,
      );
      eventId = claim.rows[0]?.provider_event_id;
      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
    if (!eventId) return false;
    try {
      await processWebhookEvent(this.pool, this.gateway, eventId);
    } catch (error) {
      // Clear the lease so the next poll can retry; durable observations are idempotent.
      await this.pool.query(
        "UPDATE webhook_inbox SET processing_at = NULL WHERE provider_event_id = $1 AND processed_at IS NULL",
        [eventId],
      );
      throw error;
    }
    return true;
  }

  private async drain(): Promise<void> {
    try {
      while (await this.processPendingOnce()) { /* Drain currently available rows. */ }
    } catch {
      // The polling timer retries transient database/provider failures.
    }
  }
}
