import { describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { reserveBridge } from "../../server/src/ledger/reservations.js";
import type { CustomerRosterEntry } from "../../server/src/roster/types.js";
import { SALESFORCE_FIELDS } from "../../server/src/salesforce/field-map.js";
import { getDemoSalesforceSnapshot } from "../../server/src/salesforce/fixture-source.js";
import { requireSalesforceMcpProductionReadiness, type SalesforceMcpConfig } from "../../server/src/salesforce/mcp-config.js";
import type { SalesforceMcpClient, SalesforceMcpPage, SalesforceMcpReadRequest } from "../../server/src/salesforce/mcp-source.js";
import type { SalesforceCustomerSnapshot } from "../../server/src/salesforce/types.js";
import { getCustomerSnapshotReadModel } from "../../server/src/snapshots/freshness.js";
import { getCurrentSnapshot } from "../../server/src/snapshots/repository.js";
import { createDemoSnapshotSource, createMcpSnapshotSource, SnapshotSyncWorker } from "../../server/src/snapshots/sync-worker.js";

const baseTime = new Date("2026-10-21T12:00:00.000Z");
const textLimits = { maxCaseTextLength: 200, maxActivityTextLength: 200 };
const salesforceConfig: SalesforceMcpConfig = {
  serverName: "salesforce",
  queryToolName: "query",
  maxAttempts: 3,
  retryDelayMs: 0,
};

describe("Salesforce snapshot acceptance integration", () => {
  it("selects active roster customers, publishes complete snapshots independently, reports freshness, and blocks stale bridges", async () => {
    const pool = new AcceptancePool([
      roster("acct_atlas001"),
      roster("acct_bloom001"),
      roster("acct_pixel001", false),
    ]);
    const first = new SnapshotSyncWorker({
      pool,
      source: createDemoSnapshotSource(),
      textLimits,
      now: () => baseTime,
      runId: () => "run-1",
    });

    const firstRun = await first.run();

    expect(firstRun.customers.map(({ customerId, status }) => [customerId, status])).toEqual([
      ["acct_atlas001", "published"],
      ["acct_bloom001", "published"],
    ]);
    expect(pool.rosterQueries[0]).toContain("WHERE is_active");
    expect(await getCurrentSnapshot(pool, "acct_bloom001")).toMatchObject({
      version: 1,
      sourceMode: "demo",
      queryCompletion: {
        account: { complete: true, pages: 1 },
        contract: { complete: true, pages: 1 },
        case: { complete: true, pages: 1 },
        activity: { complete: true, pages: 1 },
      },
    });
    expect((await getCustomerSnapshotReadModel(pool, "acct_atlas001", baseTime)).freshness).toBe("fresh");

    const later = new Date(baseTime.getTime() + 60 * 60 * 1000);
    const refresh = new SnapshotSyncWorker({
      pool,
      source: {
        mode: "demo",
        async fetchCustomer(customer) {
          if (customer.customerId === "acct_bloom001") {
            throw new Error("required Case query failed after retries");
          }
          const payload = getDemoSalesforceSnapshot(customer.customerId);
          if (!payload) throw new Error("missing synthetic fixture");
          return { kind: "fixture", payload };
        },
      },
      textLimits,
      now: () => later,
      runId: () => "run-2",
    });

    const refreshed = await refresh.run();

    expect(refreshed.customers).toEqual([
      { customerId: "acct_atlas001", status: "published" },
      { customerId: "acct_bloom001", status: "failed", error: "required Case query failed after retries" },
    ]);
    expect(await getCurrentSnapshot(pool, "acct_bloom001")).toMatchObject({ version: 1, runId: "run-1" });
    expect(await getCurrentSnapshot(pool, "acct_atlas001")).toMatchObject({ version: 2, runId: "run-2" });
    expect(await getCustomerSnapshotReadModel(pool, "acct_atlas001", later)).toMatchObject({ freshness: "fresh" });
    expect(await getCustomerSnapshotReadModel(pool, "acct_bloom001", later)).toMatchObject({
      freshness: "stale",
      latestAttempt: { status: "failed" },
    });

    await expect(reserveBridge(pool, {
      bridgeId: "00000000-0000-4000-8000-000000000010",
      customerId: "acct_bloom001",
      amountCents: 10_000,
      repaymentTerm: "asap",
    })).rejects.toThrow("Salesforce snapshot is stale");
    expect(pool.transactions).toContain("ROLLBACK");
  });

  it("waits for every production MCP page, keeps the source MCP-only, and fails closed on readiness or MCP errors", async () => {
    const pool = new AcceptancePool([roster("acct_atlas001")]);
    const requests: SalesforceMcpReadRequest[] = [];
    const client: SalesforceMcpClient = {
      async read(request) {
        requests.push(request);
        return mcpPage(request, requests.filter((item) => item.object === request.object).length);
      },
    };
    const worker = new SnapshotSyncWorker({
      pool,
      source: createMcpSnapshotSource(client, salesforceConfig, async () => {}),
      textLimits,
      now: () => baseTime,
      runId: () => "mcp-run",
    });

    const result = await worker.run();

    expect(result.customers).toEqual([{ customerId: "acct_atlas001", status: "published" }]);
    expect(requests.some(({ object, cursor }) => object === "Case" && cursor === "case-page-2")).toBe(true);
    expect((await getCurrentSnapshot(pool, "acct_atlas001"))?.queryCompletion).toMatchObject({
      account: { complete: true, pages: 1 },
      contract: { complete: true, pages: 1 },
      case: { complete: true, pages: 2 },
      activity: { complete: true, pages: 1 },
    });
    expect((await getCustomerSnapshotReadModel(pool, "acct_atlas001", baseTime)).freshness).toBe("invalid");
    expect(Object.keys(createMcpSnapshotSource(client, salesforceConfig))).toEqual(["mode", "fetchCustomer"]);
    expect(() => requireSalesforceMcpProductionReadiness({
      SALESFORCE_MCP_SERVER_NAME: "salesforce",
      SALESFORCE_MCP_QUERY_TOOL_NAME: "query",
      SALESFORCE_MCP_CAPABILITIES_VERIFIED: "true",
      SALESFORCE_MAX_CASE_TEXT_LENGTH: "200",
      SALESFORCE_MAX_ACTIVITY_TEXT_LENGTH: "200",
    })).toThrow("org verification is required");

    const failedPool = new AcceptancePool([roster("acct_atlas001")]);
    const failingRead = vi.fn(async () => { throw new Error("MCP query unavailable"); });
    const failed = await new SnapshotSyncWorker({
      pool: failedPool,
      source: createMcpSnapshotSource({ read: failingRead }, salesforceConfig, async () => {}),
      textLimits,
      now: () => baseTime,
      runId: () => "mcp-fail-run",
    }).run();
    expect(failed.customers[0]?.status).toBe("failed");
    expect(failingRead).toHaveBeenCalledTimes(1);
    expect(failedPool.state.versions.size).toBe(0);
    expect(failedPool.state.attempts.get("acct_atlas001")?.status).toBe("failed");
  });

  it("rolls back a partially written customer's publication while allowing the next customer to publish", async () => {
    const pool = new AcceptancePool([roster("acct_atlas001"), roster("acct_bloom001")]);
    const source = createDemoSnapshotSource();
    const initial = await new SnapshotSyncWorker({ pool, source, textLimits, now: () => baseTime, runId: () => "seed-run" }).run();
    expect(initial.customers.every(({ status }) => status === "published")).toBe(true);
    const beforeAtlas = await getCurrentSnapshot(pool, "acct_atlas001");
    pool.failSuccessfulAttemptRunId = "atomic-run";
    pool.failSuccessfulAttemptCustomerId = "acct_atlas001";

    const result = await new SnapshotSyncWorker({
      pool,
      source,
      textLimits,
      now: () => new Date(baseTime.getTime() + 1_000),
      runId: () => "atomic-run",
    }).run();

    expect(result.customers.map(({ customerId, status }) => [customerId, status])).toEqual([
      ["acct_atlas001", "failed"],
      ["acct_bloom001", "published"],
    ]);
    expect(await getCurrentSnapshot(pool, "acct_atlas001")).toMatchObject({
      snapshotId: beforeAtlas?.snapshotId,
      version: 1,
      runId: "seed-run",
    });
    expect(pool.state.versions.get("acct_atlas001")).toHaveLength(1);
    expect(await getCurrentSnapshot(pool, "acct_bloom001")).toMatchObject({ version: 2, runId: "atomic-run" });
    expect(pool.transactions).toContain("ROLLBACK");
  });
});

class AcceptancePool implements DatabasePool {
  readonly state: AcceptanceState = {
    roster: new Map(),
    versions: new Map(),
    current: new Map(),
    attempts: new Map(),
  };
  readonly rosterQueries: string[] = [];
  readonly transactions: string[] = [];
  failSuccessfulAttemptRunId: string | null = null;
  failSuccessfulAttemptCustomerId: string | null = null;
  private lockHeld = false;

  constructor(entries: CustomerRosterEntry[]) {
    for (const entry of entries) this.state.roster.set(entry.customerId, entry);
  }

  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    const normalized = normalize(sql);
    if (normalized.includes("FROM customer_roster WHERE is_active")) {
      this.rosterQueries.push(normalized);
      const rows = [...this.state.roster.values()]
        .filter(({ isActive }) => isActive)
        .sort((left, right) => left.customerId.localeCompare(right.customerId))
        .map(rosterRow) as Row[];
      return { rows, rowCount: rows.length };
    }
    if (normalized.startsWith("SELECT version.snapshot_id")) {
      const customerId = String(values[0]);
      const snapshotId = this.state.current.get(customerId);
      const row = this.state.versions.get(customerId)?.find(({ snapshot_id }) => snapshot_id === snapshotId);
      return { rows: row ? [row as Row] : [], rowCount: row ? 1 : 0 };
    }
    if (normalized.startsWith("SELECT roster.is_active")) {
      const customerId = String(values[0]);
      const entry = this.state.roster.get(customerId);
      if (!entry) return { rows: [], rowCount: 0 };
      const currentId = this.state.current.get(customerId);
      const snapshot = currentId
        ? this.state.versions.get(customerId)?.find(({ snapshot_id }) => snapshot_id === currentId)
        : undefined;
      const attempt = this.state.attempts.get(customerId);
      const row = {
        is_active: entry.isActive,
        snapshot_id: snapshot?.snapshot_id ?? null,
        version: snapshot?.version ?? null,
        source_mode: snapshot?.source_mode ?? null,
        schema_version: snapshot?.schema_version ?? null,
        mapping_version: snapshot?.mapping_version ?? null,
        run_id: snapshot?.run_id ?? null,
        captured_at: snapshot?.captured_at ?? null,
        published_at: snapshot?.published_at ?? null,
        canonical_payload: snapshot?.canonical_payload ?? null,
        attempt_run_id: attempt?.run_id ?? null,
        attempt_status: attempt?.status ?? null,
        attempt_finished_at: attempt?.finished_at ?? null,
      } as Row;
      return { rows: [row], rowCount: 1 };
    }
    if (normalized.startsWith("INSERT INTO customer_snapshot_attempts")) {
      const customerId = String(values[0]);
      const runId = String(values[1]);
      this.state.attempts.set(customerId, {
        run_id: runId,
        status: "failed",
        finished_at: values[6] as Date,
      });
      return { rows: [], rowCount: 1 };
    }
    return { rows: [], rowCount: 1 };
  }

  async connect(): Promise<DatabaseClient> {
    let transaction: AcceptanceState | null = null;
    return {
      query: async <Row>(sql: string, values: readonly unknown[] = []) => {
        const normalized = normalize(sql);
        this.transactions.push(normalized);
        if (normalized === "BEGIN") {
          transaction = cloneState(this.state);
          return emptyResult<Row>();
        }
        if (normalized === "COMMIT") {
          if (transaction) this.replaceState(transaction);
          transaction = null;
          this.releaseLock();
          return emptyResult<Row>();
        }
        if (normalized === "ROLLBACK") {
          transaction = null;
          this.releaseLock();
          return emptyResult<Row>();
        }
        if (normalized.includes("FROM facility_projections") && normalized.includes("FOR UPDATE")) {
          this.lockHeld = true;
          return { rows: [{ deployable_cents: "1500000" } as Row], rowCount: 1 };
        }
        if (normalized.includes("FROM facility_holds")) return { rows: [], rowCount: 0 };
        if (!transaction) throw new Error(`Query outside transaction: ${normalized}`);
        if (normalized.startsWith("SELECT customer_id FROM customer_roster")) {
          const customerId = String(values[0]);
          const rows = transaction.roster.has(customerId) ? [{ customer_id: customerId } as Row] : [];
          return { rows, rowCount: rows.length };
        }
        if (normalized.startsWith("SELECT COALESCE(MAX(version)")) {
          const rows = transaction.versions.get(String(values[0])) ?? [];
          return { rows: [{ next_version: rows.length + 1 } as Row], rowCount: 1 };
        }
        if (normalized.startsWith("SELECT roster.is_active")) return this.query<Row>(sql, values);
        if (normalized.startsWith("INSERT INTO customer_snapshot_versions")) {
          const customerId = String(values[1]);
          const versions = transaction.versions.get(customerId) ?? [];
          versions.push({
            snapshot_id: String(values[0]),
            customer_id: customerId,
            version: Number(values[2]),
            source_mode: values[3] as "demo" | "production_mcp",
            schema_version: Number(values[4]),
            mapping_version: String(values[5]),
            run_id: String(values[6]),
            sync_started_at: values[7] as Date,
            captured_at: values[8] as Date,
            published_at: baseTime,
            query_completion: JSON.parse(String(values[9])) as AcceptanceVersion["query_completion"],
            record_counts: JSON.parse(String(values[10])) as AcceptanceVersion["record_counts"],
            canonical_payload: JSON.parse(String(values[11])) as SalesforceCustomerSnapshot,
          });
          transaction.versions.set(customerId, versions);
          return emptyResult<Row>();
        }
        if (normalized.startsWith("INSERT INTO customer_snapshot_current")) {
          transaction.current.set(String(values[0]), String(values[1]));
          return emptyResult<Row>();
        }
        if (normalized.startsWith("INSERT INTO customer_snapshot_attempts")) {
          const customerId = String(values[0]);
          const runId = String(values[1]);
          if (runId === this.failSuccessfulAttemptRunId && customerId === this.failSuccessfulAttemptCustomerId) {
            throw new Error("injected publication attempt failure");
          }
          transaction.attempts.set(customerId, {
            run_id: runId,
            status: "succeeded",
            finished_at: baseTime,
          });
          return emptyResult<Row>();
        }
        if (normalized.startsWith("SELECT id FROM bridge_cases")) return { rows: [], rowCount: 0 };
        throw new Error(`Unexpected SQL: ${normalized}`);
      },
      release: () => this.releaseLock(),
    };
  }

  async end(): Promise<void> {}

  private replaceState(next: AcceptanceState): void {
    this.state.roster = next.roster;
    this.state.versions = next.versions;
    this.state.current = next.current;
    this.state.attempts = next.attempts;
  }

  private releaseLock(): void {
    if (this.lockHeld) this.lockHeld = false;
  }
}

