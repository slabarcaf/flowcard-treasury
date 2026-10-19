import type {
  AirwallexClient,
  TransferResponse,
  ChargeResponse,
  BalanceResponse,
  CardSimulationResponse,
  DepositResponse,
} from '../types.js';

let transferCounter = 0;
let chargeCounter = 0;
let depositCounter = 0;

/**
 * Mock Airwallex client that returns realistic sandbox-shaped responses.
 * All responses settle immediately (status NEW → SETTLED).
 */
export const mockAirwallexClient: AirwallexClient = {
  async simulateCardSpend(params) {
    const txnId = `txn_${Date.now()}_${++transferCounter}`;
    return {
      transaction_id: txnId,
      status: 'CLEARING',
      amount: params.amount,
      currency: params.currency,
      merchant_category_code: params.merchantCategory,
    };
  },

  async getBalance(accountId, currency) {
    // Returns a realistic balance structure; actual values
    // are managed by the demo state machine, not this mock.
    return {
      available_amount: 0,
      pending_amount: 0,
      reserved_amount: 0,
      total_amount: 0,
      currency,
    };
  },

  async createTransfer(params) {
    const id = `xfer_${Date.now()}_${++transferCounter}`;
    return {
      id,
      request_id: params.requestId,
      amount: params.amount,
      currency: params.currency,
      destination: params.destination,
      status: 'SETTLED' as const,
      created_at: new Date().toISOString(),
      reason: params.reason,
      reference: params.reference,
    };
  },

  async getTransfer(requestId) {
    // In mock mode, transfers are created synchronously, so we never
    // need to look up an existing one.
    return null;
  },

  async createCharge(params) {
    const id = `chrg_${Date.now()}_${++chargeCounter}`;
    return {
      id,
      request_id: params.requestId,
      amount: params.amount,
      currency: params.currency,
      source: params.source,
      status: 'SETTLED' as const,
      created_at: new Date().toISOString(),
      reason: params.reason,
      reference: params.reference,
    };
  },

  async simulateDeposit(params) {
    const id = `dep_${Date.now()}_${++depositCounter}`;
    const now = new Date().toISOString();
    return {
      id,
      amount: params.amount,
      currency: params.currency,
      status: 'SETTLED' as const,
      payer_name: params.payerName,
      created_at: now,
      settled_at: now,
    };
  },
};
