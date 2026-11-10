import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";
import type { IncomingMessage, ServerResponse } from "node:http";

const COOKIE_NAME = "kit5_operator_session";
const SESSION_TTL_SECONDS = 8 * 60 * 60;

export interface Kit5OperatorAuthConfig {
  operatorToken?: string;
  operatorId?: string;
  allowedOrigins?: readonly string[];
}

export function handleKit5Session(
  request: IncomingMessage,
  response: ServerResponse,
  config: Kit5OperatorAuthConfig,
): void {
  if (!config.operatorToken || config.operatorToken.length < 32 || !config.allowedOrigins?.length) {
    send(response, 503, { error: "kit5_operator_auth_unconfigured" });
    return;
  }
  if (!allowOrigin(request, response, config.allowedOrigins)) {
    send(response, 403, { error: "origin_forbidden" });
    return;
  }
  void readJson(request).then((body) => {
    const token = typeof body === "object" && body !== null
      ? (body as Record<string, unknown>).token
      : undefined;
    if (typeof token !== "string" || !constantTimeEqual(token, config.operatorToken!)) {
      send(response, 401, { error: "operator_auth_required" });
      return;
    }
    response.setHeader("Set-Cookie", `${COOKIE_NAME}=${createOperatorSessionCookie(config.operatorToken!, Date.now(), config.operatorId)}; Path=/api; HttpOnly; Secure; SameSite=None; Max-Age=${SESSION_TTL_SECONDS}`);
    send(response, 200, { status: "authenticated", approvalAvailable: Boolean(config.operatorId) });
  }).catch(() => send(response, 400, { error: "invalid_request" }));
}

export function authorizeKit5OperatorRequest(
  request: IncomingMessage,
  response: ServerResponse,
  config: Kit5OperatorAuthConfig,
): boolean {
  if (!config.operatorToken || config.operatorToken.length < 32 || !config.allowedOrigins?.length) {
    send(response, 503, { error: "kit5_operator_auth_unconfigured" });
    return false;
  }
  if (!allowOrigin(request, response, config.allowedOrigins)) {
    send(response, 403, { error: "origin_forbidden" });
    return false;
  }
  const session = parseCookies(request.headers.cookie ?? "")[COOKIE_NAME];
  if (!session || !validOperatorSession(session, config.operatorToken)) {
    send(response, 401, { error: "operator_auth_required" });
    return false;
  }
  return true;
}

export function handleKit5Preflight(
  request: IncomingMessage,
  response: ServerResponse,
  config: Kit5OperatorAuthConfig,
): void {
  if (!config.operatorToken || config.operatorToken.length < 32 || !config.allowedOrigins?.length) {
    send(response, 503, { error: "kit5_operator_auth_unconfigured" });
    return;
  }
  if (!allowOrigin(request, response, config.allowedOrigins)) {
    send(response, 403, { error: "origin_forbidden" });
    return;
  }
  response.writeHead(204, {
    "Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
    "Access-Control-Allow-Headers": "content-type, authorization, if-match, if-none-match, idempotency-key",
    "Access-Control-Max-Age": "600",
  });
  response.end();
}

export function createOperatorSessionCookie(operatorToken: string, now = Date.now(), operatorId?: string): string {
  const expiresAt = Math.floor(now / 1000) + SESSION_TTL_SECONDS;
  const nonce = randomBytes(24).toString("hex");
  const actor = operatorId ? Buffer.from(operatorId, "utf8").toString("base64url") : "";
  const payload = `${expiresAt}.${nonce}.${actor}`;
  return `${payload}.${sign(payload, operatorToken)}`;
}

/** Return only the server-configured actor identity bound into the signed session. */
export function authenticatedKit5OperatorActor(
  request: IncomingMessage,
  config: Kit5OperatorAuthConfig,
): string | null {
  if (!config.operatorToken || config.operatorToken.length < 32 || !config.operatorId) return null;
  const session = parseCookies(request.headers.cookie ?? "")[COOKIE_NAME];
  if (!session || !validOperatorSession(session, config.operatorToken)) return null;
  const [, , actor, signature, extra] = session.split(".");
  if (!actor || !signature || extra !== undefined) return null;
  let decoded: string;
  try {
    decoded = Buffer.from(actor, "base64url").toString("utf8");
  } catch {
    return null;
  }
  return decoded === config.operatorId ? decoded : null;
}

function validOperatorSession(value: string, operatorToken: string): boolean {
  const [expiresAt, nonce, actor, signature, extra] = value.split(".");
  if (!expiresAt || !nonce || actor === undefined || !signature || extra !== undefined || !/^\d+$/.test(expiresAt)) return false;
  const expiration = Number(expiresAt);
  if (!Number.isSafeInteger(expiration) || expiration <= Math.floor(Date.now() / 1000)) return false;
  return constantTimeEqual(signature, sign(`${expiresAt}.${nonce}.${actor}`, operatorToken));
}

function sign(payload: string, secret: string): string {
  return createHmac("sha256", secret).update(payload).digest("base64url");
}

function constantTimeEqual(left: string, right: string): boolean {
  const leftBytes = Buffer.from(left);
  const rightBytes = Buffer.from(right);
  return leftBytes.length === rightBytes.length && timingSafeEqual(leftBytes, rightBytes);
}

function allowOrigin(request: IncomingMessage, response: ServerResponse, allowedOrigins: readonly string[]): boolean {
  const origin = request.headers.origin;
  if (!origin || !allowedOrigins.includes(origin)) return false;
  response.setHeader("Access-Control-Allow-Origin", origin);
  response.setHeader("Access-Control-Allow-Credentials", "true");
  response.setHeader("Vary", "Origin");
  return true;
}

function parseCookies(header: string): Record<string, string> {
  return Object.fromEntries(header.split(";").flatMap((part) => {
    const separator = part.indexOf("=");
    if (separator < 0) return [];
    try {
      return [[part.slice(0, separator).trim(), decodeURIComponent(part.slice(separator + 1).trim())]];
    } catch {
      return [];
    }
  }));
}

function readJson(request: IncomingMessage): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    request.on("data", (chunk: Buffer | string) => {
      const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      size += bytes.length;
      if (size > 4096) {
        reject(new Error("request too large"));
        request.destroy();
        return;
      }
      chunks.push(bytes);
    });
    request.on("end", () => {
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")) as unknown);
      } catch (error) {
        reject(error);
      }
    });
    request.on("error", reject);
  });
}

function send(response: ServerResponse, status: number, body: Record<string, unknown>): void {
  if (response.headersSent) return;
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  response.end(JSON.stringify(body));
}
