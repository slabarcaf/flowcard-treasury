import { describe, expect, it, vi } from "vitest";
import { HttpAirwallexGateway, ProviderHttpError, type IssuingSimulationRequest } from "../../server/src/airwallex/client.js";
import { AmbiguousIssuingResponseError, simulateIssuingTransaction } from "../../server/src/airwallex/issuing.js";

const lifecycleId = "00000000-0000-4000-8000-000000000001";
const input = {
  accountId: "fixture-account-bloom",
  cardId: "fixture-card-bloom",
  amountCents: 900_000,
  currency: "USD",
  merchant: "Fixture Supplier",
  merchantCategoryCode: "5411",
  scenarioId: "kit5-bloom",
};
const response = {
  process_result: "APPROVED",
  failure_reason: null,
  card_transaction_event_id: "evt_fixture_1",
  card_transaction_id: "txn_fixture_1",
  card_transaction_lifecycle_id: lifecycleId,
};

function gateway(
  create: () => Promise<unknown>,
  lookup: (lifecycleId: string, accountId: string) => Promise<unknown | null> = async () => null,
) {
  return {
    createIssuingSimulation: vi.fn(create),
    getIssuingLifecycle: vi.fn(lookup),
  };
}

describe("issuing simulation operation", () => {
  it("persists Airwallex's returned lifecycle after creating and normalizes an approved result", async () => {
    const order: string[] = [];
    const provider = gateway(async () => { order.push("create"); return response; });
    const result = await simulateIssuingTransaction(input, {
      gateway: provider,
      persistLifecycleId: async (scenarioId, id) => {
        order.push("persist");
        expect(scenarioId).toBe(input.scenarioId);
        expect(id).toBe(lifecycleId);
      },
    });

    expect(order).toEqual(["create", "persist"]);
    expect(result).toEqual({
      scenarioId: input.scenarioId,
      processResult: "APPROVED",
      failureReason: null,
      cardTransactionEventId: "evt_fixture_1",
      cardTransactionId: "txn_fixture_1",
      cardTransactionLifecycleId: lifecycleId,
    });
  });

  it("preserves a provider decline and its unexpected failure reason", async () => {
    const provider = gateway(async () => ({ ...response, process_result: "DECLINED", failure_reason: "NEW_PROVIDER_REASON" }));
    const result = await simulateIssuingTransaction(input, {
      gateway: provider,
      persistLifecycleId: async () => {},
    });
    expect(result.processResult).toBe("DECLINED");
    expect(result.failureReason).toBe("NEW_PROVIDER_REASON");
  });

  it("validates card, amount, currency, merchant, and scenario before persistence or provider calls", async () => {
    const provider = gateway(async () => response);
    const persist = vi.fn(async () => {});
    const cases = [
      [{ ...input, cardId: " " }, "card ID"],
      [{ ...input, amountCents: 1_000_000 }, "amount"],
      [{ ...input, currency: "EUR" }, "currency"],
      [{ ...input, merchant: " " }, "merchant"],
      [{ ...input, scenarioId: " " }, "scenario ID"],
    ] as const;
    for (const [invalid, message] of cases) {
      await expect(simulateIssuingTransaction(invalid, {
        gateway: provider,
        persistLifecycleId: persist,
      })).rejects.toThrow(message);
    }
    expect(persist).not.toHaveBeenCalled();
    expect(provider.createIssuingSimulation).not.toHaveBeenCalled();
  });

  it("leaves a timed-out create unresolved when Airwallex has not returned a lifecycle", async () => {
    const provider = gateway(async () => { throw new DOMException("timed out", "TimeoutError"); },
      vi.fn(async (_id: string, _accountId: string): Promise<unknown | null> => response));
    await expect(simulateIssuingTransaction(input, {
      gateway: provider,
      persistLifecycleId: async () => {},
    })).rejects.toMatchObject({ name: "AmbiguousIssuingResponseError", lifecycleId: null });
    expect(provider.getIssuingLifecycle).not.toHaveBeenCalled();
    expect(provider.createIssuingSimulation).toHaveBeenCalledTimes(1);
  });

  it("does not retry or look up a timeout without a provider lifecycle", async () => {
    const provider = gateway(async () => { throw new DOMException("timed out", "TimeoutError"); });
    await expect(simulateIssuingTransaction(input, {
      gateway: provider,
      persistLifecycleId: async () => {},
    })).rejects.toBeInstanceOf(AmbiguousIssuingResponseError);
    expect(provider.getIssuingLifecycle).not.toHaveBeenCalled();
    expect(provider.createIssuingSimulation).toHaveBeenCalledTimes(1);
  });

  it("surfaces a malformed provider response", async () => {
    const provider = gateway(async () => ({ process_result: "APPROVED" }));
    await expect(simulateIssuingTransaction(input, {
      gateway: provider,
      persistLifecycleId: async () => {},
    })).rejects.toMatchObject({ name: "AmbiguousIssuingResponseError", lifecycleId: null });
  });

  it("does not reconcile definitive provider rejections", async () => {
    const provider = gateway(async () => { throw new ProviderHttpError(400, "rejected"); });
    await expect(simulateIssuingTransaction(input, {
      gateway: provider,
      persistLifecycleId: async () => {},
    })).rejects.toBeInstanceOf(ProviderHttpError);
    expect(provider.getIssuingLifecycle).not.toHaveBeenCalled();
  });

  it("sends the server-side simulation request with customer context and API version", async () => {
    const requests: Array<{ url: string; init: RequestInit }> = [];
    const provider = new HttpAirwallexGateway({
      baseUrl: "https://airwallex.invalid",
      clientId: "fixture-client",
      apiKey: "fixture-key",
      fetcher: async (url, init = {}) => {
        requests.push({ url: String(url), init });
        if (String(url).endsWith("/authentication/login")) return new Response(JSON.stringify({ token: "fixture-token" }));
        return new Response(JSON.stringify(response));
      },
    });
    const request: IssuingSimulationRequest = {
      accountId: input.accountId,
      cardId: input.cardId,
      amountCents: input.amountCents,
      currency: input.currency,
      merchant: input.merchant,
      merchantCategoryCode: input.merchantCategoryCode,
    };
    await provider.createIssuingSimulation(request);
    expect(requests[1]?.url).toBe("https://airwallex.invalid/api/v1/simulation/issuing/create");
    const headers = new Headers(requests[1]?.init.headers);
    expect(headers.get("x-on-behalf-of")).toBe(input.accountId);
    expect(headers.get("x-api-version")).toBe("2026-08-21");
    expect(JSON.parse(String(requests[1]?.init.body))).toEqual({
      card_id: input.cardId,
      transaction_amount: 9000,
      transaction_currency: "USD",
      merchant_category_code: "5411",
      merchant_info: input.merchant.padEnd(43, " "),
    });
    expect(headers.get("x-api-key")).toBeNull();
  });
});
