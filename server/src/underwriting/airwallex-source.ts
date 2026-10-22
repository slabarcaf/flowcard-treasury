import type { AirwallexUnderwritingGateway } from "../airwallex/client.js";

export interface LedgerTransaction {
  id: string;
  accountId: string;
  occurredAt: Date;
  amountCents: number;
  currency: string;
  kind: "credit" | "debit";
  status: string;
  sourceType: string;
  payerKey: string | null;
  captured: boolean;
}

export interface BalancePoint { accountId: string; postedAt: Date; balanceCents: number; currency: string; }
export interface AccountStanding { status: "active" | "frozen" | "closed" | "unknown"; openRfi: boolean | null; }

export interface ProfileSourceData {
  transactions: LedgerTransaction[];
  balances: BalancePoint[];
  standings: Map<string, AccountStanding>;
  queryPages: { transactions: number; balances: number; account: number; expectedInflows: number };
  sourceVersions: { transactions: string; balances: string; account: string; expected_inflows: string; repayments: string };
}

const MAX_RETRIES = 3;
const MAX_REPORT_POLLS = 20;
const DAY_MS = 86_400_000;

export async function withBoundedRetries<T>(operation: () => Promise<T>, retries = MAX_RETRIES): Promise<T> {
  if (!Number.isSafeInteger(retries) || retries < 1 || retries > 5) throw new Error("Retry count must be from 1 through 5");
  let lastError: unknown;
  for (let attempt = 0; attempt < retries; attempt++) {
    try { return await operation(); } catch (error) { lastError = error; }
  }
  throw safeSourceError(lastError);
}

/** Whole-book report plus account-scoped reads. Every account ID comes from the explicit roster. */
export async function loadAirwallexProfileSources(
  gateway: AirwallexUnderwritingGateway,
  roster: ReadonlyArray<{ customerId: string; accountId: string }>,
  windowStart: Date,
  windowEnd: Date,
): Promise<ProfileSourceData> {
  const start = windowStart.toISOString().slice(0, 10);
  const end = windowEnd.toISOString().slice(0, 10);
  const report = await withBoundedRetries(() => gateway.createPlatformReport({ type: "TRANSACTION_RECON_REPORT", fromDate: start, toDate: end }));
  const reportRows = await fetchCompletedReport(gateway, report.reportId);
  const rosterAccountIds = new Set(roster.map((entry) => entry.accountId));
  const transactions = reportRows
    .filter((row) => isRecord(row) && typeof row.account_id === "string" && rosterAccountIds.has(row.account_id))
    .map(parseTransaction)
    .filter((row): row is LedgerTransaction => row !== null);

  const balances: BalancePoint[] = [];
  const standings = new Map<string, AccountStanding>();
  let balancePages = 0;
  for (const entry of roster) {
    const account = await withBoundedRetries(() => gateway.getConnectedAccount(entry.accountId));
    const status = normalizeAccountStatus(account.status);
    const rfiRows: unknown[] = [];
    let rfiNumber = 0;
    let rfiPage = await withBoundedRetries(() => gateway.getOpenRfis(entry.accountId, rfiNumber));
    rfiRows.push(...rfiPage.items);
    while (rfiPage.hasMore) {
      rfiNumber++;
      if (rfiNumber > 100) throw new Error("Airwallex RFI pagination exceeded the supported page range");
      rfiPage = await withBoundedRetries(() => gateway.getOpenRfis(entry.accountId, rfiNumber));
      rfiRows.push(...rfiPage.items);
    }
    const openRfi = rfiRows.some(isOpenPolicyRfi);
    standings.set(entry.customerId, { status, openRfi });

    for (let from = windowStart.getTime(); from < windowEnd.getTime(); from += 7 * DAY_MS) {
      const to = Math.min(from + 7 * DAY_MS, windowEnd.getTime());
      const fromDate = new Date(from).toISOString().slice(0, 10);
      const toDate = new Date(to - 1).toISOString().slice(0, 10);
      let page = 0;
      let hasMore = true;
      while (hasMore) {
        const result = await withBoundedRetries(() => gateway.getBalanceHistory(entry.accountId, fromDate, toDate, page));
        for (const raw of result.items) {
          const point = parseBalancePoint(raw, entry.accountId);
          if (point) balances.push(point);
        }
        hasMore = result.hasMore;
        balancePages++;
        if (hasMore) {
          page++;
          if (page > 100) throw new Error("Airwallex balance-history pagination exceeded the supported page range");
        }
      }
    }
  }
  return {
    transactions, balances, standings,
    queryPages: { transactions: 1, balances: Math.max(balancePages, 1), account: Math.max(roster.length, 1), expectedInflows: 1 },
    sourceVersions: { transactions: "TRANSACTION_RECON_REPORT:v1", balances: "balances-history:v1", account: "accounts-rfi:v1", expected_inflows: "expected-inflows-unavailable:v1", repayments: "internal-ledger:v1" },
  };
}

