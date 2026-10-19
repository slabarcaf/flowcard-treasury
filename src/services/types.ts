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
}

// ---------------------------------------------------------------------------
// Agent types
// ---------------------------------------------------------------------------

export interface AgentSignalExtraction {
  signal: string;
  value: string;
  evidence: string;
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
  extractSignals(note: CrmNote): Promise<AgentSignalExtraction[]>;

  proposeAllocation(params: {
    declines: Array<{ customerId: string; customerName: string; shortfall: number }>;
    deployableCapital: number;
  }): Promise<AgentAllocationProposal[]>;

  generateRationale(customerId: string, context: string): Promise<string>;
}
