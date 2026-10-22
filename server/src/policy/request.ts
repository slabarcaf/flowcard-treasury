export const SUPPORTED_SPEND_CATEGORIES = [
  "supplier",
  "payroll",
  "rent",
  "utilities",
  "taxes",
  "software",
  "inventory",
  "other_business",
] as const;

export type SupportedSpendCategory = (typeof SUPPORTED_SPEND_CATEGORIES)[number];

/** Provider or application request facts that must be traceable to a card spend. */
export interface BridgeRequest {
  requestId: string;
  customerId: string;
  transactionId: string;
  transactionAmountCents: number;
  currency: string;
  spendCategory: string;
  traceable: boolean;
}

export type RequestValidationCode =
  | "REQUEST_ID_MISSING"
  | "CUSTOMER_ID_MISSING"
  | "TRANSACTION_ID_MISSING"
  | "INVALID_AMOUNT"
  | "UNSUPPORTED_CURRENCY"
  | "UNTRACEABLE_SPEND"
  | "UNSUPPORTED_SPEND_CATEGORY";

export function validateBridgeRequest(request: BridgeRequest): RequestValidationCode[] {
  const errors: RequestValidationCode[] = [];
  if (!request.requestId.trim()) errors.push("REQUEST_ID_MISSING");
  if (!request.customerId.trim()) errors.push("CUSTOMER_ID_MISSING");
  if (!request.transactionId.trim()) errors.push("TRANSACTION_ID_MISSING");
  if (!Number.isSafeInteger(request.transactionAmountCents) || request.transactionAmountCents <= 0) {
    errors.push("INVALID_AMOUNT");
  }
  if (request.currency !== "USD") errors.push("UNSUPPORTED_CURRENCY");
  if (!request.traceable) errors.push("UNTRACEABLE_SPEND");
  if (!isSupportedSpendCategory(request.spendCategory)) errors.push("UNSUPPORTED_SPEND_CATEGORY");
  return errors;
}

export function isSupportedSpendCategory(value: string): value is SupportedSpendCategory {
  return (SUPPORTED_SPEND_CATEGORIES as readonly string[]).includes(value);
}
