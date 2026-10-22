import { describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { runRosterCommand } from "../../server/src/roster/commands.js";
import { mapCustomerAccount } from "../../server/src/roster/service.js";

const customerId = "acct_bloom001";
const oldAccountId = customerId;
const newAccountId = "provider-account-fixture-verified";

class MappingPool implements DatabasePool {
  queries: Array<{ sql: string; values: readonly unknown[] }> = [];
  accountId = oldAccountId;
  activeCustomer = true;
  activeCardBinding = false;
  duplicateAccount = false;
  failAudit = false;
  rolledBack = false;
  committed = false;
  private accountAtBegin = oldAccountId;

  async query<Row>(): Promise<QueryResult<Row>> {
    throw new Error("Use the transaction client in account mapping tests");
  }

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        this.queries.push({ sql, values });
        const text = sql.replace(/\s+/g, " ").trim();
        if (text === "BEGIN") this.accountAtBegin = this.accountId;
        if (text === "COMMIT") this.committed = true;
        if (text === "ROLLBACK") {
          this.rolledBack = true;
          this.accountId = this.accountAtBegin;
        }
        if (text.startsWith("SELECT customer_id, airwallex_connected_account_id")) {
          return { rows: this.activeCustomer ? [{ customer_id: customerId,
            airwallex_connected_account_id: this.accountId }] : [], rowCount: this.activeCustomer ? 1 : 0 } as QueryResult<Row>;
        }
        if (text.startsWith("SELECT card_id FROM issuing_card_bindings")) {
          return { rows: this.activeCardBinding ? [{ card_id: "card-fixture" }] : [], rowCount: this.activeCardBinding ? 1 : 0 } as QueryResult<Row>;
        }
        if (text.startsWith("UPDATE customer_roster")) {
          if (this.duplicateAccount) throw Object.assign(new Error("duplicate key"), { code: "23505" });
          this.accountId = String(values[1]);
          return { rows: [{ customer_id: customerId }], rowCount: 1 } as QueryResult<Row>;
        }
        if (text.startsWith("INSERT INTO customer_roster_account_mapping_audit")) {
          if (this.failAudit) throw new Error("audit insert failed");
          return { rows: [], rowCount: 1 } as QueryResult<Row>;
        }
        return { rows: [], rowCount: 0 } as QueryResult<Row>;
      },
      release: vi.fn(),
    };
  }

  async end(): Promise<void> {}
}

const validInput = {
  customerId,
  airwallexConnectedAccountId: newAccountId,
  actor: "operator-fixture",
  reason: "Verified mapping fixture",
};

describe("audited roster account mapping", () => {
  it("replaces an existing placeholder and writes old/new IDs, actor, reason, and timestamp in one transaction", async () => {
    const pool = new MappingPool();
    await expect(mapCustomerAccount(pool, validInput)).resolves.toEqual({
      customerId, oldAccountId, newAccountId,
    });
    const audit = pool.queries.find(({ sql }) => sql.includes("INSERT INTO customer_roster_account_mapping_audit"));
    expect(audit?.sql).toContain("changed_at");
    expect(audit?.sql).toContain("now()");
    expect(audit?.values).toEqual([customerId, oldAccountId, newAccountId,
      "operator-fixture", "Verified mapping fixture"]);
    expect(pool.queries.map(({ sql }) => sql.trim())).toEqual([
      "BEGIN",
      expect.stringContaining("FOR UPDATE"),
      expect.stringContaining("issuing_card_bindings"),
      expect.stringContaining("UPDATE customer_roster"),
      expect.stringContaining("INSERT INTO customer_roster_account_mapping_audit"),
      "COMMIT",
    ]);
    expect(pool.committed).toBe(true);
    expect(pool.rolledBack).toBe(false);
  });

  it("rejects an account ID already assigned to another roster customer and rolls back", async () => {
    const pool = new MappingPool();
    pool.duplicateAccount = true;
    await expect(mapCustomerAccount(pool, validInput)).rejects.toThrow("already assigned");
    expect(pool.rolledBack).toBe(true);
    expect(pool.committed).toBe(false);
    expect(pool.queries.some(({ sql }) => sql.includes("customer_roster_account_mapping_audit"))).toBe(false);
  });

  it("rejects missing or inactive customers and rolls back", async () => {
    const pool = new MappingPool();
    pool.activeCustomer = false;
    await expect(mapCustomerAccount(pool, validInput)).rejects.toThrow("Active enrolled customer not found");
    expect(pool.rolledBack).toBe(true);
    expect(pool.queries.some(({ sql }) => sql.startsWith("UPDATE customer_roster"))).toBe(false);
  });

  it("refuses an active card binding until the operator unbinds it", async () => {
    const pool = new MappingPool();
    pool.activeCardBinding = true;
    await expect(mapCustomerAccount(pool, validInput)).rejects.toThrow("Deactivate active card bindings");
    expect(pool.rolledBack).toBe(true);
    expect(pool.queries.some(({ sql }) => sql.startsWith("UPDATE customer_roster"))).toBe(false);
  });

  it("rolls the account update back if the audit insert fails", async () => {
    const pool = new MappingPool();
    pool.failAudit = true;
    await expect(mapCustomerAccount(pool, validInput)).rejects.toThrow("audit insert failed");
    expect(pool.accountId).toBe(oldAccountId);
    expect(pool.rolledBack).toBe(true);
    expect(pool.committed).toBe(false);
  });

  it("validates the account ID and rejects the internal customer alias", async () => {
    const pool = new MappingPool();
    await expect(mapCustomerAccount(pool, { ...validInput, airwallexConnectedAccountId: " " }))
      .rejects.toThrow("airwallexConnectedAccountId is required");
    await expect(mapCustomerAccount(pool, { ...validInput, airwallexConnectedAccountId: customerId }))
      .rejects.toThrow("must differ from the internal customer ID");
    expect(pool.queries).toHaveLength(0);
  });

  it("runs map-account with the documented required CLI options", async () => {
    const pool = new MappingPool();
    const output: string[] = [];
    await runRosterCommand(pool, "map-account", [
      "--customer-id", customerId,
      "--airwallex-account-id", newAccountId,
      "--actor", "operator-fixture",
      "--reason", "Verified mapping fixture",
    ], (text) => output.push(text));
    expect(JSON.parse(output[0] ?? "{}")).toEqual({ customerId, oldAccountId, newAccountId });
    await expect(runRosterCommand(pool, "map-account", ["--customer-id", customerId]))
      .rejects.toThrow("--airwallex-account-id is required");
  });
});
