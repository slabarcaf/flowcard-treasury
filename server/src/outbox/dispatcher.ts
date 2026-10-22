import { ProviderHttpError, type AirwallexGateway, type ProviderOperationResponse } from "../airwallex/client.js";
import { normalizeProviderStatus } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";

interface ClaimedCommand {
  id: number;
  provider_operation_id: string;
  command_type: string;
  operation_type: "bridge" | "fee" | "principal";
  request_id: string;
  amount_cents: string | number;
  payload: Record<string, unknown>;
  attempts: number;
}

/** Claims one durable command, resolves its request ID, then sends at most that operation. */
export async function dispatchOne(pool: DatabasePool, gateway: AirwallexGateway): Promise<boolean> {
  const client = await pool.connect();
  let command: ClaimedCommand | undefined;
  try {
    await client.query("BEGIN");
    const result = await client.query<ClaimedCommand>(
      `SELECT outbox.id, outbox.provider_operation_id, outbox.command_type,
              operation.operation_type, operation.request_id, operation.amount_cents,
              outbox.payload, outbox.attempts
         FROM outbox_commands AS outbox
         JOIN provider_operations AS operation ON operation.id = outbox.provider_operation_id
        WHERE outbox.status = 'queued' AND outbox.dispatch_ready AND outbox.available_at <= now()
        ORDER BY outbox.id
        LIMIT 1 FOR UPDATE OF outbox SKIP LOCKED`,
    );
    command = result.rows[0];
    if (!command) { await client.query("COMMIT"); return false; }
    await client.query("UPDATE outbox_commands SET status = 'processing', attempts = attempts + 1 WHERE id = $1", [command.id]);
    await client.query("COMMIT");
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }

  if (!command) return false;
  try {
    const request = {
      requestId: command.request_id,
      amountCents: Number(command.amount_cents),
      currency: "USD" as const,
      payload: command.payload,
    };
    // Search before sending, including after a timeout where the provider may have accepted it.
    const prior = command.operation_type === "bridge"
      ? await gateway.getTransferByRequestId(command.request_id)
      : await gateway.getChargeByRequestId(command.request_id);
    const response = prior ?? (command.operation_type === "bridge"
      ? await gateway.createTransfer(request)
      : await gateway.createCharge(request));
    await completeCommand(pool, command, response);
  } catch (error) {
    if (isRetryableProviderError(error)) await retryCommand(pool, command, error);
    else await failCommand(pool, command, error);
  }
  return true;
}

async function completeCommand(pool: DatabasePool, command: ClaimedCommand, response: ProviderOperationResponse): Promise<void> {
  if (response.requestId && response.requestId !== command.request_id) {
    await failCommand(pool, command, new Error("Provider response request ID did not match the command"));
    return;
  }
  const status = normalizeProviderStatus(response.status);
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await client.query(
      `UPDATE provider_operations SET provider_object_id = $2, status = $3, updated_at = now() WHERE id = $1`,
      [command.provider_operation_id, response.id, status],
    );
    await client.query("UPDATE outbox_commands SET status = 'done' WHERE id = $1", [command.id]);
    await client.query("COMMIT");
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

async function retryCommand(pool: DatabasePool, command: ClaimedCommand, error: unknown): Promise<void> {
  const client = await pool.connect();
  try {
    await client.query(
      `UPDATE outbox_commands SET status = 'queued', available_at = now() + interval '30 seconds', last_error = $2
        WHERE id = $1 AND status = 'processing'`,
      [command.id, describeError(error)],
    );
  } finally {
    client.release();
  }
  // The durable operation and its request ID remain untouched for the next attempt.
}

async function failCommand(pool: DatabasePool, command: ClaimedCommand, error: unknown): Promise<void> {
  const client = await pool.connect();
  try {
    await client.query(
      `UPDATE outbox_commands SET status = 'failed', last_error = $2
        WHERE id = $1 AND status = 'processing'`,
      [command.id, describeError(error)],
    );
  } finally {
    client.release();
  }
}

function isRetryableProviderError(error: unknown): boolean {
  if (error instanceof ProviderHttpError) {
    return error.status === 408 || error.status === 429 || error.status >= 500;
  }
  if (error instanceof TypeError) return true; // fetch network errors
  if (error instanceof Error && (error.name === "AbortError" || error.name === "TimeoutError")) return true;
  return false;
}

function describeError(error: unknown): string {
  const name = error instanceof Error ? error.name : "Error";
  let message = error instanceof Error ? error.message : String(error);
  message = message
    .replace(/Bearer\s+[^\s,;]+/gi, "Bearer [redacted]")
    .replace(/(x-api-key|api[_ -]?key|client[_ -]?secret)[=: ]+[^\s,;]+/gi, "$1=[redacted]")
    .split("")
    .map((character) => character.charCodeAt(0) < 32 ? " " : character)
    .join("")
    .slice(0, 500);
  const status = error instanceof ProviderHttpError ? ` (HTTP ${error.status})` : "";
  return `${name}${status}: ${message}`;
}
