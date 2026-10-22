import { randomUUID } from "node:crypto";
import { mapAccountRecord, mapCallActivityRecord, mapCaseRecord, mapContractRecord, SALESFORCE_MAPPING_VERSION } from "../salesforce/field-map.js";
import { getDemoSalesforceSnapshot } from "../salesforce/fixture-source.js";
import { readSalesforceAccount, type SalesforceMcpClient, type SalesforceMcpQueryResults } from "../salesforce/mcp-source.js";
import type { SalesforceMcpConfig } from "../salesforce/mcp-config.js";
import type { CustomerRosterEntry } from "../roster/types.js";
import { listActiveCustomersForSnapshots } from "../roster/repository.js";
import { validateSnapshot } from "../salesforce/validate.js";
import type { SalesforceCustomerSnapshot, TextLengthConfig } from "../salesforce/types.js";
import { publishSnapshot, recordFailedSnapshotAttempt, type SnapshotCandidate, type SnapshotQueryCompletion, type SnapshotRecordCounts, type SnapshotSourceMode } from "./repository.js";
import type { DatabasePool } from "../db/pool.js";

export type SnapshotSourceResult =
  | { kind: "fixture"; payload: SalesforceCustomerSnapshot }
  | { kind: "mcp"; queries: SalesforceMcpQueryResults };

export interface SnapshotSyncSource {
  readonly mode: SnapshotSourceMode;
  fetchCustomer(customer: CustomerRosterEntry): Promise<SnapshotSourceResult>;
}

export interface SnapshotSyncWorkerOptions {
  pool: DatabasePool;
  source: SnapshotSyncSource;
  textLimits: TextLengthConfig;
  now?: () => Date;
  runId?: () => string;
  publish?: typeof publishSnapshot;
  recordFailure?: typeof recordFailedSnapshotAttempt;
  /** Called only after this customer's complete snapshot was published. */
  onPublished?: (customer: CustomerRosterEntry) => Promise<void>;
}

export interface CustomerSyncResult {
  customerId: string;
  status: "published" | "failed";
  error?: string;
}

export interface SnapshotSyncRunResult {
  runId: string;
  customers: CustomerSyncResult[];
}

const INTERNAL_BATCH_SIZE = 1_000;
const ACTIVITY_LOOKBACK_DAYS = 90;
const MAX_CALL_ACTIVITIES = 5;

/** Runs the daily snapshot once, isolating failures to the affected customer. */
export class SnapshotSyncWorker {
  private readonly now: () => Date;
  private readonly createRunId: () => string;
  private readonly publish: typeof publishSnapshot;
  private readonly recordFailure: typeof recordFailedSnapshotAttempt;

  constructor(private readonly options: SnapshotSyncWorkerOptions) {
    this.now = options.now ?? (() => new Date());
    this.createRunId = options.runId ?? randomUUID;
    this.publish = options.publish ?? publishSnapshot;
    this.recordFailure = options.recordFailure ?? recordFailedSnapshotAttempt;
  }

  async run(): Promise<SnapshotSyncRunResult> {
    const runId = this.createRunId();
    const customers = await listActiveCustomersForSnapshots(this.options.pool);
    const results: CustomerSyncResult[] = [];
    for (const customer of customers) {
      // The SQL query is authoritative; the in-process check also protects fake or alternate repositories.
      if (!customer.isActive) continue;
      results.push(await this.syncCustomer(customer, runId));
    }
    return { runId, customers: results };
  }

