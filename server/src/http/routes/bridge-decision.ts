import type { IncomingMessage, ServerResponse } from "node:http";

const MAX_BODY_BYTES = 16_384;

export interface TrustedBridgeDecisionResult {
  evaluationId: string;
  requestId: string;
  customerId: string;
  outcome: "auto_approved" | "human_review" | "declined" | "blocked";
  amountCents: number;
  creditLineCents: number;
  reasonCodes: readonly string[];
  freshness: Record<string, string | number | null>;
  versions: Record<string, string | number | null>;
}

/** Only request keys cross the HTTP boundary; the resolver loads every decision input from trusted storage. */
export async function handleBridgeDecision(
  request: IncomingMessage,
  response: ServerResponse,
  resolve?: (requestId: string) => Promise<TrustedBridgeDecisionResult>,
): Promise<void> {
  if (!resolve) {
    send(response, 503, { error: "trusted_bridge_request_source_unavailable" });
    return;
  }
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  if (!isRecord(body) || Object.keys(body).some((key) => key !== "requestId") ||
      typeof body.requestId !== "string" || !body.requestId.trim()) {
    send(response, 400, { error: "request_id_required" });
    return;
  }
  try {
    const result = await resolve(body.requestId.trim());
    send(response, 200, {
      evaluationId: result.evaluationId,
      requestId: result.requestId,
      customerId: result.customerId,
      outcome: result.outcome,
      amountCents: result.amountCents,
      creditLineCents: result.creditLineCents,
      reasonCodes: result.reasonCodes,
      freshness: result.freshness,
      versions: result.versions,
    });
  } catch {
    send(response, 503, { error: "bridge_decision_unavailable" });
  }
}

function readBody(request: IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    request.on("data", (chunk: Buffer | string) => {
      const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      size += bytes.length;
      if (size > MAX_BODY_BYTES) {
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function send(response: ServerResponse, status: number, body: Record<string, unknown>): void {
  if (response.headersSent) return;
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  response.end(JSON.stringify(body));
}
