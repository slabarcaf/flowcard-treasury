import { describe, expect, it, beforeAll, afterAll } from "vitest";
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import pg from "pg";
import {
  runNodeMigrations,
  NODE_MIGRATION_ADVISORY_LOCK_ID,
} from "../../server/src/db/migrate.js";
import {
  seedSyntheticDemo,
  resolveSeedSqlPath,
  SEED_ADVISORY_LOCK_ID,
  validateDatabaseSafety,
} from "../../server/src/db/seed.js";

const TEST_DB_URL =
  process.env.POLICY_ACCEPTANCE_DATABASE_URL ||
  "postgres://test_onboarding:test_password@127.0.0.1:54329/flowcard_onboarding_test?sslmode=disable";

describe("Real PostgreSQL Integration (Disposable Container 54329)", () => {
  let pool: pg.Pool;
  let leaseClient: pg.PoolClient;

  beforeAll(async () => {
    // Validate safety guards before any connection is opened
    validateDatabaseSafety(TEST_DB_URL);

    pool = new pg.Pool({ connectionString: TEST_DB_URL });
    // Acquire common package lease so parallel Go / Node tests cannot race
    leaseClient = await pool.connect();
    await leaseClient.query("SELECT pg_advisory_lock($1)", [SEED_ADVISORY_LOCK_ID]);

    const res = await pool.query<{ db: string; port: number }>(
      "SELECT current_database() as db, inet_server_port() as port",
    );
    expect(res.rows[0].db).toBe("flowcard_onboarding_test");
    expect(res.rows[0].port).toBe(5432); // container internal port mapped to host 54329
  });

  afterAll(async () => {
    if (leaseClient) {
      await leaseClient.query("SELECT pg_advisory_unlock($1)", [SEED_ADVISORY_LOCK_ID]);
      leaseClient.release();
    }
    // Verify advisory lock is completely released after suite teardown
    const check = await pool.query<{ unlocked: boolean }>(
      "SELECT pg_try_advisory_lock($1) as unlocked",
      [SEED_ADVISORY_LOCK_ID],
    );
    expect(check.rows[0].unlocked).toBe(true);
    await pool.query("SELECT pg_advisory_unlock($1)", [SEED_ADVISORY_LOCK_ID]);
    await pool.end();
  });

  describe("Safety Guard Enforcement", () => {
    it("prohibits targeting remote hosts without explicit configuration", () => {
      expect(() =>
        validateDatabaseSafety("postgres://user:pass@remote-rds.amazonaws.com:54329/flowcard_onboarding_test"),
      ).toThrow(/SAFETY CHECK FAILED: automated seed prohibits remote host/);
    });

    it("prohibits non-test port 5432 by default", () => {
      expect(() =>
        validateDatabaseSafety("postgres://user:pass@127.0.0.1:5432/flowcard_onboarding_test"),
      ).toThrow(/SAFETY CHECK FAILED: port 5432 does not match explicitly authorized target port \(54329\)/);
    });

    it("prohibits non-test database names by default", () => {
      expect(() =>
        validateDatabaseSafety("postgres://user:pass@127.0.0.1:54329/flowcard_treasury"),
      ).toThrow(/SAFETY CHECK FAILED: database name 'flowcard_treasury' does not match/);
    });
  });

  describe("Node Database Migrations on Real PostgreSQL", () => {
    it("applies migrations idempotently and records checksums", async () => {
      // First run: all 21 migrations are tracked
      const results1 = await runNodeMigrations(TEST_DB_URL);
      expect(results1.length).toBe(21);
      const appliedOrExisting = results1.every((r) => r.version && r.name);
      expect(appliedOrExisting).toBe(true);

      // Verify schema_migrations table records
      const migCheck = await pool.query<{ count: string; null_checksums: string }>(
        "SELECT count(*) as count, count(*) FILTER (WHERE checksum IS NULL) as null_checksums FROM schema_migrations",
      );
      expect(Number.parseInt(migCheck.rows[0].count, 10)).toBeGreaterThanOrEqual(21);
      expect(Number.parseInt(migCheck.rows[0].null_checksums, 10)).toBe(0);

      // Second run: idempotency check (all 21 skipped)
      const results2 = await runNodeMigrations(TEST_DB_URL);
      expect(results2.length).toBe(21);
      expect(results2.filter((r) => r.applied).length).toBe(0);
      expect(results2.filter((r) => !r.applied).length).toBe(21);
    });

    it("serializes concurrent migrations on first-apply with exactly one execution", async () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "first-apply-concurrency-"));
      try {
        // Create 2 fresh unapplied migration files
        writeFileSync(
          join(tmpDir, "888_fresh_concurrency_table1.sql"),
          "CREATE TABLE IF NOT EXISTS fresh_concurrency_table1 (id text PRIMARY KEY, created_at timestamptz DEFAULT now());",
        );
        writeFileSync(
          join(tmpDir, "889_fresh_concurrency_table2.sql"),
          "CREATE TABLE IF NOT EXISTS fresh_concurrency_table2 (id text PRIMARY KEY, created_at timestamptz DEFAULT now());",
        );

        // Run two concurrent migration runners racing on unapplied migrations
        const [runA, runB] = await Promise.all([
          runNodeMigrations(TEST_DB_URL, tmpDir),
          runNodeMigrations(TEST_DB_URL, tmpDir),
        ]);

        const appliedA = runA.filter((r) => r.applied).length;
        const appliedB = runB.filter((r) => r.applied).length;

        // Exactly one runner must win the advisory lock and apply both; the other must find them applied and skip both
        expect(appliedA + appliedB).toBe(2);
        expect((appliedA === 2 && appliedB === 0) || (appliedA === 0 && appliedB === 2)).toBe(true);

        // Verify tables and tracking records exist
        const tblCheck = await pool.query<{ count: string }>(
          "SELECT count(*) as count FROM information_schema.tables WHERE table_name IN ('fresh_concurrency_table1', 'fresh_concurrency_table2')",
        );
        expect(Number.parseInt(tblCheck.rows[0].count, 10)).toBe(2);

        const trackCheck = await pool.query<{ count: string }>(
          "SELECT count(*) as count FROM schema_migrations WHERE version IN ('888', '889')",
        );
        expect(Number.parseInt(trackCheck.rows[0].count, 10)).toBe(2);
      } finally {
        await pool.query("DROP TABLE IF EXISTS fresh_concurrency_table1, fresh_concurrency_table2;");
        await pool.query("DELETE FROM schema_migrations WHERE version IN ('888', '889');");
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it("rolls back earlier DDL and tracking atomically when a later statement in a migration fails", async () => {
      const tmpDir = mkdtempSync(join(tmpdir(), "failing-atomicity-"));
      try {
        // Create a multi-statement migration where earlier statements succeed but a later statement fails
        writeFileSync(
          join(tmpDir, "999_failing_later_statement.sql"),
          `
          CREATE TABLE test_rollback_atomicity (id text PRIMARY KEY, name text NOT NULL);
          INSERT INTO test_rollback_atomicity (id, name) VALUES ('step_1', 'initial_val');
          -- Intentionally invalid SQL statement that fails after DDL and DML above
          SELECT syntax_error_fails_transaction FROM non_existent_rollback_atomicity_table;
          `,
        );

        await expect(runNodeMigrations(TEST_DB_URL, tmpDir)).rejects.toThrow(
          /Migration 999_failing_later_statement.sql failed/,
        );

        // Verify that earlier DDL was completely rolled back: table must NOT exist
        const tblCheck = await pool.query(
          "SELECT 1 FROM information_schema.tables WHERE table_name = 'test_rollback_atomicity'",
        );
        expect(tblCheck.rowCount).toBe(0);

        // Verify that failed migration was NOT recorded in schema_migrations
        const rec = await pool.query("SELECT 1 FROM schema_migrations WHERE version = '999'");
        expect(rec.rowCount).toBe(0);

        // Verify advisory lock was cleanly released and can be acquired immediately
        const lockCheck = await pool.query<{ unlocked: boolean }>(
          "SELECT pg_try_advisory_lock($1) as unlocked",
          [NODE_MIGRATION_ADVISORY_LOCK_ID],
        );
        expect(lockCheck.rows[0].unlocked).toBe(true);
        await pool.query("SELECT pg_advisory_unlock($1)", [NODE_MIGRATION_ADVISORY_LOCK_ID]);
      } finally {
        await pool.query("DROP TABLE IF EXISTS test_rollback_atomicity;");
        rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });

  describe("Synthetic Demonstration Fixture Seed on Real PostgreSQL", () => {
    it("preserves another active facility and seeds a default only when no active row exists", async () => {
      const seedSql = readFileSync(resolveSeedSqlPath(), "utf8");
      const readFacilities = async () => (await leaseClient.query(
        `SELECT id, version, currency, facility_amount_cents, reserve_floor_bps,
                operating_buffer_cents, is_active, created_at::text AS created_at
           FROM facility_configs ORDER BY id`,
      )).rows;

      await leaseClient.query("BEGIN");
      try {
        // These rows are transaction-owned fixtures. ROLLBACK restores any
        // facility_configs rows, including an existing usd-primary row.
        await leaseClient.query("DELETE FROM facility_configs");
        await leaseClient.query(`
          INSERT INTO facility_configs
            (id, version, currency, facility_amount_cents, reserve_floor_bps, operating_buffer_cents, is_active)
          VALUES ('lead-review-active', 9, 'USD', 9000000, 1700, 0, true)
        `);
        const activeBeforeSeed = await readFacilities();
        expect(activeBeforeSeed).toHaveLength(1);
        await leaseClient.query(seedSql);
        expect(await readFacilities()).toEqual(activeBeforeSeed);
        await leaseClient.query(seedSql);
        expect(await readFacilities()).toEqual(activeBeforeSeed);

        // With no active row and no seeded ID, the synthetic default is added.
        await leaseClient.query("DELETE FROM facility_configs");
        await leaseClient.query(seedSql);
        const freshRows = await readFacilities();
        expect(freshRows).toHaveLength(1);
        expect(freshRows[0]).toMatchObject({
          id: "usd-primary",
          version: 1,
          currency: "USD",
          facility_amount_cents: "5000000",
          reserve_floor_bps: 2000,
          operating_buffer_cents: "0",
          is_active: true,
        });
        await leaseClient.query(seedSql);
        expect(await readFacilities()).toEqual(freshRows);

        // Preserve an existing inactive operator row and leave it inactive.
        await leaseClient.query("DELETE FROM facility_configs");
        await leaseClient.query(`
          INSERT INTO facility_configs
            (id, version, currency, facility_amount_cents, reserve_floor_bps, operating_buffer_cents, is_active)
          VALUES ('operator-inactive', 4, 'USD', 7000000, 1500, 0, false)
        `);
        const inactiveBeforeSeed = await readFacilities();
        await leaseClient.query(seedSql);
        const inactiveRows = await readFacilities();
        expect(inactiveRows.find((row: { id: string }) => row.id === "operator-inactive")).toEqual(inactiveBeforeSeed[0]);
        expect(inactiveRows.find((row: { id: string }) => row.id === "usd-primary")).toMatchObject({
          is_active: true,
          facility_amount_cents: "5000000",
        });
        await leaseClient.query(seedSql);
        expect(await readFacilities()).toEqual(inactiveRows);
      } finally {
        await leaseClient.query("ROLLBACK");
      }
    });

    it("applies synthetic seed non-destructively, preserving existing progress byte-for-byte", async () => {
      const initialFacilityRows = await pool.query(
        `SELECT id, version, currency, facility_amount_cents, reserve_floor_bps,
                operating_buffer_cents, is_active, created_at::text AS created_at
           FROM facility_configs ORDER BY id`,
      );
      let facilityId = "usd-primary";
      try {
        // Reset only synthetic onboarding fixtures. Do not edit facility settings.
        await pool.query(`
          DELETE FROM onboarding.applications WHERE id IN ('app_synthetic_bloom', 'app_synthetic_atlas');
          DELETE FROM onboarding.companies WHERE id IN ('comp_synthetic_bloom', 'comp_synthetic_atlas');
        `);

        // 1. Initial seed execution reusing lease client (avoids deadlock)
        const seed1 = await seedSyntheticDemo(TEST_DB_URL, undefined, undefined, undefined, leaseClient);
        expect(seed1.success).toBe(true);

        const facilityRowsAfterSeed = await pool.query<{ id: string; is_active: boolean }>(
          "SELECT id, is_active FROM facility_configs ORDER BY id",
        );
        const originalActive = initialFacilityRows.rows.find((row: { is_active: boolean }) => row.is_active);
        const originalDefault = initialFacilityRows.rows.find((row: { id: string }) => row.id === "usd-primary");
        if (originalActive || originalDefault) {
          expect(facilityRowsAfterSeed.rows.map((row) => row.id)).toEqual(initialFacilityRows.rows.map((row: { id: string }) => row.id));
          facilityId = originalActive?.id ?? "usd-primary";
        } else {
          expect(facilityRowsAfterSeed.rows.map((row) => row.id)).toContain("usd-primary");
          facilityId = "usd-primary";
        }
        for (const originalRow of initialFacilityRows.rows) {
          expect(await pool.query(
            `SELECT id, version, currency, facility_amount_cents, reserve_floor_bps,
                    operating_buffer_cents, is_active, created_at::text AS created_at
               FROM facility_configs WHERE id = $1`,
            [originalRow.id],
          ).then((result) => result.rows[0])).toEqual(originalRow);
        }

        // 2. Simulate user progress in the demo by mutating records via valid domain transitions
        await pool.query(`
          UPDATE onboarding.companies
             SET legal_name = 'Bloom Progressed Holdings Inc.',
                 revision = 5,
                 updated_at = '2026-11-11 10:30:00+00'
           WHERE id = 'comp_synthetic_bloom';

          UPDATE onboarding.applications
             SET status = 'UNDER_REVIEW',
                 decision = NULL,
                 revision = 3,
                 company_snapshot = jsonb_set(company_snapshot, '{legal_name}', '"Bloom Progressed Snapshot Invariant"'),
                 updated_at = '2026-11-11 10:35:00+00'
           WHERE id = 'app_synthetic_bloom';
        `);
        await pool.query("UPDATE facility_configs SET facility_amount_cents = $1 WHERE id = $2", [9999999, facilityId]);

        // 3. Re-run synthetic demonstration seed (replay test)
        const seed2 = await seedSyntheticDemo(TEST_DB_URL, undefined, undefined, undefined, leaseClient);
        expect(seed2.success).toBe(true);

        // 4. Assert non-destructive idempotency: existing user progress is preserved byte-for-byte
        const compRes = await pool.query<{ legal_name: string; revision: number; updated_at: Date }>(
          "SELECT legal_name, revision, updated_at FROM onboarding.companies WHERE id = 'comp_synthetic_bloom'",
        );
        expect(compRes.rows[0].legal_name).toBe("Bloom Progressed Holdings Inc.");
        expect(compRes.rows[0].revision).toBe(5);

        const appRes = await pool.query<{
          status: string;
          decision: string | null;
          revision: number;
          snapshot_legal_name: string;
        }>(
          "SELECT status, decision, revision, company_snapshot->>'legal_name' as snapshot_legal_name FROM onboarding.applications WHERE id = 'app_synthetic_bloom'",
        );
        expect(appRes.rows[0].status).toBe("UNDER_REVIEW");
        expect(appRes.rows[0].decision).toBeNull();
        expect(appRes.rows[0].revision).toBe(3);
        expect(appRes.rows[0].snapshot_legal_name).toBe("Bloom Progressed Snapshot Invariant");

        const facilityRes = await pool.query<{ facility_amount_cents: string }>(
          "SELECT facility_amount_cents FROM facility_configs WHERE id = $1", [facilityId],
        );
        expect(Number.parseInt(facilityRes.rows[0].facility_amount_cents, 10)).toBe(9999999);

        // 5. Verify periodic background worker reconciliation isolation:
        // Fictional platform accounts must NEVER be placed in the live polling table.
        const paCheck = await pool.query<{ count: string }>(
          "SELECT count(*) as count FROM onboarding.platform_accounts WHERE external_account_id = 'acct_bloom001'",
        );
        expect(Number.parseInt(paCheck.rows[0].count, 10)).toBe(0);

        const livePollCheck = await pool.query<{ count: string }>(
          "SELECT count(*) as count FROM onboarding.platform_accounts WHERE external_account_id IS NOT NULL AND external_account_id != ''",
        );
        expect(Number.parseInt(livePollCheck.rows[0].count, 10)).toBe(0);
      } finally {
        // Restore pristine baseline fixtures in safe foreign-key order
        await pool.query(`
          DELETE FROM onboarding.applications WHERE id IN ('app_synthetic_bloom', 'app_synthetic_atlas');
          DELETE FROM onboarding.companies WHERE id IN ('comp_synthetic_bloom', 'comp_synthetic_atlas');
        `);
        await seedSyntheticDemo(TEST_DB_URL, undefined, undefined, undefined, leaseClient);

        // Restore the exact pre-test facility rows. This includes any existing
        // usd-primary row and prevents fixture cleanup from overwriting it.
        await leaseClient.query("BEGIN");
        try {
          await leaseClient.query("UPDATE facility_configs SET is_active = false");
          const originalIds = initialFacilityRows.rows.map((row: { id: string }) => row.id);
          await leaseClient.query("DELETE FROM facility_configs WHERE NOT (id = ANY($1::text[]))", [originalIds]);
          for (const row of initialFacilityRows.rows) {
            await leaseClient.query(`
              INSERT INTO facility_configs
                (id, version, currency, facility_amount_cents, reserve_floor_bps, operating_buffer_cents, is_active, created_at)
              VALUES ($1, $2, $3, $4, $5, $6, $7, $8::timestamptz)
              ON CONFLICT (id) DO UPDATE SET
                version = EXCLUDED.version,
                currency = EXCLUDED.currency,
                facility_amount_cents = EXCLUDED.facility_amount_cents,
                reserve_floor_bps = EXCLUDED.reserve_floor_bps,
                operating_buffer_cents = EXCLUDED.operating_buffer_cents,
                is_active = EXCLUDED.is_active,
                created_at = EXCLUDED.created_at
            `, [row.id, row.version, row.currency, row.facility_amount_cents, row.reserve_floor_bps, row.operating_buffer_cents, row.is_active, row.created_at]);
          }
          await leaseClient.query("COMMIT");
        } catch (restoreErr) {
          await leaseClient.query("ROLLBACK");
          throw restoreErr;
        }
      }
    });
  });
});
