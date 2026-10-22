import { randomUUID } from "node:crypto";
import type { DatabaseClient, DatabasePool } from "../db/pool.js";
import type { SnapshotCandidate, PublishedSnapshot } from "./repository.js";

/** Publish one complete customer candidate and advance its pointer atomically. */
export async function publishCustomerSnapshot(
  pool: DatabasePool,
  candidate: SnapshotCandidate,
): Promise<PublishedSnapshot> {
  assertCompleteCandidate(candidate);
  const client = await pool.connect();
  const snapshotId = randomUUID();
  let transactionOpen = false;
  try {
    await client.query("BEGIN");
    transactionOpen = true;

    const roster = await client.query<{ customer_id: string }>(
      `SELECT customer_id FROM customer_roster WHERE customer_id = $1 FOR UPDATE`,
      [candidate.customerId],
    );
    if (!roster.rows[0]) throw new Error(`Customer ${candidate.customerId} is not enrolled`);

    const next = await client.query<{ next_version: number }>(
      `SELECT COALESCE(MAX(version), 0) + 1 AS next_version
         FROM customer_snapshot_versions WHERE customer_id = $1`,
      [candidate.customerId],
    );
    const version = Number(next.rows[0]?.next_version);
    if (!Number.isSafeInteger(version) || version < 1) throw new Error("Could not allocate snapshot version");

    await client.query(
      `INSERT INTO customer_snapshot_versions (
         snapshot_id, customer_id, version, source_mode, schema_version,
         mapping_version, run_id, sync_started_at, captured_at, query_completion,
         record_counts, canonical_payload
       ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12::jsonb)`,
      [snapshotId, candidate.customerId, version, candidate.sourceMode,
        candidate.payload.schemaVersion, candidate.payload.mappingVersion,
        candidate.runId, candidate.startedAt, candidate.capturedAt, JSON.stringify(candidate.queryCompletion),
        JSON.stringify(candidate.recordCounts), JSON.stringify(candidate.payload)],
    );

    await client.query(
      `INSERT INTO customer_snapshot_current (customer_id, snapshot_id, updated_at)
       VALUES ($1, $2, now())
       ON CONFLICT (customer_id) DO UPDATE
         SET snapshot_id = EXCLUDED.snapshot_id, updated_at = EXCLUDED.updated_at`,
      [candidate.customerId, snapshotId],
    );

    await client.query(
      `INSERT INTO customer_snapshot_attempts (
         customer_id, run_id, status, source_mode, schema_version, mapping_version,
         started_at, finished_at, query_completion, record_counts
       ) VALUES ($1, $2, 'succeeded', $3, $4, $5, $6, now(), $7::jsonb, $8::jsonb)`,
      [candidate.customerId, candidate.runId, candidate.sourceMode,
        candidate.payload.schemaVersion, candidate.payload.mappingVersion,
        candidate.startedAt, JSON.stringify(candidate.queryCompletion),
        JSON.stringify(candidate.recordCounts)],
    );

    await client.query("COMMIT");
    transactionOpen = false;
    return {
      ...candidate,
      snapshotId,
      version,
      publishedAt: new Date(),
    };
  } catch (error) {
    if (transactionOpen) await rollback(client);
    throw error;
  } finally {
    client.release();
  }
}

function assertCompleteCandidate(candidate: SnapshotCandidate): void {
  if (!candidate.customerId.trim()) throw new Error("customerId is required");
  if (!candidate.runId.trim()) throw new Error("runId is required");
  const requiredQueries = ["account", "contract", "case", "activity"] as const;
  for (const queryName of requiredQueries) {
    const result = candidate.queryCompletion[queryName];
    if (!result) throw new Error(`Snapshot candidate is missing completion metadata for ${queryName}`);
    if (!result.complete || !Number.isInteger(result.pages) || result.pages < 1) {
      throw new Error(`Snapshot candidate is incomplete for ${queryName}`);
    }
  }
  for (const queryName of requiredQueries) {
    const count = candidate.recordCounts[queryName];
    if (!Number.isSafeInteger(count) || count < 0) throw new Error(`Invalid record count for ${queryName}`);
  }
}

async function rollback(client: DatabaseClient): Promise<void> {
  try {
    await client.query("ROLLBACK");
  } catch {
    // Keep the publication error; release() returns the connection to the pool.
  }
}
