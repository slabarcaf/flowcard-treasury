import { readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createDatabasePool, type DatabasePool, type DatabaseClient } from "./pool.js";
import { sanitizeDatabaseError } from "./migrate.js";

export const DISPOSABLE_TEST_PORT = 54329;
export const DISPOSABLE_TEST_DB = "flowcard_onboarding_test";
export const SEED_ADVISORY_LOCK_ID = 88888888;

export interface SeedTargetConfig {
  expectedHost?: string;
  expectedPort?: number;
  expectedDatabase?: string;
}

/**
 * Validates connection safety when targeting databases for seeding.
 * Strictly verifies the connection host, port, and database against explicit,
 * authorized target expectations (defaulting to local disposable test container 54329).
 * Never permits blanket remote connections.
 */
export function validateDatabaseSafety(databaseUrl: string, config?: SeedTargetConfig): void {
  const parsed = new URL(databaseUrl);
  const dbName = decodeURIComponent(parsed.pathname.slice(1));
  const hostname = parsed.hostname.toLowerCase();
  const port = parsed.port ? Number.parseInt(parsed.port, 10) : 5432;

  const expectedHost = (config?.expectedHost ?? "127.0.0.1").toLowerCase();
  const expectedPort = config?.expectedPort ?? DISPOSABLE_TEST_PORT;
  const expectedDatabase = config?.expectedDatabase ?? DISPOSABLE_TEST_DB;

  // Validate Host
  const isLoopbackExpected = ["127.0.0.1", "localhost", "::1"].includes(expectedHost);
  const isActualLoopback = ["127.0.0.1", "localhost", "::1"].includes(hostname);

  if (isLoopbackExpected) {
    if (!isActualLoopback) {
      throw new Error(
        `SAFETY CHECK FAILED: automated seed prohibits remote host '${hostname}'. Expected local loopback '${expectedHost}'.`,
      );
    }
  } else {
    if (hostname !== expectedHost) {
      throw new Error(
        `SAFETY CHECK FAILED: host '${hostname}' does not match explicitly authorized target host '${expectedHost}'.`,
      );
    }
  }

  // Validate Port
  if (port !== expectedPort) {
    throw new Error(
      `SAFETY CHECK FAILED: port ${port} does not match explicitly authorized target port (${expectedPort}).`,
    );
  }

  // Validate Database Name
  if (dbName !== expectedDatabase) {
    throw new Error(
      `SAFETY CHECK FAILED: database name '${dbName}' does not match explicitly authorized target database ('${expectedDatabase}').`,
    );
  }
}

/**
 * Resolves the absolute path to the synthetic demo SQL seed script.
 */
export function resolveSeedSqlPath(): string {
  const candidates = [
    resolve(fileURLToPath(new URL("./seeds/seed-synthetic-demo.sql", import.meta.url))),
    resolve(fileURLToPath(new URL("../../../server/src/db/seeds/seed-synthetic-demo.sql", import.meta.url))),
    resolve(fileURLToPath(new URL("../../../infra/scripts/seed-synthetic-demo.sql", import.meta.url))),
    resolve(process.cwd(), "server/src/db/seeds/seed-synthetic-demo.sql"),
    resolve(process.cwd(), ".dev/server/server/src/db/seeds/seed-synthetic-demo.sql"),
    resolve(process.cwd(), "dist/server/src/db/seeds/seed-synthetic-demo.sql"),
    resolve(process.cwd(), "infra/scripts/seed-synthetic-demo.sql"),
    resolve(process.cwd(), ".worktrees/aws-demo-deployment/server/src/db/seeds/seed-synthetic-demo.sql"),
  ];

  for (const candidate of candidates) {
    if (existsSync(candidate)) {
      return candidate;
    }
  }

  throw new Error(`Synthetic seed SQL file not found. Checked: ${candidates.join(", ")}`);
}

/**
 * Executes the idempotent synthetic demonstration seed.
 * Verifies live database identity on the connection in all modes before running SQL.
 */
