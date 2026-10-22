import { describe, expect, it, vi } from "vitest";
import boundedTextFixture from "../../server/src/salesforce/fixtures/bounded-text.json" with { type: "json" };
import invalidMappingFixture from "../../server/src/salesforce/fixtures/invalid-mapping.json" with { type: "json" };
import { getDemoSalesforceSnapshot } from "../../server/src/salesforce/fixture-source.js";
import { validateSnapshot } from "../../server/src/salesforce/validate.js";
import { mockCrmClient } from "../../src/services/mock/CrmClient.js";
import type { SalesforceCustomerSnapshot } from "../../server/src/salesforce/types.js";

const demoCustomerIds = ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"];

function expectKeys(value: object, keys: string[]): void {
  expect(Object.keys(value).sort()).toEqual([...keys].sort());
}

function expectSourcedFields(value: object, recordId: string, fields: string[]): void {
  const record = value as Record<string, unknown>;
  for (const field of fields) {
    const sourced = record[field] as { value: unknown; source: { recordId: string; sourceUpdatedAt: string | null } };
    expectKeys(sourced, ["value", "source"]);
    expectKeys(sourced.source, ["recordId", "sourceUpdatedAt"]);
    expect(sourced.source.recordId).toBe(recordId);
  }
}

describe("Salesforce demo snapshot fixtures", () => {
  it("provides schema-valid canonical snapshots for every synthetic roster customer", () => {
    for (const customerId of demoCustomerIds) {
      const snapshot = getDemoSalesforceSnapshot(customerId);
      expect(snapshot).not.toBeNull();
      expect(validateSnapshot(snapshot!)).toEqual([]);
      expect(snapshot).toMatchObject({
        schemaVersion: 1,
        mappingVersion: "salesforce-proposed-v1",
        mappingVerified: false,
      });
      expect(Object.keys(snapshot!)).toEqual(["schemaVersion", "mappingVersion", "mappingVerified", "account", "contract", "cases", "callActivities"]);
      expect(snapshot!.account.recordId).toMatch(/^demo-sf-/);
      expect(snapshot!.contract.recordId).toMatch(/^demo-contract-/);
      expectKeys(snapshot!.account, ["recordId", "sourceUpdatedAt", "customerStatus", "kybStatus", "creditReviewStatus", "approvedBridgeCap", "priorDefaultStatus", "loyaltyTier"]);
      expectSourcedFields(snapshot!.account, snapshot!.account.recordId, ["customerStatus", "kybStatus", "creditReviewStatus", "approvedBridgeCap", "priorDefaultStatus", "loyaltyTier"]);
      expectKeys(snapshot!.contract, ["recordId", "sourceUpdatedAt", "status", "endDate"]);
      expectSourcedFields(snapshot!.contract, snapshot!.contract.recordId, ["status", "endDate"]);
      for (const item of snapshot!.cases) {
        expectKeys(item, ["recordId", "sourceUpdatedAt", "status", "priority", "reason", "createdAt", "closedAt", "isEscalated", "riskCategory", "description"]);
        expectSourcedFields(item, item.recordId, ["status", "priority", "reason", "createdAt", "closedAt", "isEscalated", "riskCategory", "description"]);
      }
      for (const item of snapshot!.callActivities) {
        expectKeys(item, ["recordId", "sourceUpdatedAt", "activityAt", "outcome", "note"]);
        expectSourcedFields(item, item.recordId, ["activityAt", "outcome", "note"]);
      }
    }
  });

  it("converts the synthetic Bloom CRM note into a canonical call activity note", async () => {
    const bloom = getDemoSalesforceSnapshot("acct_bloom001");
    expect(bloom?.callActivities[0]?.note.value).toContain("Spoke with Bloom's CFO");

    const notes = await mockCrmClient.getLatestNotes("acct_bloom001");
    expect(notes).toEqual([{
      author: "Salesforce call activity",
      timestamp: "2026-10-20T15:40:00Z",
      text: "Spoke with Bloom's CFO. Northstar Supply is the largest client, representing about 65% of revenue, and is disputing last month's invoice. Payment expected next week at the earliest.",
      sourceRecordId: "demo-call-bloom001",
    }]);
    expect(await mockCrmClient.getLatestNotes("unknown-customer")).toEqual([]);
  });

  it("accepts successful empty Cases and Activities for a required demo customer", () => {
    const snapshot = getDemoSalesforceSnapshot("acct_atlas001");
    expect(snapshot?.cases).toEqual([]);
    expect(snapshot?.callActivities).toEqual([]);
    expect(validateSnapshot(snapshot!)).toEqual([]);
  });

  it("rejects a canonical fixture carrying an unmapped value", () => {
    expect(validateSnapshot(invalidMappingFixture as SalesforceCustomerSnapshot)).toContain(
      "Account customer status is not a canonical value",
    );
  });

  it("keeps fixture notes within configured text bounds", () => {
    const snapshot = boundedTextFixture as SalesforceCustomerSnapshot;
    expect(validateSnapshot(snapshot)).toEqual([]);
    expect(Array.from(snapshot.cases[0]?.description.value ?? "").length).toBeLessThanOrEqual(1200);
    expect(Array.from(snapshot.callActivities[0]?.note.value ?? "").length).toBeLessThanOrEqual(500);
  });

  it("uses local fixtures without making Salesforce or Airwallex requests", async () => {
    const fetch = vi.fn(() => Promise.reject(new Error("unexpected provider request")));
    vi.stubGlobal("fetch", fetch);
    try {
      expect(getDemoSalesforceSnapshot("acct_bloom001")?.account.recordId).toBe("demo-sf-bloom001");
      await mockCrmClient.getLatestNotes("acct_bloom001");
      expect(fetch).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
