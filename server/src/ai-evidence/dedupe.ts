import { canonicalEvidence, evidenceFingerprint, type CanonicalEvidence } from "./canonicalize.js";
import type { EvidenceSourceType } from "./types.js";

export interface EvidenceCandidate extends CanonicalEvidence {
  sourceId: string;
  sortAt: string | null;
}

export interface DeduplicatedEvidence {
  customerId: string;
  sourceType: EvidenceSourceType;
  sourceIds: string[];
  fields: Readonly<Record<string, unknown>>;
  canonical: string;
}

/** Hashes narrow duplicate candidates; exact canonical values decide whether to collapse. */
export function deduplicateEvidence(candidates: readonly EvidenceCandidate[]): DeduplicatedEvidence[] {
  const buckets = new Map<string, DeduplicatedEvidence[]>();
  for (const candidate of candidates) {
    if (!candidate.customerId.trim() || !candidate.sourceId.trim()) throw new Error("Evidence customer and source IDs are required");
    const canonical = canonicalEvidence(candidate);
    const fingerprint = evidenceFingerprint(canonical);
    const bucket = buckets.get(fingerprint) ?? [];
    const duplicate = bucket.find((entry) => entry.customerId === candidate.customerId &&
      entry.sourceType === candidate.sourceType && entry.canonical === canonical);
    if (duplicate) duplicate.sourceIds.push(candidate.sourceId);
    else bucket.push({
      customerId: candidate.customerId,
      sourceType: candidate.sourceType,
      sourceIds: [candidate.sourceId],
      fields: candidate.fields,
      canonical,
    });
    buckets.set(fingerprint, bucket);
  }
  return [...buckets.values()].flat().map((entry) => ({
    ...entry,
    sourceIds: [...new Set(entry.sourceIds)].sort(),
  }));
}
