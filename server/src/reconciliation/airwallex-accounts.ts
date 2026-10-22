import type { AirwallexGateway, ProviderConnectedAccount } from "../airwallex/client.js";

/** Lists every connected-account page through the existing server gateway. */
export function listAirwallexConnectedAccounts(
  gateway: Pick<AirwallexGateway, "listConnectedAccounts">,
): Promise<ProviderConnectedAccount[]> {
  return gateway.listConnectedAccounts();
}
