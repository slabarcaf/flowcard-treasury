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

  it("creates a settled Global Account deposit simulation on behalf of a connected account", async () => {
    const calls: Array<{ url: string; init: RequestInit }> = [];
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input, init = {}) => {
        const url = String(input);
        calls.push({ url, init });
        if (url.endsWith("/authentication/login")) return new Response(JSON.stringify({ token: "access" }));
        return new Response(JSON.stringify({
          id: "dep_fixture_1",
          amount: 8000,
          currency: "USD",
          deposit_type: "CREDIT",
          fee_amount: 2.5,
          status: "SETTLED",
          create_time: "2026-10-22T12:30:00Z",
        }), { status: 201 });
      },
    });

    await expect(gateway.createGlobalAccountDepositSimulation({
      accountId: "acct_nova_fixture",
      globalAccountId: "ga_nova_usd_fixture",
      amountCents: 800_000,
      payerBankname: " Fixture Bank ",
      payerCountry: "us",
      payerName: "Fixture Payer",
      reference: "Nova Day +9 fixture",
      statementRef: "nova-day-9-fixture-001",
    })).resolves.toEqual({
      id: "dep_fixture_1",
      amountCents: 800_000,
      currency: "USD",
      depositType: "CREDIT",
      feeAmountCents: 250,
      status: "settled",
      createTime: "2026-10-22T12:30:00Z",
    });

    expect(calls.map(({ url }) => url)).toEqual([
      "https://airwallex.invalid/api/v1/authentication/login",
      "https://airwallex.invalid/api/v1/simulation/deposit/create",
    ]);
    expect(new Headers(calls[1]?.init.headers).get("authorization")).toBe("Bearer access");
    expect(new Headers(calls[1]?.init.headers).get("x-on-behalf-of")).toBe("acct_nova_fixture");
    expect(JSON.parse(String(calls[1]?.init.body))).toEqual({
      global_account_id: "ga_nova_usd_fixture",
      amount: 8000,
      payer_bankname: "Fixture Bank",
      payer_country: "US",
      payer_name: "Fixture Payer",
      reference: "Nova Day +9 fixture",
      statement_ref: "nova-day-9-fixture-001",
      status: "SETTLED",
    });
  });

  it("rejects invalid deposit simulation inputs before making a provider request", async () => {
    let calls = 0;
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async () => {
        calls++;
        return new Response(JSON.stringify({ token: "access" }));
      },
    });
    await expect(gateway.createGlobalAccountDepositSimulation({
      accountId: "acct_fixture",
      globalAccountId: "ga_fixture",
      amountCents: 0,
    })).rejects.toThrow("positive safe integer");
    await expect(gateway.createGlobalAccountDepositSimulation({
      accountId: "acct_fixture",
      globalAccountId: "ga_fixture",
      amountCents: 100,
      payerCountry: "USA",
    })).rejects.toThrow("two-letter code");
    expect(calls).toBe(0);
  });

  it("rejects malformed simulation responses and surfaces provider errors", async () => {
    const malformed = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input) => String(input).endsWith("/authentication/login")
        ? new Response(JSON.stringify({ token: "access" }))
        : new Response(JSON.stringify({ id: "dep_fixture", amount: 1, status: "SETTLED" }), { status: 201 }),
    });
    const input = { accountId: "acct_fixture", globalAccountId: "ga_fixture", amountCents: 100 };
    await expect(malformed.createGlobalAccountDepositSimulation(input)).rejects.toThrow("incomplete or invalid");

    const failing = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input) => String(input).endsWith("/authentication/login")
        ? new Response(JSON.stringify({ token: "access" }))
        : new Response("{}", { status: 403 }),
    });
    await expect(failing.createGlobalAccountDepositSimulation(input)).rejects.toBeInstanceOf(ProviderHttpError);
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
