import { createHmac } from "node:crypto";
import type { IncomingMessage, ServerResponse } from "node:http";
import { Readable } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "../../server/src/config.js";
import type { DatabasePool } from "../../server/src/db/pool.js";
import { createRequestHandler } from "../../server/src/http/server.js";

const secret = "synthetic-webhook-secret-0000000000000000";
const config: ServerConfig = {
  port: 3000, databaseUrl: "fixture",

};

function request(url: string, body: string, headers: Record<string, string> = {}): IncomingMessage {
  return Object.assign(Readable.from([body]), { method: "POST", url, headers }) as IncomingMessage;
}

function invoke(handler: ReturnType<typeof createRequestHandler>, req: IncomingMessage) {
  return new Promise<{ status: number; headers: Record<string, string>; body: Record<string, unknown> }>((resolve) => {
    let status = 0;
    let headers: Record<string, string> = {};
    const response = {
      writeHead(code: number, values: Record<string, string>) { status = code; headers = values; },
      end(body: string) { resolve({ status, headers, body: JSON.parse(body) }); },
    } as unknown as ServerResponse;
    handler(req, response);
  });
}

function signedEvent(name: string) {
  const body = JSON.stringify({ id: "synthetic-event", name, data: {} });
  const timestamp = String(Date.now());
  const signature = createHmac("sha256", secret).update(timestamp).update(body).digest("hex");
  return request("/webhooks/airwallex", body, { "x-timestamp": timestamp, "x-signature": signature });
}

function database() {
  const query = vi.fn(async (_sql: string, _values?: readonly unknown[]) => ({ rows: [{ id: "synthetic-event" }], rowCount: 1 }));
  const connect = vi.fn();
  const pool = { query, connect, end: vi.fn() } as unknown as DatabasePool;
  return { pool, query, connect };
}

describe("retired Node financial execution", () => {
  it("returns explicit retirement for old funding, approval and demo commands without database work", async () => {
    const { pool, query, connect } = database();
    const handler = createRequestHandler(config, pool);
    for (const url of ["/api/remote-authorization", "/api/bridge-decisions", "/api/kit5/next-customer",
      "/api/kit5/approve", "/api/kit5/repayment-deposit", "/api/kit5/fixture-checkpoint/recover?legacy=1"]) {
      const result = await invoke(handler, request(url, "{}"));
      expect(result.status).toBe(410);
      expect(result.headers["cache-control"]).toBe("no-store");
      expect(result.body.error).toBe("legacy_funding_retired");
    }
    expect(query).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
  });

  it("retires every provider callback without receipt, forwarding or signature consumption", async () => {
    const { pool, query, connect } = database();
    const handler = createRequestHandler(config, pool);
    for (const name of ["account.active", "rfi.created", "connected_account_transfer.settled", "deposit.settled"]) {
      const req = signedEvent(name);
      const result = await invoke(handler, req);
      expect(result.status).toBe(410);
      expect(result.headers["cache-control"]).toBe("no-store");
      expect(result.body.error).toBe("legacy_webhook_retired");
      expect(result.headers.location).toBeUndefined();
      expect(req.readableEnded).toBe(false);
    }
    const invalid = signedEvent("account.active");
    invalid.headers["x-signature"] = "invalid";
    expect((await invoke(handler, invalid)).status).toBe(410);
    expect((await invoke(handler, request("/webhooks/airwallex/?old=1", "{}"))).status).toBe(410);
    expect(query).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
  });
});
