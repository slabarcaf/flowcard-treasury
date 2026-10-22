import { isKnownSignalType } from "./catalog.js";
import {
  AI_EVIDENCE_SCHEMA_VERSION,
  type EvidenceReference,
  type ExtractionResult,
  type ExtractionStatus,
  type ExtractedSignal,
  type ModelConfigurationMetadata,
  type QuoteSpan,
  type SignalAlternative,
  type UnmappedExtractionResult,
} from "./types.js";

type JsonObject = Record<string, unknown>;

function object(value: unknown, path: string): JsonObject {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error(`${path} must be an object`);
  return value as JsonObject;
}

function onlyKeys(item: JsonObject, allowed: readonly string[], path: string): void {
  const unexpected = Object.keys(item).find((key) => !allowed.includes(key));
  if (unexpected) throw new Error(`${path}.${unexpected} is not allowed`);
}

function string(value: unknown, path: string): string {
  if (typeof value !== "string" || value.trim() === "") throw new Error(`${path} must be a non-empty string`);
  return value;
}

function nullableString(value: unknown, path: string): string | null {
  return value === null ? null : string(value, path);
}

function score(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 1) {
    throw new Error(`${path} must be a number between 0 and 1`);
  }
  return value;
}

function reference(value: unknown, path: string): EvidenceReference {
  const item = object(value, path);
  onlyKeys(item, ["sourceType", "sourceId"], path);
  if (item.sourceType !== "case" && item.sourceType !== "call_activity") throw new Error(`${path}.sourceType is invalid`);
  return { sourceType: item.sourceType, sourceId: string(item.sourceId, `${path}.sourceId`) };
}

function references(value: unknown, path: string): EvidenceReference[] {
  if (!Array.isArray(value)) throw new Error(`${path} must be an array`);
  return value.map((item, index) => reference(item, `${path}[${index}]`));
}

function quote(value: unknown, path: string): QuoteSpan {
  const item = object(value, path);
  onlyKeys(item, ["sourceType", "sourceId", "quote", "start", "end"], path);
  if (item.sourceType !== "case" && item.sourceType !== "call_activity") throw new Error(`${path}.sourceType is invalid`);
  const ref: EvidenceReference = { sourceType: item.sourceType, sourceId: string(item.sourceId, `${path}.sourceId`) };
  const quoted = string(item.quote, `${path}.quote`);
  if (!Number.isInteger(item.start) || !Number.isInteger(item.end) || (item.start as number) < 0 || (item.end as number) <= (item.start as number)) {
    throw new Error(`${path} must have a valid quote span`);
  }
  return { ...ref, quote: quoted, start: item.start as number, end: item.end as number };
}

function quotes(value: unknown, path: string): QuoteSpan[] {
  if (!Array.isArray(value)) throw new Error(`${path} must be an array`);
  return value.map((item, index) => quote(item, `${path}[${index}]`));
}

function alternative(value: unknown, path: string): SignalAlternative {
  const item = object(value, path);
  onlyKeys(item, ["value", "evidence", "quotes", "rationale"], path);
  return {
    value: item.value,
    evidence: references(item.evidence, `${path}.evidence`),
    quotes: quotes(item.quotes, `${path}.quotes`),
    rationale: nullableString(item.rationale, `${path}.rationale`),
  };
}

function signal(value: unknown, path: string): ExtractedSignal {
  const item = object(value, path);
  onlyKeys(item, ["type", "value", "counterparty", "evidence", "quotes", "rawScore", "calibratedScore", "alternatives", "selectionRationale"], path);
  if (typeof item.type !== "string" || !isKnownSignalType(item.type)) throw new Error(`${path}.type is unknown`);
  if (!Array.isArray(item.alternatives)) throw new Error(`${path}.alternatives must be an array`);
  const counterparty = item.counterparty === null ? null : object(item.counterparty, `${path}.counterparty`);
  if (counterparty) onlyKeys(counterparty, ["name"], `${path}.counterparty`);
  return {
    type: item.type,
    value: item.value,
    counterparty: counterparty === null ? null : { name: string(counterparty.name, `${path}.counterparty.name`) },
    evidence: references(item.evidence, `${path}.evidence`),
    quotes: quotes(item.quotes, `${path}.quotes`),
    rawScore: score(item.rawScore, `${path}.rawScore`),
    calibratedScore: item.calibratedScore === null ? null : score(item.calibratedScore, `${path}.calibratedScore`),
    alternatives: item.alternatives.map((entry, index) => alternative(entry, `${path}.alternatives[${index}]`)),
    selectionRationale: nullableString(item.selectionRationale, `${path}.selectionRationale`),
  };
}

