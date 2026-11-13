import { describe, expect, it, vi } from "vitest";
import {
  validateDatabaseSafety,
  seedSyntheticDemo,
  resolveSeedSqlPath,
  DISPOSABLE_TEST_PORT,
  DISPOSABLE_TEST_DB,
  SEED_ADVISORY_LOCK_ID,
} from "../../server/src/db/seed.js";

describe("Synthetic demonstration fixture seed", () => {
  it("resolves the synthetic seed SQL file", () => {
    const path = resolveSeedSqlPath();
    expect(path).toBeTruthy();
    expect(path.endsWith("seed-synthetic-demo.sql")).toBe(true);
  });

  describe("validateDatabaseSafety", () => {
    it("allows loopback connection with exact port 54329 and flowcard_onboarding_test database", () => {
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@127.0.0.1:${DISPOSABLE_TEST_PORT}/${DISPOSABLE_TEST_DB}`),
      ).not.toThrow();
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@localhost:${DISPOSABLE_TEST_PORT}/${DISPOSABLE_TEST_DB}`),
      ).not.toThrow();
    });

    it("rejects standard PostgreSQL port 5432 in test mode", () => {
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@127.0.0.1:5432/${DISPOSABLE_TEST_DB}`),
      ).toThrow("does not match explicitly authorized target port (54329)");
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@localhost:5432/${DISPOSABLE_TEST_DB}`),
      ).toThrow("does not match explicitly authorized target port (54329)");
    });

    it("rejects non-test loopback ports", () => {
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@127.0.0.1:54321/${DISPOSABLE_TEST_DB}`),
      ).toThrow("does not match explicitly authorized target port (54329)");
    });

    it("rejects remote hosts in test mode", () => {
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@db.example.com:${DISPOSABLE_TEST_PORT}/${DISPOSABLE_TEST_DB}`),
      ).toThrow("automated seed prohibits remote host 'db.example.com'");
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@10.0.1.50:${DISPOSABLE_TEST_PORT}/${DISPOSABLE_TEST_DB}`),
      ).toThrow("automated seed prohibits remote host '10.0.1.50'");
    });

    it("rejects database names other than flowcard_onboarding_test in test mode", () => {
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@127.0.0.1:${DISPOSABLE_TEST_PORT}/flowcard_demo`),
      ).toThrow("does not match explicitly authorized target database ('flowcard_onboarding_test')");
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@127.0.0.1:${DISPOSABLE_TEST_PORT}/production_db`),
      ).toThrow("does not match explicitly authorized target database ('flowcard_onboarding_test')");
    });

    it("validates explicit target config boundaries", () => {
      // Remote host without explicit target config fails closed
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@rds.flowcard.internal:5432/flowcard_demo`),
      ).toThrow("automated seed prohibits remote host 'rds.flowcard.internal'");

      // Remote host with matching explicit target config is permitted
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@rds.flowcard.internal:5432/flowcard_demo`, {
          expectedHost: "rds.flowcard.internal",
          expectedPort: 5432,
          expectedDatabase: "flowcard_demo",
        }),
      ).not.toThrow();

      // Mismatched expected host in target config fails closed
      expect(() =>
        validateDatabaseSafety(`postgresql://user:pass@rds.flowcard.internal:5432/flowcard_demo`, {
          expectedHost: "other.flowcard.internal",
          expectedPort: 5432,
          expectedDatabase: "flowcard_demo",
        }),
      ).toThrow("does not match explicitly authorized target host 'other.flowcard.internal'");
    });
  });

  describe("seedSyntheticDemo execution", () => {
    it("fails closed when DATABASE_URL is empty", async () => {
      await expect(seedSyntheticDemo("")).rejects.toThrow("DATABASE_URL is required to apply synthetic seed");
    });

    it("fails closed when current_database() does not match flowcard_onboarding_test", async () => {
      const mockClient = {
        query: vi.fn(async (text: string) => {
          if (text.includes("current_database()")) {
            return { rows: [{ db_name: "wrong_db" }], rowCount: 1 };
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
        seedSyntheticDemo(
          `postgresql://mock:mock@127.0.0.1:${DISPOSABLE_TEST_PORT}/${DISPOSABLE_TEST_DB}`,
          undefined,
          mockFactory,
        ),
      ).rejects.toThrow("does not match expected target database 'flowcard_onboarding_test'");

      expect(mockClient.release).toHaveBeenCalledOnce();
      expect(mockPool.end).toHaveBeenCalledOnce();
    });

    it("applies synthetic seed with advisory lock and transaction wrapping using mock client", async () => {
      const executedQueries: Array<{ text: string; values?: readonly unknown[] }> = [];

      const mockClient = {
        query: vi.fn(async (text: string, _values?: readonly unknown[]) => {
          executedQueries.push({ text, values: _values });
          if (text.includes("current_database()")) {
            return { rows: [{ db_name: DISPOSABLE_TEST_DB }], rowCount: 1 };
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

      const result = await seedSyntheticDemo(
        `postgresql://mock:mock@127.0.0.1:${DISPOSABLE_TEST_PORT}/${DISPOSABLE_TEST_DB}`,
        undefined,
        mockFactory,
      );

      expect(result.success).toBe(true);

      // Verify advisory lock acquisition and release
      const lockQuery = executedQueries.find((q) => q.text.includes("pg_advisory_lock"));
      expect(lockQuery).toBeDefined();
      expect(lockQuery?.values).toEqual([SEED_ADVISORY_LOCK_ID]);

      const unlockQuery = executedQueries.find((q) => q.text.includes("pg_advisory_unlock"));
      expect(unlockQuery).toBeDefined();
      expect(unlockQuery?.values).toEqual([SEED_ADVISORY_LOCK_ID]);

      // Verify current_database verification
      expect(executedQueries.some((q) => q.text.includes("current_database()"))).toBe(true);

      // Verify BEGIN and COMMIT were invoked
      expect(executedQueries.some((q) => q.text === "BEGIN")).toBe(true);
      expect(executedQueries.some((q) => q.text === "COMMIT")).toBe(true);

      // Verify SQL content contains strictly synthetic aliases
      const seedSqlCall = executedQueries.find((q) => q.text.includes("facility_configs"));
      expect(seedSqlCall).toBeDefined();
      expect(seedSqlCall?.text).toContain("acct_bloom001");
      expect(seedSqlCall?.text).toContain("comp_synthetic_bloom");

      expect(mockPool.end).toHaveBeenCalledOnce();
      expect(mockClient.release).toHaveBeenCalledOnce();
    });

    it("rolls back transaction, releases advisory lock, and sanitizes errors on SQL failure", async () => {
      let rollbackCalled = false;
      let unlockCalled = false;

      const mockClient = {
        query: vi.fn(async (text: string, _values?: readonly unknown[]) => {
          if (text === "ROLLBACK") rollbackCalled = true;
          if (text.includes("pg_advisory_unlock")) unlockCalled = true;
          if (text.includes("current_database()")) {
            return { rows: [{ db_name: DISPOSABLE_TEST_DB }], rowCount: 1 };
          }
          if (text.includes("facility_configs")) {
            throw new Error("violates unique constraint with password=sensitive_secret");
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
        seedSyntheticDemo(
          `postgresql://mock:mock@127.0.0.1:${DISPOSABLE_TEST_PORT}/${DISPOSABLE_TEST_DB}`,
          undefined,
          mockFactory,
        ),
      ).rejects.toThrow("violates unique constraint with password=***");

      expect(rollbackCalled).toBe(true);
      expect(unlockCalled).toBe(true);
      expect(mockClient.release).toHaveBeenCalledOnce();
      expect(mockPool.end).toHaveBeenCalledOnce();
    });
  });
});
