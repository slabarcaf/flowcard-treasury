import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import { createRequestHandler } from "../../server/src/http/server.js";
import type { ServerConfig } from "../../server/src/config.js";
import type { DatabasePool } from "../../server/src/db/pool.js";
import { SalesforceSnapshotApiClient } from "../../src/services/http/SalesforceSnapshotApiClient.js";

const config: ServerConfig = {
  port: 3000, databaseUrl: "fixture", airwallexBaseUrl: "http://127.0.0.1",
  airwallexClientId: "synthetic-client", airwallexApiKey: "synthetic-key",
  airwallexWebhookSecret: "synthetic-webhook-secret",
};

describe("retired Node customer snapshots", () => {
  it.each(["GET", "POST"])("refuses %s before any database access", (method) => {
    const query = vi.fn();
    const connect = vi.fn();
    const handler = createRequestHandler(config, { query, connect } as unknown as DatabasePool);
    const response = { writeHead: vi.fn(), end: vi.fn() };
    handler({ method, url: "/api/customers/acct_bloom001/snapshot?legacy=1", headers: {} } as IncomingMessage,
      response as unknown as ServerResponse);
    expect(response.writeHead).toHaveBeenCalledWith(410, {
      "content-type": "application/json", "cache-control": "no-store",
    });
    expect(JSON.parse(response.end.mock.calls[0]?.[0] as string)).toMatchObject({ error: "legacy_customer_snapshot_retired" });
    expect(query).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
  });

  it("surfaces retirement to the legacy browser client without a fixture fallback", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => new Response("{}", { status: 410 }));
    await expect(new SalesforceSnapshotApiClient("", fetcher).getSnapshot("acct_bloom001"))
      .rejects.toThrow("legacy CRM snapshot API is retired");
    expect(fetcher).toHaveBeenCalledOnce();
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/customers/acct_bloom001/snapshot");
  });
});
