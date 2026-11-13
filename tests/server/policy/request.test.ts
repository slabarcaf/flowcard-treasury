import { describe, expect, it } from "vitest";
import { validateBridgeRequest, type BridgeRequest } from "../../../server/src/policy/request.js";

const validRequest = (overrides: Partial<BridgeRequest> = {}): BridgeRequest => ({
  requestId: "request-1",
  customerId: "customer-1",
  transactionId: "txn-1",
  transactionAmountCents: 12_000,
  currency: "USD",
  spendCategory: "supplier",
  traceable: true,
  ...overrides,
});

describe("bridge request validation", () => {
  it("accepts a traceable, supported USD card spend with a positive safe amount", () => {
    expect(validateBridgeRequest(validRequest())).toEqual([]);
  });

  it.each([
    ["zero amount", { transactionAmountCents: 0 }, "INVALID_AMOUNT"],
    ["negative amount", { transactionAmountCents: -1 }, "INVALID_AMOUNT"],
    ["fractional cents", { transactionAmountCents: 1.5 }, "INVALID_AMOUNT"],
    ["unsafe amount", { transactionAmountCents: Number.MAX_SAFE_INTEGER + 1 }, "INVALID_AMOUNT"],
    ["non-USD currency", { currency: "EUR" }, "UNSUPPORTED_CURRENCY"],
    ["cash spend", { spendCategory: "cash" }, "UNSUPPORTED_SPEND_CATEGORY"],
    ["quasi-cash spend", { spendCategory: "quasi_cash" }, "UNSUPPORTED_SPEND_CATEGORY"],
    ["untraceable spend", { traceable: false }, "UNTRACEABLE_SPEND"],
    ["missing transaction provenance", { transactionId: "   " }, "TRANSACTION_ID_MISSING"],
  ] as const)("rejects %s", (_name, overrides, code) => {
    expect(validateBridgeRequest(validRequest(overrides))).toContain(code);
  });
});
