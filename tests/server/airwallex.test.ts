import { describe, expect, it } from "vitest";
import { HttpAirwallexGateway } from "../../server/src/airwallex/client.js";

describe("Airwallex gateway", () => {

  it("sends the documented Sandbox Issuing simulation fields and MCC", async () => {
    let requestBody: Record<string, unknown> | undefined;
    const headers: Headers[] = [];
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input, init = {}) => {
        const url = String(input);
        headers.push(new Headers(init.headers));
        if (url.endsWith("/authentication/login")) return new Response(JSON.stringify({ token: "access" }));
        requestBody = JSON.parse(String(init.body)) as Record<string, unknown>;
        return new Response(JSON.stringify({ status: "DECLINED" }));
      },
    });

    await gateway.createIssuingSimulation({
      accountId: "acct_bloom",
      cardId: "card_bloom",
      amountCents: 900_000,
      currency: "USD",
      merchant: "Supplier",
      merchantCategoryCode: "5411",
      lifecycleId: "d8af1614-a6a1-4d45-aae7-6858fc6d9ede",
    });

    expect(requestBody).toEqual({
      card_id: "card_bloom",
      transaction_amount: 9000,
      transaction_currency: "USD",
      merchant_category_code: "5411",
      merchant_info: "Supplier".padEnd(43, " "),
      card_transaction_lifecycle_id: "d8af1614-a6a1-4d45-aae7-6858fc6d9ede",
    });
    expect(requestBody).not.toHaveProperty("transaction_failure_reason");
    expect(headers[1].get("x-on-behalf-of")).toBe("acct_bloom");
    expect(headers[1].get("x-api-version")).toBe("2026-08-21");
  });

  it("retrieves all issuing events for a lifecycle with account scoping and pagination", async () => {
    const calls: Array<{ url: URL; headers: Headers }> = [];
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input, init = {}) => {
        const url = new URL(String(input));
        if (url.pathname.endsWith("/authentication/login")) {
          return new Response(JSON.stringify({ token: "access" }));
        }
        calls.push({ url, headers: new Headers(init.headers) });
        if (url.searchParams.get("page") === "next-bookmark") {
          return new Response(JSON.stringify({ items: [{ id: "event-2" }], has_more: false }));
        }
        return new Response(JSON.stringify({ items: [{ id: "event-1" }], has_more: true, page_after: "next-bookmark" }));
      },
    });

    await expect(gateway.getIssuingLifecycleEvents("lifecycle-1", "account-bloom")).resolves.toEqual([
      { id: "event-1" }, { id: "event-2" },
    ]);

    expect(calls.map(({ url }) => url.pathname)).toEqual([
      "/api/v1/issuing/card_transaction_events",
      "/api/v1/issuing/card_transaction_events",
    ]);
    expect(calls[0]?.url.searchParams.get("lifecycle_id")).toBe("lifecycle-1");
    expect(calls[0]?.url.searchParams.get("page_size")).toBe("100");
    expect(calls[1]?.url.searchParams.get("page")).toBe("next-bookmark");
    expect(calls[0]?.headers.get("x-on-behalf-of")).toBe("account-bloom");
    expect(calls[0]?.headers.get("x-api-version")).toBe("2026-08-21");
  });

  it("keeps generic lifecycle retrieval independent from the number of lifecycle events", async () => {
    const calls: URL[] = [];
    const lifecycle = {
      process_result: "DECLINED",
      failure_reason: "INSUFFICIENT_FUNDS",
      card_transaction_event_id: "latest-event",
      card_transaction_id: "transaction-1",
      card_transaction_lifecycle_id: "lifecycle-1",
      events: [{ id: "authorization-event" }, { id: "clearing-event" }],
    };
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input) => {
        const url = new URL(String(input));
        calls.push(url);
        if (url.pathname.endsWith("/authentication/login")) return new Response(JSON.stringify({ token: "access" }));
        return new Response(JSON.stringify(lifecycle));
      },
    });

    await expect(gateway.getIssuingLifecycle("lifecycle-1", "account-bloom")).resolves.toEqual(lifecycle);

    expect(calls.map(({ pathname }) => pathname)).toEqual([
      "/api/v1/authentication/login",
      "/api/v1/issuing/lifecycles/lifecycle-1",
    ]);
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
