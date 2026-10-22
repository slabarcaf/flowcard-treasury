import { createHmac, timingSafeEqual } from "node:crypto";

/** Airwallex signs x-timestamp followed directly by the original body using HMAC-SHA256 hex. */
export function verifyAirwallexSignature(input: {
  rawBody: Uint8Array;
  timestamp: string | undefined;
  signature: string | undefined;
  secret: string;
}): boolean {
  if (!input.timestamp || !/^\d+$/.test(input.timestamp) || !input.signature || !/^[a-f\d]{64}$/i.test(input.signature)) return false;
  const expected = createHmac("sha256", input.secret)
    .update(input.timestamp, "utf8")
    .update(input.rawBody)
    .digest();
  const supplied = Buffer.from(input.signature, "hex");
  return supplied.length === expected.length && timingSafeEqual(supplied, expected);
}
