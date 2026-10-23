import { ProviderHttpError } from "./errors.js";

/** Typed boundary for server-side Airwallex calls. Tests inject a fake gateway. */
import { AIRWALLEX_ISSUING_API_VERSION } from "../config.js";

export type ProviderOperationKind = "transfer" | "charge";

export interface IssuingSimulationRequest {
  accountId: string;
  cardId: string;
  amountCents: number;
  currency: string;
  merchant: string;
  merchantCategoryCode?: string;
  /** Provider lifecycle ID to associate with the new authorization. */
  lifecycleId?: string;
}

export interface IssuingSimulationResponse {
  processResult: "APPROVED" | "DECLINED";
  failureReason: string | null;
  cardTransactionEventId: string;
  cardTransactionId: string;
  cardTransactionLifecycleId: string;
  /** Present only when Airwallex returns transaction facts with the simulation result. */
  transactionType?: "AUTHORIZATION" | "CLEARING";
  transactionCategory?: string;
  merchantName?: string;
  merchantCategoryCode?: string;
  accountId?: string;
  cardId?: string;
  transactionAmountCents?: number;
  transactionCurrency?: string;
}

export type ProviderStatus = "pending" | "suspended" | "settled" | "failed" | "unknown";

export interface ProviderOperationRequest {
  requestId: string;
  amountCents: number;
  currency: "USD";
  payload: Record<string, unknown>;
}

export interface ProviderOperationResponse {
  id: string;
  requestId?: string;
  status: ProviderStatus;
  rawStatus?: string;
  duplicate?: boolean;
}

export interface GlobalAccountDepositSimulationRequest {
  accountId: string;
  globalAccountId: string;
  amountCents: number;
  payerBankname?: string;
  payerCountry?: string;
  payerName?: string;
  reference?: string;
  statementRef?: string;
}

export type GlobalAccountDepositStatus = "pending" | "rejected" | "settled";

export interface GlobalAccountDepositSimulationResponse {
  id: string;
  amountCents: number;
  currency: string;
  depositType: "DEBIT" | "CREDIT";
  feeAmountCents: number;
  status: GlobalAccountDepositStatus;
  createTime: string;
}

export interface ProviderConnectedAccount {
  id: string;
  status: string;
}

export interface IssuingSimulationGateway {
  createIssuingSimulation(request: IssuingSimulationRequest): Promise<unknown>;
  getIssuingLifecycle(lifecycleId: string, accountId: string): Promise<unknown | null>;
  getIssuingLifecycleEvents?(lifecycleId: string, accountId: string): Promise<unknown[] | null>;
}

export interface AirwallexGateway {
  listConnectedAccounts(): Promise<ProviderConnectedAccount[]>;
  createTransfer(request: ProviderOperationRequest): Promise<ProviderOperationResponse>;
  createCharge(request: ProviderOperationRequest): Promise<ProviderOperationResponse>;
  getTransferByRequestId(requestId: string): Promise<ProviderOperationResponse | null>;
  getChargeByRequestId(requestId: string): Promise<ProviderOperationResponse | null>;
  getBalance(accountId: string, currency: "USD"): Promise<{ availableCents: number; currency: "USD" }>;
  getDeposit(depositId: string, accountId: string): Promise<{ id: string; amountCents: number; currency: "USD"; status: ProviderStatus; settledAt: Date | null }>;
  createGlobalAccountDepositSimulation(request: GlobalAccountDepositSimulationRequest): Promise<GlobalAccountDepositSimulationResponse>;
  retrySandboxCard(request: { requestId: string; accountId: string; cardId: string; amountCents: number; currency: "USD" }): Promise<ProviderOperationResponse>;
}

export { HttpAirwallexUnderwritingGateway } from "./underwriting-client.js";
export type { AirwallexUnderwritingGateway } from "./underwriting-client.js";

export function normalizeProviderStatus(value: unknown): ProviderStatus {
  switch (String(value ?? "").toLowerCase()) {
    case "pending": case "processing": case "pending_transfer": case "pending_charge": return "pending";
    case "suspended": case "requires_action": return "suspended";
    case "settled": case "completed": case "succeeded": return "settled";
    case "failed": case "cancelled": case "canceled": case "reversed": return "failed";
    default: return "unknown";
  }
}

/** Fetch-backed gateway. Constructing it is side-effect free; calls happen only when dispatched. */
export class HttpAirwallexGateway implements AirwallexGateway, IssuingSimulationGateway {
  private accessToken: string | undefined;
  private tokenExpiresAt = 0;

