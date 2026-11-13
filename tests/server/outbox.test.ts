import { describe, expect, it } from "vitest";
import { ProviderHttpError, type AirwallexGateway, type ProviderOperationResponse } from "../../server/src/airwallex/client.js";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { dispatchOne } from "../../server/src/outbox/dispatcher.js";
import { OutboxWorker } from "../../server/src/outbox/worker.js";

class OutboxPool implements DatabasePool {
  status = "queued";
  operationStatus = "created";
  lastError: string | null = null;
  readonly requestId = "bridge-00000000-0000-4000-8000-000000000001";
  readonly command = { id: 7, provider_operation_id: "op-1", command_type: "create_bridge_transfer", operation_type: "bridge" as const, request_id: this.requestId, amount_cents: 250000, payload: { bridgeId: "bridge-1" }, attempts: 0 };
  readonly sql: string[] = [];
  async query<Row>(): Promise<QueryResult<Row>> { throw new Error("Unexpected pool query"); }
  async connect(): Promise<DatabaseClient> {
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        const normalized = sql.replace(/\s+/g, " ").trim();
        this.sql.push(normalized);
        if (normalized.startsWith("SELECT outbox.id")) return { rows: this.status === "queued" ? [this.command] : [], rowCount: this.status === "queued" ? 1 : 0 } as QueryResult<Row>;
        if (normalized.startsWith("UPDATE outbox_commands SET status = 'processing'")) this.status = "processing";
        if (normalized.startsWith("UPDATE outbox_commands SET status = 'queued'")) this.status = "queued";
        if (normalized.startsWith("UPDATE outbox_commands SET status = 'failed'")) this.status = "failed";
        if (normalized.startsWith("UPDATE outbox_commands SET status = 'done'")) this.status = "done";
        if (normalized.startsWith("UPDATE provider_operations SET provider_object_id")) this.operationStatus = String(values[2] ?? "unknown");
        if (normalized.startsWith("UPDATE outbox_commands SET status = 'queued'") || normalized.startsWith("UPDATE outbox_commands SET status = 'failed'")) this.lastError = String(values[1] ?? "");
        return { rows: [], rowCount: 1 } as QueryResult<Row>;
      },
      release() {},
    };
  }
  async end() {}
}

