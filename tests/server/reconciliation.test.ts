import { describe, expect, it } from "vitest";
import type { AirwallexGateway, ProviderOperationResponse } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { reconcileProviderOperations } from "../../server/src/reconciliation/service.js";

const operation = {
  id: "op-1", bridge_id: "00000000-0000-4000-8000-000000000001", operation_type: "bridge" as const,
  request_id: "bridge-00000000-0000-4000-8000-000000000001", provider_object_id: null,
  status: "created", amount_cents: 50_000, outbox_status: "done",
};

class ReconciliationPool implements DatabasePool {
  readonly holds: Array<{ reason: string; active: boolean }> = [];
  readonly statements: string[] = [];
  observations = 0;
  ledgerEvents = 0;
  constructor(public observed: ProviderOperationResponse | null, public readonly ops = [operation]) {}

  async query<Row>(sql: string): Promise<QueryResult<Row>> {
    this.statements.push(sql);
    if (sql.includes("FROM provider_operations AS operation")) return { rows: this.ops as Row[], rowCount: this.ops.length };
    throw new Error(`Unexpected pool query: ${sql}`);
  }

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string): Promise<QueryResult<Row>> => {
        this.statements.push(sql);
        if (sql.includes("INSERT INTO facility_holds")) {
          const reason = this.observed ? "mismatch" : "missing";
          if (!this.holds.some((hold) => hold.reason === reason && hold.active)) this.holds.push({ reason, active: true });
          return { rows: [], rowCount: 1 };
        }
        if (sql.includes("UPDATE facility_holds")) {
          const active = this.holds.filter((hold) => hold.active);
          active.forEach((hold) => { hold.active = false; });
          return { rows: [], rowCount: active.length };
        }
        if (sql.includes("FROM provider_operations WHERE id")) return { rows: [this.ops[0]] as Row[], rowCount: 1 };
        if (sql.includes("INSERT INTO reconciliation_observations")) { this.observations++; return { rows: [], rowCount: 1 }; }
        if (sql.includes("INSERT INTO ledger_events")) { this.ledgerEvents++; return { rows: [{ id: "1" }] as Row[], rowCount: 1 }; }
        if (sql.includes("FROM facility_configs")) return { rows: [{ facility_amount_cents: 5_000_000, reserve_floor_bps: 2000 }] as Row[], rowCount: 1 };
        if (sql.includes("FROM bridge_cases ORDER BY")) return { rows: [{ id: operation.bridge_id, customer_id: "c1" }] as Row[], rowCount: 1 };
        if (sql.includes("FROM ledger_events")) return { rows: [{ id: 1, event_type: "bridge_reserved", bridge_id: operation.bridge_id, amount_cents: 50_000, currency: "USD", provenance: "approved_command", source_id: "reserve", payload: {}, occurred_at: new Date() }] as Row[], rowCount: 1 };
        return { rows: [], rowCount: 1 };
      },
      release: () => {},
    };
  }
  async end(): Promise<void> {}
}

function gateway(response: ProviderOperationResponse | null): AirwallexGateway {
  return {
    listConnectedAccounts: async () => [],
    getTransferByRequestId: async () => response,
    getChargeByRequestId: async () => response,
    createTransfer: async () => { throw new Error("not used"); },
    createCharge: async () => { throw new Error("not used"); },
    getBalance: async () => ({ availableCents: 0, currency: "USD" }),
    getDeposit: async () => ({ id: "dep", amountCents: 0, currency: "USD", status: "settled", settledAt: new Date("2026-10-21T00:00:00Z") }),
    retrySandboxCard: async () => { throw new Error("not used"); },
  };
}

describe("provider reconciliation", () => {
  it("recovers a missed settled event by request ID as an immutable observation and ledger event", async () => {
    const pool = new ReconciliationPool({ id: "tr-1", requestId: operation.request_id, status: "settled" });
    const result = await reconcileProviderOperations(pool, gateway(pool.observed));

    expect(result).toMatchObject({ scanned: 1, recovered: 1, held: 0 });
    expect(pool.statements.some((sql) => sql.includes("WHERE operation.status NOT IN"))).toBe(true);
    expect(pool.observations).toBe(1);
    expect(pool.ledgerEvents).toBe(1);
    expect(pool.statements.some((sql) => sql.includes("UPDATE provider_operations SET provider_object_id"))).toBe(true);
  });

  it("holds funding when the provider returns a different request ID", async () => {
    const pool = new ReconciliationPool({ id: "tr-1", requestId: "bridge-other", status: "pending" });
    const result = await reconcileProviderOperations(pool, gateway(pool.observed));

    expect(result.held).toBe(1);
    expect(pool.holds).toEqual([{ reason: "mismatch", active: true }]);
    expect(pool.observations).toBe(0);
    expect(pool.statements.some((sql) => sql.includes("INSERT INTO facility_holds"))).toBe(true);
  });

  it("retains a hold when an operation is absent, then releases it after a clean reconciliation", async () => {
    const pool = new ReconciliationPool(null);
    const first = await reconcileProviderOperations(pool, gateway(null));
    expect(first.held).toBe(1);
    expect(first.holdReleased).toBe(false);

    pool.observed = { id: "tr-1", requestId: operation.request_id, status: "pending" };
    const second = await reconcileProviderOperations(pool, gateway(pool.observed));
    expect(second).toMatchObject({ recovered: 1, held: 0, holdReleased: true });
    expect(pool.holds[0]?.active).toBe(false);
  });
});
