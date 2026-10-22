import type { DatabaseClient, DatabasePool } from "../db/pool.js";
import type { CustomerRosterEntry, EnrollCustomerInput } from "./types.js";

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

export async function listActiveCustomersForSnapshots(pool: DatabasePool): Promise<CustomerRosterEntry[]> {
  const result = await pool.query<RosterRow>(
    `SELECT ${SELECT_COLUMNS} FROM customer_roster WHERE is_active ORDER BY customer_id`,
  );
  return result.rows.map(mapRosterRow);
}
