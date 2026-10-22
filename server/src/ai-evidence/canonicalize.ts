import { createHash } from "node:crypto";
import type { EvidenceSourceType } from "./types.js";

export interface CanonicalEvidence {
  customerId: string;
  sourceType: EvidenceSourceType;
  fields: Readonly<Record<string, unknown>>;
}

/** Serialize model-visible evidence fields in stable key order, excluding provenance IDs. */
export function canonicalEvidence(evidence: CanonicalEvidence): string {
  return stableStringify({
    customerId: evidence.customerId,
    sourceType: evidence.sourceType,
    fields: evidence.fields,
  });
}

export function evidenceFingerprint(canonicalValue: string): string {
  return createHash("sha256").update(canonicalValue, "utf8").digest("hex");
}

function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") {
    const encoded = JSON.stringify(value);
    if (encoded === undefined) throw new Error("Evidence contains a non-JSON value");
    return encoded;
  }
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(",")}]`;
  const record = value as Record<string, unknown>;
  return `{${Object.keys(record).sort().map((key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`).join(",")}}`;
}
