import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { reservePolicyBridge, type PolicyReservationInput } from "../../../server/src/ledger/reservations.js";
import type { RequestDecision } from "../../../server/src/policy/decision-service.js";
import type { StoredDailyPrecheck } from "../../../server/src/policy/repository.js";
import { getDemoSalesforceSnapshot } from "../../../server/src/salesforce/fixture-source.js";
import type { SalesforceCustomerSnapshot } from "../../../server/src/salesforce/types.js";

interface State {
  deployable: number;
  bridges: Array<{ id: string; customer_id: string; amount_cents: number }>;
  events: Array<Record<string, unknown>>;
  outbox: unknown[];
  evaluations: unknown[];
  operations: Array<{ id: string; bridge_id: string }>;
  requestDecisions: Map<string, readonly unknown[]>;
  failOutbox: boolean;
  evidenceRunId: string;
  evidenceAllowed: boolean;
}

class PolicyReservationPool implements DatabasePool {
  readonly state: State = {
    deployable: 1_500_000,
    bridges: [],
    events: [{ id: 1, event_type: "opening_exposure", bridge_id: null, amount_cents: 2_500_000, currency: "USD", provenance: "fixture", source_id: "opening", payload: {}, occurred_at: new Date(0) }],
    outbox: [],
    evaluations: [],
    operations: [],
    requestDecisions: new Map(),
    failOutbox: false,
    evidenceRunId: "evidence-1",
    evidenceAllowed: true,
  };
  private transaction: State | null = null;
  private locked = false;
  private waiters: Array<() => void> = [];

  async query<Row>(): Promise<QueryResult<Row>> {
    throw new Error("Pool-level query is unused");
  }

  async connect(): Promise<DatabaseClient> {
    let ownsLock = false;
    const releaseLock = () => {
      if (!ownsLock) return;
      ownsLock = false;
      const next = this.waiters.shift();
      if (next) next();
      else this.locked = false;
    };
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        const text = sql.replace(/\s+/g, " ").trim();
        const state = this.transaction ?? this.state;
        if (text === "BEGIN") {
          this.transaction = structuredClone(this.state);
          return empty<Row>();
        }
        if (text === "COMMIT") {
          Object.assign(this.state, this.transaction);
          this.transaction = null;
          releaseLock();
          return empty<Row>();
        }
        if (text === "ROLLBACK") {
          this.transaction = null;
          releaseLock();
          return empty<Row>();
        }
        if (text.includes("FROM facility_projections") && text.includes("FOR UPDATE")) {
          if (this.locked) await new Promise<void>((resolve) => this.waiters.push(resolve));
          else this.locked = true;
          ownsLock = true;
          this.transaction = structuredClone(this.state);
          return rows<Row>([{ deployable_cents: this.transaction.deployable }]);
        }
        if (text.includes("FROM facility_holds")) return empty<Row>();
        if (text.includes("FROM customer_roster AS roster")) {
          const snapshot = demoSnapshot();
          return rows<Row>([{
            is_active: true,
            snapshot_id: "snapshot-1",
            version: 1,
            source_mode: "demo",
            schema_version: 1,
            mapping_version: snapshot.mappingVersion,
            run_id: "run-1",
            captured_at: new Date(Date.now() - 60_000),
            published_at: new Date(Date.now() - 59_000),
            canonical_payload: snapshot,
            attempt_run_id: "run-1",
            attempt_status: "succeeded",
            attempt_finished_at: new Date(),
          }]);
        }
        if (text.includes("FROM ai_evidence_extractions")) {
          return rows<Row>([{ extraction_id: state.evidenceRunId, status: "success", decision_allowed: state.evidenceAllowed, decision_block_reason: "fixture blocked" }]);
        }
        if (text.includes("FROM bridge_cases AS bridge") && text.includes("JOIN provider_operations")) {
          const bridge = state.bridges.find((item) => item.id === values[0]);
          const operation = state.operations.find((item) => item.bridge_id === String(values[0]));
          return bridge && operation ? rows<Row>([{
            customer_id: bridge.customer_id,
            amount_cents: bridge.amount_cents,
            bridge_request_id: `bridge-${bridge.id}`,
            provider_operation_id: operation.id,
          }]) : empty<Row>();
        }
        if (text.includes("FROM policy_request_evaluations WHERE decision_id")) {
          const row = state.requestDecisions.get(String(values[0]));
          return row ? rows<Row>([{
            evaluation_id: row[1], request_id: row[2], customer_id: row[3], outcome: row[4], amount_cents: row[5],
            credit_line_cents: row[6], tier: row[7], capital_band: row[8], door: row[9],
            reason_codes: JSON.parse(String(row[10])), rationale: row[11], actor_id: row[12], approval_reason: row[13],
            ledger_sequence: row[14],
          }]) : empty<Row>();
        }
        if (text.includes("INSERT INTO policy_evaluations")) {
          state.evaluations.push({ kind: "daily", values });
          return empty<Row>();
        }
        if (text.includes("SELECT evaluation_id FROM policy_evaluations")) {
          return rows<Row>([{ evaluation_id: "evaluation-1" }]);
        }
        if (text.includes("INSERT INTO policy_request_evaluations")) {
          state.evaluations.push({ kind: "request", values });
          state.requestDecisions.set(String(values[0]), values);
          return empty<Row>();
        }
        if (text.includes("FROM bridge_cases") && text.includes("WHERE customer_id = $1")) {
          const matches = state.bridges.filter((bridge) => bridge.customer_id === values[0]);
          return rows<Row>(matches.slice(0, 1));
        }
        if (text.includes("INSERT INTO bridge_cases")) {
          state.bridges.push({ id: String(values[0]), customer_id: String(values[1]), amount_cents: Number(values[2]) });
          return empty<Row>();
        }
        if (text.includes("INSERT INTO ledger_events")) {
          state.events.push({ event_type: values[0], bridge_id: values[1], amount_cents: values[2], source_id: values[4] });
          return rows<Row>([{ id: "1" }]);
        }
        if (text.includes("INSERT INTO provider_operations")) {
          state.operations.push({ id: String(values[0]), bridge_id: String(values[1]) });
          return empty<Row>();
        }
        if (text.includes("INSERT INTO outbox_commands")) {
          if (state.failOutbox) throw new Error("outbox insert failed");
          state.outbox.push(values);
          return empty<Row>();
        }
        if (text.includes("FROM facility_configs")) return rows<Row>([{ facility_amount_cents: 5_000_000, reserve_floor_bps: 2000 }]);
        if (text.includes("SELECT id, customer_id FROM bridge_cases")) return rows<Row>(state.bridges);
        if (text.includes("FROM ledger_events")) return rows<Row>(state.events);
        if (text.includes("INSERT INTO facility_projections")) {
          state.deployable = Number(values[5]);
          return empty<Row>();
        }
        return empty<Row>();
      },
      release: releaseLock,
    };
  }

  async end(): Promise<void> {}
}

