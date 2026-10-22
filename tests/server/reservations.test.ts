import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { reserveBridge } from "../../server/src/ledger/reservations.js";
import { getDemoSalesforceSnapshot } from "../../server/src/salesforce/fixture-source.js";
import type { SalesforceCustomerSnapshot } from "../../server/src/salesforce/types.js";

interface FakeState {
  deployable: number;
  activeHold: boolean;
  customerActive: boolean;
  snapshotFreshness: "fresh" | "missing" | "stale" | "invalid";
  evidenceDecisionAllowed: boolean;
  bridges: Array<{ id: string; customer_id: string; amount_cents: number; repayment_term?: string; repayment_due_at?: Date | null }>;
  events: Array<Record<string, unknown>>;
  outbox: unknown[];
  operationIds: Set<string>;
}

class ReservationPool implements DatabasePool {
  readonly state: FakeState = {
    deployable: 1_500_000,
    activeHold: false,
    customerActive: true,
    snapshotFreshness: "fresh",
    evidenceDecisionAllowed: true,
    bridges: [],
    events: [{
      id: 1,
      event_type: "opening_exposure",
      bridge_id: null,
      amount_cents: 2_500_000,
      currency: "USD",
      provenance: "demo_seed",
      source_id: "demo-seed-opening-exposure-v1",
      payload: { synthetic: true },
      occurred_at: new Date(0),
    }],
    outbox: [],
    operationIds: new Set(),
  };
  private lockHeld = false;
  private waiters: Array<() => void> = [];

  async query<Row>(): Promise<QueryResult<Row>> {
    throw new Error("Pool-level query is not used by reservation tests");
  }

