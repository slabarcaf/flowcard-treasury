import type { CustomerSnapshotReadModel } from "../snapshots/freshness.js";
import type { SalesforceCaseSnapshot, SalesforceCustomerSnapshot, SalesforceCallActivitySnapshot } from "../salesforce/types.js";
import { deduplicateEvidence, type EvidenceCandidate } from "./dedupe.js";
import type { EvidenceSourceType } from "./types.js";

export const EVIDENCE_PACKET_BUILDER_VERSION = "packet-v1";
export const EVIDENCE_LOOKBACK_DAYS = 90;
export const MAX_CALL_ACTIVITIES = 5;

export interface EvidencePacketEntry {
  sourceType: EvidenceSourceType;
  sourceIds: string[];
  fields: Readonly<Record<string, unknown>>;
}

export interface EvidencePacket {
  customerId: string;
  snapshotId: string;
  builderVersion: typeof EVIDENCE_PACKET_BUILDER_VERSION;
  entries: EvidencePacketEntry[];
  byteLength: number;
}

export type EvidencePacketResult =
  | { status: "ready"; packet: EvidencePacket }
  | { status: "failed"; customerId: string; snapshotId: string | null; reason: string };

export interface EvidencePacketOptions {
  now?: Date;
  maxPacketBytes: number;
  maxNarrativeChars: number;
}

/** Build a packet only from a fresh, active, atomically published snapshot. */
export function buildEvidencePacket(
  readModel: CustomerSnapshotReadModel,
  options: EvidencePacketOptions,
): EvidencePacketResult {
  const snapshot = readModel.snapshot;
  if (!readModel.active || readModel.freshness !== "fresh" || !snapshot?.data) {
    return failed(readModel.customerId, snapshot?.snapshotId ?? null, "A fresh active published snapshot is required");
  }
  if (!Number.isSafeInteger(options.maxPacketBytes) || options.maxPacketBytes < 1 ||
      !Number.isSafeInteger(options.maxNarrativeChars) || options.maxNarrativeChars < 1) {
    throw new Error("Evidence packet limits must be positive safe integers");
  }

  let candidates: EvidenceCandidate[];
  try {
    candidates = buildCandidates(readModel.customerId, snapshot.data, options.now ?? new Date(), options.maxNarrativeChars);
  } catch (error) {
    return failed(readModel.customerId, snapshot.snapshotId, error instanceof Error ? error.message : "Invalid evidence packet input");
  }
  const deduplicated = deduplicateEvidence(candidates);
  const entries: EvidencePacketEntry[] = deduplicated.map((item) => ({
    sourceType: item.sourceType,
    sourceIds: item.sourceIds,
    fields: item.fields,
  })).sort(compareEntries);
  const content = { customerId: readModel.customerId, snapshotId: snapshot.snapshotId,
    builderVersion: EVIDENCE_PACKET_BUILDER_VERSION as typeof EVIDENCE_PACKET_BUILDER_VERSION, entries };
  const byteLength = new TextEncoder().encode(JSON.stringify(content)).byteLength;
  if (byteLength > options.maxPacketBytes) {
    return failed(readModel.customerId, snapshot.snapshotId,
      `Evidence packet exceeds configured size limit (${byteLength} > ${options.maxPacketBytes} bytes)`);
  }
  return { status: "ready", packet: { ...content, byteLength } };
}

function buildCandidates(customerId: string, snapshot: SalesforceCustomerSnapshot, now: Date, maxText: number): EvidenceCandidate[] {
  const nowMs = now.getTime();
  if (!Number.isFinite(nowMs)) throw new Error("Evidence packet clock is invalid");
  const cases = snapshot.cases.filter((item) => item.riskCategory.value !== null)
    .map((item) => caseCandidate(customerId, item, maxText));
  const cutoff = nowMs - EVIDENCE_LOOKBACK_DAYS * 24 * 60 * 60 * 1000;
  const calls = snapshot.callActivities.flatMap((item) => {
    const at = Date.parse(item.activityAt.value ?? "");
    if (!Number.isFinite(at) || at < cutoff || at > nowMs) return [];
    return [activityCandidate(customerId, item, maxText)];
  }).sort((left, right) => compareDatesDescending(left.sortAt, right.sortAt) || left.sourceId.localeCompare(right.sourceId))
    .slice(0, MAX_CALL_ACTIVITIES);
  return [...cases, ...calls];
}

function caseCandidate(customerId: string, item: SalesforceCaseSnapshot, maxText: number): EvidenceCandidate {
  const description = bounded(item.description.value, maxText, "Case description");
  const fields = {
    createdAt: item.createdAt.value,
    status: item.status.value,
    priority: item.priority.value,
    reason: item.reason.value,
    isEscalated: item.isEscalated.value,
    riskCategory: item.riskCategory.value,
    description,
  };
  return { customerId, sourceType: "case", sourceId: item.recordId, sortAt: item.createdAt.value, fields };
}

function activityCandidate(customerId: string, item: SalesforceCallActivitySnapshot, maxText: number): EvidenceCandidate {
  const note = bounded(item.note.value, maxText, "Call Activity note");
  const fields = { activityAt: item.activityAt.value, outcome: item.outcome.value, note };
  return { customerId, sourceType: "call_activity", sourceId: item.recordId, sortAt: item.activityAt.value, fields };
}

function bounded(text: string | null, max: number, label: string): string | null {
  if (text !== null && [...text].length > max) throw new Error(`${label} exceeds configured text limit`);
  return text;
}

function compareEntries(left: EvidencePacketEntry, right: EvidencePacketEntry): number {
  const leftDate = String(left.fields.createdAt ?? left.fields.activityAt ?? "");
  const rightDate = String(right.fields.createdAt ?? right.fields.activityAt ?? "");
  return left.sourceType.localeCompare(right.sourceType) || rightDate.localeCompare(leftDate) ||
    JSON.stringify(left.fields).localeCompare(JSON.stringify(right.fields)) || left.sourceIds[0]!.localeCompare(right.sourceIds[0]!);
}

function compareDatesDescending(left: string | null, right: string | null): number {
  return (right ?? "").localeCompare(left ?? "");
}

function failed(customerId: string, snapshotId: string | null, reason: string): EvidencePacketResult {
  return { status: "failed", customerId, snapshotId, reason };
}
