import { describe, expect, it, vi } from "vitest";
import { loadSalesforceMcpConfig, requireSalesforceMcpProductionReadiness } from "../../server/src/salesforce/mcp-config.js";
import {
  readSalesforceAccount,
  readSalesforceQuery,
  type SalesforceMcpClient,
  type SalesforceMcpPage,
  type SalesforceMcpReadRequest,
} from "../../server/src/salesforce/mcp-source.js";
import { SALESFORCE_QUERIES, isSalesforceQueryName } from "../../server/src/salesforce/queries.js";

const config = { serverName: "salesforce", queryToolName: "query", maxAttempts: 3, retryDelayMs: 10 };
const rosterEntry = { salesforceAccountId: "001-roster-account" };

function page(records: Record<string, unknown>[] = [], overrides: Partial<SalesforceMcpPage> = {}): SalesforceMcpPage {
  return { records, nextCursor: null, complete: true, ...overrides };
}

function client(read: SalesforceMcpClient["read"]): SalesforceMcpClient {
  return { read };
}

describe("Salesforce MCP configuration", () => {
  it("requires explicit MCP server and tool configuration without a REST fallback", () => {
    expect(() => loadSalesforceMcpConfig({})).toThrow("SALESFORCE_MCP_SERVER_NAME");
    expect(() => loadSalesforceMcpConfig({ SALESFORCE_MCP_SERVER_NAME: "sf" })).toThrow("SALESFORCE_MCP_QUERY_TOOL_NAME");
    expect(loadSalesforceMcpConfig({ SALESFORCE_MCP_SERVER_NAME: " sf ", SALESFORCE_MCP_QUERY_TOOL_NAME: "query" })).toEqual({
      serverName: "sf", queryToolName: "query", maxAttempts: 3, retryDelayMs: 100,
    });
    expect(() => loadSalesforceMcpConfig({
      SALESFORCE_MCP_SERVER_NAME: "sf", SALESFORCE_MCP_QUERY_TOOL_NAME: "query", SALESFORCE_MCP_MAX_ATTEMPTS: "0",
    })).toThrow("positive integer");
    expect(() => loadSalesforceMcpConfig({
      SALESFORCE_MCP_SERVER_NAME: "sf", SALESFORCE_MCP_QUERY_TOOL_NAME: "query", SALESFORCE_MCP_MAX_ATTEMPTS: "11",
    })).toThrow("must not exceed");
  });

  it("keeps production blocked until the provisional map and MCP capabilities are verified", () => {
    expect(() => requireSalesforceMcpProductionReadiness({
      SALESFORCE_MCP_SERVER_NAME: "sf",
      SALESFORCE_MCP_QUERY_TOOL_NAME: "query",
      SALESFORCE_MAX_CASE_TEXT_LENGTH: "1200",
      SALESFORCE_MAX_ACTIVITY_TEXT_LENGTH: "500",
      SALESFORCE_MCP_CAPABILITIES_VERIFIED: "true",
    })).toThrow("provisional");
  });
});