export async function seedSyntheticDemo(
  databaseUrl: string,
  sqlFilePath?: string,
  poolFactory: (config: { databaseUrl: string }) => Promise<DatabasePool> = createDatabasePool,
  config?: SeedTargetConfig,
  clientOverride?: DatabaseClient | { query: (text: string, values?: readonly unknown[]) => Promise<any> },
): Promise<{ success: boolean; statementsExecuted: number }> {
  const trimmedUrl = databaseUrl.trim();
  if (!trimmedUrl) {
    throw new Error("DATABASE_URL is required to apply synthetic seed");
  }

  validateDatabaseSafety(trimmedUrl, config);

  const expectedDatabase = config?.expectedDatabase ?? DISPOSABLE_TEST_DB;
  const seedSqlPath = sqlFilePath ?? resolveSeedSqlPath();
  const sql = await readFile(seedSqlPath, "utf8");

  const pool = clientOverride ? null : await poolFactory({ databaseUrl: trimmedUrl });
  try {
    const client = (clientOverride as DatabaseClient) ?? (await pool!.connect());
    try {
      // 1. Acquire session-level advisory lock during seed execution if not reusing an existing client session
      if (!clientOverride) {
        await client.query("SELECT pg_advisory_lock($1)", [SEED_ADVISORY_LOCK_ID]);
      }
      try {
        // 2. Live database name verification on the active connection in all execution modes
        const dbCheck = await client.query<{ db_name: string }>("SELECT current_database() AS db_name");
        const currentDb = dbCheck.rows[0]?.db_name;
        if (currentDb !== expectedDatabase) {
          throw new Error(
            `SAFETY CHECK FAILED: connected live database '${currentDb}' does not match expected target database '${expectedDatabase}'`,
          );
        }

        // 3. Atomic execution wrapped in transaction
        await client.query("BEGIN");
        await client.query(sql);
        await client.query("COMMIT");
      } catch (sqlErr) {
        try {
          await client.query("ROLLBACK");
        } catch {
          // Ignore rollback failure if connection severed
        }
        throw new Error(`Synthetic seed execution failed: ${sanitizeDatabaseError(sqlErr)}`);
      } finally {
        if (!clientOverride) {
          try {
            await client.query("ROLLBACK");
          } catch {
            // Clean up any remaining transaction state before advisory unlock
          }
          await client.query("SELECT pg_advisory_unlock($1)", [SEED_ADVISORY_LOCK_ID]);
        }
      }
      return { success: true, statementsExecuted: 1 };
    } finally {
      if (!clientOverride) {
        client.release();
      }
    }
  } finally {
    if (pool) {
      await pool.end();
    }
  }
}

// CLI entrypoint when executed directly via `node dist/server/src/db/seed.js`
if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const dbUrl = process.env.DATABASE_URL || process.env.DATABASE_URL_HOST;
  if (!dbUrl) {
    process.stderr.write("error: seed requires DATABASE_URL or DATABASE_URL_HOST\n");
    process.exit(1);
  }

  // Parse explicit target CLI arguments (governance: no unapproved environment variables)
  const args = process.argv.slice(2);
  const targetConfig: SeedTargetConfig = {};

  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--target-host" && i + 1 < args.length) {
      targetConfig.expectedHost = args[++i];
    } else if (args[i] === "--target-port" && i + 1 < args.length) {
      targetConfig.expectedPort = Number.parseInt(args[++i], 10);
    } else if (args[i] === "--target-db" && i + 1 < args.length) {
      targetConfig.expectedDatabase = args[++i];
    }
  }

  seedSyntheticDemo(dbUrl, undefined, undefined, targetConfig)
    .then(() => {
      process.stdout.write("Synthetic demonstration seed applied successfully (idempotent).\n");
      process.exit(0);
    })
    .catch((err) => {
      process.stderr.write(`Synthetic seed failed: ${sanitizeDatabaseError(err)}\n`);
      process.exit(1);
    });
}
