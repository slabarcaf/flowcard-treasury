import { randomUUID } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import {
  createServiceAssertion,
  DEFAULT_ISSUER,
  DEFAULT_WEBHOOK_AUDIENCE,
} from "../http/onboarding-proxy-auth.js";

export interface OnboardingForwarderOptions {
  pool: DatabasePool;
  onboardingServiceUrl: string;
  serviceSecret: string;
  pollIntervalMs?: number;
  leaseDurationMs?: number;
  requestTimeoutMs?: number;
  maxRetries?: number;
  fetchFn?: typeof fetch;
}

export interface ForwardingCandidate {
  id: number;
  provider_event_id: string;
  event_type: string;
  attempts: number;
  lease_token: string;
}

export class OnboardingWebhookForwarder {
  private timer: ReturnType<typeof setInterval> | undefined;
  private activeRun: Promise<void> | undefined;
  private isStopping = false;
  private readonly activeControllers = new Set<AbortController>();
  private readonly pool: DatabasePool;
  private readonly onboardingServiceUrl: string;
  private readonly serviceSecret: string;
  private readonly pollIntervalMs: number;
  private readonly leaseDurationMs: number;
  private readonly requestTimeoutMs: number;
  private readonly maxRetries: number;
  private readonly fetchFn: typeof fetch;

  constructor(options: OnboardingForwarderOptions) {
    this.pool = options.pool;
    this.onboardingServiceUrl = options.onboardingServiceUrl.replace(/\/+$/, "");
    this.serviceSecret = options.serviceSecret;
    this.pollIntervalMs = options.pollIntervalMs ?? 1_000;
    this.leaseDurationMs = options.leaseDurationMs ?? 30_000;
    this.requestTimeoutMs = options.requestTimeoutMs ?? 10_000;
    this.maxRetries = options.maxRetries ?? 10;
    this.fetchFn = options.fetchFn ?? globalThis.fetch;
  }

  start(): void {
    if (this.timer || this.isStopping) return;
    this.timer = setInterval(() => this.nudge(), this.pollIntervalMs);
    this.nudge();
  }

