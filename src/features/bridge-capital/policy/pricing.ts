/** Bridge pricing and terms (spec §5). */
import type { BridgeOffer, BridgeRequest } from '../types.js';
import { PRICING } from './policy.config.js';
import { addDays, round2 } from './util.js';

export interface Price {
  principal: number;
  term_days: number;
  /** Service fee plus the prorated monthly rate, for the full chosen term. */
  fee: number;
  /** principal + fee. */
  total: number;
}

/** Spec §5: terms are 15, 30, 60 or 90 days, or a custom 7 to 90. */
export function isValidTerm(termDays: number): boolean {
  if (!Number.isInteger(termDays)) return false;
  return termDays >= PRICING.minCustomTermDays && termDays <= PRICING.maxCustomTermDays;
}

/**
 * Price a bridge.
 *
 *   fee = $50 + principal x 2% per month, prorated daily over the chosen term
 *       = 50 + principal x 0.02 x term_days / 30
 *
 * Example from the spec: $10,000 for 60 days = 50 + 400 = $450.
 *
 * The fee is for the full chosen term; repaying early does not reduce it, which
 * is why nothing here takes an actual repayment date.
 */
export function priceOffer(principal: number, termDays: number): Price {
  if (principal < 0) {
    throw new RangeError(`principal must not be negative, got ${principal}`);
  }
  if (!isValidTerm(termDays)) {
    throw new RangeError(
      `term must be an integer ${PRICING.minCustomTermDays}-${PRICING.maxCustomTermDays} days, got ${termDays}`,
    );
  }
  const interest = principal * PRICING.monthlyRate * (termDays / PRICING.daysPerMonth);
  const fee = round2(PRICING.serviceFee + interest);
  return { principal: round2(principal), term_days: termDays, fee, total: round2(principal + fee) };
}

/**
 * Fee accrued on an overdue balance, at the monthly rate (spec §5: "outstanding
 * keeps accruing at 2% per month"). No second service fee.
 */
export function accruedLateFee(principalOutstanding: number, daysPastDue: number): number {
  if (daysPastDue <= 0) return 0;
  return round2(
    principalOutstanding * PRICING.monthlyRate * (daysPastDue / PRICING.daysPerMonth),
  );
}

/** Build the offer a customer accepts. Its fee and due date are bound to the terms. */
export function buildOffer(
  request: BridgeRequest,
  principal = request.principal,
  termDays = request.term_days,
): BridgeOffer {
  const price = priceOffer(principal, termDays);
  return {
    id: `offer_${request.id}`,
    customer_id: request.customer_id,
    transaction_id: request.transaction.id,
    principal: price.principal,
    term_days: price.term_days,
    fee: price.fee,
    offered_at: request.requested_at,
    due_date: addDays(request.requested_at, price.term_days),
  };
}
