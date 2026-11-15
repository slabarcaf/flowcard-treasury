import { providerHttpErrorDetails, ProviderHttpError, type ProviderHttpErrorPhase } from "./errors.js";
import { AIRWALLEX_ISSUING_API_VERSION } from "../config.js";

async function readProviderHttpErrorDetails(response: Response, phase: ProviderHttpErrorPhase) {
  try {
    return providerHttpErrorDetails(await response.clone().json() as unknown, phase);
  } catch {
    return { phase };
  }
}

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

export interface ProviderConnectedAccount {
  id: string;
  status: string;
}

export interface IssuingSimulationGateway {
  createIssuingSimulation(request: IssuingSimulationRequest): Promise<unknown>;
  getIssuingLifecycle(lifecycleId: string, accountId: string): Promise<unknown | null>;
  getIssuingLifecycleEvents?(lifecycleId: string, accountId: string): Promise<unknown[] | null>;
}

/** Retained account directory for roster reconciliation. */
export interface AirwallexGateway {
  listConnectedAccounts(): Promise<ProviderConnectedAccount[]>;
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
      const phase = path === "/api/v1/simulation/issuing/create" ? "issuing_simulation" : "provider_request";
      throw new ProviderHttpError(
        response.status,
        `Airwallex request failed (${response.status})`,
        await readProviderHttpErrorDetails(response, phase),
      );
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
    if (!response.ok) {
      throw new ProviderHttpError(
        response.status,
        `Airwallex login failed (${response.status})`,
        await readProviderHttpErrorDetails(response, "authentication"),
      );
    }
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

export { ProviderHttpError };
