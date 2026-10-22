import { normalizeCounterpartyName } from "./normalize-counterparty.js";
import { parseExtractionResult } from "./schema.js";
import type { EvidencePacket } from "./packet.js";
import type { EvidenceReference, ExtractionResult, QuoteSpan, UnmappedExtractionResult } from "./types.js";

export type ValidatedExtraction = ExtractionResult | UnmappedExtractionResult;

/** Parse an untrusted model result and bind every claim and quote to this packet. */
export function validateExtractionResult(input: unknown, packet: EvidencePacket): ValidatedExtraction {
  const result = parseExtractionResult(input);
  if (result.customerId !== packet.customerId) throw new Error("result.customerId does not match the linked snapshot Account");
  if (result.snapshotId !== packet.snapshotId) throw new Error("result.snapshotId does not match the evidence packet");

  const sourceText = new Map<string, string | null>();
  for (const entry of packet.entries) {
    const narrativeField = entry.sourceType === "case" ? "description" : "note";
    const rawNarrative = entry.fields[narrativeField];
    const narrative = typeof rawNarrative === "string" ? rawNarrative : null;
    for (const sourceId of entry.sourceIds) sourceText.set(referenceKey({ sourceType: entry.sourceType, sourceId }), narrative);
  }

  for (const signal of result.signals) {
    assertNoPolicyAuthority(signal.value, "signal.value");
    if (!signal.counterparty || normalizeCounterpartyName(signal.counterparty.name) === "") {
      throw new Error("signal requires an explicit counterparty name");
    }
    validateReferences(signal.evidence, sourceText, "signal.evidence");
    validateQuotes(signal.quotes, signal.evidence, sourceText, "signal.quotes");
    if (signal.evidence.length === 0 || signal.quotes.length === 0) {
      throw new Error("text-derived signal requires evidence references and exact quotes");
    }
    const normalizedCounterparty = normalizeCounterpartyName(signal.counterparty.name);
    if (signal.quotes.some((quote) => !containsNameTokens(quote.quote, normalizedCounterparty))) {
      throw new Error("counterparty must be explicitly named in every joined source quote");
    }
    for (const alternative of signal.alternatives) {
      assertNoPolicyAuthority(alternative.value, "signal.alternatives.value");
      validateReferences(alternative.evidence, sourceText, "signal.alternatives.evidence");
      validateQuotes(alternative.quotes, alternative.evidence, sourceText, "signal.alternatives.quotes");
    }
  }
  validateReferences(result.unmapped.flatMap((item) => item.evidence), sourceText, "result.unmapped.evidence");
  return result;
}

function containsNameTokens(text: string, normalizedName: string): boolean {
  const tokens = normalizedName.split(" ").filter(Boolean);
  const textTokens = normalizeCounterpartyName(text).split(" ").filter(Boolean);
  if (tokens.length === 0 || tokens.length > textTokens.length) return false;
  return textTokens.some((_, start) => tokens.every((token, offset) => textTokens[start + offset] === token));
}

function assertNoPolicyAuthority(value: unknown, path: string): void {
  if (Array.isArray(value)) {
    value.forEach((entry, index) => assertNoPolicyAuthority(entry, `${path}[${index}]`));
  } else if (value && typeof value === "object") {
    for (const [key, entry] of Object.entries(value)) {
      if (/(?:rating|tier|credit.?line|eligib|bridge.?amount|transfer|funding|approval)/i.test(key)) {
        throw new Error(`${path}.${key} is outside model authority`);
      }
      assertNoPolicyAuthority(entry, `${path}.${key}`);
    }
  }
}

function validateReferences(references: readonly EvidenceReference[], sourceText: Map<string, string | null>, path: string): void {
  for (const reference of references) {
    if (!sourceText.has(referenceKey(reference))) throw new Error(`${path} contains a source outside this customer packet`);
  }
}

function validateQuotes(quotes: readonly QuoteSpan[], evidence: readonly EvidenceReference[], sourceText: Map<string, string | null>, path: string): void {
  const evidenceKeys = new Set(evidence.map(referenceKey));
  for (const quote of quotes) {
    const key = referenceKey(quote);
    if (!evidenceKeys.has(key)) throw new Error(`${path} quote is not linked to the signal evidence`);
    const text = sourceText.get(key);
    if (text === null || text === undefined || quote.end > text.length || text.slice(quote.start, quote.end) !== quote.quote) {
      throw new Error(`${path} quote does not exactly match the referenced source text`);
    }
  }
  for (const key of evidenceKeys) {
    if (!quotes.some((quote) => referenceKey(quote) === key)) throw new Error(`${path} must quote every referenced source`);
  }
}

function referenceKey(reference: EvidenceReference): string {
  return `${reference.sourceType}\u0000${reference.sourceId}`;
}