interface AcceptanceVersion {
  snapshot_id: string;
  customer_id: string;
  version: number;
  source_mode: "demo" | "production_mcp";
  schema_version: number;
  mapping_version: string;
  run_id: string;
  sync_started_at: Date;
  captured_at: Date;
  published_at: Date;
  query_completion: {
    account: { complete: boolean; pages: number };
    contract: { complete: boolean; pages: number };
    case: { complete: boolean; pages: number };
    activity: { complete: boolean; pages: number };
  };
  record_counts: Record<string, number>;
  canonical_payload: SalesforceCustomerSnapshot;
}

interface AcceptanceState {
  roster: Map<string, CustomerRosterEntry>;
  versions: Map<string, AcceptanceVersion[]>;
  current: Map<string, string>;
  attempts: Map<string, { run_id: string; status: "succeeded" | "failed"; finished_at: Date }>;
}

function roster(customerId: string, isActive = true): CustomerRosterEntry {
  return {
    customerId,
    salesforceAccountId: `sf-${customerId}`,
    airwallexConnectedAccountId: `awx-${customerId}`,
    isActive,
    enrolledAt: baseTime,
    enrolledBy: "test-operator",
    enrollmentReason: "acceptance integration fixture",
    deactivatedAt: null,
    deactivatedBy: null,
    deactivationReason: null,
    updatedAt: baseTime,
  };
}

