import type { IssuingSimulationGateway, IssuingSimulationResponse } from "./client.js";
import { ProviderHttpError } from "./client.js";

const MAX_SIMULATION_AMOUNT_CENTS = 999_999;

export interface IssuingSimulationInput {
  accountId: string;
  cardId: string;
  amountCents: number;
  currency: string;
  merchant: string;
  merchantCategoryCode?: string;
  scenarioId: string;
}

export interface IssuingSimulationResult extends IssuingSimulationResponse {
  scenarioId: string;
}

export interface IssuingSimulationDependencies {
  gateway: IssuingSimulationGateway;
  persistLifecycleId: (scenarioId: string, lifecycleId: string) => Promise<void>;
}

/** Validate a simulation and persist the lifecycle ID returned by Airwallex. */
export async function simulateIssuingTransaction(
  input: IssuingSimulationInput,
  dependencies: IssuingSimulationDependencies,
): Promise<IssuingSimulationResult> {
  const request = validateInput(input);
  let response: unknown;
  try {
    response = await dependencies.gateway.createIssuingSimulation(request);
  } catch (error) {
    if (!isAmbiguous(error)) throw error;
    throw new AmbiguousIssuingResponseError(null, error);
  }

  let result: IssuingSimulationResponse;
  try {
    result = normalizeIssuingResponse(response);
  } catch (error) {
    throw new AmbiguousIssuingResponseError(null, error);
  }
  try {
    await dependencies.persistLifecycleId(request.scenarioId, result.cardTransactionLifecycleId);
  } catch (error) {
    throw new AmbiguousIssuingResponseError(result.cardTransactionLifecycleId, error);
  }
  return { ...result, scenarioId: request.scenarioId };
}

export class AmbiguousIssuingResponseError extends Error {
  constructor(readonly lifecycleId: string | null, cause: unknown) {
    super("Airwallex issuing result is unresolved; an operator must reconcile before retry", { cause });
    this.name = "AmbiguousIssuingResponseError";
  }
}

export function normalizeIssuingResponse(value: unknown, expectedLifecycleId?: string): IssuingSimulationResponse {
  if (typeof value !== "object" || value === null) {
    throw new Error("Airwallex issuing simulation returned a malformed response");
  }
  const response = value as Record<string, unknown>;
  const result = typeof response.process_result === "string" ? response.process_result.trim().toUpperCase() : "";
  if (result !== "APPROVED" && result !== "DECLINED") {
    throw new Error("Airwallex issuing simulation returned an unexpected process_result");
  }
  if (response.failure_reason !== undefined && response.failure_reason !== null && typeof response.failure_reason !== "string") {
    throw new Error("Airwallex issuing simulation returned a malformed failure_reason");
  }

  const cardTransactionEventId = requiredProviderId(response.card_transaction_event_id, "card_transaction_event_id");
  const cardTransactionId = requiredProviderId(response.card_transaction_id, "card_transaction_id");
  const cardTransactionLifecycleId = requiredProviderId(response.card_transaction_lifecycle_id, "card_transaction_lifecycle_id");
  if (expectedLifecycleId && cardTransactionLifecycleId !== expectedLifecycleId) {
    throw new Error("Airwallex issuing simulation returned a different lifecycle ID");
  }

  const transactionType = response.transaction_type;
  if (transactionType !== undefined && transactionType !== "AUTHORIZATION" && transactionType !== "CLEARING") {
    throw new Error("Airwallex issuing simulation returned a malformed transaction_type");
  }
  const transactionCategory = optionalProviderString(response.transaction_category, "transaction_category");
  const merchantCategoryCode = optionalProviderString(response.merchant_category_code, "merchant_category_code");
  const accountId = optionalProviderString(response.account_id, "account_id");
  const cardId = optionalProviderString(response.card_id, "card_id");
  const transactionCurrency = optionalProviderString(response.transaction_currency, "transaction_currency");
  let transactionAmountCents: number | undefined;
  if (response.transaction_amount !== undefined && response.transaction_amount !== null) {
    if (typeof response.transaction_amount !== "number" || !Number.isFinite(response.transaction_amount) || response.transaction_amount <= 0) {
      throw new Error("Airwallex issuing simulation returned a malformed transaction_amount");
    }
    transactionAmountCents = Math.round(response.transaction_amount * 100);
    if (!Number.isSafeInteger(transactionAmountCents)) throw new Error("Airwallex issuing simulation returned an invalid transaction_amount");
  }
  return {
    processResult: result,
    failureReason: typeof response.failure_reason === "string" ? response.failure_reason : null,
    cardTransactionEventId,
    cardTransactionId,
    cardTransactionLifecycleId,
    ...(transactionType === undefined ? {} : { transactionType }),
    ...(transactionCategory === undefined ? {} : { transactionCategory }),
    ...(merchantCategoryCode === undefined ? {} : { merchantCategoryCode }),
    ...(accountId === undefined ? {} : { accountId }),
    ...(cardId === undefined ? {} : { cardId }),
    ...(transactionAmountCents === undefined ? {} : { transactionAmountCents }),
    ...(transactionCurrency === undefined ? {} : { transactionCurrency }),
  };
}

function optionalProviderString(value: unknown, label: string): string | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== "string" || !value.trim()) throw new Error(`Airwallex issuing simulation returned a malformed ${label}`);
  return value.trim();
}

function validateInput(input: IssuingSimulationInput): Omit<IssuingSimulationInput, "currency"> & { currency: "USD" } {
  const accountId = requiredInput(input.accountId, "account ID");
  const cardId = requiredInput(input.cardId, "card ID");
  const merchant = requiredInput(input.merchant, "merchant");
  const scenarioId = requiredInput(input.scenarioId, "scenario ID");
  const merchantCategoryCode = input.merchantCategoryCode?.trim();
  if (merchantCategoryCode && !/^\d{4}$/.test(merchantCategoryCode)) {
    throw new Error("Issuing simulation merchant category code must contain four digits");
  }
  if (!Number.isSafeInteger(input.amountCents) || input.amountCents < 1 || input.amountCents > MAX_SIMULATION_AMOUNT_CENTS) {
    throw new Error("Issuing simulation amount must be between 1 cent and 999999 cents");
  }
  const currency = requiredInput(input.currency, "currency").toUpperCase();
  if (currency !== "USD") throw new Error("Issuing simulation currency must be USD");
  return {
    accountId, cardId, amountCents: input.amountCents, currency, merchant, scenarioId,
    ...(merchantCategoryCode ? { merchantCategoryCode } : {}),
  };
}

function requiredInput(value: string, label: string): string {
  if (typeof value !== "string" || !value.trim()) throw new Error(`Issuing simulation ${label} is required`);
  return value.trim();
}

function requiredProviderId(value: unknown, label: string): string {
  if (typeof value !== "string" || !value.trim()) {
    throw new Error(`Airwallex issuing simulation response is missing ${label}`);
  }
  return value.trim();
}

function isAmbiguous(error: unknown): boolean {
  if (error instanceof ProviderHttpError) return error.status >= 500;
  if (error instanceof TypeError) return true;
  return error instanceof Error && (error.name === "TimeoutError" || error.name === "AbortError" || error.name === "SyntaxError");
}
