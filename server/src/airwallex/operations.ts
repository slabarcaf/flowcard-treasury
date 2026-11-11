import { randomUUID } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";

export type BridgeOperationType = "bridge" | "fee" | "principal";

/** Adds an operation and its command atomically; callers send only after this commits. */
export async function queueProviderOperation(
  pool: DatabasePool,
  input: { operationType: BridgeOperationType; bridgeId: string; amountCents: number; payload: Record<string, unknown>; operationId?: string },
): Promise<{ providerOperationId: string; requestId: string; outboxCommandId: number }> {
  if (!Number.isSafeInteger(input.amountCents) || input.amountCents <= 0) throw new Error("amountCents must be a positive safe integer");
  const prefix = input.operationType;
  const providerOperationId = input.operationId ?? randomUUID();
  const requestId = `${prefix}-${randomUUID()}`;
  if (requestId.length > 50) throw new Error("Provider request ID exceeds 50 characters");
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await client.query(
      `INSERT INTO provider_operations (id, bridge_id, operation_type, request_id, status, amount_cents, currency)
       VALUES ($1, $2, $3, $4, 'created', $5, 'USD')`,
      [providerOperationId, input.bridgeId, input.operationType, requestId, input.amountCents],
    );
    const result = await client.query<{ id: number }>(
      `INSERT INTO outbox_commands (provider_operation_id, command_type, payload)
       VALUES ($1, $2, $3::jsonb) RETURNING id`,
      [providerOperationId, input.operationType === "bridge" ? "create_bridge_transfer" : "create_collection_charge", JSON.stringify({ ...input.payload, bridgeId: input.bridgeId, amountCents: input.amountCents, currency: "USD", requestId })],
    );
    const row = result.rows[0];
    if (!row) throw new Error("Outbox command was not created");
    await client.query("COMMIT");
    return { providerOperationId, requestId, outboxCommandId: Number(row.id) };
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}
