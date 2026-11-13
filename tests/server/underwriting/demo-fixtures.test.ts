import { describe, expect, it } from "vitest";
import { buildDemoUnderwritingProfileCandidate } from "../../../server/src/underwriting/demo-fixtures.js";
import { validateProfileCandidate } from "../../../server/src/underwriting/types.js";

describe("demo underwriting fixtures", () => {
  it("builds complete, explicitly synthetic profiles for every seeded demo customer", () => {
    const now = new Date("2026-10-22T12:00:00.000Z");
    for (const customerId of ["acct_bloom001", "acct_atlas001", "acct_nova001", "acct_pixel001"]) {
      const candidate = buildDemoUnderwritingProfileCandidate(customerId, now, "fixture-run");
      expect(candidate?.sourceMode).toBe("demo");
      expect(candidate?.queryCompletion.expected_inflows).toEqual({ complete: true, pages: 1 });
      expect(candidate && validateProfileCandidate(candidate)).toEqual([]);
    }
  });

  it("does not fabricate profiles for roster IDs without an explicit fixture", () => {
    expect(buildDemoUnderwritingProfileCandidate("unmapped-customer")).toBeNull();
  });
});
