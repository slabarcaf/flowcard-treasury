import { request as httpRequest } from "node:http";
import { request as httpsRequest } from "node:https";
import type { IncomingMessage, ServerResponse } from "node:http";
import { posix } from "node:path";
import { URL } from "node:url";
import {
  authorizeKit5OperatorRequest,
  authenticatedKit5OperatorActor,
  handleKit5Preflight,
  type Kit5OperatorAuthConfig,
} from "../kit5-operator-auth.js";
import {
  createServiceAssertion,
  DEFAULT_ISSUER,
  DEFAULT_OPERATOR_AUDIENCE,
} from "../onboarding-proxy-auth.js";

const MAX_JSON_BODY_BYTES = 1_048_576; // 1 MiB for standard API calls
const MAX_MULTIPART_BODY_BYTES = 15_728_640; // 15 MiB for document uploads (10 MiB payload + headers)
const DEFAULT_UPSTREAM_TIMEOUT_MS = 30_000;

export class PathGuardError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PathGuardError";
  }
}

export interface OnboardingProxyDependencies {
  upstreamUrl?: string; // e.g. "http://127.0.0.1:8080"
  serviceSecret?: string;
  operatorAuth: Kit5OperatorAuthConfig;
  upstreamTimeoutMs?: number;
}

/** Validates configured upstream URL strictly. Rejects userinfo, path prefixes, queries, and fragments. */
export function validateUpstreamUrl(urlStr: string): URL {
  let parsed: URL;
  try {
    parsed = new URL(urlStr);
  } catch {
    throw new Error("invalid upstream URL");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("upstream protocol must be http or https");
  }
  if (!parsed.hostname || parsed.hostname.length === 0) {
    throw new Error("upstream hostname must not be empty");
  }
  if (parsed.username !== "" || parsed.password !== "") {
    throw new Error("upstream URL must not contain userinfo credentials");
  }
  if (parsed.pathname !== "/" && parsed.pathname !== "") {
    throw new Error("upstream URL must not contain a path prefix");
  }
  if (parsed.search !== "" || parsed.hash !== "") {
    throw new Error("upstream URL must not contain query or fragment");
  }
  return parsed;
}

/** Resolves target resource path against explicit resource route allowlist. */
export function resolveTargetResourcePath(rawUrl: string): { targetPath: string; isUpload: boolean } | null {
  // Separate path from query string so query string contents (e.g. ?search=hello..world) are not treated as path traversal
  const qIdx = rawUrl.indexOf("?");
  const rawPath = qIdx >= 0 ? rawUrl.slice(0, qIdx) : rawUrl;
  const search = qIdx >= 0 ? rawUrl.slice(qIdx) : "";

  // Check for raw null bytes
  if (rawPath.includes("\0") || rawPath.toLowerCase().includes("%00")) {
    throw new PathGuardError("invalid_path_traversal");
  }

  // Decode path and check for double encoding
  let decodedPath: string;
  try {
    decodedPath = decodeURIComponent(rawPath);
  } catch {
    throw new PathGuardError("invalid_request_url");
  }

  let twiceDecoded = decodedPath;
  if (decodedPath.includes("%")) {
    try {
      twiceDecoded = decodeURIComponent(decodedPath);
    } catch {
      // not double encoded, ignore
    }
  }

  for (const p of [decodedPath, twiceDecoded]) {
    if (p.includes("\0")) {
      throw new PathGuardError("invalid_path_traversal");
    }
    const segments = p.split("/");
    for (const seg of segments) {
      if (seg === ".." || seg === ".") {
        throw new PathGuardError("invalid_path_traversal");
      }
    }
  }

  // Posix normalization check
  const normalized = posix.normalize(decodedPath);
  if (normalized.startsWith("..") || normalized.includes("/../") || normalized.endsWith("/..")) {
    throw new PathGuardError("invalid_path_traversal");
  }

  // 1. Direct resource routes allowlist
  const directAllowlist = [
    "/api/companies",
    "/api/onboarding-applications",
    "/api/submissions",
    "/api/platform-accounts",
  ];

  for (const prefix of directAllowlist) {
    if (normalized === prefix || normalized.startsWith(prefix + "/")) {
      const isUpload = normalized.includes("/documents");
      return { targetPath: `${normalized}${search}`, isUpload };
    }
  }

  // 2. Gateway alias routes under /api/onboarding/
  if (normalized === "/api/onboarding" || normalized.startsWith("/api/onboarding/")) {
    if (normalized === "/api/onboarding" || normalized === "/api/onboarding/") {
      return null;
    }
    const sub = normalized.slice("/api/onboarding".length);
    if (sub === "/companies" || sub.startsWith("/companies/")) {
      const mapped = `/api${sub}`;
      return { targetPath: `${mapped}${search}`, isUpload: mapped.includes("/documents") };
    }
    if (sub === "/onboarding-applications" || sub.startsWith("/onboarding-applications/")) {
      const mapped = `/api${sub}`;
      return { targetPath: `${mapped}${search}`, isUpload: mapped.includes("/documents") };
    }
    if (sub === "/applications" || sub.startsWith("/applications/")) {
      const mapped = `/api/onboarding-applications${sub.slice("/applications".length)}`;
      return { targetPath: `${mapped}${search}`, isUpload: mapped.includes("/documents") };
    }
    if (sub === "/submissions" || sub.startsWith("/submissions/")) {
      const mapped = `/api${sub}`;
      return { targetPath: `${mapped}${search}`, isUpload: false };
    }
    if (sub === "/platform-accounts" || sub.startsWith("/platform-accounts/")) {
      const mapped = `/api${sub}`;
      return { targetPath: `${mapped}${search}`, isUpload: false };
    }
    // Any other subpath (e.g. /api/onboarding/webhooks or /api/onboarding/admin) is denied
    return null;
  }

  return null;
}

