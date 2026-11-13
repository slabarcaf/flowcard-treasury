import type { DatabasePool } from "../db/pool.js";
import { bindIssuingCard, deactivateIssuingCardBinding } from "./repository.js";
import { deactivateCustomer, enrollCustomer, mapCustomerAccount, listCustomers } from "./service.js";

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

/** Execute a roster CLI command with an injected database and output writer. */
export async function runRosterCommand(
  pool: DatabasePool,
  command: string | undefined,
  args: string[],
  write: (text: string) => void = (text) => process.stdout.write(text),
): Promise<void> {
  if (!command || !["enroll", "deactivate", "list", "bind-card", "unbind-card", "map-account"].includes(command)) {
    throw new Error("Usage: roster:cli <enroll|deactivate|list|bind-card|unbind-card|map-account> [--options]");
  }
  const parsed = options(args);
  if (command === "enroll") {
    const entry = await enrollCustomer(pool, {
      customerId: required(parsed, "--customer-id"),
      salesforceAccountId: required(parsed, "--salesforce-account-id"),
      airwallexConnectedAccountId: required(parsed, "--airwallex-account-id"),
      actor: required(parsed, "--actor"),
      reason: required(parsed, "--reason"),
    });
    write(`${JSON.stringify(entry, null, 2)}\n`);
    return;
  }
  if (command === "deactivate") {
    await deactivateCustomer(pool, {
      customerId: required(parsed, "--customer-id"),
      actor: required(parsed, "--actor"),
      reason: required(parsed, "--reason"),
    });
    write("Customer deactivated. Existing bridge servicing remains available.\n");
    return;
  }
  if (command === "map-account") {
    const mapping = await mapCustomerAccount(pool, {
      customerId: required(parsed, "--customer-id"),
      airwallexConnectedAccountId: required(parsed, "--airwallex-account-id"),
      actor: required(parsed, "--actor"),
      reason: required(parsed, "--reason"),
    });
    write(`${JSON.stringify(mapping, null, 2)}\n`);
    return;
  }
  if (command === "bind-card") {
    await bindIssuingCard(pool, {
      customerId: required(parsed, "--customer-id"),
      accountId: required(parsed, "--airwallex-account-id"),
      cardId: required(parsed, "--card-id"),
      actor: required(parsed, "--actor"),
      reason: required(parsed, "--reason"),
    });
    write("Issuing card bound to the active roster customer.\n");
    return;
  }
  if (command === "unbind-card") {
    const deactivated = await deactivateIssuingCardBinding(pool, {
      cardId: required(parsed, "--card-id"),
      actor: required(parsed, "--actor"),
      reason: required(parsed, "--reason"),
    });
    if (!deactivated) throw new Error("Active card binding was not found");
    write("Issuing card binding deactivated.\n");
    return;
  }
  write(`${JSON.stringify(await listCustomers(pool), null, 2)}\n`);
}
