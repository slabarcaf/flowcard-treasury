import { createHmac } from "node:crypto";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { PassThrough, Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createRequestHandler } from "../../server/src/http/server.js";
import { createOperatorSessionCookie } from "../../server/src/http/kit5-operator-auth.js";
import {
  createServiceAssertion,
  verifyServiceAssertion,
  DEFAULT_ISSUER,
  DEFAULT_OPERATOR_AUDIENCE,
  DEFAULT_WEBHOOK_AUDIENCE,
} from "../../server/src/http/onboarding-proxy-auth.js";
import {
  validateUpstreamUrl,
  resolveTargetResourcePath,
  handleOnboardingProxy,
} from "../../server/src/http/routes/onboarding-proxy.js";

const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
const operatorId = "operator-fixture-123";
const allowedOrigin = "https://app.flowcard.test";
const serviceSecret = "backend-service-secret-at-least-32-bytes-long-12345";

function responseRecorder() {
  let statusCode = 0;
  const headers: Record<string, string | string[]> = {};
  const passThrough = new PassThrough();
  let body = "";
  passThrough.on("data", (chunk) => { body += chunk.toString("utf8"); });
  const origEnd = passThrough.end.bind(passThrough);
  const origWrite = passThrough.write.bind(passThrough);

  const response = Object.assign(passThrough, {
    headersSent: false,
    setHeader(name: string, value: string | string[]) { headers[name.toLowerCase()] = value; return this; },
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

describe("Onboarding Service-to-Service JWT Assertion", () => {
  it("creates and verifies a valid short-lived HS256 JWT assertion", () => {
    const token = createServiceAssertion({
      secret: serviceSecret,
      subject: operatorId,
      issuer: DEFAULT_ISSUER,
      audience: DEFAULT_OPERATOR_AUDIENCE,
      ttlSeconds: 30,
    });

    const claims = verifyServiceAssertion(token, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
      expectedIssuer: DEFAULT_ISSUER,
    });

    expect(claims.sub).toBe(operatorId);
    expect(claims.iss).toBe(DEFAULT_ISSUER);
    expect(claims.aud).toBe(DEFAULT_OPERATOR_AUDIENCE);
    expect(claims.exp - claims.iat).toBe(30);
    expect(claims.jti).toBeDefined();
  });

  it("fails closed on secret shorter than 32 characters or empty subject", () => {
    expect(() => createServiceAssertion({
      secret: "too-short",
      subject: operatorId,
    })).toThrow(/at least 32 characters/);

    expect(() => createServiceAssertion({
      secret: serviceSecret,
      subject: "   ",
    })).toThrow(/must not be empty/);
  });

  it("strictly rejects algorithm confusion such as alg: none or RS256", () => {
    const headerNone = Buffer.from(JSON.stringify({ alg: "none", typ: "JWT" })).toString("base64url");
    const payload = Buffer.from(JSON.stringify({
      iss: DEFAULT_ISSUER,
      aud: DEFAULT_OPERATOR_AUDIENCE,
      sub: operatorId,
      iat: Math.floor(Date.now() / 1000),
      exp: Math.floor(Date.now() / 1000) + 30,
    })).toString("base64url");
    const tokenNone = `${headerNone}.${payload}.`;

    expect(() => verifyServiceAssertion(tokenNone, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
    })).toThrow(/unsupported or disallowed JWT algorithm/);

    const headerRS256 = Buffer.from(JSON.stringify({ alg: "RS256", typ: "JWT" })).toString("base64url");
    const tokenRS256 = `${headerRS256}.${payload}.some-sig`;
    expect(() => verifyServiceAssertion(tokenRS256, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
    })).toThrow(/unsupported or disallowed JWT algorithm/);
  });

  it("strictly rejects tampered payload and tampered signature", () => {
    const token = createServiceAssertion({
      secret: serviceSecret,
      subject: operatorId,
    });
    const parts = token.split(".");

    // Tamper with payload (sub)
    const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
    payload.sub = "spoofed-admin";
    const tamperedPayload = Buffer.from(JSON.stringify(payload)).toString("base64url");
    const tamperedToken = `${parts[0]}.${tamperedPayload}.${parts[2]}`;

    expect(() => verifyServiceAssertion(tamperedToken, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
    })).toThrow(/invalid JWT signature/);

    // Tamper with signature
    const badSigToken = `${parts[0]}.${parts[1]}.invalid-signature`;
    expect(() => verifyServiceAssertion(badSigToken, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
    })).toThrow(/invalid JWT signature/);
  });

  it("strictly rejects expired tokens and excessive TTL", () => {
    const now = Math.floor(Date.now() / 1000);

    // Expired token
    const expiredToken = createServiceAssertion({
      secret: serviceSecret,
      subject: operatorId,
      now: now - 60,
      ttlSeconds: 30,
    });
    expect(() => verifyServiceAssertion(expiredToken, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
      now,
    })).toThrow(/has expired/);

    // Token with excessive TTL (> 30s)
    const longTtlToken = createServiceAssertion({
      secret: serviceSecret,
      subject: operatorId,
      now,
      ttlSeconds: 3600,
    });
    // Manually forge payload with 1 hour TTL
    const parts = longTtlToken.split(".");
    const longPayload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
    longPayload.exp = now + 3600;
    const reEncodedPayload = Buffer.from(JSON.stringify(longPayload)).toString("base64url");
    const sig = createHmac("sha256", serviceSecret).update(`${parts[0]}.${reEncodedPayload}`).digest("base64url");
    const reSigned = `${parts[0]}.${reEncodedPayload}.${sig}`;
    expect(() => verifyServiceAssertion(reSigned, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
      maxTtlSeconds: 30,
      now,
    })).toThrow(/exceeds maximum permitted/);
  });

  it("rejects audience mismatch (e.g. operator token cannot be used for webhook)", () => {
    const operatorAssertion = createServiceAssertion({
      secret: serviceSecret,
      subject: operatorId,
      audience: DEFAULT_OPERATOR_AUDIENCE,
    });

    expect(() => verifyServiceAssertion(operatorAssertion, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_WEBHOOK_AUDIENCE,
    })).toThrow(/audience mismatch/);
  });
});

