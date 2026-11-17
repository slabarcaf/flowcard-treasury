import { describe, expect, it } from "vitest";
import { HttpAirwallexGateway } from "../../server/src/airwallex/client.js";

describe("Airwallex gateway", () => {

  it("lists connected accounts through every source page", async () => {
    const requests: URL[] = [];
    const methods: string[] = [];
    const gateway = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "id",
      apiKey: "key",
      fetcher: async (input, init) => {
        methods.push(init?.method ?? "GET");
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
    expect(methods).toEqual(["POST", "GET", "GET"]);
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
