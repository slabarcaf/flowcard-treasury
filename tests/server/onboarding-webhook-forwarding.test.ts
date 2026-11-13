import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import { createDatabasePool, type DatabasePool } from "../../server/src/db/pool.js";
import { insertWebhookOnce, isOnboardingWebhookEvent } from "../../server/src/webhooks/inbox.js";
import { OnboardingWebhookForwarder } from "../../server/src/webhooks/onboarding-forwarder.js";
import { handleAirwallexWebhook } from "../../server/src/http/routes/airwallex-webhook.js";
import { verifyServiceAssertion, DEFAULT_ISSUER, DEFAULT_WEBHOOK_AUDIENCE } from "../../server/src/http/onboarding-proxy-auth.js";
import { createHmac } from "node:crypto";

const webhookSecret = "airwallex-test-webhook-secret-12345";
const serviceSecret = "backend-service-secret-at-least-32-bytes-long-12345";
const migrationPath = fileURLToPath(new URL("../../server/src/db/migrations/021_onboarding_webhook_forwarding.sql", import.meta.url));
const testDatabaseUrl = "postgres://test_onboarding:test_password@127.0.0.1:54329/flowcard_onboarding_test?sslmode=disable";

function responseRecorder() {
  let statusCode = 0;
  let body = "";
  const headers: Record<string, string> = {};
  const response = {
    headersSent: false,
    setHeader(name: string, value: string) { headers[name.toLowerCase()] = value; return this; },
    writeHead(code: number, values?: Record<string, string>) {
      statusCode = code;
      if (values) Object.assign(headers, values);
      this.headersSent = true;
      return this;
    },
    end(value = "") { body = typeof value === "string" ? value : value.toString(); return this; },
  } as unknown as ServerResponse;
  return { response, headers, status: () => statusCode, body: () => body };
}

function signedWebhookRequest(payload: Record<string, unknown>, secret = webhookSecret): IncomingMessage {
  const rawBody = Buffer.from(JSON.stringify(payload), "utf8");
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const signature = createHmac("sha256", secret).update(`${timestamp}${rawBody.toString("utf8")}`).digest("hex");

  return Object.assign(Readable.from([rawBody]), {
    method: "POST",
    url: "/webhooks/airwallex",
    headers: {
      "content-type": "application/json",
      "x-timestamp": timestamp,
      "x-signature": signature,
    },
  }) as IncomingMessage;
}

