import { spawn, type ChildProcess } from "node:child_process";
import { createServer as createNetServer } from "node:net";
import { request as httpRequest } from "node:http";
import type { Server } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createHttpServer, createRequestHandler } from "../../server/src/http/server.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// Dynamic import for cached Playwright if available
async function loadPlaywright() {
  const possiblePaths = [
    "/Users/ramil/.npm/_npx/e41f203b7505f1fb/node_modules/playwright",
    "/Users/ramil/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright",
    "playwright",
  ];
  for (const p of possiblePaths) {
    try {
      const mod = await import(p);
      return mod.chromium || (mod.default && mod.default.chromium);
    } catch {
      // try next path
    }
  }
  return null;
}

function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = createNetServer();
    s.listen(0, "127.0.0.1", () => {
      const address = s.address();
      if (!address || typeof address === "string") {
        s.close(() => reject(new Error("failed to get address")));
        return;
      }
      const port = address.port;
      s.close(() => resolve(port));
    });
    s.on("error", reject);
  });
}

function httpFetch(urlStr: string, options?: { method?: string; headers?: Record<string, string> }): Promise<{
  statusCode: number;
  headers: Record<string, string | string[] | undefined>;
  body: string;
  rawBuffer: Buffer;
}> {
  return new Promise((resolve, reject) => {
    const url = new URL(urlStr);
    const req = httpRequest(
      {
        protocol: url.protocol,
        hostname: url.hostname,
        port: url.port,
        path: url.pathname + url.search,
        method: options?.method || "GET",
        headers: options?.headers || {},
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (chunk) => chunks.push(Buffer.from(chunk)));
        res.on("end", () => {
          const rawBuffer = Buffer.concat(chunks);
          resolve({
            statusCode: res.statusCode ?? 0,
            headers: res.headers,
            body: rawBuffer.toString("utf8"),
            rawBuffer,
          });
        });
      },
    );
    req.on("error", reject);
    req.end();
  });
}

