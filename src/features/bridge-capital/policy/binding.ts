/**
 * Acceptances and approvals are bound to the terms they were given for.
 *
 * Spec §5: "If any of those change before disbursement, the acceptance is void
 * and a new offer is issued."
 * Spec §7: "Human approvals are bound to customer, amount, fee and term. Any
 * change invalidates the approval."
 */
import type { Acceptance, Approval, BridgeOffer } from '../types.js';

/** Which bound fields differ between a record and the offer it was given for. */
export function bindingMismatches(
  bound: { offer_id: string; principal: number; fee: number; term_days: number; customer_id?: string },
  offer: BridgeOffer,
): string[] {
  const out: string[] = [];
  if (bound.offer_id !== offer.id) {
    out.push(`offer ${bound.offer_id} is not offer ${offer.id}`);
  }
  if (bound.principal !== offer.principal) {
    out.push(`amount changed from ${bound.principal} to ${offer.principal}`);
  }
  if (bound.fee !== offer.fee) {
    out.push(`fee changed from ${bound.fee} to ${offer.fee}`);
  }
  if (bound.term_days !== offer.term_days) {
    out.push(`term changed from ${bound.term_days} to ${offer.term_days} days`);
  }
  if (bound.customer_id !== undefined && bound.customer_id !== offer.customer_id) {
    out.push(`customer changed from ${bound.customer_id} to ${offer.customer_id}`);
  }
  return out;
}

/** False once the amount, fee or term has moved — a new offer is then required. */
export function isAcceptanceValid(acceptance: Acceptance, offer: BridgeOffer): boolean {
  return bindingMismatches(acceptance, offer).length === 0;
}

/** False once the customer, amount, fee or term has moved. */
export function isApprovalValid(approval: Approval, offer: BridgeOffer): boolean {
  return bindingMismatches(approval, offer).length === 0;
}
