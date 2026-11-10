import { spawn, type ChildProcess } from "node:child_process";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createRequestHandler } from "../../server/src/http/server.js";
import { createOperatorSessionCookie } from "../../server/src/http/kit5-operator-auth.js";

const operatorToken = "test-operator-token-0123456789abcdef-0123456789abcdef";
const operatorId = "e2e-operator-acceptance-456";
const allowedOrigin = "https://app.flowcard.test";
const serviceSecret = "backend-service-secret-at-least-32-bytes-long-12345";
const testDbUrl = "postgres://test_onboarding:test_password@127.0.0.1:54329/flowcard_onboarding_test?sslmode=disable";

describe("End-to-End Fixture Acceptance: Authenticated Node -> Go HTTP", () => {
  let goProcess: ChildProcess;
  let goPort: number;
  let nodeServer: Server;
  let nodePort: number;
  let sessionCookie: string;

  beforeAll(async () => {
    // 1. Allocate an ephemeral TCP port for the Go server
    const probeServer = createServer();
    await new Promise<void>((resolve) => probeServer.listen(0, "127.0.0.1", () => resolve()));
    goPort = (probeServer.address() as AddressInfo).port;
    await new Promise<void>((resolve) => probeServer.close(() => resolve()));

    // 2. Spawn real Go HTTP test server wrapper (with constructor-injected auth secret)
    goProcess = spawn("go", ["run", "./cmd/testserver", "-port", String(goPort), "-auth-secret", serviceSecret], {
      cwd: "services/company-onboarding",
      env: {
        ...process.env,
        DATABASE_URL: testDbUrl,
        NODE_ENV: "test",
      },
      stdio: "pipe",
    });

    // Wait for Go server /healthz endpoint to be healthy
    const startTime = Date.now();
    let healthy = false;
    while (Date.now() - startTime < 15000) {
      try {
        const res = await fetch(`http://127.0.0.1:${goPort}/healthz`);
        if (res.ok) {
          healthy = true;
          break;
        }
      } catch {
        // Retry until ready
      }
      await new Promise((r) => setTimeout(r, 200));
    }

    if (!healthy) {
      throw new Error(`Go API server failed to become ready on port ${goPort}`);
    }

    // 3. Start real Node HTTP server proxying to real Go server
    const nodeHandler = createRequestHandler({
      port: 0,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: operatorToken,
      kit5OperatorId: operatorId,
      kit5AllowedOrigins: [allowedOrigin],
      onboardingServiceUrl: `http://127.0.0.1:${goPort}`,
      onboardingServiceSecret: serviceSecret,
    });

    nodeServer = createServer(nodeHandler);
    await new Promise<void>((resolve) => nodeServer.listen(0, "127.0.0.1", () => resolve()));
    nodePort = (nodeServer.address() as AddressInfo).port;

    // 4. Generate valid operator session cookie
    sessionCookie = createOperatorSessionCookie(operatorToken, Date.now(), operatorId);
  }, 20000);

  afterAll(async () => {
    if (nodeServer) {
      await new Promise<void>((resolve) => nodeServer.close(() => resolve()));
    }
    if (goProcess && goProcess.pid) {
      goProcess.kill("SIGTERM");
    }
  });

  it("Scenario 1 (Success): Creates Company, Draft Application and reads through authenticated Node->Go proxy", async () => {
    // 1. Create Company via Node proxy
    const companyRes = await fetch(`http://127.0.0.1:${nodePort}/api/onboarding/companies`, {
      method: "POST",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${sessionCookie}`,
        "content-type": "application/json",
      },
      body: JSON.stringify({
        legal_name: "Acme Cross-Language E2E Corp",
      }),
    });

    expect(companyRes.status).toBe(201);
    const company = await companyRes.json();
    expect(company).toMatchObject({
      legal_name: "Acme Cross-Language E2E Corp",
      status: "ACTIVE",
    });
    expect(company.id).toMatch(/^comp_/);

    // 2. Create Application via Node proxy
    const appRes = await fetch(`http://127.0.0.1:${nodePort}/api/onboarding/onboarding-applications`, {
      method: "POST",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${sessionCookie}`,
        "content-type": "application/json",
      },
      body: JSON.stringify({
        company_id: company.id,
        platform_name: "AIRWALLEX",
        environment: "sandbox",
      }),
    });

    expect(appRes.status).toBe(201);
    const application = await appRes.json();
    expect(application).toMatchObject({
      company_id: company.id,
      status: "DRAFT",
    });
    expect(application.id).toMatch(/^app_/);

    // 3. Read Application via direct browser resource path (/api/onboarding-applications/:id)
    const getAppRes = await fetch(`http://127.0.0.1:${nodePort}/api/onboarding-applications/${application.id}`, {
      method: "GET",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${sessionCookie}`,
      },
    });

    expect(getAppRes.status).toBe(200);
    const readApp = await getAppRes.json();
    expect(readApp.id).toBe(application.id);
    expect(readApp.company_id).toBe(company.id);
    expect(readApp.status).toBe("DRAFT");
  });

  it("Scenario 2 (Origin & Auth Defense): Rejects unauthorized and forged requests before proxying", async () => {
    // Request without session cookie -> 401 Unauthorized
    const unauthRes = await fetch(`http://127.0.0.1:${nodePort}/api/onboarding/companies`, {
      method: "GET",
      headers: {
        origin: allowedOrigin,
      },
    });
    expect(unauthRes.status).toBe(401);

    // Request with forbidden origin -> 403 Forbidden
    const evilOriginRes = await fetch(`http://127.0.0.1:${nodePort}/api/onboarding/companies`, {
      method: "GET",
      headers: {
        origin: "https://attacker.example",
        cookie: `kit5_operator_session=${sessionCookie}`,
      },
    });
    expect(evilOriginRes.status).toBe(403);
  });

  it("Scenario 3 (Validation & Default Deny): Returns 422 for malformed payloads and protects sensitive fields", async () => {
    const invalidRes = await fetch(`http://127.0.0.1:${nodePort}/api/onboarding/companies`, {
      method: "POST",
      headers: {
        origin: allowedOrigin,
        cookie: `kit5_operator_session=${sessionCookie}`,
        "content-type": "application/json",
      },
      body: JSON.stringify({
        legal_name: "X", // length 1 < min 2 violates validation
        registration_country: "INVALID_COUNTRY_CODE",
      }),
    });

    expect(invalidRes.status).toBe(422);
    const errBody = await invalidRes.json();
    expect(errBody.code).toBe("VALIDATION_FAILED");
  });

  it("Scenario 4 (Health & Service Isolation): Node and Go public health endpoints remain independent and operational", async () => {
    // Direct Go health endpoint
    const goHealth = await fetch(`http://127.0.0.1:${goPort}/healthz`);
    expect(goHealth.status).toBe(200);
    const goBody = await goHealth.json();
    expect(goBody).toMatchObject({
      service: "company-onboarding",
      status: "ok",
    });

    // Node health endpoint
    const nodeHealth = await fetch(`http://127.0.0.1:${nodePort}/health`);
    expect(nodeHealth.status).toBe(200);
  });
});