function rosterRow(entry: CustomerRosterEntry) {
  return {
    customer_id: entry.customerId,
    salesforce_account_id: entry.salesforceAccountId,
    airwallex_connected_account_id: entry.airwallexConnectedAccountId,
    is_active: entry.isActive,
    enrolled_at: entry.enrolledAt,
    enrolled_by: entry.enrolledBy,
    enrollment_reason: entry.enrollmentReason,
    deactivated_at: entry.deactivatedAt,
    deactivated_by: entry.deactivatedBy,
    deactivation_reason: entry.deactivationReason,
    updated_at: entry.updatedAt,
  };
}

function cloneState(state: AcceptanceState): AcceptanceState {
  return {
    roster: new Map(state.roster),
    versions: new Map([...state.versions].map(([customerId, rows]) => [customerId, [...rows]])),
    current: new Map(state.current),
    attempts: new Map(state.attempts),
  };
}

function emptyResult<Row>(): QueryResult<Row> {
  return { rows: [], rowCount: 1 };
}

function normalize(sql: string): string {
  return sql.replace(/\s+/g, " ").trim();
}

function mcpPage(request: SalesforceMcpReadRequest, pageCount: number): SalesforceMcpPage {
  if (request.object === "Account") {
    return {
      records: [{
        Id: "sf-atlas",
        [SALESFORCE_FIELDS.account.updatedAt]: baseTime.toISOString(),
        [SALESFORCE_FIELDS.account.customerStatus]: "Active",
        [SALESFORCE_FIELDS.account.kybStatus]: "Verified",
        [SALESFORCE_FIELDS.account.creditReviewStatus]: "Approved",
        [SALESFORCE_FIELDS.account.approvedBridgeCap]: 10_000,
        [SALESFORCE_FIELDS.account.priorDefaultStatus]: "No",
        [SALESFORCE_FIELDS.account.loyaltyTier]: "Standard",
      }],
      nextCursor: null,
      complete: true,
    };
  }
  if (request.object === "Contract") {
    return {
      records: [{
        Id: "sf-contract",
        [SALESFORCE_FIELDS.contract.updatedAt]: baseTime.toISOString(),
        [SALESFORCE_FIELDS.contract.status]: "Activated",
        [SALESFORCE_FIELDS.contract.endDate]: "2027-10-21",
      }],
      nextCursor: null,
      complete: true,
    };
  }
  if (request.object === "Case") {
    const record = {
      Id: `sf-case-${pageCount}`,
      [SALESFORCE_FIELDS.case.updatedAt]: baseTime.toISOString(),
      [SALESFORCE_FIELDS.case.status]: "Working",
      [SALESFORCE_FIELDS.case.priority]: "Medium",
      [SALESFORCE_FIELDS.case.reason]: "Risk review",
      [SALESFORCE_FIELDS.case.createdAt]: baseTime.toISOString(),
      [SALESFORCE_FIELDS.case.closedAt]: null,
      [SALESFORCE_FIELDS.case.isEscalated]: false,
      [SALESFORCE_FIELDS.case.riskCategory]: "Repayment",
      [SALESFORCE_FIELDS.case.description]: `Case ${pageCount}`,
    };
    return pageCount === 1
      ? { records: [record], nextCursor: "case-page-2", complete: false }
      : { records: [record], nextCursor: null, complete: true };
  }
  return { records: [], nextCursor: null, complete: true };
}
