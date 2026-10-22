import type { IncomingMessage, ServerResponse } from "node:http";
import type { IssuingSimulationGateway } from "../../airwallex/client.js";
import type { DatabasePool } from "../../db/pool.js";
import { processNextCustomerAction, reconcileKit5CustomerAction, type Kit5CustomerFixture, type NextCustomerInput } from "../../demo/next-customer.js";
import { authorizeKit5OperatorRequest, type Kit5OperatorAuthConfig } from "../kit5-operator-auth.js";

const MAX_REQUEST_BYTES = 16_384;
const ACTION_ID_PATTERN = /^[A-Za-z0-9._:-]{1,120}$/;
const IDENTIFIER_PATTERN = /^[A-Za-z0-9._:-]{1,100}$/;

export interface Kit5DemoRouteDependencies {
  pool: DatabasePool;
  gateway: IssuingSimulationGateway;
  resolveScenario: (scenarioId: string) => Kit5CustomerFixture | null;
  operatorAuth: Kit5OperatorAuthConfig;
}

/** Accept one local customer action; provider IDs and amounts are resolved server-side. */
export async function handleKit5NextCustomer(
  request: IncomingMessage,
  response: ServerResponse,
  dependencies: Kit5DemoRouteDependencies,
): Promise<void> {
  if (!authorizeKit5OperatorRequest(request, response, dependencies.operatorAuth)) return;
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  const input = validateInput(body);
  if (!input) {
    send(response, 400, { error: "invalid_action" });
    return;
  }
  try {
    const fixture = dependencies.resolveScenario(input.scenarioId);
    const attemptedAmount = fixture?.resolveCustomer(input.customerId);
    const outcome = await processNextCustomerAction(dependencies.pool, dependencies.gateway, input, fixture);
    if (outcome.status === "rejected") {
      send(response, 409, { status: "rejected", reason: outcome.reason });
      return;
    }
    if (outcome.status === "completed") {
      send(response, 200, {
        status: outcome.status,
        attemptId: outcome.attempt.actionId,
        attemptedAmount: attemptedAmount ? { amountCents: attemptedAmount.amountCents, currency: attemptedAmount.currency } : null,
        result: outcome.result,
      });
      return;
    }
    send(response, outcome.status === "failed" ? 502 : 202, {
      status: outcome.status,
      attemptId: outcome.attempt.actionId,
      lifecycleId: outcome.attempt.lifecycleId,
      attemptedAmount: attemptedAmount ? { amountCents: attemptedAmount.amountCents, currency: attemptedAmount.currency } : null,
    });
  } catch {
    send(response, 503, { error: "kit5_attempt_unavailable" });
  }
}

export async function handleKit5AttemptStatus(
  request: IncomingMessage,
  response: ServerResponse,
  dependencies: Kit5DemoRouteDependencies,
): Promise<void> {
  if (!authorizeKit5OperatorRequest(request, response, dependencies.operatorAuth)) return;
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  const input = validateInput(body);
  if (!input) {
    send(response, 400, { error: "invalid_action" });
    return;
  }
  const fixture = dependencies.resolveScenario(input.scenarioId);
  const attemptedAmount = fixture?.resolveCustomer(input.customerId);
  const attempt = await reconcileKit5CustomerAction(dependencies.pool, dependencies.gateway, input, fixture);
  if (!attempt) {
    send(response, 404, { error: "kit5_attempt_not_found" });
    return;
  }
  send(response, 200, {
    status: attempt.status === "accepted" || attempt.status === "submitting" ? "in_progress" : attempt.status,
    attemptId: attempt.actionId,
    lifecycleId: attempt.lifecycleId,
    attemptedAmount: attemptedAmount ? { amountCents: attemptedAmount.amountCents, currency: attemptedAmount.currency } : null,
    ...(attempt.status === "completed" && attempt.result ? { result: attempt.result } : {}),
  });
}

function validateInput(value: unknown): NextCustomerInput | null {
  if (typeof value !== "object" || value === null) return null;
  const record = value as Record<string, unknown>;
  if (typeof record.scenarioId !== "string" || !IDENTIFIER_PATTERN.test(record.scenarioId)
    || typeof record.customerId !== "string" || !IDENTIFIER_PATTERN.test(record.customerId)
    || typeof record.actionId !== "string" || !ACTION_ID_PATTERN.test(record.actionId)) {
    return null;
  }
  return { scenarioId: record.scenarioId, customerId: record.customerId, actionId: record.actionId };
}

function readBody(request: IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    request.on("data", (chunk: Buffer | string) => {
      const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      size += bytes.length;
      if (size > MAX_REQUEST_BYTES) {
        reject(new Error("request too large"));
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
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  response.end(JSON.stringify(body));
}
