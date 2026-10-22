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
  if (!snapshot.contract.recordId.trim()) errors.push("Contract record ID is required");
  if (snapshot.contract.status.value === null) errors.push("Contract status is required");
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
