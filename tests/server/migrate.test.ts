import { describe, expect, it, vi } from "vitest";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import {
  resolveMigrationsDirectory,
  sanitizeDatabaseError,
  computeSqlChecksum,
  hasStandaloneTransaction,
  runNodeMigrations,
  NODE_MIGRATION_ADVISORY_LOCK_ID,
} from "../../server/src/db/migrate.js";

describe("Node database migration runner", () => {
  it("resolves the migration directory containing all 21 numbered SQL files", async () => {
    const dir = resolveMigrationsDirectory();
    expect(dir).toBeTruthy();

    const files = (await readdir(dir))
      .filter((f) => f.endsWith(".sql"))
      .sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));

    expect(files.length).toBe(21);
    expect(files[0]).toBe("001_bridge_ledger.sql");
    expect(files.at(-1)).toBe("021_onboarding_webhook_forwarding.sql");
  });

  it("computes deterministic SHA-256 checksums for SQL files", () => {
    const c1 = computeSqlChecksum("SELECT 1;");
    const c2 = computeSqlChecksum("SELECT 1;");
    const c3 = computeSqlChecksum("SELECT 2;");
    expect(c1).toBe(c2);
    expect(c1).not.toBe(c3);
    expect(c1).toMatch(/^[a-f0-9]{64}$/);
  });

  it("detects standalone transaction statements vs plpgsql functions", () => {
    expect(hasStandaloneTransaction("BEGIN; CREATE TABLE a (id int); COMMIT;")).toBe(true);
    expect(hasStandaloneTransaction("ROLLBACK;")).toBe(true);
    expect(
      hasStandaloneTransaction(`
        CREATE FUNCTION test() RETURNS trigger AS $$
        BEGIN
          RAISE NOTICE 'hi';
        END;
        $$ LANGUAGE plpgsql;
      `),
    ).toBe(false);
  });

  it("sanitizes connection credentials and quoted/unquoted passwords in error messages", () => {
    const uriErr = new Error("connection to postgresql://admin:super_secret_pass@10.0.1.5:5432/flowcard failed");
    expect(sanitizeDatabaseError(uriErr)).toBe("connection to postgresql://***:***@10.0.1.5:5432/flowcard failed");

    const quotedErr = new Error('failed password="my complex;secret pass" host=db.internal');
    expect(sanitizeDatabaseError(quotedErr)).toBe("failed password=*** host=db.internal");

    const singleQuotedErr = new Error("failed password='my complex secret pass' host=db.internal");
    expect(sanitizeDatabaseError(singleQuotedErr)).toBe("failed password=*** host=db.internal");

    const unquotedErr = new Error("failed password=plaintext_secret; host=db.internal");
    expect(sanitizeDatabaseError(unquotedErr)).toBe("failed password=***; host=db.internal");
  });

  it("fails closed when DATABASE_URL is empty", async () => {
    await expect(runNodeMigrations("")).rejects.toThrow("DATABASE_URL is required to run migrations");
    await expect(runNodeMigrations("   ")).rejects.toThrow("DATABASE_URL is required to run migrations");
  });

  it("runs migrations idempotently with advisory lock and drift check using mock database client", async () => {
    const executedQueries: Array<{ text: string; values?: readonly unknown[] }> = [];
    const dir = resolveMigrationsDirectory();
    const sql001 = await readFile(join(dir, "001_bridge_ledger.sql"), "utf8");
    const checksum001 = computeSqlChecksum(sql001);

    const appliedRecords = new Map<string, { version: string; name: string; checksum: string | null }>([
      ["001", { version: "001", name: "001_bridge_ledger.sql", checksum: checksum001 }],
    ]);

    const mockClient = {
      query: vi.fn(async (queryText: string, values?: readonly unknown[]) => {
        executedQueries.push({ text: queryText, values });
        if (queryText.includes("SELECT version, name, checksum FROM schema_migrations")) {
          return {
            rows: Array.from(appliedRecords.values()),
            rowCount: appliedRecords.size,
          };
        }
        if (queryText.includes("INSERT INTO schema_migrations")) {
          const version = values?.[0] as string;
          const name = values?.[1] as string;
          const checksum = values?.[2] as string;
          appliedRecords.set(version, { version, name, checksum });
          return { rows: [], rowCount: 1 };
        }
        return { rows: [], rowCount: 0 };
      }),
      release: vi.fn(),
    };

    const mockPool = {
      connect: vi.fn(async () => mockClient),
      end: vi.fn(async () => {}),
      query: vi.fn(),
    };

    const mockFactory = vi.fn(async () => mockPool as any);

    const results = await runNodeMigrations("postgresql://mock:mock@localhost:5432/test", undefined, mockFactory);

    expect(results.length).toBe(21);
    // 001 was already applied
    expect(results[0]).toEqual({ version: "001", name: "001_bridge_ledger.sql", applied: false });
    // 002 through 021 applied
    expect(results[1]).toEqual({ version: "002", name: "002_customer_roster.sql", applied: true });
    expect(results.at(-1)).toEqual({ version: "021", name: "021_onboarding_webhook_forwarding.sql", applied: true });

    // Verify advisory lock was acquired and released
    const lockCalls = executedQueries.filter((q) => q.text.includes("pg_advisory_lock"));
    expect(lockCalls.length).toBe(1);
    expect(lockCalls[0].values).toEqual([NODE_MIGRATION_ADVISORY_LOCK_ID]);

    const unlockCalls = executedQueries.filter((q) => q.text.includes("pg_advisory_unlock"));
    expect(unlockCalls.length).toBe(1);
    expect(unlockCalls[0].values).toEqual([NODE_MIGRATION_ADVISORY_LOCK_ID]);

    expect(mockPool.end).toHaveBeenCalledOnce();
    expect(mockClient.release).toHaveBeenCalledOnce();
  });

  it("fails closed when migration name drift is detected", async () => {
    const mockClient = {
      query: vi.fn(async (queryText: string) => {
        if (queryText.includes("SELECT version, name, checksum FROM schema_migrations")) {
          return {
            rows: [{ version: "001", name: "001_altered_name.sql", checksum: "somechecksum" }],
            rowCount: 1,
          };
        }
        return { rows: [], rowCount: 0 };
      }),
      release: vi.fn(),
    };

    const mockPool = {
      connect: vi.fn(async () => mockClient),
      end: vi.fn(async () => {}),
      query: vi.fn(),
    };

    const mockFactory = vi.fn(async () => mockPool as any);

    await expect(
      runNodeMigrations("postgresql://mock:mock@localhost:5432/test", undefined, mockFactory),
    ).rejects.toThrow("Migration drift detected for version 001");
  });

  it("fails closed when migration checksum drift is detected", async () => {
    const mockClient = {
      query: vi.fn(async (queryText: string) => {
        if (queryText.includes("SELECT version, name, checksum FROM schema_migrations")) {
          return {
            rows: [{ version: "001", name: "001_bridge_ledger.sql", checksum: "corrupted_checksum_value" }],
            rowCount: 1,
          };
        }
        return { rows: [], rowCount: 0 };
      }),
      release: vi.fn(),
    };

    const mockPool = {
      connect: vi.fn(async () => mockClient),
      end: vi.fn(async () => {}),
      query: vi.fn(),
    };

    const mockFactory = vi.fn(async () => mockPool as any);

    await expect(
      runNodeMigrations("postgresql://mock:mock@localhost:5432/test", undefined, mockFactory),
    ).rejects.toThrow("Migration checksum drift detected for version 001");
  });

  it("rolls back transaction and releases advisory lock on SQL failure", async () => {
    let rolledBack = false;
    let lockReleased = false;

    const mockClient = {
      query: vi.fn(async (queryText: string) => {
        if (queryText.includes("SELECT version, name, checksum FROM schema_migrations")) {
          return { rows: [], rowCount: 0 };
        }
        if (queryText === "ROLLBACK") {
          rolledBack = true;
          return { rows: [], rowCount: 0 };
        }
        if (queryText.includes("pg_advisory_unlock")) {
          lockReleased = true;
          return { rows: [], rowCount: 0 };
        }
        if (queryText.includes("CREATE TABLE") && queryText.includes("bridge_cases")) {
          throw new Error("syntax error at or near 'INVALID_SQL' with password=secret123");
        }
        return { rows: [], rowCount: 0 };
      }),
      release: vi.fn(),
    };

    const mockPool = {
      connect: vi.fn(async () => mockClient),
      end: vi.fn(async () => {}),
      query: vi.fn(),
    };

    const mockFactory = vi.fn(async () => mockPool as any);

    await expect(
      runNodeMigrations("postgresql://mock:mock@localhost:5432/test", undefined, mockFactory),
    ).rejects.toThrow("syntax error at or near 'INVALID_SQL' with password=***");

    expect(rolledBack).toBe(true);
    expect(lockReleased).toBe(true);
    expect(mockClient.release).toHaveBeenCalledOnce();
    expect(mockPool.end).toHaveBeenCalledOnce();
  });
});
