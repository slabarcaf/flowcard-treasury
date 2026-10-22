import { createHash, createHmac, timingSafeEqual } from "node:crypto";
import type { IncomingMessage, ServerResponse } from "node:http";

const MAX_BODY_BYTES = 16_384;
const DEFAULT_MAX_AGE_MS = 30_000;
const RESOLUTION_BUDGET_MS = 2_000;
/** Explicit v1 MCC allowlist retained from the Airwallex card requirements. */
export const TRACEABLE_PURCHASE_MCCS = new Set(["5411", "5812", "5814", "5734"]);

export interface AirwallexRemoteAuthorizationV2 {
  version: 2;
  accountId: string;
  cardId: string;
  cardTransactionEventId: string;
  cardTransactionId: string;
  cardTransactionLifecycleId: string;
  transactionType: "AUTHORIZATION" | "CLEARING";
  transactionCategory: string;
  transactionAmount: number;
  transactionCurrency: string;
  merchantCategoryCode: string;
}

export interface RemoteAuthorizationDecision {
  responseStatus: "AUTHORIZED" | "DECLINED";
  statusReason: string;
}

export type RemoteAuthorizationResolver = (
  input: AirwallexRemoteAuthorizationV2,
  nonce: string,
  signal: AbortSignal,
) => Promise<RemoteAuthorizationDecision>;

export type RemoteAuthorizationNonceClaimer = (
  input: AirwallexRemoteAuthorizationV2,
  nonce: string,
  receivedAt: Date,
) => Promise<"claimed" | "exact_retry" | "replay_mismatch">;