export function handleOnboardingProxy(
  request: IncomingMessage,
  response: ServerResponse,
  deps: OnboardingProxyDependencies,
): void {
  // Handle CORS preflight options
  if (request.method === "OPTIONS") {
    handleKit5Preflight(request, response, deps.operatorAuth);
    return;
  }

  // Check operator authentication and origin
  if (!authorizeKit5OperatorRequest(request, response, deps.operatorAuth)) {
    return;
  }

  const actor = authenticatedKit5OperatorActor(request, deps.operatorAuth);
  if (!actor) {
    sendJson(response, 401, { error: "operator_auth_required" });
    return;
  }

  if (!deps.upstreamUrl || !deps.serviceSecret || deps.serviceSecret.length < 32) {
    sendJson(response, 503, { error: "onboarding_service_unconfigured" });
    return;
  }

  let validatedUpstream: URL;
  try {
    validatedUpstream = validateUpstreamUrl(deps.upstreamUrl);
  } catch {
    sendJson(response, 500, { error: "invalid_upstream_configuration" });
    return;
  }

  // Validate path traversal, normalization, and route allowlist
  let target: { targetPath: string; isUpload: boolean } | null;
  try {
    target = resolveTargetResourcePath(request.url ?? "");
  } catch (err) {
    if (err instanceof PathGuardError) {
      sendJson(response, 400, { error: err.message });
      return;
    }
    sendJson(response, 400, { error: "invalid_request_url" });
    return;
  }

  if (!target) {
    sendJson(response, 404, { error: "not_found" });
    return;
  }

  let targetUrl: URL;
  try {
    targetUrl = new URL(target.targetPath, validatedUpstream);
  } catch {
    sendJson(response, 500, { error: "invalid_upstream_configuration" });
    return;
  }

  // Generate short-lived signed JWT assertion with verified actor identity
  let assertion: string;
  try {
    assertion = createServiceAssertion({
      secret: deps.serviceSecret,
      subject: actor,
      issuer: DEFAULT_ISSUER,
      audience: DEFAULT_OPERATOR_AUDIENCE,
      ttlSeconds: 30,
    });
  } catch {
    sendJson(response, 500, { error: "assertion_generation_failed" });
    return;
  }

  // Strip untrusted client headers; build upstream headers
  const upstreamHeaders: Record<string, string> = {
    authorization: `Bearer ${assertion}`,
    host: targetUrl.host,
  };

  // Pass safe client headers
  const passHeaders = [
    "content-type",
    "content-length",
    "accept",
    "if-match",
    "if-none-match",
    "idempotency-key",
  ];

  for (const headerName of passHeaders) {
    const val = request.headers[headerName];
    if (typeof val === "string" && val.length > 0) {
      upstreamHeaders[headerName] = val;
    }
  }

  const maxBytes = target.isUpload ? MAX_MULTIPART_BODY_BYTES : MAX_JSON_BODY_BYTES;
  const requester = targetUrl.protocol === "https:" ? httpsRequest : httpRequest;
  const timeoutMs = deps.upstreamTimeoutMs ?? DEFAULT_UPSTREAM_TIMEOUT_MS;

  const upstreamReq = requester(
    {
      protocol: targetUrl.protocol,
      hostname: targetUrl.hostname,
      port: targetUrl.port || (targetUrl.protocol === "https:" ? 443 : 80),
      path: targetUrl.pathname + targetUrl.search,
      method: request.method,
      headers: upstreamHeaders,
      timeout: timeoutMs,
    },
    (upstreamRes) => {
      // Forward status and headers to client
      const clientHeaders: Record<string, string | string[]> = {};
      for (const [k, v] of Object.entries(upstreamRes.headers)) {
        if (v !== undefined && !k.startsWith("x-internal-")) {
          clientHeaders[k] = v;
        }
      }

      response.writeHead(upstreamRes.statusCode ?? 502, clientHeaders);
      upstreamRes.pipe(response);
    },
  );

  let timedOut = false;
  upstreamReq.on("timeout", () => {
    timedOut = true;
    upstreamReq.destroy(new Error("upstream_timeout"));
  });

  upstreamReq.on("error", (err: Error) => {
    if (response.headersSent) {
      response.destroy();
      return;
    }
    if (timedOut || err.message === "upstream_timeout") {
      sendJson(response, 504, { error: "onboarding_service_timeout" });
    } else {
      sendJson(response, 502, { error: "onboarding_service_unavailable" });
    }
  });

  // Pipe request body with strict size bounding
  let receivedBytes = 0;
  request.on("data", (chunk: Buffer) => {
    receivedBytes += chunk.length;
    if (receivedBytes > maxBytes) {
      request.destroy();
      upstreamReq.destroy();
      if (!response.headersSent) {
        sendJson(response, 413, { error: "payload_too_large" });
      }
    }
  });

  request.pipe(upstreamReq);
}

function sendJson(response: ServerResponse, status: number, body: Record<string, unknown>): void {
  if (response.headersSent) return;
  response.writeHead(status, {
    "content-type": "application/json",
    "cache-control": "no-store",
  });
  response.end(JSON.stringify(body));
}
