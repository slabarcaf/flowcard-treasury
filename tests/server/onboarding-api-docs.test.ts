import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { PassThrough, Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createRequestHandler } from "../../server/src/http/server.js";
import {
  ALLOWED_DOCS_ASSETS,
  isOnboardingDocsRoute,
  resolveDocsTargetResourcePath,
  handleOnboardingDocsProxy,
  PathGuardError,
} from "../../server/src/http/routes/onboarding-proxy.js";

const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
const operatorId = "operator-fixture-123";
const allowedOrigin = "https://app.flowcard.test";

function responseRecorder() {
  let statusCode = 0;
  const headers: Record<string, string | string[]> = {};
  const passThrough = new PassThrough();
  let body = "";
  passThrough.on("data", (chunk) => {
    body += chunk.toString("utf8");
  });
  const origEnd = passThrough.end.bind(passThrough);
  const origWrite = passThrough.write.bind(passThrough);

  const response = Object.assign(passThrough, {
    headersSent: false,
    setHeader(name: string, value: string | string[]) {
      headers[name.toLowerCase()] = value;
      return this;
    },
    writeHead(code: number, values?: Record<string, string | string[]>) {
      statusCode = code;
      if (values) {
        for (const [k, v] of Object.entries(values)) {
          headers[k.toLowerCase()] = v;
        }
      }
      this.headersSent = true;
      return this;
    },
    write(chunk: any, encoding?: any, cb?: any) {
      return origWrite(chunk, encoding, cb);
    },
    end(value?: any, encoding?: any, cb?: any) {
      this.headersSent = true;
      if (value) origWrite(value);
      origEnd(encoding, cb);
      return this;
    },
  }) as unknown as ServerResponse;

  return { response, headers, status: () => statusCode, body: () => body };
}

describe("Onboarding Documentation Route Matching & Path Guard", () => {
  it("matches valid public documentation paths exactly", () => {
    expect(isOnboardingDocsRoute("/docs/api")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/index.html")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/openapi.json")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/webhooks.json")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/swagger-ui.css")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/swagger-ui-bundle.js")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/swagger-ui-standalone-preset.js")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/favicon-32x32.png")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/assets/swagger-ui.css")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/assets/swagger-ui-bundle.js")).toBe(true);
  });

  it("permits harmless query strings without modifying route resolution", () => {
    expect(isOnboardingDocsRoute("/docs/api/?urls.primaryName=Operator+API")).toBe(true);
    expect(isOnboardingDocsRoute("/docs/api/openapi.json?version=3.1")).toBe(true);
    expect(resolveDocsTargetResourcePath("/docs/api/openapi.json?version=3.1")).toBe("/docs/api/openapi.json?version=3.1");
  });

  it("strictly rejects out-of-bounds, unapproved, and internal paths", () => {
    expect(isOnboardingDocsRoute(undefined)).toBe(false);
    expect(isOnboardingDocsRoute("")).toBe(false);
    expect(isOnboardingDocsRoute("/")).toBe(false);
    expect(isOnboardingDocsRoute("/docs")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/plans")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/plans/20261110-onboarding-openapi-documentation.md")).toBe(false);
    expect(isOnboardingDocsRoute("/api/companies")).toBe(false);
    expect(isOnboardingDocsRoute("/api/onboarding-applications")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api/unknown-file.txt")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api/private.key")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api/assets/secret.env")).toBe(false);
  });

  it("strictly rejects path traversal and null bytes in all encodings", () => {
    expect(isOnboardingDocsRoute("/docs/api/../plans")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api/..%2fplans")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api/%2e%2e/plans")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api/%252e%252e/plans")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api%00")).toBe(false);
    expect(isOnboardingDocsRoute("/docs/api\0")).toBe(false);

    expect(() => resolveDocsTargetResourcePath("/docs/api/../etc/passwd")).toThrow(PathGuardError);
    expect(() => resolveDocsTargetResourcePath("/docs/api/%2e%2e/plans")).toThrow(PathGuardError);
    expect(() => resolveDocsTargetResourcePath("/docs/api/%252e%252e/plans")).toThrow(PathGuardError);
    expect(() => resolveDocsTargetResourcePath("/docs/api%00/secret")).toThrow(PathGuardError);
    expect(() => resolveDocsTargetResourcePath("/docs/api\0")).toThrow(PathGuardError);
  });

  it("contains all required bundled assets in the allowlist", () => {
    expect(ALLOWED_DOCS_ASSETS.has("index.html")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("swagger-ui-bundle.js")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("swagger-ui-standalone-preset.js")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("swagger-ui.css")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("favicon-16x16.png")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("favicon-32x32.png")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("LICENSE")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("NOTICE")).toBe(true);
    expect(ALLOWED_DOCS_ASSETS.has("VERSION")).toBe(true);
  });
});

