import { readFile } from "node:fs/promises";
import { describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { getDemoSalesforceSnapshot } from "../../server/src/salesforce/fixture-source.js";
import type { SalesforceCustomerSnapshot } from "../../server/src/salesforce/types.js";
import type { SnapshotCandidate } from "../../server/src/snapshots/repository.js";
import {
  getCurrentSnapshot,
  publishSnapshot,
  recordFailedSnapshotAttempt,
} from "../../server/src/snapshots/repository.js";

const migrationUrl = new URL(
  "../../server/src/db/migrations/003_salesforce_snapshots.sql",
  import.meta.url,
);

interface SnapshotRow {
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
  query_completion: SnapshotCandidate["queryCompletion"];
  record_counts: SnapshotCandidate["recordCounts"];
  canonical_payload: SalesforceCustomerSnapshot;
}

interface StoreState {
  enrolledCustomers: Map<string, boolean>;
  versions: Map<string, SnapshotRow[]>;
  pointers: Map<string, string>;
  attempts: Map<string, { status: string }>;
}

class SnapshotPool implements DatabasePool {
  state: StoreState = {
    enrolledCustomers: new Map([["customer-1", true], ["customer-2", true]]),
    versions: new Map(),
    pointers: new Map(),
    attempts: new Map(),
  };
  failAttemptRunId: string | null = null;
  statements: string[] = [];

  async query<Row>(text: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    this.statements.push(normalize(text));
    if (normalize(text).startsWith("SELECT version.snapshot_id")) {
      const customerId = String(values[0]);
      const snapshotId = this.state.pointers.get(customerId);
      const row = this.state.versions.get(customerId)?.find((version) => version.snapshot_id === snapshotId);
      return { rows: row ? [row as Row] : [], rowCount: row ? 1 : 0 };
    }
    if (normalize(text).startsWith("INSERT INTO customer_snapshot_attempts")) {
      const runId = String(values[1]);
      this.state.attempts.set(`${String(values[0])}:${runId}`, { status: "failed" });
    }
    return { rows: [], rowCount: 1 };
  }

  async connect(): Promise<DatabaseClient> {
    let transaction: StoreState | null = null;
    const client: DatabaseClient = {
      query: async <Row>(text: string, values: readonly unknown[] = []) => {
        const sql = normalize(text);
        this.statements.push(sql);
        if (sql === "BEGIN") {
          transaction = cloneState(this.state);
          return emptyResult<Row>();
        }
        if (!transaction) throw new Error("Query outside transaction");
        if (sql === "COMMIT") {
          this.state = transaction;
          transaction = null;
          return emptyResult<Row>();
        }
        if (sql === "ROLLBACK") {
          transaction = null;
          return emptyResult<Row>();
        }
        if (sql.startsWith("SELECT customer_id, is_active FROM customer_roster")) {
          const customerId = String(values[0]);
          const isActive = transaction.enrolledCustomers.get(customerId);
          const rows = isActive !== undefined
            ? [{ customer_id: customerId, is_active: isActive } as Row]
            : [];
          return { rows, rowCount: rows.length };
        }
        if (sql.startsWith("SELECT COALESCE(MAX(version)")) {
          const versions = transaction.versions.get(String(values[0])) ?? [];
          return { rows: [{ next_version: versions.length + 1 } as Row], rowCount: 1 };
        }
        if (sql.startsWith("INSERT INTO customer_snapshot_versions")) {
          const customerId = String(values[1]);
          const rows = transaction.versions.get(customerId) ?? [];
          rows.push({
            snapshot_id: String(values[0]),
            customer_id: customerId,
            version: Number(values[2]),
            source_mode: values[3] as "demo" | "production_mcp",
            schema_version: Number(values[4]),
            mapping_version: String(values[5]),
            run_id: String(values[6]),
            sync_started_at: values[7] as Date,
            captured_at: values[8] as Date,
            published_at: new Date(),
            query_completion: JSON.parse(String(values[9])) as SnapshotCandidate["queryCompletion"],
            record_counts: JSON.parse(String(values[10])) as SnapshotCandidate["recordCounts"],
            canonical_payload: JSON.parse(String(values[11])) as SalesforceCustomerSnapshot,
          });
          transaction.versions.set(customerId, rows);
          return emptyResult<Row>();
        }
        if (sql.startsWith("INSERT INTO customer_snapshot_current")) {
          transaction.pointers.set(String(values[0]), String(values[1]));
          return emptyResult<Row>();
        }
        if (sql.startsWith("INSERT INTO customer_snapshot_attempts")) {
          if (String(values[1]) === this.failAttemptRunId) throw new Error("injected attempt failure");
          transaction.attempts.set(`${String(values[0])}:${String(values[1])}`, { status: "succeeded" });
          return emptyResult<Row>();
        }
        throw new Error(`Unexpected SQL: ${sql}`);
      },
      release: vi.fn(),
    };
    return client;
  }

  async end(): Promise<void> {}
}

describe("Salesforce snapshot publication schema", () => {
  it("stores versioned canonical data, a constrained current pointer, and separate attempts", async () => {
    const sql = await readFile(migrationUrl, "utf8");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS customer_snapshot_versions");
    expect(sql).toContain("canonical_payload jsonb NOT NULL");
    expect(sql).toContain("query_completion jsonb NOT NULL");
    expect(sql).toContain("record_counts jsonb NOT NULL");
    expect(sql).toContain("sync_started_at timestamptz NOT NULL");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS customer_snapshot_current");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS customer_snapshot_attempts");
    expect(sql).toContain("customer_snapshot_versions_immutable");
    expect(sql).toContain("FOREIGN KEY (customer_id, snapshot_id)");
  });
});

