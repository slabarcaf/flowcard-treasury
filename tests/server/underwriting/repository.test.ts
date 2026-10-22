import { readFile } from "node:fs/promises";
import { describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { getCurrentUnderwritingProfile, listActiveProfileCustomerBatches } from "../../../server/src/underwriting/repository.js";
import type { ProfileQueryName, PublishedUnderwritingProfile } from "../../../server/src/underwriting/types.js";
import { publishCustomerProfile } from "../../../server/src/underwriting/publisher.js";
import type { UnderwritingProfileCandidate } from "../../../server/src/underwriting/types.js";

const migrationUrl = new URL("../../../server/src/db/migrations/005_underwriting_profiles.sql", import.meta.url);

interface StoredProfile extends PublishedUnderwritingProfile {}
interface State {
  active: Map<string, boolean>;
  versions: Map<string, StoredProfile[]>;
  pointers: Map<string, string>;
  attempts: Map<string, { status: string; error?: string }>;
}

class ProfilePool implements DatabasePool {
  state: State = {
    active: new Map([["customer-1", true], ["customer-2", true], ["customer-off", false]]),
    versions: new Map(), pointers: new Map(), attempts: new Map(),
  };
  failSucceededAttempt = false;
  selectedRoster: string[] = [];
  statements: string[] = [];

  async query<Row>(text: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    const sql = normalize(text);
    this.statements.push(sql);
    if (sql.startsWith("SELECT version.profile_id")) {
      const customerId = String(values[0]);
      const id = this.state.pointers.get(customerId);
      const row = this.state.versions.get(customerId)?.find((profile) => profile.profileId === id);
      return { rows: row ? [toRow(row) as Row] : [], rowCount: row ? 1 : 0 };
    }
    if (sql.startsWith("SELECT customer_id FROM customer_roster")) {
      return { rows: this.selectedRoster.filter((id) => this.state.active.get(id)).sort().map((customer_id) => ({ customer_id } as Row)), rowCount: this.selectedRoster.length };
    }
    if (sql.startsWith("INSERT INTO underwriting_profile_attempts")) {
      this.state.attempts.set(`${String(values[0])}:${String(values[1])}`, { status: "failed", error: String(values[7]) });
      return empty<Row>();
    }
    throw new Error(`Unexpected pool SQL: ${sql}`);
  }

  async connect(): Promise<DatabaseClient> {
    let transaction: State | null = null;
    return {
      query: async <Row>(text: string, values: readonly unknown[] = []) => {
        const sql = normalize(text);
        this.statements.push(sql);
        if (sql === "BEGIN") { transaction = cloneState(this.state); return empty<Row>(); }
        if (!transaction) throw new Error("query outside transaction");
        if (sql === "COMMIT") { this.state = transaction; transaction = null; return empty<Row>(); }
        if (sql === "ROLLBACK") { transaction = null; return empty<Row>(); }
        if (sql.startsWith("SELECT customer_id, is_active FROM customer_roster")) {
          const customerId = String(values[0]);
          const active = transaction.active.get(customerId);
          return { rows: active === undefined ? [] : [{ customer_id: customerId, is_active: active } as Row], rowCount: active === undefined ? 0 : 1 };
        }
        if (sql.startsWith("SELECT COALESCE(MAX(version)")) {
          const count = transaction.versions.get(String(values[0]))?.length ?? 0;
          return { rows: [{ next_version: count + 1 } as Row], rowCount: 1 };
        }
        if (sql.startsWith("INSERT INTO underwriting_profile_versions")) {
          const customerId = String(values[1]);
          const versions = transaction.versions.get(customerId) ?? [];
          const candidateData = JSON.parse(String(values[13])) as PublishedUnderwritingProfile["data"];
          versions.push({
            ...candidate(customerId, String(values[3])), data: candidateData,
            profileId: String(values[0]), version: Number(values[2]), publishedAt: new Date(),
            sourceMode: values[4] as "demo" | "production",
            schemaVersion: Number(values[5]) as 1,
            featureVersion: String(values[6]),
            sourceVersions: JSON.parse(String(values[7])) as Record<ProfileQueryName, string>,
            windowStart: values[8] as Date, windowEnd: values[9] as Date,
            startedAt: values[10] as Date, capturedAt: values[11] as Date,
            queryCompletion: JSON.parse(String(values[12])) as UnderwritingProfileCandidate["queryCompletion"],
          });
          transaction.versions.set(customerId, versions);
          return empty<Row>();
        }
        if (sql.startsWith("INSERT INTO underwriting_profile_current")) {
          transaction.pointers.set(String(values[0]), String(values[1]));
          return empty<Row>();
        }
        if (sql.startsWith("INSERT INTO underwriting_profile_attempts")) {
          if (this.failSucceededAttempt) throw new Error("injected attempt failure");
          transaction.attempts.set(`${String(values[0])}:${String(values[1])}`, { status: "succeeded" });
          return empty<Row>();
        }
        throw new Error(`Unexpected client SQL: ${sql}`);
      },
      release: vi.fn(),
    };
  }

  async end(): Promise<void> {}
}

describe("underwriting profile schema and repository", () => {
  it("stores immutable profile versions, source provenance, current pointer, and attempts", async () => {
    const sql = await readFile(migrationUrl, "utf8");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS underwriting_profile_versions");
    expect(sql).toContain("source_versions jsonb NOT NULL");
    expect(sql).toContain("query_completion jsonb NOT NULL");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS underwriting_profile_current");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS underwriting_profile_attempts");
    expect(sql).toContain("underwriting_profile_versions_immutable");
    expect(sql).toContain("FOREIGN KEY (customer_id, profile_id)");
  });

  it("keeps unknown values distinct from measured zero and retains source versions", async () => {
    const pool = new ProfilePool();
    const firstCandidate = candidate("customer-1", "run-1");
    firstCandidate.data.averageMonthlySettledInflowsCents = 0;
    firstCandidate.data.expectedDateReliability = null;
    // Expected-date reliability is a required production feature, so unknown blocks publication.
    const failed = await publishCustomerProfile(pool, firstCandidate);
    expect(failed.status).toBe("failed");
    expect(pool.state.versions.get("customer-1") ?? []).toHaveLength(0);
    expect(pool.state.attempts.get("customer-1:run-1")?.status).toBe("failed");

    const complete = candidate("customer-1", "run-2");
    complete.data.averageMonthlySettledInflowsCents = 0;
    const published = await publishCustomerProfile(pool, complete);
    expect(published.status).toBe("published");
    if (published.status !== "published") return;
    expect(published.profile.data.averageMonthlySettledInflowsCents).toBe(0);
    expect(published.profile.sourceVersions.account).toBe("account-v7");
    expect(await getCurrentUnderwritingProfile(pool, "customer-1", new Date("2026-10-22T10:00:00.000Z"))).toMatchObject({
      profile: { profileId: published.profile.profileId, sourceVersions: { account: "account-v7" }, data: { averageMonthlySettledInflowsCents: 0 } },
      freshness: "fresh",
    });
    const stale = await getCurrentUnderwritingProfile(pool, "customer-1", new Date("2026-10-23T10:00:01.000Z"));
    expect(stale.freshness).toBe("stale");
  });

  it("rolls back version and pointer changes when the success attempt cannot be recorded", async () => {
    const pool = new ProfilePool();
    const first = await publishCustomerProfile(pool, candidate("customer-1", "run-1"));
    if (first.status !== "published") throw new Error("expected first profile publication");
    pool.failSucceededAttempt = true;
    await expect(publishCustomerProfile(pool, candidate("customer-1", "run-2")))
      .rejects.toThrow("injected attempt failure");
    expect(pool.state.versions.get("customer-1")).toHaveLength(1);
    expect(pool.state.pointers.get("customer-1")).toBe(first.profile.profileId);
    expect(pool.statements).toContain("ROLLBACK");
  });

  it("records a failed customer's attempt without stopping another active customer", async () => {
    const pool = new ProfilePool();
    const incomplete = candidate("customer-1", "run-incomplete");
    incomplete.queryCompletion.transactions.complete = false;
    expect((await publishCustomerProfile(pool, incomplete)).status).toBe("failed");
    const other = await publishCustomerProfile(pool, candidate("customer-2", "run-good"));
    expect(other.status).toBe("published");
    expect(pool.state.pointers.has("customer-1")).toBe(false);
    expect(pool.state.pointers.has("customer-2")).toBe(true);
  });

  it("uses only active roster customers and enforces batches no larger than 1,000", async () => {
    const pool = new ProfilePool();
    pool.selectedRoster = ["customer-1", "customer-2", "customer-off", ...Array.from({ length: 1001 }, (_, i) => `active-${i}`)];
    for (const id of pool.selectedRoster) if (!pool.state.active.has(id)) pool.state.active.set(id, true);
    pool.state.active.set("customer-off", false);
    const batches = await listActiveProfileCustomerBatches(pool);
    expect(batches.map((batch) => batch.length)).toEqual([1000, 3]);
    expect(batches.flat()).not.toContain("customer-off");
    await expect(listActiveProfileCustomerBatches(pool, 1001)).rejects.toThrow("1,000");
  });
});