  constructor(private readonly options: { baseUrl: string; clientId: string; apiKey: string; fetcher?: typeof fetch }) {}

  async listConnectedAccounts(): Promise<ProviderConnectedAccount[]> {
    const pageSize = 500;
    const accounts: ProviderConnectedAccount[] = [];
    let pageNum = 0;
    let hasMore = true;
    while (hasMore) {
      const result = await this.get<unknown>(`/api/v1/accounts?page_num=${pageNum}&page_size=${pageSize}`);
      if (!isConnectedAccountPage(result)) {
        throw new Error("Airwallex connected-account list returned an invalid page");
      }
      for (const item of result.items) {
        if (typeof item !== "object" || item === null) {
          throw new Error("Airwallex connected-account list returned a malformed account item");
        }
        const account = item as Record<string, unknown>;
        const id = typeof account.id === "string" ? account.id.trim() : "";
        const status = typeof account.status === "string" ? account.status.trim() : "";
        if (!id || !status) {
          throw new Error("Airwallex connected-account list returned an item without an ID or status");
        }
        accounts.push({ id, status });
      }
      hasMore = result.has_more;
      if (hasMore && result.items.length === 0) {
        throw new Error("Airwallex connected-account pagination returned an empty page with more results");
      }
      pageNum++;
      if (!Number.isSafeInteger(pageNum)) {
        throw new Error("Airwallex connected-account pagination exceeded the supported page range");
      }
    }
    return accounts;
  }

  createTransfer(request: ProviderOperationRequest) { return this.postOperation("/api/v1/connected_account_transfers/create", request); }
  createCharge(request: ProviderOperationRequest) { return this.postOperation("/api/v1/charges/create", request); }
  getTransferByRequestId(requestId: string) { return this.lookup("/api/v1/connected_account_transfers", requestId); }
  getChargeByRequestId(requestId: string) { return this.lookup("/api/v1/charges", requestId); }
  async getBalance(accountId: string, currency: "USD") {
    const result = await this.get<Array<{ account_type?: string; available_amount?: number; currency?: string }>>(
      "/api/v1/balances/current?account_type=cash",
      { "x-on-behalf-of": accountId },
    );
    if (!Array.isArray(result)) throw new Error("Airwallex current balances response was not a list");
    const balance = result.find((entry) => entry.currency === currency &&
      (!entry.account_type || entry.account_type.toLowerCase() === "cash"));
    const availableCents = balance && typeof balance.available_amount === "number"
      ? Math.round(balance.available_amount * 100)
      : Number.NaN;
    if (!Number.isSafeInteger(availableCents) || availableCents < 0) {
      throw new Error(`Airwallex did not return a valid available ${currency} cash balance`);
    }
    return { availableCents, currency };
  }
  async getDeposit(depositId: string, accountId: string) {
    const result = await this.get<{ id: string; amount: number; currency: "USD"; status: string; settled_at?: string | null }>(`/api/v1/deposits/${encodeURIComponent(depositId)}`, { "x-on-behalf-of": accountId });
    return {
      id: result.id,
      amountCents: Math.round(result.amount * 100),
      currency: result.currency,
      status: normalizeProviderStatus(result.status),
      settledAt: result.settled_at ? new Date(result.settled_at) : null,
    };
  }
  async createGlobalAccountDepositSimulation(request: GlobalAccountDepositSimulationRequest): Promise<GlobalAccountDepositSimulationResponse> {
    validateGlobalAccountDepositSimulationRequest(request);
    const response = await this.request<unknown>("/api/v1/simulation/deposit/create", {
      method: "POST",
      body: JSON.stringify({
        global_account_id: request.globalAccountId.trim(),
        amount: request.amountCents / 100,
        ...(request.payerBankname ? { payer_bankname: request.payerBankname.trim() } : {}),
        ...(request.payerCountry ? { payer_country: request.payerCountry.trim().toUpperCase() } : {}),
        ...(request.payerName ? { payer_name: request.payerName.trim() } : {}),
        ...(request.reference ? { reference: request.reference.trim() } : {}),
        ...(request.statementRef ? { statement_ref: request.statementRef.trim() } : {}),
        status: "SETTLED",
      }),
    }, { "x-on-behalf-of": request.accountId.trim() });
    return normalizeGlobalAccountDepositSimulationResponse(response);
  }
  retrySandboxCard(request: { requestId: string; accountId: string; cardId: string; amountCents: number; currency: "USD" }) {
    return this.postOperation("/api/v1/simulation/issuing/create", { ...request, payload: { card_id: request.cardId } }, { "x-on-behalf-of": request.accountId });
  }