  async connect(): Promise<DatabaseClient> {
    let ownsLock = false;
    const releaseLock = () => {
      if (!ownsLock) return;
      ownsLock = false;
      const next = this.waiters.shift();
      if (next) next();
      else {
        this.lockHeld = false;
      }
    };
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        const text = sql.replace(/\s+/g, " ").trim();
        if (text === "BEGIN" || text === "COMMIT" || text === "ROLLBACK") {
          if (text !== "BEGIN") releaseLock();
          return { rows: [], rowCount: 0 } as QueryResult<Row>;
        }
        if (text.includes("FROM facility_projections") && text.includes("FOR UPDATE")) {
          if (this.lockHeld) await new Promise<void>((resolve) => this.waiters.push(resolve));
          else this.lockHeld = true;
          ownsLock = true;
          return {
            rows: [{ deployable_cents: String(this.state.deployable) }],
            rowCount: 1,
          } as QueryResult<Row>;
        }
        if (text.includes("FROM facility_holds")) {
          return { rows: this.state.activeHold ? [{ id: "1" }] : [], rowCount: this.state.activeHold ? 1 : 0 } as QueryResult<Row>;
        }
        if (text.includes("FROM customer_roster AS roster")) {
          const snapshot = this.state.snapshotFreshness === "missing" ? null : reservationSnapshot(this.state.snapshotFreshness);
          return {
            rows: [{
              is_active: this.state.customerActive,
              snapshot_id: snapshot ? "snapshot-1" : null,
              version: snapshot ? 1 : null,
              source_mode: snapshot ? "demo" : null,
              schema_version: snapshot ? 1 : null,
              mapping_version: snapshot ? snapshot.mappingVersion : null,
              run_id: snapshot ? "run-1" : null,
              captured_at: new Date(Date.now() - 60_000),
              published_at: new Date(Date.now() - 59_000),
              canonical_payload: snapshot,
              attempt_run_id: this.state.snapshotFreshness === "stale" ? "failed-run" : "run-1",
              attempt_status: this.state.snapshotFreshness === "stale" ? "failed" : "succeeded",
              attempt_finished_at: new Date(),
            }],
            rowCount: 1,
          } as QueryResult<Row>;
        }
        if (text.includes("FROM ai_evidence_extractions")) {
          return { rows: [{ status: "success", decision_allowed: this.state.evidenceDecisionAllowed,
            decision_block_reason: "fixture evidence gate blocked" }], rowCount: 1 } as QueryResult<Row>;
        }
        if (text.includes("INSERT INTO bridge_cases")) {
          const [id, customerId, amountCents, repaymentTerm, repaymentDueAt] = values as [string, string, number, string, Date | null];
          this.state.bridges.push({ id, customer_id: customerId, amount_cents: amountCents, repayment_term: repaymentTerm, repayment_due_at: repaymentDueAt });
          return { rows: [], rowCount: 1 } as QueryResult<Row>;
        }
        if (text.includes("FROM bridge_cases") && text.includes("WHERE customer_id = $1")) {
          const matches = this.state.bridges.filter((bridge) => bridge.customer_id === values[0]);
          return { rows: matches.slice(0, 1), rowCount: matches.length ? 1 : 0 } as QueryResult<Row>;
        }
        if (text.includes("INSERT INTO provider_operations")) {
          this.state.operationIds.add(String(values[0]));
          return { rows: [], rowCount: 1 } as QueryResult<Row>;
        }
        if (text.includes("INSERT INTO ledger_events")) {
          const sourceId = String(values[4]);
          if (this.state.events.some((row) => row.source_id === sourceId)) {
            return { rows: [], rowCount: 0 } as QueryResult<Row>;
          }
          this.state.events.push({
            id: this.state.events.length + 1,
            event_type: values[0],
            bridge_id: values[1],
            amount_cents: values[2],
            currency: "USD",
            provenance: values[3],
            source_id: sourceId,
            payload: JSON.parse(String(values[5])),
            occurred_at: values[6],
          });
          return { rows: [{ id: "1" }], rowCount: 1 } as QueryResult<Row>;
        }
        if (text.includes("INSERT INTO outbox_commands")) {
          this.state.outbox.push(values);
          return { rows: [], rowCount: 1 } as QueryResult<Row>;
        }
        if (text.includes("FROM facility_configs")) {
          return {
            rows: [{ facility_amount_cents: "5000000", reserve_floor_bps: 2000 }],
            rowCount: 1,
          } as QueryResult<Row>;
        }
        if (text.includes("SELECT id, customer_id FROM bridge_cases")) {
          return { rows: this.state.bridges, rowCount: this.state.bridges.length } as QueryResult<Row>;
        }
        if (text.includes("FROM ledger_events")) {
          return { rows: this.state.events, rowCount: this.state.events.length } as QueryResult<Row>;
        }
        if (text.includes("INSERT INTO facility_projections")) {
          this.state.deployable = Number(values[5]);
          return { rows: [], rowCount: 1 } as QueryResult<Row>;
        }
        return { rows: [], rowCount: 1 } as QueryResult<Row>;
      },
      release: releaseLock,
    };
  }

  async end(): Promise<void> {}
}

