import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import { createRequestHandler } from "../../server/src/http/server.js";
import type { ServerConfig } from "../../server/src/config.js";
import type { DatabasePool } from "../../server/src/db/pool.js";

const config: ServerConfig = {
  port: 3000, databaseUrl: "fixture",

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

});
