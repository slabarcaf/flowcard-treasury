/** Typed boundary for server-side Airwallex calls. Tests inject a fake gateway. */
export type ProviderOperationKind = "transfer" | "charge";

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

export interface ProviderConnectedAccount {
  id: string;
  status: string;
}

export interface AirwallexGateway {
  listConnectedAccounts(): Promise<ProviderConnectedAccount[]>;
  createTransfer(request: ProviderOperationRequest): Promise<ProviderOperationResponse>;
  createCharge(request: ProviderOperationRequest): Promise<ProviderOperationResponse>;
  getTransferByRequestId(requestId: string): Promise<ProviderOperationResponse | null>;
  getChargeByRequestId(requestId: string): Promise<ProviderOperationResponse | null>;
  getBalance(accountId: string, currency: "USD"): Promise<{ availableCents: number; currency: "USD" }>;
  getDeposit(depositId: string, accountId: string): Promise<{ id: string; amountCents: number; currency: "USD"; status: ProviderStatus; settledAt: Date | null }>;
  retrySandboxCard(request: { requestId: string; accountId: string; cardId: string; amountCents: number; currency: "USD" }): Promise<ProviderOperationResponse>;
}

/** Read-only underwriting endpoints. These are kept separate from operation APIs. */
export interface AirwallexUnderwritingGateway {
  createPlatformReport(input: { type: "TRANSACTION_RECON_REPORT"; fromDate: string; toDate: string }): Promise<{ reportId: string }>;
  getPlatformReport(reportId: string): Promise<{ status: "pending" | "completed" | "failed"; downloadUrl?: string }>;
  downloadPlatformReport(url: string): Promise<unknown[]>;
  getConnectedAccount(accountId: string): Promise<{ status: string }>;
  getOpenRfis(accountId: string, page: number): Promise<{ items: unknown[]; hasMore: boolean }>;
  getBalanceHistory(accountId: string, fromDate: string, toDate: string, page: number): Promise<{ items: unknown[]; hasMore: boolean }>;
}

/** HTTP implementation for offline-injected REST reads; never uses MCP. */
export class HttpAirwallexUnderwritingGateway implements AirwallexUnderwritingGateway {
  private accessToken: string | undefined;
  private tokenExpiresAt = 0;

  constructor(private readonly options: { baseUrl: string; clientId: string; apiKey: string; fetcher?: typeof fetch }) {}

  async createPlatformReport(input: { type: "TRANSACTION_RECON_REPORT"; fromDate: string; toDate: string }) {
    const data = await this.request<Record<string, unknown>>("/api/v1/platform_reports/create", { method: "POST", body: JSON.stringify({ report_type: input.type, from_date: input.fromDate, to_date: input.toDate }) });
    const reportId = stringField(data, "report_id", "id");
    if (!reportId) throw new Error("Airwallex report creation returned an invalid response");
    return { reportId };
  }

  async getPlatformReport(reportId: string) {
    const data = await this.get<Record<string, unknown>>(`/api/v1/platform_reports/${encodeURIComponent(reportId)}`);
    const status = String(data.status ?? "").toLowerCase();
    if (status === "failed" || status === "error") return { status: "failed" as const };
    if (status === "completed" || status === "succeeded") {
      const downloadUrl = stringField(data, "download_url", "url");
      return { status: "completed" as const, ...(downloadUrl ? { downloadUrl } : {}) };
    }
    return { status: "pending" as const };
  }

  async downloadPlatformReport(url: string): Promise<unknown[]> {
    const response = await (this.options.fetcher ?? fetch)(new URL(url), { method: "GET", signal: AbortSignal.timeout(15_000) });
    if (!response.ok) throw new ProviderHttpError(response.status, `Airwallex report download failed (${response.status})`);
    const data: unknown = await response.json();
    if (Array.isArray(data)) return data;
    if (typeof data === "object" && data !== null && Array.isArray((data as Record<string, unknown>).items)) return (data as { items: unknown[] }).items;
    throw new Error("Airwallex report download returned an invalid payload");
  }

  async getConnectedAccount(accountId: string) {
    const data = await this.get<Record<string, unknown>>(`/api/v1/accounts/${encodeURIComponent(accountId)}`, { "x-on-behalf-of": accountId });
    const status = typeof data.status === "string" ? data.status : "";
    if (!status) throw new Error("Airwallex account response did not contain a status");
    return { status };
  }

