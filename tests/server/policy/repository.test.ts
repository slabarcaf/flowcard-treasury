import { readFile } from "node:fs/promises";
import { describe, expect, it } from "vitest";
import type { DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { persistDailyPrecheck, policyEvaluationKey, type StoredDailyPrecheck } from "../../../server/src/policy/repository.js";

const evaluation = (overrides: Partial<StoredDailyPrecheck> = {}): StoredDailyPrecheck => ({
  customerId: "customer-1", policyVersion: "underwriting-2026-10-20-v1", profileId: "00000000-0000-4000-8000-000000000001",
  snapshotId: "00000000-0000-4000-8000-000000000002", evidenceRunId: "00000000-0000-4000-8000-000000000003",
  ratings: { inflowReliability: "green", inflowConcentration: "green", cashBuffer: "green", trackRecord: "yellow" },
  tier: "A", creditLineCents: 100_000, outcome: "ready", humanApprovalRequired: false,
  reasonCodes: ["TIER_A"], inputVersions: { profileVersion: 1 }, mappingVersions: { industry: "industry-1" },
  evaluatedAt: new Date("2026-10-22T00:00:00Z"), rosterUpdatedAt: new Date("2026-10-21T00:00:00Z"), ...overrides,
});
const migrationUrl = new URL("../../../server/src/db/migrations/006_policy_evaluations.sql", import.meta.url);

class MemoryPool implements DatabasePool {
  rows = new Map<string, string>();
  writes: Array<{ sql: string; values: readonly unknown[] }> = [];
  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    if (sql.includes("INSERT INTO policy_evaluations")) {
      this.writes.push({ sql, values });
      const key = String(values[1]);
      if (!this.rows.has(key)) this.rows.set(key, String(values[0]));
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SELECT evaluation_id FROM policy_evaluations")) {
      const id = this.rows.get(String(values[0]));
      return { rows: (id ? [{ evaluation_id: id }] : []) as Row[], rowCount: id ? 1 : 0 };
    }
    throw new Error(`Unexpected SQL: ${sql}`);
  }
  async connect() { throw new Error("not used"); }
  async end() {}
}

describe("policy evaluation repository", () => {
  it("uses a stable idempotency key for the exact customer and source tuple", () => {
    const first = evaluation();
    expect(policyEvaluationKey(first)).toBe(policyEvaluationKey({ ...first, evaluatedAt: new Date("2026-10-23T00:00:00Z") }));
    expect(policyEvaluationKey(first)).not.toBe(policyEvaluationKey({ ...first, policyVersion: "underwriting-next" }));
    expect(policyEvaluationKey(first)).not.toBe(policyEvaluationKey({ ...first, customerId: "customer-2" }));
    expect(policyEvaluationKey(first)).not.toBe(policyEvaluationKey({ ...first, evidenceRunId: "00000000-0000-4000-8000-000000000004" }));
    expect(policyEvaluationKey(first)).not.toBe(policyEvaluationKey({ ...first, inputVersions: { profileVersion: 2 } }));
    expect(policyEvaluationKey(first)).not.toBe(policyEvaluationKey({ ...first, mappingVersions: { industry: "industry-2" } }));
    expect(policyEvaluationKey(first)).toBe(policyEvaluationKey({ ...first, inputVersions: { profileVersion: 1 }, mappingVersions: { industry: "industry-1" } }));
  });

  it("appends once, returns the original row on retry, and appends policy-version history", async () => {
    const pool = new MemoryPool();
    const first = evaluation();
    const originalId = await persistDailyPrecheck(pool, first);
    expect(await persistDailyPrecheck(pool, { ...first, creditLineCents: 1 })).toBe(originalId);
    const laterId = await persistDailyPrecheck(pool, { ...first, policyVersion: "underwriting-next" });
    expect(laterId).not.toBe(originalId);
    expect(pool.rows.size).toBe(2);
    expect(pool.writes.every(({ sql }) => sql.includes("ON CONFLICT (idempotency_key) DO NOTHING"))).toBe(true);
    expect(pool.writes[0]?.values.slice(2, 8)).toEqual([
      "customer-1", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002",
      "00000000-0000-4000-8000-000000000003", "underwriting-2026-10-20-v1", first.rosterUpdatedAt,
    ]);
    expect(pool.writes[0]?.values[15]).toEqual(JSON.stringify({ profileVersion: 1 }));
    expect(pool.writes[0]?.sql).not.toMatch(/\b(UPDATE|DELETE)\b/i);
  });

  it("enforces immutable history and source-version references in PostgreSQL", async () => {
    const sql = await readFile(migrationUrl, "utf8");
    expect(sql).toMatch(/idempotency_key text NOT NULL UNIQUE/);
    expect(sql).toContain("BEFORE UPDATE OR DELETE ON policy_evaluations");
    expect(sql).toContain("REFERENCES underwriting_profile_versions(customer_id, profile_id)");
    expect(sql).toContain("REFERENCES customer_snapshot_versions(customer_id, snapshot_id)");
    expect(sql).toContain("REFERENCES ai_evidence_extractions(customer_id, snapshot_id, extraction_id)");
  });
});
