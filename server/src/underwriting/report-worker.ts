import type { AirwallexUnderwritingGateway } from "../airwallex/client.js";
import { loadAirwallexProfileSources } from "./airwallex-source.js";
import type { ProfileSourceData } from "./airwallex-source.js";

/** Fetch the whole-book report once, then enrich only explicitly enrolled accounts. */
export async function collectUnderwritingSources(
  gateway: AirwallexUnderwritingGateway,
  roster: readonly { customerId: string; airwallexConnectedAccountId: string; isActive: boolean }[],
  windowStart: Date,
  windowEnd: Date,
): Promise<ProfileSourceData> {
  const active = roster.filter((customer) => customer.isActive);
  const uniqueIds = new Set<string>();
  for (const customer of active) {
    if (!customer.customerId.trim() || !customer.airwallexConnectedAccountId.trim()) throw new Error("Active roster contains an incomplete Airwallex mapping");
    if (uniqueIds.has(customer.customerId)) throw new Error("Active roster contains duplicate customer IDs");
    uniqueIds.add(customer.customerId);
  }
  return loadAirwallexProfileSources(gateway, active.map((customer) => ({ customerId: customer.customerId, accountId: customer.airwallexConnectedAccountId })), windowStart, windowEnd);
}
