import type { DatabasePool } from "../db/pool.js";
import { validateSnapshot } from "../salesforce/validate.js";
import type { SalesforceCustomerSnapshot } from "../salesforce/types.js";
import type { SnapshotSourceMode } from "./repository.js";

const MAX_FRESH_AGE_MS = 24 * 60 * 60 * 1000;

export type SnapshotFreshness = "fresh" | "missing" | "stale" | "invalid" | "deactivated";

interface CustomerSnapshotRow {
  is_active: boolean;
  snapshot_id: string | null;
  version: number | null;
  source_mode: SnapshotSourceMode | null;
  schema_version: number | null;
  mapping_version: string | null;
  run_id: string | null;
  captured_at: Date | string | null;
  published_at: Date | string | null;
  canonical_payload: SalesforceCustomerSnapshot | null;
  attempt_run_id: string | null;
  attempt_status: "succeeded" | "failed" | null;
  attempt_finished_at: Date | string | null;
}

export interface CustomerSnapshotReadModel {
  customerId: string;
  active: boolean;
  freshness: SnapshotFreshness;
  ageSeconds: number | null;
  snapshot: null | {
    snapshotId: string;
    version: number;
    capturedAt: string;
    publishedAt: string;
    sourceMode: SnapshotSourceMode;
    schemaVersion: number;
    mappingVersion: string;
    data: SalesforceCustomerSnapshot | null;
  };
  latestAttempt: null | { status: "succeeded" | "failed"; finishedAt: string };
  validationErrors: string[];
}

/** Reads the active roster, current publication, and latest attempt together. */
export async function getCustomerSnapshotReadModel(
  database: Pick<DatabasePool, "query">,
  customerId: string,
  now = new Date(),
  lockRoster = false,
): Promise<CustomerSnapshotReadModel> {
  const lock = lockRoster ? "FOR UPDATE OF roster" : "";
  const result = await database.query<CustomerSnapshotRow>(
    `SELECT roster.is_active,
            version.snapshot_id, version.version, version.source_mode,
            version.schema_version, version.mapping_version, version.run_id,
            version.captured_at, version.published_at, version.canonical_payload,
            attempt.run_id AS attempt_run_id, attempt.status AS attempt_status,
            attempt.finished_at AS attempt_finished_at
       FROM customer_roster AS roster
       LEFT JOIN customer_snapshot_current AS current
         ON current.customer_id = roster.customer_id
       LEFT JOIN customer_snapshot_versions AS version
         ON version.customer_id = current.customer_id
        AND version.snapshot_id = current.snapshot_id
       LEFT JOIN LATERAL (
         SELECT run_id, status, finished_at
           FROM customer_snapshot_attempts
          WHERE customer_id = roster.customer_id
          ORDER BY finished_at DESC, started_at DESC, run_id DESC
          LIMIT 1
       ) AS attempt ON true
      WHERE roster.customer_id = $1
      ${lock}`,
    [customerId],
  );
  const row = result.rows[0];
  if (!row) {
    return {
      customerId,
      active: false,
      freshness: "missing",
      ageSeconds: null,
      snapshot: null,
      latestAttempt: null,
      validationErrors: [],
    };
  }

  const snapshot = row.snapshot_id && row.version !== null && row.source_mode &&
      row.schema_version !== null && row.mapping_version && row.captured_at && row.published_at
    ? {
      snapshotId: row.snapshot_id,
      version: Number(row.version),
      capturedAt: toIsoString(row.captured_at),
      publishedAt: toIsoString(row.published_at),
      sourceMode: row.source_mode,
      schemaVersion: Number(row.schema_version),
      mappingVersion: row.mapping_version,
      data: row.canonical_payload,
    }
    : null;
  const latestAttempt = row.attempt_status && row.attempt_finished_at
    ? { status: row.attempt_status, finishedAt: toIsoString(row.attempt_finished_at) }
    : null;
  const ageMs = snapshot ? now.getTime() - Date.parse(snapshot.capturedAt) : null;
  const ageSeconds = ageMs === null || !Number.isFinite(ageMs) ? null : Math.max(0, Math.floor(ageMs / 1000));
  const validationErrors: string[] = [];
  let allowlistedData: SalesforceCustomerSnapshot | null = null;
  if (snapshot) {
    if (!snapshot.data) {
      validationErrors.push("Canonical snapshot payload is missing");
    } else {
      try {
        validationErrors.push(...validateSnapshot(snapshot.data));
      } catch {
        validationErrors.push("Canonical snapshot payload is malformed");
      }
      if (snapshot.sourceMode === "production_mcp" && snapshot.data.mappingVerified !== true) {
        validationErrors.push("Production Salesforce mapping is not verified");
      }
      try {
        allowlistedData = allowlistedSnapshot(snapshot.data);
      } catch {
        validationErrors.push("Canonical snapshot payload is malformed");
      }
    }
  }

  let freshness: SnapshotFreshness;
  if (!row.is_active) freshness = "deactivated";
  else if (!snapshot) freshness = "missing";
  else if (latestAttempt?.status === "failed" || ageMs === null || ageMs < 0 || ageMs > MAX_FRESH_AGE_MS) freshness = "stale";
  else if (validationErrors.length > 0) freshness = "invalid";
  else freshness = "fresh";

  return {
    customerId,
    active: row.is_active,
    freshness,
    ageSeconds,
    snapshot: snapshot ? { ...snapshot, data: allowlistedData } : null,
    latestAttempt,
    validationErrors,
  };
}