  async getOpenRfis(accountId: string, page: number) {
    const params = new URLSearchParams({ page_num: String(page), page_size: "500" });
    const data = await this.get<Record<string, unknown>>(`/api/v1/rfis?${params}`, { "x-on-behalf-of": accountId });
    const items = Array.isArray(data.items) ? data.items : [];
    if (!Array.isArray(data.items)) throw new Error("Airwallex RFI response returned an invalid page");
    return { items, hasMore: data.has_more === true };
  }

  async getBalanceHistory(accountId: string, fromDate: string, toDate: string, page: number) {
    const params = new URLSearchParams({ from_date: fromDate, to_date: toDate, page_num: String(page), page_size: "500" });
    const data = await this.get<Record<string, unknown>>(`/api/v1/balances/history?${params}`, { "x-on-behalf-of": accountId });
    if (!Array.isArray(data.items) || typeof data.has_more !== "boolean") throw new Error("Airwallex balance-history response returned an invalid page");
    return { items: data.items, hasMore: data.has_more };
  }

  private get<T>(path: string, headers: Record<string, string> = {}) { return this.request<T>(path, { method: "GET" }, headers); }
  private async request<T>(path: string, init: RequestInit, extraHeaders: Record<string, string> = {}): Promise<T> {
    const token = await this.getAccessToken();
    const response = await (this.options.fetcher ?? fetch)(new URL(path, this.options.baseUrl), {
      ...init, headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}`, ...extraHeaders, ...init.headers }, signal: AbortSignal.timeout(15_000),
    });
    if (!response.ok) throw new ProviderHttpError(response.status, `Airwallex request failed (${response.status})`);
    return await response.json() as T;
  }
  private async getAccessToken(): Promise<string> {
    if (this.accessToken && Date.now() < this.tokenExpiresAt - 30_000) return this.accessToken;
    const response = await (this.options.fetcher ?? fetch)(new URL("/api/v1/authentication/login", this.options.baseUrl), {
      method: "POST", headers: { "Content-Type": "application/json", "x-client-id": this.options.clientId, "x-api-key": this.options.apiKey }, body: JSON.stringify({}), signal: AbortSignal.timeout(15_000),
    });
    if (!response.ok) throw new ProviderHttpError(response.status, `Airwallex login failed (${response.status})`);
    const data = await response.json() as { token?: string; expires_at?: string; expires_in?: number };
    if (!data.token) throw new Error("Airwallex login response did not contain a token");
    this.accessToken = data.token;
    this.tokenExpiresAt = data.expires_at ? Date.parse(data.expires_at) : Date.now() + (data.expires_in ?? 1800) * 1000;
    return data.token;
  }
}

function stringField(data: Record<string, unknown>, ...keys: string[]): string | null {
  for (const key of keys) if (typeof data[key] === "string" && (data[key] as string).trim()) return (data[key] as string).trim();
  return null;
}

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
export class HttpAirwallexGateway implements AirwallexGateway {
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
    const result = await this.get<{ available_amount: number; currency: "USD" }>(`/api/v1/balances/current?currency=${currency}`, { "x-on-behalf-of": accountId });
    return { availableCents: Math.round(result.available_amount * 100), currency };
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
  retrySandboxCard(request: { requestId: string; accountId: string; cardId: string; amountCents: number; currency: "USD" }) {
    return this.postOperation("/api/v1/simulation/issuing/create", { ...request, payload: { card_id: request.cardId } }, { "x-on-behalf-of": request.accountId });
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

interface ConnectedAccountPage {
  has_more: boolean;
  items: unknown[];
}

function isConnectedAccountPage(value: unknown): value is ConnectedAccountPage {
  if (typeof value !== "object" || value === null) return false;
  const page = value as Record<string, unknown>;
  return typeof page.has_more === "boolean" && Array.isArray(page.items);
}

export class ProviderHttpError extends Error {
  constructor(readonly status: number, message: string) { super(message); this.name = "ProviderHttpError"; }
}

function parseOperation(value: Record<string, unknown>, requestId: string): ProviderOperationResponse {
  const id = String(value.id ?? value.transfer_id ?? value.charge_id ?? "");
  if (!id) throw new Error("Airwallex response did not contain an operation ID");
  const returnedRequestId = String(value.request_id ?? requestId);
  return { id, requestId: returnedRequestId, status: normalizeProviderStatus(value.status), rawStatus: String(value.status ?? ""), duplicate: Boolean(value.duplicate) };
}
