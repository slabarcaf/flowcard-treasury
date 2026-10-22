import type { SalesforceAccountSnapshot, SalesforceCustomerSnapshot, TextLengthConfig } from "./types.js";
import { SALESFORCE_MAPPING_VERIFIED, requireVerifiedMapping } from "./field-map.js";

export function parseTextLengthConfig(env: NodeJS.ProcessEnv = process.env): TextLengthConfig {
  return {
    maxCaseTextLength: parseOptionalPositiveInteger(env.SALESFORCE_MAX_CASE_TEXT_LENGTH, "SALESFORCE_MAX_CASE_TEXT_LENGTH"),
    maxActivityTextLength: parseOptionalPositiveInteger(env.SALESFORCE_MAX_ACTIVITY_TEXT_LENGTH, "SALESFORCE_MAX_ACTIVITY_TEXT_LENGTH"),
  };
}

export function requireProductionTextLimits(config: TextLengthConfig): void {
  if (config.maxCaseTextLength === null || config.maxActivityTextLength === null) {
    throw new Error("Production Salesforce sync requires case and activity text length limits");
  }
}

export function requireProductionSalesforceReadiness(config: TextLengthConfig): void {
  requireProductionTextLimits(config);
  requireVerifiedMapping();
}

export function validateAccountSnapshot(account: SalesforceAccountSnapshot): string[] {
  const errors: string[] = [];
  if (!account.recordId.trim()) errors.push("Account record ID is required");
  if (account.customerStatus.value === null) errors.push("Account customer status is required");
  if (account.kybStatus.value === null) errors.push("Account KYB status is required");
  if (account.creditReviewStatus.value === null) errors.push("Account credit review status is required");
  if (account.approvedBridgeCap.value === null) errors.push("Account approved bridge cap is required");
  else if (!Number.isFinite(account.approvedBridgeCap.value) || account.approvedBridgeCap.value < 0) {
    errors.push("Account approved bridge cap must be a finite non-negative number");
  }
  return errors;
}

export function validateSnapshot(snapshot: SalesforceCustomerSnapshot): string[] {
  const errors = validateAccountSnapshot(snapshot.account);
  if (snapshot.schemaVersion !== 1) errors.push(`Unsupported snapshot schema version: ${snapshot.schemaVersion}`);
  if (!snapshot.mappingVersion.trim()) errors.push("Mapping version is required");
  if (snapshot.account.customerStatus.value !== null && !["active", "paused", "closed", "under_review"].includes(snapshot.account.customerStatus.value)) {
    errors.push("Account customer status is not a canonical value");
  }
  if (snapshot.account.kybStatus.value !== null && !["verified", "pending", "failed"].includes(snapshot.account.kybStatus.value)) {
    errors.push("Account KYB status is not a canonical value");
  }
  if (snapshot.account.creditReviewStatus.value !== null && !["approved", "watch", "hold", "review_required"].includes(snapshot.account.creditReviewStatus.value)) {
    errors.push("Account credit review status is not a canonical value");
  }
  if (snapshot.account.priorDefaultStatus.value !== null && !["yes", "no", "unknown"].includes(snapshot.account.priorDefaultStatus.value)) {
    errors.push("Account prior default status is not a canonical value");
  }
  if (snapshot.account.loyaltyTier.value !== null && !["starter", "standard", "premium", "unknown"].includes(snapshot.account.loyaltyTier.value)) {
    errors.push("Account loyalty tier is not a canonical value");
  }
  if (!snapshot.contract.recordId.trim()) errors.push("Contract record ID is required");
  if (snapshot.contract.status.value === null) errors.push("Contract status is required");
  if (snapshot.contract.status.value !== null && !["active", "inactive", "expired", "terminated"].includes(snapshot.contract.status.value)) {
    errors.push("Contract status is not a canonical value");
  }
  const caseStatuses = ["new", "working", "escalated", "closed"];
  const priorities = ["low", "medium", "high"];
  const riskCategories = ["financial_distress", "repayment", "business_continuity", "account_identity", "security"];
  for (const [index, item] of snapshot.cases.entries()) {
    if (!item.recordId.trim()) errors.push(`Case ${index} record ID is required`);
    if (item.status.value !== null && !caseStatuses.includes(item.status.value)) errors.push(`Case ${index} status is not a canonical value`);
    if (item.priority.value !== null && !priorities.includes(item.priority.value)) errors.push(`Case ${index} priority is not a canonical value`);
    if (item.riskCategory.value !== null && !riskCategories.includes(item.riskCategory.value)) errors.push(`Case ${index} risk category is not a canonical value`);
  }
  const outcomes = ["connected", "no_answer", "voicemail", "follow_up_required"];
  for (const [index, item] of snapshot.callActivities.entries()) {
    if (!item.recordId.trim()) errors.push(`Call activity ${index} record ID is required`);
    if (item.outcome.value !== null && !outcomes.includes(item.outcome.value)) errors.push(`Call activity ${index} outcome is not a canonical value`);
  }
  return errors;
}

export function isProductionMappingVerified(): boolean {
  return SALESFORCE_MAPPING_VERIFIED;
}

export function boundText(value: unknown, maxLength: number | null, fieldName: string): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "string") throw new Error(`${fieldName} must be text or null`);
  if (maxLength === null) throw new Error(`${fieldName} text length limit is not configured`);
  return Array.from(value).slice(0, maxLength).join("");
}

function parseOptionalPositiveInteger(value: string | undefined, name: string): number | null {
  if (value === undefined || value.trim() === "") return null;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 1) throw new Error(`${name} must be a positive integer`);
  return parsed;
}
