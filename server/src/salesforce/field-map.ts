import {
  SNAPSHOT_SCHEMA_VERSION,
  type CallOutcome,
  type CasePriority,
  type CaseRiskCategory,
  type CaseStatus,
  type CreditReviewStatus,
  type CustomerStatus,
  type KybStatus,
  type LoyaltyTier,
  type PriorDefaultStatus,
  type SalesforceAccountSnapshot,
  type SalesforceCaseSnapshot,
  type SalesforceCallActivitySnapshot,
  type SalesforceContractSnapshot,
} from "./types.js";

/** Proposed Salesforce names from the source specification; not org-verified. */
export const SALESFORCE_MAPPING_VERSION = "salesforce-proposed-v1";
export const SALESFORCE_MAPPING_VERIFIED = false;

export const SALESFORCE_FIELDS = {
  account: {
    customerStatus: "Customer_Status__c",
    kybStatus: "KYB_Status__c",
    creditReviewStatus: "Credit_Review_Status__c",
    approvedBridgeCap: "Approved_Bridge_Cap__c",
    priorDefaultStatus: "Prior_Default_Status__c",
    loyaltyTier: "Loyalty_Tier__c",
    updatedAt: "LastModifiedDate",
  },
  contract: { status: "Status", endDate: "EndDate", updatedAt: "LastModifiedDate" },
  case: {
    status: "Status",
    priority: "Priority",
    reason: "Reason",
    createdAt: "CreatedDate",
    closedAt: "ClosedDate",
    isEscalated: "IsEscalated",
    riskCategory: "Financial_Risk_Category__c",
    description: "Description",
    updatedAt: "LastModifiedDate",
  },
  activity: {
    activityAt: "ActivityDate",
    outcome: "Call_Outcome__c",
    note: "Description",
    updatedAt: "LastModifiedDate",
  },
} as const;

export const PICKLIST_VALUES = {
  contractStatus: {
    Activated: "active",
    Draft: "inactive",
    Inactive: "inactive",
    Expired: "expired",
    Terminated: "terminated",
  } satisfies Record<string, SalesforceContractSnapshot["status"]["value"] & string>,
  customerStatus: {
    Active: "active",
    Paused: "paused",
    Closed: "closed",
    "Under Review": "under_review",
  } satisfies Record<string, CustomerStatus>,
  kybStatus: { Verified: "verified", Pending: "pending", Failed: "failed" } satisfies Record<string, KybStatus>,
  creditReviewStatus: {
    Approved: "approved",
    Watch: "watch",
    Hold: "hold",
    "Review Required": "review_required",
  } satisfies Record<string, CreditReviewStatus>,
  priorDefaultStatus: { Yes: "yes", No: "no", Unknown: "unknown" } satisfies Record<string, PriorDefaultStatus>,
  loyaltyTier: {
    Starter: "starter",
    Standard: "standard",
    Premium: "premium",
    Unknown: "unknown",
  } satisfies Record<string, LoyaltyTier>,
  caseStatus: {
    New: "new",
    Working: "working",
    Escalated: "escalated",
    Closed: "closed",
  } satisfies Record<string, CaseStatus>,
  casePriority: { Low: "low", Medium: "medium", High: "high" } satisfies Record<string, CasePriority>,
  riskCategory: {
    "Financial Distress": "financial_distress",
    Repayment: "repayment",
    "Business Continuity": "business_continuity",
    "Account Identity": "account_identity",
    Security: "security",
  } satisfies Record<string, CaseRiskCategory>,
  callOutcome: {
    Connected: "connected",
    "No Answer": "no_answer",
    Voicemail: "voicemail",
    "Follow-up Required": "follow_up_required",
  } satisfies Record<string, CallOutcome>,
} as const;

export type PicklistName = keyof typeof PICKLIST_VALUES;

type SalesforceRecord = Record<string, unknown>;

export function boundedText(value: unknown, maxLength: number | null, name: string): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "string") throw new Error(`${name} must be text or null`);
  if (maxLength === null) throw new Error(`${name} text length limit is not configured`);
  return Array.from(value).slice(0, maxLength).join("");
}

function nullableString(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "string") throw new Error("Expected Salesforce text or null");
  return value;
}

function source(record: SalesforceRecord, id: string) {
  return { recordId: id, sourceUpdatedAt: nullableString(record["LastModifiedDate"]) };
}

function requiredRecordId(record: SalesforceRecord): string {
  const id = record["Id"];
  if (typeof id !== "string" || id.trim() === "") throw new Error("Salesforce record Id is required");
  return id;
}

function optionalDate(record: SalesforceRecord, fieldName: string): string | null {
  const value = record[fieldName];
  if (value === null || value === undefined || value === "") return null;
  if (typeof value !== "string" || Number.isNaN(Date.parse(value))) throw new Error(`Invalid Salesforce date field ${fieldName}`);
  return value;
}