/** Airwallex remote authorization v2 endpoint. Unknown and unsafe requests decline. */
export async function handleRemoteAuthorization(
  request: IncomingMessage,
  response: ServerResponse,
  options: {
    sharedSecret?: string;
    resolve?: RemoteAuthorizationResolver;
    claimNonce?: RemoteAuthorizationNonceClaimer;
    now?: Date;
    maxAgeMs?: number;
    resolutionBudgetMs?: number;
  },
): Promise<void> {
  let body: unknown;
  try {
    body = JSON.parse((await readBody(request)).toString("utf8"));
  } catch {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  const parsed = parseRequest(body);
  if (!parsed) {
    send(response, 400, { error: "invalid_request" });
    return;
  }
  if (!options.sharedSecret?.trim()) {
    send(response, 200, providerResponse(parsed.cardTransactionEventId, "DECLINED", "remote_authorization_not_configured"));
    return;
  }
  const receivedAt = options.now ?? new Date();
  if (!isAuthentic(request, options.sharedSecret, receivedAt, options.maxAgeMs ?? DEFAULT_MAX_AGE_MS)) {
    send(response, 200, providerResponse(parsed.cardTransactionEventId, "DECLINED", "invalid_request_signature"));
    return;
  }
  const nonce = header(request, "x-nonce")!;
  if (!options.claimNonce) {
    send(response, 200, providerResponse(parsed.cardTransactionEventId, "DECLINED", "nonce_claim_unavailable"));
    return;
  }
  let nonceClaim: "claimed" | "exact_retry" | "replay_mismatch";
  try {
    nonceClaim = await options.claimNonce(parsed, nonce, receivedAt);
  } catch {
    send(response, 200, providerResponse(parsed.cardTransactionEventId, "DECLINED", "nonce_claim_unavailable"));
    return;
  }
  if (nonceClaim === "replay_mismatch") {
    send(response, 200, providerResponse(parsed.cardTransactionEventId, "DECLINED", "request_replay_mismatch"));
    return;
  }
  if (parsed.transactionType !== "AUTHORIZATION" || parsed.transactionCategory !== "PURCHASE" ||
      parsed.transactionCurrency !== "USD" || !TRACEABLE_PURCHASE_MCCS.has(parsed.merchantCategoryCode)) {
    send(response, 200, providerResponse(parsed.cardTransactionEventId, "DECLINED", "unsupported_transaction"));
    return;
  }

  let result: RemoteAuthorizationDecision = { responseStatus: "DECLINED", statusReason: "policy_unavailable" };
  if (options.resolve) {
    const controller = new AbortController();
    let timeout: ReturnType<typeof setTimeout> | undefined;
    try {
      result = await Promise.race([
        options.resolve(parsed, nonce, controller.signal),
        new Promise<never>((_, reject) => {
          timeout = setTimeout(() => {
            controller.abort(new Error("remote authorization deadline exceeded"));
            reject(new Error("remote authorization deadline exceeded"));
          }, options.resolutionBudgetMs ?? RESOLUTION_BUDGET_MS);
        }),
      ]);
    } catch {
      result = { responseStatus: "DECLINED", statusReason: "policy_timeout_or_unavailable" };
    } finally {
      if (timeout !== undefined) clearTimeout(timeout);
    }
  }
  send(response, 200, providerResponse(parsed.cardTransactionEventId, result.responseStatus, result.statusReason));
}

export function fingerprintRemoteAuthorizationRequest(request: AirwallexRemoteAuthorizationV2): string {
  const canonical = JSON.stringify([
    request.version,
    request.accountId,
    request.cardId,
    request.cardTransactionEventId,
    request.cardTransactionId,
    request.cardTransactionLifecycleId,
    request.transactionType,
    request.transactionCategory,
    request.transactionAmount,
    request.transactionCurrency,
    request.merchantCategoryCode,
  ]);
  return createHash("sha256").update(canonical, "utf8").digest("hex");
}

export function authenticateAirwallexNonce(
  nonce: string | undefined,
  signature: string | undefined,
  sharedSecret: string,
  now = new Date(),
  maxAgeMs = DEFAULT_MAX_AGE_MS,
): boolean {
  if (!nonce || !signature || !/^[A-Za-z0-9+/]{43}=$/.test(signature) || !sharedSecret.trim() ||
      !Number.isSafeInteger(maxAgeMs) || maxAgeMs < 1) return false;
  const separator = nonce.indexOf(".");
  if (separator < 1 || separator === nonce.length - 1) return false;
  const timestampText = nonce.slice(0, separator);
  if (!/^\d{13}$/.test(timestampText)) return false;
  const timestamp = Number(timestampText);
  const delta = now.getTime() - timestamp;
  if (!Number.isFinite(now.getTime()) || !Number.isFinite(delta) || delta < 0 || delta > maxAgeMs) return false;
  const expected = createHmac("sha256", sharedSecret).update(nonce, "utf8").digest();
  let provided: Buffer;
  try {
    provided = Buffer.from(signature, "base64");
  } catch {
    return false;
  }
  return provided.length === expected.length && timingSafeEqual(provided, expected);
}

function isAuthentic(
  request: IncomingMessage,
  sharedSecret: string,
  now: Date,
  maxAgeMs: number,
): boolean {
  return authenticateAirwallexNonce(
    header(request, "x-nonce"), header(request, "x-signature"), sharedSecret, now, maxAgeMs,
  );
}

function parseRequest(value: unknown): AirwallexRemoteAuthorizationV2 | null {
  if (!isRecord(value) || value.version !== 2 || !isRecord(value.merchant)) return null;
  const accountId = string(value.account_id);
  const cardId = string(value.card_id);
  const cardTransactionEventId = string(value.card_transaction_event_id);
  const cardTransactionId = string(value.card_transaction_id);
  const cardTransactionLifecycleId = string(value.card_transaction_lifecycle_id);
  const transactionType = value.transaction_type;
  const transactionCategory = string(value.transaction_category);
  const transactionCurrency = string(value.transaction_currency).toUpperCase();
  const transactionAmount = value.transaction_amount;
  const merchantCategoryCode = string(value.merchant.category_code);
  if (!accountId || !cardId || !cardTransactionEventId || !cardTransactionId || !cardTransactionLifecycleId ||
      !transactionCategory || !transactionCurrency || !/^\d{4}$/.test(merchantCategoryCode) ||
      (transactionType !== "AUTHORIZATION" && transactionType !== "CLEARING") ||
      typeof transactionAmount !== "number" || !Number.isFinite(transactionAmount) || transactionAmount <= 0 ||
      !Number.isSafeInteger(Math.round(transactionAmount * 100)) ||
      Math.abs(transactionAmount * 100 - Math.round(transactionAmount * 100)) > 1e-7) return null;
  return {
    version: 2, accountId, cardId, cardTransactionEventId, cardTransactionId,
    cardTransactionLifecycleId, transactionType, transactionCategory,
    transactionAmount, transactionCurrency, merchantCategoryCode,
  };
}

function providerResponse(
  eventId: string,
  responseStatus: "AUTHORIZED" | "DECLINED",
  statusReason: string,
): Record<string, string> {
  return {
    card_transaction_event_id: eventId,
    response_status: responseStatus,
    status_reason: statusReason.slice(0, 100),
  };
}

function header(request: IncomingMessage, name: string): string | undefined {
  const value = request.headers[name];
  return Array.isArray(value) ? value[0] : value;
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

function string(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function send(response: ServerResponse, status: number, body: Record<string, unknown>): void {
  if (response.headersSent) return;
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  response.end(JSON.stringify(body));
}