  createIssuingSimulation(request: IssuingSimulationRequest): Promise<unknown> {
    return this.request("/api/v1/simulation/issuing/create", {
      method: "POST",
      body: JSON.stringify({
        card_id: request.cardId,
        transaction_amount: request.amountCents / 100,
        transaction_currency: request.currency,
        ...(request.merchantCategoryCode ? { merchant_category_code: request.merchantCategoryCode } : {}),
        merchant_info: request.merchant.trim().padEnd(43, " ").slice(0, 43),
        ...(request.lifecycleId ? { card_transaction_lifecycle_id: request.lifecycleId } : {}),
      }),
    }, { "x-on-behalf-of": request.accountId, "x-api-version": AIRWALLEX_ISSUING_API_VERSION });
  }

  getIssuingLifecycle(lifecycleId: string, accountId: string): Promise<unknown | null> {
    return this.get(`/api/v1/issuing/lifecycles/${encodeURIComponent(lifecycleId)}`, { "x-on-behalf-of": accountId })
      .catch((error: unknown) => {
        if (error instanceof ProviderHttpError && error.status === 404) return null;
        throw error;
      });
  }

  async getIssuingLifecycleEvents(lifecycleId: string, accountId: string): Promise<unknown[] | null> {
    const events: unknown[] = [];
    const seenPages = new Set<string>();
    let page: string | null = null;
    try {
      while (true) {
        const query = new URLSearchParams({ lifecycle_id: lifecycleId, page_size: "100" });
        if (page) query.set("page", page);
        const result = await this.get<unknown>(`/api/v1/issuing/card_transaction_events?${query.toString()}`, {
          "x-on-behalf-of": accountId,
          "x-api-version": AIRWALLEX_ISSUING_API_VERSION,
        });
        if (typeof result !== "object" || result === null || Array.isArray(result) ||
            !Array.isArray((result as Record<string, unknown>).items)) {
          throw new Error("Airwallex issuing transaction event list returned a malformed response");
        }
        const response = result as Record<string, unknown>;
        for (const item of response.items as unknown[]) {
          if (typeof item !== "object" || item === null || Array.isArray(item)) {
            throw new Error("Airwallex issuing transaction event list returned a malformed event");
          }
          events.push(item);
        }

        const nextPage = typeof response.page_after === "string" && response.page_after.trim()
          ? response.page_after.trim()
          : null;
        if (response.has_more === true && nextPage === null) {
          throw new Error("Airwallex issuing transaction event pagination omitted its next page marker");
        }
        if (response.has_more === false || nextPage === null) return events;
        if (seenPages.has(nextPage)) {
          throw new Error("Airwallex issuing transaction event pagination repeated a page marker");
        }
        seenPages.add(nextPage);
        page = nextPage;
      }
    } catch (error) {
      if (error instanceof ProviderHttpError && error.status === 404) return null;
      throw error;
    }
  }