  private async syncCustomer(customer: CustomerRosterEntry, runId: string): Promise<CustomerSyncResult> {
    const startedAt = this.now();
    const completion = emptyCompletion();
    const counts: Partial<SnapshotRecordCounts> = {};
    try {
      const source = await this.options.source.fetchCustomer(customer);
      const built = source.kind === "fixture"
        ? fixtureCandidateParts(source.payload)
        : buildMcpCandidateParts(source.queries, customer.salesforceAccountId, this.options.textLimits, this.now());
      Object.assign(completion, built.queryCompletion);
      Object.assign(counts, built.recordCounts);

      const errors = validateSnapshot(built.payload);
      if (errors.length > 0) throw new Error(`Snapshot validation failed: ${errors.join("; ")}`);

      const candidate: SnapshotCandidate = {
        customerId: customer.customerId,
        runId,
        sourceMode: this.options.source.mode,
        startedAt,
        capturedAt: this.now(),
        queryCompletion: completion,
        recordCounts: counts as SnapshotRecordCounts,
        payload: built.payload,
      };
      await this.publish(this.options.pool, candidate);
      try {
        await this.options.onPublished?.(customer);
      } catch (error) {
        // Extraction errors must not invalidate a successfully published daily snapshot.
        console.error(`Post-publication processing failed for ${customer.customerId}`, error);
      }
      return { customerId: customer.customerId, status: "published" };
    } catch (error) {
      const message = error instanceof Error ? error.message : "Salesforce snapshot sync failed";
      try {
        await this.recordFailure(this.options.pool, {
          customerId: customer.customerId,
          runId,
          sourceMode: this.options.source.mode,
          schemaVersion: 1,
          mappingVersion: SALESFORCE_MAPPING_VERSION,
          startedAt,
          finishedAt: this.now(),
          queryCompletion: completion,
          recordCounts: counts,
          errorMessage: message,
        });
      } catch (recordError) {
        const persistenceMessage = recordError instanceof Error ? recordError.message : "unknown persistence error";
        return { customerId: customer.customerId, status: "failed", error: `${message}; could not record failed attempt: ${persistenceMessage}` };
      }
      return { customerId: customer.customerId, status: "failed", error: message };
    }
  }
}

/** Map records only after the source adapter has exhausted every provider page. */
export function mapRecordsInBatches<Input, Output>(
  records: readonly Input[],
  mapper: (record: Input) => Output,
  batchSize = INTERNAL_BATCH_SIZE,
): Output[] {
  if (!Number.isSafeInteger(batchSize) || batchSize < 1 || batchSize > INTERNAL_BATCH_SIZE) {
    throw new Error(`Internal snapshot batch size must be between 1 and ${INTERNAL_BATCH_SIZE}`);
  }
  const mapped: Output[] = [];
  for (let start = 0; start < records.length; start += batchSize) {
    const batch = records.slice(start, start + batchSize);
    for (const record of batch) mapped.push(mapper(record));
  }
  return mapped;
}

function fixtureCandidateParts(payload: SalesforceCustomerSnapshot) {
  return {
    payload,
    queryCompletion: completeQueryMetadata(),
    recordCounts: {
      account: 1,
      contract: 1,
      case: payload.cases.length,
      activity: payload.callActivities.length,
    } satisfies SnapshotRecordCounts,
  };
}

function buildMcpCandidateParts(
  queries: SalesforceMcpQueryResults,
  expectedAccountId: string,
  textLimits: TextLengthConfig,
  now: Date,
) {
  assertRecordReference(queries.account.records, "Id", expectedAccountId, "Account");
  assertRecordReference(queries.contract.records, "AccountId", expectedAccountId, "Contract");
  assertRecordReference(queries.case.records, "AccountId", expectedAccountId, "Case");
  assertRecordReference(queries.activity.records, "WhatId", expectedAccountId, "Task");

  const accountRecords = mapRecordsInBatches(queries.account.records, mapAccountRecord);
  const contractRecords = mapRecordsInBatches(queries.contract.records, mapContractRecord);
  if (accountRecords.length !== 1) throw new Error(`Expected one Salesforce Account record, received ${accountRecords.length}`);
  if (contractRecords.length === 0) throw new Error("Salesforce Contract query returned no records");

  const caseRecords = mapRecordsInBatches(queries.case.records, (record) => mapCaseRecord(record, textLimits.maxCaseTextLength))
    .filter((item) => item.riskCategory.value !== null
      && (item.status.value === "new" || item.status.value === "working" || item.status.value === "escalated" || item.isEscalated.value === true));
  const eligibleActivities = queries.activity.records
    .filter((record) => isWithinLookback(record.ActivityDate, now))
    .sort((left, right) => dateTimestamp(right.ActivityDate) - dateTimestamp(left.ActivityDate))
    .slice(0, MAX_CALL_ACTIVITIES);
  const activityRecords = mapRecordsInBatches(eligibleActivities, (record) => mapCallActivityRecord(record, textLimits.maxActivityTextLength));

  const queryCompletion: SnapshotQueryCompletion = {
    account: { complete: queries.account.complete, pages: queries.account.pages },
    contract: { complete: queries.contract.complete, pages: queries.contract.pages },
    case: { complete: queries.case.complete, pages: queries.case.pages },
    activity: { complete: queries.activity.complete, pages: queries.activity.pages },
  };
  const recordCounts: SnapshotRecordCounts = {
    account: queries.account.records.length,
    contract: queries.contract.records.length,
    case: caseRecords.length,
    activity: queries.activity.records.length,
  };

  // One canonical contract is required; when Salesforce returns history, prefer the
  // active contract and otherwise use the most recently modified record.
  const contract = selectContract(contractRecords);
  return {
    payload: {
      schemaVersion: 1,
      mappingVersion: SALESFORCE_MAPPING_VERSION,
      mappingVerified: false,
      account: accountRecords[0]!,
      contract,
      cases: caseRecords,
      callActivities: activityRecords,
    } satisfies SalesforceCustomerSnapshot,
    queryCompletion,
    recordCounts,
  };
}

