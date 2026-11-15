import { createHmac } from "node:crypto";
import { describe, expect, it } from "vitest";
import { verifyAirwallexSignature } from "../../server/src/webhooks/verify.js";

const secret = "notification-url-secret";
const body = Buffer.from('{ "id":"evt-1", "name":"connected_account_transfer.settled", "data":{"object":{"id":"tr_1","request_id":"bridge-123"}} }');
const timestamp = "1792600000000";
const signature = createHmac("sha256", secret).update(timestamp).update(body).digest("hex");

describe("Airwallex webhook signatures", () => {
  it("verifies the exact raw body and rejects changed bytes or missing headers", () => {
    expect(verifyAirwallexSignature({ rawBody: body, timestamp, signature, secret })).toBe(true);
    expect(verifyAirwallexSignature({ rawBody: Buffer.from(body.toString().replace(" ", "")), timestamp, signature, secret })).toBe(false);
    expect(verifyAirwallexSignature({ rawBody: body, timestamp: undefined, signature, secret })).toBe(false);
  });
});