describe("Salesforce MCP source", () => {
  it("issues only allowlisted reads keyed to the roster Salesforce Account ID", async () => {
    const requests: SalesforceMcpReadRequest[] = [];
    const read = vi.fn(async (request: SalesforceMcpReadRequest) => {
      requests.push(request);
      return page([{ Id: "sf-record", unexpected_secret__c: "must not escape" }]);
    });

    const result = await readSalesforceAccount(client(read), config, rosterEntry);

    expect(requests.map(({ object }) => object)).toEqual(["Account", "Contract", "Case", "Task"]);
    expect(requests.every((request) => request.accountId === "001-roster-account")).toBe(true);
    expect(requests.every((request) => request.cursor === null)).toBe(true);
    expect(requests[0]).toMatchObject({ accountIdField: "Id", object: "Account" });
    expect(requests[1]).toMatchObject({ accountIdField: "AccountId", object: "Contract" });
    expect(requests[3]).toMatchObject({ accountIdField: "WhatId", filter: "call", object: "Task" });
    expect(result.account).toEqual({ records: [{ Id: "sf-record" }], complete: true, pages: 1 });
    expect(result.case.complete).toBe(true);
    expect(requests.every((request) => request.fields.every((field) => SALESFORCE_QUERIES["account"].fields.includes(field) || Object.values(SALESFORCE_QUERIES).some((definition) => definition.fields.includes(field))))).toBe(true);
  });

  it("fetches every page, exposes complete metadata, and accepts a successful empty result", async () => {
    const read = vi.fn()
      .mockResolvedValueOnce(page([{ Id: "one" }], { nextCursor: "next", complete: false }))
      .mockResolvedValueOnce(page([{ Id: "two" }]))
      .mockResolvedValueOnce(page());
    const source = client(read);

    const roster = { salesforceAccountId: "001-roster" };
    const paged = await readSalesforceQuery(source, config, "account", roster, async () => {});
    const empty = await readSalesforceQuery(source, config, "case", roster, async () => {});

    expect(paged).toEqual({ records: [{ Id: "one" }, { Id: "two" }], complete: true, pages: 2 });
    expect(read.mock.calls[1]?.[0].cursor).toBe("next");
    expect(empty).toEqual({ records: [], complete: true, pages: 1 });
  });

  it("fails closed on incomplete pages or repeated pagination cursors", async () => {
    await expect(readSalesforceQuery(client(async () => page([], { complete: false })), config, "case", rosterEntry, async () => {}))
      .rejects.toMatchObject({ code: "incomplete_page" });

    const repeated = client(vi.fn()
      .mockResolvedValueOnce(page([], { complete: false, nextCursor: "same" }))
      .mockResolvedValueOnce(page([], { complete: false, nextCursor: "same" })));
    await expect(readSalesforceQuery(repeated, config, "case", rosterEntry, async () => {}))
      .rejects.toThrow("repeated a pagination cursor");
  });

  it("does not retry permission errors and does not publish incomplete result metadata", async () => {
    const read = vi.fn(async () => { throw Object.assign(new Error("forbidden"), { code: "PERMISSION_DENIED" }); });
    await expect(readSalesforceQuery(client(read), config, "account", rosterEntry, async () => {}))
      .rejects.toMatchObject({ code: "permission_denied" });
    expect(read).toHaveBeenCalledTimes(1);
  });

  it("retries timeouts with bounded exponential delays and exhausts at the configured limit", async () => {
    const read = vi.fn()
      .mockRejectedValueOnce(Object.assign(new Error("timed out"), { code: "TIMEOUT" }))
      .mockRejectedValueOnce(Object.assign(new Error("unavailable"), { code: "UNAVAILABLE" }))
      .mockResolvedValueOnce(page([{ Id: "ok" }]));
    const sleep = vi.fn(async () => {});
    await expect(readSalesforceQuery(client(read), config, "account", rosterEntry, sleep))
      .resolves.toMatchObject({ complete: true, records: [{ Id: "ok" }] });
    expect(read).toHaveBeenCalledTimes(3);
    expect(sleep.mock.calls.map(([ms]) => ms)).toEqual([10, 20]);

    const exhausted = vi.fn(async () => { throw Object.assign(new Error("timeout"), { code: "DEADLINE_EXCEEDED" }); });
    await expect(readSalesforceQuery(client(exhausted), config, "account", rosterEntry, async () => {}))
      .rejects.toMatchObject({ code: "transient" });
    expect(exhausted).toHaveBeenCalledTimes(3);
  });

  it("rejects invalid pages and exposes no fields outside the allowlist", async () => {
    await expect(readSalesforceQuery(client(async () => ({ records: "bad", nextCursor: null, complete: true } as never)), config, "account", rosterEntry, async () => {}))
      .rejects.toMatchObject({ code: "invalid_response" });
    const result = await readSalesforceQuery(client(async () => page([{ Id: "001", secret__c: "hidden" }])), config, "account", rosterEntry, async () => {});
    expect(result.records).toEqual([{ Id: "001" }]);
  });

  it("does not accept caller-selected query names or blank roster IDs", async () => {
    expect(isSalesforceQueryName("account")).toBe(true);
    expect(isSalesforceQueryName("query_all")).toBe(false);
    await expect(readSalesforceQuery(client(async () => page()), config, "account", { salesforceAccountId: " " }, async () => {}))
      .rejects.toThrow("Salesforce Account ID is required");
  });
});