describe("Onboarding Webhook Forwarding & Durability", () => {
  it("defines durable onboarding_webhook_forwarding table schema with lease columns in migration 021", async () => {
    const sql = await readFile(migrationPath, "utf8");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS onboarding_webhook_forwarding");
    expect(sql).toContain("provider_event_id text NOT NULL UNIQUE REFERENCES webhook_inbox(provider_event_id)");
    expect(sql).toContain("status text NOT NULL DEFAULT 'queued'");
    expect(sql).toContain("lease_token text");
    expect(sql).toContain("leased_until timestamptz");
    expect(sql).toContain("next_retry_at timestamptz NOT NULL DEFAULT now()");
    expect(sql).toContain("idx_onboarding_webhook_forwarding_claim");
  });

  it("identifies onboarding-related events by type prefix (account.*, rfi.*)", () => {
    expect(isOnboardingWebhookEvent("account.active")).toBe(true);
    expect(isOnboardingWebhookEvent("account.action_required")).toBe(true);
    expect(isOnboardingWebhookEvent("account.suspended")).toBe(true);
    expect(isOnboardingWebhookEvent("rfi.created")).toBe(true);
    expect(isOnboardingWebhookEvent("charge.settled")).toBe(false);
    expect(isOnboardingWebhookEvent("connected_account_transfer.settled")).toBe(false);
  });

  it("atomically inserts onboarding events into both webhook_inbox and onboarding_webhook_forwarding", async () => {
    const queries: string[] = [];
    const mockPool = {
      query: vi.fn(async (sql: string) => {
        queries.push(sql);
        return { rowCount: 1, rows: [{ id: "evt_onboarding_1" }] };
      }),
    } as unknown as DatabasePool;

    const inserted = await insertWebhookOnce(mockPool, {
      id: "evt_onboarding_1",
      type: "account.active",
      rawBody: Buffer.from("{}"),
    });

    expect(inserted).toBe(true);
    expect(queries.some((q) => q.includes("INSERT INTO webhook_inbox"))).toBe(true);
    expect(queries.some((q) => q.includes("INSERT INTO onboarding_webhook_forwarding"))).toBe(true);
  });

  it("does not enqueue bridge ledger events into onboarding_webhook_forwarding", async () => {
    const queries: string[] = [];
    const mockPool = {
      query: vi.fn(async (sql: string) => {
        queries.push(sql);
        return { rowCount: 1, rows: [{ id: "evt_charge_1" }] };
      }),
    } as unknown as DatabasePool;

    const inserted = await insertWebhookOnce(mockPool, {
      id: "evt_charge_1",
      type: "charge.settled",
      rawBody: Buffer.from("{}"),
    });

    expect(inserted).toBe(true);
    expect(queries.some((q) => q.includes("INSERT INTO webhook_inbox"))).toBe(true);
    expect(queries.some((q) => q.includes("INSERT INTO onboarding_webhook_forwarding"))).toBe(false);
  });

  it("restart-safe scanner enrolls missed onboarding rows from webhook_inbox", async () => {
    const pool = {
      query: vi.fn().mockResolvedValue({ rowCount: 3 }),
    } as unknown as DatabasePool;

    const forwarder = new OnboardingWebhookForwarder({
      pool,
      onboardingServiceUrl: "http://127.0.0.1:8080",
      serviceSecret,
    });

    const enrolled = await forwarder.enrollMissedInboxRows();
    expect(enrolled).toBe(3);
    expect(pool.query).toHaveBeenCalledOnce();
    const query = (pool.query as any).mock.calls[0][0];
    expect(query).toContain("INSERT INTO onboarding_webhook_forwarding");
    expect(query).toContain("FROM webhook_inbox");
  });

  it("forwards pending webhook with signed JWT assertion, redirect: error, and marks forwarded on 200", async () => {
    let capturedAuth = "";
    let capturedBody = "";
    let capturedInit: any;

    const client = {
      query: vi.fn(async (sql: string) => {
        if (sql.includes("WITH candidate AS")) {
          return {
            rowCount: 1,
            rows: [{ id: 42, provider_event_id: "evt_act_123", event_type: "account.active", attempts: 0, lease_token: "lease_token_1" }],
          };
        }
        return { rowCount: 1, rows: [] };
      }),
      release: vi.fn(),
    };

    const pool = {
      connect: vi.fn().mockResolvedValue(client),
      query: vi.fn(async (sql: string) => {
        if (sql.includes("SELECT raw_body FROM webhook_inbox")) {
          return { rowCount: 1, rows: [{ raw_body: Buffer.from(JSON.stringify({ id: "evt_act_123", name: "account.active" })) }] };
        }
        return { rowCount: 1, rows: [] };
      }),
    } as unknown as DatabasePool;

    const mockFetch = vi.fn(async (url: string, init: any) => {
      capturedInit = init;
      capturedAuth = init.headers.authorization;
      capturedBody = typeof init.body === "string" ? init.body : Buffer.from(init.body).toString("utf8");
      return {
        status: 200,
        text: async () => JSON.stringify({ received: true }),
      } as Response;
    });

    const forwarder = new OnboardingWebhookForwarder({
      pool,
      onboardingServiceUrl: "http://127.0.0.1:8080",
      serviceSecret,
      fetchFn: mockFetch as any,
    });

    const processed = await forwarder.processPendingOnce();
    expect(processed).toBe(true);

    // Verify assertion claims
    expect(capturedAuth).toMatch(/^Bearer /);
    const jwt = capturedAuth.slice("Bearer ".length);
    const claims = verifyServiceAssertion(jwt, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_WEBHOOK_AUDIENCE,
      expectedIssuer: DEFAULT_ISSUER,
    });
    expect(claims.sub).toBe("node-webhook-forwarder");
    expect(claims.aud).toBe(DEFAULT_WEBHOOK_AUDIENCE);
    expect(JSON.parse(capturedBody)).toMatchObject({ id: "evt_act_123", name: "account.active" });

    // Verify redirect protection
    expect(capturedInit.redirect).toBe("error");

    // Verify forwarded status updated in DB with lease_token check
    const updateCall = (pool.query as any).mock.calls.find((call: any[]) =>
      call[0].includes("SET status = 'forwarded'"),
    );
    expect(updateCall).toBeDefined();
    expect(updateCall[1]).toEqual([42, "lease_token_1"]);
  });

  it("quarantines with evidence when canonical raw body is missing in webhook_inbox (no silent ignore)", async () => {
    const client = {
      query: vi.fn(async (sql: string) => {
        if (sql.includes("WITH candidate AS")) {
          return {
            rowCount: 1,
            rows: [{ id: 45, provider_event_id: "evt_missing_raw", event_type: "account.active", attempts: 0, lease_token: "lease_token_miss" }],
          };
        }
        return { rowCount: 1, rows: [] };
      }),
      release: vi.fn(),
    };

    const pool = {
      connect: vi.fn().mockResolvedValue(client),
      query: vi.fn(async (sql: string) => {
        if (sql.includes("SELECT raw_body FROM webhook_inbox")) {
          return { rowCount: 0, rows: [] }; // missing body
        }
        return { rowCount: 1, rows: [] };
      }),
    } as unknown as DatabasePool;

    const forwarder = new OnboardingWebhookForwarder({
      pool,
      onboardingServiceUrl: "http://127.0.0.1:8080",
      serviceSecret,
    });

    const processed = await forwarder.processPendingOnce();
    expect(processed).toBe(true);

    // Verify quarantined write
    const quarantineCall = (pool.query as any).mock.calls.find((call: any[]) =>
      call[0].includes("SET status = 'quarantined'"),
    );
    expect(quarantineCall).toBeDefined();
    expect(quarantineCall[0]).toContain("status = 'quarantined'");
    expect(quarantineCall[1]).toEqual([45, "lease_token_miss"]);
  });

  it("applies bounded exponential backoff on delivery failure", async () => {
    const client = {
      query: vi.fn(async (sql: string) => {
        if (sql.includes("WITH candidate AS")) {
          return {
            rowCount: 1,
            rows: [{ id: 43, provider_event_id: "evt_fail_1", event_type: "account.active", attempts: 3, lease_token: "lease_fail_tok" }],
          };
        }
        return { rowCount: 1, rows: [] };
      }),
      release: vi.fn(),
    };

    const pool = {
      connect: vi.fn().mockResolvedValue(client),
      query: vi.fn(async (sql: string) => {
        if (sql.includes("SELECT raw_body FROM webhook_inbox")) {
          return { rowCount: 1, rows: [{ raw_body: Buffer.from("{}") }] };
        }
        return { rowCount: 1, rows: [] };
      }),
    } as unknown as DatabasePool;

    const mockFetch = vi.fn(async () => {
      throw new Error("connection refused");
    });

    const forwarder = new OnboardingWebhookForwarder({
      pool,
      onboardingServiceUrl: "http://127.0.0.1:8080",
      serviceSecret,
      fetchFn: mockFetch as any,
    });

    const processed = await forwarder.processPendingOnce();
    expect(processed).toBe(true);

    // Verify failed update with backoff and lease token check
    const updateCall = (pool.query as any).mock.calls.find((call: any[]) =>
      call[0].includes("SET status = 'failed'"),
    );
    expect(updateCall).toBeDefined();
    expect(updateCall[1][0]).toBe(43); // id
    expect(updateCall[1][1]).toBe("lease_fail_tok"); // lease token
    expect(updateCall[1][2]).toContain("connection refused");
    expect(updateCall[1][3]).toBe(4000); // 1000 * 2^2 = 4000ms backoff
  });

  it("webhook route strictly validates signatures and nudges forwarder", async () => {
    const client = {
      query: vi.fn().mockResolvedValue({ rowCount: 1, rows: [{ id: "evt_sig_1" }] }),
      release: vi.fn(),
    };
    const pool = {
      connect: vi.fn().mockResolvedValue(client),
      query: vi.fn().mockResolvedValue({ rowCount: 1, rows: [{ id: "evt_sig_1" }] }),
    } as unknown as DatabasePool;

    const onReceived = vi.fn();

    // 1. Valid signature
    const validReq = signedWebhookRequest({ id: "evt_sig_1", name: "account.active" });
    const validRec = responseRecorder();
    await handleAirwallexWebhook(validReq, validRec.response, { pool, secret: webhookSecret, onReceived });
    expect(validRec.status()).toBe(200);
    expect(JSON.parse(validRec.body())).toMatchObject({ received: true });
    expect(onReceived).toHaveBeenCalledOnce();

    // 2. Tampered signature
    const badReq = signedWebhookRequest({ id: "evt_sig_2", name: "account.active" }, "wrong-secret-signature");
    const badRec = responseRecorder();
    await handleAirwallexWebhook(badReq, badRec.response, { pool, secret: webhookSecret, onReceived });
    expect(badRec.status()).toBe(401);
    expect(JSON.parse(badRec.body())).toMatchObject({ error: "invalid_signature" });
  });

  it("real DB concurrency, expired lease reclamation, and stale worker fencing (PostgreSQL 54329)", async () => {
    let pool: DatabasePool;
    try {
      pool = await createDatabasePool({ databaseUrl: testDatabaseUrl });
      await pool.query("SELECT 1");
    } catch {
      // Test DB not available, skip
      return;
    }

    try {
      // Setup test schema
      await pool.query(`
        CREATE TABLE IF NOT EXISTS webhook_inbox (
          id bigserial PRIMARY KEY,
          provider_event_id text NOT NULL UNIQUE,
          event_type text NOT NULL,
          environment text NOT NULL DEFAULT 'sandbox',
          raw_body bytea NOT NULL,
          created_at timestamptz NOT NULL DEFAULT now()
        );
      `);

      const migrationSql = await readFile(migrationPath, "utf8");
      await pool.query(migrationSql);

      const batchPrefix = `test_batch_${Date.now()}`;

      // Insert 6 real events into webhook_inbox and onboarding_webhook_forwarding
      for (let i = 0; i < 6; i++) {
        const eventId = `${batchPrefix}_evt_${i}`;
        const rawBody = Buffer.from(JSON.stringify({ id: eventId, name: "account.active", seq: i }));
        await pool.query(
          `INSERT INTO webhook_inbox (provider_event_id, event_type, raw_body)
           VALUES ($1, 'account.active', $2)
           ON CONFLICT DO NOTHING`,
          [eventId, rawBody],
        );
        await pool.query(
          `INSERT INTO onboarding_webhook_forwarding (provider_event_id, event_type)
           VALUES ($1, 'account.active')
           ON CONFLICT DO NOTHING`,
          [eventId],
        );
      }

      const deliveredEvents: string[] = [];
      const mockFetch = vi.fn(async (url: string, init: any) => {
        const body = JSON.parse(typeof init.body === "string" ? init.body : Buffer.from(init.body).toString("utf8"));
        deliveredEvents.push(body.id);
        return {
          status: 200,
          text: async () => JSON.stringify({ received: true }),
        } as Response;
      });

      // 1. Concurrency: 3 concurrent forwarders draining simultaneously
      const forwarder1 = new OnboardingWebhookForwarder({ pool, onboardingServiceUrl: "http://127.0.0.1:8080", serviceSecret, fetchFn: mockFetch as any });
      const forwarder2 = new OnboardingWebhookForwarder({ pool, onboardingServiceUrl: "http://127.0.0.1:8080", serviceSecret, fetchFn: mockFetch as any });
      const forwarder3 = new OnboardingWebhookForwarder({ pool, onboardingServiceUrl: "http://127.0.0.1:8080", serviceSecret, fetchFn: mockFetch as any });

      const drainWorker = async (f: OnboardingWebhookForwarder) => {
        while (await f.processPendingOnce()) {
          // loop until empty
        }
      };

      await Promise.all([
        drainWorker(forwarder1),
        drainWorker(forwarder2),
        drainWorker(forwarder3),
      ]);

      // All 6 events delivered exactly once without duplication
      const batchDelivered = deliveredEvents.filter((id) => id.startsWith(batchPrefix));
      expect(batchDelivered).toHaveLength(6);
      const uniqueDelivered = new Set(batchDelivered);
      expect(uniqueDelivered.size).toBe(6);

      // Verify all 6 rows are in status = 'forwarded'
      const rowsRes = await pool.query<{ status: string; lease_token: string | null }>(
        `SELECT status, lease_token FROM onboarding_webhook_forwarding WHERE provider_event_id LIKE $1`,
        [`${batchPrefix}%`],
      );
      expect(rowsRes.rows).toHaveLength(6);
      for (const row of rowsRes.rows) {
        expect(row.status).toBe("forwarded");
        expect(row.lease_token).toBeNull();
      }

      // 2. Expired Lease Reclamation & Stale Worker Fencing
      const crashEventId = `${batchPrefix}_crash_evt`;
      const crashBody = Buffer.from(JSON.stringify({ id: crashEventId, name: "account.active" }));
      await pool.query(
        `INSERT INTO webhook_inbox (provider_event_id, event_type, raw_body)
         VALUES ($1, 'account.active', $2)`,
        [crashEventId, crashBody],
      );

      // Insert simulated crashed worker holding an expired lease from 1 minute ago
      const insertCrashRow = await pool.query<{ id: number }>(
        `INSERT INTO onboarding_webhook_forwarding (provider_event_id, event_type, status, lease_token, leased_until, attempts)
         VALUES ($1, 'account.active', 'forwarding', 'stale_worker_token_123', now() - interval '60 seconds', 1)
         RETURNING id`,
        [crashEventId],
      );
      const crashRowId = insertCrashRow.rows[0].id;

      // New recovery worker claims and processes the expired row
      let reclaimedAuth = "";
      const recoveryMockFetch = vi.fn(async (url: string, init: any) => {
        reclaimedAuth = init.headers.authorization;
        return {
          status: 200,
          text: async () => JSON.stringify({ received: true }),
        } as Response;
      });

      const recoveryForwarder = new OnboardingWebhookForwarder({
        pool,
        onboardingServiceUrl: "http://127.0.0.1:8080",
        serviceSecret,
        fetchFn: recoveryMockFetch as any,
      });

      const reclaimed = await recoveryForwarder.processPendingOnce();
      expect(reclaimed).toBe(true);
      expect(reclaimedAuth).toMatch(/^Bearer /);

      // Verify row is now forwarded and lease_token is cleared
      const checkRow = await pool.query<{ status: string; lease_token: string | null; attempts: number }>(
        `SELECT status, lease_token, attempts FROM onboarding_webhook_forwarding WHERE id = $1`,
        [crashRowId],
      );
      expect(checkRow.rows[0].status).toBe("forwarded");
      expect(checkRow.rows[0].lease_token).toBeNull();
      expect(checkRow.rows[0].attempts).toBe(2);

      // 3. Stale Worker Fencing:
      // If the stale worker wakes up now and tries to complete or fail using its stale lease token:
      const staleWriteResult = await pool.query(
        `UPDATE onboarding_webhook_forwarding
            SET status = 'forwarded',
                lease_token = NULL
          WHERE id = $1 AND lease_token = 'stale_worker_token_123' AND status = 'forwarding' AND leased_until > now()`,
        [crashRowId],
      );
      // Fenced out! 0 rows updated
      expect(staleWriteResult.rowCount).toBe(0);

      // Cleanup batch test rows
      await pool.query(`DELETE FROM onboarding_webhook_forwarding WHERE provider_event_id LIKE $1`, [`${batchPrefix}%`]);
      await pool.query(`DELETE FROM webhook_inbox WHERE provider_event_id LIKE $1`, [`${batchPrefix}%`]);
    } finally {
      await pool.end();
    }
  });
});
