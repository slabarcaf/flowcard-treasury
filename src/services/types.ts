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

export interface Kit5ProviderTransaction {
  processResult: 'APPROVED' | 'DECLINED';
  failureReason: string | null;
  cardTransactionEventId: string;
  cardTransactionId: string;
  cardTransactionLifecycleId: string;
}

export interface Kit5PolicyDecisionSummary {
  outcome: 'human_review' | 'auto_approved' | 'declined' | 'blocked';
  amountCents: number;
  creditLineCents: number;
  tier: string;
  capitalBand: string | null;
  door: string | null;
  reasonCodes: string[];
  rationale: string;
}

export interface Kit5RepaymentCollectionCharge {
  status: string;
  amountCents: number;
  providerOperationId: string;
  providerObjectId: string | null;
}

export interface Kit5RepaymentCollectionSummary {
  fee: Kit5RepaymentCollectionCharge | null;
  principal: Kit5RepaymentCollectionCharge | null;
}

export interface Kit5RepaymentDepositResponse {
  status: 'creating' | 'ambiguous' | 'created' | 'settled' | 'blocked';
  depositId: string | null;
  providerStatus: string | null;
  bridgeId: string;
  statementRef: string;
  error: string | null;
  collection: Kit5RepaymentCollectionSummary | null;
}

export type Kit5NextCustomerResponse =
  | { status: 'completed'; attemptId: string; lifecycleId?: string | null; attemptedAmount: { amountCents: number; currency: string } | null; result: Kit5ProviderTransaction; policyStatus?: string; policyError?: string | null; policyDecisionId?: string | null; policyDecision?: Kit5PolicyDecisionSummary | null; bridgeId?: string | null; retryLifecycleId?: string | null; retryResult?: Kit5ProviderTransaction & { requestId?: string } | null; retryError?: string | null; humanApproved?: boolean }
  | { status: 'in_progress' | 'ambiguous' | 'failed'; attemptId: string; lifecycleId: string | null; attemptedAmount: { amountCents: number; currency: string } | null; result?: Kit5ProviderTransaction; policyStatus?: string; policyError?: string | null; policyDecisionId?: string | null; policyDecision?: Kit5PolicyDecisionSummary | null; bridgeId?: string | null; retryLifecycleId?: string | null; retryResult?: Kit5ProviderTransaction & { requestId?: string } | null; retryError?: string | null; humanApproved?: boolean }
  | { status: 'rejected'; reason: string };

export type Kit5AttemptStatusResponse = Exclude<Kit5NextCustomerResponse, { status: 'rejected' }>;

export interface Kit5NextCustomerRequest {
  scenarioId: string;
  customerId: string;
  actionId: string;
}

/** Browser input for the provider-only Bloom fixture checkpoint. */
export interface Kit5FixtureCheckpointRequest extends Kit5NextCustomerRequest {
  /** Set only after presenter confirmation to begin another resolved checkpoint run. */
  startNew?: boolean;
}

export type Kit5FixtureCheckpointRecoveryResponse =
  | { status: 'not_found' }
  | (Exclude<Kit5FixtureCheckpointResponse, { status: 'rejected' }> & {
    scenarioId: string;
    customerId: string;
    actionId: string;
  });

/** Operator-supplied lifecycle identity for resolving an ambiguous checkpoint without another create. */
export interface Kit5FixtureCheckpointLifecycleRequest extends Kit5FixtureCheckpointRequest {
  lifecycleId: string;
}

export type Kit5FixtureCheckpointResponse =
  | {
    status: 'completed';
    scenarioId?: string;
    customerId?: string;
    actionId?: string;
    attemptId: string;
    lifecycleId: string | null;
    attemptedAmount: { amountCents: number; currency: string } | null;
    result: Kit5ProviderTransaction;
  }
  | {
    status: 'in_progress' | 'ambiguous' | 'failed';
    scenarioId?: string;
    customerId?: string;
    actionId?: string;
    attemptId: string;
    lifecycleId: string | null;
    attemptedAmount: { amountCents: number; currency: string } | null;
    providerError?: string;
    result?: Kit5ProviderTransaction;
  }
  | { status: 'rejected'; reason: string };

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
