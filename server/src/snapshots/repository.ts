import type { DatabasePool } from "../db/pool.js";
import type { SalesforceCustomerSnapshot } from "../salesforce/types.js";
import type { SalesforceQueryName } from "../salesforce/queries.js";
import { publishCustomerSnapshot } from "./publisher.js";

export type SnapshotSourceMode = "demo" | "production_mcp";

export interface SnapshotQueryCompletion {
  account: { complete: boolean; pages: number };
  contract: { complete: boolean; pages: number };
  case: { complete: boolean; pages: number };
  activity: { complete: boolean; pages: number };
}

export type SnapshotRecordCounts = Record<SalesforceQueryName, number>;

export interface SnapshotCandidate {
  customerId: string;
  runId: string;
  sourceMode: SnapshotSourceMode;
  capturedAt: Date;
  startedAt: Date;
  queryCompletion: SnapshotQueryCompletion;
  recordCounts: SnapshotRecordCounts;
  payload: SalesforceCustomerSnapshot;
}

export interface PublishedSnapshot extends SnapshotCandidate {
  snapshotId: string;
  version: number;
  publishedAt: Date;
}

export interface FailedSnapshotAttempt {
  customerId: string;
  runId: string;
  sourceMode: SnapshotSourceMode;
  schemaVersion: number;
  mappingVersion: string;
  startedAt: Date;
  finishedAt: Date;
  queryCompletion: Partial<SnapshotQueryCompletion>;
  recordCounts: Partial<SnapshotRecordCounts>;
  errorMessage: string;
}

interface SnapshotVersionRow {
  snapshot_id: string;
  customer_id: string;
  version: number;
  source_mode: SnapshotSourceMode;
  schema_version: number;
  mapping_version: string;
  run_id: string;
  sync_started_at: Date;
  captured_at: Date;
  published_at: Date;
  query_completion: SnapshotQueryCompletion;
  record_counts: SnapshotRecordCounts;
  canonical_payload: SalesforceCustomerSnapshot;
}

export async function publishSnapshot(
  pool: DatabasePool,
  candidate: SnapshotCandidate,
): Promise<PublishedSnapshot> {
  return publishCustomerSnapshot(pool, candidate);
}

/** Store an exhausted/failed run without changing the customer's current version. */
export async function recordFailedSnapshotAttempt(
  pool: DatabasePool,
  attempt: FailedSnapshotAttempt,
): Promise<void> {
  await pool.query(
    `INSERT INTO customer_snapshot_attempts (
       customer_id, run_id, status, source_mode, schema_version, mapping_version,
       started_at, finished_at, query_completion, record_counts, error_message
     ) VALUES ($1, $2, 'failed', $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10)
     ON CONFLICT (customer_id, run_id) DO NOTHING`,
    [attempt.customerId, attempt.runId, attempt.sourceMode, attempt.schemaVersion,
      attempt.mappingVersion, attempt.startedAt, attempt.finishedAt,
      JSON.stringify(attempt.queryCompletion), JSON.stringify(attempt.recordCounts),
      attempt.errorMessage],
  );
}

export async function getCurrentSnapshot(
  pool: DatabasePool,
  customerId: string,
): Promise<PublishedSnapshot | null> {
  const result = await pool.query<SnapshotVersionRow>(
    `SELECT version.snapshot_id, version.customer_id, version.version, version.source_mode,
            version.schema_version, version.mapping_version, version.run_id,
            version.sync_started_at, version.captured_at, version.published_at, version.query_completion,
            version.record_counts, version.canonical_payload
       FROM customer_snapshot_current AS current
       JOIN customer_snapshot_versions AS version
         ON version.customer_id = current.customer_id
        AND version.snapshot_id = current.snapshot_id
      WHERE current.customer_id = $1`,
    [customerId],
  );
  const row = result.rows[0];
  return row ? mapSnapshotRow(row) : null;
}

function mapSnapshotRow(row: SnapshotVersionRow): PublishedSnapshot {
  return {
    snapshotId: row.snapshot_id,
    customerId: row.customer_id,
    version: row.version,
    sourceMode: row.source_mode,
    runId: row.run_id,
    capturedAt: row.captured_at,
    startedAt: row.sync_started_at,
    publishedAt: row.published_at,
    queryCompletion: row.query_completion,
    recordCounts: row.record_counts,
    payload: row.canonical_payload,
  };
}
