import atlas from "./fixtures/atlas.json" with { type: "json" };
import bloom from "./fixtures/bloom.json" with { type: "json" };
import nova from "./fixtures/nova.json" with { type: "json" };
import pixel from "./fixtures/pixel.json" with { type: "json" };
import type { SalesforceCustomerSnapshot } from "./types.js";

const fixtures: Readonly<Record<string, SalesforceCustomerSnapshot>> = {
  acct_bloom001: bloom as SalesforceCustomerSnapshot,
  acct_atlas001: atlas as SalesforceCustomerSnapshot,
  acct_nova001: nova as SalesforceCustomerSnapshot,
  acct_pixel001: pixel as SalesforceCustomerSnapshot,
};

/** Return a complete synthetic snapshot without constructing a provider client. */
export function getDemoSalesforceSnapshot(customerId: string): SalesforceCustomerSnapshot | null {
  return fixtures[customerId] ?? null;
}