async function fetchCompletedReport(gateway: AirwallexUnderwritingGateway, reportId: string): Promise<unknown[]> {
  for (let attempt = 0; attempt < MAX_REPORT_POLLS; attempt++) {
    const report = await withBoundedRetries(() => gateway.getPlatformReport(reportId));
    if (report.status === "failed") throw new Error("Airwallex underwriting report failed");
    if (report.status === "completed") {
      if (!report.downloadUrl) throw new Error("Completed Airwallex report has no download URL");
      // Download immediately: the provider URL expires shortly after completion.
      return withBoundedRetries(() => gateway.downloadPlatformReport(report.downloadUrl!));
    }
  }
  throw new Error("Airwallex underwriting report did not complete within the bounded poll window");
}

function parseTransaction(value: unknown): LedgerTransaction | null {
  if (!isRecord(value)) throw new Error("Airwallex transaction report contains a malformed row");
  const accountId = requiredString(value, "account_id");
  const id = requiredString(value, "transaction_id", "id");
  const timestamp = requiredString(value, "settled_at", "posted_at", "transaction_date");
  const occurredAt = new Date(timestamp);
  const amount = numberField(value, "amount", "net");
  const currency = requiredString(value, "currency").toUpperCase();
  const status = requiredString(value, "status").toLowerCase();
  const sourceType = requiredString(value, "source_type", "transaction_type").toLowerCase();
  const direction = String(value.direction ?? value.type ?? "").toLowerCase();
  if (!accountId || !id || !Number.isFinite(occurredAt.getTime()) || amount === null || !currency || !status || !sourceType) throw new Error("Airwallex transaction report contains an incomplete row");
  const kind = direction.includes("credit") || direction === "inbound" ? "credit" : direction.includes("debit") || direction === "outbound" ? "debit" : amount >= 0 ? "credit" : "debit";
  const cents = Math.round(Math.abs(amount) * 100);
  if (!Number.isSafeInteger(cents)) throw new Error("Airwallex transaction report contains an invalid amount");
  const payer = typeof value.payer_id === "string" ? value.payer_id.trim() : typeof value.payer_name === "string" ? normalizePayerKey(value.payer_name) : null;
  return { id, accountId, occurredAt, amountCents: cents, currency, kind, status, sourceType, payerKey: payer || null, captured: /capture|clearing/.test(sourceType) || value.capture_status === "CAPTURED" };
}

function parseBalancePoint(value: unknown, accountId: string): BalancePoint | null {
  if (!isRecord(value)) throw new Error("Airwallex balance-history contains a malformed row");
  const postedAt = new Date(requiredString(value, "posted_at"));
  const balance = numberField(value, "balance");
  const currency = requiredString(value, "currency").toUpperCase();
  if (!Number.isFinite(postedAt.getTime()) || balance === null || !currency) throw new Error("Airwallex balance-history contains an incomplete row");
  const balanceCents = Math.round(balance * 100);
  if (!Number.isSafeInteger(balanceCents)) throw new Error("Airwallex balance-history contains an invalid balance");
  return { accountId, postedAt, balanceCents, currency };
}

function normalizeAccountStatus(value: string): AccountStanding["status"] {
  switch (value.toLowerCase()) { case "active": return "active"; case "frozen": case "suspended": return "frozen"; case "closed": case "offboarded": return "closed"; default: return "unknown"; }
}
function isOpenPolicyRfi(value: unknown): boolean {
  if (!isRecord(value)) throw new Error("Airwallex RFI response contains a malformed row");
  const type = String(value.type ?? "").toLowerCase();
  const status = String(value.status ?? "").toLowerCase();
  return (type === "kyc" || type === "transaction") && !["resolved", "closed", "completed", "cancelled"].includes(status);
}
function normalizePayerKey(value: string): string { return value.trim().toLocaleLowerCase("en-US").replace(/\s+/g, " "); }
function isRecord(value: unknown): value is Record<string, unknown> { return typeof value === "object" && value !== null && !Array.isArray(value); }
function requiredString(value: Record<string, unknown>, ...keys: string[]): string { for (const key of keys) if (typeof value[key] === "string" && (value[key] as string).trim()) return (value[key] as string).trim(); return ""; }
function numberField(value: Record<string, unknown>, ...keys: string[]): number | null { for (const key of keys) if (typeof value[key] === "number" && Number.isFinite(value[key])) return value[key] as number; return null; }
function safeSourceError(error: unknown): Error { return new Error(error instanceof Error ? error.message.replace(/https?:\/\/\S+/gi, "[redacted-url]").replace(/Bearer\s+\S+/gi, "Bearer [redacted]").slice(0, 300) : "Airwallex underwriting source failed"); }
