import { describe, expect, it } from "vitest";
import { HttpAirwallexGateway, normalizeProviderStatus, ProviderHttpError } from "../../server/src/airwallex/client.js";

const request = { requestId: "bridge-00000000-0000-4000-8000-000000000001", amountCents: 500_000, currency: "USD" as const, payload: { destination: "acct" } };

describe("Airwallex gateway", () => {
  it("normalizes provider statuses into the ledger contract", () => {
    expect(normalizeProviderStatus("COMPLETED")).toBe("settled");
    expect(normalizeProviderStatus("pending_transfer")).toBe("pending");
    expect(normalizeProviderStatus("mystery")).toBe("unknown");
  });

  it("preserves duplicate operation responses as successful idempotent results", async () => {
    const calls: Array<{ url: string; init: RequestInit }> = [];
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input, init = {}) => {
        calls.push({ url: String(input), init });
        if (String(input).endsWith("/authentication/login")) return new Response(JSON.stringify({ token: "access", expires_in: 1800 }));
        return new Response(JSON.stringify({ id: "tr_1", status: "PENDING", duplicate: true, request_id: request.requestId }), { status: 409 });
      },
    });
    await expect(gateway.createTransfer(request)).resolves.toMatchObject({ id: "tr_1", status: "pending", duplicate: true });
    expect(calls.map((call) => call.url)).toEqual([
      "https://airwallex.invalid/api/v1/authentication/login",
      "https://airwallex.invalid/api/v1/connected_account_transfers/create",
    ]);
    expect(new Headers(calls[0].init.headers).get("x-client-id")).toBe("id");
    expect(new Headers(calls[1].init.headers).get("authorization")).toBe("Bearer access");
    expect(new Headers(calls[1].init.headers).get("x-api-key")).toBeNull();
  });

  it("surfaces provider errors and request timeouts", async () => {
    const failing = new HttpAirwallexGateway({ baseUrl: "https://airwallex.invalid", clientId: "id", apiKey: "key", fetcher: async (input) => String(input).endsWith("/authentication/login") ? new Response(JSON.stringify({ token: "access" })) : new Response("{}", { status: 503 }) });
    await expect(failing.createCharge(request)).rejects.toBeInstanceOf(ProviderHttpError);
    let calls = 0;
    const timeout = new HttpAirwallexGateway({ baseUrl: "https://airwallex.invalid", clientId: "id", apiKey: "key", fetcher: async () => {
      calls++;
      if (calls === 1) return new Response(JSON.stringify({ token: "access" }));
      throw new DOMException("timed out", "TimeoutError");
    } });
    await expect(timeout.createTransfer(request)).rejects.toMatchObject({ name: "TimeoutError" });
  });

  it("uses the documented balance, deposit, charge, and sandbox routes", async () => {
    const paths: string[] = [];
    const queryIds: Array<string | null> = [];
    const headers: Headers[] = [];
    const gateway = new HttpAirwallexGateway({ baseUrl: "https://airwallex.invalid", clientId: "id", apiKey: "key", fetcher: async (input, init = {}) => {
      const url = String(input);
      paths.push(new URL(url).pathname);
      queryIds.push(new URL(url).searchParams.get("request_id"));
      headers.push(new Headers(init.headers));
      if (url.endsWith("/authentication/login")) return new Response(JSON.stringify({ token: "access" }));
      if (url.includes("/balances/current")) return new Response(JSON.stringify({ available_amount: 12.34, currency: "USD" }));
      if (url.includes("/deposits/dep_1")) return new Response(JSON.stringify({ id: "dep_1", amount: 12.34, currency: "USD", status: "SETTLED", settled_at: "2026-10-21T12:30:00Z" }));
      return new Response(JSON.stringify({ id: "op_1", status: "PENDING", request_id: request.requestId }));
    } });
    await gateway.getBalance("acct_1", "USD");
    await expect(gateway.getDeposit("dep_1", "acct_1")).resolves.toMatchObject({ settledAt: new Date("2026-10-21T12:30:00Z") });
    await gateway.getTransferByRequestId(request.requestId);
    await gateway.createCharge(request);
    await gateway.retrySandboxCard({ requestId: request.requestId, accountId: "acct_1", cardId: "card_1", amountCents: 1234, currency: "USD" });
    expect(paths).toEqual([
      "/api/v1/authentication/login", "/api/v1/balances/current", "/api/v1/deposits/dep_1",
      "/api/v1/connected_account_transfers", "/api/v1/charges/create", "/api/v1/simulation/issuing/create",
    ]);
    expect(queryIds[3]).toBe(request.requestId);
    expect(headers[1].get("x-on-behalf-of")).toBe("acct_1");
    expect(headers[2].get("x-on-behalf-of")).toBe("acct_1");
    expect(headers[5].get("x-on-behalf-of")).toBe("acct_1");
  });

  it("lists connected accounts through every source page", async () => {
    const requests: URL[] = [];
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input) => {
        const url = new URL(String(input));
        if (url.pathname.endsWith("/authentication/login")) return new Response(JSON.stringify({ token: "access" }));
        requests.push(url);
        const page = Number(url.searchParams.get("page_num"));
        return new Response(JSON.stringify({
          has_more: page === 0,
          items: [{ id: `acct-${page}`, status: page === 0 ? "ACTIVE" : "SUSPENDED" }],
        }));
      },
    });

    await expect(gateway.listConnectedAccounts()).resolves.toEqual([
      { id: "acct-0", status: "ACTIVE" },
      { id: "acct-1", status: "SUSPENDED" },
    ]);
    expect(requests.map((url) => url.pathname)).toEqual(["/api/v1/accounts", "/api/v1/accounts"]);
    expect(requests.map((url) => url.searchParams.get("page_num"))).toEqual(["0", "1"]);
    expect(requests.every((url) => url.searchParams.get("page_size") === "500")).toBe(true);
  });

  it("fails closed when a connected-account page is malformed", async () => {
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input) => String(input).endsWith("/authentication/login")
        ? new Response(JSON.stringify({ token: "access" }))
        : new Response(JSON.stringify({ has_more: false, items: [{ id: "acct-1" }] })),
    });

    await expect(gateway.listConnectedAccounts()).rejects.toThrow("without an ID or status");
  });

  it("fails closed when pagination claims more results after an empty page", async () => {
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input) => String(input).endsWith("/authentication/login")
        ? new Response(JSON.stringify({ token: "access" }))
        : new Response(JSON.stringify({ has_more: true, items: [] })),
    });

    await expect(gateway.listConnectedAccounts()).rejects.toThrow("empty page with more results");
  });
});
