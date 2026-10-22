import { Readable } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { ServerConfig } from "../../../server/src/config.js";
import type { DatabasePool } from "../../../server/src/db/pool.js";
import { createRequestHandler } from "../../../server/src/http/server.js";

function config(token?: string): ServerConfig {
  return {
    port: 3000, databaseUrl: "postgres://fixture", airwallexClientId: "fixture",
    airwallexApiKey: "fixture", airwallexWebhookSecret: "fixture",
    ...(token ? { policyDecisionReadToken: token } : {}),
  };
}

async function call(token: string | undefined, resolver: ReturnType<typeof vi.fn>) {
  const request = Readable.from([JSON.stringify({ requestId: "event-1" })]) as IncomingMessage;
  request.method = "POST";
  request.url = "/api/bridge-decisions";
  request.headers = token ? { authorization: `Bearer ${token}` } : {};
  const result = { status: 0, body: "", headersSent: false,
    writeHead(status: number) { this.status = status; this.headersSent = true; },
    end(body: string) { this.body = body; },
  };
  const handler = createRequestHandler(config("read-secret"), {} as DatabasePool, undefined, resolver);
  handler(request, result as unknown as ServerResponse);
  await new Promise<void>((resolve) => setTimeout(resolve, 0));
  return { ...result, json: result.body ? JSON.parse(result.body) : null };
}

describe("stored bridge decision API authorization", () => {
  it("rejects unauthenticated reads before invoking the resolver", async () => {
    const resolver = vi.fn();
    const result = await call(undefined, resolver);
    expect(result.status).toBe(401);
    expect(resolver).not.toHaveBeenCalled();
  });

  it("allows a configured internal reader and returns the resolver's redacted result", async () => {
    const resolver = vi.fn(async () => ({
      evaluationId: "eval-1", requestId: "event-1", customerId: "customer-1", outcome: "blocked" as const,
      amountCents: 0, creditLineCents: 0, reasonCodes: ["PRECHECK_BLOCKED"], freshness: {}, versions: {},
    }));
    const result = await call("read-secret", resolver);
    expect(result.status).toBe(200);
    expect(resolver).toHaveBeenCalledWith("event-1");
    expect(result.json).toMatchObject({ requestId: "event-1", outcome: "blocked" });
  });

  it("keeps decision reads disabled when no token is configured", () => {
    const resolver = vi.fn();
    const handler = createRequestHandler(config(), {} as DatabasePool, undefined, resolver);
    const request = { method: "POST", url: "/api/bridge-decisions", headers: {} } as IncomingMessage;
    const response = { status: 0, body: "", headersSent: false,
      writeHead(status: number) { this.status = status; this.headersSent = true; },
      end(body: string) { this.body = body; },
    };
    handler(request, response as unknown as ServerResponse);
    expect(response.status).toBe(503);
    expect(resolver).not.toHaveBeenCalled();
  });
});