function candidate(customerId: string, runId: string): UnderwritingProfileCandidate {
  const end = new Date("2026-10-22T10:00:00.000Z");
  const queries = ["account", "balances", "transactions", "expected_inflows", "repayments"] as const;
  return {
    customerId, runId, sourceMode: "demo", capturedAt: end,
    startedAt: new Date(end.getTime() - 60_000),
    sourceVersions: Object.fromEntries(queries.map((query) => [query, `${query}-v7`])) as Record<ProfileQueryName, string>,
    queryCompletion: Object.fromEntries(queries.map((query) => [query, { complete: true, pages: 1 }])) as UnderwritingProfileCandidate["queryCompletion"],
    schemaVersion: 1, featureVersion: "features-v1",
    windowStart: new Date(end.getTime() - 90 * 24 * 60 * 60 * 1000), windowEnd: end,
    data: {
      averageMonthlySettledInflowsCents: null, expectedDateReliability: 0.92,
      topPayerConcentration: 0.31, lowBalanceDays: 0, historyDays: 90,
      eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false,
      repaymentHistory: "never_borrowed",
    },
  };
}

function toRow(profile: StoredProfile) {
  return {
    profile_id: profile.profileId, customer_id: profile.customerId, version: profile.version,
    run_id: profile.runId, source_mode: profile.sourceMode, schema_version: profile.schemaVersion,
    feature_version: profile.featureVersion, source_versions: profile.sourceVersions,
    window_start: profile.windowStart, window_end: profile.windowEnd,
    started_at: profile.startedAt, captured_at: profile.capturedAt, published_at: profile.publishedAt,
    query_completion: profile.queryCompletion, canonical_payload: profile.data,
  };
}

function cloneState(state: State): State {
  return {
    active: new Map(state.active),
    versions: new Map([...state.versions].map(([id, rows]) => [id, [...rows]])),
    pointers: new Map(state.pointers), attempts: new Map(state.attempts),
  };
}

function empty<Row>(): QueryResult<Row> { return { rows: [], rowCount: 1 }; }
function normalize(sql: string): string { return sql.replace(/\s+/g, " ").trim(); }