describe("USD facility reservations", () => {
  beforeEach(() => vi.stubEnv("SALESFORCE_SNAPSHOT_MODE", "demo"));
  afterEach(() => vi.unstubAllEnvs());

  it("writes the reservation, provider operation, event, and outbox in one locked transaction", async () => {
    const pool = new ReservationPool();
    const result = await reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000001",
      customerId: "customer-1",
      amountCents: 1_000_000,
      repaymentTerm: "asap",
    });

    expect(result.requestId).toBe("bridge-00000000-0000-4000-8000-000000000001");
    expect(pool.state.bridges).toHaveLength(1);
    expect(pool.state.operationIds).toContain(result.providerOperationId);
    expect(pool.state.events).toHaveLength(2);
    expect(pool.state.outbox).toHaveLength(1);
    expect(pool.state.deployable).toBe(500_000);
  });

  it("stores the selected fixed repayment date and requires one only for that term", async () => {
    const pool = new ReservationPool();
    const repaymentDueAt = new Date("2026-11-10T00:00:00.000Z");
    await reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000041",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "fixed_date",
      repaymentDueAt,
    });
    expect(pool.state.bridges[0]).toMatchObject({ repayment_term: "fixed_date", repayment_due_at: repaymentDueAt });

    await expect(reserveBridge(new ReservationPool(), {
      bridgeId: "00000000-0000-4000-8000-000000000042",
      customerId: "customer-2",
      amountCents: 250_000,
      repaymentTerm: "fixed_date",
    })).rejects.toThrow("repaymentDueAt is required");
    await expect(reserveBridge(new ReservationPool(), {
      bridgeId: "00000000-0000-4000-8000-000000000043",
      customerId: "customer-3",
      amountCents: 250_000,
      repaymentTerm: "asap",
      repaymentDueAt,
    })).rejects.toThrow("only valid for the fixed_date term");
  });

  it("serializes competing reservations so total active reservations stay in capacity", async () => {
    const pool = new ReservationPool();
    const requests = [
      reserveBridge(pool, {
        bridgeId: "00000000-0000-4000-8000-000000000011",
        customerId: "customer-1",
        amountCents: 1_000_000,
        repaymentTerm: "asap",
      }),
      reserveBridge(pool, {
        bridgeId: "00000000-0000-4000-8000-000000000012",
        customerId: "customer-2",
        amountCents: 1_000_000,
        repaymentTerm: "asap",
      }),
    ];
    const results = await Promise.allSettled(requests);

    expect(results.filter((result) => result.status === "fulfilled")).toHaveLength(1);
    expect(results.filter((result) => result.status === "rejected")).toHaveLength(1);
    expect(pool.state.bridges).toHaveLength(1);
    expect(pool.state.deployable).toBe(500_000);
  });

  it("rejects a second open bridge for one customer without consuming capacity", async () => {
    const pool = new ReservationPool();
    await reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000021",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "asap",
    });

    await expect(reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000022",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "asap",
    })).rejects.toThrow();
    expect(pool.state.bridges).toHaveLength(1);
    expect(pool.state.events).toHaveLength(2);
    expect(pool.state.outbox).toHaveLength(1);
    expect(pool.state.deployable).toBe(1_250_000);
  });

  it("blocks new bridge reservations while the facility has an active reconciliation hold", async () => {
    const pool = new ReservationPool();
    pool.state.activeHold = true;

    await expect(reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000031",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "asap",
    })).rejects.toThrow("facility is on hold");
    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.events).toHaveLength(1);
  });

  it("blocks new bridge reservations for a soft-deactivated customer", async () => {
    const pool = new ReservationPool();
    pool.state.customerActive = false;

    await expect(reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000051",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "asap",
    })).rejects.toThrow("not actively enrolled");
    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.events).toHaveLength(1);
  });

  it.each(["missing", "stale", "invalid"] as const)("blocks new bridge reservations for a %s snapshot", async (snapshotFreshness) => {
    const pool = new ReservationPool();
    pool.state.snapshotFreshness = snapshotFreshness;

    await expect(reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000061",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "asap",
    })).rejects.toThrow(`Salesforce snapshot is ${snapshotFreshness}`);
    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.events).toHaveLength(1);
    expect(pool.state.outbox).toHaveLength(0);
  });

  it("rejects a successful extraction without an allowed decision gate", async () => {
    const pool = new ReservationPool();
    pool.state.evidenceDecisionAllowed = false;
    await expect(reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000081",
      customerId: "customer-1", amountCents: 250_000, repaymentTerm: "asap",
    })).rejects.toThrow("fixture evidence gate blocked");
    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.outbox).toHaveLength(0);
  });

  it("blocks a fresh demo snapshot when the runtime is configured for production MCP", async () => {
    vi.stubEnv("SALESFORCE_SNAPSHOT_MODE", "production_mcp");

    await expect(reserveBridge(new ReservationPool(), {
      bridgeId: "00000000-0000-4000-8000-000000000071",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "asap",
    })).rejects.toThrow("snapshot source does not match runtime mode production_mcp");
  });

  it("fails closed when the runtime snapshot mode is missing", async () => {
    vi.stubEnv("SALESFORCE_SNAPSHOT_MODE", "");

    await expect(reserveBridge(new ReservationPool(), {
      bridgeId: "00000000-0000-4000-8000-000000000072",
      customerId: "customer-1",
      amountCents: 250_000,
      repaymentTerm: "asap",
    })).rejects.toThrow("Salesforce snapshot mode is not configured");
  });
});

function reservationSnapshot(freshness: FakeState["snapshotFreshness"]): SalesforceCustomerSnapshot {
  const snapshot = structuredClone(getDemoSalesforceSnapshot("acct_bloom001")!);
  if (freshness === "invalid") snapshot.account.approvedBridgeCap.value = null;
  return snapshot;
}
