import type { AirwallexGateway, ProviderConnectedAccount } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { listCustomers } from "../roster/repository.js";
import type { CustomerRosterEntry } from "../roster/types.js";
import { listAirwallexConnectedAccounts } from "./airwallex-accounts.js";

export interface CustomerRosterReconciliationReport {
  source: "airwallex_connected_account_list";
  completedAt: string;
  missing: Array<{
    customerId: string;
    airwallexConnectedAccountId: string;
    rosterStatus: "active" | "deactivated";
  }>;
  extra: Array<{
    airwallexConnectedAccountId: string;
    providerStatus: string;
  }>;
  duplicates: Array<{
    airwallexConnectedAccountId: string;
    rosterCustomerIds: string[];
    providerOccurrences: number;
  }>;
  statusMismatches: Array<{
    customerId: string;
    airwallexConnectedAccountId: string;
    rosterStatus: "active" | "deactivated";
    providerStatus: string;
  }>;
}

/** Compares stable account IDs and enrollment state without changing the roster. */
export function buildCustomerRosterReconciliation(
  roster: readonly CustomerRosterEntry[],
  providerAccounts: readonly ProviderConnectedAccount[],
  completedAt = new Date(),
): CustomerRosterReconciliationReport {
  const rosterByAccountId = groupBy(roster, (entry) => entry.airwallexConnectedAccountId);
  const providerByAccountId = groupBy(providerAccounts, (account) => account.id);
  const allIds = new Set([...rosterByAccountId.keys(), ...providerByAccountId.keys()]);
  const missing: CustomerRosterReconciliationReport["missing"] = [];
  const extra: CustomerRosterReconciliationReport["extra"] = [];
  const duplicates: CustomerRosterReconciliationReport["duplicates"] = [];
  const statusMismatches: CustomerRosterReconciliationReport["statusMismatches"] = [];

  for (const accountId of [...allIds].sort()) {
    const rosterEntries = rosterByAccountId.get(accountId) ?? [];
    const providerEntries = providerByAccountId.get(accountId) ?? [];

    if (rosterEntries.length > 1 || providerEntries.length > 1) {
      duplicates.push({
        airwallexConnectedAccountId: accountId,
        rosterCustomerIds: rosterEntries.map((entry) => entry.customerId).sort(),
        providerOccurrences: providerEntries.length,
      });
    }

    if (rosterEntries.length > 0 && providerEntries.length === 0) {
      for (const entry of rosterEntries) {
        missing.push({
          customerId: entry.customerId,
          airwallexConnectedAccountId: accountId,
          rosterStatus: entry.isActive ? "active" : "deactivated",
        });
      }
    } else if (rosterEntries.length === 0) {
      for (const account of providerEntries) {
        extra.push({ airwallexConnectedAccountId: accountId, providerStatus: account.status });
      }
    } else if (rosterEntries.length === 1 && providerEntries.length === 1) {
      const entry = rosterEntries[0];
      const account = providerEntries[0];
      const rosterIsActive = entry.isActive;
      const providerIsActive = account.status.toUpperCase() === "ACTIVE";
      if (rosterIsActive !== providerIsActive) {
        statusMismatches.push({
          customerId: entry.customerId,
          airwallexConnectedAccountId: accountId,
          rosterStatus: rosterIsActive ? "active" : "deactivated",
          providerStatus: account.status,
        });
      }
    }
  }

  return {
    source: "airwallex_connected_account_list",
    completedAt: completedAt.toISOString(),
    missing,
    extra,
    duplicates,
    statusMismatches,
  };
}

/** Reads the complete roster and provider list; this operation has no writes. */
export async function reconcileCustomerRoster(
  pool: DatabasePool,
  gateway: Pick<AirwallexGateway, "listConnectedAccounts">,
  completedAt = new Date(),
): Promise<CustomerRosterReconciliationReport> {
  const [roster, accounts] = await Promise.all([
    listCustomers(pool),
    listAirwallexConnectedAccounts(gateway),
  ]);
  return buildCustomerRosterReconciliation(roster, accounts, completedAt);
}

function groupBy<T>(values: readonly T[], keyOf: (value: T) => string): Map<string, T[]> {
  const groups = new Map<string, T[]>();
  for (const value of values) {
    const key = keyOf(value);
    const group = groups.get(key) ?? [];
    group.push(value);
    groups.set(key, group);
  }
  return groups;
}
