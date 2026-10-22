// ---------------------------------------------------------------------------
// Airwallex-shaped response types
// ---------------------------------------------------------------------------

export type TransferStatus = 'NEW' | 'PENDING' | 'SETTLED' | 'SUSPENDED' | 'FAILED';
export type ChargeStatus = 'NEW' | 'PENDING' | 'SETTLED' | 'SUSPENDED' | 'FAILED';
export type CardTransactionStatus = 'CLEARING' | 'SETTLED' | 'DECLINED';

export interface TransferResponse {
  id: string;
  request_id: string;
  amount: number;
  currency: string;
  destination: string;
  status: TransferStatus;
  created_at: string;
  reason: string;
  reference: string;
}

export interface ChargeResponse {
  id: string;
  request_id: string;
  amount: number;
  currency: string;
  source: string;
  status: ChargeStatus;
  created_at: string;
  reason: string;
  reference: string;
}

export interface BalanceResponse {
  available_amount: number;
  pending_amount: number;
  reserved_amount: number;
  total_amount: number;
  currency: string;
}

export interface CardSimulationResponse {
  transaction_id: string;
  status: CardTransactionStatus;
  amount: number;
  currency: string;
  failure_reason?: string;
  merchant_category_code?: string;
}

export interface DepositResponse {
  id: string;
  amount: number;
  currency: string;
  status: 'PENDING' | 'SETTLED' | 'REJECTED' | 'REVERSED';
  payer_name: string;
  created_at: string;
  settled_at?: string;
}

// ---------------------------------------------------------------------------
// CRM types
// ---------------------------------------------------------------------------

export interface CrmNote {
  author: string;
  timestamp: string;
  text: string;
  sourceRecordId?: string;
}

// ---------------------------------------------------------------------------
// Salesforce snapshot read API
// ---------------------------------------------------------------------------

import type { SalesforceCustomerSnapshot } from '../../server/src/salesforce/types.js';
export type { DemoPolicyReadiness } from '../../shared/policy/types.js';

export type SalesforceSnapshotSourceMode = 'demo' | 'production_mcp';
export type SalesforceSnapshotFreshness = 'fresh' | 'missing' | 'stale' | 'invalid' | 'deactivated';

/** Browser-safe shape returned by the server's read-only snapshot endpoint. */
export interface SalesforceSnapshotReadModel {
  customerId: string;
  active: boolean;
  freshness: SalesforceSnapshotFreshness;
  ageSeconds: number | null;
  snapshot: null | {
    snapshotId: string;
    version: number;
    capturedAt: string;
    publishedAt: string;
    sourceMode: SalesforceSnapshotSourceMode;
    schemaVersion: number;
    mappingVersion: string;
    data: SalesforceCustomerSnapshot | null;
  };
  latestAttempt: null | { status: 'succeeded' | 'failed'; finishedAt: string };
  validationErrors: string[];
}

export interface SalesforceSnapshotClient {
  getSnapshot(customerId: string): Promise<SalesforceSnapshotReadModel>;
}

// ---------------------------------------------------------------------------
// Agent types
// ---------------------------------------------------------------------------

export interface AgentSignalExtraction {
  signal: string;
  value: string;
  evidence: string;
  sourceReference: string;
  counterparty: string | null;
  confidenceStatus: 'qualified' | 'review';
  alternatives: string[];
}

export type DemoEvidenceScenario = 'no_signal' | 'unmapped' | 'conflicts' | 'multi_signal' | 'correction' | 'failure';

export interface AgentEvidenceExtraction {
  scenario: DemoEvidenceScenario;
  status: 'no_signal' | 'unmapped' | 'review_required' | 'success' | 'failed';
  signals: AgentSignalExtraction[];
  unmapped: Array<{ type: string; value: string; evidence: string; sourceReference: string }>;
  message: string;
}

export interface AgentAllocationProposal {
  customerId: string;
  customerName: string;
  amount: number;
  rationale: string;
}

// ---------------------------------------------------------------------------
// Service interfaces
// ---------------------------------------------------------------------------

export type BridgeLedgerStatus = 'reserved' | 'pending' | 'suspended' | 'funded' | 'collecting' | 'repaid' | 'failed';

export interface BridgeLedgerProjection {
  bridgeId: string;
  customerId: string;
  status: BridgeLedgerStatus;
  principalOutstandingCents: number;
  feeOutstandingCents: number;
}

export interface FacilityLedgerProjection {
  currency: 'USD';
  facilityAmountCents: number;
  reserveFloorCents: number;
  openingExposureCents: number;
  settledPrincipalCents: number;
  activeReservationsCents: number;
  deployableCents: number;
}

export interface LedgerProjectionResponse {
  facility: FacilityLedgerProjection;
  bridges: BridgeLedgerProjection[];
}

export interface AirwallexClient {
  simulateCardSpend(params: {
    accountId: string;
    cardId: string;
    amount: number;
    currency: string;
    merchantCategory?: string;
  }): Promise<CardSimulationResponse>;

  getBalance(accountId: string, currency: string): Promise<BalanceResponse>;

  createTransfer(params: {
    destination: string;
    amount: number;
    currency: string;
    reason: string;
    reference: string;
    requestId: string;
  }): Promise<TransferResponse>;

  getTransfer(requestId: string): Promise<TransferResponse | null>;

  createCharge(params: {
    source: string;
    amount: number;
    currency: string;
    reason: string;
    reference: string;
    requestId: string;
  }): Promise<ChargeResponse>;

  simulateDeposit(params: {
    accountId: string;
    amount: number;
    currency: string;
    payerName: string;
  }): Promise<DepositResponse>;
}

export interface CrmClient {
  getLatestNotes(accountId: string): Promise<CrmNote[]>;
}

export interface AgentClient {
  extractSignals(note: CrmNote): Promise<AgentEvidenceExtraction>;

  proposeAllocation(params: {
    declines: Array<{ customerId: string; customerName: string; shortfall: number }>;
    deployableCapital: number;
  }): Promise<AgentAllocationProposal[]>;

  generateRationale(customerId: string, context: string): Promise<string>;
}
