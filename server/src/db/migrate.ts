import { readdir, readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { createDatabasePool, type DatabasePool } from "./pool.js";

export const NODE_MIGRATION_ADVISORY_LOCK_ID = 71829304;

export interface MigrationResult {
  version: string;
  name: string;
  applied: boolean;
}

export type DatabasePoolFactory = (config: { databaseUrl: string }) => Promise<DatabasePool>;

/**
 * Sanitizes connection strings and credentials from error messages.
 * Handles URI formats, key-value connection parameters, and quoted passwords.
 */
export function sanitizeDatabaseError(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return message
    // Match postgres(ql) URIs: postgresql://[user]:[password]@[host]
    .replace(/postgres(?:ql)?:\/\/[^@\s]+@/gi, "postgresql://***:***@")
    // Match quoted key-value passwords: password="secret" or password='secret'
    .replace(/password=(["'])[\s\S]*?\1/gi, "password=***")
    // Match unquoted key-value passwords: password=secret (up to space, semicolon, or end of string)
    .replace(/password=[^\s;]+/gi, "password=***");
}

/**
 * Computes a SHA-256 checksum for the provided SQL string.
 */
export function computeSqlChecksum(sql: string): string {
  return createHash("sha256").update(sql.trim(), "utf8").digest("hex");
}

/**
 * Detects standalone transaction control statements (BEGIN/COMMIT/ROLLBACK)
 * that would conflict with atomic outer transaction management.
 * Note: plpgsql blocks ($$ BEGIN ... END; $$) and DO $$ blocks are preserved.
 */
export function hasStandaloneTransaction(sql: string): boolean {
  const stripped = sql.replace(/\$\$[\s\S]*?\$\$/g, ""); // strip $$ plpgsql/DO blocks $$
  return /\b(BEGIN|COMMIT|ROLLBACK)\s*;/i.test(stripped);
}

/**
 * Resolves the absolute directory containing the SQL migration files.
 */
export function resolveMigrationsDirectory(): string {
  const candidates = [
    // 1. Relative to this module file (supports both src/ and dist/)
    resolve(fileURLToPath(new URL("../../../server/src/db/migrations", import.meta.url))),
    // 2. Relative to process working directory in container or repo root
    resolve(process.cwd(), "server/src/db/migrations"),
    resolve(process.cwd(), ".dev/server/server/src/db/migrations"),
    resolve(process.cwd(), "dist/server/src/db/migrations"),
  ];

  for (const dir of candidates) {
    if (existsSync(dir)) {
      return dir;
    }
  }

  throw new Error(`Migrations directory not found. Checked: ${candidates.join(", ")}`);
}

/**
 * Applies all pending Node database migrations in strict numerical order.
 * - Acquires a dedicated PostgreSQL advisory lock to serialize concurrent migrations.
 * - Idempotently tracks applied migrations and SHA-256 checksums in schema_migrations.
 * - Verifies checksum and name drift against previously applied migrations.
 * - Wraps migrations in atomic transactions with tracking; rejects untracked commits.
 * - Ensures transaction rollback before advisory unlock to prevent aborted-transaction lock leaks.
 */
export async function runNodeMigrations(
  databaseUrl: string,
  migrationsDir?: string,
  poolFactory: DatabasePoolFactory = createDatabasePool,
): Promise<MigrationResult[]> {
  const trimmedUrl = databaseUrl.trim();
  if (!trimmedUrl) {
    throw new Error("DATABASE_URL is required to run migrations");
  }

  let pool: DatabasePool;
  try {
    pool = await poolFactory({ databaseUrl: trimmedUrl });
  } catch (poolErr) {
    throw new Error(`Failed to initialize database pool: ${sanitizeDatabaseError(poolErr)}`);
  }

  try {
    let client;
    try {
      client = await pool.connect();
    } catch (connErr) {
      throw new Error(`Database connection failed: ${sanitizeDatabaseError(connErr)}`);
    }

    try {
      // 1. Dedicated session-level advisory lock to serialize concurrent releases
      await client.query("SELECT pg_advisory_lock($1)", [NODE_MIGRATION_ADVISORY_LOCK_ID]);

      try {
        // 2. Ensure schema_migrations table exists with checksum column
        await client.query(`
          CREATE TABLE IF NOT EXISTS schema_migrations (
            version text PRIMARY KEY,
            name text NOT NULL,
            checksum text,
            applied_at timestamptz NOT NULL DEFAULT now()
          );
        `);
        await client.query(`
          ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum text;
        `);

        // 3. Query already applied migrations
        const existingRes = await client.query<{ version: string; name: string; checksum: string | null }>(
          "SELECT version, name, checksum FROM schema_migrations ORDER BY version ASC",
        );
        const appliedRecords = new Map<string, { name: string; checksum: string | null }>();
        for (const row of existingRes.rows) {
          appliedRecords.set(row.version, { name: row.name, checksum: row.checksum });
        }

        // 4. Read migration files from disk
        const dir = migrationsDir ?? resolveMigrationsDirectory();
        const files = (await readdir(dir))
          .filter((f) => f.endsWith(".sql"))
          .sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));

        const results: MigrationResult[] = [];

        for (const file of files) {
          const match = /^(\d+)_/.exec(file);
          if (!match) continue;

          const version = match[1];
          const sql = await readFile(join(dir, file), "utf8");
          const checksum = computeSqlChecksum(sql);

          if (appliedRecords.has(version)) {
            const recorded = appliedRecords.get(version)!;

            // Name drift verification
            if (recorded.name !== file) {
              throw new Error(
                `Migration drift detected for version ${version}: recorded name "${recorded.name}" does not match disk file "${file}"`,
              );
            }

            // Checksum drift verification
            if (recorded.checksum && recorded.checksum !== checksum) {
              throw new Error(
                `Migration checksum drift detected for version ${version} (${file}): recorded checksum "${recorded.checksum}" does not match disk checksum "${checksum}"`,
              );
            }

            // Backfill checksum if historical record had null checksum
            if (!recorded.checksum) {
              await client.query("UPDATE schema_migrations SET checksum = $1 WHERE version = $2", [checksum, version]);
            }

            results.push({ version, name: file, applied: false });
            continue;
          }

          // 5. Standalone transaction control detection:
          // FlowCard migrations must be atomically tracked. Reject embedded top-level BEGIN/COMMIT
          // to eliminate crash gaps between DDL commits and tracking inserts.
          if (hasStandaloneTransaction(sql)) {
            throw new Error(
              `Migration ${file} contains standalone transaction control statements (BEGIN/COMMIT). Migrations must be atomic and transaction-managed by the runner.`,
            );
          }

          // 6. Apply new migration wrapped in atomic transaction with tracking insert
          await client.query("BEGIN");
          try {
            await client.query(sql);
            await client.query(
              "INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
              [version, file, checksum],
            );
            await client.query("COMMIT");
            results.push({ version, name: file, applied: true });
          } catch (migrationError) {
            try {
              await client.query("ROLLBACK");
            } catch {
              // Ignore rollback failure if connection severed
            }
            throw new Error(`Migration ${file} failed: ${sanitizeDatabaseError(migrationError)}`);
          }
        }

        return results;
      } finally {
        // Guarantee clean rollback of any active or aborted transaction block
        // before invoking pg_advisory_unlock, preventing 'current transaction is aborted' lock leaks.
        try {
          await client.query("ROLLBACK");
        } catch {
          // Ignore if no active transaction
        }
        await client.query("SELECT pg_advisory_unlock($1)", [NODE_MIGRATION_ADVISORY_LOCK_ID]);
      }
    } finally {
      client.release();
    }
  } finally {
    await pool.end();
  }
}

// CLI entrypoint when executed directly via `node dist/server/src/db/migrate.js`
if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const dbUrl = process.env.DATABASE_URL || process.env.DATABASE_URL_HOST;
  if (!dbUrl) {
    process.stderr.write("error: migration requires DATABASE_URL or DATABASE_URL_HOST\n");
    process.exit(1);
  }

  runNodeMigrations(dbUrl)
    .then((results) => {
      const appliedCount = results.filter((r) => r.applied).length;
      const skippedCount = results.filter((r) => !r.applied).length;
      process.stdout.write(
        `Node database migrations completed successfully: ${appliedCount} applied, ${skippedCount} skipped (already applied).\n`,
      );
      process.exit(0);
    })
    .catch((err) => {
      process.stderr.write(`Node database migration failed: ${sanitizeDatabaseError(err)}\n`);
      process.exit(1);
    });
}
