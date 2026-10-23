import { lookup } from "node:dns/promises";
import { isIP } from "node:net";
import { ProviderHttpError } from "./errors.js";

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

  constructor(private readonly options: {
    baseUrl: string;
    clientId: string;
    apiKey: string;
    fetcher?: typeof fetch;
    resolveHost?: (hostname: string) => Promise<string[]>;
  }) {}

  async createPlatformReport(input: { type: "TRANSACTION_RECON_REPORT"; fromDate: string; toDate: string }) {
    const data = await this.request<Record<string, unknown>>("/api/v1/platform_reports/create", { method: "POST", body: JSON.stringify({
      file_format: "CSV", type: input.type,
      from_created_at: `${input.fromDate}T00:00:00+0000`,
      to_created_at: `${input.toDate}T00:00:00+0000`,
    }) });
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
    let target: URL;
    try {
      target = new URL(url);
    } catch {
      throw new Error("Airwallex report download URL is invalid");
    }
    if (target.protocol !== "https:" || target.username || target.password || target.port && target.port !== "443" ||
        !target.hostname || isIP(target.hostname) || !isAllowedReportHost(target.hostname, this.options.baseUrl)) {
      throw new Error("Airwallex report download URL is not an allowed HTTPS host");
    }
    const addresses = await (this.options.resolveHost ?? resolvePublicAddresses)(target.hostname);
    if (addresses.length === 0 || addresses.some(isPrivateOrLocalAddress)) {
      throw new Error("Airwallex report download host resolves to a private or local address");
    }
    const response = await (this.options.fetcher ?? fetch)(target, { method: "GET", signal: AbortSignal.timeout(15_000), redirect: "error" });
    if (!response.ok) throw new ProviderHttpError(response.status, `Airwallex report download failed (${response.status})`);
    const csv = await response.text();
    return parseReportCsv(csv);
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
    if (typeof data.has_more !== "boolean") throw new Error("Airwallex RFI response returned an invalid page");
    return { items, hasMore: data.has_more };
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

function isAllowedReportHost(hostname: string, baseUrl: string): boolean {
  const host = hostname.toLowerCase();
  const apiHost = new URL(baseUrl).hostname.toLowerCase();
  return host === apiHost || host === "storage.googleapis.com" || host.endsWith(".airwallex.com") || host.endsWith(".amazonaws.com");
}

/** Parse provider CSV without losing quoted commas, escaped quotes, or embedded newlines. */
export function parseReportCsv(csv: string): Record<string, string>[] {
  if (csv.length > 100_000_000) throw new Error("Airwallex report exceeds the supported size");
  const records: string[][] = [];
  let row: string[] = [];
  let field = "";
  let quoted = false;
  let afterQuote = false;
  for (let index = 0; index < csv.length; index++) {
    const char = csv[index]!;
    if (quoted) {
      if (char === '"' && csv[index + 1] === '"') { field += '"'; index++; }
      else if (char === '"') { quoted = false; afterQuote = true; }
      else field += char;
      continue;
    }
    if (afterQuote && char !== ',' && char !== '\n' && char !== '\r') throw new Error("Airwallex report contains malformed CSV quoting");
    if (char === '"') {
      if (field.length) throw new Error("Airwallex report contains malformed CSV quoting");
      quoted = true;
    } else if (char === ',') { row.push(field); field = ""; afterQuote = false; }
    else if (char === '\n' || char === '\r') {
      row.push(field); records.push(row); row = []; field = "";
      afterQuote = false;
      if (char === '\r' && csv[index + 1] === '\n') index++;
    } else field += char;
  }
  if (quoted) throw new Error("Airwallex report contains an unterminated CSV field");
  if (field.length || row.length) { row.push(field); records.push(row); }
  while (records.length && records[records.length - 1]!.every((value) => value === "")) records.pop();
  if (!records.length) throw new Error("Airwallex report CSV is empty");
  const headers = records.shift()!.map((header) => header.replace(/^\uFEFF/, "").trim());
  if (headers.some((header) => !header) || new Set(headers).size !== headers.length) throw new Error("Airwallex report contains invalid CSV headers");
  return records.map((values) => {
    if (values.length !== headers.length) throw new Error("Airwallex report contains a malformed CSV row");
    return Object.fromEntries(headers.map((header, index) => [header, values[index]!]));
  });
}

async function resolvePublicAddresses(hostname: string): Promise<string[]> {
  const records = await lookup(hostname, { all: true, verbatim: true });
  return records.map(({ address }) => address);
}

function isPrivateOrLocalAddress(address: string): boolean {
  const version = isIP(address);
  if (version === 4) {
    const octets = address.split(".").map(Number);
    const [first = 0, second = 0] = octets;
    return first === 0 || first === 10 || first === 127 || first >= 224 ||
      first === 169 && second === 254 || first === 172 && second >= 16 && second <= 31 ||
      first === 192 && (second === 168 || second === 0) || first === 100 && second >= 64 && second <= 127;
  }
  if (version === 6) {
    const normalized = address.toLowerCase();
    return normalized === "::" || normalized === "::1" || normalized.startsWith("fc") ||
      normalized.startsWith("fd") || normalized.startsWith("fe8") || normalized.startsWith("fe9") ||
      normalized.startsWith("fea") || normalized.startsWith("feb") || normalized.startsWith("::ffff:");
  }
  return true;
}

function stringField(data: Record<string, unknown>, ...keys: string[]): string | null {
  for (const key of keys) if (typeof data[key] === "string" && (data[key] as string).trim()) return (data[key] as string).trim();
  return null;
}