describe("atomic per-customer snapshot publication", () => {
  it("rolls back a failed publication and leaves the previous pointer and version immutable", async () => {
    const pool = new SnapshotPool();
    const first = await publishSnapshot(pool, candidate("customer-1", "run-1"));
    pool.failAttemptRunId = "run-2";

    await expect(publishSnapshot(pool, candidate("customer-1", "run-2")))
      .rejects.toThrow("injected attempt failure");

    expect(await getCurrentSnapshot(pool, "customer-1")).toMatchObject({
      snapshotId: first.snapshotId,
      version: 1,
      runId: "run-1",
    });
    expect(pool.state.versions.get("customer-1")).toHaveLength(1);
    expect(pool.statements).toContain("ROLLBACK");
  });

  it("publishes customers independently and advances only the successful customer's pointer", async () => {
    const pool = new SnapshotPool();
    pool.failAttemptRunId = "run-customer-1";

    await expect(publishSnapshot(pool, candidate("customer-1", "run-customer-1")))
      .rejects.toThrow("injected attempt failure");
    const second = await publishSnapshot(pool, candidate("customer-2", "run-customer-2"));

    expect(await getCurrentSnapshot(pool, "customer-1")).toBeNull();
    expect(await getCurrentSnapshot(pool, "customer-2")).toMatchObject({
      snapshotId: second.snapshotId,
      customerId: "customer-2",
      version: 1,
    });
    expect(pool.state.versions.get("customer-1") ?? []).toHaveLength(0);
    expect(pool.state.versions.get("customer-2")).toHaveLength(1);
  });

  it("rejects incomplete candidates before opening a transaction", async () => {
    const pool = new SnapshotPool();
    const incomplete = candidate("customer-1", "run-incomplete");
    incomplete.queryCompletion.case = { complete: false, pages: 1 };

    await expect(publishSnapshot(pool, incomplete)).rejects.toThrow("incomplete for case");
    expect(pool.statements).toHaveLength(0);
  });

  it("rejects a customer deactivated before the publication lock", async () => {
    const pool = new SnapshotPool();
    pool.state.enrolledCustomers.set("customer-1", false);

    await expect(publishSnapshot(pool, candidate("customer-1", "run-deactivated")))
      .rejects.toThrow("Customer customer-1 is not active");
    expect(pool.state.versions.get("customer-1") ?? []).toHaveLength(0);
    expect(pool.statements).toContain("ROLLBACK");
  });

  it("advances from version one to two while retaining the earlier version", async () => {
    const pool = new SnapshotPool();
    const first = await publishSnapshot(pool, candidate("customer-1", "run-1"));
    const second = await publishSnapshot(pool, candidate("customer-1", "run-2"));

    expect(second.version).toBe(2);
    expect(pool.state.versions.get("customer-1")).toHaveLength(2);
    expect(await getCurrentSnapshot(pool, "customer-1")).toMatchObject({ snapshotId: second.snapshotId, version: 2 });
    expect(pool.state.versions.get("customer-1")?.[0]).toMatchObject({ snapshot_id: first.snapshotId, version: 1 });
  });

  it("records a failed attempt separately without changing the last published version", async () => {
    const pool = new SnapshotPool();
    const published = await publishSnapshot(pool, candidate("customer-1", "run-good"));
    await recordFailedSnapshotAttempt(pool, {
      customerId: "customer-1",
      runId: "run-failed",
      sourceMode: "demo",
      schemaVersion: 1,
      mappingVersion: "salesforce-proposed-v1",
      startedAt: new Date("2026-10-21T11:00:00Z"),
      finishedAt: new Date("2026-10-21T11:05:00Z"),
      queryCompletion: { account: { complete: true, pages: 1 } },
      recordCounts: { account: 1 },
      errorMessage: "Required Case query failed",
    });

    expect(pool.state.attempts.get("customer-1:run-failed")).toEqual({ status: "failed" });
    expect(pool.state.versions.get("customer-1")).toHaveLength(1);
    expect(await getCurrentSnapshot(pool, "customer-1")).toMatchObject({
      snapshotId: published.snapshotId,
      runId: "run-good",
    });
  });
});

function candidate(customerId: string, runId: string): SnapshotCandidate {
  const payload = getDemoSalesforceSnapshot("acct_bloom001");
  if (!payload) throw new Error("Synthetic demo fixture missing");
  return {
    customerId,
    runId,
    sourceMode: "demo",
    startedAt: new Date("2026-10-21T10:00:00Z"),
    capturedAt: new Date("2026-10-21T10:05:00Z"),
    queryCompletion: {
      account: { complete: true, pages: 1 },
      contract: { complete: true, pages: 1 },
      case: { complete: true, pages: 1 },
      activity: { complete: true, pages: 1 },
    },
    recordCounts: { account: 1, contract: 1, case: 2, activity: 2 },
    payload,
  };
}

function cloneState(state: StoreState): StoreState {
  return {
    enrolledCustomers: new Map(state.enrolledCustomers),
    versions: new Map([...state.versions].map(([customerId, rows]) => [customerId, [...rows]])),
    pointers: new Map(state.pointers),
    attempts: new Map(state.attempts),
  };
}

function emptyResult<Row>(): QueryResult<Row> {
  return { rows: [], rowCount: 1 };
}

function normalize(sql: string): string {
  return sql.replace(/\s+/g, " ").trim();
}
