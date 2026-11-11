import type { DatabasePool } from "../db/pool.js";
import { enrollCustomer as insertCustomer, listActiveCustomersForSnapshots, listCustomers, mapCustomerConnectedAccount as mapAccount, softDeactivateCustomer as deactivate } from "./repository.js";
import type { CustomerAccountMappingChange, CustomerRosterEntry, DeactivateCustomerInput, EnrollCustomerInput, MapCustomerAccountInput } from "./types.js";

function required(value: string, field: string): string {
  const normalized = value.trim();
  if (!normalized) throw new Error(`${field} is required`);
  return normalized;
}

function validateEnrollment(input: EnrollCustomerInput): EnrollCustomerInput {
  return {
    customerId: required(input.customerId, "customerId"),
    salesforceAccountId: required(input.salesforceAccountId, "salesforceAccountId"),
    airwallexConnectedAccountId: required(input.airwallexConnectedAccountId, "airwallexConnectedAccountId"),
    actor: required(input.actor, "actor"),
    reason: required(input.reason, "reason"),
  };
}

function validateDeactivation(input: DeactivateCustomerInput): DeactivateCustomerInput {
  return {
    customerId: required(input.customerId, "customerId"),
    actor: required(input.actor, "actor"),
    reason: required(input.reason, "reason"),
  };
}

async function inTransaction<T>(
  pool: DatabasePool,
  operation: (client: Awaited<ReturnType<DatabasePool["connect"]>>) => Promise<T>,
  uniqueViolationMessage = "Customer ID and provider account IDs must be unique across the roster",
): Promise<T> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const result = await operation(client);
    await client.query("COMMIT");
    return result;
  } catch (error) {
    await client.query("ROLLBACK");
    if (isUniqueViolation(error)) {
      throw new Error(uniqueViolationMessage);
    }
    throw error;
  } finally {
    client.release();
  }
}

function isUniqueViolation(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "23505";
}

export async function enrollCustomer(pool: DatabasePool, rawInput: EnrollCustomerInput): Promise<CustomerRosterEntry> {
  const input = validateEnrollment(rawInput);
  return inTransaction(pool, (client) => insertCustomer(client, input));
}

export async function deactivateCustomer(pool: DatabasePool, rawInput: DeactivateCustomerInput): Promise<void> {
  const input = validateDeactivation(rawInput);
  const changed = await inTransaction(pool, (client) => deactivate(client, input));
  if (!changed) throw new Error(`Active enrolled customer not found: ${input.customerId}`);
}

export async function mapCustomerAccount(
  pool: DatabasePool,
  rawInput: MapCustomerAccountInput,
): Promise<CustomerAccountMappingChange> {
  const input = {
    customerId: required(rawInput.customerId, "customerId"),
    airwallexConnectedAccountId: required(rawInput.airwallexConnectedAccountId, "airwallexConnectedAccountId"),
    actor: required(rawInput.actor, "actor"),
    reason: required(rawInput.reason, "reason"),
  };
  if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$/.test(input.airwallexConnectedAccountId)) {
    throw new Error("airwallexConnectedAccountId must be a valid provider ID");
  }
  if (input.airwallexConnectedAccountId === input.customerId) {
    throw new Error("airwallexConnectedAccountId must differ from the internal customer ID");
  }
  return inTransaction(pool, (client) => mapAccount(client, input),
    "Connected-account ID is already assigned to a roster customer");
}

export { listActiveCustomersForSnapshots, listCustomers };