  private async postOperation(path: string, request: ProviderOperationRequest, headers: Record<string, string> = {}): Promise<ProviderOperationResponse> {
    const body = { ...request.payload, request_id: request.requestId, amount: request.amountCents / 100, currency: request.currency };
    const data = await this.request<Record<string, unknown>>(path, { method: "POST", body: JSON.stringify(body) }, headers);
    return parseOperation(data, request.requestId);
  }
  private async lookup(path: string, requestId: string): Promise<ProviderOperationResponse | null> {
    try {
      const data = await this.get<Record<string, unknown>>(`${path}?request_id=${encodeURIComponent(requestId)}`);
      return parseOperation(data, requestId);
    } catch (error) {
      if (error instanceof ProviderHttpError && error.status === 404) return null;
      throw error;
    }
  }
  private get<T>(path: string, headers: Record<string, string> = {}) { return this.request<T>(path, { method: "GET" }, headers); }
  private async request<T>(path: string, init: RequestInit, extraHeaders: Record<string, string> = {}): Promise<T> {
    const token = await this.getAccessToken();
    const response = await (this.options.fetcher ?? fetch)(new URL(path, this.options.baseUrl), {
      ...init,
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}`, ...extraHeaders, ...init.headers },
      signal: AbortSignal.timeout(15_000),
    });
    if (!response.ok) {
      if (response.status === 409 && init.method === "POST") return await response.json() as T;
      throw new ProviderHttpError(response.status, `Airwallex request failed (${response.status})`);
    }
    return await response.json() as T;
  }

  private async getAccessToken(): Promise<string> {
    if (this.accessToken && Date.now() < this.tokenExpiresAt - 30_000) return this.accessToken;
    const response = await (this.options.fetcher ?? fetch)(new URL("/api/v1/authentication/login", this.options.baseUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", "x-client-id": this.options.clientId, "x-api-key": this.options.apiKey },
      body: JSON.stringify({}),
      signal: AbortSignal.timeout(15_000),
    });
    if (!response.ok) throw new ProviderHttpError(response.status, `Airwallex login failed (${response.status})`);
    const data = await response.json() as { token?: string; expires_at?: string; expires_in?: number };
    if (!data.token) throw new Error("Airwallex login response did not contain a token");
    this.accessToken = data.token;
    this.tokenExpiresAt = data.expires_at ? Date.parse(data.expires_at) : Date.now() + (data.expires_in ?? 1800) * 1000;
    return data.token;
  }
}

function validateGlobalAccountDepositSimulationRequest(request: GlobalAccountDepositSimulationRequest): void {
  for (const [value, label] of [
    [request.accountId, "connected account ID"],
    [request.globalAccountId, "global account ID"],
  ] as const) {
    if (typeof value !== "string" || !value.trim()) {
      throw new Error(`Global Account deposit simulation ${label} is required`);
    }
  }
  if (!Number.isSafeInteger(request.amountCents) || request.amountCents <= 0) {
    throw new Error("Global Account deposit simulation amount must be a positive safe integer in cents");
  }
  if (request.payerCountry && !/^[A-Za-z]{2}$/.test(request.payerCountry.trim())) {
    throw new Error("Global Account deposit simulation payer country must be a two-letter code");
  }
  for (const [value, label] of [
    [request.payerBankname, "payer bank name"],
    [request.payerName, "payer name"],
    [request.reference, "reference"],
    [request.statementRef, "statement reference"],
  ] as const) {
    if (value !== undefined && (typeof value !== "string" || !value.trim())) {
      throw new Error(`Global Account deposit simulation ${label} cannot be empty`);
    }
  }
}

function normalizeGlobalAccountDepositSimulationResponse(value: unknown): GlobalAccountDepositSimulationResponse {
  if (typeof value !== "object" || value === null) {
    throw new Error("Airwallex Global Account deposit simulation returned a malformed response");
  }
  const response = value as Record<string, unknown>;
  const id = typeof response.id === "string" ? response.id.trim() : "";
  const currency = typeof response.currency === "string" ? response.currency.trim().toUpperCase() : "";
  const depositType = typeof response.deposit_type === "string" ? response.deposit_type.trim().toUpperCase() : "";
  const status = typeof response.status === "string" ? response.status.trim().toLowerCase() : "";
  const createTime = typeof response.create_time === "string" ? response.create_time.trim() : "";
  if (!id || !/^[A-Z]{3}$/.test(currency) || (depositType !== "DEBIT" && depositType !== "CREDIT") ||
      (status !== "pending" && status !== "rejected" && status !== "settled") || !createTime) {
    throw new Error("Airwallex Global Account deposit simulation returned incomplete or invalid transaction details");
  }
  const amountCents = providerAmountToCents(response.amount, "amount");
  const feeAmountCents = providerAmountToCents(response.fee_amount, "fee amount", true);
  return { id, amountCents, currency, depositType, feeAmountCents, status, createTime };
}

function providerAmountToCents(value: unknown, label: string, allowZero = false): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || (!allowZero && value === 0)) {
    throw new Error(`Airwallex Global Account deposit simulation returned an invalid ${label}`);
  }
  const cents = Math.round(value * 100);
  if (!Number.isSafeInteger(cents) || Math.abs(value * 100 - cents) > 1e-6) {
    throw new Error(`Airwallex Global Account deposit simulation returned an invalid ${label}`);
  }
  return cents;
}

interface ConnectedAccountPage {
  has_more: boolean;
  items: unknown[];
}

function isConnectedAccountPage(value: unknown): value is ConnectedAccountPage {
  if (typeof value !== "object" || value === null) return false;
  const page = value as Record<string, unknown>;
  return typeof page.has_more === "boolean" && Array.isArray(page.items);
}

function parseOperation(value: Record<string, unknown>, requestId: string): ProviderOperationResponse {
  const id = String(value.id ?? value.transfer_id ?? value.charge_id ?? "");
  if (!id) throw new Error("Airwallex response did not contain an operation ID");
  const returnedRequestId = String(value.request_id ?? requestId);
  return { id, requestId: returnedRequestId, status: normalizeProviderStatus(value.status), rawStatus: String(value.status ?? ""), duplicate: Boolean(value.duplicate) };
}

export { ProviderHttpError } from "./errors.js";
