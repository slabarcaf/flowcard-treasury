import { describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { listActiveCustomersForSnapshots, listCustomers } from "../../server/src/roster/repository.js";
import { deactivateCustomer, enrollCustomer } from "../../server/src/roster/service.js";

const row = {
  customer_id: "customer-1",
  salesforce_account_id: "sf-001",
  airwallex_connected_account_id: "acct-001",
  is_active: true,
  enrolled_at: new Date("2026-10-21T00:00:00Z"),
  enrolled_by: "operator@example.com",
  enrollment_reason: "Approved for demo",
  deactivated_at: null,
  deactivated_by: null,
  deactivation_reason: null,
  updated_at: new Date("2026-10-21T00:00:00Z"),
};

class RosterPool implements DatabasePool {
  queries: Array<{ sql: string; values: readonly unknown[] }> = [];
  customer: typeof row | null = row;
  uniqueViolation = false;

  async query<Row>(): Promise<QueryResult<Row>> {
    throw new Error("Use transaction client in roster service tests");
  }

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        this.queries.push({ sql, values });
        const text = sql.replace(/\s+/g, " ").trim();
        if (text === "BEGIN" || text === "COMMIT" || text === "ROLLBACK") {
          return { rows: [], rowCount: null } as QueryResult<Row>;
        }
        if (this.uniqueViolation && text.startsWith("INSERT INTO customer_roster")) {
          throw Object.assign(new Error("duplicate key"), { code: "23505" });
        }
        if (text.startsWith("INSERT INTO customer_roster (") && text.includes("RETURNING")) {
          return { rows: [row], rowCount: 1 } as QueryResult<Row>;
        }
        if (text.startsWith("UPDATE customer_roster")) {
          return { rows: this.customer?.is_active ? [{ customer_id: row.customer_id }] : [], rowCount: this.customer?.is_active ? 1 : 0 } as QueryResult<Row>;
        }
        if (text.startsWith("INSERT INTO customer_roster_audit")) {
          return { rows: [], rowCount: 1 } as QueryResult<Row>;
        }
        return { rows: [], rowCount: 0 } as QueryResult<Row>;
      },
      release: vi.fn(),
    };
  }

  async end(): Promise<void> {}
}

describe("customer enrollment roster", () => {
  it("enrolls an explicitly approved customer and writes an audit record", async () => {
    const pool = new RosterPool();
    const enrolled = await enrollCustomer(pool, {
      customerId: " customer-1 ",
      salesforceAccountId: "sf-001",
      airwallexConnectedAccountId: "acct-001",
      actor: " operator@example.com ",
      reason: " Approved for demo ",
    });

    expect(enrolled.customerId).toBe("customer-1");
    expect(pool.queries.map(({ sql }) => sql.trim().split(/\s+/)[0])).toEqual(["BEGIN", "INSERT", "INSERT", "COMMIT"]);
    expect(pool.queries[1].values).toEqual(["customer-1", "sf-001", "acct-001", "operator@example.com", "Approved for demo"]);
    expect(pool.queries[2].values).toEqual(["customer-1", "operator@example.com", "Approved for demo"]);
  });

  it("rejects missing identity, mappings, actor, and reason before database writes", async () => {
    const pool = new RosterPool();
    await expect(enrollCustomer(pool, {
      customerId: "customer-1", salesforceAccountId: " ", airwallexConnectedAccountId: "acct-1", actor: "operator", reason: "ok",
    })).rejects.toThrow("salesforceAccountId is required");
    await expect(enrollCustomer(pool, {
      customerId: "customer-1", salesforceAccountId: "sf-1", airwallexConnectedAccountId: "acct-1", actor: "operator", reason: " ",
    })).rejects.toThrow("reason is required");
    expect(pool.queries).toHaveLength(0);
  });

  it("rejects duplicate customer or provider IDs without leaving a partial enrollment", async () => {
    const pool = new RosterPool();
    pool.uniqueViolation = true;
    await expect(enrollCustomer(pool, {
      customerId: "customer-1", salesforceAccountId: "sf-001", airwallexConnectedAccountId: "acct-001", actor: "operator", reason: "approved",
    })).rejects.toThrow("must be unique");
    expect(pool.queries.some(({ sql }) => sql === "ROLLBACK")).toBe(true);
    expect(pool.queries.some(({ sql }) => sql.includes("customer_roster_audit"))).toBe(false);
  });

  it("soft-deactivates with audit metadata while retaining the row", async () => {
    const pool = new RosterPool();
    await deactivateCustomer(pool, { customerId: "customer-1", actor: "operator", reason: "Program ended" });
    expect(pool.queries[1].sql).toContain("SET is_active = false");
    expect(pool.queries[1].sql).toContain("WHERE customer_id = $1 AND is_active");
    expect(pool.queries[1].values).toEqual(["customer-1", "operator", "Program ended"]);
    expect(pool.queries[2].sql).toContain("customer_roster_audit");
  });

  it("requires a reason and reports attempts to deactivate unknown or already inactive rows", async () => {
    const pool = new RosterPool();
    await expect(deactivateCustomer(pool, { customerId: "customer-1", actor: "operator", reason: " " })).rejects.toThrow("reason is required");
    pool.customer = null;
    await expect(deactivateCustomer(pool, { customerId: "missing", actor: "operator", reason: "cleanup" })).rejects.toThrow("Active enrolled customer not found");
  });

  it("lists active snapshot members separately from the full retained roster", async () => {
    const query = vi.fn().mockResolvedValue({ rows: [row], rowCount: 1 });
    const pool = { query } as unknown as DatabasePool;
    await listActiveCustomersForSnapshots(pool);
    expect(query.mock.calls[0][0]).toContain("WHERE is_active");
    await listCustomers(pool);
    expect(query.mock.calls[1][0]).not.toContain("WHERE is_active");
  });

  it("pages active snapshot members with a bounded keyset query", async () => {
    const query = vi.fn().mockResolvedValue({ rows: [row], rowCount: 1 });
    const pool = { query } as unknown as DatabasePool;
    const customers = await listActiveCustomersForSnapshots(pool, { afterCustomerId: "customer-0", limit: 1_000 });
    expect(customers.map(({ customerId }) => customerId)).toEqual([row.customer_id]);
    expect(query.mock.calls[0]?.[0]).toContain("customer_id > $1");
    expect(query.mock.calls[0]?.[0]).toContain("LIMIT $2");
    expect(query.mock.calls[0]?.[1]).toEqual(["customer-0", 1_000]);
    await expect(listActiveCustomersForSnapshots(pool, { afterCustomerId: null, limit: 1_001 })).rejects.toThrow("1 through 1,000");
  });
});