const readyPrecheck = (evidenceRunId = "evidence-1", customerId = "customer-1"): StoredDailyPrecheck => ({
  customerId,
  policyVersion: "policy-v1",
  profileId: "profile-1",
  snapshotId: "snapshot-1",
  evidenceRunId,
  ratings: null,
  tier: "A",
  creditLineCents: 1_000_000,
  outcome: "ready",
  humanApprovalRequired: false,
  reasonCodes: [],
  inputVersions: {},
  mappingVersions: {},
  rosterUpdatedAt: new Date("2026-10-22T00:00:00.000Z"),
  evaluatedAt: new Date("2026-10-22T12:00:00.000Z"),
});

const decision = (outcome: RequestDecision["outcome"], amountCents: number, customerId = "customer-1"): RequestDecision => ({
  requestId: "request-1",
  customerId,
  outcome,
  amountCents,
  creditLineCents: 1_000_000,
  tier: "A",
  capitalBand: "a_lot",
  door: "auto",
  reasonCodes: [outcome === "auto_approved" ? "AUTO_APPROVED" : "PRECHECK_BLOCKED"],
  rationale: "fixture decision",
});

function input(
  revalidate: PolicyReservationInput["revalidate"],
  overrides: Partial<PolicyReservationInput> = {},
): PolicyReservationInput {
  return {
    bridgeId: "00000000-0000-4000-8000-000000000101",
    customerId: "customer-1",
    amountCents: 1_000_000,
    repaymentTerm: "asap",
    decisionId: "00000000-0000-4000-8000-000000000102",
    requestId: "request-1",
    currentBalance: { availableCents: 0, currency: "USD" },
    revalidate,
    ...overrides,
  };
}

