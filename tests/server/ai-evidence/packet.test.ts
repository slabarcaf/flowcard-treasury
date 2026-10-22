import { describe, expect, it } from "vitest";
import bloom from "../../../server/src/salesforce/fixtures/bloom.json";
import type { SalesforceCustomerSnapshot } from "../../../server/src/salesforce/types.js";
import type { CustomerSnapshotReadModel } from "../../../server/src/snapshots/freshness.js";
import { canonicalEvidence, evidenceFingerprint } from "../../../server/src/ai-evidence/canonicalize.js";
import { deduplicateEvidence } from "../../../server/src/ai-evidence/dedupe.js";
import { buildEvidencePacket } from "../../../server/src/ai-evidence/packet.js";

const snapshot = structuredClone(bloom) as SalesforceCustomerSnapshot;
const now = new Date("2026-10-21T00:00:00Z");

function readModel(data = snapshot, customerId = "customer-1"): CustomerSnapshotReadModel {
  return {
    customerId,
    active: true,
    freshness: "fresh",
    ageSeconds: 60,
    snapshot: {
      snapshotId: "snapshot-1",
      version: 1,
      capturedAt: "2026-10-20T23:00:00Z",
      publishedAt: "2026-10-20T23:01:00Z",
      sourceMode: "demo",
      schemaVersion: 1,
      mappingVersion: "salesforce-proposed-v1",
      data,
    },
    latestAttempt: { status: "succeeded", finishedAt: "2026-10-20T23:01:00Z" },
    validationErrors: [],
  };
}

function build(data = snapshot, customerId = "customer-1", maxPacketBytes = 100_000) {
  return buildEvidencePacket(readModel(data, customerId), { now, maxPacketBytes, maxNarrativeChars: 2_000 });
}

describe("bounded customer evidence packets", () => {
  it("uses only risk Cases and recent call Activities with allowlisted fields", () => {
    const packet = build();
    expect(packet.status).toBe("ready");
    if (packet.status !== "ready") return;
    expect(packet.packet.entries).toHaveLength(2);
    expect(packet.packet.entries.map(({ sourceType }) => sourceType)).toEqual(["call_activity", "case"]);
    expect(packet.packet.entries[0]?.fields).toEqual({
      activityAt: "2026-10-20T15:40:00Z",
      outcome: "connected",
      note: "Spoke with Bloom's CFO. Their largest client, about 65% of revenue, is disputing last month's invoice. Payment expected next week at the earliest.",
    });
    expect(JSON.stringify(packet.packet)).not.toMatch(/attachment|email|sourceUpdatedAt|recordId/);
  });

  it("deduplicates exact records, preserves all sorted source IDs, and leaves changed fields distinct", () => {
    const data = structuredClone(snapshot);
    data.cases.push({ ...data.cases[0]!, recordId: "case-duplicate", sourceUpdatedAt: "2026-10-21T00:00:00Z" });
    data.cases.push({ ...data.cases[0]!, recordId: "case-different-date", createdAt: { ...data.cases[0]!.createdAt, value: "2026-10-19T00:00:00Z" } });
    data.cases.push({ ...data.cases[0]!, recordId: "case-different-category", riskCategory: { ...data.cases[0]!.riskCategory, value: "financial_distress" } });
    const packet = build(data);
    expect(packet.status).toBe("ready");
    if (packet.status !== "ready") return;
    const cases = packet.packet.entries.filter((entry) => entry.sourceType === "case");
    expect(cases).toHaveLength(3);
    expect(cases.find((entry) => entry.sourceIds.includes("case-duplicate"))?.sourceIds).toEqual([
      "case-duplicate", "demo-case-bloom001",
    ]);
  });

  it("keeps near-duplicate narratives separate because only exact canonical matches collapse", () => {
    const data = structuredClone(snapshot);
    const original = data.cases[0]!;
    data.cases.push({
      ...original,
      recordId: "case-near-duplicate",
      description: { ...original.description, value: `${original.description.value} Follow up.` },
    });
    const packet = build(data);
    expect(packet.status).toBe("ready");
    if (packet.status !== "ready") return;
    expect(packet.packet.entries.filter((entry) => entry.sourceType === "case")).toHaveLength(2);
    expect(packet.packet.entries.find((entry) => entry.sourceIds.includes("case-near-duplicate"))?.sourceIds)
      .toEqual(["case-near-duplicate"]);
  });

  it("orders recent calls deterministically, limits them to five, and excludes calls outside 90 days", () => {
    const data = structuredClone(snapshot);
    const base = data.callActivities[0]!;
    data.callActivities = Array.from({ length: 7 }, (_, index) => ({
      ...base,
      recordId: `call-${index}`,
      activityAt: { ...base.activityAt, value: `2026-10-${String(20 - index).padStart(2, "0")}T10:00:00Z` },
    }));
    data.callActivities.push({ ...base, recordId: "call-old", activityAt: { ...base.activityAt, value: "2026-07-22T00:00:00Z" } });
    const first = build(data);
    const second = build({ ...data, callActivities: [...data.callActivities].reverse() });
    expect(first).toEqual(second);
    expect(first.status).toBe("ready");
    if (first.status !== "ready") return;
    const calls = first.packet.entries.filter((entry) => entry.sourceType === "call_activity");
    expect(calls).toHaveLength(5);
    expect(calls.map((entry) => entry.sourceIds[0])).toEqual(["call-0", "call-1", "call-2", "call-3", "call-4"]);
  });

  it("keeps customer identity in canonical fingerprints and excludes source IDs and ingestion timestamps", () => {
    const fields = { date: "2026-10-20", category: "repayment", text: "claim" };
    const first = canonicalEvidence({ customerId: "customer-1", sourceType: "case", fields });
    expect(first).toBe(canonicalEvidence({ customerId: "customer-1", sourceType: "case", fields: { text: "claim", date: "2026-10-20", category: "repayment" } }));
    expect(evidenceFingerprint(first)).not.toBe(evidenceFingerprint(canonicalEvidence({ customerId: "customer-2", sourceType: "case", fields })));
    expect(evidenceFingerprint(first)).toMatch(/^[a-f0-9]{64}$/);
    expect(deduplicateEvidence([
      { customerId: "customer-1", sourceType: "case", sourceId: "case-a", sortAt: null, fields },
      { customerId: "customer-2", sourceType: "case", sourceId: "case-b", sortAt: null, fields },
    ])).toHaveLength(2);
  });

  it("fails closed on stale or inactive snapshots and oversized packets without truncating", () => {
    expect(buildEvidencePacket({ ...readModel(), freshness: "stale" }, { now, maxPacketBytes: 100_000, maxNarrativeChars: 2_000 })).toMatchObject({ status: "failed" });
    expect(buildEvidencePacket({ ...readModel(), active: false }, { now, maxPacketBytes: 100_000, maxNarrativeChars: 2_000 })).toMatchObject({ status: "failed" });
    expect(build(snapshot, "customer-1", 10)).toMatchObject({ status: "failed", reason: expect.stringContaining("exceeds configured size limit") });
    const tooLong = structuredClone(snapshot);
    tooLong.cases[0]!.description.value = "x".repeat(2_001);
    expect(build(tooLong)).toMatchObject({ status: "failed", reason: expect.stringContaining("exceeds configured text limit") });
  });
});
