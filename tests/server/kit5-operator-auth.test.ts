import { Readable } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import { describe, expect, it } from "vitest";
import { handleKit5Session } from "../../server/src/http/kit5-operator-auth.js";

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

describe("retained onboarding operator session", () => {
  it("issues a short-lived HttpOnly Secure SameSite cookie after runtime credential validation", async () => {
    const recorder = responseRecorder();
    handleKit5Session(sessionRequest({ token }), recorder.response, auth);
    await new Promise((resolve) => setImmediate(resolve));

    expect(recorder.status()).toBe(200);
    expect(recorder.headers["Set-Cookie"]).toMatch(/HttpOnly/);
    expect(recorder.headers["Set-Cookie"]).toMatch(/Secure/);
    expect(recorder.headers["Set-Cookie"]).toMatch(/SameSite=None/);
    expect(recorder.headers["Set-Cookie"]).not.toContain(token);
    expect(JSON.parse(recorder.body())).toMatchObject({ approvalAvailable: false });
  });

  it("fails closed when server-only authentication or origin configuration is absent", async () => {
    const recorder = responseRecorder();
    handleKit5Session(sessionRequest({ token }), recorder.response, {});
    await new Promise((resolve) => setImmediate(resolve));
    expect(recorder.status()).toBe(503);
  });

});
