import { createHmac } from "node:crypto";
import { Readable } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import type { IncomingMessage, ServerResponse } from "node:http";
import {
  authenticateAirwallexNonce,
  fingerprintRemoteAuthorizationRequest,
  handleRemoteAuthorization,
  type AirwallexRemoteAuthorizationV2,
} from "../../../server/src/http/routes/remote-authorization.js";

const secret = "fixture-shared-secret";
const eventId = "019dd72a-5bde-7000-8975-760d03a26bad";
const payload = {
  version: 2,
  account_id: "acct_demo",
  card_id: "card_demo",
  card_transaction_event_id: eventId,
  card_transaction_id: "019dd72a-5bde-7000-8975-760d03a26bac",
  card_transaction_lifecycle_id: "019dd72a-5bde-7000-8975-760d03a26bab",
  transaction_type: "AUTHORIZATION",
  transaction_category: "PURCHASE",
  transaction_amount: 12.34,
  transaction_currency: "USD",
  merchant: { category_code: "5411", name: "not persisted" },
};

function response() {
  return {
    status: 0,
    headers: {} as Record<string, string>,
    body: "",
    headersSent: false,
    writeHead(status: number, headers: Record<string, string>) { this.status = status; this.headers = headers; this.headersSent = true; },
    end(body: string) { this.body = body; },
  };
}

function signedRequest(now: Date, value: unknown = payload, nonceSuffix = "fixture-nonce") {
  const nonce = `${now.getTime()}.${nonceSuffix}`;
  const signature = createHmac("sha256", secret).update(nonce).digest("base64");
  const request = Readable.from([JSON.stringify(value)]) as IncomingMessage;
  request.headers = { "x-nonce": nonce, "x-signature": signature };
  return request;
}

function nonceClaimer() {
  const claims = new Map<string, { eventId: string; fingerprint: string }>();
  return vi.fn(async (request: AirwallexRemoteAuthorizationV2, nonce: string) => {
    const fingerprint = fingerprintRemoteAuthorizationRequest(request);
    const prior = claims.get(nonce);
    if (!prior) {
      claims.set(nonce, { eventId: request.cardTransactionEventId, fingerprint });
      return "claimed" as const;
    }
    return prior.eventId === request.cardTransactionEventId && prior.fingerprint === fingerprint
      ? "exact_retry" as const
      : "replay_mismatch" as const;
  });
}

async function invoke(request: IncomingMessage, options: Parameters<typeof handleRemoteAuthorization>[2]) {
  const result = response();
  await handleRemoteAuthorization(request, result as unknown as ServerResponse, options);
  return { ...result, json: JSON.parse(result.body) as Record<string, unknown> };
}