describe("transactional policy reservation", () => {
  beforeEach(() => vi.stubEnv("SALESFORCE_SNAPSHOT_MODE", "demo"));
  afterEach(() => vi.unstubAllEnvs());

  it("records a blocked evaluation without creating a reservation or outbox command", async () => {
    const pool = new PolicyReservationPool();
    const result = await reservePolicyBridge(pool, input(async () => ({
      decision: decision("blocked", 0),
      precheck: readyPrecheck(),
    })));

    expect(result.reservation).toBeNull();
    expect(pool.state.evaluations).toHaveLength(2);
    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.outbox).toHaveLength(0);
  });

  it("rejects reservation when successful evidence has no allowed decision gate", async () => {
    const pool = new PolicyReservationPool();
    pool.state.evidenceAllowed = false;
    await expect(reservePolicyBridge(pool, input(async () => ({
      decision: decision("auto_approved", 1_000_000),
      precheck: readyPrecheck(),
    })))).rejects.toThrow("fixture blocked");
    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.outbox).toHaveLength(0);
  });

  it("rolls back evaluation and reservation when outbox creation fails", async () => {
    const pool = new PolicyReservationPool();
    pool.state.failOutbox = true;
    await expect(reservePolicyBridge(pool, input(async () => ({
      decision: decision("auto_approved", 1_000_000),
      precheck: readyPrecheck(),
    })))).rejects.toThrow("outbox insert failed");

    expect(pool.state.evaluations).toHaveLength(0);
    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.events).toHaveLength(1);
    expect(pool.state.outbox).toHaveLength(0);
  });

  it("returns the original reservation for an idempotent retry", async () => {
    const pool = new PolicyReservationPool();
    const request = input(async () => ({
      decision: decision("auto_approved", 1_000_000),
      precheck: readyPrecheck(),
    }));
    const first = await reservePolicyBridge(pool, request);
    const retry = await reservePolicyBridge(pool, request);

    expect(retry).toEqual(first);
    expect(pool.state.bridges).toHaveLength(1);
    expect(pool.state.outbox).toHaveLength(1);
  });

  it("rechecks open-bridge uniqueness and returns a decline without an outbox command", async () => {
    const pool = new PolicyReservationPool();
    const initial = input(async () => ({
      decision: decision("auto_approved", 1_000_000),
      precheck: readyPrecheck(),
    }));
    await reservePolicyBridge(pool, initial);
    const next = input(async ({ openBridgeCount, requestId }) => ({
      decision: { ...decision(openBridgeCount > 0 ? "declined" : "auto_approved", openBridgeCount > 0 ? 0 : 1_000_000), requestId },
      precheck: readyPrecheck(),
    }), {
      bridgeId: "00000000-0000-4000-8000-000000000105",
      decisionId: "00000000-0000-4000-8000-000000000106",
      currentBalance: { availableCents: 0, currency: "USD" },
      requestId: "request-2",
    });
    const result = await reservePolicyBridge(pool, next);

    expect(result.decision.outcome).toBe("declined");
    expect(result.reservation).toBeNull();
    expect(pool.state.bridges).toHaveLength(1);
    expect(pool.state.outbox).toHaveLength(1);
  });

  it("serializes simultaneous requests so only the one within current capacity reserves", async () => {
    const pool = new PolicyReservationPool();
    const revalidate: PolicyReservationInput["revalidate"] = async ({ deployableCents, requestId, customerId }) => ({
      decision: { ...decision(deployableCents >= 1_000_000 ? "auto_approved" : "declined", deployableCents >= 1_000_000 ? 1_000_000 : 0, customerId), requestId },
      precheck: readyPrecheck("evidence-1", customerId),
    });
    const first = input(revalidate);
    const second = input(revalidate, {
      bridgeId: "00000000-0000-4000-8000-000000000103",
      decisionId: "00000000-0000-4000-8000-000000000104",
      requestId: "request-2",
      customerId: "customer-2",
    });
    const results = await Promise.all([
      reservePolicyBridge(pool, first), reservePolicyBridge(pool, second),
    ]);

    expect(results.filter(({ reservation }) => reservation !== null)).toHaveLength(1);
    expect(pool.state.bridges).toHaveLength(1);
    expect(pool.state.outbox).toHaveLength(1);
    expect(pool.state.deployable).toBe(500_000);
  });

  it("rejects approval when evidence changed after the original evaluation", async () => {
    const pool = new PolicyReservationPool();
    pool.state.evidenceRunId = "evidence-new";
    await expect(reservePolicyBridge(pool, input(async ({ evidenceRunId }) => ({
      decision: decision("auto_approved", 1_000_000),
      precheck: readyPrecheck(evidenceRunId === "evidence-1" ? evidenceRunId : "evidence-old"),
    }), {
      humanApproval: { requestId: "request-1", amountCents: 1_000_000, actorId: "operator-1", reason: "Reviewed" },
    }))).rejects.toThrow("do not match current snapshot and evidence versions");

    expect(pool.state.bridges).toHaveLength(0);
    expect(pool.state.outbox).toHaveLength(0);
  });
});

function demoSnapshot(): SalesforceCustomerSnapshot {
  return structuredClone(getDemoSalesforceSnapshot("acct_bloom001")!);
}

function empty<Row>(): QueryResult<Row> { return { rows: [], rowCount: 0 }; }
function rows<Row>(items: unknown[]): QueryResult<Row> { return { rows: items as Row[], rowCount: items.length }; }