  async stop(): Promise<void> {
    this.isStopping = true;
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = undefined;
    }
    for (const controller of this.activeControllers) {
      controller.abort(new Error("forwarder_stopped"));
    }
    this.activeControllers.clear();
    if (this.activeRun) {
      await this.activeRun.catch(() => {});
    }
  }

  nudge(): void {
    if (this.isStopping) return;
    if (!this.activeRun) {
      this.activeRun = this.drain().finally(() => {
        this.activeRun = undefined;
      });
    }
  }

  /** Restart-safe scan enrollment: enqueues any webhook_inbox rows that lack a forwarding record. */
  async enrollMissedInboxRows(): Promise<number> {
    const result = await this.pool.query(
      `INSERT INTO onboarding_webhook_forwarding (provider_event_id, event_type)
       SELECT provider_event_id, event_type
         FROM webhook_inbox
        WHERE (event_type LIKE 'account.%' OR event_type LIKE 'rfi.%')
          AND provider_event_id NOT IN (SELECT provider_event_id FROM onboarding_webhook_forwarding)
       ON CONFLICT (provider_event_id) DO NOTHING`,
    );
    return result.rowCount ?? 0;
  }

  /** Claims one pending or expired-lease candidate, delivers to Go webhook inbox with signed JWT assertion. */
  async processPendingOnce(): Promise<boolean> {
    if (this.isStopping) return false;

    const leaseToken = randomUUID();
    const client = await this.pool.connect();
    let candidate: ForwardingCandidate | undefined;

    try {
      await client.query("BEGIN");
      const claim = await client.query<ForwardingCandidate>(
        `WITH candidate AS (
           SELECT id, provider_event_id, event_type, attempts
             FROM onboarding_webhook_forwarding
            WHERE (status IN ('queued', 'failed') AND next_retry_at <= now())
               OR (status = 'forwarding' AND leased_until < now())
            ORDER BY id
            LIMIT 1
            FOR UPDATE SKIP LOCKED
         )
         UPDATE onboarding_webhook_forwarding AS f
            SET status = 'forwarding',
                lease_token = $1,
                leased_until = now() + ($2 || ' milliseconds')::interval,
                attempts = f.attempts + 1
           FROM candidate
          WHERE f.id = candidate.id
          RETURNING f.id, f.provider_event_id, f.event_type, f.attempts, f.lease_token`,
        [leaseToken, this.leaseDurationMs],
      );
      candidate = claim.rows[0];
      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }

    if (!candidate) return false;

    // Retrieve raw body from canonical webhook_inbox (no DB lock held)
    const bodyResult = await this.pool.query<{ raw_body: Buffer }>(
      `SELECT raw_body FROM webhook_inbox WHERE provider_event_id = $1`,
      [candidate.provider_event_id],
    );

    if (!bodyResult.rows[0]?.raw_body) {
      // Quarantine with evidence for operator replay; do NOT silently ignore
      await this.pool.query(
        `UPDATE onboarding_webhook_forwarding
            SET status = 'quarantined',
                lease_token = NULL,
                leased_until = NULL,
                last_error = 'missing_canonical_raw_body: event payload missing from webhook_inbox'
          WHERE id = $1 AND lease_token = $2 AND status = 'forwarding' AND leased_until > now()`,
        [candidate.id, candidate.lease_token],
      );
      return true;
    }

    const rawBody = bodyResult.rows[0].raw_body;
    const payloadBody = typeof rawBody === "string" ? rawBody : new Uint8Array(rawBody);

    // Generate signed service JWT assertion for webhook delivery
    let assertion: string;
    try {
      assertion = createServiceAssertion({
        secret: this.serviceSecret,
        subject: "node-webhook-forwarder",
        issuer: DEFAULT_ISSUER,
        audience: DEFAULT_WEBHOOK_AUDIENCE,
        ttlSeconds: 30,
      });
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      await this.markFailedFenced(candidate.id, candidate.lease_token, candidate.attempts, `assertion generation failed: ${msg}`);
      throw err;
    }

    const targetUrl = `${this.onboardingServiceUrl}/api/webhooks/airwallex`;
    const controller = new AbortController();
    this.activeControllers.add(controller);
    const timeoutId = setTimeout(() => controller.abort(new Error("forwarding_timeout")), this.requestTimeoutMs);

    try {
      const response = await this.fetchFn(targetUrl, {
        method: "POST",
        headers: {
          authorization: `Bearer ${assertion}`,
          "content-type": "application/json",
          "x-provider-event-id": candidate.provider_event_id,
        },
        body: payloadBody,
        signal: controller.signal,
        redirect: "error", // Prevent secret/assertion or body redirection
      });

      if (response.status === 200 || response.status === 202) {
        // Fenced completion update: verifies lease_token to guarantee no race stomping
        const ackResult = await this.pool.query(
          `UPDATE onboarding_webhook_forwarding
              SET status = 'forwarded',
                  forwarded_at = now(),
                  lease_token = NULL,
                  leased_until = NULL,
                  last_error = NULL
            WHERE id = $1 AND lease_token = $2 AND status = 'forwarding' AND leased_until > now()`,
          [candidate.id, candidate.lease_token],
        );
        if ((ackResult.rowCount ?? 0) === 0) {
          // Lease expired and was reclaimed by another worker; fenced out
          return true;
        }
        return true;
      }

      // Read bounded and sanitized response snippet
      let responseSnippet = "";
      try {
        const text = await response.text();
        responseSnippet = Array.from(text.slice(0, 512))
          .map((ch) => (ch.charCodeAt(0) < 32 || ch.charCodeAt(0) === 127 ? " " : ch))
          .join("");
      } catch {
        responseSnippet = "<body unreadable>";
      }
      const errorMsg = `HTTP ${response.status}: ${responseSnippet}`;
      await this.markFailedFenced(candidate.id, candidate.lease_token, candidate.attempts, errorMsg);
      return true;
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      const errorMsg = `network error: ${msg}`;
      await this.markFailedFenced(candidate.id, candidate.lease_token, candidate.attempts, errorMsg);
      return true;
    } finally {
      clearTimeout(timeoutId);
      this.activeControllers.delete(controller);
    }
  }

  private async markFailedFenced(
    id: number,
    leaseToken: string,
    currentAttempts: number,
    errorMsg: string,
  ): Promise<void> {
    // Bounded exponential backoff: min 1s, max 60s
    const backoffMs = Math.min(1000 * Math.pow(2, Math.max(0, currentAttempts - 1)), 60_000);

    await this.pool.query(
      `UPDATE onboarding_webhook_forwarding
          SET status = 'failed',
              lease_token = NULL,
              leased_until = NULL,
              last_error = $3,
              next_retry_at = now() + ($4 || ' milliseconds')::interval
        WHERE id = $1 AND lease_token = $2 AND status = 'forwarding' AND leased_until > now()`,
      [id, leaseToken, errorMsg, backoffMs],
    );
  }

  private async drain(): Promise<void> {
    try {
      await this.enrollMissedInboxRows();
      while (!this.isStopping && (await this.processPendingOnce())) {
        // Drain until no more pending rows
      }
    } catch {
      // Periodic timer retries
    }
  }
}