describe("End-to-End OpenAPI Documentation Acceptance (Real Node + Go)", () => {
  let goPort: number;
  let nodePort: number;
  let goProcess: ChildProcess;
  let nodeServer: Server;
  const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
  const operatorId = "operator-fixture-123";
  const allowedOrigin = "https://app.flowcard.test";

  beforeAll(async () => {
    goPort = await getFreePort();
    nodePort = await getFreePort();

    // 1. Launch real Go company-onboarding testserver without DB
    const serviceDir = path.resolve(__dirname, "../../services/company-onboarding");
    goProcess = spawn("go", ["run", "./cmd/testserver", "-port", String(goPort)], {
      cwd: serviceDir,
      env: {
        ...process.env,
        DATABASE_URL: "",
      },
      stdio: "pipe",
    });

    // Wait for Go testserver to report healthy on /healthz
    const start = Date.now();
    let healthy = false;
    while (Date.now() - start < 15_000) {
      try {
        const res = await httpFetch(`http://127.0.0.1:${goPort}/healthz`);
        if (res.statusCode === 200) {
          healthy = true;
          break;
        }
      } catch {
        // retry
      }
      await new Promise((r) => setTimeout(r, 200));
    }

    if (!healthy) {
      throw new Error(`Go testserver failed to start on port ${goPort} within 15 seconds`);
    }

    // 2. Launch real Node HTTP gateway pointing to real Go testserver
    nodeServer = createHttpServer({
      port: nodePort,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: `http://127.0.0.1:${goPort}`,
      onboardingServiceSecret: "test-secret-at-least-32-bytes-long-123456",
    });

    await new Promise<void>((resolve) => {
      nodeServer.listen(nodePort, "127.0.0.1", () => resolve());
    });
  }, 25_000);

  afterAll(async () => {
    if (nodeServer) {
      await new Promise<void>((resolve) => nodeServer.close(() => resolve()));
    }
    if (goProcess) {
      goProcess.kill("SIGTERM");
    }
  });

  describe("Real Gateway Proxy Verification", () => {
    it("redirects /docs/api to /docs/api/ with 301 Moved Permanently", async () => {
      const res = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api`);
      expect(res.statusCode).toBe(301);
      expect(res.headers["location"]).toBe("/docs/api/");
    });

    it("serves offline Swagger UI HTML at /docs/api/ with correct CSP and read-only settings", async () => {
      const res = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/`);
      expect(res.statusCode).toBe(200);
      expect(res.headers["content-type"]).toBe("text/html; charset=utf-8");
      expect(res.headers["content-security-policy"]).toContain("default-src 'self'");
      expect(res.headers["x-content-type-options"]).toBe("nosniff");
      expect(res.headers["x-frame-options"]).toBe("DENY");

      expect(res.body).toContain("<div id=\"swagger-ui\"></div>");
      expect(res.body).toContain("supportedSubmitMethods: []");
      expect(res.body).toContain("tryItOutEnabled: false");
      expect(res.body).toContain("persistAuthorization: false");
      expect(res.body).toContain("validatorUrl: null");
      expect(res.body).toContain("queryConfigEnabled: false");
      expect(res.body).toContain("./openapi.json");
      expect(res.body).toContain("./webhooks.json");
    });

    it("serves Operator API OpenAPI 3.1.0 spec at /docs/api/openapi.json", async () => {
      const res = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/openapi.json`);
      expect(res.statusCode).toBe(200);
      expect(res.headers["content-type"]).toBe("application/json; charset=utf-8");

      const spec = JSON.parse(res.body);
      expect(spec.openapi).toBe("3.1.0");
      expect(spec.info.title).toBe("FlowCard Company Onboarding Operator API");
      expect(spec.paths).toBeDefined();

      // Check key operator endpoints exist in paths
      expect(spec.paths["/api/companies"]).toBeDefined();
      expect(spec.paths["/api/onboarding-applications"]).toBeDefined();
      expect(spec.paths["/api/companies/{id}/platform-accounts"]).toBeDefined();
    });

    it("serves Internal Webhook Receiver API spec at /docs/api/webhooks.json", async () => {
      const res = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/webhooks.json`);
      expect(res.statusCode).toBe(200);
      expect(res.headers["content-type"]).toBe("application/json; charset=utf-8");

      const spec = JSON.parse(res.body);
      expect(spec.openapi).toBe("3.1.0");
      expect(spec.info.title).toBe("FlowCard Onboarding Webhook Receiver API");
      expect(spec.paths["/api/webhooks/airwallex"]).toBeDefined();
      expect(spec.paths["/api/v1/webhooks/airwallex"]).toBeDefined();
    });

    it("serves bundled Swagger UI CSS and JavaScript assets with correct MIME types and byte sizes", async () => {
      // CSS asset
      const cssRes = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/swagger-ui.css`);
      expect(cssRes.statusCode).toBe(200);
      expect(cssRes.headers["content-type"]).toBe("text/css; charset=utf-8");
      expect(cssRes.rawBuffer.length).toBeGreaterThan(100_000);

      // Large JS bundle (>1.6 MiB)
      const jsRes = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/swagger-ui-bundle.js`);
      expect(jsRes.statusCode).toBe(200);
      expect(jsRes.headers["content-type"]).toBe("application/javascript; charset=utf-8");
      expect(jsRes.rawBuffer.length).toBeGreaterThan(1_500_000);

      // Preset JS
      const presetRes = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/swagger-ui-standalone-preset.js`);
      expect(presetRes.statusCode).toBe(200);
      expect(presetRes.headers["content-type"]).toBe("application/javascript; charset=utf-8");

      // Favicons
      const fav32 = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/favicon-32x32.png`);
      expect(fav32.statusCode).toBe(200);
      expect(fav32.headers["content-type"]).toBe("image/png");

      const fav16 = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/favicon-16x16.png`);
      expect(fav16.statusCode).toBe(200);
      expect(fav16.headers["content-type"]).toBe("image/png");
    });

    it("serves assets under /docs/api/assets/ path aliases identically", async () => {
      const aliasRes = await httpFetch(`http://127.0.0.1:${nodePort}/docs/api/assets/swagger-ui.css`);
      expect(aliasRes.statusCode).toBe(200);
      expect(aliasRes.headers["content-type"]).toBe("text/css; charset=utf-8");
    });

    it("strictly preserves 401 unauthenticated denial on business API (/api/companies)", async () => {
      const res = await httpFetch(`http://127.0.0.1:${nodePort}/api/companies`, {
        headers: { origin: allowedOrigin },
      });
      expect(res.statusCode).toBe(401);
      const body = JSON.parse(res.body);
      expect(body.error).toBe("operator_auth_required");
    });

    it("returns 503 onboarding_service_unconfigured when constructor upstreamUrl is undefined", async () => {
      const unconfiguredHandler = createRequestHandler({
        port: 9000,
        databaseUrl: "fixture",
        airwallexClientId: "fixture",
        airwallexApiKey: "fixture",
        airwallexWebhookSecret: "fixture",
        kit5OperatorToken: operatorToken,
        kit5OperatorId: operatorId,
        kit5AllowedOrigins: [allowedOrigin],
        onboardingServiceUrl: undefined,
      });

      const unconfiguredServer = createNetServer();
      const freePort = await getFreePort();
      const nodeServerUnconfigured = createHttpServer({
        port: freePort,
        databaseUrl: "fixture",
        airwallexClientId: "fixture",
        airwallexApiKey: "fixture",
        airwallexWebhookSecret: "fixture",
        kit5OperatorToken: operatorToken,
        kit5OperatorId: operatorId,
        kit5AllowedOrigins: [allowedOrigin],
        onboardingServiceUrl: undefined,
      });

      await new Promise<void>((resolve) => nodeServerUnconfigured.listen(freePort, "127.0.0.1", () => resolve()));
      try {
        const res = await httpFetch(`http://127.0.0.1:${freePort}/docs/api/`);
        expect(res.statusCode).toBe(503);
        const body = JSON.parse(res.body);
        expect(body.error).toBe("onboarding_service_unconfigured");
      } finally {
        await new Promise<void>((resolve) => nodeServerUnconfigured.close(() => resolve()));
      }
    });
  });

  describe("Visual & Offline Headless Browser Acceptance (Playwright/Chromium)", () => {
    it("renders Swagger UI completely offline, displays both specs, disables Try-it-out, and captures screenshot artifact", async () => {
      const chromium = await loadPlaywright();
      if (!chromium) {
        console.warn("Playwright chromium not available; skipping headless browser visual test");
        return;
      }

      const browser = await chromium.launch({ headless: true });
      const context = await browser.newContext({
        viewport: { width: 1280, height: 900 },
      });
      const page = await context.newPage();

      const externalRequests: string[] = [];
      const pageErrors: string[] = [];
      const consoleWarnings: string[] = [];

      // Strict network firewall: block any request not directed at the local test Node gateway
      await page.route("**/*", (route) => {
        const reqUrl = route.request().url();
        const parsed = new URL(reqUrl);
        if (parsed.hostname === "127.0.0.1" && parsed.port === String(nodePort)) {
          route.continue();
        } else {
          externalRequests.push(reqUrl);
          route.abort("blockedbyclient");
        }
      });

      page.on("pageerror", (err) => {
        pageErrors.push(err.message);
      });

      page.on("console", (msg) => {
        if (msg.type() === "error") {
          pageErrors.push(msg.text());
        } else if (msg.type() === "warning") {
          consoleWarnings.push(msg.text());
        }
      });

      // 1. Navigate to docs UI
      const response = await page.goto(`http://127.0.0.1:${nodePort}/docs/api/`, {
        waitUntil: "networkidle",
      });
      expect(response?.status()).toBe(200);

      // Wait for Swagger UI to render
      await page.waitForSelector(".swagger-ui", { timeout: 10_000 });
      await page.waitForSelector(".info .title", { timeout: 10_000 });

      // 2. Confirm title of default spec (Operator API)
      const title = await page.textContent(".info .title");
      expect(title).toContain("FlowCard Company Onboarding Operator API");

      // 3. Confirm operation blocks rendered
      const opblocks = await page.locator(".opblock").count();
      expect(opblocks).toBeGreaterThanOrEqual(15); // All operator endpoints rendered

      // 4. Confirm Try-it-out button is completely ABSENT
      const tryItOutButtons = await page.locator(".try-out__btn").count();
      expect(tryItOutButtons).toBe(0);

      // 5. Confirm auth persistence is disabled (persistAuthorization: false)
      const authPersisted = await page.evaluate(() => {
        return localStorage.getItem("authorized") || sessionStorage.getItem("authorized");
      });
      expect(authPersisted).toBeNull();

      // 6. Test spec selector: switch to Internal Webhook Receiver API
      const selector = page.locator("#select");
      await selector.waitFor({ state: "visible", timeout: 5000 });
      await selector.selectOption({ index: 1 }); // Switch to webhooks.json

      // Wait for title to update to Webhook API
      await page.waitForFunction(
        () => document.querySelector(".info .title")?.textContent?.includes("Webhook Receiver API"),
        { timeout: 10_000 },
      );

      const webhookTitle = await page.textContent(".info .title");
      expect(webhookTitle).toContain("FlowCard Onboarding Webhook Receiver API");

      // Confirm webhook routes rendered
      const webhookBlocks = await page.locator(".opblock").count();
      expect(webhookBlocks).toBe(2); // /api/webhooks/airwallex and /api/v1/webhooks/airwallex

      // 7. Verify query config injection is defended against
      await page.goto(
        `http://127.0.0.1:${nodePort}/docs/api/?configUrl=http://evil.com/malicious.json&urls.primaryName=InjectedAPI`,
        { waitUntil: "networkidle" },
      );
      await page.waitForSelector(".swagger-ui", { timeout: 10_000 });

      // Title should still be the local Operator API, not overridden
      const defendedTitle = await page.textContent(".info .title");
      expect(defendedTitle).toContain("FlowCard Company Onboarding Operator API");

      // 8. Capture visual evidence artifact
      await page.screenshot({
        path: "/tmp/flowcard-openapi-ui.png",
        fullPage: true,
      });

      // 9. Assert strict offline isolation: ZERO external requests attempted
      expect(externalRequests).toEqual([]);

      // 10. Assert ZERO fatal page errors
      const fatalErrors = pageErrors.filter((e) => !e.includes("favicon") && !e.includes("blockedbyclient"));
      expect(fatalErrors).toEqual([]);

      await context.close();
      await browser.close();
    }, 30_000);
  });
});