describe("Onboarding Documentation Gateway Proxy (End-to-End)", () => {
  let mockGoServer: Server;
  let mockGoPort: number;
  let lastUpstreamRequest: {
    method?: string;
    url?: string;
    headers?: Record<string, string | string[]>;
  } = {};

  // 1.8 MiB payload to test streaming beyond the 1 MiB JSON cap
  const largeAssetPayload = Buffer.alloc(1_887_436, "A");

  beforeAll(async () => {
    mockGoServer = createServer((req, res) => {
      lastUpstreamRequest = {
        method: req.method,
        url: req.url,
        headers: req.headers,
      };

      if (req.url?.includes("timeout")) {
        return; // hang indefinitely for timeout test
      }

      // Mimic Go handler /docs/api redirect
      if (req.url === "/docs/api") {
        res.writeHead(301, {
          Location: "/docs/api/",
          "Content-Type": "text/html; charset=utf-8",
        });
        res.end();
        return;
      }

      // Mimic Go handler /docs/api/
      if (req.url === "/docs/api/" || req.url === "/docs/api/index.html") {
        res.writeHead(200, {
          "Content-Type": "text/html; charset=utf-8",
          "Cache-Control": "no-cache, must-revalidate",
          "x-internal-go-debug": "strip-this-header",
        });
        if (req.method === "HEAD") {
          res.end();
          return;
        }
        res.end("<!DOCTYPE html><html><head><title>FlowCard API Docs</title></head><body><div id=\"swagger-ui\"></div></body></html>");
        return;
      }

      // Mimic Go handler /docs/api/openapi.json
      if (req.url === "/docs/api/openapi.json") {
        res.writeHead(200, {
          "Content-Type": "application/json; charset=utf-8",
          "Cache-Control": "no-cache, must-revalidate",
        });
        if (req.method === "HEAD") {
          res.end();
          return;
        }
        res.end(JSON.stringify({
          openapi: "3.1.0",
          info: { title: "FlowCard Operator Onboarding API", version: "1.0.0" },
        }));
        return;
      }

      // Mimic Go handler /docs/api/webhooks.json
      if (req.url === "/docs/api/webhooks.json") {
        res.writeHead(200, {
          "Content-Type": "application/json; charset=utf-8",
          "Cache-Control": "no-cache, must-revalidate",
        });
        if (req.method === "HEAD") {
          res.end();
          return;
        }
        res.end(JSON.stringify({
          openapi: "3.1.0",
          info: { title: "FlowCard Internal Webhook Receiver API", version: "1.0.0" },
        }));
        return;
      }

      // Mimic Go handler large bundled asset streaming (>1 MiB)
      if (req.url === "/docs/api/swagger-ui-bundle.js" || req.url === "/docs/api/assets/swagger-ui-bundle.js") {
        res.writeHead(200, {
          "Content-Type": "application/javascript; charset=utf-8",
          "Content-Length": String(largeAssetPayload.length),
          "Cache-Control": "no-cache, must-revalidate",
        });
        if (req.method === "HEAD") {
          res.end();
          return;
        }
        res.end(largeAssetPayload);
        return;
      }

      // Standard small asset
      if (req.url === "/docs/api/swagger-ui.css") {
        res.writeHead(200, {
          "Content-Type": "text/css; charset=utf-8",
          "Cache-Control": "no-cache, must-revalidate",
        });
        if (req.method === "HEAD") {
          res.end();
          return;
        }
        res.end("/* swagger-ui css */");
        return;
      }

      // Default not found
      res.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
      res.end("404 page not found\n");
    });

    await new Promise<void>((resolve) => {
      mockGoServer.listen(0, "127.0.0.1", () => {
        mockGoPort = (mockGoServer.address() as AddressInfo).port;
        resolve();
      });
    });
  });

  afterAll(async () => {
    await new Promise<void>((resolve) => mockGoServer.close(() => resolve()));
  });

  const getHandler = (upstreamUrl?: string) => {
    return createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: upstreamUrl ?? `http://127.0.0.1:${mockGoPort}`,
      onboardingServiceSecret: "test-secret-at-least-32-bytes-long-123456",
    });
  };

  it("proxies /docs/api and forwards 301 redirect to /docs/api/", async () => {
    const handler = getHandler();
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api";
    req.headers = { host: "app.flowcard.test" };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 80));

    expect(recorder.status()).toBe(301);
    expect(recorder.headers["location"]).toBe("/docs/api/");
    expect(lastUpstreamRequest.url).toBe("/docs/api");
  });

  it("serves /docs/api/ publicly without authentication or cookies", async () => {
    const handler = getHandler();
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api/";
    req.headers = {
      host: "app.flowcard.test",
      accept: "text/html,application/xhtml+xml",
    };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 80));

    expect(recorder.status()).toBe(200);
    expect(recorder.headers["content-type"]).toBe("text/html; charset=utf-8");
    expect(recorder.headers["x-internal-go-debug"]).toBeUndefined();
    expect(recorder.body()).toContain("FlowCard API Docs");
  });

  it("strips client cookies and authorization headers before forwarding upstream", async () => {
    const handler = getHandler();
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api/openapi.json";
    req.headers = {
      host: "app.flowcard.test",
      cookie: "kit5_operator_session=secret-session-cookie-value; stolen_token=123",
      authorization: "Bearer super-secret-operator-bearer-token",
      "x-custom-forward": "ignore-me",
    };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 80));

    expect(recorder.status()).toBe(200);
    // Upstream MUST NOT receive client credentials or authorization
    expect(lastUpstreamRequest.headers?.["cookie"]).toBeUndefined();
    expect(lastUpstreamRequest.headers?.["authorization"]).toBeUndefined();
    // Upstream MUST NOT receive a service JWT assertion (docs are public)
    expect(lastUpstreamRequest.headers?.["authorization"]).toBeUndefined();
  });

  it("serves both openapi.json and webhooks.json specs with exact audience separation", async () => {
    const handler = getHandler();

    // 1. Operator spec
    const recorder1 = responseRecorder();
    const req1 = Readable.from([]) as unknown as IncomingMessage;
    req1.method = "GET";
    req1.url = "/docs/api/openapi.json";
    req1.headers = { host: "app.flowcard.test" };
    handler(req1, recorder1.response);
    await new Promise((resolve) => setTimeout(resolve, 80));

    expect(recorder1.status()).toBe(200);
    const spec1 = JSON.parse(recorder1.body());
    expect(spec1.openapi).toBe("3.1.0");
    expect(spec1.info.title).toBe("FlowCard Operator Onboarding API");

    // 2. Webhooks spec
    const recorder2 = responseRecorder();
    const req2 = Readable.from([]) as unknown as IncomingMessage;
    req2.method = "GET";
    req2.url = "/docs/api/webhooks.json";
    req2.headers = { host: "app.flowcard.test" };
    handler(req2, recorder2.response);
    await new Promise((resolve) => setTimeout(resolve, 80));

    expect(recorder2.status()).toBe(200);
    const spec2 = JSON.parse(recorder2.body());
    expect(spec2.openapi).toBe("3.1.0");
    expect(spec2.info.title).toBe("FlowCard Internal Webhook Receiver API");
  });

  it("streams large static assets (>1 MiB) without triggering payload_too_large 413", async () => {
    const handler = getHandler();
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api/swagger-ui-bundle.js";
    req.headers = { host: "app.flowcard.test" };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 120));

    expect(recorder.status()).toBe(200);
    expect(recorder.headers["content-type"]).toBe("application/javascript; charset=utf-8");
    expect(recorder.headers["content-length"]).toBe(String(largeAssetPayload.length));
    expect(recorder.body().length).toBe(largeAssetPayload.length);
  });

  it("supports HEAD requests with 200 status and empty body", async () => {
    const handler = getHandler();
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "HEAD";
    req.url = "/docs/api/swagger-ui.css";
    req.headers = { host: "app.flowcard.test" };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 80));

    expect(recorder.status()).toBe(200);
    expect(recorder.headers["content-type"]).toBe("text/css; charset=utf-8");
    expect(recorder.body()).toBe("");
  });

  it("strictly rejects mutating methods with 405 Method Not Allowed and Allow: GET, HEAD", async () => {
    const handler = getHandler();
    const mutatingMethods = ["POST", "PUT", "DELETE", "PATCH"];

    for (const method of mutatingMethods) {
      const recorder = responseRecorder();
      const req = Readable.from(["{\"data\":\"tamper\"}"]) as unknown as IncomingMessage;
      req.method = method;
      req.url = "/docs/api/";
      req.headers = { host: "app.flowcard.test", "content-type": "application/json" };

      handler(req, recorder.response);
      await new Promise((resolve) => setTimeout(resolve, 50));

      expect(recorder.status()).toBe(405);
      expect(recorder.headers["allow"]).toBe("GET, HEAD");
      const err = JSON.parse(recorder.body());
      expect(err.error).toBe("method_not_allowed");
    }
  });

  it("returns 503 onboarding_service_unconfigured when upstreamUrl is not configured", async () => {
    const handler = getHandler(""); // unconfigured upstream
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api/";
    req.headers = { host: "app.flowcard.test" };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 50));

    expect(recorder.status()).toBe(503);
    const err = JSON.parse(recorder.body());
    expect(err.error).toBe("onboarding_service_unconfigured");
  });

  it("returns 502 onboarding_service_unavailable when upstream Go service is down", async () => {
    // Port 59999 has no running service
    const handler = getHandler("http://127.0.0.1:59999");
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api/";
    req.headers = { host: "app.flowcard.test" };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 100));

    expect(recorder.status()).toBe(502);
    const err = JSON.parse(recorder.body());
    expect(err.error).toBe("onboarding_service_unavailable");
  });

  it("returns 504 onboarding_service_timeout when upstream Go service times out", async () => {
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api/index.html?timeout=1";
    req.headers = { host: "app.flowcard.test" };

    handleOnboardingDocsProxy(req, recorder.response, {
      upstreamUrl: `http://127.0.0.1:${mockGoPort}`,
      upstreamTimeoutMs: 50, // fast timeout for test
    });

    await new Promise((resolve) => setTimeout(resolve, 120));

    expect(recorder.status()).toBe(504);
    const err = JSON.parse(recorder.body());
    expect(err.error).toBe("onboarding_service_timeout");
  });

  it("preserves operator authentication requirement on business routes (/api/companies)", async () => {
    const handler = getHandler();
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/api/companies";
    req.headers = {
      host: "app.flowcard.test",
      origin: allowedOrigin,
    };

    handler(req, recorder.response);
    await new Promise((resolve) => setTimeout(resolve, 50));

    // Must be rejected with 401: operator auth is still required for business APIs
    expect(recorder.status()).toBe(401);
    const err = JSON.parse(recorder.body());
    expect(err.error).toBe("operator_auth_required");
  });

  it("rejects unauthorized paths and path traversal attempts with 400 or 404", async () => {
    const handler = getHandler();

    const traversalPaths = [
      "/docs/api/../docs/plans/20261110-onboarding-openapi-documentation.md",
      "/docs/api/..%2fpackage.json",
      "/docs/api/%2e%2e/server.ts",
      "/docs/api/%252e%252e/server.ts",
      "/docs/api/secret.env",
      "/docs/plans",
      "/docs",
    ];

    for (const path of traversalPaths) {
      const recorder = responseRecorder();
      const req = Readable.from([]) as unknown as IncomingMessage;
      req.method = "GET";
      req.url = path;
      req.headers = { host: "app.flowcard.test" };

      handler(req, recorder.response);
      await new Promise((resolve) => setTimeout(resolve, 50));

      expect([400, 404]).toContain(recorder.status());
      // Upstream should never have received traversal requests
      if (lastUpstreamRequest.url) {
        expect(lastUpstreamRequest.url).not.toContain("package.json");
        expect(lastUpstreamRequest.url).not.toContain("server.ts");
        expect(lastUpstreamRequest.url).not.toContain("plans");
      }
    }
  });

  it("handleOnboardingDocsProxy returns 400 PathGuardError on direct traversal invocation", () => {
    const recorder = responseRecorder();
    const req = Readable.from([]) as unknown as IncomingMessage;
    req.method = "GET";
    req.url = "/docs/api/../etc/passwd";
    req.headers = { host: "app.flowcard.test" };

    handleOnboardingDocsProxy(req, recorder.response, {
      upstreamUrl: `http://127.0.0.1:${mockGoPort}`,
    });

    expect(recorder.status()).toBe(400);
    const err = JSON.parse(recorder.body());
    expect(err.error).toBe("invalid_path_traversal");
  });
});
