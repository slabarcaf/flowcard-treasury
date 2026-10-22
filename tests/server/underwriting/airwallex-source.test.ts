import { describe, expect, it, vi } from "vitest";
import transactions from "../../../server/src/underwriting/fixtures/transactions.json" with { type: "json" };
import balances from "../../../server/src/underwriting/fixtures/balances.json" with { type: "json" };
import { loadAirwallexProfileSources, withBoundedRetries } from "../../../server/src/underwriting/airwallex-source.js";
import { HttpAirwallexUnderwritingGateway, type AirwallexUnderwritingGateway } from "../../../server/src/airwallex/client.js";

function fakeGateway(overrides: Partial<AirwallexUnderwritingGateway> = {}): AirwallexUnderwritingGateway {
  return {
    createPlatformReport: vi.fn(async () => ({ reportId: "report-1" })),
    getPlatformReport: vi.fn(async () => ({ status: "completed", downloadUrl: "https://temporary.invalid/report" })),
    downloadPlatformReport: vi.fn(async () => transactions),
    getConnectedAccount: vi.fn(async () => ({ status: "ACTIVE" })),
    getOpenRfis: vi.fn(async () => ({ items: [], hasMore: false })),
    getBalanceHistory: vi.fn(async (_accountId, _fromDate, _toDate, page) => page === 0 ? { items: balances.slice(0, 2), hasMore: true } : { items: balances.slice(2), hasMore: false }),
    ...overrides,
  };
}

describe("Airwallex underwriting source", () => {
  it("uses whole-book reports, filters to roster IDs, pages 7-day history and downloads completed reports immediately", async () => {
    const gateway = fakeGateway();
    const start = new Date("2026-07-24T00:00:00Z");
    const end = new Date("2026-10-22T00:00:00Z");
    const data = await loadAirwallexProfileSources(gateway, [{ customerId: "c1", accountId: "acct-roster-1" }], start, end);
    expect(data.transactions).toHaveLength(3);
    expect(data.transactions.every((row) => row.accountId === "acct-roster-1")).toBe(true);
    expect(gateway.createPlatformReport).toHaveBeenCalledWith({ type: "TRANSACTION_RECON_REPORT", fromDate: "2026-07-24", toDate: "2026-10-22" });
    expect(gateway.downloadPlatformReport).toHaveBeenCalledWith("https://temporary.invalid/report");
    expect(gateway.getBalanceHistory).toHaveBeenCalled();
    for (const call of vi.mocked(gateway.getBalanceHistory).mock.calls) {
      expect(Date.parse(call[2]) - Date.parse(call[1])).toBeLessThanOrEqual(6 * 86_400_000);
    }
    expect(data.queryPages.balances).toBeGreaterThan(2);
    expect(data.standings.get("c1")).toEqual({ status: "active", openRfi: false });
  });

  it("retries bounded failures and stops after the configured number of attempts", async () => {
    let attempts = 0;
    await expect(withBoundedRetries(async () => { attempts++; if (attempts < 3) throw new Error("transient"); return "ok"; })).resolves.toBe("ok");
    expect(attempts).toBe(3);
    attempts = 0;
    await expect(withBoundedRetries(async () => { attempts++; throw new Error("permanent"); }, 2)).rejects.toThrow("permanent");
    expect(attempts).toBe(2);
  });

  it("fails closed on malformed matching rows and failed or incomplete reports without exposing URLs", async () => {
    const badRow = fakeGateway({ downloadPlatformReport: vi.fn(async () => [{ account_id: "acct-roster-1" }]) });
    await expect(loadAirwallexProfileSources(badRow, [{ customerId: "c1", accountId: "acct-roster-1" }], new Date("2026-07-24Z"), new Date("2026-10-22Z"))).rejects.toThrow(/incomplete row/);
    const failed = fakeGateway({ getPlatformReport: vi.fn(async () => ({ status: "failed" })) });
    await expect(loadAirwallexProfileSources(failed, [{ customerId: "c1", accountId: "acct-roster-1" }], new Date("2026-07-24Z"), new Date("2026-10-22Z"))).rejects.toThrow("report failed");
    const slow = fakeGateway({ getPlatformReport: vi.fn(async () => ({ status: "pending" })) });
    await expect(loadAirwallexProfileSources(slow, [{ customerId: "c1", accountId: "acct-roster-1" }], new Date("2026-07-24Z"), new Date("2026-10-22Z"))).rejects.toThrow(/bounded poll/);
  });

  it("does not retry malformed data and rejects pagination that cannot progress", async () => {
    const gateway = fakeGateway({ getBalanceHistory: vi.fn(async () => ({ items: [null], hasMore: false })) });
    await expect(loadAirwallexProfileSources(gateway, [{ customerId: "c1", accountId: "acct-roster-1" }], new Date("2026-07-24Z"), new Date("2026-10-22Z"))).rejects.toThrow(/malformed row/);
    const stuck = fakeGateway({ getBalanceHistory: vi.fn(async () => ({ items: [], hasMore: true })) });
    await expect(loadAirwallexProfileSources(stuck, [{ customerId: "c1", accountId: "acct-roster-1" }], new Date("2026-07-24Z"), new Date("2026-10-22Z"))).rejects.toThrow(/supported page range/);
  });

  it("sends per-account REST reads with x-on-behalf-of and keeps report URLs out of errors", async () => {
    const calls: Array<{ url: string; headers: Headers }> = [];
    const fetcher: typeof fetch = vi.fn(async (input, init) => {
      const url = String(input);
      const headers = new Headers(init?.headers);
      calls.push({ url, headers });
      if (url.endsWith("/api/v1/authentication/login")) return new Response(JSON.stringify({ token: "fake-token", expires_in: 3600 }), { status: 200 });
      if (url.includes("/api/v1/accounts/")) return new Response(JSON.stringify({ status: "ACTIVE" }), { status: 200 });
      if (url.includes("/api/v1/rfis")) return new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 });
      if (url.includes("/api/v1/balances/history")) return new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 });
      return new Response(JSON.stringify({}), { status: 200 });
    });
    const gateway = new HttpAirwallexUnderwritingGateway({ baseUrl: "https://api.invalid", clientId: "fixture", apiKey: "fixture", fetcher });
    await gateway.getConnectedAccount("acct-1");
    await gateway.getOpenRfis("acct-1", 0);
    await gateway.getBalanceHistory("acct-1", "2026-10-01", "2026-10-07", 0);
    const scoped = calls.filter((call) => call.url.includes("/api/v1/accounts/") || call.url.includes("/api/v1/rfis") || call.url.includes("/api/v1/balances/history"));
    expect(scoped).toHaveLength(3);
    expect(scoped.every((call) => call.headers.get("x-on-behalf-of") === "acct-1")).toBe(true);
  });
});
