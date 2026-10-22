import { describe, expect, it, vi } from "vitest";
import type { DatabasePool } from "../../server/src/db/pool.js";
import { loadConfig } from "../../server/src/config.js";
import {
  createKit5RepaymentDepositTargetResolver,
  createKit5ScenarioResolver,
} from "../../server/src/demo/kit5-scenario-resolver.js";

const validEnv = {
  DATABASE_URL: "postgres://localhost/flowcard",
  AIRWALLEX_CLIENT_ID: "local-client",
  AIRWALLEX_API_KEY: "local-secret",
  AIRWALLEX_WEBHOOK_SECRET: "webhook-secret",
};

function binding(customerId: string, overrides: Record<string, unknown> = {}) {
  return {
    customer_id: customerId,
    roster_account_id: `verified-account-${customerId}`,
    roster_active: true,
    card_id: `verified-card-${customerId}`,
    binding_account_id: `verified-account-${customerId}`,
    binding_active: true,
    ...overrides,
  };
}

function poolWith(rows: Record<string, unknown>[]) {
  const query = vi.fn().mockResolvedValue({ rows, rowCount: rows.length });
  return { pool: { query } as unknown as DatabasePool, query };
}

describe("Kit 5 server-side scenario mappings", () => {
  it("resolves an exact active roster/account/card binding for the full scenario", async () => {
    const ids = ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"];
    const { pool, query } = poolWith(ids.map((id) => binding(id)));
    const fixture = await createKit5ScenarioResolver(pool)("original");

    expect(query.mock.calls[0]?.[0]).toContain("FROM customer_roster AS roster");
    expect(query.mock.calls[0]?.[0]).toContain("LEFT JOIN issuing_card_bindings AS binding");
    expect(fixture?.orderedCustomerIds).toEqual(ids);
    expect(fixture?.resolveCustomer("acct_bloom001")).toMatchObject({
      accountId: "verified-account-acct_bloom001",
      cardId: "verified-card-acct_bloom001",
      amountCents: 900_000,
      currency: "USD",
    });
  });

  it("fails closed when a customer has no roster or card binding", async () => {
    const { pool } = poolWith([]);
    await expect(createKit5ScenarioResolver(pool)("original")).resolves.toBeNull();
  });

  it("fails closed when a customer has duplicate card bindings", async () => {
    const ids = ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"];
    const rows = ids.map((id) => binding(id));
    rows.push(binding("acct_bloom001", { card_id: "second-card" }));
    const { pool } = poolWith(rows);
    await expect(createKit5ScenarioResolver(pool)("original")).resolves.toBeNull();
  });

  it("fails closed on inactive roster or card bindings", async () => {
    const ids = ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"];
    for (const overrides of [{ roster_active: false }, { binding_active: false }]) {
      const rows = ids.map((id) => binding(id, id === "acct_bloom001" ? overrides : {}));
      const { pool } = poolWith(rows);
      await expect(createKit5ScenarioResolver(pool)("original")).resolves.toBeNull();
    }
  });

  it("fails closed when a card binding account differs from its roster account", async () => {
    const ids = ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"];
    const rows = ids.map((id) => binding(id, id === "acct_bloom001" ? { binding_account_id: "other-account" } : {}));
    const { pool } = poolWith(rows);
    await expect(createKit5ScenarioResolver(pool)("original")).resolves.toBeNull();
  });

  it("fails closed when a seeded synthetic customer alias is used as a provider account ID", async () => {
    const ids = ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"];
    const rows = ids.map((id) => binding(id, id === "acct_bloom001"
      ? { roster_account_id: id, binding_account_id: id }
      : {}));
    const { pool } = poolWith(rows);
    await expect(createKit5ScenarioResolver(pool)("original")).resolves.toBeNull();
  });

  it("fails closed on malformed provider account or card values", async () => {
    const ids = ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"];
    for (const overrides of [{ roster_account_id: "bad account" }, { card_id: "" }]) {
      const rows = ids.map((id) => binding(id, id === "acct_bloom001" ? overrides : {}));
      const { pool } = poolWith(rows);
      await expect(createKit5ScenarioResolver(pool)("original")).resolves.toBeNull();
    }
  });
});

describe("Nova server-side USD Global Account mapping", () => {
  const config = {
    globalAccountId: "verified-global-account",
    connectedAccountId: "verified-account-acct_nova001",
    currency: "USD" as const,
  };

  it("loads only a configured USD Global Account matching Nova's active account/card binding", async () => {
    const { pool } = poolWith([binding("acct_nova001")]);
    await expect(createKit5RepaymentDepositTargetResolver(pool, config)("kit5-target", "acct_nova001"))
      .resolves.toEqual({
        accountId: "verified-account-acct_nova001",
        globalAccountId: "verified-global-account",
      });
  });

  it.each([
    ["missing Global Account ID", { ...config, globalAccountId: undefined }],
    ["malformed Global Account ID", { ...config, globalAccountId: "bad global id" }],
    ["missing connected account binding", { ...config, connectedAccountId: undefined }],
    ["wrong currency", { ...config, currency: undefined }],
    ["different connected account", { ...config, connectedAccountId: "another-account" }],
  ])("fails closed for %s", async (_name, targetConfig) => {
    const { pool } = poolWith([binding("acct_nova001")]);
    await expect(createKit5RepaymentDepositTargetResolver(pool, targetConfig as typeof config)("kit5-target", "acct_nova001"))
      .resolves.toBeNull();
  });

  it("loads optional Global Account configuration only for a valid USD operator setting", () => {
    const withAccount = loadConfig({
      ...validEnv,
      KIT5_NOVA_GLOBAL_ACCOUNT_ID: " verified-global-account ",
      KIT5_NOVA_GLOBAL_ACCOUNT_CONNECTED_ACCOUNT_ID: " verified-connected-account ",
      KIT5_NOVA_GLOBAL_ACCOUNT_CURRENCY: "USD",
    });
    expect(withAccount.kit5NovaGlobalAccountId).toBe("verified-global-account");
    expect(withAccount.kit5NovaGlobalAccountConnectedAccountId).toBe("verified-connected-account");
    expect(withAccount.kit5NovaGlobalAccountCurrency).toBe("USD");
    expect(loadConfig({ ...validEnv, KIT5_NOVA_GLOBAL_ACCOUNT_CURRENCY: "EUR" }).kit5NovaGlobalAccountCurrency)
      .toBeUndefined();
  });
});
