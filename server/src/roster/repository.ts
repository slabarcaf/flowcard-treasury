import type { DatabaseClient, DatabasePool } from "../db/pool.js";
import type { CustomerAccountMappingChange, CustomerRosterEntry, EnrollCustomerInput, MapCustomerAccountInput } from "./types.js";

interface RosterRow {
  customer_id: string;
  salesforce_account_id: string;
  airwallex_connected_account_id: string;
  is_active: boolean;
  enrolled_at: Date;
  enrolled_by: string;
  enrollment_reason: string;
  deactivated_at: Date | null;
  deactivated_by: string | null;
  deactivation_reason: string | null;
  updated_at: Date;
}

const SELECT_COLUMNS = `customer_id, salesforce_account_id, airwallex_connected_account_id,
  is_active, enrolled_at, enrolled_by, enrollment_reason, deactivated_at,
  deactivated_by, deactivation_reason, updated_at`;

export function mapRosterRow(row: RosterRow): CustomerRosterEntry {
  return {
    customerId: row.customer_id,
    salesforceAccountId: row.salesforce_account_id,
    airwallexConnectedAccountId: row.airwallex_connected_account_id,
    isActive: row.is_active,
    enrolledAt: row.enrolled_at,
    enrolledBy: row.enrolled_by,
    enrollmentReason: row.enrollment_reason,
    deactivatedAt: row.deactivated_at,
    deactivatedBy: row.deactivated_by,
    deactivationReason: row.deactivation_reason,
    updatedAt: row.updated_at,
  };
}

export async function enrollCustomer(
  client: DatabaseClient,
  input: EnrollCustomerInput,
): Promise<CustomerRosterEntry> {
  const result = await client.query<RosterRow>(
    `INSERT INTO customer_roster (
       customer_id, salesforce_account_id, airwallex_connected_account_id,
       enrolled_by, enrollment_reason
     ) VALUES ($1, $2, $3, $4, $5)
     RETURNING ${SELECT_COLUMNS}`,
    [input.customerId, input.salesforceAccountId, input.airwallexConnectedAccountId, input.actor, input.reason],
  );
  const customer = result.rows[0];
  if (!customer) throw new Error("Customer enrollment did not return a roster row");
  await client.query(
    `INSERT INTO customer_roster_audit (customer_id, action, actor, reason)
     VALUES ($1, 'enrolled', $2, $3)`,
    [input.customerId, input.actor, input.reason],
  );
  return mapRosterRow(customer);
}

export async function softDeactivateCustomer(
  client: DatabaseClient,
  input: { customerId: string; actor: string; reason: string },
): Promise<boolean> {
  const result = await client.query(
    `UPDATE customer_roster
        SET is_active = false, deactivated_at = now(), deactivated_by = $2,
            deactivation_reason = $3, updated_at = now()
      WHERE customer_id = $1 AND is_active
      RETURNING customer_id`,
    [input.customerId, input.actor, input.reason],
  );
  if (result.rowCount !== 1) return false;
  await client.query(
    `INSERT INTO customer_roster_audit (customer_id, action, actor, reason)
     VALUES ($1, 'deactivated', $2, $3)`,
    [input.customerId, input.actor, input.reason],
  );
  return true;
}

/** Replace a seeded or stale connected-account mapping and record the old and new values atomically. */
export async function mapCustomerConnectedAccount(
  client: DatabaseClient,
  input: MapCustomerAccountInput,
): Promise<CustomerAccountMappingChange> {
  const selected = await client.query<{ customer_id: string; airwallex_connected_account_id: string }>(
    `SELECT customer_id, airwallex_connected_account_id
       FROM customer_roster
      WHERE customer_id = $1 AND is_active
      FOR UPDATE`,
    [input.customerId],
  );
  const customer = selected.rows[0];
  if (!customer) throw new Error(`Active enrolled customer not found: ${input.customerId}`);
  if (customer.airwallex_connected_account_id === input.airwallexConnectedAccountId) {
    throw new Error("Connected-account ID is already mapped to this customer");
  }

  const activeBinding = await client.query(
    `SELECT card_id FROM issuing_card_bindings
      WHERE customer_id = $1 AND is_active
      LIMIT 1 FOR UPDATE`,
    [input.customerId],
  );
  if (activeBinding.rowCount !== 0) {
    throw new Error("Deactivate active card bindings before changing the connected-account mapping");
  }

  const updated = await client.query(
    `UPDATE customer_roster
        SET airwallex_connected_account_id = $2, updated_at = now()
      WHERE customer_id = $1 AND is_active
      RETURNING customer_id`,
    [input.customerId, input.airwallexConnectedAccountId],
  );
  if (updated.rowCount !== 1) throw new Error(`Active enrolled customer not found: ${input.customerId}`);
  await client.query(
    `INSERT INTO customer_roster_account_mapping_audit (
       customer_id, old_connected_account_id, new_connected_account_id,
       actor, reason, changed_at
     ) VALUES ($1, $2, $3, $4, $5, now())`,
    [input.customerId, customer.airwallex_connected_account_id, input.airwallexConnectedAccountId,
      input.actor, input.reason],
  );
  return {
    customerId: customer.customer_id,
    oldAccountId: customer.airwallex_connected_account_id,
    newAccountId: input.airwallexConnectedAccountId,
  };
}

