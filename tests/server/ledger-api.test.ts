import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "../../server/src/config.js";
import type { DatabasePool } from "../../server/src/db/pool.js";
import { createRequestHandler } from "../../server/src/http/server.js";

const config: ServerConfig = {
  port: 3000, databaseUrl: "fixture",

};

describe("retired Node ledger API", () => {
  it.each([
    ["GET", "/api/ledger", true],
    ["GET", "/api/ledger?legacy=1", true],
    ["POST", "/api/ledger", true],
    ["GET", "/api/ledger", false],
  ] as const)("refuses %s %s with storage configured=%s", (method, url, configured) => {
    const query = vi.fn();
    const connect = vi.fn();
    const pool = configured ? { query, connect } as unknown as DatabasePool : undefined;
    const response = { writeHead: vi.fn(), end: vi.fn() };
    createRequestHandler(config, pool)({ method, url, headers: {} } as IncomingMessage,
      response as unknown as ServerResponse);
    expect(response.writeHead).toHaveBeenCalledWith(410, {
      "content-type": "application/json", "cache-control": "no-store",
    });
    expect(JSON.parse(response.end.mock.calls[0]?.[0] as string))
      .toMatchObject({ error: "legacy_ledger_retired" });
    expect(query).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
  });
});