describe("outbox dispatcher", () => {
  it("dispatches persisted work when explicitly run and preserves retry state after an ambiguous send", async () => {
    const pool = new OutboxPool();
    let lookupCount = 0;
    let createCount = 0;
    const gateway = {
      listConnectedAccounts: async () => [],
      getTransferByRequestId: async (requestId: string) => {
        lookupCount++;
        expect(requestId).toBe(pool.requestId);
        return null;
      },
      getChargeByRequestId: async () => null,
      createTransfer: async () => {
        createCount++;
        throw new DOMException("network request timed out", "TimeoutError");
      },
      createCharge: async () => { throw new Error("unexpected charge"); },
      getBalance: async () => ({ availableCents: 0, currency: "USD" as const }),
      getDeposit: async () => ({ id: "dep", amountCents: 0, currency: "USD" as const, status: "unknown" as const, settledAt: null }),
      retrySandboxCard: async () => ({ id: "auth", status: "unknown" as const }),
    } satisfies AirwallexGateway;
    const worker = new OutboxWorker(pool, gateway);

    expect(pool.status).toBe("queued");
    expect(lookupCount).toBe(0);
    await expect(worker.runOnce()).resolves.toBe(true);

    expect(pool.status).toBe("queued");
    expect(pool.operationStatus).toBe("created");
    expect(pool.lastError).toContain("TimeoutError: network request timed out");
    expect(createCount).toBe(1);
    expect(lookupCount).toBe(1);
  });

  it("reuses the persisted request ID and recovers an accepted timed-out send by lookup", async () => {
    const pool = new OutboxPool();
    let createCount = 0;
    const response: ProviderOperationResponse = { id: "tr_1", requestId: pool.requestId, status: "pending" };
    const gateway: AirwallexGateway = {
      listConnectedAccounts: async () => [],
      getTransferByRequestId: async (id) => { expect(id).toBe(pool.requestId); return createCount ? response : null; },
      getChargeByRequestId: async () => null,
      createTransfer: async (input) => { createCount++; expect(input.requestId).toBe(pool.requestId); throw new DOMException("network request timed out after provider accepted request", "TimeoutError"); },
      createCharge: async () => { throw new Error("unexpected charge"); },
      getBalance: async () => ({ availableCents: 0, currency: "USD" }),
      getDeposit: async () => ({ id: "dep", amountCents: 0, currency: "USD", status: "unknown", settledAt: null }),
      retrySandboxCard: async () => response,
    };
    await expect(dispatchOne(pool, gateway)).resolves.toBe(true);
    expect(pool.status).toBe("queued");
    await expect(dispatchOne(pool, gateway)).resolves.toBe(true);
    expect(createCount).toBe(1);
    expect(pool.status).toBe("done");
    expect(pool.sql.filter((sql) => sql.includes("INSERT INTO outbox_commands"))).toHaveLength(0);
  });

  it("does not create a second outbox operation after a command is completed", async () => {
    const pool = new OutboxPool();
    pool.status = "done";
    const gateway = { } as AirwallexGateway;
    await expect(dispatchOne(pool, gateway)).resolves.toBe(false);
  });

  it("records terminal FAILED and completes the command without resending or releasing its reservation", async () => {
    const pool = new OutboxPool();
    let createCount = 0;
    const gateway = {
      listConnectedAccounts: async () => [],
      getTransferByRequestId: async () => ({ id: "tr_failed", requestId: pool.requestId, status: "failed" as const }),
      getChargeByRequestId: async () => null,
      createTransfer: async () => { createCount++; throw new Error("must not resend terminal failure"); },
      createCharge: async () => { throw new Error("unexpected charge"); },
      getBalance: async () => ({ availableCents: 0, currency: "USD" as const }),
      getDeposit: async () => ({ id: "dep", amountCents: 0, currency: "USD" as const, status: "unknown" as const, settledAt: null }),
      retrySandboxCard: async () => ({ id: "auth", status: "unknown" as const }),
    } satisfies AirwallexGateway;
    await dispatchOne(pool, gateway);
    expect(pool.status).toBe("done");
    expect(pool.operationStatus).toBe("failed");
    expect(createCount).toBe(0);
    // Dispatcher records provider state only. Task 5 owns ledger observation and reservation release.
    expect(pool.sql.some((sql) => sql.includes("UPDATE bridge_cases") || sql.includes("ledger_events"))).toBe(false);
  });

  it.each([429, 503])("requeues HTTP %i with diagnostic context", async (status) => {
    const pool = new OutboxPool();
    const gateway = {
      listConnectedAccounts: async () => [],
      getTransferByRequestId: async () => { throw new ProviderHttpError(status, `Airwallex request failed (${status})`); },
    } as unknown as AirwallexGateway;
    await dispatchOne(pool, gateway);
    expect(pool.status).toBe("queued");
    expect(pool.lastError).toContain(`ProviderHttpError (HTTP ${status}): Airwallex request failed (${status})`);
    expect(pool.operationStatus).toBe("created");
  });

  it("marks permanent 4xx errors failed without retrying or inventing provider state", async () => {
    const pool = new OutboxPool();
    const gateway = {
      listConnectedAccounts: async () => [],
      getTransferByRequestId: async () => { throw new ProviderHttpError(422, "Invalid destination"); },
    } as unknown as AirwallexGateway;
    await dispatchOne(pool, gateway);
    expect(pool.status).toBe("failed");
    expect(pool.lastError).toContain("ProviderHttpError (HTTP 422): Invalid destination");
    expect(pool.operationStatus).toBe("created");
    expect(pool.sql.some((sql) => sql.includes("UPDATE bridge_cases") || sql.includes("ledger_events"))).toBe(false);
  });
});
