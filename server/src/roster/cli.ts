import { createDatabasePool } from "../db/pool.js";
import { deactivateCustomer, enrollCustomer, listCustomers } from "./service.js";

function options(args: string[]): Map<string, string> {
  const parsed = new Map<string, string>();
  for (let index = 0; index < args.length; index += 1) {
    const name = args[index];
    const value = args[index + 1];
    if (!name?.startsWith("--") || !value || value.startsWith("--")) {
      throw new Error(`Expected --option value near: ${name ?? "<end>"}`);
    }
    if (parsed.has(name)) throw new Error(`Duplicate option: ${name}`);
    parsed.set(name, value);
    index += 1;
  }
  return parsed;
}

function required(parsed: Map<string, string>, name: string): string {
  const value = parsed.get(name)?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function run(): Promise<void> {
  const [command, ...args] = process.argv.slice(2);
  if (!command || !["enroll", "deactivate", "list"].includes(command)) {
    throw new Error("Usage: roster:cli <enroll|deactivate|list> [--options]");
  }
  if (!process.env.DATABASE_URL?.trim()) throw new Error("DATABASE_URL is required");
  const parsed = options(args);
  const pool = await createDatabasePool({ databaseUrl: process.env.DATABASE_URL });
  try {
    if (command === "enroll") {
      const entry = await enrollCustomer(pool, {
        customerId: required(parsed, "--customer-id"),
        salesforceAccountId: required(parsed, "--salesforce-account-id"),
        airwallexConnectedAccountId: required(parsed, "--airwallex-account-id"),
        actor: required(parsed, "--actor"),
        reason: required(parsed, "--reason"),
      });
      process.stdout.write(`${JSON.stringify(entry, null, 2)}\n`);
      return;
    }
    if (command === "deactivate") {
      await deactivateCustomer(pool, {
        customerId: required(parsed, "--customer-id"),
        actor: required(parsed, "--actor"),
        reason: required(parsed, "--reason"),
      });
      process.stdout.write("Customer deactivated. Existing bridge servicing remains available.\n");
      return;
    }
    process.stdout.write(`${JSON.stringify(await listCustomers(pool), null, 2)}\n`);
  } finally {
    await pool.end();
  }
}

run().catch((error: unknown) => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
