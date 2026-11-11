import { SALESFORCE_FIELDS } from "./field-map.js";

/** Query identifiers are the only Salesforce objects the snapshot source can read. */
export type SalesforceQueryName = "account" | "contract" | "case" | "activity";

export interface SalesforceQueryDefinition {
  object: "Account" | "Contract" | "Case" | "Task";
  fields: readonly string[];
  accountIdField: "Id" | "AccountId" | "WhatId";
  filter: "all" | "risk" | "call";
}

const accountFields = SALESFORCE_FIELDS.account;
const contractFields = SALESFORCE_FIELDS.contract;
const caseFields = SALESFORCE_FIELDS.case;
const activityFields = SALESFORCE_FIELDS.activity;

/** Fixed field/object allowlist; account ID is always supplied separately as a bind value. */
export const SALESFORCE_QUERIES: Readonly<Record<SalesforceQueryName, SalesforceQueryDefinition>> = {
  account: {
    object: "Account",
    fields: ["Id", accountFields.customerStatus, accountFields.kybStatus, accountFields.creditReviewStatus,
      accountFields.approvedBridgeCap, accountFields.priorDefaultStatus, accountFields.loyaltyTier, accountFields.updatedAt],
    accountIdField: "Id",
    filter: "all",
  },
  contract: {
    object: "Contract",
    fields: ["Id", "AccountId", contractFields.status, contractFields.endDate, contractFields.updatedAt],
    accountIdField: "AccountId",
    filter: "all",
  },
  case: {
    object: "Case",
    fields: ["Id", "AccountId", caseFields.status, caseFields.priority, caseFields.reason, caseFields.createdAt,
      caseFields.closedAt, caseFields.isEscalated, caseFields.riskCategory, caseFields.description, caseFields.updatedAt],
    accountIdField: "AccountId",
    filter: "risk",
  },
  activity: {
    object: "Task",
    fields: ["Id", "WhatId", activityFields.activityAt, activityFields.outcome, activityFields.note, activityFields.updatedAt],
    accountIdField: "WhatId",
    filter: "call",
  },
};

export function isSalesforceQueryName(value: string): value is SalesforceQueryName {
  return Object.hasOwn(SALESFORCE_QUERIES, value);
}
