import { createServer, type RequestListener, type Server } from "node:http";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { ServerConfig } from "../config.js";
import type { DatabasePool } from "../db/pool.js";
import type { WebhookInboxWorker } from "../webhooks/worker.js";
import { handleAirwallexWebhook } from "./routes/airwallex-webhook.js";
import { handleCustomerSnapshot, requestAuthorization } from "./routes/customer-snapshot.js";
import { handleLedgerProjection } from "./routes/ledger-projection.js";
import { handleKit5AttemptStatus, handleKit5NextCustomer, type Kit5DemoRouteDependencies } from "./routes/kit5-demo.js";
import { handleKit5Preflight, handleKit5Session } from "./kit5-operator-auth.js";

export function createHttpServer(
  config: ServerConfig,
  pool?: DatabasePool,
  webhookWorker?: WebhookInboxWorker,
  kit5Demo?: Kit5DemoRouteDependencies,
): Server {
  return createServer(createRequestHandler(config, pool, webhookWorker, kit5Demo));
}

/** Exposed so route wiring can be tested directly without opening a TCP listener. */
export function createRequestHandler(
  config: ServerConfig,
  pool?: DatabasePool,
  webhookWorker?: WebhookInboxWorker,
  kit5Demo?: Kit5DemoRouteDependencies,
): RequestListener {
  return (request, response) => {
    const operatorAuth = {
      operatorToken: config.kit5OperatorToken,
      allowedOrigins: config.kit5AllowedOrigins,
    };
    if (request.url === "/api/kit5/session" && request.method === "OPTIONS") {
      handleKit5Preflight(request, response, operatorAuth);
      return;
    }
    if (request.url === "/api/kit5/session" && request.method === "POST") {
      handleKit5Session(request, response, operatorAuth);
      return;
    }
    if (request.url === "/api/kit5/next-customer" && request.method === "OPTIONS") {
      handleKit5Preflight(request, response, operatorAuth);
      return;
    }
    if (request.method === "POST" && request.url === "/api/kit5/next-customer" && kit5Demo) {
      void handleKit5NextCustomer(request, response, kit5Demo);
      return;
    }
    if (request.url === "/api/kit5/attempt-status" && request.method === "OPTIONS") {
      handleKit5Preflight(request, response, operatorAuth);
      return;
    }
    if (request.method === "POST" && request.url === "/api/kit5/attempt-status" && kit5Demo) {
      void handleKit5AttemptStatus(request, response, kit5Demo);
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

export function handleRequest(request: IncomingMessage, response: ServerResponse): void {
  if (request.method === "GET" && request.url === "/health") {
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ status: "ok" }));
    return;
  }
  response.writeHead(404, { "content-type": "application/json" });
  response.end(JSON.stringify({ error: "not_found" }));
}