export function mapAccountRecord(record: SalesforceRecord): SalesforceAccountSnapshot {
  const id = requiredRecordId(record);
  const metadata = source(record, id);
  const cap = record[SALESFORCE_FIELDS.account.approvedBridgeCap];
  if (cap !== null && cap !== undefined && (typeof cap !== "number" || !Number.isFinite(cap))) throw new Error("Invalid approved bridge cap value");
  return {
    ...metadata,
    customerStatus: { value: mapPicklist("customerStatus", record[SALESFORCE_FIELDS.account.customerStatus]) as CustomerStatus | null, source: metadata },
    kybStatus: { value: mapPicklist("kybStatus", record[SALESFORCE_FIELDS.account.kybStatus]) as KybStatus | null, source: metadata },
    creditReviewStatus: { value: mapPicklist("creditReviewStatus", record[SALESFORCE_FIELDS.account.creditReviewStatus]) as CreditReviewStatus | null, source: metadata },
    approvedBridgeCap: { value: (cap as number | null | undefined) ?? null, source: metadata },
    priorDefaultStatus: { value: mapPicklist("priorDefaultStatus", record[SALESFORCE_FIELDS.account.priorDefaultStatus]) as PriorDefaultStatus | null, source: metadata },
    loyaltyTier: { value: mapPicklist("loyaltyTier", record[SALESFORCE_FIELDS.account.loyaltyTier]) as LoyaltyTier | null, source: metadata },
  };
}

export function mapContractRecord(record: SalesforceRecord): SalesforceContractSnapshot {
  const id = requiredRecordId(record);
  const metadata = source(record, id);
  return {
    ...metadata,
    status: { value: mapPicklist("contractStatus", record[SALESFORCE_FIELDS.contract.status]), source: metadata },
    endDate: { value: optionalDate(record, SALESFORCE_FIELDS.contract.endDate), source: metadata },
  };
}

export function mapCaseRecord(record: SalesforceRecord, maxTextLength: number | null): SalesforceCaseSnapshot {
  const id = requiredRecordId(record);
  const metadata = source(record, id);
  const escalated = record[SALESFORCE_FIELDS.case.isEscalated];
  if (escalated !== null && escalated !== undefined && typeof escalated !== "boolean") throw new Error("Invalid Salesforce escalation flag");
  return {
    ...metadata,
    status: { value: mapPicklist("caseStatus", record[SALESFORCE_FIELDS.case.status]) as CaseStatus | null, source: metadata },
    priority: { value: mapPicklist("casePriority", record[SALESFORCE_FIELDS.case.priority]) as CasePriority | null, source: metadata },
    reason: { value: nullableString(record[SALESFORCE_FIELDS.case.reason]), source: metadata },
    createdAt: { value: optionalDate(record, SALESFORCE_FIELDS.case.createdAt), source: metadata },
    closedAt: { value: optionalDate(record, SALESFORCE_FIELDS.case.closedAt), source: metadata },
    isEscalated: { value: (escalated as boolean | null | undefined) ?? null, source: metadata },
    riskCategory: { value: mapPicklist("riskCategory", record[SALESFORCE_FIELDS.case.riskCategory]) as CaseRiskCategory | null, source: metadata },
    description: { value: boundedText(record[SALESFORCE_FIELDS.case.description], maxTextLength, "Case description"), source: metadata },
  };
}

export function mapCallActivityRecord(record: SalesforceRecord, maxTextLength: number | null): SalesforceCallActivitySnapshot {
  const id = requiredRecordId(record);
  const metadata = source(record, id);
  return {
    ...metadata,
    activityAt: { value: optionalDate(record, SALESFORCE_FIELDS.activity.activityAt), source: metadata },
    outcome: { value: mapPicklist("callOutcome", record[SALESFORCE_FIELDS.activity.outcome]) as CallOutcome | null, source: metadata },
    note: { value: boundedText(record[SALESFORCE_FIELDS.activity.note], maxTextLength, "Call activity note"), source: metadata },
  };
}

export function mapPicklist<K extends PicklistName>(name: K, value: unknown): (typeof PICKLIST_VALUES)[K][keyof (typeof PICKLIST_VALUES)[K]] | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "string") throw new Error(`Invalid ${name} picklist value type`);
  const mapped = PICKLIST_VALUES[name][value as keyof (typeof PICKLIST_VALUES)[K]];
  if (mapped === undefined) throw new Error(`Unrecognized ${name} picklist value: ${value}`);
  return mapped;
}

export function requireVerifiedMapping(): void {
  if (!SALESFORCE_MAPPING_VERIFIED) {
    throw new Error(`Salesforce mapping ${SALESFORCE_MAPPING_VERSION} is provisional; org verification is required`);
  }
}

export function assertSupportedSchemaVersion(version: number): void {
  if (version !== SNAPSHOT_SCHEMA_VERSION) {
    throw new Error(`Unsupported Salesforce snapshot schema version: ${version}`);
  }
}