describe("Airwallex remote authorization v2 route", () => {
  const now = new Date("2026-10-22T12:00:00.000Z");

  it("validates HMAC-SHA256 and a fresh timestamped nonce", () => {
    const nonce = `${now.getTime()}.fixture-nonce`;
    const signature = createHmac("sha256", secret).update(nonce).digest("base64");
    expect(authenticateAirwallexNonce(nonce, signature, secret, now)).toBe(true);
    expect(authenticateAirwallexNonce(nonce, signature, secret, new Date(now.getTime() + 30_001))).toBe(false);
    expect(authenticateAirwallexNonce(nonce, "bad", secret, now)).toBe(false);
  });

  it("declines a stale signed nonce before policy evaluation", async () => {
    const staleTime = new Date(now.getTime() - 30_001);
    const resolve = vi.fn();
    const claimNonce = nonceClaimer();
    const result = await invoke(signedRequest(staleTime), { sharedSecret: secret, resolve, claimNonce, now });
    expect(result.status).toBe(200);
    expect(result.json).toEqual({
      card_transaction_event_id: eventId,
      response_status: "DECLINED",
      status_reason: "invalid_request_signature",
    });
    expect(resolve).not.toHaveBeenCalled();
    expect(claimNonce).not.toHaveBeenCalled();
  });

  it("fails closed if the shared secret is missing", async () => {
    const result = await invoke(signedRequest(now), { now });
    expect(result.status).toBe(200);
    expect(result.json).toEqual({ card_transaction_event_id: eventId, response_status: "DECLINED", status_reason: "remote_authorization_not_configured" });
  });

  it("echoes the v2 event ID and declines when no policy resolver is wired", async () => {
    const result = await invoke(signedRequest(now), { sharedSecret: secret, claimNonce: nonceClaimer(), now });
    expect(result.status).toBe(200);
    expect(result.json).toEqual({ card_transaction_event_id: eventId, response_status: "DECLINED", status_reason: "policy_unavailable" });
    expect(result.headers["cache-control"]).toBe("no-store");
  });

  it("passes only allowlisted payload fields and echoes the provider contract fields", async () => {
    const resolve = vi.fn(async () => ({ responseStatus: "AUTHORIZED" as const, statusReason: "policy_approved" }));
    const result = await invoke(signedRequest(now), { sharedSecret: secret, resolve, claimNonce: nonceClaimer(), now });
    expect(resolve).toHaveBeenCalledWith(expect.objectContaining({
      version: 2, accountId: "acct_demo", cardId: "card_demo", cardTransactionEventId: eventId,
      cardTransactionId: payload.card_transaction_id, transactionAmount: 12.34, merchantCategoryCode: "5411",
    }), `${now.getTime()}.fixture-nonce`, expect.any(AbortSignal));
    expect(result.json).toEqual({ card_transaction_event_id: eventId, response_status: "AUTHORIZED", status_reason: "policy_approved" });
    expect(result.body).not.toContain("not persisted");
  });

  it("rejects malformed contracts and invalid signatures before policy evaluation", async () => {
    const resolve = vi.fn();
    const claimNonce = nonceClaimer();
    const malformed = await invoke(signedRequest(now, { ...payload, version: 1 }), { sharedSecret: secret, resolve, claimNonce, now });
    expect(malformed.status).toBe(400);
    const invalidSignatureRequest = signedRequest(now);
    invalidSignatureRequest.headers["x-signature"] = "bad";
    const invalid = await invoke(invalidSignatureRequest, { sharedSecret: secret, resolve, claimNonce, now });
    expect(invalid.status).toBe(200);
    expect(invalid.json).toMatchObject({ card_transaction_event_id: eventId, response_status: "DECLINED" });
    expect(resolve).not.toHaveBeenCalled();
  });

  it("declines non-purchase, non-USD, and non-allowlisted spend before policy evaluation", async () => {
    const resolve = vi.fn();
    const claimNonce = nonceClaimer();
    let index = 0;
    for (const changes of [
      { transaction_type: "CLEARING" },
      { transaction_category: "CASH_DISBURSEMENT" },
      { transaction_currency: "EUR" },
      { merchant: { category_code: "7995" } },
    ]) {
      const result = await invoke(signedRequest(now, { ...payload, ...changes }, `unsupported-${index++}`), {
        sharedSecret: secret, resolve, claimNonce, now,
      });
      expect(result.status).toBe(200);
      expect(result.json).toMatchObject({ card_transaction_event_id: eventId, response_status: "DECLINED", status_reason: "unsupported_transaction" });
    }
    expect(resolve).not.toHaveBeenCalled();
  });

  it("consumes an authentic unsupported nonce before eligibility and rejects altered purchase replay", async () => {
    const nonceSuffix = "unsupported-replay";
    const claimNonce = nonceClaimer();
    const resolve = vi.fn(async () => ({ responseStatus: "AUTHORIZED" as const, statusReason: "policy_approved" }));
    const unsupported = await invoke(signedRequest(now, { ...payload, merchant: { category_code: "7995" } }, nonceSuffix), {
      sharedSecret: secret, resolve, claimNonce, now,
    });
    expect(unsupported.json).toMatchObject({ response_status: "DECLINED", status_reason: "unsupported_transaction" });

    const alteredPurchase = await invoke(signedRequest(now, {
      ...payload,
      card_transaction_event_id: "019dd72a-5bde-7000-8975-760d03a26bee",
      transaction_amount: 999.99,
      merchant: { category_code: "5411" },
    }, nonceSuffix), { sharedSecret: secret, resolve, claimNonce, now });

    expect(alteredPurchase.json).toMatchObject({ response_status: "DECLINED", status_reason: "request_replay_mismatch" });
    expect(resolve).not.toHaveBeenCalled();
    expect(claimNonce).toHaveBeenCalledTimes(2);
  });

  it("aborts an over-budget resolver and returns a provider-compatible decline", async () => {
    let aborted = false;
    const resolve = vi.fn((_request, _nonce, signal: AbortSignal) => new Promise<never>((_resolve, reject) => {
      signal.addEventListener("abort", () => { aborted = true; reject(signal.reason); }, { once: true });
    }));
    const result = await invoke(signedRequest(now), {
      sharedSecret: secret, resolve, claimNonce: nonceClaimer(), now, resolutionBudgetMs: 5,
    });
    expect(aborted).toBe(true);
    expect(result.status).toBe(200);
    expect(result.json).toMatchObject({ card_transaction_event_id: eventId, response_status: "DECLINED", status_reason: "policy_timeout_or_unavailable" });
  });
});
