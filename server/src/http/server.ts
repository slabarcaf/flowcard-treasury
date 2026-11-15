import { createServer, type RequestListener, type Server } from "node:http";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { ServerConfig } from "../config.js";
import type { DatabasePool } from "../db/pool.js";
import { handleAirwallexWebhook } from "./routes/airwallex-webhook.js";
import { handleKit5Preflight, handleKit5Session } from "./kit5-operator-auth.js";
import { handleOnboardingProxy, handleOnboardingDocsProxy, isOnboardingDocsRoute } from "./routes/onboarding-proxy.js";
import type { OnboardingWebhookForwarder } from "../webhooks/onboarding-forwarder.js";

export function createHttpServer(
  config: ServerConfig,
  pool?: DatabasePool,
  onboardingForwarder?: OnboardingWebhookForwarder,
): Server {
  return createServer(createRequestHandler(config, pool, onboardingForwarder));
}

/** Node retains onboarding and mapping tooling; financial APIs are retired. */
export function createRequestHandler(
  config: ServerConfig,
  pool?: DatabasePool,
  onboardingForwarder?: OnboardingWebhookForwarder,
): RequestListener {
  return (request, response) => {
    const pathname = (request.url ?? "").split("?", 1)[0]!;
    if (isRetiredFinancialRoute(pathname)) {
      response.writeHead(410, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify({ error: "legacy_funding_retired", message: "Use the Funding and Collection service's authenticated offer workflow." }));
      return;
    }
    if (/^\/api\/customers\/[^/]+\/snapshot\/?$/.test(pathname)) {
      response.writeHead(410, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify({ error: "legacy_customer_snapshot_retired", message: "Use the Go underwriting customer-context contract with its service authentication." }));
      return;
    }
    if (pathname === "/api/ledger" || pathname === "/api/ledger/") {
      response.writeHead(410, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify({ error: "legacy_ledger_retired", message: "Use the authenticated Go financial reads. Historical Node records have not been migrated by this retirement." }));
      return;
    }
    const operatorAuth = {
      operatorToken: config.kit5OperatorToken,
      operatorId: config.kit5OperatorId,
      allowedOrigins: config.kit5AllowedOrigins,
    };
    if (isOnboardingDocsRoute(request.url)) {
      handleOnboardingDocsProxy(request, response, { upstreamUrl: config.onboardingServiceUrl });
      return;
    }
    if (isOnboardingRoute(request.url)) {
      handleOnboardingProxy(request, response, {
        upstreamUrl: config.onboardingServiceUrl,
        serviceSecret: config.onboardingServiceSecret,
        operatorAuth,
      });
      return;
    }
    // This established session endpoint is also used by the onboarding UI.
    if (request.url === "/api/kit5/session" && request.method === "OPTIONS") {
      handleKit5Preflight(request, response, operatorAuth);
      return;
    }
    if (request.url === "/api/kit5/session" && request.method === "POST") {
      handleKit5Session(request, response, operatorAuth);
      return;
    }
    if (request.method === "POST" && request.url === "/webhooks/airwallex" && pool) {
      void handleAirwallexWebhook(request, response, {
        pool,
        secret: config.airwallexWebhookSecret,
        onReceived: () => onboardingForwarder?.nudge(),
      });
      return;
    }
    handleRequest(request, response);
  };
}

function isRetiredFinancialRoute(pathname: string): boolean {
  return pathname === "/api/remote-authorization" || pathname === "/api/bridge-decisions" ||
    (pathname.startsWith("/api/kit5/") && pathname !== "/api/kit5/session");
}

function isOnboardingRoute(url: string | undefined): boolean {
  const pathname = (url ?? "").split("?", 1)[0]!;
  return ["/api/onboarding", "/api/companies", "/api/onboarding-applications", "/api/submissions", "/api/platform-accounts"]
    .some((route) => pathname === route || pathname.startsWith(route + "/"));
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