function assertRecordReference(
  records: readonly Record<string, unknown>[],
  field: "Id" | "AccountId" | "WhatId",
  expectedAccountId: string,
  objectName: string,
): void {
  for (const record of records) {
    if (record[field] !== expectedAccountId) {
      throw new Error(`Salesforce ${objectName} record ${String(record.Id ?? "<unknown>")} has unexpected ${field}; expected roster Account ${expectedAccountId}`);
    }
  }
}

function selectContract(contracts: ReturnType<typeof mapContractRecord>[]): ReturnType<typeof mapContractRecord> {
  return [...contracts].sort((left, right) => {
    const activeDelta = Number(right.status.value === "active") - Number(left.status.value === "active");
    if (activeDelta !== 0) return activeDelta;
    return dateTimestamp(right.sourceUpdatedAt) - dateTimestamp(left.sourceUpdatedAt);
  })[0]!;
}

function isWithinLookback(value: unknown, now: Date): boolean {
  if (typeof value !== "string") return false;
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) return false;
  const age = now.getTime() - timestamp;
  return age >= 0 && age <= ACTIVITY_LOOKBACK_DAYS * 24 * 60 * 60 * 1000;
}

function dateTimestamp(value: unknown): number {
  if (typeof value !== "string") return 0;
  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

function emptyCompletion(): SnapshotQueryCompletion {
  return {
    account: { complete: false, pages: 0 },
    contract: { complete: false, pages: 0 },
    case: { complete: false, pages: 0 },
    activity: { complete: false, pages: 0 },
  };
}

function completeQueryMetadata(): SnapshotQueryCompletion {
  return {
    account: { complete: true, pages: 1 },
    contract: { complete: true, pages: 1 },
    case: { complete: true, pages: 1 },
    activity: { complete: true, pages: 1 },
  };
}

/** Fixture source factory: deterministic synthetic data, with no provider client. */
export function createDemoSnapshotSource(): SnapshotSyncSource {
  return {
    mode: "demo",
    async fetchCustomer(customer) {
      const payload = getDemoSalesforceSnapshot(customer.customerId);
      if (!payload) throw new Error(`No synthetic Salesforce snapshot fixture for ${customer.customerId}`);
      return { kind: "fixture", payload };
    },
  };
}

/** Production source is explicitly backed by the configured MCP client; there is no REST or fixture fallback. */
export function createMcpSnapshotSource(
  client: SalesforceMcpClient,
  config: SalesforceMcpConfig,
  sleep?: (ms: number) => Promise<void>,
): SnapshotSyncSource {
  return {
    mode: "production_mcp",
    async fetchCustomer(customer) {
      return { kind: "mcp", queries: await readSalesforceAccount(client, config, customer, sleep) };
    },
  };
}
