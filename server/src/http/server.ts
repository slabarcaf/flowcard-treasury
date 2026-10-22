import { createServer, type RequestListener, type Server } from "node:http";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { ServerConfig } from "../config.js";
import type { DatabasePool } from "../db/pool.js";
import type { WebhookInboxWorker } from "../webhooks/worker.js";
import { handleAirwallexWebhook } from "./routes/airwallex-webhook.js";
import { handleCustomerSnapshot, requestAuthorization } from "./routes/customer-snapshot.js";
import { handleLedgerProjection } from "./routes/ledger-projection.js";
import { handleBridgeDecision } from "./routes/bridge-decision.js";
import type { TrustedBridgeDecisionResult } from "./routes/bridge-decision.js";
import { handleRemoteAuthorization, type RemoteAuthorizationResolver } from "./routes/remote-authorization.js";
import { timingSafeEqual } from "node:crypto";

export function createHttpServer(
  config: ServerConfig,
  pool?: DatabasePool,
  webhookWorker?: WebhookInboxWorker,
  resolveBridgeDecision?: (requestId: string) => Promise<TrustedBridgeDecisionResult>,
  resolveRemoteAuthorization?: RemoteAuthorizationResolver,
): Server {
  return createServer(createRequestHandler(config, pool, webhookWorker, resolveBridgeDecision, resolveRemoteAuthorization));
}

/** Exposed so route wiring can be tested directly without opening a TCP listener. */
export function createRequestHandler(
  config: ServerConfig,
  pool?: DatabasePool,
  webhookWorker?: WebhookInboxWorker,
  resolveBridgeDecision?: (requestId: string) => Promise<TrustedBridgeDecisionResult>,
  resolveRemoteAuthorization?: RemoteAuthorizationResolver,
): RequestListener {
  return (request, response) => {
    if (request.method === "POST" && request.url === "/api/remote-authorization") {
      void handleRemoteAuthorization(request, response, {
        sharedSecret: config.airwallexRemoteAuthorizationSecret,
        resolve: resolveRemoteAuthorization,
      });
      return;
    }
    if (request.method === "POST" && request.url === "/api/bridge-decisions" && pool) {
      if (!config.policyDecisionReadToken) {
        response.writeHead(503, { "content-type": "application/json", "cache-control": "no-store" });
        response.end(JSON.stringify({ error: "policy_decision_read_unavailable" }));
        return;
      }
      const authorization = request.headers.authorization;
      const token = typeof authorization === "string" && authorization.startsWith("Bearer ")
        ? authorization.slice("Bearer ".length)
        : "";
      if (!sameSecret(token, config.policyDecisionReadToken)) {
        response.writeHead(401, { "content-type": "application/json", "cache-control": "no-store" });
        response.end(JSON.stringify({ error: "unauthorized" }));
        return;
      }
      void handleBridgeDecision(request, response, resolveBridgeDecision);
      return;
    }
    if (request.method === "POST" && request.url === "/webhooks/airwallex" && pool) {
      void handleAirwallexWebhook(request, response, { pool, secret: config.airwallexWebhookSecret, onReceived: () => webhookWorker?.nudge() });
      return;
    }
    if (request.method === "GET" && request.url === "/api/ledger" && pool) {
      void handleLedgerProjection(response, pool);
      return;
    }
    if (request.method === "GET" && pool) {
      const match = /^\/api\/customers\/([^/]+)\/snapshot(?:\?.*)?$/.exec(request.url ?? "");
      if (match) {
        let customerId: string;
        try {
          customerId = decodeURIComponent(match[1]!);
        } catch {
          response.writeHead(400, { "content-type": "application/json", "cache-control": "no-store" });
          response.end(JSON.stringify({ error: "invalid_customer_id" }));
          return;
        }
        void handleCustomerSnapshot(response, pool, customerId, requestAuthorization(request),
          config.salesforceSnapshotReadToken, config.salesforceSnapshotMode);
        return;
      }
    }
    handleRequest(request, response);
  };
}

function sameSecret(provided: string, expected: string): boolean {
  const providedBytes = Buffer.from(provided, "utf8");
  const expectedBytes = Buffer.from(expected, "utf8");
  return providedBytes.length === expectedBytes.length && timingSafeEqual(providedBytes, expectedBytes);
}

export function handleRequest(request: IncomingMessage, response: ServerResponse): void {
  if (request.method === "GET" && request.url === "/health") {
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ status: "ok" }));
    return;
  }
  response.writeHead(404, { "content-type": "application/json" });
  response.end(JSON.stringify({ error: "not_found" }));
}
