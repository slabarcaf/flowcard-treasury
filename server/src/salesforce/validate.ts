import type { SalesforceAccountSnapshot, SalesforceCustomerSnapshot, TextLengthConfig } from "./types.js";
import { PICKLIST_VALUES, SALESFORCE_MAPPING_VERIFIED, requireVerifiedMapping } from "./field-map.js";

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
  if (snapshot.account.customerStatus.value !== null && !isCanonicalValue(snapshot.account.customerStatus.value, "customerStatus")) {
    errors.push("Account customer status is not a canonical value");
  }
  if (snapshot.account.kybStatus.value !== null && !isCanonicalValue(snapshot.account.kybStatus.value, "kybStatus")) {
    errors.push("Account KYB status is not a canonical value");
  }
  if (snapshot.account.creditReviewStatus.value !== null && !isCanonicalValue(snapshot.account.creditReviewStatus.value, "creditReviewStatus")) {
    errors.push("Account credit review status is not a canonical value");
  }
  if (snapshot.account.priorDefaultStatus.value !== null && !isCanonicalValue(snapshot.account.priorDefaultStatus.value, "priorDefaultStatus")) {
    errors.push("Account prior default status is not a canonical value");
  }
  if (snapshot.account.loyaltyTier.value !== null && !isCanonicalValue(snapshot.account.loyaltyTier.value, "loyaltyTier")) {
    errors.push("Account loyalty tier is not a canonical value");
  }
  if (!snapshot.contract.recordId.trim()) errors.push("Contract record ID is required");
  if (snapshot.contract.status.value === null) errors.push("Contract status is required");
  if (snapshot.contract.status.value !== null && !isCanonicalValue(snapshot.contract.status.value, "contractStatus")) {
    errors.push("Contract status is not a canonical value");
  }
  for (const [index, item] of snapshot.cases.entries()) {
    if (!item.recordId.trim()) errors.push(`Case ${index} record ID is required`);
    if (item.status.value !== null && !isCanonicalValue(item.status.value, "caseStatus")) errors.push(`Case ${index} status is not a canonical value`);
    if (item.priority.value !== null && !isCanonicalValue(item.priority.value, "casePriority")) errors.push(`Case ${index} priority is not a canonical value`);
    if (item.riskCategory.value !== null && !isCanonicalValue(item.riskCategory.value, "riskCategory")) errors.push(`Case ${index} risk category is not a canonical value`);
  }
  for (const [index, item] of snapshot.callActivities.entries()) {
    if (!item.recordId.trim()) errors.push(`Call activity ${index} record ID is required`);
    if (item.outcome.value !== null && !isCanonicalValue(item.outcome.value, "callOutcome")) errors.push(`Call activity ${index} outcome is not a canonical value`);
  }
  return errors;
}

export function isProductionMappingVerified(): boolean {
  return SALESFORCE_MAPPING_VERIFIED;
}

function isCanonicalValue<K extends keyof typeof PICKLIST_VALUES>(value: string, name: K): boolean {
  return (Object.values(PICKLIST_VALUES[name]) as string[]).includes(value);
}

function parseOptionalPositiveInteger(value: string | undefined, name: string): number | null {
  if (value === undefined || value.trim() === "") return null;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 1) throw new Error(`${name} must be a positive integer`);
  return parsed;
}
