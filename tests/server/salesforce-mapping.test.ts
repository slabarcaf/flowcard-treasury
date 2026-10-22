import { describe, expect, it } from "vitest";
import {
  SALESFORCE_FIELDS,
  SALESFORCE_MAPPING_VERSION,
  assertSupportedSchemaVersion,
  mapAccountRecord,
  mapCallActivityRecord,
  mapCaseRecord,
  mapContractRecord,
  mapPicklist,
  requireVerifiedMapping,
} from "../../server/src/salesforce/field-map.js";
import {
  boundText,
  isProductionMappingVerified,
  parseTextLengthConfig,
  requireProductionSalesforceReadiness,
  validateAccountSnapshot,
} from "../../server/src/salesforce/validate.js";

const accountRecord = () => ({
  Id: "001-demo",
  LastModifiedDate: "2026-10-20T08:00:00Z",
  [SALESFORCE_FIELDS.account.customerStatus]: "Active",
  [SALESFORCE_FIELDS.account.kybStatus]: "Verified",
  [SALESFORCE_FIELDS.account.creditReviewStatus]: "Approved",
  [SALESFORCE_FIELDS.account.approvedBridgeCap]: 2500,
  [SALESFORCE_FIELDS.account.priorDefaultStatus]: null,
  [SALESFORCE_FIELDS.account.loyaltyTier]: "Standard",
});

describe("Salesforce field mapping", () => {
  it("maps Account fields to canonical names with source IDs, timestamps, and nulls", () => {
    const account = mapAccountRecord(accountRecord());
    expect(account.recordId).toBe("001-demo");
    expect(account.sourceUpdatedAt).toBe("2026-10-20T08:00:00Z");
    expect(account.customerStatus.value).toBe("active");
    expect(account.approvedBridgeCap.value).toBe(2500);
    expect(account.priorDefaultStatus.value).toBeNull();
    expect(account.priorDefaultStatus.source).toEqual({ recordId: "001-demo", sourceUpdatedAt: "2026-10-20T08:00:00Z" });
    expect(Object.keys(account)).not.toContain("Customer_Status__c");
  });

  it("maps Contract, Case, and call-Activity fields and bounds text", () => {
    expect(mapContractRecord({ Id: "800-demo", LastModifiedDate: null, Status: "Activated", EndDate: "2027-01-01" }).status.value).toBe("active");
    const caseRecord = mapCaseRecord({
      Id: "500-demo",
      LastModifiedDate: "2026-10-20T08:00:00Z",
      Status: "New",
      Priority: "High",
      Reason: "Other",
      CreatedDate: "2026-10-20T08:00:00Z",
      ClosedDate: null,
      IsEscalated: true,
      Financial_Risk_Category__c: "Repayment",
      Description: "customer reported a delayed repayment",
    }, 18);
    expect(caseRecord.riskCategory.value).toBe("repayment");
    expect(caseRecord.description.value).toBe("customer reported ");
    expect(caseRecord.isEscalated.value).toBe(true);
    const activity = mapCallActivityRecord({
      Id: "00T-demo",
      LastModifiedDate: null,
      ActivityDate: "2026-10-19",
      Call_Outcome__c: "Follow-up Required",
      Description: "call notes",
    }, 5);
    expect(activity.outcome.value).toBe("follow_up_required");
    expect(activity.note.value).toBe("call ");
  });

  it("preserves optional null values and rejects unknown or drifted picklists", () => {
    expect(mapPicklist("kybStatus", null)).toBeNull();
    expect(() => mapPicklist("kybStatus", "In Progress")).toThrow(/Unrecognized kybStatus/);
    expect(() => mapAccountRecord({ ...accountRecord(), Customer_Status__c: "Newly Onboarded" })).toThrow(/Unrecognized customerStatus/);
    expect(() => mapCaseRecord({ Id: "500-demo", Status: "Pending Customer" }, 10)).toThrow(/Unrecognized caseStatus/);
    expect(() => mapCallActivityRecord({ Id: "00T-demo", Call_Outcome__c: "Reached" }, 10)).toThrow(/Unrecognized callOutcome/);
  });

  it("rejects missing record IDs, invalid dates, malformed amounts, and non-boolean escalation flags", () => {
    expect(() => mapAccountRecord({})).toThrow(/record Id is required/);
    expect(() => mapAccountRecord({ ...accountRecord(), Approved_Bridge_Cap__c: "2500" })).toThrow(/Invalid approved bridge cap/);
    expect(() => mapContractRecord({ Id: "800-demo", EndDate: "not-a-date" })).toThrow(/Invalid Salesforce date/);
    expect(() => mapCaseRecord({ Id: "500-demo", IsEscalated: "true" }, 10)).toThrow(/escalation flag/);
  });

  it("marks the API field map provisional and rejects future schema versions", () => {
    expect(SALESFORCE_MAPPING_VERSION).toBe("salesforce-proposed-v1");
    expect(isProductionMappingVerified()).toBe(false);
    expect(() => requireVerifiedMapping()).toThrow(/provisional/);
    expect(() => assertSupportedSchemaVersion(2)).toThrow(/Unsupported Salesforce snapshot schema/);
    expect(() => mapCaseRecord({ Id: "500-demo", Description: "text" }, null)).toThrow(/limit is not configured/);
  });
});

describe("Salesforce snapshot validation and text configuration", () => {
  it("rejects null or invalid approved caps and required Account fields", () => {
    const account = mapAccountRecord(accountRecord());
    expect(validateAccountSnapshot(account)).toEqual([]);
    expect(validateAccountSnapshot({ ...account, approvedBridgeCap: { ...account.approvedBridgeCap, value: null } })).toContain("Account approved bridge cap is required");
    expect(validateAccountSnapshot({ ...account, approvedBridgeCap: { ...account.approvedBridgeCap, value: -1 } })).toContain("Account approved bridge cap must be a finite non-negative number");
    expect(validateAccountSnapshot({ ...account, kybStatus: { ...account.kybStatus, value: null } })).toContain("Account KYB status is required");
  });

  it("parses configurable text limits and requires both before production", () => {
    expect(parseTextLengthConfig({ SALESFORCE_MAX_CASE_TEXT_LENGTH: "1200", SALESFORCE_MAX_ACTIVITY_TEXT_LENGTH: "500" })).toEqual({ maxCaseTextLength: 1200, maxActivityTextLength: 500 });
    expect(parseTextLengthConfig({})).toEqual({ maxCaseTextLength: null, maxActivityTextLength: null });
    expect(() => parseTextLengthConfig({ SALESFORCE_MAX_CASE_TEXT_LENGTH: "0" })).toThrow(/positive integer/);
    expect(() => requireProductionSalesforceReadiness({ maxCaseTextLength: 1200, maxActivityTextLength: 500 })).toThrow(/mapping .* provisional/);
    expect(() => requireProductionSalesforceReadiness({ maxCaseTextLength: 1200, maxActivityTextLength: null })).toThrow(/text length limits/);
    expect(boundText("abcdef", 3, "note")).toBe("abc");
    expect(boundText(null, null, "note")).toBeNull();
    expect(() => boundText("abc", null, "note")).toThrow(/not configured/);
  });
});
