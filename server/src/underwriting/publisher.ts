import { randomUUID } from "node:crypto";
import type { DatabaseClient, DatabasePool } from "../db/pool.js";
import { allowlistProfileData, validateProfileCandidate } from "./types.js";
import type { FailedProfileAttempt, PublishedUnderwritingProfile, UnderwritingProfileCandidate } from "./types.js";

export type ProfilePublicationResult =
  | { status: "published"; profile: PublishedUnderwritingProfile }
  | { status: "failed"; customerId: string; runId: string; errors: string[] };

/** Publish one customer version and move its pointer within the same transaction. */
export async function publishCustomerProfile(
  pool: DatabasePool,
  candidate: UnderwritingProfileCandidate,
): Promise<ProfilePublicationResult> {
  const validationErrors = validateProfileCandidate(candidate);
  if (validationErrors.length) {
    await recordFailedProfileAttempt(pool, failedAttempt(candidate, validationErrors.join("; ")));
    return { status: "failed", customerId: candidate.customerId, runId: candidate.runId, errors: validationErrors };
  }

  const client = await pool.connect();
  const profileId = randomUUID();
  let transactionOpen = false;
  try {
    await client.query("BEGIN");
    transactionOpen = true;
    const roster = await client.query<{ customer_id: string; is_active: boolean }>(
      `SELECT customer_id, is_active FROM customer_roster WHERE customer_id = $1 FOR UPDATE`,
      [candidate.customerId],
    );
    if (!roster.rows[0]) throw new Error(`Customer ${candidate.customerId} is not enrolled`);
    if (!roster.rows[0].is_active) throw new Error(`Customer ${candidate.customerId} is not active`);

    const next = await client.query<{ next_version: number }>(
      `SELECT COALESCE(MAX(version), 0) + 1 AS next_version
         FROM underwriting_profile_versions WHERE customer_id = $1`,
      [candidate.customerId],
    );
    const version = Number(next.rows[0]?.next_version);
    if (!Number.isSafeInteger(version) || version < 1) throw new Error("Could not allocate underwriting profile version");

    const data = allowlistProfileData(candidate.data);
    await client.query(
      `INSERT INTO underwriting_profile_versions (
         profile_id, customer_id, version, run_id, source_mode, schema_version,
         feature_version, source_versions, window_start, window_end, started_at,
         captured_at, query_completion, canonical_payload
       ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13::jsonb, $14::jsonb)`,
      [profileId, candidate.customerId, version, candidate.runId, candidate.sourceMode,
        candidate.schemaVersion, candidate.featureVersion, JSON.stringify(candidate.sourceVersions),
        candidate.windowStart, candidate.windowEnd, candidate.startedAt, candidate.capturedAt,
        JSON.stringify(candidate.queryCompletion), JSON.stringify(data)],
    );
    await client.query(
      `INSERT INTO underwriting_profile_current (customer_id, profile_id, updated_at)
       VALUES ($1, $2, now())
       ON CONFLICT (customer_id) DO UPDATE
         SET profile_id = EXCLUDED.profile_id, updated_at = EXCLUDED.updated_at`,
      [candidate.customerId, profileId],
    );
    await client.query(
      `INSERT INTO underwriting_profile_attempts (
         customer_id, run_id, status, source_mode, started_at, finished_at,
         source_versions, query_completion
       ) VALUES ($1, $2, 'succeeded', $3, $4, now(), $5::jsonb, $6::jsonb)`,
      [candidate.customerId, candidate.runId, candidate.sourceMode, candidate.startedAt,
        JSON.stringify(candidate.sourceVersions), JSON.stringify(candidate.queryCompletion)],
    );
    await client.query("COMMIT");
    transactionOpen = false;
    return {
      status: "published",
      profile: { ...candidate, data, profileId, version, publishedAt: new Date() },
    };
  } catch (error) {
    if (transactionOpen) await rollback(client);
    throw error;
  } finally {
    client.release();
  }
}

/** Store a failed attempt separately, preserving any current profile pointer. */
export async function recordFailedProfileAttempt(pool: DatabasePool, attempt: FailedProfileAttempt): Promise<void> {
  if (!attempt.customerId.trim()) throw new Error("customerId is required for failed profile attempt");
  if (!attempt.runId.trim()) throw new Error("runId is required for failed profile attempt");
  if (!attempt.errorMessage.trim()) throw new Error("errorMessage is required for failed profile attempt");
  await pool.query(
    `INSERT INTO underwriting_profile_attempts (
       customer_id, run_id, status, source_mode, started_at, finished_at,
       source_versions, query_completion, error_message
     ) VALUES ($1, $2, 'failed', $3, $4, $5, $6::jsonb, $7::jsonb, $8)
     ON CONFLICT (customer_id, run_id) DO NOTHING`,
    [attempt.customerId, attempt.runId, attempt.sourceMode, attempt.startedAt, attempt.finishedAt,
      JSON.stringify(attempt.sourceVersions), JSON.stringify(attempt.queryCompletion), attempt.errorMessage],
  );
}

function failedAttempt(candidate: UnderwritingProfileCandidate, errorMessage: string): FailedProfileAttempt {
  return {
    customerId: candidate.customerId,
    runId: candidate.runId,
    sourceMode: candidate.sourceMode,
    startedAt: candidate.startedAt,
    finishedAt: new Date(),
    sourceVersions: candidate.sourceVersions,
    queryCompletion: candidate.queryCompletion,
    errorMessage,
  };
}

async function rollback(client: DatabaseClient): Promise<void> {
  try {
    await client.query("ROLLBACK");
  } catch {
    // Preserve the original publication error; release() returns the connection to the pool.
  }
}