function toIsoString(value: Date | string): string {
  const date = value instanceof Date ? value : new Date(value);
  if (!Number.isFinite(date.getTime())) throw new Error("Snapshot metadata contains an invalid timestamp");
  return date.toISOString();
}

/** Rebuild canonical payloads from the allowlist so unexpected stored keys are never exposed. */
function allowlistedSnapshot(value: SalesforceCustomerSnapshot): SalesforceCustomerSnapshot {
  const sourced = <T>(field: { value: T | null; source: { recordId: string; sourceUpdatedAt: string | null } }) => ({
    value: field.value,
    source: { recordId: field.source.recordId, sourceUpdatedAt: field.source.sourceUpdatedAt },
  });
  return {
    schemaVersion: value.schemaVersion,
    mappingVersion: value.mappingVersion,
    mappingVerified: value.mappingVerified,
    account: {
      recordId: value.account.recordId,
      sourceUpdatedAt: value.account.sourceUpdatedAt,
      customerStatus: sourced(value.account.customerStatus),
      kybStatus: sourced(value.account.kybStatus),
      creditReviewStatus: sourced(value.account.creditReviewStatus),
      approvedBridgeCap: sourced(value.account.approvedBridgeCap),
      priorDefaultStatus: sourced(value.account.priorDefaultStatus),
      loyaltyTier: sourced(value.account.loyaltyTier),
    },
    contract: {
      recordId: value.contract.recordId,
      sourceUpdatedAt: value.contract.sourceUpdatedAt,
      status: sourced(value.contract.status),
      endDate: sourced(value.contract.endDate),
    },
    cases: value.cases.map((item) => ({
      recordId: item.recordId,
      sourceUpdatedAt: item.sourceUpdatedAt,
      status: sourced(item.status),
      priority: sourced(item.priority),
      reason: sourced(item.reason),
      createdAt: sourced(item.createdAt),
      closedAt: sourced(item.closedAt),
      isEscalated: sourced(item.isEscalated),
      riskCategory: sourced(item.riskCategory),
      description: sourced(item.description),
    })),
    callActivities: value.callActivities.map((item) => ({
      recordId: item.recordId,
      sourceUpdatedAt: item.sourceUpdatedAt,
      activityAt: sourced(item.activityAt),
      outcome: sourced(item.outcome),
      note: sourced(item.note),
    })),
  };
}
