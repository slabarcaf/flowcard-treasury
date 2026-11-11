import { describe, expect, it, vi } from "vitest";
import type { DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { buildCustomerRosterReconciliation, reconcileCustomerRoster } from "../../server/src/reconciliation/customer-roster.js";
import type { CustomerRosterEntry } from "../../server/src/roster/types.js";

function rosterEntry(customerId: string, accountId: string, isActive: boolean): CustomerRosterEntry {
  return {
    customerId,
    salesforceAccountId: `sf-${customerId}`,
    airwallexConnectedAccountId: accountId,
    isActive,
    enrolledAt: new Date("2026-10-21T00:00:00Z"),
    enrolledBy: "operator",
    enrollmentReason: "approved",
    deactivatedAt: isActive ? null : new Date("2026-10-22T00:00:00Z"),
    deactivatedBy: isActive ? null : "operator",
    deactivationReason: isActive ? null : "closed program",
    updatedAt: new Date("2026-10-21T00:00:00Z"),
  };
}

describe("Airwallex customer roster reconciliation", () => {
  it("reports missing, extra, duplicate, and status-mismatched IDs by stable IDs", () => {
    const report = buildCustomerRosterReconciliation(
      [
        rosterEntry("customer-active", "acct-missing", true),
        rosterEntry("customer-deactivated", "acct-deactivated", false),
        rosterEntry("customer-mismatch", "acct-mismatch", false),
        rosterEntry("customer-duplicate-a", "acct-duplicate", true),
        rosterEntry("customer-duplicate-b", "acct-duplicate", false),
      ],
      [
        { id: "acct-deactivated", status: "SUSPENDED" },
        { id: "acct-mismatch", status: "ACTIVE" },
        { id: "acct-duplicate", status: "ACTIVE" },
        { id: "acct-duplicate", status: "ACTIVE" },
        { id: "acct-extra", status: "CREATED" },
      ],
      new Date("2026-10-23T12:00:00Z"),
    );

    expect(report).toEqual({
      source: "airwallex_connected_account_list",
      completedAt: "2026-10-23T12:00:00.000Z",
      missing: [{ customerId: "customer-active", airwallexConnectedAccountId: "acct-missing", rosterStatus: "active" }],
      extra: [{ airwallexConnectedAccountId: "acct-extra", providerStatus: "CREATED" }],
      duplicates: [{
        airwallexConnectedAccountId: "acct-duplicate",
        rosterCustomerIds: ["customer-duplicate-a", "customer-duplicate-b"],
        providerOccurrences: 2,
      }],
      statusMismatches: [{
        customerId: "customer-mismatch",
        airwallexConnectedAccountId: "acct-mismatch",
        rosterStatus: "deactivated",
        providerStatus: "ACTIVE",
      }],
    });
  });

  it("reads provider pages and roster without writing or changing Salesforce state", async () => {
    const rosterRow = {
      customer_id: "customer-1",
      salesforce_account_id: "sf-1",
      airwallex_connected_account_id: "acct-1",
      is_active: true,
      enrolled_at: new Date("2026-10-21T00:00:00Z"),
      enrolled_by: "operator",
      enrollment_reason: "approved",
      deactivated_at: null,
      deactivated_by: null,
      deactivation_reason: null,
      updated_at: new Date("2026-10-21T00:00:00Z"),
    };
    const query = vi.fn().mockResolvedValue({ rows: [rosterRow], rowCount: 1 } satisfies QueryResult<typeof rosterRow>);
    const pool = { query, connect: vi.fn(), end: vi.fn() } as unknown as DatabasePool;
    const gateway = { listConnectedAccounts: vi.fn().mockResolvedValue([{ id: "acct-1", status: "ACTIVE" }]) };

    const report = await reconcileCustomerRoster(pool, gateway, new Date("2026-10-23T12:00:00Z"));

    expect(gateway.listConnectedAccounts).toHaveBeenCalledOnce();
    expect(query).toHaveBeenCalledOnce();
    expect(query.mock.calls[0][0]).toContain("FROM customer_roster ORDER BY customer_id");
    expect(query.mock.calls[0][0]).not.toMatch(/\b(INSERT|UPDATE|DELETE)\b/i);
    expect(report).toMatchObject({
      source: "airwallex_connected_account_list",
      missing: [],
      extra: [],
      duplicates: [],
      statusMismatches: [],
    });
    expect(report).not.toHaveProperty("salesforceCompleteness");
    expect(pool.connect).not.toHaveBeenCalled();
  });

  it("does not infer identity from names or emails", () => {
    const report = buildCustomerRosterReconciliation(
      [rosterEntry("customer-1", "acct-roster", true)],
      [{ id: "acct-provider", status: "ACTIVE" }],
    );

    expect(report.missing).toHaveLength(1);
    expect(report.extra).toHaveLength(1);
    expect(report.statusMismatches).toHaveLength(0);
  });
});