describe("Onboarding Upstream URL & Path Guard Validation", () => {
  it("validates upstream URLs strictly: allows http/https host without path or query; rejects userinfo, path, query, hash", () => {
    // Valid
    expect(validateUpstreamUrl("http://127.0.0.1:8080").origin).toBe("http://127.0.0.1:8080");
    expect(validateUpstreamUrl("https://api.internal.local:8443/").origin).toBe("https://api.internal.local:8443");

    // Invalid scheme
    expect(() => validateUpstreamUrl("ftp://127.0.0.1:8080")).toThrow(/upstream protocol must be http or https/);
    expect(() => validateUpstreamUrl("javascript:alert(1)")).toThrow();

    // Userinfo credentials forbidden
    expect(() => validateUpstreamUrl("http://admin:secret@127.0.0.1:8080")).toThrow(/must not contain userinfo/);

    // Path prefix forbidden
    expect(() => validateUpstreamUrl("http://127.0.0.1:8080/api/v1")).toThrow(/must not contain a path prefix/);

    // Query or hash forbidden
    expect(() => validateUpstreamUrl("http://127.0.0.1:8080/?query=1")).toThrow(/must not contain query or fragment/);
    expect(() => validateUpstreamUrl("http://127.0.0.1:8080/#hash")).toThrow(/must not contain query or fragment/);
  });

  it("strictly rejects path traversal in all encoded variations (mixed case hex, double encoding, null bytes)", () => {
    // Standard traversal
    expect(() => resolveTargetResourcePath("/api/onboarding/../../etc/passwd")).toThrow(/invalid_path_traversal/);
    // Uppercase hex %2E%2E
    expect(() => resolveTargetResourcePath("/api/onboarding/%2E%2E/%2E%2E/etc/passwd")).toThrow(/invalid_path_traversal/);
    // Mixed case hex %2e%2E and %2E%2e
    expect(() => resolveTargetResourcePath("/api/onboarding/%2e%2E/companies")).toThrow(/invalid_path_traversal/);
    expect(() => resolveTargetResourcePath("/api/onboarding/%2E%2e/companies")).toThrow(/invalid_path_traversal/);
    // Double encoded %252e%252e
    expect(() => resolveTargetResourcePath("/api/onboarding/%252e%252e/companies")).toThrow(/invalid_path_traversal/);
    // Null byte injection
    expect(() => resolveTargetResourcePath("/api/companies%00/secret")).toThrow(/invalid_path_traversal/);
  });

  it("permits harmless .. within query parameters without triggering path traversal", () => {
    const res = resolveTargetResourcePath("/api/companies?filter=name..corp&search=..test..");
    expect(res).not.toBeNull();
    expect(res?.targetPath).toBe("/api/companies?filter=name..corp&search=..test..");
  });

  it("enforces explicit resource route allowlist and denies webhooks, internal, or non-matching routes", () => {
    // Direct resource paths allowed
    expect(resolveTargetResourcePath("/api/companies")?.targetPath).toBe("/api/companies");
    expect(resolveTargetResourcePath("/api/onboarding-applications/app_123")?.targetPath).toBe("/api/onboarding-applications/app_123");
    expect(resolveTargetResourcePath("/api/submissions/sub_123")?.targetPath).toBe("/api/submissions/sub_123");
    expect(resolveTargetResourcePath("/api/platform-accounts/acct_123")?.targetPath).toBe("/api/platform-accounts/acct_123");

    // Gateway alias paths mapped to Go resource endpoints without /v1
    expect(resolveTargetResourcePath("/api/onboarding/companies")?.targetPath).toBe("/api/companies");
    expect(resolveTargetResourcePath("/api/onboarding/onboarding-applications")?.targetPath).toBe("/api/onboarding-applications");
    expect(resolveTargetResourcePath("/api/onboarding/applications")?.targetPath).toBe("/api/onboarding-applications");
    expect(resolveTargetResourcePath("/api/onboarding/submissions/sub_1")?.targetPath).toBe("/api/submissions/sub_1");
    expect(resolveTargetResourcePath("/api/onboarding/platform-accounts/acct_1")?.targetPath).toBe("/api/platform-accounts/acct_1");

    // Denied routes: webhooks, health, or unmatched prefixes
    expect(resolveTargetResourcePath("/api/webhooks/airwallex")).toBeNull();
    expect(resolveTargetResourcePath("/api/onboarding/webhooks/airwallex")).toBeNull();
    expect(resolveTargetResourcePath("/api/onboarding-fraud")).toBeNull();
    expect(resolveTargetResourcePath("/api/onboarding_test")).toBeNull();
    expect(resolveTargetResourcePath("/healthz")).toBeNull();
  });
});

