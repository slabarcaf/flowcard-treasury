/** Canonical Salesforce snapshot types. Provider field names stay in field-map.ts. */
export const SNAPSHOT_SCHEMA_VERSION = 1;

export interface SourceMetadata {
  recordId: string;
  sourceUpdatedAt: string | null;
}

export interface SourcedValue<T> {
  value: T | null;
  source: SourceMetadata;
}

export type CustomerStatus = "active" | "paused" | "closed" | "under_review";
export type KybStatus = "verified" | "pending" | "failed";
export type CreditReviewStatus = "approved" | "watch" | "hold" | "review_required";
export type PriorDefaultStatus = "yes" | "no" | "unknown";
export type LoyaltyTier = "starter" | "standard" | "premium" | "unknown";
export type CaseStatus = "new" | "working" | "escalated" | "closed";
export type CasePriority = "low" | "medium" | "high";
export type CaseRiskCategory =
  | "financial_distress"
  | "repayment"
  | "business_continuity"
  | "account_identity"
  | "security";
export type CallOutcome = "connected" | "no_answer" | "voicemail" | "follow_up_required";

export interface SalesforceAccountSnapshot {
  recordId: string;
  sourceUpdatedAt: string | null;
  customerStatus: SourcedValue<CustomerStatus>;
  kybStatus: SourcedValue<KybStatus>;
  creditReviewStatus: SourcedValue<CreditReviewStatus>;
  approvedBridgeCap: SourcedValue<number>;
  priorDefaultStatus: SourcedValue<PriorDefaultStatus>;
  loyaltyTier: SourcedValue<LoyaltyTier>;
}

export interface SalesforceContractSnapshot {
  recordId: string;
  sourceUpdatedAt: string | null;
  status: SourcedValue<"active" | "inactive" | "expired" | "terminated">;
  endDate: SourcedValue<string>;
}

export interface SalesforceCaseSnapshot {
  recordId: string;
  sourceUpdatedAt: string | null;
  status: SourcedValue<CaseStatus>;
  priority: SourcedValue<CasePriority>;
  reason: SourcedValue<string>;
  createdAt: SourcedValue<string>;
  closedAt: SourcedValue<string>;
  isEscalated: SourcedValue<boolean>;
  riskCategory: SourcedValue<CaseRiskCategory>;
  description: SourcedValue<string>;
}

export interface SalesforceCallActivitySnapshot {
  recordId: string;
  sourceUpdatedAt: string | null;
  activityAt: SourcedValue<string>;
  outcome: SourcedValue<CallOutcome>;
  note: SourcedValue<string>;
}

export interface SalesforceCustomerSnapshot {
  schemaVersion: typeof SNAPSHOT_SCHEMA_VERSION;
  mappingVersion: string;
  mappingVerified: boolean;
  account: SalesforceAccountSnapshot;
  contract: SalesforceContractSnapshot;
  cases: SalesforceCaseSnapshot[];
  callActivities: SalesforceCallActivitySnapshot[];
}

export interface TextLengthConfig {
  maxCaseTextLength: number | null;
  maxActivityTextLength: number | null;
}
