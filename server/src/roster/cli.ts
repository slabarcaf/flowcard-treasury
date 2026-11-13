import { createDatabasePool } from "../db/pool.js";
import { runRosterCommand } from "./commands.js";

async function run(): Promise<void> {
  const [command, ...args] = process.argv.slice(2);
  if (!command || !["enroll", "deactivate", "list", "bind-card", "unbind-card", "map-account"].includes(command)) {
    throw new Error("Usage: roster:cli <enroll|deactivate|list|bind-card|unbind-card|map-account> [--options]");
  }
  if (!process.env.DATABASE_URL?.trim()) throw new Error("DATABASE_URL is required");
  const pool = await createDatabasePool({ databaseUrl: process.env.DATABASE_URL });
  try {
    await runRosterCommand(pool, command, args);
  } finally {
    await pool.end();
  }
}

run().catch((error: unknown) => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
