import type { DatabasePool } from "../db/pool.js";
import type { ProfileQueryName, PublishedUnderwritingProfile } from "./types.js";
import { isProfileFresh } from "./types.js";
import { publishCustomerProfile } from "./publisher.js";
import type { UnderwritingProfileCandidate } from "./types.js";

interface ProfileVersionRow extends Omit<PublishedUnderwritingProfile, "profileId" | "version" | "publishedAt" | "capturedAt" | "startedAt" | "windowStart" | "windowEnd"> {
  profile_id: string;
  customer_id: string;
  version: number;
  run_id: string;
  source_mode: PublishedUnderwritingProfile["sourceMode"];
  schema_version: number;
  feature_version: string;
  source_versions: Record<ProfileQueryName, string>;
  window_start: Date;
  window_end: Date;
  started_at: Date;
  captured_at: Date;
  published_at: Date;
  query_completion: UnderwritingProfileCandidate["queryCompletion"];
  canonical_payload: PublishedUnderwritingProfile["data"];
}

export type ProfileFreshness = "fresh" | "missing" | "stale";
export interface CurrentProfileReadModel {
  profile: PublishedUnderwritingProfile | null;
  freshness: ProfileFreshness;
  ageSeconds: number | null;
}

/** Resolve only the profile version referenced by the customer's current pointer. */
export async function getCurrentUnderwritingProfile(
  pool: DatabasePool,
  customerId: string,
  now = new Date(),
): Promise<CurrentProfileReadModel> {
  const result = await pool.query<ProfileVersionRow>(
    `SELECT version.profile_id, version.customer_id, version.version, version.run_id,
            version.source_mode, version.schema_version, version.feature_version,
            version.source_versions, version.window_start, version.window_end,
            version.started_at, version.captured_at, version.published_at,
            version.query_completion, version.canonical_payload
       FROM underwriting_profile_current AS current
       JOIN underwriting_profile_versions AS version
         ON version.customer_id = current.customer_id
        AND version.profile_id = current.profile_id
      WHERE current.customer_id = $1`,
    [customerId],
  );
  const row = result.rows[0];
  if (!row) return { profile: null, freshness: "missing", ageSeconds: null };
  const profile = mapProfileRow(row);
  const ageMs = now.getTime() - profile.capturedAt.getTime();
  return {
    profile,
    freshness: isProfileFresh(profile.capturedAt, now) ? "fresh" : "stale",
    ageSeconds: Number.isFinite(ageMs) ? Math.max(0, Math.floor(ageMs / 1000)) : null,
  };
}

/** Read active enrollment once and return bounded batches, never provider-derived customer lists. */
export async function listActiveProfileCustomerBatches(
  pool: DatabasePool,
  batchSize = 1_000,
): Promise<string[][]> {
  if (!Number.isSafeInteger(batchSize) || batchSize < 1 || batchSize > 1_000) {
    throw new Error("Profile batch size must be an integer from 1 through 1,000");
  }
  const result = await pool.query<{ customer_id: string }>(
    `SELECT customer_id FROM customer_roster WHERE is_active ORDER BY customer_id`,
  );
  const batches: string[][] = [];
  for (let index = 0; index < result.rows.length; index += batchSize) {
    batches.push(result.rows.slice(index, index + batchSize).map((row) => row.customer_id));
  }
  return batches;
}

export async function publishUnderwritingProfile(
  pool: DatabasePool,
  candidate: UnderwritingProfileCandidate,
) {
  return publishCustomerProfile(pool, candidate);
}

function mapProfileRow(row: ProfileVersionRow): PublishedUnderwritingProfile {
  return {
    profileId: row.profile_id,
    customerId: row.customer_id,
    version: Number(row.version),
    runId: row.run_id,
    sourceMode: row.source_mode,
    schemaVersion: row.schema_version as UnderwritingProfileCandidate["schemaVersion"],
    featureVersion: row.feature_version,
    sourceVersions: row.source_versions,
    windowStart: row.window_start,
    windowEnd: row.window_end,
    startedAt: row.started_at,
    capturedAt: row.captured_at,
    publishedAt: row.published_at,
    queryCompletion: row.query_completion,
    data: row.canonical_payload,
  };
}
