import { SALESFORCE_QUERIES, type SalesforceQueryName } from "./queries.js";
import type { SalesforceMcpConfig } from "./mcp-config.js";
import type { CustomerRosterEntry } from "../roster/types.js";

/** Request shape presented to the host's already-configured MCP query tool. */
export interface SalesforceMcpReadRequest {
  object: "Account" | "Contract" | "Case" | "Task";
  fields: readonly string[];
  accountIdField: "Id" | "AccountId" | "WhatId";
  accountId: string;
  filter: "all" | "risk" | "call";
  cursor: string | null;
}

export interface SalesforceMcpPage {
  records: Record<string, unknown>[];
  nextCursor: string | null;
  complete: boolean;
}

/** Implemented by the host's MCP runtime; deliberately offers no REST operation. */
export interface SalesforceMcpClient {
  read(request: SalesforceMcpReadRequest, options: { serverName: string; toolName: string }): Promise<SalesforceMcpPage>;
}

export interface CompleteSalesforceQuery {
  records: Record<string, unknown>[];
  complete: true;
  pages: number;
}

export class SalesforceMcpError extends Error {
  constructor(message: string, readonly code: "permission_denied" | "incomplete_page" | "invalid_response" | "transient" | "permanent") {
    super(message);
    this.name = "SalesforceMcpError";
  }
}

/** Read one fixed allowlisted query for one roster Account ID, exhausting MCP pagination. */
export async function readSalesforceQuery(
  client: SalesforceMcpClient,
  config: SalesforceMcpConfig,
  queryName: SalesforceQueryName,
  rosterEntry: Pick<CustomerRosterEntry, "salesforceAccountId">,
  sleep: (ms: number) => Promise<void> = delay,
): Promise<CompleteSalesforceQuery> {
  const salesforceAccountId = rosterEntry.salesforceAccountId;
  if (!salesforceAccountId.trim()) throw new Error("Salesforce Account ID is required");
  const definition = SALESFORCE_QUERIES[queryName];
  const records: Record<string, unknown>[] = [];
  const cursors = new Set<string>();
  let cursor: string | null = null;
  let pages = 0;
  while (true) {
    const page = await withRetries(
      () => client.read({
        object: definition.object,
        fields: definition.fields,
        accountIdField: definition.accountIdField,
        accountId: salesforceAccountId,
        filter: definition.filter,
        cursor,
      }, { serverName: config.serverName, toolName: config.queryToolName }),
      config,
      sleep,
    );
    validatePage(page);
    pages += 1;
    records.push(...page.records.map((record) => projectAllowlistedFields(record, definition.fields)));
    if (page.complete) return { records, complete: true, pages };
    if (!page.nextCursor) throw new SalesforceMcpError("MCP returned an incomplete page without a continuation cursor", "incomplete_page");
    if (cursors.has(page.nextCursor)) throw new SalesforceMcpError("MCP repeated a pagination cursor", "incomplete_page");
    cursors.add(page.nextCursor);
    cursor = page.nextCursor;
  }
}

export interface SalesforceMcpQueryResults {
  account: CompleteSalesforceQuery;
  contract: CompleteSalesforceQuery;
  case: CompleteSalesforceQuery;
  activity: CompleteSalesforceQuery;
}

/** Read all allowlisted record families in sequence for a single enrolled account. */
export async function readSalesforceAccount(
  client: SalesforceMcpClient,
  config: SalesforceMcpConfig,
  rosterEntry: Pick<CustomerRosterEntry, "salesforceAccountId">,
  sleep?: (ms: number) => Promise<void>,
): Promise<SalesforceMcpQueryResults> {
  return {
    account: await readSalesforceQuery(client, config, "account", rosterEntry, sleep),
    contract: await readSalesforceQuery(client, config, "contract", rosterEntry, sleep),
    case: await readSalesforceQuery(client, config, "case", rosterEntry, sleep),
    activity: await readSalesforceQuery(client, config, "activity", rosterEntry, sleep),
  };
}

function validatePage(page: SalesforceMcpPage): void {
  if (!page || !Array.isArray(page.records) || typeof page.complete !== "boolean") {
    throw new SalesforceMcpError("MCP returned an invalid page response", "invalid_response");
  }
  if (page.nextCursor !== null && typeof page.nextCursor !== "string") {
    throw new SalesforceMcpError("MCP returned an invalid pagination cursor", "invalid_response");
  }
  for (const record of page.records) {
    if (!record || typeof record !== "object" || Array.isArray(record)) {
      throw new SalesforceMcpError("MCP returned a non-record value", "invalid_response");
    }
  }
}

function projectAllowlistedFields(record: Record<string, unknown>, fields: readonly string[]): Record<string, unknown> {
  const projected: Record<string, unknown> = {};
  for (const field of fields) {
    if (Object.hasOwn(record, field)) projected[field] = record[field];
  }
  return projected;
}

async function withRetries<T>(operation: () => Promise<T>, config: SalesforceMcpConfig, sleep: (ms: number) => Promise<void>): Promise<T> {
  for (let attempt = 1; ; attempt += 1) {
    try {
      return await operation();
    } catch (error) {
      const normalized = normalizeError(error);
      if (normalized.code !== "transient" || attempt >= config.maxAttempts) throw normalized;
      await sleep(config.retryDelayMs * 2 ** (attempt - 1));
    }
  }
}

function normalizeError(error: unknown): SalesforceMcpError {
  if (error instanceof SalesforceMcpError) return error;
  const candidate = error as { code?: unknown; message?: unknown };
  const code = candidate?.code;
  const message = typeof candidate?.message === "string" ? candidate.message : "Salesforce MCP read failed";
  if (code === "PERMISSION_DENIED" || code === "permission_denied" || code === "UNAUTHORIZED") {
    return new SalesforceMcpError(message, "permission_denied");
  }
  if (code === "UNAVAILABLE" || code === "DEADLINE_EXCEEDED" || code === "TIMEOUT" || code === "transient") {
    return new SalesforceMcpError(message, "transient");
  }
  return new SalesforceMcpError(message, "permanent");
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
