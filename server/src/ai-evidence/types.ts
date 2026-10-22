/** Versioned contracts for extracting evidence from published CRM snapshots. */
export const AI_EVIDENCE_SCHEMA_VERSION = 1 as const;
export const AI_EVIDENCE_CATALOG_VERSION = "proposed-v1" as const;

export type ExtractionStatus =
  | "success"
  | "no_signal"
  | "review_required"
  | "unmapped"
  | "failed";

export type EvidenceSourceType = "case" | "call_activity";
export type ProposedSignalType =
  | "client_payment_dispute"
  | "client_revenue_share"
  | "expected_inflow_delay"
  | "positive_note";

export interface EvidenceReference {
  sourceType: EvidenceSourceType;
  sourceId: string;
}

export interface QuoteSpan extends EvidenceReference {
  quote: string;
  start: number;
  end: number;
}

export interface CounterpartyIdentity {
  /** Exact name stated by the source; normalization is a later deterministic step. */
  name: string;
}

export interface SignalAlternative {
  value: unknown;
  evidence: EvidenceReference[];
  quotes: QuoteSpan[];
  rationale: string | null;
}

export interface ExtractedSignal {
  type: ProposedSignalType;
  value: unknown;
  counterparty: CounterpartyIdentity | null;
  evidence: EvidenceReference[];
  quotes: QuoteSpan[];
  rawScore: number;
  calibratedScore: number | null;
  alternatives: SignalAlternative[];
  selectionRationale: string | null;
}

export interface ModelConfigurationMetadata {
  modelId: string;
  promptVersion: string;
  catalogVersion: string;
  outputSchemaVersion: number;
  packetBuilderVersion: string;
  calibrationVersion: string | null;
}

export interface ExtractionResult {
  schemaVersion: typeof AI_EVIDENCE_SCHEMA_VERSION;
  customerId: string;
  snapshotId: string;
  status: ExtractionStatus;
  signals: ExtractedSignal[];
  unmapped: Array<{ type: string; value: unknown; evidence: EvidenceReference[] }>;
  model: ModelConfigurationMetadata;
}

export interface UnmappedExtractionResult extends Omit<ExtractionResult, "status" | "signals"> {
  status: "unmapped";
  signals: ExtractedSignal[];
}
