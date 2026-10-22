import { Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it, vi } from "vitest";
import { createRequestHandler } from "../../server/src/http/server.js";
import { createOperatorSessionCookie, handleKit5Session } from "../../server/src/http/kit5-operator-auth.js";

const token = "test-operator-token-0123456789abcdef-0123456789abcdef";
const origin = "https://demo.example";
const auth = { operatorToken: token, operatorId: "operator-fixture", allowedOrigins: [origin] };

function responseRecorder() {
  let statusCode = 0;
  let body = "";
  const headers: Record<string, string> = {};
  const response = {
    headersSent: false,
    setHeader(name: string, value: string) { headers[name] = value; return this; },
    writeHead(code: number, values?: Record<string, string>) {
      statusCode = code;
      Object.assign(headers, values);
      this.headersSent = true;
      return this;
    },
    end(value = "") { body = value; return this; },
  } as unknown as ServerResponse;
  return { response, headers, status: () => statusCode, body: () => body };
}

function sessionRequest(value: unknown, requestOrigin = origin): IncomingMessage {
  return Object.assign(Readable.from([JSON.stringify(value)]), {
    method: "POST",
    url: "/api/kit5/session",
    headers: { origin: requestOrigin },
  }) as IncomingMessage;
}

describe("Kit 5 operator authorization", () => {
  it("issues a short-lived HttpOnly Secure SameSite cookie after runtime credential validation", async () => {
    const recorder = responseRecorder();
    handleKit5Session(sessionRequest({ token }), recorder.response, auth);
    await new Promise((resolve) => setImmediate(resolve));

    expect(recorder.status()).toBe(200);
    expect(recorder.headers["Set-Cookie"]).toMatch(/HttpOnly/);
    expect(recorder.headers["Set-Cookie"]).toMatch(/Secure/);
    expect(recorder.headers["Set-Cookie"]).toMatch(/SameSite=None/);
    expect(recorder.headers["Set-Cookie"]).not.toContain(token);
    expect(JSON.parse(recorder.body())).toMatchObject({ approvalAvailable: true });
  });

  it("denies unauthenticated transaction requests and disallowed origins before provider use", async () => {
    const create = vi.fn();
    const handler = createRequestHandler({
      port: 8000,
      databaseUrl: "fixture",
      airwallexClientId: "fixture",
      airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture",
      kit5OperatorToken: token,
      kit5AllowedOrigins: [origin],
    }, undefined, undefined, {
      pool: { query: vi.fn(), connect: vi.fn(), end: vi.fn() },
      gateway: { createIssuingSimulation: create, getIssuingLifecycle: vi.fn() },
      resolveScenario: () => null,
      operatorAuth: auth,
    });

    for (const headers of [{ origin }, { origin: "https://attacker.example", cookie: "kit5_operator_session=invalid" }]) {
      const request = Object.assign(Readable.from(["{}"]), {
        method: "POST", url: "/api/kit5/next-customer", headers,
      }) as IncomingMessage;
      const recorder = responseRecorder();
      handler(request, recorder.response);
      await new Promise((resolve) => setImmediate(resolve));
      expect(recorder.status()).toBe(headers.origin === origin ? 401 : 403);
    }
    expect(create).not.toHaveBeenCalled();
  });

  it("fails closed when server-only authentication or origin configuration is absent", async () => {
    const recorder = responseRecorder();
    handleKit5Session(sessionRequest({ token }), recorder.response, {});
    await new Promise((resolve) => setImmediate(resolve));
    expect(recorder.status()).toBe(503);
  });

  it("authenticates approvals with the server-bound actor and rejects extra browser terms", async () => {
    const operatorId = "operator-fixture";
    const approval = vi.fn(async () => ({ status: "approved" as const, bridgeId: "fixture-bridge" }));
    const handler = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: token, kit5OperatorId: operatorId, kit5AllowedOrigins: [origin],
    }, undefined, undefined, {
      pool: { query: vi.fn(), connect: vi.fn(), end: vi.fn() },
      gateway: { createIssuingSimulation: vi.fn(), getIssuingLifecycle: vi.fn() },
      resolveScenario: () => null,
      operatorAuth: { operatorToken: token, operatorId, allowedOrigins: [origin] },
      bridgeFunding: { approveHumanReview: approval } as never,
    });
    const send = async (body: unknown, actor: string | undefined) => {
      const request = Object.assign(Readable.from([JSON.stringify(body)]), {
        method: "POST", url: "/api/kit5/approve", headers: {
          origin,
          ...(actor ? { cookie: `kit5_operator_session=${createOperatorSessionCookie(token, Date.now(), actor)}` } : {}),
        },
      }) as IncomingMessage;
      const recorder = responseRecorder();
      handler(request, recorder.response);
      await new Promise((resolve) => setImmediate(resolve));
      return recorder.status();
    };

    expect(await send({ scenarioId: "s", customerId: "c", actionId: "a", reason: "reviewed" }, undefined)).toBe(401);
    expect(await send({ scenarioId: "s", customerId: "c", actionId: "a", reason: "reviewed", amountCents: 1 }, operatorId)).toBe(400);
    expect(await send({ scenarioId: "s", customerId: "c", actionId: "a", reason: "reviewed" }, "other-operator")).toBe(503);
    expect(await send({ scenarioId: "s", customerId: "c", actionId: "a", reason: "reviewed" }, operatorId)).toBe(200);
    expect(approval).toHaveBeenCalledOnce();
    expect(approval).toHaveBeenCalledWith({ scenarioId: "s", customerId: "c", actionId: "a", actorId: operatorId, reason: "reviewed" });
  });

  it("requires an operator session and rejects browser-supplied deposit terms", async () => {
    const query = vi.fn();
    const createDeposit = vi.fn();
    const handler = createRequestHandler({
      port: 8000, databaseUrl: "fixture", airwallexClientId: "fixture", airwallexApiKey: "fixture",
      airwallexWebhookSecret: "fixture", kit5OperatorToken: token, kit5OperatorId: "operator-fixture",
      kit5AllowedOrigins: [origin],
    }, undefined, undefined, {
      pool: { query, connect: vi.fn(), end: vi.fn() },
      gateway: { createIssuingSimulation: vi.fn(), getIssuingLifecycle: vi.fn() },
      resolveScenario: () => null,
      operatorAuth: auth,
      repaymentDepositGateway: { createGlobalAccountDepositSimulation: createDeposit } as never,
      resolveRepaymentDepositTarget: () => null,
    });
    const send = async (body: unknown, authenticated: boolean) => {
      const request = Object.assign(Readable.from([JSON.stringify(body)]), {
        method: "POST", url: "/api/kit5/repayment-deposit", headers: {
          origin,
          ...(authenticated ? { cookie: `kit5_operator_session=${createOperatorSessionCookie(token, Date.now(), "operator-fixture")}` } : {}),
        },
      }) as IncomingMessage;
      const recorder = responseRecorder();
      handler(request, recorder.response);
      await new Promise((resolve) => setImmediate(resolve));
      return recorder.status();
    };

    expect(await send({ scenarioId: "s", customerId: "c", actionId: "a" }, false)).toBe(401);
    expect(await send({ scenarioId: "s", customerId: "c", actionId: "a", amountCents: 800_000 }, true)).toBe(400);
    expect(await send({ scenarioId: "s", customerId: "c", actionId: "a", accountId: "provider-id" }, true)).toBe(400);
    expect(query).not.toHaveBeenCalled();
    expect(createDeposit).not.toHaveBeenCalled();
  });
});