export async function listCustomers(pool: DatabasePool): Promise<CustomerRosterEntry[]> {
  const result = await pool.query<RosterRow>(
    `SELECT ${SELECT_COLUMNS} FROM customer_roster ORDER BY customer_id`,
  );
  return result.rows.map(mapRosterRow);
}

export async function getCustomerRosterEntry(
  client: Pick<DatabasePool, "query">,
  customerId: string,
): Promise<CustomerRosterEntry | null> {
  const result = await client.query<RosterRow>(
    `SELECT ${SELECT_COLUMNS} FROM customer_roster WHERE customer_id = $1`,
    [customerId],
  );
  return result.rows[0] ? mapRosterRow(result.rows[0]) : null;
}

export async function listActiveCustomersForSnapshots(
  pool: DatabasePool,
  page: { afterCustomerId: string | null; limit: number } = { afterCustomerId: null, limit: 1_000 },
): Promise<CustomerRosterEntry[]> {
  if (!Number.isSafeInteger(page.limit) || page.limit < 1 || page.limit > 1_000) {
    throw new Error("Active roster page limit must be from 1 through 1,000");
  }
  const result = await pool.query<RosterRow>(
    `SELECT ${SELECT_COLUMNS} FROM customer_roster
      WHERE is_active AND ($1::text IS NULL OR customer_id > $1)
      ORDER BY customer_id LIMIT $2`, [page.afterCustomerId, page.limit],
  );
  return result.rows.map(mapRosterRow);
}

/** Bind a provider card to the roster customer owning its connected account. */
export async function bindIssuingCard(
  pool: DatabasePool,
  input: { customerId: string; cardId: string; accountId: string; actor: string; reason: string },
): Promise<void> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const customer = await client.query(
      `SELECT customer_id FROM customer_roster
        WHERE customer_id = $1 AND airwallex_connected_account_id = $2 AND is_active FOR UPDATE`,
      [input.customerId, input.accountId],
    );
    if (customer.rowCount !== 1) throw new Error("Card binding does not match an active roster account");
    const result = await client.query(
      `INSERT INTO issuing_card_bindings (card_id, customer_id, airwallex_account_id, bound_by, binding_reason)
       VALUES ($1, $2, $3, $4, $5)
       ON CONFLICT (card_id) DO UPDATE SET is_active = true, bound_by = EXCLUDED.bound_by,
         binding_reason = EXCLUDED.binding_reason, bound_at = now()
       WHERE issuing_card_bindings.customer_id = EXCLUDED.customer_id
         AND issuing_card_bindings.airwallex_account_id = EXCLUDED.airwallex_account_id
         AND NOT issuing_card_bindings.is_active
       RETURNING card_id`,
      [input.cardId, input.customerId, input.accountId, input.actor, input.reason],
    );
    if (result.rowCount !== 1) throw new Error("Card already has a binding; deactivate it before rebinding");
    await client.query(
      `INSERT INTO issuing_card_binding_audit (card_id, customer_id, airwallex_account_id, action, actor, reason)
       VALUES ($1, $2, $3, 'bound', $4, $5)`,
      [input.cardId, input.customerId, input.accountId, input.actor, input.reason],
    );
    await client.query("COMMIT");
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

export async function deactivateIssuingCardBinding(
  pool: DatabasePool,
  input: { cardId: string; actor: string; reason: string },
): Promise<boolean> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const result = await client.query<{ card_id: string; customer_id: string; airwallex_account_id: string }>(
      `UPDATE issuing_card_bindings SET is_active = false
        WHERE card_id = $1 AND is_active
        RETURNING card_id, customer_id, airwallex_account_id`, [input.cardId]);
    const row = result.rows[0];
    if (!row) {
      await client.query("COMMIT");
      return false;
    }
    await client.query(
      `INSERT INTO issuing_card_binding_audit (card_id, customer_id, airwallex_account_id, action, actor, reason)
       VALUES ($1, $2, $3, 'deactivated', $4, $5)`,
      [row.card_id, row.customer_id, row.airwallex_account_id, input.actor, input.reason],
    );
    await client.query("COMMIT");
    return true;
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}