describe("Onboarding Node Proxy Route (End-to-End)", () => {
  let mockGoServer: Server;
  let mockGoPort: number;
  let lastUpstreamRequest: {
    method?: string;
    url?: string;
    headers?: Record<string, string | string[]>;
    body?: string;
  } = {};

  beforeAll(async () => {
    // Emulates the Go service router: rejects /api/v1 with 404, handles /api/companies and /api/onboarding-applications
    mockGoServer = createServer((req, res) => {
      let body = "";
      req.on("data", (chunk) => { body += chunk; });
      req.on("end", () => {
        lastUpstreamRequest = {
          method: req.method,
          url: req.url,
          headers: req.headers,
          body,
        };

        if (req.url?.includes("timeout")) {
          return; // hang for timeout testing
        }

        // Rejection of erroneous /api/v1 paths
        if (req.url?.startsWith("/api/v1/")) {
          res.writeHead(404, { "content-type": "application/json" });
          res.end(JSON.stringify({ code: "NOT_FOUND", message: "route not found (Go routes are mounted at /api/*)" }));
          return;
        }

        const validPrefixes = ["/api/companies", "/api/onboarding-applications", "/api/submissions", "/api/platform-accounts"];
        const matches = validPrefixes.some((p) => req.url === p || req.url?.startsWith(p + "/") || req.url?.startsWith(p + "?"));

        if (!matches) {
          res.writeHead(404, { "content-type": "application/json" });
          res.end(JSON.stringify({ error: "not_found" }));
          return;
        }

        res.writeHead(200, {
          "content-type": "application/json",
          "cache-control": "no-store",
          "x-custom-go-header": "test-value",
          "x-internal-secret-header": "must-be-stripped",
        });
        res.end(JSON.stringify({ status: "success", receivedPath: req.url, method: req.method }));
      });
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

  const validSessionCookie = createOperatorSessionCookie(operatorToken, Date.now(), operatorId);

  function createProxyHandler(allowedOrigins: readonly string[]) {
    return createRequestHandler({
      port: 0,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: allowedOrigins,
      onboardingServiceUrl: `http://127.0.0.1:${mockGoPort}`,
      onboardingServiceSecret: serviceSecret,
    });
  }

  it("serves authenticated same-origin reads without Origin through a local Node listener", async () => {
    let proxyHandler: ReturnType<typeof createRequestHandler> | undefined;
    let browserGetHeaders: IncomingMessage["headers"] | undefined;
    const localServer = createServer((request, response) => {
      if (!proxyHandler) {
        response.writeHead(503);
        response.end();
        return;
      }
      if (request.method === "GET" && request.url === "/api/companies") {
        browserGetHeaders = request.headers;
      }
      proxyHandler(request, response);
    });

    await new Promise<void>((resolve) => localServer.listen(0, "127.0.0.1", () => resolve()));
    const address = localServer.address() as AddressInfo;
    const localOrigin = `http://127.0.0.1:${address.port}`;
    proxyHandler = createProxyHandler([localOrigin]);

    try {
      for (const method of ["GET", "HEAD"] as const) {
        const result = await fetch(`${localOrigin}/api/companies`, {
          method,
          headers: {
            cookie: `kit5_operator_session=${validSessionCookie}`,
            referer: `${localOrigin}/#/onboarding`,
            "sec-fetch-site": "same-origin",
          },
        });
        expect(result.status).toBe(200);
        expect(result.headers.get("vary")).toContain("Referer");
        expect(result.headers.get("cache-control")).toBe("no-store");
        expect(browserGetHeaders?.origin).toBeUndefined();
        expect(browserGetHeaders?.referer).toBe(`${localOrigin}/#/onboarding`);
        expect(browserGetHeaders?.["sec-fetch-site"]).toBe("same-origin");
      }
      expect(lastUpstreamRequest.method).toBe("HEAD");
      expect(lastUpstreamRequest.headers?.authorization).toMatch(/^Bearer /);
      expect(lastUpstreamRequest.headers?.cookie).toBeUndefined();
    } finally {
      await new Promise<void>((resolve, reject) => localServer.close(error => error ? reject(error) : resolve()));
    }
  });

  it("rejects cross-site or forged origins even with a valid session cookie", async () => {
    const rejectedRequests: Array<{ allowedOrigins: readonly string[]; headers: Record<string, string> }> = [
      { allowedOrigins: [allowedOrigin], headers: { referer: "https://attacker.example/page", "sec-fetch-site": "same-origin" } },
      { allowedOrigins: [allowedOrigin], headers: { referer: `${allowedOrigin}/page`, "sec-fetch-site": "cross-site" } },
      { allowedOrigins: [allowedOrigin], headers: { referer: `${allowedOrigin}/page`, "sec-fetch-site": "same-site" } },
      { allowedOrigins: [allowedOrigin], headers: { origin: "null", referer: `${allowedOrigin}/page`, "sec-fetch-site": "same-origin" } },
      { allowedOrigins: [allowedOrigin], headers: { origin: "", referer: `${allowedOrigin}/page`, "sec-fetch-site": "same-origin" } },
      { allowedOrigins: [allowedOrigin], headers: { origin: "https://attacker.example", referer: `${allowedOrigin}/page`, "sec-fetch-site": "same-origin" } },
      { allowedOrigins: [allowedOrigin], headers: { referer: "not a URL", "sec-fetch-site": "same-origin" } },
      { allowedOrigins: [allowedOrigin], headers: { referer: "https://user:pass@app.flowcard.test/page", "sec-fetch-site": "same-origin" } },
      { allowedOrigins: [allowedOrigin], headers: { referer: "blob:https://app.flowcard.test/document-id", "sec-fetch-site": "same-origin" } },
      { allowedOrigins: ["ftp://app.flowcard.test"], headers: { referer: "ftp://app.flowcard.test/page", "sec-fetch-site": "same-origin" } },
    ];

    for (const testCase of rejectedRequests) {
      const handler = createProxyHandler(testCase.allowedOrigins);
      const request = Object.assign(Readable.from([]), {
        method: "GET",
        url: "/api/companies",
        headers: { ...testCase.headers, cookie: `kit5_operator_session=${validSessionCookie}` },
      }) as IncomingMessage;
      const recorder = responseRecorder();
      handler(request, recorder.response);
      await new Promise((resolve) => setImmediate(resolve));
      expect(recorder.status()).toBe(403);
      expect(recorder.headers["cache-control"]).toBe("no-store");
      expect(JSON.parse(recorder.body())).toMatchObject({ error: "origin_forbidden" });
    }
  });

  it("keeps unauthenticated reads, writes, preflight, and session establishment strict", async () => {
    const handler = createProxyHandler([allowedOrigin]);
    const noOriginHeaders = {
      cookie: `kit5_operator_session=${validSessionCookie}`,
      referer: `${allowedOrigin}/#/onboarding`,
      "sec-fetch-site": "same-origin",
    };
    const cases = [
      { method: "PATCH", url: "/api/companies/comp_1", headers: noOriginHeaders, body: "{}", status: 403 },
      { method: "OPTIONS", url: "/api/companies", headers: noOriginHeaders, body: "", status: 403 },
      { method: "POST", url: "/api/kit5/session", headers: { "content-type": "application/json" }, body: JSON.stringify({ token: operatorToken }), status: 403 },
      { method: "GET", url: "/api/companies", headers: { origin: allowedOrigin }, body: "", status: 401 },
    ] as const;

    for (const testCase of cases) {
      const request = Object.assign(Readable.from(testCase.body ? [testCase.body] : []), {
        method: testCase.method,
        url: testCase.url,
        headers: testCase.headers,
      }) as IncomingMessage;
      const recorder = responseRecorder();
      handler(request, recorder.response);
      await new Promise((resolve) => setImmediate(resolve));
      expect(recorder.status()).toBe(testCase.status);
      expect(recorder.headers["cache-control"]).toBe("no-store");
    }
  });

  it("handles CORS preflight including PATCH, idempotency-key, and if-match headers", async () => {
    const handler = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: `http://127.0.0.1:${mockGoPort}`,
      onboardingServiceSecret: serviceSecret,
    });

    const req = Object.assign(Readable.from([]), {
      method: "OPTIONS",
      url: "/api/companies/comp_123",
      headers: { origin: allowedOrigin },
    }) as IncomingMessage;

    const recorder = responseRecorder();
    handler(req, recorder.response);
    await new Promise((resolve) => setImmediate(resolve));

    expect(recorder.status()).toBe(204);
    expect(recorder.headers["access-control-allow-origin"]).toBe(allowedOrigin);
    expect(recorder.headers["access-control-allow-methods"]).toContain("PATCH");
    expect(recorder.headers["access-control-allow-methods"]).toBe("GET, POST, PUT, PATCH, DELETE, OPTIONS");
    expect(recorder.headers["access-control-allow-headers"]).toContain("idempotency-key");
    expect(recorder.headers["access-control-allow-headers"]).toContain("if-match");
  });

  it("denies unauthenticated client requests (missing or invalid cookie)", async () => {
    const handler = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: `http://127.0.0.1:${mockGoPort}`,
      onboardingServiceSecret: serviceSecret,
    });

    const req = Object.assign(Readable.from([]), {
      method: "GET",
      url: "/api/companies",
      headers: { origin: allowedOrigin }, // missing cookie
    }) as IncomingMessage;

    const recorder = responseRecorder();
    handler(req, recorder.response);
    await new Promise((resolve) => setImmediate(resolve));

    expect(recorder.status()).toBe(401);
    expect(JSON.parse(recorder.body())).toMatchObject({ error: "operator_auth_required" });
  });

  it("denies requests from disallowed origins with 403", async () => {
    const handler = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: `http://127.0.0.1:${mockGoPort}`,
      onboardingServiceSecret: serviceSecret,
    });

    const req = Object.assign(Readable.from([]), {
      method: "GET",
      url: "/api/companies",
      headers: {
        origin: "https://evil-attacker.example",
        cookie: `kit5_operator_session=${validSessionCookie}`,
      },
    }) as IncomingMessage;

    const recorder = responseRecorder();
    handler(req, recorder.response);
    await new Promise((resolve) => setImmediate(resolve));

    expect(recorder.status()).toBe(403);
    expect(JSON.parse(recorder.body())).toMatchObject({ error: "origin_forbidden" });
  });

  it("strips client credentials/spoofed headers, signs assertion, forwards PATCH with idempotency-key and maps path to Go cleanly", async () => {
    const handler = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: `http://127.0.0.1:${mockGoPort}`,
      onboardingServiceSecret: serviceSecret,
    });

    const patchPayload = JSON.stringify({ legal_name: "Acme Updated LLC" });
    const req = Object.assign(Readable.from([patchPayload]), {
      method: "PATCH",
      url: "/api/onboarding/companies/comp_123?sync=true",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${validSessionCookie}`,
        "content-type": "application/json",
        authorization: "Bearer client-supplied-token-should-be-stripped",
        "x-actor-id": "spoofed-actor-id",
        "x-actor": "spoofed-actor",
        "idempotency-key": "idem_patch_123",
        "if-match": '"rev_1"',
      },
    }) as IncomingMessage;

    const recorder = responseRecorder();
    handler(req, recorder.response);

    await new Promise((resolve) => setTimeout(resolve, 100));

    expect(recorder.status()).toBe(200);
    expect(recorder.headers["x-custom-go-header"]).toBe("test-value");
    expect(recorder.headers["x-internal-secret-header"]).toBeUndefined();

    // Verify upstream received sanitized headers and signed assertion
    expect(lastUpstreamRequest.method).toBe("PATCH");
    // Path mapped to /api/companies (NOT /api/v1/companies)
    expect(lastUpstreamRequest.url).toBe("/api/companies/comp_123?sync=true");
    expect(lastUpstreamRequest.headers?.["cookie"]).toBeUndefined();
    expect(lastUpstreamRequest.headers?.["x-actor-id"]).toBeUndefined();
    expect(lastUpstreamRequest.headers?.["x-actor"]).toBeUndefined();
    expect(lastUpstreamRequest.headers?.["idempotency-key"]).toBe("idem_patch_123");
    expect(lastUpstreamRequest.headers?.["if-match"]).toBe('"rev_1"');
    expect(lastUpstreamRequest.body).toBe(patchPayload);

    // Verify the Authorization header was replaced with our signed service JWT assertion
    const authHeader = lastUpstreamRequest.headers?.["authorization"] as string;
    expect(authHeader).toMatch(/^Bearer /);
    const jwt = authHeader.slice("Bearer ".length);
    const claims = verifyServiceAssertion(jwt, {
      secret: serviceSecret,
      expectedAudience: DEFAULT_OPERATOR_AUDIENCE,
      expectedIssuer: DEFAULT_ISSUER,
    });
    expect(claims.sub).toBe(operatorId);
  });

  it("routes direct browser resource paths (/api/onboarding-applications) directly to Go", async () => {
    const handler = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: `http://127.0.0.1:${mockGoPort}`,
      onboardingServiceSecret: serviceSecret,
    });

    const req = Object.assign(Readable.from([]), {
      method: "GET",
      url: "/api/onboarding-applications/app_abc",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${validSessionCookie}`,
      },
    }) as IncomingMessage;

    const recorder = responseRecorder();
    handler(req, recorder.response);

    await new Promise((resolve) => setTimeout(resolve, 100));

    expect(recorder.status()).toBe(200);
    expect(lastUpstreamRequest.method).toBe("GET");
    expect(lastUpstreamRequest.url).toBe("/api/onboarding-applications/app_abc");
  });

  it("returns 502 Bad Gateway when upstream Go service is unreachable", async () => {
    const handler = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: "http://127.0.0.1:54321", // non-listening port
      onboardingServiceSecret: serviceSecret,
    });

    const req = Object.assign(Readable.from([]), {
      method: "GET",
      url: "/api/companies",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${validSessionCookie}`,
      },
    }) as IncomingMessage;

    const recorder = responseRecorder();
    handler(req, recorder.response);

    await new Promise((resolve) => setTimeout(resolve, 100));

    expect(recorder.status()).toBe(502);
    expect(JSON.parse(recorder.body())).toMatchObject({ error: "onboarding_service_unavailable" });
  });

  it("returns 504 Gateway Timeout when upstream Go service times out", async () => {
    const req = Object.assign(Readable.from([]), {
      method: "GET",
      url: "/api/companies/timeout",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${validSessionCookie}`,
      },
    }) as IncomingMessage;

    const recorder = responseRecorder();
    handleOnboardingProxy(req, recorder.response, {
      upstreamUrl: `http://127.0.0.1:${mockGoPort}`,
      serviceSecret,
      operatorAuth: {
        operatorToken,
        operatorId,
        allowedOrigins: [allowedOrigin],
      },
      upstreamTimeoutMs: 100, // 100ms timeout for test
    });

    await new Promise((resolve) => setTimeout(resolve, 200));

    expect(recorder.status()).toBe(504);
    expect(JSON.parse(recorder.body())).toMatchObject({ error: "onboarding_service_timeout" });
  });
});