function metadata(value: unknown): ModelConfigurationMetadata {
  const item = object(value, "model");
  onlyKeys(item, ["modelId", "promptVersion", "catalogVersion", "outputSchemaVersion", "packetBuilderVersion", "calibrationVersion"], "model");
  if (!Number.isInteger(item.outputSchemaVersion) || (item.outputSchemaVersion as number) < 1) throw new Error("model.outputSchemaVersion must be a positive integer");
  return {
    modelId: string(item.modelId, "model.modelId"),
    promptVersion: string(item.promptVersion, "model.promptVersion"),
    catalogVersion: string(item.catalogVersion, "model.catalogVersion"),
    outputSchemaVersion: item.outputSchemaVersion as number,
    packetBuilderVersion: string(item.packetBuilderVersion, "model.packetBuilderVersion"),
    calibrationVersion: nullableString(item.calibrationVersion, "model.calibrationVersion"),
  };
}

const STATUSES: readonly ExtractionStatus[] = ["success", "no_signal", "review_required", "unmapped", "failed"];

/** Parse an untrusted provider result; any unknown signal is retained for review. */
export function parseExtractionResult(input: unknown): ExtractionResult | UnmappedExtractionResult {
  const item = object(input, "result");
  onlyKeys(item, ["schemaVersion", "customerId", "snapshotId", "status", "signals", "unmapped", "model"], "result");
  if (item.schemaVersion !== AI_EVIDENCE_SCHEMA_VERSION) throw new Error("result.schemaVersion is unsupported");
  if (typeof item.status !== "string" || !STATUSES.includes(item.status as ExtractionStatus)) throw new Error("result.status is invalid");
  if (!Array.isArray(item.signals)) throw new Error("result.signals must be an array");
  if (!Array.isArray(item.unmapped)) throw new Error("result.unmapped must be an array");
  const unmapped = item.unmapped.map((entry, index) => {
    const value = object(entry, `result.unmapped[${index}]`);
    onlyKeys(value, ["type", "value", "evidence"], `result.unmapped[${index}]`);
    if (typeof value.evidence !== "undefined") references(value.evidence, `result.unmapped[${index}].evidence`);
    return {
      type: string(value.type, `result.unmapped[${index}].type`),
      value: value.value,
      evidence: value.evidence === undefined ? [] : references(value.evidence, `result.unmapped[${index}].evidence`),
    };
  });
  const unknown = item.signals.filter((entry) => {
    const candidate = object(entry, "result.signals[]");
    return typeof candidate.type !== "string" || !isKnownSignalType(candidate.type);
  });
  const parsedSignals = item.signals.filter((entry) => !unknown.includes(entry)).map((entry, index) => signal(entry, `result.signals[${index}]`));
  if (unknown.length) {
    for (const [index, entry] of unknown.entries()) {
      const candidate = object(entry, `result.signals[unknown ${index}]`);
      unmapped.push({
        type: typeof candidate.type === "string" ? candidate.type : "<missing-type>",
        value: candidate.value,
        evidence: candidate.evidence === undefined ? [] : references(candidate.evidence, `result.signals[unknown ${index}].evidence`),
      });
    }
  }
  const result = {
    schemaVersion: AI_EVIDENCE_SCHEMA_VERSION,
    customerId: string(item.customerId, "result.customerId"),
    snapshotId: string(item.snapshotId, "result.snapshotId"),
    status: unknown.length || unmapped.length > 0 ? "unmapped" as const : item.status as ExtractionStatus,
    signals: parsedSignals,
    unmapped,
    model: metadata(item.model),
  };
  if (result.status === "unmapped") return result;
  if (result.status === "no_signal" && (result.signals.length > 0 || result.unmapped.length > 0)) throw new Error("no_signal result cannot contain signals or unmapped evidence");
  if (result.status === "success" && result.signals.length === 0) throw new Error("success result requires at least one signal");
  return result;
}
