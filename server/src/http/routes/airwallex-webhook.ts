import type { IncomingMessage, ServerResponse } from "node:http";
import type { DatabasePool } from "../../db/pool.js";
import { insertWebhookOnce, type AirwallexWebhookEvent } from "../../webhooks/inbox.js";
import { verifyAirwallexSignature } from "../../webhooks/verify.js";

const MAX_BODY_BYTES = 1_048_576;

export async function handleAirwallexWebhook(
  request: IncomingMessage,
  response: ServerResponse,
  deps: { pool: DatabasePool; secret: string; onReceived?: () => void },
): Promise<void> {
  let rawBody: Buffer;
  try {
    rawBody = await readRawBody(request);
  } catch {
    send(response, 413, { error: "payload_too_large" });
    return;
  }
  if (!verifyAirwallexSignature({
    rawBody,
    timestamp: header(request, "x-timestamp"),
    signature: header(request, "x-signature"),
    secret: deps.secret,
  })) {
    send(response, 401, { error: "invalid_signature" });
    return;
  }
  let event: AirwallexWebhookEvent;
  try {
    event = JSON.parse(rawBody.toString("utf8")) as AirwallexWebhookEvent;
    if (typeof event.id !== "string" || !event.id || typeof event.name !== "string" || !event.name) throw new Error("invalid event");
  } catch {
    send(response, 400, { error: "invalid_event" });
    return;
  }
  try {
    await insertWebhookOnce(deps.pool, { id: event.id, type: event.name, rawBody });
    // Duplicates are acknowledged too; processing is idempotent by provider event ID.
    send(response, 200, { received: true });
    // Nudge after acknowledgement for both new events and retries. The poller also
    // discovers pending rows after a process restart if this request process exits.
    deps.onReceived?.();
  } catch {
    send(response, 500, { error: "inbox_unavailable" });
  }
}

function header(request: IncomingMessage, name: string): string | undefined {
  const value = request.headers[name];
  return Array.isArray(value) ? value[0] : value;
}

function readRawBody(request: IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    request.on("data", (chunk: Buffer | string) => {
      const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      size += bytes.length;
      if (size > MAX_BODY_BYTES) {
        reject(new Error("body too large"));
        request.destroy();
        return;
      }
      chunks.push(bytes);
    });
    request.on("end", () => resolve(Buffer.concat(chunks)));
    request.on("error", reject);
  });
}

function send(response: ServerResponse, status: number, body: Record<string, unknown>): void {
  if (response.headersSent) return;
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(body));
}
