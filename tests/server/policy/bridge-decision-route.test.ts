import { Readable } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import type { IncomingMessage, ServerResponse } from "node:http";
import { handleBridgeDecision } from "../../../server/src/http/routes/bridge-decision.js";

function response() {
  return {
    status: 0,
    headers: {} as Record<string, string>,
    body: "",
    headersSent: false,
    writeHead(status: number, headers: Record<string, string>) {
      this.status = status;
      this.headers = headers;
      this.headersSent = true;
    },
    end(body: string) { this.body = body; },
  };
}

async function call(body: unknown, resolver?: (requestId: string) => Promise<never>) {
  const request = Readable.from([JSON.stringify(body)]) as IncomingMessage;
  const result = response();
  await handleBridgeDecision(request, result as unknown as ServerResponse, resolver);
  return { ...result, json: JSON.parse(result.body) as Record<string, unknown> };
}

describe("bridge decision HTTP route", () => {
  it.each([
    { requestId: "req-1", approved: true },
    { requestId: "req-1", tier: "A" },
    { requestId: "req-1", creditLineCents: 999_999 },
    { requestId: "req-1", reasonCodes: ["AUTO_APPROVED"] },
    { requestId: "req-1", amountCents: 100 },
  ])("rejects client policy fields: %o", async (body) => {
    const resolver = vi.fn();
    const result = await call(body, resolver);
    expect(result.status).toBe(400);
    expect(resolver).not.toHaveBeenCalled();
  });

  it("fails closed if no trusted request resolver is configured", async () => {
    const result = await call({ requestId: "req-1" });
    expect(result.status).toBe(503);
    expect(result.json).toEqual({ error: "trusted_bridge_request_source_unavailable" });
  });

  it("returns only the allowlisted evaluation result and freshness/version metadata", async () => {
    const resolver = vi.fn(async () => ({
      evaluationId: "eval-1", requestId: "req-1", customerId: "cust-1", outcome: "human_review" as const,
      amountCents: 12_300, creditLineCents: 50_000, reasonCodes: ["HUMAN_REVIEW_REQUIRED"],
      freshness: { profileAgeSeconds: 60 }, versions: { profileId: "profile-1" },
      providerSecret: "secret", rawNotes: "private source note",
    }));
    const result = await call({ requestId: " req-1 " }, resolver);
    expect(result.status).toBe(200);
    expect(resolver).toHaveBeenCalledWith("req-1");
    expect(result.body).not.toContain("secret");
    expect(result.body).not.toContain("private source note");
    expect(result.json).toMatchObject({ evaluationId: "eval-1", outcome: "human_review", amountCents: 12_300,
      creditLineCents: 50_000, reasonCodes: ["HUMAN_REVIEW_REQUIRED"], freshness: { profileAgeSeconds: 60 } });
    expect(result.headers["cache-control"]).toBe("no-store");
  });
});
