import { providerHttpErrorDetails, ProviderHttpError, type ProviderHttpErrorPhase } from "./errors.js";

async function readProviderHttpErrorDetails(response: Response, phase: ProviderHttpErrorPhase) {
  try {
    return providerHttpErrorDetails(await response.clone().json() as unknown, phase);
  } catch {
    return { phase };
  }
}

export interface ProviderConnectedAccount {
  id: string;
  status: string;
}

/** Retained account directory for roster reconciliation. */
export interface AirwallexGateway {
  listConnectedAccounts(): Promise<ProviderConnectedAccount[]>;
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

  private async get<T>(path: string): Promise<T> {
    const token = await this.getAccessToken();
    const response = await (this.options.fetcher ?? fetch)(new URL(path, this.options.baseUrl), {
      method: "GET",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(15_000),
    });
    if (!response.ok) {
      throw new ProviderHttpError(
        response.status,
        `Airwallex request failed (${response.status})`,
        await readProviderHttpErrorDetails(response, "provider_request"),
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
